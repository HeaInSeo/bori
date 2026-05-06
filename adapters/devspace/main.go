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
)

func main() {
	profile := flag.String("profile", "devspace", "profile: devspace|kind|multipass")
	appsDir := flag.String("apps-dir", "", "directory containing app repos (default: parent of bori root)")
	smokeCmd := flag.String("smoke-cmd", "", "shell command to run as smoke step")
	smokeWait := flag.Duration("smoke-wait", 10*time.Second, "wait duration if --smoke-cmd is not set")
	outDir := flag.String("out", "bori-gate-output", "output directory for artifacts")
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

	apps, err := discoverApps(*appsDir)
	if err != nil {
		fatalf("discover apps: %v", err)
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "[bori] no .bori/component.yaml files found — nothing to evaluate")
		os.Exit(0)
	}

	logf("found %d registered app(s)", len(apps))

	runner := &adapter.GateRunner{SlintGateBin: *slintGate}
	ctx := context.Background()

	overall := "PASS"
	var failures []string

	for _, app := range apps {
		policyPath := filepath.Join(app.BoriDir, fmt.Sprintf("policy.%s.yaml", *profile))
		if _, err := os.Stat(policyPath); os.IsNotExist(err) {
			logf("skip %s: no policy.%s.yaml", app.Comp.Name, *profile)
			continue
		}

		logf("pre-smoke scrape: %s", app.Comp.Name)
		before, err := scrapeMetrics(ctx, app.Comp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: pre-smoke: %v\n", app.Comp.Name, err)
			failures = append(failures, app.Comp.Name)
			continue
		}
		preAt := time.Now().UTC()

		if err := runSmoke(ctx, *smokeCmd, *smokeWait, app.Comp.Name, logf); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: smoke failed: %v\n", app.Comp.Name, err)
			failures = append(failures, app.Comp.Name)
			continue
		}

		logf("post-smoke scrape: %s", app.Comp.Name)
		after, err := scrapeMetrics(ctx, app.Comp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: post-smoke: %v\n", app.Comp.Name, err)
			failures = append(failures, app.Comp.Name)
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
			failures = append(failures, app.Comp.Name)
			continue
		}

		fmt.Printf("[bori] %-20s %s — %s\n", app.Comp.Name, result.GateResult, result.Message)

		switch result.GateResult {
		case "FAIL":
			overall = "FAIL"
			failures = append(failures, app.Comp.Name)
		case "WARN":
			if overall == "PASS" {
				overall = "WARN"
			}
		}
	}

	fmt.Printf("[bori] overall: %s\n", overall)
	if len(failures) > 0 {
		fmt.Printf("[bori] failed: %s\n", strings.Join(failures, ", "))
		os.Exit(1)
	}
}

func runSmoke(ctx context.Context, cmd string, wait time.Duration, appName string, logf func(string, ...any)) error {
	if cmd != "" {
		logf("smoke cmd: %s", cmd)
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[bori] "+format+"\n", args...)
	os.Exit(1)
}
