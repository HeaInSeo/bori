package operations

import (
	"fmt"
	"time"
)

// Contract is a reusable, app-neutral operational contract: which facts a
// kind of application must assert, which capabilities it offers, what each
// capability explicitly requires, and which operational envelopes are defined.
// It names no concrete target.
type Contract struct {
	Identity        ContractIdentity
	Assertions      []AssertionSlot
	DependencySlots []DependencySlot
	Capabilities    []Capability
	Envelopes       []Envelope
}

// AssertionSlot declares a typed current fact the contract needs.
type AssertionSlot struct {
	Name string
	Type ValueType
	// Enum optionally closes a String slot to a set of values.
	Enum []string
	// MaxAge bounds how long after ObservedAt a value may be used.
	MaxAge time.Duration
}

func (s AssertionSlot) admits(v Value) bool {
	if v.Type != s.Type {
		return false
	}
	if s.Type != TypeString || len(s.Enum) == 0 {
		return true
	}
	for _, e := range s.Enum {
		if e == v.Str {
			return true
		}
	}
	return false
}

// DependencySlot is a named placeholder for another OperationalTarget. The
// concrete target is chosen by the OperationalTarget's DependencyBinding.
type DependencySlot struct {
	Name string
}

// Capability is a user- or platform-facing function whose truth is derived
// only from the requirements it explicitly references.
type Capability struct {
	Type         CapabilityType
	Requirements []Requirement
}

// Impact is the declared effect of a proven-unmet requirement. There is no
// universal mapping from requirement kind to impact.
type Impact string

const (
	ImpactDegraded    Impact = "DEGRADED"
	ImpactUnavailable Impact = "UNAVAILABLE"
)

// Requirement is exactly one of: a local assertion predicate, a capability of
// a bound dependency target, another capability of the same contract, or a
// named envelope of the same contract. OnUnmet declares the impact when the
// requirement is proven unmet; an unproven requirement always yields UNKNOWN.
type Requirement struct {
	Predicate       *Predicate
	Dependency      *DependencyRequirement
	LocalCapability string
	Envelope        string

	OnUnmet Impact
}

// DependencyRequirement requires the exact qualified capability of the target
// bound to Slot.
type DependencyRequirement struct {
	Slot       string
	Capability CapabilityType
}

// EnvelopeClass distinguishes minimum from recommended operating conditions.
type EnvelopeClass string

const (
	EnvelopeMinimum     EnvelopeClass = "Minimum"
	EnvelopeRecommended EnvelopeClass = "Recommended"
)

// Envelope is an independent AllOf assessment over local assertions. It does
// not affect any capability unless a capability explicitly references it.
type Envelope struct {
	Name         string
	Class        EnvelopeClass
	Requirements []Predicate
}

// Validate reports every structural defect of the contract. A contract with
// any defect is invalid and none of its targets are assessed.
func (c Contract) Validate() []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if !c.Identity.complete() {
		add("contract identity must set namespace, name, uid and specDigest")
	}

	slots := map[string]AssertionSlot{}
	for _, s := range c.Assertions {
		switch {
		case s.Name == "":
			add("assertion slot with empty name")
			continue
		case slots[s.Name].Name != "":
			add("duplicate assertion slot %q", s.Name)
			continue
		}
		switch s.Type {
		case TypeBoolean, TypeInteger:
			if len(s.Enum) > 0 {
				add("assertion %q: enum is only valid for String", s.Name)
			}
		case TypeString:
		default:
			add("assertion %q: unknown value type %q", s.Name, s.Type)
		}
		if s.MaxAge <= 0 {
			add("assertion %q: maxAge must be positive", s.Name)
		}
		slots[s.Name] = s
	}

	depSlots := map[string]bool{}
	for _, d := range c.DependencySlots {
		if d.Name == "" || depSlots[d.Name] {
			add("dependency slot %q empty or duplicate", d.Name)
			continue
		}
		depSlots[d.Name] = true
	}

	envelopes := map[string]bool{}
	for _, e := range c.Envelopes {
		if e.Name == "" || envelopes[e.Name] {
			add("envelope %q empty or duplicate", e.Name)
			continue
		}
		envelopes[e.Name] = true
		if e.Class != EnvelopeMinimum && e.Class != EnvelopeRecommended {
			add("envelope %q: unknown class %q", e.Name, e.Class)
		}
		if len(e.Requirements) == 0 {
			add("envelope %q has no requirements", e.Name)
		}
		for _, p := range e.Requirements {
			if err := p.validate(slots); err != nil {
				add("envelope %q: %v", e.Name, err)
			}
		}
	}

	byName := map[string]bool{}
	byType := map[CapabilityType]bool{}
	for _, cp := range c.Capabilities {
		if !cp.Type.complete() {
			add("capability %q: type must set domain, name and revision", cp.Type)
		}
		if byName[cp.Type.Name] || byType[cp.Type] {
			add("duplicate capability %q", cp.Type)
		}
		byName[cp.Type.Name] = true
		byType[cp.Type] = true
	}

	for _, cp := range c.Capabilities {
		if len(cp.Requirements) == 0 {
			add("capability %q has no requirements", cp.Type)
		}
		for i, r := range cp.Requirements {
			where := fmt.Sprintf("capability %q requirement %d", cp.Type, i)
			if r.OnUnmet != ImpactDegraded && r.OnUnmet != ImpactUnavailable {
				add("%s: onUnmet must be DEGRADED or UNAVAILABLE, got %q", where, r.OnUnmet)
			}
			kinds := 0
			if r.Predicate != nil {
				kinds++
				if err := r.Predicate.validate(slots); err != nil {
					add("%s: %v", where, err)
				}
			}
			if r.Dependency != nil {
				kinds++
				if !depSlots[r.Dependency.Slot] {
					add("%s: undeclared dependency slot %q", where, r.Dependency.Slot)
				}
				if !r.Dependency.Capability.complete() {
					add("%s: dependency capability must be fully qualified", where)
				}
			}
			if r.LocalCapability != "" {
				kinds++
				if !byName[r.LocalCapability] {
					add("%s: undeclared local capability %q", where, r.LocalCapability)
				}
			}
			if r.Envelope != "" {
				kinds++
				if !envelopes[r.Envelope] {
					add("%s: undeclared envelope %q", where, r.Envelope)
				}
			}
			if kinds != 1 {
				add("%s: must reference exactly one of predicate, dependency, local capability or envelope", where)
			}
		}
	}
	return errs
}

func (c Contract) capability(name string) (Capability, bool) {
	for _, cp := range c.Capabilities {
		if cp.Type.Name == name {
			return cp, true
		}
	}
	return Capability{}, false
}

func (c Contract) capabilityByType(t CapabilityType) (Capability, bool) {
	for _, cp := range c.Capabilities {
		if cp.Type == t {
			return cp, true
		}
	}
	return Capability{}, false
}

func (c Contract) slot(name string) (AssertionSlot, bool) {
	for _, s := range c.Assertions {
		if s.Name == name {
			return s, true
		}
	}
	return AssertionSlot{}, false
}

func (c Contract) envelope(name string) (Envelope, bool) {
	for _, e := range c.Envelopes {
		if e.Name == name {
			return e, true
		}
	}
	return Envelope{}, false
}
