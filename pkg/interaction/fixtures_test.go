package interaction

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

// Generic, app-neutral fixtures. Every summary is computed through the real
// path: wire objects → opswire.Build → operations.Evaluate → opswire.Project
// → Project. Nothing here names a product application, and no expected
// level is looked up by scenario name.

var at = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

const fresh = 5 * time.Second

func capT(name string) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: "example.io", Name: name, Revision: "v1"}
}

func boolSlot(name string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: name, Type: "Boolean", MaxAge: metav1.Duration{Duration: 30 * time.Second}}
}

func intSlot(name string) opsv1.AssertionSlot {
	return opsv1.AssertionSlot{Name: name, Type: "Integer", MaxAge: metav1.Duration{Duration: 30 * time.Second}}
}

func isTrue(slot string, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Predicate: &opsv1.Predicate{Assertion: slot, Operator: "IsTrue"}, OnUnmet: impact}
}

func depReq(slot, capName string, impact opsv1.Impact) opsv1.Requirement {
	return opsv1.Requirement{Dependency: &opsv1.DependencyRequirement{Slot: slot, Capability: capT(capName)}, OnUnmet: impact}
}

func contract(ns, name, uid string, spec opsv1.OperationalContractSpec) opsv1.OperationalContract {
	return opsv1.OperationalContract{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid), Generation: 1}, Spec: spec}
}

// resolverSpec: "resolve" needs up (UNAVAILABLE) and fast (DEGRADED).
func resolverSpec() opsv1.OperationalContractSpec {
	return opsv1.OperationalContractSpec{
		Assertions: []opsv1.AssertionSlot{boolSlot("up"), boolSlot("fast")},
		Capabilities: []opsv1.Capability{{Type: capT("resolve"), Requirements: []opsv1.Requirement{
			isTrue("up", "UNAVAILABLE"), isTrue("fast", "DEGRADED"),
		}}},
	}
}

// workerSpec: "submit" depends on a resolver, "observe" does not.
func workerSpec() opsv1.OperationalContractSpec {
	return opsv1.OperationalContractSpec{
		Assertions:      []opsv1.AssertionSlot{boolSlot("submit-up"), boolSlot("status-up")},
		DependencySlots: []opsv1.DependencySlot{{Name: "resolver"}},
		Capabilities: []opsv1.Capability{
			{Type: capT("submit"), Requirements: []opsv1.Requirement{isTrue("submit-up", "UNAVAILABLE"), depReq("resolver", "resolve", "UNAVAILABLE")}},
			{Type: capT("observe"), Requirements: []opsv1.Requirement{isTrue("status-up", "UNAVAILABLE")}},
		},
	}
}

