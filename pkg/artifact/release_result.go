package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReleaseResult is written to .bori/runs/<run-id>/release-result.json
// after all components in a release have been verified.
type ReleaseResult struct {
	SchemaVersion           string            `json:"schemaVersion"`
	RunID                   string            `json:"runId"`
	Release                 string            `json:"release"`
	Environment             string            `json:"environment"`
	CreatedAt               time.Time         `json:"createdAt"`
	GateResult              string            `json:"gateResult"` // PASS|WARN|FAIL|NO_GRADE
	CompatibilityViolations []string          `json:"compatibilityViolations,omitempty"`
	AffectedComponents      []string          `json:"affectedComponents,omitempty"`
	Components              []CompReleaseGate `json:"components"`
}

// CompReleaseGate records the verification outcome for one component in a release.
type CompReleaseGate struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	GateResult string `json:"gateResult"`
	// Affected indicates the component was included because it or a dependency changed.
	Affected bool `json:"affected"`
	// Skipped indicates the component was not re-verified (unchanged in incremental mode).
	Skipped bool `json:"skipped,omitempty"`
}

// WriteReleaseResult serializes r as JSON to <runDir>/release-result.json.
func WriteReleaseResult(runDir string, r ReleaseResult) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", runDir, err)
	}
	path := filepath.Join(runDir, "release-result.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(r); encErr != nil {
		_ = f.Close()
		return fmt.Errorf("encode: %w", encErr)
	}
	return f.Close()
}
