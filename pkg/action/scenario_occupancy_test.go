package action_test

import (
	"context"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Codex r4237539530, r4237539535 and r4237539537: an attempt whose outcome
// is unknown keeps its target, a resend carries only its own still-valid
// decision, and an attempt that never definitively executed is never
// assessed as recovered.

// twoActions declares remount (A) and failover (B) for y.persist, both
// approved by alice, in the given order.
func twoActions(t *testing.T, order string, tune func(a, b *interaction.Response)) (*world, action.View, action.View) {
	t.Helper()
	a, b := remount(), remount()
	b.Action.Name = "failover"
	if tune != nil {
		tune(&a, &b)
	}
	return pair(t, a, b, order)
}

func (w *world) recordOf(proposalID string) action.Record {
	w.t.Helper()
	r, ok := w.latestRecord(proposalID)
	if !ok {
		w.t.Fatalf("no record of %s", proposalID)
	}
	return r
}

// unknownOutcome makes A execute at the actor and stay live there while
// BORI shows it TimedOut (completion timeout) or Withdrawn (receipt lost,
// result unknown, approval expired before the resend).
func unknownOutcome(t *testing.T, order, mode string) (*world, action.View, action.View) {
	t.Helper()
	w, va, vb := twoActions(t, order, func(a, _ *interaction.Response) {
		if mode == "withdrawn" {
			a.Execution.ApprovalTTL.Duration = 2 * time.Second
		}
	})
	w.actor.Default = actiontest.AcceptOnly
	if mode == "withdrawn" {
		w.actor.LoseReceipts = 1
	}
	w.approve(va)
	w.advance(time.Second)
	if w.actor.TotalExecutions() != 1 {
		t.Fatalf("A not executed: %d", w.actor.TotalExecutions())
	}
	want := action.PhaseTimedOut
	if mode == "timed out" {
		w.advance(61 * time.Second)
	} else {
		w.actor.Forge = []action.Result{{State: action.ResultUnknown}}
		w.advance(4 * time.Second)
		want = action.PhaseWithdrawn
	}
	if r := w.recordOf(va.ProposalID); r.Phase != want || !r.EndedAt.IsZero() {
		t.Fatalf("setup: %s, want %s without an end", r.Phase, want)
	}
	if !w.actor.Live(w.recordOf(va.ProposalID).Key) {
		t.Fatal("setup: A is not live at the actor")
	}
	return w, va, vb
}

var unknownModes = []string{"timed out", "withdrawn"}

func TestUnknownOutcomeKeepsTheTargetOccupied(t *testing.T) {
	for _, mode := range unknownModes {
		for _, order := range orders {
			t.Run(mode+"/"+order, func(t *testing.T) {
				w, va, vb := unknownOutcome(t, order, mode)
				w.approve(vb)
				for i := 0; i < 4; i++ {
					w.advance(10 * time.Second)
				}
				w.restart()
				out := w.advance(10 * time.Second)
				// An overlapping engine over the same journal sees the same.
				other := &action.Engine{Journal: w.journal, Providers: w.eng.Providers, Decisions: w.decisions, Verifier: w.signer}
				last := w.history[len(w.history)-1]
				if _, err := other.Step(context.Background(), action.Input{Snapshot: last.snap, Assessment: last.a, Profile: w.profile, Now: w.now}); err != nil {
					t.Fatal(err)
				}
				w.assertSafe()
				if n := w.actor.TotalExecutions(); n != 1 || len(w.actor.Handoffs) != 1 {
					t.Fatalf("executions %d handoffs %d: an unknown outcome released the target", n, len(w.actor.Handoffs))
				}
				if v := w.viewOf(out, vb.ProposalID); v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonTargetBusy) {
					t.Fatalf("B %+v, want Blocked target-busy", v)
				}
				if r := w.recordOf(va.ProposalID); r.Recovery != action.RecoveryNone {
					t.Fatalf("A assessed for recovery without an end: %+v", r)
				}
			})
		}
	}
}

