package investigate

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// seed stores a held observation for slot of the first target, aged by age.
func (w *world) seed(slot string, v operations.Value, age time.Duration) {
	q := w.queries()[w.base.Targets[0].Identity.UID][slot]
	w.iv.mu.Lock()
	defer w.iv.mu.Unlock()
	w.iv.admit(operations.Observation{
		Key: q.Request.Key, Outcome: operations.OutcomeValue, Value: v,
		ObservedAt: w.clk.now().Add(-age), EvidenceRef: "seed:" + slot,
	})
}

func expectCalls(t *testing.T, p *scripted, want ...string) {
	t.Helper()
	if got := p.callList(); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("calls %v, want %v", got, want)
	}
}

func episode(t *testing.T, w *world) Episode {
	t.Helper()
	e, ok := w.iv.Episode("t-svc")
	if !ok {
		t.Fatal("no episode")
	}
	return e
}

func candidate(e Episode, id string) CandidateStatus {
	for _, c := range e.Candidates {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

const (
	candReplicas = "example.io/serve@v1/assertion:available-replicas"
	candServing  = "example.io/serve@v1/assertion:api-serving"
)

// ── evidence-conditioned selection ──────────────────────────────────────────

func TestNextQueryDependsOnEvidence(t *testing.T) {
	t.Run("replica fact held → only the HTTP app assertion is queried", func(t *testing.T) {
		w := newWorld(ReferenceLimits())
		w.seed("available-replicas", operations.Int(3), time.Second)
		w.seed("ready-replicas", operations.Int(3), time.Second)
		w.http.answers["api-serving"] = boolAt(w.clk, true)
		w.run(context.Background())
		expectCalls(t, w.http, "api-serving")
		expectCalls(t, w.kube)
		if s := capState(w.assess(), "serve"); s != operations.Available {
			t.Fatalf("serve %s", s)
		}
		e := episode(t, w)
		if e.State != Resolved || e.Trace[0].Action != "select" || e.Trace[0].Slot != "api-serving" {
			t.Fatalf("episode %+v", e)
		}
	})
	t.Run("app assertion held → only the Kubernetes replica fact is queried", func(t *testing.T) {
		w := newWorld(ReferenceLimits())
		w.seed("api-serving", operations.Bool(true), time.Second)
		w.seed("ready-replicas", operations.Int(3), time.Second)
		w.kube.answers["available-replicas"] = intAt(w.clk, 2)
		w.run(context.Background())
		expectCalls(t, w.kube, "available-replicas")
		expectCalls(t, w.http)
		if s := capState(w.assess(), "serve"); s != operations.Available {
			t.Fatalf("serve %s", s)
		}
	})
}

func TestSelectionRanksByOpenCandidatesNotByOrder(t *testing.T) {
	w := newWorld(ReferenceLimits())
	c := svcContract()
	// available-replicas now decides two open candidates, api-serving one.
	c.Capabilities = append(c.Capabilities, operations.Capability{Type: capT("scale"), Requirements: []operations.Requirement{
		{Predicate: &operations.Predicate{Assertion: "available-replicas", Op: operations.OpGte, Operand: operations.Int(2)}, OnUnmet: operations.ImpactDegraded},
	}})
	w.base.Contracts = []operations.Contract{c}
	w.seed("ready-replicas", operations.Int(1), time.Second)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.run(context.Background())
	e := episode(t, w)
	if e.Trace[0].Slot != "available-replicas" || !strings.Contains(e.Trace[0].Detail, "decides 2 open") {
		t.Fatalf("first selection %+v", e.Trace[0])
	}
}

func TestCandidatesRefutedAndMaintainedAreNotFacts(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("ready-replicas", operations.Int(1), time.Second)
	w.http.answers["api-serving"] = boolAt(w.clk, false)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())

	// Tie on score → slot name: api-serving first, then the remaining open one.
	expectCalls(t, w.http, "api-serving")
	expectCalls(t, w.kube, "available-replicas")
	e := episode(t, w)
	if candidate(e, candServing) != Maintained || candidate(e, candReplicas) != Refuted || e.State != Resolved {
		t.Fatalf("candidates %+v state %s", e.Candidates, e.State)
	}
	var transitions []string
	for _, tr := range e.Trace {
		if tr.Action == "candidate" {
			transitions = append(transitions, tr.Detail)
		}
	}
	want := []string{candServing + ": Open→Maintained", candReplicas + ": Open→Refuted"}
	if !reflect.DeepEqual(transitions, want) {
		t.Fatalf("transitions %v", transitions)
	}
	// Capability truth is O1's, from facts only; the Maintained hypothesis is
	// not a cause and changes nothing.
	ta := w.assess()
	cr, _ := ta.Capability("serve")
	if cr.State != operations.Unavailable || len(cr.Reasons) != 1 || cr.Reasons[0].Code != operations.ReasonPredicateUnmet {
		t.Fatalf("serve %+v", cr)
	}

	// A later episode with the same facts keeps the refuted candidate refuted.
	w.clk.advance(16 * time.Second)
	w.run(context.Background())
	if e := episode(t, w); candidate(e, candReplicas) != Refuted {
		t.Fatalf("refuted candidate revived: %+v", e.Candidates)
	}
}

func TestUnusedSlotIsNeverQueried(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.http.answers["unused-slot"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 1)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 1)
	for i := 0; i < 6; i++ {
		w.run(context.Background())
		w.clk.advance(11 * time.Second)
	}
	for _, s := range w.http.callList() {
		if s == "unused-slot" {
			t.Fatal("slot read by no capability was queried")
		}
	}
}

