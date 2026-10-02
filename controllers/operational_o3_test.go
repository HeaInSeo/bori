package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/investigate"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// Generic A/X/Y vertical slice with both real adapters:
//
//	A provides "provide": api-serving (HTTP) IsTrue + available-replicas (Kubernetes) Gte 1
//	X "consume": ready-replicas (Kubernetes) Gte 1 + dependency on A's provide (DEGRADED)
//	Y "work": api-serving (HTTP) IsTrue + ready-replicas (Kubernetes) Gte 1
//	Y "count": ready-replicas (Kubernetes) Gte 1 only

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type driverKey struct{}

// appServer is a controlled HTTP producer of typed responses.
type appServer struct {
	*httptest.Server
	mu      sync.Mutex
	serving map[string]bool
	status  map[string]int
	hits    map[string]int
	clk     *fakeClock
	subject map[string]string
}

func newAppServer(t *testing.T, clk *fakeClock) *appServer {
	s := &appServer{serving: map[string]bool{}, status: map[string]int{}, hits: map[string]int{}, clk: clk, subject: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits[r.URL.Path]++
		if code := s.status[r.URL.Path]; code != 0 {
			w.WriteHeader(code)
			return
		}
		fmt.Fprintf(w, `{"subject":%q,"observedAt":%q,"values":{"serving":%t}}`,
			s.subject[r.URL.Path], s.clk.now().Format(time.RFC3339Nano), s.serving[r.URL.Path])
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *appServer) set(path string, serving bool, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serving[path], s.status[path] = serving, status
}

func (s *appServer) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

type countingLookup struct {
	reg     investigate.ProviderLookup
	mu      sync.Mutex
	lookups map[string]int
}

func (c *countingLookup) Lookup(id operations.ProviderIdentity) (providers.Provider, bool) {
	c.mu.Lock()
	c.lookups[id.Name]++
	c.mu.Unlock()
	return c.reg.Lookup(id)
}

type o3Env struct {
	t            *testing.T
	c            client.Client
	r            *OperationalReconciler
	clk          *fakeClock
	app          *appServer
	lookup       *countingLookup
	statusWrites map[string]int
	otherWrites  int
	kubeGets     map[string]int
	mu           sync.Mutex
}

func boolS(n string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: n, Type: "Boolean", MaxAge: metav1.Duration{Duration: 30 * time.Second}}
}

func intS(n string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: n, Type: "Integer", MaxAge: metav1.Duration{Duration: 30 * time.Second}}
}

func isTrueReq(slot string, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Predicate: &opsv1.Predicate{Assertion: slot, Operator: "IsTrue"}, OnUnmet: impact}
}

func gteReq(slot string, n int64, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Predicate: &opsv1.Predicate{Assertion: slot, Operator: "Gte", Operand: &opsv1.Operand{Integer: &n}}, OnUnmet: impact}
}

func ct(name string) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: "example.io", Name: name, Revision: "v1"}
}

func deploy(name string, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: name, UID: types.UID("w-" + name), Generation: 1},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: ready, ReadyReplicas: ready},
	}
}

func opsTarget(name, contract string, bindings ...opsv1.AssertionBinding) *opsv1.OperationalTarget {
	return &opsv1.OperationalTarget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: name, UID: types.UID("t-" + name), Generation: 1},
		Spec: opsv1.OperationalTargetSpec{
			TargetRef:         opsv1.TargetReference{APIVersion: "apps/v1", Kind: "Deployment", Name: name},
			ContractRef:       opsv1.LocalContractReference{Name: contract},
			AssertionBindings: bindings,
		},
	}
}

func kubeBind(slot string) opsv1.AssertionBinding {
	return opsv1.AssertionBinding{Slot: slot, Provider: opsv1.ProviderReference{Name: "kube-status", ConfigRevision: "r1"}}
}

func httpBind(slot string) opsv1.AssertionBinding {
	return opsv1.AssertionBinding{Slot: slot, Provider: opsv1.ProviderReference{Name: "app-http", ConfigRevision: "r1"}}
}

