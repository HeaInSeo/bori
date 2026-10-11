package action_test

import (
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Codex r4237824547 and r4237824549: a resend is bound to the complete
// decision recorded with the attempt, not to its ID, and a refusal the
// journal learns only by polling is still a refusal, never an executed end.

// lostFirstSend declares A (remount, approval TTL ttl) and B (failover) in
// the given order, persists A approved by alice's d-1 and loses its first
// send; A's resend is due a few seconds later.
func lostFirstSend(t *testing.T, order string, ttl time.Duration) (*world, action.View, action.View) {
	t.Helper()
	w, va, vb := twoActions(t, order, func(a, _ *interaction.Response) { a.Execution.ApprovalTTL.Duration = ttl })
	w.actor.DropSubmits = 1
	w.decision("d-1", va, "alice", "", action.Approve, w.now, nil)
	w.advance(time.Second)
	w.advance(3 * time.Second)
	if r := w.recordOf(va.ProposalID); r.Approval.DecisionID != "d-1" || r.Phase != action.PhaseDispatching ||
		w.actor.TotalExecutions() != 0 || len(w.actor.Handoffs) != 0 {
		t.Fatalf("setup %+v executions %d", r, w.actor.TotalExecutions())
	}
	return w, va, vb
}

func isD1(d action.Decision) bool { return d.ID == "d-1" }

// A newly signed decision that reuses the recorded ID, principal, proposal
// and digest with a later time does not renew the expired original.
func TestSameIDRenewalDoesNotRenewTheRecordedApproval(t *testing.T) {
	for _, original := range []string{"kept", "removed"} {
		for _, restart := range []bool{false, true} {
			for _, order := range orders {
				name := original + "/" + order
				if restart {
					name += "/restart"
				}
				t.Run(name, func(t *testing.T) {
					w, va, _ := lostFirstSend(t, order, 2*time.Second)
					if original == "removed" {
						w.decisions.Remove(isD1)
					}
					w.decision("d-1", va, "alice", "", action.Approve, w.now, nil) // validly signed, new time
					if restart {
						w.restart()
					}
					for i := 0; i < 4; i++ {
						w.advance(time.Second)
					}
					w.assertSafe()
					if len(w.actor.Handoffs) != 0 || w.actor.TotalExecutions() != 0 {
						t.Fatalf("handoffs %+v: a same-ID renewal resent the expired d-1", w.actor.Handoffs)
					}
					if r := w.recordOf(va.ProposalID); r.Phase != action.PhaseWithdrawn {
						t.Fatalf("A %s, want Withdrawn", r.Phase)
					}
				})
			}
		}
	}
}

// Controls on the recorded decision: re-presented unchanged and still valid
// it is resent once, exactly; any other content under its ID, or its
// removal, lends nothing; a valid rejection stops the resend.
func TestRecordedDecisionControls(t *testing.T) {
	for _, order := range orders {
		for _, restart := range []bool{false, true} {
			sfx := "/" + order
			if restart {
				sfx += "/restart"
			}
			t.Run("unchanged and valid"+sfx, func(t *testing.T) {
				w, va, _ := lostFirstSend(t, order, time.Hour)
				if restart {
					w.restart()
				}
				w.advance(time.Second)
				w.advance(time.Second)
				w.assertSafe()
				r := w.recordOf(va.ProposalID)
				if len(w.actor.Handoffs) != 1 || w.actor.Handoffs[0].Approval != r.Approval || r.Approval.DecisionID != "d-1" ||
					w.actor.TotalExecutions() != 1 {
					t.Fatalf("handoffs %+v, want one resend with exactly d-1", w.actor.Handoffs)
				}
			})
			t.Run("valid rejection"+sfx, func(t *testing.T) {
				w, va, _ := lostFirstSend(t, order, time.Hour)
				w.decision("r-1", va, "alice", "", action.Reject, w.now, nil)
				if restart {
					w.restart()
				}
				w.advance(time.Second)
				w.assertSafe()
				if len(w.actor.Handoffs) != 0 || w.recordOf(va.ProposalID).Phase != action.PhaseWithdrawn {
					t.Fatalf("resent despite a valid rejection: %+v", w.actor.Handoffs)
				}
			})
		}
		// The original's own age is still valid, but the source no longer
		// presents it: a same-ID decision of other content does not stand in.
		t.Run("valid original replaced under its ID/"+order, func(t *testing.T) {
			w, va, _ := lostFirstSend(t, order, time.Hour)
			w.decisions.Remove(isD1)
			w.decision("d-1", va, "alice", "", action.Approve, w.now, nil)
			w.advance(time.Second)
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 || w.recordOf(va.ProposalID).Phase != action.PhaseWithdrawn {
				t.Fatalf("handoffs %+v", w.actor.Handoffs)
			}
		})
		t.Run("valid original withdrawn from the source/"+order, func(t *testing.T) {
			w, va, _ := lostFirstSend(t, order, time.Hour)
			w.decisions.Remove(isD1)
			w.advance(time.Second)
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 || w.recordOf(va.ProposalID).Phase != action.PhaseWithdrawn {
				t.Fatalf("handoffs %+v", w.actor.Handoffs)
			}
		})
		for name, tune := range map[string]struct {
			who, key string
			at       time.Duration
			f        func(*action.Decision)
		}{
			"wrong digest":     {"alice", "", 0, func(d *action.Decision) { d.Digest = "other" }},
			"forged signature": {"alice", keys["mallory"], 0, nil},
			"not an approver":  {"bob", "", 0, nil},
			"future":           {"alice", "", time.Hour, nil},
			"expired":          {"alice", "", -time.Hour, nil},
			"rejection":        {"alice", "", 0, func(d *action.Decision) { d.Verdict = action.Reject }},
		} {
			t.Run("expired original, same ID "+name+"/"+order, func(t *testing.T) {
				w, va, _ := lostFirstSend(t, order, 2*time.Second)
				w.decisions.Remove(isD1)
				w.decision("d-1", va, tune.who, tune.key, action.Approve, w.now.Add(tune.at), tune.f)
				for i := 0; i < 3; i++ {
					w.advance(time.Second)
				}
				w.assertSafe()
				if len(w.actor.Handoffs) != 0 || w.recordOf(va.ProposalID).Phase != action.PhaseWithdrawn {
					t.Fatalf("handoffs %+v", w.actor.Handoffs)
				}
			})
		}
		t.Run("expired original, same ID unsigned/"+order, func(t *testing.T) {
			w, va, _ := lostFirstSend(t, order, 2*time.Second)
			w.decisions.Remove(isD1)
			w.decisions.Add(action.Decision{ID: "d-1", ProposalID: va.ProposalID, Digest: va.Digest, Principal: "alice",
				Verdict: action.Approve, DecidedAt: w.now})
			for i := 0; i < 3; i++ {
				w.advance(time.Second)
			}
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("handoffs %+v", w.actor.Handoffs)
			}
		})
	}
}

