// bori is the unified deploy/verify gateway for genomic dataplane apps.
//
// Usage:
//
//	bori plan       --release <name> --env <name> [--bori-root <dir>]
//	bori deploy     --release <name> --env <name> [--bori-root <dir>] [--apps-dir <dir>]
//	bori verify     [--release <name> --env <name>] [--changed <a,b>] [--bori-root <dir>]
//	bori status     --run <run-id> [--bori-dir <dir>]
//	bori revision   list [--release <name>] [--bori-dir <dir>]
//	bori rollout    plan --release <name> --env <name> [--bori-root <dir>]
//	bori shadow     status --release <name> [--bori-root <dir>] [--bori-dir <dir>]
//	bori reconcile  --release <name> --env <name> [--bori-root <dir>] [--dry-run] [--skip-if-in-sync]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
	relpkg "github.com/HeaInSeo/bori/pkg/release"
	"github.com/HeaInSeo/bori/pkg/revision"
	"github.com/HeaInSeo/bori/pkg/rollout"
	"github.com/HeaInSeo/bori/pkg/security"
	shadowpkg "github.com/HeaInSeo/bori/pkg/shadow"
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
	case "revision":
		cmdRevision(os.Args[2:])
	case "rollout":
		cmdRollout(os.Args[2:])
	case "shadow":
		cmdShadow(os.Args[2:])
	case "reconcile":
		cmdReconcile(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "bori: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `bori — genomic dataplane control plane gateway

Usage:
  bori plan           --release <name> --env <name> [--bori-root <dir>]
  bori deploy         --release <name> --env <name> [--bori-root <dir>] [--apps-dir <dir>]
  bori verify         [--release <name> --env <name>] [--changed <a,b>] [--bori-root <dir>]
  bori status         --run <run-id> [--bori-dir <dir>]
  bori revision list  [--release <name>] [--bori-dir <dir>]
  bori rollout plan   --release <name> --env <name> [--bori-root <dir>]
  bori shadow status  --release <name> [--bori-root <dir>] [--bori-dir <dir>] [--json]
  bori reconcile      --release <name> --env <name> [--bori-root <dir>] [--apps-dir <dir>]
                      [--dry-run] [--skip-if-in-sync] [-v]`)
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

	// Build revision snapshot (pending) and rollout plan from the deploy plan.
	rev, err := revision.BuildFromPlan(*plan, *boriRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not build revision: %v\n", err)
	} else {
		if _, err := revision.Write(*boriDir, rev); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write revision: %v\n", err)
		}
		ro := rollout.BuildFromPlan(*plan, rev.RevisionID)
		if _, err := rollout.Write(*boriDir, ro); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write rollout: %v\n", err)
		}
		fmt.Printf("[bori] revision: %s  content-hash: %s\n", rev.RevisionID, rev.ContentHash)
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

	abs, err := filepath.Abs(*boriRoot)
	if err != nil {
		fatalf("abs bori-root: %v", err)
	}

	p := planner.New(abs)
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

	// Create a pending revision snapshot before deploying.
	rev, revErr := revision.BuildFromPlan(*plan, abs)
	if revErr == nil {
		if _, err := revision.Write(*boriDir, rev); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write revision: %v\n", err)
			revErr = err
		} else {
			fmt.Printf("[bori] revision: %s  content-hash: %s\n", rev.RevisionID, rev.ContentHash)
		}
		ro := rollout.BuildFromPlan(*plan, rev.RevisionID)
		if _, err := rollout.Write(*boriDir, ro); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write rollout: %v\n", err)
		}
	}

	adapters := buildAdapterRegistry(*appsDir)
	ctx := context.Background()
	overall := verification.GateResultPass
	var compStatuses []artifact.CompStatus
	var compDeploys []artifact.CompDeploy

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

		deployResult, err := a.Deploy(ctx, adapter.DeployRequest{
			Component:   comp,
			Environment: env,
			DryRun:      *dryRun,
			OutDir:      filepath.Join(runDir, "deploy", cp.Name),
		})
		cs := artifact.CompStatus{Name: cp.Name}
		cd := artifact.CompDeploy{Name: cp.Name, Version: cp.Version, Adapter: cp.Adapter}
		if err != nil || (deployResult != nil && !deployResult.Success) {
			msg := ""
			if err != nil {
				msg = security.RedactString(err.Error())
			} else {
				msg = deployResult.Message
			}
			fmt.Fprintf(os.Stderr, "[bori] %s: deploy failed: %s\n", cp.Name, msg)
			cs.GateResult = string(verification.GateResultFail)
			cs.Message = msg
			cd.Success, cd.Message = false, msg
			overall = verification.Max(overall, verification.GateResultFail)
		} else {
			fmt.Printf("[bori] %-24s deployed  (%s)\n", cp.Name, deployResult.Message)
			cs.GateResult = string(verification.GateResultPass)
			cs.Message = deployResult.Message
			cd.Success, cd.Message = true, deployResult.Message
		}
		compStatuses = append(compStatuses, cs)
		compDeploys = append(compDeploys, cd)
	}

	// Write deploy-result.json to run archive.
	deployOverall := "success"
	if overall == verification.GateResultFail {
		deployOverall = "failed"
	} else if len(compDeploys) > 0 {
		for _, cd := range compDeploys {
			if !cd.Success {
				deployOverall = "partial"
				break
			}
		}
	}
	dr := artifact.DeployResult{
		SchemaVersion: "bori.deployResult.v1",
		RunID:         runID,
		Release:       *releaseName,
		Environment:   *envName,
		StartedAt:     startedAt,
		FinishedAt:    time.Now().UTC(),
		Overall:       deployOverall,
		Components:    compDeploys,
	}
	if err := artifact.WriteDeployResult(runDir, dr); err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not write deploy-result.json: %v\n", err)
	}

	// On successful deploy, promote the revision and record baseline provenance.
	if overall == verification.GateResultPass && revErr == nil {
		baselineRef := fmt.Sprintf("%s/evidence", runDir)
		revision.Promote(&rev, baselineRef)
		rev.VerificationRunID = runID
		if _, err := revision.Write(*boriDir, rev); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not update promoted revision: %v\n", err)
		} else {
			fmt.Printf("[bori] revision promoted: %s\n", rev.RevisionID)
		}
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

// resolvedPolicy is a fully resolved verification policy for one component.
type resolvedPolicy struct {
	Name       string
	PolicyPath string
	FailOn     verification.FailOn
	// Blocking: if true and the gate is FAIL/NO_GRADE, bori halts immediately.
	Blocking bool
}

// verifyTarget carries everything the verification loop needs for one component.
// Each component may have multiple policies; the measurement (sli-summary.json)
// is built once and evaluated against all policies.
type verifyTarget struct {
	Name        string
	Namespace   string
	ServiceName string
	Port        int
	MetricsPath string
	Policies    []resolvedPolicy
}

// cmdVerify runs verification for a release (--release/--env) or
// via app-directory discovery (legacy --apps-dir mode).
func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	// Model-based mode (Phase 3+)
	releaseName := fs.String("release", "", "release name — enables model-based verify")
	envName := fs.String("env", "", "environment name (required with --release)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root (used with --release)")
	// Phase 4: incremental — only verify components affected by these changes
	changedFlag := fs.String("changed", "", "comma-separated list of changed component names (incremental verify)")
	// Legacy mode
	appsDir := fs.String("apps-dir", "", "directory containing app repos (legacy, default: parent of cwd)")
	// Common
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

	runID := time.Now().UTC().Format("20060102-150405")
	runDir := artifact.RunDir(*boriDir, runID)
	startedAt := time.Now().UTC()

	status := artifact.Status{
		SchemaVersion: "bori.run.v1",
		RunID:         runID,
		Release:       *releaseName,
		Environment:   *envName,
		Profile:       *profile,
		StartedAt:     startedAt,
		Phase:         "Failed",
		Result:        string(verification.GateResultNoGrade),
	}
	defer func() {
		status.FinishedAt = time.Now().UTC()
		if err := artifact.Write(runDir, status); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write status.json: %v\n", err)
		} else {
			logf("run archive: %s/status.json", runDir)
		}
	}()

	var targets []verifyTarget

	var releaseComps map[string]model.BoriComponent // populated in model-based mode
	var orderedPlanComps []artifact.ComponentPlan   // plan components in dep order

	if *releaseName != "" {
		// --- Phase 3+4: model-based verification ---
		if *envName == "" {
			fatalf("verify: --env is required with --release")
		}
		abs, err := filepath.Abs(*boriRoot)
		if err != nil {
			fatalf("abs bori-root: %v", err)
		}
		if *appsDir == "" {
			*appsDir = filepath.Join(abs, "..")
		}
		p := planner.New(abs)
		plan, err := p.Plan(runID, *releaseName, *envName)
		if err != nil {
			fatalf("plan: %v", err)
		}
		orderedPlanComps = plan.Components

		// Load components for AffectedComponents calculation.
		rel, err := model.LoadReleaseByName(abs, *releaseName)
		if err != nil {
			fatalf("load release: %v", err)
		}
		releaseComps, err = planner.New(abs).LoadComps(rel)
		if err != nil {
			fatalf("load components: %v", err)
		}

		// Phase 4: compute affected set when --changed is given.
		var affectedSet map[string]bool
		if *changedFlag != "" {
			changed := strings.Split(*changedFlag, ",")
			var allRefs []model.ComponentRef
			for _, cp := range plan.Components {
				allRefs = append(allRefs, model.ComponentRef{Name: cp.Name, Version: cp.Version})
			}
			affected := relpkg.AffectedComponents(changed, allRefs, releaseComps)
			affectedSet = make(map[string]bool, len(affected))
			for _, n := range affected {
				affectedSet[n] = true
			}
			fmt.Printf("[bori] incremental verify — changed: %s → affected: %s\n",
				strings.Join(changed, ", "), strings.Join(affected, ", "))
		}

		for _, cp := range plan.Components {
			if cp.Action == "violation" {
				logf("skip %s: namespace violation", cp.Name)
				continue
			}
			// Incremental mode: skip components not in the affected set.
			if affectedSet != nil && !affectedSet[cp.Name] {
				logf("skip %s: not affected by --changed", cp.Name)
				continue
			}
			comp, err := model.LoadComponentByName(abs, cp.Name)
			if err != nil {
				logf("skip %s: load component: %v", cp.Name, err)
				continue
			}
			policies := resolvePolicies(abs, *appsDir, comp, *profile, logf)
			if len(policies) == 0 {
				logf("skip %s: no verification policies resolved", cp.Name)
				continue
			}
			targets = append(targets, verifyTarget{
				Name:        comp.Name,
				Namespace:   cp.Namespace,
				ServiceName: comp.Name,
				Port:        comp.Ports.Metrics,
				MetricsPath: comp.Metrics.Path,
				Policies:    policies,
			})
		}
	} else {
		// --- Legacy: app-discovery mode ---
		if *appsDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fatalf("getwd: %v", err)
			}
			*appsDir = filepath.Join(cwd, "..")
		}
		apps, err := component.Discover(*appsDir)
		if err != nil {
			fatalf("discover apps: %v", err)
		}
		if len(apps) == 0 {
			fmt.Fprintln(os.Stderr, "[bori] no .bori/component.yaml files found — nothing to evaluate")
			status.Phase = "Verified"
			status.Result = string(verification.GateResultPass)
			return
		}
		logf("found %d app(s) in %s", len(apps), *appsDir)
		for _, app := range apps {
			policyPath := filepath.Join(app.BoriDir, fmt.Sprintf("policy.%s.yaml", *profile))
			if _, err := os.Stat(policyPath); os.IsNotExist(err) {
				logf("skip %s: no policy.%s.yaml", app.Comp.Name, *profile)
				continue
			}
			targets = append(targets, verifyTarget{
				Name:        app.Comp.Name,
				Namespace:   app.Comp.Namespace,
				ServiceName: app.Comp.Name,
				Port:        app.Comp.Port,
				MetricsPath: app.Comp.MetricsPath,
				Policies: []resolvedPolicy{{
					Name:       "smoke",
					PolicyPath: policyPath,
					FailOn:     verification.FailOnFailOrNoGrade,
					Blocking:   false,
				}},
			})
		}
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "[bori] no verification targets — nothing to evaluate")
		status.Phase = "Verified"
		status.Result = string(verification.GateResultPass)
		return
	}

	// --- Verification loop (shared by both modes) ---
	provider := verification.NewKubeSlintProvider(*slintGate)
	ctx := context.Background()
	overall := verification.GateResultPass
	var compStatuses []artifact.CompStatus

	halted := false
	for _, t := range targets {
		cs, gr, blocked := runOneVerification(ctx, t, runID, runDir, *profile, *smokeCmd, *smokeWait, provider, logf)
		compStatuses = append(compStatuses, cs)
		overall = verification.Max(overall, gr)
		if blocked {
			fmt.Fprintf(os.Stderr, "[bori] blocking gate failed for %s — halting verification\n", t.Name)
			halted = true
			break
		}
	}

	status.Components = compStatuses
	status.Result = string(overall)
	if overall == verification.GateResultPass || overall == verification.GateResultWarn {
		status.Phase = "Verified"
	} else {
		status.Phase = "Failed"
	}

	// Write release-result.json for model-based runs.
	if *releaseName != "" && len(orderedPlanComps) > 0 {
		verifiedSet := make(map[string]artifact.CompStatus, len(compStatuses))
		for _, cs := range compStatuses {
			verifiedSet[cs.Name] = cs
		}
		var compGates []artifact.CompReleaseGate
		for _, cp := range orderedPlanComps {
			cg := artifact.CompReleaseGate{
				Name:    cp.Name,
				Version: cp.Version,
			}
			if cs, ok := verifiedSet[cp.Name]; ok {
				cg.GateResult = cs.GateResult
				cg.Affected = true
			} else {
				cg.GateResult = string(verification.GateResultPass)
				cg.Skipped = true
			}
			compGates = append(compGates, cg)
		}
		rr := artifact.ReleaseResult{
			SchemaVersion: "bori.releaseResult.v1",
			RunID:         runID,
			Release:       *releaseName,
			Environment:   *envName,
			CreatedAt:     time.Now().UTC(),
			GateResult:    string(overall),
			Components:    compGates,
		}
		if err := artifact.WriteReleaseResult(runDir, rr); err != nil {
			fmt.Fprintf(os.Stderr, "[bori] warning: could not write release-result.json: %v\n", err)
		}
	}

	fmt.Printf("[bori] overall: %s  run-id: %s", overall, runID)
	if halted {
		fmt.Println("  (halted: blocking gate)")
	} else {
		fmt.Println()
	}
	if verification.IsBlocking(overall, verification.FailOnFailOrNoGrade) || halted {
		os.Exit(1)
	}
}

// runOneVerification executes the scrape→summary→gate flow for one target.
// The sli-summary.json is built once and evaluated against ALL policies in t.Policies.
// Returns (CompStatus, worstGateResult, blockedByHardGate).
func runOneVerification(
	ctx context.Context,
	t verifyTarget,
	runID, runDir, profile, smokeCmd string,
	smokeWait time.Duration,
	provider verification.Provider,
	logf func(string, ...any),
) (artifact.CompStatus, verification.GateResult, bool) {
	cs := artifact.CompStatus{Name: t.Name, GateResult: string(verification.GateResultNoGrade)}
	evidenceDir := filepath.Join(runDir, "evidence", t.Name)

	// Step 1: collect metrics before smoke
	logf("pre-smoke scrape: %s", t.Name)
	before, err := collect.ScrapeMetrics(ctx, collect.Target{
		Namespace:   t.Namespace,
		ServiceName: t.ServiceName,
		Port:        t.Port,
		MetricsPath: t.MetricsPath,
	})
	if err != nil {
		msg := security.RedactString(err.Error())
		fmt.Fprintf(os.Stderr, "[bori] %s: pre-smoke scrape: %v\n", t.Name, msg)
		cs.Message = msg
		return cs, verification.GateResultNoGrade, false
	}
	preAt := time.Now().UTC()

	if err := runSmoke(ctx, smokeCmd, smokeWait, t.Name, logf); err != nil {
		msg := security.RedactString(err.Error())
		fmt.Fprintf(os.Stderr, "[bori] %s: smoke: %v\n", t.Name, msg)
		cs.Message = msg
		return cs, verification.GateResultFail, false
	}

	// Step 2: collect metrics after smoke
	logf("post-smoke scrape: %s", t.Name)
	after, err := collect.ScrapeMetrics(ctx, collect.Target{
		Namespace:   t.Namespace,
		ServiceName: t.ServiceName,
		Port:        t.Port,
		MetricsPath: t.MetricsPath,
	})
	if err != nil {
		msg := security.RedactString(err.Error())
		fmt.Fprintf(os.Stderr, "[bori] %s: post-smoke scrape: %v\n", t.Name, msg)
		cs.Message = msg
		return cs, verification.GateResultNoGrade, false
	}
	postAt := time.Now().UTC()

	// Step 3: build ONE sli-summary.json shared by all policies
	summaryReq := adapter.RunRequest{
		Profile: profile,
		App:     t.Name,
		Before:  adapter.AppSnapshot{App: t.Name, At: preAt, Values: before},
		After:   adapter.AppSnapshot{App: t.Name, At: postAt, Values: after},
	}
	summaryPath, err := adapter.BuildMeasurementSummary(summaryReq, evidenceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[bori] %s: build summary: %v\n", t.Name, err)
		cs.Message = err.Error()
		return cs, verification.GateResultNoGrade, false
	}

	// Step 4: evaluate each policy against the same summary
	overall := verification.GateResultPass
	for _, pol := range t.Policies {
		if _, statErr := os.Stat(pol.PolicyPath); os.IsNotExist(statErr) {
			logf("skip policy %q for %s: file not found: %s", pol.Name, t.Name, pol.PolicyPath)
			// Missing churn policy file is not fatal in dev; mark as NO_GRADE.
			overall = verification.Max(overall, verification.GateResultNoGrade)
			if pol.Blocking {
				cs.GateResult = string(overall)
				cs.Message = fmt.Sprintf("blocking policy %q: policy file not found", pol.Name)
				return cs, overall, true
			}
			continue
		}

		polOutDir := filepath.Join(evidenceDir, pol.Name)
		result, err := provider.Run(ctx, verification.Request{
			RunID:                  runID,
			App:                    t.Name,
			PolicyPath:             pol.PolicyPath,
			MeasurementSummaryPath: summaryPath,
			FailOn:                 pol.FailOn,
			OutDir:                 polOutDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bori] %s/%s: gate error: %v\n", t.Name, pol.Name, err)
			overall = verification.Max(overall, verification.GateResultNoGrade)
			if pol.Blocking {
				cs.GateResult = string(overall)
				cs.Message = fmt.Sprintf("blocking policy %q: gate error: %v", pol.Name, err)
				return cs, overall, true
			}
			continue
		}

		fmt.Printf("[bori] %-24s %-32s %s\n", t.Name, pol.Name, result.GateResult)
		overall = verification.Max(overall, result.GateResult)

		// Check blocking: if this policy is blocking and the result is bad, halt.
		if pol.Blocking && verification.IsBlocking(result.GateResult, pol.FailOn) {
			cs.GateResult = string(overall)
			cs.Message = fmt.Sprintf("blocking policy %q: %s — %s", pol.Name, result.GateResult, result.Message)
			return cs, overall, true
		}
	}

	cs.GateResult = string(overall)
	return cs, overall, false
}

// resolvePolicies returns all resolved verification policies for a component.
// Each BoriVerificationPolicy in comp.VerificationPolicies becomes one resolvedPolicy.
// Missing policy files in the bori registry fall back to the app-local convention.
func resolvePolicies(boriRoot, appsDir string, comp model.BoriComponent, profile string, logf func(string, ...any)) []resolvedPolicy {
	appDir := filepath.Join(appsDir, comp.Name)
	var policies []resolvedPolicy

	for _, polName := range comp.VerificationPolicies {
		pol, err := model.LoadPolicyByName(boriRoot, polName)
		if err != nil {
			logf("policy %q not in registry, falling back to app-local convention: %v", polName, err)
			// Fallback: resolve directly from app repo (.bori/policy.{profile}.yaml)
			policies = append(policies, resolvedPolicy{
				Name:       polName,
				PolicyPath: filepath.Join(appDir, ".bori", fmt.Sprintf("policy.%s.yaml", profile)),
				FailOn:     verification.FailOnFailOrNoGrade,
				Blocking:   false,
			})
			continue
		}
		failOn := verification.FailOn(pol.FailOn)
		if failOn == "" {
			failOn = verification.FailOnFailOrNoGrade
		}
		policies = append(policies, resolvedPolicy{
			Name:       pol.Name,
			PolicyPath: pol.ResolvePolicyPath(appDir, profile),
			FailOn:     failOn,
			Blocking:   pol.Blocking,
		})
	}

	return policies
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

// cmdRevision dispatches revision sub-commands.
func cmdRevision(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bori revision list [--release <name>] [--bori-dir <dir>]")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		cmdRevisionList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "bori revision: unknown sub-command %q\n", args[0])
		os.Exit(1)
	}
}

// cmdRevisionList prints stored revisions, newest first.
func cmdRevisionList(args []string) {
	fs := flag.NewFlagSet("revision list", flag.ExitOnError)
	filterRelease := fs.String("release", "", "filter by release name")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory")
	_ = fs.Parse(args)

	revs, err := revision.List(*boriDir)
	if err != nil {
		fatalf("list revisions: %v", err)
	}
	if len(revs) == 0 {
		fmt.Println("[bori] no revisions found")
		return
	}

	fmt.Printf("%-52s  %-18s  %-11s  %-16s  %s\n",
		"REVISION ID", "RELEASE", "STATUS", "CONTENT HASH", "CREATED AT")
	fmt.Println(strings.Repeat("-", 120))
	for _, rev := range revs {
		if *filterRelease != "" && rev.Release != *filterRelease {
			continue
		}
		fmt.Printf("%-52s  %-18s  %-11s  %-16s  %s\n",
			rev.RevisionID, rev.Release, rev.PromotionStatus,
			rev.ContentHash, rev.CreatedAt.Format("2006-01-02 15:04:05"))
	}
}

// cmdRollout dispatches rollout sub-commands.
func cmdRollout(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bori rollout plan --release <name> --env <name>")
		os.Exit(1)
	}
	switch args[0] {
	case "plan":
		cmdRolloutPlan(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "bori rollout: unknown sub-command %q\n", args[0])
		os.Exit(1)
	}
}

// cmdRolloutPlan generates a dry-run rollout plan without applying anything.
func cmdRolloutPlan(args []string) {
	fs := flag.NewFlagSet("rollout plan", flag.ExitOnError)
	releaseName := fs.String("release", "", "release name (required)")
	envName := fs.String("env", "", "environment name (required)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory")
	_ = fs.Parse(args)

	if *releaseName == "" || *envName == "" {
		fmt.Fprintln(os.Stderr, "bori rollout plan: --release and --env are required")
		os.Exit(1)
	}

	abs, err := filepath.Abs(*boriRoot)
	if err != nil {
		fatalf("abs bori-root: %v", err)
	}

	runID := time.Now().UTC().Format("20060102-150405")
	p := planner.New(abs)
	plan, err := p.Plan(runID, *releaseName, *envName)
	if err != nil {
		fatalf("plan: %v", err)
	}

	rev, err := revision.BuildFromPlan(*plan, abs)
	if err != nil {
		fatalf("build revision: %v", err)
	}
	ro := rollout.BuildFromPlan(*plan, rev.RevisionID)

	if _, err := rollout.Write(*boriDir, ro); err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not write rollout: %v\n", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ro)

	fmt.Printf("[bori] rollout plan: %s  revision: %s\n", ro.RolloutID, rev.RevisionID)
}

// cmdShadow dispatches shadow mode sub-commands.
func cmdShadow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bori shadow status --release <name> [--bori-root <dir>]")
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		cmdShadowStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "bori shadow: unknown sub-command %q\n", args[0])
		os.Exit(1)
	}
}

// cmdShadowStatus reconciles desired vs actual state for a release and prints
// a status report — without applying anything to the cluster.
func cmdShadowStatus(args []string) {
	fs := flag.NewFlagSet("shadow status", flag.ExitOnError)
	releaseName := fs.String("release", "", "release name (required)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory")
	outputJSON := fs.Bool("json", false, "output raw JSON instead of human-readable summary")
	_ = fs.Parse(args)

	if *releaseName == "" {
		fmt.Fprintln(os.Stderr, "bori shadow status: --release is required")
		os.Exit(1)
	}

	abs, err := filepath.Abs(*boriRoot)
	if err != nil {
		fatalf("abs bori-root: %v", err)
	}

	rel, err := model.LoadReleaseByName(abs, *releaseName)
	if err != nil {
		fatalf("load release %q: %v", *releaseName, err)
	}

	state, err := shadowpkg.Reconcile(rel, *boriDir)
	if err != nil {
		fatalf("shadow reconcile: %v", err)
	}

	if err := shadowpkg.WriteState(*boriDir, *releaseName, *state); err != nil {
		fmt.Fprintf(os.Stderr, "[bori] warning: could not persist shadow state: %v\n", err)
	}

	if *outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(state)
		return
	}

	// Human-readable summary.
	fmt.Printf("[bori shadow] release: %s\n", state.Release)
	fmt.Printf("  computed at:      %s\n", state.ComputedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  actual revision:  %s\n", orNone(state.ActualRevision))
	fmt.Println()
	fmt.Println("  Conditions:")
	for _, c := range state.Conditions {
		icon := "  "
		switch c.Status {
		case "True":
			icon = "✓ "
		case "False":
			icon = "✗ "
		default:
			icon = "? "
		}
		fmt.Printf("    %s%-12s  %s\n", icon, c.Type, c.Message)
	}
	fmt.Println()
	fmt.Println("  Drift:")
	for _, d := range state.Drift {
		icon := "="
		if d.SyncStatus == "out-of-sync" {
			icon = "≠"
		} else if d.SyncStatus == "unknown" {
			icon = "?"
		}
		fmt.Printf("    %s  %-24s  desired: %-10s  actual: %s\n",
			icon, d.Component, d.DesiredVersion, orNone(d.ActualVersion))
	}

	// Exit 1 if any component is out-of-sync or uninstalled.
	degraded := false
	for _, c := range state.Conditions {
		if c.Type == "Degraded" && c.Status == "True" {
			degraded = true
		}
		if c.Type == "Installed" && c.Status == "False" {
			degraded = true
		}
	}
	if degraded {
		os.Exit(1)
	}
}

// cmdReconcile runs one shadow-mode reconcile pass: drift detection → plan →
// deploy (unless dry-run or in-sync) → promote → shadow state update.
// This is the CLI prototype for what a future bori operator reconcile loop would do.
func cmdReconcile(args []string) {
	fs := flag.NewFlagSet("reconcile", flag.ExitOnError)
	releaseName := fs.String("release", "", "release name (required)")
	envName := fs.String("env", "", "environment name (required)")
	boriRoot := fs.String("bori-root", ".", "path to bori repo root")
	appsDir := fs.String("apps-dir", "", "directory containing app repos (default: parent of bori-root)")
	boriDir := fs.String("bori-dir", ".bori", "local .bori directory for run archives")
	dryRun := fs.Bool("dry-run", false, "compute plan and drift without applying")
	skipIfInSync := fs.Bool("skip-if-in-sync", false, "skip deploy when all components are in-sync")
	verbose := fs.Bool("v", false, "verbose output")
	_ = fs.Parse(args)

	if *releaseName == "" || *envName == "" {
		fmt.Fprintln(os.Stderr, "bori reconcile: --release and --env are required")
		os.Exit(1)
	}
	if *appsDir == "" {
		abs, err := filepath.Abs(*boriRoot)
		if err != nil {
			fatalf("abs bori-root: %v", err)
		}
		*appsDir = filepath.Join(abs, "..")
	}

	logf := func(string, ...any) {}
	if *verbose {
		logf = func(f string, a ...any) { fmt.Printf("[bori reconcile] "+f+"\n", a...) }
	}

	r := reconcilepkg.NewReconciler(*appsDir, logf)
	r.AdapterRegistry = buildAdapterRegistry(*appsDir)

	res, err := r.Run(context.Background(), reconcilepkg.Request{
		BoriRoot:     *boriRoot,
		AppsDir:      *appsDir,
		BoriDir:      *boriDir,
		ReleaseName:  *releaseName,
		EnvName:      *envName,
		DryRun:       *dryRun,
		SkipIfInSync: *skipIfInSync,
	})
	if err != nil {
		fatalf("reconcile: %v", err)
	}

	fmt.Printf("[bori reconcile] release: %s  env: %s  run: %s\n", res.Release, res.Environment, res.RunID)
	fmt.Printf("  drift:    %v\n", res.DriftDetected)
	fmt.Printf("  deploy:   %s\n", res.DeployStatus)
	fmt.Printf("  promoted: %v\n", res.Promoted)
	if res.RevisionID != "" {
		fmt.Printf("  revision: %s\n", res.RevisionID)
	}
	if res.ShadowState != nil {
		fmt.Println("  conditions:")
		for _, c := range res.ShadowState.Conditions {
			icon := "? "
			switch c.Status {
			case "True":
				icon = "✓ "
			case "False":
				icon = "✗ "
			}
			fmt.Printf("    %s%-12s  %s\n", icon, c.Type, c.Message)
		}
	}

	if res.DeployStatus == "failed" {
		os.Exit(1)
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[bori] "+format+"\n", args...)
	os.Exit(1)
}
