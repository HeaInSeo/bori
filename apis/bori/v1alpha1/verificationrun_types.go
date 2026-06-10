package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/HeaInSeo/bori/pkg/artifact"
)

// BoriVerificationRunSpec records the outcome of a single verification gate execution.
//
// Scope (ADR-003): release/revision gate results only.
// Health state, network-baseline measurements, and runtime observations are NOT
// stored here. Each field maps directly to artifact.VerificationRun on disk.
type BoriVerificationRunSpec struct {
	// Provider is the verification backend (e.g. kube-slint).
	Provider string `json:"provider"`
	// App is the component name when this run covers a single component.
	// Empty when the run covers an entire release.
	App string `json:"app,omitempty"`
	// Release is the BoriRelease name.
	Release string `json:"release,omitempty"`
	// Environment is the target environment name.
	Environment string `json:"environment,omitempty"`
	// RevisionID is the name of the BoriRevision this run verified.
	// BoriVerificationRunReconciler uses this to link BoriRevision.status.verificationRunId.
	RevisionID string `json:"revisionId,omitempty"`
	// GateResult is the overall gate outcome: PASS|WARN|FAIL|NO_GRADE
	GateResult string `json:"gateResult"`
	// PromotionDecision is the promotion eligibility: eligible|blocked
	PromotionDecision string `json:"promotionDecision"`
	// StartedAt is when the verification run started.
	StartedAt metav1.Time `json:"startedAt"`
	// FinishedAt is when the verification run completed.
	FinishedAt metav1.Time `json:"finishedAt"`
	// MeasurementSummaryPath is the relative path to sli-summary.json in the run archive.
	MeasurementSummaryPath string `json:"measurementSummaryPath,omitempty"`
	// GateSummaryPath is the relative path to slint-gate-summary.json in the run archive.
	GateSummaryPath string `json:"gateSummaryPath,omitempty"`
}

// BoriVerificationRunStatus records when this CR was last observed by the controller.
type BoriVerificationRunStatus struct {
	// ObservedAt is when this status was last written.
	ObservedAt metav1.Time `json:"observedAt"`
}

// BoriVerificationRun is an immutable record of one verification gate execution.
//
// Design contract:
//   - CR name = runID (e.g. "20260610-150405").
//   - Created by `bori verify` CLI (KUBECONFIG lab fallback) or Phase 12 Ingestion API.
//   - Spec is immutable after creation; never mutated.
//   - Disk artifact (.bori/runs/<runID>/evidence/) is always preserved — CLI works
//     without K8s access.
//   - BoriVerificationRunReconciler links spec.revisionId to
//     BoriRevision.status.verificationRunId (first-write-wins).
//   - Scope: release/revision gate results only. See ADR-003.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bvr
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.spec.release`
// +kubebuilder:printcolumn:name="GateResult",type=string,JSONPath=`.spec.gateResult`
// +kubebuilder:printcolumn:name="Decision",type=string,JSONPath=`.spec.promotionDecision`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BoriVerificationRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BoriVerificationRunSpec   `json:"spec,omitempty"`
	Status BoriVerificationRunStatus `json:"status,omitempty"`
}

// BoriVerificationRunList contains a list of BoriVerificationRun objects.
//
// +kubebuilder:object:root=true
type BoriVerificationRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BoriVerificationRun `json:"items"`
}

// FromArtifact constructs a BoriVerificationRun from a disk artifact.VerificationRun.
// revisionID may be empty when the revision is not yet known at creation time;
// BoriVerificationRunReconciler will link it asynchronously.
func FromArtifact(vr artifact.VerificationRun, revisionID string) BoriVerificationRun {
	return BoriVerificationRun{
		Spec: BoriVerificationRunSpec{
			Provider:               vr.Provider,
			App:                    vr.App,
			Release:                vr.Release,
			Environment:            vr.Environment,
			RevisionID:             revisionID,
			GateResult:             vr.GateResult,
			PromotionDecision:      vr.PromotionDecision,
			StartedAt:              metav1.NewTime(vr.StartedAt),
			FinishedAt:             metav1.NewTime(vr.FinishedAt),
			MeasurementSummaryPath: vr.MeasurementSummaryPath,
			GateSummaryPath:        vr.GateSummaryPath,
		},
	}
}
