// Package verification defines the bori verification provider interface
// and gate result semantics.
package verification

import "context"

// FailOn controls when bori treats a gate result as promotion-blocking.
type FailOn string

const (
	// FailOnNever means bori reads the JSON result and decides — slint-gate is
	// called with --fail-on NEVER. This is the recommended mode because it
	// guarantees run artifacts are written regardless of gate outcome.
	FailOnNever FailOn = "NEVER"

	// FailOnFail blocks promotion only when gate_result=FAIL.
	FailOnFail FailOn = "FAIL"

	// FailOnFailOrNoGrade blocks promotion for FAIL and NO_GRADE.
	// Recommended for promotion and release gates.
	FailOnFailOrNoGrade FailOn = "FAIL_OR_NOGRADE"

	// FailOnWarn blocks promotion for WARN and above.
	FailOnWarn FailOn = "WARN"
)

// GateResult is a normalized gate outcome.
type GateResult string

const (
	GateResultPass    GateResult = "PASS"
	GateResultWarn    GateResult = "WARN"
	GateResultNoGrade GateResult = "NO_GRADE"
	GateResultFail    GateResult = "FAIL"
)

// severity maps GateResult to a comparable integer.
// Higher value = more severe. Severity order: PASS < WARN < NO_GRADE < FAIL.
func severity(g GateResult) int {
	switch g {
	case GateResultPass:
		return 0
	case GateResultWarn:
		return 1
	case GateResultNoGrade:
		return 2
	case GateResultFail:
		return 3
	default:
		return 2 // unknown result is treated as NO_GRADE
	}
}

// Max returns the more severe of two gate results.
func Max(a, b GateResult) GateResult {
	if severity(a) >= severity(b) {
		return a
	}
	return b
}

// IsBlocking reports whether result blocks promotion under the given failOn policy.
func IsBlocking(result GateResult, failOn FailOn) bool {
	switch failOn {
	case FailOnNever:
		return false
	case FailOnFail:
		return result == GateResultFail
	case FailOnFailOrNoGrade:
		return result == GateResultFail || result == GateResultNoGrade
	case FailOnWarn:
		return severity(result) >= severity(GateResultWarn)
	default:
		// Default to FAIL_OR_NOGRADE for unknown policies.
		return result == GateResultFail || result == GateResultNoGrade
	}
}

// Request is the input to a verification provider.
type Request struct {
	App        string
	Profile    string
	PolicyPath string
	FailOn     FailOn
	OutDir     string
}

// Result is the output of a verification provider.
type Result struct {
	App         string
	GateResult  GateResult
	Message     string
	SummaryPath string
	GatePath    string
}

// Provider is the interface for verification backends (e.g. kube-slint).
type Provider interface {
	Run(ctx context.Context, req Request) (*Result, error)
}
