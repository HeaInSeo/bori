package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// GateRunner is the default Runner.
// It builds sli-summary.json from metric deltas, then shells out to slint-gate.
type GateRunner struct {
	// SlintGateBin is the path to the slint-gate binary.
	// Defaults to "slint-gate" (resolved via PATH).
	SlintGateBin string
}

// NewGateRunner returns a GateRunner using slint-gate from PATH.
func NewGateRunner() Runner {
	return &GateRunner{SlintGateBin: "slint-gate"}
}

func (r *GateRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := os.MkdirAll(req.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", req.OutDir, err)
	}

	// 1. Build and write sli-summary.json from metric deltas
	sum := buildDeltaSummary(req)
	summaryPath := filepath.Join(req.OutDir, req.App+"-sli-summary.json")
	if err := writeSummary(summaryPath, sum); err != nil {
		return nil, fmt.Errorf("write summary: %w", err)
	}

	// 2. Shell out to slint-gate
	gatePath := filepath.Join(req.OutDir, req.App+"-gate-summary.json")
	bin := r.SlintGateBin
	cmd := exec.CommandContext(ctx, bin,
		"--measurement-summary", summaryPath,
		"--policy", req.PolicyPath,
		"--output", gatePath,
		"--fail-on", "FAIL",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// slint-gate exits non-zero when gate_result=FAIL and --fail-on=FAIL
		// still attempt to read the output JSON before propagating the error
		if _, statErr := os.Stat(gatePath); statErr != nil {
			return nil, fmt.Errorf("slint-gate: %w", err)
		}
	}

	// 3. Read gate result from output JSON
	gateResult, message, err := readGateResult(gatePath)
	if err != nil {
		return nil, fmt.Errorf("read gate result: %w", err)
	}

	return &RunResult{
		App:         req.App,
		GateResult:  gateResult,
		SummaryPath: summaryPath,
		GatePath:    gatePath,
		Message:     message,
	}, nil
}

type gateSummaryMinimal struct {
	GateResult     string `json:"gate_result"`
	OverallMessage string `json:"overall_message"`
}

func readGateResult(path string) (gateResult, message string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var gs gateSummaryMinimal
	if err := json.Unmarshal(data, &gs); err != nil {
		return "", "", err
	}
	return gs.GateResult, gs.OverallMessage, nil
}
