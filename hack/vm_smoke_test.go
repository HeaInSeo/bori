package hack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKubectl emulates the kubectl calls vm-smoke.sh makes against one
// BoriDataPlane kept in $FAKE_KUBE_STATE:
//
//   - FAKE_RECONCILE_AFTER=N: the operator sets observedGeneration on the Nth
//     get after create (0 = never).
//   - FAKE_DELETE_NOOP=1: delete leaves the object in place.
//   - FAKE_STALE_READ=1: get always returns a stale object that was reconciled
//     by an earlier run.
const fakeKubectl = `#!/usr/bin/env bash
set -euo pipefail
S="${FAKE_KUBE_STATE}"
echo "$1" >> "$S/calls"
case "$1" in
  delete)
    [ "${FAKE_DELETE_NOOP:-}" = 1 ] && exit 0
    rm -f "$S/uid" "$S/gen" "$S/obs" "$S/gets" ;;
  create)
    if [ -f "$S/uid" ]; then echo "AlreadyExists" >&2; exit 1; fi
    SEQ=$(( $(cat "$S/seq" 2>/dev/null || echo 0) + 1 )); echo "$SEQ" > "$S/seq"
    echo "uid-$SEQ" > "$S/uid"; echo 1 > "$S/gen"; : > "$S/obs"; echo 0 > "$S/gets"
    printf '%s %s\n' "$(cat "$S/uid")" "$(cat "$S/gen")" ;;
  get)
    if [ "${FAKE_STALE_READ:-}" = 1 ]; then printf 'uid-old 1\n'; exit 0; fi
    [ -f "$S/uid" ] || { echo "NotFound" >&2; exit 1; }
    GETS=$(( $(cat "$S/gets") + 1 )); echo "$GETS" > "$S/gets"
    AFTER="${FAKE_RECONCILE_AFTER:-0}"
    if [ "$AFTER" -gt 0 ] && [ "$GETS" -ge "$AFTER" ]; then cp "$S/gen" "$S/obs"; fi
    printf '%s %s\n' "$(cat "$S/uid")" "$(cat "$S/obs")" ;;
  *) echo "unexpected kubectl $*" >&2; exit 2 ;;
esac
`

type smokeEnv struct {
	state string
	path  string
}

// newSmokeEnv returns a fake cluster that already holds the fixture as an
// earlier run left it: reconciled, observedGeneration == generation == 1.
func newSmokeEnv(t *testing.T) smokeEnv {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(fakeKubectl), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	for name, v := range map[string]string{"uid": "uid-old", "gen": "1", "obs": "1", "gets": "0"} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return smokeEnv{state: state, path: bin + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func (e smokeEnv) run(t *testing.T, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "vm-smoke.sh", "bori-system", "infra-lab-smoke", "fixture.yaml")
	cmd.Env = append(os.Environ(),
		"PATH="+e.path,
		"FAKE_KUBE_STATE="+e.state,
		"VM_SMOKE_ATTEMPTS=3",
		"VM_SMOKE_POLL_SECONDS=0",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e smokeEnv) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.state, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestVMSmoke_WaitsForTheNewObjectGeneration(t *testing.T) {
	e := newSmokeEnv(t)
	out, err := e.run(t, "FAKE_RECONCILE_AFTER=2")
	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reconciled: uid=uid-1 observedGeneration=1") {
		t.Fatalf("output:\n%s", out)
	}
	if got := e.read(t, "calls"); got != "delete\ncreate\nget\nget" {
		t.Fatalf("kubectl calls = %q", got)
	}
}

// Every run recreates the fixture and waits for its own reconcile; the status
// the previous run produced is never accepted.
func TestVMSmoke_RerunWaitsAgain(t *testing.T) {
	e := newSmokeEnv(t)
	for i, uid := range []string{"uid-1", "uid-2"} {
		out, err := e.run(t, "FAKE_RECONCILE_AFTER=2")
		if err != nil {
			t.Fatalf("run %d failed: %v\n%s", i+1, err, out)
		}
		if !strings.Contains(out, "reconciled: uid="+uid+" ") {
			t.Fatalf("run %d output:\n%s", i+1, out)
		}
		if gets := e.read(t, "gets"); gets != "2" {
			t.Fatalf("run %d accepted after %s gets, want 2", i+1, gets)
		}
	}
}

func TestVMSmoke_FailsClosed(t *testing.T) {
	cases := map[string][]string{
		// The operator never reconciles the new object, although the old one
		// had observedGeneration 1.
		"never reconciled": {"FAKE_RECONCILE_AFTER=0"},
		// Reads keep returning the earlier run's reconciled object.
		"stale read": {"FAKE_STALE_READ=1", "FAKE_RECONCILE_AFTER=1"},
		// The old object survives delete, so it cannot be recreated.
		"delete no-op": {"FAKE_DELETE_NOOP=1", "FAKE_RECONCILE_AFTER=1"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := newSmokeEnv(t).run(t, env...)
			if err == nil {
				t.Fatalf("smoke passed, want failure:\n%s", out)
			}
		})
	}
}
