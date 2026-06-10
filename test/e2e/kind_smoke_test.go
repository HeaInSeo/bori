//go:build kind

// K0 boot smoke: operator 기동, /metrics, BoriReleaseReconciler counts, finalizer 설정 확인.
// bori-root = emptyDir → planner가 environment 파일을 찾지 못해 Runner.Run() 실패.
// K0는 "operator가 뜨고 API가 동작한다"를 검증한다; 실제 reconcile 완료는 K1 범위다.
//
// K1 functional smoke (BoriRevision 생성): hack/test-kind-functional-smoke.sh
package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/kube-slint/pkg/slint"
)

var (
	k0ctx  context.Context
	k0k8s  client.Client
	k0sess *slint.Session
)

var _ = BeforeSuite(func() {
	k0ctx = context.Background()
	k0k8s = buildK8sClient()
	k0sess = buildSlintSession()
	k0sess.Start()
})

var _ = AfterSuite(func() {
	logSlintSummary(k0ctx, k0sess)
})

var _ = Describe("K0 boot smoke", Ordered, func() {
	It("BoriRelease CR exists", func() {
		br := &v1alpha1.BoriRelease{}
		Expect(k0k8s.Get(k0ctx, types.NamespacedName{
			Name: "smoke-release", Namespace: namespace,
		}, br)).To(Succeed())
		Expect(br.Name).To(Equal("smoke-release"))
	})

	It("BoriDataPlane CR exists", func() {
		bdp := &v1alpha1.BoriDataPlane{}
		Expect(k0k8s.Get(k0ctx, types.NamespacedName{
			Name: "smoke-dp", Namespace: namespace,
		}, bdp)).To(Succeed())
	})

	// DataPlane controller adds finalizer in the first reconcile pass (before Runner.Run()).
	// Waiting for the finalizer confirms the controller loop is running.
	It("BoriDataPlane finalizer is set by controller", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k0k8s.Get(k0ctx, types.NamespacedName{
				Name: "smoke-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			g.Expect(controllerutil.ContainsFinalizer(bdp, "bori.dev/cleanup")).To(BeTrue(),
				"finalizer bori.dev/cleanup should be set")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	// BoriReleaseReconciler counts referencing dataplanes independently of reconcile success.
	It("BoriRelease.status.activeDataPlanes >= 1", func() {
		Eventually(func(g Gomega) {
			br := &v1alpha1.BoriRelease{}
			g.Expect(k0k8s.Get(k0ctx, types.NamespacedName{
				Name: "smoke-release", Namespace: namespace,
			}, br)).To(Succeed())
			g.Expect(br.Status.ActiveDataPlanes).To(BeNumerically(">=", 1),
				"BoriReleaseReconciler should count smoke-dp")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	// In K0, Runner.Run() always fails (environment file not found in emptyDir).
	// ObservedGeneration stays 0. This is expected and documented behavior.
	It("BoriDataPlane.status.observedGeneration is 0 in K0 (no environment file)", func() {
		// Short wait to confirm it doesn't accidentally succeed.
		Consistently(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k0k8s.Get(k0ctx, types.NamespacedName{
				Name: "smoke-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			g.Expect(bdp.Status.ObservedGeneration).To(BeZero(),
				"K0 emptyDir has no environment file — reconcile never completes")
		}, 10*time.Second, 3*time.Second).Should(Succeed())
	})
})