// Only the provider's matching definitive result releases the target, also
// when it arrives late; the next action then runs exactly once.
func TestLateDefinitiveResultReleasesTheTarget(t *testing.T) {
	for _, end := range []actiontest.Behaviour{actiontest.Succeed, actiontest.Fail} {
		for _, mode := range unknownModes {
			for _, order := range orders {
				t.Run(mode+"/"+order, func(t *testing.T) {
					w, va, vb := unknownOutcome(t, order, mode)
					w.approve(vb)
					w.advance(10 * time.Second)
					if w.actor.TotalExecutions() != 1 {
						t.Fatal("B ran while A was live")
					}
					w.restart() // the result is still looked up by key
					w.actor.Settle(w.recordOf(va.ProposalID).Key, end)
					w.advance(time.Second)
					r := w.recordOf(va.ProposalID)
					want := action.PhaseSucceeded
					if end == actiontest.Fail {
						want = action.PhaseFailed
					}
					if r.Phase != want || !r.EndedAt.Equal(w.now) {
						t.Fatalf("A %s ended %v, want %s now", r.Phase, r.EndedAt, want)
					}
					for i := 0; i < 3; i++ {
						w.advance(10 * time.Second)
					}
					w.assertSafe()
					if w.actor.TotalExecutions() != 2 || w.recordOf(vb.ProposalID).Approval.Principal != "alice" {
						t.Fatalf("executions %d, want A then B once", w.actor.TotalExecutions())
					}
				})
			}
		}
	}
}

// A definite refusal executed nothing and releases the target at once; a
// target with no unknown outcome is not held by another target's.
func TestOccupancyControls(t *testing.T) {
	for _, order := range orders {
		t.Run("refused/"+order, func(t *testing.T) {
			w, va, vb := twoActions(t, order, nil)
			w.actor.PerAction["remount@r1"] = actiontest.Refuse
			w.approve(va)
			w.advance(time.Second)
			if r := w.recordOf(va.ProposalID); r.Phase != action.PhaseFailed || !r.EndedAt.IsZero() {
				t.Fatalf("A %+v", r)
			}
			w.approve(vb)
			w.advance(time.Second)
			w.assertSafe()
			if w.actor.TotalExecutions() != 1 || w.actor.Executions[w.recordOf(vb.ProposalID).Key] != 1 {
				t.Fatalf("executions %d", w.actor.TotalExecutions())
			}
		})
	}
	t.Run("unrelated target", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.AcceptOnly
		onA := remount()
		onA.Target.Name = "a"
		w.truth["a"]["storage-writable"] = operations.Bool(false)
		w.profile.Responses = append(w.profile.Responses, onA)
		w.approve(w.view(w.step()))
		w.advance(time.Second)
		w.advance(61 * time.Second)
		if r := w.records(); len(r) != 1 || r[0].Phase != action.PhaseTimedOut {
			t.Fatalf("setup %+v", r)
		}
		vs := w.history[len(w.history)-1].out.Views["t-a"]
		if len(vs) != 1 {
			t.Fatalf("a views %+v", vs)
		}
		w.approve(vs[0])
		w.advance(time.Second)
		w.assertSafe()
		if w.actor.TotalExecutions() != 2 {
			t.Fatalf("a held by y's unknown outcome: executions %d", w.actor.TotalExecutions())
		}
	})
}

// decision adds one decision with an explicit ID; tune runs before signing
// and key overrides the signing key.
func (w *world) decision(id string, v action.View, who, key string, verdict action.Verdict, at time.Time, tune func(*action.Decision)) {
	d := action.Decision{ID: id, ProposalID: v.ProposalID, Digest: v.Digest, Principal: who, Verdict: verdict, DecidedAt: at}
	if tune != nil {
		tune(&d)
	}
	if key == "" {
		key = keys[who]
	}
	w.decisions.Add(actiontest.Sign(d, key))
}

