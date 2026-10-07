package investigate

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// targetProvider answers for one healthy target UID and blocks (context-
// cooperatively) for every other target.
type targetProvider struct {
	mu      sync.Mutex
	calls   map[string]int
	healthy string
}

func (p *targetProvider) Observe(ctx context.Context, req providers.Request) providers.Result {
	p.mu.Lock()
	p.calls[req.Key.Target.UID]++
	p.mu.Unlock()
	if req.Key.Target.UID == p.healthy {
		return providers.Result{Value: operations.Bool(true), ObservedAt: time.Now()}
	}
	<-ctx.Done()
	return providers.Result{Unavailable: "timeout"}
}

func (p *targetProvider) count(uid string) int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls[uid] }

// manyTargets builds n O1-valid seven-slot targets t00..t(n-1); real clock,
// reference proportions scaled by 1/100.
func manyTargets(t *testing.T, n int, healthy string) (*world, *targetProvider) {
	t.Helper()
	l := ReferenceLimits()
	l.RunSlice = 200 * time.Millisecond
	l.EpisodeDeadline = 100 * time.Millisecond
	l.EpisodeCooldown = 100 * time.Millisecond
	l.PerCallTimeout = 20 * time.Millisecond
	w := newWorld(l)
	w.base = sevenSlot()
	proto := w.base.Targets[0]
	w.base.Targets = nil
	for i := 0; i < n; i++ {
		tg := proto
		tg.Identity = operations.TargetIdentity{Namespace: "apps", Name: fmt.Sprintf("svc%d", i), UID: fmt.Sprintf("t%02d", i)}
		tg.ResolvedUID = fmt.Sprintf("w%02d", i)
		w.base.Targets = append(w.base.Targets, tg)
	}
	w.iv = New(l, time.Now)
	p := &targetProvider{calls: map[string]int{}, healthy: healthy}
	w.reg.ps[httpID] = p
	return w, p
}

func healthyAvailableWithin(t *testing.T, w *world, p *targetProvider, uid string, runs int) int {
	t.Helper()
	for r := 1; r <= runs; r++ {
		w.run(context.Background())
		s := w.base
		s.At = time.Now()
		s.Observations = w.iv.Observations()
		a, _ := operations.Evaluate(s)
		if ta, _ := a.Target(uid); p.count(uid) > 0 && capState(ta, "c0") == operations.Available {
			return r
		}
	}
	t.Fatalf("healthy target %s not AVAILABLE within %d runs (calls to it: %d)", uid, runs, p.count(uid))
	return 0
}

// Control kept from the central probe: four targets.
func TestRunSlicesReachFourthTarget(t *testing.T) {
	w, p := manyTargets(t, 4, "t03")
	healthyAvailableWithin(t, w, p, "t03", 8)
}

// Codex r4205754688 regression: progress through the target list carries
// across run slices, so a healthy late target in a larger list is reached in
// a bounded number of runs despite slow early targets. Declared bound: with
// two slow targets per 200ms slice, 16 targets are visited within 8 runs;
// 16 runs leaves margin. (The old loop restarted at t00 every run: 0 calls
// to t15 in 32 runs.)
func TestLargeTargetListHasBoundedProgress(t *testing.T) {
	w, p := manyTargets(t, 16, "t15")
	healthyAvailableWithin(t, w, p, "t15", 16)
	// The rotation state is a single cursor, not per-target state.
	if w.iv.cursor == "" {
		t.Fatal("no rotation cursor recorded")
	}
}

// The cursor follows the sorted order: a removed cursor target resumes at
// the next UID, a new smaller UID is visited after the wrap.
func TestRotationCursorSurvivesTargetListChanges(t *testing.T) {
	w, p := manyTargets(t, 6, "t05")
	w.run(context.Background())
	cur := w.iv.cursor
	// Remove the cursor target and add one sorting first.
	var kept []operations.Target
	for _, tg := range w.base.Targets {
		if tg.Identity.UID != cur {
			kept = append(kept, tg)
		}
	}
	extra := kept[0]
	extra.Identity.UID, extra.Identity.Name, extra.ResolvedUID = "t-00first", "first", "w-first"
	w.base.Targets = append(kept, extra)
	healthyAvailableWithin(t, w, p, "t05", 12)
}

