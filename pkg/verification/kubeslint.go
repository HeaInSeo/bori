package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/HeaInSeo/bori/pkg/artifact"
)

// KubeSlintProvider implements Provider using the slint-gate CLI.
//
// It calls slint-gate with --fail-on NEVER so that:
//   - run artifacts (BoriVerificationRun) are always written regardless of gate result
//   - bori applies FailOn policy independently, per-profile
//
// This matches the recommended pattern from docs/kube-slint-integration.md §slint-gate 호출 방식.
type KubeSlintProvider struct {
	SlintGateBin string
}

// NewKubeSlintProvider returns a Provider backed by slint-gate.
// If slintGateBin is empty, "slint-gate" is resolved via PATH.
func NewKubeSlintProvider(slintGateBin string) Provider {
	if slintGateBin == "" {
		slintGateBin = "slint-gate"
	}
	return &KubeSlintProvider{SlintGateBin: slintGateBin}
}

func (p *KubeSlintProvider) Run(ctx context.Context, req Request) (*Result, error) {
	if err := os.MkdirAll(req.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", req.OutDir, err)
	}

	gatePath := filepath.Join(req.OutDir, req.App+"-gate-summary.json")
	startedAt := time.Now().UTC()

	// Call slint-gate with --fail-on NEVER: bori owns the promotion decision.
	cmd := exec.CommandContext(ctx, p.SlintGateBin,
		"--measurement-summary", req.MeasurementSummaryPath,
		"--policy", req.PolicyPath,
		"--output", gatePath,
		"--fail-on", "NEVER",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	// Read JSON result regardless of exit code.
	if _, statErr := os.Stat(gatePath); statErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("slint-gate: %w", runErr)
		}
		return nil, fmt.Errorf("slint-gate produced no output at %s", gatePath)
	}

	gateResult, message, err := readGateSummary(gatePath)
	if err != nil {
		return nil, fmt.Errorf("read gate summary: %w", err)
	}
	finishedAt := time.Now().UTC()

	// Apply bori-side FailOn policy.
	promotionDecision := "eligible"
	if IsBlocking(gateResult, req.FailOn) {
		promotionDecision = "blocked"
	}

	// Write BoriVerificationRun — slint-gate result wrapped with bori metadata.
	run := artifact.VerificationRun{
		SchemaVersion:          "bori.verificationRun.v1",
		RunID:                  req.RunID,
		App:                    req.App,
		Provider:               "kube-slint",
		MeasurementSummaryPath: req.MeasurementSummaryPath,
		GateSummaryPath:        gatePath,
		GateResult:             string(gateResult),
		PromotionDecision:      promotionDecision,
		StartedAt:              startedAt,
		FinishedAt:             finishedAt,
	}
	runPath := filepath.Join(req.OutDir, req.App+"-verification-run.json")
	if writeErr := artifact.WriteVerificationRun(runPath, run); writeErr != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not write verification run: %v\n", writeErr)
	}

	return &Result{
		App:         req.App,
		GateResult:  gateResult,
		Message:     message,
		SummaryPath: req.MeasurementSummaryPath,
		GatePath:    gatePath,
	}, nil
}

type gateSummaryMinimal struct {
	GateResult     string `json:"gate_result"`
	OverallMessage string `json:"overall_message"`
}

func readGateSummary(path string) (GateResult, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GateResultNoGrade, "", err
	}
	var gs gateSummaryMinimal
	if err := json.Unmarshal(data, &gs); err != nil {
		return GateResultNoGrade, "", err
	}
	result := GateResult(gs.GateResult)
	if result == "" {
		result = GateResultNoGrade
	}
	return result, gs.OverallMessage, nil
}
