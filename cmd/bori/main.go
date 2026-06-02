// bori is the unified deploy/verify gateway for genomic dataplane apps.
//
// Usage:
//
//	bori plan   --release <name> --env <name> [--bori-root <dir>]
//	bori deploy --release <name> --env <name> [--bori-root <dir>] [--apps-dir <dir>]
//	bori verify [--apps-dir <dir>] [--profile <p>] [--bori-dir <dir>]
//	bori status --run <run-id> [--bori-dir <dir>]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	devspaceadapter "github.com/HeaInSeo/bori/adapters/devspace"
	koadapter "github.com/HeaInSeo/bori/adapters/ko"
	kustomizeadapter "github.com/HeaInSeo/bori/adapters/kustomize"
	shelladapter "github.com/HeaInSeo/bori/adapters/shell"
	"github.com/HeaInSeo/bori/pkg/adapter"
	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/collect"
	"github.com/HeaInSeo/bori/pkg/component"
	"github.com/HeaInSeo/bori/pkg/model"
	"github.com/HeaInSeo/bori/pkg/planner"
	"github.com/HeaInSeo/bori/pkg/verification"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "plan":
		cmdPlan(os.Args[2:])
	case "deploy":
		cmdDeploy(os.Args[2:])
	case "verify":
		cmdVerify(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "bori: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `bori — genomic dataplane control plane gateway

Usage:
  bori plan   --release <name> --env <name> [--bori-root <dir>] [--bori-dir <dir>]
  bori deploy --release <name> --env <name> [--bori-root <dir>] [--apps-dir <dir>] [--bori-dir <dir>]
  bori verify [--apps-dir <dir>] [--profile <p>] [--bori-dir <dir>]
  bori status --run <run-id> [--bori-dir <dir>]`)
}

// cmdPlan loads the release/environment model and prints the deploy plan.
func cmdPlan(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	releaseName := fs.String("release", "", "release name (required)")
	envName := fs.String("env", "", "environment name (required)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory for run archives")
	_ = fs.Parse(args)

	if *releaseName == "" || *envName == "" {
		fmt.Fprintln(os.Stderr, "bori plan: --release and --env are required")
		os.Exit(1)
	}

	runID := time.Now().UTC().Format("20060102-150405")
	runDir := artifact.RunDir(*boriDir, runID)

	p := planner.New(*boriRoot)
	plan, err := p.Plan(runID, *releaseName, *envName)
	if err != nil {
		fatalf("plan: %v", err)
	}

	if err := artifact.WritePlan(runDir, *plan); err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not write plan.json: %v\n", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(plan)

	if len(plan.Violations) > 0 {
		fmt.Fprintf(os.Stderr, "[bori] plan: %d namespace violation(s) — deploy blocked\n", len(plan.Violations))
		for _, v := range plan.Violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", v)
		}
		os.Exit(1)
	}
	fmt.Printf("[bori] plan ok — %d component(s)  run-id: %s\n", len(plan.Components), runID)
}

// cmdDeploy runs the deploy plan and executes each adapter.
func cmdDeploy(args []string) {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	releaseName := fs.String("release", "", "release name (required)")
	envName := fs.String("env", "", "environment name (required)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root")
	appsDir := fs.String("apps-dir", "", "directory containing app repos (default: parent of bori-root)")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory for run archives")
	dryRun := fs.Bool("dry-run", false, "print plan without applying")
	_ = fs.Parse(args)

	if *releaseName == "" || *envName == "" {
		fmt.Fprintln(os.Stderr, "bori deploy: --release and --env are required")
		os.Exit(1)
	}
	if *appsDir == "" {
		abs, err := filepath.Abs(*boriRoot)
		if err != nil {
			fatalf("abs bori-root: %v", err)
		}
		*appsDir = filepath.Join(abs, "..")
	}

	runID := time.Now().UTC().Format("20060102-150405")
	runDir := artifact.RunDir(*boriDir, runID)
	startedAt := time.Now().UTC()

	status := artifact.Status{
		SchemaVersion: "bori.run.v1",
		RunID:         runID,
		Release:       *releaseName,
		Environment:   *envName,
		StartedAt:     startedAt,
		Phase:         "Failed",
		Result:        string(verification.GateResultNoGrade),
	}
	defer func() {
		status.FinishedAt = time.Now().UTC()
		if err := artifact.Write(runDir, status); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write status.json: %v\n", err)
		}
	}()

	p := planner.New(*boriRoot)
	plan, err := p.Plan(runID, *releaseName, *envName)
	if err != nil {
		fatalf("plan: %v", err)
	}
	if err := artifact.WritePlan(runDir, *plan); err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not write plan.json: %v\n", err)
	}
	if len(plan.Violations) > 0 {
		fmt.Fprintf(os.Stderr, "[bori] deploy blocked: namespace violations\n")
		for _, v := range plan.Violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", v)
		}
		os.Exit(1)
	}

	adapters := buildAdapterRegistry(*appsDir)
	ctx := context.Background()
	overall := verification.GateResultPass
	var compStatuses []artifact.CompStatus

	for _, cp := range plan.Components {
		a, ok := adapters[cp.Adapter]
		if !ok {
			fmt.Fprintf(os.Stderr, "[bori] %s: unknown adapter %q\n", cp.Name, cp.Adapter)
			compStatuses = append(compStatuses, artifact.CompStatus{
				Name:       cp.Name,
				GateResult: string(verification.GateResultFail),
				Message:    fmt.Sprintf("unknown adapter: %s", cp.Adapter),
			})
			overall = verification.Max(overall, verification.GateResultFail)
			continue
		}

		comp, err := compForDeploy(*boriRoot, cp.Name, cp.Version, cp.ImageRef)
		if err != nil {
			fatalf("load component %s: %v", cp.Name, err)
		}
		env, err := envForDeploy(*boriRoot, *envName)
		if err != nil {
			fatalf("load environment %s: %v", *envName, err)
		}

		result, err := a.Deploy(ctx, adapter.DeployRequest{
			Component:   comp,
			Environment: env,
			DryRun:      *dryRun,
			OutDir:      filepath.Join(runDir, "deploy", cp.Name),
		})
		cs := artifact.CompStatus{Name: cp.Name}
		if err != nil || (result != nil && !result.Success) {
			msg := ""
			if err != nil {
				msg = err.Error()
			} else {
				msg = result.Message
			}
			fmt.Fprintf(os.Stderr, "[bori] %s: deploy failed: %s\n", cp.Name, msg)
			cs.GateResult = string(verification.GateResultFail)
			cs.Message = msg
			overall = verification.Max(overall, verification.GateResultFail)
		} else {
			fmt.Printf("[bori] %-24s deployed  (%s)\n", cp.Name, result.Message)
			cs.GateResult = string(verification.GateResultPass)
			cs.Message = result.Message
		}
		compStatuses = append(compStatuses, cs)
	}

	status.Components = compStatuses
	status.Result = string(overall)
	if overall == verification.GateResultPass {
		status.Phase = "Deployed"
	} else {
		status.Phase = "Failed"
	}

	fmt.Printf("[bori] deploy overall: %s  run-id: %s\n", overall, runID)
	if overall == verification.GateResultFail {
		os.Exit(1)
	}
}

// cmdVerify discovers apps and runs verification, writing a run archive always.
func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	appsDir := fs.String("apps-dir", "", "directory containing app repos (default: parent of cwd)")
	profile := fs.String("profile", "devspace", "profile: devspace|kind|multipass")
	smokeCmd := fs.String("smoke-cmd", "", "shell command for smoke step — developer mode only")
	smokeWait := fs.Duration("smoke-wait", 10*time.Second, "wait if --smoke-cmd is not set")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory for run archives")
	slintGate := fs.String("slint-gate", "slint-gate", "path to slint-gate binary")
	verbose := fs.Bool("v", false, "verbose output")
	_ = fs.Parse(args)

	logf := func(string, ...any) {}
	if *verbose {
		logf = func(f string, a ...any) { fmt.Printf("[bori] "+f+"\n", a...) }
	}

	if *appsDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatalf("getwd: %v", err)
		}
		*appsDir = filepath.Join(cwd, "..")
	}

	runID := time.Now().UTC().Format("20060102-150405")
	runDir := artifact.RunDir(*boriDir, runID)
	startedAt := time.Now().UTC()

	status := artifact.Status{
		SchemaVersion: "bori.run.v1",
		RunID:         runID,
		Profile:       *profile,
		StartedAt:     startedAt,
		Phase:         "Failed",
		Result:        string(verification.GateResultNoGrade),
	}

	// Always write status.json, even if we exit early.
	defer func() {
		status.FinishedAt = time.Now().UTC()
		if err := artifact.Write(runDir, status); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write status.json: %v\n", err)
		} else {
			logf("run archive: %s/status.json", runDir)
		}
	}()

	apps, err := component.Discover(*appsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[bori] discover apps: %v\n", err)
		os.Exit(1)
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "[bori] no .bori/component.yaml files found — nothing to evaluate")
		status.Phase = "Verified"
		status.Result = string(verification.GateResultPass)
		os.Exit(0)
	}

	logf("found %d app(s) in %s", len(apps), *appsDir)

	// Phase 1.5: two-step verification flow
	//   Step 1 — measure: build sli-summary.json (temporary shim)
	//   Step 2 — evaluate: KubeSlintProvider calls slint-gate --fail-on NEVER,
	//             reads JSON result, applies bori-side FailOn policy,
	//             writes BoriVerificationRun artifact
	provider := verification.NewKubeSlintProvider(*slintGate)
	ctx := context.Background()
	overall := verification.GateResultPass
	var compStatuses []artifact.CompStatus

	for _, app := range apps {
		policyPath := filepath.Join(app.BoriDir, fmt.Sprintf("policy.%s.yaml", *profile))
		if _, err := os.Stat(policyPath); os.IsNotExist(err) {
			logf("skip %s: no policy.%s.yaml", app.Comp.Name, *profile)
			continue
		}

		cs := artifact.CompStatus{
			Name:       app.Comp.Name,
			GateResult: string(verification.GateResultNoGrade),
		}
		evidenceDir := filepath.Join(runDir, "evidence", app.Comp.Name)

		// Step 1: collect metrics
		logf("pre-smoke scrape: %s", app.Comp.Name)
		before, err := collect.ScrapeMetrics(ctx, collect.Target{
			Namespace:   app.Comp.Namespace,
			ServiceName: app.Comp.Name,
			Port:        app.Comp.Port,
			MetricsPath: app.Comp.MetricsPath,
		})
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
		after, err := collect.ScrapeMetrics(ctx, collect.Target{
			Namespace:   app.Comp.Namespace,
			ServiceName: app.Comp.Name,
			Port:        app.Comp.Port,
			MetricsPath: app.Comp.MetricsPath,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: post-smoke scrape: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}
		postAt := time.Now().UTC()

		// Step 1b: build sli-summary.json (measurement shim)
		summaryReq := adapter.RunRequest{
			Profile:    *profile,
			App:        app.Comp.Name,
			PolicyPath: policyPath,
			Before:     adapter.AppSnapshot{App: app.Comp.Name, At: preAt, Values: before},
			After:      adapter.AppSnapshot{App: app.Comp.Name, At: postAt, Values: after},
		}
		summaryPath, err := adapter.BuildMeasurementSummary(summaryReq, evidenceDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: build summary: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}

		// Step 2: evaluate via kube-slint provider (slint-gate --fail-on NEVER)
		result, err := provider.Run(ctx, verification.Request{
			RunID:                  runID,
			App:                    app.Comp.Name,
			PolicyPath:             policyPath,
			MeasurementSummaryPath: summaryPath,
			FailOn:                 verification.FailOnFailOrNoGrade,
			OutDir:                 evidenceDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s: gate: %v\n", app.Comp.Name, err)
			cs.Message = err.Error()
			compStatuses = append(compStatuses, cs)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			continue
		}

		fmt.Printf("[bori] %-20s %s — %s\n", app.Comp.Name, result.GateResult, result.Message)
		cs.GateResult = string(result.GateResult)
		cs.Message = result.Message
		compStatuses = append(compStatuses, cs)
		overall = verification.Max(overall, result.GateResult)
	}

	status.Components = compStatuses
	status.Result = string(overall)
	if overall == verification.GateResultPass || overall == verification.GateResultWarn {
		status.Phase = "Verified"
	} else {
		status.Phase = "Failed"
	}

	fmt.Printf("[bori] overall: %s  run-id: %s\n", overall, runID)

	if verification.IsBlocking(overall, verification.FailOnFailOrNoGrade) {
		os.Exit(1)
	}
}

// cmdStatus reads and prints the run archive status.json for a given run ID.
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	runID := fs.String("run", "", "run ID (required)")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory")
	_ = fs.Parse(args)

	if *runID == "" {
		fmt.Fprintln(os.Stderr, "bori status: --run <run-id> is required")
		os.Exit(1)
	}

	runDir := artifact.RunDir(*boriDir, *runID)
	s, err := artifact.Read(runDir)
	if err != nil {
		fatalf("read status: %v", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
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

// buildAdapterRegistry returns all available deploy adapters keyed by name.
func buildAdapterRegistry(appsDir string) map[string]adapter.DeployAdapter {
	return map[string]adapter.DeployAdapter{
		"devspace":  devspaceadapter.New(appsDir),
		"ko":        koadapter.New(appsDir),
		"kustomize": kustomizeadapter.New(appsDir),
		"shell":     shelladapter.New(appsDir),
	}
}

// compForDeploy loads a BoriComponent from the registry, overriding version/image from the plan.
func compForDeploy(boriRoot, name, version, imageRef string) (model.BoriComponent, error) {
	comp, err := model.LoadComponentByName(boriRoot, name)
	if err != nil {
		return model.BoriComponent{}, err
	}
	if version != "" {
		comp.Version = version
	}
	if imageRef != "" {
		comp.Image.Ref = imageRef
	}
	return comp, nil
}

// envForDeploy loads a BoriEnvironment from the registry.
func envForDeploy(boriRoot, envName string) (model.BoriEnvironment, error) {
	return model.LoadEnvironmentByName(boriRoot, envName)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[bori] "+format+"\n", args...)
	os.Exit(1)
}
