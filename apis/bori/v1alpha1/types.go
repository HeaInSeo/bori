// Package v1alpha1 defines the bori API types for the bori.dev group.
//
// These types are registered as Kubernetes CRDs starting from Phase 7.
// The CLI model (releases/, components/, environments/ YAML files) maps to
// the same data model via the type hierarchy below.
//
//	releases/<name>/release.yaml        → spec.release → BoriRelease
//	environments/<name>/environment.yaml → spec.environment → BoriEnvironment
//	.bori/revisions/<id>.json           → status.currentRevision
//	.bori/runs/<id>/                    → status.conditions (via shadow reconcile)
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Type aliases for Kubernetes-native condition types.
// Code that uses v1alpha1.Condition interoperates directly with metav1.Condition.
type Condition = metav1.Condition
type ConditionStatus = metav1.ConditionStatus

const (
	ConditionTrue    = metav1.ConditionTrue
	ConditionFalse   = metav1.ConditionFalse
	ConditionUnknown = metav1.ConditionUnknown
)

// Standard condition type names used across bori resources.
const (
	// ConditionInstalled: all components have been deployed at least once.
	ConditionInstalled = "Installed"
	// ConditionReady: all components are reporting healthy (health gate passed).
	ConditionReady = "Ready"
	// ConditionVerified: the latest deployment passed all verification gates.
	ConditionVerified = "Verified"
	// ConditionPromoted: the latest revision has been promoted.
	ConditionPromoted = "Promoted"
	// ConditionDegraded: one or more components failed verification or are out-of-sync.
	ConditionDegraded = "Degraded"
	// ConditionViolation: the release targets a namespace not in the environment's
	// allowed list. The operator sets Degraded=True alongside this condition and
	// does not attempt to deploy until the CR is corrected.
	ConditionViolation = "Violation"
)

// BoriDataPlaneSpec describes the desired state of a genomic dataplane app set.
// It references the release and environment definitions that live in the bori repo.
type BoriDataPlaneSpec struct {
	// Release is the name of the BoriRelease (releases/<name>/release.yaml).
	Release string `json:"release"`
	// Environment is the name of the BoriEnvironment (environments/<name>/environment.yaml).
	Environment string `json:"environment"`
}

// ComponentStatus is the sync status of one managed component within a BoriDataPlane.
type ComponentStatus struct {
	Name            string `json:"name"`
	DesiredVersion  string `json:"desiredVersion"`
	DeployedVersion string `json:"deployedVersion,omitempty"`
	// SyncStatus: in-sync | out-of-sync | unknown
	SyncStatus string      `json:"syncStatus"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// BoriDataPlaneStatus is the observed state of a BoriDataPlane.
// It is populated by the shadow reconciler and persisted to the Kubernetes API server.
type BoriDataPlaneStatus struct {
	// Conditions summarizes the overall state of the dataplane.
	// Standard types: Installed, Ready, Verified, Promoted, Degraded, Violation.
	Conditions []Condition `json:"conditions,omitempty"`
	// CurrentRevision is the revision ID of the last promoted deployment.
	CurrentRevision string `json:"currentRevision,omitempty"`
	// Components holds per-component sync status.
	Components []ComponentStatus `json:"components,omitempty"`
	// ObservedAt is when this status was last computed.
	ObservedAt metav1.Time `json:"observedAt,omitempty"`
	// ObservedGeneration is the metadata.generation that this status corresponds to.
	// The controller skips an expensive reconcile pass when this matches the
	// current generation and no Degraded or Violation condition is set.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// BoriDataPlane is the Kubernetes API resource for a managed dataplane app set.
//
// Each BoriDataPlane describes a release + environment combination. The bori
// operator reconciles it by running plan→deploy→verify→promote and updating
// status.conditions from the resulting shadow state.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bdp
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.spec.release`
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.currentRevision`
// +kubebuilder:printcolumn:name="ObservedGen",type=integer,JSONPath=`.status.observedGeneration`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BoriDataPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BoriDataPlaneSpec   `json:"spec,omitempty"`
	Status BoriDataPlaneStatus `json:"status,omitempty"`
}

// BoriDataPlaneList contains a list of BoriDataPlane objects.
//
// +kubebuilder:object:root=true
type BoriDataPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BoriDataPlane `json:"items"`
}
