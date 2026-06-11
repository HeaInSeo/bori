// Package kubeapply implements adapter.DeployAdapter using controller-runtime
// SSA (Server-Side Apply) without shelling out to kubectl.
// Intended for the bori-operator (distroless image); the CLI uses the
// kubectl-backed kustomize/manifest adapters instead.
package kubeapply

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigs_client "sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/HeaInSeo/bori/pkg/adapter"
)

// Adapter applies K8s manifests in-process using controller-runtime SSA.
// Reads files listed in kustomization.yaml (resources order) or all *.yaml files.
type Adapter struct {
	Client sigs_client.Client
}

// New returns a DeployAdapter backed by controller-runtime SSA.
func New(client sigs_client.Client) adapter.DeployAdapter {
	return &Adapter{Client: client}
}

func (a *Adapter) Name() string { return "kube-apply" }

func (a *Adapter) Deploy(ctx context.Context, req adapter.DeployRequest) (*adapter.DeployResult, error) {
	bootstrap := req.Component.Deploy.Bootstrap
	if bootstrap == nil || bootstrap.Path == "" {
		return nil, fmt.Errorf("kube-apply adapter requires deploy.bootstrap.path")
	}
	if req.BoriRoot == "" {
		return nil, fmt.Errorf("kube-apply adapter requires BoriRoot")
	}
	manifestDir := filepath.Join(req.BoriRoot, bootstrap.Path)
	if _, err := os.Stat(manifestDir); err != nil {
		return nil, fmt.Errorf("manifest dir not found: %s", manifestDir)
	}

	files, err := resolveFiles(manifestDir)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest files: %w", err)
	}

	if req.DryRun {
		return &adapter.DeployResult{
			Success: true,
			Message: fmt.Sprintf("[dry-run] kube-apply %s (%d files)", manifestDir, len(files)),
		}, nil
	}

	applied := 0
	for _, f := range files {
		n, err := a.applyFile(ctx, f)
		if err != nil {
			return &adapter.DeployResult{
				Success: false,
				Message: fmt.Sprintf("apply %s: %v", filepath.Base(f), err),
			}, err
		}
		applied += n
	}

	return &adapter.DeployResult{
		Success: true,
		Message: fmt.Sprintf("applied %d objects for %s", applied, req.Component.Name),
	}, nil
}

// applyFile reads a YAML file (may contain multiple documents) and applies each
// object via SSA with field manager "bori-operator".
func (a *Adapter) applyFile(ctx context.Context, filePath string) (int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", filePath, err)
	}

	reader := k8syaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	applied := 0
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return applied, fmt.Errorf("parse yaml: %w", err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}

		jsonBytes, err := sigsyaml.YAMLToJSON(doc)
		if err != nil {
			return applied, fmt.Errorf("yaml→json: %w", err)
		}
		var rawMap map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &rawMap); err != nil {
			return applied, fmt.Errorf("unmarshal: %w", err)
		}
		obj := &unstructured.Unstructured{Object: rawMap}
		if obj.GetKind() == "" || obj.GetAPIVersion() == "" {
			continue // skip empty/comment-only documents
		}

		force := true
		if err := a.Client.Patch(ctx, obj, sigs_client.Apply, &sigs_client.PatchOptions{
			FieldManager: "bori-operator",
			Force:        &force,
		}); err != nil {
			return applied, fmt.Errorf("SSA patch %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		applied++
	}
	return applied, nil
}

// kustomization is a minimal subset of kustomization.yaml used to read resource order.
type kustomization struct {
	Resources []string `yaml:"resources"`
}

// resolveFiles returns manifest file paths in the order they should be applied.
// If kustomization.yaml exists, its resources list is used (preserving author intent).
// Otherwise, all *.yaml files except kustomization.yaml are returned alphabetically.
func resolveFiles(dir string) ([]string, error) {
	kustomPath := filepath.Join(dir, "kustomization.yaml")
	if data, err := os.ReadFile(kustomPath); err == nil {
		var k kustomization
		if yerr := sigsyaml.Unmarshal(data, &k); yerr == nil && len(k.Resources) > 0 {
			var files []string
			for _, r := range k.Resources {
				p := filepath.Join(dir, r)
				if _, serr := os.Stat(p); serr == nil {
					files = append(files, p)
				}
			}
			return files, nil
		}
	}

	// Fall back: all *.yaml except kustomization.yaml, alphabetical.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") || e.Name() == "kustomization.yaml" {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files, nil
}
