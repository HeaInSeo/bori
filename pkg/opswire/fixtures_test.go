package opswire

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Generic, app-neutral wire fixtures. Providers are ecosystem-style
// identities; nothing here names a product application.

var at = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	domain = "example.io"
	fresh  = 5 * time.Second
)

func ptr[T any](v T) *T { return &v }

func dur(d time.Duration) metav1.Duration { return metav1.Duration{Duration: d} }

func capT(name string) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: domain, Name: name, Revision: "v1"}
}

func boolSlot(name string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: name, Type: "Boolean", MaxAge: dur(30 * time.Second)}
}

func isTrue(slot string, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Predicate: &opsv1.Predicate{Assertion: slot, Operator: "IsTrue"}, OnUnmet: impact}
}

func depReq(slot, capName string, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Dependency: &opsv1.DependencyRequirement{Slot: slot, Capability: capT(capName)}, OnUnmet: impact}
}

func contract(ns, name, uid string, spec opsv1.OperationalContractSpec) opsv1.OperationalContract {
	return opsv1.OperationalContract{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid), Generation: 1},
		Spec:       spec,
	}
}

// serviceSpec: a generic HTTP service with a replica envelope.
func serviceSpec() opsv1.OperationalContractSpec {
	return opsv1.OperationalContractSpec{
		Assertions: []opsv1.AssertionSlot{
			boolSlot("api-serving"),
			{Name: "available-replicas", Type: "Integer", MaxAge: dur(30 * time.Second)},
			{Name: "role", Type: "String", Enum: []string{"primary", "replica"}, MaxAge: dur(30 * time.Second)},
		},
		Capabilities: []opsv1.Capability{
			{Type: capT("serve"), Requirements: []opsv1.Requirement{isTrue("api-serving", "UNAVAILABLE")}},
			{Type: capT("accept-writes"), Requirements: []opsv1.Requirement{
				{Predicate: &opsv1.Predicate{Assertion: "role", Operator: "Eq", Operand: &opsv1.Operand{String: ptr("primary")}}, OnUnmet: "UNAVAILABLE"},
			}},
			{Type: capT("redundant"), Requirements: []opsv1.Requirement{{Envelope: "recommended", OnUnmet: "DEGRADED"}}},
		},
		Envelopes: []opsv1.Envelope{
			{Name: "minimum", Class: "Minimum", Requirements: []opsv1.Predicate{
				{Assertion: "available-replicas", Operator: "Gte", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
			}},
			{Name: "recommended", Class: "Recommended", Requirements: []opsv1.Predicate{
				{Assertion: "available-replicas", Operator: "Gte", Operand: &opsv1.Operand{Integer: ptr[int64](2)}},
				{Assertion: "api-serving", Operator: "IsTrue"},
			}},
		},
	}
}

// resolverSpec: one capability other targets depend on.
func resolverSpec() opsv1.OperationalContractSpec {
	return opsv1.OperationalContractSpec{
		Assertions: []opsv1.AssertionSlot{boolSlot("up"), boolSlot("fast")},
		Capabilities: []opsv1.Capability{{Type: capT("resolve"), Requirements: []opsv1.Requirement{
			isTrue("up", "UNAVAILABLE"),
			isTrue("fast", "DEGRADED"),
		}}},
	}
}

// workerSpec: one capability depends on a resolver, one does not.
func workerSpec() opsv1.OperationalContractSpec {
	return opsv1.OperationalContractSpec{
		Assertions:      []opsv1.AssertionSlot{boolSlot("submit-up"), boolSlot("status-up")},
		DependencySlots: []opsv1.DependencySlot{{Name: "resolver"}},
		Capabilities: []opsv1.Capability{
			{Type: capT("submit"), Requirements: []opsv1.Requirement{
				isTrue("submit-up", "UNAVAILABLE"),
				depReq("resolver", "resolve", "UNAVAILABLE"),
			}},
			{Type: capT("observe"), Requirements: []opsv1.Requirement{isTrue("status-up", "UNAVAILABLE")}},
		},
	}
}

func provider(name string) opsv1.ProviderReference {
	return opsv1.ProviderReference{Name: name, ConfigRevision: "r1"}
}

func target(ns, name, uid, contractName string, bindings ...opsv1.AssertionBinding) opsv1.OperationalTarget {
	return opsv1.OperationalTarget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid), Generation: 1},
		Spec: opsv1.OperationalTargetSpec{
			TargetRef:         opsv1.TargetReference{APIVersion: "apps/v1", Kind: "Deployment", Name: name},
			ContractRef:       opsv1.LocalContractReference{Name: contractName},
			AssertionBindings: bindings,
		},
	}
}

func bind(slot, providerName string) opsv1.AssertionBinding {
	return opsv1.AssertionBinding{Slot: slot, Provider: provider(providerName)}
}

