package interaction

import (
	"testing"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

func resp(action, owner string, capName string) Response {
	return Response{
		Action: ActionRef{Name: action, Revision: "r1"},
		Target: TargetRef{Namespace: "apps", Name: "y"},
		For:    capT(capName),
		Owner:  owner,
	}
}

// yDown: Y.persist UNAVAILABLE (storage-writable false), Y.serve and
// Y.count AVAILABLE on current evidence.
func yDown() *world {
	w := pipeline()
	w.healthy()
	w.observations[6] = w.fact("y", "storage-writable", operations.Bool(false), fresh)
	return w
}

// The declared human-decision boundary decides between AWARENESS,
// DECISION_REQUIRED and IMMEDIATE_INTERVENTION; nothing is inferred.
func TestDeclaredBoundaryLevels(t *testing.T) {
	withPre := func(r Response, pre ...string) Response {
		for _, p := range pre {
			r.Preconditions = append(r.Preconditions, capT(p))
		}
		return r
	}
	approval := resp("failover", "storage-oncall", "persist")
	approval.RequiresApproval = true
	risky := resp("rebuild-volume", "storage-oncall", "persist")
	risky.Risks = []string{RiskDataLoss}

	cases := []struct {
		name      string
		mutate    func(w *world)
		responses []Response
		level     opsv1.InteractionLevel
		reason    string
		status    []string
	}{
		{name: "no declaration reports the lack", level: opsv1.LevelAwareness, reason: ReasonNoDeclaredResponse},
		{name: "single ready response", responses: []Response{withPre(resp("remount", "storage-oncall", "persist"), "serve")},
			level: opsv1.LevelAwareness, reason: ReasonDeclaredResponseReady, status: []string{ResponseReady}},
		{name: "approval required", responses: []Response{approval}, level: opsv1.LevelDecisionRequired, reason: ReasonApprovalRequired, status: []string{ResponseApprovalRequired}},
		{name: "declared risk", responses: []Response{risky}, level: opsv1.LevelDecisionRequired, reason: ReasonApprovalRequired, status: []string{ResponseApprovalRequired}},
		{name: "several candidates, no priority authority",
			responses: []Response{resp("remount", "storage-oncall", "persist"), resp("reschedule", "platform-oncall", "persist")},
			level:     opsv1.LevelDecisionRequired, reason: ReasonNoPriorityAuthority, status: []string{ResponseReady, ResponseReady}},
		{name: "owner undeclared", responses: []Response{resp("remount", "", "persist")}, level: opsv1.LevelDecisionRequired, reason: ReasonOwnerUndeclared, status: []string{ResponseOwnerUndeclared}},
		{name: "owner conflict", responses: []Response{resp("remount", "storage-oncall", "persist"), resp("remount", "platform-oncall", "persist")},
			level: opsv1.LevelDecisionRequired, reason: ReasonOwnerConflict, status: []string{ResponseOwnerConflict, ResponseOwnerConflict}},
		{name: "safety condition unproven",
			mutate:    func(w *world) { w.observations = append(w.observations[:4], w.observations[5:]...) }, // serve UNKNOWN
			responses: []Response{withPre(resp("remount", "storage-oncall", "persist"), "serve")},
			level:     opsv1.LevelDecisionRequired, reason: ReasonSafetyUnproven, status: []string{ResponseSafetyUnproven}},
		{name: "no safe path for an unavailable capability",
			mutate:    func(w *world) { w.observations[5] = w.fact("y", "ready-replicas", operations.Int(0), fresh) }, // count DEGRADED
			responses: []Response{withPre(resp("remount", "storage-oncall", "persist"), "count")},
			level:     opsv1.LevelImmediateIntervention, reason: ReasonNoSafePath, status: []string{ResponseInapplicable}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := yDown()
			if c.mutate != nil {
				c.mutate(w)
			}
			if c.responses != nil {
				w.profile = &Profile{Revision: "p1", Responses: c.responses}
			}
			r := w.reconcile()
			zero(t, c.name, check(r, map[string][]string{"y": {"serve"}}))
			y := r.summary["y"]
			if y.Level != c.level || !hasReason(y.LevelReasons, c.reason) {
				t.Fatalf("level %s reasons %v, want %s/%s", y.Level, reasonCodes(y.LevelReasons), c.level, c.reason)
			}
			if len(y.Responses) != len(c.status) {
				t.Fatalf("responses %+v", y.Responses)
			}
			for i, s := range c.status {
				if y.Responses[i].Status != s || y.Responses[i].Target != "apps/y#t-y" {
					t.Fatalf("response %d %+v, want %s", i, y.Responses[i], s)
				}
			}
			if (c.level == opsv1.LevelDecisionRequired || c.level == opsv1.LevelImmediateIntervention) && len(y.HumanReasons) == 0 {
				t.Fatal("a decision without a stated human reason")
			}
			if c.level == opsv1.LevelAwareness && len(y.HumanReasons) != 0 {
				t.Fatalf("awareness with human reasons %v", reasonCodes(y.HumanReasons))
			}
		})
	}
}

