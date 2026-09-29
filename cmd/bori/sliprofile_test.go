package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/HeaInSeo/kube-slint/pkg/slo/spec"
)

func TestLookupProfile_BoriOperatorIsExactFourIntentSet(t *testing.T) {
	p, err := lookupProfile("bori-operator")
	if err != nil {
		t.Fatalf("lookupProfile: %v", err)
	}
	var ids []string
	for _, s := range p.Specs() {
		ids = append(ids, s.ID)
		for _, in := range s.Inputs {
			if in.Selector == nil {
				t.Errorf("%s: input %q is not a selector", s.ID, in.Key)
			}
		}
	}
	sort.Strings(ids)
	want := []string{"reconcile-delta", "reconcile-errors-delta", "rest-errors-delta", "workqueue-depth-end"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("intents = %v, want %v", ids, want)
	}
	if p.Namespace != "bori-system" || p.MetricsService != "bori-operator-metrics" {
		t.Errorf("endpoint = %s/%s", p.Namespace, p.MetricsService)
	}
	if p.SourceConfigID == "" {
		t.Error("SourceConfigID is empty")
	}
}

func TestLookupProfile_UnknownTargetFailsClosed(t *testing.T) {
	for _, target := range []string{"", "jumi", "Bori-Operator", "bori-devspace"} {
		if _, err := lookupProfile(target); err == nil {
			t.Errorf("lookupProfile(%q): want error", target)
		}
	}
}

// TestBoriOperatorSpecs_Semantics pins the selector, aggregation and compute
// mode of each intent so a change needs a deliberate test edit.
func TestBoriOperatorSpecs_Semantics(t *testing.T) {
	want := map[string]struct {
		key  string
		mode spec.ComputeMode
	}{
		"reconcile-delta":        {`kube-slint.select/v1 sum controller_runtime_reconcile_total{controller="boridataplane"}`, spec.ComputeDelta},
		"reconcile-errors-delta": {`kube-slint.select/v1 one controller_runtime_reconcile_errors_total{controller="boridataplane"}`, spec.ComputeDelta},
		"workqueue-depth-end":    {`kube-slint.select/v1 one workqueue_depth{name="boridataplane"}`, spec.ComputeEnd},
		"rest-errors-delta":      {`kube-slint.select/v1 sum empty=zero_if_family_present rest_client_requests_total{code=~"5[0-9][0-9]"}`, spec.ComputeDelta},
	}
	for _, s := range boriOperatorSpecs() {
		w, ok := want[s.ID]
		if !ok {
			t.Fatalf("unexpected intent %q", s.ID)
		}
		if len(s.Inputs) != 1 || s.Inputs[0].Key != w.key {
			t.Errorf("%s: inputs = %+v, want key %s", s.ID, s.Inputs, w.key)
		}
		if s.Compute.Mode != w.mode {
			t.Errorf("%s: mode = %s, want %s", s.ID, s.Compute.Mode, w.mode)
		}
		if s.Judge != nil {
			t.Errorf("%s: judge is set; thresholds are not part of the measurement profile", s.ID)
		}
	}
}

func TestValidateProfile_RejectsWrongIntentSets(t *testing.T) {
	base := targetProfiles["bori-operator"]
	withSpecs := func(f func([]spec.SLISpec) []spec.SLISpec) targetProfile {
		p := base
		p.Specs = func() []spec.SLISpec { return f(boriOperatorSpecs()) }
		return p
	}
	cases := map[string]targetProfile{
		"missing intent": withSpecs(func(s []spec.SLISpec) []spec.SLISpec { return s[:3] }),
		"extra intent": withSpecs(func(s []spec.SLISpec) []spec.SLISpec {
			extra := s[0]
			extra.ID = "extra"
			return append(s, extra)
		}),
		"duplicate intent": withSpecs(func(s []spec.SLISpec) []spec.SLISpec { return append(s[:3], s[0]) }),
		"exact-key input": withSpecs(func(s []spec.SLISpec) []spec.SLISpec {
			s[1].Inputs = []spec.MetricRef{spec.PromMetric("controller_runtime_reconcile_errors_total", map[string]string{"controller": "boridataplane"})}
			return s
		}),
		"invalid selector": withSpecs(func(s []spec.SLISpec) []spec.SLISpec {
			s[2].Inputs = []spec.MetricRef{spec.SelectMetric("workqueue_depth", spec.AggregateSum,
				spec.LabelEq("name", "boridataplane")).WithEmptyMatch(spec.EmptyMatchZeroIfFamilyPresent)}
			return s // zero_if_family_present is not valid for ComputeEnd
		}),
		"no source config id": func() targetProfile { p := base; p.SourceConfigID = ""; return p }(),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateProfile(p, boriOperatorIntents); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if err := validateProfile(base, boriOperatorIntents); err != nil {
		t.Fatalf("base profile: %v", err)
	}
}
