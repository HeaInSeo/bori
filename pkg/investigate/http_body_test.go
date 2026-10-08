package investigate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// Codex 4177407393 regression through the investigator: a held green
// followed by a malformed (trailing-data) body becomes UNKNOWN — no fallback
// to the cached success, and the accepted-value watermark does not move.
func TestMalformedHTTPBodyAfterGreenIsUnknownWithoutFallback(t *testing.T) {
	clk := &clock{t: t0}
	var mu sync.Mutex
	tail := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `{"subject":"w-svc","observedAt":%q,"values":{"serving":true}}%s`, clk.now().Format(time.RFC3339Nano), tail)
	}))
	defer srv.Close()
	cfg := &providers.Config{
		HTTP: providers.HTTPProfile{AllowInsecureHTTP: true, AllowPrivateNetworks: true},
		Providers: []providers.ProviderConfig{{Namespace: "apps", Name: "app-http", ConfigRevision: "r1",
			HTTP: &providers.HTTPConfig{Endpoints: []providers.HTTPEndpoint{{SubjectUID: "w-svc", URL: srv.URL, Fields: map[string]string{"api-serving": "serving"}}}}}},
	}
	reg, err := cfg.Build(nil, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(ReferenceLimits())
	w.clk = clk
	w.iv = New(ReferenceLimits(), clk.now)
	w.reg = &registry{ps: map[operations.ProviderIdentity]providers.Provider{httpID: mustLookup(t, reg, httpID), kubeID: w.kube}}

	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.run(context.Background())
	if s := capState(w.assess(), "serve"); s != operations.Available {
		t.Fatalf("green: serve %s", s)
	}
	key := w.queries()["t-svc"]["api-serving"].Request.Key
	markBefore := w.iv.marks[key]

	mu.Lock()
	tail = `{"subject":"w-svc","observedAt":"2026-01-01T12:00:59Z","values":{"serving":true}}`
	mu.Unlock()
	clk.advance(16 * time.Second) // due for refresh, cooldown elapsed
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.run(context.Background())

	ta := w.assess()
	cr, _ := ta.Capability("serve")
	if cr.State != operations.Unknown || cr.Reasons[0].Code != operations.ReasonProviderUnavailable {
		t.Fatalf("serve %+v after a malformed body (cached success must not be used)", cr)
	}
	if got := w.iv.marks[key]; !got.Equal(markBefore) {
		t.Fatalf("watermark moved %s → %s on a malformed body", markBefore, got)
	}
}

func mustLookup(t *testing.T, reg *providers.Registry, id operations.ProviderIdentity) providers.Provider {
	t.Helper()
	p, ok := reg.Lookup(id)
	if !ok {
		t.Fatalf("provider %s not registered", id)
	}
	return p
}
