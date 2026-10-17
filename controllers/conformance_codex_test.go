package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

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

	// r4271250376: a declared, executable response that needs no human
	// decision is authorized only by the engine's policy approval, Kind
	// "policy" and Principal "policy:<profile revision>" (here p1).
	policies := []struct {
		name     string
		approval action.Approval
		want     int
	}{
		{"control: policy approval of the loaded profile revision", action.Approval{Kind: "policy", Principal: "policy:p1"}, 0},
		{"empty approval", action.Approval{}, 1},
		{"approval of an unknown kind", action.Approval{Kind: "automatic", Principal: "policy:p1"}, 1},
		{"policy approval naming a previous profile revision", action.Approval{Kind: "policy", Principal: "policy:p0"}, 1},
		{"policy approval with a forged principal", action.Approval{Kind: "policy", Principal: "ops-lead"}, 1},
		{"policy approval carrying a decision", action.Approval{Kind: "policy", Principal: "policy:p1", DecisionID: "d-1"}, 1},
	}
	for i, c := range policies {
		t.Run(c.name, func(t *testing.T) {
			e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
			r := e.p.profile.Responses[0]
			r.Action = interaction.ActionRef{Name: "page-owner", Revision: "r1"}
			r.RequiresApproval = false
			e.p.profile.Responses = append(e.p.profile.Responses, r)
			inject(t, e, 20+i, func(h *action.Handoff) {
				h.Action = r.Action
				h.Approval = c.approval
			})
			e.judge(e.claims(), e.truth.facts())
			wantForbidden(t, e.ck, 0, 0, c.want, 0)
		})
	}
	// A response that needs a human decision is never authorized by the
	// policy approval, however well formed.
	t.Run("policy approval of an approval-requiring response", func(t *testing.T) {
		e := runTo(t, "jumi-artifact-handoff", "restart-without-registry-recovery")
		inject(t, e, 30, func(h *action.Handoff) { h.Approval = action.Approval{Kind: "policy", Principal: "policy:p1"} })
		e.judge(e.claims(), e.truth.facts())
		wantForbidden(t, e.ck, 0, 0, 1, 0)
	})
}
