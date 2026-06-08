// Package controllers implements the bori operator controller loop.
//
// Phase 8 — Operator Deployment Hardening.
//
// Changes from Phase 7:
//   - Finalizer (bori.dev/cleanup): ensures graceful deletion
//   - Generation-aware reconcile: skips expensive Runner.Run() when the CR
//     spec hasn't changed and no problem condition is set
//   - Namespace policy enforcement: ViolationError → Violation condition, no error requeue
//   - Secret redaction: all event messages pass through security.RedactString()
package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/model"
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
	"github.com/HeaInSeo/bori/pkg/revision"
	"github.com/HeaInSeo/bori/pkg/security"
)

const finalizerName = "bori.dev/cleanup"

// DataPlaneReconciler reconciles BoriDataPlane objects.
type DataPlaneReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	BoriRoot string
	BoriDir  string
	AppsDir  string
	// Runner executes plan→deploy→verify→promote.
	// *pkg/reconcile.Reconciler in production; mock in tests.
	Runner          reconcilepkg.Runner
	RequeueInterval time.Duration
}

// Reconcile processes one BoriDataPlane event.
//
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bori.dev,resources=boridataplanes/finalizers,verbs=update
func (r *DataPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	interval := r.requeueInterval()

	var bdp v1alpha1.BoriDataPlane
	if err := r.Get(ctx, req.NamespacedName, &bdp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion: clean up and remove finalizer.
	if !bdp.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &bdp)
	}

	// Ensure finalizer is present.
	if !controllerutil.ContainsFinalizer(&bdp, finalizerName) {
		controllerutil.AddFinalizer(&bdp, finalizerName)
		if err := r.Update(ctx, &bdp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Generation-aware: skip expensive Runner.Run() when the CR spec hasn't
	// changed since the last reconcile and there is no active problem condition.
	// ObservedGeneration > 0 ensures we always run at least once.
	if bdp.Status.ObservedGeneration > 0 &&
		bdp.Status.ObservedGeneration == bdp.Generation &&
		!isUnhealthy(&bdp) {
		return ctrl.Result{RequeueAfter: interval}, nil
	}

	// Resolve release: K8s API first, filesystem fallback.
	release, err := r.resolveRelease(ctx, &bdp)
	if err != nil {
		r.Recorder.Event(&bdp, corev1.EventTypeWarning, "ReleaseResolveFailed",
			security.RedactString(fmt.Sprintf("resolve BoriRelease: %v", err)))
		return ctrl.Result{RequeueAfter: interval}, err
	}

	res, err := r.Runner.Run(ctx, reconcilepkg.Request{
		BoriRoot:     r.BoriRoot,
		BoriDir:      r.BoriDir,
		AppsDir:      r.AppsDir,
		ReleaseName:  bdp.Spec.Release,
		EnvName:      bdp.Spec.Environment,
		SkipIfInSync: true,
		Release:      release,
	})

	// Namespace policy violations are not runtime errors: set a condition and
	// requeue slowly instead of returning an error that would trigger backoff.
	var violErr *reconcilepkg.ViolationError
	if errors.As(err, &violErr) {
		return r.reconcileViolation(ctx, &bdp, violErr)
	}

	if err != nil {
		r.Recorder.Event(&bdp, corev1.EventTypeWarning, "ReconcileFailed",
			security.RedactString(fmt.Sprintf("reconcile error: %v", err)))
		return ctrl.Result{RequeueAfter: interval}, err
	}

	patch := client.MergeFrom(bdp.DeepCopy())
	bdp.Status = buildStatus(res, bdp.Generation)
	if patchErr := r.Status().Patch(ctx, &bdp, patch); patchErr != nil {
		logger.Error(patchErr, "failed to patch BoriDataPlane status")
		return ctrl.Result{RequeueAfter: interval}, patchErr
	}

	if res.DeployStatus != "skipped" && res.DeployStatus != "skipped (dry-run)" {
		r.Recorder.Event(&bdp, corev1.EventTypeNormal, "Reconciled",
			security.RedactString(fmt.Sprintf("deploy=%s promoted=%v revision=%s",
				res.DeployStatus, res.Promoted, res.RevisionID)))
	}

	// Upsert BoriRevision CR after a successful promotion (non-fatal on error).
	if res.Promoted && res.RevisionID != "" {
		if err := r.upsertBoriRevision(ctx, bdp.Namespace, res.RevisionID); err != nil {
			log.FromContext(ctx).Error(err, "failed to upsert BoriRevision CR — disk artifact is preserved")
		}
	}

	return ctrl.Result{RequeueAfter: interval}, nil
}

// reconcileDelete removes the finalizer after recording that the object was deleted.
// bori retains shadow state and revision history on disk — the adapters own the
// actual Kubernetes resources (Deployments etc.) and they are not cleaned up here.
func (r *DataPlaneReconciler) reconcileDelete(ctx context.Context, bdp *v1alpha1.BoriDataPlane) (ctrl.Result, error) {
	log.FromContext(ctx).Info("handling deletion",
		"release", bdp.Spec.Release, "environment", bdp.Spec.Environment)

	controllerutil.RemoveFinalizer(bdp, finalizerName)
	if err := r.Update(ctx, bdp); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Event(bdp, corev1.EventTypeNormal, "Deleted",
		fmt.Sprintf("BoriDataPlane %s/%s deleted; shadow state and revision history retained on disk",
			bdp.Namespace, bdp.Name))
	return ctrl.Result{}, nil
}

// reconcileViolation sets Violation + Degraded conditions and requeues slowly.
// Violations (namespace not in allowed list) don't self-heal — they require a CR
// or environment spec change. Using a long requeue avoids high-frequency retries.
func (r *DataPlaneReconciler) reconcileViolation(ctx context.Context, bdp *v1alpha1.BoriDataPlane, violErr *reconcilepkg.ViolationError) (ctrl.Result, error) {
	now := metav1.NewTime(time.Now().UTC())
	msg := strings.Join(violErr.Violations, "; ")

	patch := client.MergeFrom(bdp.DeepCopy())
	bdp.Status.ObservedGeneration = bdp.Generation
	bdp.Status.ObservedAt = now
	bdp.Status.Conditions = setCondition(bdp.Status.Conditions, v1alpha1.Condition{
		Type:               v1alpha1.ConditionViolation,
		Status:             v1alpha1.ConditionTrue,
		Reason:             "NamespaceViolation",
		Message:            msg,
		LastTransitionTime: now,
	})
	bdp.Status.Conditions = setCondition(bdp.Status.Conditions, v1alpha1.Condition{
		Type:               v1alpha1.ConditionDegraded,
		Status:             v1alpha1.ConditionTrue,
		Reason:             "NamespaceViolation",
		Message:            "namespace policy violations prevent deployment",
		LastTransitionTime: now,
	})

	if err := r.Status().Patch(ctx, bdp, patch); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Event(bdp, corev1.EventTypeWarning, "NamespaceViolation",
		security.RedactString(msg))

	// Long requeue: violations require manual correction, not rapid retry.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// resolveRelease tries to fetch the BoriRelease from the Kubernetes API.
// If the CR is not found, it returns nil — the reconciler falls back to the filesystem.
// This allows CLI users (no BoriRelease CRs) and operator users to coexist.
func (r *DataPlaneReconciler) resolveRelease(ctx context.Context, bdp *v1alpha1.BoriDataPlane) (*model.BoriRelease, error) {
	var br v1alpha1.BoriRelease
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: bdp.Namespace,
		Name:      bdp.Spec.Release,
	}, &br); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil // not a CR — use filesystem
		}
		return nil, fmt.Errorf("get BoriRelease %s/%s: %w", bdp.Namespace, bdp.Spec.Release, err)
	}
	rel := br.ToModel()
	return &rel, nil
}

