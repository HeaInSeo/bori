package providers

import (
	"errors"
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// Registry maps an exact provider identity (namespace/name + configRevision)
// to its adapter. A binding whose identity is not registered — including a
// binding whose configRevision differs from the registered one — has no
// provider and is never dispatched.
type Registry struct {
	providers map[operations.ProviderIdentity]Provider
}

// NewRegistry returns a registry over the given providers.
func NewRegistry(ps map[operations.ProviderIdentity]Provider) *Registry {
	r := &Registry{providers: map[operations.ProviderIdentity]Provider{}}
	for k, v := range ps {
		r.providers[k] = v
	}
	return r
}

// Lookup returns the provider registered under exactly id.
func (r *Registry) Lookup(id operations.ProviderIdentity) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[id]
	return p, ok
}

// Config is the process-local reference configuration file.
type Config struct {
	HTTP      HTTPProfile      `json:"http"`
	Limits    *LimitsOverride  `json:"limits,omitempty"`
	Providers []ProviderConfig `json:"providers"`
}

// ProviderConfig registers one provider. Exactly one adapter section is set.
type ProviderConfig struct {
	Namespace        string            `json:"namespace"`
	Name             string            `json:"name"`
	ConfigRevision   string            `json:"configRevision"`
	KubernetesStatus *KubeStatusConfig `json:"kubernetesStatus,omitempty"`
	HTTP             *HTTPConfig       `json:"http,omitempty"`
}

// LimitsOverride optionally changes reference limits within hard caps.
type LimitsOverride struct {
	PerCallTimeout  string `json:"perCallTimeout,omitempty"`
	MaxResponseByte int64  `json:"maxResponseBytes,omitempty"`
}

// Hard caps for the HTTP adapter regardless of configuration.
const (
	DefaultPerCallTimeout = 2 * time.Second
	MaxPerCallTimeout     = 10 * time.Second
	DefaultMaxBodyBytes   = 16 << 10
	MaxBodyBytesCap       = 256 << 10
)

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalStrict(b, &c); err != nil {
		return nil, fmt.Errorf("provider config: %w", err)
	}
	return &c, nil
}

// Build constructs the registry. The Kubernetes adapter reads through reader,
// which should be an uncached reader so each query is one bounded GET.
func (c *Config) Build(reader client.Reader, now func() time.Time) (*Registry, error) {
	timeout, maxBody := DefaultPerCallTimeout, int64(DefaultMaxBodyBytes)
	if c.Limits != nil {
		if c.Limits.PerCallTimeout != "" {
			d, err := time.ParseDuration(c.Limits.PerCallTimeout)
			if err != nil || d <= 0 || d > MaxPerCallTimeout {
				return nil, fmt.Errorf("perCallTimeout must be in (0, %s]", MaxPerCallTimeout)
			}
			timeout = d
		}
		if c.Limits.MaxResponseByte != 0 {
			if c.Limits.MaxResponseByte < 0 || c.Limits.MaxResponseByte > MaxBodyBytesCap {
				return nil, fmt.Errorf("maxResponseBytes must be in (0, %d]", MaxBodyBytesCap)
			}
			maxBody = c.Limits.MaxResponseByte
		}
	}
	ps := map[operations.ProviderIdentity]Provider{}
	for _, pc := range c.Providers {
		if pc.Namespace == "" || pc.Name == "" || pc.ConfigRevision == "" {
			return nil, errors.New("provider needs namespace, name and configRevision")
		}
		id := operations.ProviderIdentity{Name: pc.Namespace + "/" + pc.Name, ConfigRevision: pc.ConfigRevision}
		if _, dup := ps[id]; dup {
			return nil, fmt.Errorf("provider %s registered twice", id)
		}
		switch {
		case pc.KubernetesStatus != nil && pc.HTTP == nil:
			if reader == nil {
				return nil, errors.New("kubernetesStatus provider needs an API reader")
			}
			ps[id] = &KubeStatus{Reader: reader, Config: *pc.KubernetesStatus, Now: now}
		case pc.HTTP != nil && pc.KubernetesStatus == nil:
			h, err := NewHTTPTyped(id.Name, *pc.HTTP, c.HTTP, timeout, maxBody)
			if err != nil {
				return nil, err
			}
			ps[id] = h
		default:
			return nil, fmt.Errorf("provider %s must set exactly one of kubernetesStatus or http", id)
		}
	}
	return NewRegistry(ps), nil
}