// droppedFirstSend persists remount approved by alice's d-1 and loses the
// first send; the resend is due one step later.
func droppedFirstSend(t *testing.T, ttl time.Duration, faulty bool) (*world, action.View, *actiontest.FaultJournal) {
	t.Helper()
	w := newWorld(t)
	var fj *actiontest.FaultJournal
	if faulty {
		fj = &actiontest.FaultJournal{Journal: w.journal}
		w.journal = fj
		w.restart()
	}
	w.profile.Responses[0].Execution.ApprovalTTL.Duration = ttl
	w.actor.DropSubmits = 1
	v := w.view(w.step())
	w.decision("d-1", v, "alice", "", action.Approve, w.now, nil)
	w.advance(time.Second)
	w.advance(3 * time.Second)
	if rs := w.records(); len(rs) != 1 || rs[0].Approval.DecisionID != "d-1" || rs[0].Phase != action.PhaseDispatching ||
		w.actor.TotalExecutions() != 0 {
		t.Fatalf("setup %+v executions %d", rs, w.actor.TotalExecutions())
	}
	return w, v, fj
}

// second is a later decision of the same proposal; every variant but
// "valid" and "reject" does not count.
var second = map[string]func(w *world, id string, v action.View){
	"valid": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", "", action.Approve, w.now, nil)
	},
	"reject": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", "", action.Reject, w.now, nil)
	},
	"wrong digest": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", "", action.Approve, w.now, func(d *action.Decision) { d.Digest = "other" })
	},
	"forged signature": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", keys["mallory"], action.Approve, w.now, nil)
	},
	"not an approver": func(w *world, id string, v action.View) {
		w.decision(id, v, "bob", "", action.Approve, w.now, nil)
	},
	"future": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", "", action.Approve, w.now.Add(time.Hour), nil)
	},
	"expired": func(w *world, id string, v action.View) {
		w.decision(id, v, "alice", "", action.Approve, w.now.Add(-time.Hour), nil)
	},
}

// A resend carries exactly the recorded decision, revalidated itself.
// Another decision of the same principal never lends it validity, whatever
// its ID sorts as.
func TestResendCarriesOnlyTheRecordedDecision(t *testing.T) {
	for name, add := range second {
		for _, id := range []string{"a-0", "z-9"} { // sorts before / after d-1
			t.Run("d-1 expired/"+name+"/"+id, func(t *testing.T) {
				w, v, _ := droppedFirstSend(t, 2*time.Second, false)
				add(w, id, v)
				for i := 0; i < 4; i++ {
					w.advance(time.Second)
				}
				w.assertSafe()
				if len(w.actor.Handoffs) != 0 || w.recordOf(v.ProposalID).Phase != action.PhaseWithdrawn {
					t.Fatalf("handoffs %+v record %+v: the expired d-1 was sent", w.actor.Handoffs, w.records())
				}
			})
			t.Run("d-1 valid/"+name+"/"+id, func(t *testing.T) {
				w, v, _ := droppedFirstSend(t, time.Hour, false)
				add(w, id, v)
				w.advance(time.Second)
				w.assertSafe()
				if name == "reject" {
					if len(w.actor.Handoffs) != 0 || w.recordOf(v.ProposalID).Phase != action.PhaseWithdrawn {
						t.Fatalf("resent despite a valid rejection: %+v", w.actor.Handoffs)
					}
					return
				}
				if len(w.actor.Handoffs) != 1 || w.actor.Handoffs[0].Approval.DecisionID != "d-1" || w.actor.TotalExecutions() != 1 {
					t.Fatalf("handoffs %+v, want one resend with d-1", w.actor.Handoffs)
				}
			})
		}
	}
}

