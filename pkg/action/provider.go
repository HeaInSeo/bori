package action

import (
	"context"

	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Handoff is what BORI sends to an ActionProvider: the exact action, the
// exact bound target and contract, the approval that authorised it, and the
// key and fence the provider must use to deduplicate and to reject a stale
// sender. It carries no instruction text.
type Handoff struct {
	Key            string
	Fence          uint64
	ProposalID     string
	Digest         string
	Action         interaction.ActionRef
	Owner          string
	Target         operations.TargetIdentity
	ResolvedUID    string
	Contract       operations.ContractIdentity
	For            operations.CapabilityType
	ExpectedImpact []operations.CapabilityType
	Approval       Approval
}

// Receipt is the provider's answer to Submit. Accepted means durable
// acceptance of responsibility only (R2-B): not applied, not completed.
type Receipt struct {
	Key      string
	Fence    uint64
	Digest   string
	Accepted bool
	Ref      string
}

// ResultState is the provider-reported state of one keyed handoff.
type ResultState string

const (
	// ResultUnknown: the provider has no handoff with this key.
	ResultUnknown   ResultState = "Unknown"
	ResultPending   ResultState = "Pending"
	ResultSucceeded ResultState = "Succeeded"
	ResultFailed    ResultState = "Failed"
)

// Result is the provider's statement about one keyed handoff. It is never
// capability evidence.
type Result struct {
	Key    string
	Fence  uint64
	Digest string
	State  ResultState
	Ref    string
}

// Provider is the logical ActionProvider interface (API v0.1 §13): it
// forwards a registered action to the authoritative actor and reports on it.
// The transport is OPEN (§19).
type Provider interface {
	Submit(ctx context.Context, h Handoff) (Receipt, error)
	Result(ctx context.Context, key string) (Result, error)
}

// Registration binds a provider to the one owner it acts for and to the
// namespaces and actions it is authorised to carry out.
type Registration struct {
	Provider   Provider
	Owner      string
	Namespaces []string
	// Actions are "name@revision".
	Actions []string
}

func (r Registration) authorises(owner, namespace string, a interaction.ActionRef) bool {
	if r.Owner == "" || r.Owner != owner {
		return false
	}
	return contains(r.Namespaces, namespace) && contains(r.Actions, a.String())
}

// Registry maps a provider name to its registration.
type Registry map[string]Registration

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
