package action

import (
	"bytes"
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

// copy returns d with its own Proof, so a stored copy never shares memory
// with the source or a reader.
func (d Decision) copy() Decision {
	d.Proof = append(d.Proof[:0:0], d.Proof...)
	return d
}

// same reports whether two decisions are identical in every signed field
// and in their proof.
func (d Decision) same(o Decision) bool {
	return d.ID == o.ID && d.ProposalID == o.ProposalID && d.Digest == o.Digest && d.Principal == o.Principal &&
		d.Verdict == o.Verdict && d.DecidedAt.Equal(o.DecidedAt) && bytes.Equal(d.Proof, o.Proof)
}
