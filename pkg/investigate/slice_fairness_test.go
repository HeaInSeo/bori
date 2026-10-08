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

// blocking is a context-cooperative provider: each call takes d (real time)
// unless its context ends first.
type blocking struct {
	mu    sync.Mutex
	d     time.Duration
	calls []string // target UID + slot
}

func (b *blocking) Observe(ctx context.Context, req providers.Request) providers.Result {
	b.mu.Lock()
	b.calls = append(b.calls, req.Key.Target.UID+"/"+req.Key.Slot)
	b.mu.Unlock()
	select {
	case <-time.After(b.d):
		return providers.Result{Value: operations.Bool(true), ObservedAt: time.Now()}
	case <-ctx.Done():
		return providers.Result{Unavailable: "transport-error"}
	}
}

func (b *blocking) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.calls) }

// Codex r4181835334 regression: an in-flight call is bounded by the run
// slice, not only by the (longer) per-call timeout.
func TestInFlightCallIsBoundedByRunSlice(t *testing.T) {
	l := ReferenceLimits()
	l.RunSlice = 60 * time.Millisecond
	l.PerCallTimeout = 400 * time.Millisecond
	w := newWorld(l)
	w.iv = New(l, time.Now)
	slow := &blocking{d: 5 * time.Second}
	w.reg.ps[httpID] = slow
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)

	start := time.Now()
	w.run(context.Background())
	if el := time.Since(start); el > 250*time.Millisecond {
		t.Fatalf("Run took %s: the in-flight call overran the 60ms slice (per-call bound 400ms)", el)
	}
	if slow.count() != 1 {
		t.Fatalf("%d calls", slow.count())
	}
	// A slice-cut result is discarded and nothing is recorded for the slot;
	// the episode stays active with its remaining budget.
	for _, o := range w.iv.Observations() {
		if o.Key.Slot == "api-serving" {
			t.Fatalf("slice-cut call recorded %+v", o)
		}
	}
	if e, _ := w.iv.Episode("t-svc"); e.State != Active || e.Calls != 1 {
		t.Fatalf("episode %+v", e)
	}
}

// Codex r4181835334 regression: several targets consume the slice; the last
// call starts near expiry, is cut at the slice bound and nothing is
// dispatched afterwards.
func TestRunSliceCutsTheLastCallAcrossTargets(t *testing.T) {
	l := ReferenceLimits()
	l.RunSlice = 200 * time.Millisecond
	l.PerCallTimeout = 2 * time.Second
	w := newWorld(l)
	w.iv = New(l, time.Now)
	slow := &blocking{d: 150 * time.Millisecond}
	w.reg.ps[httpID] = slow
	w.kube.answers["available-replicas"] = func() providers.Result {
		return providers.Result{Value: operations.Int(3), ObservedAt: time.Now()}
	}
	w.kube.answers["ready-replicas"] = w.kube.answers["available-replicas"]
	w.base.Targets = append(w.base.Targets, svcTarget("t-svc2", "w-svc2"), svcTarget("t-svc3", "w-svc3"))

	start := time.Now()
	w.run(context.Background())
	el := time.Since(start)
	if el > 270*time.Millisecond {
		t.Fatalf("Run took %s, beyond the 200ms slice", el)
	}
	calls := slow.count()
	if calls != 2 {
		t.Fatalf("http calls %d (%v), want 2: one complete, one cut by the slice", calls, slow.calls)
	}
	time.Sleep(200 * time.Millisecond)
	if slow.count() != calls {
		t.Fatal("dispatch after the slice ended")
	}
	if e, ok := w.iv.Episode("t-svc3"); ok {
		t.Fatalf("third target admitted after the slice: %+v", e)
	}
	for _, o := range w.iv.Observations() {
		if o.Key.Target.UID == "t-svc2" && o.Key.Slot == "api-serving" {
			t.Fatalf("slice-cut result recorded: %+v", o)
		}
	}
}

// sevenSlot builds an O1-valid target with seven independent equal-score
// Boolean slots s0..s6, each the only input of its capability c0..c6.
func sevenSlot() operations.Snapshot {
	c := operations.Contract{Identity: operations.ContractIdentity{Namespace: "apps", Name: "seven", UID: "c-7", SpecDigest: "sha256:7"}}
	t := operations.Target{
		Identity:    operations.TargetIdentity{Namespace: "apps", Name: "svc", UID: "t-svc"},
		ResolvedUID: "w-svc",
		ContractRef: c.Identity,
	}
	for i := 0; i < 7; i++ {
		s := fmt.Sprintf("s%d", i)
		c.Assertions = append(c.Assertions, operations.AssertionSlot{Name: s, Type: operations.TypeBoolean, MaxAge: 30 * time.Second})
		c.Capabilities = append(c.Capabilities, operations.Capability{Type: capT(fmt.Sprintf("c%d", i)), Requirements: []operations.Requirement{
			{Predicate: &operations.Predicate{Assertion: s, Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable},
		}})
		t.AssertionBindings = append(t.AssertionBindings, operations.AssertionBinding{Slot: s, Provider: httpID})
	}
	return operations.Snapshot{Contracts: []operations.Contract{c}, Targets: []operations.Target{t}}
}

// Codex r4181835341 regression: six equal-score slots that stay unavailable
// must not starve the seventh healthy one across exhausted episodes.
func TestEqualScoreSlotsProgressAcrossEpisodes(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.base = sevenSlot()
	for i := 0; i < 6; i++ {
		w.http.answers[fmt.Sprintf("s%d", i)] = func() providers.Result { return providers.Result{Unavailable: "http-status-503"} }
	}
	w.http.answers["s6"] = boolAt(w.clk, true)

	queried := false
	for ep := 0; ep < 3 && !queried; ep++ {
		before := len(w.http.callList())
		w.run(context.Background())
		if n := len(w.http.callList()) - before; n > ReferenceLimits().MaxCallsPerEpisode {
			t.Fatalf("episode %d made %d calls", ep, n)
		}
		for _, s := range w.http.callList() {
			if s == "s6" {
				queried = true
			}
		}
		// Repeated reconciles inside the cooldown: zero calls.
		mark := len(w.http.callList())
		for i := 0; i < 3; i++ {
			w.clk.advance(time.Second)
			w.run(context.Background())
		}
		if len(w.http.callList()) != mark {
			t.Fatal("calls inside the cooldown")
		}
		w.clk.advance(8 * time.Second)
	}
	if !queried {
		t.Fatalf("healthy slot s6 never queried; calls %v", w.http.callList())
	}
	ta := w.assess()
	for i := 0; i < 7; i++ {
		want := operations.Unknown
		if i == 6 {
			want = operations.Available
		}
		if s := capState(ta, fmt.Sprintf("c%d", i)); s != want {
			t.Fatalf("c%d = %s, want %s", i, s, want)
		}
	}
	// Progress state is bounded by the target's slots and dropped with the
	// identity.
	if n := len(w.iv.progress["t-svc"].seq); n > 7 {
		t.Fatalf("progress holds %d slots", n)
	}
	w.base.Targets[0].ResolvedUID = "w-svc-recreated"
	w.clk.advance(11 * time.Second)
	w.run(context.Background())
	if p := w.iv.progress["t-svc"]; p != nil && p.key != episode(t, w).Key {
		t.Fatal("progress of the old identity still steers selection")
	}
	w.iv.Run(context.Background(), w.base, Queries{})
	if _, ok := w.iv.progress["t-svc"]; ok {
		t.Fatal("progress kept for a target that is no longer queryable")
	}
}
