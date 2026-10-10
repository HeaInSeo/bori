package action

import (
	"context"
	"time"
)

// Verdict is an approver's decision.
type Verdict string

const (
	Approve Verdict = "Approve"
	Reject  Verdict = "Reject"
)

// Decision is one approver's decision about one exact proposal. It is
// counted only when a Verifier authenticates it for exactly these fields;
// the right to edit a Kubernetes object is never a decision.
type Decision struct {
	ID         string
	ProposalID string
	Digest     string
	Principal  string
	Verdict    Verdict
	DecidedAt  time.Time
	Proof      []byte
}

// Verifier authenticates a decision: it reports whether Proof binds
// Principal to exactly this ID, proposal, digest, verdict and time. The
// identity system behind it is OPEN (API v0.1 §19).
type Verifier interface {
	Verify(d Decision) bool
}

// DecisionSource supplies submitted decisions from outside the cluster
// object model.
type DecisionSource interface {
	Decisions(ctx context.Context) ([]Decision, error)
}
