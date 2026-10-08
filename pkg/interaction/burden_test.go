package interaction

import (
	"fmt"
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// burden counts what a person would receive. It is a reproducible
// simulation, not a measurement of people: no time saving or SLO is claimed.
type burden struct {
	decisionRequests int // requests for a human decision
	notices          int // non-decision notices (AWARENESS and above)
	duplicates       int // repeated notices/requests for an unchanged meaning
	manualQueries    int // slots a person would have to look up by hand
	statusWrites     int // summary changes that would be written
}

// fanOutWorld: resolver A, n workers depending on A, and an unrelated Y. A
// declared response for A.resolve needs approval.
func fanOutWorld(n int) *world {
	w := pipeline()
	rc := w.contracts[0]
	a := w.get("a")
	x := w.get("x")
	for i := 1; i < n; i++ {
		xi := target("apps", fmt.Sprintf("x%d", i), fmt.Sprintf("t-x%d", i), "worker-v1", x.Spec.AssertionBindings...)
		xi.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("resolver", a, rc)}
		w.targets = append(w.targets, xi)
	}
	failover := Response{Action: ActionRef{Name: "failover", Revision: "r1"}, Target: TargetRef{Namespace: "apps", Name: "a"},
		For: capT("resolve"), Owner: "resolver-oncall", RequiresApproval: true}
	w.profile = &Profile{Revision: "p1", Responses: []Response{failover}}
	return w
}

func (w *world) facts(aUp, yWritable bool) {
	w.observations = nil
	for _, t := range w.targets {
		switch t.Spec.ContractRef.Name {
		case "resolver-v1":
			w.observe(t.Name, "up", operations.Bool(aUp))
			w.observe(t.Name, "fast", operations.Bool(true))
		case "worker-v1":
			w.observe(t.Name, "submit-up", operations.Bool(true))
			w.observe(t.Name, "status-up", operations.Bool(true))
		case "service-v1":
			w.observe(t.Name, "api-serving", operations.Bool(true))
			w.observe(t.Name, "ready-replicas", operations.Int(3))
			w.observe(t.Name, "storage-writable", operations.Bool(yWritable))
		}
	}
}

// TestBurdenAgainstBaseline replays one event script and compares the
// projection's requests with a naive per-capability, per-reconcile baseline.
// Safety is checked independently at every step: no burden reduction can
// offset a forbidden outcome.
func TestBurdenAgainstBaseline(t *testing.T) {
	const workers = 6
	type step struct {
		name      string
		aUp, yW   bool
		refreshes int // timestamp-only refreshes after the change
	}
	script := []step{
		{"healthy", true, true, 2},
		{"A fails (fan-out to workers)", false, true, 4},
		{"A flaps up", true, true, 1},
		{"A flaps down", false, true, 1},
		{"A flaps up", true, true, 1},
		{"A flaps down", false, true, 1},
		{"independent Y failure", false, false, 3},
		{"all up", true, true, 2},
	}
	w := fanOutWorld(workers)
	unrelated := map[string][]string{"y": {"serve", "count"}}
	for i := 0; i < workers; i++ {
		name := "x"
		if i > 0 {
			name = fmt.Sprintf("x%d", i)
		}
		unrelated[name] = []string{"observe"}
	}

	var bori, base burden
	last := map[string]string{}
	baseSeen := map[string]bool{}
	requested := map[string]bool{}
	for _, s := range script {
		w.facts(s.aUp, s.yW)
		for rep := 0; rep <= s.refreshes; rep++ {
			w.now = w.now.Add(10 * time.Second)
			for j := range w.observations {
				w.observations[j].ObservedAt = w.now.Add(-fresh)
			}
			r := w.reconcile()
			zero(t, s.name, check(r, unrelated))
			for name, sum := range r.summary {
				st := r.status[name]
				// Baseline: alert per non-AVAILABLE capability on every
				// reconcile; a decision request per affected capability with a
				// declared approval; a person re-queries every slot of every
				// target with an alert.
				alerting := false
				for _, c := range st.Capabilities {
					if c.State == "AVAILABLE" {
						continue
					}
					alerting = true
					key := name + "|" + c.Type.Name + "|" + c.State
					if baseSeen[key] {
						base.duplicates++
					}
					baseSeen[key] = true
					if c.Type.Name == "resolve" || c.Type.Name == "submit" {
						base.decisionRequests++
					} else {
						base.notices++
					}
				}
				if alerting {
					base.manualQueries += len(st.Evidence)
				}
				// Projection: at most one notice per new, non-recurring
				// meaning; decisions only where the boundary requires them.
				if sum.Fingerprint == last[name] {
					continue // unchanged meaning: nothing is written or raised
				}
				last[name] = sum.Fingerprint
				bori.statusWrites++
				if sum.Recurring {
					continue
				}
				switch sum.Level {
				case opsv1.LevelDecisionRequired, opsv1.LevelImmediateIntervention:
					if requested[name+"|"+sum.Fingerprint] {
						bori.duplicates++ // a meaning requested before, not flagged recurring
					}
					requested[name+"|"+sum.Fingerprint] = true
					bori.decisionRequests++
					bori.manualQueries += len(sum.MissingEvidence) // only what BORI could not confirm
				case opsv1.LevelAwareness:
					bori.notices++
				}
			}
		}
	}
	t.Logf("burden (simulation, %d workers on one resolver, 2 flaps, 1 independent failure)", workers)
	t.Logf("%-18s %10s %10s", "", "baseline", "projection")
	t.Logf("%-18s %10d %10d", "decision requests", base.decisionRequests, bori.decisionRequests)
	t.Logf("%-18s %10d %10d", "notices", base.notices, bori.notices)
	t.Logf("%-18s %10d %10d", "duplicates", base.duplicates, bori.duplicates)
	t.Logf("%-18s %10d %10d", "manual queries", base.manualQueries, bori.manualQueries)
	t.Logf("%-18s %10s %10d", "status writes", "-", bori.statusWrites)

	// One decision for A's failure (the only declared approval boundary);
	// the workers' impact is awareness, flaps are recurrences.
	if bori.decisionRequests != 1 {
		t.Fatalf("decision requests %d, want 1", bori.decisionRequests)
	}
	t.Logf("%-18s %10d %10d", "items to a person", base.decisionRequests+base.notices, bori.decisionRequests+bori.notices)
	if bori.duplicates != 0 || bori.decisionRequests >= base.decisionRequests ||
		bori.decisionRequests+bori.notices >= base.decisionRequests+base.notices || bori.manualQueries >= base.manualQueries {
		t.Fatalf("no reduction: baseline %+v projection %+v", base, bori)
	}
}
