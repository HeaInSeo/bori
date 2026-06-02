package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeployResult is written to .bori/runs/<run-id>/deploy-result.json
// after all adapter Deploy calls complete.
type DeployResult struct {
	SchemaVersion string       `json:"schemaVersion"`
	RunID         string       `json:"runId"`
	Release       string       `json:"release"`
	Environment   string       `json:"environment"`
	StartedAt     time.Time    `json:"startedAt"`
	FinishedAt    time.Time    `json:"finishedAt"`
	Overall       string       `json:"overall"` // success | partial | failed
	Components    []CompDeploy `json:"components"`
}

// CompDeploy records the deploy outcome for one component.
type CompDeploy struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Adapter string `json:"adapter"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// WriteDeployResult serializes r as JSON to <runDir>/deploy-result.json.
func WriteDeployResult(runDir string, r DeployResult) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", runDir, err)
	}
	path := filepath.Join(runDir, "deploy-result.json")
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
