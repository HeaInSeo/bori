// Package ko implements adapter.DeployAdapter using ko build + kubectl apply.
package ko

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter runs `ko apply -f <manifest>` in the target app directory.
type Adapter struct {
	AppsDir string
}

// New returns a DeployAdapter backed by ko.
func New(appsDir string) adapter.DeployAdapter {
	return &Adapter{AppsDir: appsDir}
}

func (a *Adapter) Name() string { return "ko" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	appDir := filepath.Join(a.AppsDir, req.Component.Name)
	if _, err := os.Stat(appDir); err != nil {
		return nil, fmt.Errorf("app dir not found: %s", appDir)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] ko apply in %s", appDir),
		}, nil
	}

	// ko apply -f config/ --namespace <ns>
	ns := req.Component.Name + "-system"
	cmd := exec.CommandContext(ctx, "ko", "apply",
		"-f", "config/",
		"--namespace", ns,
	)
	cmd.Dir = appDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("ko apply: %v", err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("deployed %s via ko", req.Component.Name),
	}, nil
}
