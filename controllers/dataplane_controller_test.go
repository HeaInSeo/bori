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
			Name:       name,
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: v1alpha1.BoriDataPlaneSpec{
			Release:     release,
			Environment: env,
		},
	}
}

func newReconciler(scheme *runtime.Scheme, runner reconcilepkg.Runner, objects ...runtime.Object) (*DataPlaneReconciler, *fake.ClientBuilder) {
	objs := make([]runtime.Object, len(objects))
	copy(objs, objects)
	_ = objs
	return nil, fake.NewClientBuilder().WithScheme(scheme)
}

// ── Phase 7 tests (still valid) ────────────────────────────────────────────

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
		t.Fatalf("expected nil for not-found, got: %v", err)
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

	// First reconcile adds the finalizer and requeues.
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-release"},
	})
	if err != nil {
		t.Fatalf("first Reconcile (add finalizer): %v", err)
	}
	if !res.Requeue {
		t.Error("expected Requeue=true after adding finalizer")
	}

	// Second reconcile: finalizer present, run the full cycle.
	res, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-release"},
	})
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("requeue: want 10s, got %v", res.RequeueAfter)
	}
	if !runner.called {
		t.Fatal("runner was not called on second reconcile")
	}

	// Verify runner got the right spec.
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
	if got.Status.ObservedGeneration != 1 {
		t.Errorf("observedGeneration: want 1, got %d", got.Status.ObservedGeneration)
	}
	if len(got.Status.Conditions) == 0 {
		t.Error("expected at least one condition")
	}
}

func TestReconcile_runnerError(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test-release", "test-release", "dev")
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithStatusSubresource(bdp).WithObjects(bdp).Build()

	runner := &mockRunner{err: fmt.Errorf("adapter failed")}
	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 5 * time.Second,
	}

	// First reconcile: add finalizer.
	r.Reconcile(context.Background(), ctrl.Request{ //nolint:errcheck
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-release"},
	})

	// Second reconcile: runner error → requeue with error.
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
		WithScheme(scheme).WithStatusSubresource(bdp).WithObjects(bdp).Build()

	runner := &mockRunner{
		result: &reconcilepkg.Result{
			DeployStatus: "skipped",
			ShadowState:  &shadowpkg.ShadowState{ComputedAt: time.Now().UTC()},
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

	// First reconcile: add finalizer.
	r.Reconcile(context.Background(), ctrl.Request{ //nolint:errcheck
		NamespacedName: types.NamespacedName{Namespace: "bori-system", Name: "my-release"},
	})
	// Second reconcile: actual run.
	r.Reconcile(context.Background(), ctrl.Request{ //nolint:errcheck
		NamespacedName: types.NamespacedName{Namespace: "bori-system", Name: "my-release"},
	})

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
		t.Error("SkipIfInSync must be true")
	}
}

// ── Phase 8 new tests ───────────────────────────────────────────────────────

func TestReconcile_addsFinalizer(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test", "rel", "dev")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bdp).Build()

	runner := &mockRunner{result: &reconcilepkg.Result{DeployStatus: "skipped"}}
	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Requeue {
		t.Error("expected Requeue=true after adding finalizer")
	}
	if runner.called {
		t.Error("runner must not be called on the finalizer-add pass")
	}

	// Verify finalizer was written to the object.
	var got v1alpha1.BoriDataPlane
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "test"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == finalizerName {
			found = true
		}
	}
	if !found {
		t.Errorf("finalizer %q not found in %v", finalizerName, got.Finalizers)
	}
}

func TestReconcile_handlesDeletion(t *testing.T) {
	scheme := setupScheme(t)
	now := metav1.NewTime(time.Now())
	bdp := &v1alpha1.BoriDataPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test",
			Namespace:         "default",
			Generation:        1,
			DeletionTimestamp: &now,
			Finalizers:        []string{finalizerName},
		},
		Spec: v1alpha1.BoriDataPlaneSpec{Release: "rel", Environment: "dev"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bdp).Build()

	runner := &mockRunner{}
	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if runner.called {
		t.Error("runner must not be called during deletion")
	}

	// After removal of the last finalizer, the fake client garbage-collects the
	// object (it was already marked for deletion). Either outcome is correct:
	// not-found means it was deleted, or the object exists with no finalizers.
	var got v1alpha1.BoriDataPlane
	getErr := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "test"}, &got)
	if getErr == nil {
		for _, f := range got.Finalizers {
			if f == finalizerName {
				t.Errorf("finalizer %q should have been removed", finalizerName)
			}
		}
	}
	// getErr != nil (not found) is the expected happy path — object was deleted.
}

func TestReconcile_skipsIfGenerationMatches(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test", "rel", "dev")
	// Pre-populate status with observedGeneration matching bdp.Generation.
	bdp.Status = v1alpha1.BoriDataPlaneStatus{ObservedGeneration: 1}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bdp).
		WithObjects(bdp).
		Build()

	runner := &mockRunner{result: &reconcilepkg.Result{DeployStatus: "skipped"}}
	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	// First reconcile: no finalizer yet → adds it and requeues.
	r.Reconcile(context.Background(), ctrl.Request{ //nolint:errcheck
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})
	runner.called = false // reset

	// Second reconcile: finalizer present, generation matches → skip runner.
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if runner.called {
		t.Error("runner must not be called when generation already observed and not unhealthy")
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("requeue: want 10s, got %v", res.RequeueAfter)
	}
}

func TestReconcile_namespaceViolation(t *testing.T) {
	scheme := setupScheme(t)
	bdp := newBDP("default", "test", "rel", "dev")
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(bdp).
		WithObjects(bdp).
		Build()

	runner := &mockRunner{
		err: &reconcilepkg.ViolationError{
			Violations: []string{`jumi: namespace "jumi-system" not allowed`},
		},
	}
	r := &DataPlaneReconciler{
		Client:          fakeClient,
		Recorder:        record.NewFakeRecorder(10),
		Runner:          runner,
		RequeueInterval: 10 * time.Second,
	}

	// First reconcile: add finalizer.
	r.Reconcile(context.Background(), ctrl.Request{ //nolint:errcheck
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})

	// Second reconcile: ViolationError → no error returned, long requeue.
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test"},
	})
	if err != nil {
		t.Fatalf("expected nil error for violation, got: %v", err)
	}
	if res.RequeueAfter != 5*time.Minute {
		t.Errorf("requeue: want 5m, got %v", res.RequeueAfter)
	}

	// Verify Violation and Degraded conditions are set.
	var got v1alpha1.BoriDataPlane
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "test"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	checkCond := func(condType string, wantStatus v1alpha1.ConditionStatus) {
		t.Helper()
		for _, c := range got.Status.Conditions {
			if c.Type == condType {
				if c.Status != wantStatus {
					t.Errorf("condition %s: want Status=%s, got %s", condType, wantStatus, c.Status)
				}
				return
			}
		}
		t.Errorf("condition %s not found in %+v", condType, got.Status.Conditions)
	}
	checkCond(v1alpha1.ConditionViolation, v1alpha1.ConditionTrue)
	checkCond(v1alpha1.ConditionDegraded, v1alpha1.ConditionTrue)
}
