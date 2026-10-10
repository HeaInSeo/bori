package action_test

import (
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// o4 projects y's O4 interaction summary for the world's current evidence
// and profile: the display the O5 engine must not override.
func (w *world) o4() *opsv1.InteractionStatus {
	w.t.Helper()
	w.observe()
	a, err := operations.Evaluate(w.snapshot())
	if err != nil {
		w.t.Fatal(err)
	}
	y := w.targets["y"].Identity
	ta, _ := a.Target(y.UID)
	st := opsv1.OperationalTargetStatus{Valid: ta.Valid}
	for _, c := range ta.Capabilities {
		st.Capabilities = append(st.Capabilities, opsv1.CapabilityStatus{
			Type: opsv1.CapabilityType{Domain: c.Type.Domain, Name: c.Type.Name, Revision: c.Type.Revision}, State: string(c.State)})
	}
	return interaction.Project(interaction.Input{Namespace: y.Namespace, Name: y.Name, UID: y.UID, Status: st, Profile: w.profile}, nil)
}

func humanCodes(s *opsv1.InteractionStatus) map[string]bool {
	out := map[string]bool{}
	for _, r := range s.HumanReasons {
		out[r.Code] = true
	}
	return out
}

// boundaryVariants are second declarations of the same action, owner and
// capability as the ready (policy) remount, each differing in exactly one
// field of the declared human boundary. O4 keeps both (DECISION_REQUIRED,
// no-priority-authority).
var boundaryVariants = map[string]func(r *interaction.Response){
	"requires approval": func(r *interaction.Response) { r.RequiresApproval = true },
	"risks":             func(r *interaction.Response) { r.Risks = []string{"data-loss"} },
	"preconditions":     func(r *interaction.Response) { r.Preconditions = append(r.Preconditions, opsCap("ready")) },
}

// Guardrail P1-1 (9505b98): the same action declared twice for the same
// capability and owner, once without and once with a human boundary. O4
// shows DECISION_REQUIRED/no-priority-authority; O5 must not dispatch on
// policy in either declaration order, and must agree with O4's reasons.
func TestSameActionDifferentBoundaryNeedsAPerson(t *testing.T) {
	for name, vary := range boundaryVariants {
		for _, order := range []string{"ready first", "boundary first"} {
			t.Run(name+"/"+order, func(t *testing.T) {
				w := newWorld(t)
				ready, other := policyResponse("remount"), policyResponse("remount")
				vary(&other)
				w.profile.Responses = []interaction.Response{ready, other}
				if order == "boundary first" {
					w.profile.Responses = []interaction.Response{other, ready}
				}
				s := w.o4()
				if s.Level != opsv1.LevelDecisionRequired || !humanCodes(s)[interaction.ReasonNoPriorityAuthority] || len(s.Responses) != 2 {
					t.Fatalf("O4 premise: %s %+v responses %d", s.Level, s.HumanReasons, len(s.Responses))
				}
				var vs []action.View
				for i := 0; i < 5; i++ {
					vs = w.view2(w.advance(time.Second))
					if len(vs) != 2 {
						t.Fatalf("a differing declaration was dropped: %+v", vs)
					}
					for _, v := range vs {
						if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonNoPriorityAuthority) {
							t.Fatalf("%+v", v)
						}
					}
				}
				if len(w.actor.Handoffs) != 0 {
					t.Fatalf("dispatched on policy despite O4 DECISION_REQUIRED: %+v", w.actor.Handoffs)
				}
				w.assertSafe()
				// A person chooses one; only that one runs.
				w.approve(vs[1])
				w.advance(time.Second)
				w.advance(time.Second)
				if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].ProposalID != vs[1].ProposalID {
					t.Fatalf("executions %d %+v", w.actor.TotalExecutions(), w.actor.Handoffs)
				}
				w.assertSafe()
			})
		}
	}
}

// The proposals and their human reasons do not depend on declaration order.
func TestBoundaryIsDeclarationOrderIndependent(t *testing.T) {
	for name, vary := range boundaryVariants {
		t.Run(name, func(t *testing.T) {
			views := map[string]map[string]string{} // order → proposal → digest+reasons
			for _, order := range []string{"ready first", "boundary first"} {
				w := newWorld(t)
				ready, other := policyResponse("remount"), policyResponse("remount")
				vary(&other)
				w.profile.Responses = []interaction.Response{ready, other}
				if order == "boundary first" {
					w.profile.Responses = []interaction.Response{other, ready}
				}
				m := map[string]string{}
				for _, v := range w.view2(w.step()) {
					k := v.Digest + string(v.Phase)
					for _, r := range v.Reasons {
						k += "|" + r.Code
					}
					m[v.ProposalID] = k
				}
				w.advance(time.Second)
				w.assertSafe()
				views[order] = m
			}
			a, b := views["ready first"], views["boundary first"]
			if len(a) != 2 || len(a) != len(b) {
				t.Fatalf("%v vs %v", a, b)
			}
			for id, k := range a {
				if b[id] != k {
					t.Fatalf("proposal %s differs by order: %q vs %q", id, k, b[id])
				}
			}
		})
	}
}

