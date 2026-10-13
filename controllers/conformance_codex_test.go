package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// Codex review of 2279312 (r4245188586, r4245188592, r4245188596): the
// recovery check must rest on observations actually delivered after the
// actor's own end of the execution; an execution must carry exactly an
// approval that was given for its proposal and digest; and a display-only
// response must never execute. Each fault is injected into an otherwise
// genuine run, next to a normal control.

// untilExecuted runs a scenario up to its first approve step and stops right
// after the reconcile in which the actor executed, before BORI could observe
// anything newer.
func untilExecuted(t *testing.T, pack, scenario string) *confEnv {
	t.Helper()
	p, scs := packByName(t, pack)
	s := scs[scenario]
	e := newConfEnv(t, p, false)
	setEffect(e, s.OnExecute)
	e.settle()
	for _, st := range s.Steps {
		if len(st.Approve) > 0 {
			for _, ref := range st.Approve {
				e.approve(ref)
			}
			e.reconcile()
			e.check()
			if e.actor.TotalExecutions() == 0 {
				t.Fatal("setup: nothing executed")
			}
			return e
		}
		e.apply(st)
		if st.Advance != "" {
			d, _ := time.ParseDuration(st.Advance)
			e.clk.add(d)
		}
		for r := 0; r < 10; r++ {
			if r > 0 {
				e.clk.add(3 * time.Second)
			}
			e.reconcile()
			e.check()
		}
	}
	t.Fatal("setup: no approve step")
	return nil
}

func markRecovered(cl claims, target string) {
	st := cl[target]
	for i := range st.Interaction.Actions {
		st.Interaction.Actions[i].Recovery = "Recovered"
	}
	cl[target] = st
}

// inject hands the actor a variant of the genuine execution, as a broken
// controller would, under a fresh key.
func inject(t *testing.T, e *confEnv, n int, tune func(h *action.Handoff)) {
	t.Helper()
	var h0 action.Handoff
	for _, h := range e.actor.Handoffs {
		if e.actor.Executions[h.Key] > 0 {
			h0 = h
		}
	}
	if h0.Key == "" {
		t.Fatal("setup: no genuine execution")
	}
	h := h0
	h.Key = fmt.Sprintf("%s/injected-%d", h0.ProposalID, n)
	tune(&h)
	if _, err := e.actor.Submit(context.Background(), h); err != nil {
		t.Fatal(err)
	}
}

