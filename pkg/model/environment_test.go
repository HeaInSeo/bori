package model

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBoriEnvironmentWithoutNetworkIntegration(t *testing.T) {
	raw := `
name: kind
cluster:
  kubeconfig: /tmp/kubeconfig
namespacePolicy:
  allowed:
    - jumi-system
`
	var e BoriEnvironment
	if err := yaml.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.NetworkIntegration.Type != "" {
		t.Errorf("expected empty Type for env without networkIntegration, got %q", e.NetworkIntegration.Type)
	}
	if len(e.NetworkIntegration.Checks) != 0 {
		t.Errorf("expected nil Checks, got %v", e.NetworkIntegration.Checks)
	}
}

func TestBoriEnvironmentNetworkIntegrationTypeNone(t *testing.T) {
	raw := `
name: infra-lab
networkIntegration:
  type: none
`
	var e BoriEnvironment
	if err := yaml.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.NetworkIntegration.Type != "none" {
		t.Errorf("expected type 'none', got %q", e.NetworkIntegration.Type)
	}
}

func TestBoriEnvironmentNetworkIntegrationWithChecks(t *testing.T) {
	raw := `
name: staging
networkIntegration:
  type: kubernetes-service
  checks:
    - service-dns-call
`
	var e BoriEnvironment
	if err := yaml.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.NetworkIntegration.Type != "kubernetes-service" {
		t.Errorf("expected type 'kubernetes-service', got %q", e.NetworkIntegration.Type)
	}
	if len(e.NetworkIntegration.Checks) != 1 || e.NetworkIntegration.Checks[0] != "service-dns-call" {
		t.Errorf("unexpected checks: %v", e.NetworkIntegration.Checks)
	}
}
