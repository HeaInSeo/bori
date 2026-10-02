package controllers

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

var opsNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// staticSource is an in-memory ObservationSource.
type staticSource struct {
	mu  sync.Mutex
	obs []operations.Observation
}

func (s *staticSource) Observations(context.Context) ([]operations.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]operations.Observation(nil), s.obs...), nil
}

func (s *staticSource) set(obs ...operations.Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = obs
}

// writeCounter counts every status write, by object name.
type writeCounter struct {
	mu     sync.Mutex
	writes map[string]int
	total  int
	last   map[string]int
}

func (w *writeCounter) add(obj client.Object) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes[obj.GetName()]++
	w.total++
}

func (w *writeCounter) reset() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := w.total
	w.last = w.writes
	w.total = 0
	w.writes = map[string]int{}
	return n
}

type opsEnv struct {
	c       client.Client
	r       *OperationalReconciler
	src     *staticSource
	writes  *writeCounter
	now     time.Time
	workers map[string]types.UID
}

func boolSlot(name string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: name, Type: "Boolean", MaxAge: metav1.Duration{Duration: 30 * time.Second}}
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ct := &opsv1.OperationalContract{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "svc-v1", UID: "c-svc", Generation: 1},
		Spec: opsv1.OperationalContractSpec{
			Assertions: []opsv1.AssertionSlot{boolSlot("serving"), boolSlot("metrics-up")},
			Capabilities: []opsv1.Capability{
				{Type: opsv1.CapabilityType{Domain: "example.io", Name: "serve", Revision: "v1"},
					Requirements: []opsv1.Requirement{{Predicate: &opsv1.Predicate{Assertion: "serving", Operator: "IsTrue"}, OnUnmet: "UNAVAILABLE"}}},
				{Type: opsv1.CapabilityType{Domain: "example.io", Name: "export-metrics", Revision: "v1"},
					Requirements: []opsv1.Requirement{{Predicate: &opsv1.Predicate{Assertion: "metrics-up", Operator: "IsTrue"}, OnUnmet: "DEGRADED"}}},
			},
		},
	}
	env := &opsEnv{src: &staticSource{}, writes: &writeCounter{writes: map[string]int{}}, now: opsNow, workers: map[string]types.UID{}}
	objs := []client.Object{ct}
	for _, n := range []string{"web", "api"} {
		uid := types.UID("workload-" + n)
		env.workers[n] = uid
		objs = append(objs,
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: n, UID: uid}},
			&opsv1.OperationalTarget{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: n, UID: types.UID("t-" + n), Generation: 1},
				Spec: opsv1.OperationalTargetSpec{
					TargetRef:   opsv1.TargetReference{APIVersion: "apps/v1", Kind: "Deployment", Name: n},
					ContractRef: opsv1.LocalContractReference{Name: "svc-v1"},
					AssertionBindings: []opsv1.AssertionBinding{
						{Slot: "serving", Provider: opsv1.ProviderReference{Name: "generic-http-probe", ConfigRevision: "r1"}},
						{Slot: "metrics-up", Provider: opsv1.ProviderReference{Name: "generic-http-probe", ConfigRevision: "r1"}},
					},
				},
			})
	}
	env.c = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&opsv1.OperationalContract{}, &opsv1.OperationalTarget{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				env.writes.add(obj)
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
				env.writes.add(obj)
				return c.SubResource(sub).Patch(ctx, obj, p, opts...)
			},
		}).
		Build()
	env.r = &OperationalReconciler{
		Client:          env.c,
		Observations:    env.src,
		TargetKinds:     DefaultTargetKinds,
		Now:             func() time.Time { return env.now },
		RequeueInterval: 30 * time.Second,
	}
	env.observeAll(true, true)
	return env
}

// observeAll emits fresh evidence for both targets under their current binding.
func (e *opsEnv) observeAll(serving, metrics bool) {
	var obs []operations.Observation
	for _, n := range []string{"web", "api"} {
		obs = append(obs, e.obs(n, "serving", serving, "r1"), e.obs(n, "metrics-up", metrics, "r1"))
	}
	e.src.set(obs...)
}

func (e *opsEnv) obs(name, slot string, v bool, ref string) operations.Observation {
	var c opsv1.OperationalContract
	_ = e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: "svc-v1"}, &c)
	return operations.Observation{
		Key: operations.ApplicabilityKey{
			Target:      operations.TargetIdentity{Namespace: "apps", Name: name, UID: "t-" + name},
			ResolvedUID: string(e.workers[name]),
			Contract:    opswire.ContractIdentity(&c),
			Slot:        slot,
			Provider:    opswire.ProviderIdentity("apps", "generic-http-probe", "r1"),
		},
		Outcome:     operations.OutcomeValue,
		Value:       operations.Bool(v),
		ObservedAt:  opsNow.Add(-5 * time.Second),
		EvidenceRef: "ref:" + name + "/" + slot + "/" + ref,
	}
}