func newO3Env(t *testing.T) *o3Env {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = opsv1.AddToScheme(scheme)
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	e := &o3Env{t: t, clk: clk, statusWrites: map[string]int{}, kubeGets: map[string]int{}}

	contracts := []*opsv1.OperationalContract{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "provider-v1", UID: "c-p", Generation: 1},
			Spec: opsv1.OperationalContractSpec{
				Assertions: []opsv1.AssertionSlot{boolS("api-serving"), intS("available-replicas")},
				Capabilities: []opsv1.Capability{{Type: ct("provide"), Requirements: []opsv1.Requirement{
					isTrueReq("api-serving", "UNAVAILABLE"), gteReq("available-replicas", 1, "UNAVAILABLE"),
				}}},
			}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "consumer-v1", UID: "c-c", Generation: 1},
			Spec: opsv1.OperationalContractSpec{
				Assertions:      []opsv1.AssertionSlot{intS("ready-replicas")},
				DependencySlots: []opsv1.DependencySlot{{Name: "upstream"}},
				Capabilities: []opsv1.Capability{{Type: ct("consume"), Requirements: []opsv1.Requirement{
					gteReq("ready-replicas", 1, "UNAVAILABLE"),
					{Dependency: &opsv1.DependencyRequirement{Slot: "upstream", Capability: ct("provide")}, OnUnmet: "DEGRADED"},
				}}},
			}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "solo-v1", UID: "c-s", Generation: 1},
			Spec: opsv1.OperationalContractSpec{
				Assertions: []opsv1.AssertionSlot{boolS("api-serving"), intS("ready-replicas")},
				Capabilities: []opsv1.Capability{
					{Type: ct("work"), Requirements: []opsv1.Requirement{isTrueReq("api-serving", "UNAVAILABLE"), gteReq("ready-replicas", 1, "UNAVAILABLE")}},
					{Type: ct("count"), Requirements: []opsv1.Requirement{gteReq("ready-replicas", 1, "DEGRADED")}},
				},
			}},
	}
	a := opsTarget("a", "provider-v1", httpBind("api-serving"), kubeBind("available-replicas"))
	x := opsTarget("x", "consumer-v1", kubeBind("ready-replicas"))
	x.Spec.DependencyBindings = []opsv1.DependencyBinding{{
		Slot:   "upstream",
		Target: opsv1.DependencyTargetReference{Name: "a", UID: "t-a"},
		Contract: opsv1.ContractPin{Name: "provider-v1", UID: "c-p",
			SpecDigest: opswire.SpecDigest(&contracts[0].Spec)},
	}}
	y := opsTarget("y", "solo-v1", httpBind("api-serving"), kubeBind("ready-replicas"))
	objs := []client.Object{a, x, y, deploy("a", 2), deploy("x", 2), deploy("y", 2)}
	for _, c := range contracts {
		objs = append(objs, c)
	}

	e.c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithStatusSubresource(&opsv1.OperationalContract{}, &opsv1.OperationalTarget{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*unstructured.Unstructured); ok {
					e.mu.Lock()
					e.kubeGets[key.Name]++
					e.mu.Unlock()
				}
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				e.other(ctx)
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				e.other(ctx)
				return c.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
				e.other(ctx)
				return c.Patch(ctx, obj, p, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				e.other(ctx)
				return c.Delete(ctx, obj, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if ctx.Value(driverKey{}) == nil {
					if _, ok := obj.(*opsv1.OperationalTarget); !ok {
						if _, ok := obj.(*opsv1.OperationalContract); !ok {
							e.other(ctx) // status write to anything but ops objects
						}
					}
					e.mu.Lock()
					e.statusWrites[obj.GetName()]++
					e.mu.Unlock()
				}
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).Build()

	e.app = newAppServer(t, clk)
	e.app.subject["/a"], e.app.subject["/y"] = "w-a", "w-y"
	e.app.set("/a", true, 0)
	e.app.set("/y", true, 0)
	cfg := &providers.Config{
		HTTP: providers.HTTPProfile{AllowInsecureHTTP: true, AllowPrivateNetworks: true},
		Providers: []providers.ProviderConfig{
			{Namespace: "apps", Name: "kube-status", ConfigRevision: "r1", KubernetesStatus: &providers.KubeStatusConfig{
				Fields: map[string]string{"available-replicas": "availableReplicas", "ready-replicas": "readyReplicas"}}},
			{Namespace: "apps", Name: "app-http", ConfigRevision: "r1", HTTP: &providers.HTTPConfig{Endpoints: []providers.HTTPEndpoint{
				{SubjectUID: "w-a", URL: e.app.URL + "/a", Fields: map[string]string{"api-serving": "serving"}},
				{SubjectUID: "w-y", URL: e.app.URL + "/y", Fields: map[string]string{"api-serving": "serving"}},
			}}},
			{Namespace: "other", Name: "app-http", ConfigRevision: "r1", HTTP: &providers.HTTPConfig{Endpoints: []providers.HTTPEndpoint{
				{SubjectUID: "w-y", URL: e.app.URL + "/y-foreign", Fields: map[string]string{"api-serving": "serving"}},
			}}},
		},
	}
	e.app.subject["/y-foreign"] = "w-y"
	e.app.set("/y-foreign", true, 0)
	reg, err := cfg.Build(e.c, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	e.lookup = &countingLookup{reg: reg, lookups: map[string]int{}}
	e.r = &OperationalReconciler{
		Client:          e.c,
		TargetKinds:     DefaultTargetKinds,
		Now:             clk.now,
		RequeueInterval: 30 * time.Second,
		Investigator:    investigate.New(investigate.ReferenceLimits(), clk.now),
		Providers:       e.lookup,
	}
	return e
}

func (e *o3Env) other(ctx context.Context) {
	if ctx.Value(driverKey{}) != nil {
		return
	}
	e.mu.Lock()
	e.otherWrites++
	e.mu.Unlock()
}

func (e *o3Env) driver() context.Context {
	return context.WithValue(context.Background(), driverKey{}, true)
}

func (e *o3Env) reconcile() map[string]int {
	e.t.Helper()
	if _, err := e.r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		e.t.Fatalf("reconcile: %v", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	w := e.statusWrites
	e.statusWrites = map[string]int{}
	delete(w, "provider-v1")
	delete(w, "consumer-v1")
	delete(w, "solo-v1")
	return w
}

func (e *o3Env) status(name string) opsv1.OperationalTargetStatus {
	e.t.Helper()
	var t opsv1.OperationalTarget
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: "apps", Name: name}, &t); err != nil {
		e.t.Fatal(err)
	}
	return t.Status
}

func (e *o3Env) capState(target, capName string) string {
	for _, c := range e.status(target).Capabilities {
		if c.Type.Name == capName {
			return c.State
		}
	}
	return "missing"
}

func (e *o3Env) expect(target, capName, want string) {
	e.t.Helper()
	if got := e.capState(target, capName); got != want {
		e.t.Fatalf("%s/%s = %s, want %s (status %+v)", target, capName, got, want, e.status(target))
	}
}

func (e *o3Env) scaleDriver(name string, ready int32) {
	var d appsv1.Deployment
	ctx := e.driver()
	_ = e.c.Get(ctx, types.NamespacedName{Namespace: "apps", Name: name}, &d)
	d.Status.ReadyReplicas, d.Status.AvailableReplicas = ready, ready
	if err := e.c.Status().Update(ctx, &d); err != nil {
		e.t.Fatal(err)
	}
}

func TestO3VerticalSliceNonContamination(t *testing.T) {
	e := newO3Env(t)
	e.app.set("/a", false, 0) // A: fresh negative application fact
	e.reconcile()

	e.expect("a", "provide", "UNAVAILABLE")
	e.expect("x", "consume", "DEGRADED") // X's own onUnmet for its dependency
	e.expect("y", "work", "AVAILABLE")   // Y untouched by A
	e.expect("y", "count", "AVAILABLE")
	if e.app.hitCount("/a") != 1 || e.app.hitCount("/y") != 1 {
		t.Fatalf("http hits a=%d y=%d", e.app.hitCount("/a"), e.app.hitCount("/y"))
	}
	if e.kubeGets["a"] != 1 || e.kubeGets["x"] != 1 || e.kubeGets["y"] != 1 {
		t.Fatalf("kube reads %v", e.kubeGets)
	}
	ep, ok := e.r.Investigator.Episode("t-a")
	if !ok || ep.State != investigate.Resolved || ep.Trace[0].Action != "select" {
		t.Fatalf("episode %+v", ep)
	}
	if st := e.status("y"); st.Sync != "NotApplicable" {
		t.Fatalf("sync %q", st.Sync)
	}
	if e.otherWrites != 0 {
		t.Fatalf("%d non-status writes by the operator", e.otherWrites)
	}
}

func TestO3RepeatedFreshFactsWriteNothing(t *testing.T) {
	e := newO3Env(t)
	e.reconcile()
	e.expect("y", "work", "AVAILABLE")
	hitsBefore := e.app.hitCount("/y")
	for i := 0; i < 10; i++ { // 30s: crosses refresh points; read times change
		e.clk.add(3 * time.Second)
		if w := e.reconcile(); len(w) != 0 {
			t.Fatalf("round %d wrote %v for unchanged semantics", i, w)
		}
	}
	if e.app.hitCount("/y") <= hitsBefore {
		t.Fatal("no refresh happened; the zero-write check proved nothing")
	}
	e.expect("y", "work", "AVAILABLE")
	if e.otherWrites != 0 {
		t.Fatalf("%d non-status writes", e.otherWrites)
	}
}

func TestO3RealTransitionAndProviderFailure(t *testing.T) {
	e := newO3Env(t)
	e.reconcile()
	e.expect("a", "provide", "AVAILABLE")

	// A real state change observed through the Kubernetes provider.
	e.scaleDriver("a", 0)
	var writes map[string]int
	for i := 0; i < 10 && e.capState("a", "provide") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		writes = e.reconcile()
	}
	e.expect("a", "provide", "UNAVAILABLE")
	e.expect("x", "consume", "DEGRADED")
	if writes["a"] != 1 || writes["x"] != 1 || writes["y"] != 0 {
		t.Fatalf("transition writes %v", writes)
	}

	// The HTTP provider fails: affected slots UNKNOWN, no fallback to the
	// previous AVAILABLE; Y's Kubernetes-only capability stays proven.
	e.app.set("/y", true, 503)
	for i := 0; i < 10 && e.capState("y", "work") != "UNKNOWN"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	e.expect("y", "work", "UNKNOWN")
	e.expect("y", "count", "AVAILABLE")
	for _, ev := range e.status("y").Evidence {
		if ev.Slot == "api-serving" && ev.State != "ProviderUnavailable" {
			t.Fatalf("evidence %+v", ev)
		}
	}
	if e.otherWrites != 0 {
		t.Fatalf("%d non-status writes", e.otherWrites)
	}
}

