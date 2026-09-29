package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HeaInSeo/bori/pkg/collect"
	"github.com/HeaInSeo/bori/pkg/verification"
)

// TestVerifyChild is not a test by itself: the dispatch tests below re-exec the
// test binary into it so cmdVerify (which may os.Exit) runs in a child process
// with the scraper replaced by a recorder.
func TestVerifyChild(t *testing.T) {
	if os.Getenv("BORI_VERIFY_CHILD") != "1" {
		t.Skip("helper process")
	}
	scrapeMetrics = func(_ context.Context, tg collect.Target) (map[string]float64, error) {
		fmt.Printf("SCRAPE %s/%s:%d%s\n", tg.Namespace, tg.ServiceName, tg.Port, tg.MetricsPath)
		return map[string]float64{"up": 1}, nil
	}
	cmdVerify(strings.Split(os.Getenv("BORI_VERIFY_ARGS"), "\x1f"))
	os.Exit(0)
}

// runVerifyChild runs `bori verify args...` with a recording scraper and a fake
// slint-gate that grades every summary PASS.
func runVerifyChild(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	gate := filepath.Join(dir, "slint-gate")
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = --output ] && out=$2; shift; done\n" +
		"printf '{\"gate_result\":\"PASS\"}' > \"$out\"\n"
	if err := os.WriteFile(gate, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	args = append(args, "--slint-gate", gate, "--bori-dir", filepath.Join(dir, ".bori"), "--smoke-wait", "0s")

	cmd := exec.Command(os.Args[0], "-test.run=^TestVerifyChild$")
	cmd.Env = append(os.Environ(),
		"BORI_VERIFY_CHILD=1",
		"BORI_VERIFY_ARGS="+strings.Join(args, "\x1f"),
		"KUBECONFIG=",
	)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("run child: %v", err)
	}
	return out.String(), errOut.String(), exitCode
}