// capFixture: 32 capabilities a00..a31 whose only requirement is a
// non-queryable hypothesis (local capability or dependency), plus zserve that
// requires one Boolean fact from the registered provider. The 32 prefix
// candidates sort before zserve's assertion candidate.
func capFixture(kind string) operations.Snapshot {
	c := operations.Contract{
		Identity:   svcContract().Identity,
		Assertions: []operations.AssertionSlot{{Name: "fact", Type: operations.TypeBoolean, MaxAge: 30 * time.Second}},
	}
	if kind == "dependency" {
		c.DependencySlots = []operations.DependencySlot{{Name: "upstream"}}
	}
	for n := 0; n < 32; n++ {
		r := operations.Requirement{LocalCapability: "zserve", OnUnmet: operations.ImpactDegraded}
		if kind == "dependency" {
			r = operations.Requirement{Dependency: &operations.DependencyRequirement{Slot: "upstream", Capability: capT("remote")}, OnUnmet: operations.ImpactDegraded}
		}
		c.Capabilities = append(c.Capabilities, operations.Capability{Type: capT(fmt.Sprintf("a%02d", n)), Requirements: []operations.Requirement{r}})
	}
	c.Capabilities = append(c.Capabilities, operations.Capability{Type: capT("zserve"), Requirements: []operations.Requirement{
		{Predicate: &operations.Predicate{Assertion: "fact", Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable},
	}})
	tg := svcTarget("t-svc", "w-svc")
	tg.ContractRef = c.Identity
	tg.AssertionBindings = []operations.AssertionBinding{{Slot: "fact", Provider: httpID}}
	return operations.Snapshot{Contracts: []operations.Contract{c}, Targets: []operations.Target{tg}}
}

// Codex r4205754694 regression: the candidate cap must not hide an
// assertion that has an authorized registered provider.
func TestCandidateCapKeepsQueryableAssertion(t *testing.T) {
	for _, kind := range []string{"local", "dependency"} {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(ReferenceLimits())
			w.base = capFixture(kind)
			if errs := w.base.Contracts[0].Validate(); len(errs) != 0 {
				t.Fatalf("invalid contract: %v", errs)
			}
			if ta := w.assess(); !ta.Valid {
				t.Fatalf("invalid target %+v", ta)
			}
			w.http.answers["fact"] = boolAt(w.clk, true)
			for n := 0; n < 5; n++ {
				w.run(context.Background())
				w.clk.advance(11 * time.Second)
			}
			if len(w.http.callList()) == 0 {
				t.Fatal("registered assertion hidden by the candidate cap: 0 calls")
			}
			ta := w.assess()
			if s := capState(ta, "zserve"); s != operations.Available {
				t.Fatalf("zserve %s", s)
			}
			if e, ok := w.iv.Episode("t-svc"); ok && len(e.Candidates) > ReferenceLimits().MaxCandidates {
				t.Fatalf("stored candidates %d exceed the cap", len(e.Candidates))
			}
		})
	}
}

// More queryable assertion candidates than the cap: the 32 lexically first
// slots stay unavailable (so their candidates stay Open and would fill the
// cap); the healthy later slots must still be reached across episodes, and
// an unregistered binding never is.
func TestMoreQueryableAssertionsThanCapAllProgress(t *testing.T) {
	const n = 40
	c := operations.Contract{Identity: operations.ContractIdentity{Namespace: "apps", Name: "wide", UID: "c-w", SpecDigest: "sha256:w"}}
	tg := operations.Target{
		Identity: operations.TargetIdentity{Namespace: "apps", Name: "svc", UID: "t-svc"}, ResolvedUID: "w-svc", ContractRef: c.Identity,
	}
	unregistered := operations.ProviderIdentity{Name: "apps/not-registered", ConfigRevision: "r1"}
	for i := 0; i <= n; i++ { // slot b40 is bound to an unregistered provider
		s := fmt.Sprintf("b%02d", i)
		c.Assertions = append(c.Assertions, operations.AssertionSlot{Name: s, Type: operations.TypeBoolean, MaxAge: 120 * time.Second})
		c.Capabilities = append(c.Capabilities, operations.Capability{Type: capT("k" + s), Requirements: []operations.Requirement{
			{Predicate: &operations.Predicate{Assertion: s, Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable},
		}})
		p := httpID
		if i == n {
			p = unregistered
		}
		tg.AssertionBindings = append(tg.AssertionBindings, operations.AssertionBinding{Slot: s, Provider: p})
	}
	w := newWorld(ReferenceLimits())
	w.base = operations.Snapshot{Contracts: []operations.Contract{c}, Targets: []operations.Target{tg}}
	for i := 0; i <= n; i++ {
		if i < 32 {
			w.http.answers[fmt.Sprintf("b%02d", i)] = func() providers.Result { return providers.Result{Unavailable: "http-status-503"} }
			continue
		}
		w.http.answers[fmt.Sprintf("b%02d", i)] = boolAt(w.clk, true)
	}
	// Declared bound: 40 slots at 6 calls per admitted episode = 7 episodes;
	// one extra for margin.
	episodes := (n+ReferenceLimits().MaxCallsPerEpisode-1)/ReferenceLimits().MaxCallsPerEpisode + 1
	for e := 0; e < episodes; e++ {
		before := len(w.http.callList())
		w.run(context.Background())
		if d := len(w.http.callList()) - before; d > ReferenceLimits().MaxCallsPerEpisode {
			t.Fatalf("episode %d made %d calls", e, d)
		}
		if ep, ok := w.iv.Episode("t-svc"); ok && len(ep.Candidates) > ReferenceLimits().MaxCandidates {
			t.Fatalf("stored candidates %d exceed the cap", len(ep.Candidates))
		}
		w.clk.advance(11 * time.Second)
	}
	ta := w.assess()
	for i := 0; i < n; i++ {
		want := operations.Available
		if i < 32 {
			want = operations.Unknown
		}
		if s := capState(ta, fmt.Sprintf("kb%02d", i)); s != want {
			t.Fatalf("kb%02d = %s, want %s after %d episodes; calls %d", i, s, want, episodes, len(w.http.callList()))
		}
	}
	if s := capState(ta, fmt.Sprintf("kb%02d", n)); s != operations.Unknown {
		t.Fatalf("unregistered binding produced %s", s)
	}
	for _, s := range w.http.callList() {
		if s == fmt.Sprintf("b%02d", n) {
			t.Fatal("unregistered binding dispatched")
		}
	}
	distinct := map[string]bool{}
	for _, s := range w.http.callList() {
		distinct[s] = true
	}
	if len(distinct) != n {
		t.Fatalf("%d distinct slots queried, want all %d registered", len(distinct), n)
	}
}
