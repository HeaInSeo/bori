package action

import (
	"time"

	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Phase is where a proposal or one of its executions stands. Proposal
// phases (Blocked, AwaitingApproval, Rejected) are derived each step;
// execution phases are journal state.
type Phase string

const (
	PhaseBlocked          Phase = "Blocked"
	PhaseAwaitingApproval Phase = "AwaitingApproval"
	PhaseRejected         Phase = "Rejected"
	// PhaseDispatching: the handoff intent is persisted and may have been
	// sent; no acknowledgement is recorded.
	PhaseDispatching Phase = "Dispatching"
	// PhaseAccepted: the provider durably accepted the handoff. Acceptance
	// only: nothing has been done yet as far as BORI knows.
	PhaseAccepted  Phase = "Accepted"
	PhaseSucceeded Phase = "Succeeded"
	PhaseFailed    Phase = "Failed"
	// PhaseTimedOut: no acknowledgement or no result in time. The outcome
	// is unknown and is never treated as success.
	PhaseTimedOut Phase = "TimedOut"
	// PhaseWithdrawn: an unacknowledged handoff whose revalidation failed;
	// it is not sent again. An earlier send may still have been delivered,
	// so the outcome is unknown as for TimedOut.
	PhaseWithdrawn Phase = "Withdrawn"
)

func (p Phase) inFlight() bool { return p == PhaseDispatching || p == PhaseAccepted }

// occupies reports whether the attempt may still hold live mutation
// responsibility at its provider: it was persisted for sending and neither
// a matching definitive result nor an explicit refusal is recorded. A
// timeout or a withdrawal changes only what is shown; the attempt keeps its
// target and is still polled by key until its provider answers definitively.
func (p Phase) occupies() bool {
	return p == PhaseDispatching || p == PhaseAccepted || p == PhaseTimedOut || p == PhaseWithdrawn
}

// Recovery is the RecoveryAssessment of one execution: whether the expected
// capabilities are AVAILABLE on evidence observed after the execution ended.
type Recovery string

const (
	RecoveryNone         Recovery = ""
	RecoveryPending      Recovery = "Pending"
	RecoveryRecovered    Recovery = "Recovered"
	RecoveryPartial      Recovery = "Partial"
	RecoveryNotRecovered Recovery = "NotRecovered"
	RecoveryUnconfirmed  Recovery = "Unconfirmed"
	RecoveryInapplicable Recovery = "Inapplicable"
)

func (r Recovery) final() bool {
	return r != RecoveryNone && r != RecoveryPending
}

// Reason codes of proposal views.
const (
	ReasonOwnerUndeclared       = "owner-undeclared"
	ReasonProviderUnregistered  = "provider-unregistered"
	ReasonAuthorityInsufficient = "authority-insufficient"
	ReasonPreconditionUnproven  = "precondition-unproven"
	ReasonPreconditionUnmet     = "precondition-unmet"
	ReasonJournalNotDurable     = "journal-not-durable"
	ReasonTargetBusy            = "target-busy"
	ReasonAttemptsExhausted     = "attempts-exhausted"
	ReasonExecutedNotRecovered  = "executed-not-recovered"
	ReasonOutcomeUnknown        = "outcome-unknown"
	ReasonIdentityChanged       = "identity-changed"
	ReasonJournalWriteFailed    = "journal-write-failed"

	// Why a person must decide (declared).
	ReasonApprovalDeclared = "approval-declared"
	ReasonRiskDeclared     = "risk-declared"
	ReasonMayInterrupt     = "may-interrupt"
	// Derived from the other declared responses (as O4 does).
	ReasonNoPriorityAuthority = "no-priority-authority"
	ReasonOwnerConflict       = "owner-conflict"

	// Why a submitted decision was not counted.
	ReasonApprovalUnverified     = "approval-unverified"
	ReasonApproverNotAllowed     = "approver-not-allowed"
	ReasonApprovalDigestMismatch = "approval-digest-mismatch"
	ReasonApprovalExpired        = "approval-expired"
	ReasonApprovalFuture         = "approval-future"

	// Recovery detail per expected capability.
	ReasonRecoveredOnFreshEvidence = "available-on-post-execution-evidence"
	ReasonNotRecovered             = "not-available-on-post-execution-evidence"
	ReasonRecoveryUnproven         = "unproven-on-post-execution-evidence"
)

// Reason is one code with the input it is about.
type Reason struct {
	Code    string
	Subject string
}

// Binding is the exact identity a proposal is bound to. Any difference
// makes an approval, a result or a recovery of the old binding inapplicable.
type Binding struct {
	Target      operations.TargetIdentity
	ResolvedUID string
	Contract    operations.ContractIdentity
	// Providers are "slot=provider@configRevision", sorted.
	Providers []string
	// Dependencies are "slot=targetUID/contract", sorted.
	Dependencies []string
}

func (b Binding) equal(o Binding) bool {
	if b.Target != o.Target || b.ResolvedUID != o.ResolvedUID || b.Contract != o.Contract ||
		len(b.Providers) != len(o.Providers) || len(b.Dependencies) != len(o.Dependencies) {
		return false
	}
	for i := range b.Providers {
		if b.Providers[i] != o.Providers[i] {
			return false
		}
	}
	for i := range b.Dependencies {
		if b.Dependencies[i] != o.Dependencies[i] {
			return false
		}
	}
	return true
}

// Approval records which decision authorised a handoff.
type Approval struct {
	// Kind is "human" or "policy".
	Kind string
	// Principal is the verified approver, or "policy:<profile revision>".
	Principal  string
	DecisionID string
}

// CapRecovery is the post-execution state of one expected capability.
type CapRecovery struct {
	Type  operations.CapabilityType
	State operations.CapabilityState
}

// Record is one execution attempt in the journal.
type Record struct {
	// Key is the idempotency key "<proposal ID>/<attempt>".
	Key        string
	ProposalID string
	// Subject identifies the proposal subject across episodes.
	Subject   string
	TargetUID string
	Digest    string
	Attempt   int
	Phase     Phase
	// Fence is the journal epoch of the engine that owns the record;
	// SentFence is the fence the last send carried. Receipts and results
	// must carry SentFence.
	Fence     uint64
	SentFence uint64
	Sends     int
	Approval  Approval
	Binding   Binding
	Action    interaction.ActionRef
	Provider  string
	Owner     string
	For       operations.CapabilityType
	Expected  []operations.CapabilityType
	// Execution is the declared execution contract the attempt was derived
	// and approved under, copied verbatim from the profile. It is journal
	// state only, never sent to the provider: a reader can map the attempt
	// to its exact declaration and its own approvers and approval age.
	Execution interaction.Execution
	// Times are BORI's own clock, never the provider's. CompletedAt closes
	// the shown phase (a result, a refusal, a timeout or a withdrawal);
	// EndedAt is set only when a matching definitive result of the provider
	// ends the execution, and is the only end recovery is assessed from.
	CreatedAt   time.Time
	LastSentAt  time.Time
	AckedAt     time.Time
	CompletedAt time.Time
	EndedAt     time.Time
	ProviderRef string

	AckTimeout        time.Duration
	CompletionTimeout time.Duration
	RecoveryWindow    time.Duration
	Retryable         bool
	MaxAttempts       int
	MaxSends          int

	Recovery       Recovery
	RecoveryDetail []CapRecovery
	// Version is the journal's compare-and-swap version.
	Version uint64
}

// View is the operator-facing projection of one proposal or execution.
type View struct {
	ProposalID string
	Digest     string
	Action     interaction.ActionRef
	For        operations.CapabilityType
	Phase      Phase
	Attempt    int
	Reasons    []Reason
	Recovery   Recovery
	Detail     []CapRecovery
}
