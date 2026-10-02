// Package opswire maps the O2 candidate public wire (apis/ops/v1alpha1) to
// and from the O1 semantic core (pkg/operations).
//
// It owns translation, reference trust checks and status projection only.
// Every evaluation rule — capability state, UNKNOWN composition, dependency
// impact, evidence applicability, supersession, conflict, cycles, envelopes —
// stays in pkg/operations. The package is pure: no client, no clock.
package opswire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// ContractFromWire returns the O1 contract for a wire object. Its identity
// is namespace, name, Kubernetes UID and the deterministic spec digest.
func ContractFromWire(c *opsv1.OperationalContract) operations.Contract {
	oc := semanticContract(&c.Spec)
	oc.Identity = ContractIdentity(c)
	return oc
}

// ContractIdentity is the exact O1 identity of a wire contract.
func ContractIdentity(c *opsv1.OperationalContract) operations.ContractIdentity {
	return operations.ContractIdentity{
		Namespace:  c.Namespace,
		Name:       c.Name,
		UID:        string(c.UID),
		SpecDigest: SpecDigest(&c.Spec),
	}
}

// SpecDigest is a deterministic digest of the contract's O1 semantics. Lists
// whose order carries no meaning are sorted first, and durations are compared
// by value, so reordering or re-spelling the same contract does not change
// the digest while any semantic change does.
func SpecDigest(spec *opsv1.OperationalContractSpec) string {
	oc := semanticContract(spec)
	normalize(&oc)
	b, err := json.Marshal(oc)
	if err != nil {
		// operations.Contract contains only JSON-encodable fields.
		panic("opswire: marshal contract: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func semanticContract(spec *opsv1.OperationalContractSpec) operations.Contract {
	var oc operations.Contract
	for _, a := range spec.Assertions {
		oc.Assertions = append(oc.Assertions, operations.AssertionSlot{
			Name:   a.Name,
			Type:   operations.ValueType(a.Type),
			Enum:   append([]string(nil), a.Enum...),
			MaxAge: a.MaxAge.Duration,
		})
	}
	for _, d := range spec.DependencySlots {
		oc.DependencySlots = append(oc.DependencySlots, operations.DependencySlot{Name: d.Name})
	}
	for _, cp := range spec.Capabilities {
		c := operations.Capability{Type: capabilityType(cp.Type)}
		for _, r := range cp.Requirements {
			c.Requirements = append(c.Requirements, requirement(r))
		}
		oc.Capabilities = append(oc.Capabilities, c)
	}
	for _, e := range spec.Envelopes {
		env := operations.Envelope{Name: e.Name, Class: operations.EnvelopeClass(e.Class)}
		for _, p := range e.Requirements {
			env.Requirements = append(env.Requirements, predicate(p))
		}
		oc.Envelopes = append(oc.Envelopes, env)
	}
	return oc
}

func capabilityType(t opsv1.CapabilityType) operations.CapabilityType {
	return operations.CapabilityType{Domain: t.Domain, Name: t.Name, Revision: t.Revision}
}

func requirement(r opsv1.Requirement) operations.Requirement {
	out := operations.Requirement{
		LocalCapability: r.LocalCapability,
		Envelope:        r.Envelope,
		OnUnmet:         operations.Impact(r.OnUnmet),
	}
	if r.Predicate != nil {
		p := predicate(*r.Predicate)
		out.Predicate = &p
	}
	if r.Dependency != nil {
		out.Dependency = &operations.DependencyRequirement{
			Slot:       r.Dependency.Slot,
			Capability: capabilityType(r.Dependency.Capability),
		}
	}
	return out
}

func predicate(p opsv1.Predicate) operations.Predicate {
	return operations.Predicate{
		Assertion: p.Assertion,
		Op:        operations.Operator(p.Operator),
		Operand:   operand(p.Operand),
	}
}

// operand maps a wire operand to an O1 value without interpreting it. An
// operand with several typed values set becomes a non-canonical O1 value, so
// O1 contract validation rejects it (fail closed) rather than this layer
// choosing one of them.
func operand(o *opsv1.Operand) operations.Value {
	var v operations.Value
	if o == nil {
		return v
	}
	if o.String != nil {
		v.Type, v.Str = operations.TypeString, *o.String
	}
	if o.Integer != nil {
		v.Type, v.Int = operations.TypeInteger, *o.Integer
	}
	if o.Boolean != nil {
		v.Type, v.Bool = operations.TypeBoolean, *o.Boolean
	}
	return v
}

// normalize sorts every list whose order is semantically irrelevant in O1.
func normalize(c *operations.Contract) {
	for i := range c.Assertions {
		sort.Strings(c.Assertions[i].Enum)
	}
	sort.Slice(c.Assertions, func(i, j int) bool { return c.Assertions[i].Name < c.Assertions[j].Name })
	sort.Slice(c.DependencySlots, func(i, j int) bool { return c.DependencySlots[i].Name < c.DependencySlots[j].Name })
	for i := range c.Capabilities {
		sortByJSON(c.Capabilities[i].Requirements)
	}
	sort.Slice(c.Capabilities, func(i, j int) bool {
		return c.Capabilities[i].Type.String() < c.Capabilities[j].Type.String()
	})
	for i := range c.Envelopes {
		sortByJSON(c.Envelopes[i].Requirements)
	}
	sort.Slice(c.Envelopes, func(i, j int) bool { return c.Envelopes[i].Name < c.Envelopes[j].Name })
}

func sortByJSON[T any](xs []T) {
	keys := make([]string, len(xs))
	for i, x := range xs {
		b, _ := json.Marshal(x)
		keys[i] = string(b)
	}
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	sorted := make([]T, len(xs))
	for i, j := range idx {
		sorted[i] = xs[j]
	}
	copy(xs, sorted)
}

// ContractStatus is the semantic status of a contract: its digest and the O1
// validation result. It contains no timestamps.
func ContractStatus(c *opsv1.OperationalContract) opsv1.OperationalContractStatus {
	oc := ContractFromWire(c)
	errs := oc.Validate()
	sort.Strings(errs)
	if len(errs) > maxValidationErrors {
		errs = errs[:maxValidationErrors]
	}
	for i := range errs {
		errs[i] = truncate(errs[i], maxDetail)
	}
	return opsv1.OperationalContractStatus{
		ObservedGeneration: c.Generation,
		SpecDigest:         oc.Identity.SpecDigest,
		Valid:              len(errs) == 0,
		ValidationErrors:   errs,
	}
}

const (
	maxValidationErrors = 32
	maxDetail           = 256
	maxSubject          = 512
	maxEvidenceRef      = 256
	maxReasons          = 40
	maxInvalidReasons   = 32
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
