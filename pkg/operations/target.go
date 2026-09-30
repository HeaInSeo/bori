package operations

import "time"

// Target binds one concrete workload instance to an exact contract.
type Target struct {
	Identity TargetIdentity
	// ResolvedUID is the UID of the concrete resource the target refers to.
	// Replacing that resource invalidates evidence about the old one.
	ResolvedUID string
	// ContractRef must be in the target's namespace (v0.1 has no
	// cross-namespace contract reference).
	ContractRef        ContractIdentity
	AssertionBindings  []AssertionBinding
	DependencyBindings []DependencyBinding
	// EnvelopeSelection names the envelope the operator wants highlighted.
	// It is not Intent or Current Desired and changes no capability truth.
	EnvelopeSelection string
}

// AssertionBinding designates the single authoritative provider of a slot.
type AssertionBinding struct {
	Slot     string
	Provider ProviderIdentity
}

// DependencyBinding pins a dependency slot to an exact target instance and its
// exact contract. If either changes, the dependency is UNKNOWN until rebound.
type DependencyBinding struct {
	Slot      string
	TargetUID string
	Contract  ContractIdentity
}

// Outcome distinguishes a provider-reported value from a provider reporting
// that it cannot currently observe the subject.
type Outcome string

const (
	OutcomeValue               Outcome = "Value"
	OutcomeProviderUnavailable Outcome = "ProviderUnavailable"
)

// Observation is one provider statement about one assertion slot of one exact
// binding. Raw telemetry never enters the evaluator; only this bounded form.
type Observation struct {
	Key     ApplicabilityKey
	Outcome Outcome
	Value   Value
	// ObservedAt is when the provider observed the fact. ValidUntil, when set,
	// is the provider's own currentness bound; the slot MaxAge always applies.
	ObservedAt  time.Time
	ValidUntil  time.Time
	EvidenceRef string
}

// Snapshot is the complete, explicit input of one evaluation. At is the
// evaluation instant; the evaluator never reads a clock.
type Snapshot struct {
	At           time.Time
	Contracts    []Contract
	Targets      []Target
	Observations []Observation
}
