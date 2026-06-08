package revision

import (
	"encoding/json"
	"strings"
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

func TestComputeContentHashWithImageDigest(t *testing.T) {
	withTag := []CompRevision{
		{Name: "jumi", ImageRef: "harbor.local/jumi:v0.4.0"},
	}
	withDigest := []CompRevision{
		{Name: "jumi", ImageRef: "harbor.local/jumi@sha256:abc123", ImageDigest: "sha256:abc123"},
	}

	h1 := ComputeContentHash(withTag)
	h2 := ComputeContentHash(withDigest)
	if h1 == h2 {
		t.Error("expected different hash when ImageDigest changes")
	}

	// Same digest twice must be stable.
	h3 := ComputeContentHash(withDigest)
	if h2 != h3 {
		t.Errorf("hash must be stable: %q != %q", h2, h3)
	}
}

func TestFail(t *testing.T) {
	rev := BoriRevision{
		RevisionID:      "test-rev",
		PromotionStatus: "pending",
		Components: []CompRevision{
			{Name: "jumi", ImageRef: "harbor.local/jumi@sha256:abc123", ImageDigest: "sha256:abc123"},
		},
	}
	rev.ContentHash = ComputeContentHash(rev.Components)

	Fail(&rev, "kubectl set image: exit status 1")

	if rev.PromotionStatus != "failed" {
		t.Errorf("expected PromotionStatus 'failed', got %q", rev.PromotionStatus)
	}
	if rev.FailReason == "" {
		t.Error("expected FailReason to be set")
	}
	// ContentHash must still be valid after Fail.
	if len(rev.ContentHash) != 16 {
		t.Errorf("expected 16-char ContentHash after Fail, got %q", rev.ContentHash)
	}
	// PromotedAt must not be set.
	if rev.PromotedAt != nil {
		t.Error("expected PromotedAt to be nil after Fail")
	}
}

func TestNetworkVerificationNilOmittedFromJSON(t *testing.T) {
	rev := BoriRevision{
		RevisionID:      "test-rev",
		PromotionStatus: "pending",
		Components: []CompRevision{
			{Name: "jumi", ImageRef: "harbor.local/jumi:v0.4.0"},
		},
	}
	data, err := json.Marshal(rev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "networkVerification") {
		t.Error("expected networkVerification to be omitted when nil")
	}
}

func TestNetworkVerificationConstants(t *testing.T) {
	if NetworkVerificationPass != "pass" {
		t.Errorf("unexpected value: %q", NetworkVerificationPass)
	}
	if NetworkVerificationFail != "fail" {
		t.Errorf("unexpected value: %q", NetworkVerificationFail)
	}
	if NetworkVerificationSkip != "skip" {
		t.Errorf("unexpected value: %q", NetworkVerificationSkip)
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
	// ContentHash must not change: Promote only sets BaselineRef on BoriRevision,
	// not on CompRevision, so ComputeContentHash input is unchanged.
	if rev.ContentHash != before {
		t.Errorf("ContentHash must not change after Promote: %q → %q", before, rev.ContentHash)
	}
}