func bindDep(slot string, to opsv1.OperationalTarget, toContract opsv1.OperationalContract) opsv1.DependencyBinding {
	return opsv1.DependencyBinding{
		Slot:   slot,
		Target: opsv1.DependencyTargetReference{Namespace: to.Namespace, Name: to.Name, UID: string(to.UID)},
		Contract: opsv1.ContractPin{
			Name:       toContract.Name,
			UID:        string(toContract.UID),
			SpecDigest: SpecDigest(&toContract.Spec),
		},
	}
}

func grant(ns string, fromNS string, to ...opsv1.ReferenceGrantTo) opsv1.OperationalReferenceGrant {
	return opsv1.OperationalReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "g-" + fromNS},
		Spec: opsv1.OperationalReferenceGrantSpec{
			From: []opsv1.ReferenceGrantFrom{{Kind: "OperationalTarget", Namespace: fromNS}},
			To:   to,
		},
	}
}

// world is a set of wire objects plus resolution and evidence, mutable per
// test before building.
type world struct {
	contracts    []opsv1.OperationalContract
	targets      []opsv1.OperationalTarget
	grants       []opsv1.OperationalReferenceGrant
	resolved     map[types.NamespacedName]Resolution
	observations []operations.Observation
}

func (w *world) input() Input {
	return Input{
		At:           at,
		Contracts:    w.contracts,
		Targets:      w.targets,
		Grants:       w.grants,
		Resolutions:  w.resolved,
		Observations: w.observations,
	}
}

func (w *world) resolveAll() {
	if w.resolved == nil {
		w.resolved = map[types.NamespacedName]Resolution{}
	}
	for _, t := range w.targets {
		k := types.NamespacedName{Namespace: t.Namespace, Name: t.Name}
		if _, ok := w.resolved[k]; !ok {
			w.resolved[k] = Resolution{UID: "workload-" + string(t.UID)}
		}
	}
}

func (w *world) contractOf(t opsv1.OperationalTarget) opsv1.OperationalContract {
	for _, c := range w.contracts {
		if c.Namespace == t.Namespace && c.Name == t.Spec.ContractRef.Name {
			return c
		}
	}
	panic("no contract for " + t.Name)
}

// observe produces the observation a provider would emit for the target's
// current binding of slot.
func (w *world) observe(t opsv1.OperationalTarget, slot string, v operations.Value, age time.Duration) operations.Observation {
	c := w.contractOf(t)
	var p operations.ProviderIdentity
	for _, b := range t.Spec.AssertionBindings {
		if b.Slot == slot {
			ns := b.Provider.Namespace
			if ns == "" {
				ns = t.Namespace
			}
			p = ProviderIdentity(ns, b.Provider.Name, b.Provider.ConfigRevision)
		}
	}
	return operations.Observation{
		Key: operations.ApplicabilityKey{
			Target:      operations.TargetIdentity{Namespace: t.Namespace, Name: t.Name, UID: string(t.UID)},
			ResolvedUID: "workload-" + string(t.UID),
			Contract:    ContractIdentity(&c),
			Slot:        slot,
			Provider:    p,
		},
		Outcome:     operations.OutcomeValue,
		Value:       v,
		ObservedAt:  at.Add(-age),
		EvidenceRef: "ref:" + string(t.UID) + "/" + slot,
	}
}

func (w *world) eval() (Built, operations.Assessment) {
	w.resolveAll()
	b := Build(w.input())
	a, err := operations.Evaluate(b.Snapshot)
	if err != nil {
		panic(err)
	}
	return b, a
}

func (w *world) status(name string) opsv1.OperationalTargetStatus {
	b, a := w.eval()
	for i := range w.targets {
		if w.targets[i].Name == name {
			return Project(&w.targets[i], b, a)
		}
	}
	panic("no target " + name)
}

// pipeline: resolver and worker in namespace "a", healthy.
func pipeline() *world {
	rc := contract("a", "resolver-v1", "c-res", resolverSpec())
	wc := contract("a", "worker-v1", "c-wrk", workerSpec())
	r := target("a", "resolver", "t-res", "resolver-v1", bind("up", "generic-http-probe"), bind("fast", "generic-http-probe"))
	wk := target("a", "worker", "t-wrk", "worker-v1", bind("submit-up", "generic-http-probe"), bind("status-up", "kubernetes-workload-status"))
	wk.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("resolver", r, rc)}
	w := &world{contracts: []opsv1.OperationalContract{rc, wc}, targets: []opsv1.OperationalTarget{r, wk}}
	w.observations = []operations.Observation{
		w.observe(r, "up", operations.Bool(true), fresh),
		w.observe(r, "fast", operations.Bool(true), fresh),
		w.observe(wk, "submit-up", operations.Bool(true), fresh),
		w.observe(wk, "status-up", operations.Bool(true), fresh),
	}
	return w
}
