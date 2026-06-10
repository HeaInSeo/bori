//go:build kinddigest

// K2 digest smoke: imageDigest 기반 BoriRelease → planner가 digest-qualified imageRef 생성
// → --deploy-dry-run으로 revision 프로모션 → ComponentStatus.ImageDigest / DeployedImage 확인.
//
// 검증 대상:
//   - BoriRelease에 imageDigest 설정 시 API 수락
//   - Runner.Run() 완료 (--deploy-dry-run) → observedGeneration >= 1
//   - BoriDataPlane.status.components[jumi].imageDigest = sha256:aaa...
//   - BoriDataPlane.status.components[jumi].deployedImage = harbor.lab.local:5000/bori/jumi@sha256:aaa...
//   - BoriRevision CR 생성 + spec.components[jumi].imageDigest 기록
//   - BoriRelease.status.activeDataPlanes >= 1
//
// Harbor 불필요: --deploy-dry-run이 실제 kubectl set image 호출을 건너뜀.
//
// 실행: hack/test-kind-digest-smoke.sh
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

const (
	digestFixture = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	harborRef     = "harbor.lab.local:5000/bori/jumi@" + digestFixture
)

var (
	k2ctx  context.Context
	k2k8s  client.Client
	k2sess *slint.Session
)

var _ = BeforeSuite(func() {
	k2ctx = context.Background()
	k2k8s = buildK8sClient()
	k2sess = buildSlintSession()
	k2sess.Start()
})

var _ = AfterSuite(func() {
	logSlintSummary(k2ctx, k2sess)
})

var _ = Describe("K2 digest smoke", Ordered, func() {
	It("BoriRelease with imageDigest exists", func() {
		br := &v1alpha1.BoriRelease{}
		Expect(k2k8s.Get(k2ctx, types.NamespacedName{
			Name: "digest-release", Namespace: namespace,
		}, br)).To(Succeed())
		Expect(br.Spec.Components).NotTo(BeEmpty())
		Expect(br.Spec.Components[0].ImageDigest).To(Equal(digestFixture))
	})

	It("BoriDataPlane reconcile completes (observedGeneration >= 1)", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k2k8s.Get(k2ctx, types.NamespacedName{
				Name: "digest-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			g.Expect(bdp.Status.ObservedGeneration).To(BeNumerically(">=", 1),
				"Runner.Run() should succeed with --deploy-dry-run")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriDataPlane.status.conditions.Installed = True", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k2k8s.Get(k2ctx, types.NamespacedName{
				Name: "digest-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			installed := findCondition(bdp.Status.Conditions, v1alpha1.ConditionInstalled)
			g.Expect(installed).NotTo(BeNil(), "Installed condition should exist")
			g.Expect(installed.Status).To(Equal(v1alpha1.ConditionTrue))
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("ComponentStatus.imageDigest reflects BoriRelease.imageDigest", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k2k8s.Get(k2ctx, types.NamespacedName{
				Name: "digest-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			g.Expect(bdp.Status.Components).NotTo(BeEmpty(),
				"status.components should be populated after deploy-dry-run")
			var jumiStatus *v1alpha1.ComponentStatus
			for i := range bdp.Status.Components {
				if bdp.Status.Components[i].Name == "jumi" {
					jumiStatus = &bdp.Status.Components[i]
				}
			}
			g.Expect(jumiStatus).NotTo(BeNil(), "jumi ComponentStatus should exist")
			g.Expect(jumiStatus.ImageDigest).To(Equal(digestFixture),
				"ImageDigest should match BoriRelease.spec.components.imageDigest")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("ComponentStatus.deployedImage is harbor digest-qualified ref", func() {
		Eventually(func(g Gomega) {
			bdp := &v1alpha1.BoriDataPlane{}
			g.Expect(k2k8s.Get(k2ctx, types.NamespacedName{
				Name: "digest-dp", Namespace: namespace,
			}, bdp)).To(Succeed())
			var jumiStatus *v1alpha1.ComponentStatus
			for i := range bdp.Status.Components {
				if bdp.Status.Components[i].Name == "jumi" {
					jumiStatus = &bdp.Status.Components[i]
				}
			}
			g.Expect(jumiStatus).NotTo(BeNil())
			g.Expect(jumiStatus.DeployedImage).To(Equal(harborRef),
				"DeployedImage should be harbor.lab.local:5000/bori/jumi@sha256:aaa...")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriRevision CR records imageDigest", func() {
		Eventually(func(g Gomega) {
			var revList v1alpha1.BoriRevisionList
			g.Expect(k2k8s.List(k2ctx, &revList, client.InNamespace(namespace))).To(Succeed())
			g.Expect(revList.Items).NotTo(BeEmpty(), "BoriRevision CR should be created")
			found := false
			for _, rev := range revList.Items {
				for _, comp := range rev.Spec.Components {
					if comp.Name == "jumi" && comp.ImageDigest == digestFixture {
						found = true
					}
				}
			}
			g.Expect(found).To(BeTrue(),
				"BoriRevision should record jumi imageDigest=%s", digestFixture)
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("BoriRelease.status.activeDataPlanes >= 1", func() {
		Eventually(func(g Gomega) {
			br := &v1alpha1.BoriRelease{}
			g.Expect(k2k8s.Get(k2ctx, types.NamespacedName{
				Name: "digest-release", Namespace: namespace,
			}, br)).To(Succeed())
			g.Expect(br.Status.ActiveDataPlanes).To(BeNumerically(">=", 1))
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})
})
