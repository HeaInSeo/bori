package action_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// The L4 world: one generic contract with three independent capabilities,
// two targets bound to it, two evidence providers, and the reference actor.
//
//	serve   ← api-serving IsTrue        (application fact)
//	persist ← storage-writable IsTrue   (application fact)
//	ready   ← ready-replicas Gte 1      (workload readiness, "Pod Ready")

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

const maxAge = 30 * time.Second

func cap(name string) operations.CapabilityType {
	return operations.CapabilityType{Domain: "example.io", Name: name, Revision: "v1"}
}

func opsCap(name string) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: "example.io", Name: name, Revision: "v1"}
}

func boolCap(name, slot string) operations.Capability {
	return operations.Capability{Type: cap(name), Requirements: []operations.Requirement{{
		Predicate: &operations.Predicate{Assertion: slot, Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable}}}
}

type world struct {
	t        *testing.T
	now      time.Time
	contract operations.Contract
	targets  map[string]*operations.Target // by name
	truth    map[string]map[string]operations.Value
	obs      map[string]operations.Observation // target/slot → latest
	frozen   map[string]bool                   // target/slot whose provider stopped refreshing
	profile  *interaction.Profile

	journal   action.Journal
	actor     *actiontest.Actor
	decisions *actiontest.Decisions
	signer    *actiontest.Signer
	eng       *action.Engine

	history []stepLog
	pending []executed // handoffs newly executed during the current step
	ck      forbidden
	// effect is what the authoritative actor's execution does to the world.
	effect func(h action.Handoff)
}

type stepLog struct {
	at   time.Time
	snap operations.Snapshot
	a    operations.Assessment
	out  action.Output
}

type executed struct {
	h    action.Handoff
	step int
}

var keys = map[string]string{"alice": "alice-key", "bob": "bob-key", "mallory": "mallory-key"}

func newWorld(t *testing.T) *world {
	w := &world{
		t: t, now: t0,
		contract: operations.Contract{
			Identity: operations.ContractIdentity{Namespace: "apps", Name: "svc-v1", UID: "c-1", SpecDigest: "d1"},
			Assertions: []operations.AssertionSlot{
				{Name: "api-serving", Type: operations.TypeBoolean, MaxAge: maxAge},
				{Name: "storage-writable", Type: operations.TypeBoolean, MaxAge: maxAge},
				{Name: "ready-replicas", Type: operations.TypeInteger, MaxAge: maxAge},
			},
			Capabilities: []operations.Capability{
				boolCap("serve", "api-serving"), boolCap("persist", "storage-writable"),
				{Type: cap("ready"), Requirements: []operations.Requirement{{
					Predicate: &operations.Predicate{Assertion: "ready-replicas", Op: operations.OpGte, Operand: operations.Int(1)},
					OnUnmet:   operations.ImpactUnavailable}}},
			},
		},
		targets: map[string]*operations.Target{},
		truth:   map[string]map[string]operations.Value{},
		obs:     map[string]operations.Observation{},
		frozen:  map[string]bool{},
	}
	for _, n := range []string{"a", "y"} {
		w.targets[n] = &operations.Target{
			Identity:    operations.TargetIdentity{Namespace: "apps", Name: n, UID: "t-" + n},
			ResolvedUID: "w-" + n, ContractRef: w.contract.Identity,
			AssertionBindings: []operations.AssertionBinding{
				{Slot: "api-serving", Provider: operations.ProviderIdentity{Name: "app", ConfigRevision: "r1"}},
				{Slot: "storage-writable", Provider: operations.ProviderIdentity{Name: "app", ConfigRevision: "r1"}},
				{Slot: "ready-replicas", Provider: operations.ProviderIdentity{Name: "kube", ConfigRevision: "r1"}},
			},
		}
		w.truth[n] = map[string]operations.Value{
			"api-serving": operations.Bool(true), "storage-writable": operations.Bool(true), "ready-replicas": operations.Int(2),
		}
	}
	w.truth["y"]["storage-writable"] = operations.Bool(false) // y.persist UNAVAILABLE

	w.profile = &interaction.Profile{Revision: "p1", Responses: []interaction.Response{remount()}}
	w.journal = action.NewSimulatedDurableJournal()
	w.actor = actiontest.NewActor(actiontest.Succeed)
	w.decisions = &actiontest.Decisions{}
	w.signer = actiontest.NewSigner(map[string]string{"alice": keys["alice"], "bob": keys["bob"], "mallory": keys["mallory"]})
	w.restart()
	w.actor.OnExecute = func(h action.Handoff) {
		w.pending = append(w.pending, executed{h: h, step: len(w.history)})
		if w.effect != nil {
			w.effect(h)
		}
	}
	return w
}

// remount: restore y.persist; owner storage-oncall; approval by alice.
func remount() interaction.Response {
	return interaction.Response{
		Action: interaction.ActionRef{Name: "remount", Revision: "r1"},
		Target: interaction.TargetRef{Namespace: "apps", Name: "y"},
		For:    opsCap("persist"), Owner: "storage-oncall", RequiresApproval: true,
		Preconditions: []opsv1.CapabilityType{opsCap("serve")},
		Execution: &interaction.Execution{
			Provider: "actor", Approvers: []string{"alice"},
			ExpectedImpact:    []opsv1.CapabilityType{opsCap("persist")},
			AckTimeout:        metav1.Duration{Duration: 10 * time.Second},
			CompletionTimeout: metav1.Duration{Duration: 60 * time.Second},
			RecoveryWindow:    metav1.Duration{Duration: 60 * time.Second},
		},
	}
}

// restart replaces the engine (a BORI process restart) over the same journal
// and provider registry.
func (w *world) restart() {
	w.eng = &action.Engine{
		Journal: w.journal,
		Providers: action.Registry{"actor": {
			Provider: w.actor, Owner: "storage-oncall", Namespaces: []string{"apps"}, Actions: []string{"remount@r1", "failover@r1"},
		}},
		Decisions: w.decisions, Verifier: w.signer,
	}
}

func okey(target, slot string) string { return target + "/" + slot }

// observe makes every provider report the current truth now, except frozen
// (stale) slots.
func (w *world) observe() {
	for n, t := range w.targets {
		for _, b := range t.AssertionBindings {
			k := okey(n, b.Slot)
			if w.frozen[k] {
				continue
			}
			w.obs[k] = operations.Observation{
				Key: operations.ApplicabilityKey{Target: t.Identity, ResolvedUID: t.ResolvedUID, Contract: t.ContractRef,
					Slot: b.Slot, Provider: b.Provider},
				Outcome: operations.OutcomeValue, Value: w.truth[n][b.Slot], ObservedAt: w.now,
				EvidenceRef: fmt.Sprintf("%s:%s@%d", b.Provider.Name, k, w.now.Unix()),
			}
		}
	}
}

func (w *world) snapshot() operations.Snapshot {
	s := operations.Snapshot{At: w.now, Contracts: []operations.Contract{w.contract}}
	for _, n := range []string{"a", "y"} {
		if t, ok := w.targets[n]; ok {
			s.Targets = append(s.Targets, *t)
		}
	}
	for _, n := range []string{"a", "y"} {
		for _, slot := range []string{"api-serving", "ready-replicas", "storage-writable"} {
			if o, ok := w.obs[okey(n, slot)]; ok {
				s.Observations = append(s.Observations, o)
			}
		}
	}
	return s
}

// step: providers observe, O1 evaluates, the engine steps, the checker runs.
func (w *world) step() action.Output {
	w.t.Helper()
	w.observe()
	s := w.snapshot()
	a, err := operations.Evaluate(s)
	if err != nil {
		w.t.Fatal(err)
	}
	w.pending = nil
	out, err := w.eng.Step(context.Background(), action.Input{Snapshot: s, Assessment: a, Profile: w.profile, Now: w.now})
	if err != nil {
		w.t.Fatal(err)
	}
	w.history = append(w.history, stepLog{at: w.now, snap: s, a: a, out: out})
	w.check()
	return out
}

func (w *world) advance(d time.Duration) action.Output {
	w.now = w.now.Add(d)
	return w.step()
}

// view returns y's single view.
func (w *world) view(out action.Output) action.View {
	w.t.Helper()
	vs := out.Views["t-y"]
	if len(vs) != 1 {
		w.t.Fatalf("y views %+v", vs)
	}
	return vs[0]
}

func (w *world) decide(v action.View, principal, key string, verdict action.Verdict, at time.Time) {
	d := action.Decision{ID: fmt.Sprintf("d%d", len(w.history)), ProposalID: v.ProposalID, Digest: v.Digest,
		Principal: principal, Verdict: verdict, DecidedAt: at}
	w.decisions.Add(actiontest.Sign(d, key))
}

func (w *world) approve(v action.View) { w.decide(v, "alice", keys["alice"], action.Approve, w.now) }

func (w *world) records() []action.Record {
	rs, err := w.journal.List(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return rs
}

func hasReason(v action.View, code string) bool {
	for _, r := range v.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
