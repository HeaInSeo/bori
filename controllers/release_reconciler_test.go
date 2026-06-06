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

func TestBoriReleaseReconciler_countsActiveDataPlanes(t *testing.T) {
	scheme := setupScheme(t)
	br := newBoriRelease("default", "my-release")
	bdp1 := newBDP("default", "dp-one", "my-release", "dev")
	bdp2 := newBDP("default", "dp-two", "my-release", "dev")
	bdp3 := newBDP("default", "dp-other", "other-release", "dev") // different release

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(br).
		WithObjects(br, bdp1, bdp2, bdp3).
		Build()

	r := &BoriReleaseReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "my-release"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got v1alpha1.BoriRelease
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "my-release"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ActiveDataPlanes != 2 {
		t.Errorf("activeDataPlanes: want 2, got %d", got.Status.ActiveDataPlanes)
	}
}

func TestBoriReleaseReconciler_excludesDeletingDataPlanes(t *testing.T) {
	scheme := setupScheme(t)
	br := newBoriRelease("default", "my-release")

	now := metav1.NewTime(time.Now())
	deletingBDP := &v1alpha1.BoriDataPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "dp-deleting",
			Namespace:         "default",
			Generation:        1,
			DeletionTimestamp: &now,
			Finalizers:        []string{finalizerName},
		},
		Spec: v1alpha1.BoriDataPlaneSpec{Release: "my-release", Environment: "dev"},
	}
	activeBDP := newBDP("default", "dp-active", "my-release", "dev")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(br).
		WithObjects(br, deletingBDP, activeBDP).
		Build()

	r := &BoriReleaseReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "my-release"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got v1alpha1.BoriRelease
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "my-release"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Only the active BDP counts; the deleting one is excluded.
	if got.Status.ActiveDataPlanes != 1 {
		t.Errorf("activeDataPlanes: want 1, got %d", got.Status.ActiveDataPlanes)
	}
}

func TestBoriReleaseReconciler_releaseNotFound(t *testing.T) {
	scheme := setupScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &BoriReleaseReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "nonexistent"},
	})
	if err != nil {
		t.Fatalf("expected nil for not-found BoriRelease, got: %v", err)
	}
}

func TestBoriReleaseReconciler_zeroWhenNoDataPlanes(t *testing.T) {
	scheme := setupScheme(t)
	br := newBoriRelease("default", "unused-release")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(br).
		WithObjects(br).
		Build()

	r := &BoriReleaseReconciler{Client: fakeClient, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "unused-release"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got v1alpha1.BoriRelease
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "unused-release"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ActiveDataPlanes != 0 {
		t.Errorf("activeDataPlanes: want 0, got %d", got.Status.ActiveDataPlanes)
	}
}

func TestBoriReleaseReconciler_findReleaseForDataPlane(t *testing.T) {
	scheme := setupScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &BoriReleaseReconciler{Client: fakeClient}
	bdp := newBDP("bori-system", "dp-one", "rel-a", "dev")

	reqs := r.findReleaseForDataPlane(context.Background(), bdp)
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	if reqs[0].Namespace != "bori-system" || reqs[0].Name != "rel-a" {
		t.Errorf("request: want bori-system/rel-a, got %s/%s", reqs[0].Namespace, reqs[0].Name)
	}
}
