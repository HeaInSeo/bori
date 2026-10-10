package action_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func productionFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*.go", "actiontest/*.go"} {
		fs, _ := filepath.Glob(pat)
		for _, f := range fs {
			if !strings.HasSuffix(f, "_test.go") {
				out = append(out, f)
			}
		}
	}
	if len(out) < 8 {
		t.Fatalf("scanned only %d files", len(out))
	}
	return out
}

// Production code never branches on a scenario, a product, or workload
// readiness: recovery is decided only by O1 on post-execution evidence.
func TestActionCodeIsAppScenarioAndReadinessNeutral(t *testing.T) {
	banned := regexp.MustCompile(`(?i)\bjumi\b|artifact-handoff|\btori\b|nodesentinel|genomic|\bS\d{2}\b|scenario|\bpods?\b|readyreplicas|ready-replicas|rollout`)
	for _, f := range productionFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := banned.FindIndex(b); loc != nil {
			t.Errorf("%s contains %q", f, b[loc[0]:loc[1]])
		}
	}
}

// The action package cannot mutate anything in a cluster: it imports no
// Kubernetes client at all; the only way out is the Provider interface.
func TestActionCodeHasNoClusterClient(t *testing.T) {
	banned := []string{"sigs.k8s.io/controller-runtime", "k8s.io/client-go", "net/http", "os/exec"}
	for _, f := range productionFiles(t) {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			for _, b := range banned {
				if strings.HasPrefix(p, b) {
					t.Errorf("%s imports %s", f, p)
				}
			}
		}
	}
}
