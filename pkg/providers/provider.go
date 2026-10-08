// Package providers holds the O3 reference evidence providers: a Kubernetes
// status adapter and an HTTP typed-response adapter, registered through an
// explicit, process-local configuration.
//
// REFERENCE PROFILE — NOT A PROTOCOL. This is a bounded private experiment.
// It does not define the provider transport, discovery or registration API,
// nor an evidence URI/integrity standard; those remain OPEN.
//
// A provider answers exactly one Request at a time. The Request carries the
// full O1 applicability key, which is owned by the caller: nothing a provider
// reads (an API object, an HTTP payload) can change the key, the subject, the
// destination or the budget. A provider never interprets its fact as
// capability truth; pkg/operations does that.
package providers

import (
	"context"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// Subject is the exact object a request is about. Its namespace is always the
// OperationalTarget's own namespace.
type Subject struct {
	Namespace   string
	APIVersion  string
	Kind        string
	Name        string
	ResolvedUID string
}

// Request asks one provider for one assertion slot of one exact binding.
type Request struct {
	Key      operations.ApplicabilityKey
	Subject  Subject
	SlotType operations.ValueType
}

// Result is a provider's answer. When Unavailable is set the provider could
// not produce a valid current fact (read failure, identity mismatch,
// malformed response, ...); this is never evidence about the application.
type Result struct {
	Value       operations.Value
	ObservedAt  time.Time
	ValidUntil  time.Time
	EvidenceRef string
	Unavailable string
}

// Provider produces typed facts. Implementations perform exactly one bounded
// I/O operation per call (no retries, no redirects, no fan-out) and honour
// ctx cancellation.
type Provider interface {
	Observe(ctx context.Context, req Request) Result
}

// Observation converts a result into an O1 observation keyed by the
// request's caller-owned key. A provider-unavailable statement is stamped
// with the caller's clock reading taken after the call (at); a value keeps
// the provider's own ObservedAt and is never restamped.
func Observation(req Request, res Result, at time.Time) operations.Observation {
	if res.Unavailable != "" {
		return operations.Observation{
			Key:        req.Key,
			Outcome:    operations.OutcomeProviderUnavailable,
			ObservedAt: at,
		}
	}
	return operations.Observation{
		Key:         req.Key,
		Outcome:     operations.OutcomeValue,
		Value:       res.Value,
		ObservedAt:  res.ObservedAt,
		ValidUntil:  res.ValidUntil,
		EvidenceRef: res.EvidenceRef,
	}
}
