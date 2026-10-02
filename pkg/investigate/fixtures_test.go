package investigate

import (
	"context"
	"sync"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// Generic fixture: one service target with two evidence sources.
//
//	serve  requires available-replicas Gte 1 (Kubernetes status) AND
//	       api-serving IsTrue (HTTP typed response), both UNAVAILABLE on unmet
//	export requires ready-replicas Gte 1 (Kubernetes status), DEGRADED on unmet
//	unused-slot is bound but read by nothing.

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	kubeID = operations.ProviderIdentity{Name: "apps/kube-status", ConfigRevision: "r1"}
	httpID = operations.ProviderIdentity{Name: "apps/app-http", ConfigRevision: "r1"}
)

func capT(n string) operations.CapabilityType {
	return operations.CapabilityType{Domain: "example.io", Name: n, Revision: "v1"}
}

func svcContract() operations.Contract {
	slot := func(n string, t operations.ValueType) operations.AssertionSlot {
		return operations.AssertionSlot{Name: n, Type: t, MaxAge: 30 * time.Second}
	}
	return operations.Contract{
		Identity: operations.ContractIdentity{Namespace: "apps", Name: "svc", UID: "c-svc", SpecDigest: "sha256:svc"},
		Assertions: []operations.AssertionSlot{
			slot("available-replicas", operations.TypeInteger),
			slot("ready-replicas", operations.TypeInteger),
			slot("api-serving", operations.TypeBoolean),
			slot("unused-slot", operations.TypeBoolean),
		},
		Capabilities: []operations.Capability{
			{Type: capT("serve"), Requirements: []operations.Requirement{
				{Predicate: &operations.Predicate{Assertion: "available-replicas", Op: operations.OpGte, Operand: operations.Int(1)}, OnUnmet: operations.ImpactUnavailable},
				{Predicate: &operations.Predicate{Assertion: "api-serving", Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable},
			}},
			{Type: capT("export"), Requirements: []operations.Requirement{
				{Predicate: &operations.Predicate{Assertion: "ready-replicas", Op: operations.OpGte, Operand: operations.Int(1)}, OnUnmet: operations.ImpactDegraded},
			}},
		},
	}
}

func svcTarget(uid, resolved string) operations.Target {
	return operations.Target{
		Identity:    operations.TargetIdentity{Namespace: "apps", Name: "svc", UID: uid},
		ResolvedUID: resolved,
		ContractRef: svcContract().Identity,
		AssertionBindings: []operations.AssertionBinding{
			{Slot: "available-replicas", Provider: kubeID},
			{Slot: "ready-replicas", Provider: kubeID},
			{Slot: "api-serving", Provider: httpID},
			{Slot: "unused-slot", Provider: httpID},
		},
	}
}

func baseSnapshot(targets ...operations.Target) operations.Snapshot {
	if len(targets) == 0 {
		targets = []operations.Target{svcTarget("t-svc", "w-svc")}
	}
	return operations.Snapshot{Contracts: []operations.Contract{svcContract()}, Targets: targets}
}

// scripted is a counting provider whose answer per slot is a function of the
// clock; it can also advance the clock to simulate call duration.
type scripted struct {
	mu       sync.Mutex
	clk      *clock
	answers  map[string]func() providers.Result
	delay    time.Duration
	calls    []string
	inFlight int
	maxIn    int
}

func (p *scripted) Observe(ctx context.Context, req providers.Request) providers.Result {
	p.mu.Lock()
	p.calls = append(p.calls, req.Key.Slot)
	p.inFlight++
	if p.inFlight > p.maxIn {
		p.maxIn = p.inFlight
	}
	ans := p.answers[req.Key.Slot]
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.inFlight--; p.mu.Unlock() }()
	if p.delay > 0 {
		p.clk.advance(p.delay)
	}
	if ans == nil {
		return providers.Result{Unavailable: "no-script"}
	}
	return ans()
}

func (p *scripted) callList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func intAt(clk *clock, v int64) func() providers.Result {
	return func() providers.Result {
		return providers.Result{Value: operations.Int(v), ObservedAt: clk.now(), EvidenceRef: "k8s:ref"}
	}
}

func boolAt(clk *clock, v bool) func() providers.Result {
	return func() providers.Result {
		return providers.Result{Value: operations.Bool(v), ObservedAt: clk.now(), EvidenceRef: "http:ref"}
	}
}

// registry counts lookups so tests can prove denied bindings never reach it.
type registry struct {
	ps      map[operations.ProviderIdentity]providers.Provider
	lookups int
}

func (r *registry) Lookup(id operations.ProviderIdentity) (providers.Provider, bool) {
	r.lookups++
	p, ok := r.ps[id]
	return p, ok
}

type world struct {
	clk  *clock
	kube *scripted
	http *scripted
	reg  *registry
	iv   *Investigator
	base operations.Snapshot
}

func newWorld(l Limits) *world {
	clk := &clock{t: t0}
	w := &world{
		clk:  clk,
		kube: &scripted{clk: clk, answers: map[string]func() providers.Result{}},
		http: &scripted{clk: clk, answers: map[string]func() providers.Result{}},
		base: baseSnapshot(),
	}
	w.reg = &registry{ps: map[operations.ProviderIdentity]providers.Provider{kubeID: w.kube, httpID: w.http}}
	w.iv = New(l, clk.now)
	return w
}

func (w *world) subjects() map[string]providers.Subject {
	out := map[string]providers.Subject{}
	for _, t := range w.base.Targets {
		out[t.Identity.UID] = providers.Subject{Namespace: "apps", APIVersion: "apps/v1", Kind: "Deployment", Name: t.Identity.Name, ResolvedUID: t.ResolvedUID}
	}
	return out
}

func (w *world) queries() Queries { return Requests(w.base, w.subjects(), w.reg) }

func (w *world) run(ctx context.Context) {
	w.iv.Run(ctx, w.base, w.queries())
}

func (w *world) assess() operations.TargetAssessment {
	s := w.base
	s.At = w.clk.now()
	s.Observations = w.iv.Observations()
	a, _ := operations.Evaluate(s)
	ta, _ := a.Target(w.base.Targets[0].Identity.UID)
	return ta
}

func capState(ta operations.TargetAssessment, name string) operations.CapabilityState {
	c, _ := ta.Capability(name)
	return c.State
}
