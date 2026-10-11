package action_test

import (
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Codex r4242054568 and r4242054571: a proposal is bound to the target's
// complete binding, so a provider or dependency rebind is a new proposal
// with no inherited execution history; and an attempt of an earlier episode
// that may still hold live responsibility stays visible beside the later
// episode's proposal, so its unknown outcome still reaches a person.

// withDependency gives the contract a dependency slot and binds y's to a.
func withDependency(w *world) {
	w.contract.DependencySlots = []operations.DependencySlot{{Name: "upstream"}}
	w.targets["y"].DependencyBindings = []operations.DependencyBinding{{Slot: "upstream", TargetUID: "t-a", Contract: w.contract.Identity}}
}

var rebinds = map[string]func(w *world){
	"provider revision": func(w *world) { w.targets["y"].AssertionBindings[2].Provider.ConfigRevision = "r2" },
	"dependency":        func(w *world) { w.targets["y"].DependencyBindings[0].Contract.SpecDigest = "d2" },
}

// history leaves remount's first proposal with one attempt in the given
// state; the actor result that ends it is adopted on the next step.
var histories = map[string]func(t *testing.T, w *world) action.View{
	"succeeded": func(t *testing.T, w *world) action.View {
		v := approved(w)
		w.advance(time.Second)
		if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseSucceeded {
			t.Fatalf("setup %+v", r)
		}
		return v
	},
	"failed, attempts exhausted": func(t *testing.T, w *world) action.View {
		w.actor.Default = actiontest.Fail
		v := approved(w)
		w.advance(time.Second)
		if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseFailed || r.Attempt != r.MaxAttempts {
			t.Fatalf("setup %+v", r)
		}
		return v
	},
	// The failure is reported but not yet adopted when the rebind happens:
	// the retry budget must not carry over.
	"failed, retry pending": func(t *testing.T, w *world) action.View {
		w.profile.Responses[0].Execution.Retryable = true
		w.profile.Responses[0].Execution.MaxAttempts = 2
		w.actor.Default = actiontest.Fail
		v := approved(w)
		if r := w.recordOf(v.ProposalID); r.Phase != action.PhaseAccepted {
			t.Fatalf("setup %+v", r)
		}
		return v
	},
}

func TestRebindIsANewProposal(t *testing.T) {
	for rname, rebind := range rebinds {
		for hname, history := range histories {
			t.Run(rname+"/"+hname, func(t *testing.T) {
				w := newWorld(t)
				withDependency(w)
				old := history(t, w)
				before := w.actor.TotalExecutions()
				rebind(w)
				v := w.view2(w.advance(time.Second))
				var cur action.View
				for _, x := range v {
					if x.ProposalID != old.ProposalID {
						cur = x
					}
				}
				if cur.ProposalID == "" || cur.Phase != action.PhaseAwaitingApproval || cur.Attempt != 1 {
					t.Fatalf("views %+v: the rebound identity reused %s's history", v, old.ProposalID)
				}
				// The old approval names the old proposal: nothing is lent.
				w.advance(time.Second)
				if w.actor.TotalExecutions() != before {
					t.Fatalf("executions %d, want %d", w.actor.TotalExecutions(), before)
				}
				w.actor.Default = actiontest.Succeed
				w.approve(cur)
				w.advance(time.Second)
				w.assertSafe()
				if w.actor.TotalExecutions() != before+1 || w.actor.Handoffs[len(w.actor.Handoffs)-1].Key != cur.ProposalID+"/1" {
					t.Fatalf("handoffs %+v", w.actor.Handoffs)
				}
			})
		}
	}
}

// Controls: an unchanged identity keeps its proposal through evidence
// refreshes and binding reorderings; a rebind while the earlier attempt is
// live keeps the target occupied.
func TestRebindControls(t *testing.T) {
	t.Run("unchanged identity keeps its proposal", func(t *testing.T) {
		w := newWorld(t)
		withDependency(w)
		id := w.view(w.step()).ProposalID
		for i := 0; i < 3; i++ {
			if v := w.view(w.advance(10 * time.Second)); v.ProposalID != id {
				t.Fatalf("evidence refresh churned %s → %s", id, v.ProposalID)
			}
		}
		bs := w.targets["y"].AssertionBindings
		for i, j := 0, len(bs)-1; i < j; i, j = i+1, j-1 {
			bs[i], bs[j] = bs[j], bs[i]
		}
		if v := w.view(w.advance(time.Second)); v.ProposalID != id {
			t.Fatalf("binding order churned %s → %s", id, v.ProposalID)
		}
		w.assertSafe()
	})
	for rname, rebind := range rebinds {
		t.Run("rebind while the earlier attempt is live/"+rname, func(t *testing.T) {
			w := newWorld(t)
			withDependency(w)
			w.actor.Default = actiontest.AcceptOnly
			old := approved(w)
			rebind(w)
			vs := w.view2(w.advance(time.Second))
			var cur action.View
			for _, v := range vs {
				if v.ProposalID != old.ProposalID {
					cur = v
				}
			}
			w.approve(cur)
			for i := 0; i < 3; i++ {
				vs = w.view2(w.advance(time.Second))
			}
			w.assertSafe()
			if w.actor.TotalExecutions() != 1 || !w.shows(w.history[len(w.history)-1].out, old.ProposalID) {
				t.Fatalf("executions %d views %+v", w.actor.TotalExecutions(), vs)
			}
			if v := w.viewOf(w.history[len(w.history)-1].out, cur.ProposalID); v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonTargetBusy) {
				t.Fatalf("%+v", v)
			}
		})
	}
}

