package interaction

import (
	"encoding/json"
	"strings"
	"testing"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

// With O5 off (no actions) the summary, including its fingerprint, is
// exactly the O4 summary. The values are from main 905a253 (O4).
func TestFingerprintUnchangedWithoutActions(t *testing.T) {
	w := yDown()
	w.profile = &Profile{Revision: "p1", Responses: []Response{resp("remount", "storage-oncall", "persist")}}
	want := map[string]string{
		"a": "263fdc8e675488bbd8b2d993da22fbcd",
		"x": "813aca63d10ff6e2addc8d87598e3b23",
		"y": "c88fb857ba4e51218ff0957ba30c2dfe",
	}
	got := w.reconcile().summary
	for n, f := range want {
		if got[n].Fingerprint != f || got[n].Actions != nil {
			t.Fatalf("%s: fingerprint %s actions %v, want O4 %s", n, got[n].Fingerprint, got[n].Actions, f)
		}
	}
}

func projectWith(t *testing.T, as ...opsv1.InteractionAction) *opsv1.InteractionStatus {
	t.Helper()
	w := yDown()
	w.profile = &Profile{Revision: "p1", Responses: []Response{resp("remount", "storage-oncall", "persist")}}
	b, a := w.build()
	y := w.get("y")
	in := Input{Namespace: y.Namespace, Name: y.Name, UID: string(y.UID), Status: opswire.Project(&y, b, a), Profile: w.profile, Actions: as}
	s := Project(in, nil)
	if len(as) == 0 && s.Level != opsv1.LevelAwareness {
		t.Fatalf("baseline level %s", s.Level)
	}
	return s
}

func validateActions(t *testing.T, s *opsv1.InteractionStatus) []string {
	t.Helper()
	b, _ := json.Marshal(s)
	var v any
	_ = json.Unmarshal(b, &v)
	return validate("status.interaction", interactionSchema(t), v)
}

// An unknown outcome or an execution without recovery needs a person; an
// action waiting for approval, in flight or recovered adds nothing, and a
// recovery never lowers the level (it follows the current capability state).
func TestActionsEscalateOnlyOnUnknownOutcomeOrNonRecovery(t *testing.T) {
	act := func(phase, recovery string) opsv1.InteractionAction {
		return opsv1.InteractionAction{Proposal: "p", Digest: "d", Name: "remount@r1", For: capT("persist"), Phase: phase, Attempt: 1, Recovery: recovery}
	}
	cases := []struct {
		a     opsv1.InteractionAction
		level opsv1.InteractionLevel
		human string
	}{
		{act("TimedOut", "Unconfirmed"), opsv1.LevelDecisionRequired, ReasonActionOutcomeUnknown},
		{act("Withdrawn", ""), opsv1.LevelDecisionRequired, ReasonActionOutcomeUnknown},
		{act("Succeeded", "NotRecovered"), opsv1.LevelDecisionRequired, ReasonActionNotRecovered},
		{act("Failed", "Partial"), opsv1.LevelDecisionRequired, ReasonActionNotRecovered},
		{act("Succeeded", "Unconfirmed"), opsv1.LevelDecisionRequired, ReasonActionNotRecovered},
		{act("Succeeded", "Pending"), opsv1.LevelAwareness, ""},
		{act("Succeeded", "Recovered"), opsv1.LevelAwareness, ""},
		{act("Accepted", ""), opsv1.LevelAwareness, ""},
		{act("Succeeded", "Inapplicable"), opsv1.LevelAwareness, ""},
		{act("TimedOut", "Recovered"), opsv1.LevelAwareness, ""},
	}
	for _, c := range cases {
		s := projectWith(t, c.a)
		if s.Level != c.level || (c.human != "" && !hasReason(s.HumanReasons, c.human)) || (c.human == "" && len(s.HumanReasons) != 0) {
			t.Fatalf("%s/%s: level %s human %v", c.a.Phase, c.a.Recovery, s.Level, reasonCodes(s.HumanReasons))
		}
		if len(s.Actions) != 1 || s.Fingerprint == projectWith(t).Fingerprint {
			t.Fatalf("%s/%s: actions not part of the summary's meaning", c.a.Phase, c.a.Recovery)
		}
		if strings.Contains(strings.ToLower(s.Summary), "recover") {
			t.Fatalf("summary line claims recovery: %s", s.Summary)
		}
	}
}

// The actions projection at its bounds fits the generated CRD schema.
func TestActionsAtBoundsFitTheSchema(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) +
		"/" + strings.Repeat("n", 63) + "@" + strings.Repeat("r", 63) // 381
	var as []opsv1.InteractionAction
	for i := 0; i < 8; i++ {
		a := opsv1.InteractionAction{
			Proposal: strings.Repeat("f", 32), Digest: strings.Repeat("e", 64),
			Name:  strings.Repeat("m", 63) + "@" + strings.Repeat("v", 63),
			For:   opsv1.CapabilityType{Domain: strings.Repeat("d", 253), Name: strings.Repeat("n", 63), Revision: strings.Repeat("r", 63)},
			Phase: "AwaitingApproval", Attempt: 3, Recovery: "Unconfirmed",
		}
		for j := 0; j < 16; j++ {
			a.Reasons = append(a.Reasons, opsv1.StatusReason{Code: "precondition-unproven", Subject: long})
		}
		for j := 0; j < 8; j++ {
			a.RecoveryDetail = append(a.RecoveryDetail, opsv1.StatusReason{Code: "UNKNOWN", Subject: long})
		}
		as = append(as, a)
	}
	validateSummary(t, projectWith(t, as...))
	for _, bad := range []func(a *opsv1.InteractionAction){
		func(a *opsv1.InteractionAction) { a.Phase = "Recovered" }, // a phase is never a recovery
		func(a *opsv1.InteractionAction) { a.Recovery = "Succeeded" },
	} {
		x := as[0]
		bad(&x)
		s := projectWith(t, x)
		if errs := validateActions(t, s); len(errs) == 0 {
			t.Fatalf("schema accepted %+v", x)
		}
	}
}

// History never escalates a capability that is AVAILABLE on current
// evidence: a not-recovered or unknown-outcome execution of a capability
// that later recovered by other means adds no human reason.
func TestActionHistoryDoesNotEscalateAvailableCapability(t *testing.T) {
	w := pipeline()
	w.healthy()
	w.profile = &Profile{Revision: "p1", Responses: []Response{resp("remount", "storage-oncall", "persist")}}
	b, a := w.build()
	y := w.get("y")
	for _, ph := range [][2]string{{"Succeeded", "NotRecovered"}, {"TimedOut", "Unconfirmed"}, {"Withdrawn", ""}} {
		in := Input{Namespace: y.Namespace, Name: y.Name, UID: string(y.UID), Status: opswire.Project(&y, b, a), Profile: w.profile,
			Actions: []opsv1.InteractionAction{{Proposal: "p", Digest: "d", Name: "remount@r1", For: capT("persist"), Phase: ph[0], Attempt: 1, Recovery: ph[1]}}}
		s := Project(in, nil)
		if s.Level != opsv1.LevelNoAction || len(s.HumanReasons) != 0 || len(s.Actions) != 1 {
			t.Fatalf("%v: level %s human %v", ph, s.Level, reasonCodes(s.HumanReasons))
		}
	}
}
