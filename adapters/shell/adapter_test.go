package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HeaInSeo/bori/pkg/adapter"
	"github.com/HeaInSeo/bori/pkg/model"
)

// writeDeployScript creates <appsDir>/<comp>/deploy.sh so the adapter's
// os.Stat(scriptPath) precondition passes and execution reaches the shell gate.
func writeDeployScript(t *testing.T, appsDir, comp string) {
	t.Helper()
	compDir := filepath.Join(appsDir, comp)
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(compDir, "deploy.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write deploy.sh: %v", err)
	}
}

// TestDeploy_NoShellCapability_FailsFastUnsupported is the regression test for
// the K1 distroless failure: a runtime without an `sh` interpreter (no shell
// capability) must fail fast with an explicit unsupported error rather than a
// cryptic exec error that a reconcile loop retries indefinitely.
func TestDeploy_NoShellCapability_FailsFastUnsupported(t *testing.T) {
	appsDir := t.TempDir()
	writeDeployScript(t, appsDir, "jumi")

	// Simulate a runtime without the shell capability: no `sh` on PATH.
	t.Setenv("PATH", t.TempDir())

	a := New(appsDir)
	res, err := a.Deploy(context.Background(), adapter.DeployRequest{
		Component: model.BoriComponent{Name: "jumi"},
	})

	if err == nil {
		t.Fatalf("expected unsupported error when sh is absent, got nil (res=%+v)", res)
	}
	if !strings.Contains(err.Error(), "no shell capability") {
		t.Fatalf("error should name the missing shell capability, got: %v", err)
	}
	if res == nil || res.Success {
		t.Fatalf("expected non-nil failed result, got %+v", res)
	}
	if !strings.Contains(res.Message, "shell capability") {
		t.Fatalf("result message should explain the missing shell capability, got: %q", res.Message)
	}
}

// TestDeploy_DryRun_NeedsNoShellCapability documents that the dry-run path
// (used by the K1/K2 deploy-dry-run smoke) short-circuits before the shell
// gate, so it succeeds on a shell-less runtime.
func TestDeploy_DryRun_NeedsNoShellCapability(t *testing.T) {
	appsDir := t.TempDir()
	writeDeployScript(t, appsDir, "jumi")

	t.Setenv("PATH", t.TempDir())

	a := New(appsDir)
	res, err := a.Deploy(context.Background(), adapter.DeployRequest{
		Component: model.BoriComponent{Name: "jumi"},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("dry-run should not require a shell capability, got err: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("dry-run should succeed, got %+v", res)
	}
}
