package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// BoriVerificationPolicy is the policy definition stored in
// verification/policies/<name>.yaml inside the bori repo.
// It is bori's YAML contract format — not a Kubernetes CRD.
type BoriVerificationPolicy struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"` // kube-slint
	Mode     string `yaml:"mode"`     // cli
	// Policy is the path to the kube-slint SLI policy file relative to the app repo.
	// Supports the {profile} placeholder which is substituted at runtime.
	// Example: .bori/policy.{profile}.yaml
	Policy   string `yaml:"policy"`
	Baseline string `yaml:"baseline,omitempty"`
	// FailOn controls when bori treats the result as promotion-blocking.
	// NEVER | FAIL | FAIL_OR_NOGRADE | WARN
	FailOn   string `yaml:"failOn"`
	Blocking bool   `yaml:"blocking"`
}

// ResolvePolicyPath substitutes {profile} in the Policy field and joins it
// with appDir to produce an absolute policy path.
func (p BoriVerificationPolicy) ResolvePolicyPath(appDir, profile string) string {
	rel := strings.ReplaceAll(p.Policy, "{profile}", profile)
	return filepath.Join(appDir, rel)
}

// LoadPolicy parses verification/policies/<name>.yaml.
func LoadPolicy(path string) (BoriVerificationPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriVerificationPolicy{}, fmt.Errorf("read %s: %w", path, err)
	}
	var pol BoriVerificationPolicy
	if err := yaml.Unmarshal(data, &pol); err != nil {
		return BoriVerificationPolicy{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return pol, nil
}

// LoadPolicyByName loads verification/policies/<name>.yaml from the bori repo root.
func LoadPolicyByName(boriRoot, name string) (BoriVerificationPolicy, error) {
	path := filepath.Join(boriRoot, "verification", "policies", name+".yaml")
	return LoadPolicy(path)
}
