// Package e2e contains operator integration tests.
// Build tags select which suite runs:
//
//	kind        → K0 boot smoke   (hack/test-kind-boot-smoke.sh)
//	kindfunc    → K1 functional smoke  (hack/test-kind-functional-smoke.sh)
//	kinddigest  → K2 digest smoke  (hack/test-kind-digest-smoke.sh)
package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBoriOperatorE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bori Operator E2E")
}
