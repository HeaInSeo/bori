package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/adapter"
	"github.com/HeaInSeo/bori/pkg/revision"
)

// mockAdapter is a test double for adapter.DeployAdapter.
type mockAdapter struct {
	called  bool
	success bool
	message string
}

func (m *mockAdapter) Name() string { return "mock" }
func (m *mockAdapter) Deploy(_ context.Context, _ adapter.DeployRequest) (*adapter.DeployResult, error) {
	m.called = true
	return &adapter.DeployResult{Success: m.success, Message: m.message}, nil
}

// setupFixtures creates a minimal bori repo fixture under root.
// Component adapter is set to "mock" so tests can inject a mockAdapter.
func setupFixtures(t *testing.T, root string) {
	t.Helper()
	dirs := []string{
		filepath.Join(root, "releases", "test-release"),
		filepath.Join(root, "components", "comp-a"),
		filepath.Join(root, "environments", "dev"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeFixture(t, filepath.Join(root, "releases", "test-release", "release.yaml"), `
name: test-release
components:
  - name: comp-a
    version: v1.0.0
`)
	writeFixture(t, filepath.Join(root, "components", "comp-a", "component.yaml"), `
name: comp-a
version: v1.0.0
deploy:
  adapter: mock
`)
	writeFixture(t, filepath.Join(root, "environments", "dev", "environment.yaml"), `
name: dev
namespacePolicy:
  allowed:
    - comp-a-system
`)
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writePromotedRevision(t *testing.T, boriDir, release, compVersion string) {
	t.Helper()
	now := time.Now().UTC()
	promotedAt := now
	rev := revision.BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		RevisionID:      release + "-20260601-120000-abc001",
		Release:         release,
		CreatedAt:       now,
		PromotionStatus: "promoted",
		PromotedAt:      &promotedAt,
		Components: []revision.CompRevision{
			{Name: "comp-a", Version: compVersion},
		},
	}
	if _, err := revision.Write(boriDir, rev); err != nil {
		t.Fatalf("write revision: %v", err)
	}
}

func newTestReconciler(mock *mockAdapter) *Reconciler {
	r := NewReconciler("", nil)
	r.AdapterRegistry["mock"] = mock
	return r
}

func TestReconciler_dryRun(t *testing.T) {
	root := t.TempDir()
	boriDir := filepath.Join(root, ".bori")
	setupFixtures(t, root)

	mock := &mockAdapter{success: true}
	r := newTestReconciler(mock)

	res, err := r.Run(context.Background(), Request{
		BoriRoot:    root,
		AppsDir:     root,
		BoriDir:     boriDir,
		ReleaseName: "test-release",
		EnvName:     "dev",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DeployStatus != "skipped (dry-run)" {
		t.Errorf("deploy status: want %q, got %q", "skipped (dry-run)", res.DeployStatus)
	}
	if mock.called {
		t.Error("adapter must not be called in dry-run mode")
	}
	if res.Promoted {
		t.Error("revision must not be promoted in dry-run mode")
	}
}

func TestReconciler_skipIfInSync(t *testing.T) {
	root := t.TempDir()
	boriDir := filepath.Join(root, ".bori")
	setupFixtures(t, root)

	// Promoted revision matches desired version → in-sync.
	writePromotedRevision(t, boriDir, "test-release", "v1.0.0")

	mock := &mockAdapter{success: true}
	r := newTestReconciler(mock)

	res, err := r.Run(context.Background(), Request{
		BoriRoot:     root,
		AppsDir:      root,
		BoriDir:      boriDir,
		ReleaseName:  "test-release",
		EnvName:      "dev",
		SkipIfInSync: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DeployStatus != "skipped" {
		t.Errorf("deploy status: want %q, got %q", "skipped", res.DeployStatus)
	}
	if mock.called {
		t.Error("adapter must not be called when all components are in-sync")
	}
	if res.DriftDetected {
		t.Error("no drift expected when promoted revision matches desired versions")
	}
}

func TestReconciler_deploysOnDrift(t *testing.T) {
	root := t.TempDir()
	boriDir := filepath.Join(root, ".bori")
	setupFixtures(t, root)

	// Promoted revision has old version → out-of-sync.
	writePromotedRevision(t, boriDir, "test-release", "v0.9.0")

	mock := &mockAdapter{success: true, message: "deployed"}
	r := newTestReconciler(mock)

	res, err := r.Run(context.Background(), Request{
		BoriRoot:     root,
		AppsDir:      root,
		BoriDir:      boriDir,
		ReleaseName:  "test-release",
		EnvName:      "dev",
		SkipIfInSync: true, // skip if in-sync, but drift should still trigger deploy
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.DriftDetected {
		t.Error("drift expected when promoted version differs from desired")
	}
	if !mock.called {
		t.Error("adapter must be called when drift is detected")
	}
	if res.DeployStatus != "success" {
		t.Errorf("deploy status: want %q, got %q", "success", res.DeployStatus)
	}
	if !res.Promoted {
		t.Error("revision must be promoted after successful deploy")
	}
}

func TestReconciler_deployFails(t *testing.T) {
	root := t.TempDir()
	boriDir := filepath.Join(root, ".bori")
	setupFixtures(t, root)

	mock := &mockAdapter{success: false, message: "adapter error"}
	r := newTestReconciler(mock)

	res, err := r.Run(context.Background(), Request{
		BoriRoot:    root,
		AppsDir:     root,
		BoriDir:     boriDir,
		ReleaseName: "test-release",
		EnvName:     "dev",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DeployStatus != "failed" {
		t.Errorf("deploy status: want %q, got %q", "failed", res.DeployStatus)
	}
	if res.Promoted {
		t.Error("revision must not be promoted after failed deploy")
	}
}
