// Package release provides release-level orchestration:
// compatibility checking, dependency ordering, and affected-component diffing.
package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/HeaInSeo/bori/pkg/model"
)

// CompatibilityMatrix defines which component versions are compatible.
type CompatibilityMatrix struct {
	Constraints []Constraint `yaml:"constraints"`
}

// Constraint says that a specific component version requires certain others.
type Constraint struct {
	Component string               `yaml:"component"`
	Version   string               `yaml:"version"`
	Requires  []VersionRequirement `yaml:"requires"`
}

// VersionRequirement specifies a minimum version for a dependency.
type VersionRequirement struct {
	Component  string `yaml:"component"`
	MinVersion string `yaml:"minVersion"`
}

// Violation is a compatibility constraint error.
type Violation struct {
	Component string
	Message   string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s", v.Component, v.Message)
}

// LoadMatrix parses a compatibility matrix YAML file.
func LoadMatrix(path string) (CompatibilityMatrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CompatibilityMatrix{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m CompatibilityMatrix
	if err := yaml.Unmarshal(data, &m); err != nil {
		return CompatibilityMatrix{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// LoadMatrixForRelease loads the compatibility matrix referenced by the release.
// boriRoot is the path to the bori repo root.
func LoadMatrixForRelease(boriRoot string, rel model.BoriRelease) (CompatibilityMatrix, error) {
	if rel.Compatibility.Matrix == "" {
		return CompatibilityMatrix{}, nil
	}
	path := filepath.Join(boriRoot, rel.Compatibility.Matrix)
	return LoadMatrix(path)
}

// CheckCompatibility validates the release's component versions against the matrix.
// Returns a list of violations; an empty list means all constraints are satisfied.
func CheckCompatibility(rel model.BoriRelease, matrix CompatibilityMatrix) []Violation {
	versions := make(map[string]string, len(rel.Components))
	for _, ref := range rel.Components {
		versions[ref.Name] = ref.Version
	}

	var violations []Violation
	for _, constraint := range matrix.Constraints {
		releaseVersion, ok := versions[constraint.Component]
		if !ok || releaseVersion != constraint.Version {
			continue // constraint only applies to this specific version
		}
		for _, req := range constraint.Requires {
			actualVersion, ok := versions[req.Component]
			if !ok {
				violations = append(violations, Violation{
					Component: constraint.Component,
					Message:   fmt.Sprintf("requires %s %s but %s is not in the release", req.Component, req.MinVersion, req.Component),
				})
				continue
			}
			if !versionAtLeast(actualVersion, req.MinVersion) {
				violations = append(violations, Violation{
					Component: constraint.Component,
					Message:   fmt.Sprintf("requires %s >= %s but release has %s", req.Component, req.MinVersion, actualVersion),
				})
			}
		}
	}
	return violations
}

// versionAtLeast reports whether version >= minVersion using simple semver comparison.
func versionAtLeast(version, minVersion string) bool {
	return semverCompare(
		strings.TrimPrefix(version, "v"),
		strings.TrimPrefix(minVersion, "v"),
	) >= 0
}

// semverCompare compares two "X.Y.Z" version strings.
// Returns >0 if a > b, 0 if equal, <0 if a < b.
func semverCompare(a, b string) int {
	pa := strings.SplitN(a, ".", 3)
	pb := strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na != nb {
			if na > nb {
				return 1
			}
			return -1
		}
	}
	return 0
}