// ── budget and episodes ─────────────────────────────────────────────────────

func TestCallBudgetExhaustionLeavesUnprovenUnknownAndBlocksIO(t *testing.T) {
	l := ReferenceLimits()
	l.MaxCallsPerEpisode = 1
	w := newWorld(l)
	w.seed("ready-replicas", operations.Int(2), time.Second)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())

	if e := episode(t, w); e.State != ExhaustedCalls || e.Calls != 1 {
		t.Fatalf("episode %+v", e)
	}
	ta := w.assess()
	if capState(ta, "serve") != operations.Unknown || capState(ta, "export") != operations.Available {
		t.Fatalf("serve %s export %s", capState(ta, "serve"), capState(ta, "export"))
	}
	before := len(w.http.callList()) + len(w.kube.callList())
	for i := 0; i < 5; i++ { // repeated reconciles inside the cooldown
		w.clk.advance(time.Second)
		w.run(context.Background())
	}
	if after := len(w.http.callList()) + len(w.kube.callList()); after != before {
		t.Fatalf("repeated reconciles dispatched %d more calls", after-before)
	}
	if w.iv.Stats().AdmissionDenied != 5 {
		t.Fatalf("stats %+v", w.iv.Stats())
	}
	// Re-investigation resumes only after the cooldown.
	w.clk.advance(6 * time.Second)
	w.run(context.Background())
	if after := len(w.http.callList()) + len(w.kube.callList()); after != before+1 {
		t.Fatalf("after cooldown: %d calls", after-before)
	}
}

func TestStepBudget(t *testing.T) {
	l := ReferenceLimits()
	l.MaxStepsPerEpisode = 1
	w := newWorld(l)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if e := episode(t, w); e.State != ExhaustedSteps || e.Calls != 1 {
		t.Fatalf("episode %+v", e)
	}
}

func TestDeadlineDiscardsLateResultAndStopsDispatch(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("ready-replicas", operations.Int(1), time.Second)
	w.http.delay = 11 * time.Second // the call itself outlives the episode
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())

	e := episode(t, w)
	if e.State != DeadlineHit || w.iv.Stats().LateDiscarded != 1 {
		t.Fatalf("episode %+v stats %+v", e, w.iv.Stats())
	}
	expectCalls(t, w.kube) // no dispatch after the deadline
	ta := w.assess()
	if capState(ta, "serve") != operations.Unknown {
		t.Fatalf("late value promoted: serve %s", capState(ta, "serve"))
	}
	for _, ev := range ta.Evidence {
		if ev.Slot == "api-serving" && ev.Status != operations.EvidenceProviderUnavailable {
			t.Fatalf("api-serving evidence %s", ev.Status)
		}
	}
}

func TestTimeoutNeverFallsBackToOlderValue(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), time.Second)
	w.seed("ready-replicas", operations.Int(3), time.Second)
	w.seed("api-serving", operations.Bool(true), 20*time.Second) // current but due
	if capState(w.assess(), "serve") != operations.Available {
		t.Fatal("precondition")
	}
	w.http.delay = 3 * time.Second // beyond the 2s per-call timeout
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.run(context.Background())

	expectCalls(t, w.http, "api-serving")
	if e := episode(t, w); e.Kind != KindRefresh {
		t.Fatalf("kind %s", e.Kind)
	}
	ta := w.assess()
	cr, _ := ta.Capability("serve")
	if cr.State != operations.Unknown || cr.Reasons[0].Code != operations.ReasonProviderUnavailable {
		t.Fatalf("serve %+v (old AVAILABLE must not be kept)", cr)
	}
	if capState(ta, "export") != operations.Available {
		t.Fatal("unrelated capability contaminated by a provider timeout")
	}
}