// UNKNOWN alone never reaches IMMEDIATE_INTERVENTION, even when every
// declared response is inapplicable.
func TestUnknownNeverImmediate(t *testing.T) {
	w := pipeline()
	w.healthy()
	w.observations = w.observations[:6] // persist UNKNOWN
	w.observations[5] = w.fact("y", "ready-replicas", operations.Int(0), fresh)
	r0 := resp("remount", "storage-oncall", "persist")
	r0.States = []string{"UNKNOWN"}
	r0.Preconditions = []opsv1.CapabilityType{capT("count")} // DEGRADED: unmet
	w.profile = &Profile{Revision: "p1", Responses: []Response{r0}}
	r := w.reconcile()
	zero(t, "unknown-inapplicable", check(r, nil))
	y := r.summary["y"]
	if y.Level != opsv1.LevelDecisionRequired || !hasReason(y.LevelReasons, ReasonNoViableResponse) {
		t.Fatalf("%s %v", y.Level, reasonCodes(y.LevelReasons))
	}
}

// Responses are bound to the exact target: another target, another
// capability, an untriggered state or a pinned UID of a replaced instance
// produce nothing.
func TestResponsesBindToExactTarget(t *testing.T) {
	w := yDown()
	other := resp("remount", "storage-oncall", "persist")
	other.Target.Name = "a"
	wrongCap := resp("remount", "storage-oncall", "serve") // serve AVAILABLE
	pinned := resp("remount", "storage-oncall", "persist")
	pinned.Target.UID = "t-y-old"
	w.profile = &Profile{Revision: "p1", Responses: []Response{other, wrongCap, pinned}}
	r := w.reconcile()
	zero(t, "exact", check(r, nil))
	if n := len(r.summary["y"].Responses) + len(r.summary["a"].Responses); n != 0 {
		t.Fatalf("%d responses offered", n)
	}
	if !hasReason(r.summary["y"].LevelReasons, ReasonNoDeclaredResponse) {
		t.Fatal("lack of an applicable declaration not reported")
	}
}

// The forbidden-outcome checker itself must detect each violation class.
func TestForbiddenCheckerDetectsViolations(t *testing.T) {
	w := yDown()
	r := w.reconcile()
	y := *r.summary["y"]
	y.Affected = append(y.Affected, opsv1.InteractionCapability{Type: capT("serve"), State: "UNAVAILABLE"})
	y.Unaffected = append(y.Unaffected, opsv1.InteractionCapability{Type: capT("persist"), State: "AVAILABLE"})
	y.Summary = "persist recovered"
	y.MissingEvidence = append(y.MissingEvidence, opsv1.EvidenceStatus{Slot: "api-serving", State: "Stale", EvidenceRef: "ref:old"})
	y.Responses = append(y.Responses, opsv1.InteractionResponse{Target: "apps/y#t-old", Status: "Executed"})
	r.summary["y"] = &y
	f := check(r, map[string][]string{"y": {"serve"}})
	if f.unrelatedFailure == 0 || f.unjustifiedNormal == 0 || f.unauthorizedExec == 0 || f.staleReuse == 0 {
		t.Fatalf("checker missed a class: %+v", f)
	}
}
