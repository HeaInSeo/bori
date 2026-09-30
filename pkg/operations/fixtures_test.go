package operations

import (
	"time"
)

// Generic, app-neutral fixtures. Provider names model ecosystem-style evidence
// sources (a Kubernetes workload status reader, a generic HTTP probe, a GitOps
// sync reporter); no network integration is involved.

var at = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	ns     = "apps"
	domain = "example.io"
)

var (
	httpProbe    = ProviderIdentity{Name: "generic-http-probe", ConfigRevision: "r1"}
	workloadStat = ProviderIdentity{Name: "kubernetes-workload-status", ConfigRevision: "r1"}
	gitopsSync   = ProviderIdentity{Name: "gitops-sync-provider", ConfigRevision: "r1"}
)

func capType(name string) CapabilityType {
	return CapabilityType{Domain: domain, Name: name, Revision: "v1"}
}

func contractID(name, uid, digest string) ContractIdentity {
	return ContractIdentity{Namespace: ns, Name: name, UID: uid, SpecDigest: digest}
}

func boolSlot(name string) AssertionSlot {
	return AssertionSlot{Name: name, Type: TypeBoolean, MaxAge: 30 * time.Second}
}

func intSlot(name string) AssertionSlot {
	return AssertionSlot{Name: name, Type: TypeInteger, MaxAge: 30 * time.Second}
}

func isTrue(slot string, onUnmet Impact) Requirement {
	return Requirement{Predicate: &Predicate{Assertion: slot, Op: OpIsTrue}, OnUnmet: onUnmet}
}

func pred(slot string, op Operator, v Value, onUnmet Impact) Requirement {
	return Requirement{Predicate: &Predicate{Assertion: slot, Op: op, Operand: v}, OnUnmet: onUnmet}
}

func dep(slot, capName string, onUnmet Impact) Requirement {
	return Requirement{Dependency: &DependencyRequirement{Slot: slot, Capability: capType(capName)}, OnUnmet: onUnmet}
}

func target(name, uid string, c ContractIdentity, bindings ...AssertionBinding) Target {
	return Target{
		Identity:          TargetIdentity{Namespace: ns, Name: name, UID: uid},
		ResolvedUID:       "workload-" + uid,
		ContractRef:       c,
		AssertionBindings: bindings,
	}
}

func bind(slot string, p ProviderIdentity) AssertionBinding {
	return AssertionBinding{Slot: slot, Provider: p}
}

func bindDep(slot string, to Target) DependencyBinding {
	return DependencyBinding{Slot: slot, TargetUID: to.Identity.UID, Contract: to.ContractRef}
}

// keyFor is the applicability key the evaluator expects for t/slot today.
func keyFor(t Target, slot string) ApplicabilityKey {
	k := ApplicabilityKey{Target: t.Identity, ResolvedUID: t.ResolvedUID, Contract: t.ContractRef, Slot: slot}
	for _, b := range t.AssertionBindings {
		if b.Slot == slot {
			k.Provider = b.Provider
		}
	}
	return k
}

// observe records a value observed age before the evaluation instant.
func observe(t Target, slot string, v Value, age time.Duration) Observation {
	return Observation{
		Key:         keyFor(t, slot),
		Outcome:     OutcomeValue,
		Value:       v,
		ObservedAt:  at.Add(-age),
		EvidenceRef: "ref:" + t.Identity.UID + "/" + slot,
	}
}

func providerDown(t Target, slot string, age time.Duration) Observation {
	return Observation{Key: keyFor(t, slot), Outcome: OutcomeProviderUnavailable, ObservedAt: at.Add(-age)}
}

const fresh = 5 * time.Second

// ── simple-http-service ─────────────────────────────────────────────────────

func httpServiceContract() Contract {
	return Contract{
		Identity: contractID("http-service-v1", "c-http", "sha256:http1"),
		Assertions: []AssertionSlot{
			boolSlot("api-serving"),
			boolSlot("latency-within-slo"),
			boolSlot("metrics-endpoint-up"),
		},
		Capabilities: []Capability{
			{Type: capType("serve-requests"), Requirements: []Requirement{isTrue("api-serving", ImpactUnavailable)}},
			{Type: capType("serve-within-slo"), Requirements: []Requirement{
				isTrue("api-serving", ImpactUnavailable),
				isTrue("latency-within-slo", ImpactDegraded),
			}},
			{Type: capType("export-metrics"), Requirements: []Requirement{isTrue("metrics-endpoint-up", ImpactUnavailable)}},
		},
	}
}

