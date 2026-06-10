package controllers

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
)

func newBVR(namespace, name, release, revisionID, gateResult string) *v1alpha1.BoriVerificationRun {
	now := metav1.NewTime(time.Now().UTC())
	return &v1alpha1.BoriVerificationRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.BoriVerificationRunSpec{
			Provider:          "kube-slint",
			Release:           release,
			Environment:       "kind",
			RevisionID:        revisionID,
			GateResult:        gateResult,
			PromotionDecision: "eligible",
			StartedAt:         now,
			FinishedAt:        now,
		},
	}
}

func newBoriRevision(namespace, name, release string) *v1alpha1.BoriRevision {
	return &v1alpha1.BoriRevision{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.BoriRevisionSpec{
			Release:     release,
			Environment: "kind",
			ContentHash: "abc123",
			Components:  []v1alpha1.RevisionComponentRef{{Name: "jumi", Version: "v0.1.0"}},
		},
		Status: v1alpha1.BoriRevisionStatus{
			PromotionStatus: "promoted",
			ObservedAt:      metav1.NewTime(time.Now().UTC()),
		},
	}
}

func TestBoriVerificationRunReconciler_linksRevision(t *testing.T) {
	scheme := setupScheme(t)
	revName := "jumi-20260610-120000-abc123"
	bvr := newBVR("default", "20260610-150405", "jumi-ah-dev", revName, "PASS")
	rev := newBoriRevision("default", revName, "jumi-ah-dev")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bvr, rev).
		WithObjects(bvr, rev).
		Build()

	r := &BoriVerificationRunReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "20260610-150405"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var gotRev v1alpha1.BoriRevision
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: revName}, &gotRev); err != nil {
		t.Fatalf("Get BoriRevision: %v", err)
	}
	if gotRev.Status.VerificationRunID != "20260610-150405" {
		t.Errorf("verificationRunId: want %q, got %q", "20260610-150405", gotRev.Status.VerificationRunID)
	}
}

func TestBoriVerificationRunReconciler_revisionNotFound(t *testing.T) {
	scheme := setupScheme(t)
	bvr := newBVR("default", "20260610-150405", "jumi-ah-dev", "nonexistent-revision", "PASS")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bvr).
		WithObjects(bvr).
		Build()

	r := &BoriVerificationRunReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "20260610-150405"},
	})
	if err != nil {
		t.Fatalf("expected nil for missing BoriRevision, got: %v", err)
	}
}

func TestBoriVerificationRunReconciler_idempotent(t *testing.T) {
	scheme := setupScheme(t)
	revName := "jumi-20260610-120000-abc123"
	bvr := newBVR("default", "20260610-150405", "jumi-ah-dev", revName, "PASS")
	rev := newBoriRevision("default", revName, "jumi-ah-dev")
	rev.Status.VerificationRunID = "20260610-150405" // already linked

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bvr, rev).
		WithObjects(bvr, rev).
		Build()

	r := &BoriVerificationRunReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "20260610-150405"},
	})
	if err != nil {
		t.Fatalf("Reconcile (idempotent): %v", err)
	}
	// No change — still linked to the same BVR.
	var gotRev v1alpha1.BoriRevision
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: revName}, &gotRev); err != nil {
		t.Fatalf("Get BoriRevision: %v", err)
	}
	if gotRev.Status.VerificationRunID != "20260610-150405" {
		t.Errorf("verificationRunId changed unexpectedly: %q", gotRev.Status.VerificationRunID)
	}
}

func TestBoriVerificationRunReconciler_noUpdateWhenAlreadyLinked(t *testing.T) {
	scheme := setupScheme(t)
	revName := "jumi-20260610-120000-abc123"
	// Newer BVR arrives, but the revision is already linked to an older one.
	bvr := newBVR("default", "20260610-160000", "jumi-ah-dev", revName, "PASS")
	rev := newBoriRevision("default", revName, "jumi-ah-dev")
	rev.Status.VerificationRunID = "20260610-150405" // linked to a different BVR

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bvr, rev).
		WithObjects(bvr, rev).
		Build()

	r := &BoriVerificationRunReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "20260610-160000"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var gotRev v1alpha1.BoriRevision
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: revName}, &gotRev); err != nil {
		t.Fatalf("Get BoriRevision: %v", err)
	}
	// first-write-wins: original link must be preserved.
	if gotRev.Status.VerificationRunID != "20260610-150405" {
		t.Errorf("first-write-wins violated: want %q, got %q",
			"20260610-150405", gotRev.Status.VerificationRunID)
	}
}
