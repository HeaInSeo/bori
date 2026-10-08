package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// HTTPProfile is the destination policy shared by all HTTP providers.
type HTTPProfile struct {
	// AllowInsecureHTTP permits http:// endpoints (test/lab only).
	AllowInsecureHTTP bool `json:"allowInsecureHTTP"`
	// AllowPrivateNetworks permits loopback, private and link-local
	// destinations (test/lab or in-cluster only).
	AllowPrivateNetworks bool `json:"allowPrivateNetworks"`
}

// HTTPEndpoint pins one endpoint to one subject. The endpoint is queried only
// for a request whose resolved workload UID equals SubjectUID, and its
// response must name the same subject.
type HTTPEndpoint struct {
	SubjectUID string `json:"subjectUID"`
	URL        string `json:"url"`
	// Fields maps a slot name to a key of the response "values" object.
	Fields map[string]string `json:"fields"`
}

// HTTPConfig lists the endpoints of one HTTP provider.
type HTTPConfig struct {
	Endpoints []HTTPEndpoint `json:"endpoints"`
}

// typedResponse is the bounded response format. Unknown fields are ignored;
// nothing in the payload can redirect the request, rename the provider or the
// subject, or change any limit.
type typedResponse struct {
	Subject    string                     `json:"subject"`
	ObservedAt string                     `json:"observedAt"`
	ValidUntil string                     `json:"validUntil"`
	Values     map[string]json.RawMessage `json:"values"`
}

// HTTPTyped reads one typed value from a pinned endpoint.
type HTTPTyped struct {
	providerName string
	endpoints    []HTTPEndpoint
	client       *http.Client
	maxBody      int64
}

// NewHTTPTyped validates the endpoints against the profile and builds a
// client that cannot leave them: no proxy (environment ignored), no
// redirects, TLS verification on, destination re-checked at dial time.
func NewHTTPTyped(providerName string, cfg HTTPConfig, profile HTTPProfile, timeout time.Duration, maxBody int64) (*HTTPTyped, error) {
	if timeout <= 0 || maxBody <= 0 {
		return nil, errors.New("http provider: timeout and body limit must be positive")
	}
	for _, ep := range cfg.Endpoints {
		if err := validateEndpoint(ep, profile); err != nil {
			return nil, fmt.Errorf("http provider %s: %w", providerName, err)
		}
	}
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("destination %q is not an IP", host)
			}
			if !profile.AllowPrivateNetworks && restrictedIP(ip) {
				return fmt.Errorf("destination %s is not permitted by the profile", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    true,
	}
	return &HTTPTyped{
		providerName: providerName,
		endpoints:    append([]HTTPEndpoint(nil), cfg.Endpoints...),
		maxBody:      maxBody,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func validateEndpoint(ep HTTPEndpoint, profile HTTPProfile) error {
	if ep.SubjectUID == "" || len(ep.Fields) == 0 {
		return errors.New("endpoint needs subjectUID and fields")
	}
	u, err := url.Parse(ep.URL)
	if err != nil {
		return fmt.Errorf("endpoint url: %w", err)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && profile.AllowInsecureHTTP:
	default:
		return fmt.Errorf("endpoint scheme %q not permitted", u.Scheme)
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("endpoint %q must have a host and no userinfo or fragment", ep.URL)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !profile.AllowPrivateNetworks && restrictedIP(ip) {
		return fmt.Errorf("endpoint %q targets a restricted address", ep.URL)
	}
	return nil
}

func restrictedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

// Observe performs at most one GET. A request whose subject has no pinned
// endpoint, or whose slot is not mapped, is not dispatched.
func (h *HTTPTyped) Observe(ctx context.Context, req Request) Result {
	var ep *HTTPEndpoint
	for i := range h.endpoints {
		if h.endpoints[i].SubjectUID == req.Subject.ResolvedUID {
			ep = &h.endpoints[i]
			break
		}
	}
	if ep == nil {
		return Result{Unavailable: "no-endpoint-for-subject"}
	}
	field, ok := ep.Fields[req.Key.Slot]
	if !ok {
		return Result{Unavailable: "slot-not-configured"}
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.URL, nil)
	if err != nil {
		return Result{Unavailable: "request-invalid"}
	}
	hreq.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(hreq)
	if err != nil {
		return Result{Unavailable: "transport-error"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Unavailable: fmt.Sprintf("http-status-%d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, h.maxBody+1))
	if err != nil {
		return Result{Unavailable: "read-failed"}
	}
	if int64(len(body)) > h.maxBody {
		return Result{Unavailable: "body-too-large"}
	}

	var tr typedResponse
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&tr); err != nil {
		return Result{Unavailable: "malformed-response"}
	}
	// The whole body must be that one object (plus whitespace): trailing
	// garbage or a second JSON value makes the response ambiguous, so the
	// first object is never promoted to evidence.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return Result{Unavailable: "malformed-response"}
	}
	if tr.Subject != ep.SubjectUID {
		return Result{Unavailable: "subject-mismatch"}
	}
	observedAt, err := time.Parse(time.RFC3339Nano, tr.ObservedAt)
	if err != nil {
		return Result{Unavailable: "malformed-observedAt"}
	}
	var validUntil time.Time
	if tr.ValidUntil != "" {
		if validUntil, err = time.Parse(time.RFC3339Nano, tr.ValidUntil); err != nil {
			return Result{Unavailable: "malformed-validUntil"}
		}
	}
	raw, ok := tr.Values[field]
	if !ok {
		return Result{Unavailable: "field-missing"}
	}
	v, ok := typedValue(raw)
	if !ok {
		return Result{Unavailable: "malformed-value"}
	}
	return Result{
		Value:       v,
		ObservedAt:  observedAt,
		ValidUntil:  validUntil,
		EvidenceRef: "http:" + h.providerName + "#" + field,
	}
}

// typedValue maps a JSON scalar to an O1 value without coercion: true/false,
// an integral number, or a string. The slot type is checked by O1.
func typedValue(raw json.RawMessage) (operations.Value, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return operations.Value{}, false
	}
	switch v := x.(type) {
	case bool:
		return operations.Bool(v), true
	case string:
		return operations.String(v), true
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			return operations.Value{}, false
		}
		i, err := v.Int64()
		if err != nil {
			return operations.Value{}, false
		}
		return operations.Int(i), true
	}
	return operations.Value{}, false
}
