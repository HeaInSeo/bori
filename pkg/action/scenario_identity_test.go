package action_test

import (
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Scenario 8: after a target recreation or a contract/provider revision
// change, no earlier approval, result or evidence is reused.
func TestIdentityChangeReusesNothing(t *testing.T) {
	before := map[string]func(w *world){
		"workload recreated": func(w *world) { w.targets["y"].ResolvedUID = "w-y2" },
		"contract revision":  func(w *world) { w.contract.Identity.SpecDigest = "d2"; w.retarget() },
		"provider revision":  func(w *world) { w.targets["y"].AssertionBindings[1].Provider.ConfigRevision = "r2" },
		"target recreated": func(w *world) {
			w.targets["y"].Identity.UID = "t-y2"
		},
	}
	for name, change := range before {
		t.Run("approved, then "+name, func(t *testing.T) {
			w := newWorld(t)
			v := w.view(w.step())
			w.approve(v)
			change(w)
			out := w.advance(time.Second)
			uid := w.targets["y"].Identity.UID
			if len(out.Views[uid]) != 1 {
				t.Fatalf("views %+v", out.Views)
			}
			nv := out.Views[uid][0]
			if nv.Phase != action.PhaseAwaitingApproval || nv.Digest == v.Digest {
				t.Fatalf("%+v", nv)
			}
			if len(w.actor.Handoffs) != 0 {
				t.Fatal("old approval executed the new identity")
			}
			w.assertSafe()
		})
	}

	after := map[string]func(w *world){
		"workload recreated": func(w *world) { w.targets["y"].ResolvedUID = "w-y2" },
		"contract revision":  func(w *world) { w.contract.Identity.SpecDigest = "d2"; w.retarget() },
		"provider revision":  func(w *world) { w.targets["y"].AssertionBindings[1].Provider.ConfigRevision = "r2" },
	}
	for name, change := range after {
		t.Run("executed, then "+name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.Default = actiontest.AcceptOnly
			w.effect = fixPersist(w) // the new instance is healthy
			approved(w)
			change(w)
			for _, k := range w.actor.ExecutedKeys() {
				w.actor.Settle(k, actiontest.Succeed)
			}
			var rec action.Record
			for i := 0; i < 5; i++ {
				w.advance(10 * time.Second)
			}
			for _, r := range w.records() {
				rec = r
			}
			if rec.Phase != action.PhaseSucceeded || rec.Recovery != action.RecoveryInapplicable {
				t.Fatalf("record %s/%s", rec.Phase, rec.Recovery)
			}
			for _, v := range w.history[len(w.history)-1].out.Views["t-y"] {
				if v.Recovery == action.RecoveryRecovered {
					t.Fatalf("old execution credited with the new identity's health: %+v", v)
				}
			}
			w.assertSafe()
		})
	}

	t.Run("executed, then target recreated", func(t *testing.T) {
		w := newWorld(t)
		w.effect = fixPersist(w)
		approved(w)
		w.targets["y"].Identity.UID = "t-y2"
		w.truth["y"]["storage-writable"] = operations.Bool(false)
		out := w.advance(10 * time.Second)
		if len(out.Views["t-y"]) != 0 {
			t.Fatalf("old target's execution shown: %+v", out.Views["t-y"])
		}
		if vs := out.Views["t-y2"]; len(vs) != 1 || vs[0].Phase != action.PhaseAwaitingApproval {
			t.Fatalf("new target %+v", vs)
		}
		for _, r := range w.records() {
			if r.TargetUID == "t-y" && r.Recovery != action.RecoveryInapplicable && r.Recovery != action.RecoveryNone {
				t.Fatalf("old record %+v", r)
			}
		}
		w.assertSafe()
	})
}

// retarget points every target at the current contract identity.
func (w *world) retarget() {
	for _, t := range w.targets {
		t.ContractRef = w.contract.Identity
	}
}

// A recurrence after a confirmed recovery is a new proposal (new episode):
// the earlier approval does not carry over, and nothing runs again without
// a new decision.
func TestRecurrenceAfterRecoveryNeedsANewApproval(t *testing.T) {
	w := newWorld(t)
	w.effect = fixPersist(w)
	first := w.view(w.step())
	w.approve(first)
	for i := 0; i < 3; i++ {
		w.advance(time.Second)
	}
	if v := w.view(w.history[len(w.history)-1].out); v.Recovery != action.RecoveryRecovered {
		t.Fatalf("%+v", v)
	}
	w.truth["y"]["storage-writable"] = operations.Bool(false) // fails again later
	var v action.View
	for i := 0; i < 5; i++ {
		v = w.view(w.advance(10 * time.Second))
	}
	if v.ProposalID == first.ProposalID || v.Phase != action.PhaseAwaitingApproval || w.actor.TotalExecutions() != 1 {
		t.Fatalf("%+v (first %s) executions %d", v, first.ProposalID, w.actor.TotalExecutions())
	}
	w.approve(v)
	w.advance(time.Second)
	if w.actor.TotalExecutions() != 2 {
		t.Fatalf("new approval not honoured: %d", w.actor.TotalExecutions())
	}
	w.assertSafe()
}
