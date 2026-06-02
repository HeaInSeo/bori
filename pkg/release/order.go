package release

import (
	"fmt"

	"github.com/HeaInSeo/bori/pkg/model"
)

// Order returns the release components sorted in dependency-topological order.
// Components with no dependencies come first; dependents come after their dependencies.
// Returns an error if a dependency cycle is detected.
func Order(rel model.BoriRelease, comps map[string]model.BoriComponent) ([]model.ComponentRef, error) {
	// Build index: name -> ComponentRef
	refByName := make(map[string]model.ComponentRef, len(rel.Components))
	for _, ref := range rel.Components {
		refByName[ref.Name] = ref
	}

	// In-degree count and adjacency list (dependency → dependent)
	inDegree := make(map[string]int, len(rel.Components))
	dependents := make(map[string][]string) // dep → list of components that depend on dep
	for _, ref := range rel.Components {
		inDegree[ref.Name] = 0
	}
	for _, ref := range rel.Components {
		comp, ok := comps[ref.Name]
		if !ok {
			continue
		}
		for _, dep := range comp.Dependencies {
			if _, inRelease := refByName[dep]; !inRelease {
				continue // dependency not in this release, skip
			}
			inDegree[ref.Name]++
			dependents[dep] = append(dependents[dep], ref.Name)
		}
	}

	// Kahn's algorithm
	var queue []string
	for _, ref := range rel.Components {
		if inDegree[ref.Name] == 0 {
			queue = append(queue, ref.Name)
		}
	}

	var ordered []model.ComponentRef
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		ordered = append(ordered, refByName[name])
		for _, dependent := range dependents[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(ordered) != len(rel.Components) {
		return nil, fmt.Errorf("dependency cycle detected in release %q", rel.Name)
	}
	return ordered, nil
}
