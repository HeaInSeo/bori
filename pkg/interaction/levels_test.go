package interaction

import (
	"reflect"
	"strings"
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// NO_ACTION needs proof: every capability AVAILABLE on current evidence and
// every considered envelope SATISFIED.
func TestNoActionOnlyWithCurrentProof(t *testing.T) {
	w := pipeline()
	r := w.reconcile()
	zero(t, "healthy", check(r, nil))
	for _, n := range []string{"a", "x", "y"} {
		s := r.summary[n]
		if s.Level != opsv1.LevelNoAction || !hasReason(s.LevelReasons, ReasonAllAvailable) {
			t.Fatalf("%s: %s %v", n, s.Level, reasonCodes(s.LevelReasons))
		}
		if len(s.PendingPostConditions) != 0 || len(s.HumanReasons) != 0 {
			t.Fatalf("%s: %+v", n, s)
		}
	}
	if got := r.summary["y"].Summary; got != "all 3 capabilities AVAILABLE on current evidence" {
		t.Fatalf("summary %q", got)
	}
	if len(r.summary["y"].ConfirmedFacts) != 3 || r.summary["y"].ConfirmedFacts[0].EvidenceRef == "" {
		t.Fatalf("facts %+v", r.summary["y"].ConfirmedFacts)
	}
}

// UNKNOWN on a declared capability is never NO_ACTION, and never more than
// AWARENESS by itself.
func TestUnknownIsAwarenessNotFailure(t *testing.T) {
	w := pipeline()
	w.observations = w.observations[:len(w.observations)-1] // y storage-writable missing
	r := w.reconcile()
	zero(t, "unknown", check(r, nil))
	s := r.summary["y"]
	if s.Level != opsv1.LevelAwareness || !hasReason(s.LevelReasons, ReasonCapabilityUnknown) {
		t.Fatalf("%s %v", s.Level, reasonCodes(s.LevelReasons))
	}
	if len(s.Affected) != 0 || !reflect.DeepEqual(capNames(s.Unknown), []string{"persist"}) {
		t.Fatalf("affected %v unknown %v", capNames(s.Affected), capNames(s.Unknown))
	}
	if len(s.MissingEvidence) != 1 || s.MissingEvidence[0].Slot != "storage-writable" || s.MissingEvidence[0].State != "Missing" {
		t.Fatalf("missing %+v", s.MissingEvidence)
	}
	if !hasReason(s.PendingPostConditions, PostCapabilityAvailable) {
		t.Fatal("no pending post-condition for the unknown capability")
	}
}

// A required binding missing from the spec (slot unbound) is a missing
// check: UNKNOWN, never NO_ACTION.
func TestUnboundRequiredSlotIsNotNoAction(t *testing.T) {
	w := pipeline()
	y := w.get("y")
	y.Spec.AssertionBindings = y.Spec.AssertionBindings[:2] // storage-writable unbound
	w.targets[2] = y
	w.healthy()
	w.observations = w.observations[:len(w.observations)-1]
	r := w.reconcile()
	zero(t, "unbound", check(r, nil))
	s := r.summary["y"]
	if s.Level == opsv1.LevelNoAction || len(s.Unknown) != 1 {
		t.Fatalf("%s unknown=%v", s.Level, capNames(s.Unknown))
	}
	if s.MissingEvidence[0].State != string(operations.EvidenceUnbound) {
		t.Fatalf("missing %+v", s.MissingEvidence)
	}
}

// An invalid target is not assessed: no capability result is fabricated, and
// the summary requires a spec correction rather than claiming anything.
func TestInvalidTargetIsDecisionWithoutCapabilities(t *testing.T) {
	w := pipeline()
	y := w.get("y")
	y.Spec.AssertionBindings = append(y.Spec.AssertionBindings, bind("api-serving", "other-probe"))
	w.targets[2] = y
	r := w.reconcile()
	zero(t, "invalid", check(r, nil))
	s := r.summary["y"]
	if s.Level != opsv1.LevelDecisionRequired || !hasReason(s.LevelReasons, ReasonTargetNotAssessed) || !hasReason(s.HumanReasons, ReasonSpecCorrection) {
		t.Fatalf("%s %v %v", s.Level, reasonCodes(s.LevelReasons), reasonCodes(s.HumanReasons))
	}
	if len(s.Affected)+len(s.Unaffected)+len(s.Unknown)+len(s.ConfirmedFacts) != 0 {
		t.Fatalf("fabricated results %+v", s)
	}
	if !strings.Contains(s.Summary, "duplicate-assertion-binding") {
		t.Fatalf("summary %q", s.Summary)
	}
}

// Zero targets: nothing is assessed, which is not NO_ACTION.
func TestZeroTargetsIsNothingAssessed(t *testing.T) {
	o := OverviewOf(nil)
	if !o.NothingAssessed || o.ByLevel[opsv1.LevelNoAction] != 0 {
		t.Fatalf("%+v", o)
	}
	if got := RenderText(nil); !strings.Contains(got, "nothing is assessed (this is not NO_ACTION)") {
		t.Fatalf("%q", got)
	}
}

// A→X: A's failure affects only X.submit (via the dependency); X.observe
// keeps its own current proof; unrelated Y is untouched. X does not repeat
// A's decision (impact-from-dependency).
func TestDependencyImpactIsLocalized(t *testing.T) {
	w := pipeline()
	w.healthy()
	w.observations[0] = w.fact("a", "up", operations.Bool(false), fresh)
	r := w.reconcile()
	zero(t, "a-down", check(r, map[string][]string{"x": {"observe"}, "y": {"serve", "count", "persist"}}))

	a, x, y := r.summary["a"], r.summary["x"], r.summary["y"]
	if !reflect.DeepEqual(capNames(a.Affected), []string{"resolve"}) || a.Affected[0].State != "UNAVAILABLE" {
		t.Fatalf("a %+v", a.Affected)
	}
	if !reflect.DeepEqual(capNames(x.Affected), []string{"submit"}) || !reflect.DeepEqual(capNames(x.Unaffected), []string{"observe"}) {
		t.Fatalf("x affected %v unaffected %v", capNames(x.Affected), capNames(x.Unaffected))
	}
	if x.Level != opsv1.LevelAwareness || !hasReason(x.LevelReasons, ReasonImpactFromDependency) {
		t.Fatalf("x %s %v", x.Level, reasonCodes(x.LevelReasons))
	}
	if a.Level != opsv1.LevelAwareness || !hasReason(a.LevelReasons, ReasonNoDeclaredResponse) {
		t.Fatalf("a %s %v", a.Level, reasonCodes(a.LevelReasons))
	}
	if y.Level != opsv1.LevelNoAction {
		t.Fatalf("unrelated y %s", y.Level)
	}

	// Y's own evidence goes stale: Y is UNKNOWN on its own evidence (not
	// AVAILABLE kept, not attributed to A).
	w.observations[4] = w.fact("y", "api-serving", operations.Bool(true), time.Minute)
	r = w.reconcile()
	zero(t, "y-stale", check(r, map[string][]string{"y": {"serve", "count", "persist"}}))
	y = r.summary["y"]
	if y.Level != opsv1.LevelAwareness || !reflect.DeepEqual(capNames(y.Unknown), []string{"serve"}) {
		t.Fatalf("y %s unknown %v", y.Level, capNames(y.Unknown))
	}
	if y.Unknown[0].Reasons[0].Code != string(operations.ReasonEvidenceStale) || strings.Contains(y.Summary, "resolve") {
		t.Fatalf("y attributed to %+v / %q", y.Unknown[0].Reasons, y.Summary)
	}
}

// Partial failure in one target and two independent failures in two targets
// stay separate: no merged root cause, no shared fingerprint.
func TestPartialAndIndependentFailuresStaySeparate(t *testing.T) {
	w := pipeline()
	w.healthy()
	w.observations[1] = w.fact("a", "fast", operations.Bool(false), fresh)             // A degraded
	w.observations[6] = w.fact("y", "storage-writable", operations.Bool(false), fresh) // Y persist down
	r := w.reconcile()
	zero(t, "independent", check(r, map[string][]string{"y": {"serve", "count"}}))
	y := r.summary["y"]
	if !reflect.DeepEqual(capNames(y.Affected), []string{"persist"}) || len(y.Unaffected) != 2 {
		t.Fatalf("y partial: affected %v unaffected %v", capNames(y.Affected), capNames(y.Unaffected))
	}
	if r.summary["a"].Affected[0].State != "DEGRADED" {
		t.Fatalf("a %+v", r.summary["a"].Affected)
	}
	if r.summary["a"].Fingerprint == y.Fingerprint || strings.Contains(y.Summary, "resolve") || strings.Contains(r.summary["a"].Summary, "persist") {
		t.Fatal("independent failures merged")
	}
}

// Provider outage, stale and conflicting evidence are failures to confirm,
// not application failures, and never fall back to an older AVAILABLE.
func TestEvidenceFailuresAreNotAppFailures(t *testing.T) {
	cases := map[string]func(w *world){
		"provider-outage": func(w *world) {
			w.observations = w.observations[:4]
			w.observe("y", "ready-replicas", operations.Int(3))
			w.observe("y", "storage-writable", operations.Bool(true))
			w.observations = append(w.observations, w.fact("y", "api-serving", operations.Bool(true), 20*time.Second))
			w.outage("y", "api-serving")
		},
		"stale": func(w *world) {
			w.observations[4] = w.fact("y", "api-serving", operations.Bool(true), time.Minute)
		},
		"conflict": func(w *world) {
			w.observations = append(w.observations, w.fact("y", "api-serving", operations.Bool(false), fresh))
		},
	}
	want := map[string]string{"provider-outage": "ProviderUnavailable", "stale": "Stale", "conflict": "Conflicting"}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := pipeline()
			mutate(w)
			r := w.reconcile()
			zero(t, name, check(r, map[string][]string{"y": {"serve", "count", "persist"}}))
			y := r.summary["y"]
			if y.Level != opsv1.LevelAwareness || len(y.Affected) != 0 || !reflect.DeepEqual(capNames(y.Unknown), []string{"serve"}) {
				t.Fatalf("%s affected %v unknown %v", y.Level, capNames(y.Affected), capNames(y.Unknown))
			}
			found := false
			for _, m := range y.MissingEvidence {
				found = found || (m.Slot == "api-serving" && m.State == want[name])
			}
			if !found {
				t.Fatalf("missing %+v", y.MissingEvidence)
			}
			for _, f := range y.ConfirmedFacts {
				if f.Slot == "api-serving" {
					t.Fatalf("non-current evidence shown as confirmed %+v", f)
				}
			}
		})
	}
}

