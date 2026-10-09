package interaction

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
)

// Repeated reconciles and timestamp-only evidence refreshes (same values,
// same evidence references, as both reference providers emit) do not change
// the summary, so the status is not written (opswire.Apply: no change).
func TestRepeatAndRefreshDoNotChurn(t *testing.T) {
	w := yDown()
	first := w.reconcile()
	for i := 0; i < 5; i++ {
		w.now = w.now.Add(10 * time.Second)
		for j := range w.observations {
			w.observations[j].ObservedAt = w.now.Add(-fresh) // same values, new stamps
		}
		next := w.reconcile()
		for name := range first.summary {
			if !reflect.DeepEqual(first.summary[name], next.summary[name]) {
				t.Fatalf("%s changed on refresh %d:\n%+v\n%+v", name, i, first.summary[name], next.summary[name])
			}
			cur, _ := opswire.Apply(opsv1.OperationalTargetStatus{}, opsv1.OperationalTargetStatus{Interaction: first.summary[name]}, at)
			des := opsv1.OperationalTargetStatus{Interaction: next.summary[name]}
			if _, changed := opswire.Apply(cur, des, w.now); changed {
				t.Fatalf("%s would be written", name)
			}
		}
	}
}

// Flapping between two meanings shows every real transition (fingerprint
// changes) but raises a decision request only once per meaning.
func TestFlappingIsShownButRequestedOnce(t *testing.T) {
	w := yDown()
	approval := resp("failover", "storage-oncall", "persist")
	approval.RequiresApproval = true
	w.profile = &Profile{Revision: "p1", Responses: []Response{approval}}

	var requests, transitions int
	var last string
	for i := 0; i < 10; i++ {
		v := i%2 == 1 // false, true, false, ...
		w.observations[6] = w.fact("y", "storage-writable", operations.Bool(v), fresh)
		s := w.reconcile().summary["y"]
		if s.Fingerprint != last {
			transitions++
			if (s.Level == opsv1.LevelDecisionRequired || s.Level == opsv1.LevelImmediateIntervention) && !s.Recurring {
				requests++
			}
		}
		last = s.Fingerprint
		if len(s.RecentFingerprints) > maxHistory {
			t.Fatalf("history %d", len(s.RecentFingerprints))
		}
	}
	if transitions != 10 || requests != 1 {
		t.Fatalf("transitions %d (want 10, none hidden), decision requests %d (want 1)", transitions, requests)
	}
	if s := w.prev["t-y"]; !s.Recurring || s.Recurrences != 8 {
		t.Fatalf("recurring %v recurrences %d", s.Recurring, s.Recurrences)
	}
}

// A changed identity (recreated target, workload, contract revision,
// provider configuration or profile revision) starts a fresh history:
// nothing judged for the old identity is reused.
func TestIdentityChangeResetsHistory(t *testing.T) {
	cases := map[string]func(w *world){
		"target recreated": func(w *world) { w.targets[2].UID = "t-y2" },
		"contract revision": func(w *world) {
			w.contracts[2].UID = "c-svc2"
		},
		"provider config revision": func(w *world) { w.targets[2].Spec.AssertionBindings[2].Provider.ConfigRevision = "r2" },
		"profile revision":         func(w *world) { w.profile.Revision = "p2" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			w := yDown()
			approval := resp("failover", "storage-oncall", "persist")
			approval.RequiresApproval = true
			approval.Target.UID = "t-y"
			w.profile = &Profile{Revision: "p1", Responses: []Response{approval}}
			before := w.reconcile().summary["y"]
			w.observations[6] = w.fact("y", "storage-writable", operations.Bool(true), fresh)
			w.reconcile()
			change(w)
			w.healthy()
			w.observations[6] = w.fact("y", "storage-writable", operations.Bool(false), fresh)
			r := w.reconcile()
			zero(t, name, check(r, nil))
			after := r.summary["y"]
			if after.IdentityDigest == before.IdentityDigest {
				t.Fatal("identity digest unchanged")
			}
			if after.Recurring || after.Recurrences != 0 || len(after.RecentFingerprints) != 1 {
				t.Fatalf("history reused across identities: %+v", after)
			}
			for _, f := range after.RecentFingerprints {
				if f == before.Fingerprint {
					t.Fatal("old fingerprint reused")
				}
			}
			if name == "target recreated" && len(after.Responses) != 0 {
				t.Fatalf("response pinned to the old instance offered: %+v", after.Responses)
			}
		})
	}
}

