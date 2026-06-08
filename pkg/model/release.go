package model

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BoriRelease is the release definition stored in
// releases/<name>/release.yaml inside the bori repo.
// A release is a pinned, compatible set of component versions.
type BoriRelease struct {
	Name          string              `yaml:"name"`
	Components    []ComponentRef      `yaml:"components"`
	Compatibility CompatibilityRef    `yaml:"compatibility,omitempty"`
	Verification  ReleaseVerification `yaml:"verification,omitempty"`
	Promotion     PromotionPolicy     `yaml:"promotion,omitempty"`
}

// ComponentRef pins a component at a specific version.
// When ImageDigest is set, bori uses the imageswap adapter to deploy the exact
// Harbor digest instead of building from source.
type ComponentRef struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	ImageDigest string `yaml:"imageDigest,omitempty"`
	GitSha      string `yaml:"gitSha,omitempty"`
}

// CompatibilityRef points to the version compatibility matrix for this release.
type CompatibilityRef struct {
	Matrix string `yaml:"matrix,omitempty"`
}

// ReleaseVerification lists the verification policies that apply to this release.
type ReleaseVerification struct {
	Policies []string `yaml:"policies,omitempty"`
}

// PromotionPolicy defines the promotion gate requirements.
type PromotionPolicy struct {
	RequiredGateResult string         `yaml:"requiredGateResult"` // PASS
	BaselinePolicy     BaselinePolicy `yaml:"baselinePolicy,omitempty"`
}

// BaselinePolicy controls how baselines are updated after promotion.
type BaselinePolicy struct {
	UpdateFrom     string `yaml:"updateFrom"` // promoted-revision-evidence
	ReviewRequired bool   `yaml:"reviewRequired"`
}

// LoadRelease parses releases/<name>/release.yaml.
func LoadRelease(path string) (BoriRelease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriRelease{}, fmt.Errorf("read %s: %w", path, err)
	}
	var r BoriRelease
	if err := yaml.Unmarshal(data, &r); err != nil {
		return BoriRelease{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return r, nil
}

// LoadReleaseByName loads releases/<name>/release.yaml from the bori repo root.
func LoadReleaseByName(boriRoot, name string) (BoriRelease, error) {
	path := filepath.Join(boriRoot, "releases", name, "release.yaml")
	return LoadRelease(path)
}
