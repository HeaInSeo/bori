package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HeaInSeo/kube-slint/pkg/slint"
	"github.com/HeaInSeo/kube-slint/pkg/slo/engine"
	"github.com/HeaInSeo/kube-slint/pkg/slo/summary"
)

// measurementSummaryFile is the static summary alias kube-slint writes into
// the session's ArtifactsDir.
const measurementSummaryFile = "sli-summary.json"

// measurementSession is the part of *slint.Session bori drives.
type measurementSession interface {
	Start()
	End(ctx context.Context) (*summary.Summary, error)
}

// newMeasurementSession builds the kube-slint producer session. Tests replace
// it with a double.
var newMeasurementSession = func(cfg slint.SessionConfig) measurementSession {
	return slint.NewSession(cfg)
}

// measureRequest carries the per-run inputs bori supplies to the producer.
// SubjectID and WindowID are caller-authoritative TrustContract coordinates;
// bori never derives them from the target, run ID or timestamps.
type measureRequest struct {
	Profile     targetProfile
	RunID       string
	SubjectID   string
	WindowID    string
	EvidenceDir string

	ServiceAccount string
	Token          string
	CurlImage      string
}

// errNoTrustContract reports missing caller-authoritative coordinates.
var errNoTrustContract = errors.New("subject-id and window-id are required for a trust-correct measurement")

// sessionConfig maps a request to the producer configuration.
func (r measureRequest) sessionConfig() slint.SessionConfig {
	return slint.SessionConfig{
		Namespace:          r.Profile.Namespace,
		MetricsServiceName: r.Profile.MetricsService,
		TestCase:           r.Profile.Target,
		Suite:              "bori-verify",
		RunID:              r.RunID,
		ServiceAccountName: r.ServiceAccount,
		Token:              r.Token,
		ArtifactsDir:       r.EvidenceDir,
		ServiceURLFormat:   r.Profile.ServiceURLFormat,
		CurlImage:          r.CurlImage,
		Specs:              r.Profile.Specs(),
		TrustContract: &engine.TrustContract{
			SubjectID:      r.SubjectID,
			SourceConfigID: r.Profile.SourceConfigID,
			WindowID:       r.WindowID,
		},
	}
}

// measureAround runs the kube-slint producer around smoke: Start before smoke
// and End after it. It returns the path of the validated slo.v4 summary. Any
// producer error, smoke error, or summary that does not carry every profile
// intent with a value and comparability identity is an error; there is no
// soft success.
func measureAround(ctx context.Context, req measureRequest, smoke func(context.Context) error) (string, error) {
	if strings.TrimSpace(req.SubjectID) == "" || strings.TrimSpace(req.WindowID) == "" {
		return "", errNoTrustContract
	}
	if err := os.MkdirAll(req.EvidenceDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", req.EvidenceDir, err)
	}
	summaryPath := filepath.Join(req.EvidenceDir, measurementSummaryFile)
	// A summary left by an earlier run in the same directory must never be
	// read as this run's evidence.
	if err := os.Remove(summaryPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove stale summary %s: %w", summaryPath, err)
	}

	sess := newMeasurementSession(req.sessionConfig())
	sess.Start()
	if err := smoke(ctx); err != nil {
		return "", fmt.Errorf("smoke: %w", err)
	}
	if _, err := sess.End(ctx); err != nil {
		return "", fmt.Errorf("kube-slint producer: %w", err)
	}
	// Validate the file the gate will read, not the in-memory result.
	written, err := summary.LoadFile(summaryPath)
	if err != nil {
		return "", fmt.Errorf("kube-slint producer wrote no summary at %s: %w", summaryPath, err)
	}
	if err := validateSummary(&written, req); err != nil {
		return "", err
	}
	return summaryPath, nil
}

// validateSummary fails closed unless sum is a trust-correct slo.v4 summary
// that carries exactly the profile's intents, each with a value, a non-skip
// status and a complete comparability identity stamped with this request's
// subject and source configuration.
func validateSummary(sum *summary.Summary, req measureRequest) error {
	if sum == nil {
		return errors.New("kube-slint producer returned no summary")
	}
	if sum.SchemaVersion != summary.SchemaVersionTrust {
		return fmt.Errorf("summary schema %q, want %q", sum.SchemaVersion, summary.SchemaVersionTrust)
	}
	want := map[string]bool{}
	for _, s := range req.Profile.Specs() {
		want[s.ID] = true
	}
	got := map[string]bool{}
	var problems []string
	for _, r := range sum.Results {
		if !want[r.ID] {
			problems = append(problems, fmt.Sprintf("unexpected result %q", r.ID))
			continue
		}
		if got[r.ID] {
			problems = append(problems, fmt.Sprintf("duplicate result %q", r.ID))
			continue
		}
		got[r.ID] = true
		switch {
		case r.Status == summary.StatusSkip:
			problems = append(problems, fmt.Sprintf("%s skipped: %s", r.ID, r.Reason))
		case r.Value == nil:
			problems = append(problems, fmt.Sprintf("%s has no value", r.ID))
		case !r.Comparability.Complete():
			problems = append(problems, fmt.Sprintf("%s has no complete comparability identity", r.ID))
		// kube-slint derives each SLI's WindowID from the request WindowID and
		// the SLI's compute semantics, so only subject and source are compared
		// verbatim.
		case r.Comparability.SubjectID != req.SubjectID ||
			r.Comparability.SourceConfigID != req.Profile.SourceConfigID:
			problems = append(problems, fmt.Sprintf("%s comparability does not match the request TrustContract", r.ID))
		}
	}
	for id := range want {
		if !got[id] {
			problems = append(problems, fmt.Sprintf("missing result %q", id))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("incomplete measurement for target %q: %s", req.Profile.Target, strings.Join(problems, "; "))
	}
	return nil
}