// refusedUnrecorded makes the actor refuse A while the journal never
// records the refusal (its write fails, or its receipt is lost). After a
// restart the engine learns it only by polling the key.
func refusedUnrecorded(t *testing.T, order, how string) (*world, action.View, action.View, *actiontest.FaultJournal) {
	t.Helper()
	w, va, vb := twoActions(t, order, nil)
	fj := &actiontest.FaultJournal{Journal: w.journal}
	w.journal = fj
	w.restart()
	w.actor.PerAction["remount@r1"] = actiontest.Refuse
	if how == "refusal write fails" {
		fj.FailPhase = action.PhaseFailed
	} else {
		w.actor.LoseReceipts = 1
	}
	w.approve(va)
	w.advance(time.Second)
	if r := w.recordOf(va.ProposalID); r.Phase != action.PhaseDispatching || w.actor.TotalExecutions() != 0 || len(w.actor.Handoffs) != 1 {
		t.Fatalf("setup %+v executions %d", r, w.actor.TotalExecutions())
	}
	return w, va, vb, fj
}

var unrecorded = []string{"refusal write fails", "refusal receipt lost"}

func (w *world) shows(out action.Output, id string) bool {
	for _, v := range w.view2(out) {
		if v.ProposalID == id {
			return true
		}
	}
	return false
}

