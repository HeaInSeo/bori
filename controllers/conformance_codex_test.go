package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/action/actiontest"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
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

// accept adds a result to the record of what providers returned, as the
// recording lookup does for BORI's own reads.
func accept(e *confEnv, o providerObs) {
	e.mu.Lock()
	e.observed = append(e.observed, o)
	e.mu.Unlock()
}

// deliverWeb has the evidence server serve the web endpoint and the HTTP
// provider accept every slot of it.
func deliverWeb(e *confEnv, o servedObs) {
	e.ev.mu.Lock()
	e.ev.served = append(e.ev.served, o)
	e.ev.mu.Unlock()
	for slot, key := range map[string]string{"api-serving": "serving", "latency-within-slo": "latencyOK", "metrics-endpoint-up": "metricsUp"} {
		accept(e, providerObs{provider: "conf-http/app-http@r1", uid: "w-web", slot: slot, at: o.at, value: o.values[key]})
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
			web := c.web(end)
			deliverWeb(e, web)
			replicasAt := end
			if c.replicasAfter {
				replicasAt = end.Add(time.Second)
			}
			accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: replicasAt, value: "2"})
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
		deliverWeb(e, webAt(end.Add(time.Second), "true"))
		accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: end.Add(time.Second), value: "2"})
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
	// display-only; executing it is unauthorized. In a world where the engine
	// itself dispatched the response by policy, only the display-only rule
	// can count it.
	for _, displayOnly := range []bool{true, false} {
		name := "execution of a display-only response"
		if !displayOnly {
			name = "control: the same response with an execution block"
		}
		t.Run(name, func(t *testing.T) {
			e := policyWorld(t)
			if displayOnly {
				e.p.profile.Responses[0].Execution = nil
			}
			e.judge(e.claims(), e.truth.facts())
			want := 0
			if displayOnly {
				want = 1
			}
			wantForbidden(t, e.ck, 0, 0, want, 0)
		})
	}
}

// policyWorld runs a world in which the shipped engine itself authorizes by
// policy: the pack's only response for the capability declares no approval,
// risk or interruption, so the degraded capability is restarted with the
// policy approval and no approve step. The genuine run counts nothing.
func policyWorld(t *testing.T, tune ...func(*confPack)) *confEnv {
	t.Helper()
	p, scs := packByName(t, "jumi-artifact-handoff")
	s := scs["restart-without-registry-recovery"]
	p.profile.Responses[0].RequiresApproval = false
	for _, f := range tune {
		f(p)
	}
	e := newConfEnv(t, p, false)
	setEffect(e, s.OnExecute)
	e.settle()
	e.apply(confStep{Values: s.Steps[0].Values})
	for r := 0; r < 10 && e.actor.TotalExecutions() == 0; r++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
		e.check()
	}
	n := 0
	for _, h := range e.actor.Handoffs {
		if e.actor.Executions[h.Key] == 0 {
			continue
		}
		n++
		if h.Approval != (action.Approval{Kind: "policy", Principal: "policy:p1"}) {
			t.Fatalf("setup: engine dispatched under %+v", h.Approval)
		}
	}
	if n == 0 || len(e.approvals) != 0 {
		t.Fatalf("setup: %d policy executions, %d approvals", n, len(e.approvals))
	}
	wantForbidden(t, e.ck, 0, 0, 0, 0) // the genuine run
	e.ck = confForbidden{}
	return e
}

// setObservedGeneration moves a target's workload to a new spec generation
// and sets the generation its status was recorded for: lag > 0 leaves the
// status describing an older spec, 0 means caught up.
func setObservedGeneration(t *testing.T, e *confEnv, target string, lag int64) {
	t.Helper()
	tg := e.target(target)
	o := e.workload(tg.Spec.TargetRef.Kind + "/" + tg.Spec.TargetRef.Name)
	o.SetGeneration(o.GetGeneration() + 1)
	if err := e.c.Update(e.driver(), o); err != nil {
		t.Fatal(err)
	}
	o = e.workload(tg.Spec.TargetRef.Kind + "/" + tg.Spec.TargetRef.Name)
	switch d := o.(type) {
	case *appsv1.Deployment:
		d.Status.ObservedGeneration = d.Generation - lag
	case *appsv1.StatefulSet:
		d.Status.ObservedGeneration = d.Generation - lag
	default:
		t.Fatalf("workload %T has no observed generation", o)
	}
	if err := e.c.Status().Update(e.driver(), o); err != nil {
		t.Fatal(err)
	}
}

