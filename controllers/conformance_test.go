package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/investigate"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// O6 conformance harness. It knows nothing about any product: every pack
// under conformance/packs is data (manifests, provider configuration, an
// optional response profile and scenarios), and every pack runs through the
// same shipped path: the operational reconciler with the reference HTTP and
// Kubernetes-status providers, bounded investigation, the interaction
// summary and, when a pack declares responses, the action engine with the
// reference actor. Scenario steps are generic verbs on evidence, workloads
// and the clock. Expectations are the pack's own; an independent checker
// (conformance_check_test.go) recomputes the forbidden outcomes after every
// step from the harness's ground truth, never from BORI's status.
//
// The evidence server stands in for an adapter that publishes a product's
// facts in the reference typed-response form; it is a fixture, not a
// product integration (see conformance/README.md).

const packsDir = "../conformance/packs"

type confPack struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// ExtraTargetKinds are targetRef kinds resolved in addition to the
	// shipped DefaultTargetKinds (a non-shipped extension, declared).
	ExtraTargetKinds []string       `json:"extraTargetKinds,omitempty"`
	Endpoints        []confEndpoint `json:"endpoints"`
	Actor            *confActor     `json:"actor,omitempty"`
	Scenarios        []confScenario `json:"scenarios"`

	dir       string
	objects   []client.Object
	workloads []client.Object
	providers []byte
	profile   *interaction.Profile
}

type confEndpoint struct {
	Path    string         `json:"path"`
	Subject string         `json:"subject"`
	Values  map[string]any `json:"values"`
}

type confActor struct {
	Provider   string   `json:"provider"`
	Owner      string   `json:"owner"`
	Namespaces []string `json:"namespaces"`
	Actions    []string `json:"actions"`
	// Behaviour is Succeed, Fail or AcceptOnly.
	Behaviour string `json:"behaviour"`
	// Approver signs approvals in approve steps.
	Approver string `json:"approver,omitempty"`
}

type confScenario struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	// Gap marks a scenario that demonstrates a requirement the model or
	// the products cannot meet today; its expectations pin the failure.
	Gap string `json:"gap,omitempty"`
	// OnExecute is applied to the world when the actor executes, as the
	// real authority's actuation would; without it the action changes
	// nothing in the world.
	OnExecute *confStep `json:"onExecute,omitempty"`
	// ShippedKindsOnly runs with the operator's shipped target kinds only,
	// ignoring the pack's declared extra kinds.
	ShippedKindsOnly bool       `json:"shippedKindsOnly,omitempty"`
	Steps            []confStep `json:"steps"`
}

type confStep struct {
	Note   string                    `json:"note,omitempty"`
	Values map[string]map[string]any `json:"values,omitempty"`
	// Unset removes published keys: the fact is not published at all.
	Unset      map[string][]string     `json:"unset,omitempty"`
	HTTPStatus map[string]int          `json:"httpStatus,omitempty"`
	Freeze     []string                `json:"freeze,omitempty"`
	Unfreeze   []string                `json:"unfreeze,omitempty"`
	Workload   map[string]confWorkload `json:"workload,omitempty"`
	Recreate   map[string]string       `json:"recreate,omitempty"`
	// Rebind sets "<target>/<slot>" to another provider config revision.
	Rebind  map[string]string `json:"rebind,omitempty"`
	Approve []string          `json:"approve,omitempty"`
	Advance string            `json:"advance,omitempty"`
	Rounds  int               `json:"rounds,omitempty"`
	Expect  *confExpect       `json:"expect,omitempty"`
}

type confWorkload struct {
	Ready     *int32 `json:"ready,omitempty"`
	Available *int32 `json:"available,omitempty"`
	Replicas  *int32 `json:"replicas,omitempty"`
}

type confExpect struct {
	Capabilities map[string]map[string]string `json:"capabilities,omitempty"`
	Envelopes    map[string]map[string]string `json:"envelopes,omitempty"`
	Invalid      map[string]string            `json:"invalid,omitempty"`
	Level        map[string]string            `json:"level,omitempty"`
	Human        map[string][]string          `json:"human,omitempty"`
	Actions      map[string][]confAction      `json:"actions,omitempty"`
	Executions   *int                         `json:"executions,omitempty"`
	// Quiet requires the step's reconciles to write no target status.
	Quiet bool `json:"quiet,omitempty"`
}

