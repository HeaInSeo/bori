package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/HeaInSeo/bori/pkg/operations"
)

var readAt = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func deployment(uid string, generation, observedGen int64, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web", UID: types.UID(uid), Generation: generation},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: observedGen, AvailableReplicas: available, ReadyReplicas: available},
	}
}

func kube(t *testing.T, gets *int, objs ...client.Object) *KubeStatus {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			*gets++
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	return &KubeStatus{Reader: c, Config: KubeStatusConfig{Fields: map[string]string{
		"available-replicas": "availableReplicas", "ready-replicas": "readyReplicas", "bad-field": "spec.replicas",
	}}, Now: func() time.Time { return readAt }}
}

func kreq(slot string, uid string) Request {
	r := req(slot, operations.TypeInteger)
	r.Subject.ResolvedUID = uid
	r.Key.ResolvedUID = uid
	return r
}

func TestKubeStatusReadsTypedFact(t *testing.T) {
	var gets int
	k := kube(t, &gets, deployment("w-1", 3, 3, 2))
	res := k.Observe(context.Background(), kreq("available-replicas", "w-1"))
	if res.Unavailable != "" || res.Value != operations.Int(2) || !res.ObservedAt.Equal(readAt) || gets != 1 {
		t.Fatalf("result %+v gets %d", res, gets)
	}
	if res.EvidenceRef != "k8s:Deployment/apps/web@w-1#status.availableReplicas,generation=3" {
		t.Fatalf("ref %q", res.EvidenceRef)
	}
	// Same object, field and generation: same handle (no status churn).
	if again := k.Observe(context.Background(), kreq("available-replicas", "w-1")); again.EvidenceRef != res.EvidenceRef {
		t.Fatal("evidence handle not stable")
	}
}

func TestKubeStatusZeroIsOmittedButObserved(t *testing.T) {
	var gets int
	res := kube(t, &gets, deployment("w-1", 1, 1, 0)).Observe(context.Background(), kreq("available-replicas", "w-1"))
	if res.Unavailable != "" || res.Value != operations.Int(0) {
		t.Fatalf("result %+v", res)
	}
}

func TestKubeStatusIsNotANewFactWhenItShouldNotBe(t *testing.T) {
	cases := map[string]struct {
		obj  client.Object
		req  Request
		want string
		gets int
	}{
		"recreated workload (UID mismatch)": {deployment("w-2", 1, 1, 2), kreq("available-replicas", "w-1"), "subject-uid-mismatch", 1},
		"deleted workload":                  {nil, kreq("available-replicas", "w-1"), "subject-not-found", 1},
		"status lags the current spec":      {deployment("w-1", 4, 3, 2), kreq("available-replicas", "w-1"), "status-lagging-generation", 1},
		"status never observed":             {deployment("w-1", 1, 0, 0), kreq("available-replicas", "w-1"), "status-not-observed", 1},
		"field not in the bounded set":      {deployment("w-1", 1, 1, 2), kreq("bad-field", "w-1"), "slot-not-configured", 0},
		"unmapped slot":                     {deployment("w-1", 1, 1, 2), kreq("other", "w-1"), "slot-not-configured", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var gets int
			var objs []client.Object
			if tc.obj != nil {
				objs = append(objs, tc.obj)
			}
			res := kube(t, &gets, objs...).Observe(context.Background(), tc.req)
			if res.Unavailable != tc.want || gets != tc.gets {
				t.Fatalf("result %+v gets %d", res, gets)
			}
		})
	}
}

func TestKubeStatusUnsupportedKindAndTypeAreNotRead(t *testing.T) {
	var gets int
	k := kube(t, &gets, deployment("w-1", 1, 1, 2))
	r := kreq("available-replicas", "w-1")
	r.Subject.APIVersion, r.Subject.Kind = "v1", "Secret"
	if res := k.Observe(context.Background(), r); res.Unavailable != "unsupported-kind" {
		t.Fatalf("result %+v", res)
	}
	r = kreq("available-replicas", "w-1")
	r.SlotType = operations.TypeBoolean
	if res := k.Observe(context.Background(), r); res.Unavailable != "slot-type-not-integer" {
		t.Fatalf("result %+v", res)
	}
	if gets != 0 {
		t.Fatalf("%d reads for requests that must not be dispatched", gets)
	}
}

func TestKubeStatusReadFailureIsUnavailable(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("connection refused")
		},
	}).Build()
	k := &KubeStatus{Reader: c, Config: KubeStatusConfig{Fields: map[string]string{"available-replicas": "availableReplicas"}}, Now: time.Now}
	if res := k.Observe(context.Background(), kreq("available-replicas", "w-1")); res.Unavailable != "read-failed" {
		t.Fatalf("result %+v", res)
	}
}

func TestObservationKeepsCallerKey(t *testing.T) {
	r := kreq("available-replicas", "w-1")
	o := Observation(r, Result{Unavailable: "x"}, readAt)
	if o.Key != r.Key || o.Outcome != operations.OutcomeProviderUnavailable || !o.ObservedAt.Equal(readAt) {
		t.Fatalf("observation %+v", o)
	}
}
