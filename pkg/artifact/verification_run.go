package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// VerificationRun wraps slint-gate output with bori-side metadata.
// It is written to <runDir>/evidence/<app>-verification-run.json after every
// slint-gate evaluation, regardless of gate_result.
type VerificationRun struct {
	SchemaVersion          string    `json:"schemaVersion"`
	RunID                  string    `json:"runId"`
	App                    string    `json:"app,omitempty"`
	Release                string    `json:"release,omitempty"`
	Environment            string    `json:"environment,omitempty"`
	Provider               string    `json:"provider"`
	MeasurementSummaryPath string    `json:"measurementSummaryPath"`
	GateSummaryPath        string    `json:"gateSummaryPath"`
	GateResult             string    `json:"gateResult"`        // PASS|WARN|FAIL|NO_GRADE
	PromotionDecision      string    `json:"promotionDecision"` // eligible|blocked
	StartedAt              time.Time `json:"startedAt"`
	FinishedAt             time.Time `json:"finishedAt"`
}

// WriteVerificationRun serializes r as JSON to path.
func WriteVerificationRun(path string, r VerificationRun) error {
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
