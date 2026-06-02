// Package shell implements adapter.DeployAdapter using a shell script.
// This adapter is for developer mode only and must not be used in
// shared/integration/promotion environments.
package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter runs a deploy script (deploy.sh) in the target app directory.
// Arbitrary shell execution is unsafe for agent-facing gateways.
// Use this adapter only in explicitly marked developer-mode environments.
type Adapter struct {
	AppsDir string
	// ScriptName is the deploy script to run (default: deploy.sh).
	ScriptName string
}

// New returns a DeployAdapter backed by a shell script.
func New(appsDir string) adapter.DeployAdapter {
	return &Adapter{
		AppsDir:    appsDir,
		ScriptName: "deploy.sh",
	}
}

func (a *Adapter) Name() string { return "shell" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	appDir := filepath.Join(a.AppsDir, req.Component.Name)
	scriptPath := filepath.Join(appDir, a.ScriptName)

	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("deploy script not found: %s", scriptPath)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] sh %s (developer mode)", scriptPath),
		}, nil
	}

	// Shell adapter is developer mode only — log a clear warning.
	fmt.Fprintf(os.Stderr, "[bori] WARNING: shell adapter is developer mode only — unsafe for shared envs\n")

	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	cmd.Dir = appDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("shell deploy: %v", err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("deployed %s via shell (developer mode)", req.Component.Name),
	}, nil
}
