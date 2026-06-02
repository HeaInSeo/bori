package adapter

import (
	"context"

	"github.com/HeaInSeo/bori/pkg/model"
)

// DeployAdapter knows how to apply a component into a target environment.
// Each tool (devspace, ko, kustomize, shell) implements this interface.
// bori orchestrates adapters; business logic stays in the app repo.
type DeployAdapter interface {
	// Name returns the adapter identifier (e.g. "devspace", "ko", "kustomize").
	Name() string
	// Deploy applies the component and returns a structured result.
	Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error)
}

// DeployRequest carries everything a DeployAdapter needs to apply a component.
type DeployRequest struct {
	Component   model.BoriComponent
	Environment model.BoriEnvironment
	// DryRun, if true, asks the adapter to compute a plan without applying it.
	DryRun bool
	// OutDir is the run archive evidence directory for this component.
	OutDir string
}

// DeployResult is the structured output of a DeployAdapter.Deploy call.
type DeployResult struct {
	Success bool
	Message string
	// ManifestPath is the path to the rendered manifest, if written.
	ManifestPath string
}
