// Package model defines the bori-managed component and environment data models.
// These are distinct from the app-local .bori/component.yaml (pkg/component),
// which is the self-registration format used by individual app repos.
package model

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BoriComponent is the managed component definition stored in
// components/<name>/component.yaml inside the bori repo.
type BoriComponent struct {
	Name                 string   `yaml:"name"`
	Kind                 string   `yaml:"kind"`    // control-component | data-component
	Version              string   `yaml:"version"` // semver
	Image                ImageRef `yaml:"image"`
	Ports                Ports    `yaml:"ports"`
	Health               Endpoint `yaml:"health"`
	Metrics              Endpoint `yaml:"metrics"`
	Dependencies         []string `yaml:"dependencies,omitempty"`
	Contracts            []string `yaml:"contracts,omitempty"`
	VerificationPolicies []string `yaml:"verificationPolicies,omitempty"`
}

// ImageRef holds the full image reference including digest.
// bori prefers digest over tag for immutability.
type ImageRef struct {
	Ref string `yaml:"ref"`
}

// Ports declares named ports for a component.
type Ports struct {
	Metrics int `yaml:"metrics"`
	Health  int `yaml:"health"`
}

// Endpoint describes an HTTP endpoint path.
type Endpoint struct {
	Path string `yaml:"path"`
}

// LoadComponent parses components/<name>/component.yaml.
func LoadComponent(path string) (BoriComponent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriComponent{}, fmt.Errorf("read %s: %w", path, err)
	}
	var c BoriComponent
	if err := yaml.Unmarshal(data, &c); err != nil {
		return BoriComponent{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadComponentByName loads components/<name>/component.yaml from the
// given bori repo root.
func LoadComponentByName(boriRoot, name string) (BoriComponent, error) {
	path := filepath.Join(boriRoot, "components", name, "component.yaml")
	return LoadComponent(path)
}