// Cross-namespace: the summary of X (namespace "b") never carries more than
// X's own redacted status. A denied binding reveals nothing about whether
// the referent exists, and a foreign cycle detail stays redacted.
func TestCrossNamespaceNonDisclosure(t *testing.T) {
	build := func(grant, aExists bool) (*world, result) {
		w := newWorld()
		rc := contract("a", "resolver-v1", "c-res", resolverSpec())
		wc := contract("b", "worker-v1", "c-wrk", workerSpec())
		a := target("a", "secret-resolver", "t-a", "resolver-v1", bind("up", "http-probe"), bind("fast", "http-probe"))
		x := target("b", "x", "t-x", "worker-v1", bind("submit-up", "http-probe"), bind("status-up", "kube-status"))
		x.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("resolver", a, rc)}
		w.contracts = []opsv1.OperationalContract{rc, wc}
		w.targets = []opsv1.OperationalTarget{x}
		if aExists {
			w.targets = append(w.targets, a)
		}
		if grant {
			w.grants = []opsv1.OperationalReferenceGrant{{
				ObjectMeta: x.ObjectMeta, // replaced below
				Spec: opsv1.OperationalReferenceGrantSpec{
					From: []opsv1.ReferenceGrantFrom{{Kind: "OperationalTarget", Namespace: "b"}},
					To:   []opsv1.ReferenceGrantTo{{Type: opsv1.ReferenceDependency, Name: "secret-resolver"}},
				},
			}}
			w.grants[0].Namespace, w.grants[0].Name = "a", "g"
		}
		w.observe("x", "submit-up", operations.Bool(true))
		w.observe("x", "status-up", operations.Bool(true))
		if aExists {
			w.observe("secret-resolver", "up", operations.Bool(false))
			w.observe("secret-resolver", "fast", operations.Bool(true))
		}
		return w, w.reconcile()
	}

	_, granted := build(true, true)
	x := granted.summary["x"]
	if !reflect.DeepEqual(capNames(x.Affected), []string{"submit"}) {
		t.Fatalf("granted: %v", capNames(x.Affected))
	}
	raw, _ := json.Marshal(x)
	for _, leak := range []string{"ref:a/", "http-probe@r1\",\"evidenceRef\":\"ref:a", "c-res"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("foreign detail %q in %s", leak, raw)
		}
	}
	statusRaw, _ := json.Marshal(granted.status["x"])
	if strings.Contains(string(raw), "secret-resolver") && !strings.Contains(string(statusRaw), "secret-resolver") {
		t.Fatal("summary discloses a name the status does not")
	}

	_, deniedPresent := build(false, true)
	_, deniedAbsent := build(false, false)
	p, ab := deniedPresent.summary["x"], deniedAbsent.summary["x"]
	if !reflect.DeepEqual(p, ab) {
		t.Fatalf("denied summary depends on referent existence:\n%+v\n%+v", p, ab)
	}
	found := false
	for _, m := range p.MissingEvidence {
		found = found || m.State == "BindingDenied:Dependency"
	}
	if !found || len(p.Unknown) != 1 {
		t.Fatalf("denied: missing %+v unknown %v", p.MissingEvidence, capNames(p.Unknown))
	}
}

// Same input in any order gives byte-identical output.
func TestDeterministicUnderInputOrder(t *testing.T) {
	render := func(seed int64) string {
		w := yDown()
		w.observations[0] = w.fact("a", "up", operations.Bool(false), fresh)
		w.profile = &Profile{Revision: "p1", Responses: []Response{
			resp("remount", "storage-oncall", "persist"), resp("reschedule", "platform-oncall", "persist"), resp("remount", "", "persist"),
		}}
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(w.targets), func(i, j int) { w.targets[i], w.targets[j] = w.targets[j], w.targets[i] })
		rng.Shuffle(len(w.contracts), func(i, j int) { w.contracts[i], w.contracts[j] = w.contracts[j], w.contracts[i] })
		rng.Shuffle(len(w.observations), func(i, j int) { w.observations[i], w.observations[j] = w.observations[j], w.observations[i] })
		rng.Shuffle(len(w.profile.Responses), func(i, j int) {
			w.profile.Responses[i], w.profile.Responses[j] = w.profile.Responses[j], w.profile.Responses[i]
		})
		for i := range w.targets {
			bs := w.targets[i].Spec.AssertionBindings
			rng.Shuffle(len(bs), func(a, b int) { bs[a], bs[b] = bs[b], bs[a] })
		}
		r := w.reconcile()
		b, _ := json.Marshal([]any{r.summary["a"], r.summary["x"], r.summary["y"]})
		return string(b)
	}
	want := render(0)
	for seed := int64(1); seed < 20; seed++ {
		if got := render(seed); got != want {
			t.Fatalf("seed %d differs:\n%s\n%s", seed, got, want)
		}
	}
}

