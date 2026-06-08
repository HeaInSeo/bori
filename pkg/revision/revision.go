// Package revision manages immutable BoriRevision snapshots.
// Each successful deploy creates a revision that records exactly what was
// deployed, how it was verified, and whether it was promoted.
package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/model"
)

// BoriRevision is an immutable snapshot of one deployed release state.
type BoriRevision struct {
	SchemaVersion     string         `json:"schemaVersion"`
	RevisionID        string         `json:"revisionId"`
	Release           string         `json:"release"`
	Environment       string         `json:"environment"`
	ParentRevisionID  string         `json:"parentRevisionId,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
	Components        []CompRevision `json:"components"`
	ContentHash       string         `json:"contentHash"`
	VerificationRunID string         `json:"verificationRunId,omitempty"`
	PromotionStatus   string         `json:"promotionStatus"` // pending | promoted | rejected
	PromotedAt        *time.Time     `json:"promotedAt,omitempty"`
	// BaselineRef points to the sli-summary.json that becomes the next baseline
	// when this revision is promoted.
	BaselineRef string `json:"baselineRef,omitempty"`
	// FailReason is set when PromotionStatus is "failed" to record why deploy failed.
	FailReason string `json:"failReason,omitempty"`
	// NetworkVerification holds the outcome of NetworkIntegrationProfile checks run
	// after rollout. Nil when the environment profile type is "none" or not set.
	NetworkVerification *NetworkVerificationResult `json:"networkVerification,omitempty"`
}

// CompRevision captures the state of one component within a revision.
// All digests are SHA256 of the corresponding input file contents.
type CompRevision struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	ImageRef string `json:"imageRef"`
	// ImageDigest is the Harbor sha256 digest used when deploying via imageswap.
	ImageDigest string `json:"imageDigest,omitempty"`
	// GitSha is the source commit that produced the image.
	GitSha string `json:"gitSha,omitempty"`
	// ComponentSpecDigest is SHA256 of components/<name>/component.yaml
	ComponentSpecDigest string `json:"componentSpecDigest"`
	// EnvironmentDigest is SHA256 of environments/<name>/environment.yaml
	EnvironmentDigest string `json:"environmentDigest"`
	// VerificationPolicyDigest is SHA256 of the first verification policy file
	VerificationPolicyDigest string `json:"verificationPolicyDigest,omitempty"`
	// BaselineRef points to the baseline used for regression comparison
	BaselineRef string `json:"baselineRef,omitempty"`
}

// Network verification result values — use these constants to avoid string literals.
const (
	NetworkVerificationPass = "pass"
	NetworkVerificationFail = "fail"
	NetworkVerificationSkip = "skip"
)

// NetworkVerificationResult records the outcome of NetworkIntegrationProfile checks
// run after a successful rollout. Populated by the netverify layer (not yet implemented);
// nil when type is "none" or verification was skipped.
type NetworkVerificationResult struct {
	Type    string               `json:"type"`
	Overall string               `json:"overall"` // use NetworkVerification* constants
	Checks  []NetworkCheckResult `json:"checks,omitempty"`
}

// NetworkCheckResult is the result of one capability check within a NetworkIntegrationProfile.
type NetworkCheckResult struct {
	Check   string `json:"check"`
	Result  string `json:"result"` // use NetworkVerification* constants
	Message string `json:"message,omitempty"`
}

// NewRevisionID generates a deterministic revision ID from release, env, and timestamp.
func NewRevisionID(release, environment string, at time.Time) string {
	ts := at.UTC().Format("20060102-150405")
	h := sha256.Sum256([]byte(release + "|" + environment + "|" + ts))
	short := hex.EncodeToString(h[:])[:6]
	// Sanitize release name for use as a filename segment.
	safe := strings.NewReplacer("/", "-", " ", "-").Replace(release)
	return fmt.Sprintf("%s-%s-%s", safe, ts, short)
}

// ComputeContentHash produces a 16-char hex hash that uniquely identifies the
// set of inputs used for a revision. Same inputs always produce the same hash.
func ComputeContentHash(comps []CompRevision) string {
	sorted := make([]CompRevision, len(comps))
	copy(sorted, comps)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	h := sha256.New()
	for _, c := range sorted {
		fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s\n",
			c.Name, c.ImageRef, c.ImageDigest,
			c.ComponentSpecDigest,
			c.EnvironmentDigest,
			c.VerificationPolicyDigest,
			c.BaselineRef,
		)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// BuildFromPlan creates a pending BoriRevision from a deploy plan.
// Digests are computed from the bori repo files at build time.
func BuildFromPlan(plan artifact.Plan, boriRoot string) (BoriRevision, error) {
	now := time.Now().UTC()

	envDigest, _ := fileDigest(
		filepath.Join(boriRoot, "environments", plan.Environment, "environment.yaml"),
	)

	var comps []CompRevision
	for _, cp := range plan.Components {
		if cp.Action == "violation" {
			continue
		}
		cr := CompRevision{
			Name:              cp.Name,
			Version:           cp.Version,
			ImageRef:          cp.ImageRef,
			ImageDigest:       cp.ImageDigest,
			GitSha:            cp.GitSha,
			EnvironmentDigest: envDigest,
		}
		cr.ComponentSpecDigest, _ = fileDigest(
			filepath.Join(boriRoot, "components", cp.Name, "component.yaml"),
		)
		comps = append(comps, cr)
	}

	rev := BoriRevision{
		SchemaVersion:   "bori.revision.v1",
		Release:         plan.Release,
		Environment:     plan.Environment,
		CreatedAt:       now,
		Components:      comps,
		PromotionStatus: "pending",
	}
	rev.ContentHash = ComputeContentHash(comps)
	rev.RevisionID = NewRevisionID(plan.Release, plan.Environment, now)
	return rev, nil
}

// AddVerificationPolicyDigests fills VerificationPolicyDigest for each component
// by hashing the first verification policy file found in the bori registry.
func AddVerificationPolicyDigests(rev *BoriRevision, boriRoot, appsDir, profile string, comps map[string]model.BoriComponent) {
	for i, cr := range rev.Components {
		comp, ok := comps[cr.Name]
		if !ok || len(comp.VerificationPolicies) == 0 {
			continue
		}
		// Try bori registry policy file first, fall back to app-local.
		polPath := filepath.Join(boriRoot, "verification", "policies", comp.VerificationPolicies[0]+".yaml")
		if _, err := os.Stat(polPath); os.IsNotExist(err) {
			polPath = filepath.Join(appsDir, cr.Name, ".bori", fmt.Sprintf("policy.%s.yaml", profile))
		}
		rev.Components[i].VerificationPolicyDigest, _ = fileDigest(polPath)
	}
	// Recompute hash after adding policy digests.
	rev.ContentHash = ComputeContentHash(rev.Components)
}

// Fail marks the revision as failed and records the reason.
// A failed revision is immutable evidence — it is written to disk and never retried.
// Components are not modified so ContentHash is not recomputed.
func Fail(rev *BoriRevision, reason string) {
	rev.PromotionStatus = "failed"
	rev.FailReason = reason
}

// Promote marks the revision as promoted and records a baseline reference.
func Promote(rev *BoriRevision, baselineRef string) {
	now := time.Now().UTC()
	rev.PromotionStatus = "promoted"
	rev.PromotedAt = &now
	rev.BaselineRef = baselineRef
	rev.ContentHash = ComputeContentHash(rev.Components)
}

// Write serializes rev as JSON to <boriDir>/revisions/<rev.RevisionID>.json.
func Write(boriDir string, rev BoriRevision) (string, error) {
	dir := filepath.Join(boriDir, "revisions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, rev.RevisionID+".json")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(rev); encErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("encode: %w", encErr)
	}
	return path, f.Close()
}

// Read parses <boriDir>/revisions/<revisionID>.json.
func Read(boriDir, revisionID string) (BoriRevision, error) {
	path := filepath.Join(boriDir, "revisions", revisionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriRevision{}, fmt.Errorf("read %s: %w", path, err)
	}
	var rev BoriRevision
	if err := json.Unmarshal(data, &rev); err != nil {
		return BoriRevision{}, fmt.Errorf("parse: %w", err)
	}
	return rev, nil
}

// List returns all revisions in <boriDir>/revisions/, sorted newest first.
func List(boriDir string) ([]BoriRevision, error) {
	dir := filepath.Join(boriDir, "revisions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var revs []BoriRevision
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		revID := strings.TrimSuffix(e.Name(), ".json")
		rev, err := Read(boriDir, revID)
		if err != nil {
			continue // skip unreadable files
		}
		revs = append(revs, rev)
	}
	// Sort newest first.
	sort.Slice(revs, func(i, j int) bool {
		return revs[i].CreatedAt.After(revs[j].CreatedAt)
	})
	return revs, nil
}

// fileDigest returns the SHA256 hex digest of a file's contents.
// Returns an empty string if the file cannot be read (non-fatal).
func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
