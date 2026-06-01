package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HeaInSeo/bori/pkg/adapter"
	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/verification"
)

func main() {
	profile := flag.String("profile", "devspace", "profile: devspace|kind|multipass")
	appsDir := flag.String("apps-dir", "", "directory containing app repos (default: parent of bori root)")
	smokeCmd := flag.String("smoke-cmd", "", "shell command for smoke step — developer mode only, unsafe in shared envs")
	smokeWait := flag.Duration("smoke-wait", 10*time.Second, "wait duration if --smoke-cmd is not set")
	outDir := flag.String("out", "bori-gate-output", "output directory for gate artifacts")
	slintGate := flag.String("slint-gate", "slint-gate", "path to slint-gate binary")
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()

	logf := func(string, ...any) {}
	if *verbose {
		logf = func(f string, a ...any) { fmt.Printf("[bori] "+f+"\n", a...) }
	}

	if *appsDir == "" {
		*appsDir = filepath.Join(boriRoot(), "..")
	}

	runID := time.Now().UTC().Format("20060102-150405")
	startedAt := time.Now().UTC()

	// status.json is written on every exit — success or failure.
	status := artifact.Status{
		SchemaVersion: "bori.run.v1",
		RunID:         runID,
		Profile:       *profile,
		StartedAt:     startedAt,
		Phase:         "Failed",
		Result:        "NO_GRADE",
	}
	defer func() {
		status.FinishedAt = time.Now().UTC()
		if err := artifact.Write(*outDir, status); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write status.json: %v\n", err)
		}
	}()

	apps, err := discoverApps(*appsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[bori] discover apps: %v\n", err)
		os.Exit(1)
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "[bori] no .bori/component.yaml files found — nothing to evaluate")
		status.Phase = "Verified"
		status.Result = "PASS"
		os.Exit(0)
	}

	logf("found %d registered app(s)", len(apps))

	runner := &adapter.GateRunner{SlintGateBin: *slintGate}
	ctx := context.Background()

	overall := verification.GateResultPass
	var compStatuses []artifact.CompStatus

	for _, app := range apps {
		policyPath := filepath.Join(app.BoriDir, fmt.Sprintf("policy.%s.yaml", *profile))
		if _, err := os.Stat(policyPath); os.IsNotExist(err) {
			logf("skip %s: no policy.%s.yaml", app.Comp.Name, *profile)
			continue
		}

		cs := artifact.CompStatus{Name: app.Comp.Name, GateResult: string(verification.GateResultNoGrade)}

		logf("pre-smoke scrape: %s", app.Comp.Name)
		before, err := scrapeMetrics(ctx, app.Comp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: pre-smoke scrape: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}
		preAt := time.Now().UTC()

		if err := runSmoke(ctx, *smokeCmd, *smokeWait, app.Comp.Name, logf); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: smoke: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultFail)
			continue
		}

		logf("post-smoke scrape: %s", app.Comp.Name)
		after, err := scrapeMetrics(ctx, app.Comp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: post-smoke scrape: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}
		postAt := time.Now().UTC()

		req := adapter.RunRequest{
			Profile:    *profile,
			App:        app.Comp.Name,
			PolicyPath: policyPath,
			Before:     adapter.AppSnapshot{App: app.Comp.Name, At: preAt, Values: before},
			After:      adapter.AppSnapshot{App: app.Comp.Name, At: postAt, Values: after},
			OutDir:     filepath.Join(*outDir, app.Comp.Name),
		}
		result, err := runner.Run(ctx, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: gate: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}

		fmt.Printf("[bori] %-20s %s — %s\n", app.Comp.Name, result.GateResult, result.Message)

		gateResult := verification.GateResult(result.GateResult)
		cs.GateResult = string(gateResult)
		cs.Message = result.Message
		compStatuses = append(compStatuses, cs)
		overall = verification.Max(overall, gateResult)
	}

	status.Components = compStatuses
	status.Result = string(overall)
	if overall == verification.GateResultPass || overall == verification.GateResultWarn {
		status.Phase = "Verified"
	} else {
		status.Phase = "Failed"
	}

	fmt.Printf("[bori] overall: %s\n", overall)

	if verification.IsBlocking(overall, verification.FailOnFail) {
		var failed []string
		for _, cs := range compStatuses {
			if cs.GateResult == string(verification.GateResultFail) {
				failed = append(failed, cs.Name)
			}
		}
		fmt.Printf("[bori] failed: %s\n", strings.Join(failed, ", "))
		os.Exit(1)
	}
}

func runSmoke(ctx context.Context, cmd string, wait time.Duration, appName string, logf func(string, ...any)) error {
	if cmd != "" {
		logf("smoke cmd (developer mode): %s", cmd)
		c := exec.CommandContext(ctx, "sh", "-c", cmd)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("smoke command: %w", err)
		}
		return nil
	}
	logf("waiting %s for %s", wait, appName)
	time.Sleep(wait)
	return nil
}

// boriRoot walks up from the executable to find the bori repo root
// (identified by the presence of devspace.yaml).
func boriRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	for {
		if _, err := os.Stat(filepath.Join(dir, "devspace.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}
