package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func writeConfig(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodConfig = `
http:
  allowInsecureHTTP: true
  allowPrivateNetworks: true
providers:
  - namespace: apps
    name: kube-status
    configRevision: r1
    kubernetesStatus:
      fields: {available-replicas: availableReplicas}
  - namespace: apps
    name: app-http
    configRevision: r1
    http:
      endpoints:
        - subjectUID: w-1
          url: http://127.0.0.1:8080/bori/assertions
          fields: {api-serving: serving}
`

func TestRegistryIsExactIdentity(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, goodConfig))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cfg.Build(fake.NewClientBuilder().Build(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[operations.ProviderIdentity]bool{
		{Name: "apps/kube-status", ConfigRevision: "r1"}:   true,
		{Name: "apps/app-http", ConfigRevision: "r1"}:      true,
		{Name: "apps/app-http", ConfigRevision: "r2"}:      false,
		{Name: "other/app-http", ConfigRevision: "r1"}:     false,
		{Name: "apps/kube-status/x", ConfigRevision: "r1"}: false,
	} {
		if _, ok := reg.Lookup(id); ok != want {
			t.Errorf("lookup %s = %v", id, ok)
		}
	}
}

func TestRegistryRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"unknown field":    strings.Replace(goodConfig, "configRevision: r1\n    kubernetesStatus", "configRevision: r1\n    discovery: true\n    kubernetesStatus", 1),
		"duplicate":        goodConfig + "  - namespace: apps\n    name: kube-status\n    configRevision: r1\n    kubernetesStatus: {fields: {a: availableReplicas}}\n",
		"both adapters":    goodConfig + "  - namespace: apps\n    name: x\n    configRevision: r1\n    kubernetesStatus: {fields: {a: b}}\n    http: {endpoints: []}\n",
		"no revision":      goodConfig + "  - namespace: apps\n    name: y\n    kubernetesStatus: {fields: {a: b}}\n",
		"insecure http":    strings.Replace(goodConfig, "allowInsecureHTTP: true", "allowInsecureHTTP: false", 1),
		"timeout over cap": goodConfig + "limits: {perCallTimeout: 60s}\n",
		"body over cap":    goodConfig + "limits: {maxResponseBytes: 10000000}\n",
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadConfig(writeConfig(t, c))
			if err == nil {
				_, err = cfg.Build(fake.NewClientBuilder().Build(), time.Now)
			}
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
