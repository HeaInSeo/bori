package investigate

import (
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// Query is one authorized, registered evidence request.
type Query struct {
	Request  providers.Request
	Provider providers.Provider
	MaxAge   time.Duration
}

// Queries maps OperationalTarget UID → slot → query.
type Queries map[string]map[string]Query

// ProviderLookup resolves an exact provider identity.
type ProviderLookup interface {
	Lookup(operations.ProviderIdentity) (providers.Provider, bool)
}

// Requests derives the queryable set from an O1 snapshot built by
// opswire.Build. That snapshot already excludes unresolved targets and every
// binding a grant did not authorize, so a denied binding never reaches the
// provider registry. subjects gives each target's targetRef (its own
// namespace) and resolved UID.
func Requests(s operations.Snapshot, subjects map[string]providers.Subject, reg ProviderLookup) Queries {
	contracts := map[operations.ContractIdentity]operations.Contract{}
	for _, c := range s.Contracts {
		contracts[c.Identity] = c
	}
	out := Queries{}
	for _, t := range s.Targets {
		subj, ok := subjects[t.Identity.UID]
		if !ok || subj.ResolvedUID != t.ResolvedUID {
			continue
		}
		c, ok := contracts[t.ContractRef]
		if !ok {
			continue
		}
		for _, b := range t.AssertionBindings {
			slot, ok := slotOf(c, b.Slot)
			if !ok {
				continue
			}
			p, ok := reg.Lookup(b.Provider)
			if !ok {
				continue
			}
			if out[t.Identity.UID] == nil {
				out[t.Identity.UID] = map[string]Query{}
			}
			out[t.Identity.UID][b.Slot] = Query{
				Request: providers.Request{
					Key: operations.ApplicabilityKey{
						Target:      t.Identity,
						ResolvedUID: t.ResolvedUID,
						Contract:    t.ContractRef,
						Slot:        b.Slot,
						Provider:    b.Provider,
					},
					Subject:  subj,
					SlotType: slot.Type,
				},
				Provider: p,
				MaxAge:   slot.MaxAge,
			}
		}
	}
	return out
}

func slotOf(c operations.Contract, name string) (operations.AssertionSlot, bool) {
	for _, s := range c.Assertions {
		if s.Name == name {
			return s, true
		}
	}
	return operations.AssertionSlot{}, false
}