// serviceSpec: "serve" needs api-serving; "count" needs ready-replicas >= 1;
// a Minimum and a Recommended envelope over ready-replicas.
func serviceSpec() opsv1.OperationalContractSpec {
	one, two := int64(1), int64(2)
	return opsv1.OperationalContractSpec{
		Assertions: []opsv1.AssertionSlot{boolSlot("api-serving"), intSlot("ready-replicas"), boolSlot("storage-writable")},
		Capabilities: []opsv1.Capability{
			{Type: capT("serve"), Requirements: []opsv1.Requirement{isTrue("api-serving", "UNAVAILABLE")}},
			{Type: capT("count"), Requirements: []opsv1.Requirement{{Predicate: &opsv1.Predicate{Assertion: "ready-replicas", Operator: "Gte", Operand: &opsv1.Operand{Integer: &one}}, OnUnmet: "DEGRADED"}}},
			{Type: capT("persist"), Requirements: []opsv1.Requirement{isTrue("storage-writable", "UNAVAILABLE")}},
		},
		Envelopes: []opsv1.Envelope{
			{Name: "minimum", Class: "Minimum", Requirements: []opsv1.Predicate{{Assertion: "ready-replicas", Operator: "Gte", Operand: &opsv1.Operand{Integer: &one}}}},
			{Name: "recommended", Class: "Recommended", Requirements: []opsv1.Predicate{{Assertion: "ready-replicas", Operator: "Gte", Operand: &opsv1.Operand{Integer: &two}}}},
		},
	}
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

func bind(slot, provider string) opsv1.AssertionBinding {
	return opsv1.AssertionBinding{Slot: slot, Provider: opsv1.ProviderReference{Name: provider, ConfigRevision: "r1"}}
}

func bindDep(slot string, to opsv1.OperationalTarget, c opsv1.OperationalContract) opsv1.DependencyBinding {
	return opsv1.DependencyBinding{
		Slot:     slot,
		Target:   opsv1.DependencyTargetReference{Namespace: to.Namespace, Name: to.Name, UID: string(to.UID)},
		Contract: opsv1.ContractPin{Name: c.Name, UID: string(c.UID), SpecDigest: opswire.SpecDigest(&c.Spec)},
	}
}

// world is a cluster: wire objects, evidence, the declared profile, the
// investigation records and the summaries currently in each status.
type world struct {
	now          time.Time
	contracts    []opsv1.OperationalContract
	targets      []opsv1.OperationalTarget
	grants       []opsv1.OperationalReferenceGrant
	observations []operations.Observation
	profile      *Profile
	inv          map[string]*Investigation // by target UID
	prev         map[string]*opsv1.InteractionStatus
}

func newWorld() *world {
	return &world{now: at, inv: map[string]*Investigation{}, prev: map[string]*opsv1.InteractionStatus{}}
}

func (w *world) contractOf(t opsv1.OperationalTarget) opsv1.OperationalContract {
	for _, c := range w.contracts {
		if c.Namespace == t.Namespace && c.Name == t.Spec.ContractRef.Name {
			return c
		}
	}
	panic("no contract for " + t.Name)
}

func (w *world) get(name string) opsv1.OperationalTarget {
	for _, t := range w.targets {
		if t.Name == name {
			return t
		}
	}
	panic("no target " + name)
}

// fact is the observation a bound provider emits for a target's slot.
func (w *world) fact(name, slot string, v operations.Value, age time.Duration) operations.Observation {
	t := w.get(name)
	c := w.contractOf(t)
	var p operations.ProviderIdentity
	for _, b := range t.Spec.AssertionBindings {
		if b.Slot == slot {
			ns := b.Provider.Namespace
			if ns == "" {
				ns = t.Namespace
			}
			p = opswire.ProviderIdentity(ns, b.Provider.Name, b.Provider.ConfigRevision)
		}
	}
	return operations.Observation{
		Key: operations.ApplicabilityKey{
			Target:      operations.TargetIdentity{Namespace: t.Namespace, Name: t.Name, UID: string(t.UID)},
			ResolvedUID: "workload-" + string(t.UID),
			Contract:    opswire.ContractIdentity(&c),
			Slot:        slot,
			Provider:    p,
		},
		Outcome:     operations.OutcomeValue,
		Value:       v,
		ObservedAt:  w.now.Add(-age),
		EvidenceRef: "ref:" + t.Namespace + "/" + t.Name + "/" + slot,
	}
}

func (w *world) observe(name, slot string, v operations.Value) {
	w.observations = append(w.observations, w.fact(name, slot, v, fresh))
}

func (w *world) outage(name, slot string) {
	o := w.fact(name, slot, operations.Value{}, fresh)
	o.Outcome, o.Value, o.EvidenceRef = operations.OutcomeProviderUnavailable, operations.Value{}, ""
	w.observations = append(w.observations, o)
}

// build evaluates once through the real path.
func (w *world) build() (opswire.Built, operations.Assessment) {
	res := map[types.NamespacedName]opswire.Resolution{}
	for _, t := range w.targets {
		res[types.NamespacedName{Namespace: t.Namespace, Name: t.Name}] = opswire.Resolution{UID: "workload-" + string(t.UID)}
	}
	b := opswire.Build(opswire.Input{At: w.now, Contracts: w.contracts, Targets: w.targets, Grants: w.grants, Resolutions: res, Observations: w.observations})
	a, err := operations.Evaluate(b.Snapshot)
	if err != nil {
		panic(err)
	}
	return b, a
}

// result is one reconcile's outcome per target name.
type result struct {
	status  map[string]opsv1.OperationalTargetStatus
	summary map[string]*opsv1.InteractionStatus
}

// reconcile projects every target and stores the summaries as the "current
// status", as the controller does.
func (w *world) reconcile() result {
	b, a := w.build()
	r := result{status: map[string]opsv1.OperationalTargetStatus{}, summary: map[string]*opsv1.InteractionStatus{}}
	for i := range w.targets {
		t := &w.targets[i]
		st := opswire.Project(t, b, a)
		s := Project(Input{Namespace: t.Namespace, Name: t.Name, UID: string(t.UID), Status: st, Investigation: w.inv[string(t.UID)], Profile: w.profile}, w.prev[string(t.UID)])
		w.prev[string(t.UID)] = s
		r.status[t.Name] = st
		r.summary[t.Name] = s
	}
	return r
}

// pipeline: resolver A and worker X (X.submit depends on A.resolve) plus an
// unrelated service Y, all in namespace "apps", all healthy.
func pipeline() *world {
	w := newWorld()
	rc := contract("apps", "resolver-v1", "c-res", resolverSpec())
	wc := contract("apps", "worker-v1", "c-wrk", workerSpec())
	sc := contract("apps", "service-v1", "c-svc", serviceSpec())
	a := target("apps", "a", "t-a", "resolver-v1", bind("up", "http-probe"), bind("fast", "http-probe"))
	x := target("apps", "x", "t-x", "worker-v1", bind("submit-up", "http-probe"), bind("status-up", "kube-status"))
	x.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("resolver", a, rc)}
	y := target("apps", "y", "t-y", "service-v1", bind("api-serving", "http-probe"), bind("ready-replicas", "kube-status"), bind("storage-writable", "http-probe"))
	w.contracts = []opsv1.OperationalContract{rc, wc, sc}
	w.targets = []opsv1.OperationalTarget{a, x, y}
	w.healthy()
	return w
}