// Codex review of 8dc3988 (r4271250366, r4271250376): a status GET the
// provider refuses is no recovery evidence, and a non-human approval must be
// exactly the engine's policy approval for the loaded profile revision.
func TestConformanceCheckerCodexFindingsHead2(t *testing.T) {
	recoveries := []struct{ pack, scenario, target string }{
		{"simple-http-service", "action-succeeds-and-recovers", "web"},
		{"jumi-artifact-handoff", "restart-with-registry-recovery", "jumi"},
	}
	for _, rc := range recoveries {
		for _, lag := range []int64{1, 0} {
			name := "Recovered on a status lagging the spec/" + rc.pack
			if lag == 0 {
				name = "control: Recovered on a status caught up with the spec/" + rc.pack
			}
			// r4271250366: after the execution ended the workload moved to a
			// new spec. Every status GET still succeeds with healthy replica
			// counts, but while the status lags the spec the provider refuses
			// it (status-lagging-generation).
			t.Run(name, func(t *testing.T) {
				e := untilExecuted(t, rc.pack, rc.scenario)
				setObservedGeneration(t, e, rc.target, lag)
				for r := 0; r < 10; r++ {
					e.clk.add(3 * time.Second)
					e.reconcile()
					e.check()
				}
				wantForbidden(t, e.ck, 0, 0, 0, 0) // the genuine run
				refused := 0
				for _, o := range e.observed {
					if o.unavailable == "status-lagging-generation" {
						refused++
					}
				}
				if (lag > 0) != (refused > 0) {
					t.Fatalf("setup: %d lagging-generation refusals at lag %d", refused, lag)
				}
				e.ck = confForbidden{}
				cl := e.claims()
				markRecovered(cl, rc.target)
				e.judge(cl, e.truth.facts())
				if lag > 0 {
					wantForbidden(t, e.ck, 0, 1, 0, 0)
				} else {
					wantForbidden(t, e.ck, 0, 0, 0, 0)
				}
			})
		}
	}
	// Only the bound provider's acceptance of a healthy value counts: the
	// serving fact is delivered healthy after the end, and the replica count
	// is accepted after it only from another provider, or only unhealthy.
	for _, c := range []struct{ name, provider, value string }{
		{"replicas accepted only from another provider", "conf-http/kube-status@r0", "2"},
		{"replicas accepted after the end only unhealthy", "conf-http/kube-status@r1", "0"},
	} {
		t.Run("Recovered with "+c.name, func(t *testing.T) {
			e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
			var end time.Time
			for _, at := range e.endedAt {
				end = at
			}
			deliverWeb(e, servedObs{path: "/web", at: end.Add(time.Second), values: map[string]string{"serving": "true", "latencyOK": "true", "metricsUp": "true"}})
			accept(e, providerObs{provider: c.provider, uid: "w-web", slot: "ready-replicas", at: end.Add(time.Second), value: c.value})
			e.ck = confForbidden{}
			cl := e.claims()
			markRecovered(cl, "web")
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, 1, 0, 0)
		})
	}

	// An HTTP fact the provider accepted but the evidence server never
	// delivered (here: serving was last served unhealthy) proves nothing.
	t.Run("Recovered with serving accepted but never served healthy after the end", func(t *testing.T) {
		e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
		var end time.Time
		for _, at := range e.endedAt {
			end = at
		}
		at := end.Add(time.Second)
		e.ev.mu.Lock()
		e.ev.served = append(e.ev.served, servedObs{path: "/web", at: at, values: map[string]string{"serving": "false", "latencyOK": "true", "metricsUp": "true"}})
		e.ev.mu.Unlock()
		accept(e, providerObs{provider: "conf-http/app-http@r1", uid: "w-web", slot: "api-serving", at: at, value: "true"})
		accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: at, value: "2"})
		e.ck = confForbidden{}
		cl := e.claims()
		markRecovered(cl, "web")
		e.judge(cl, e.truth.facts())
		wantForbidden(t, e.ck, 0, 1, 0, 0)
	})

	// A sibling slot's acceptance never stands in for the slot under test:
	// serving was served healthy, but the provider accepted only the other
	// slots of that response.
	t.Run("Recovered with only sibling slots accepted after the end", func(t *testing.T) {
		e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
		var end time.Time
		for _, at := range e.endedAt {
			end = at
		}
		at := end.Add(time.Second)
		e.ev.mu.Lock()
		e.ev.served = append(e.ev.served, servedObs{path: "/web", at: at, values: map[string]string{"serving": "true", "latencyOK": "true", "metricsUp": "true"}})
		e.ev.mu.Unlock()
		accept(e, providerObs{provider: "conf-http/app-http@r1", uid: "w-web", slot: "latency-within-slo", at: at, value: "true"})
		accept(e, providerObs{provider: "conf-http/app-http@r1", uid: "w-web", slot: "metrics-endpoint-up", at: at, value: "true"})
		accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: at, value: "2"})
		e.ck = confForbidden{}
		cl := e.claims()
		markRecovered(cl, "web")
		e.judge(cl, e.truth.facts())
		wantForbidden(t, e.ck, 0, 1, 0, 0)
	})
	// The serve must corroborate the accepted observation's own instant: a
	// later healthy serve, never accepted, does not vouch for an accepted
	// observation whose instant was served unhealthy.
	t.Run("Recovered with serving accepted at an instant served unhealthy, healthy only later", func(t *testing.T) {
		e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
		var end time.Time
		for _, at := range e.endedAt {
			end = at
		}
		t1, t2 := end.Add(time.Second), end.Add(2*time.Second)
		e.ev.mu.Lock()
		e.ev.served = append(e.ev.served,
			servedObs{path: "/web", at: t1, values: map[string]string{"serving": "false", "latencyOK": "true", "metricsUp": "true"}},
			servedObs{path: "/web", at: t2, values: map[string]string{"serving": "true", "latencyOK": "true", "metricsUp": "true"}})
		e.ev.mu.Unlock()
		accept(e, providerObs{provider: "conf-http/app-http@r1", uid: "w-web", slot: "api-serving", at: t1, value: "true"})
		accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: t1, value: "2"})
		e.ck = confForbidden{}
		cl := e.claims()
		markRecovered(cl, "web")
		e.judge(cl, e.truth.facts())
		wantForbidden(t, e.ck, 0, 1, 0, 0)
	})
	// A generation move whose status is caught up changes no input: a
	// capability resting on it stays under the unrelated check. Only a
	// status lagging its spec is a changed input.
	t.Run("unrelated contamination after a caught-up generation move", func(t *testing.T) {
		e := runTo(t, "jumi-artifact-handoff", "resolver-down")
		setObservedGeneration(t, e, "jumi", 0)
		e.check()
		e.ck = confForbidden{}
		cl := e.claims()
		setCap(cl, "jumi", "query-runs", "UNKNOWN")
		e.judge(cl, e.truth.facts())
		wantForbidden(t, e.ck, 1, 0, 0, 0)
	})

	// r4271250376: where nothing is for a person to decide, the engine's own
	// policy approval, Kind "policy" and Principal "policy:<profile
	// revision>" (here p1), is the only authorization. Each case resubmits
	// the engine's genuine policy execution with its approval replaced.
	policies := []struct {
		name     string
		approval action.Approval
		want     int
	}{
		{"control: the engine's policy approval of the loaded profile revision", action.Approval{Kind: "policy", Principal: "policy:p1"}, 0},
		{"empty approval", action.Approval{}, 1},
		{"approval of an unknown kind", action.Approval{Kind: "automatic", Principal: "policy:p1"}, 1},
		{"policy approval naming a previous profile revision", action.Approval{Kind: "policy", Principal: "policy:p0"}, 1},
		{"policy approval with a forged principal", action.Approval{Kind: "policy", Principal: "ops-lead"}, 1},
		{"policy approval carrying a decision", action.Approval{Kind: "policy", Principal: "policy:p1", DecisionID: "d-1"}, 1},
	}
	for i, c := range policies {
		t.Run(c.name, func(t *testing.T) {
			e := policyWorld(t)
			inject(t, e, 20+i, func(h *action.Handoff) { h.Approval = c.approval })
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, c.want, 0)
		})
	}
	// The engine refuses a human approval where nothing needs one, even one
	// an approve step signed exactly for this proposal and digest.
	t.Run("exactly signed human approval of a response that needs no human decision", func(t *testing.T) {
		e := policyWorld(t)
		var h0 action.Handoff
		for _, h := range e.actor.Handoffs {
			h0 = h
		}
		d := action.Decision{ID: "d-exact", ProposalID: h0.ProposalID, Digest: h0.Digest, Principal: "ops-lead", Verdict: action.Approve, DecidedAt: e.clk.now()}
		e.approvals = append(e.approvals, confApproval{decision: d, target: "jumi", action: h0.Action.String()})
		inject(t, e, 27, func(h *action.Handoff) {
			h.Approval = action.Approval{Kind: "human", Principal: d.Principal, DecisionID: d.ID}
		})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
	// The same declaration repeated with other trigger states is one
	// declaration, not a competitor: the engine still dispatches by policy.
	t.Run("control: a duplicate declaration differing only in trigger states", func(t *testing.T) {
		e := policyWorld(t, func(p *confPack) {
			r := p.profile.Responses[0]
			r.States = []string{"UNAVAILABLE", "DEGRADED", "UNKNOWN"}
			p.profile.Responses = append(p.profile.Responses, r)
		})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
	// A second, distinct declaration for the same capability leaves no
	// priority authority: the engine blocks both and dispatches neither by
	// policy, so a well-formed policy approval authorizes neither.
	t.Run("policy approval while another declaration competes for the capability", func(t *testing.T) {
		p, scs := packByName(t, "jumi-artifact-handoff")
		r := p.profile.Responses[0]
		r.Action = interaction.ActionRef{Name: "page-owner", Revision: "r1"}
		r.RequiresApproval = false
		p.profile.Responses = append(p.profile.Responses, r)
		e := newConfEnv(t, p, false)
		e.run(scs["restart-without-registry-recovery"])
		for _, h := range e.actor.Handoffs {
			if h.Action == r.Action && e.actor.Executions[h.Key] > 0 {
				t.Fatalf("setup: the engine dispatched competing %s", r.Action)
			}
		}
		e.ck = confForbidden{}
		inject(t, e, 28, func(h *action.Handoff) {
			h.Action = r.Action
			h.Approval = action.Approval{Kind: "policy", Principal: "policy:p1"}
		})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
	// A response that declares a human decision is never authorized by the
	// policy approval, however well formed.
	t.Run("policy approval of an approval-requiring response", func(t *testing.T) {
		e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
		inject(t, e, 30, func(h *action.Handoff) { h.Approval = action.Approval{Kind: "policy", Principal: "policy:p1"} })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
}

// Codex review of b761b9b (r4303213858, r4303213864, r4303213869): recovery
// is judged by the contract's predicates, not by the value before the fault;
// an execution must carry exactly the declared capability, impacts and the
// target's identities as they were when it ran; and a declaration lists its
// risks and preconditions as sets.
func TestConformanceCheckerCodexFindingsHead3(t *testing.T) {
	// r4303213858: the action restores service with one ready replica
	// where there were two. serve-requests needs ready-replicas Gte 1, so
	// BORI's genuine Recovered claim is justified.
	t.Run("genuine recovery to another healthy replica count", func(t *testing.T) {
		p, scs := packByName(t, "simple-http-service")
		s := scs["action-succeeds-and-recovers"]
		one := int32(1)
		s.Steps = append([]confStep{{Workload: map[string]confWorkload{"Deployment/web": {Ready: &one, Available: &one}}}}, s.Steps...)
		e := newConfEnv(t, p, false)
		e.run(s)
		recovered := false
		for _, a := range e.target("web").Status.Interaction.Actions {
			recovered = recovered || a.Recovery == "Recovered"
		}
		if !recovered {
			t.Fatal("setup: BORI did not claim Recovered")
		}
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
	for _, c := range []struct {
		replicas string
		want     int
	}{{"1", 0}, {"3", 0}, {"0", 1}} {
		name := "Recovered with ready-replicas " + c.replicas + " accepted after the end"
		if c.want == 0 {
			name = "control: " + name
		}
		t.Run(name, func(t *testing.T) {
			e := untilExecuted(t, "simple-http-service", "action-succeeds-and-recovers")
			var end time.Time
			for _, at := range e.endedAt {
				end = at
			}
			deliverWeb(e, servedObs{path: "/web", at: end.Add(time.Second), values: map[string]string{"serving": "true", "latencyOK": "true", "metricsUp": "true"}})
			accept(e, providerObs{provider: "conf-http/kube-status@r1", uid: "w-web", slot: "ready-replicas", at: end.Add(time.Second), value: c.replicas})
			e.ck = confForbidden{}
			cl := e.claims()
			markRecovered(cl, "web")
			e.judge(cl, e.truth.facts())
			wantForbidden(t, e.ck, 0, c.want, 0, 0)
		})
	}
	t.Run("predicates as the contract states them", func(t *testing.T) {
		i := func(n int64) *opsv1.Operand { return &opsv1.Operand{Integer: &n} }
		b := func(v bool) *opsv1.Operand { return &opsv1.Operand{Boolean: &v} }
		str := func(v string) *opsv1.Operand { return &opsv1.Operand{String: &v} }
		for _, c := range []struct {
			op   opsv1.Operator
			o    *opsv1.Operand
			typ  opsv1.ValueType
			v    string
			want bool
		}{
			{"IsTrue", nil, "Boolean", "true", true},
			{"IsTrue", nil, "Boolean", "false", false},
			{"IsFalse", nil, "Boolean", "false", true},
			{"IsFalse", nil, "Boolean", "true", false},
			{"IsTrue", nil, "String", "true", false},
			{"Gte", i(1), "Integer", "1", true},
			{"Gte", i(1), "Integer", "2", true},
			{"Gte", i(1), "Integer", "0", false},
			{"Lte", i(1), "Integer", "1", true},
			{"Lte", i(1), "Integer", "2", false},
			{"Gte", i(1), "Integer", "x", false},
			{"Gte", i(1), "String", "2", false},
			{"Eq", i(2), "Integer", "2", true},
			{"Eq", i(2), "Integer", "3", false},
			{"NotEq", i(2), "Integer", "3", true},
			{"NotEq", i(2), "Integer", "2", false},
			{"Eq", str("primary"), "String", "primary", true},
			{"Eq", str("primary"), "String", "replica", false},
			{"Eq", b(true), "Boolean", "true", true},
			{"Eq", str("true"), "Boolean", "true", false},
			{"Eq", nil, "Integer", "", false},
		} {
			p := opsv1.Predicate{Assertion: "a", Operator: c.op, Operand: c.o}
			if got := satisfies(p, c.typ, c.v); got != c.want {
				t.Errorf("%s %s on %s %q = %v, want %v", c.op, operand(c.o), c.typ, c.v, got, c.want)
			}
		}
	})

	// r4303213864: the engine's genuine policy execution resubmitted with
	// one binding field changed; proposal, digest and approval are kept.
	other := operations.CapabilityType{Domain: "jumi.heainseo.dev", Name: "accept-runs", Revision: "v1"}
	bindings := []struct {
		name string
		tune func(h *action.Handoff)
	}{
		{"for another capability", func(h *action.Handoff) { h.For = other }},
		{"for another target UID", func(h *action.Handoff) { h.Target.UID = "t-other" }},
		{"for another resolved workload", func(h *action.Handoff) { h.ResolvedUID = "w-other" }},
		{"under another contract UID", func(h *action.Handoff) { h.Contract.UID = "c-other" }},
		{"under another contract digest", func(h *action.Handoff) { h.Contract.SpecDigest = "other-digest" }},
		{"expecting another impact", func(h *action.Handoff) { h.ExpectedImpact = []operations.CapabilityType{other} }},
		{"expecting an extra impact", func(h *action.Handoff) { h.ExpectedImpact = append(h.ExpectedImpact, other) }},
	}
	t.Run("control: the genuine binding resubmitted", func(t *testing.T) {
		e := policyWorld(t)
		inject(t, e, 40, func(*action.Handoff) {})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
	for i, c := range bindings {
		t.Run("execution "+c.name, func(t *testing.T) {
			e := policyWorld(t)
			inject(t, e, 41+i, c.tune)
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, 1, 0)
		})
	}
	// The same under an exact human approval: the approved proposal and
	// digest do not bind the workload.
	t.Run("exactly approved execution for another resolved workload", func(t *testing.T) {
		e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
		inject(t, e, 50, func(h *action.Handoff) { h.ResolvedUID = "w-other" })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
	// The binding is the target as it was when the actor ran: a workload
	// recreated afterwards does not void an exact execution.
	t.Run("control: exact execution, workload recreated afterwards", func(t *testing.T) {
		e := policyWorld(t)
		e.apply(confStep{Recreate: map[string]string{"Deployment/jumi": "w-jumi-2"}})
		for r := 0; r < 10; r++ {
			e.clk.add(3 * time.Second)
			e.reconcile()
			e.check()
		}
		if _, uid, _ := e.truth.workloadOf(e.target("jumi")); uid != "w-jumi-2" {
			t.Fatalf("setup: workload %q", uid)
		}
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})

	// r4303213869: the shipped engine dispatches by policy although the
	// declaration is listed twice with its preconditions in another order.
	pre := []opsv1.CapabilityType{
		{Domain: "jumi.heainseo.dev", Name: "accept-runs", Revision: "v1"},
		{Domain: "jumi.heainseo.dev", Name: "execute-unbound-nodes", Revision: "v1"},
	}
	withPre := func(p *confPack) { p.profile.Responses[0].Preconditions = append([]opsv1.CapabilityType(nil), pre...) }
	t.Run("control: a duplicate declaration listing its preconditions in another order", func(t *testing.T) {
		e := policyWorld(t, withPre, func(p *confPack) {
			r := p.profile.Responses[0]
			r.Preconditions = []opsv1.CapabilityType{pre[1], pre[0]}
			p.profile.Responses = append(p.profile.Responses, r)
		})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
	// Order is all that is ignored: another set of preconditions is another
	// declaration, which competes.
	t.Run("policy execution while a declaration with other preconditions competes", func(t *testing.T) {
		e := policyWorld(t, withPre)
		r := e.p.profile.Responses[0]
		r.Preconditions = pre[:1]
		e.p.profile.Responses = append(e.p.profile.Responses, r)
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
}

// Guardrail review of 86bdb61 (N4, N5): a declaration's identity is its
// response, not where it is pinned or whether it executes; one pinned to
// another target instance is not this target's; only executable
// declarations compete by execution contract. An execution carries its
// expected impacts as a set and is bound to the target as it was when that
// execution ran.
func TestConformanceCheckerCodexFindingsHead4(t *testing.T) {
	pre := []opsv1.CapabilityType{
		{Domain: "jumi.heainseo.dev", Name: "accept-runs", Revision: "v1"},
		{Domain: "jumi.heainseo.dev", Name: "execute-unbound-nodes", Revision: "v1"},
	}
	withPre := func(p *confPack) { p.profile.Responses[0].Preconditions = append([]opsv1.CapabilityType(nil), pre...) }
	sibling := func(f func(r *interaction.Response)) func(*confPack) {
		return func(p *confPack) {
			r := p.profile.Responses[0]
			f(&r)
			p.profile.Responses = append(p.profile.Responses, r)
		}
	}
	otherContract := func(r *interaction.Response) {
		ex := *r.Execution
		ex.ExpectedImpact = append(append([]opsv1.CapabilityType(nil), ex.ExpectedImpact...), pre[0])
		r.Execution = &ex
	}

	// The shipped engine still dispatches by policy: each sibling is the
	// same declaration for this instance, or none of its.
	for _, c := range []struct {
		name string
		f    func(r *interaction.Response)
	}{
		{"the same declaration pinned to this instance", func(r *interaction.Response) { r.Target.UID = "t-jumi" }},
		{"the same declaration pinned to another instance", func(r *interaction.Response) { r.Target.UID = "t-earlier-instance" }},
		{"another declaration pinned to another instance", func(r *interaction.Response) {
			r.Target.UID = "t-earlier-instance"
			r.Preconditions = pre[:1]
		}},
		{"a display-only sibling of the same declaration", func(r *interaction.Response) { r.Execution = nil }},
	} {
		t.Run("control: "+c.name, func(t *testing.T) {
			e := policyWorld(t, withPre, sibling(c.f))
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, 0, 0)
		})
	}

	// Real competitors: the shipped engine dispatches nothing by policy, so
	// a policy execution next to one is unauthorized.
	for _, c := range []struct {
		name string
		f    func(r *interaction.Response)
	}{
		{"another declaration pinned to this instance", func(r *interaction.Response) {
			r.Target.UID = "t-jumi"
			r.Preconditions = pre[:1]
		}},
		{"a display-only declaration of another response", func(r *interaction.Response) {
			r.Execution = nil
			r.Preconditions = pre[:1]
		}},
		{"the same response with another execution contract", otherContract},
		{"the same declaration under another owner", func(r *interaction.Response) { r.Owner = "ops-other" }},
		{"the same declaration requiring approval", func(r *interaction.Response) { r.RequiresApproval = true }},
	} {
		t.Run("policy execution while "+c.name+" competes", func(t *testing.T) {
			p, scs := packByName(t, "jumi-artifact-handoff")
			s := scs["restart-without-registry-recovery"]
			p.profile.Responses[0].RequiresApproval = false
			withPre(p)
			sibling(c.f)(p)
			g := newConfEnv(t, p, false)
			setEffect(g, s.OnExecute)
			g.settle()
			g.apply(confStep{Values: s.Steps[0].Values})
			for r := 0; r < 10; r++ {
				g.clk.add(3 * time.Second)
				g.reconcile()
				g.check()
			}
			for _, h := range g.actor.Handoffs {
				if g.actor.Executions[h.Key] > 0 && h.Approval.Kind == "policy" {
					t.Fatalf("setup: the engine dispatched %s by policy", h.Action)
				}
			}

			e := policyWorld(t, withPre)
			r := e.p.profile.Responses[0]
			c.f(&r)
			e.p.profile.Responses = append(e.p.profile.Responses, r)
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, 1, 0)
		})
	}

	// N5: the declaration expects two impacts.
	twoImpacts := func(p *confPack) { otherContract(&p.profile.Responses[0]) }
	t.Run("control: execution carrying the two impacts in the other order", func(t *testing.T) {
		e := policyWorld(t, twoImpacts)
		inject(t, e, 60, func(h *action.Handoff) {
			if len(h.ExpectedImpact) != 2 {
				t.Fatalf("setup: %d expected impacts", len(h.ExpectedImpact))
			}
			h.ExpectedImpact = []operations.CapabilityType{h.ExpectedImpact[1], h.ExpectedImpact[0]}
		})
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
	t.Run("execution dropping one of the two impacts", func(t *testing.T) {
		e := policyWorld(t, twoImpacts)
		inject(t, e, 61, func(h *action.Handoff) { h.ExpectedImpact = h.ExpectedImpact[:1] })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})

	// Each execution is bound to the target as it was when that execution
	// ran, not when any other did.
	recreated := func(t *testing.T) *confEnv {
		e := policyWorld(t)
		e.apply(confStep{Recreate: map[string]string{"Deployment/jumi": "w-jumi-2"}})
		for r := 0; r < 10; r++ {
			e.clk.add(3 * time.Second)
			e.reconcile()
			e.check()
		}
		if _, uid, _ := e.truth.workloadOf(e.target("jumi")); uid != "w-jumi-2" {
			t.Fatalf("setup: workload %q", uid)
		}
		e.ck = confForbidden{}
		return e
	}
	t.Run("execution after a workload recreation carrying the earlier workload", func(t *testing.T) {
		e := recreated(t)
		inject(t, e, 62, func(h *action.Handoff) { h.ResolvedUID = "w-jumi" })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
	t.Run("control: execution after a workload recreation carrying the new workload", func(t *testing.T) {
		e := recreated(t)
		inject(t, e, 63, func(h *action.Handoff) { h.ResolvedUID = "w-jumi-2" })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 0, 0)
	})
}
