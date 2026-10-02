package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReferenceType scopes a grant to one kind of reference. Grants of different
// types never authorize each other. v1alpha1 supports no Secret and no action
// references.
// +kubebuilder:validation:Enum=Dependency;EvidenceProvider
type ReferenceType string

const (
	// ReferenceDependency lets a dependency binding name an OperationalTarget
	// in the grant's namespace.
	ReferenceDependency ReferenceType = "Dependency"
	// ReferenceEvidenceProvider lets an assertion binding name an evidence
	// provider identity in the grant's namespace.
	ReferenceEvidenceProvider ReferenceType = "EvidenceProvider"
)

// OperationalReferenceGrantSpec authorizes references from the listed
// namespaces into the grant's own namespace. Every From entry is combined
// with every To entry; all values are exact (no wildcard, no selector).
type OperationalReferenceGrantSpec struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	From []ReferenceGrantFrom `json:"from"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	To []ReferenceGrantTo `json:"to"`
}

// ReferenceGrantFrom names one referring namespace and kind.
type ReferenceGrantFrom struct {
	// +kubebuilder:validation:Enum=OperationalTarget
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace"`
}

// ReferenceGrantTo names one exact referent in the grant's namespace.
type ReferenceGrantTo struct {
	Type ReferenceType `json:"type"`
	// Name is the OperationalTarget name (Dependency) or provider name
	// (EvidenceProvider).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
}

// OperationalReferenceGrant is the explicit cross-namespace trust handshake
// of the referent's namespace owner. It is semantic permission only and does
// not replace RBAC.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type OperationalReferenceGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec OperationalReferenceGrantSpec `json:"spec"`
}

// OperationalReferenceGrantList contains a list of OperationalReferenceGrant.
//
// +kubebuilder:object:root=true
type OperationalReferenceGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OperationalReferenceGrant `json:"items"`
}
