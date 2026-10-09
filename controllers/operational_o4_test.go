package controllers

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/investigate"
)

// O4 vertical slice: real O3 investigation (HTTP + Kubernetes status
// providers) → O1 evaluation → status projection → interaction summary.

func newO4Env(t *testing.T, profile *interaction.Profile) *o3Env {
	e := newO3Env(t)
	e.r.Interaction = true
	e.r.InteractionProfile = profile
	return e
}

func (e *o3Env) interaction(name string) *opsv1.InteractionStatus {
	e.t.Helper()
	s := e.status(name).Interaction
	if s == nil {
		e.t.Fatalf("%s has no interaction summary", name)
	}
	return s
}

func capTypeNames(cs []opsv1.InteractionCapability) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Type.Name)
	}
	return out
}

func hasCode(rs []opsv1.StatusReason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

// What BORI checked (the actual episode), what it confirmed, what failed and
// who is affected reach the summary; Y is not contaminated by A; nothing but
// ops status is written.
func TestO4VerticalSlice(t *testing.T) {
	e := newO4Env(t, nil)
	e.reconcile()
	for _, n := range []string{"a", "x", "y"} {
		s := e.interaction(n)
		if s.Level != opsv1.LevelNoAction {
			t.Fatalf("%s: %s %+v", n, s.Level, s.LevelReasons)
		}
		if s.Investigation == nil || s.Investigation.Outcome != "Concluded" || len(s.CauseCandidates) != 0 {
			t.Fatalf("%s investigation %+v", n, s.Investigation)
		}
	}
	if f := e.interaction("a").ConfirmedFacts; len(f) != 2 || !strings.HasPrefix(f[0].EvidenceRef, "http:") && !strings.HasPrefix(f[0].EvidenceRef, "k8s:") {
		t.Fatalf("facts %+v", f)
	}

	e.app.set("/a", false, 0) // A's application fact turns negative
	for i := 0; i < 10 && e.capState("a", "provide") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	a, x, y := e.interaction("a"), e.interaction("x"), e.interaction("y")
	if !reflect.DeepEqual(capTypeNames(a.Affected), []string{"provide"}) || a.Level != opsv1.LevelAwareness || !hasCode(a.LevelReasons, interaction.ReasonNoDeclaredResponse) {
		t.Fatalf("a %s %+v %+v", a.Level, a.Affected, a.LevelReasons)
	}
	if a.Investigation.Outcome != "Concluded" || a.Investigation.Maintained == 0 || a.Investigation.Refuted == 0 ||
		len(a.CauseCandidates) == 0 || a.CauseCandidates[0].Status != "Maintained" {
		t.Fatalf("a investigation %+v candidates %+v", a.Investigation, a.CauseCandidates)
	}
	if !reflect.DeepEqual(capTypeNames(x.Affected), []string{"consume"}) || !hasCode(x.LevelReasons, interaction.ReasonImpactFromDependency) {
		t.Fatalf("x %s %+v %+v", x.Level, x.Affected, x.LevelReasons)
	}
	if y.Level != opsv1.LevelNoAction || len(y.Affected)+len(y.Unknown) != 0 {
		t.Fatalf("unrelated y contaminated: %+v", y)
	}
	if e.otherWrites != 0 {
		t.Fatalf("%d non-status writes", e.otherWrites)
	}
}

// Provider failure: confirmation fails, the app is not declared failed, the
// previous AVAILABLE is not reused, and the actual episode outcome is shown.
func TestO4ProviderFailureIsNotAppFailure(t *testing.T) {
	e := newO4Env(t, nil)
	e.reconcile()
	e.app.set("/y", true, 503)
	for i := 0; i < 10 && e.capState("y", "work") != "UNKNOWN"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	y := e.interaction("y")
	if y.Level != opsv1.LevelAwareness || len(y.Affected) != 0 || !reflect.DeepEqual(capTypeNames(y.Unknown), []string{"work"}) {
		t.Fatalf("y %s affected %v unknown %v", y.Level, capTypeNames(y.Affected), capTypeNames(y.Unknown))
	}
	if !reflect.DeepEqual(capTypeNames(y.Unaffected), []string{"count"}) {
		t.Fatalf("y unaffected %v", capTypeNames(y.Unaffected))
	}
	found := false
	for _, m := range y.MissingEvidence {
		found = found || (m.Slot == "api-serving" && m.State == "ProviderUnavailable" && m.EvidenceRef == "")
	}
	if !found || y.Investigation == nil || y.Investigation.Outcome == "" || y.Investigation.Open == 0 {
		t.Fatalf("missing %+v investigation %+v", y.MissingEvidence, y.Investigation)
	}
	t.Logf("y investigation after outage: %+v", *y.Investigation)
}

// The summary adds no provider call, no lookup and no budget: the same
// script with and without it dispatches exactly the same I/O.
func TestO4AddsNoProviderIO(t *testing.T) {
	run := func(on bool) (investigate.Stats, int, int, map[string]int) {
		e := newO3Env(t)
		e.r.Interaction = on
		script := []func(){
			func() {}, func() { e.app.set("/a", false, 0) }, func() { e.app.set("/y", true, 503) },
			func() { e.app.set("/a", true, 0) }, func() {},
		}
		for _, s := range script {
			s()
			for i := 0; i < 5; i++ {
				e.clk.add(3 * time.Second)
				e.reconcile()
			}
		}
		lookups := 0
		for _, n := range e.lookup.lookups {
			lookups += n
		}
		return e.r.Investigator.Stats(), e.app.hitCount("/a") + e.app.hitCount("/y"), lookups, e.kubeGets
	}
	s1, h1, l1, k1 := run(false)
	s2, h2, l2, k2 := run(true)
	if s1 != s2 || h1 != h2 || l1 != l2 || !reflect.DeepEqual(k1, k2) {
		t.Fatalf("I/O differs: off %+v/%d/%d/%v on %+v/%d/%d/%v", s1, h1, l1, k1, s2, h2, l2, k2)
	}
	t.Logf("identical I/O with and without the summary: %+v http=%d lookups=%d kube=%v", s2, h2, l2, k2)
}

// Repeated reconciles and evidence refreshes write nothing; a real
// transition writes once per changed target.
func TestO4ZeroChurn(t *testing.T) {
	e := newO4Env(t, nil)
	e.reconcile()
	hits := e.app.hitCount("/y")
	for i := 0; i < 10; i++ {
		e.clk.add(3 * time.Second)
		if w := e.reconcile(); len(w) != 0 {
			t.Fatalf("round %d wrote %v", i, w)
		}
	}
	if e.app.hitCount("/y") <= hits {
		t.Fatal("no refresh happened; the zero-write check proved nothing")
	}
	e.app.set("/y", false, 0)
	var writes map[string]int
	for i := 0; i < 10 && e.capState("y", "work") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		writes = e.reconcile()
	}
	if writes["y"] != 1 || writes["a"] != 0 || writes["x"] != 0 {
		t.Fatalf("transition writes %v", writes)
	}
}

