//go:build kind

// Package e2e contains kind smoke tests for the bori operator.
//
// Prerequisites (handled by hack/test-kind-smoke.sh):
//
//	kind create cluster --name bori-smoke
//	docker build -t bori-operator:dev .
//	kind load docker-image bori-operator:dev --name bori-smoke
//	kubectl apply -f config/crd/ config/operator/namespace.yaml config/rbac/
//	kubectl apply -f test/e2e/manifests/
//	kubectl -n bori-system apply -f config/operator/configmap.yaml
//	kubectl -n bori-system rollout status deployment/bori-operator --timeout=90s
//	kubectl -n bori-system apply -f test/e2e/fixtures/
//
// Run directly (cluster must be ready):
//
//	KUBECONFIG=$(kind get kubeconfig --name bori-smoke) \
//	SLINT_SA_TOKEN=$(kubectl -n bori-system create token kube-slint --duration=1h) \
//	  go test -tags kind -v -timeout 300s ./test/e2e/
package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/kube-slint/pkg/slint"
)

const (
	namespace        = "bori-system"
	metricsService   = "bori-operator-metrics"
	curlImage        = "curlimages/curl:8.6.0"
	reconcileTimeout = 90 * time.Second
	pollInterval     = 3 * time.Second
)

// TestKindSmoke is the main smoke test suite run by hack/test-kind-smoke.sh.
func TestKindSmoke(t *testing.T) {
	ctx := context.Background()
	k8s := buildClient(t)

	// kube-slint: workload 전 snapshot 시작
	sess := buildSlintSession(t)
	sess.Start()

	t.Run("BoriRelease_exists", func(t *testing.T) {
		br := &v1alpha1.BoriRelease{}
		if err := k8s.Get(ctx, types.NamespacedName{Name: "smoke-release", Namespace: namespace}, br); err != nil {
			t.Fatalf("BoriRelease smoke-release not found: %v", err)
		}
		t.Logf("BoriRelease found: %s", br.Name)
	})

	t.Run("BoriDataPlane_reconciled", func(t *testing.T) {
		var lastGen int64
		err := wait.PollUntilContextTimeout(ctx, pollInterval, reconcileTimeout, true,
			func(ctx context.Context) (bool, error) {
				bdp := &v1alpha1.BoriDataPlane{}
				if err := k8s.Get(ctx, types.NamespacedName{Name: "smoke-dp", Namespace: namespace}, bdp); err != nil {
					return false, nil
				}
				lastGen = bdp.Status.ObservedGeneration
				return lastGen >= 1, nil
			})
		if err != nil {
			t.Fatalf("BoriDataPlane not reconciled within %s (last observedGeneration=%d): %v",
				reconcileTimeout, lastGen, err)
		}
		t.Logf("reconciled: observedGeneration=%d", lastGen)
	})

	t.Run("BoriDataPlane_has_conditions", func(t *testing.T) {
		bdp := &v1alpha1.BoriDataPlane{}
		if err := k8s.Get(ctx, types.NamespacedName{Name: "smoke-dp", Namespace: namespace}, bdp); err != nil {
			t.Fatalf("get BoriDataPlane: %v", err)
		}
		if len(bdp.Status.Conditions) == 0 {
			t.Fatal("expected at least one status.condition, got none")
		}
		for _, c := range bdp.Status.Conditions {
			t.Logf("  condition type=%-12s status=%-8s reason=%s", c.Type, c.Status, c.Reason)
		}
	})

	t.Run("BoriRelease_activeDataPlanes", func(t *testing.T) {
		var got int32
		err := wait.PollUntilContextTimeout(ctx, pollInterval, reconcileTimeout, true,
			func(ctx context.Context) (bool, error) {
				br := &v1alpha1.BoriRelease{}
				if err := k8s.Get(ctx, types.NamespacedName{Name: "smoke-release", Namespace: namespace}, br); err != nil {
					return false, nil
				}
				got = br.Status.ActiveDataPlanes
				return got >= 1, nil
			})
		if err != nil {
			t.Fatalf("BoriRelease.status.activeDataPlanes did not reach 1 within %s (got %d): %v",
				reconcileTimeout, got, err)
		}
		t.Logf("activeDataPlanes=%d", got)
	})

	// kube-slint: workload 후 snapshot + sli-summary.json 생성
	// hard fail 없음 — artifact 생성이 목적
	t.Run("kube_slint_summary", func(t *testing.T) {
		sum, err := sess.End(ctx)
		if err != nil {
			t.Logf("kube-slint End() warning (non-fatal): %v", err)
		}
		if sum == nil {
			t.Log("kube-slint: no summary produced")
			return
		}
		t.Logf("reliability: %s", sum.Reliability.CollectionStatus)
		for _, r := range sum.Results {
			val := "<nil>"
			if r.Value != nil {
				val = fmt.Sprintf("%v", *r.Value)
			}
			t.Logf("  %-45s status=%-12s value=%s", r.ID, r.Status, val)
		}
		if _, statErr := os.Stat("artifacts/sli-summary.json"); statErr == nil {
			t.Log("artifact written: artifacts/sli-summary.json")
		}
	})
}

func buildClient(t *testing.T) client.Client {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Fatal("KUBECONFIG is not set — run via hack/test-kind-smoke.sh")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("build k8s client: %v", err)
	}
	return c
}

// buildSlintSession creates a kube-slint session.
// SLINT_SA_TOKEN이 없으면 측정은 non-fatal로 skip된다.
func buildSlintSession(t *testing.T) *slint.Session {
	t.Helper()
	token, _ := slint.ReadServiceAccountTokenFromEnv("SLINT_SA_TOKEN", "")
	if token == "" {
		t.Log("SLINT_SA_TOKEN not set — kube-slint will attempt measurement without auth")
	}
	if err := os.MkdirAll("artifacts", 0o755); err != nil {
		t.Logf("mkdir artifacts: %v", err)
	}
	return slint.NewSession(slint.SessionConfig{
		Namespace:             namespace,
		MetricsServiceName:    metricsService,
		ServiceAccountName:    "kube-slint",
		Token:                 token,
		ArtifactsDir:          "artifacts",
		Specs:                 slint.DefaultSpecs(),
		ServiceURLFormat:      slint.ServiceURLHTTP,
		TLSInsecureSkipVerify: false,
		CurlImage:             curlImage,
	})
}