// SetupWithManager registers the controller with a controller-runtime Manager.
// It watches both BoriDataPlane and BoriRelease objects:
// when a BoriRelease changes, all BoriDataPlanes that reference it are enqueued.
func (r *DataPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BoriDataPlane{}).
		Watches(
			&v1alpha1.BoriRelease{},
			handler.EnqueueRequestsFromMapFunc(r.findDataPlanesForRelease),
		).
		Complete(r)
}

// findDataPlanesForRelease maps a BoriRelease event to the BoriDataPlane objects
// that reference it, so they are re-reconciled when the release definition changes.
func (r *DataPlaneReconciler) findDataPlanesForRelease(ctx context.Context, obj client.Object) []ctrl.Request {
	release, ok := obj.(*v1alpha1.BoriRelease)
	if !ok {
		return nil
	}
	var bdpList v1alpha1.BoriDataPlaneList
	if err := r.List(ctx, &bdpList, client.InNamespace(release.Namespace)); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for _, bdp := range bdpList.Items {
		if bdp.Spec.Release == release.Name {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: bdp.Namespace,
					Name:      bdp.Name,
				},
			})
		}
	}
	return requests
}

// requeueInterval returns the configured interval, falling back to 30s.
func (r *DataPlaneReconciler) requeueInterval() time.Duration {
	if r.RequeueInterval > 0 {
		return r.RequeueInterval
	}
	return 30 * time.Second
}