func httpServiceTarget() Target {
	c := httpServiceContract()
	return target("web", "t-web", c.Identity,
		bind("api-serving", httpProbe),
		bind("latency-within-slo", httpProbe),
		bind("metrics-endpoint-up", httpProbe),
	)
}

func httpServiceSnapshot(apiServing, latency, metrics bool) Snapshot {
	c, t := httpServiceContract(), httpServiceTarget()
	return Snapshot{
		At:        at,
		Contracts: []Contract{c},
		Targets:   []Target{t},
		Observations: []Observation{
			observe(t, "api-serving", Bool(apiServing), fresh),
			observe(t, "latency-within-slo", Bool(latency), fresh),
			observe(t, "metrics-endpoint-up", Bool(metrics), fresh),
		},
	}
}

// ── async-producer-resolver-worker ──────────────────────────────────────────

func resolverContract() Contract {
	return Contract{
		Identity:   contractID("resolver-v1", "c-resolver", "sha256:res1"),
		Assertions: []AssertionSlot{boolSlot("resolve-api-serving")},
		Capabilities: []Capability{
			{Type: capType("resolve-artifact"), Requirements: []Requirement{isTrue("resolve-api-serving", ImpactUnavailable)}},
		},
	}
}

func workerContract() Contract {
	return Contract{
		Identity: contractID("worker-v1", "c-worker", "sha256:wrk1"),
		Assertions: []AssertionSlot{
			boolSlot("submit-api-serving"),
			boolSlot("status-api-serving"),
		},
		DependencySlots: []DependencySlot{{Name: "resolver"}},
		Capabilities: []Capability{
			{Type: capType("submit-work"), Requirements: []Requirement{
				isTrue("submit-api-serving", ImpactUnavailable),
				dep("resolver", "resolve-artifact", ImpactUnavailable),
			}},
			{Type: capType("observe-running-work"), Requirements: []Requirement{
				isTrue("status-api-serving", ImpactUnavailable),
			}},
		},
	}
}

func producerContract() Contract {
	return Contract{
		Identity:        contractID("producer-v1", "c-producer", "sha256:prd1"),
		Assertions:      []AssertionSlot{boolSlot("producer-up")},
		DependencySlots: []DependencySlot{{Name: "worker"}},
		Capabilities: []Capability{
			// The producer can buffer, so losing submission only degrades it.
			{Type: capType("produce-work"), Requirements: []Requirement{
				isTrue("producer-up", ImpactUnavailable),
				dep("worker", "submit-work", ImpactDegraded),
			}},
		},
	}
}

type pipeline struct {
	resolver, worker, producer Target
}

func pipelineTargets() pipeline {
	r := target("resolver", "t-resolver", resolverContract().Identity, bind("resolve-api-serving", httpProbe))
	w := target("worker", "t-worker", workerContract().Identity,
		bind("submit-api-serving", httpProbe),
		bind("status-api-serving", httpProbe),
	)
	w.DependencyBindings = []DependencyBinding{bindDep("resolver", r)}
	p := target("producer", "t-producer", producerContract().Identity, bind("producer-up", workloadStat))
	p.DependencyBindings = []DependencyBinding{bindDep("worker", w)}
	return pipeline{resolver: r, worker: w, producer: p}
}

func pipelineSnapshot(resolverServing bool) Snapshot {
	p := pipelineTargets()
	return Snapshot{
		At:        at,
		Contracts: []Contract{resolverContract(), workerContract(), producerContract()},
		Targets:   []Target{p.resolver, p.worker, p.producer},
		Observations: []Observation{
			observe(p.resolver, "resolve-api-serving", Bool(resolverServing), fresh),
			observe(p.worker, "submit-api-serving", Bool(true), fresh),
			observe(p.worker, "status-api-serving", Bool(true), fresh),
			observe(p.producer, "producer-up", Bool(true), fresh),
		},
	}
}

