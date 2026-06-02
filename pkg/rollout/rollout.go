// Package rollout manages BoriRollout plans.
// A rollout describes the ordered sequence of steps needed to deploy a revision.
// It supports dry-run mode: plan is generated without applying anything.
package rollout

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HeaInSeo/bori/pkg/artifact"
)

// BoriRollout is the planned deployment sequence for one revision.
type BoriRollout struct {
	SchemaVersion string `json:"schemaVersion"`
	RolloutID     string `json:"rolloutId"`
	Release       string `json:"release"`
	Environment   string `json:"environment"`
	RevisionID    string `json:"revisionId"`
	// FromRevision is the previously promoted revision being replaced, if known.
	FromRevision string        `json:"fromRevision,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	Steps        []RolloutStep `json:"steps"`
	// Status: planned | in-progress | completed | failed | rolled-back
	Status string `json:"status"`
}

// RolloutStep is one action in the rollout sequence.
type RolloutStep struct {
	Component string `json:"component"`
	// Action: deploy | verify | gate | skip
	Action      string `json:"action"`
	Adapter     string `json:"adapter,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion"`
	// Status: pending | success | failed | skipped
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// BuildFromPlan derives a rollout plan from a deploy plan and revision ID.
// The plan is in pending / dry-run state until bori deploy executes it.
func BuildFromPlan(plan artifact.Plan, revisionID string) BoriRollout {
	now := time.Now().UTC()
	rollout := BoriRollout{
		SchemaVersion: "bori.rollout.v1",
		RolloutID:     newRolloutID(plan.Release, plan.Environment, now),
		Release:       plan.Release,
		Environment:   plan.Environment,
		RevisionID:    revisionID,
		CreatedAt:     now,
		Status:        "planned",
	}

	for _, cp := range plan.Components {
		action := "deploy"
		status := "pending"
		if cp.Action == "violation" {
			action = "skip"
			status = "skipped"
		}
		rollout.Steps = append(rollout.Steps, RolloutStep{
			Component: cp.Name,
			Action:    action,
			Adapter:   cp.Adapter,
			Namespace: cp.Namespace,
			ToVersion: cp.Version,
			Status:    status,
		})
		// Add a verification step after each deploy step.
		if action == "deploy" {
			rollout.Steps = append(rollout.Steps, RolloutStep{
				Component: cp.Name,
				Action:    "verify",
				ToVersion: cp.Version,
				Status:    "pending",
			})
		}
	}
	return rollout
}

// Write serializes r as JSON to <boriDir>/rollouts/<r.RolloutID>.json.
func Write(boriDir string, r BoriRollout) (string, error) {
	dir := filepath.Join(boriDir, "rollouts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, r.RolloutID+".json")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(r); encErr != nil {
		_ = f.Close()
		return "", fmt.Errorf("encode: %w", encErr)
	}
	return path, f.Close()
}

// Read parses <boriDir>/rollouts/<rolloutID>.json.
func Read(boriDir, rolloutID string) (BoriRollout, error) {
	path := filepath.Join(boriDir, "rollouts", rolloutID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriRollout{}, fmt.Errorf("read %s: %w", path, err)
	}
	var r BoriRollout
	if err := json.Unmarshal(data, &r); err != nil {
		return BoriRollout{}, fmt.Errorf("parse: %w", err)
	}
	return r, nil
}

func newRolloutID(release, environment string, at time.Time) string {
	ts := at.UTC().Format("20060102-150405")
	safe := strings.NewReplacer("/", "-", " ", "-").Replace(release)
	return fmt.Sprintf("rollout-%s-%s-%s", safe, environment, ts)
}
