package release

import (
	"github.com/HeaInSeo/bori/pkg/model"
)

// AffectedComponents returns the names of components that need re-verification
// when the given set of components has changed.
//
// A component is affected if:
//   - it is in the changed set, OR
//   - any component it depends on (transitively) is in the changed set.
//
// The result is returned in the same order as orderedRefs.
func AffectedComponents(changed []string, orderedRefs []model.ComponentRef, comps map[string]model.BoriComponent) []string {
	changedSet := make(map[string]bool, len(changed))
	for _, c := range changed {
		changedSet[c] = true
	}

	// Build reverse dependency graph: dep → list of components that depend on it.
	reverseDeps := make(map[string][]string)
	for _, ref := range orderedRefs {
		comp, ok := comps[ref.Name]
		if !ok {
			continue
		}
		for _, dep := range comp.Dependencies {
			reverseDeps[dep] = append(reverseDeps[dep], ref.Name)
		}
	}

	// BFS from changed components through the reverse dependency graph.
	affected := make(map[string]bool)
	queue := append([]string(nil), changed...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if affected[name] {
			continue
		}
		affected[name] = true
		for _, dependent := range reverseDeps[name] {
			if !affected[dependent] {
				queue = append(queue, dependent)
			}
		}
	}

	// Return in orderedRefs order to preserve deploy/verify sequence.
	var result []string
	for _, ref := range orderedRefs {
		if affected[ref.Name] {
			result = append(result, ref.Name)
		}
	}
	return result
}