func setYProviderNamespace(t *testing.T, e *o3Env, ns string) {
	t.Helper()
	var y opsv1.OperationalTarget
	ctx := e.driver()
	_ = e.c.Get(ctx, types.NamespacedName{Namespace: "apps", Name: "y"}, &y)
	y.Spec.AssertionBindings[0].Provider.Namespace = ns
	if err := e.c.Update(ctx, &y); err != nil {
		t.Fatal(err)
	}
}

func providerGrant(typ opsv1.ReferenceType, name string) *opsv1.OperationalReferenceGrant {
	return &opsv1.OperationalReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "g"},
		Spec: opsv1.OperationalReferenceGrantSpec{
			From: []opsv1.ReferenceGrantFrom{{Kind: "OperationalTarget", Namespace: "apps"}},
			To:   []opsv1.ReferenceGrantTo{{Type: typ, Name: name}},
		},
	}
}

func TestO3GrantGatesLookupAndDispatch(t *testing.T) {
	e := newO3Env(t)
	setYProviderNamespace(t, e, "other")
	foreign := "other/app-http"

	for name, g := range map[string]*opsv1.OperationalReferenceGrant{
		"no grant":   nil,
		"wrong type": providerGrant(opsv1.ReferenceDependency, "app-http"),
		"wrong name": providerGrant(opsv1.ReferenceEvidenceProvider, "other-provider"),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := e.driver()
			if g != nil {
				if err := e.c.Create(ctx, g); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = e.c.Delete(ctx, g) }()
			}
			e.clk.add(11 * time.Second)
			e.reconcile()
			if e.lookup.lookups[foreign] != 0 || e.app.hitCount("/y-foreign") != 0 {
				t.Fatalf("denied binding reached registry (%d) or network (%d)", e.lookup.lookups[foreign], e.app.hitCount("/y-foreign"))
			}
			st := e.status("y")
			want := []opsv1.DeniedReference{{Type: opsv1.ReferenceEvidenceProvider, Slot: "api-serving", Namespace: "other"}}
			if !reflect.DeepEqual(st.DeniedReferences, want) {
				t.Fatalf("denied %+v", st.DeniedReferences)
			}
			e.expect("y", "work", "UNKNOWN")
			e.expect("y", "count", "AVAILABLE")
		})
	}

	// Exact grant: authorized, dispatched.
	g := providerGrant(opsv1.ReferenceEvidenceProvider, "app-http")
	if err := e.c.Create(e.driver(), g); err != nil {
		t.Fatal(err)
	}
	e.clk.add(11 * time.Second)
	e.reconcile()
	if e.app.hitCount("/y-foreign") != 1 || len(e.status("y").DeniedReferences) != 0 {
		t.Fatalf("granted provider not used: hits %d", e.app.hitCount("/y-foreign"))
	}
	e.expect("y", "work", "AVAILABLE")

	// Revocation: no further dispatch, held evidence for the binding dropped.
	if err := e.c.Delete(e.driver(), g); err != nil {
		t.Fatal(err)
	}
	hits := e.app.hitCount("/y-foreign")
	for i := 0; i < 4; i++ {
		e.clk.add(11 * time.Second)
		e.reconcile()
	}
	if e.app.hitCount("/y-foreign") != hits {
		t.Fatal("dispatch after revocation")
	}
	e.expect("y", "work", "UNKNOWN")
	if len(e.status("y").DeniedReferences) != 1 {
		t.Fatal("revoked reference not reported")
	}
}

