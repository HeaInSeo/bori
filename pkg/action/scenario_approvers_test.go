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

// disjoint returns one action/capability/owner declared with two execution
// contracts whose approvers do not overlap: A by alice, B by bob.
func disjoint() (a, b interaction.Response) {
	a, b = remount(), remount()
	b.Execution.Approvers = []string{"bob"}
	return a, b
}

// proposalID is the proposal ID of a declaration, taken from a world that
// declares it alone (the ID depends only on the subject, not on siblings).
func proposalID(t *testing.T, r interaction.Response) string {
	t.Helper()
	w := newWorld(t)
	w.profile.Responses = []interaction.Response{r}
	return w.view(w.step()).ProposalID
}

// pair steps a world declaring a and b in the given order and returns the
// views of a and b.
func pair(t *testing.T, a, b interaction.Response, order string) (*world, action.View, action.View) {
	t.Helper()
	idA, idB := proposalID(t, a), proposalID(t, b)
	if idA == idB {
		t.Fatal("two declarations, one proposal")
	}
	w := newWorld(t)
	w.profile.Responses = []interaction.Response{a, b}
	if order == "b first" {
		w.profile.Responses = []interaction.Response{b, a}
	}
	var va, vb action.View
	for _, v := range w.view2(w.step()) {
		switch v.ProposalID {
		case idA:
			va = v
		case idB:
			vb = v
		}
	}
	if va.ProposalID == "" || vb.ProposalID == "" {
		t.Fatalf("views %+v", w.view2(w.history[len(w.history)-1].out))
	}
	return w, va, vb
}

func (w *world) viewOf(out action.Output, id string) action.View {
	w.t.Helper()
	for _, v := range w.view2(out) {
		if v.ProposalID == id {
			return v
		}
	}
	w.t.Fatalf("no view %s", id)
	return action.View{}
}

var orders = []string{"a first", "b first"}

// A principal declared only for one execution contract cannot approve
// another contract of the same action, even with a valid signature on that
// proposal's exact ID and digest.
func TestApproversAreDeclarationScoped(t *testing.T) {
	for _, order := range orders {
		t.Run("bob for A/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, _ := pair(t, a, b, order)
			w.decide(va, "bob", keys["bob"], action.Approve, w.now)
			var vs []action.View
			for i := 0; i < 3; i++ {
				vs = append(vs, w.viewOf(w.advance(time.Second), va.ProposalID))
			}
			// The independent checker first, then the engine's own outcome.
			t.Logf("handoffs=%d", len(w.actor.Handoffs))
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("bob's approval executed A: %+v", w.actor.Handoffs)
			}
			for _, v := range vs {
				if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonApproverNotAllowed) {
					t.Fatalf("%+v", v)
				}
			}
		})

		// Controls, each in its own world: the declared approver runs
		// exactly its own proposal once.
		for _, c := range []struct{ name, who string }{{"alice for A", "alice"}, {"bob for B", "bob"}} {
			t.Run(c.name+"/"+order, func(t *testing.T) {
				a, b := disjoint()
				w, va, vb := pair(t, a, b, order)
				v := va
				if c.who == "bob" {
					v = vb
				}
				w.decide(v, c.who, keys[c.who], action.Approve, w.now)
				for i := 0; i < 4; i++ {
					w.advance(time.Second)
				}
				if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].ProposalID != v.ProposalID ||
					w.actor.Handoffs[0].Approval.Principal != c.who {
					t.Fatalf("executions %d: %+v", w.actor.TotalExecutions(), w.actor.Handoffs)
				}
				w.assertSafe()
			})
		}

		t.Run("crossed ID and digest/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, vb := pair(t, a, b, order)
			// alice signs A's ID with B's digest; bob signs B's ID with A's
			// digest.
			w.decide(action.View{ProposalID: va.ProposalID, Digest: vb.Digest}, "alice", keys["alice"], action.Approve, w.now)
			w.decide(action.View{ProposalID: vb.ProposalID, Digest: va.Digest}, "bob", keys["bob"], action.Approve, w.now)
			for i := 0; i < 3; i++ {
				out := w.advance(time.Second)
				for _, id := range []string{va.ProposalID, vb.ProposalID} {
					if v := w.viewOf(out, id); v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonApprovalDigestMismatch) {
						t.Fatalf("%+v", v)
					}
				}
			}
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%+v", w.actor.Handoffs)
			}
		})

		t.Run("undeclared principal/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, vb := pair(t, a, b, order)
			w.decide(va, "mallory", keys["mallory"], action.Approve, w.now)
			w.decide(vb, "mallory", keys["mallory"], action.Approve, w.now)
			for i := 0; i < 3; i++ {
				out := w.advance(time.Second)
				for _, id := range []string{va.ProposalID, vb.ProposalID} {
					if v := w.viewOf(out, id); v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonApproverNotAllowed) {
						t.Fatalf("%+v", v)
					}
				}
			}
			w.assertSafe()
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%+v", w.actor.Handoffs)
			}
		})
	}
}

