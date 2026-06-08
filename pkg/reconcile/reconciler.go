// Package reconcile implements the bori operator reconciliation loop.
//
// It orchestrates the plan → deploy → verify → promote cycle and makes
// decisions based on shadow state (drift detection). This is the prototype
// for what a future bori operator would do in a reconcile loop.
//
// CRD registration and Kubernetes controller-runtime are active from Phase 7.
package reconcile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HeaInSeo/bori/pkg/adapter"
	"github.com/HeaInSeo/bori/pkg/artifact"
	"github.com/HeaInSeo/bori/pkg/model"
	"github.com/HeaInSeo/bori/pkg/planner"
	"github.com/HeaInSeo/bori/pkg/release"
	"github.com/HeaInSeo/bori/pkg/revision"
	"github.com/HeaInSeo/bori/pkg/rollout"
	shadowpkg "github.com/HeaInSeo/bori/pkg/shadow"
)

// ViolationError is returned when the deploy plan contains namespace policy violations.
// Controllers should catch this with errors.As and set a Violation condition on the CR
// instead of requeueing with an error.
type ViolationError struct {
	Violations []string
}

func (e *ViolationError) Error() string {
	return "namespace violations: " + strings.Join(e.Violations, ", ")
}

// Runner is the interface implemented by Reconciler.
// Controllers use this interface to allow mock injection in tests.
type Runner interface {
	Run(ctx context.Context, req Request) (*Result, error)
}

// Request describes one reconciliation pass.
type Request struct {
	BoriRoot    string
	AppsDir     string
	BoriDir     string
	ReleaseName string
	EnvName     string
	// DryRun computes the plan without applying.
	DryRun bool
	// SkipIfInSync skips deploy/verify when shadow state shows all components in-sync.
	SkipIfInSync bool
	// Release overrides filesystem-based LoadReleaseByName when set.
	// The operator injects a BoriRelease fetched from the Kubernetes API;
	// nil falls back to the filesystem (backward-compatible for CLI users).
	Release *model.BoriRelease
}

// Result summarizes one reconciliation pass.
type Result struct {
	RunID         string
	Release       string
	Environment   string
	RevisionID    string
	DriftDetected bool
	// DeployStatus: success | failed | skipped
	DeployStatus string
	// VerifyResult: PASS | FAIL | NO_GRADE | skipped
	VerifyResult string
	Promoted     bool
	ShadowState  *shadowpkg.ShadowState
}

// Reconciler orchestrates the full plan→deploy→verify→promote cycle.
type Reconciler struct {
	AdapterRegistry map[string]adapter.DeployAdapter
	Logf            func(string, ...any)
}

// NewReconciler returns a Reconciler with the standard adapter registry.
func NewReconciler(appsDir string, logf func(string, ...any)) *Reconciler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Reconciler{
		AdapterRegistry: map[string]adapter.DeployAdapter{
			// Adapters are registered at runtime to avoid import cycles with adapters/*.
			// cmd/bori passes them in via buildAdapterRegistry().
		},
		Logf: logf,
	}
}

