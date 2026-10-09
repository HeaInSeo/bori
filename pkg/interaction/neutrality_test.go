package interaction

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Production code never branches on a scenario ID or a product name.
func TestProjectionIsAppAndScenarioNeutral(t *testing.T) {
	banned := regexp.MustCompile(`(?i)\bjumi\b|artifact-handoff|\btori\b|nodesentinel|nodevault|genomic|\bS\d{2}\b|scenario`)
	files, _ := filepath.Glob("*.go")
	files = append(files, "../../cmd/bori/opsinteraction.go", "../../apis/ops/v1alpha1/interaction_types.go")
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n++
		if loc := banned.FindIndex(b); loc != nil {
			t.Errorf("%s contains %q", f, b[loc[0]:loc[1]])
		}
	}
	if n < 5 {
		t.Fatalf("scanned only %d files", n)
	}
}
