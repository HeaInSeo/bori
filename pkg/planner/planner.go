// Package planner loads a release + environment and produces a deploy plan.
// It validates namespace policy, checks compatibility, and sorts components
// in dependency order.
package planner

import (
	"fmt"
	"strings"
	"time"

	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/model"
	"github.com/HeaInSeo/bori/pkg/release"
)

// Planner builds BoriDeployPlans from release + environment definitions.
type Planner struct {
	// BoriRoot is the absolute path to the bori repo root.
	BoriRoot string
	// Release overrides LoadReleaseByName when set.
	// The operator injects a BoriRelease fetched from the Kubernetes API;
	// nil falls back to the filesystem.
	Release *model.BoriRelease
}

// New returns a Planner rooted at boriRoot.
func New(boriRoot string) *Planner {
	return &Planner{BoriRoot: boriRoot}
}

// Plan loads the named release and environment and returns a deploy plan.
// The plan includes:
//   - Components sorted in dependency order (dependencies first)
//   - Namespace policy violations
//   - Compatibility matrix violations
func (p *Planner) Plan(runID, releaseName, envName string) (*artifact.Plan, error) {
	var rel model.BoriRelease
	if p.Release != nil {
		rel = *p.Release
	} else {
		var err error
		rel, err = model.LoadReleaseByName(p.BoriRoot, releaseName)
		if err != nil {
			return nil, fmt.Errorf("load release %q: %w", releaseName, err)
		}
	}
	env, err := model.LoadEnvironmentByName(p.BoriRoot, envName)
	if err != nil {
		return nil, fmt.Errorf("load environment %q: %w", envName, err)
	}

	// Load all components for ordering and compatibility checks.
	comps := make(map[string]model.BoriComponent, len(rel.Components))
	for _, ref := range rel.Components {
		comp, err := model.LoadComponentByName(p.BoriRoot, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("load component %q: %w", ref.Name, err)
		}
		comps[ref.Name] = comp
	}

	// Sort components in dependency order.
	orderedRefs, err := release.Order(rel, comps)
	if err != nil {
		return nil, fmt.Errorf("order components: %w", err)
	}

	// Check compatibility matrix.
	matrix, err := release.LoadMatrixForRelease(p.BoriRoot, rel)
	if err != nil {
		// Non-fatal: matrix may not exist yet.
		matrix = release.CompatibilityMatrix{}
	}
	violations := release.CheckCompatibility(rel, matrix)

	plan := &artifact.Plan{
		SchemaVersion: "bori.plan.v1",
		RunID:         runID,
		Release:       releaseName,
		Environment:   envName,
		CreatedAt:     time.Now().UTC(),
	}

	for _, v := range violations {
		plan.Violations = append(plan.Violations, "compat: "+v.String())
	}

	allowedNS := make(map[string]bool, len(env.NamespacePolicy.Allowed))
	for _, ns := range env.NamespacePolicy.Allowed {
		allowedNS[ns] = true
	}

	for _, ref := range orderedRefs {
		comp := comps[ref.Name]
		ns := comp.Deploy.Namespace
		if ns == "" {
			ns = defaultNamespace(comp.Name)
		}
		adapterName := comp.Deploy.Adapter
		if adapterName == "" {
			adapterName = "devspace"
		}

		imageRef := comp.Image.Ref
		imageDigest := ref.ImageDigest
		if imageDigest != "" {
			// Build digest-qualified ref: strip tag/digest from base, then append @sha256:...
			base := imageRef
			if i := strings.Index(base, "@"); i >= 0 {
				base = base[:i]
			}
			if i := strings.LastIndex(base, ":"); i >= 0 && !strings.Contains(base[i:], "/") {
				base = base[:i]
			}
			imageRef = base + "@" + imageDigest
			adapterName = "imageswap"
		}

		cp := artifact.ComponentPlan{
			Name:        comp.Name,
			Version:     ref.Version,
			Adapter:     adapterName,
			Namespace:   ns,
			ImageRef:    imageRef,
			ImageDigest: imageDigest,
			GitSha:      ref.GitSha,
			Action:      "deploy",
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

// LoadComps loads all BoriComponent files for a release and returns them by name.
func (p *Planner) LoadComps(rel model.BoriRelease) (map[string]model.BoriComponent, error) {
	comps := make(map[string]model.BoriComponent, len(rel.Components))
	for _, ref := range rel.Components {
		comp, err := model.LoadComponentByName(p.BoriRoot, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("load component %q: %w", ref.Name, err)
		}
		comps[ref.Name] = comp
	}
	return comps, nil
}

// defaultNamespace returns the conventional namespace for a component.
func defaultNamespace(componentName string) string {
	return componentName + "-system"
}