func TestProviderUnavailableIsNotApplicationFailure(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("ready-replicas", operations.Int(3), time.Second)
	w.http.answers["api-serving"] = func() providers.Result { return providers.Result{Unavailable: "http-status-503"} }
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	cr, _ := w.assess().Capability("serve")
	if cr.State != operations.Unknown {
		t.Fatalf("provider outage became %s", cr.State)
	}
}

func TestCancellationStopsDispatchAndDiscards(t *testing.T) {
	w := newWorld(ReferenceLimits())
	ctx, cancel := context.WithCancel(context.Background())
	w.http.answers["api-serving"] = func() providers.Result {
		cancel()
		return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now()}
	}
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(ctx)
	e := episode(t, w)
	if e.State != Cancelled || e.Calls != 1 || w.iv.Stats().LateDiscarded != 1 {
		t.Fatalf("episode %+v stats %+v", e, w.iv.Stats())
	}
	if len(w.iv.Observations()) != 0 {
		t.Fatal("result of a cancelled call was recorded")
	}
	w.run(context.Background()) // same instant: no new allowance
	if n := len(w.http.callList()) + len(w.kube.callList()); n != 1 {
		t.Fatalf("%d calls after cancellation", n)
	}
}

func TestCallsAreSequential(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.base.Targets = append(w.base.Targets, svcTarget("t-svc2", "w-svc2"))
	w.run(context.Background())
	if w.http.maxIn > 1 || w.kube.maxIn > 1 {
		t.Fatalf("concurrent calls: http %d kube %d", w.http.maxIn, w.kube.maxIn)
	}
}

// ── identity, currentness, capacity ─────────────────────────────────────────

func TestIdentityChangeDropsHeldEvidenceAndEpisode(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if capState(w.assess(), "serve") != operations.Available {
		t.Fatal("precondition")
	}
	oldKey := episode(t, w).Key

	w.base.Targets[0].ResolvedUID = "w-svc-recreated"
	w.clk.advance(time.Second)
	w.run(context.Background()) // cooldown: new episode not admitted yet
	for _, o := range w.iv.Observations() {
		if o.Key.ResolvedUID != "w-svc-recreated" {
			t.Fatalf("old-identity evidence still held: %+v", o.Key)
		}
	}
	if s := capState(w.assess(), "serve"); s != operations.Unknown {
		t.Fatalf("serve %s before fresh evidence", s)
	}
	w.clk.advance(10 * time.Second)
	w.run(context.Background())
	if e := episode(t, w); e.Key == oldKey || capState(w.assess(), "serve") != operations.Available {
		t.Fatalf("no fresh episode for the new identity: %+v", e)
	}
}

func TestConfigRevisionMismatchIsNeverDispatched(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.base.Targets[0].AssertionBindings[2].Provider.ConfigRevision = "r2" // api-serving
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	expectCalls(t, w.http)
	if capState(w.assess(), "serve") != operations.Unknown {
		t.Fatal("unregistered provider revision produced evidence")
	}
}

func TestCurrentnessSemanticsAreO1s(t *testing.T) {
	cases := map[string]struct {
		answer func(w *world) func() providers.Result
		code   operations.ReasonCode
	}{
		"stale": {func(w *world) func() providers.Result {
			return func() providers.Result {
				return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now().Add(-time.Minute)}
			}
		}, operations.ReasonEvidenceStale},
		"expired validUntil": {func(w *world) func() providers.Result {
			return func() providers.Result {
				return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now(), ValidUntil: w.clk.now()}
			}
		}, operations.ReasonEvidenceStale},
		// A stamp after local receipt is rejected, not held for later.
		"future": {func(w *world) func() providers.Result {
			return func() providers.Result {
				return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now().Add(time.Hour)}
			}
		}, operations.ReasonProviderUnavailable},
		"wrong type": {func(w *world) func() providers.Result {
			return func() providers.Result { return providers.Result{Value: operations.Int(1), ObservedAt: w.clk.now()} }
		}, operations.ReasonEvidenceTypeMismatch},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(ReferenceLimits())
			w.seed("available-replicas", operations.Int(3), 0)
			w.seed("ready-replicas", operations.Int(3), 0)
			w.http.answers["api-serving"] = tc.answer(w)
			w.run(context.Background())
			cr, _ := w.assess().Capability("serve")
			if cr.State != operations.Unknown || cr.Reasons[0].Code != tc.code {
				t.Fatalf("serve %+v", cr)
			}
		})
	}
}