// retryOutlivesItsEpisode: remount fails once and is retried; the first
// attempt recovers on fresh evidence (advancing the subject's episode)
// while the retry still holds the target, left Accepted, TimedOut or
// possibly delivered and Withdrawn; then y.persist breaks again and the
// next episode is proposed.
func retryOutlivesItsEpisode(t *testing.T, mode string) (*world, action.Record, action.View) {
	t.Helper()
	w := newWorld(t)
	ex := w.profile.Responses[0].Execution
	ex.Retryable, ex.MaxAttempts = true, 2
	if mode == "withdrawn" {
		ex.ApprovalTTL.Duration = 5 * time.Second
	}
	w.actor.Default = actiontest.Fail
	v1 := approved(w)
	w.actor.Default = actiontest.AcceptOnly
	if mode == "withdrawn" {
		w.actor.LoseReceipts = 1
	}
	w.advance(time.Second) // attempt 1 Failed, attempt 2 sent (live)
	if mode == "withdrawn" {
		// The receipt was lost and the provider's answer stays unknown
		// until the resend finds the approval expired.
		w.actor.Forge = []action.Result{{State: action.ResultUnknown}, {State: action.ResultUnknown}}
	}
	w.truth["y"]["storage-writable"] = operations.Bool(true)
	for i := 0; i < 3; i++ {
		w.advance(3 * time.Second)
	}
	switch mode {
	case "timed out":
		w.advance(61 * time.Second)
	}
	var retry action.Record
	for _, r := range w.records() {
		switch r.Attempt {
		case 1:
			if r.Recovery != action.RecoveryRecovered {
				t.Fatalf("setup: attempt 1 %+v", r)
			}
		case 2:
			retry = r
		}
	}
	want := map[string]action.Phase{"accepted": action.PhaseAccepted, "timed out": action.PhaseTimedOut, "withdrawn": action.PhaseWithdrawn}[mode]
	if retry.Phase != want || !w.actor.Live(retry.Key) {
		t.Fatalf("setup: retry %+v live %t", retry, w.actor.Live(retry.Key))
	}
	w.truth["y"]["storage-writable"] = operations.Bool(false)
	out := w.advance(time.Second)
	var next action.View
	for _, v := range w.view2(out) {
		if v.ProposalID != v1.ProposalID {
			next = v
		}
	}
	if next.ProposalID == "" || next.Phase != action.PhaseBlocked || !hasReason(next, action.ReasonTargetBusy) {
		t.Fatalf("setup: next episode %+v", w.view2(out))
	}
	return w, retry, next
}

var episodeModes = []string{"accepted", "timed out", "withdrawn"}

// statusOf is y's status as the real chain sees it: the engine's views
// through action.Status into interaction.Project.
func statusOf(w *world, out action.Output) *opsv1.InteractionStatus {
	ta, _ := w.history[len(w.history)-1].a.Target("t-y")
	st := opsv1.OperationalTargetStatus{Valid: true}
	for _, c := range ta.Capabilities {
		st.Capabilities = append(st.Capabilities, opsv1.CapabilityStatus{
			Type: opsv1.CapabilityType{Domain: c.Type.Domain, Name: c.Type.Name, Revision: c.Type.Revision}, State: string(c.State)})
	}
	return interaction.Project(interaction.Input{Namespace: "apps", Name: "y", UID: "t-y", Status: st,
		Actions: action.Status(out.Views["t-y"])}, nil)
}

func TestOccupyingEarlierEpisodeStaysVisible(t *testing.T) {
	for _, mode := range episodeModes {
		t.Run(mode, func(t *testing.T) {
			w, retry, next := retryOutlivesItsEpisode(t, mode)
			out := w.advance(time.Second)
			w.assertSafe()
			var shown *action.View
			for _, v := range w.view2(out) {
				if v.ProposalID == retry.ProposalID {
					v := v
					shown = &v
				}
			}
			if shown == nil || shown.Phase != retry.Phase || shown.Attempt != 2 {
				t.Fatalf("views %+v: the live retry %s is hidden behind %s", w.view2(out), retry.Key, next.ProposalID)
			}
			s := statusOf(w, out)
			found := false
			for _, a := range s.Actions {
				found = found || a.Proposal == retry.ProposalID
			}
			if !found {
				t.Fatalf("status actions %+v", s.Actions)
			}
			if mode == "accepted" {
				return // in flight: shown, no escalation of its own
			}
			if s.Level != opsv1.LevelDecisionRequired && s.Level != opsv1.LevelImmediateIntervention {
				t.Fatalf("level %s", s.Level)
			}
			esc := false
			for _, r := range s.HumanReasons {
				esc = esc || (r.Code == interaction.ReasonActionOutcomeUnknown && r.Subject == "remount@r1")
			}
			if !esc {
				t.Fatalf("human %+v, want action-outcome-unknown", s.HumanReasons)
			}
		})
	}
}

