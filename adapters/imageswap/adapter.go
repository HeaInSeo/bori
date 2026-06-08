// Package imageswap implements adapter.DeployAdapter using kubectl set image + rollout wait.
// It is designed for the Harbor digest-based deploy flow: ko build → Harbor push →
// bori release set-image → bori deploy (imageswap) → rollout complete.
// Unlike the ko or devspace adapters, imageswap never builds from source — it swaps
// the running Deployment's container image to the exact digest in req.Component.Image.Ref.
package imageswap

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter patches a Kubernetes Deployment image to a Harbor digest and waits for rollout.
type Adapter struct{}

// New returns a DeployAdapter backed by kubectl set image + kubectl rollout status.
func New() adapter.DeployAdapter {
	return &Adapter{}
}

func (a *Adapter) Name() string { return "imageswap" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	name := req.Component.Name
	ns := req.Component.Deploy.Namespace
	if ns == "" {
		ns = name + "-system"
	}
	imageRef := req.Component.Image.Ref
	if imageRef == "" {
		return nil, fmt.Errorf("imageswap: component %q has no imageRef (need digest-qualified ref)", name)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] imageswap deployment/%s -n %s → %s", name, ns, imageRef),
		}, nil
	}

	// kubectl set image deployment/<name> <name>=<imageRef> -n <ns>
	setImg := exec.CommandContext(ctx,
		"kubectl", "set", "image",
		fmt.Sprintf("deployment/%s", name),
		fmt.Sprintf("%s=%s", name, imageRef),
		"-n", ns,
	)
	setImg.Stdout = os.Stdout
	setImg.Stderr = os.Stderr
	if err := setImg.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("kubectl set image deployment/%s: %v", name, err),
		}, err
	}

	// kubectl rollout status deployment/<name> -n <ns> --timeout=5m
	rolloutCmd := exec.CommandContext(ctx,
		"kubectl", "rollout", "status",
		fmt.Sprintf("deployment/%s", name),
		"-n", ns,
		"--timeout=5m",
	)
	rolloutCmd.Stdout = os.Stdout
	rolloutCmd.Stderr = os.Stderr
	if err := rolloutCmd.Run(); err != nil {
		return &adapter.DeployResult{
			Success: false,
			Message: fmt.Sprintf("rollout status deployment/%s -n %s: %v", name, ns, err),
		}, err
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("imageswap %s/%s → %s", ns, name, imageRef),
	}, nil
}