func TestEqualLatestConflictIsUnknown(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	at := t0.Add(-20 * time.Second) // current, and due for refresh
	val := true
	w.http.answers["api-serving"] = func() providers.Result {
		return providers.Result{Value: operations.Bool(val), ObservedAt: at}
	}
	w.run(context.Background())
	val = false
	w.clk.advance(10 * time.Second)
	w.run(context.Background())
	cr, _ := w.assess().Capability("serve")
	if cr.State != operations.Unknown || cr.Reasons[0].Code != operations.ReasonConflictingEvidence {
		t.Fatalf("serve %+v", cr)
	}
}

// Held statements follow local receipt order: the latest call's answer
// replaces what was held even if the producer stamps it earlier. The result
// is never a false AVAILABLE. (O1's own stale-red rule over a given set of
// statements is unchanged in pkg/operations.)
func TestLatestReceiptWinsOverProducerStampOrder(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.seed("api-serving", operations.Bool(true), 16*time.Second) // due
	w.http.answers["api-serving"] = func() providers.Result {
		return providers.Result{Value: operations.Bool(false), ObservedAt: t0.Add(-25 * time.Second)} // older stamp
	}
	w.run(context.Background())
	if s := capState(w.assess(), "serve"); s != operations.Unavailable {
		t.Fatalf("latest receipt did not win: %s", s)
	}
}

// Guardrail P1-1 regression: a far-future stamped success must not shadow
// later real negative facts nor turn into AVAILABLE once the clock catches up.
func TestFarFutureSuccessCannotShadowLaterNegativeFacts(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	answer := func() providers.Result {
		return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now().Add(time.Hour)}
	}
	w.http.answers["api-serving"] = func() providers.Result { return answer() }
	w.run(context.Background())
	if s := capState(w.assess(), "serve"); s == operations.Available {
		t.Fatal("future-stamped success accepted")
	}
	// Real negative facts follow.
	answer = func() providers.Result {
		return providers.Result{Value: operations.Bool(false), ObservedAt: w.clk.now()}
	}
	for i := 0; i < 8; i++ {
		w.clk.advance(11 * time.Second)
		w.seed("available-replicas", operations.Int(3), 0)
		w.seed("ready-replicas", operations.Int(3), 0)
		w.run(context.Background())
		if s := capState(w.assess(), "serve"); s != operations.Unavailable {
			t.Fatalf("round %d: serve %s, want the real negative fact", i, s)
		}
	}
	// Long after the future stamp would have become "current".
	w.clk.advance(time.Hour)
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.run(context.Background())
	if s := capState(w.assess(), "serve"); s == operations.Available {
		t.Fatal("shadowed future success became a false AVAILABLE")
	}
}

// Guardrail P1-1 regression: a success stamped ahead of our clock (producer
// skew) followed by a provider outage — the outage must win, at every
// evaluation instant afterwards.
func TestSkewedSuccessFollowedByOutageNeverAvailable(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.http.answers["api-serving"] = func() providers.Result {
		return providers.Result{Value: operations.Bool(true), ObservedAt: w.clk.now().Add(5 * time.Second)}
	}
	w.run(context.Background())
	w.http.answers["api-serving"] = func() providers.Result { return providers.Result{Unavailable: "http-status-503"} }
	for i := 0; i < 6; i++ {
		w.clk.advance(2 * time.Second)
		if s := capState(w.assess(), "serve"); s == operations.Available {
			t.Fatalf("t+%ds: skewed success surfaced as AVAILABLE", 2*(i+1))
		}
	}
	w.clk.advance(10 * time.Second)
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.run(context.Background())
	cr, _ := w.assess().Capability("serve")
	if cr.State != operations.Unknown || cr.Reasons[0].Code != operations.ReasonProviderUnavailable {
		t.Fatalf("serve %+v, want the outage", cr)
	}

	// Same order with a stamp that is not in the future: the later outage
	// still replaces the earlier success.
	w = newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.run(context.Background())
	w.clk.advance(16 * time.Second) // due for refresh
	w.http.answers["api-serving"] = func() providers.Result { return providers.Result{Unavailable: "http-status-503"} }
	w.run(context.Background())
	if s := capState(w.assess(), "serve"); s != operations.Unknown {
		t.Fatalf("serve %s after the outage", s)
	}
}