// Controls: once the provider ends the retry it is no longer shown beside
// the next episode, which then runs; a capability AVAILABLE again shows the
// retry without escalation; an unrelated target is untouched.
func TestOccupyingEpisodeControls(t *testing.T) {
	for _, mode := range episodeModes {
		t.Run("retry ended/"+mode, func(t *testing.T) {
			w, retry, next := retryOutlivesItsEpisode(t, mode)
			w.actor.Settle(retry.Key, actiontest.Succeed)
			out := w.advance(time.Second)
			if w.shows(out, retry.ProposalID) {
				t.Fatalf("ended retry still shown: %+v", w.view2(out))
			}
			w.approve(w.viewOf(out, next.ProposalID))
			w.advance(time.Second)
			w.assertSafe()
			if w.actor.Executions[next.ProposalID+"/1"] != 1 {
				t.Fatalf("executions %v", w.actor.Executions)
			}
			if vs := w.history[len(w.history)-1].out.Views["t-a"]; len(vs) != 0 {
				t.Fatalf("unrelated a: %+v", vs)
			}
		})
	}
	t.Run("capability available again", func(t *testing.T) {
		w, retry, _ := retryOutlivesItsEpisode(t, "timed out")
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		out := w.advance(time.Second)
		w.assertSafe()
		s := statusOf(w, out)
		if !w.shows(out, retry.ProposalID) || len(s.HumanReasons) != 0 {
			t.Fatalf("views %+v human %+v", w.view2(out), s.HumanReasons)
		}
	})
}

// The checker counts a live execution missing from its target's views as
// hidden, an execution under a binding that changed since it was persisted
// and a recovery across such a change as stale reuse, and nothing else.
func TestCheckerSeesHiddenLiveAndRebind(t *testing.T) {
	for _, drop := range []bool{true, false} {
		name := "live retry dropped from the views"
		if !drop {
			name = "control: live retry shown"
		}
		t.Run(name, func(t *testing.T) {
			w, retry, _ := retryOutlivesItsEpisode(t, "timed out")
			w.assertSafe()
			last := w.history[len(w.history)-1].out
			out := action.Output{Views: map[string][]action.View{}}
			for uid, vs := range last.Views {
				for _, v := range vs {
					if !drop || v.ProposalID != retry.ProposalID {
						out.Views[uid] = append(out.Views[uid], v)
					}
				}
			}
			w.now = w.now.Add(time.Second)
			w.injectView(out)
			want := 0
			if drop {
				want = 1
			}
			f := w.ck
			t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d hidden=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse, f.hidden)
			if f.unrelated+f.unjustifiedRecovery+f.unauthorized+f.staleReuse != 0 || f.hidden != want {
				t.Fatalf("want hidden=%d only: %+v", want, f)
			}
		})
	}
	for rname, rebind := range rebinds {
		for _, changed := range []bool{true, false} {
			sfx := rname
			if !changed {
				sfx = "control: unchanged"
			}
			t.Run("execution under the old binding/"+sfx, func(t *testing.T) {
				w := newWorld(t)
				withDependency(w)
				w.actor.DropSubmits = 1
				v := approved(w)
				if changed {
					rebind(w)
				}
				w.now = w.now.Add(time.Second)
				w.resendInjected(v.ProposalID)
				f := w.ck
				t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d hidden=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse, f.hidden)
				want := 0
				if changed {
					want = 1
				}
				if f.unrelated+f.unjustifiedRecovery+f.unauthorized+f.hidden != 0 || f.staleReuse != want {
					t.Fatalf("want staleReuse=%d only: %+v", want, f)
				}
			})
			t.Run("recovered across a rebind/"+sfx, func(t *testing.T) {
				w := newWorld(t)
				withDependency(w)
				v := approved(w)
				w.advance(time.Second) // Succeeded, ended at the actor
				if changed {
					rebind(w)
				}
				w.truth["y"]["storage-writable"] = operations.Bool(true)
				w.now = w.now.Add(time.Second)
				w.injectView(recoveredView(w.recordOf(v.ProposalID)))
				f := w.ck
				t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d hidden=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse, f.hidden)
				want := 0
				if changed {
					want = 1
				}
				if f.unrelated+f.unjustifiedRecovery+f.unauthorized+f.hidden != 0 || f.staleReuse != want {
					t.Fatalf("want staleReuse=%d only: %+v", want, f)
				}
			})
		}
	}
}
