package opswire

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// chainNode describes one target in a dependency graph fixture: its own
// namespace and the node its single capability depends on ("" for none).
type chainNode struct {
	ns   string
	next string
}

// chainWorld builds one contract + target per node. Node n has UID "uid-n",
// contract "ct-n" with capability "relay-n", and depends on relay-<next>.
// Every cross-namespace hop gets an exact Dependency grant.
func chainWorld(nodes map[string]chainNode) *world {
	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	w := &world{}
	contracts := map[string]opsv1.OperationalContract{}
	targets := map[string]opsv1.OperationalTarget{}
	for _, n := range names {
		nd := nodes[n]
		spec := opsv1.OperationalContractSpec{
			Assertions: []opsv1.AssertionSlot{boolSlot("up")},
			Capabilities: []opsv1.Capability{{Type: capT("relay-" + n), Requirements: []opsv1.Requirement{
				isTrue("up", "UNAVAILABLE"),
			}}},
		}
		if nd.next != "" {
			spec.DependencySlots = []opsv1.DependencySlot{{Name: "next"}}
			spec.Capabilities[0].Requirements = append(spec.Capabilities[0].Requirements, depReq("next", "relay-"+nd.next, "UNAVAILABLE"))
		}
		contracts[n] = contract(nd.ns, "contract-"+n, "ct-"+n, spec)
		targets[n] = target(nd.ns, "node-"+n, "uid-"+n, "contract-"+n, bind("up", "kubernetes-workload-status"))
	}
	for _, n := range names {
		nd := nodes[n]
		tg := targets[n]
		if nd.next != "" {
			to := nodes[nd.next]
			tg.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("next", targets[nd.next], contracts[nd.next])}
			if to.ns != nd.ns {
				w.grants = append(w.grants, grant(to.ns, nd.ns, opsv1.ReferenceGrantTo{Type: opsv1.ReferenceDependency, Name: "node-" + nd.next}))
			}
		}
		w.contracts = append(w.contracts, contracts[n])
		w.targets = append(w.targets, tg)
	}
	for _, tg := range w.targets {
		w.observations = append(w.observations, w.observe(tg, "up", operations.Bool(true), fresh))
	}
	return w
}

func statusOfNode(w *world, n string) opsv1.OperationalTargetStatus { return w.status("node-" + n) }

