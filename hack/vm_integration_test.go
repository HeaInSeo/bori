package hack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fakes for the remote side of test-vm-integration.sh. Every ssh call is logged
// to $FAKE_LOG; `bori verify` exits with $FAKE_BORI_RC. scp copies nothing, and
// copying the .bori run archive fails, as when bori never wrote it.
const (
	fakeSSH = `#!/usr/bin/env bash
echo "$*" >> "${FAKE_LOG}"
case "$*" in
  *"bori verify"*) exit "${FAKE_BORI_RC}" ;;
  *"uname -m"*) echo x86_64 ;;
  *mktemp*) echo /tmp/fake-remote ;;
  *imageID*) echo "registry/bori-operator@sha256:0" ;;
esac
exit 0
`
	fakeSCP = `#!/usr/bin/env bash
case "$*" in
  *.bori*) echo "scp: .bori: No such file or directory" >&2; exit 1 ;;
esac
exit 0
`
	fakeGo = `#!/usr/bin/env bash
while [ $# -gt 0 ]; do [ "$1" = -o ] && : > "$2"; shift; done
`
)

// runVMIntegration runs a copy of test-vm-integration.sh in a scratch repo
// against the fakes and returns its combined output and the ssh call log.
func runVMIntegration(t *testing.T, boriRC string) (string, string, error) {
	t.Helper()
	script, err := os.ReadFile("test-vm-integration.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"hack/test-vm-integration.sh":                  string(script),
		"hack/vm-smoke.sh":                             "",
		"test/e2e/manifests/bori-metrics-service.yaml": "",
		"test/e2e/manifests/slint-sa.yaml":             "",
		"test/e2e/.slint/policy.yaml":                  "",
		"testdata/fixtures/bdp-infra-lab-smoke.yaml":   "",
		"kube-slint/go.mod":                            "",
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	for name, content := range map[string]string{"ssh": fakeSSH, "scp": fakeSCP, "go": fakeGo} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(t.TempDir(), "ssh.log")

	cmd := exec.Command("bash", filepath.Join(root, "hack", "test-vm-integration.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BORI_VM_REMOTE=fake@vm",
		"KUBE_SLINT_DIR="+filepath.Join(root, "kube-slint"),
		"FAKE_LOG="+log,
		"FAKE_BORI_RC="+boriRC,
	)
	out, runErr := cmd.CombinedOutput()
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(calls), runErr
}

// When bori fails without leaving a run archive, the script must still report
// bori's exit code and collect diagnostics instead of aborting in the archive
// search.
func TestVMIntegration_MissingArchiveReportsBoriFailure(t *testing.T) {
	out, calls, err := runVMIntegration(t, "7")
	if err == nil {
		t.Fatalf("script passed, want failure:\n%s", out)
	}
	if !strings.Contains(out, "FAIL: bori verify --target bori-operator failed (rc=7)") {
		t.Fatalf("output lacks the explicit bori failure:\n%s", out)
	}
	if !strings.Contains(out, "collecting artifacts") || !strings.Contains(calls, "get events") {
		t.Fatalf("diagnostics were not collected:\noutput:\n%s\nssh calls:\n%s", out, calls)
	}
}

// bori succeeding without an archive is still a failure, reported as a missing
// summary with diagnostics.
func TestVMIntegration_MissingArchiveWithoutSummaryFails(t *testing.T) {
	out, calls, err := runVMIntegration(t, "0")
	if err == nil {
		t.Fatalf("script passed, want failure:\n%s", out)
	}
	if !strings.Contains(out, "FAIL: kube-slint sli-summary.json was not produced") {
		t.Fatalf("output lacks the missing-summary failure:\n%s", out)
	}
	if !strings.Contains(calls, "get events") {
		t.Fatalf("diagnostics were not collected:\n%s", calls)
	}
}