type confAction struct {
	Name     string `json:"name"`
	Phase    string `json:"phase,omitempty"`
	Recovery string `json:"recovery,omitempty"`
}

// loadPacks reads every pack directory.
func loadPacks(t *testing.T) []*confPack {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(packsDir, "*", "pack.yaml"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no packs under %s: %v", packsDir, err)
	}
	var out []*confPack
	for _, f := range dirs {
		dir := filepath.Dir(f)
		p := &confPack{dir: dir}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.UnmarshalStrict(b, p); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if p.Name != filepath.Base(dir) {
			t.Fatalf("%s: name %q does not match its directory", f, p.Name)
		}
		p.objects = decodeObjects(t, filepath.Join(dir, "manifests.yaml"))
		p.workloads = decodeObjects(t, filepath.Join(dir, "workloads.yaml"))
		if p.providers, err = os.ReadFile(filepath.Join(dir, "providers.yaml")); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "profile.yaml")); err == nil {
			j, err := yaml.YAMLToJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			if p.profile, err = interaction.ParseProfile(j); err != nil {
				t.Fatalf("%s profile: %v", p.Name, err)
			}
		}
		out = append(out, p)
	}
	return out
}

// decodeObjects reads a multi-document manifest into typed objects.
func decodeObjects(t *testing.T, path string) []client.Object {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	scheme := confScheme()
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 4096)
	var out []client.Object
	for {
		var u unstructured.Unstructured
		if err := dec.Decode(&u.Object); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("%s: %v", path, err)
		}
		if len(u.Object) == 0 {
			continue
		}
		obj, err := scheme.New(u.GroupVersionKind())
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructuredWithValidation(u.Object, obj, true); err != nil {
			t.Fatalf("%s %s/%s: %v", path, u.GetKind(), u.GetName(), err)
		}
		out = append(out, obj.(client.Object))
	}
	return out
}

func confScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = opsv1.AddToScheme(s)
	return s
}

// evidenceServer publishes typed values per path in the reference
// typed-response form.
type evidenceServer struct {
	*httptest.Server
	mu      sync.Mutex
	clk     *fakeClock
	subject map[string]string
	values  map[string]map[string]any
	status  map[string]int
	frozen  map[string]time.Time
	// served records every successful response: the observation time it
	// carried and the values it published.
	served []servedObs
}

type servedObs struct {
	path   string
	at     time.Time
	values map[string]string
}

func newEvidenceServer(t *testing.T, clk *fakeClock, eps []confEndpoint) *evidenceServer {
	s := &evidenceServer{clk: clk, subject: map[string]string{}, values: map[string]map[string]any{},
		status: map[string]int{}, frozen: map[string]time.Time{}}
	for _, ep := range eps {
		s.subject[ep.Path] = ep.Subject
		s.values[ep.Path] = map[string]any{}
		for k, v := range ep.Values {
			s.values[ep.Path][k] = v
		}
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if code := s.status[r.URL.Path]; code != 0 {
			w.WriteHeader(code)
			return
		}
		at := s.clk.now()
		if f, ok := s.frozen[r.URL.Path]; ok {
			at = f
		}
		vals := map[string]string{}
		for k, v := range s.values[r.URL.Path] {
			vals[k] = fmt.Sprint(v)
		}
		s.served = append(s.served, servedObs{path: r.URL.Path, at: at, values: vals})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subject": s.subject[r.URL.Path], "observedAt": at.Format(time.RFC3339Nano), "values": s.values[r.URL.Path]})
	}))
	t.Cleanup(s.Close)
	return s
}

type confApproval struct {
	decision       action.Decision
	target, action string
}

// providerObs is one result a shipped provider returned for a slot: the
// value it accepted, or why it refused (status lagging the spec, subject
// mismatch, ...). A successful GET or HTTP response is not acceptance.
type providerObs struct {
	provider, uid, slot string // provider "ns/name@rev" (its identity), subject UID
	at                  time.Time
	value, unavailable  string
}

