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

// digest constants for test readability.
const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestReconcile_imageDigestDrift(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      "jumi-ah-dev-20261001-120000-digest001",
		Release:         "jumi-ah-dev",
		CreatedAt:       now,
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			// jumi deployed with old digest
			{Name: "jumi", Version: "v0.3.0", ImageDigest: digestA},
		},
	}
	writeRev(t, dir, rev)

	// desired release now has a newer digest for jumi (same version)
	rel := model.BoriRelease{
		Name: "jumi-ah-dev",
		Components: []model.ComponentRef{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0", ImageDigest: digestB},
		},
	}

	state, err := Reconcile(rel, dir)
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
	// Same version but different digest → drift
	if jumiDrift.SyncStatus != "out-of-sync" {
		t.Errorf("expected out-of-sync when imageDigest changed, got %q", jumiDrift.SyncStatus)
	}
	if jumiDrift.DesiredImageDigest != digestB {
		t.Errorf("expected DesiredImageDigest=%q, got %q", digestB, jumiDrift.DesiredImageDigest)
	}
	if jumiDrift.ActualImageDigest != digestA {
		t.Errorf("expected ActualImageDigest=%q, got %q", digestA, jumiDrift.ActualImageDigest)
	}
	cond := findCondition(state.Conditions, v1alpha1.ConditionDegraded)
	if cond == nil || cond.Status != v1alpha1.ConditionTrue {
		t.Errorf("expected Degraded=True when imageDigest changed, got %+v", cond)
	}
}

func TestReconcile_imageDigestMissingInActual(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      "jumi-ah-dev-20261001-120000-nodigest",
		Release:         "jumi-ah-dev",
		CreatedAt:       now,
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			// jumi deployed without digest (pre-digest-tracking state)
			{Name: "jumi", Version: "v0.3.0"},
		},
	}
	writeRev(t, dir, rev)

	// desired now requires a specific digest
	rel := model.BoriRelease{
		Name: "jumi-ah-dev",
		Components: []model.ComponentRef{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0", ImageDigest: digestA},
		},
	}

	state, err := Reconcile(rel, dir)
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
	// desired has digest, actual does not → drift (stronger identity now required)
	if jumiDrift.SyncStatus != "out-of-sync" {
		t.Errorf("expected out-of-sync when actual has no digest but desired does, got %q", jumiDrift.SyncStatus)
	}
}

func TestReconcile_imageDigestInSync(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "revisions"), 0o755)

	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      "jumi-ah-dev-20261001-120000-insync",
		Release:         "jumi-ah-dev",
		CreatedAt:       now,
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		Components: []revision.CompRevision{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0", ImageDigest: digestA,
				ImageRef: "harbor.lab.local/bori/jumi@" + digestA},
		},
	}
	writeRev(t, dir, rev)

	rel := model.BoriRelease{
		Name: "jumi-ah-dev",
		Components: []model.ComponentRef{
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "jumi", Version: "v0.3.0", ImageDigest: digestA},
		},
	}

	state, err := Reconcile(rel, dir)
	if err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	for _, d := range state.Drift {
		if d.SyncStatus != "in-sync" {
			t.Errorf("expected in-sync for %s, got %q", d.Component, d.SyncStatus)
		}
	}

	// ComponentStatus should expose the deployed image ref
	for _, cs := range state.Components {
		if cs.Name == "jumi" {
			if cs.DeployedImage != "harbor.lab.local/bori/jumi@"+digestA {
				t.Errorf("expected DeployedImage=%q, got %q",
					"harbor.lab.local/bori/jumi@"+digestA, cs.DeployedImage)
			}
			if cs.ImageDigest != digestA {
				t.Errorf("expected ImageDigest=%q, got %q", digestA, cs.ImageDigest)
			}
		}
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
