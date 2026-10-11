package controllers

import (
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// O5 through the real controller: O3 providers (HTTP + Kubernetes status) →
// O1 → opswire → O4 summary with O5 proposals, the reference actor as the
// external authority, recovery judged on fresh provider evidence only.

func restartProfile() *interaction.Profile {
	return &interaction.Profile{Revision: "p1", Responses: []interaction.Response{{
		Action: interaction.ActionRef{Name: "restart", Revision: "r1"},
		Target: interaction.TargetRef{Namespace: "apps", Name: "y"},
		For:    ct("work"), Owner: "app-oncall", RequiresApproval: true,
		Execution: &interaction.Execution{
			Provider: "actor", Approvers: []string{"alice"},
			ExpectedImpact:    []opsv1.CapabilityType{ct("work")},
			AckTimeout:        metav1.Duration{Duration: 10 * time.Second},
			CompletionTimeout: metav1.Duration{Duration: 30 * time.Second},
			RecoveryWindow:    metav1.Duration{Duration: 30 * time.Second},
		},
	}}}
}

type o5Env struct {
	*o3Env
	actor     *actiontest.Actor
	decisions *actiontest.Decisions
}

func newO5Env(t *testing.T) *o5Env {
	e := &o5Env{o3Env: newO4Env(t, restartProfile()), actor: actiontest.NewActor(actiontest.Succeed), decisions: &actiontest.Decisions{}}
	e.r.Actions = &action.Engine{
		Journal: action.NewSimulatedDurableJournal(),
		Providers: action.Registry{"actor": {Provider: e.actor, Owner: "app-oncall",
			Namespaces: []string{"apps"}, Actions: []string{"restart@r1"}}},
		Decisions: e.decisions, Verifier: actiontest.NewSigner(map[string]string{"alice": "alice-key"}),
	}
	return e
}

func (e *o5Env) yAction() opsv1.InteractionAction {
	e.t.Helper()
	as := e.interaction("y").Actions
	if len(as) != 1 {
		e.t.Fatalf("y actions %+v", as)
	}
	return as[0]
}

func (e *o5Env) breakY() {
	e.app.set("/y", false, 0)
	for i := 0; i < 10 && e.capState("y", "work") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
}

func (e *o5Env) until(cond func() bool) {
	e.t.Helper()
	for i := 0; i < 30 && !cond(); i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	if !cond() {
		e.t.Fatalf("condition not reached; y actions %+v", e.interaction("y").Actions)
	}
}

func (e *o5Env) workloadGeneration(name string) int64 {
	var d appsv1.Deployment
	_ = e.c.Get(e.driver(), types.NamespacedName{Namespace: "apps", Name: name}, &d)
	return d.Generation
}

func TestO5ControllerApprovedActionRecoversOnFreshEvidence(t *testing.T) {
	e := newO5Env(t)
	e.actor.OnExecute = func(action.Handoff) { e.app.set("/y", true, 0) } // the external authority fixes the app
	e.reconcile()
	if len(e.interaction("y").Actions) != 0 {
		t.Fatal("proposal while healthy")
	}
	e.breakY()
	a := e.yAction()
	if a.Phase != "AwaitingApproval" || e.interaction("y").Level != opsv1.LevelDecisionRequired {
		t.Fatalf("%+v level %s", a, e.interaction("y").Level)
	}
	for i := 0; i < 5; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	if len(e.actor.Handoffs) != 0 {
		t.Fatal("handed off before approval")
	}
	d := action.Decision{ID: "d1", ProposalID: a.Proposal, Digest: a.Digest, Principal: "alice", Verdict: action.Approve, DecidedAt: e.clk.now()}
	e.decisions.Add(actiontest.Sign(d, "alice-key"))
	e.until(func() bool { return e.yAction().Recovery == "Recovered" })
	if e.yAction().Phase != "Succeeded" || e.actor.TotalExecutions() != 1 {
		t.Fatalf("%+v executions %d", e.yAction(), e.actor.TotalExecutions())
	}
	if e.otherWrites != 0 || e.workloadGeneration("y") != 1 {
		t.Fatalf("BORI wrote outside ops status: other=%d generation=%d", e.otherWrites, e.workloadGeneration("y"))
	}
	for i := 0; i < 10; i++ { // recovered and steady: nothing to write
		e.clk.add(3 * time.Second)
		if w := e.reconcile(); len(w) != 0 {
			t.Fatalf("steady round %d wrote %v", i, w)
		}
	}
}

// Pods become Ready (the actor "rolled out") but the application capability
// stays down: the action is Succeeded, recovery is NotRecovered and a person
// is asked to decide.
func TestO5ControllerPodReadyIsNotRecovery(t *testing.T) {
	e := newO5Env(t)
	e.actor.OnExecute = func(action.Handoff) { e.scaleDriver("y", 3) }
	e.reconcile()
	e.breakY()
	a := e.yAction()
	e.decisions.Add(actiontest.Sign(action.Decision{ID: "d1", ProposalID: a.Proposal, Digest: a.Digest,
		Principal: "alice", Verdict: action.Approve, DecidedAt: e.clk.now()}, "alice-key"))
	e.until(func() bool { r := e.yAction().Recovery; return r != "" && r != "Pending" })
	a = e.yAction()
	s := e.interaction("y")
	if a.Phase != "Succeeded" || a.Recovery != "NotRecovered" || s.Level != opsv1.LevelDecisionRequired ||
		!hasCode(s.HumanReasons, interaction.ReasonActionNotRecovered) {
		t.Fatalf("%+v level %s human %+v", a, s.Level, s.HumanReasons)
	}
	if e.capState("y", "count") != "AVAILABLE" || e.capState("y", "work") != "UNAVAILABLE" {
		t.Fatalf("count %s work %s", e.capState("y", "count"), e.capState("y", "work"))
	}
	if e.otherWrites != 0 {
		t.Fatalf("other writes %d", e.otherWrites)
	}
}

// The shipped wiring (no provider, non-durable journal, no verifier) shows
// the proposal and why it cannot proceed, and never hands anything off.
func TestO5ProductionWiringNeverHandsOff(t *testing.T) {
	e := newO4Env(t, restartProfile())
	e.r.Actions = &action.Engine{Journal: action.NewMemJournal(), Providers: action.Registry{}}
	e.reconcile()
	e.app.set("/y", false, 0)
	for i := 0; i < 10 && e.capState("y", "work") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	as := e.interaction("y").Actions
	if len(as) != 1 || as[0].Phase != "Blocked" || !hasCode(as[0].Reasons, action.ReasonJournalNotDurable) ||
		!hasCode(as[0].Reasons, action.ReasonProviderUnregistered) || !hasCode(as[0].Reasons, action.ReasonApprovalDeclared) {
		t.Fatalf("%+v", as)
	}
	if e.otherWrites != 0 {
		t.Fatalf("other writes %d", e.otherWrites)
	}
}

// Off keeps the O4 status byte-identical, and on (without in-flight work)
// adds no provider I/O.
func TestO5OffEquivalenceAndNoAddedEvidenceIO(t *testing.T) {
	run := func(on bool) ([]opsv1.OperationalTargetStatus, any) {
		e := newO4Env(t, restartProfile())
		if on {
			e.r.Actions = &action.Engine{Journal: action.NewMemJournal(), Providers: action.Registry{}}
		}
		script := []func(){func() {}, func() { e.app.set("/a", false, 0) }, func() { e.app.set("/a", true, 0) }}
		for _, s := range script {
			s()
			for i := 0; i < 5; i++ {
				e.clk.add(3 * time.Second)
				e.reconcile()
			}
		}
		var out []opsv1.OperationalTargetStatus
		for _, n := range []string{"a", "x", "y"} {
			out = append(out, e.status(n))
		}
		return out, []any{e.r.Investigator.Stats(), e.app.hitCount("/a"), e.app.hitCount("/y"), e.kubeGets}
	}
	off, ioOff := run(false)
	on, ioOn := run(true)
	if !reflect.DeepEqual(ioOff, ioOn) {
		t.Fatalf("evidence I/O differs: %v vs %v", ioOff, ioOn)
	}
	// y never triggers in this script, so the statuses must be identical.
	if !reflect.DeepEqual(off, on) {
		t.Fatalf("status differs with O5 on and nothing to propose")
	}
	disabled := newO3Env(t)
	disabled.r.Actions = &action.Engine{Journal: action.NewMemJournal(), Providers: action.Registry{}}
	disabled.reconcile() // Interaction off: the engine is not consulted
	if disabled.status("y").Interaction != nil {
		t.Fatal("summary without interaction enabled")
	}
}