func TestO3UnresolvedTargetsAreNotDispatched(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(e *o3Env)
		failure string
	}{
		{"missing workload", func(e *o3Env) {
			_ = e.c.Delete(e.driver(), deploy("y", 2))
		}, "target-not-found"},
		{"unsupported kind", func(e *o3Env) {
			var y opsv1.OperationalTarget
			_ = e.c.Get(e.driver(), types.NamespacedName{Namespace: "apps", Name: "y"}, &y)
			y.Spec.TargetRef = opsv1.TargetReference{APIVersion: "v1", Kind: "ConfigMap", Name: "y"}
			_ = e.c.Update(e.driver(), &y)
		}, "target-kind-unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := func(withReferent bool) opsv1.OperationalTargetStatus {
				e := newO3Env(t)
				setYProviderNamespace(t, e, "other")
				var y opsv1.OperationalTarget
				_ = e.c.Get(e.driver(), types.NamespacedName{Namespace: "apps", Name: "y"}, &y)
				y.Spec.DependencyBindings = []opsv1.DependencyBinding{{
					Slot: "upstream", Target: opsv1.DependencyTargetReference{Namespace: "other", Name: "z", UID: "t-z"},
					Contract: opsv1.ContractPin{Name: "c", UID: "u", SpecDigest: "sha256:x"},
				}}
				_ = e.c.Update(e.driver(), &y)
				if withReferent {
					z := opsTarget("z", "provider-v1")
					z.Namespace = "other"
					_ = e.c.Create(e.driver(), z)
				}
				tc.mutate(e)
				e.reconcile()
				if e.app.hitCount("/y") != 0 || e.app.hitCount("/y-foreign") != 0 || e.kubeGets["y"] != 0 {
					t.Fatalf("unresolved target dispatched: http %d/%d kube %d", e.app.hitCount("/y"), e.app.hitCount("/y-foreign"), e.kubeGets["y"])
				}
				if e.lookup.lookups["other/app-http"] != 0 {
					t.Fatal("denied provider looked up")
				}
				st := e.status("y")
				st.AssessedAt = nil
				for i := range st.Conditions {
					st.Conditions[i].LastTransitionTime = metav1.Time{}
				}
				return st
			}
			st := status(false)
			want := []opsv1.DeniedReference{
				{Type: opsv1.ReferenceEvidenceProvider, Slot: "api-serving", Namespace: "other"},
				{Type: opsv1.ReferenceDependency, Slot: "upstream", Namespace: "other"},
			}
			if st.Valid || st.InvalidReasons[0].Code != tc.failure || !reflect.DeepEqual(st.DeniedReferences, want) {
				t.Fatalf("status %+v", st)
			}
			if !reflect.DeepEqual(status(true), st) {
				t.Fatal("status depends on whether the denied referent exists")
			}
		})
	}
}

