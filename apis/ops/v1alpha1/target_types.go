package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OperationalTargetSpec binds one concrete workload to a contract.
type OperationalTargetSpec struct {
	// TargetRef is the evaluated object in the OperationalTarget's namespace.
	// Only kinds in the controller's bounded support set are resolved.
	TargetRef TargetReference `json:"targetRef"`

	// ContractRef names an OperationalContract in the same namespace. Its
	// exact identity (UID and spec digest) is resolved at evaluation time.
	ContractRef LocalContractReference `json:"contractRef"`

	// AssertionBindings designate the single authoritative provider of each
	// assertion slot.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	AssertionBindings []AssertionBinding `json:"assertionBindings,omitempty"`

	// DependencyBindings pin each dependency slot to an exact target instance
	// and its exact contract identity.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	DependencyBindings []DependencyBinding `json:"dependencyBindings,omitempty"`

	// EnvelopeSelection names the envelope to highlight. It is an operator
	// projection, not Intent, and changes no capability or envelope result.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	EnvelopeSelection string `json:"envelopeSelection,omitempty"`
}

// TargetReference names an object in the OperationalTarget's namespace.
type TargetReference struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	APIVersion string `json:"apiVersion"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// LocalContractReference names a contract in the same namespace.
type LocalContractReference struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// AssertionBinding designates the provider of one slot.
type AssertionBinding struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Slot     string            `json:"slot"`
	Provider ProviderReference `json:"provider"`
}

// ProviderReference is an evidence provider identity. It names no transport.
type ProviderReference struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
	// Namespace defaults to the OperationalTarget's namespace. Another
	// namespace requires an EvidenceProvider grant there.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// ConfigRevision changes whenever the provider's configuration changes;
	// evidence produced under another revision is inapplicable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ConfigRevision string `json:"configRevision"`
}

// DependencyBinding pins a dependency slot.
type DependencyBinding struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Slot     string                    `json:"slot"`
	Target   DependencyTargetReference `json:"target"`
	Contract ContractPin               `json:"contract"`
}

// DependencyTargetReference names an exact OperationalTarget instance.
type DependencyTargetReference struct {
	// Namespace defaults to the referring target's namespace. Another
	// namespace requires a Dependency grant there.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// UID pins the instance; a recreated target is a different instance.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

// ContractPin is the exact contract identity the dependency must have. Its
// namespace is the dependency target's namespace.
type ContractPin struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	SpecDigest string `json:"specDigest"`
}

// OperationalTargetStatus is the bounded projection of the O1 assessment.
// It carries no evaluation timestamps in its semantic fields and is written
// only when its semantic content changes.
type OperationalTargetStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// AssessedAt changes only when the semantic content of this status changes.
	// +optional
	AssessedAt *metav1.Time `json:"assessedAt,omitempty"`

	// Identity is the exact identity evidence must be produced for.
	// +optional
	Identity ObservedIdentity `json:"identity,omitempty"`

	// Valid reports whether the target could be assessed.
	// +optional
	Valid bool `json:"valid"`

	// Sync is always NotApplicable: no authoritative Sync source is bound.
	// +optional
	Sync string `json:"sync,omitempty"`

	// +optional
	EnvelopeSelection string `json:"envelopeSelection,omitempty"`

	// +kubebuilder:validation:MaxItems=32
	// +optional
	InvalidReasons []StatusReason `json:"invalidReasons,omitempty"`

	// DeniedReferences lists bindings dropped for lack of a matching grant.
	// +kubebuilder:validation:MaxItems=96
	// +optional
	DeniedReferences []DeniedReference `json:"deniedReferences,omitempty"`

	// +kubebuilder:validation:MaxItems=64
	// +optional
	Evidence []EvidenceStatus `json:"evidence,omitempty"`

	// +kubebuilder:validation:MaxItems=64
	// +optional
	Capabilities []CapabilityStatus `json:"capabilities,omitempty"`

	// +kubebuilder:validation:MaxItems=16
	// +optional
	Envelopes []EnvelopeStatus `json:"envelopes,omitempty"`

	// Interaction is the optional, derived and non-authoritative operator
	// interaction summary. It never feeds back into capability truth.
	// +optional
	Interaction *InteractionStatus `json:"interaction,omitempty"`

	// Conditions report controller protocol state only, never operational
	// truth. Type AssessmentReady.
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ObservedIdentity is the applicability identity of a target.
type ObservedIdentity struct {
	// +optional
	TargetUID string `json:"targetUID,omitempty"`
	// +optional
	ResolvedUID string `json:"resolvedUID,omitempty"`
	// +optional
	ContractName string `json:"contractName,omitempty"`
	// +optional
	ContractUID string `json:"contractUID,omitempty"`
	// +optional
	ContractSpecDigest string `json:"contractSpecDigest,omitempty"`
}

// StatusReason is a bounded, machine-readable reason.
type StatusReason struct {
	// +kubebuilder:validation:MaxLength=128
	Code string `json:"code"`
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Subject string `json:"subject,omitempty"`
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Detail string `json:"detail,omitempty"`
}

// DeniedReference is one binding dropped because no grant authorizes it.
// It never reveals whether the referent exists.
type DeniedReference struct {
	Type ReferenceType `json:"type"`
	// +kubebuilder:validation:MaxLength=63
	Slot string `json:"slot"`
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
}

// EvidenceStatus is the evidence state of one of this target's own slots.
type EvidenceStatus struct {
	Slot           string `json:"slot"`
	State          string `json:"state"`
	Provider       string `json:"provider,omitempty"`
	ConfigRevision string `json:"configRevision,omitempty"`
	// +kubebuilder:validation:MaxLength=256
	// +optional
	EvidenceRef string `json:"evidenceRef,omitempty"`
}

// CapabilityStatus is the O1 result for one capability.
type CapabilityStatus struct {
	Type  CapabilityType `json:"type"`
	State string         `json:"state"`
	// +kubebuilder:validation:MaxItems=40
	// +optional
	Reasons []StatusReason `json:"reasons,omitempty"`
}

// EnvelopeStatus is the O1 result for one envelope.
type EnvelopeStatus struct {
	Name  string `json:"name"`
	Class string `json:"class"`
	State string `json:"state"`
	// +kubebuilder:validation:MaxItems=40
	// +optional
	Reasons []StatusReason `json:"reasons,omitempty"`
}

// OperationalTarget binds a concrete workload to an OperationalContract and
// carries the derived, non-actuating assessment in its status.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Valid",type=boolean,JSONPath=`.status.valid`
// +kubebuilder:printcolumn:name="Contract",type=string,JSONPath=`.spec.contractRef.name`
// +kubebuilder:printcolumn:name="Interaction",type=string,JSONPath=`.status.interaction.level`
// +kubebuilder:printcolumn:name="Summary",type=string,priority=1,JSONPath=`.status.interaction.summary`
type OperationalTarget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OperationalTargetSpec   `json:"spec"`
	Status OperationalTargetStatus `json:"status,omitempty"`
}

// OperationalTargetList contains a list of OperationalTarget.
//
// +kubebuilder:object:root=true
type OperationalTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OperationalTarget `json:"items"`
}
