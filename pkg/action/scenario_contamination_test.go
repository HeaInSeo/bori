package action_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Scenario 10: an unrelated target's failure and recovery, and an unrelated
// capability of the same target, never enter the proposal, the handoff or
// the recovery judgement.
func TestUnrelatedFailureAndRecoveryDoNotContaminate(t *testing.T) {
	w := newWorld(t)
	w.truth["a"]["api-serving"] = operations.Bool(false) // a.serve UNAVAILABLE (no response declared)
	w.truth["y"]["ready-replicas"] = operations.Int(0)   // y.ready UNAVAILABLE (not this action's business)
	w.effect = fixPersist(w)
	v := w.view(w.step())
	digest := v.Digest
	w.truth["a"]["api-serving"] = operations.Bool(true) // a recovers on its own
	if nv := w.view(w.advance(time.Second)); nv.Digest != digest {
		t.Fatal("another target's recovery changed y's proposal")
	}
	w.truth["a"]["api-serving"] = operations.Bool(false)
	w.approve(v)
	w.advance(time.Second)
	w.advance(time.Second)
	out := w.advance(time.Second)
	if len(out.Views["t-a"]) != 0 {
		t.Fatalf("views on the unrelated target: %+v", out.Views["t-a"])
	}
	v = w.view(out)
	if v.Recovery != action.RecoveryRecovered || len(v.Detail) != 1 || v.Detail[0].Type.Name != "persist" {
		t.Fatalf("%+v", v)
	}
	ta, _ := w.history[len(w.history)-1].a.Target("t-y")
	if c, _ := ta.Capability("ready"); c.State != operations.Unavailable {
		t.Fatalf("y.ready %s", c.State)
	}
	for _, h := range w.actor.Handoffs {
		if h.Target.UID != "t-y" || len(h.ExpectedImpact) != 1 || h.ExpectedImpact[0].Name != "persist" {
			t.Fatalf("handoff %+v", h)
		}
	}
	w.assertSafe()
}

// Same input, same output: proposal IDs and digests do not depend on input
// order, and repeated steps write nothing and hand off nothing again.
func TestDeterministicAndZeroChurn(t *testing.T) {
	w := newWorld(t)
	first := w.step()
	s := w.history[0].snap
	rev := s
	rev.Targets = []operations.Target{s.Targets[1], s.Targets[0]}
	rev.Observations = nil
	for i := len(s.Observations) - 1; i >= 0; i-- {
		rev.Observations = append(rev.Observations, s.Observations[i])
	}
	a, _ := operations.Evaluate(rev)
	other := &action.Engine{Journal: action.NewSimulatedDurableJournal(), Providers: w.eng.Providers}
	out, err := other.Step(context.Background(), action.Input{Snapshot: rev, Assessment: a, Profile: w.profile, Now: w.now})
	if err != nil || !reflect.DeepEqual(out.Views, first.Views) {
		t.Fatalf("order-dependent: %+v vs %+v", out.Views, first.Views)
	}

	w.effect = fixPersist(w)
	w.approve(w.view(first))
	for i := 0; i < 4; i++ {
		w.advance(time.Second)
	}
	versions := map[string]uint64{}
	for _, r := range w.records() {
		versions[r.Key] = r.Version
	}
	last := w.history[len(w.history)-1].out
	submits := w.eng.Stats().Submits
	for i := 0; i < 10; i++ {
		out := w.advance(5 * time.Second)
		if !reflect.DeepEqual(out.Views, last.Views) {
			t.Fatalf("views changed without a change in meaning:\n%+v\n%+v", out.Views, last.Views)
		}
	}
	for _, r := range w.records() {
		if versions[r.Key] != r.Version {
			t.Fatalf("record %s rewritten %d→%d", r.Key, versions[r.Key], r.Version)
		}
	}
	if w.eng.Stats().Submits != submits {
		t.Fatal("repeated steps handed off again")
	}
	w.assertSafe()
}

// The forbidden-outcome checker itself detects each class.
func TestForbiddenCheckerDetectsViolations(t *testing.T) {
	w := newWorld(t)
	v := w.view(w.step())
	// An execution without approval (direct actor call).
	w.pending = nil
	h := action.Handoff{Key: v.ProposalID + "/9", Fence: 9, ProposalID: v.ProposalID, Digest: "stale",
		Action: v.Action, Owner: "", Target: w.targets["y"].Identity, ResolvedUID: "w-old",
		Contract: w.contract.Identity}
	if _, err := w.actor.Submit(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	// A recovery claim with no ended execution, on an unexpected capability,
	// and a view on the unrelated target.
	s := w.history[0]
	s.out.Views = map[string][]action.View{
		"t-y": {{ProposalID: v.ProposalID, Digest: v.Digest, Action: v.Action, Recovery: action.RecoveryRecovered,
			Detail: []action.CapRecovery{{Type: cap("serve"), State: operations.Available}}}},
		"t-a": {{ProposalID: "x", Action: v.Action}},
	}
	w.history = append(w.history, s)
	w.check()
	f := w.ck
	if f.unrelated == 0 || f.unjustifiedRecovery == 0 || f.unauthorized == 0 || f.staleReuse == 0 {
		t.Fatalf("checker missed a class: %+v", f)
	}
}
