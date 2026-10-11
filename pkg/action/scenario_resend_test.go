package action_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Every resend revalidates: an unacknowledged handoff is not sent again once
// a precondition fails, the meaning changes, the approval expires or the
// trigger is gone; it is withdrawn, never silently resent.
func TestResendRevalidates(t *testing.T) {
	cases := map[string]func(w *world){
		"precondition fails": func(w *world) { w.truth["y"]["api-serving"] = operations.Bool(false) },
		"meaning changes":    func(w *world) { w.targets["y"].AssertionBindings[2].Provider.ConfigRevision = "r2" },
		"trigger gone":       func(w *world) { w.truth["y"]["storage-writable"] = operations.Bool(true) },
		"approval expires": func(w *world) {
			w.profile.Responses[0].Execution.ApprovalTTL = metav1.Duration{Duration: 2 * time.Second}
		},
		"provider loses authority": func(w *world) {
			reg := w.eng.Providers["actor"]
			reg.Namespaces = nil
			w.eng.Providers["actor"] = reg
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.DropSubmits = 1
			if v := approved(w); v.Phase != action.PhaseDispatching {
				t.Fatalf("%+v", v)
			}
			change(w)
			var r action.Record
			for i := 0; i < 4; i++ {
				w.advance(4 * time.Second)
			}
			r = w.records()[0]
			if r.Phase != action.PhaseWithdrawn || len(w.actor.Handoffs) != 0 || r.Sends != 1 {
				t.Fatalf("record %s sends %d handoffs %d", r.Phase, r.Sends, len(w.actor.Handoffs))
			}
			w.assertSafe()
		})
	}
}

// At most one execution is in flight per target: a second approved
// response for the same target waits until the first one ends.
func TestOneInFlightExecutionPerTarget(t *testing.T) {
	w := newWorld(t)
	w.actor.Default = actiontest.AcceptOnly
	failover := remount()
	failover.Action = interaction.ActionRef{Name: "failover", Revision: "r1"}
	w.profile.Responses = append(w.profile.Responses, failover)
	out := w.step()
	if len(out.Views["t-y"]) != 2 {
		t.Fatalf("%+v", out.Views)
	}
	for _, v := range out.Views["t-y"] {
		w.approve(v)
	}
	out = w.advance(time.Second)
	phases := map[action.Phase]int{}
	for _, v := range out.Views["t-y"] {
		phases[v.Phase]++
		if v.Phase == action.PhaseBlocked && !hasReason(v, action.ReasonTargetBusy) {
			t.Fatalf("%+v", v)
		}
	}
	if phases[action.PhaseAccepted] != 1 || phases[action.PhaseBlocked] != 1 || w.actor.TotalExecutions() != 1 {
		t.Fatalf("phases %v executions %d", phases, w.actor.TotalExecutions())
	}
	w.assertSafe()
}

// Retrying is only for a definitive failure: a success without recovery or
// an unknown outcome is never retried, even when retries are declared.
func TestNoRetryAfterSuccessOrUnknownOutcome(t *testing.T) {
	for name, b := range map[string]actiontest.Behaviour{"succeeded": actiontest.Succeed, "timed out": actiontest.AcceptOnly} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.actor.Default = b
			w.profile.Responses[0].Execution.Retryable = true
			w.profile.Responses[0].Execution.MaxAttempts = 3
			approved(w)
			for i := 0; i < 15; i++ {
				w.advance(10 * time.Second)
			}
			if w.actor.TotalExecutions() != 1 {
				t.Fatalf("executions %d", w.actor.TotalExecutions())
			}
			w.assertSafe()
		})
	}
}

// A policy-approved action (no declared human reason) runs without a person,
// yet still through every execution gate.
func TestPolicyApprovalStillPassesGates(t *testing.T) {
	w := newWorld(t)
	w.profile.Responses[0].RequiresApproval = false
	w.profile.Responses[0].Execution.Approvers = nil
	w.effect = fixPersist(w)
	w.truth["y"]["api-serving"] = operations.Bool(false) // precondition unmet
	if v := w.view(w.step()); v.Phase != action.PhaseBlocked || !hasReason(v, action.ReasonPreconditionUnmet) {
		t.Fatalf("%+v", v)
	}
	w.truth["y"]["api-serving"] = operations.Bool(true)
	v := w.view(w.advance(time.Second))
	if v.Phase != action.PhaseAccepted || w.actor.Handoffs[0].Approval.Kind != "policy" {
		t.Fatalf("%+v", v)
	}
	w.profile.Responses[0].Execution.MayInterrupt = []opsv1.CapabilityType{opsCap("serve")}
	if got := w.profile.Responses[0].HumanDecisionReasons(); len(got) != 1 || got[0] != action.ReasonMayInterrupt {
		t.Fatalf("blast radius does not need a person: %v", got)
	}
	w.assertSafe()
}
