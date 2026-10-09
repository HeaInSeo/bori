package v1alpha1

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// These tests pin the safety-relevant parts of the generated CRD schemas:
// they fail if a regeneration drops the immutability rule, widens an enum or
// introduces a Secret/action reference. Live API-server enforcement is
// verified separately against a real cluster.

func loadCRD(t *testing.T, plural string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "ops.bori.dev_"+plural+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func at(t *testing.T, m any, path ...string) any {
	t.Helper()
	for _, p := range path {
		switch v := m.(type) {
		case map[string]any:
			m = v[p]
		case []any:
			if p != "0" {
				t.Fatalf("unsupported index %q", p)
			}
			m = v[0]
		default:
			t.Fatalf("path %v: %q not reachable", path, p)
		}
		if m == nil {
			t.Fatalf("path %v: %q missing", path, p)
		}
	}
	return m
}

func schemaOf(t *testing.T, plural string) map[string]any {
	crd := loadCRD(t, plural)
	if g := at(t, crd, "spec", "group"); g != "ops.bori.dev" {
		t.Fatalf("group %v", g)
	}
	if s := at(t, crd, "spec", "scope"); s != "Namespaced" {
		t.Fatalf("scope %v", s)
	}
	if v := at(t, crd, "spec", "versions", "0", "name"); v != "v1alpha1" {
		t.Fatalf("version %v", v)
	}
	return at(t, crd, "spec", "versions", "0", "schema", "openAPIV3Schema").(map[string]any)
}

func rules(t *testing.T, node any) []string {
	var out []string
	if vs, ok := node.(map[string]any)["x-kubernetes-validations"].([]any); ok {
		for _, v := range vs {
			out = append(out, v.(map[string]any)["rule"].(string))
		}
	}
	return out
}

func enum(t *testing.T, node any) []string {
	var out []string
	for _, v := range at(t, node, "enum").([]any) {
		out = append(out, v.(string))
	}
	return out
}

func TestContractSpecIsImmutable(t *testing.T) {
	s := schemaOf(t, "operationalcontracts")
	spec := at(t, s, "properties", "spec")
	if r := rules(t, spec); !reflect.DeepEqual(r, []string{"self == oldSelf"}) {
		t.Fatalf("spec validations %v", r)
	}
	req := at(t, s, "required").([]any)
	if !strings.Contains(strings.Join(toStrings(req), ","), "spec") {
		t.Fatal("spec must be required for the immutability rule to apply")
	}
}

func TestContractGrammarIsBounded(t *testing.T) {
	s := schemaOf(t, "operationalcontracts")
	caps := at(t, s, "properties", "spec", "properties", "capabilities", "items")
	reqItem := at(t, caps, "properties", "requirements", "items")
	pred := at(t, reqItem, "properties", "predicate")
	if got := enum(t, at(t, pred, "properties", "operator")); !reflect.DeepEqual(got, []string{"IsTrue", "IsFalse", "Eq", "NotEq", "Gte", "Lte"}) {
		t.Fatalf("operators %v", got)
	}
	if got := enum(t, at(t, reqItem, "properties", "onUnmet")); !reflect.DeepEqual(got, []string{"DEGRADED", "UNAVAILABLE"}) {
		t.Fatalf("onUnmet %v", got)
	}
	if len(rules(t, reqItem)) != 1 || len(rules(t, at(t, pred, "properties", "operand"))) != 1 {
		t.Fatal("exactly-one requirement/operand rules missing")
	}
	slot := at(t, s, "properties", "spec", "properties", "assertions", "items")
	if got := enum(t, at(t, slot, "properties", "type")); !reflect.DeepEqual(got, []string{"Boolean", "Integer", "String"}) {
		t.Fatalf("value types %v", got)
	}
}

func TestGrantIsTypedAndExact(t *testing.T) {
	s := schemaOf(t, "operationalreferencegrants")
	spec := at(t, s, "properties", "spec", "properties")
	if got := enum(t, at(t, spec, "to", "items", "properties", "type")); !reflect.DeepEqual(got, []string{"Dependency", "EvidenceProvider"}) {
		t.Fatalf("grant types %v (Secret/action must not be grantable)", got)
	}
	if got := enum(t, at(t, spec, "from", "items", "properties", "kind")); !reflect.DeepEqual(got, []string{"OperationalTarget"}) {
		t.Fatalf("from kinds %v", got)
	}
	for _, p := range []any{at(t, spec, "from", "items", "properties", "namespace"), at(t, spec, "to", "items", "properties", "name")} {
		re := regexp.MustCompile(at(t, p, "pattern").(string))
		for _, broad := range []string{"*", "", "a*", "*.*", "ns/*"} {
			if re.MatchString(broad) {
				t.Fatalf("pattern %s accepts broad value %q", re, broad)
			}
		}
	}
	for _, field := range []string{"selector", "namespaceSelector"} {
		if strings.Contains(strings.ToLower(dump(spec)), strings.ToLower(field)) {
			t.Fatalf("grant schema has %s", field)
		}
	}
}

func TestTargetContractRefIsSameNamespace(t *testing.T) {
	s := schemaOf(t, "operationaltargets")
	ref := at(t, s, "properties", "spec", "properties", "contractRef", "properties").(map[string]any)
	if len(ref) != 1 || ref["name"] == nil {
		t.Fatalf("contractRef properties %v; only a same-namespace name is allowed", ref)
	}
}

func TestNoSecretOrActionReferences(t *testing.T) {
	for _, plural := range []string{"operationalcontracts", "operationaltargets", "operationalreferencegrants"} {
		props := propertyNames(schemaOf(t, plural))
		for _, p := range props {
			// "interaction" (the O4 status summary) is not an action reference.
			l := strings.ReplaceAll(strings.ToLower(p), "interaction", "")
			if strings.Contains(l, "secret") || strings.Contains(l, "action") || strings.Contains(l, "credential") {
				t.Errorf("%s: property %q", plural, p)
			}
		}
	}
}

func propertyNames(n any) []string {
	var out []string
	switch v := n.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for k, sub := range props {
				out = append(out, k)
				out = append(out, propertyNames(sub)...)
			}
		}
		if items, ok := v["items"]; ok {
			out = append(out, propertyNames(items)...)
		}
	}
	return out
}

