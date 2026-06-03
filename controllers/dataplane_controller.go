// Package controllers implements the bori operator controller loop.
//
// Phase 7 — Limited Operator Apply Mode.
//
// DataPlaneReconciler bridges BoriDataPlane CRs to the existing
// pkg/reconcile.Reconciler engine. The controller:
//  1. Fetches a BoriDataPlane object from the Kubernetes API server.
//  2. Maps spec.release + spec.environment → pkg/reconcile.Request.
//  3. Calls Runner.Run() — existing plan→deploy→verify→promote logic.
//  4. Patches .status.conditions + .status.currentRevision via the status subresource.
//  5. Records a Kubernetes event for the outcome.
//  6. Returns RequeueAfter for periodic re-evaluation.
//
// Design invariant: all deploy logic lives in pkg/reconcile. This controller
// only bridges the Kubernetes API ↔ reconciler boundary.
package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
)

// DataPlaneReconciler reconciles BoriDataPlane objects.
type DataPlaneReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// BoriRoot is the local bori repo root (releases/, components/, environments/).
	BoriRoot string
	// BoriDir is the bori state directory (.bori/ by default).
	BoriDir string
	// AppsDir is the parent directory of app repos (used by deploy adapters).
	AppsDir string
	// Runner executes the plan→deploy→verify→promote cycle.
	// In production this is *pkg/reconcile.Reconciler; in tests a mock.
	Runner reconcilepkg.Runner
	// RequeueInterval controls how often a healthy BoriDataPlane is re-evaluated.
	RequeueInterval time.Duration
}

// Reconcile processes one BoriDataPlane reconcile event.
//
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes/finalizers,verbs=update
func (r *DataPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	interval := r.RequeueInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	var bdp v1alpha1.BoriDataPlane
	if err := r.Get(ctx, req.NamespacedName, &bdp); err != nil {
		// Not-found is not an error: the object was deleted between enqueue and reconcile.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	res, err := r.Runner.Run(ctx, reconcilepkg.Request{
		BoriRoot:     r.BoriRoot,
		BoriDir:      r.BoriDir,
		AppsDir:      r.AppsDir,
		ReleaseName:  bdp.Spec.Release,
		EnvName:      bdp.Spec.Environment,
		SkipIfInSync: true,
	})
	if err != nil {
		r.Recorder.Event(&bdp, corev1.EventTypeWarning, "ReconcileFailed",
			fmt.Sprintf("reconcile error: %v", err))
		return ctrl.Result{RequeueAfter: interval}, err
	}

	// Patch .status from the shadow reconcile result.
	patch := client.MergeFrom(bdp.DeepCopy())
	bdp.Status = buildStatus(res)
	if patchErr := r.Status().Patch(ctx, &bdp, patch); patchErr != nil {
		logger.Error(patchErr, "failed to patch BoriDataPlane status")
		return ctrl.Result{RequeueAfter: interval}, patchErr
	}

	if res.DeployStatus != "skipped" && res.DeployStatus != "skipped (dry-run)" {
		r.Recorder.Event(&bdp, corev1.EventTypeNormal, "Reconciled",
			fmt.Sprintf("deploy=%s promoted=%v revision=%s",
				res.DeployStatus, res.Promoted, res.RevisionID))
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

// SetupWithManager registers the controller with a controller-runtime Manager.
func (r *DataPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BoriDataPlane{}).
		Complete(r)
}

// buildStatus converts a reconcile result into the BoriDataPlane status subresource.
func buildStatus(res *reconcilepkg.Result) v1alpha1.BoriDataPlaneStatus {
	if res == nil || res.ShadowState == nil {
		return v1alpha1.BoriDataPlaneStatus{
			ObservedAt: metav1.NewTime(time.Now().UTC()),
		}
	}
	return v1alpha1.BoriDataPlaneStatus{
		CurrentRevision: res.ShadowState.ActualRevision,
		ObservedAt:      metav1.NewTime(res.ShadowState.ComputedAt),
		Conditions:      res.ShadowState.Conditions,
		Components:      res.ShadowState.Components,
	}
}
