package shadow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteState persists the shadow state to <boriDir>/shadow-<release>.json.
// This file is the equivalent of a Kubernetes status subresource — it records
// the observed state without modifying the cluster.
func WriteState(boriDir, release string, state ShadowState) error {
	if err := os.MkdirAll(boriDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", boriDir, err)
	}
	// Sanitize release name for filename safety.
	safe := release
	for _, r := range []string{"/", " ", ":"} {
		for i := range safe {
			if string(safe[i]) == r {
				safe = safe[:i] + "-" + safe[i+1:]
			}
		}
	}
	path := filepath.Join(boriDir, "shadow-"+safe+".json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(state); encErr != nil {
		_ = f.Close()
		return fmt.Errorf("encode: %w", encErr)
	}
	return f.Close()
}

// ReadState loads the shadow state for a release from <boriDir>/shadow-<release>.json.
func ReadState(boriDir, release string) (ShadowState, error) {
	safe := release
	path := filepath.Join(boriDir, "shadow-"+safe+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ShadowState{}, fmt.Errorf("read %s: %w", path, err)
	}
	var state ShadowState
	if err := json.Unmarshal(data, &state); err != nil {
		return ShadowState{}, fmt.Errorf("parse: %w", err)
	}
	return state, nil
}
