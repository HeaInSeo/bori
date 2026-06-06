package controllers

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
)

// BoriReleaseReconciler keeps BoriRelease.status.activeDataPlanes accurate.
//
// It watches BoriDataPlane events and, for each change, re-counts how many
// BoriDataPlanes in the same namespace reference the affected release.
// This gives operators a live view of release adoption without manual queries.
type BoriReleaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile recomputes activeDataPlanes for a single BoriRelease.
//
// +kubebuilder:rbac:groups=bori.dev,resources=borireleases,verbs=get;list;watch
// +kubebuilder:rbac:groups=bori.dev,resources=borireleases/status,verbs=get;update;patch
func (r *BoriReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var br v1alpha1.BoriRelease
	if err := r.Get(ctx, req.NamespacedName, &br); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Count active (non-deleting) BoriDataPlanes that reference this release.
	var bdpList v1alpha1.BoriDataPlaneList
	if err := r.List(ctx, &bdpList, client.InNamespace(br.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	var count int32
	for _, bdp := range bdpList.Items {
		if bdp.Spec.Release == br.Name && bdp.DeletionTimestamp.IsZero() {
			count++
		}
	}

	patch := client.MergeFrom(br.DeepCopy())
	br.Status.ActiveDataPlanes = count
	br.Status.ObservedGeneration = br.Generation
	br.Status.ObservedAt = metav1.NewTime(time.Now().UTC())
	if err := r.Status().Patch(ctx, &br, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers BoriReleaseReconciler and adds a secondary watch
// on BoriDataPlane so that a BDP create/update/delete triggers a re-count on
// the referenced BoriRelease.
func (r *BoriReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BoriRelease{}).
		Watches(
			&v1alpha1.BoriDataPlane{},
			handler.EnqueueRequestsFromMapFunc(r.findReleaseForDataPlane),
		).
		Complete(r)
}

// findReleaseForDataPlane maps a BoriDataPlane event to the BoriRelease it references.
func (r *BoriReleaseReconciler) findReleaseForDataPlane(_ context.Context, obj client.Object) []ctrl.Request {
	bdp, ok := obj.(*v1alpha1.BoriDataPlane)
	if !ok || bdp.Spec.Release == "" {
		return nil
	}
	return []ctrl.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: bdp.Namespace,
			Name:      bdp.Spec.Release,
		},
	}}
}
