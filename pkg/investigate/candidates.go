package investigate

import (
	"sort"
	"strings"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// CandidateStatus is the state of a hypothesis. It is never a fact.
type CandidateStatus string

const (
	// Open: the referenced input is unproven; a fact could decide it.
	Open CandidateStatus = "Open"
	// Maintained: current facts are consistent with the hypothesis (the
	// input is proven unmet). It remains a hypothesis, not a confirmed cause.
	Maintained CandidateStatus = "Maintained"
	// Refuted: a current fact proves the input accepted.
	Refuted CandidateStatus = "Refuted"
)

// CandidateKind is the kind of input a hypothesis names.
type CandidateKind string

const (
	KindAssertion  CandidateKind = "assertion"
	KindDependency CandidateKind = "dependency"
	KindEnvelope   CandidateKind = "envelope"
	KindLocal      CandidateKind = "local-capability"
)

// Candidate hypothesizes that one referenced input explains why a capability
// (or the selected envelope) is not AVAILABLE/SATISFIED.
type Candidate struct {
	ID     string
	Kind   CandidateKind
	Ref    string
	Status CandidateStatus
}

// unmetCodes are the O1 reasons meaning "this input is proven unmet".
var unmetCodes = map[operations.ReasonCode]bool{
	operations.ReasonPredicateUnmet:  true,
	operations.ReasonDependencyUnmet: true,
	operations.ReasonEnvelopeUnmet:   true,
	operations.ReasonLocalUnmet:      true,
}

// deriveCandidates reads hypotheses off the O1 assessment only: status comes
// from O1's own reasons for the input (unmet reason → Maintained, any other
// reason → Open, no reason → Refuted). It adds no evaluation rule. The list is
// sorted and truncated to max.
func deriveCandidates(ta operations.TargetAssessment, c operations.Contract, max int) []Candidate {
	var out []Candidate
	for _, cr := range ta.Capabilities {
		if cr.State == operations.Available {
			continue
		}
		cp, ok := capabilityOf(c, cr.Type)
		if !ok {
			continue
		}
		for _, r := range cp.Requirements {
			kind, ref, subject := requirementRef(r)
			id := cr.Type.String() + "/" + string(kind) + ":" + ref
			out = append(out, Candidate{ID: id, Kind: kind, Ref: ref, Status: statusFrom(cr.Reasons, subject)})
			if kind != KindEnvelope {
				continue
			}
			// The envelope's own predicates are the queryable inputs.
			env, _ := envelopeOf(c, ref)
			reasons := envelopeReasons(ta, ref)
			for _, p := range env.Requirements {
				out = append(out, Candidate{
					ID:     id + "/" + string(KindAssertion) + ":" + p.Assertion,
					Kind:   KindAssertion,
					Ref:    p.Assertion,
					Status: statusFrom(reasons, "assertion:"+p.Assertion),
				})
			}
		}
	}
	if sel := ta.EnvelopeSelection; sel != "" {
		for _, er := range ta.Envelopes {
			if er.Name != sel || er.State == operations.Satisfied {
				continue
			}
			env, _ := envelopeOf(c, sel)
			for _, p := range env.Requirements {
				out = append(out, Candidate{
					ID:     "envelope:" + sel + "/" + string(KindAssertion) + ":" + p.Assertion,
					Kind:   KindAssertion,
					Ref:    p.Assertion,
					Status: statusFrom(er.Reasons, "assertion:"+p.Assertion),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	out = dedupe(out)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func envelopeReasons(ta operations.TargetAssessment, name string) []operations.Reason {
	for _, e := range ta.Envelopes {
		if e.Name == name {
			return e.Reasons
		}
	}
	return nil
}

func requirementRef(r operations.Requirement) (CandidateKind, string, string) {
	switch {
	case r.Predicate != nil:
		return KindAssertion, r.Predicate.Assertion, "assertion:" + r.Predicate.Assertion
	case r.Dependency != nil:
		return KindDependency, r.Dependency.Slot, "dependency:" + r.Dependency.Slot
	case r.Envelope != "":
		return KindEnvelope, r.Envelope, "envelope:" + r.Envelope
	default:
		return KindLocal, r.LocalCapability, "local:" + r.LocalCapability
	}
}

func statusFrom(rs []operations.Reason, subject string) CandidateStatus {
	st := Refuted
	for _, r := range rs {
		if r.Subject != subject && !strings.HasPrefix(r.Subject, subject+"->") {
			continue
		}
		if unmetCodes[r.Code] {
			if st == Refuted {
				st = Maintained
			}
			continue
		}
		st = Open
	}
	return st
}

func dedupe(cs []Candidate) []Candidate {
	out := cs[:0]
	for i, c := range cs {
		if i > 0 && c.ID == cs[i-1].ID {
			continue
		}
		out = append(out, c)
	}
	return out
}

func capabilityOf(c operations.Contract, t operations.CapabilityType) (operations.Capability, bool) {
	for _, cp := range c.Capabilities {
		if cp.Type == t {
			return cp, true
		}
	}
	return operations.Capability{}, false
}

func envelopeOf(c operations.Contract, name string) (operations.Envelope, bool) {
	for _, e := range c.Envelopes {
		if e.Name == name {
			return e, true
		}
	}
	return operations.Envelope{}, false
}
