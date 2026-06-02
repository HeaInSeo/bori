package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Plan is written to .bori/runs/<run-id>/plan.json before any deployment begins.
// It captures what bori intends to do and is preserved regardless of outcome.
type Plan struct {
	SchemaVersion string          `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	Release       string          `json:"release"`
	Environment   string          `json:"environment"`
	CreatedAt     time.Time       `json:"createdAt"`
	Components    []ComponentPlan `json:"components"`
	Violations    []string        `json:"violations,omitempty"`
}

// ComponentPlan describes what bori will do for one component.
type ComponentPlan struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Adapter   string `json:"adapter"`
	Namespace string `json:"namespace"`
	ImageRef  string `json:"imageRef"`
	// Action is one of: deploy | skip | violation
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

// WritePlan serializes p as JSON to <runDir>/plan.json.
func WritePlan(runDir string, p Plan) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", runDir, err)
	}
	path := filepath.Join(runDir, "plan.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(p); encErr != nil {
		_ = f.Close()
		return fmt.Errorf("encode: %w", encErr)
	}
	return f.Close()
}

// ReadPlan reads <runDir>/plan.json.
func ReadPlan(runDir string) (Plan, error) {
	path := filepath.Join(runDir, "plan.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, fmt.Errorf("read %s: %w", path, err)
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("parse: %w", err)
	}
	return p, nil
}