func (e *opsEnv) reconcile(t *testing.T) int {
	t.Helper()
	if _, err := e.r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return e.writes.reset()
}

func (e *opsEnv) target(t *testing.T, name string) *opsv1.OperationalTarget {
	t.Helper()
	var tg opsv1.OperationalTarget
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: name}, &tg); err != nil {
		t.Fatal(err)
	}
	return &tg
}

func stateOf(tg *opsv1.OperationalTarget, capName string) string {
	for _, c := range tg.Status.Capabilities {
		if c.Type.Name == capName {
			return c.State
		}
	}
	return ""
}

func TestOperationalFirstEvaluationWritesOncePerObject(t *testing.T) {
	e := newOpsEnv(t)
	if n := e.reconcile(t); n != 3 { // 1 contract + 2 targets
		t.Fatalf("first evaluation wrote %d statuses, want 3", n)
	}
	tg := e.target(t, "web")
	if !tg.Status.Valid || stateOf(tg, "serve") != "AVAILABLE" || tg.Status.Sync != "NotApplicable" {
		t.Fatalf("status %+v", tg.Status)
	}
	var c opsv1.OperationalContract
	_ = e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: "svc-v1"}, &c)
	if !c.Status.Valid || c.Status.SpecDigest != opswire.SpecDigest(&c.Spec) {
		t.Fatalf("contract status %+v", c.Status)
	}
}

func TestOperationalRepeatedEqualEvaluationWritesNothing(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	for i := 0; i < 20; i++ {
		e.now = e.now.Add(time.Second) // time passes inside maxAge
		if n := e.reconcile(t); n != 0 {
			t.Fatalf("evaluation %d wrote %d statuses for equal semantic input", i+2, n)
		}
	}
}

func TestOperationalOrderOnlyChangeWritesNothing(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	obs, _ := e.src.Observations(context.Background())
	for i, j := 0, len(obs)-1; i < j; i, j = i+1, j-1 {
		obs[i], obs[j] = obs[j], obs[i]
	}
	e.src.set(obs...)
	if n := e.reconcile(t); n != 0 {
		t.Fatalf("reordered evidence wrote %d statuses", n)
	}
}

func TestOperationalOwnStatusEventDoesNotLoop(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	tg := e.target(t, "web")
	old := tg.DeepCopy()
	old.Status = opsv1.OperationalTargetStatus{}
	// The controller's own status write is a status-only update: same
	// generation, so the watch predicate drops it.
	if SpecOrLifecycleChanged.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: tg}) {
		t.Fatal("own status-only update would re-enqueue")
	}
	// Even if a reconcile runs anyway, it writes nothing.
	if n := e.reconcile(t); n != 0 {
		t.Fatalf("reconcile after own write wrote %d", n)
	}
}

func TestOperationalUnrelatedEventWritesNothing(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	tg := e.target(t, "web")
	old := tg.DeepCopy()
	tg.Labels = map[string]string{"team": "x"} // metadata-only: generation unchanged
	if err := e.c.Update(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	if SpecOrLifecycleChanged.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: tg}) {
		t.Fatal("label-only update would enqueue")
	}
	if n := e.reconcile(t); n != 0 {
		t.Fatalf("unrelated change wrote %d", n)
	}
	var d appsv1.Deployment
	_ = e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: "web"}, &d)
	if recreateOnly.Update(event.UpdateEvent{ObjectOld: &d, ObjectNew: &d}) {
		t.Fatal("workload update would enqueue; only recreation changes its UID")
	}
}

func TestOperationalRealTransitionWritesExactlyOnce(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	before := e.target(t, "web")
	ltt := before.Status.Conditions[0].LastTransitionTime

	// Only web's serving evidence changes.
	obs, _ := e.src.Observations(context.Background())
	for i := range obs {
		if obs[i].Key.Target.Name == "web" && obs[i].Key.Slot == "serving" {
			obs[i].Value = operations.Bool(false)
		}
	}
	e.src.set(obs...)
	e.now = e.now.Add(10 * time.Second)
	if n := e.reconcile(t); n != 1 {
		t.Fatalf("transition wrote %d statuses, want exactly 1", n)
	}
	after := e.target(t, "web")
	if stateOf(after, "serve") != "UNAVAILABLE" || stateOf(after, "export-metrics") != "AVAILABLE" {
		t.Fatalf("status %+v", after.Status.Capabilities)
	}
	if !after.Status.Conditions[0].LastTransitionTime.Equal(&ltt) {
		t.Fatal("LastTransitionTime changed without a condition transition")
	}
	if !after.Status.AssessedAt.Time.Equal(e.now) {
		t.Fatal("assessedAt not updated on semantic change")
	}
	if n := e.reconcile(t); n != 0 {
		t.Fatalf("repeat after transition wrote %d", n)
	}
}

func TestOperationalEvidenceRefChangeWrites(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	e.src.set(e.obs("web", "serving", true, "r2"), e.obs("web", "metrics-up", true, "r1"),
		e.obs("api", "serving", true, "r1"), e.obs("api", "metrics-up", true, "r1"))
	if n := e.reconcile(t); n != 1 {
		t.Fatalf("evidence ref change wrote %d, want 1", n)
	}
}