// ── replicated-service ──────────────────────────────────────────────────────

func replicatedContract() Contract {
	return Contract{
		Identity: contractID("replicated-v1", "c-repl", "sha256:rep1"),
		Assertions: []AssertionSlot{
			intSlot("available-replicas"),
			boolSlot("storage-writable"),
			boolSlot("config-in-sync"),
		},
		Capabilities: []Capability{
			{Type: capType("serve"), Requirements: []Requirement{pred("available-replicas", OpGte, Int(1), ImpactUnavailable)}},
			{Type: capType("persist"), Requirements: []Requirement{isTrue("storage-writable", ImpactUnavailable)}},
			// Only this capability is affected by the recommended envelope,
			// because only it references the envelope.
			{Type: capType("serve-with-redundancy"), Requirements: []Requirement{{Envelope: "recommended", OnUnmet: ImpactDegraded}}},
			// A GitOps-reported fact consumed as an ordinary provider-owned
			// assertion. It never becomes the target's Sync axis.
			{Type: capType("run-declared-config"), Requirements: []Requirement{isTrue("config-in-sync", ImpactDegraded)}},
		},
		Envelopes: []Envelope{
			{Name: "minimum", Class: EnvelopeMinimum, Requirements: []Predicate{
				{Assertion: "available-replicas", Op: OpGte, Operand: Int(1)},
				{Assertion: "storage-writable", Op: OpIsTrue},
			}},
			{Name: "recommended", Class: EnvelopeRecommended, Requirements: []Predicate{
				{Assertion: "available-replicas", Op: OpGte, Operand: Int(2)},
				{Assertion: "storage-writable", Op: OpIsTrue},
			}},
		},
	}
}

func replicatedTarget() Target {
	t := target("store", "t-store", replicatedContract().Identity,
		bind("available-replicas", workloadStat),
		bind("storage-writable", httpProbe),
		bind("config-in-sync", gitopsSync),
	)
	t.EnvelopeSelection = "recommended"
	return t
}

func replicatedSnapshot(replicas int64, storageAge time.Duration) Snapshot {
	t := replicatedTarget()
	return Snapshot{
		At:        at,
		Contracts: []Contract{replicatedContract()},
		Targets:   []Target{t},
		Observations: []Observation{
			observe(t, "available-replicas", Int(replicas), fresh),
			observe(t, "storage-writable", Bool(true), storageAge),
			observe(t, "config-in-sync", Bool(true), fresh),
		},
	}
}

// ── stateful-single-writer ──────────────────────────────────────────────────

func singleWriterContract() Contract {
	return Contract{
		Identity: contractID("single-writer-v1", "c-sw", "sha256:sw1"),
		Assertions: []AssertionSlot{
			intSlot("active-writers"),
			{Name: "role", Type: TypeString, Enum: []string{"primary", "replica", "fenced"}, MaxAge: 30 * time.Second},
		},
		Capabilities: []Capability{
			{Type: capType("accept-writes"), Requirements: []Requirement{
				pred("active-writers", OpEq, Int(1), ImpactUnavailable),
				pred("role", OpEq, String("primary"), ImpactUnavailable),
			}},
		},
		Envelopes: []Envelope{
			{Name: "minimum", Class: EnvelopeMinimum, Requirements: []Predicate{
				{Assertion: "active-writers", Op: OpGte, Operand: Int(1)},
				{Assertion: "active-writers", Op: OpLte, Operand: Int(1)},
			}},
		},
	}
}

func singleWriterSnapshot(writers int64, role string) Snapshot {
	t := target("db", "t-db", singleWriterContract().Identity,
		bind("active-writers", workloadStat),
		bind("role", workloadStat),
	)
	return Snapshot{
		At:        at,
		Contracts: []Contract{singleWriterContract()},
		Targets:   []Target{t},
		Observations: []Observation{
			observe(t, "active-writers", Int(writers), fresh),
			observe(t, "role", String(role), fresh),
		},
	}
}