// A refusal polled after a failed refusal write or a lost refusal receipt
// releases the target, but has no execution end: fresh AVAILABLE evidence
// afterwards neither recovers it nor advances its episode.
func TestPolledRefusalIsNotAnExecution(t *testing.T) {
	for _, how := range unrecorded {
		for _, order := range orders {
			t.Run(how+"/"+order, func(t *testing.T) {
				w, va, _, fj := refusedUnrecorded(t, order, how)
				fj.FailPhase = ""
				w.restart()
				w.advance(time.Second)
				r := w.recordOf(va.ProposalID)
				if r.Phase != action.PhaseFailed || !r.EndedAt.IsZero() {
					t.Fatalf("A %s ended %v: a polled refusal was taken for an executed end", r.Phase, r.EndedAt)
				}
				w.truth["y"]["storage-writable"] = operations.Bool(true)
				for i := 0; i < 4; i++ {
					w.advance(30 * time.Second)
				}
				if st, _ := persistState(w); st != operations.Available {
					t.Fatalf("setup: persist %s", st)
				}
				if r := w.recordOf(va.ProposalID); r.Recovery != action.RecoveryNone || !r.EndedAt.IsZero() {
					t.Fatalf("refusal assessed: %+v", r)
				}
				w.truth["y"]["storage-writable"] = operations.Bool(false)
				if out := w.advance(time.Second); !w.shows(out, va.ProposalID) {
					t.Fatalf("episode advanced by a refusal: %+v", w.view2(out))
				}
				w.assertSafe()
				if w.actor.TotalExecutions() != 0 {
					t.Fatalf("executions %d", w.actor.TotalExecutions())
				}
			})
		}
	}
}

// While the refusal cannot be written the attempt keeps its target and
// nothing is sent unpersisted; once written, the target is released and the
// other action runs exactly once.
func TestRefusalWriteFaultStaysFailClosed(t *testing.T) {
	for _, order := range orders {
		t.Run(order, func(t *testing.T) {
			w, va, vb, fj := refusedUnrecorded(t, order, "refusal write fails")
			w.approve(vb)
			for i := 0; i < 3; i++ {
				w.advance(5 * time.Second)
			}
			w.restart()
			for i := 0; i < 3; i++ {
				w.advance(5 * time.Second)
			}
			a := w.recordOf(va.ProposalID)
			if w.actor.TotalExecutions() != 0 || (a.Phase != action.PhaseDispatching && a.Phase != action.PhaseTimedOut) || !a.EndedAt.IsZero() {
				t.Fatalf("A %+v executions %d", a, w.actor.TotalExecutions())
			}
			for _, h := range w.actor.Handoffs {
				if h.ProposalID != va.ProposalID {
					t.Fatalf("B sent while A's refusal was unrecorded: %+v", h)
				}
				if _, ok := w.recordByKey(h.Key); !ok {
					t.Fatalf("sent %s without a persisted record", h.Key)
				}
			}
			fj.FailPhase = ""
			w.advance(time.Second)
			w.advance(time.Second)
			w.assertSafe()
			if a := w.recordOf(va.ProposalID); a.Phase != action.PhaseFailed || !a.EndedAt.IsZero() {
				t.Fatalf("A %+v", a)
			}
			if w.actor.TotalExecutions() != 1 || w.actor.Executions[w.recordOf(vb.ProposalID).Key] != 1 {
				t.Fatalf("executions %v", w.actor.Executions)
			}
		})
	}
}