func toStrings(xs []any) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i], _ = x.(string)
	}
	return out
}

func dump(v any) string {
	b, _ := yaml.Marshal(v)
	return string(b)
}

// The wire must not name product applications or scenarios.
func TestWireIsAppNeutral(t *testing.T) {
	banned := regexp.MustCompile(`(?i)\bjumi\b|artifact-handoff|\btori\b|nodesentinel|nodevault|genomic|\bS\d{2}b?\b`)
	files := []string{}
	for _, g := range []string{"*.go", "../../../pkg/opswire/*.go", "../../../controllers/operational_reconciler.go", "../../../config/crd/ops.bori.dev_*.yaml"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	if len(files) < 8 {
		t.Fatalf("scanned only %v", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := banned.FindIndex(b); loc != nil {
			t.Errorf("%s contains %q", f, b[loc[0]:loc[1]])
		}
	}
}

// The O4 interaction summary is status-only, its level vocabulary is exactly
// the four levels, and every list and text is bounded.
func TestInteractionSummaryIsBoundedStatus(t *testing.T) {
	s := schemaOf(t, "operationaltargets")
	for _, p := range propertyNames(at(t, s, "properties", "spec")) {
		if strings.Contains(strings.ToLower(p), "interaction") || p == "responses" || p == "level" {
			t.Fatalf("spec carries interaction field %q", p)
		}
	}
	in := at(t, s, "properties", "status", "properties", "interaction")
	props := at(t, in, "properties").(map[string]any)
	if got := enum(t, props["level"]); !reflect.DeepEqual(got, []string{"NO_ACTION", "AWARENESS", "DECISION_REQUIRED", "IMMEDIATE_INTERVENTION"}) {
		t.Fatalf("levels %v", got)
	}
	if req := toStrings(at(t, in, "required").([]any)); !reflect.DeepEqual(req, []string{"level", "summary"}) {
		t.Fatalf("required %v", req)
	}
	if at(t, props["summary"], "maxLength") != float64(256) {
		t.Fatal("summary unbounded")
	}
	for name, p := range props {
		m := p.(map[string]any)
		if m["type"] == "array" && m["maxItems"] == nil {
			t.Errorf("interaction.%s has no maxItems", name)
		}
		if m["type"] == "string" && m["maxLength"] == nil && m["enum"] == nil {
			t.Errorf("interaction.%s has no maxLength", name)
		}
	}
	resp := at(t, props["responses"], "items", "properties").(map[string]any)
	for _, banned := range []string{"command", "endpoint", "url", "secretRef", "approve", "execute"} {
		if resp[banned] != nil {
			t.Fatalf("response carries %q", banned)
		}
	}
}