// Controls on the same boundary: O4's verdict is reproduced, and nothing
// stricter than O4 is lost nor anything looser gained.
func TestSameActionBoundaryControls(t *testing.T) {
	t.Run("exact duplicate is one policy proposal", func(t *testing.T) {
		w := newWorld(t)
		w.profile.Responses = []interaction.Response{policyResponse("remount"), policyResponse("remount")}
		if s := w.o4(); s.Level != opsv1.LevelAwareness || len(s.Responses) != 1 {
			t.Fatalf("O4 premise: %s %d", s.Level, len(s.Responses))
		}
		w.view(w.step())
		w.advance(time.Second)
		if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].Approval.Kind != "policy" {
			t.Fatalf("executions %d", w.actor.TotalExecutions())
		}
		w.assertSafe()
	})

	// P3-1: a competing declaration whose precondition is unmet is
	// Inapplicable in O4 and is not a competitor; the viable one keeps its
	// own boundary. Same action and different action, both orders.
	for _, competitor := range []string{"remount", "failover"} {
		for _, order := range []string{"viable first", "inapplicable first"} {
			t.Run("unmet-precondition competitor "+competitor+"/"+order, func(t *testing.T) {
				w := newWorld(t)
				w.truth["y"]["ready-replicas"] = operations.Int(0) // y.ready UNAVAILABLE
				viable, blocked := policyResponse("remount"), policyResponse(competitor)
				blocked.RequiresApproval = true
				blocked.Preconditions = append(blocked.Preconditions, opsCap("ready"))
				w.profile.Responses = []interaction.Response{viable, blocked}
				if order == "inapplicable first" {
					w.profile.Responses = []interaction.Response{blocked, viable}
				}
				s := w.o4()
				if humanCodes(s)[interaction.ReasonNoPriorityAuthority] || len(s.Responses) != 2 {
					t.Fatalf("O4 premise: %s %+v", s.Level, s.HumanReasons)
				}
				vs := w.view2(w.step())
				if len(vs) != 2 {
					t.Fatalf("%+v", vs)
				}
				for _, v := range vs {
					if hasReason(v, action.ReasonNoPriorityAuthority) {
						t.Fatalf("inapplicable declaration counted as a competitor: %+v", v)
					}
					if hasReason(v, action.ReasonPreconditionUnmet) != (v.Phase == action.PhaseBlocked) {
						t.Fatalf("%+v", v)
					}
				}
				w.advance(time.Second)
				if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].Approval.Kind != "policy" {
					t.Fatalf("viable declaration: executions %d", w.actor.TotalExecutions())
				}
				w.assertSafe()
			})
		}
	}

	t.Run("same-action display-only declaration with a boundary", func(t *testing.T) {
		for _, order := range []string{"executable first", "display first"} {
			w := newWorld(t)
			exe, display := policyResponse("remount"), policyResponse("remount")
			display.Execution = nil
			display.RequiresApproval = true
			w.profile.Responses = []interaction.Response{exe, display}
			if order == "display first" {
				w.profile.Responses = []interaction.Response{display, exe}
			}
			if s := w.o4(); !humanCodes(s)[interaction.ReasonNoPriorityAuthority] {
				t.Fatalf("O4 premise %+v", s.HumanReasons)
			}
			for i := 0; i < 3; i++ {
				v := w.view(w.advance(time.Second))
				if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonNoPriorityAuthority) {
					t.Fatalf("%s: %+v", order, v)
				}
			}
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%s: %+v", order, w.actor.Handoffs)
			}
			w.assertSafe()
		}
	})

	t.Run("same-action display-only identical declaration", func(t *testing.T) {
		w := newWorld(t)
		display := policyResponse("remount")
		display.Execution = nil
		w.profile.Responses = []interaction.Response{display, policyResponse("remount")}
		if s := w.o4(); s.Level != opsv1.LevelAwareness || len(s.Responses) != 1 {
			t.Fatalf("O4 premise: %s %d", s.Level, len(s.Responses))
		}
		w.step()
		w.advance(time.Second)
		if w.actor.TotalExecutions() != 1 {
			t.Fatalf("executions %d", w.actor.TotalExecutions())
		}
		w.assertSafe()
	})

	// Two execution contracts for one O4 response: which one runs has no
	// declared priority, so a person decides (stricter than O4's display).
	t.Run("same declaration with different execution contracts", func(t *testing.T) {
		for _, order := range []string{"a first", "b first"} {
			w := newWorld(t)
			a, b := policyResponse("remount"), policyResponse("remount")
			b.Execution.AckTimeout.Duration = 20 * time.Second
			w.profile.Responses = []interaction.Response{a, b}
			if order == "b first" {
				w.profile.Responses = []interaction.Response{b, a}
			}
			vs := w.view2(w.advance(time.Second))
			if len(vs) != 2 {
				t.Fatalf("%+v", vs)
			}
			for _, v := range vs {
				if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonNoPriorityAuthority) {
					t.Fatalf("%s: %+v", order, v)
				}
			}
			w.advance(time.Second)
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%s: %+v", order, w.actor.Handoffs)
			}
			w.assertSafe()
		}
	})

	t.Run("owner conflict in both orders", func(t *testing.T) {
		for _, order := range []string{"a first", "b first"} {
			w := newWorld(t)
			a, b := policyResponse("remount"), policyResponse("remount")
			b.Owner = "platform-oncall"
			b.RequiresApproval = true
			w.profile.Responses = []interaction.Response{a, b}
			if order == "b first" {
				w.profile.Responses = []interaction.Response{b, a}
			}
			for _, v := range w.view2(w.step()) {
				w.approve(v)
			}
			for i := 0; i < 3; i++ {
				for _, v := range w.view2(w.advance(time.Second)) {
					if v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonOwnerConflict) {
						t.Fatalf("%s: %+v", order, v)
					}
				}
			}
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%s: %+v", order, w.actor.Handoffs)
			}
			w.assertSafe()
		}
	})

	// O4 counts owners over every triggered declaration, applicable or not:
	// an inapplicable declaration under another owner still makes the
	// action's authority ambiguous.
	t.Run("owner conflict with an inapplicable declaration", func(t *testing.T) {
		for _, order := range []string{"a first", "b first"} {
			w := newWorld(t)
			w.truth["y"]["ready-replicas"] = operations.Int(0) // y.ready UNAVAILABLE
			a, b := policyResponse("remount"), policyResponse("remount")
			b.Owner = "platform-oncall"
			b.Preconditions = append(b.Preconditions, opsCap("ready"))
			w.profile.Responses = []interaction.Response{a, b}
			if order == "b first" {
				w.profile.Responses = []interaction.Response{b, a}
			}
			conflict := false
			for _, r := range w.o4().Responses {
				conflict = conflict || (r.Owner == a.Owner && r.Status == interaction.ResponseOwnerConflict)
			}
			if !conflict {
				t.Fatalf("O4 premise: %+v", w.o4().Responses)
			}
			for i := 0; i < 3; i++ {
				for _, v := range w.view2(w.advance(time.Second)) {
					if v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonOwnerConflict) {
						t.Fatalf("%s: %+v", order, v)
					}
				}
			}
			if len(w.actor.Handoffs) != 0 {
				t.Fatalf("%s: %+v", order, w.actor.Handoffs)
			}
			w.assertSafe()
		}
	})

	t.Run("capability available", func(t *testing.T) {
		w := newWorld(t)
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		other := policyResponse("remount")
		other.RequiresApproval = true
		w.profile.Responses = []interaction.Response{policyResponse("remount"), other}
		for i := 0; i < 3; i++ {
			if vs := w.view2(w.advance(time.Second)); len(vs) != 0 {
				t.Fatalf("%+v", vs)
			}
		}
		if len(w.actor.Handoffs) != 0 {
			t.Fatal("executed for an available capability")
		}
		w.assertSafe()
	})
}

// The independent checker flags the 9505b98 counterexample (a policy
// dispatch while a same-action declaration with a human boundary is also
// viable) as an unauthorized execution, whatever the engine derived.
func TestCheckerDetectsSameActionPolicyDispatch(t *testing.T) {
	for name, vary := range boundaryVariants {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			ready, other := policyResponse("remount"), policyResponse("remount")
			vary(&other)
			w.profile.Responses = []interaction.Response{ready, other} // the order 9505b98 dispatched
			// A step whose only execution is the injected policy dispatch of
			// the ready declaration, persisted under its own contract:
			// evidence and O1 are current, the engine does not run.
			w.inject(ready, *ready.Execution, "p-ready", "d-ready", action.Approval{Kind: "policy", Principal: "policy:p1"})
			if w.ck.unrelated+w.ck.unjustifiedRecovery+w.ck.staleReuse != 0 {
				t.Fatalf("control is not isolated: %+v", w.ck)
			}
			if w.ck.unauthorized == 0 {
				t.Fatalf("checker missed the policy dispatch: %+v", w.ck)
			}
		})
	}
}