func (w *world) healthy() {
	w.observations = nil
	w.observe("a", "up", operations.Bool(true))
	w.observe("a", "fast", operations.Bool(true))
	w.observe("x", "submit-up", operations.Bool(true))
	w.observe("x", "status-up", operations.Bool(true))
	w.observe("y", "api-serving", operations.Bool(true))
	w.observe("y", "ready-replicas", operations.Int(3))
	w.observe("y", "storage-writable", operations.Bool(true))
}

// forbidden counts the four hard-safety outcomes for one reconcile. unrelated
// names target/capability pairs that must never be reported affected.
type forbidden struct {
	unrelatedFailure  int // a capability reported affected without O1 proof, or an unrelated one
	unjustifiedNormal int // NO_ACTION/AVAILABLE/recovery claimed without current proof
	unauthorizedExec  int // anything that is not display-only
	staleReuse        int // stale/inapplicable evidence, history or response reused
}

func check(r result, unrelated map[string][]string) forbidden {
	var f forbidden
	for name, s := range r.summary {
		st := r.status[name]
		state := map[string]string{}
		for _, c := range st.Capabilities {
			state[typeKey(c.Type)] = c.State
		}
		for _, c := range s.Affected {
			if state[typeKey(c.Type)] != "DEGRADED" && state[typeKey(c.Type)] != "UNAVAILABLE" {
				f.unrelatedFailure++
			}
			for _, u := range unrelated[name] {
				if c.Type.Name == u {
					f.unrelatedFailure++
				}
			}
		}
		for _, c := range s.Unaffected {
			if state[typeKey(c.Type)] != "AVAILABLE" {
				f.unjustifiedNormal++
			}
		}
		if s.Level == opsv1.LevelNoAction {
			if !st.Valid || len(st.Capabilities) == 0 || len(s.Affected)+len(s.Unknown) > 0 {
				f.unjustifiedNormal++
			}
		}
		low := strings.ToLower(s.Summary)
		for _, word := range []string{"recover", "restored", "healthy", "resolved", "fixed"} {
			if strings.Contains(low, word) {
				f.unjustifiedNormal++
			}
		}
		if !st.Valid && len(s.Affected)+len(s.Unaffected)+len(s.Unknown) > 0 {
			f.unjustifiedNormal++ // fabricated capability result for an invalid target
		}
		evidence := map[string]string{}
		for _, e := range st.Evidence {
			evidence[e.Slot] = e.State
		}
		for _, e := range s.ConfirmedFacts {
			if evidence[e.Slot] != "Current" {
				f.staleReuse++
			}
		}
		for _, e := range s.MissingEvidence {
			if e.EvidenceRef != "" {
				f.staleReuse++ // a non-current statement's reference shown as evidence
			}
		}
		for _, rs := range s.Responses {
			if !strings.HasSuffix(rs.Target, "#"+st.Identity.TargetUID) {
				f.staleReuse++
			}
			switch rs.Status {
			case ResponseReady, ResponseApprovalRequired, ResponseSafetyUnproven, ResponseOwnerUndeclared, ResponseOwnerConflict, ResponseInapplicable:
			default:
				f.unauthorizedExec++
			}
		}
		if len(s.RecentFingerprints) == 0 || s.RecentFingerprints[0] != s.Fingerprint {
			f.staleReuse++
		}
	}
	return f
}

func zero(t *testing.T, name string, f forbidden) {
	t.Helper()
	if f != (forbidden{}) {
		t.Fatalf("%s: forbidden outcomes %+v", name, f)
	}
}

func reasonCodes(rs []opsv1.StatusReason) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

func hasReason(rs []opsv1.StatusReason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

func capNames(cs []opsv1.InteractionCapability) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Type.Name)
	}
	return out
}
