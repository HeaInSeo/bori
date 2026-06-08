package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/HeaInSeo/bori/pkg/model"
)

// BoriReleaseComponentRef pins a component to a specific version.
type BoriReleaseComponentRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// BoriReleaseCompatibilityRef points to the version compatibility matrix.
type BoriReleaseCompatibilityRef struct {
	Matrix string `json:"matrix,omitempty"`
}

// BoriReleaseVerification lists the verification policies for this release.
type BoriReleaseVerification struct {
	Policies []string `json:"policies,omitempty"`
}

// BoriReleaseBaselinePolicy controls how baselines are updated after promotion.
type BoriReleaseBaselinePolicy struct {
	UpdateFrom     string `json:"updateFrom,omitempty"`
	ReviewRequired bool   `json:"reviewRequired,omitempty"`
}

// BoriReleasePromotionPolicy defines the promotion gate requirements.
type BoriReleasePromotionPolicy struct {
	// RequiredGateResult is the minimum gate result needed for promotion (e.g. PASS).
	RequiredGateResult string                    `json:"requiredGateResult,omitempty"`
	BaselinePolicy     BoriReleaseBaselinePolicy `json:"baselinePolicy,omitempty"`
}

// BoriReleaseSpec is the desired state of a BoriRelease.
// It mirrors releases/<name>/release.yaml and is the authoritative source
// when the release is managed as a Kubernetes CR.
type BoriReleaseSpec struct {
	// Components is the pinned, compatible set of component versions.
	Components []BoriReleaseComponentRef `json:"components"`
	// Compatibility points to the version compatibility matrix.
	Compatibility BoriReleaseCompatibilityRef `json:"compatibility,omitempty"`
	// Verification lists the verification policies that apply to this release.
	Verification BoriReleaseVerification `json:"verification,omitempty"`
	// Promotion defines the promotion gate requirements.
	Promotion BoriReleasePromotionPolicy `json:"promotion,omitempty"`
}

// BoriReleaseStatus is the observed state of a BoriRelease.
type BoriReleaseStatus struct {
	// ObservedGeneration is the metadata.generation reconciled into this status.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ActiveDataPlanes is the number of BoriDataPlane CRs referencing this release.
	ActiveDataPlanes int32 `json:"activeDataPlanes,omitempty"`
	// ObservedAt is when this status was last updated.
	ObservedAt metav1.Time `json:"observedAt,omitempty"`
}

// BoriRelease is the Kubernetes API resource for a pinned release definition.
//
// A BoriRelease is the Kubernetes-native equivalent of releases/<name>/release.yaml.
// When a BoriRelease CR exists, the bori operator reads the release definition
// from the Kubernetes API instead of the filesystem. If no CR is found, the
// operator falls back to the filesystem (backward-compatible for CLI users).
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=br
// +kubebuilder:printcolumn:name="DataPlanes",type=integer,JSONPath=`.status.activeDataPlanes`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BoriRelease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BoriReleaseSpec   `json:"spec,omitempty"`
	Status BoriReleaseStatus `json:"status,omitempty"`
}

// BoriReleaseList contains a list of BoriRelease objects.
//
// +kubebuilder:object:root=true
type BoriReleaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BoriRelease `json:"items"`
}

// ToModel converts a BoriRelease CR into the internal pkg/model.BoriRelease
// used by the planner, reconciler, and CLI.
func (br *BoriRelease) ToModel() model.BoriRelease {
	rel := model.BoriRelease{
		Name:          br.Name,
		Compatibility: model.CompatibilityRef{Matrix: br.Spec.Compatibility.Matrix},
		Verification:  model.ReleaseVerification{Policies: append([]string(nil), br.Spec.Verification.Policies...)},
		Promotion: model.PromotionPolicy{
			RequiredGateResult: br.Spec.Promotion.RequiredGateResult,
			BaselinePolicy: model.BaselinePolicy{
				UpdateFrom:     br.Spec.Promotion.BaselinePolicy.UpdateFrom,
				ReviewRequired: br.Spec.Promotion.BaselinePolicy.ReviewRequired,
			},
		},
	}
	for _, c := range br.Spec.Components {
		rel.Components = append(rel.Components, model.ComponentRef{
			Name:    c.Name,
			Version: c.Version,
		})
	}
	return rel
}

// FromModelRelease converts a pkg/model.BoriRelease into a BoriRelease CR.
// Used by `bori release apply` to generate CR YAML from an existing release file.
func FromModelRelease(rel model.BoriRelease, namespace string) BoriRelease {
	br := BoriRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "BoriRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rel.Name,
			Namespace: namespace,
		},
		Spec: BoriReleaseSpec{
			Compatibility: BoriReleaseCompatibilityRef{Matrix: rel.Compatibility.Matrix},
			Verification:  BoriReleaseVerification{Policies: append([]string(nil), rel.Verification.Policies...)},
			Promotion: BoriReleasePromotionPolicy{
				RequiredGateResult: rel.Promotion.RequiredGateResult,
				BaselinePolicy: BoriReleaseBaselinePolicy{
					UpdateFrom:     rel.Promotion.BaselinePolicy.UpdateFrom,
					ReviewRequired: rel.Promotion.BaselinePolicy.ReviewRequired,
				},
			},
		},
	}
	for _, c := range rel.Components {
		br.Spec.Components = append(br.Spec.Components, BoriReleaseComponentRef{
			Name:    c.Name,
			Version: c.Version,
		})
	}
	return br
}
