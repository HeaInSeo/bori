// Package controllers implements the bori operator controller loop.
//
// Phase 7 — Limited Operator Apply Mode.
//
// This file is the skeleton for the BoriDataPlane controller.
// It wires the existing pkg/reconcile.Reconciler into a controller-runtime
// reconcile loop without duplicating any deploy/verify logic.
//
// # Adding controller-runtime (Phase 7 implementation step)
//
//	go get sigs.k8s.io/controller-runtime@latest
//
// Then replace the stub types below with the real controller-runtime imports:
//
//	import ctrl "sigs.k8s.io/controller-runtime"
//	import "sigs.k8s.io/controller-runtime/pkg/client"
//
// # Design invariant
//
// The controller MUST NOT reimplement plan/deploy/verify logic.
// All deploy decisions flow through pkg/reconcile.Reconciler.Run().
// The controller only bridges: CR spec → reconcile.Request → CR status patch.
package controllers

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
)

// Request mirrors sigs.k8s.io/controller-runtime/pkg/reconcile.Request.
// Phase 7: replace with the real type once controller-runtime is added.
type Request struct {
	Namespace string
	Name      string
}

// Result mirrors sigs.k8s.io/controller-runtime/pkg/reconcile.Result.
// Phase 7: replace with the real type once controller-runtime is added.
type Result struct {
	// RequeueAfter causes the controller to requeue the object after this duration.
	RequeueAfter time.Duration
}

// DataPlaneReconciler reconciles BoriDataPlane objects.
//
// The reconcile loop is:
//
//  1. Fetch BoriDataPlane from the API server (Phase 7: via client.Get)
//  2. Map spec.release + spec.environment → reconcile.Request
//  3. Call Reconciler.Run() — plan → deploy → verify → promote
//  4. Patch status.conditions from the resulting ShadowState
//  5. Return Result{RequeueAfter: requeueInterval}
//
// Phase 7 TODO: embed controller-runtime client.Client and record.EventRecorder.
type DataPlaneReconciler struct {
	// BoriRoot is the local bori repo root (releases/, components/, environments/).
	BoriRoot string
	// BoriDir is the bori state directory (.bori/ by default).
	BoriDir string
	// AppsDir is the parent directory of app repos (used by deploy adapters).
	AppsDir string
	// Reconciler is the shared plan→deploy→verify→promote engine.
	Reconciler *reconcilepkg.Reconciler
	// RequeueInterval is how often a healthy BoriDataPlane is re-evaluated.
	// Default: 30s.
	RequeueInterval time.Duration
}

// Reconcile processes one BoriDataPlane reconcile event.
//
// Phase 7 implementation steps (in order):
//
//  1. client.Get: fetch the BoriDataPlane object by req.Namespace/req.Name
//  2. Finalizer: add bori.dev/finalizer, handle deletion
//  3. reconcilepkg.Request: map spec → Request{ReleaseName, EnvName, ...}
//  4. Run: call r.Reconciler.Run() — existing logic handles plan/deploy/promote
//  5. Patch: update .status.conditions + .status.currentRevision via client.Status().Patch()
//  6. Event: record a Kubernetes event (Normal/Warning) for the outcome
//  7. Requeue: return Result{RequeueAfter: r.requeueInterval}
func (r *DataPlaneReconciler) Reconcile(ctx context.Context, req Request) (Result, error) {
	interval := r.RequeueInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	// Phase 7 step 1: fetch BoriDataPlane spec from the API server.
	// For now we derive release/env from the resource name.
	// real impl: client.Get(ctx, types.NamespacedName{...}, &bdp)
	releaseName := req.Name
	envName := req.Namespace // Phase 7: derive from spec.environment

	res, err := r.Reconciler.Run(ctx, reconcilepkg.Request{
		BoriRoot:     r.BoriRoot,
		BoriDir:      r.BoriDir,
		AppsDir:      r.AppsDir,
		ReleaseName:  releaseName,
		EnvName:      envName,
		SkipIfInSync: true,
	})
	if err != nil {
		// Phase 7: record Warning event on the BoriDataPlane object.
		return Result{RequeueAfter: interval}, fmt.Errorf("reconcile %s/%s: %w", req.Namespace, req.Name, err)
	}

	// Phase 7 step 5: patch .status from res.ShadowState.
	// real impl: r.Client.Status().Patch(ctx, &bdp, patch)
	_ = buildStatus(res)

	// Phase 7 step 6: record Normal event.
	// real impl: r.Recorder.Event(&bdp, corev1.EventTypeNormal, "Reconciled", ...)

	return Result{RequeueAfter: interval}, nil
}

// buildStatus converts a reconcile result into a BoriDataPlaneStatus for the CR.
// Phase 7: this becomes the patch body sent to client.Status().Patch().
func buildStatus(res *reconcilepkg.Result) v1alpha1.BoriDataPlaneStatus {
	if res == nil || res.ShadowState == nil {
		return v1alpha1.BoriDataPlaneStatus{}
	}
	s := v1alpha1.BoriDataPlaneStatus{
		CurrentRevision: res.ShadowState.ActualRevision,
		ObservedAt:      res.ShadowState.ComputedAt,
		Conditions:      res.ShadowState.Conditions,
		Components:      res.ShadowState.Components,
	}
	return s
}
