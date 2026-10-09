package interaction

import (
	"fmt"
	"testing"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// multiWorld: capability "work" of target w requires `deps` failing
// resolver dependencies and, when local >= 0, one failing local predicate
// placed at requirement position `local`.
func multiWorld(deps, local int) *world {
	w := newWorld()
	rc := contract("apps", "resolver-v1", "c-res", resolverSpec())
	spec := opsv1.OperationalContractSpec{Assertions: []opsv1.AssertionSlot{boolSlot("local-up")}}
	var reqs []opsv1.Requirement
	for i := 0; i < deps; i++ {
		slot := fmt.Sprintf("dep%d", i)
		spec.DependencySlots = append(spec.DependencySlots, opsv1.DependencySlot{Name: slot})
		reqs = append(reqs, depReq(slot, "resolve", "UNAVAILABLE"))
	}
	if local >= 0 {
		l := isTrue("local-up", "UNAVAILABLE")
		reqs = append(reqs[:local], append([]opsv1.Requirement{l}, reqs[local:]...)...)
	}
	spec.Capabilities = []opsv1.Capability{{Type: capT("work"), Requirements: reqs}}
	mc := contract("apps", "multi-v1", "c-multi", spec)
	w.contracts = []opsv1.OperationalContract{rc, mc}
	wt := target("apps", "w", "t-w", "multi-v1", bind("local-up", "http-probe"))
	for i := 0; i < deps; i++ {
		r := target("apps", fmt.Sprintf("r%d", i), fmt.Sprintf("t-r%d", i), "resolver-v1", bind("up", "http-probe"), bind("fast", "http-probe"))
		w.targets = append(w.targets, r)
		wt.Spec.DependencyBindings = append(wt.Spec.DependencyBindings, bindDep(fmt.Sprintf("dep%d", i), r, rc))
	}
	w.targets = append(w.targets, wt)
	for i := 0; i < deps; i++ {
		w.observe(fmt.Sprintf("r%d", i), "up", operations.Bool(false))
		w.observe(fmt.Sprintf("r%d", i), "fast", operations.Bool(true))
	}
	w.observe("w", "local-up", operations.Bool(false))
	return w
}

// Codex r4219279487 regression: a local failure that is not among the first
// four displayed reasons keeps the capability's decision local; only an
// impact made entirely of dependency reasons is handed to the dependency.
func TestDependencyOnlyIsJudgedOnAllReasons(t *testing.T) {
	for _, pos := range []int{0, 2, 4, 5} {
		t.Run(fmt.Sprintf("local requirement at %d of 6", pos), func(t *testing.T) {
			r := multiWorld(5, pos).reconcile()
			zero(t, "mixed", check(r, nil))
			if n := len(r.status["w"].Capabilities[0].Reasons); n <= maxCapReasons {
				t.Fatalf("fixture has only %d reasons; truncation is not exercised", n)
			}
			s := r.summary["w"]
			if hasReason(s.LevelReasons, ReasonImpactFromDependency) || !hasReason(s.LevelReasons, ReasonNoDeclaredResponse) {
				t.Fatalf("local failure handed to dependencies: %v (status reasons %v)", reasonCodes(s.LevelReasons), reasonCodes(r.status["w"].Capabilities[0].Reasons))
			}
			if len(s.Affected[0].Reasons) > maxCapReasons {
				t.Fatalf("display bound lost: %d", len(s.Affected[0].Reasons))
			}
		})
	}
	t.Run("all dependencies (control)", func(t *testing.T) {
		w := multiWorld(6, -1)
		r := w.reconcile()
		zero(t, "deps", check(r, nil))
		if s := r.summary["w"]; !hasReason(s.LevelReasons, ReasonImpactFromDependency) || hasReason(s.LevelReasons, ReasonNoDeclaredResponse) {
			t.Fatalf("all-dependency impact: %v", reasonCodes(s.LevelReasons))
		}
	})
}

// The fail-closed bound matches the status reason bound of the CRD.
func TestStatusReasonBoundMatchesSchema(t *testing.T) {
	crd := loadTargetCRD(t)
	node := crd["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"]
	for _, p := range []string{"status", "capabilities"} {
		node = node.(map[string]any)["properties"].(map[string]any)[p]
	}
	reasons := node.(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["reasons"].(map[string]any)
	if reasons["maxItems"] != float64(statusReasonBound) {
		t.Fatalf("status capability reasons maxItems %v, bound %d", reasons["maxItems"], statusReasonBound)
	}
}