// inject records and hands off an execution of decl the engine did not
// make, as of a fresh step on current evidence: the observation a faulty
// engine would leave behind. contract is what the record claims.
func (w *world) inject(decl interaction.Response, contract interaction.Execution, id, digest string, ap action.Approval,
	tamper ...func(*action.Record, *action.Handoff)) {
	w.t.Helper()
	w.observe()
	snap := w.snapshot()
	a, err := operations.Evaluate(snap)
	if err != nil {
		w.t.Fatal(err)
	}
	w.history = append(w.history, stepLog{at: w.now, snap: snap, a: a})
	w.pending = nil
	ctx := context.Background()
	fence, err := w.journal.Epoch(ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	y := w.targets["y"]
	var impact []operations.CapabilityType
	for _, c := range contract.ExpectedImpact {
		impact = append(impact, opsT(c))
	}
	rec := action.Record{Key: id + "/1", ProposalID: id, Digest: digest, TargetUID: y.Identity.UID, Attempt: 1,
		Phase: action.PhaseDispatching, Fence: fence, SentFence: fence, Sends: 1, Approval: ap,
		Binding: action.Binding{Target: y.Identity, ResolvedUID: y.ResolvedUID, Contract: y.ContractRef},
		Action:  decl.Action, Provider: contract.Provider, Owner: decl.Owner, For: opsT(decl.For),
		Expected: impact, Execution: contract, CreatedAt: w.now, LastSentAt: w.now, MaxAttempts: 1, MaxSends: 1}
	h := action.Handoff{Key: rec.Key, Fence: fence, ProposalID: id, Digest: digest, Action: decl.Action,
		Owner: decl.Owner, Target: y.Identity, ResolvedUID: y.ResolvedUID, Contract: y.ContractRef,
		For: opsT(decl.For), ExpectedImpact: impact, Approval: ap}
	for _, f := range tamper {
		f(&rec, &h)
	}
	if _, err := w.journal.Put(ctx, rec); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.actor.Submit(ctx, h); err != nil {
		w.t.Fatal(err)
	}
	w.check()
}

// sign adds a signed approval of the view by who, decided at.
func (w *world) sign(v action.View, who string, at time.Time) action.Approval {
	d := action.Decision{ID: "inj-" + who, ProposalID: v.ProposalID, Digest: v.Digest, Principal: who,
		Verdict: action.Approve, DecidedAt: at}
	w.decisions.Add(actiontest.Sign(d, keys[who]))
	return action.Approval{Kind: "human", Principal: who, DecisionID: d.ID}
}

func wantOnlyUnauthorized(t *testing.T, w *world, n int) {
	t.Helper()
	f := w.ck
	t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse)
	if f.unrelated+f.unjustifiedRecovery+f.staleReuse != 0 || f.unauthorized != n {
		t.Fatalf("want unauthorized=%d only: %+v", n, f)
	}
}