// Run performs one reconciliation pass:
//  1. Shadow check — detect drift between desired and actual
//  2. Plan — compute deploy order, check compatibility
//  3. Deploy — run each adapter (skipped if in-sync and SkipIfInSync=true)
//  4. Shadow update — write shadow-status.json
func (r *Reconciler) Run(ctx context.Context, req Request) (*Result, error) {
	runID := time.Now().UTC().Format("20060102-150405")
	result := &Result{
		RunID:       runID,
		Release:     req.ReleaseName,
		Environment: req.EnvName,
	}

	abs, err := filepath.Abs(req.BoriRoot)
	if err != nil {
		return nil, fmt.Errorf("abs bori-root: %w", err)
	}

	// Step 1: Load release — use injected release (operator path) or filesystem (CLI path).
	var rel model.BoriRelease
	if req.Release != nil {
		rel = *req.Release
	} else {
		var loadErr error
		rel, loadErr = model.LoadReleaseByName(abs, req.ReleaseName)
		if loadErr != nil {
			return nil, fmt.Errorf("load release: %w", loadErr)
		}
	}

	shadowState, err := shadowpkg.Reconcile(rel, req.BoriDir)
	if err != nil {
		r.Logf("shadow reconcile warning: %v", err)
		// Non-fatal: proceed with full deploy.
	}
	result.ShadowState = shadowState

	if shadowState != nil {
		for _, d := range shadowState.Drift {
			if d.SyncStatus != "in-sync" {
				result.DriftDetected = true
				break
			}
		}
		if shadowState.ActualRevision == "" {
			result.DriftDetected = true
		}
	} else {
		result.DriftDetected = true
	}

	r.Logf("drift detected: %v", result.DriftDetected)

	if !result.DriftDetected && req.SkipIfInSync {
		r.Logf("all components in-sync — skipping deploy")
		result.DeployStatus = "skipped"
		result.VerifyResult = "skipped"
		return result, nil
	}

	// Step 2: Plan (pass injected release to avoid double filesystem read).
	p := planner.New(abs)
	p.Release = req.Release
	plan, err := p.Plan(runID, req.ReleaseName, req.EnvName)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if len(plan.Violations) > 0 {
		return nil, &ViolationError{Violations: plan.Violations}
	}

	runDir := artifact.RunDir(req.BoriDir, runID)
	if err := artifact.WritePlan(runDir, *plan); err != nil {
		r.Logf("warning: could not write plan.json: %v", err)
	}

	// Build and write revision snapshot.
	rev, revErr := revision.BuildFromPlan(*plan, abs)
	if revErr == nil {
		// Add verification policy digests.
		comps, _ := p.LoadComps(rel)
		revision.AddVerificationPolicyDigests(&rev, abs, req.AppsDir, "kind", comps)
		if _, err := revision.Write(req.BoriDir, rev); err != nil {
			r.Logf("warning: could not write revision: %v", err)
			revErr = err
		} else {
			result.RevisionID = rev.RevisionID
		}
		ro := rollout.BuildFromPlan(*plan, rev.RevisionID)
		if _, err := rollout.Write(req.BoriDir, ro); err != nil {
			r.Logf("warning: could not write rollout: %v", err)
		}
	}

	if req.DryRun {
		r.Logf("dry-run: skipping deploy")
		result.DeployStatus = "skipped (dry-run)"
		result.VerifyResult = "skipped (dry-run)"
		return result, nil
	}

	// Step 3: Deploy.
	// Load environment once — it is constant across all components.
	env, err := model.LoadEnvironmentByName(abs, req.EnvName)
	if err != nil {
		return nil, fmt.Errorf("load environment %q: %w", req.EnvName, err)
	}

	deployOK := true
	deployStartedAt := time.Now().UTC()
	var compDeploys []artifact.CompDeploy
	var failedReasons []string
	for _, cp := range plan.Components {
		if cp.Action == "violation" {
			continue
		}
		a, ok := r.AdapterRegistry[cp.Adapter]
		if !ok {
			r.Logf("unknown adapter %q for %s — skipping", cp.Adapter, cp.Name)
			deployOK = false
			failedReasons = append(failedReasons, fmt.Sprintf("%s: unknown adapter %q", cp.Name, cp.Adapter))
			continue
		}
		comp, err := model.LoadComponentByName(abs, cp.Name)
		if err != nil {
			r.Logf("load component %s: %v", cp.Name, err)
			deployOK = false
			failedReasons = append(failedReasons, fmt.Sprintf("%s: load component: %v", cp.Name, err))
			continue
		}
		// Apply plan overrides: imageRef from planner (may be digest-qualified).
		if cp.Version != "" {
			comp.Version = cp.Version
		}
		if cp.ImageRef != "" {
			comp.Image.Ref = cp.ImageRef
		}
		deployResult, err := a.Deploy(ctx, adapter.DeployRequest{
			Component:   comp,
			Environment: env,
			OutDir:      filepath.Join(runDir, "deploy", cp.Name),
		})
		cd := artifact.CompDeploy{Name: cp.Name, Version: cp.Version, Adapter: cp.Adapter}
		if err != nil || (deployResult != nil && !deployResult.Success) {
			deployOK = false
			msg := fmt.Sprintf("%v", err)
			if deployResult != nil {
				msg = deployResult.Message
			}
			cd.Message = msg
			failedReasons = append(failedReasons, fmt.Sprintf("%s: %s", cp.Name, msg))
			r.Logf("%s: deploy failed: %s", cp.Name, msg)
		} else {
			cd.Success = true
			if deployResult != nil {
				cd.Message = deployResult.Message
			}
			r.Logf("%s: deployed", cp.Name)
		}
		compDeploys = append(compDeploys, cd)
	}

	if deployOK {
		result.DeployStatus = "success"
	} else {
		result.DeployStatus = "failed"
	}

	// Write deploy-result.json.
	dr := artifact.DeployResult{
		SchemaVersion: "bori.deployResult.v1",
		RunID:         runID,
		Release:       req.ReleaseName,
		Environment:   req.EnvName,
		StartedAt:     deployStartedAt,
		FinishedAt:    time.Now().UTC(),
		Overall:       result.DeployStatus,
		Components:    compDeploys,
	}
	if err := artifact.WriteDeployResult(runDir, dr); err != nil {
		r.Logf("warning: could not write deploy-result.json: %v", err)
	}

	// Step 4: Record revision outcome — promoted on success, failed on deploy error.
	if revErr == nil {
		if deployOK {
			baselineRef := filepath.Join(runDir, "evidence")
			revision.Promote(&rev, baselineRef)
			rev.VerificationRunID = runID
			if _, err := revision.Write(req.BoriDir, rev); err != nil {
				r.Logf("warning: could not update promoted revision: %v", err)
			} else {
				result.Promoted = true
				r.Logf("revision promoted: %s", rev.RevisionID)
			}
		} else {
			reason := "deploy failed: " + strings.Join(failedReasons, "; ")
			revision.Fail(&rev, reason)
			if _, err := revision.Write(req.BoriDir, rev); err != nil {
				r.Logf("warning: could not update failed revision: %v", err)
			}
		}
	}

	// Step 5: Update shadow state.
	newShadow, err := shadowpkg.Reconcile(rel, req.BoriDir)
	if err == nil {
		result.ShadowState = newShadow
		if err := shadowpkg.WriteState(req.BoriDir, req.ReleaseName, *newShadow); err != nil {
			r.Logf("warning: could not write shadow-status.json: %v", err)
		}
	}

	return result, nil
}

