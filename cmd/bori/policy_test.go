package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/HeaInSeo/kube-slint/pkg/gate"
	"github.com/HeaInSeo/kube-slint/pkg/slo/summary"
	"gopkg.in/yaml.v3"
)

// vmPolicyPath is the gate policy hack/test-vm-integration.sh passes to
// `bori verify --target bori-operator`.
const vmPolicyPath = "../../test/e2e/.slint/policy.yaml"

// TestVMPolicy_NamesExactlyTheProfileIntents keeps the policy IDs and the
// producer IDs from drifting apart: every profile intent is named by exactly
// one threshold or informational entry, and nothing else is named.
func TestVMPolicy_NamesExactlyTheProfileIntents(t *testing.T) {
	data, err := os.ReadFile(vmPolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	var p gate.Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.SchemaVersion != "slint.policy.v2" {
		t.Fatalf("schema_version = %q, want slint.policy.v2", p.SchemaVersion)
	}
	var named []string
	for _, r := range p.Thresholds {
		named = append(named, r.Metric)
	}
	named = append(named, p.Coverage.Informational...)
	sort.Strings(named)
	want := slices.Clone(boriOperatorIntents)
	sort.Strings(want)
	if !slices.Equal(named, want) {
		t.Fatalf("policy names %v, want exactly %v", named, want)
	}
}

// producedSummary runs the real kube-slint producer for the bori-operator
// profile over one start/end snapshot pair and returns the summary path.
func producedSummary(t *testing.T, reconcileStart, reconcileEnd float64) string {
	t.Helper()
	useFetcher(t, &seriesFetcher{
		start: operatorSeries(reconcileStart, 0, nil),
		end:   operatorSeries(reconcileEnd, 0, nil),
	})
	path, err := measureAround(context.Background(), testRequest(t), noSmoke)
	if err != nil {
		t.Fatalf("measureAround: %v", err)
	}
	return path
}

func evaluateVMPolicy(t *testing.T, summaryPath string) *gate.Summary {
	t.Helper()
	out := gate.Evaluate(gate.Request{MeasurementPath: summaryPath, PolicyPath: vmPolicyPath})
	if out.PolicyStatus != "ok" || out.MeasurementStatus != "ok" {
		t.Fatalf("policy=%s measurement=%s reasons=%v warnings=%v",
			out.PolicyStatus, out.MeasurementStatus, out.Reasons, out.PolicyWarnings)
	}
	return out
}

func findCheck(out *gate.Summary, name string) *gate.Check {
	for i := range out.Checks {
		if out.Checks[i].Name == name {
			return &out.Checks[i]
		}
	}
	return nil
}

func TestVMPolicy_GradesProducedSummary(t *testing.T) {
	out := evaluateVMPolicy(t, producedSummary(t, 0, 2))
	if out.GateResult != gate.GatePass {
		t.Fatalf("gate = %s, reasons = %v, checks = %+v", out.GateResult, out.Reasons, out.Checks)
	}
	c := findCheck(out, "reconcile-delta-min")
	if c == nil || c.Status != "pass" || c.Observed != 2.0 {
		t.Fatalf("reconcile-delta-min check = %+v", c)
	}
	if slices.Contains(out.Reasons, "COVERAGE_GAP") {
		t.Fatalf("a produced intent is not covered: %+v", out.Checks)
	}
}

// With no reconcile in the window the carried-over rule grades WARN, not FAIL.
func TestVMPolicy_NoReconcileWarns(t *testing.T) {
	out := evaluateVMPolicy(t, producedSummary(t, 3, 3))
	if out.GateResult != gate.GateWarn || !slices.Contains(out.Reasons, "THRESHOLD_MISS") {
		t.Fatalf("gate = %s, reasons = %v", out.GateResult, out.Reasons)
	}
}

// A summary whose IDs do not match the policy (the pre-fix legacy names) must
// not pass: the threshold cannot grade and every measured SLI is a coverage gap.
func TestVMPolicy_MismatchedIDsDoNotPass(t *testing.T) {
	path := producedSummary(t, 0, 2)
	sum, err := summary.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sum.Results {
		sum.Results[i].ID = "bori_" + sum.Results[i].ID
	}
	data, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(t.TempDir(), "sli-summary.json")
	if err := os.WriteFile(renamed, data, 0o644); err != nil {
		t.Fatal(err)
	}

	out := evaluateVMPolicy(t, renamed)
	if out.GateResult != gate.GateFail || !slices.Contains(out.Reasons, "COVERAGE_GAP") {
		t.Fatalf("gate = %s, reasons = %v", out.GateResult, out.Reasons)
	}
	if c := findCheck(out, "reconcile-delta-min"); c == nil || c.Status != "no_grade" {
		t.Fatalf("reconcile-delta-min check = %+v", c)
	}
}