// The independent checker maps an observed execution to its exact
// declaration through the recorded contract and applies only that
// declaration's approvers and approval age.
func TestCheckerAppliesTheExecutedDeclarationsApprovers(t *testing.T) {
	for _, order := range orders {
		cases := []struct {
			name string
			run  func(w *world, a, b interaction.Response, va, vb action.View)
			want int
		}{
			{"bob executes A", func(w *world, a, _ interaction.Response, va, _ action.View) {
				w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "bob", w.now))
			}, 1},
			{"mallory executes A", func(w *world, a, _ interaction.Response, va, _ action.View) {
				w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "mallory", w.now))
			}, 1},
			{"control: alice executes A", func(w *world, a, _ interaction.Response, va, _ action.View) {
				w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "alice", w.now))
			}, 0},
			{"control: bob executes B", func(w *world, _, b interaction.Response, _, vb action.View) {
				w.inject(b, *b.Execution, vb.ProposalID, vb.Digest, w.sign(vb, "bob", w.now))
			}, 0},
			// The record claims a contract nobody declared (A's, with bob
			// added to its approvers).
			{"record claims an undeclared contract", func(w *world, a, _ interaction.Response, va, _ action.View) {
				forged := *a.Execution
				forged.Approvers = []string{"alice", "bob"}
				w.inject(a, forged, va.ProposalID, va.Digest, w.sign(va, "bob", w.now))
			}, 1},
		}
		for _, c := range cases {
			t.Run(c.name+"/"+order, func(t *testing.T) {
				a, b := disjoint()
				w, va, vb := pair(t, a, b, order)
				c.run(w, a, b, va, vb)
				wantOnlyUnauthorized(t, w, c.want)
			})
		}

		// The handoff says alice approved; its persisted record says bob.
		t.Run("handoff does not match its record/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, _ := pair(t, a, b, order)
			alice, bob := w.sign(va, "alice", w.now), w.sign(va, "bob", w.now)
			w.inject(a, *a.Execution, va.ProposalID, va.Digest, alice, func(r *action.Record, _ *action.Handoff) { r.Approval = bob })
			wantOnlyUnauthorized(t, w, 1)
		})

		// Persist before send: an execution with no record maps to no
		// declaration, whoever approved it.
		t.Run("handoff without a persisted record/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, _ := pair(t, a, b, order)
			w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "alice", w.now), func(r *action.Record, _ *action.Handoff) {
				r.Key = "unrelated/1" // the handoff's own key has no record
			})
			wantOnlyUnauthorized(t, w, 1)
		})

		// One proposal must map to one contract: a second attempt of A
		// recorded under B's contract (approved by bob, B's approver) is
		// counted as a second execution and as a remapped proposal.
		t.Run("one proposal, two recorded contracts/"+order, func(t *testing.T) {
			a, b := disjoint()
			w, va, _ := pair(t, a, b, order)
			w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "alice", w.now))
			wantOnlyUnauthorized(t, w, 0)
			w.inject(b, *b.Execution, va.ProposalID, va.Digest, w.sign(va, "bob", w.now), func(r *action.Record, h *action.Handoff) {
				r.Key, r.Attempt, h.Key = va.ProposalID+"/2", 2, va.ProposalID+"/2"
			})
			wantOnlyUnauthorized(t, w, 2)
		})

		// alice approves both contracts but only B allows a 3h-old
		// approval: A executed on it borrows B's approval age.
		t.Run("approval age is not borrowed/"+order, func(t *testing.T) {
			a, b := disjoint()
			b.Execution.Approvers = []string{"alice"}
			b.Execution.ApprovalTTL.Duration = 3 * time.Hour
			w, va, _ := pair(t, a, b, order)
			w.inject(a, *a.Execution, va.ProposalID, va.Digest, w.sign(va, "alice", w.now.Add(-2*time.Hour)))
			wantOnlyUnauthorized(t, w, 1)

			w2, _, vb := pair(t, a, b, order)
			w2.inject(b, *b.Execution, vb.ProposalID, vb.Digest, w2.sign(vb, "alice", w2.now.Add(-2*time.Hour)))
			wantOnlyUnauthorized(t, w2, 0) // control: within B's own age
		})

		// One O4 response declared with two execution contracts has no
		// priority authority: a policy dispatch of either is unauthorized.
		t.Run("several execution contracts need a person/"+order, func(t *testing.T) {
			a, b := policyResponse("remount"), policyResponse("remount")
			b.Execution.AckTimeout.Duration = 20 * time.Second
			w, va, _ := pair(t, a, b, order)
			w.inject(a, *a.Execution, va.ProposalID, va.Digest, action.Approval{Kind: "policy", Principal: "policy:p1"})
			wantOnlyUnauthorized(t, w, 1)
		})
	}

	t.Run("control: one execution contract on policy", func(t *testing.T) {
		a := policyResponse("remount")
		id := proposalID(t, a)
		w := newWorld(t) // the engine does not run: only the injected dispatch exists
		w.profile.Responses = []interaction.Response{a}
		w.inject(a, *a.Execution, id, "digest", action.Approval{Kind: "policy", Principal: "policy:p1"})
		wantOnlyUnauthorized(t, w, 0)
	})
}
