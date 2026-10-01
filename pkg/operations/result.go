package operations

// CapabilityState is the derived state of one capability. States are not
// ordered; there is no worst-of.
type CapabilityState string

const (
	Available   CapabilityState = "AVAILABLE"
	Degraded    CapabilityState = "DEGRADED"
	Unavailable CapabilityState = "UNAVAILABLE"
	Unknown     CapabilityState = "UNKNOWN"
)

// EnvelopeState is the AllOf result of one envelope.
type EnvelopeState string

const (
	Satisfied   EnvelopeState = "SATISFIED"
	Unsatisfied EnvelopeState = "UNSATISFIED"
	EnvUnknown  EnvelopeState = "UNKNOWN"
)

// SyncState is reported only to make the R0 axis mapping explicit. O1 has no
// authoritative Sync source and never derives one.
type SyncState string

const SyncNotApplicable SyncState = "NotApplicable"

// EvidenceStatus classifies the evidence for one assertion slot.
type EvidenceStatus string

const (
	EvidenceCurrent             EvidenceStatus = "Current"
	EvidenceUnbound             EvidenceStatus = "Unbound"
	EvidenceMissing             EvidenceStatus = "Missing"
	EvidenceStale               EvidenceStatus = "Stale"
	EvidenceInapplicable        EvidenceStatus = "Inapplicable"
	EvidenceProviderUnavailable EvidenceStatus = "ProviderUnavailable"
	EvidenceConflicting         EvidenceStatus = "Conflicting"
	EvidenceTypeMismatch        EvidenceStatus = "TypeMismatch"
)

// ReasonCode is a stable machine-readable reason.
type ReasonCode string

const (
	// Proven-unmet requirements.
	ReasonPredicateUnmet  ReasonCode = "predicate-unmet"
	ReasonDependencyUnmet ReasonCode = "dependency-unmet"
	ReasonLocalUnmet      ReasonCode = "local-capability-unmet"
	ReasonEnvelopeUnmet   ReasonCode = "envelope-unsatisfied"

	// Unproven requirements (always UNKNOWN).
	ReasonEvidenceUnbound            ReasonCode = "evidence-unbound"
	ReasonEvidenceMissing            ReasonCode = "evidence-missing"
	ReasonEvidenceStale              ReasonCode = "evidence-stale"
	ReasonEvidenceTargetReplaced     ReasonCode = "evidence-inapplicable-target-replaced"
	ReasonEvidenceContractChanged    ReasonCode = "evidence-inapplicable-contract-changed"
	ReasonEvidenceProviderChanged    ReasonCode = "evidence-inapplicable-provider-changed"
	ReasonProviderUnavailable        ReasonCode = "provider-unavailable"
	ReasonConflictingEvidence        ReasonCode = "conflicting-authoritative-evidence"
	ReasonEvidenceTypeMismatch       ReasonCode = "evidence-type-mismatch"
	ReasonDependencyUnbound          ReasonCode = "dependency-unbound"
	ReasonDependencyTargetMissing    ReasonCode = "dependency-target-missing"
	ReasonDependencyTargetInvalid    ReasonCode = "dependency-target-invalid"
	ReasonDependencyContractMismatch ReasonCode = "dependency-contract-mismatch"
	ReasonDependencyCapabilityAbsent ReasonCode = "dependency-capability-not-provided"
	ReasonDependencyUnknown          ReasonCode = "dependency-unknown"
	ReasonLocalUnknown               ReasonCode = "local-capability-unknown"
	ReasonEnvelopeUnknown            ReasonCode = "envelope-unknown"
	ReasonConflictingImpact          ReasonCode = "conflicting-impact"

	// Target invalidity.
	ReasonInvalidContract            ReasonCode = "invalid-contract"
	ReasonContractUnresolved         ReasonCode = "contract-unresolved"
	ReasonContractAmbiguous          ReasonCode = "contract-ambiguous"
	ReasonCrossNamespaceContract     ReasonCode = "cross-namespace-contract-ref"
	ReasonDuplicateTargetUID         ReasonCode = "duplicate-target-uid"
	ReasonDuplicateAssertionBinding  ReasonCode = "duplicate-assertion-binding"
	ReasonDuplicateDependencyBinding ReasonCode = "duplicate-dependency-binding"
	ReasonInvalidBinding             ReasonCode = "invalid-binding"
	ReasonUnknownEnvelopeSelection   ReasonCode = "unknown-envelope-selection"
	ReasonDependencyCycle            ReasonCode = "dependency-cycle"
)