// Every list is bounded whatever the input size, and the summary line stays
// within its limit.
func TestOutputIsBounded(t *testing.T) {
	st := opsv1.OperationalTargetStatus{Valid: true}
	for i := 0; i < 200; i++ {
		ct := opsv1.CapabilityType{Domain: "example.io", Name: fmt.Sprintf("cap-%03d-%s", i, strings.Repeat("x", 40)), Revision: "v1"}
		st.Capabilities = append(st.Capabilities, opsv1.CapabilityStatus{Type: ct, State: []string{"UNAVAILABLE", "UNKNOWN", "AVAILABLE"}[i%3],
			Reasons: []opsv1.StatusReason{{Code: "a"}, {Code: "b"}, {Code: "c"}, {Code: "d"}, {Code: "e"}}})
		st.Evidence = append(st.Evidence, opsv1.EvidenceStatus{Slot: fmt.Sprintf("s%03d", i), State: []string{"Current", "Missing"}[i%2]})
	}
	p := &Profile{Revision: "p1"}
	inv := &Investigation{Kind: "investigate", Outcome: "ExhaustedCalls"}
	for i := 0; i < 100; i++ {
		p.Responses = append(p.Responses, Response{Action: ActionRef{Name: fmt.Sprintf("act-%d", i), Revision: "r1"},
			Target: TargetRef{Namespace: "apps", Name: "big"}, For: st.Capabilities[0].Type, Owner: "o"})
		inv.Candidates = append(inv.Candidates, Candidate{ID: fmt.Sprintf("c%03d", i), Status: "Open"})
	}
	s := Project(Input{Namespace: "apps", Name: "big", UID: "t-big", Status: st, Profile: p, Investigation: inv}, nil)
	lens := map[string][2]int{
		"affected": {len(s.Affected), maxCaps}, "unaffected": {len(s.Unaffected), maxCaps}, "unknown": {len(s.Unknown), maxCaps},
		"facts": {len(s.ConfirmedFacts), maxFacts}, "missing": {len(s.MissingEvidence), maxFacts}, "candidates": {len(s.CauseCandidates), maxCandidates},
		"responses": {len(s.Responses), maxResponses}, "levelReasons": {len(s.LevelReasons), maxReasons}, "human": {len(s.HumanReasons), maxReasons},
		"post": {len(s.PendingPostConditions), maxPost}, "summary": {len(s.Summary), maxSummary},
	}
	for k, v := range lens {
		if v[0] > v[1] {
			t.Fatalf("%s: %d > %d", k, v[0], v[1])
		}
	}
	for _, c := range s.Affected {
		if len(c.Reasons) > maxCapReasons {
			t.Fatalf("capability reasons %d", len(c.Reasons))
		}
	}
	if b, _ := json.Marshal(s); len(b) > 64<<10 {
		t.Fatalf("summary is %d bytes", len(b))
	}
}

// A dependency cycle through another namespace is not passed through
// optimistically, and the summary of a member never names the foreign
// members (the cycle detail redaction of the status is inherited).
func TestCrossNamespaceCycleStaysRedacted(t *testing.T) {
	w := newWorld()
	spec := func(n, next string) opsv1.OperationalContractSpec {
		return opsv1.OperationalContractSpec{
			Assertions:      []opsv1.AssertionSlot{boolSlot("up")},
			DependencySlots: []opsv1.DependencySlot{{Name: "next"}},
			Capabilities: []opsv1.Capability{{Type: capT("relay-" + n), Requirements: []opsv1.Requirement{
				isTrue("up", "UNAVAILABLE"), depReq("next", "relay-"+next, "UNAVAILABLE"),
			}}},
		}
	}
	cp := contract("p", "contract-p", "ct-p", spec("p", "q"))
	cq := contract("q", "contract-q", "ct-q", spec("q", "p"))
	tp := target("p", "node-p", "uid-p", "contract-p", bind("up", "kube-status"))
	tq := target("q", "hidden-node-q", "uid-hidden-q", "contract-q", bind("up", "kube-status"))
	tp.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("next", tq, cq)}
	tq.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("next", tp, cp)}
	g := func(ns, from, to string) opsv1.OperationalReferenceGrant {
		gr := opsv1.OperationalReferenceGrant{Spec: opsv1.OperationalReferenceGrantSpec{
			From: []opsv1.ReferenceGrantFrom{{Kind: "OperationalTarget", Namespace: from}},
			To:   []opsv1.ReferenceGrantTo{{Type: opsv1.ReferenceDependency, Name: to}},
		}}
		gr.Namespace, gr.Name = ns, "g"
		return gr
	}
	w.contracts = []opsv1.OperationalContract{cp, cq}
	w.targets = []opsv1.OperationalTarget{tp, tq}
	w.grants = []opsv1.OperationalReferenceGrant{g("q", "p", "hidden-node-q"), g("p", "q", "node-p")}
	w.observe("node-p", "up", operations.Bool(true))
	w.observe("hidden-node-q", "up", operations.Bool(true))
	r := w.reconcile()
	zero(t, "cycle", check(r, nil))
	s := r.summary["node-p"]
	if s.Level == opsv1.LevelNoAction {
		t.Fatalf("cycle passed optimistically: %+v", s)
	}
	raw, _ := json.Marshal(s)
	for _, foreign := range []string{"uid-hidden-q", "hidden-node-q", "ct-q"} {
		if strings.Contains(string(raw), foreign) {
			t.Fatalf("foreign cycle member %q disclosed: %s", foreign, raw)
		}
	}
	t.Logf("node-p: %s %v", s.Level, reasonCodes(s.LevelReasons))
}
