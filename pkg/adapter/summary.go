package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// sliSummary is the JSON structure that slint-gate --measurement-summary expects.
// Schema mirrors kube-slint pkg/slo/summary.Summary (slint.summary.v4).
type sliSummary struct {
	SchemaVersion string       `json:"schemaVersion"`
	GeneratedAt   time.Time    `json:"generatedAt"`
	Config        runConfig    `json:"config"`
	Reliability   *reliability `json:"reliability,omitempty"`
	Results       []sliResult  `json:"results"`
}

type runConfig struct {
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	Mode       runMode           `json:"mode"`
	Tags       map[string]string `json:"tags,omitempty"`
	Format     string            `json:"format,omitempty"`
}

type runMode struct {
	Location string `json:"location"`
	Trigger  string `json:"trigger"`
}

type reliability struct {
	CollectionStatus string `json:"collectionStatus,omitempty"`
	EvaluationStatus string `json:"evaluationStatus,omitempty"`
}

type sliResult struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind,omitempty"`
	Value *float64 `json:"value,omitempty"`
}

// buildDeltaSummary creates an sli-summary.json from before/after metric snapshots.
// Each metric delta becomes one SLIResult with ID = metric name.
// slint-gate policy thresholds reference these IDs directly.
func buildDeltaSummary(req RunRequest) sliSummary {
	results := make([]sliResult, 0, len(req.After.Values))
	for name, afterVal := range req.After.Values {
		beforeVal := req.Before.Values[name]
		delta := afterVal - beforeVal
		d := delta
		results = append(results, sliResult{
			ID:    name,
			Kind:  "delta_counter",
			Value: &d,
		})
	}
	return sliSummary{
		SchemaVersion: "slint.summary.v4",
		GeneratedAt:   time.Now().UTC(),
		Config: runConfig{
			StartedAt:  req.Before.At,
			FinishedAt: req.After.At,
			Mode: runMode{
				Location: "outside",
				Trigger:  "bori:" + req.Profile,
			},
			Tags: map[string]string{
				"profile": req.Profile,
				"app":     req.App,
			},
			Format: "v4",
		},
		Reliability: &reliability{
			CollectionStatus: "Complete",
			EvaluationStatus: "Complete",
		},
		Results: results,
	}
}

// BuildMeasurementSummary creates sli-summary.json from before/after snapshots
// and writes it under outDir. Returns the path to the written file.
//
// This is a temporary shim. Long-term, kube-slint owns measurement collection
// and bori will consume a pre-built summary from it.
func BuildMeasurementSummary(req RunRequest, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	sum := buildDeltaSummary(req)
	path := filepath.Join(outDir, req.App+"-sli-summary.json")
	if err := writeSummary(path, sum); err != nil {
		return "", err
	}
	return path, nil
}

func writeSummary(path string, sum sliSummary) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sum); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode: %w", err)
	}
	return f.Close()
}
