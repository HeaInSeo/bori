package controllers

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
)

// BoriVerificationRunReconciler links BoriVerificationRun CRs to BoriRevision status.
//
// Responsibility: when a BoriVerificationRun is created (by bori verify CLI or
// Phase 12 Ingestion API), this reconciler finds the referenced BoriRevision via
// spec.revisionId and sets BoriRevision.status.verificationRunId = BVR.name.
//
// Design rules (ADR-003):
//   - first-write-wins: if BoriRevision.status.verificationRunId is already set to a
//     different value, it is NOT overwritten.
//   - non-fatal: a missing BoriRevision is logged but does not produce a controller error.
//   - This controller does NOT run any verification logic. It only links records.
type BoriVerificationRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile links a BoriVerificationRun to its BoriRevision.
//
// +kubebuilder:rbac:groups=bori.dev,resources=boriverificationruns,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=bori.dev,resources=boriverificationruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bori.dev,resources=borirevisions/status,verbs=get;update;patch
func (r *BoriVerificationRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var bvr v1alpha1.BoriVerificationRun
	if err := r.Get(ctx, req.NamespacedName, &bvr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Update status.observedAt.
	patch := client.MergeFrom(bvr.DeepCopy())
	bvr.Status.ObservedAt = metav1.NewTime(time.Now().UTC())
	if err := r.Status().Patch(ctx, &bvr, patch); err != nil {
		return ctrl.Result{}, err
	}

	// If no revisionId, nothing to link.
	if bvr.Spec.RevisionID == "" {
		log.V(1).Info("no revisionId — skipping BoriRevision link", "bvr", bvr.Name)
		return ctrl.Result{}, nil
	}

	// Find the BoriRevision named by spec.revisionId in the same namespace.
	var rev v1alpha1.BoriRevision
	if err := r.Get(ctx, client.ObjectKey{Namespace: bvr.Namespace, Name: bvr.Spec.RevisionID}, &rev); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		// BoriRevision not found — non-fatal, log and stop.
		log.Info("BoriRevision not found for BoriVerificationRun — skipping link",
			"bvr", bvr.Name, "revisionId", bvr.Spec.RevisionID)
		return ctrl.Result{}, nil
	}

	// first-write-wins: if already linked to a different BVR, do not overwrite.
	if rev.Status.VerificationRunID != "" && rev.Status.VerificationRunID != bvr.Name {
		log.V(1).Info("BoriRevision already linked to another BoriVerificationRun — skipping",
			"revision", rev.Name,
			"existing", rev.Status.VerificationRunID,
			"incoming", bvr.Name,
		)
		return ctrl.Result{}, nil
	}

	// Idempotent: already linked to this BVR.
	if rev.Status.VerificationRunID == bvr.Name {
		return ctrl.Result{}, nil
	}

	// Link: set BoriRevision.status.verificationRunId = BVR.name.
	revPatch := client.MergeFrom(rev.DeepCopy())
	rev.Status.VerificationRunID = bvr.Name
	rev.Status.ObservedAt = metav1.NewTime(time.Now().UTC())
	if err := r.Status().Patch(ctx, &rev, revPatch); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("linked BoriVerificationRun → BoriRevision",
		"bvr", bvr.Name, "revision", rev.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager registers BoriVerificationRunReconciler with the manager.
func (r *BoriVerificationRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BoriVerificationRun{}).
		Complete(r)
}
