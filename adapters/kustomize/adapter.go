// Package kustomize implements adapter.DeployAdapter using kubectl apply -k.
package kustomize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter runs `kubectl apply -k <overlayDir>` for the target component.
type Adapter struct {
	AppsDir string
	// OverlaySubdir is the kustomize overlay directory relative to the app root.
	// Defaults to "config/overlays/default".
	OverlaySubdir string
}

// New returns a DeployAdapter backed by kustomize.
func New(appsDir string) adapter.DeployAdapter {
	return &Adapter{
		AppsDir:       appsDir,
		OverlaySubdir: "config/overlays/default",
	}
}

func (a *Adapter) Name() string { return "kustomize" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	var overlayDir string
	bootstrap := req.Component.Deploy.Bootstrap
	if bootstrap != nil && bootstrap.Path != "" && req.BoriRoot != "" {
		overlayDir = filepath.Join(req.BoriRoot, bootstrap.Path)
	} else {
		overlayDir = filepath.Join(a.AppsDir, req.Component.Name, a.OverlaySubdir)
	}
	if _, err := os.Stat(overlayDir); err != nil {
		return nil, fmt.Errorf("kustomize overlay not found: %s", overlayDir)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] kubectl apply -k %s", overlayDir),
		}, nil
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-k", overlayDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("kubectl apply -k: %v", err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("deployed %s via kustomize", req.Component.Name),
	}, nil
}