func TestConformanceCheckerCodexFindings(t *testing.T) {
	recoveries := []struct{ pack, scenario, target string }{
		{"simple-http-service", "action-succeeds-and-recovers", "web"},
		{"jumi-artifact-handoff", "restart-with-registry-recovery", "jumi"},
	}
	for _, rc := range recoveries {
		// r4245188586: the actuation already restored the fact, but nothing
		// has been observed since the execution ended.
		t.Run("Recovered before any post-execution observation/"+rc.pack, func(t *testing.T) {
			e := untilExecuted(t, rc.pack, rc.scenario)
			e.ck = confForbidden{}
			cl := e.claims()
			markRecovered(cl, rc.target)
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, 1, 0, 0)
		})
		// The execution is still live at the actor: it has no end yet.
		t.Run("Recovered while the execution is live at the actor/"+rc.pack, func(t *testing.T) {
			e := untilExecuted(t, rc.pack, rc.scenario)
			for _, h := range e.actor.Handoffs {
				e.actor.Settle(h.Key, actiontest.AcceptOnly)
			}
			e.endedAt = map[string]time.Time{}
			e.ck = confForbidden{}
			cl := e.claims()
			markRecovered(cl, rc.target)
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, 1, 0, 0)
		})
		t.Run("control: Recovered after post-execution observations/"+rc.pack, func(t *testing.T) {
			e := untilExecuted(t, rc.pack, rc.scenario)
			for r := 0; r < 10; r++ {
				e.clk.add(3 * time.Second)
				e.reconcile()
				e.check()
			}
			e.ck = confForbidden{}
			cl := e.claims()
			recovered := false
			for _, a := range cl[rc.target].Interaction.Actions {
				recovered = recovered || a.Recovery == "Recovered"
			}
			if !recovered {
				t.Fatalf("setup: BORI did not claim recovery: %+v", cl[rc.target].Interaction.Actions)
			}
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, 0, 0)
		})
	}

	// Exactly one fact lacks a healthy observation strictly after the end:
	// either it was delivered only at the very instant the execution ended
	// (the same reconcile, before the actuation), or the only later one is
	// unhealthy. The other fact is observed healthy after the end.
	webAt := func(at time.Time, serving string) servedObs {
		return servedObs{path: "/web", at: at, values: map[string]string{"serving": serving, "latencyOK": "true", "metricsUp": "true"}}
	}
	for _, c := range []struct {
		name          string
		web           func(end time.Time) servedObs
		replicasAfter bool
	}{
		{"serving observed only at the instant of the end", func(end time.Time) servedObs { return webAt(end, "true") }, true},
		{"replicas read only at the instant of the end", func(end time.Time) servedObs { return webAt(end.Add(time.Second), "true") }, false},
		{"serving observed after the end but unhealthy", func(end time.Time) servedObs { return webAt(end.Add(time.Second), "false") }, true},
	} {
		t.Run("Recovered with "+c.name, func(t *testing.T) {
			e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
			var end time.Time
			for _, at := range e.endedAt {
				end = at
			}
			e.ev.mu.Lock()
			e.ev.served = append(e.ev.served, c.web(end))
			e.ev.mu.Unlock()
			e.mu.Lock()
			read := kubeRead{namespace: "conf-http", name: "web", uid: "w-web", at: end, status: map[string]string{"readyReplicas": "2"}}
			if c.replicasAfter {
				read.at = end.Add(time.Second)
			}
			e.kubeReads = append(e.kubeReads, read)
			e.mu.Unlock()
			e.ck = confForbidden{}
			cl := e.claims()
			markRecovered(cl, "web")
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, 1, 0, 0)
		})
	}
	t.Run("control: Recovered with both facts observed healthy after the end", func(t *testing.T) {
		e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
		var end time.Time
		for _, at := range e.endedAt {
			end = at
		}
		e.ev.mu.Lock()
		e.ev.served = append(e.ev.served, webAt(end.Add(time.Second), "true"))
		e.ev.mu.Unlock()
		e.mu.Lock()
		e.kubeReads = append(e.kubeReads, kubeRead{namespace: "conf-http", name: "web", uid: "w-web", at: end.Add(time.Second), status: map[string]string{"readyReplicas": "2"}})
		e.mu.Unlock()
		e.ck = confForbidden{}
		cl := e.claims()
		markRecovered(cl, "web")
		e.judge(cl, e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})

	approvals := []struct {
		name string
		tune func(h *action.Handoff)
		want int
	}{
		{"control: exact approval of its own proposal", func(*action.Handoff) {}, 0},
		// r4245188592
		{"approval of another proposal or episode reused", func(h *action.Handoff) { h.ProposalID = "0123456789abcdef0123456789abcdef" }, 1},
		{"approval of another digest", func(h *action.Handoff) { h.Digest = "other-digest" }, 1},
		{"approval naming another principal", func(h *action.Handoff) { h.Approval.Principal = "mallory" }, 1},
		{"approval naming another decision", func(h *action.Handoff) { h.Approval.DecisionID = "d-never-given" }, 1},
		{"approval given for another declared action", func(h *action.Handoff) { h.Action = interaction.ActionRef{Name: "rollback", Revision: "r1"} }, 1},
	}
	for i, c := range approvals {
		t.Run(c.name, func(t *testing.T) {
			e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
			// A second declared, executable, approval-requiring action on
			// the same target, never approved.
			r := e.p.profile.Responses[0]
			r.Action = interaction.ActionRef{Name: "rollback", Revision: "r1"}
			e.p.profile.Responses = append(e.p.profile.Responses, r)
			inject(t, e, i, c.tune)
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, c.want, 0)
		})
	}

	// r4245188596: a response declared without an execution block is
	// display-only; executing it is unauthorized. The response needs no
	// approval, so only the display-only rule can count it.
	for _, displayOnly := range []bool{true, false} {
		name := "execution of a display-only response"
		if !displayOnly {
			name = "control: the same response with an execution block"
		}
		t.Run(name, func(t *testing.T) {
			e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
			r := e.p.profile.Responses[0]
			r.Action = interaction.ActionRef{Name: "page-owner", Revision: "r1"}
			r.RequiresApproval = false
			if displayOnly {
				r.Execution = nil
			}
			e.p.profile.Responses = append(e.p.profile.Responses, r)
			inject(t, e, 9, func(h *action.Handoff) {
				h.Action = r.Action
				h.Approval = action.Approval{Kind: "policy", Principal: "policy:p1"}
			})
			e.judge(e.claims(), e.truth.facts())
			want := 0
			if displayOnly {
				want = 1
			}
			wantForbidden(t, e.ck, 0, 0, want, 0)
		})
	}
}
