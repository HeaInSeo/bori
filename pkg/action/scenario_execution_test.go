package action_test

import (
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// approved runs the world to a valid approval and the handoff step.
func approved(w *world) action.View {
	w.t.Helper()
	v := w.view(w.step())
	w.approve(v)
	return w.view(w.advance(time.Second))
}

// Scenario 3: an undeclared owner, an unregistered or unauthorised provider,
// and an unproven or unmet precondition each block execution even with a
// valid approval.
func TestExecutionGatesBlockEvenWhenApproved(t *testing.T) {
	cases := map[string]struct {
		mutate func(w *world)
		reason string
	}{
		"owner undeclared": {func(w *world) { w.profile.Responses[0].Owner = "" }, action.ReasonOwnerUndeclared},
		"provider unregistered": {func(w *world) { w.profile.Responses[0].Execution.Provider = "nobody" },
			action.ReasonProviderUnregistered},
		"provider acts for another owner": {func(w *world) {
			reg := w.eng.Providers["actor"]
			reg.Owner = "platform-oncall"
			w.eng.Providers["actor"] = reg
		}, action.ReasonAuthorityInsufficient},
		"provider not authorised for the namespace": {func(w *world) {
			reg := w.eng.Providers["actor"]
			reg.Namespaces = []string{"other"}
			w.eng.Providers["actor"] = reg
		}, action.ReasonAuthorityInsufficient},
		"provider not authorised for the action": {func(w *world) {
			reg := w.eng.Providers["actor"]
			reg.Actions = []string{"remount@r2"}
			w.eng.Providers["actor"] = reg
		}, action.ReasonAuthorityInsufficient},
		"precondition unmet": {func(w *world) { w.truth["y"]["api-serving"] = operations.Bool(false) }, action.ReasonPreconditionUnmet},
		"precondition unproven (stale evidence)": {func(w *world) {
			w.observe()
			w.frozen[okey("y", "api-serving")] = true
			w.now = w.now.Add(maxAge + time.Second)
		}, action.ReasonPreconditionUnproven},
		"journal not durable": {func(w *world) { w.journal = action.NewMemJournal(); w.restart() }, action.ReasonJournalNotDurable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			c.mutate(w)
			v := w.view(w.step())
			w.approve(v)
			for i := 0; i < 3; i++ {
				v = w.view(w.advance(time.Second))
			}
			if v.Phase != action.PhaseBlocked || !hasReason(v, c.reason) {
				t.Fatalf("%+v, want Blocked with %s", v, c.reason)
			}
			if len(w.actor.Handoffs) != 0 || len(w.records()) != 0 {
				t.Fatalf("handoffs %d records %d", len(w.actor.Handoffs), len(w.records()))
			}
			w.assertSafe()
		})
	}
}

