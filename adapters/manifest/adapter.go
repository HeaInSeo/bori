// Package manifest implements adapter.DeployAdapter using kubectl apply -f.
// The manifest directory is resolved from deploy.bootstrap.path relative to BoriRoot.
package manifest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter runs `kubectl apply -f <dir>` for plain K8s manifests stored in the bori repo.
type Adapter struct{}

// New returns a DeployAdapter backed by plain manifest apply.
func New() adapter.DeployAdapter { return &Adapter{} }

func (a *Adapter) Name() string { return "manifest" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	bootstrap := req.Component.Deploy.Bootstrap
	if bootstrap == nil || bootstrap.Path == "" {
		return nil, fmt.Errorf("manifest adapter requires deploy.bootstrap.path to be set in component.yaml")
	}
	if req.BoriRoot == "" {
		return nil, fmt.Errorf("manifest adapter requires BoriRoot")
	}
	manifestDir := filepath.Join(req.BoriRoot, bootstrap.Path)
	if _, err := os.Stat(manifestDir); err != nil {
		return nil, fmt.Errorf("manifest dir not found: %s", manifestDir)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] kubectl apply -f %s", manifestDir),
		}, nil
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", manifestDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("kubectl apply -f: %v", err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("deployed %s via manifest", req.Component.Name),
	}, nil
}
