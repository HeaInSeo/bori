package main

import (
	"fmt"
	"sort"

	"github.com/HeaInSeo/kube-slint/pkg/slint"
	"github.com/HeaInSeo/kube-slint/pkg/slo/spec"
)

// targetProfile is the compile-time measurement profile for one verification
// target. It is the only source of measurement semantics on the canonical
// verify path: bori supplies the target, smoke and artifact context, and
// kube-slint owns fetching, selector resolution, computation and the slo.v4
// summary schema.
//
// Profiles are keyed by target name and are not loaded from files, derived
// from a scrape, or filled in from a global default. Adding a target or
// changing a profile's intents needs a new design disposition.
type targetProfile struct {
	// Target is the verification target name the profile is keyed by.
	Target string
	// SourceConfigID is the TrustContract source identity. It must change
	// whenever the endpoint or any intent changes meaning.
	SourceConfigID string
	// Namespace and MetricsService locate the target's metrics Service.
	Namespace      string
	MetricsService string
	// ServiceURLFormat is the kube-slint metrics URL template.
	ServiceURLFormat string
	// Specs returns a fresh copy of the target's predeclared SLI intents.
	Specs func() []spec.SLISpec
}

// Intent IDs of the bori-operator VM profile. The set is closed: the profile
// is rejected unless it declares exactly these four.
const (
	intentReconcileDelta       = "reconcile-delta"
	intentReconcileErrorsDelta = "reconcile-errors-delta"
	intentWorkqueueDepthEnd    = "workqueue-depth-end"
	intentRESTErrorsDelta      = "rest-errors-delta"
)

// boriOperatorController is the controller whose series the profile selects.
const boriOperatorController = "boridataplane"

var boriOperatorIntents = []string{
	intentReconcileDelta,
	intentReconcileErrorsDelta,
	intentWorkqueueDepthEnd,
	intentRESTErrorsDelta,
}

// boriOperatorSpecs is the bori-operator VM profile. Series labels follow the
// controller-runtime metrics bori-operator exports:
//
//   - controller_runtime_reconcile_total{controller,result}: summed over result.
//   - controller_runtime_reconcile_errors_total{controller}: one series.
//   - workqueue_depth{name,controller,priority}: one series per queue name.
//   - rest_client_requests_total{code,method,host}: summed over every 5xx code,
//     method and host. A run with no 5xx series is 0 only when the family is
//     present in that snapshot.
func boriOperatorSpecs() []spec.SLISpec {
	return []spec.SLISpec{
		{
			ID:    intentReconcileDelta,
			Title: "BoriDataPlane reconcile delta",
			Unit:  "count",
			Kind:  "delta_counter",
			Inputs: []spec.MetricRef{
				spec.SelectMetric("controller_runtime_reconcile_total", spec.AggregateSum,
					spec.LabelEq("controller", boriOperatorController)),
			},
			Compute: spec.ComputeSpec{Mode: spec.ComputeDelta},
		},
		{
			ID:    intentReconcileErrorsDelta,
			Title: "BoriDataPlane reconcile errors delta",
			Unit:  "count",
			Kind:  "delta_counter",
			Inputs: []spec.MetricRef{
				spec.SelectMetric("controller_runtime_reconcile_errors_total", spec.AggregateNone,
					spec.LabelEq("controller", boriOperatorController)),
			},
			Compute: spec.ComputeSpec{Mode: spec.ComputeDelta},
		},
		{
			ID:    intentWorkqueueDepthEnd,
			Title: "BoriDataPlane workqueue depth at end",
			Unit:  "count",
			Kind:  "gauge",
			Inputs: []spec.MetricRef{
				spec.SelectMetric("workqueue_depth", spec.AggregateNone,
					spec.LabelEq("name", boriOperatorController)),
			},
			Compute: spec.ComputeSpec{Mode: spec.ComputeEnd},
		},
		{
			ID:    intentRESTErrorsDelta,
			Title: "REST client 5xx delta",
			Unit:  "count",
			Kind:  "delta_counter",
			Inputs: []spec.MetricRef{
				spec.SelectMetric("rest_client_requests_total", spec.AggregateSum,
					spec.LabelRegexp("code", "5[0-9][0-9]")).
					WithEmptyMatch(spec.EmptyMatchZeroIfFamilyPresent),
			},
			Compute: spec.ComputeSpec{Mode: spec.ComputeDelta},
		},
	}
}

// targetProfiles holds every compile-time profile. The current bori-operator
// VM target is the only one.
var targetProfiles = map[string]targetProfile{
	"bori-operator": {
		Target:           "bori-operator",
		SourceConfigID:   "bori/cmd-bori/target-profile/bori-operator/v1",
		Namespace:        "bori-system",
		MetricsService:   "bori-operator-metrics",
		ServiceURLFormat: slint.ServiceURLHTTP,
		Specs:            boriOperatorSpecs,
	},
}

// requiredIntents is the closed intent set per target.
var requiredIntents = map[string][]string{
	"bori-operator": boriOperatorIntents,
}

// lookupProfile returns the validated profile for target. An unknown target,
// or a profile whose intent set is not exactly the required one, is an error.
func lookupProfile(target string) (targetProfile, error) {
	p, ok := targetProfiles[target]
	if !ok {
		return targetProfile{}, fmt.Errorf("no compile-time measurement profile for target %q", target)
	}
	if err := validateProfile(p, requiredIntents[target]); err != nil {
		return targetProfile{}, err
	}
	return p, nil
}

// validateProfile checks the profile's endpoint and TrustContract source
// fields, that its specs are exactly the required intents, and that every
// selector input is well formed for its compute mode.
func validateProfile(p targetProfile, required []string) error {
	if p.SourceConfigID == "" || p.Namespace == "" || p.MetricsService == "" || p.ServiceURLFormat == "" || p.Specs == nil {
		return fmt.Errorf("target %q: incomplete measurement profile", p.Target)
	}
	if len(required) == 0 {
		return fmt.Errorf("target %q: no required intent set", p.Target)
	}
	specs := p.Specs()
	got := make([]string, 0, len(specs))
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.ID] {
			return fmt.Errorf("target %q: duplicate intent %q", p.Target, s.ID)
		}
		seen[s.ID] = true
		got = append(got, s.ID)
		if len(s.Inputs) == 0 {
			return fmt.Errorf("target %q: intent %q has no inputs", p.Target, s.ID)
		}
		for _, in := range s.Inputs {
			if in.Selector == nil {
				return fmt.Errorf("target %q: intent %q: input %q is not a selector", p.Target, s.ID, in.Key)
			}
		}
		if err := s.ValidateSelectors(); err != nil {
			return fmt.Errorf("target %q: %w", p.Target, err)
		}
	}
	want := append([]string(nil), required...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		return fmt.Errorf("target %q: intent set %v, want exactly %v", p.Target, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("target %q: intent set %v, want exactly %v", p.Target, got, want)
		}
	}
	return nil
}
