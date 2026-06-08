// Package imageswap implements adapter.DeployAdapter using kubectl set image + rollout wait.
// It is designed for the Harbor digest-based deploy flow: ko build → Harbor push →
// bori release set-image → bori deploy (imageswap) → rollout complete.
// Unlike the ko or devspace adapters, imageswap never builds from source — it swaps
// the running Deployment's container image to the exact digest in req.Component.Image.Ref.
//
// Convention (v1): the Deployment name and the primary container name are both assumed
// to equal comp.Name (e.g. component "jumi" → deployment/jumi, container "jumi").
// Apps that use a different Deployment or container name are not yet supported.
package imageswap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// validK8sName matches Kubernetes RFC 1123 DNS label names.
// Component names that do not match will be rejected before kubectl is invoked.
var validK8sName = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

// Adapter patches a Kubernetes Deployment image to a Harbor digest and waits for rollout.
type Adapter struct{}

// New returns a DeployAdapter backed by kubectl set image + kubectl rollout status.
func New() adapter.DeployAdapter {
	return &Adapter{}
}

func (a *Adapter) Name() string { return "imageswap" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	name := req.Component.Name
	if !validK8sName.MatchString(name) {
		return nil, fmt.Errorf("imageswap: component name %q is not a valid Kubernetes name", name)
	}

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
