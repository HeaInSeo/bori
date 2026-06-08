package model

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BoriEnvironment is the environment definition stored in
// environments/<name>/environment.yaml inside the bori repo.
type BoriEnvironment struct {
	Name               string                    `yaml:"name"`
	Cluster            ClusterConfig             `yaml:"cluster"`
	NamespacePolicy    NamespacePolicy           `yaml:"namespacePolicy"`
	Registry           RegistryConfig            `yaml:"registry"`
	Secrets            SecretsConfig             `yaml:"secrets"`
	NetworkIntegration NetworkIntegrationProfile `yaml:"networkIntegration,omitempty" json:"networkIntegration,omitempty"`
}

// NetworkIntegrationProfile declares what connectivity/policy checks to run after
// a successful rollout. bori core does not implement checks directly — type maps
// to a verifier adapter. Omit or set type: none to skip all checks.
//
// Supported types (MVP): none, kubernetes-service
// Future: cilium, istio, linkerd
type NetworkIntegrationProfile struct {
	// Type selects the verifier adapter: none | kubernetes-service | cilium | istio
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Checks lists the capability checks to run (e.g. service-dns-call, network-policy-positive).
	// When empty, the adapter uses its default check set for the given type.
	Checks []string `yaml:"checks,omitempty" json:"checks,omitempty"`
}

// ClusterConfig describes how to reach the Kubernetes cluster.
type ClusterConfig struct {
	Kubeconfig string `yaml:"kubeconfig"`
	Context    string `yaml:"context,omitempty"`
}

// NamespacePolicy restricts which namespaces bori may write to.
// Deployments targeting a namespace not in Allowed are rejected at plan time.
type NamespacePolicy struct {
	Allowed                     []string `yaml:"allowed"`
	AllowClusterScopedResources bool     `yaml:"allowClusterScopedResources,omitempty"`
}

// RegistryConfig sets the default container registry for this environment.
type RegistryConfig struct {
	Default string `yaml:"default"`
}

// SecretsConfig controls how bori handles secrets in this environment.
type SecretsConfig struct {
	Mode      string `yaml:"mode"`      // reference-only
	Redaction string `yaml:"redaction"` // strict | relaxed
}

// LoadEnvironment parses environments/<name>/environment.yaml.
func LoadEnvironment(path string) (BoriEnvironment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BoriEnvironment{}, fmt.Errorf("read %s: %w", path, err)
	}
	var e BoriEnvironment
	if err := yaml.Unmarshal(data, &e); err != nil {
		return BoriEnvironment{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return e, nil
}

// LoadEnvironmentByName loads environments/<name>/environment.yaml from the
// given bori repo root.
func LoadEnvironmentByName(boriRoot, name string) (BoriEnvironment, error) {
	path := filepath.Join(boriRoot, "environments", name, "environment.yaml")
	return LoadEnvironment(path)
}
