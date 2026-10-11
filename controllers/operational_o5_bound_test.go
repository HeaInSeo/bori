package controllers

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Codex r4237824550: the summary shows at most eight actions, but the
// required human escalation is judged on all of them, whatever position the
// alerting one has.

var levelRank = map[opsv1.InteractionLevel]int{
	opsv1.LevelNoAction: 0, opsv1.LevelAwareness: 1, opsv1.LevelDecisionRequired: 2, opsv1.LevelImmediateIntervention: 3,
}

// crowdedProfile declares restart (no approval, no precondition) and n
// ordinary responses for y.work that wait on y.work itself: with y.work
// down they are blocked and do not compete, so restart runs by policy.
func crowdedProfile(n int) *interaction.Profile {
	p := restartProfile()
	p.Responses[0].RequiresApproval = false
	for i := 0; i < n; i++ {
		r := p.Responses[0]
		r.Action.Name = fmt.Sprintf("noop-%02d", i)
		r.Preconditions = []opsv1.CapabilityType{ct("work")}
		ex := *r.Execution
		r.Execution = &ex
		p.Responses = append(p.Responses, r)
	}
	return p
}

func TestO5ControllerEscalatesOnActionsBeyondTheDisplayBound(t *testing.T) {
	for _, c := range []struct {
		name  string
		b     actiontest.Behaviour
		human string
		ok    func(a opsv1.InteractionAction) bool
	}{
		{"timed out", actiontest.AcceptOnly, interaction.ReasonActionOutcomeUnknown,
			func(a opsv1.InteractionAction) bool { return a.Phase == "TimedOut" }},
		{"not recovered", actiontest.Succeed, interaction.ReasonActionNotRecovered,
			func(a opsv1.InteractionAction) bool { return a.Recovery == "NotRecovered" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &o5Env{o3Env: newO4Env(t, crowdedProfile(40)), actor: actiontest.NewActor(c.b), decisions: &actiontest.Decisions{}}
			e.r.Actions = &action.Engine{
				Journal: action.NewSimulatedDurableJournal(),
				Providers: action.Registry{"actor": {Provider: e.actor, Owner: "app-oncall",
					Namespaces: []string{"apps"}, Actions: []string{"restart@r1"}}},
				Decisions: e.decisions, Verifier: actiontest.NewSigner(map[string]string{"alice": "alice-key"}),
			}
			e.reconcile()
			e.breakY()
			for i := 0; i < 30 && e.actor.TotalExecutions() == 0; i++ {
				e.clk.add(3 * time.Second)
				e.reconcile()
			}
			for i := 0; i < 30; i++ { // past the completion timeout and the recovery window
				e.clk.add(3 * time.Second)
				e.reconcile()
			}
			s := e.interaction("y")
			if e.actor.TotalExecutions() != 1 || len(e.actor.Handoffs) == 0 || e.actor.Handoffs[0].Action.Name != "restart" {
				t.Fatalf("setup: executions %d", e.actor.TotalExecutions())
			}
			if len(s.Actions) != 8 {
				t.Fatalf("shown %d actions, want the bound 8", len(s.Actions))
			}
			for _, a := range s.Actions {
				if a.Name == "restart@r1" {
					t.Fatalf("setup: restart is shown (%+v); the test needs it beyond the bound", a)
				}
			}
			if e.capState("y", "work") == "AVAILABLE" {
				t.Fatal("setup: work recovered")
			}
			if levelRank[s.Level] < levelRank[opsv1.LevelDecisionRequired] || !hasReason(s.HumanReasons, c.human, "restart@r1") {
				t.Fatalf("level %s human %+v: the escalation of an action beyond the bound was lost", s.Level, s.HumanReasons)
			}
			for _, n := range []string{"a", "x"} { // unrelated targets carry nothing of y's actions
				if o := e.interaction(n); len(o.Actions) != 0 || hasCode(o.HumanReasons, c.human) {
					t.Fatalf("%s: %+v", n, o)
				}
			}
		})
	}
}

