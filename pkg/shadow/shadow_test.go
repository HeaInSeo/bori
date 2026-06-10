package shadow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/model"
	"github.com/HeaInSeo/bori/pkg/revision"
)

func makeRel() model.BoriRelease {
	return model.BoriRelease{
		Name: "jumi-ah-dev",
		Components: []model.ComponentRef{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0"},
		},
	}
}

func writeRev(t *testing.T, boriDir string, rev revision.BoriRevision) {
	t.Helper()
	if _, err := revision.Write(boriDir, rev); err != nil {
		t.Fatalf("write revision: %v", err)
	}
}

func TestReconcile_noRevision(t *testing.T) {
	dir := t.TempDir()
	state, err := Reconcile(makeRel(), dir)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	if state.ActualRevision != "" {
		t.Errorf("expected no actual revision, got %q", state.ActualRevision)
	}
	cond := findCondition(state.Conditions, v1alpha1.ConditionInstalled)
	if cond == nil || cond.Status != v1alpha1.ConditionFalse {
		t.Errorf("expected Installed=False, got %+v", cond)
	}
	for _, d := range state.Drift {
		if d.SyncStatus != "unknown" {
			t.Errorf("expected unknown sync status for %s, got %q", d.Component, d.SyncStatus)
		}
	}
}

func TestReconcile_inSync(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:     "bori.revision.v1",
		RevisionID:        "jumi-ah-dev-20261001-120000-abc123",
		Release:           "jumi-ah-dev",
		CreatedAt:         now,
		PromotionStatus:   "promoted",
		PromotedAt:        &promotedAt,
		VerificationRunID: "run-123",
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0"},
		},
	}
	writeRev(t, dir, rev)

	state, err := Reconcile(makeRel(), dir)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	if state.ActualRevision != rev.RevisionID {
		t.Errorf("expected actual revision %q, got %q", rev.RevisionID, state.ActualRevision)
	}
	for _, d := range state.Drift {
		if d.SyncStatus != "in-sync" {
			t.Errorf("expected in-sync for %s, got %q", d.Component, d.SyncStatus)
		}
	}
	cond := findCondition(state.Conditions, v1alpha1.ConditionDegraded)
	if cond == nil || cond.Status != v1alpha1.ConditionFalse {
		t.Errorf("expected Degraded=False, got %+v", cond)
	}
	cond = findCondition(state.Conditions, v1alpha1.ConditionPromoted)
	if cond == nil || cond.Status != v1alpha1.ConditionTrue {
		t.Errorf("expected Promoted=True, got %+v", cond)
	}
	// VerificationRunID is "run-123" → Verified=True.
	cond = findCondition(state.Conditions, v1alpha1.ConditionVerified)
	if cond == nil || cond.Status != v1alpha1.ConditionTrue {
		t.Errorf("expected Verified=True when VerificationRunID set, got %+v", cond)
	}
}

func TestReconcile_promotedWithoutVerification(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      "jumi-ah-dev-20261001-120000-noverify",
		Release:         "jumi-ah-dev",
		CreatedAt:       now,
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		// VerificationRunID intentionally empty — deploy-only, no verification gate ran.
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0"},
		},
	}
	writeRev(t, dir, rev)

	state, err := Reconcile(makeRel(), dir)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	cond := findCondition(state.Conditions, v1alpha1.ConditionVerified)
	if cond == nil {
		t.Fatal("expected Verified condition to be present")
	}
	if cond.Status != v1alpha1.ConditionUnknown {
		t.Errorf("expected Verified=Unknown when no verification gate ran, got Status=%s", cond.Status)
	}
	if cond.Reason != "NotConfigured" {
		t.Errorf("expected Reason=NotConfigured, got %q", cond.Reason)
	}
}

func TestReconcile_outOfSync(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      "jumi-ah-dev-20260901-120000-old001",
		Release:         "jumi-ah-dev",
		CreatedAt:       now.Add(-24 * 60 * 60 * 1e9),
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.2.0"}, // old version
		},
	}
	writeRev(t, dir, rev)

	// Release desires jumi v0.3.0
	state, err := Reconcile(makeRel(), dir)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	var jumiDrift *DriftItem
	for i := range state.Drift {
		if state.Drift[i].Component == "jumi" {
			jumiDrift = &state.Drift[i]
		}
	}
	if jumiDrift == nil {
		t.Fatal("expected drift item for jumi")
	}
	if jumiDrift.SyncStatus != "out-of-sync" {
		t.Errorf("expected out-of-sync for jumi, got %q", jumiDrift.SyncStatus)
	}
	cond := findCondition(state.Conditions, v1alpha1.ConditionDegraded)
	if cond == nil || cond.Status != v1alpha1.ConditionTrue {
		t.Errorf("expected Degraded=True, got %+v", cond)
	}
}

func findCondition(conditions []v1alpha1.Condition, condType string) *v1alpha1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
