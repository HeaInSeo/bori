package action_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// The recorded decision copy is the journal's own: neither the source nor a
// reader shares its memory, and it is never sent or shown.
func TestRecordedDecisionCopyIsIsolated(t *testing.T) {
	w, va, _ := lostFirstSend(t, "a first", time.Hour)
	ds, _ := w.decisions.Decisions(context.Background())
	var signed []byte
	for _, d := range ds {
		if d.ID == "d-1" {
			signed = append([]byte(nil), d.Proof...)
			d.Proof[0] ^= 0xff // the source's own bytes
		}
	}
	r := w.recordOf(va.ProposalID)
	if r.Decision.ID != "d-1" || r.Decision.Principal != "alice" || r.Decision.ProposalID != va.ProposalID ||
		r.Decision.Digest != va.Digest || !bytes.Equal(r.Decision.Proof, signed) || !w.signer.Verify(r.Decision) {
		t.Fatalf("recorded copy %+v follows the source", r.Decision)
	}
	r.Decision.Proof[0] ^= 0xff // a reader's copy
	if again := w.recordOf(va.ProposalID); !bytes.Equal(again.Decision.Proof, signed) {
		t.Fatal("a reader changed the journal's copy")
	}
	// The source no longer presents the signed decision unchanged.
	w.advance(time.Second)
	w.assertSafe()
	if len(w.actor.Handoffs) != 0 || w.recordOf(va.ProposalID).Phase != action.PhaseWithdrawn {
		t.Fatalf("handoffs %+v", w.actor.Handoffs)
	}
	// Exposure: the handoff and the view carry no decision.
	for _, typ := range []reflect.Type{reflect.TypeOf(action.Handoff{}), reflect.TypeOf(action.View{}), reflect.TypeOf(action.Approval{})} {
		for i := 0; i < typ.NumField(); i++ {
			if f := typ.Field(i); f.Type == reflect.TypeOf(action.Decision{}) || f.Type == reflect.TypeOf([]byte(nil)) {
				t.Fatalf("%s.%s exposes the decision", typ.Name(), f.Name)
			}
		}
	}
}

// Refusal answers that do not match the attempt, or that contradict an
// accepted handoff, release nothing; an executed failure is still an
// execution end assessed on fresh evidence.
func TestRefusalAnswerControls(t *testing.T) {
	for _, order := range orders {
		t.Run("mismatched refusals/"+order, func(t *testing.T) {
			w, va, vb := twoActions(t, order, nil)
			w.actor.Default = actiontest.AcceptOnly
			w.actor.LoseReceipts = 1 // executed and live; BORI has no receipt
			w.approve(va)
			w.advance(time.Second)
			a := w.recordOf(va.ProposalID)
			w.actor.Forge = []action.Result{
				{Key: a.Key + "x", Fence: a.SentFence, Digest: a.Digest, State: action.ResultRefused},
				{Key: a.Key, Fence: a.SentFence + 7, Digest: a.Digest, State: action.ResultRefused},
				{Key: a.Key, Fence: a.SentFence, Digest: "other", State: action.ResultRefused},
			}
			w.approve(vb)
			for i := 0; i < 3; i++ {
				w.advance(time.Second)
			}
			w.assertSafe()
			if r := w.recordOf(va.ProposalID); r.Phase != action.PhaseDispatching || !r.EndedAt.IsZero() || w.actor.TotalExecutions() != 1 {
				t.Fatalf("A %+v executions %d", r, w.actor.TotalExecutions())
			}
		})
		t.Run("refusal after acceptance/"+order, func(t *testing.T) {
			w, va, vb := twoActions(t, order, nil)
			w.actor.Default = actiontest.AcceptOnly
			w.approve(va)
			w.advance(time.Second)
			a := w.recordOf(va.ProposalID)
			if a.Phase != action.PhaseAccepted {
				t.Fatalf("setup %+v", a)
			}
			w.actor.Forge = []action.Result{{Key: a.Key, Fence: a.SentFence, Digest: a.Digest, State: action.ResultRefused}}
			w.approve(vb)
			w.advance(time.Second)
			w.advance(time.Second)
			w.assertSafe()
			if r := w.recordOf(va.ProposalID); r.Phase != action.PhaseAccepted || r.Refused || w.actor.TotalExecutions() != 1 ||
				w.eng.Stats().IgnoredResponses == 0 {
				t.Fatalf("A %+v executions %d", r, w.actor.TotalExecutions())
			}
		})
	}
	t.Run("executed failure is assessed", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.Fail
		v := approved(w)
		w.advance(time.Second)
		r := w.recordOf(v.ProposalID)
		if r.Phase != action.PhaseFailed || r.Refused || r.EndedAt.IsZero() {
			t.Fatalf("setup %+v", r)
		}
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		w.advance(time.Second)
		w.advance(time.Second)
		w.assertSafe()
		if r := w.recordOf(v.ProposalID); r.Recovery != action.RecoveryRecovered {
			t.Fatalf("recovery %s", r.Recovery)
		}
	})
}

// resendInjected hands the persisted attempt of proposalID to the actor
// again, as a broken engine would, and runs the checker over that step.
func (w *world) resendInjected(proposalID string) {
	w.t.Helper()
	w.observe()
	snap := w.snapshot()
	a, err := operations.Evaluate(snap)
	if err != nil {
		w.t.Fatal(err)
	}
	w.history = append(w.history, stepLog{at: w.now, snap: snap, a: a})
	w.pending = nil
	r := w.recordOf(proposalID)
	h := action.Handoff{Key: r.Key, Fence: r.SentFence, ProposalID: r.ProposalID, Digest: r.Digest, Action: r.Action,
		Owner: r.Owner, Target: r.Binding.Target, ResolvedUID: r.Binding.ResolvedUID, Contract: r.Binding.Contract,
		For: r.For, ExpectedImpact: r.Expected, Approval: r.Approval}
	if _, err := w.actor.Submit(context.Background(), h); err != nil {
		w.t.Fatal(err)
	}
	w.check()
}

// The checker judges a resend against the decision as the source presented
// it when the attempt was persisted, and a refusal as no execution.
func TestCheckerSeesRenewalAndRefusal(t *testing.T) {
	for _, renewed := range []bool{true, false} {
		name := "resend under a same-ID renewal of an expired original"
		ttl := 2 * time.Second
		if !renewed {
			name, ttl = "control: resend of a still valid original", time.Hour
		}
		t.Run(name, func(t *testing.T) {
			w, va, _ := lostFirstSend(t, "a first", ttl)
			if renewed {
				w.decision("d-1", va, "alice", "", action.Approve, w.now, nil)
			}
			w.resendInjected(va.ProposalID)
			want := 0
			if renewed {
				want = 1
			}
			wantOnly(t, w, 0, want)
		})
	}
	for _, refused := range []bool{true, false} {
		name := "recovered after a refusal"
		if !refused {
			name = "control: recovered after an executed failure"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.Default = actiontest.Fail
			if refused {
				w.actor.Default = actiontest.Refuse
			}
			v := approved(w)
			w.advance(time.Second)
			w.truth["y"]["storage-writable"] = operations.Bool(true)
			w.now = w.now.Add(time.Second)
			w.injectView(recoveredView(w.recordOf(v.ProposalID)))
			want := 0
			if refused {
				want = 1
			}
			wantOnly(t, w, want, 0)
		})
	}
}
