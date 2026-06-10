//go:build kind || kindfunc || kinddigest

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
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

func buildK8sClient() client.Client {
	GinkgoHelper()
	kubeconfig := os.Getenv("KUBECONFIG")
	Expect(kubeconfig).NotTo(BeEmpty(), "KUBECONFIG must be set — run via hack/test-kind-*-smoke.sh")

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred(), "build kubeconfig")

	s := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
	Expect(v1alpha1.AddToScheme(s)).To(Succeed())

	c, err := client.New(cfg, client.Options{Scheme: s})
	Expect(err).NotTo(HaveOccurred(), "build k8s client")
	return c
}

// e2eArtifactsDir returns the directory where e2e artifacts are written.
// BORI_E2E_ARTIFACTS_DIR overrides the default so shell scripts can route
// sli-summary.json into the tier-specific path (artifacts/kind, artifacts/kind-func).
func e2eArtifactsDir() string {
	if d := os.Getenv("BORI_E2E_ARTIFACTS_DIR"); d != "" {
		return d
	}
	return "artifacts"
}

func buildSlintSession() *slint.Session {
	GinkgoHelper()
	token, _ := slint.ReadServiceAccountTokenFromEnv("SLINT_SA_TOKEN", "")
	if token == "" {
		GinkgoWriter.Println("SLINT_SA_TOKEN not set — kube-slint will attempt measurement without auth")
	}
	dir := e2eArtifactsDir()
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	return slint.NewSession(slint.SessionConfig{
		Namespace:             namespace,
		MetricsServiceName:    metricsService,
		ServiceAccountName:    "kube-slint",
		Token:                 token,
		ArtifactsDir:          dir,
		Specs:                 slint.DefaultSpecs(),
		ServiceURLFormat:      slint.ServiceURLHTTP,
		TLSInsecureSkipVerify: false,
		CurlImage:             curlImage,
	})
}

func logSlintSummary(ctx context.Context, sess *slint.Session) {
	GinkgoHelper()
	if sess == nil {
		return
	}
	sum, err := sess.End(ctx)
	if err != nil {
		GinkgoWriter.Printf("kube-slint End() warning (non-fatal): %v\n", err)
	}
	if sum == nil {
		GinkgoWriter.Println("kube-slint: no summary produced")
		return
	}
	GinkgoWriter.Printf("reliability: %s\n", sum.Reliability.CollectionStatus)
	for _, r := range sum.Results {
		val := "<nil>"
		if r.Value != nil {
			val = fmt.Sprintf("%v", *r.Value)
		}
		GinkgoWriter.Printf("  %-45s status=%-12s value=%s\n", r.ID, r.Status, val)
	}
	summaryPath := filepath.Join(e2eArtifactsDir(), "sli-summary.json")
	if _, statErr := os.Stat(summaryPath); statErr == nil {
		GinkgoWriter.Printf("artifact written: %s\n", summaryPath)
	}
}
