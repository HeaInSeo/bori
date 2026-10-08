package investigate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// Codex 4166341274 regression: the configured perCallTimeout bounds the
// investigator's calls to both providers, below and above the 2s default.

func realWorld(t *testing.T, cfg *providers.Config, reader client.Reader) (*Investigator, operations.Snapshot, Queries) {
	t.Helper()
	reg, err := cfg.Build(reader, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := ForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := baseSnapshot()
	subj := map[string]providers.Subject{"t-svc": {Namespace: "apps", APIVersion: "apps/v1", Kind: "Deployment", Name: "svc", ResolvedUID: "w-svc"}}
	return New(limits, time.Now), base, Requests(base, subj, reg)
}

func slotStatus(iv *Investigator, base operations.Snapshot, slot string) operations.EvidenceStatus {
	s := base
	s.At = time.Now()
	s.Observations = iv.Observations()
	a, _ := operations.Evaluate(s)
	ta, _ := a.Target("t-svc")
	for _, e := range ta.Evidence {
		if e.Slot == slot {
			return e.Status
		}
	}
	return ""
}

func TestConfiguredTimeoutAboveDefaultIsHonoured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2500 * time.Millisecond) // beyond the 2s default, within 3s
		fmt.Fprintf(w, `{"subject":"w-svc","observedAt":%q,"values":{"serving":true}}`, time.Now().UTC().Format(time.RFC3339Nano))
	}))
	defer srv.Close()
	cfg := &providers.Config{
		HTTP:   providers.HTTPProfile{AllowInsecureHTTP: true, AllowPrivateNetworks: true},
		Limits: &providers.LimitsOverride{PerCallTimeout: "3s"},
		Providers: []providers.ProviderConfig{{Namespace: "apps", Name: "app-http", ConfigRevision: "r1",
			HTTP: &providers.HTTPConfig{Endpoints: []providers.HTTPEndpoint{{SubjectUID: "w-svc", URL: srv.URL, Fields: map[string]string{"api-serving": "serving"}}}}}},
	}
	iv, base, qs := realWorld(t, cfg, nil)
	if iv.limits.PerCallTimeout != 3*time.Second {
		t.Fatalf("investigator per-call timeout %s", iv.limits.PerCallTimeout)
	}
	iv.Run(context.Background(), base, qs)
	if st := slotStatus(iv, base, "api-serving"); st != operations.EvidenceCurrent {
		t.Fatalf("answer within the configured 3s was %s (discarded by a 2s default?)", st)
	}
	if iv.Stats().LateDiscarded != 0 {
		t.Fatal("in-time answer discarded as late")
	}
}

func TestConfiguredTimeoutBelowDefaultBoundsKubernetesCalls(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	slow := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			select { // an API server answering after 1s
			case <-time.After(time.Second):
				return fmt.Errorf("unexpectedly not cancelled")
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}).Build()
	cfg := &providers.Config{
		Limits: &providers.LimitsOverride{PerCallTimeout: "300ms"},
		Providers: []providers.ProviderConfig{{Namespace: "apps", Name: "kube-status", ConfigRevision: "r1",
			KubernetesStatus: &providers.KubeStatusConfig{Fields: map[string]string{"available-replicas": "availableReplicas", "ready-replicas": "readyReplicas"}}}},
	}
	iv, base, qs := realWorld(t, cfg, slow)
	if iv.limits.PerCallTimeout != 300*time.Millisecond {
		t.Fatalf("investigator per-call timeout %s", iv.limits.PerCallTimeout)
	}
	start := time.Now()
	iv.Run(context.Background(), base, qs)
	// Two Kubernetes slots, each bounded by 300ms rather than 1s or 2s.
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Fatalf("calls not bounded by the configured 300ms: %s", el)
	}
	for _, slot := range []string{"available-replicas", "ready-replicas"} {
		if st := slotStatus(iv, base, slot); st != operations.EvidenceProviderUnavailable {
			t.Fatalf("%s evidence %s", slot, st)
		}
	}
}