// recordingLookup hands BORI the shipped providers unchanged and records
// every result they return.
type recordingLookup struct {
	inner investigate.ProviderLookup
	e     *confEnv
}

func (l recordingLookup) Lookup(id operations.ProviderIdentity) (providers.Provider, bool) {
	p, ok := l.inner.Lookup(id)
	if !ok {
		return nil, false
	}
	return recordingProvider{inner: p, e: l.e}, true
}

type recordingProvider struct {
	inner providers.Provider
	e     *confEnv
}

func (p recordingProvider) Observe(ctx context.Context, req providers.Request) providers.Result {
	res := p.inner.Observe(ctx, req)
	o := providerObs{provider: req.Key.Provider.String(), uid: req.Subject.ResolvedUID,
		slot: req.Key.Slot, at: res.ObservedAt, unavailable: res.Unavailable}
	switch {
	case res.Unavailable != "":
	case res.Value.Type == operations.TypeString:
		o.value = res.Value.Str // as the fixture publishes it, unquoted
	default:
		o.value = res.Value.String()
	}
	p.e.mu.Lock()
	p.e.observed = append(p.e.observed, o)
	p.e.mu.Unlock()
	return res
}

// confEnv is one scenario's world.
type confEnv struct {
	t         *testing.T
	p         *confPack
	c         client.Client
	r         *OperationalReconciler
	clk       *fakeClock
	ev        *evidenceServer
	actor     *actiontest.Actor
	decisions *actiontest.Decisions
	// approvals are the exact decisions approve steps signed, with the
	// target and action each was given for.
	approvals []confApproval
	// observed records every result a provider returned to BORI (not to
	// the checker): what it accepted, when, and for which subject.
	observed []providerObs
	// executedAt and endedAt are the reference actor's own facts per
	// handoff key: when it executed, and when the harness first saw the
	// actor report the execution ended. Never BORI's record.
	executedAt map[string]time.Time
	endedAt    map[string]time.Time
	// boundAt is the checker's record, per executed key, of every target
	// as it was when the actor executed it.
	boundAt map[string]map[string]handoffTruth
	effect  *confStep
	writes  int
	mu      sync.Mutex
	truth   *truth
	ck      confForbidden
}

func newConfEnv(t *testing.T, p *confPack, shippedOnly bool) *confEnv {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	e := &confEnv{t: t, p: p, clk: clk, executedAt: map[string]time.Time{}, endedAt: map[string]time.Time{},
		boundAt: map[string]map[string]handoffTruth{}}
	var objs []client.Object
	for _, o := range append(append([]client.Object(nil), p.objects...), p.workloads...) {
		objs = append(objs, o.DeepCopyObject().(client.Object))
	}
	e.c = fake.NewClientBuilder().WithScheme(confScheme()).WithObjects(objs...).
		WithStatusSubresource(&opsv1.OperationalContract{}, &opsv1.OperationalTarget{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*opsv1.OperationalTarget); ok && ctx.Value(driverKey{}) == nil {
					e.mu.Lock()
					e.writes++
					e.mu.Unlock()
				}
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).Build()
	e.ev = newEvidenceServer(t, clk, p.Endpoints)
	cfg := &providers.Config{}
	if err := yaml.UnmarshalStrict([]byte(strings.ReplaceAll(string(p.providers), "${EVIDENCE_URL}", e.ev.URL)), cfg); err != nil {
		t.Fatalf("%s providers: %v", p.Name, err)
	}
	reg, err := cfg.Build(e.c, clk.now)
	if err != nil {
		t.Fatalf("%s providers: %v", p.Name, err)
	}
	kinds := append([]schema.GroupVersionKind(nil), DefaultTargetKinds...)
	for _, k := range p.ExtraTargetKinds {
		if shippedOnly {
			break
		}
		// "<apiVersion>/<Kind>", e.g. "batch/v1/CronJob".
		cut := strings.LastIndex(k, "/")
		gv, err := schema.ParseGroupVersion(k[:max(cut, 0)])
		if cut < 0 || err != nil {
			t.Fatalf("extra kind %q", k)
		}
		kinds = append(kinds, gv.WithKind(k[cut+1:]))
	}
	e.r = &OperationalReconciler{
		Client: e.c, TargetKinds: kinds, Now: clk.now, RequeueInterval: 30 * time.Second,
		Investigator: investigate.New(investigate.ReferenceLimits(), clk.now), Providers: recordingLookup{inner: reg, e: e},
		Interaction: true, InteractionProfile: p.profile,
	}
	if p.Actor != nil {
		b := map[string]actiontest.Behaviour{"Succeed": actiontest.Succeed, "Fail": actiontest.Fail, "AcceptOnly": actiontest.AcceptOnly}
		beh, ok := b[p.Actor.Behaviour]
		if !ok {
			t.Fatalf("actor behaviour %q", p.Actor.Behaviour)
		}
		e.actor = actiontest.NewActor(beh)
		e.decisions = &actiontest.Decisions{}
		e.r.Actions = &action.Engine{
			Journal: action.NewSimulatedDurableJournal(),
			Providers: action.Registry{p.Actor.Provider: {Provider: e.actor, Owner: p.Actor.Owner,
				Namespaces: p.Actor.Namespaces, Actions: p.Actor.Actions}},
			Decisions: e.decisions, Verifier: actiontest.NewSigner(map[string]string{p.Actor.Approver: p.Actor.Approver + "-key"}),
		}
		e.actor.OnExecute = func(h action.Handoff) {
			// Runs inside the actor's Submit: only harness state here.
			e.noteBinding(h.Key)
			e.mu.Lock()
			e.executedAt[h.Key] = clk.now()
			eff := e.effect
			e.mu.Unlock()
			if eff != nil {
				e.apply(*eff)
			}
		}
	}
	e.truth = newTruth(e)
	return e
}

