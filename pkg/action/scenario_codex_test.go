package action_test

import (
	"context"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

func policyResponse(name string) interaction.Response {
	r := remount()
	r.Action = interaction.ActionRef{Name: name, Revision: "r1"}
	r.RequiresApproval = false
	r.Execution.Approvers = []string{"alice"} // allowed to decide, not required by declaration
	return r
}

// Codex r4234734088: two otherwise-ready responses for the same capability
// have no priority authority (O4 DECISION_REQUIRED). Neither may run on
// policy approval; a person chooses, and only the chosen one runs.
func TestCompetingResponsesNeedAPerson(t *testing.T) {
	w := newWorld(t)
	w.profile.Responses = []interaction.Response{policyResponse("remount"), policyResponse("failover")}
	for i := 0; i < 5; i++ {
		out := w.advance(time.Second)
		for _, v := range out.Views["t-y"] {
			if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonNoPriorityAuthority) {
				t.Fatalf("%+v", v)
			}
		}
	}
	if len(w.actor.Handoffs) != 0 {
		t.Fatalf("ran without a person choosing: %+v", w.actor.Handoffs)
	}
	vs := w.history[len(w.history)-1].out.Views["t-y"]
	w.approve(vs[0])
	w.advance(time.Second)
	w.advance(time.Second)
	if w.actor.TotalExecutions() != 1 || w.actor.Handoffs[0].ProposalID != vs[0].ProposalID {
		t.Fatalf("executions %d", w.actor.TotalExecutions())
	}
	w.assertSafe()

	t.Run("display-only competitor", func(t *testing.T) {
		w := newWorld(t)
		display := policyResponse("inspect")
		display.Execution = nil
		w.profile.Responses = []interaction.Response{policyResponse("remount"), display}
		v := w.view(w.advance(time.Second))
		if v.Phase != action.PhaseAwaitingApproval || !hasReason(v, action.ReasonNoPriorityAuthority) || len(w.actor.Handoffs) != 0 {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})

	t.Run("owner conflict", func(t *testing.T) {
		w := newWorld(t)
		a, b := remount(), remount()
		b.Owner = "platform-oncall"
		w.profile.Responses = []interaction.Response{a, b}
		v := w.view(w.step())
		w.approve(v)
		for i := 0; i < 3; i++ {
			v = w.view(w.advance(time.Second))
		}
		if v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonOwnerConflict) || len(w.actor.Handoffs) != 0 {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
}

// interleave runs hook once, right before the wrapped engine's first write.
type interleave struct {
	action.Journal
	hook func()
}

func (j *interleave) Put(ctx context.Context, r action.Record) (action.Record, error) {
	if h := j.hook; h != nil {
		j.hook = nil
		h()
	}
	return j.Journal.Put(ctx, r)
}

// Codex r4234734092: two overlapping engines both list the journal before
// either writes, each sees the target free and each picks a different
// approved proposal. Only one execution may be in flight for the target.
func TestOverlappingEnginesCannotBothOccupyATarget(t *testing.T) {
	w := newWorld(t)
	w.actor.Default = actiontest.AcceptOnly
	failover := remount()
	failover.Action = interaction.ActionRef{Name: "failover", Revision: "r1"}
	w.profile.Responses = append(w.profile.Responses, failover)
	for _, v := range w.view2(w.step()) {
		w.approve(v)
	}
	shared := w.journal
	old := &action.Engine{Journal: &interleave{Journal: shared}, Providers: w.eng.Providers, Decisions: w.decisions, Verifier: w.signer}
	// The old engine takes its epoch and lists, but has not written yet.
	w.now = w.now.Add(time.Second)
	w.observe()
	s := w.snapshot()
	a, _ := evaluate(s)
	in := action.Input{Snapshot: s, Assessment: a, Profile: w.profile, Now: w.now}
	newer := &action.Engine{Journal: shared, Providers: w.eng.Providers, Decisions: w.decisions, Verifier: w.signer}
	// Swap the order of the two proposals for the newer engine so that the
	// two engines choose different proposals.
	rev := *w.profile
	rev.Responses = []interaction.Response{w.profile.Responses[1], w.profile.Responses[0]}
	old.Journal.(*interleave).hook = func() {
		if _, err := newer.Step(context.Background(), action.Input{Snapshot: s, Assessment: a, Profile: &rev, Now: w.now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Step(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	inflight := map[string]bool{}
	for _, r := range w.records() {
		if r.Phase == action.PhaseDispatching || r.Phase == action.PhaseAccepted {
			inflight[r.ProposalID] = true
		}
	}
	if w.actor.TotalExecutions() > 1 || len(inflight) > 1 {
		t.Fatalf("two actions in flight on one target: executions %d records %+v", w.actor.TotalExecutions(), w.records())
	}
}
