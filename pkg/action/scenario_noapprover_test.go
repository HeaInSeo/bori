package action_test

import (
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// Codex r4241955183: a response that needs no person by its own declaration
// can acquire a derived human boundary (no-priority-authority: several
// viable responses, or several execution contracts of one response). With
// no approver declared no decision can ever count, so the proposal is
// blocked with no-approver instead of waiting for an approval that cannot
// exist, and nothing is handed off.

// unapproved is a policy response (no declared human reason) without
// approvers.
func unapproved(name string) interaction.Response {
	r := policyResponse(name)
	r.Execution.Approvers = nil
	return r
}

// derivedBoundaries are the two ways a policy response acquires a derived
// human boundary.
var derivedBoundaries = map[string]func() (a, b interaction.Response){
	"several viable responses": func() (interaction.Response, interaction.Response) {
		return unapproved("remount"), unapproved("failover")
	},
	"several execution contracts": func() (interaction.Response, interaction.Response) {
		a, b := unapproved("remount"), unapproved("remount")
		b.Execution.AckTimeout.Duration = 20 * time.Second
		return a, b
	},
}

func profileOf(order string, a, b interaction.Response) []interaction.Response {
	if order == "b first" {
		return []interaction.Response{b, a}
	}
	return []interaction.Response{a, b}
}

func TestDerivedBoundaryWithoutApproverIsBlocked(t *testing.T) {
	for name, mk := range derivedBoundaries {
		for _, order := range orders {
			t.Run(name+"/"+order, func(t *testing.T) {
				w := newWorld(t)
				a, b := mk()
				w.profile.Responses = profileOf(order, a, b)
				vs := w.view2(w.step())
				if len(vs) != 2 {
					t.Fatalf("%+v", vs)
				}
				// Decisions by a principal of the deployment change nothing:
				// no declared approver means none can count.
				for _, v := range vs {
					w.approve(v)
				}
				for i := 0; i < 3; i++ {
					vs = w.view2(w.advance(time.Second))
				}
				w.assertSafe()
				if len(w.actor.Handoffs) != 0 {
					t.Fatalf("handed off %+v", w.actor.Handoffs)
				}
				for _, v := range vs {
					if v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonNoApprover) ||
						!hasReason(v, action.ReasonNoPriorityAuthority) {
						t.Fatalf("%+v, want Blocked with no-approver and no-priority-authority", v)
					}
				}
			})
		}
	}
}

// Controls: a standalone policy response still runs by policy; a derived
// boundary with declared approvers waits for and honours the approver; when
// only one competitor names an approver, that one is approvable and the
// other is blocked.
func TestNoApproverGateControls(t *testing.T) {
	t.Run("standalone policy response without approvers", func(t *testing.T) {
		w := newWorld(t)
		w.profile.Responses = []interaction.Response{unapproved("remount")}
		w.step()
		w.advance(time.Second)
		w.assertSafe()
		if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].Approval.Kind != "policy" {
			t.Fatalf("handoffs %+v", w.actor.Handoffs)
		}
	})
	for name, mk := range derivedBoundaries {
		for _, order := range orders {
			t.Run("approvers declared/"+name+"/"+order, func(t *testing.T) {
				w := newWorld(t)
				a, b := mk()
				a.Execution.Approvers, b.Execution.Approvers = []string{"alice"}, []string{"alice"}
				w.profile.Responses = profileOf(order, a, b)
				vs := w.view2(w.step())
				for _, v := range vs {
					if v.Phase != action.PhaseAwaitingApproval || hasReason(v, action.ReasonNoApprover) {
						t.Fatalf("%+v", v)
					}
				}
				w.approve(vs[0])
				w.advance(time.Second)
				w.advance(time.Second)
				w.assertSafe()
				if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].ProposalID != vs[0].ProposalID ||
					w.actor.Handoffs[0].Approval.Principal != "alice" {
					t.Fatalf("handoffs %+v", w.actor.Handoffs)
				}
			})
		}
	}
	for _, order := range orders {
		t.Run("only one competitor has approvers/"+order, func(t *testing.T) {
			a, b := unapproved("remount"), unapproved("failover")
			a.Execution.Approvers = []string{"alice"}
			w, va, vb := pair(t, a, b, order)
			if va.Phase != action.PhaseAwaitingApproval || vb.Phase != action.PhaseBlocked || !hasReason(vb, action.ReasonNoApprover) {
				t.Fatalf("A %+v B %+v", va, vb)
			}
			w.approve(va)
			w.approve(vb)
			w.advance(time.Second)
			w.advance(time.Second)
			w.assertSafe()
			if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].ProposalID != va.ProposalID {
				t.Fatalf("handoffs %+v", w.actor.Handoffs)
			}
		})
	}
}

// The checker independently counts a dispatch under a derived boundary
// without approvers as unauthorized, whatever approval it carries.
func TestCheckerSeesDispatchWithoutApprover(t *testing.T) {
	for name, mk := range derivedBoundaries {
		for _, order := range orders {
			for _, ap := range []string{"policy", "alice"} {
				t.Run(name+"/"+order+"/"+ap, func(t *testing.T) {
					a, b := mk()
					w, va, _ := pair(t, a, b, order)
					approval := action.Approval{Kind: "policy", Principal: "policy:p1"}
					if ap == "alice" {
						approval = w.sign(va, "alice", w.now)
					}
					w.inject(a, *a.Execution, va.ProposalID, va.Digest, approval)
					wantOnlyUnauthorized(t, w, 1)
				})
			}
		}
	}
}
