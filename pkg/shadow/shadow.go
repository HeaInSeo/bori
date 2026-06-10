// Package shadow implements bori's operator shadow mode.
//
// Shadow mode reads the release definition (desired state) and the most recently
// promoted revision (actual state), computes drift, and produces status conditions —
// without applying any changes to the cluster.
//
// This is the prototype for what a future bori operator reconciler would do.
// CRD registration and Kubernetes status condition writes are active from Phase 7.
package shadow

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/model"
	"github.com/HeaInSeo/bori/pkg/revision"
)

// DriftItem describes the difference between desired and actual for one component.
type DriftItem struct {
	Component      string `json:"component"`
	DesiredVersion string `json:"desiredVersion"`
	ActualVersion  string `json:"actualVersion,omitempty"`
	// DesiredImageDigest is the sha256 digest from BoriRelease (empty when not set).
	DesiredImageDigest string `json:"desiredImageDigest,omitempty"`
	// ActualImageDigest is the sha256 digest recorded in the promoted revision.
	ActualImageDigest string `json:"actualImageDigest,omitempty"`
	// SyncStatus: in-sync | out-of-sync | unknown
	SyncStatus string `json:"syncStatus"`
}

// ShadowState is the full computed state from one shadow reconciliation pass.
type ShadowState struct {
	SchemaVersion   string                     `json:"schemaVersion"`
	Release         string                     `json:"release"`
	Environment     string                     `json:"environment"`
	ComputedAt      time.Time                  `json:"computedAt"`
	DesiredRevision string                     `json:"desiredRevision,omitempty"`
	ActualRevision  string                     `json:"actualRevision,omitempty"`
	Drift           []DriftItem                `json:"drift"`
	Conditions      []v1alpha1.Condition       `json:"conditions"`
	Components      []v1alpha1.ComponentStatus `json:"components"`
}

// Reconcile computes the shadow state for a release in an environment.
//
// It loads:
//   - the BoriRelease (desired state)
//   - the most recently promoted BoriRevision (actual state)
//
// and produces status conditions and a drift report.
func Reconcile(rel model.BoriRelease, boriDir string) (*ShadowState, error) {
	now := time.Now().UTC()
	state := &ShadowState{
		SchemaVersion: "bori.shadowState.v1",
		Release:       rel.Name,
		ComputedAt:    now,
	}

	// Find the most recently promoted revision.
	revs, err := revision.List(boriDir)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}

	var latest *revision.BoriRevision
	for i := range revs {
		r := &revs[i]
		if r.Release != rel.Name {
			continue
		}
		if r.PromotionStatus == "promoted" {
			latest = r
			break // List returns newest-first
		}
	}

	// Build actual component map from the promoted revision.
	actualRevs := make(map[string]revision.CompRevision)
	if latest != nil {
		state.ActualRevision = latest.RevisionID
		for _, cr := range latest.Components {
			actualRevs[cr.Name] = cr
		}
	}

	// Compute per-component drift and status.
	// Drift rule:
	//   - If desired.imageDigest is set → compare imageDigest (strongest identity).
	//     current.imageDigest missing or different → drift.
	//   - Otherwise → fallback to version comparison.
	//   - gitSha is recorded for provenance only, not used as a drift criterion.
	allInSync := true
	allInstalled := latest != nil
	for _, ref := range rel.Components {
		actualRev, ok := actualRevs[ref.Name]
		syncStatus := "unknown"
		if ok {
			if componentIdentityInSync(ref, actualRev) {
				syncStatus = "in-sync"
			} else {
				syncStatus = "out-of-sync"
				allInSync = false
			}
		} else {
			allInSync = false
		}

		state.Drift = append(state.Drift, DriftItem{
			Component:          ref.Name,
			DesiredVersion:     ref.Version,
			ActualVersion:      actualRev.Version,
			DesiredImageDigest: ref.ImageDigest,
			ActualImageDigest:  actualRev.ImageDigest,
			SyncStatus:         syncStatus,
		})

		deployedImage := ""
		if ok {
			deployedImage = actualRev.ImageRef
		}
		compStatus := v1alpha1.ComponentStatus{
			Name:            ref.Name,
			DesiredVersion:  ref.Version,
			DeployedVersion: actualRev.Version,
			SyncStatus:      syncStatus,
			ImageDigest:     ref.ImageDigest,
			DeployedImage:   deployedImage,
		}
		state.Components = append(state.Components, compStatus)
	}

	// Compute conditions.
	state.Conditions = computeConditions(latest, allInstalled, allInSync, now)

	return state, nil
}