func TestO3IdentityChangeRejectsOldEvidence(t *testing.T) {
	e := newO3Env(t)
	e.reconcile()
	e.expect("y", "work", "AVAILABLE")
	// Recreate Y's workload: new UID. Old evidence must not apply.
	ctx := e.driver()
	_ = e.c.Delete(ctx, deploy("y", 2))
	d := deploy("y", 2)
	d.UID = "w-y-2"
	_ = e.c.Create(ctx, d)
	e.clk.add(time.Second)
	e.reconcile()
	e.expect("y", "work", "UNKNOWN") // cooldown: no fresh evidence yet
	if got := e.status("y").Identity.ResolvedUID; got != "w-y-2" {
		t.Fatalf("resolved %q", got)
	}
	// The HTTP endpoint is pinned to the old UID: it is never queried for
	// the new workload, so api-serving stays unproven even after cooldown.
	hits := e.app.hitCount("/y")
	e.clk.add(11 * time.Second)
	e.reconcile()
	if e.app.hitCount("/y") != hits {
		t.Fatal("endpoint pinned to the old workload was queried for the new one")
	}
	e.expect("y", "work", "UNKNOWN")
	e.expect("y", "count", "AVAILABLE") // fresh Kubernetes fact for the new UID
}

func TestO3NoProvidersKeepsO2Behaviour(t *testing.T) {
	e := newO3Env(t)
	e.r.Investigator, e.r.Providers = nil, nil
	e.reconcile()
	e.expect("y", "work", "UNKNOWN")
	if e.app.hitCount("/y") != 0 || len(e.kubeGets) != 0 {
		t.Fatal("I/O without an opted-in provider profile")
	}
}

func TestO3TraceIsBoundedAndOwnSlotsOnly(t *testing.T) {
	e := newO3Env(t)
	e.reconcile()
	ep, _ := e.r.Investigator.Episode("t-y")
	if len(ep.Trace) == 0 || len(ep.Trace) > investigate.ReferenceLimits().MaxTraceEntries {
		t.Fatalf("trace %d entries", len(ep.Trace))
	}
	for _, tr := range ep.Trace {
		if tr.Slot != "" && tr.Slot != "api-serving" && tr.Slot != "ready-replicas" {
			t.Fatalf("trace names a slot outside the target: %+v", tr)
		}
		if strings.Contains(tr.Detail, "w-a") || strings.Contains(tr.Detail, "t-a") {
			t.Fatalf("trace leaks another target: %+v", tr)
		}
	}
}