// A declared approval boundary yields DECISION_REQUIRED with its reasons;
// the response is displayed only: no write outside ops status, the workload
// is unchanged.
func TestO4DeclaredApprovalIsDisplayOnly(t *testing.T) {
	p := &interaction.Profile{Revision: "p1", Responses: []interaction.Response{{
		Action: interaction.ActionRef{Name: "restart", Revision: "r1"},
		Target: interaction.TargetRef{Namespace: "apps", Name: "y"},
		For:    ct("work"), Owner: "app-oncall", RequiresApproval: true,
		Risks: []string{interaction.RiskServiceInterruption},
	}}}
	e := newO4Env(t, p)
	e.reconcile()
	e.app.set("/y", false, 0)
	for i := 0; i < 10 && e.capState("y", "work") != "UNAVAILABLE"; i++ {
		e.clk.add(3 * time.Second)
		e.reconcile()
	}
	y := e.interaction("y")
	if y.Level != opsv1.LevelDecisionRequired || !hasCode(y.HumanReasons, interaction.ReasonApprovalRequired) {
		t.Fatalf("y %s %+v", y.Level, y.HumanReasons)
	}
	if len(y.Responses) != 1 || y.Responses[0].Status != interaction.ResponseApprovalRequired || y.Responses[0].Target != "apps/y#t-y" {
		t.Fatalf("responses %+v", y.Responses)
	}
	if e.otherWrites != 0 {
		t.Fatalf("%d non-status writes", e.otherWrites)
	}
	raw, _ := json.Marshal(y)
	if strings.Contains(strings.ToLower(string(raw)), "recover") {
		t.Fatalf("recovery claimed: %s", raw)
	}
}

// A recreated workload is a new identity: no earlier investigation record,
// history or decision is reused for it.
func TestO4IdentityChangeReusesNothing(t *testing.T) {
	e := newO4Env(t, nil)
	e.reconcile()
	before := e.interaction("y")
	ctx := e.driver()
	_ = e.c.Delete(ctx, deploy("y", 2))
	d := deploy("y", 2)
	d.UID = "w-y-2"
	_ = e.c.Create(ctx, d)
	e.clk.add(time.Second)
	if _, err := e.r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatal(err)
	}
	after := e.interaction("y")
	if after.IdentityDigest == before.IdentityDigest || len(after.RecentFingerprints) != 1 || after.Recurring {
		t.Fatalf("identity not reset: %+v", after)
	}
	if after.Investigation == nil || after.Investigation.Outcome != "NotRun" {
		t.Fatalf("old identity's investigation reused: %+v", after.Investigation)
	}
	if after.Level == opsv1.LevelNoAction {
		t.Fatal("new identity claimed NO_ACTION without fresh evidence")
	}
}

// Disabled, the status is exactly the O3 status (no summary field).
func TestO4DisabledKeepsO3Status(t *testing.T) {
	e := newO3Env(t)
	e.reconcile()
	for _, n := range []string{"a", "x", "y"} {
		if e.status(n).Interaction != nil {
			t.Fatalf("%s has a summary while disabled", n)
		}
	}
}

// Steady failure states — an application fact staying negative, a provider
// staying down — write nothing once the transition is recorded, although
// investigation and refresh episodes keep running.
func TestO4SteadyFailureDoesNotChurn(t *testing.T) {
	for name, fail := range map[string]func(e *o3Env){
		"app fact negative": func(e *o3Env) { e.app.set("/a", false, 0) },
		"provider down":     func(e *o3Env) { e.app.set("/y", true, 503) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newO4Env(t, nil)
			e.reconcile()
			fail(e)
			for i := 0; i < 15; i++ { // settle: transition and first episodes
				e.clk.add(3 * time.Second)
				e.reconcile()
			}
			started := e.r.Investigator.Stats().EpisodesStarted
			for i := 0; i < 20; i++ {
				e.clk.add(3 * time.Second)
				if w := e.reconcile(); len(w) != 0 {
					t.Fatalf("steady round %d wrote %v: %+v", i, w, e.status("a").Interaction)
				}
			}
			if e.r.Investigator.Stats().EpisodesStarted == started {
				t.Fatal("no episode ran; the steady check proved nothing")
			}
		})
	}
}
