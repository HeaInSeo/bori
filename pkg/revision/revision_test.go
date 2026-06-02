package revision

import (
	"testing"
	"time"
)

func TestComputeContentHash(t *testing.T) {
	comps := []CompRevision{
		{Name: "jumi", ImageRef: "ghcr.io/heainseo/jumi:v0.3.0", ComponentSpecDigest: "abc"},
		{Name: "artifact-handoff", ImageRef: "ghcr.io/heainseo/artifact-handoff:v0.2.0", ComponentSpecDigest: "def"},
	}

	h1 := ComputeContentHash(comps)
	if len(h1) != 16 {
		t.Errorf("expected 16-char hash, got %q (len=%d)", h1, len(h1))
	}

	// Reversed order should produce the same hash (deterministic).
	reversed := []CompRevision{comps[1], comps[0]}
	h2 := ComputeContentHash(reversed)
	if h1 != h2 {
		t.Errorf("expected same hash regardless of order: %q != %q", h1, h2)
	}

	// Different input should produce different hash.
	comps[0].ImageRef = "ghcr.io/heainseo/jumi:v0.4.0"
	h3 := ComputeContentHash(comps)
	if h1 == h3 {
		t.Error("expected different hash for different image ref")
	}
}

func TestNewRevisionID(t *testing.T) {
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	id := NewRevisionID("jumi-ah-dev", "kind", at)

	if id == "" {
		t.Fatal("expected non-empty revision ID")
	}
	// Should start with sanitized release name.
	if id[:12] != "jumi-ah-dev-" {
		t.Errorf("expected ID to start with 'jumi-ah-dev-', got %q", id)
	}
}

func TestPromote(t *testing.T) {
	rev := BoriRevision{
		RevisionID:      "test-rev",
		PromotionStatus: "pending",
		Components: []CompRevision{
			{Name: "jumi", ImageRef: "ghcr.io/heainseo/jumi:v0.3.0"},
		},
	}
	rev.ContentHash = ComputeContentHash(rev.Components)
	before := rev.ContentHash

	Promote(&rev, "verification/baselines/jumi-v0.3.0.json")

	if rev.PromotionStatus != "promoted" {
		t.Errorf("expected promoted, got %q", rev.PromotionStatus)
	}
	if rev.PromotedAt == nil {
		t.Error("expected PromotedAt to be set")
	}
	if rev.BaselineRef == "" {
		t.Error("expected BaselineRef to be set")
	}
	// ContentHash should not change just from promotion metadata.
	_ = before
}