func TestOperationalWorkloadRecreationAndConditionTransition(t *testing.T) {
	e := newOpsEnv(t)
	e.reconcile(t)
	ctx := context.Background()

	// Workload deleted: target invalid, condition transitions, LTT moves.
	var d appsv1.Deployment
	_ = e.c.Get(ctx, types.NamespacedName{Namespace: "apps", Name: "web"}, &d)
	if err := e.c.Delete(ctx, &d); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(10 * time.Second) // stay within maxAge so only web changes
	if n := e.reconcile(t); n != 1 {
		t.Fatalf("workload deletion wrote %d, want 1 (%v)", n, e.writes.last)
	}
	tg := e.target(t, "web")
	cond := tg.Status.Conditions[0]
	if tg.Status.Valid || cond.Status != metav1.ConditionFalse || !cond.LastTransitionTime.Time.Equal(e.now) {
		t.Fatalf("status %+v", tg.Status)
	}
	if n := e.reconcile(t); n != 0 {
		t.Fatalf("repeat wrote %d", n)
	}

	// Recreated with a new UID: old evidence is inapplicable → UNKNOWN.
	e.workers["web"] = "workload-web-2"
	if err := e.c.Create(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web", UID: "workload-web-2"}}); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Second)
	e.reconcile(t)
	tg = e.target(t, "web")
	if !tg.Status.Valid || stateOf(tg, "serve") != "UNKNOWN" || tg.Status.Identity.ResolvedUID != "workload-web-2" {
		t.Fatalf("after recreation %+v", tg.Status)
	}
}

func TestOperationalUnsupportedKindIsNotRead(t *testing.T) {
	e := newOpsEnv(t)
	tg := e.target(t, "api")
	tg.Spec.TargetRef = opsv1.TargetReference{APIVersion: "v1", Kind: "Secret", Name: "api"}
	if err := e.c.Update(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	e.reconcile(t)
	got := e.target(t, "api")
	if got.Status.Valid || got.Status.InvalidReasons[0].Code != opswire.ReasonTargetKindUnsupported {
		t.Fatalf("status %+v", got.Status)
	}
	if stateOf(e.target(t, "web"), "serve") != "AVAILABLE" {
		t.Fatal("unrelated target contaminated")
	}
}

func TestOperationalNoObservationsIsUnknownNotGuessed(t *testing.T) {
	e := newOpsEnv(t)
	e.r.Observations = NoObservations{}
	e.reconcile(t)
	if s := stateOf(e.target(t, "web"), "serve"); s != "UNKNOWN" {
		t.Fatalf("no evidence gave %s", s)
	}
}

func TestOperationalUnresolvedTargetKeepsDeniedReferences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		breakTarget func(t *testing.T, e *opsEnv, tg *opsv1.OperationalTarget)
		failure     string
	}{
		{"missing workload", func(t *testing.T, e *opsEnv, _ *opsv1.OperationalTarget) {
			var d appsv1.Deployment
			_ = e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: "api"}, &d)
			if err := e.c.Delete(context.Background(), &d); err != nil {
				t.Fatal(err)
			}
		}, opswire.ReasonTargetNotFound},
		{"unsupported kind", func(_ *testing.T, _ *opsEnv, tg *opsv1.OperationalTarget) {
			tg.Spec.TargetRef = opsv1.TargetReference{APIVersion: "v1", Kind: "ConfigMap", Name: "api"}
		}, opswire.ReasonTargetKindUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newOpsEnv(t)
			tg := e.target(t, "api")
			// Ungranted cross-namespace provider and dependency bindings.
			tg.Spec.AssertionBindings[0].Provider.Namespace = "other"
			tg.Spec.DependencyBindings = []opsv1.DependencyBinding{{
				Slot:     "upstream",
				Target:   opsv1.DependencyTargetReference{Namespace: "other", Name: "x", UID: "uid-x"},
				Contract: opsv1.ContractPin{Name: "c", UID: "u", SpecDigest: "sha256:x"},
			}}
			tc.breakTarget(t, e, tg)
			if err := e.c.Update(context.Background(), tg); err != nil {
				t.Fatal(err)
			}
			e.reconcile(t)
			st := e.target(t, "api").Status
			want := []opsv1.DeniedReference{
				{Type: opsv1.ReferenceEvidenceProvider, Slot: "serving", Namespace: "other"},
				{Type: opsv1.ReferenceDependency, Slot: "upstream", Namespace: "other"},
			}
			if st.Valid || st.InvalidReasons[0].Code != tc.failure || !reflect.DeepEqual(st.DeniedReferences, want) {
				t.Fatalf("status %+v", st)
			}
			if n := e.reconcile(t); n != 0 {
				t.Fatalf("repeat wrote %d statuses", n)
			}
		})
	}
}
