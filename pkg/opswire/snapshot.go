package opswire

import (
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Resolution is the outcome of resolving an OperationalTarget's targetRef.
// Exactly one of UID or Failure is set.
type Resolution struct {
	UID     string
	Failure string
}

// Wire-level target failures. A target that fails here is not handed to the
// O1 evaluator; its dependents see it as missing (UNKNOWN).
const (
	ReasonTargetKindUnsupported = "target-kind-unsupported"
	ReasonTargetNotFound        = "target-not-found"
)

// Input is everything one evaluation needs. Observations come from evidence
// providers as O1 observations; this package does not fetch them.
type Input struct {
	At           time.Time
	Contracts    []opsv1.OperationalContract
	Targets      []opsv1.OperationalTarget
	Grants       []opsv1.OperationalReferenceGrant
	Resolutions  map[types.NamespacedName]Resolution
	Observations []operations.Observation
}

// Built is the O1 snapshot plus the wire-level facts the evaluator does not
// know about, keyed by OperationalTarget UID.
type Built struct {
	Snapshot operations.Snapshot
	Wire     map[string]TargetWire
	// namespaceOf maps every OperationalTarget UID to its namespace; status
	// projection uses it to keep foreign identities out of reason details.
	namespaceOf map[string]string
}

// TargetWire holds wire-level results for one target.
type TargetWire struct {
	// Failure is set when the target was not handed to the evaluator.
	Failure string
	// ResolvedUID is the resolved workload UID, if any.
	ResolvedUID string
	// Contract is the identity the target was bound to.
	Contract operations.ContractIdentity
	// Denied lists bindings dropped for lack of a matching grant.
	Denied []opsv1.DeniedReference
	// CrossNamespaceSlots are dependency slots bound into another namespace.
	CrossNamespaceSlots map[string]bool
}

// Build translates wire objects into an O1 snapshot. It applies only
// reference resolution and trust: same-namespace contract lookup, typed
// cross-namespace grants, and exact dependency instance pinning.
func Build(in Input) Built {
	b := Built{
		Snapshot:    operations.Snapshot{At: in.At, Observations: in.Observations},
		Wire:        map[string]TargetWire{},
		namespaceOf: map[string]string{},
	}
	grants := newGrantIndex(in.Grants)

	contracts := map[types.NamespacedName]*opsv1.OperationalContract{}
	for i := range in.Contracts {
		c := &in.Contracts[i]
		contracts[types.NamespacedName{Namespace: c.Namespace, Name: c.Name}] = c
		b.Snapshot.Contracts = append(b.Snapshot.Contracts, ContractFromWire(c))
	}
	targets := map[types.NamespacedName]*opsv1.OperationalTarget{}
	for i := range in.Targets {
		t := &in.Targets[i]
		targets[types.NamespacedName{Namespace: t.Namespace, Name: t.Name}] = t
		b.namespaceOf[string(t.UID)] = t.Namespace
	}

	for i := range in.Targets {
		t := &in.Targets[i]
		key := types.NamespacedName{Namespace: t.Namespace, Name: t.Name}
		tw := TargetWire{CrossNamespaceSlots: map[string]bool{}}

		// contractRef is same-namespace only; there is no wire field for
		// another namespace. A missing contract is passed with an incomplete
		// identity so O1 reports contract-unresolved.
		tw.Contract = operations.ContractIdentity{Namespace: t.Namespace, Name: t.Spec.ContractRef.Name}
		if c, ok := contracts[types.NamespacedName{Namespace: t.Namespace, Name: t.Spec.ContractRef.Name}]; ok {
			tw.Contract = ContractIdentity(c)
		}

		ot := operations.Target{
			Identity:          operations.TargetIdentity{Namespace: t.Namespace, Name: t.Name, UID: string(t.UID)},
			ContractRef:       tw.Contract,
			EnvelopeSelection: t.Spec.EnvelopeSelection,
		}

		// Grant checks run before the targetRef resolution short-circuit so
		// that deniedReferences do not depend on whether the workload resolves.

		for _, ab := range t.Spec.AssertionBindings {
			ns := ab.Provider.Namespace
			if ns == "" {
				ns = t.Namespace
			}
			if !grants.allows(t.Namespace, ns, opsv1.ReferenceEvidenceProvider, ab.Provider.Name) {
				tw.Denied = append(tw.Denied, opsv1.DeniedReference{Type: opsv1.ReferenceEvidenceProvider, Slot: ab.Slot, Namespace: ns})
				continue
			}
			ot.AssertionBindings = append(ot.AssertionBindings, operations.AssertionBinding{
				Slot:     ab.Slot,
				Provider: ProviderIdentity(ns, ab.Provider.Name, ab.Provider.ConfigRevision),
			})
		}

		for _, db := range t.Spec.DependencyBindings {
			ns := db.Target.Namespace
			if ns == "" {
				ns = t.Namespace
			}
			// Trust is checked before any lookup so that a denied reference
			// never reveals whether its referent exists.
			if !grants.allows(t.Namespace, ns, opsv1.ReferenceDependency, db.Target.Name) {
				tw.Denied = append(tw.Denied, opsv1.DeniedReference{Type: opsv1.ReferenceDependency, Slot: db.Slot, Namespace: ns})
				continue
			}
			if ns != t.Namespace {
				tw.CrossNamespaceSlots[db.Slot] = true
			}
			ot.DependencyBindings = append(ot.DependencyBindings, operations.DependencyBinding{
				Slot:      db.Slot,
				TargetUID: pinnedUID(targets, ns, db.Target),
				Contract: operations.ContractIdentity{
					Namespace:  ns,
					Name:       db.Contract.Name,
					UID:        db.Contract.UID,
					SpecDigest: db.Contract.SpecDigest,
				},
			})
		}

		sort.Slice(tw.Denied, func(i, j int) bool {
			a, c := tw.Denied[i], tw.Denied[j]
			if a.Slot != c.Slot {
				return a.Slot < c.Slot
			}
			if a.Type != c.Type {
				return a.Type < c.Type
			}
			return a.Namespace < c.Namespace
		})

		// An unresolved target keeps its trust report but is not handed to the
		// O1 evaluator.
		res := in.Resolutions[key]
		if res.UID == "" {
			tw.Failure = res.Failure
			if tw.Failure == "" {
				tw.Failure = ReasonTargetNotFound
			}
			b.Wire[string(t.UID)] = tw
			continue
		}
		tw.ResolvedUID = res.UID
		ot.ResolvedUID = res.UID

		b.Wire[string(t.UID)] = tw
		b.Snapshot.Targets = append(b.Snapshot.Targets, ot)
	}
	return b
}

// ProviderIdentity is the O1 provider identity of a provider reference. The
// namespace is part of the identity, so moving a provider is a rebind.
func ProviderIdentity(namespace, name, configRevision string) operations.ProviderIdentity {
	return operations.ProviderIdentity{Name: namespace + "/" + name, ConfigRevision: configRevision}
}

// pinnedUID returns the pinned UID only if the named target in the granted
// namespace currently has it. O1 resolves dependencies by UID across the whole
// snapshot; returning an unresolvable UID otherwise prevents a binding from
// reaching a same-UID object outside the namespace the grant covered.
func pinnedUID(targets map[types.NamespacedName]*opsv1.OperationalTarget, ns string, ref opsv1.DependencyTargetReference) string {
	if t, ok := targets[types.NamespacedName{Namespace: ns, Name: ref.Name}]; ok && string(t.UID) == ref.UID {
		return ref.UID
	}
	return "unresolved:" + ns + "/" + ref.Name + "@" + ref.UID
}

// grantIndex answers exact, typed cross-namespace trust questions.
type grantIndex map[grantKey]bool

type grantKey struct {
	toNamespace   string
	fromNamespace string
	refType       opsv1.ReferenceType
	name          string
}

func newGrantIndex(grants []opsv1.OperationalReferenceGrant) grantIndex {
	idx := grantIndex{}
	for _, g := range grants {
		for _, from := range g.Spec.From {
			if from.Kind != "OperationalTarget" || !exact(from.Namespace) {
				continue
			}
			for _, to := range g.Spec.To {
				if (to.Type != opsv1.ReferenceDependency && to.Type != opsv1.ReferenceEvidenceProvider) || !exact(to.Name) {
					continue
				}
				idx[grantKey{g.Namespace, from.Namespace, to.Type, to.Name}] = true
			}
		}
	}
	return idx
}

// exact rejects empty and wildcard-looking values even if schema validation
// was bypassed; grants never express broad trust.
func exact(s string) bool {
	return s != "" && !strings.ContainsAny(s, "*?")
}

// allows reports whether a target in fromNS may reference name of refType in
// toNS. Same-namespace references need no grant.
func (g grantIndex) allows(fromNS, toNS string, refType opsv1.ReferenceType, name string) bool {
	if fromNS == toNS {
		return true
	}
	return g[grantKey{toNS, fromNS, refType, name}]
}
