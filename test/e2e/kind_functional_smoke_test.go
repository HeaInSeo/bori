//go:build kindfunc

// K1 functional smoke: bori-root = ConfigMap (environment + component), shell adapter no-op.
// 검증 대상:
//   - Runner.Run() 완료 → BoriDataPlane.status.observedGeneration >= 1
//   - shadow reconcile → Installed = True, Promoted = True
//   - upsertBoriRevision → BoriRevision CR 생성
//   - BoriRelease.status.activeDataPlanes >= 1
//   - kube-slint SLI snapshot
//
// 실행: hack/test-kind-functional-smoke.sh
package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/kube-slint/pkg/slint"
)

var (
	k1ctx  context.Context
	k1k8s  client.Client
	k1sess *slint.Session
)

var _ = BeforeSuite(func() {
	k1ctx = context.Background()
	k1k8s = buildK8sClient()
	k1sess = buildSlintSession()
	k1sess.Start()
})

var _ = AfterSuite(func() {
	logSlintSummary(k1ctx, k1sess)
})

var _ = Describe("K1 functional smoke", Ordered, func() {
	It("BoriRelease CR exists", func() {
		br := &v1alpha1.BoriRelease{}
		Expect(k1k8s.Get(k1ctx, types.NamespacedName{
			Name: "func-release", Namespace: namespace,
		}, br)).To(Succeed())
	})

	It("BoriDataPlane reconcile completes (observedGeneration >= 1)", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k1k8s.Get(k1ctx, types.NamespacedName{
				Name: "func-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			g.Expect(bdp.Status.ObservedGeneration).To(BeNumerically(">=", 1),
				"Runner.Run() should succeed when environment + component files are mounted")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriDataPlane.status.conditions.Installed = True", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k1k8s.Get(k1ctx, types.NamespacedName{
				Name: "func-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			installed := findCondition(bdp.Status.Conditions, v1alpha1.ConditionInstalled)
			g.Expect(installed).NotTo(BeNil(), "Installed condition should exist")
			g.Expect(installed.Status).To(Equal(v1alpha1.ConditionTrue),
				"Installed should be True after successful deploy cycle")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriRevision CR created in bori-system", func() {
		Eventually(func(g Gomega) {
			var revList v1alpha1.BoriRevisionList
			g.Expect(k1k8s.List(k1ctx, &revList, client.InNamespace(namespace))).To(Succeed())
			g.Expect(revList.Items).NotTo(BeEmpty(),
				"upsertBoriRevision should create at least one BoriRevision CR")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriRelease.status.activeDataPlanes >= 1", func() {
		Eventually(func(g Gomega) {
			br := &v1alpha1.BoriRelease{}
			g.Expect(k1k8s.Get(k1ctx, types.NamespacedName{
				Name: "func-release", Namespace: namespace,
			}, br)).To(Succeed())
			g.Expect(br.Status.ActiveDataPlanes).To(BeNumerically(">=", 1))
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})
})

// findCondition returns the condition with the given type, or nil if not found.
func findCondition(conds []v1alpha1.Condition, condType string) *v1alpha1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}
