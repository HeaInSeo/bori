// Package devspace implements adapter.DeployAdapter using DevSpace.
package devspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter runs `devspace deploy` in the target app directory.
type Adapter struct {
	// AppsDir is the parent directory that contains app repos as subdirectories.
	AppsDir string
}

// New returns a DeployAdapter backed by DevSpace.
func New(appsDir string) adapter.DeployAdapter {
	return &Adapter{AppsDir: appsDir}
}

func (a *Adapter) Name() string { return "devspace" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	fmt.Fprintln(os.Stderr, "[bori] DEPRECATED: devspace adapter is deprecated; migrate to kustomize or manifest bootstrap adapter")
	appDir := filepath.Join(a.AppsDir, req.Component.Name)
	if _, err := os.Stat(appDir); err != nil {
		return nil, fmt.Errorf("app dir not found: %s", appDir)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] devspace deploy in %s", appDir),
		}, nil
	}

	cmd := exec.CommandContext(ctx, "devspace", "deploy")
	cmd.Dir = appDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("devspace deploy: %v", err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("deployed %s via devspace", req.Component.Name),
	}, nil
}
