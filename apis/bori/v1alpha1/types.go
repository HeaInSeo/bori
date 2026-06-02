// Package v1alpha1 defines the Go type representations of bori's future API types.
//
// These are NOT Kubernetes CRDs. CRD definitions are deferred to Phase 6/7.
// The types here describe the shape of what will become CRDs once the CLI model
// is proven stable. See docs/api-design.md for the full design rationale.
//
// Current bori YAML contracts map to these types as follows:
//
//	releases/<name>/release.yaml        → BoriRelease
//	components/<name>/component.yaml    → BoriComponent (via pkg/model)
//	environments/<name>/environment.yaml → BoriEnvironment (via pkg/model)
//	.bori/revisions/<id>.json           → BoriRevision (via pkg/revision)
//	.bori/runs/<id>/                    → BoriVerificationRun (via pkg/artifact)
package v1alpha1

import "time"

// ConditionStatus is the value of a status condition.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition represents a single status condition on a bori resource.
// Mirrors the Kubernetes meta/v1 Condition pattern for future compatibility.
type Condition struct {
	// Type is the condition identifier: Installed | Ready | Verified | Promoted | Degraded
	Type    string          `json:"type" yaml:"type"`
	Status  ConditionStatus `json:"status" yaml:"status"`
	Reason  string          `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message string          `json:"message,omitempty" yaml:"message,omitempty"`
	// LastTransitionTime is when Status last changed.
	LastTransitionTime time.Time `json:"lastTransitionTime" yaml:"lastTransitionTime"`
}

// BoriDataPlaneSpec describes the desired state of a genomic dataplane app set.
type BoriDataPlaneSpec struct {
	Release     string `json:"release" yaml:"release"`
	Environment string `json:"environment" yaml:"environment"`
}

// BoriDataPlaneStatus is the observed state of a BoriDataPlane.
type BoriDataPlaneStatus struct {
	// Conditions summarizes the state across all managed components.
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	// CurrentRevision is the revision ID of the last promoted deployment.
	CurrentRevision string `json:"currentRevision,omitempty" yaml:"currentRevision,omitempty"`
	// Components holds per-component status.
	Components []ComponentStatus `json:"components,omitempty" yaml:"components,omitempty"`
	// ObservedAt is when this status was last computed.
	ObservedAt time.Time `json:"observedAt" yaml:"observedAt"`
}

// ComponentStatus is the status of one managed component.
type ComponentStatus struct {
	Name            string `json:"name" yaml:"name"`
	DeployedVersion string `json:"deployedVersion,omitempty" yaml:"deployedVersion,omitempty"`
	DesiredVersion  string `json:"desiredVersion" yaml:"desiredVersion"`
	// SyncStatus: in-sync | out-of-sync | unknown
	SyncStatus string      `json:"syncStatus" yaml:"syncStatus"`
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// BoriDataPlane is the top-level type for a managed dataplane app set.
// Future CRD candidate. Not registered with Kubernetes in this phase.
type BoriDataPlane struct {
	Name   string              `json:"name" yaml:"name"`
	Spec   BoriDataPlaneSpec   `json:"spec" yaml:"spec"`
	Status BoriDataPlaneStatus `json:"status" yaml:"status"`
}

// Standard condition type names used across bori status.
const (
	// ConditionInstalled: all components have been deployed at least once.
	ConditionInstalled = "Installed"
	// ConditionReady: all components are reporting healthy (health gate passed).
	ConditionReady = "Ready"
	// ConditionVerified: the latest deployment passed all verification gates.
	ConditionVerified = "Verified"
	// ConditionPromoted: the latest revision has been promoted.
	ConditionPromoted = "Promoted"
	// ConditionDegraded: one or more components have failed verification or health.
	ConditionDegraded = "Degraded"
)