func (e *confEnv) driver() context.Context {
	return context.WithValue(context.Background(), driverKey{}, true)
}

// workload finds a fixture workload by "<Kind>/<name>".
func (e *confEnv) workload(ref string) client.Object {
	e.t.Helper()
	kind, name, ok := strings.Cut(ref, "/")
	if !ok {
		e.t.Fatalf("workload ref %q", ref)
	}
	for _, o := range e.p.workloads {
		if o.GetObjectKind().GroupVersionKind().Kind == kind && o.GetName() == name {
			cur := o.DeepCopyObject().(client.Object)
			if err := e.c.Get(e.driver(), types.NamespacedName{Namespace: o.GetNamespace(), Name: name}, cur); err != nil {
				e.t.Fatal(err)
			}
			return cur
		}
	}
	e.t.Fatalf("no workload %s", ref)
	return nil
}

// apply performs one step's world changes (not its expectations).
func (e *confEnv) apply(s confStep) {
	e.t.Helper()
	e.ev.mu.Lock()
	for path, vs := range s.Values {
		if e.ev.values[path] == nil {
			e.t.Fatalf("no endpoint %s", path)
		}
		for k, v := range vs {
			e.ev.values[path][k] = v
		}
	}
	for path, keys := range s.Unset {
		if e.ev.values[path] == nil {
			e.t.Fatalf("no endpoint %s", path)
		}
		for _, k := range keys {
			delete(e.ev.values[path], k)
		}
	}
	for path, code := range s.HTTPStatus {
		e.ev.status[path] = code
	}
	for _, path := range s.Freeze {
		e.ev.frozen[path] = e.clk.now()
	}
	for _, path := range s.Unfreeze {
		delete(e.ev.frozen, path)
	}
	e.ev.mu.Unlock()
	for _, ref := range sortedKeys(s.Workload) {
		w := s.Workload[ref]
		o := e.workload(ref)
		setReplicas(e.t, o, w)
		if err := e.c.Status().Update(e.driver(), o); err != nil {
			e.t.Fatal(err)
		}
	}
	for _, ref := range sortedKeys(s.Rebind) {
		tname, slot, _ := strings.Cut(ref, "/")
		t := e.target(tname)
		found := false
		for i := range t.Spec.AssertionBindings {
			if t.Spec.AssertionBindings[i].Slot == slot {
				t.Spec.AssertionBindings[i].Provider.ConfigRevision = s.Rebind[ref]
				found = true
			}
		}
		if !found {
			e.t.Fatalf("rebind: no binding %s", ref)
		}
		t.Generation++
		if err := e.c.Update(e.driver(), t); err != nil {
			e.t.Fatal(err)
		}
	}
	for _, ref := range sortedKeys(s.Recreate) {
		o := e.workload(ref)
		if err := e.c.Delete(e.driver(), o); err != nil {
			e.t.Fatal(err)
		}
		n := o.DeepCopyObject().(client.Object)
		n.SetResourceVersion("")
		n.SetUID(types.UID(s.Recreate[ref]))
		if err := e.c.Create(e.driver(), n); err != nil {
			e.t.Fatal(err)
		}
		if err := e.c.Status().Update(e.driver(), n); err != nil {
			e.t.Fatal(err)
		}
	}
}

