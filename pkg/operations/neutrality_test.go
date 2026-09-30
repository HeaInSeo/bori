package operations

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The core must stay app-neutral and pure: no product/scenario identifiers,
// no wall clock, no Kubernetes or network dependency in non-test code.
func TestCoreIsAppNeutralAndPure(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bjumi\b`),
		regexp.MustCompile(`(?i)artifact-handoff`),
		regexp.MustCompile(`(?i)\btori\b`),
		regexp.MustCompile(`(?i)nodesentinel`),
		regexp.MustCompile(`(?i)\bnan\b`),
		regexp.MustCompile(`\bS\d{2}b?\b`),
		regexp.MustCompile(`time\.Now\(`),
		regexp.MustCompile(`"k8s\.io/|"sigs\.k8s\.io/|"net/http"|"google\.golang\.org/grpc`),
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, re := range banned {
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s contains banned token %q", f, b[loc[0]:loc[1]])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no core files scanned")
	}
}