// Scenario 4: a receipt is acceptance only, and a missing acknowledgement or
// result ends as TimedOut, never as success.
func TestAcceptedOrTimedOutIsNotSuccess(t *testing.T) {
	t.Run("accepted, no result", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.AcceptOnly
		v := approved(w)
		for i := 0; i < 5; i++ {
			if v = w.view(w.advance(10 * time.Second)); v.Phase != action.PhaseAccepted {
				break
			}
			if v.Recovery != action.RecoveryNone {
				t.Fatalf("recovery assessed while only accepted: %+v", v)
			}
		}
		v = w.view(w.advance(30 * time.Second))
		if v.Phase != action.PhaseTimedOut || !hasReason(v, action.ReasonOutcomeUnknown) {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
	t.Run("no acknowledgement", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.Silent
		v := approved(w)
		if v.Phase != action.PhaseDispatching {
			t.Fatalf("%+v", v)
		}
		for i := 0; i < 12 && v.Phase == action.PhaseDispatching; i++ {
			v = w.view(w.advance(time.Second))
		}
		if v.Phase != action.PhaseTimedOut {
			t.Fatalf("%+v", v)
		}
		if st := w.eng.Stats(); st.Resends > 2 || st.SendFailures != 3 {
			t.Fatalf("sends beyond budget: %+v", st)
		}
		for i := 0; i < 10; i++ {
			v = w.view(w.advance(10 * time.Second))
		}
		// Three sends with one key (the send budget); nothing executed.
		if v.Phase != action.PhaseTimedOut || v.Recovery == action.RecoveryRecovered ||
			len(w.actor.Handoffs) != 3 || w.actor.TotalExecutions() != 0 {
			t.Fatalf("%+v handoffs %d executions %d", v, len(w.actor.Handoffs), w.actor.TotalExecutions())
		}
		for _, h := range w.actor.Handoffs {
			if h.Key != w.actor.Handoffs[0].Key {
				t.Fatalf("resend changed the idempotency key: %s", h.Key)
			}
		}
		w.assertSafe()
	})
}

// Scenario 5: execution failure, success without recovery and partial
// recovery are three distinct outcomes.
func TestFailureNonRecoveryAndPartialAreDistinct(t *testing.T) {
	t.Run("execution failed", func(t *testing.T) {
		w := newWorld(t)
		w.actor.Default = actiontest.Fail
		approved(w)
		v := w.view(w.advance(time.Second))
		if v.Phase != action.PhaseFailed || !hasReason(v, action.ReasonAttemptsExhausted) {
			t.Fatalf("%+v", v)
		}
		v = w.view(w.advance(70 * time.Second))
		if v.Phase != action.PhaseFailed || v.Recovery != action.RecoveryNotRecovered {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
	t.Run("succeeded, not recovered", func(t *testing.T) {
		w := newWorld(t)
		approved(w)
		v := w.view(w.advance(time.Second))
		if v.Phase != action.PhaseSucceeded || v.Recovery != action.RecoveryPending {
			t.Fatalf("%+v", v)
		}
		v = w.view(w.advance(70 * time.Second))
		if v.Phase != action.PhaseSucceeded || v.Recovery != action.RecoveryNotRecovered || !hasReason(v, action.ReasonExecutedNotRecovered) {
			t.Fatalf("%+v", v)
		}
		for i := 0; i < 5; i++ {
			w.advance(10 * time.Second)
		}
		if w.actor.TotalExecutions() != 1 {
			t.Fatalf("re-executed after non-recovery: %d", w.actor.TotalExecutions())
		}
		w.assertSafe()
	})
	t.Run("partial", func(t *testing.T) {
		w := newWorld(t)
		w.truth["y"]["ready-replicas"] = operations.Int(0) // y.ready UNAVAILABLE too
		w.profile.Responses[0].Execution.ExpectedImpact = []opsv1.CapabilityType{opsCap("persist"), opsCap("ready")}
		w.effect = fixPersist(w) // restores persist only
		approved(w)
		w.advance(time.Second)
		v := w.view(w.advance(time.Second))
		if v.Recovery != action.RecoveryPending {
			t.Fatalf("%+v", v)
		}
		v = w.view(w.advance(70 * time.Second))
		if v.Recovery != action.RecoveryPartial {
			t.Fatalf("%+v", v)
		}
		got := map[string]operations.CapabilityState{}
		for _, d := range v.Detail {
			got[d.Type.Name] = d.State
		}
		if got["persist"] != operations.Available || got["ready"] != operations.Unavailable {
			t.Fatalf("detail %+v", v.Detail)
		}
		w.assertSafe()
	})
}

// Scenario 6: the workload becomes Ready (rollout succeeded, the actor
// reports success) while the business capability is still failed: no
// recovery is declared.
func TestPodReadyIsNotCapabilityRecovery(t *testing.T) {
	w := newWorld(t)
	w.truth["y"]["ready-replicas"] = operations.Int(0)
	w.effect = func(action.Handoff) { w.truth["y"]["ready-replicas"] = operations.Int(3) } // Pods Ready, storage still broken
	approved(w)
	w.advance(time.Second)
	var v action.View
	for i := 0; i < 8; i++ {
		v = w.view(w.advance(10 * time.Second))
		if v.Recovery == action.RecoveryRecovered {
			t.Fatalf("recovery declared from readiness: %+v", v)
		}
	}
	ta, _ := w.history[len(w.history)-1].a.Target("t-y")
	ready, _ := ta.Capability("ready")
	persist, _ := ta.Capability("persist")
	if ready.State != operations.Available || persist.State != operations.Unavailable {
		t.Fatalf("world: ready %s persist %s", ready.State, persist.State)
	}
	if v.Phase != action.PhaseSucceeded || v.Recovery != action.RecoveryNotRecovered {
		t.Fatalf("%+v", v)
	}
	w.assertSafe()
}

// Scenario 7: only new, applicable evidence confirms recovery; held or
// stale evidence never does, and the actor's success is never evidence.
func TestOnlyFreshApplicableEvidenceConfirmsRecovery(t *testing.T) {
	t.Run("provider silent after execution", func(t *testing.T) {
		w := newWorld(t)
		w.effect = func(action.Handoff) {
			w.truth["y"]["storage-writable"] = operations.Bool(true)
			w.frozen[okey("y", "storage-writable")] = true // no new observation
		}
		approved(w)
		var v action.View
		for i := 0; i < 5; i++ {
			v = w.view(w.advance(10 * time.Second))
			if v.Recovery == action.RecoveryRecovered {
				t.Fatalf("%+v", v)
			}
		}
		v = w.view(w.advance(30 * time.Second))
		if v.Recovery != action.RecoveryUnconfirmed {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
	t.Run("provider resumes within the window", func(t *testing.T) {
		w := newWorld(t)
		w.effect = func(action.Handoff) {
			w.truth["y"]["storage-writable"] = operations.Bool(true)
			w.frozen[okey("y", "storage-writable")] = true
		}
		approved(w)
		v := w.view(w.advance(10 * time.Second))
		if v.Recovery != action.RecoveryPending {
			t.Fatalf("%+v", v)
		}
		delete(w.frozen, okey("y", "storage-writable"))
		if v = w.view(w.advance(10 * time.Second)); v.Recovery != action.RecoveryRecovered {
			t.Fatalf("%+v", v)
		}
		w.assertSafe()
	})
	t.Run("evidence older than the end is not post-execution", func(t *testing.T) {
		w := newWorld(t)
		// The capability recovers by itself before the result arrives; that
		// earlier observation must not count, only later ones.
		w.actor.Default = actiontest.AcceptOnly
		approved(w)
		w.truth["y"]["storage-writable"] = operations.Bool(true)
		w.advance(time.Second)
		w.frozen[okey("y", "storage-writable")] = true
		for _, k := range w.actor.ExecutedKeys() {
			w.actor.Settle(k, actiontest.Succeed)
		}
		if v := w.view(w.advance(time.Second)); v.Phase != action.PhaseSucceeded {
			t.Fatalf("%+v", v)
		}
		var v action.View
		for i := 0; i < 8; i++ {
			v = w.view(w.advance(10 * time.Second))
			if v.Recovery == action.RecoveryRecovered {
				t.Fatalf("pre-completion evidence used: %+v", v)
			}
		}
		w.assertSafe()
	})
}
