package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RevisionComponentRef captures the state of one component within a BoriRevision.
type RevisionComponentRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// ImageRef is the full image reference including digest.
	ImageRef string `json:"imageRef,omitempty"`
	// ImageDigest is the Harbor sha256 digest used when deploying via imageswap.
	ImageDigest string `json:"imageDigest,omitempty"`
	// GitSha is the source commit that produced the image.
	GitSha string `json:"gitSha,omitempty"`
	// ComponentSpecDigest is the SHA256 of components/<name>/component.yaml.
	ComponentSpecDigest string `json:"componentSpecDigest,omitempty"`
	// EnvironmentDigest is the SHA256 of environments/<name>/environment.yaml.
	EnvironmentDigest string `json:"environmentDigest,omitempty"`
	// VerificationPolicyDigest is the SHA256 of the first verification policy file.
	VerificationPolicyDigest string `json:"verificationPolicyDigest,omitempty"`
}

// BoriRevisionSpec is the immutable record of what was deployed in one revision.
type BoriRevisionSpec struct {
	// Release is the BoriRelease name this revision was built from.
	Release string `json:"release"`
	// Environment is the target environment name.
	Environment string `json:"environment"`
	// ContentHash is the SHA256 of all component inputs (image, spec, policy, baseline).
	ContentHash string `json:"contentHash"`
	// Components is the exact set of components deployed in this revision.
	Components []RevisionComponentRef `json:"components"`
	// ParentRevisionID is the previous promoted revision, if any.
	ParentRevisionID string `json:"parentRevisionId,omitempty"`
	// BaselineRef points to the sli-summary.json used as regression baseline.
	BaselineRef string `json:"baselineRef,omitempty"`
}

// BoriRevisionStatus is the observed promotion state of a BoriRevision.
type BoriRevisionStatus struct {
	// PromotionStatus: pending | promoted | rejected
	PromotionStatus string `json:"promotionStatus"`
	// PromotedAt is when this revision was promoted.
	PromotedAt *metav1.Time `json:"promotedAt,omitempty"`
	// VerificationRunID is the run that verified and promoted this revision.
	VerificationRunID string `json:"verificationRunId,omitempty"`
	// ObservedAt is when this status was last written.
	ObservedAt metav1.Time `json:"observedAt"`
}

// BoriRevision is a write-once deployment history resource.
//
// Design contract:
//   - Created once per promoted revision by DataPlaneReconciler.upsertBoriRevision().
//   - Never mutated after the initial write (spec is immutable; status is written once
//     at creation and updated only if the operator restarts and re-promotes).
//   - NOT owned by BoriDataPlane. ownerReference is intentionally absent so that
//     BoriRevision CRs survive BoriDataPlane deletion — they are audit records, not
//     derived state. See docs/adr/ADR-001-borirevision-failreason.md.
//   - Disk artifacts (.bori/revisions/*.json) remain the source of truth for the CLI.
//     The K8s CR is a read-only projection that makes history queryable via kubectl.
//
// Pending decisions (see ADR-001):
//   - failReason field: spec (immutable historical fact) vs status (conventional CRD).
//   - networkVerification: held until PR-2 netverify is implemented.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=brev
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.spec.release`
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.promotionStatus`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BoriRevision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BoriRevisionSpec   `json:"spec,omitempty"`
	Status BoriRevisionStatus `json:"status,omitempty"`
}

// BoriRevisionList contains a list of BoriRevision objects.
//
// +kubebuilder:object:root=true
type BoriRevisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BoriRevision `json:"items"`
}