func hasReason(rs []opsv1.StatusReason, code, subject string) bool {
	for _, r := range rs {
		if r.Code == code && r.Subject == subject {
			return true
		}
	}
	return false
}

// boundFixture is a real y status with work UNAVAILABLE and count AVAILABLE,
// without its interaction summary.
func boundFixture(t *testing.T) opsv1.OperationalTargetStatus {
	t.Helper()
	e := newO4Env(t, restartProfile())
	e.reconcile()
	e.app.set("/y", false, 0)
	for i := 0; i < 10 && e.capState("y", "work") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	st := e.status("y")
	st.Interaction = nil
	if e.capState("y", "work") != "UNAVAILABLE" || e.capState("y", "count") != "AVAILABLE" {
		t.Fatalf("fixture: work %s count %s", e.capState("y", "work"), e.capState("y", "count"))
	}
	return st
}

// wantEscalation is the oracle, from the untruncated fixture alone: every
// action whose capability is not AVAILABLE and whose outcome is unknown or
// not recovered needs a person, named by the first such action.
func wantEscalation(st opsv1.OperationalTargetStatus, vs []action.View) map[string]string {
	available := map[string]bool{}
	for _, c := range st.Capabilities {
		available[c.Type.Name] = c.State == "AVAILABLE"
	}
	want := map[string]string{}
	for _, v := range vs {
		if available[v.For.Name] || v.Recovery == action.RecoveryRecovered || v.Recovery == action.RecoveryInapplicable {
			continue
		}
		code := ""
		switch {
		case v.Phase == action.PhaseTimedOut || v.Phase == action.PhaseWithdrawn:
			code = "action-outcome-unknown"
		case v.Recovery == action.RecoveryNotRecovered || v.Recovery == action.RecoveryPartial || v.Recovery == action.RecoveryUnconfirmed:
			code = "action-not-recovered"
		}
		if n, ok := want[code]; code != "" && (!ok || v.Action.String() < n) {
			want[code] = v.Action.String()
		}
	}
	return want
}

func ordinary(n int) []action.View {
	var vs []action.View
	for i := 0; i < n; i++ {
		vs = append(vs, action.View{ProposalID: fmt.Sprintf("p%02d", i), Digest: "d",
			Action: interaction.ActionRef{Name: fmt.Sprintf("noop-%02d", i), Revision: "r1"},
			For:    capOf("work"), Phase: action.PhaseAwaitingApproval, Attempt: 1})
	}
	return vs
}

func capOf(name string) operations.CapabilityType {
	return operations.CapabilityType{Domain: "example.io", Name: name, Revision: "v1"}
}