// RollbackPlan builds a deploy plan using versions from a previous revision.
// The returned plan can be passed to a Reconciler.Run (without SkipIfInSync).
func RollbackPlan(boriRoot, boriDir, releaseName, envName, targetRevisionID string) (*artifact.Plan, *revision.BoriRevision, error) {
	abs, err := filepath.Abs(boriRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("abs: %w", err)
	}

	rev, err := revision.Read(boriDir, targetRevisionID)
	if err != nil {
		return nil, nil, fmt.Errorf("read revision %q: %w", targetRevisionID, err)
	}

	// Check compatibility of the rollback versions.
	rel, err := model.LoadReleaseByName(abs, releaseName)
	if err != nil {
		return nil, nil, fmt.Errorf("load release: %w", err)
	}

	// Build a rollback release (same as the target revision's versions).
	rollbackRel := rel
	rollbackRel.Components = nil
	for _, cr := range rev.Components {
		rollbackRel.Components = append(rollbackRel.Components, model.ComponentRef{
			Name:    cr.Name,
			Version: cr.Version,
		})
	}

	// Validate compatibility of rollback versions.
	matrix, _ := release.LoadMatrixForRelease(abs, rollbackRel)
	violations := release.CheckCompatibility(rollbackRel, matrix)
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "[bori rollback] warning: %s\n", v)
		}
	}

	// Build a plan using the rollback component versions.
	runID := time.Now().UTC().Format("20060102-150405")
	plan := &artifact.Plan{
		SchemaVersion: "bori.plan.v1",
		RunID:         runID,
		Release:       releaseName,
		Environment:   envName,
		CreatedAt:     time.Now().UTC(),
	}

	env, err := model.LoadEnvironmentByName(abs, envName)
	if err != nil {
		return nil, nil, fmt.Errorf("load environment: %w", err)
	}
	allowedNS := make(map[string]bool, len(env.NamespacePolicy.Allowed))
	for _, ns := range env.NamespacePolicy.Allowed {
		allowedNS[ns] = true
	}

	for _, cr := range rev.Components {
		comp, err := model.LoadComponentByName(abs, cr.Name)
		if err != nil {
			continue
		}
		ns := comp.Deploy.Namespace
		if ns == "" {
			ns = cr.Name + "-system"
		}
		adapterName := comp.Deploy.Adapter
		if adapterName == "" {
			adapterName = "devspace"
		}
		cp := artifact.ComponentPlan{
			Name:        cr.Name,
			Version:     cr.Version,
			Adapter:     adapterName,
			Namespace:   ns,
			ImageRef:    cr.ImageRef,
			ImageDigest: cr.ImageDigest,
			GitSha:      cr.GitSha,
			Action:      "deploy",
		}
		if !allowedNS[ns] {
			cp.Action = "violation"
			cp.Message = fmt.Sprintf("namespace %q not allowed", ns)
			plan.Violations = append(plan.Violations, fmt.Sprintf("%s: %s", cr.Name, cp.Message))
		}
		plan.Components = append(plan.Components, cp)
	}

	return plan, &rev, nil
}
