package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

// ObservationSource supplies current evidence as O1 observations. O2 defines
// no provider transport; an adapter implements this interface in-process.
type ObservationSource interface {
	Observations(ctx context.Context) ([]operations.Observation, error)
}

// NoObservations is the default source: no evidence, so every capability that
// needs evidence is UNKNOWN (evidence-missing) rather than guessed.
type NoObservations struct{}

// Observations returns no observations.
func (NoObservations) Observations(context.Context) ([]operations.Observation, error) {
	return nil, nil
}

// DefaultTargetKinds is the bounded set of targetRef kinds resolved by
// default. Only object metadata (UID) is read; the operator's RBAC already
// allows get/list/watch on these kinds. Any other kind is reported as
// target-kind-unsupported unless explicitly opted in.
var DefaultTargetKinds = []schema.GroupVersionKind{
	{Group: "apps", Version: "v1", Kind: "Deployment"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"},
}

// assessmentRequest is the single work item. All relevant events collapse
// into it, so a burst of changes yields one cluster-wide evaluation.
var assessmentRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: "operational-assessment"}}

// OperationalReconciler is a non-actuating evaluator/status controller for
// ops.bori.dev objects. It reads OperationalContract, OperationalTarget,
// OperationalReferenceGrant and targetRef metadata, evaluates with the O1
// core, and writes status only when its semantic content changes. It never
// writes any spec and never touches workloads.
//
// +kubebuilder:rbac:groups=ops.bori.dev,resources=operationalcontracts;operationaltargets;operationalreferencegrants,verbs=get;list;watch
// +kubebuilder:rbac:groups=ops.bori.dev,resources=operationalcontracts/status;operationaltargets/status,verbs=get;update;patch
type OperationalReconciler struct {
	Client       client.Client
	Observations ObservationSource
	// TargetKinds is the bounded set of resolvable targetRef kinds.
	TargetKinds []schema.GroupVersionKind
	// Now supplies the evaluation instant; the O1 core reads no clock.
	Now             func() time.Time
	RequeueInterval time.Duration
}

// Reconcile performs one cluster-wide evaluation.
func (r *OperationalReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	now := r.now()

	var contracts opsv1.OperationalContractList
	if err := r.Client.List(ctx, &contracts); err != nil {
		return ctrl.Result{}, fmt.Errorf("list contracts: %w", err)
	}
	var targets opsv1.OperationalTargetList
	if err := r.Client.List(ctx, &targets); err != nil {
		return ctrl.Result{}, fmt.Errorf("list targets: %w", err)
	}
	var grants opsv1.OperationalReferenceGrantList
	if err := r.Client.List(ctx, &grants); err != nil {
		return ctrl.Result{}, fmt.Errorf("list grants: %w", err)
	}

	resolutions := map[types.NamespacedName]opswire.Resolution{}
	for i := range targets.Items {
		t := &targets.Items[i]
		res, err := r.resolve(ctx, t)
		if err != nil {
			return ctrl.Result{}, err
		}
		resolutions[types.NamespacedName{Namespace: t.Namespace, Name: t.Name}] = res
	}

	src := r.Observations
	if src == nil {
		src = NoObservations{}
	}
	obs, err := src.Observations(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("observations: %w", err)
	}

	built := opswire.Build(opswire.Input{
		At:           now,
		Contracts:    contracts.Items,
		Targets:      targets.Items,
		Grants:       grants.Items,
		Resolutions:  resolutions,
		Observations: obs,
	})
	assessment, err := operations.Evaluate(built.Snapshot)
	if err != nil {
		return ctrl.Result{}, err
	}

	var errs []error
	for i := range contracts.Items {
		c := &contracts.Items[i]
		desired := opswire.ContractStatus(c)
		if opswire.ContractStatusEqual(c.Status, desired) {
			continue
		}
		c.Status = desired
		if err := r.Client.Status().Update(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("contract %s/%s status: %w", c.Namespace, c.Name, err))
		}
	}
	for i := range targets.Items {
		t := &targets.Items[i]
		next, changed := opswire.Apply(t.Status, opswire.Project(t, built, assessment), now)
		if !changed {
			continue
		}
		t.Status = next
		if err := r.Client.Status().Update(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("target %s/%s status: %w", t.Namespace, t.Name, err))
		}
	}
	if len(errs) > 0 {
		return ctrl.Result{}, errors.Join(errs...)
	}
	return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}

func (r *OperationalReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// resolve reads only the metadata (UID) of a targetRef whose kind is in the
// bounded support set, in the OperationalTarget's own namespace.
func (r *OperationalReconciler) resolve(ctx context.Context, t *opsv1.OperationalTarget) (opswire.Resolution, error) {
	gv, err := schema.ParseGroupVersion(t.Spec.TargetRef.APIVersion)
	if err != nil {
		return opswire.Resolution{Failure: opswire.ReasonTargetKindUnsupported}, nil
	}
	gvk := gv.WithKind(t.Spec.TargetRef.Kind)
	if !r.supported(gvk) {
		return opswire.Resolution{Failure: opswire.ReasonTargetKindUnsupported}, nil
	}
	var md metav1.PartialObjectMetadata
	md.SetGroupVersionKind(gvk)
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: t.Namespace, Name: t.Spec.TargetRef.Name}, &md)
	switch {
	case apierrors.IsNotFound(err):
		return opswire.Resolution{Failure: opswire.ReasonTargetNotFound}, nil
	case err != nil:
		return opswire.Resolution{}, fmt.Errorf("resolve %s %s/%s: %w", gvk.Kind, t.Namespace, t.Spec.TargetRef.Name, err)
	}
	return opswire.Resolution{UID: string(md.UID)}, nil
}

func (r *OperationalReconciler) supported(gvk schema.GroupVersionKind) bool {
	for _, k := range r.TargetKinds {
		if k == gvk {
			return true
		}
	}
	return false
}

// SpecOrLifecycleChanged admits create/delete and spec (generation) changes,
// and drops status-only updates, including this controller's own writes.
var SpecOrLifecycleChanged = predicate.GenerationChangedPredicate{}

// recreateOnly admits only creation and deletion of targetRef objects: a
// resolved UID can only change by recreation.
var recreateOnly = predicate.Funcs{
	UpdateFunc:  func(event.UpdateEvent) bool { return false },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// SetupWithManager wires watches that all map to the single assessment item.
func (r *OperationalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toAssessment := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{assessmentRequest}
	})
	b := ctrl.NewControllerManagedBy(mgr).
		Named("operational-assessment").
		Watches(&opsv1.OperationalTarget{}, toAssessment, builder.WithPredicates(SpecOrLifecycleChanged)).
		Watches(&opsv1.OperationalContract{}, toAssessment, builder.WithPredicates(SpecOrLifecycleChanged)).
		Watches(&opsv1.OperationalReferenceGrant{}, toAssessment)
	for _, gvk := range r.TargetKinds {
		md := &metav1.PartialObjectMetadata{}
		md.SetGroupVersionKind(gvk)
		b = b.WatchesMetadata(md, toAssessment, builder.WithPredicates(recreateOnly))
	}
	return b.Complete(r)
}
