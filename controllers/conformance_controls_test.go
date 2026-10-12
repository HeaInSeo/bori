package controllers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

func packByName(t *testing.T, name string) (*confPack, map[string]confScenario) {
	t.Helper()
	for _, p := range loadPacks(t) {
		if p.Name == name {
			sc := map[string]confScenario{}
			for _, s := range p.Scenarios {
				sc[s.Name] = s
			}
			return p, sc
		}
	}
	t.Fatalf("no pack %s", name)
	return nil, nil
}

// runTo runs a scenario of a pack and returns the world after it, with the
// checker's counts reset so a control sees only what it injects.
func runTo(t *testing.T, pack, scenario string) *confEnv {
	t.Helper()
	p, sc := packByName(t, pack)
	s, ok := sc[scenario]
	if !ok {
		t.Fatalf("no scenario %s/%s", pack, scenario)
	}
	e := newConfEnv(t, p, s.ShippedKindsOnly)
	e.run(s)
	e.ck = confForbidden{}
	return e
}

func setCap(cl claims, target, capName, state string) {
	st := cl[target]
	for i := range st.Capabilities {
		if st.Capabilities[i].Type.Name == capName {
			st.Capabilities[i].State = state
		}
	}
	cl[target] = st
}

func wantForbidden(t *testing.T, f confForbidden, unrelated, recovery, unauthorized, stale int) {
	t.Helper()
	t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d", f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse)
	if f.unrelated != unrelated || f.unjustifiedRecovery != recovery || f.unauthorized != unauthorized || f.staleReuse != stale {
		t.Fatalf("want unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d: %v", unrelated, recovery, unauthorized, stale, f.notes)
	}
}

// The checker detects each forbidden outcome injected into BORI's claims (or
// into the harness's record of approvals), in exactly its own class, and
// counts nothing on the untampered claims.
func TestConformanceCheckerControls(t *testing.T) {
	cases := []struct {
		name, pack, scenario string
		tamper               func(e *confEnv, cl claims)
		want                 [4]int // unrelated, unjustifiedRecovery, unauthorized, staleReuse
	}{
		{"control: untampered local failure", "simple-http-service", "local-failure-is-contained", nil, [4]int{}},
		{"unrelated capability contaminated", "simple-http-service", "local-failure-is-contained",
			func(_ *confEnv, cl claims) { setCap(cl, "web", "export-metrics", "UNAVAILABLE") }, [4]int{1, 0, 0, 0}},
		{"independent capability contaminated by a dependency", "jumi-artifact-handoff", "resolver-down",
			func(_ *confEnv, cl claims) { setCap(cl, "jumi", "query-runs", "UNKNOWN") }, [4]int{1, 0, 0, 0}},
		{"control: untampered dependency failure", "jumi-artifact-handoff", "resolver-down", nil, [4]int{}},
		{"AVAILABLE on stale evidence", "simple-http-service", "evidence-stale-is-unknown",
			func(_ *confEnv, cl claims) { setCap(cl, "web", "serve-requests", "AVAILABLE") }, [4]int{0, 0, 0, 1}},
		{"AVAILABLE through an UNKNOWN dependency", "jumi-artifact-handoff", "resolver-evidence-outage",
			func(_ *confEnv, cl claims) { setCap(cl, "jumi", "complete-runs", "AVAILABLE") }, [4]int{0, 0, 0, 1}},
		{"AVAILABLE on a replaced workload's evidence", "stateful-single-writer", "workload-recreated",
			// The writer facts are bound to the old workload UID only:
			// claiming AVAILABLE reuses evidence of the replaced identity.
			func(_ *confEnv, cl claims) { setCap(cl, "db", "accept-writes", "AVAILABLE") }, [4]int{0, 0, 0, 1}},
		{"control: untampered not-recovered action", "simple-http-service", "action-succeeds-without-recovery", nil, [4]int{}},
		{"Recovered while the capability is still down", "simple-http-service", "action-succeeds-without-recovery",
			func(_ *confEnv, cl claims) {
				st := cl["web"]
				for i := range st.Interaction.Actions {
					st.Interaction.Actions[i].Recovery = "Recovered"
				}
				cl["web"] = st
			}, [4]int{0, 1, 0, 0}},
		{"control: untampered recovered action", "simple-http-service", "action-succeeds-and-recovers", nil, [4]int{}},
		{"execution without an approval", "jumi-artifact-handoff", "restart-without-registry-recovery",
			func(e *confEnv, _ claims) { e.approved = map[string]bool{} }, [4]int{0, 0, 1, 0}},
		{"execution of an undeclared action", "replicated-service", "pods-ready-is-not-recovery",
			func(e *confEnv, _ claims) { e.p.profile = nil }, [4]int{0, 0, 1, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := runTo(t, c.pack, c.scenario)
			cl := e.claims()
			if c.tamper != nil {
				c.tamper(e, cl)
			}
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, c.want[0], c.want[1], c.want[2], c.want[3])
		})
	}
}

// The harness and checker know no product: no product name, no pack name,
// no scenario name appears in their code, so no branch can key on one.
func TestConformanceHarnessIsProductNeutral(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`(?i)jumi`), regexp.MustCompile(`(?i)artifact-handoff`), regexp.MustCompile(`(?i)\btori\b`),
		regexp.MustCompile(`(?i)heainseo\.dev`), regexp.MustCompile(`(?i)nodesentinel`),
	}
	packs, err := filepath.Glob(filepath.Join(packsDir, "*"))
	if err != nil || len(packs) == 0 {
		t.Fatal("no packs")
	}
	for _, f := range []string{"conformance_test.go", "conformance_check_test.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, re := range banned {
			if loc := re.FindStringIndex(src); loc != nil {
				t.Errorf("%s names a product: %q", f, src[loc[0]:loc[1]])
			}
		}
		for _, p := range packs {
			name := filepath.Base(p)
			if strings.Contains(src, `"`+name) {
				t.Errorf("%s names pack %s", f, name)
			}
		}
	}
}

// Every pack's contracts are valid and every dependency pin names the exact
// spec digest of the contract it binds.
func TestConformancePacksAreValid(t *testing.T) {
	for _, p := range loadPacks(t) {
		digests := map[string]string{}
		for _, o := range p.objects {
			if c, ok := o.(*opsv1.OperationalContract); ok {
				st := opswire.ContractStatus(c)
				if !st.Valid {
					t.Errorf("%s: contract %s invalid: %v", p.Name, c.Name, st.ValidationErrors)
				}
				digests[c.Name] = opswire.SpecDigest(&c.Spec)
			}
		}
		for _, o := range p.objects {
			if tg, ok := o.(*opsv1.OperationalTarget); ok {
				for _, db := range tg.Spec.DependencyBindings {
					if digests[db.Contract.Name] != db.Contract.SpecDigest {
						t.Errorf("%s: %s pins %s at %s, current %s", p.Name, tg.Name, db.Contract.Name, db.Contract.SpecDigest, digests[db.Contract.Name])
					}
				}
			}
		}
		if len(p.Scenarios) == 0 {
			t.Errorf("%s has no scenarios", p.Name)
		}
	}
}
