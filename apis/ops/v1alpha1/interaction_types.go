package v1alpha1

// InteractionLevel is the operator interaction level. It is a derived,
// non-authoritative projection: it never feeds back into capability truth,
// the R0 axes or any action authority, and it is not a severity order of
// capability states.
//
// +kubebuilder:validation:Enum=NO_ACTION;AWARENESS;DECISION_REQUIRED;IMMEDIATE_INTERVENTION
type InteractionLevel string

const (
	LevelNoAction              InteractionLevel = "NO_ACTION"
	LevelAwareness             InteractionLevel = "AWARENESS"
	LevelDecisionRequired      InteractionLevel = "DECISION_REQUIRED"
	LevelImmediateIntervention InteractionLevel = "IMMEDIATE_INTERVENTION"
)

// InteractionStatus is the bounded operator summary of one target, derived
// from this target's own status, its current investigation record and the
// operator's declared reference profile. It contains no evaluation
// timestamp; it changes only when its meaning changes.
type InteractionStatus struct {
	Level InteractionLevel `json:"level"`

	// Summary is one deterministic line stating the operational problem.
	// +kubebuilder:validation:MaxLength=256
	Summary string `json:"summary"`

	// LevelReasons say why this level was chosen.
	// +kubebuilder:validation:MaxItems=8
	// +optional
	LevelReasons []StatusReason `json:"levelReasons,omitempty"`

	// Affected capabilities are proven DEGRADED or UNAVAILABLE.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Affected []InteractionCapability `json:"affected,omitempty"`

	// Unaffected capabilities are AVAILABLE on current evidence.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Unaffected []InteractionCapability `json:"unaffected,omitempty"`

	// Unknown capabilities are not proven either way.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Unknown []InteractionCapability `json:"unknown,omitempty"`

	// ConfirmedFacts are this target's slots with current, applicable
	// evidence, with the permitted evidence reference.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	ConfirmedFacts []EvidenceStatus `json:"confirmedFacts,omitempty"`

	// MissingEvidence are this target's slots without current, applicable
	// evidence, and bindings dropped for lack of a grant.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	MissingEvidence []EvidenceStatus `json:"missingEvidence,omitempty"`

	// CauseCandidates are hypotheses from the investigation, never facts.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	CauseCandidates []InteractionCandidate `json:"causeCandidates,omitempty"`

	// Investigation is what BORI already checked for this identity.
	// +optional
	Investigation *InteractionInvestigation `json:"investigation,omitempty"`

	// Responses are declared response candidates for this exact target.
	// They are shown only; nothing here requests, approves or runs them.
	// +kubebuilder:validation:MaxItems=8
	// +optional
	Responses []InteractionResponse `json:"responses,omitempty"`

	// HumanReasons say why a person is needed, when one is.
	// +kubebuilder:validation:MaxItems=8
	// +optional
	HumanReasons []StatusReason `json:"humanReasons,omitempty"`

	// PendingPostConditions are what must still be shown by fresh evidence
	// before any capability may be called AVAILABLE again. An action result
	// or workload readiness never satisfies them.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	PendingPostConditions []StatusReason `json:"pendingPostConditions,omitempty"`

	// IdentityDigest identifies the exact target, workload, contract and
	// provider bindings this summary is for.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	IdentityDigest string `json:"identityDigest,omitempty"`

	// Fingerprint identifies the meaning of this summary within its
	// identity; a consumer raises at most one notice per fingerprint.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Fingerprint string `json:"fingerprint,omitempty"`

	// RecentFingerprints are the last distinct meanings of this identity,
	// newest first, including the current one.
	// +kubebuilder:validation:MaxItems=4
	// +optional
	RecentFingerprints []string `json:"recentFingerprints,omitempty"`

	// Recurring is true when the current meaning was already seen among the
	// recent ones (flapping): it is shown, but is not a new request.
	// +optional
	Recurring bool `json:"recurring,omitempty"`

	// Recurrences counts re-entries into a recent meaning for this identity.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Recurrences int32 `json:"recurrences,omitempty"`
}

// InteractionCapability is one capability with its O1 state and the
// (already redacted) O1 reasons that explain it.
type InteractionCapability struct {
	Type  CapabilityType `json:"type"`
	State string         `json:"state"`
	// +kubebuilder:validation:MaxItems=4
	// +optional
	Reasons []StatusReason `json:"reasons,omitempty"`
}

// InteractionCandidate is one investigation hypothesis.
type InteractionCandidate struct {
	// ID is the O3 candidate identity, copied unchanged. Its longest legal
	// form is "<domain 253>/<name 63>@<revision 63>/envelope:<63>/assertion:<63>"
	// = 528 characters.
	// +kubebuilder:validation:MaxLength=528
	ID string `json:"id"`
	// +kubebuilder:validation:MaxLength=32
	Kind string `json:"kind"`
	// +kubebuilder:validation:MaxLength=253
	Ref string `json:"ref"`
	// Status is Open, Maintained or Refuted.
	// +kubebuilder:validation:MaxLength=16
	Status string `json:"status"`
}

// InteractionInvestigation summarizes the latest investigation episode of
// this exact identity by meaning only: its outcome class and the hypotheses
// about capabilities that are not AVAILABLE now. It carries no kind,
// counters or times, so routine evidence refreshes do not change it.
type InteractionInvestigation struct {
	// Outcome is NotRun, InProgress, Concluded, NoAllowedQuery,
	// BudgetExhausted, Cancelled, CapacityLimited or Superseded.
	// +kubebuilder:validation:MaxLength=32
	Outcome string `json:"outcome"`
	// +optional
	Refuted int32 `json:"refuted,omitempty"`
	// +optional
	Maintained int32 `json:"maintained,omitempty"`
	// +optional
	Open int32 `json:"open,omitempty"`
}

// InteractionResponse is one declared response candidate and why it can or
// cannot be taken now. It is display-only.
type InteractionResponse struct {
	// Name is the declared response identity, name@revision.
	// +kubebuilder:validation:MaxLength=128
	Name string `json:"name"`
	// Target is the exact target the action is declared for.
	// +kubebuilder:validation:MaxLength=512
	Target string `json:"target"`
	// For is the capability this response is declared for.
	For CapabilityType `json:"for"`
	// Owner is the declared execution owner; empty means undeclared.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Owner string `json:"owner,omitempty"`
	// +optional
	RequiresApproval bool `json:"requiresApproval,omitempty"`
	// +kubebuilder:validation:MaxItems=4
	// +optional
	Risks []string `json:"risks,omitempty"`
	// Preconditions with their proof state (proven|unproven|unmet).
	// +kubebuilder:validation:MaxItems=8
	// +optional
	Preconditions []StatusReason `json:"preconditions,omitempty"`
	// Status is Ready, ApprovalRequired, SafetyUnproven, OwnerUndeclared,
	// OwnerConflict or Inapplicable.
	// +kubebuilder:validation:MaxLength=32
	Status string `json:"status"`
}
