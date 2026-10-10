package action_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
)

// Scenario 9: journal write failures, crashes around the send, restarts,
// retries and duplicate or mismatched responses never produce a duplicate
// execution or a wrong result linkage.
func TestFaultsNeverDuplicateOrMislink(t *testing.T) {
	t.Run("journal write fails before send", func(t *testing.T) {
		w := newWorld(t)
		fj := &actiontest.FaultJournal{Journal: w.journal, FailPuts: 1}
		w.journal = fj
		w.restart()
		v := w.view(w.step())
		w.approve(v)
		v = w.view(w.advance(time.Second))
		if v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonJournalWriteFailed) || len(w.actor.Handoffs) != 0 {
			t.Fatalf("%+v handoffs %d", v, len(w.actor.Handoffs))
		}
		if v = w.view(w.advance(time.Second)); v.Phase != action.PhaseAccepted || w.actor.TotalExecutions() != 1 {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})

	t.Run("crash after persist, before send", func(t *testing.T) {
		w := newWorld(t)
		w.actor.DropSubmits = 1
		if v := approved(w); v.Phase != action.PhaseDispatching {
			t.Fatalf("%+v", v)
		}
		w.restart()
		v := w.view(w.advance(4 * time.Second))
		if v.Phase != action.PhaseAccepted || w.actor.TotalExecutions() != 1 {
			t.Fatalf("%+v executions %d", v, w.actor.TotalExecutions())
		}
		h := w.actor.Handoffs[0]
		if h.Fence != 2 || h.Key != w.records()[0].Key {
			t.Fatalf("resend %+v", h)
		}
		w.assertSafe()
	})

	t.Run("crash after send, before the receipt is persisted", func(t *testing.T) {
		w := newWorld(t)
		fj := &actiontest.FaultJournal{Journal: w.journal, FailPhase: action.PhaseAccepted}
		w.journal = fj
		w.restart()
		if v := approved(w); v.Phase != action.PhaseDispatching || w.actor.TotalExecutions() != 1 {
			t.Fatalf("%+v", v)
		}
		fj.FailPhase = ""
		w.restart()
		v := w.view(w.advance(4 * time.Second))
		if v.Phase != action.PhaseSucceeded || len(w.actor.Handoffs) != 1 || w.actor.TotalExecutions() != 1 {
			t.Fatalf("%+v handoffs %d executions %d", v, len(w.actor.Handoffs), w.actor.TotalExecutions())
		}
		if st := w.eng.Stats(); st.Adopted != 1 || st.Submits != 0 {
			t.Fatalf("restart resent instead of polling by key: %+v", st)
		}
		w.assertSafe()
	})

	t.Run("receipt lost on the wire", func(t *testing.T) {
		w := newWorld(t)
		w.actor.LoseReceipts = 1
		approved(w)
		v := w.view(w.advance(time.Second))
		if v.Phase != action.PhaseSucceeded || w.actor.TotalExecutions() != 1 || len(w.actor.Handoffs) != 1 {
			t.Fatalf("%+v executions %d", v, w.actor.TotalExecutions())
		}
		w.assertSafe()
	})

	t.Run("duplicate and mismatched results", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.AcceptOnly
		approved(w)
		rec := w.records()[0]
		w.actor.Forge = []action.Result{
			{Key: rec.Key + "x", Fence: rec.SentFence, Digest: rec.Digest, State: action.ResultSucceeded},
			{Key: rec.Key, Fence: rec.SentFence + 7, Digest: rec.Digest, State: action.ResultSucceeded},
			{Key: rec.Key, Fence: rec.SentFence, Digest: "other", State: action.ResultFailed},
		}
		for i := 0; i < 3; i++ {
			if v := w.view(w.advance(time.Second)); v.Phase != action.PhaseAccepted {
				t.Fatalf("mismatched result adopted: %+v", v)
			}
		}
		if st := w.eng.Stats(); st.IgnoredResponses != 3 {
			t.Fatalf("%+v", st)
		}
		w.actor.Settle(rec.Key, actiontest.Succeed)
		w.advance(time.Second)
		version := w.records()[0].Version
		for i := 0; i < 5; i++ {
			w.advance(time.Second) // the same result answered again and again
		}
		r := w.records()[0]
		if r.Phase != action.PhaseSucceeded || r.Version != version || w.eng.Stats().Adopted != 1 {
			t.Fatalf("duplicate result rewrote the record: %+v", r)
		}
		w.assertSafe()
	})

	t.Run("stale engine after takeover, record still unacknowledged", func(t *testing.T) {
		w := newWorld(t)
		w.actor.DropSubmits = 2 // the first send and the new owner's resend are both lost
		approved(w)
		old := w.eng
		w.restart()
		w.advance(4 * time.Second)
		if r := w.records()[0]; r.Phase != action.PhaseDispatching || r.Fence != 2 {
			t.Fatalf("takeover %+v", r)
		}
		before, st := len(w.actor.Handoffs), old.Stats()
		w.eng = old
		w.advance(4 * time.Second) // due for a resend: only the owner may send
		if len(w.actor.Handoffs) != before || old.Stats().Submits != st.Submits {
			t.Fatalf("stale engine resent: handoffs %d→%d", before, len(w.actor.Handoffs))
		}
		w.assertSafe()
	})

	t.Run("stale engine after takeover", func(t *testing.T) {
		w := newWorld(t)
		w.actor.DropSubmits = 1
		approved(w)
		old := w.eng
		w.restart()
		w.advance(4 * time.Second) // the new engine takes over and resends
		before := len(w.actor.Handoffs)
		w.eng = old
		w.advance(4 * time.Second) // the old engine must not act
		if len(w.actor.Handoffs) != before || w.actor.TotalExecutions() != 1 {
			t.Fatalf("stale engine sent: handoffs %d→%d", before, len(w.actor.Handoffs))
		}
		if _, err := w.actor.Submit(context.Background(), action.Handoff{Key: "x/1", Fence: 1}); !errors.Is(err, actiontest.ErrStaleFence) {
			t.Fatalf("stale fence accepted: %v", err)
		}
		w.assertSafe()
	})

	t.Run("retry only after a definitive failure, within budget", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.Fail
		w.profile.Responses[0].Execution.Retryable = true
		w.profile.Responses[0].Execution.MaxAttempts = 2
		approved(w)
		for i := 0; i < 10; i++ {
			w.advance(10 * time.Second)
		}
		keys := w.actor.ExecutedKeys()
		if len(keys) != 2 || w.actor.TotalExecutions() != 2 || keys[0] == keys[1] {
			t.Fatalf("executed %v", keys)
		}
		v := w.view(w.history[len(w.history)-1].out)
		if v.Phase != action.PhaseFailed || v.Attempt != 2 || !hasReason(v, action.ReasonAttemptsExhausted) {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})

	t.Run("restart at every step", func(t *testing.T) {
		w := newWorld(t)
		w.effect = fixPersist(w)
		v := w.view(w.step())
		w.approve(v)
		for i := 0; i < 10; i++ {
			w.restart()
			v = w.view(w.advance(2 * time.Second))
		}
		if v.Recovery != action.RecoveryRecovered || w.actor.TotalExecutions() != 1 {
			t.Fatalf("%+v executions %d", v, w.actor.TotalExecutions())
		}
		w.assertSafe()
	})
}