func writeApp(t *testing.T, appsDir, name string) {
	t.Helper()
	boriDir := filepath.Join(appsDir, name, ".bori")
	if err := os.MkdirAll(boriDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(boriDir, "component.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(boriDir, "policy.devspace.yaml"), []byte("gate:\n  fail_on: NONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Legacy discovery components have no compile-time profile; they must keep the
// scrape→summary→gate path and pass instead of failing on lookupProfile.
func TestVerify_LegacyDiscoveryUsesScrapePath(t *testing.T) {
	appsDir := t.TempDir()
	writeApp(t, appsDir, "jumi")
	writeApp(t, appsDir, "artifact-handoff")

	stdout, stderr, code := runVerifyChild(t, "--apps-dir", appsDir)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"SCRAPE jumi/jumi:8080/metrics",
		"SCRAPE artifact-handoff/artifact-handoff:8080/metrics",
		"overall: PASS",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stderr, "compile-time measurement profile") {
		t.Errorf("legacy mode consulted the compile-time profiles:\n%s", stderr)
	}
}

// Release components (jumi, artifact-handoff) are scraped at their declared
// metrics endpoint, never routed through the compile-time profile table.
func TestVerify_ReleaseUsesScrapePath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, _ := runVerifyChild(t,
		"--release", "jumi-ah-dev", "--env", "jumi-ah-dev", "--bori-root", root, "--apps-dir", t.TempDir())
	for _, comp := range []string{"jumi", "artifact-handoff"} {
		if !strings.Contains(stdout, "SCRAPE ") || !strings.Contains(stdout, "/"+comp+":") {
			t.Errorf("release component %s was not scraped:\nstdout:\n%s\nstderr:\n%s", comp, stdout, stderr)
		}
	}
	if strings.Contains(stderr, "compile-time measurement profile") {
		t.Errorf("release mode consulted the compile-time profiles:\n%s", stderr)
	}
}

// An unknown explicit --target stays fail closed: NO_GRADE and a nonzero exit,
// with no scrape fallback.
func TestVerify_UnknownExplicitTargetFailsClosed(t *testing.T) {
	stdout, stderr, code := runVerifyChild(t,
		"--target", "jumi", "--policy", filepath.Join(t.TempDir(), "policy.yaml"),
		"--subject-id", "s", "--window-id", "w")
	if code == 0 {
		t.Fatalf("unknown target exited 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, `no compile-time measurement profile for target "jumi"`) {
		t.Errorf("stderr missing profile error:\n%s", stderr)
	}
	if strings.Contains(stdout, "SCRAPE") {
		t.Errorf("explicit target fell back to the scrape path:\n%s", stdout)
	}
}

func legacyTarget(name, policyPath string) verifyTarget {
	return verifyTarget{
		Name: name, Namespace: name, ServiceName: name, Port: 8080, MetricsPath: "/metrics",
		Policies: []resolvedPolicy{{
			Name: "smoke", PolicyPath: policyPath, FailOn: verification.FailOnFailOrNoGrade,
		}},
	}
}

func useScraper(t *testing.T, f func(context.Context, collect.Target) (map[string]float64, error)) {
	t.Helper()
	orig := scrapeMetrics
	scrapeMetrics = f
	t.Cleanup(func() { scrapeMetrics = orig })
}

func TestRunOneVerification_ScrapeTargetGatesBoriSummary(t *testing.T) {
	s := &fakeSession{}
	useSession(t, s)
	var scraped []collect.Target
	useScraper(t, func(_ context.Context, tg collect.Target) (map[string]float64, error) {
		scraped = append(scraped, tg)
		return map[string]float64{"up": 1}, nil
	})
	prov := &recordingProvider{}
	runDir := t.TempDir()
	cs, gr, blocked := runOneVerification(context.Background(), legacyTarget("jumi", writePolicy(t)), "run-1",
		runDir, "devspace", "", 0, testOpts, prov, logNone)
	if gr != verification.GateResultPass || cs.GateResult != string(verification.GateResultPass) || blocked {
		t.Fatalf("result = %s / %s, blocked = %v", gr, cs.GateResult, blocked)
	}
	if len(scraped) != 2 || scraped[0].ServiceName != "jumi" || scraped[0].Port != 8080 {
		t.Fatalf("scrapes = %+v", scraped)
	}
	if s.started {
		t.Fatal("scrape target started a kube-slint producer session")
	}
	want := filepath.Join(runDir, "evidence", "jumi", "jumi-sli-summary.json")
	if len(prov.calls) != 1 || prov.calls[0].MeasurementSummaryPath != want {
		t.Fatalf("gate calls = %+v, want summary %s", prov.calls, want)
	}
}

func TestRunOneVerification_ScrapeFailureIsNoGradeWithoutGate(t *testing.T) {
	useScraper(t, func(context.Context, collect.Target) (map[string]float64, error) {
		return nil, errors.New("port-forward failed")
	})
	prov := &recordingProvider{}
	_, gr, blocked := runOneVerification(context.Background(), legacyTarget("jumi", writePolicy(t)), "run-1",
		t.TempDir(), "devspace", "", 0, testOpts, prov, logNone)
	if gr != verification.GateResultNoGrade || blocked || len(prov.calls) != 0 {
		t.Fatalf("result = %s, blocked = %v, gate calls = %d", gr, blocked, len(prov.calls))
	}
}

func TestRunOneVerification_ScrapeSmokeFailureIsFail(t *testing.T) {
	useScraper(t, func(context.Context, collect.Target) (map[string]float64, error) {
		return map[string]float64{}, nil
	})
	prov := &recordingProvider{}
	_, gr, blocked := runOneVerification(context.Background(), legacyTarget("jumi", writePolicy(t)), "run-1",
		t.TempDir(), "devspace", "exit 3", 0, testOpts, prov, logNone)
	if gr != verification.GateResultFail || blocked || len(prov.calls) != 0 {
		t.Fatalf("result = %s, blocked = %v, gate calls = %d", gr, blocked, len(prov.calls))
	}
}