// Workload readiness alone is not capability recovery: with only
// ready-replicas current, serve stays UNKNOWN, the post-condition stays
// unconfirmed and nothing claims recovery — even with a Ready response.
func TestReadinessAloneIsNotRecovery(t *testing.T) {
	w := pipeline()
	w.observations = w.observations[:4]
	w.observe("y", "ready-replicas", operations.Int(3))
	w.observe("y", "storage-writable", operations.Bool(true))
	w.profile = &Profile{Revision: "p1", Responses: []Response{{
		Action: ActionRef{Name: "restart", Revision: "r1"}, Target: TargetRef{Namespace: "apps", Name: "y"},
		For: capT("serve"), States: []string{"UNKNOWN", "UNAVAILABLE"}, Owner: "platform-oncall",
	}}}
	r := w.reconcile()
	zero(t, "ready-only", check(r, nil))
	y := r.summary["y"]
	if y.Level == opsv1.LevelNoAction || !reflect.DeepEqual(capNames(y.Unknown), []string{"serve"}) {
		t.Fatalf("%s %v", y.Level, capNames(y.Unknown))
	}
	if !hasReason(y.PendingPostConditions, PostCapabilityAvailable) || y.PendingPostConditions[0].Detail != PostUnconfirmed {
		t.Fatalf("post %+v", y.PendingPostConditions)
	}
}

// Envelope gaps: Minimum or selected envelope not satisfied is awareness.
func TestEnvelopeGapIsAwareness(t *testing.T) {
	w := pipeline()
	y := w.get("y")
	y.Spec.EnvelopeSelection = "recommended"
	w.targets[2] = y
	w.healthy()
	w.observations[5] = w.fact("y", "ready-replicas", operations.Int(1), fresh)
	r := w.reconcile()
	zero(t, "envelope", check(r, nil))
	s := r.summary["y"]
	if s.Level != opsv1.LevelAwareness || !hasReason(s.LevelReasons, ReasonEnvelopeUnsatisfied) || len(s.Affected) != 0 {
		t.Fatalf("%s %v", s.Level, reasonCodes(s.LevelReasons))
	}
}