func statusJSON(t *testing.T, st opsv1.OperationalTargetStatus) string {
	t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// expectNoForeign fails if the status of node self mentions any identity of
// a node it neither is nor directly pins.
func expectNoForeign(t *testing.T, w *world, nodes map[string]chainNode, self string) {
	t.Helper()
	js := statusJSON(t, statusOfNode(w, self))
	for other := range nodes {
		if other == self || other == nodes[self].next {
			continue
		}
		for _, id := range []string{"uid-" + other, "relay-" + other, "ct-" + other, "contract-" + other, "ref:uid-" + other} {
			if strings.Contains(js, id) {
				t.Errorf("status of %s discloses unbound %q: %s", self, id, js)
			}
		}
	}
}

func cycleReason(t *testing.T, st opsv1.OperationalTargetStatus) opsv1.StatusReason {
	t.Helper()
	if st.Valid {
		t.Fatalf("cycle member assessed as valid: %+v", st)
	}
	return expectReason(t, st.InvalidReasons, operations.ReasonDependencyCycle)
}

var threeNamespaceCycle = map[string]chainNode{
	"a": {ns: "ns-a", next: "b"},
	"b": {ns: "ns-b", next: "c"},
	"c": {ns: "ns-c", next: "a"},
}

func TestCrossNamespaceCycleDetailIsRedacted(t *testing.T) {
	w := chainWorld(threeNamespaceCycle)
	for _, n := range []string{"a", "b", "c"} {
		r := cycleReason(t, statusOfNode(w, n))
		if r.Subject != "capabilities:relay-"+n {
			t.Errorf("%s: own-capability subject not kept: %q", n, r.Subject)
		}
		if r.Detail != "" {
			t.Errorf("%s: cross-namespace cycle detail exposed: %q", n, r.Detail)
		}
		expectNoForeign(t, w, threeNamespaceCycle, n)
	}

	// O1 itself is unchanged: it still reports the full SCC.
	w.resolveAll()
	a, _ := operations.Evaluate(Build(w.input()).Snapshot)
	ta, _ := a.Target("uid-a")
	if ta.Valid || len(ta.InvalidReasons) == 0 ||
		ta.InvalidReasons[0].Detail != "cycle members: uid-a/relay-a,uid-b/relay-b,uid-c/relay-c" {
		t.Fatalf("O1 result changed: %+v", ta)
	}
}

func TestMixedNamespaceCycleDoesNotDiscloseIndirectMember(t *testing.T) {
	mixed := map[string]chainNode{
		"a": {ns: "ns-a", next: "b"},
		"b": {ns: "ns-b", next: "c"},
		"c": {ns: "ns-b", next: "a"},
	}
	w := chainWorld(mixed)
	for _, n := range []string{"a", "b", "c"} {
		if r := cycleReason(t, statusOfNode(w, n)); r.Detail != "" {
			t.Errorf("%s: detail of a cycle spanning namespaces exposed: %q", n, r.Detail)
		}
		expectNoForeign(t, w, mixed, n)
	}
}

func TestSameNamespaceCycleKeepsUsefulDetail(t *testing.T) {
	w := chainWorld(map[string]chainNode{
		"a": {ns: "ns-a", next: "b"},
		"b": {ns: "ns-a", next: "a"},
	})
	for _, n := range []string{"a", "b"} {
		r := cycleReason(t, statusOfNode(w, n))
		if r.Detail != "cycle members: uid-a/relay-a,uid-b/relay-b" {
			t.Errorf("%s: same-namespace cycle detail lost: %q", n, r.Detail)
		}
	}
}

func TestUnparseableCycleDetailFailsClosed(t *testing.T) {
	ns := map[string]string{"uid-a": "ns-a"}
	for _, d := range []string{"", "members: uid-a/x", "cycle members: ", "cycle members: uid-a", "cycle members: uid-z/x", "cycle members: uid-a/x,/y"} {
		if cycleWithinNamespace(d, "ns-a", ns) {
			t.Errorf("%q treated as same-namespace", d)
		}
	}
	if !cycleWithinNamespace("cycle members: uid-a/x", "ns-a", ns) {
		t.Error("own-namespace member rejected")
	}
}

func TestUnrelatedTargetUnaffectedByForeignCycle(t *testing.T) {
	alone := map[string]chainNode{"d": {ns: "ns-d"}}
	withCycle := map[string]chainNode{"d": {ns: "ns-d"}}
	for k, v := range threeNamespaceCycle {
		withCycle[k] = v
	}
	want := statusOfNode(chainWorld(alone), "d")
	got := statusOfNode(chainWorld(withCycle), "d")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unrelated target changed by a foreign cycle\n got %+v\nwant %+v", got, want)
	}
	expectState(t, got, "relay-d", operations.Available)
}

func TestCycleRedactionIsDeterministicAndChurnFree(t *testing.T) {
	want := map[string]opsv1.OperationalTargetStatus{}
	for _, n := range []string{"a", "b", "c"} {
		want[n] = statusOfNode(chainWorld(threeNamespaceCycle), n)
	}
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 100; i++ {
		w := chainWorld(threeNamespaceCycle)
		r.Shuffle(len(w.contracts), func(a, b int) { w.contracts[a], w.contracts[b] = w.contracts[b], w.contracts[a] })
		r.Shuffle(len(w.targets), func(a, b int) { w.targets[a], w.targets[b] = w.targets[b], w.targets[a] })
		r.Shuffle(len(w.grants), func(a, b int) { w.grants[a], w.grants[b] = w.grants[b], w.grants[a] })
		r.Shuffle(len(w.observations), func(a, b int) { w.observations[a], w.observations[b] = w.observations[b], w.observations[a] })
		for n, st := range want {
			if got := statusOfNode(w, n); !reflect.DeepEqual(got, st) {
				t.Fatalf("iteration %d: %s status depends on input order", i, n)
			}
		}
	}
	first, changed := Apply(opsv1.OperationalTargetStatus{}, want["a"], at)
	if !changed {
		t.Fatal("first status not written")
	}
	for i := 0; i < 10; i++ {
		if _, changed := Apply(first, statusOfNode(chainWorld(threeNamespaceCycle), "a"), at.Add(time.Duration(i+1)*time.Second)); changed {
			t.Fatalf("repeat %d: redacted cycle status rewritten", i)
		}
	}
}
