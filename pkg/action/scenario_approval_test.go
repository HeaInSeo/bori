package action_test

import (
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/operations"
)

func fixPersist(w *world) func(action.Handoff) {
	return func(action.Handoff) { w.truth["y"]["storage-writable"] = operations.Bool(true) }
}

// Full flow: proposal → approval → handoff → receipt → result → recovery on
// post-execution evidence only.
func TestFlowApprovedSucceededRecovered(t *testing.T) {
	w := newWorld(t)
	w.effect = fixPersist(w)
	v := w.view(w.step())
	if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonApprovalDeclared) {
		t.Fatalf("first view %+v", v)
	}
	w.approve(v)
	v = w.view(w.advance(time.Second))
	if v.Phase != action.PhaseAccepted || w.actor.TotalExecutions() != 1 {
		t.Fatalf("after approval %+v executions %d", v, w.actor.TotalExecutions())
	}
	v = w.view(w.advance(time.Second))
	if v.Phase != action.PhaseSucceeded || v.Recovery != action.RecoveryPending {
		t.Fatalf("result step %+v (the completion instant's evidence must not count)", v)
	}
	v = w.view(w.advance(time.Second))
	if v.Recovery != action.RecoveryRecovered || len(v.Detail) != 1 || v.Detail[0].State != operations.Available {
		t.Fatalf("recovery %+v", v)
	}
	for i := 0; i < 5; i++ {
		w.advance(10 * time.Second)
	}
	if w.actor.TotalExecutions() != 1 {
		t.Fatalf("executions %d", w.actor.TotalExecutions())
	}
	w.assertSafe()
}

// Scenario 1: an approval-required proposal is never executed before a
// valid approval, however long it waits.
func TestApprovalRequiredIsNotExecutedBeforeApproval(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < 20; i++ {
		v := w.view(w.advance(5 * time.Second))
		if v.Phase != action.PhaseAwaitingApproval {
			t.Fatalf("step %d: %+v", i, v)
		}
	}
	if len(w.actor.Handoffs) != 0 || len(w.records()) != 0 {
		t.Fatalf("handoffs %d records %d before approval", len(w.actor.Handoffs), len(w.records()))
	}
	w.assertSafe()
}

// Scenario 2: wrong approver, forged signature, another proposal's approval,
// an approval of the pre-change meaning, expired and future decisions are
// all refused; a valid rejection stops the proposal.
func TestInvalidApprovalsAreRefused(t *testing.T) {
	cases := map[string]struct {
		decide func(w *world, v action.View)
		reason string
	}{
		"approver not declared": {func(w *world, v action.View) { w.decide(v, "mallory", keys["mallory"], action.Approve, w.now) },
			action.ReasonApproverNotAllowed},
		"forged signature (an object edit is not a decision)": {func(w *world, v action.View) {
			w.decide(v, "alice", keys["mallory"], action.Approve, w.now)
		}, action.ReasonApprovalUnverified},
		"unsigned": {func(w *world, v action.View) {
			w.decisions.Add(action.Decision{ID: "raw", ProposalID: v.ProposalID, Digest: v.Digest, Principal: "alice",
				Verdict: action.Approve, DecidedAt: w.now})
		}, action.ReasonApprovalUnverified},
		"expired": {func(w *world, v action.View) {
			w.decide(v, "alice", keys["alice"], action.Approve, w.now.Add(-2*time.Hour))
		},
			action.ReasonApprovalExpired},
		"future": {func(w *world, v action.View) {
			w.decide(v, "alice", keys["alice"], action.Approve, w.now.Add(time.Hour))
		},
			action.ReasonApprovalFuture},
		"other digest": {func(w *world, v action.View) {
			v.Digest = "0000"
			w.decide(v, "alice", keys["alice"], action.Approve, w.now)
		}, action.ReasonApprovalDigestMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			v := w.view(w.step())
			c.decide(w, v)
			for i := 0; i < 3; i++ {
				v = w.view(w.advance(time.Second))
			}
			if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, c.reason) {
				t.Fatalf("%+v, want AwaitingApproval with %s", v, c.reason)
			}
			if len(w.actor.Handoffs) != 0 {
				t.Fatal("handed off")
			}
			w.assertSafe()
		})
	}

	t.Run("approval of another proposal", func(t *testing.T) {
		w := newWorld(t)
		v := w.view(w.step())
		other := v
		other.ProposalID = "another-proposal"
		w.approve(other)
		if v = w.view(w.advance(time.Second)); v.Phase != action.PhaseAwaitingApproval || len(w.actor.Handoffs) != 0 {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})

	t.Run("approval before a change of meaning", func(t *testing.T) {
		w := newWorld(t)
		v := w.view(w.step())
		w.approve(v)
		// Before the next step the ready-replicas provider is reconfigured:
		// the binding (and so the proposal's meaning) changes.
		w.targets["y"].AssertionBindings[2].Provider.ConfigRevision = "r2"
		nv := w.view(w.advance(time.Second))
		if nv.Phase != action.PhaseAwaitingApproval || nv.Digest == v.Digest || !hasReason(nv, action.ReasonApprovalDigestMismatch) {
			t.Fatalf("%+v (old digest %s)", nv, v.Digest)
		}
		if len(w.actor.Handoffs) != 0 {
			t.Fatal("old approval executed the changed proposal")
		}
		w.assertSafe()
	})

	t.Run("rejection", func(t *testing.T) {
		w := newWorld(t)
		v := w.view(w.step())
		w.decide(v, "alice", keys["alice"], action.Reject, w.now)
		w.approve(v) // a rejection is not overridden
		if v = w.view(w.advance(time.Second)); v.Phase != action.PhaseRejected || len(w.actor.Handoffs) != 0 {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
}
