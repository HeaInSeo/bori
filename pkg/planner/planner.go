// Package planner loads a release + environment and produces a deploy plan.
// It validates namespace policy and selects the correct adapter for each component.
package planner

import (
	"fmt"
	"time"

	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/model"
)

// Planner builds BoriDeployPlans from release + environment definitions.
type Planner struct {
	// BoriRoot is the absolute path to the bori repo root.
	BoriRoot string
}

// New returns a Planner rooted at boriRoot.
func New(boriRoot string) *Planner {
	return &Planner{BoriRoot: boriRoot}
}

// Plan loads the named release and environment and returns a deploy plan.
// Namespace violations are recorded in Plan.Violations; they do not cause an error.
func (p *Planner) Plan(runID, releaseName, envName string) (*artifact.Plan, error) {
	rel, err := model.LoadReleaseByName(p.BoriRoot, releaseName)
	if err != nil {
		return nil, fmt.Errorf("load release %q: %w", releaseName, err)
	}
	env, err := model.LoadEnvironmentByName(p.BoriRoot, envName)
	if err != nil {
		return nil, fmt.Errorf("load environment %q: %w", envName, err)
	}

	plan := &artifact.Plan{
		SchemaVersion: "bori.plan.v1",
		RunID:         runID,
		Release:       releaseName,
		Environment:   envName,
		CreatedAt:     time.Now().UTC(),
	}

	allowedNS := make(map[string]bool, len(env.NamespacePolicy.Allowed))
	for _, ns := range env.NamespacePolicy.Allowed {
		allowedNS[ns] = true
	}

	for _, ref := range rel.Components {
		comp, err := model.LoadComponentByName(p.BoriRoot, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("load component %q: %w", ref.Name, err)
		}

		ns := defaultNamespace(comp.Name)
		adapterName := comp.Deploy.Adapter
		if adapterName == "" {
			adapterName = "devspace"
		}

		cp := artifact.ComponentPlan{
			Name:      comp.Name,
			Version:   ref.Version,
			Adapter:   adapterName,
			Namespace: ns,
			ImageRef:  comp.Image.Ref,
			Action:    "deploy",
		}

		if !allowedNS[ns] {
			cp.Action = "violation"
			cp.Message = fmt.Sprintf("namespace %q not in environment allowed list", ns)
			plan.Violations = append(plan.Violations, fmt.Sprintf("%s: %s", comp.Name, cp.Message))
		}

		plan.Components = append(plan.Components, cp)
	}

	return plan, nil
}

// defaultNamespace returns the conventional namespace for a component.
func defaultNamespace(componentName string) string {
	return componentName + "-system"
}
