package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/investigate"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// interactionSchema is the generated CRD schema of status.interaction.
func loadTargetCRD(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../config/crd/ops.bori.dev_operationaltargets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatal(err)
	}
	return crd
}

func interactionSchema(t *testing.T) map[string]any {
	t.Helper()
	crd := loadTargetCRD(t)
	node := crd["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"]
	for _, p := range []string{"status", "interaction"} {
		node = node.(map[string]any)["properties"].(map[string]any)[p]
	}
	return node.(map[string]any)
}

// validate checks v against the bounds the API server enforces for this
// schema: maxLength (characters), maxItems, enum and required. It returns
// every violation with its path.
func validate(path string, schema map[string]any, v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for _, r := range asStrings(schema["required"]) {
			if _, ok := x[r]; !ok {
				out = append(out, path+"."+r+": required")
			}
		}
		for k, sub := range x {
			ps, ok := props[k].(map[string]any)
			if !ok {
				out = append(out, path+"."+k+": not in schema")
				continue
			}
			out = append(out, validate(path+"."+k, ps, sub)...)
		}
	case []any:
		if m, ok := schema["maxItems"].(float64); ok && float64(len(x)) > m {
			out = append(out, fmt.Sprintf("%s: %d items > %v", path, len(x), m))
		}
		items, _ := schema["items"].(map[string]any)
		for i, e := range x {
			out = append(out, validate(fmt.Sprintf("%s[%d]", path, i), items, e)...)
		}
	case string:
		if m, ok := schema["maxLength"].(float64); ok && float64(utf8.RuneCountInString(x)) > m {
			out = append(out, fmt.Sprintf("%s: length %d > %v", path, utf8.RuneCountInString(x), m))
		}
		if en := asStrings(schema["enum"]); len(en) > 0 && !contains(en, x) {
			out = append(out, path+": not in enum")
		}
	}
	return out
}

func asStrings(v any) []string {
	var out []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			out = append(out, x.(string))
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func validateSummary(t *testing.T, s *opsv1.InteractionStatus) {
	t.Helper()
	b, _ := json.Marshal(s)
	var v any
	_ = json.Unmarshal(b, &v)
	if errs := validate("status.interaction", interactionSchema(t), v); len(errs) > 0 {
		t.Fatalf("summary rejected by the CRD schema:\n%s", strings.Join(errs, "\n"))
	}
}

// constProvider answers every request with one integer, as of now.
type constProvider struct{ v int64 }

func (p constProvider) Observe(_ context.Context, _ providers.Request) providers.Result {
	return providers.Result{Value: operations.Int(p.v), ObservedAt: at, EvidenceRef: "k8s:max"}
}

type oneLookup struct{ p providers.Provider }

func (l oneLookup) Lookup(operations.ProviderIdentity) (providers.Provider, bool) { return l.p, true }

// Codex r4219279473 regression: a contract with maximum-length legal names,
// investigated by the real O3 planner, yields candidate IDs up to 528
// characters; the summary carrying them must satisfy the CRD schema, so the
// status can be written.
func TestMaxLengthLegalNamesFitTheSchema(t *testing.T) {
	label := func(c byte) string { return strings.Repeat(string(c), 63) }
	domain := strings.Join([]string{label('a'), label('b'), label('c'), strings.Repeat("d", 61)}, ".") // 253
	if len(domain) != 253 {
		t.Fatalf("domain %d", len(domain))
	}
	env, slot := label('e'), label('s')
	five := int64(5)
	ct := opsv1.CapabilityType{Domain: domain, Name: label('n'), Revision: label('r')}
	spec := opsv1.OperationalContractSpec{
		Assertions:   []opsv1.AssertionSlot{intSlot(slot)},
		Capabilities: []opsv1.Capability{{Type: ct, Requirements: []opsv1.Requirement{{Envelope: env, OnUnmet: "DEGRADED"}}}},
		Envelopes: []opsv1.Envelope{{Name: env, Class: "Recommended", Requirements: []opsv1.Predicate{
			{Assertion: slot, Operator: "Gte", Operand: &opsv1.Operand{Integer: &five}},
		}}},
	}
	c := contract("apps", "max-v1", "c-max", spec)
	tg := target("apps", "max", "t-max", "max-v1", bind(slot, "kube-status"))
	res := map[types.NamespacedName]opswire.Resolution{{Namespace: "apps", Name: "max"}: {UID: "w-max"}}
	b := opswire.Build(opswire.Input{At: at, Contracts: []opsv1.OperationalContract{c}, Targets: []opsv1.OperationalTarget{tg}, Resolutions: res})

	// Actual O3: requests from the snapshot, one real planner run.
	subjects := map[string]providers.Subject{"t-max": {Namespace: "apps", APIVersion: "apps/v1", Kind: "Deployment", Name: "max", ResolvedUID: "w-max"}}
	qs := investigate.Requests(b.Snapshot, subjects, oneLookup{constProvider{1}})
	iv := investigate.New(investigate.ReferenceLimits(), func() time.Time { return at })
	iv.Run(context.Background(), b.Snapshot, qs)
	b.Snapshot.Observations = iv.Observations()
	a, err := operations.Evaluate(b.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ta, _ := a.Target("t-max")
	ep, ok := iv.Current(ta, qs["t-max"])
	if !ok {
		t.Fatal("no episode for the current identity")
	}
	inv := &Investigation{Kind: string(ep.Kind), Outcome: string(ep.State)}
	longest := 0
	for _, cd := range ep.Candidates {
		inv.Candidates = append(inv.Candidates, Candidate{ID: cd.ID, Kind: string(cd.Kind), Ref: cd.Ref, Status: string(cd.Status)})
		if len(cd.ID) > longest {
			longest = len(cd.ID)
		}
	}
	if longest != 528 {
		t.Fatalf("longest candidate ID %d, want the 528-character maximum", longest)
	}

	st := opswire.Project(&tg, b, a)
	s := Project(Input{Namespace: "apps", Name: "max", UID: "t-max", Status: st, Investigation: inv}, nil)
	if len(s.Affected) != 1 || s.Affected[0].State != "DEGRADED" {
		t.Fatalf("affected %+v", s.Affected)
	}
	found := false
	for _, cd := range s.CauseCandidates {
		found = found || len(cd.ID) == 528
	}
	if !found {
		t.Fatalf("528-character candidate not shown (ids must be copied unchanged): %+v", s.CauseCandidates)
	}
	validateSummary(t, s)
}

// Every pure scenario's summary also satisfies the schema.
func TestScenarioSummariesFitTheSchema(t *testing.T) {
	w := yDown()
	w.observations[0] = w.fact("a", "up", operations.Bool(false), fresh)
	w.profile = &Profile{Revision: "p1", Responses: []Response{resp("remount", "storage-oncall", "persist"), resp("reschedule", "", "persist")}}
	for _, s := range w.reconcile().summary {
		validateSummary(t, s)
	}
}