// The same chain the controller runs (action.Status → interaction.Project
// → actionsOf) on fixtures with an alerting action placed beyond the bound.
func TestO5EscalationIsJudgedBeforeTheDisplayBound(t *testing.T) {
	st := boundFixture(t)
	project := func(vs []action.View) *opsv1.InteractionStatus {
		return interaction.Project(interaction.Input{Namespace: "apps", Name: "y", UID: "t-y", Status: st,
			Actions: action.Status(vs)}, nil)
	}
	alerts := map[string]func(v *action.View){
		"TimedOut":     func(v *action.View) { v.Phase = action.PhaseTimedOut },
		"Withdrawn":    func(v *action.View) { v.Phase = action.PhaseWithdrawn },
		"NotRecovered": func(v *action.View) { v.Phase, v.Recovery = action.PhaseSucceeded, action.RecoveryNotRecovered },
		"Partial":      func(v *action.View) { v.Phase, v.Recovery = action.PhaseFailed, action.RecoveryPartial },
		"Unconfirmed":  func(v *action.View) { v.Phase, v.Recovery = action.PhaseSucceeded, action.RecoveryUnconfirmed },
	}
	quiet := map[string]func(v *action.View){
		"Recovered":    func(v *action.View) { v.Phase, v.Recovery = action.PhaseTimedOut, action.RecoveryRecovered },
		"Inapplicable": func(v *action.View) { v.Phase, v.Recovery = action.PhaseSucceeded, action.RecoveryInapplicable },
		"capability AVAILABLE": func(v *action.View) {
			v.Phase, v.For = action.PhaseTimedOut, capOf("count")
		},
		"in flight": func(v *action.View) { v.Phase = action.PhaseAccepted },
	}
	critical := func(id string, tune func(v *action.View)) action.View {
		v := action.View{ProposalID: id, Digest: "d", Action: interaction.ActionRef{Name: "restart", Revision: "r1"},
			For: capOf("work"), Phase: action.PhaseAwaitingApproval, Attempt: 1}
		tune(&v)
		return v
	}
	check := func(t *testing.T, vs []action.View) {
		t.Helper()
		want := wantEscalation(st, vs)
		var reversed []action.View
		for i := len(vs) - 1; i >= 0; i-- {
			reversed = append(reversed, vs[i])
		}
		s := project(vs)
		if n := len(vs); len(s.Actions) != min(n, 8) || !sort.SliceIsSorted(s.Actions, func(i, j int) bool { return s.Actions[i].Proposal < s.Actions[j].Proposal }) {
			t.Fatalf("shown %d of %d actions", len(s.Actions), n)
		}
		for code, name := range want {
			if !hasReason(s.HumanReasons, code, name) || levelRank[s.Level] < levelRank[opsv1.LevelDecisionRequired] {
				t.Fatalf("level %s human %+v, want %s naming %s", s.Level, s.HumanReasons, code, name)
			}
		}
		for _, r := range s.HumanReasons {
			if _, ok := want[r.Code]; (r.Code == "action-outcome-unknown" || r.Code == "action-not-recovered") && !ok {
				t.Fatalf("unexpected %+v", r)
			}
		}
		if len(want) == 0 && s.Level != opsv1.LevelAwareness {
			t.Fatalf("level %s without an alerting action", s.Level)
		}
		if r := project(reversed); !reflect.DeepEqual(r.Actions, s.Actions) || r.Fingerprint != s.Fingerprint ||
			r.Level != s.Level || !reflect.DeepEqual(r.HumanReasons, s.HumanReasons) {
			t.Fatalf("input order changed the summary")
		}
	}
	for name, tune := range alerts {
		for _, at := range []string{"p08", "p99", "a00"} { // ninth, last, first
			t.Run(name+"/"+at, func(t *testing.T) {
				vs := append(ordinary(8), critical(at, tune))
				if at != "a00" && len(wantEscalation(st, vs)) != 1 {
					t.Fatal("oracle")
				}
				check(t, vs)
			})
		}
	}
	for name, tune := range quiet {
		t.Run("control "+name, func(t *testing.T) {
			vs := append(ordinary(8), critical("p99", tune))
			if len(wantEscalation(st, vs)) != 0 {
				t.Fatal("oracle")
			}
			check(t, vs)
		})
	}
	t.Run("more alerting actions than the bound", func(t *testing.T) {
		var vs []action.View
		for i := 0; i < 12; i++ {
			for _, k := range []string{"TimedOut", "NotRecovered"} {
				v := critical(fmt.Sprintf("q%s%02d", k, i), alerts[k])
				v.Action.Name = fmt.Sprintf("act-%s-%02d", k, i)
				vs = append(vs, v)
			}
		}
		if len(wantEscalation(st, vs)) != 2 {
			t.Fatal("oracle")
		}
		check(t, vs)
	})
	t.Run("control: no actions", func(t *testing.T) {
		s := project(nil)
		if len(s.Actions) != 0 || s.Level != opsv1.LevelAwareness {
			t.Fatalf("%+v", s)
		}
	})
}