func setReplicas(t *testing.T, o client.Object, w confWorkload) {
	switch d := o.(type) {
	case *appsv1.Deployment:
		if w.Ready != nil {
			d.Status.ReadyReplicas = *w.Ready
		}
		if w.Available != nil {
			d.Status.AvailableReplicas = *w.Available
		}
		if w.Replicas != nil {
			d.Status.Replicas = *w.Replicas
		}
	case *appsv1.StatefulSet:
		if w.Ready != nil {
			d.Status.ReadyReplicas = *w.Ready
		}
		if w.Available != nil {
			d.Status.AvailableReplicas = *w.Available
		}
		if w.Replicas != nil {
			d.Status.Replicas = *w.Replicas
		}
	default:
		t.Fatalf("workload %T has no replica status", o)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// approve signs the approver's decision for the action's current proposal
// on the named target.
func (e *confEnv) approve(ref string) {
	e.t.Helper()
	tname, act, ok := strings.Cut(ref, "/")
	if !ok || e.decisions == nil {
		e.t.Fatalf("approve %q", ref)
	}
	for _, a := range e.target(tname).Status.Interaction.Actions {
		if a.Name == act && a.Phase == "AwaitingApproval" {
			d := action.Decision{ID: fmt.Sprintf("d-%s-%d", tname, e.clk.now().Unix()), ProposalID: a.Proposal, Digest: a.Digest,
				Principal: e.p.Actor.Approver, Verdict: action.Approve, DecidedAt: e.clk.now()}
			e.decisions.Add(actiontest.Sign(d, e.p.Actor.Approver+"-key"))
			e.approvals = append(e.approvals, confApproval{decision: d, target: tname, action: act})
			return
		}
	}
	e.t.Fatalf("no proposal of %s awaiting approval on %s: %+v", act, tname, e.target(tname).Status.Interaction)
}

func (e *confEnv) target(name string) *opsv1.OperationalTarget {
	e.t.Helper()
	for _, o := range e.p.objects {
		if t, ok := o.(*opsv1.OperationalTarget); ok && t.Name == name {
			var cur opsv1.OperationalTarget
			if err := e.c.Get(e.driver(), types.NamespacedName{Namespace: t.Namespace, Name: name}, &cur); err != nil {
				e.t.Fatal(err)
			}
			return &cur
		}
	}
	e.t.Fatalf("no target %s", name)
	return nil
}

func (e *confEnv) reconcile() {
	e.t.Helper()
	if _, err := e.r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		e.t.Fatalf("reconcile: %v", err)
	}
}

// run executes one scenario: each step applies its changes, advances the
// clock, reconciles (rounds, 3s apart; 10 by default), runs the independent checker and
// compares the pack's expectations.
func (e *confEnv) run(sc confScenario) {
	e.t.Helper()
	e.effect = sc.OnExecute
	e.settle()
	for i, s := range sc.Steps {
		if s.Approve != nil {
			for _, ref := range s.Approve {
				e.approve(ref)
			}
		}
		e.apply(s)
		if s.Advance != "" {
			d, err := time.ParseDuration(s.Advance)
			if err != nil {
				e.t.Fatal(err)
			}
			e.clk.add(d)
		}
		rounds := s.Rounds
		if rounds == 0 {
			// One maxAge of observation: held evidence that is still current
			// is re-queried within this span.
			rounds = 10
		}
		e.mu.Lock()
		e.writes = 0
		e.mu.Unlock()
		for r := 0; r < rounds; r++ {
			if r > 0 {
				e.clk.add(3 * time.Second)
			}
			e.reconcile()
			e.check()
		}
		if s.Expect != nil {
			e.expect(i, s)
		}
	}
	e.ck.assertZero(e.t)
}

func (e *confEnv) expect(i int, s confStep) {
	e.t.Helper()
	x := s.Expect
	where := fmt.Sprintf("step %d (%s)", i, s.Note)
	for _, tn := range sortedKeys(x.Capabilities) {
		st := e.target(tn).Status
		for _, cn := range sortedKeys(x.Capabilities[tn]) {
			got := ""
			for _, c := range st.Capabilities {
				if c.Type.Name == cn {
					got = c.State
				}
			}
			if got != x.Capabilities[tn][cn] {
				e.t.Errorf("%s: %s %s is %q, want %q (reasons %+v)", where, tn, cn, got, x.Capabilities[tn][cn], reasonsOf(st, cn))
			}
		}
	}
	for _, tn := range sortedKeys(x.Envelopes) {
		st := e.target(tn).Status
		for _, en := range sortedKeys(x.Envelopes[tn]) {
			got := ""
			for _, v := range st.Envelopes {
				if v.Name == en {
					got = v.State
				}
			}
			if got != x.Envelopes[tn][en] {
				e.t.Errorf("%s: %s envelope %s is %q, want %q", where, tn, en, got, x.Envelopes[tn][en])
			}
		}
	}
	for _, tn := range sortedKeys(x.Invalid) {
		st := e.target(tn).Status
		found := false
		for _, r := range st.InvalidReasons {
			found = found || r.Code == x.Invalid[tn]
		}
		if st.Valid || !found {
			e.t.Errorf("%s: %s valid=%t reasons %+v, want invalid %s", where, tn, st.Valid, st.InvalidReasons, x.Invalid[tn])
		}
	}
	for _, tn := range sortedKeys(x.Level) {
		in := e.target(tn).Status.Interaction
		if in == nil || string(in.Level) != x.Level[tn] {
			e.t.Errorf("%s: %s level %+v, want %s", where, tn, in, x.Level[tn])
		}
	}
	for _, tn := range sortedKeys(x.Human) {
		in := e.target(tn).Status.Interaction
		for _, code := range x.Human[tn] {
			if in == nil || !hasCode(in.HumanReasons, code) {
				e.t.Errorf("%s: %s human %+v, want %s", where, tn, in, code)
			}
		}
	}
	for _, tn := range sortedKeys(x.Actions) {
		in := e.target(tn).Status.Interaction
		for _, want := range x.Actions[tn] {
			ok := false
			if in != nil {
				for _, a := range in.Actions {
					ok = ok || (a.Name == want.Name && (want.Phase == "" || a.Phase == want.Phase) && (want.Recovery == "" || a.Recovery == want.Recovery))
				}
			}
			if !ok {
				var got []opsv1.InteractionAction
				if in != nil {
					got = in.Actions
				}
				e.t.Errorf("%s: %s actions %+v, want %+v", where, tn, got, want)
			}
		}
	}
	if x.Executions != nil && (e.actor == nil || e.actor.TotalExecutions() != *x.Executions) {
		n := 0
		if e.actor != nil {
			n = e.actor.TotalExecutions()
		}
		e.t.Errorf("%s: executions %d, want %d", where, n, *x.Executions)
	}
	if x.Quiet && e.writes != 0 {
		e.t.Errorf("%s: %d target status writes, want 0", where, e.writes)
	}
}

func reasonsOf(st opsv1.OperationalTargetStatus, capName string) []opsv1.StatusReason {
	for _, c := range st.Capabilities {
		if c.Type.Name == capName {
			return c.Reasons
		}
	}
	return nil
}

// TestConformancePacks runs every scenario of every pack.
func TestConformancePacks(t *testing.T) {
	for _, p := range loadPacks(t) {
		for _, sc := range p.Scenarios {
			t.Run(p.Name+"/"+sc.Name, func(t *testing.T) {
				e := newConfEnv(t, p, sc.ShippedKindsOnly)
				e.run(sc)
				t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d steps=%d",
					e.ck.unrelated, e.ck.unjustifiedRecovery, e.ck.unauthorized, e.ck.staleReuse, len(sc.Steps))
			})
		}
	}
}
