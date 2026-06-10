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

	// Build desired version map from the release.
	desired := make(map[string]string, len(rel.Components))
	for _, ref := range rel.Components {
		desired[ref.Name] = ref.Version
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

	// Build actual version map from the promoted revision.
	actual := make(map[string]string)
	if latest != nil {
		state.ActualRevision = latest.RevisionID
		for _, cr := range latest.Components {
			actual[cr.Name] = cr.Version
		}
	}

	// Compute per-component drift and status.
	allInSync := true
	allInstalled := latest != nil
	for _, ref := range rel.Components {
		actualVer, ok := actual[ref.Name]
		syncStatus := "unknown"
		if ok {
			if actualVer == ref.Version {
				syncStatus = "in-sync"
			} else {
				syncStatus = "out-of-sync"
				allInSync = false
			}
		} else {
			allInSync = false
		}

		state.Drift = append(state.Drift, DriftItem{
			Component:      ref.Name,
			DesiredVersion: ref.Version,
			ActualVersion:  actualVer,
			SyncStatus:     syncStatus,
		})

		compStatus := v1alpha1.ComponentStatus{
			Name:            ref.Name,
			DesiredVersion:  ref.Version,
			DeployedVersion: actualVer,
			SyncStatus:      syncStatus,
		}
		state.Components = append(state.Components, compStatus)
	}

	// Compute conditions.
	state.Conditions = computeConditions(latest, allInstalled, allInSync, now)

	return state, nil
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