// Guardrail P2-1 / Codex 4166341286 regression.
func TestNextWakeIgnoresElapsedAndFinishedWork(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background()) // Resolved: no cooldown wake needed
	qs := w.queries()
	wake := w.iv.NextWake(qs, w.clk.now())
	if want := t0.Add(15 * time.Second); !wake.Equal(want) {
		t.Fatalf("wake %s, want the refresh due time %s", wake, want)
	}
	// Target gone: no wake at all, and its old record is forgotten once
	// its cooldown has elapsed.
	w.clk.advance(11 * time.Second)
	w.iv.Run(context.Background(), w.base, Queries{})
	if wk := w.iv.NextWake(Queries{}, w.clk.now()); !wk.IsZero() {
		t.Fatalf("wake %s for a target that is no longer queryable", wk)
	}
	if _, ok := w.iv.Episode("t-svc"); ok {
		t.Fatal("obsolete terminal episode retained")
	}

	// Exhausted episode: the cooldown end is a wake while it lies ahead,
	// and never once it has elapsed.
	l := ReferenceLimits()
	l.MaxCallsPerEpisode = 1
	w = newWorld(l)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.run(context.Background())
	qs = w.queries()
	if wk := w.iv.NextWake(qs, w.clk.now()); !wk.Equal(t0.Add(10 * time.Second)) {
		t.Fatalf("wake %s, want the cooldown end", wk)
	}
	if wk := w.iv.NextWake(qs, t0.Add(10*time.Second)); !wk.IsZero() && !wk.After(t0.Add(10*time.Second)) {
		t.Fatalf("elapsed cooldown returned as wake %s", wk)
	}
}

func TestCapacityRefusesInsteadOfEvicting(t *testing.T) {
	l := ReferenceLimits()
	l.MaxHeldObservations = 1
	w := newWorld(l)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if e := episode(t, w); e.State != CapacityHit || len(w.iv.Observations()) != 1 {
		t.Fatalf("episode %+v held %d", e, len(w.iv.Observations()))
	}

	l = ReferenceLimits()
	l.MaxResidentEpisodes = 1
	w = newWorld(l)
	w.base.Targets = append(w.base.Targets, svcTarget("t-svc2", "w-svc2"))
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.run(context.Background())
	if _, ok := w.iv.Episode("t-svc2"); ok || w.iv.Stats().CapacityDenied != 1 {
		t.Fatalf("second episode admitted beyond capacity: %+v", w.iv.Stats())
	}
	w.clk.advance(10 * time.Second) // first episode's cooldown elapsed: prunable
	w.run(context.Background())
	if _, ok := w.iv.Episode("t-svc2"); !ok {
		t.Fatal("capacity not released after cooldown")
	}
}

func TestRefreshQueriesOnlyDueSlots(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 2*time.Second)
	w.seed("ready-replicas", operations.Int(3), 2*time.Second)
	w.seed("api-serving", operations.Bool(true), 16*time.Second)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.run(context.Background())
	expectCalls(t, w.http, "api-serving")
	expectCalls(t, w.kube)
	if e := episode(t, w); e.Kind != KindRefresh || e.State != Refreshed {
		t.Fatalf("episode %+v", e)
	}
}

func TestNothingToDoStartsNoEpisode(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.seed("available-replicas", operations.Int(3), 0)
	w.seed("ready-replicas", operations.Int(3), 0)
	w.seed("api-serving", operations.Bool(true), 0)
	w.run(context.Background())
	if _, ok := w.iv.Episode("t-svc"); ok || w.iv.Stats().Dispatched != 0 {
		t.Fatal("episode started with nothing to investigate or refresh")
	}
}

func TestOnlyAuthorizedBindingsReachTheRegistry(t *testing.T) {
	w := newWorld(ReferenceLimits())
	w.base.Targets[0].AssertionBindings = w.base.Targets[0].AssertionBindings[:1] // others denied upstream
	w.queries()
	if w.reg.lookups != 1 {
		t.Fatalf("registry lookups %d, want 1", w.reg.lookups)
	}
}
