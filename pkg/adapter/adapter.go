package adapter

import (
	"context"
	"time"
)

// AppSnapshot holds raw prometheus metric values for one app at one point in time.
type AppSnapshot struct {
	App    string
	At     time.Time
	Values map[string]float64
}

// RunRequest is the input to a gate evaluation.
type RunRequest struct {
	Profile    string      // devspace | kind | multipass
	App        string      // matches .bori/component.yaml name
	PolicyPath string      // path to .bori/policy.<profile>.yaml
	Before     AppSnapshot // metrics before smoke
	After      AppSnapshot // metrics after smoke
	OutDir     string      // output directory for artifacts
}

// RunResult is the output of a gate evaluation.
type RunResult struct {
	App         string
	GateResult  string // PASS | FAIL | WARN | NO_GRADE
	SummaryPath string
	GatePath    string
	Message     string
}

// Runner converts before/after metric snapshots into a slint-gate evaluation.
// Implementations shell out to the slint-gate binary; kube-slint is not imported.
type Runner interface {
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
}
