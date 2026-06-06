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
	// Source documents the primary data source used by the kube-slint policy.
	// Informational — does not change how bori invokes slint-gate.
	// Values: metric-2point | k8s-object-snapshot | network-baseline
	// k8s-object-snapshot: uses K8sObjectFetcher (kube-slint v1.2.0+, Track K5).
	Source string `yaml:"source,omitempty"`
	// OnCounterReset documents the CounterResetPolicy set in the kube-slint SLI spec.
	// Advisory — the actual policy is in the app repo's kube-slint policy file.
	// Values: warn | no_grade | fail | skip (matches kube-slint CounterResetPolicy).
	// no_grade: counter reset → StatusSkip → gate NO_GRADE → blocks FAIL_OR_NOGRADE.
	// Requires kube-slint v1.1.0+ (Track K2).
	OnCounterReset string `yaml:"onCounterReset,omitempty"`
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