func TestResendRevalidationSurvivesFaults(t *testing.T) {
	t.Run("resend write fails", func(t *testing.T) {
		w, v, fj := droppedFirstSend(t, time.Hour, true)
		fj.FailPuts = 1
		w.advance(time.Second)
		if len(w.actor.Handoffs) != 0 {
			t.Fatal("sent although the resend was not persisted")
		}
		w.advance(time.Second)
		w.assertSafe()
		if len(w.actor.Handoffs) != 1 || w.actor.Handoffs[0].Approval.DecisionID != "d-1" {
			t.Fatalf("%+v", w.actor.Handoffs)
		}
		if r := w.recordOf(v.ProposalID); r.Approval.DecisionID != "d-1" {
			t.Fatalf("record approval changed: %+v", r.Approval)
		}
	})
	for _, ttl := range []time.Duration{2 * time.Second, time.Hour} {
		t.Run("restart before resend/"+ttl.String(), func(t *testing.T) {
			w, v, _ := droppedFirstSend(t, ttl, false)
			w.decision("z-9", v, "alice", "", action.Approve, w.now, nil)
			w.restart()
			w.advance(time.Second)
			w.assertSafe()
			sent := len(w.actor.Handoffs)
			if ttl < time.Minute && sent != 0 || ttl > time.Minute && (sent != 1 || w.actor.Handoffs[0].Approval.DecisionID != "d-1") {
				t.Fatalf("ttl %s handoffs %+v", ttl, w.actor.Handoffs)
			}
		})
	}
}

// An attempt that never definitively executed is never recovered, and so
// never advances its proposal's episode, though its capability is AVAILABLE.
func TestOnlyADefinitivelyEndedExecutionIsAssessed(t *testing.T) {
	t.Run("dropped then withdrawn", func(t *testing.T) {
		w, v, _ := droppedFirstSend(t, 2*time.Second, false)
		w.advance(time.Second)
		if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseWithdrawn || w.actor.TotalExecutions() != 0 {
			t.Fatalf("setup %+v", r)
		}
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		for i := 0; i < 3; i++ {
			w.advance(30 * time.Second)
		}
		if st, _ := persistState(w); st != operations.Available {
			t.Fatalf("setup: persist %s", st)
		}
		if r := w.recordOf(v.ProposalID); r.Recovery != action.RecoveryNone {
			t.Fatalf("withdrawn intent assessed: %+v", r)
		}
		w.truth["y"]["storage-writable"] = operations.Bool(false)
		if again := w.view(w.advance(time.Second)); again.ProposalID != v.ProposalID {
			t.Fatalf("episode advanced by a withdrawn intent: %s → %s", v.ProposalID, again.ProposalID)
		}
		w.assertSafe()
	})
	t.Run("refused", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.Refuse
		v := approved(w)
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		w.advance(time.Second)
		w.advance(90 * time.Second)
		if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseFailed || r.Recovery != action.RecoveryNone {
			t.Fatalf("refusal assessed: %+v", r)
		}
		w.assertSafe()
	})
	for _, stale := range []bool{false, true} {
		name := "timed out, then late success"
		if stale {
			name += ", stale evidence"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.Default = actiontest.AcceptOnly
			v := approved(w)
			w.advance(61 * time.Second)
			w.truth["y"]["storage-writable"] = operations.Bool(true)
			w.advance(time.Second)
			if stale {
				w.frozen[okey("y", "storage-writable")] = true
			}
			for i := 0; i < 3; i++ {
				w.advance(10 * time.Second)
			}
			if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseTimedOut || r.Recovery != action.RecoveryNone {
				t.Fatalf("assessed while live at the actor: %+v", r)
			}
			w.actor.Settle(w.recordOf(v.ProposalID).Key, actiontest.Succeed)
			w.advance(time.Second)
			end := w.recordOf(v.ProposalID).EndedAt
			if !end.Equal(w.now) {
				t.Fatalf("late result not adopted: %+v", w.recordOf(v.ProposalID))
			}
			for i := 0; i < 8; i++ {
				w.advance(10 * time.Second)
			}
			w.assertSafe()
			r := w.recordOf(v.ProposalID)
			if stale && r.Recovery == action.RecoveryRecovered || !stale && r.Recovery != action.RecoveryRecovered {
				t.Fatalf("stale %t: recovery %s", stale, r.Recovery)
			}
		})
	}
}