// buildStatus converts a reconcile result into BoriDataPlaneStatus.
func buildStatus(res *reconcilepkg.Result, generation int64) v1alpha1.BoriDataPlaneStatus {
	if res == nil || res.ShadowState == nil {
		return v1alpha1.BoriDataPlaneStatus{
			ObservedAt:         metav1.NewTime(time.Now().UTC()),
			ObservedGeneration: generation,
		}
	}
	return v1alpha1.BoriDataPlaneStatus{
		CurrentRevision:    res.ShadowState.ActualRevision,
		ObservedAt:         metav1.NewTime(res.ShadowState.ComputedAt),
		ObservedGeneration: generation,
		Conditions:         res.ShadowState.Conditions,
		Components:         res.ShadowState.Components,
	}
}

// setCondition upserts a condition into the slice, preserving LastTransitionTime
// when the Status value has not changed.
func setCondition(conditions []v1alpha1.Condition, cond v1alpha1.Condition) []v1alpha1.Condition {
	for i, c := range conditions {
		if c.Type == cond.Type {
			if c.Status == cond.Status {
				cond.LastTransitionTime = c.LastTransitionTime
			}
			conditions[i] = cond
			return conditions
		}
	}
	return append(conditions, cond)
}

// upsertBoriRevision creates or updates a BoriRevision CR from the on-disk
// revision file. This is a dual-write: disk remains the source of truth for
// the CLI; the K8s CR makes history queryable via kubectl.
//
// ownerReference is intentionally NOT set. BoriRevision is an append-only
// deployment history resource: it must survive BoriDataPlane deletion so that
// audit trails and promotion history are preserved. Cascade deletion is not
// desired. See docs/adr/ADR-001-borirevision-failreason.md for the broader
// BoriRevision design context.
func (r *DataPlaneReconciler) upsertBoriRevision(ctx context.Context, namespace, revisionID string) error {
	rev, err := revision.Read(r.BoriDir, revisionID)
	if err != nil {
		return fmt.Errorf("read revision %s from disk: %w", revisionID, err)
	}

	cr := revisionToCR(namespace, rev)

	var existing v1alpha1.BoriRevision
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: revisionID}, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get BoriRevision: %w", err)
		}
		// First time: create the CR.
		return r.Create(ctx, &cr)
	}

	// Already exists: update status only (spec is immutable after creation).
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status = cr.Status
	return r.Status().Patch(ctx, &existing, patch)
}

// revisionToCR converts a pkg/revision.BoriRevision into a v1alpha1.BoriRevision CR.
func revisionToCR(namespace string, rev revision.BoriRevision) v1alpha1.BoriRevision {
	cr := v1alpha1.BoriRevision{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "BoriRevision",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rev.RevisionID,
			Namespace: namespace,
		},
		Spec: v1alpha1.BoriRevisionSpec{
			Release:          rev.Release,
			Environment:      rev.Environment,
			ContentHash:      rev.ContentHash,
			ParentRevisionID: rev.ParentRevisionID,
			BaselineRef:      rev.BaselineRef,
		},
		Status: v1alpha1.BoriRevisionStatus{
			PromotionStatus:   rev.PromotionStatus,
			VerificationRunID: rev.VerificationRunID,
			ObservedAt:        metav1.NewTime(time.Now().UTC()),
		},
	}
	if rev.PromotedAt != nil {
		t := metav1.NewTime(*rev.PromotedAt)
		cr.Status.PromotedAt = &t
	}
	for _, c := range rev.Components {
		cr.Spec.Components = append(cr.Spec.Components, v1alpha1.RevisionComponentRef{
			Name:                     c.Name,
			Version:                  c.Version,
			ImageRef:                 c.ImageRef,
			ImageDigest:              c.ImageDigest,
			GitSha:                   c.GitSha,
			ComponentSpecDigest:      c.ComponentSpecDigest,
			EnvironmentDigest:        c.EnvironmentDigest,
			VerificationPolicyDigest: c.VerificationPolicyDigest,
		})
	}
	return cr
}

// isUnhealthy reports whether the BoriDataPlane has an active Degraded or
// Violation condition. Unhealthy objects are always fully reconciled regardless
// of generation matching.
func isUnhealthy(bdp *v1alpha1.BoriDataPlane) bool {
	for _, c := range bdp.Status.Conditions {
		switch c.Type {
		case v1alpha1.ConditionDegraded, v1alpha1.ConditionViolation:
			if c.Status == v1alpha1.ConditionTrue {
				return true
			}
		}
	}
	return false
}
