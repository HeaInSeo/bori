// Package artifact manages bori run archives.
// Every bori run — success or failure — writes a status.json.
package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Status is written to .bori/runs/<run-id>/status.json after every run.
// It is written regardless of success or failure.
type Status struct {
	SchemaVersion string       `json:"schemaVersion"`
	RunID         string       `json:"runId"`
	Release       string       `json:"release,omitempty"`
	Environment   string       `json:"environment,omitempty"`
	Profile       string       `json:"profile,omitempty"`
	StartedAt     time.Time    `json:"startedAt"`
	FinishedAt    time.Time    `json:"finishedAt"`
	Phase         string       `json:"phase"`  // Verified | Failed | PartialFail | Running
	Result        string       `json:"result"` // PASS | WARN | FAIL | NO_GRADE
	Message       string       `json:"message,omitempty"`
	Components    []CompStatus `json:"components,omitempty"`
}

// CompStatus holds the gate result for a single component within a run.
type CompStatus struct {
	Name       string `json:"name"`
	GateResult string `json:"gateResult"` // PASS | WARN | FAIL | NO_GRADE
	Message    string `json:"message,omitempty"`
}

// Write serializes s as JSON to <runDir>/status.json.
// The directory is created if it does not exist.
func Write(runDir string, s Status) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", runDir, err)
	}
	path := filepath.Join(runDir, "status.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(s); encErr != nil {
		_ = f.Close()
		return fmt.Errorf("encode: %w", encErr)
	}
	return f.Close()
}

// Read parses <runDir>/status.json.
func Read(runDir string) (Status, error) {
	path := filepath.Join(runDir, "status.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, fmt.Errorf("read %s: %w", path, err)
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, fmt.Errorf("parse: %w", err)
	}
	return s, nil
}

// RunDir returns the canonical run archive directory path.
func RunDir(baseDir, runID string) string {
	return filepath.Join(baseDir, "runs", runID)
}
