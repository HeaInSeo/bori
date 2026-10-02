package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValueType is the declared type of an assertion slot.
// +kubebuilder:validation:Enum=Boolean;Integer;String
type ValueType string

// Operator is one of the bounded typed predicate operators.
// +kubebuilder:validation:Enum=IsTrue;IsFalse;Eq;NotEq;Gte;Lte
type Operator string

// Impact is the declared effect of a proven-unmet requirement.
// +kubebuilder:validation:Enum=DEGRADED;UNAVAILABLE
type Impact string

// EnvelopeClass distinguishes minimum from recommended operating conditions.
// +kubebuilder:validation:Enum=Minimum;Recommended
type EnvelopeClass string

// OperationalContractSpec is a reusable, app-neutral operational contract. It
// is the wire form of an O1 operations.Contract without its identity, which is
// derived from object metadata and the deterministic spec digest.
type OperationalContractSpec struct {
	// Assertions are the typed current facts the contract needs.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Assertions []AssertionSlot `json:"assertions,omitempty"`

	// DependencySlots are placeholders for other OperationalTargets; the
	// concrete target is chosen by each OperationalTarget's binding.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	DependencySlots []DependencySlot `json:"dependencySlots,omitempty"`

	// Capabilities are derived only from the requirements they reference.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Capabilities []Capability `json:"capabilities"`

	// Envelopes are independent AllOf assessments over local assertions.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Envelopes []Envelope `json:"envelopes,omitempty"`
}

// AssertionSlot declares a typed current fact.
type AssertionSlot struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	Type ValueType `json:"type"`

	// Enum optionally closes a String slot to a set of values.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +optional
	Enum []string `json:"enum,omitempty"`

	// MaxAge bounds how long after its observation a value may be used.
	MaxAge metav1.Duration `json:"maxAge"`
}

// DependencySlot names a placeholder for another OperationalTarget.
type DependencySlot struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
}

// CapabilityType is the qualified, cross-contract identity of a capability.
// Name doubles as the contract-local alias and must be unique in a contract.
type CapabilityType struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Domain string `json:"domain"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Revision string `json:"revision"`
}

// Capability is a user- or platform-facing function.
type Capability struct {
	Type CapabilityType `json:"type"`

	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Requirements []Requirement `json:"requirements"`
}

// Requirement references exactly one input and declares the impact when that
// input is proven unmet. An unproven input always yields UNKNOWN.
// +kubebuilder:validation:XValidation:rule="[has(self.predicate), has(self.dependency), has(self.localCapability), has(self.envelope)].filter(x, x).size() == 1",message="exactly one of predicate, dependency, localCapability or envelope must be set"
type Requirement struct {
	// +optional
	Predicate *Predicate `json:"predicate,omitempty"`
	// +optional
	Dependency *DependencyRequirement `json:"dependency,omitempty"`
	// LocalCapability is the name of another capability of this contract.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	LocalCapability string `json:"localCapability,omitempty"`
	// Envelope is the name of an envelope of this contract.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Envelope string `json:"envelope,omitempty"`

	OnUnmet Impact `json:"onUnmet"`
}

// DependencyRequirement requires the exact qualified capability of the target
// bound to Slot.
type DependencyRequirement struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Slot       string         `json:"slot"`
	Capability CapabilityType `json:"capability"`
}

// Predicate tests one assertion slot of the same target.
type Predicate struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Assertion string   `json:"assertion"`
	Operator  Operator `json:"operator"`
	// Operand is required for Eq/NotEq/Gte/Lte and must be absent for
	// IsTrue/IsFalse; type/operator pairing is validated against the slot.
	// +optional
	Operand *Operand `json:"operand,omitempty"`
}

// Operand is one typed value.
// +kubebuilder:validation:XValidation:rule="[has(self.boolean), has(self.integer), has(self.string)].filter(x, x).size() == 1",message="operand must set exactly one typed value"
type Operand struct {
	// +optional
	Boolean *bool `json:"boolean,omitempty"`
	// +optional
	Integer *int64 `json:"integer,omitempty"`
	// +kubebuilder:validation:MaxLength=253
	// +optional
	String *string `json:"string,omitempty"`
}

// Envelope is an AllOf assessment over local assertions.
type Envelope struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name  string        `json:"name"`
	Class EnvelopeClass `json:"class"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Requirements []Predicate `json:"requirements"`
}

// OperationalContractStatus reports the derived identity and validity of the
// contract. SpecDigest is what OperationalTarget dependency bindings pin.
type OperationalContractStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// SpecDigest is the deterministic digest of the semantic spec.
	// +optional
	SpecDigest string `json:"specDigest,omitempty"`
	// Valid reports whether the contract passes O1 contract validation.
	// +optional
	Valid bool `json:"valid"`
	// ValidationErrors lists O1 validation errors, bounded.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ValidationErrors []string `json:"validationErrors,omitempty"`
}

// OperationalContract is a namespaced, immutable operational contract. New
// semantics are published as a new object, never by editing this one.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Valid",type=boolean,JSONPath=`.status.valid`
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.status.specDigest`,priority=1
type OperationalContract struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="OperationalContract spec is immutable; publish a new contract object for new semantics"
	Spec   OperationalContractSpec   `json:"spec"`
	Status OperationalContractStatus `json:"status,omitempty"`
}

// OperationalContractList contains a list of OperationalContract.
//
// +kubebuilder:object:root=true
type OperationalContractList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OperationalContract `json:"items"`
}
