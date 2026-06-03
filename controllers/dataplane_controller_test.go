package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
	shadowpkg "github.com/HeaInSeo/bori/pkg/shadow"
)

// mockRunner is a test double for reconcilepkg.Runner.
type mockRunner struct {
	result  *reconcilepkg.Result
	err     error
	called  bool
	lastReq reconcilepkg.Request
}

func (m *mockRunner) Run(_ context.Context, req reconcilepkg.Request) (*reconcilepkg.Result, error) {
	m.called = true
	m.lastReq = req
	return m.result, m.err
}

func setupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func newBDP(namespace, name, release, env string) *v1alpha1.BoriDataPlane {
	return &v1alpha1.BoriDataPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.BoriDataPlaneSpec{
			Release:     release,
			Environment: env,
		},
	}
}

func TestReconcile_notFound(t *testing.T) {
	scheme := setupScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	runner := &mockRunner{result: &reconcilepkg.Result{DeployStatus: "skipped"}}

	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "nonexistent"},
	})
	if err != nil {
		t.Fatalf("expected nil error for not-found, got: %v", err)
	}
	if runner.called {
		t.Error("runner must not be called when the object is not found")
	}
}

func TestReconcile_patchesStatus(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test-release", "test-release", "dev")

	now := time.Now().UTC()
	shadowState := &shadowpkg.ShadowState{
		Release:        "test-release",
		ComputedAt:     now,
		ActualRevision: "rev-abc123",
		Conditions: []v1alpha1.Condition{
			{
				Type:               v1alpha1.ConditionInstalled,
				Status:             v1alpha1.ConditionTrue,
				Reason:             "RevisionFound",
				Message:            "installed",
				LastTransitionTime: metav1.NewTime(now),
			},
		},
	}
	runner := &mockRunner{
		result: &reconcilepkg.Result{
			RunID:        "run-001",
			Release:      "test-release",
			DeployStatus: "skipped",
			ShadowState:  shadowState,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bdp).
		WithObjects(bdp).
		Build()

	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-release"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("requeue: want 10s, got %v", res.RequeueAfter)
	}

	// Verify runner was called with the correct release/env.
	if !runner.called {
		t.Fatal("runner was not called")
	}
	if runner.lastReq.ReleaseName != "test-release" {
		t.Errorf("release: want %q, got %q", "test-release", runner.lastReq.ReleaseName)
	}
	if runner.lastReq.EnvName != "dev" {
		t.Errorf("env: want %q, got %q", "dev", runner.lastReq.EnvName)
	}

	// Verify status was patched.
	var got v1alpha1.BoriDataPlane
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "test-release"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.CurrentRevision != "rev-abc123" {
		t.Errorf("currentRevision: want %q, got %q", "rev-abc123", got.Status.CurrentRevision)
	}
	if len(got.Status.Conditions) != 1 {
		t.Errorf("conditions: want 1, got %d", len(got.Status.Conditions))
	}
	if len(got.Status.Conditions) > 0 && got.Status.Conditions[0].Type != v1alpha1.ConditionInstalled {
		t.Errorf("condition type: want %q, got %q",
			v1alpha1.ConditionInstalled, got.Status.Conditions[0].Type)
	}
}

func TestReconcile_runnerError(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test-release", "test-release", "dev")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bdp).
		WithObjects(bdp).
		Build()

	runner := &mockRunner{err: fmt.Errorf("adapter failed")}

	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 5 * time.Second,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-release"},
	})
	if err == nil {
		t.Fatal("expected error when runner fails, got nil")
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("requeue: want 5s, got %v", res.RequeueAfter)
	}
}

func TestReconcile_mapsSpecToRequest(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("bori-system", "my-release", "custom-release", "production")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bdp).
		WithObjects(bdp).
		Build()

	runner := &mockRunner{
		result: &reconcilepkg.Result{
			DeployStatus: "skipped",
			ShadowState: &shadowpkg.ShadowState{
				ComputedAt: time.Now().UTC(),
			},
		},
	}

	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		BoriRoot:        "/bori",
		BoriDir:         "/bori/.bori",
		AppsDir:         "/apps",
		RequeueInterval: 30 * time.Second,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "bori-system", Name: "my-release"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if runner.lastReq.ReleaseName != "custom-release" {
		t.Errorf("release: want %q, got %q", "custom-release", runner.lastReq.ReleaseName)
	}
	if runner.lastReq.EnvName != "production" {
		t.Errorf("env: want %q, got %q", "production", runner.lastReq.EnvName)
	}
	if runner.lastReq.BoriRoot != "/bori" {
		t.Errorf("bori-root: want %q, got %q", "/bori", runner.lastReq.BoriRoot)
	}
	if !runner.lastReq.SkipIfInSync {
		t.Error("SkipIfInSync must be true in the default reconcile path")
	}
}