// Reason attributes a result to an exact input. Subject names the input, for
// example "assertion:storage-writable" or "dependency:<targetUID>/<capability>".
type Reason struct {
	Code    ReasonCode
	Subject string
	Detail  string
}

// Assessment is the complete evaluator output. It contains no evaluation
// timestamp, so identical inputs give identical, comparable output.
type Assessment struct {
	Targets []TargetAssessment
}

// TargetAssessment is the per-target result. When Valid is false no
// capability or envelope is assessed and InvalidReasons explains why.
type TargetAssessment struct {
	Target            TargetIdentity
	Contract          ContractIdentity
	Valid             bool
	InvalidReasons    []Reason
	Sync              SyncState
	EnvelopeSelection string
	Evidence          []SlotEvidence
	Capabilities      []CapabilityResult
	Envelopes         []EnvelopeResult
}

// SlotEvidence reports the evidence status of one assertion slot.
type SlotEvidence struct {
	Slot        string
	Status      EvidenceStatus
	Provider    ProviderIdentity
	EvidenceRef string
}

// CapabilityResult is the derived state of one capability with its reasons.
type CapabilityResult struct {
	Type    CapabilityType
	State   CapabilityState
	Reasons []Reason
}

// EnvelopeResult is the derived state of one envelope with its reasons.
type EnvelopeResult struct {
	Name    string
	Class   EnvelopeClass
	State   EnvelopeState
	Reasons []Reason
}

// Target returns the assessment of the target with the given UID.
func (a Assessment) Target(uid string) (TargetAssessment, bool) {
	for _, t := range a.Targets {
		if t.Target.UID == uid {
			return t, true
		}
	}
	return TargetAssessment{}, false
}

// Capability returns the result for the capability with the given display
// name within this target.
func (t TargetAssessment) Capability(name string) (CapabilityResult, bool) {
	for _, c := range t.Capabilities {
		if c.Type.Name == name {
			return c, true
		}
	}
	return CapabilityResult{}, false
}

// Envelope returns the result for the named envelope within this target.
func (t TargetAssessment) Envelope(name string) (EnvelopeResult, bool) {
	for _, e := range t.Envelopes {
		if e.Name == name {
			return e, true
		}
	}
	return EnvelopeResult{}, false
}

// CapabilityRef names one capability of one target in a Summary.
type CapabilityRef struct {
	TargetUID  string
	Capability CapabilityType
}

// Summary is the operator-facing projection of an Assessment. It is derived,
// non-authoritative and never collapses to a single health value.
type Summary struct {
	Affected   []CapabilityRef // DEGRADED or UNAVAILABLE
	Unaffected []CapabilityRef // AVAILABLE
	Unknown    []CapabilityRef // UNKNOWN
	Invalid    []string        // target UIDs that could not be assessed
}

// Summarize groups capabilities by state, preserving assessment order.
func (a Assessment) Summarize() Summary {
	var s Summary
	for _, t := range a.Targets {
		if !t.Valid {
			s.Invalid = append(s.Invalid, t.Target.UID)
			continue
		}
		for _, c := range t.Capabilities {
			ref := CapabilityRef{TargetUID: t.Target.UID, Capability: c.Type}
			switch c.State {
			case Available:
				s.Unaffected = append(s.Unaffected, ref)
			case Degraded, Unavailable:
				s.Affected = append(s.Affected, ref)
			default:
				s.Unknown = append(s.Unknown, ref)
			}
		}
	}
	return s
}
