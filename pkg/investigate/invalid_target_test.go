package investigate

import (
	"context"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// makeInvalid adds a duplicate authoritative binding, which O1 rejects.
func makeInvalid(t *operations.Target) {
	t.AssertionBindings = append(t.AssertionBindings, operations.AssertionBinding{Slot: "api-serving", Provider: httpID})
}

func heldFor(iv *Investigator, uid string) int {
	n := 0
	for _, o := range iv.Observations() {
		if o.Key.Target.UID == uid {
			n++
		}
	}
	return n
}

// Codex r4179707802 regression: evidence held for a target must not survive
// the target becoming O1-invalid. Correcting the spec afterwards must need a
// new provider call; the old evidence is never reused.
func TestInvalidTargetEvidenceIsNotReusedAfterCorrection(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if capState(w.assess(), "serve") != operations.Available || heldFor(w.iv, "t-svc") == 0 {
		t.Fatal("precondition: fresh evidence held")
	}
	valid := w.base.Targets[0]

	invalid := valid
	invalid.AssertionBindings = append([]operations.AssertionBinding(nil), valid.AssertionBindings...)
	makeInvalid(&invalid)
	w.base.Targets[0] = invalid
	w.clk.advance(time.Second)
	w.run(context.Background())
	if n := heldFor(w.iv, "t-svc"); n != 0 || len(w.iv.marks) != 0 {
		t.Fatalf("invalid target retained %d held keys / %d watermarks", n, len(w.iv.marks))
	}
	if e, _ := w.iv.Episode("t-svc"); e.active() {
		t.Fatalf("episode still active for an invalid target: %+v", e)
	}

	// Spec corrected before maxAge would have expired, still inside the
	// cooldown: no provider call is possible yet, so nothing may be AVAILABLE.
	w.base.Targets[0] = valid
	calls := len(w.http.callList()) + len(w.kube.callList())
	w.clk.advance(time.Second)
	w.run(context.Background())
	if n := len(w.http.callList()) + len(w.kube.callList()); n != calls {
		t.Fatalf("unexpected calls inside the cooldown: %d", n-calls)
	}
	if s := capState(w.assess(), "serve"); s == operations.Available {
		t.Fatal("stale evidence of the invalid period reused without a provider call")
	}
	// After the cooldown, fresh calls restore the state.
	w.clk.advance(10 * time.Second)
	w.run(context.Background())
	if n := len(w.http.callList()) + len(w.kube.callList()); n <= calls {
		t.Fatal("no new provider call after correction")
	}
	if s := capState(w.assess(), "serve"); s != operations.Available {
		t.Fatalf("serve %s after fresh calls", s)
	}
}

// Codex r4179707802 regression: a permanently invalid target must not hold
// MaxHeldObservations capacity that a valid target needs.
func TestInvalidTargetDoesNotHoldCapacity(t *testing.T) {
	l := ReferenceLimits()
	l.MaxHeldObservations = 3 // exactly one target's used slots
	w := newWorld(l)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if heldFor(w.iv, "t-svc") != 3 {
		t.Fatalf("precondition: %d held keys", heldFor(w.iv, "t-svc"))
	}

	makeInvalid(&w.base.Targets[0])
	w.base.Targets = append(w.base.Targets, svcTarget("t-svc2", "w-svc2"))
	w.clk.advance(11 * time.Second)
	w.run(context.Background())

	s := w.base
	s.At = w.clk.now()
	s.Observations = w.iv.Observations()
	a, _ := operations.Evaluate(s)
	ta, _ := a.Target("t-svc2")
	if c, _ := ta.Capability("serve"); c.State != operations.Available {
		t.Fatalf("valid target starved of capacity by an invalid one: serve %s, stats %+v", c.State, w.iv.Stats())
	}
	if heldFor(w.iv, "t-svc") != 0 {
		t.Fatal("invalid target still holds keys")
	}
}