// componentIdentityInSync returns true when the actual deployed component matches
// the desired release identity.
//
// If desired.ImageDigest is set, it is the primary criterion: the deployed
// component must have the same digest. If actual has no digest recorded, the
// deployed state predates digest tracking and is treated as drift.
//
// When desired.ImageDigest is empty, version is used as the fallback criterion.
func componentIdentityInSync(desired model.ComponentRef, actual revision.CompRevision) bool {
	if desired.ImageDigest != "" {
		return actual.ImageDigest == desired.ImageDigest
	}
	return actual.Version == desired.Version
}

// computeConditions derives the standard status conditions from shadow reconcile data.
func computeConditions(
	latest *revision.BoriRevision,
	allInstalled, allInSync bool,
	now time.Time,
) []v1alpha1.Condition {
	cond := func(t string, s v1alpha1.ConditionStatus, reason, msg string) v1alpha1.Condition {
		return v1alpha1.Condition{
			Type:               t,
			Status:             s,
			Reason:             reason,
			Message:            msg,
			LastTransitionTime: metav1.NewTime(now),
		}
	}

	var conditions []v1alpha1.Condition

	// Installed: at least one promoted revision exists.
	if allInstalled {
		conditions = append(conditions, cond(
			v1alpha1.ConditionInstalled,
			v1alpha1.ConditionTrue,
			"RevisionFound",
			fmt.Sprintf("promoted revision %s found", latest.RevisionID),
		))
	} else {
		conditions = append(conditions, cond(
			v1alpha1.ConditionInstalled,
			v1alpha1.ConditionFalse,
			"NoPromotedRevision",
			"no promoted revision found — run `bori deploy` first",
		))
	}

	// Verified: an explicit verification gate (kube-slint, SLI smoke, etc.)
	// ran and passed for the latest promoted revision.
	// Deploy success alone does NOT set VerificationRunID, so Verified stays
	// Unknown until a dedicated gate explicitly records its run ID.
	if latest != nil && latest.VerificationRunID != "" {
		conditions = append(conditions, cond(
			v1alpha1.ConditionVerified,
			v1alpha1.ConditionTrue,
			"VerificationRunFound",
			fmt.Sprintf("verification run %s passed", latest.VerificationRunID),
		))
	} else {
		conditions = append(conditions, cond(
			v1alpha1.ConditionVerified,
			v1alpha1.ConditionUnknown,
			"NotConfigured",
			"no verification gate has run; deploy success alone does not set Verified=True",
		))
	}

	// Promoted: most recent revision is promoted.
	if latest != nil && latest.PromotionStatus == "promoted" {
		conditions = append(conditions, cond(
			v1alpha1.ConditionPromoted,
			v1alpha1.ConditionTrue,
			"Promoted",
			fmt.Sprintf("revision %s promoted at %s", latest.RevisionID,
				func() string {
					if latest.PromotedAt != nil {
						return latest.PromotedAt.Format(time.RFC3339)
					}
					return "unknown"
				}()),
		))
	} else {
		conditions = append(conditions, cond(
			v1alpha1.ConditionPromoted,
			v1alpha1.ConditionFalse,
			"NotPromoted",
			"no promoted revision",
		))
	}

	// Degraded: any component is out-of-sync.
	if !allInSync {
		conditions = append(conditions, cond(
			v1alpha1.ConditionDegraded,
			v1alpha1.ConditionTrue,
			"DriftDetected",
			"one or more components are out-of-sync with the release definition",
		))
	} else if allInstalled {
		conditions = append(conditions, cond(
			v1alpha1.ConditionDegraded,
			v1alpha1.ConditionFalse,
			"InSync",
			"all components match the release definition",
		))
	}

	return conditions
}