func persistState(w *world) (operations.CapabilityState, bool) {
	ta, ok := w.history[len(w.history)-1].a.Target("t-y")
	for _, c := range ta.Capabilities {
		if c.Type == cap("persist") {
			return c.State, ok
		}
	}
	return operations.Unknown, false
}

// injectView runs the checker over one step whose engine output is out.
func (w *world) injectView(out action.Output) {
	w.t.Helper()
	w.observe()
	snap := w.snapshot()
	a, err := operations.Evaluate(snap)
	if err != nil {
		w.t.Fatal(err)
	}
	w.pending = nil
	w.history = append(w.history, stepLog{at: w.now, snap: snap, a: a, out: out})
	w.check()
}

func recoveredView(r action.Record) action.Output {
	return action.Output{Views: map[string][]action.View{r.TargetUID: {{
		ProposalID: r.ProposalID, Digest: r.Digest, Action: r.Action, For: r.For, Phase: r.Phase, Attempt: r.Attempt,
		Recovery: action.RecoveryRecovered, Detail: []action.CapRecovery{{Type: cap("persist"), State: operations.Available}},
	}}}}
}

func wantOnly(t *testing.T, w *world, unjustified, unauthorized int) {
	t.Helper()
	f := w.ck
	t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse)
	if f.unrelated+f.staleReuse != 0 || f.unjustifiedRecovery != unjustified || f.unauthorized != unauthorized {
		t.Fatalf("want unjustifiedRecovery=%d unauthorized=%d only: %+v", unjustified, unauthorized, f)
	}
}

// The checker judges live responsibility and execution from the reference
// actor's own facts, not from BORI's phases or times.
func TestCheckerSeesLiveResponsibilityAndUnexecutedRecovery(t *testing.T) {
	for _, order := range orders {
		for _, settled := range []bool{false, true} {
			name := "execution while another is live/" + order
			if settled {
				name = "control: earlier execution ended/" + order
			}
			t.Run(name, func(t *testing.T) {
				w, va, vb := twoActions(t, order, nil)
				w.actor.Default = actiontest.AcceptOnly
				w.approve(va)
				w.advance(time.Second)
				w.advance(61 * time.Second) // BORI shows A TimedOut
				if settled {
					w.actor.Settle(w.recordOf(va.ProposalID).Key, actiontest.Succeed)
				}
				w.assertSafe()
				_, b := remount(), remount()
				b.Action.Name = "failover"
				w.inject(b, *b.Execution, vb.ProposalID, vb.Digest, w.sign(vb, "alice", w.now))
				want := 1
				if settled {
					want = 0
				}
				wantOnly(t, w, 0, want)
			})
		}
	}
	t.Run("recovered without an execution", func(t *testing.T) {
		w, v, _ := droppedFirstSend(t, 2*time.Second, false)
		w.advance(time.Second) // Withdrawn, nothing executed
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		w.now = w.now.Add(time.Second)
		w.injectView(recoveredView(w.recordOf(v.ProposalID)))
		wantOnly(t, w, 1, 0)
	})
	for _, ended := range []bool{false, true} {
		name := "recovered while live at the actor"
		if ended {
			name = "control: recovered after the actor ended it"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.Default = actiontest.AcceptOnly
			v := approved(w)
			w.advance(61 * time.Second)
			if ended {
				w.actor.Settle(w.recordOf(v.ProposalID).Key, actiontest.Succeed)
				w.advance(time.Second)
			}
			w.truth["y"]["storage-writable"] = operations.Bool(true)
			w.now = w.now.Add(time.Second)
			w.injectView(recoveredView(w.recordOf(v.ProposalID)))
			if ended {
				wantOnly(t, w, 0, 0)
			} else {
				wantOnly(t, w, 1, 0)
			}
		})
	}
}
