package opswire

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

func capState(t *testing.T, st opsv1.OperationalTargetStatus, name string) opsv1.CapabilityStatus {
	t.Helper()
	if !st.Valid {
		t.Fatalf("target invalid: %+v", st.InvalidReasons)
	}
	for _, c := range st.Capabilities {
		if c.Type.Name == name {
			return c
		}
	}
	t.Fatalf("capability %s missing in %+v", name, st.Capabilities)
	return opsv1.CapabilityStatus{}
}

func expectState(t *testing.T, st opsv1.OperationalTargetStatus, name string, want operations.CapabilityState) opsv1.CapabilityStatus {
	t.Helper()
	c := capState(t, st, name)
	if c.State != string(want) {
		t.Fatalf("%s = %s, want %s (%+v)", name, c.State, want, c.Reasons)
	}
	return c
}

func expectReason(t *testing.T, rs []opsv1.StatusReason, code operations.ReasonCode) opsv1.StatusReason {
	t.Helper()
	for _, r := range rs {
		if r.Code == string(code) {
			return r
		}
	}
	t.Fatalf("missing reason %s in %+v", code, rs)
	return opsv1.StatusReason{}
}

// ── contract wire ───────────────────────────────────────────────────────────

func TestContractWireMapsLosslesslyToO1(t *testing.T) {
	c := contract("a", "svc-v1", "c-svc", serviceSpec())
	got := ContractFromWire(&c)
	want := operations.Contract{
		Identity: operations.ContractIdentity{Namespace: "a", Name: "svc-v1", UID: "c-svc", SpecDigest: SpecDigest(&c.Spec)},
		Assertions: []operations.AssertionSlot{
			{Name: "api-serving", Type: operations.TypeBoolean, Enum: []string{}, MaxAge: 30 * time.Second},
			{Name: "available-replicas", Type: operations.TypeInteger, Enum: []string{}, MaxAge: 30 * time.Second},
			{Name: "role", Type: operations.TypeString, Enum: []string{"primary", "replica"}, MaxAge: 30 * time.Second},
		},
		Capabilities: []operations.Capability{
			{Type: operations.CapabilityType{Domain: domain, Name: "serve", Revision: "v1"}, Requirements: []operations.Requirement{
				{Predicate: &operations.Predicate{Assertion: "api-serving", Op: operations.OpIsTrue}, OnUnmet: operations.ImpactUnavailable},
			}},
			{Type: operations.CapabilityType{Domain: domain, Name: "accept-writes", Revision: "v1"}, Requirements: []operations.Requirement{
				{Predicate: &operations.Predicate{Assertion: "role", Op: operations.OpEq, Operand: operations.String("primary")}, OnUnmet: operations.ImpactUnavailable},
			}},
			{Type: operations.CapabilityType{Domain: domain, Name: "redundant", Revision: "v1"}, Requirements: []operations.Requirement{
				{Envelope: "recommended", OnUnmet: operations.ImpactDegraded},
			}},
		},
		Envelopes: []operations.Envelope{
			{Name: "minimum", Class: operations.EnvelopeMinimum, Requirements: []operations.Predicate{
				{Assertion: "available-replicas", Op: operations.OpGte, Operand: operations.Int(1)},
			}},
			{Name: "recommended", Class: operations.EnvelopeRecommended, Requirements: []operations.Predicate{
				{Assertion: "available-replicas", Op: operations.OpGte, Operand: operations.Int(2)},
				{Assertion: "api-serving", Op: operations.OpIsTrue},
			}},
		},
	}
	// nil and empty enum are the same; compare via the digest-independent fields.
	for i := range got.Assertions {
		if got.Assertions[i].Enum == nil {
			got.Assertions[i].Enum = []string{}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lossy mapping\n got %+v\nwant %+v", got, want)
	}
	if st := ContractStatus(&c); !st.Valid || st.SpecDigest != want.Identity.SpecDigest {
		t.Fatalf("contract status %+v", st)
	}
}

func TestContractPredicateGrammarIsO1Validated(t *testing.T) {
	valid := map[string]opsv1.Predicate{
		"Boolean IsTrue":  {Assertion: "b", Operator: "IsTrue"},
		"Boolean IsFalse": {Assertion: "b", Operator: "IsFalse"},
		"Integer Gte":     {Assertion: "n", Operator: "Gte", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
		"Integer Lte":     {Assertion: "n", Operator: "Lte", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
		"Integer Eq":      {Assertion: "n", Operator: "Eq", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
		"String NotEq":    {Assertion: "s", Operator: "NotEq", Operand: &opsv1.Operand{String: ptr("x")}},
		"Enum Eq":         {Assertion: "e", Operator: "Eq", Operand: &opsv1.Operand{String: ptr("on")}},
	}
	invalid := map[string]opsv1.Predicate{
		"IsTrue on Integer":         {Assertion: "n", Operator: "IsTrue"},
		"Gte on Boolean":            {Assertion: "b", Operator: "Gte", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
		"Gte on String":             {Assertion: "s", Operator: "Gte", Operand: &opsv1.Operand{Integer: ptr[int64](1)}},
		"Eq type mismatch":          {Assertion: "n", Operator: "Eq", Operand: &opsv1.Operand{String: ptr("1")}},
		"Enum operand outside enum": {Assertion: "e", Operator: "Eq", Operand: &opsv1.Operand{String: ptr("maybe")}},
		"IsTrue with operand":       {Assertion: "b", Operator: "IsTrue", Operand: &opsv1.Operand{Boolean: ptr(true)}},
		"Eq without operand":        {Assertion: "n", Operator: "Eq"},
		"non-canonical operand":     {Assertion: "n", Operator: "Eq", Operand: &opsv1.Operand{Integer: ptr[int64](0), String: ptr("stray")}},
		"bool operand + residue":    {Assertion: "b", Operator: "Eq", Operand: &opsv1.Operand{Boolean: ptr(true), String: ptr("stray")}},
		"string operand + residue":  {Assertion: "s", Operator: "Eq", Operand: &opsv1.Operand{String: ptr("x"), Integer: ptr[int64](0)}},
		"unknown operator":          {Assertion: "n", Operator: "Matches", Operand: &opsv1.Operand{String: ptr(".*")}},
	}
	build := func(p opsv1.Predicate) opsv1.OperationalContract {
		return contract("a", "p", "c-p", opsv1.OperationalContractSpec{
			Assertions: []opsv1.AssertionSlot{
				boolSlot("b"),
				{Name: "n", Type: "Integer", MaxAge: dur(time.Second)},
				{Name: "s", Type: "String", MaxAge: dur(time.Second)},
				{Name: "e", Type: "String", Enum: []string{"on", "off"}, MaxAge: dur(time.Second)},
			},
			Capabilities: []opsv1.Capability{{Type: capT("c"), Requirements: []opsv1.Requirement{{Predicate: &p, OnUnmet: "DEGRADED"}}}},
		})
	}
	for name, p := range valid {
		c := build(p)
		if st := ContractStatus(&c); !st.Valid {
			t.Errorf("%s rejected: %v", name, st.ValidationErrors)
		}
	}
	for name, p := range invalid {
		c := build(p)
		if st := ContractStatus(&c); st.Valid {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLocalAliasMustBeUniqueWithinContract(t *testing.T) {
	spec := serviceSpec()
	dup := spec.Capabilities[0]
	dup.Type.Domain = "other.example"
	spec.Capabilities = append(spec.Capabilities, dup)
	c := contract("a", "svc", "c", spec)
	if ContractStatus(&c).Valid {
		t.Fatal("two capabilities with the same local alias accepted")
	}
}

func shuffled(spec opsv1.OperationalContractSpec, r *rand.Rand) opsv1.OperationalContractSpec {
	s := *spec.DeepCopy()
	r.Shuffle(len(s.Assertions), func(i, j int) { s.Assertions[i], s.Assertions[j] = s.Assertions[j], s.Assertions[i] })
	r.Shuffle(len(s.Capabilities), func(i, j int) { s.Capabilities[i], s.Capabilities[j] = s.Capabilities[j], s.Capabilities[i] })
	r.Shuffle(len(s.Envelopes), func(i, j int) { s.Envelopes[i], s.Envelopes[j] = s.Envelopes[j], s.Envelopes[i] })
	for i := range s.Assertions {
		e := s.Assertions[i].Enum
		r.Shuffle(len(e), func(a, b int) { e[a], e[b] = e[b], e[a] })
	}
	for i := range s.Capabilities {
		q := s.Capabilities[i].Requirements
		r.Shuffle(len(q), func(a, b int) { q[a], q[b] = q[b], q[a] })
	}
	for i := range s.Envelopes {
		q := s.Envelopes[i].Requirements
		r.Shuffle(len(q), func(a, b int) { q[a], q[b] = q[b], q[a] })
	}
	return s
}

func TestSpecDigestIsDeterministicAndSemantic(t *testing.T) {
	base := serviceSpec()
	want := SpecDigest(&base)
	if !strings.HasPrefix(want, "sha256:") {
		t.Fatalf("digest %q", want)
	}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 100; i++ {
		s := shuffled(base, r)
		if got := SpecDigest(&s); got != want {
			t.Fatalf("reordering changed digest: %s vs %s", got, want)
		}
	}
	respelled := serviceSpec()
	respelled.Assertions[0].MaxAge = dur(30000 * time.Millisecond)
	if SpecDigest(&respelled) != want {
		t.Fatal("equal duration spelled differently changed digest")
	}

	changes := map[string]func(*opsv1.OperationalContractSpec){
		"maxAge":              func(s *opsv1.OperationalContractSpec) { s.Assertions[0].MaxAge = dur(31 * time.Second) },
		"onUnmet":             func(s *opsv1.OperationalContractSpec) { s.Capabilities[0].Requirements[0].OnUnmet = "DEGRADED" },
		"operand":             func(s *opsv1.OperationalContractSpec) { s.Envelopes[0].Requirements[0].Operand.Integer = ptr[int64](3) },
		"capability revision": func(s *opsv1.OperationalContractSpec) { s.Capabilities[0].Type.Revision = "v2" },
		"capability domain":   func(s *opsv1.OperationalContractSpec) { s.Capabilities[0].Type.Domain = "other.example" },
		"enum value":          func(s *opsv1.OperationalContractSpec) { s.Assertions[2].Enum = []string{"primary"} },
		"envelope class":      func(s *opsv1.OperationalContractSpec) { s.Envelopes[1].Class = "Minimum" },
	}
	for name, mut := range changes {
		s := serviceSpec()
		mut(&s)
		if SpecDigest(&s) == want {
			t.Errorf("semantic change %q did not change digest", name)
		}
	}
}

func TestCapabilityTypeRoundTrip(t *testing.T) {
	c := contract("a", "svc", "c", serviceSpec())
	st := func() opsv1.OperationalTargetStatus {
		w := &world{contracts: []opsv1.OperationalContract{c},
			targets: []opsv1.OperationalTarget{target("a", "svc", "t1", "svc", bind("api-serving", "generic-http-probe"))}}
		return w.status("svc")
	}()
	for _, cs := range st.Capabilities {
		if cs.Type.Domain != domain || cs.Type.Revision != "v1" {
			t.Fatalf("capability type not round-tripped: %+v", cs.Type)
		}
	}
}

// ── target wire ─────────────────────────────────────────────────────────────

func TestContractRefIsSameNamespaceOnly(t *testing.T) {
	if n := reflect.TypeOf(opsv1.LocalContractReference{}).NumField(); n != 1 {
		t.Fatalf("contractRef has %d fields; v1alpha1 allows only a same-namespace name", n)
	}
	w := pipeline()
	// A contract of the same name in another namespace is never used.
	other := contract("b", "worker-v1", "c-other", workerSpec())
	w.contracts = []opsv1.OperationalContract{w.contracts[0], other}
	st := w.status("worker")
	if st.Valid {
		t.Fatal("target bound to a contract in another namespace")
	}
	expectReason(t, st.InvalidReasons, operations.ReasonContractUnresolved)
}

func TestSameNamespaceContractIdentityIsExact(t *testing.T) {
	w := pipeline()
	st := w.status("worker")
	c := w.contracts[1]
	if st.Identity.ContractUID != "c-wrk" || st.Identity.ContractSpecDigest != SpecDigest(&c.Spec) ||
		st.Identity.TargetUID != "t-wrk" || st.Identity.ResolvedUID != "workload-t-wrk" {
		t.Fatalf("identity %+v", st.Identity)
	}
}

func TestTargetUIDReplacementInvalidatesEvidence(t *testing.T) {
	w := pipeline()
	w.targets[1].UID = "t-wrk-recreated"
	w.resolved = nil
	st := w.status("worker")
	c := expectState(t, st, "observe", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonEvidenceTargetReplaced)
}

func TestResolvedUIDReplacementInvalidatesEvidence(t *testing.T) {
	w := pipeline()
	w.resolveAll()
	w.resolved[types.NamespacedName{Namespace: "a", Name: "worker"}] = Resolution{UID: "workload-new"}
	st := w.status("worker")
	c := expectState(t, st, "observe", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonEvidenceTargetReplaced)
}

func TestProviderConfigRevisionChangeInvalidatesEvidence(t *testing.T) {
	w := pipeline()
	w.targets[1].Spec.AssertionBindings[1].Provider.ConfigRevision = "r2"
	st := w.status("worker")
	c := expectState(t, st, "observe", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonEvidenceProviderChanged)
	expectState(t, st, "submit", operations.Available)
}

func TestProviderNamespaceIsPartOfIdentity(t *testing.T) {
	w := pipeline()
	w.targets[1].Spec.AssertionBindings[1].Provider.Namespace = "a" // explicit, same identity
	expectState(t, w.status("worker"), "observe", operations.Available)
}

func TestDependencyExactContractIdentityPreserved(t *testing.T) {
	w := pipeline()
	w.resolveAll()
	b := Build(w.input())
	var got operations.DependencyBinding
	for _, ot := range b.Snapshot.Targets {
		if ot.Identity.UID == "t-wrk" {
			got = ot.DependencyBindings[0]
		}
	}
	rc := w.contracts[0]
	want := operations.DependencyBinding{Slot: "resolver", TargetUID: "t-res", Contract: ContractIdentity(&rc)}
	if got != want {
		t.Fatalf("dependency binding %+v, want %+v", got, want)
	}
}

func TestDependencyContractRevisionIsNotSilentlyFollowed(t *testing.T) {
	w := pipeline()
	// The resolver's contract is replaced: same name, new UID and spec.
	spec := resolverSpec()
	spec.Assertions[0].MaxAge = dur(time.Minute)
	w.contracts[0] = contract("a", "resolver-v1", "c-res-2", spec)
	w.observations[0] = w.observe(w.targets[0], "up", operations.Bool(true), fresh)
	w.observations[1] = w.observe(w.targets[0], "fast", operations.Bool(true), fresh)
	expectState(t, w.status("resolver"), "resolve", operations.Available)
	st := w.status("worker")
	c := expectState(t, st, "submit", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonDependencyContractMismatch)
	expectState(t, st, "observe", operations.Available)
}

func TestDependencyTargetRecreationIsNotSilentlyFollowed(t *testing.T) {
	w := pipeline()
	w.targets[0].UID = "t-res-2"
	w.resolved = nil
	c := expectState(t, w.status("worker"), "submit", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonDependencyTargetMissing)
}

func TestEnvelopeSelectionDoesNotChangeTruth(t *testing.T) {
	c := contract("a", "svc", "c", serviceSpec())
	tg := target("a", "svc", "t1", "svc",
		bind("api-serving", "generic-http-probe"), bind("available-replicas", "kubernetes-workload-status"), bind("role", "kubernetes-workload-status"))
	results := map[string]opsv1.OperationalTargetStatus{}
	for _, sel := range []string{"", "minimum", "recommended"} {
		tt := *tg.DeepCopy()
		tt.Spec.EnvelopeSelection = sel
		w := &world{contracts: []opsv1.OperationalContract{c}, targets: []opsv1.OperationalTarget{tt}}
		w.observations = []operations.Observation{
			w.observe(tt, "api-serving", operations.Bool(true), fresh),
			w.observe(tt, "available-replicas", operations.Int(1), fresh),
			w.observe(tt, "role", operations.String("primary"), fresh),
		}
		results[sel] = w.status("svc")
	}
	for sel, st := range results {
		if !reflect.DeepEqual(st.Capabilities, results[""].Capabilities) || !reflect.DeepEqual(st.Envelopes, results[""].Envelopes) {
			t.Fatalf("envelopeSelection %q changed results", sel)
		}
		if st.EnvelopeSelection != sel {
			t.Fatalf("selection not projected: %q", st.EnvelopeSelection)
		}
	}
	expectState(t, results["recommended"], "redundant", operations.Degraded)
}

func TestUnresolvedTargetRefIsInvalidAndDependentsFailClosed(t *testing.T) {
	for _, failure := range []string{ReasonTargetKindUnsupported, ReasonTargetNotFound} {
		w := pipeline()
		w.resolved = map[types.NamespacedName]Resolution{{Namespace: "a", Name: "resolver"}: {Failure: failure}}
		st := w.status("resolver")
		if st.Valid || len(st.InvalidReasons) != 1 || st.InvalidReasons[0].Code != failure {
			t.Fatalf("%s: status %+v", failure, st)
		}
		c := expectState(t, w.status("worker"), "submit", operations.Unknown)
		expectReason(t, c.Reasons, operations.ReasonDependencyTargetMissing)
		expectState(t, w.status("worker"), "observe", operations.Available)
	}
}

// ── ReferenceGrant ──────────────────────────────────────────────────────────

// crossNS moves the resolver (and its contract) into namespace "b".
func crossNS() *world {
	w := pipeline()
	w.contracts[0].Namespace = "b"
	w.targets[0].Namespace = "b"
	r, rc := w.targets[0], w.contracts[0]
	w.targets[1].Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("resolver", r, rc)}
	w.observations[0] = w.observe(r, "up", operations.Bool(true), fresh)
	w.observations[1] = w.observe(r, "fast", operations.Bool(true), fresh)
	return w
}

func depGrant(fromNS, name string) opsv1.OperationalReferenceGrant {
	return grant("b", fromNS, opsv1.ReferenceGrantTo{Type: opsv1.ReferenceDependency, Name: name})
}

func TestSameNamespaceDependencyNeedsNoGrant(t *testing.T) {
	st := pipeline().status("worker")
	expectState(t, st, "submit", operations.Available)
	if len(st.DeniedReferences) != 0 {
		t.Fatalf("denied %+v", st.DeniedReferences)
	}
}

func TestCrossNamespaceDependencyWithExactGrant(t *testing.T) {
	w := crossNS()
	w.grants = []opsv1.OperationalReferenceGrant{depGrant("a", "resolver")}
	st := w.status("worker")
	expectState(t, st, "submit", operations.Available)
	if len(st.DeniedReferences) != 0 {
		t.Fatalf("denied %+v", st.DeniedReferences)
	}
}

func TestCrossNamespaceDependencyDeniedFailsClosed(t *testing.T) {
	cases := map[string][]opsv1.OperationalReferenceGrant{
		"no grant":                    nil,
		"wrong type (provider grant)": {grant("b", "a", opsv1.ReferenceGrantTo{Type: opsv1.ReferenceEvidenceProvider, Name: "resolver"})},
		"wrong from namespace":        {depGrant("c", "resolver")},
		"wrong name":                  {depGrant("a", "other")},
		"grant in wrong namespace":    {func() opsv1.OperationalReferenceGrant { g := depGrant("a", "resolver"); g.Namespace = "a"; return g }()},
		"wildcard from namespace":     {depGrant("*", "resolver")},
		"wildcard name":               {depGrant("a", "*")},
		"wrong from kind": {func() opsv1.OperationalReferenceGrant {
			g := depGrant("a", "resolver")
			g.Spec.From[0].Kind = "Pod"
			return g
		}()},
		"unsupported type Action": {grant("b", "a", opsv1.ReferenceGrantTo{Type: "Action", Name: "resolver"})},
		"unsupported type Secret": {grant("b", "a", opsv1.ReferenceGrantTo{Type: "Secret", Name: "resolver"})},
	}
	for name, grants := range cases {
		t.Run(name, func(t *testing.T) {
			w := crossNS()
			w.grants = grants
			st := w.status("worker")
			c := expectState(t, st, "submit", operations.Unknown)
			expectReason(t, c.Reasons, operations.ReasonDependencyUnbound)
			expectState(t, st, "observe", operations.Available)
			want := []opsv1.DeniedReference{{Type: opsv1.ReferenceDependency, Slot: "resolver", Namespace: "b"}}
			if !reflect.DeepEqual(st.DeniedReferences, want) {
				t.Fatalf("denied %+v", st.DeniedReferences)
			}
		})
	}
}

func TestDeniedReferenceDoesNotRevealExistence(t *testing.T) {
	exists := crossNS()
	missing := crossNS()
	missing.targets = missing.targets[1:]
	if !reflect.DeepEqual(exists.status("worker"), missing.status("worker")) {
		t.Fatal("status differs depending on whether an ungranted referent exists")
	}
}

func TestEvidenceProviderGrantIsTypeScoped(t *testing.T) {
	setup := func(grants ...opsv1.OperationalReferenceGrant) opsv1.OperationalTargetStatus {
		w := pipeline()
		w.targets[1].Spec.AssertionBindings[1].Provider.Namespace = "b"
		w.observations[3] = w.observe(w.targets[1], "status-up", operations.Bool(true), fresh)
		w.grants = grants
		return w.status("worker")
	}
	ok := setup(grant("b", "a", opsv1.ReferenceGrantTo{Type: opsv1.ReferenceEvidenceProvider, Name: "kubernetes-workload-status"}))
	expectState(t, ok, "observe", operations.Available)

	for name, g := range map[string]opsv1.OperationalReferenceGrant{
		"dependency grant does not authorize provider": grant("b", "a", opsv1.ReferenceGrantTo{Type: opsv1.ReferenceDependency, Name: "kubernetes-workload-status"}),
		"other provider name":                          grant("b", "a", opsv1.ReferenceGrantTo{Type: opsv1.ReferenceEvidenceProvider, Name: "generic-http-probe"}),
	} {
		st := setup(g)
		c := expectState(t, st, "observe", operations.Unknown)
		expectReason(t, c.Reasons, operations.ReasonEvidenceUnbound)
		if len(st.DeniedReferences) != 1 || st.DeniedReferences[0].Type != opsv1.ReferenceEvidenceProvider {
			t.Fatalf("%s: denied %+v", name, st.DeniedReferences)
		}
	}
}

func TestPinnedUIDCannotEscapeGrantedNamespace(t *testing.T) {
	w := crossNS()
	// A target in namespace "c" whose UID the binding names, while the grant
	// covers only b/resolver: the binding must not reach it.
	w.grants = []opsv1.OperationalReferenceGrant{depGrant("a", "resolver")}
	impostor := target("c", "resolver", "t-imp", "resolver-v1")
	w.targets = append(w.targets, impostor)
	w.targets[1].Spec.DependencyBindings[0].Target.UID = "t-imp"
	c := expectState(t, w.status("worker"), "submit", operations.Unknown)
	expectReason(t, c.Reasons, operations.ReasonDependencyTargetMissing)
}

func TestCrossNamespaceDependencyStatusHidesForeignDetail(t *testing.T) {
	w := crossNS()
	w.grants = []opsv1.OperationalReferenceGrant{depGrant("a", "resolver")}
	w.targets[1].Spec.DependencyBindings[0].Contract.SpecDigest = "sha256:stale"
	st := w.status("worker")
	r := expectReason(t, expectState(t, st, "submit", operations.Unknown).Reasons, operations.ReasonDependencyContractMismatch)
	if r.Detail != "" {
		t.Fatalf("foreign contract detail exposed: %q", r.Detail)
	}
	for _, e := range st.Evidence {
		if strings.Contains(e.EvidenceRef, "t-res") {
			t.Fatalf("foreign evidence ref exposed: %+v", e)
		}
	}
}

// ── evaluator integration / conformance ─────────────────────────────────────

func TestWireEvaluationEqualsDirectO1Evaluation(t *testing.T) {
	w := pipeline()
	w.resolveAll()
	b := Build(w.input())
	rc, wc := w.contracts[0], w.contracts[1]
	direct := operations.Snapshot{
		At:        at,
		Contracts: []operations.Contract{ContractFromWire(&rc), ContractFromWire(&wc)},
		Targets: []operations.Target{
			{
				Identity: operations.TargetIdentity{Namespace: "a", Name: "resolver", UID: "t-res"}, ResolvedUID: "workload-t-res",
				ContractRef: ContractIdentity(&rc),
				AssertionBindings: []operations.AssertionBinding{
					{Slot: "up", Provider: operations.ProviderIdentity{Name: "a/generic-http-probe", ConfigRevision: "r1"}},
					{Slot: "fast", Provider: operations.ProviderIdentity{Name: "a/generic-http-probe", ConfigRevision: "r1"}},
				},
			},
			{
				Identity: operations.TargetIdentity{Namespace: "a", Name: "worker", UID: "t-wrk"}, ResolvedUID: "workload-t-wrk",
				ContractRef: ContractIdentity(&wc),
				AssertionBindings: []operations.AssertionBinding{
					{Slot: "submit-up", Provider: operations.ProviderIdentity{Name: "a/generic-http-probe", ConfigRevision: "r1"}},
					{Slot: "status-up", Provider: operations.ProviderIdentity{Name: "a/kubernetes-workload-status", ConfigRevision: "r1"}},
				},
				DependencyBindings: []operations.DependencyBinding{{Slot: "resolver", TargetUID: "t-res", Contract: ContractIdentity(&rc)}},
			},
		},
		Observations: w.observations,
	}
	if !reflect.DeepEqual(b.Snapshot, direct) {
		t.Fatalf("wire snapshot differs from direct O1 snapshot\n got %+v\nwant %+v", b.Snapshot, direct)
	}
	wire, _ := operations.Evaluate(b.Snapshot)
	want, _ := operations.Evaluate(direct)
	if !reflect.DeepEqual(wire, want) {
		t.Fatal("wire evaluation differs from direct O1 evaluation")
	}
	// The projection carries O1 states verbatim.
	for _, ta := range want.Targets {
		for i := range w.targets {
			if string(w.targets[i].UID) != ta.Target.UID {
				continue
			}
			st := Project(&w.targets[i], b, wire)
			for j, c := range ta.Capabilities {
				if st.Capabilities[j].State != string(c.State) || st.Capabilities[j].Type.Name != c.Type.Name {
					t.Fatalf("projection altered %s", c.Type)
				}
			}
		}
	}
}

func TestConformanceThroughWire(t *testing.T) {
	type check func(t *testing.T, w *world)
	cases := map[string]struct {
		mut   func(w *world)
		check check
	}{
		"stale evidence": {
			func(w *world) {
				w.observations[3] = w.observe(w.targets[1], "status-up", operations.Bool(true), time.Minute)
			},
			func(t *testing.T, w *world) {
				expectReason(t, expectState(t, w.status("worker"), "observe", operations.Unknown).Reasons, operations.ReasonEvidenceStale)
			},
		},
		"provider unavailable": {
			func(w *world) {
				o := w.observe(w.targets[1], "status-up", operations.Value{}, fresh)
				o.Outcome = operations.OutcomeProviderUnavailable
				w.observations[3] = o
			},
			func(t *testing.T, w *world) {
				expectReason(t, expectState(t, w.status("worker"), "observe", operations.Unknown).Reasons, operations.ReasonProviderUnavailable)
			},
		},
		"conflicting evidence": {
			func(w *world) {
				w.observations = append(w.observations, w.observe(w.targets[1], "status-up", operations.Bool(false), fresh))
			},
			func(t *testing.T, w *world) {
				expectReason(t, expectState(t, w.status("worker"), "observe", operations.Unknown).Reasons, operations.ReasonConflictingEvidence)
			},
		},
		"contract recreated": {
			func(w *world) { w.contracts[1].UID = "c-wrk-2" },
			func(t *testing.T, w *world) {
				expectReason(t, expectState(t, w.status("worker"), "observe", operations.Unknown).Reasons, operations.ReasonEvidenceContractChanged)
			},
		},
		"DEGRADED dependency uses dependent's own onUnmet": {
			func(w *world) { w.observations[1] = w.observe(w.targets[0], "fast", operations.Bool(false), fresh) },
			func(t *testing.T, w *world) {
				expectState(t, w.status("resolver"), "resolve", operations.Degraded)
				expectReason(t, expectState(t, w.status("worker"), "submit", operations.Unavailable).Reasons, operations.ReasonDependencyUnmet)
				expectState(t, w.status("worker"), "observe", operations.Available)
			},
		},
		"conflicting impact": {
			func(w *world) {
				w.observations[0] = w.observe(w.targets[0], "up", operations.Bool(false), fresh)
				w.observations[1] = w.observe(w.targets[0], "fast", operations.Bool(false), fresh)
			},
			func(t *testing.T, w *world) {
				expectReason(t, expectState(t, w.status("resolver"), "resolve", operations.Unknown).Reasons, operations.ReasonConflictingImpact)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := pipeline()
			tc.mut(w)
			tc.check(t, w)
		})
	}
}

func TestCrossTargetCycleThroughWireWithoutContamination(t *testing.T) {
	peer := opsv1.OperationalContractSpec{
		Assertions:      []opsv1.AssertionSlot{boolSlot("up")},
		DependencySlots: []opsv1.DependencySlot{{Name: "peer"}},
		Capabilities: []opsv1.Capability{{Type: capT("replicate"), Requirements: []opsv1.Requirement{
			isTrue("up", "UNAVAILABLE"), depReq("peer", "replicate", "DEGRADED"),
		}}},
	}
	pc := contract("a", "peer-v1", "c-peer", peer)
	x := target("a", "x", "t-x", "peer-v1", bind("up", "kubernetes-workload-status"))
	y := target("a", "y", "t-y", "peer-v1", bind("up", "kubernetes-workload-status"))
	x.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("peer", y, pc)}
	y.Spec.DependencyBindings = []opsv1.DependencyBinding{bindDep("peer", x, pc)}
	w := pipeline()
	w.contracts = append(w.contracts, pc)
	w.targets = append(w.targets, x, y)
	for _, n := range []string{"x", "y"} {
		st := w.status(n)
		if st.Valid {
			t.Fatalf("%s on a cycle is valid", n)
		}
		expectReason(t, st.InvalidReasons, operations.ReasonDependencyCycle)
	}
	expectState(t, w.status("worker"), "submit", operations.Available)
	expectState(t, w.status("resolver"), "resolve", operations.Available)
}

func TestUnrelatedTargetNonContamination(t *testing.T) {
	w := pipeline()
	w.observations[0] = w.observe(w.targets[0], "up", operations.Bool(false), fresh)
	expectState(t, w.status("resolver"), "resolve", operations.Unavailable)
	st := w.status("worker")
	expectState(t, st, "submit", operations.Unavailable)
	o := expectState(t, st, "observe", operations.Available)
	if len(o.Reasons) != 0 {
		t.Fatalf("unrelated capability carries reasons %+v", o.Reasons)
	}
}

func TestSyncIsNotApplicable(t *testing.T) {
	if st := pipeline().status("worker"); st.Sync != "NotApplicable" {
		t.Fatalf("sync %q", st.Sync)
	}
}

// ── determinism / status write semantics ────────────────────────────────────

func TestProjectionIsOrderIndependent(t *testing.T) {
	want := map[string]opsv1.OperationalTargetStatus{}
	base := crossNS()
	base.grants = []opsv1.OperationalReferenceGrant{depGrant("a", "resolver"), depGrant("c", "x")}
	for _, n := range []string{"resolver", "worker"} {
		want[n] = base.status(n)
	}
	r := rand.New(rand.NewSource(9))
	for i := 0; i < 100; i++ {
		w := crossNS()
		w.grants = []opsv1.OperationalReferenceGrant{depGrant("a", "resolver"), depGrant("c", "x")}
		r.Shuffle(len(w.contracts), func(a, b int) { w.contracts[a], w.contracts[b] = w.contracts[b], w.contracts[a] })
		r.Shuffle(len(w.targets), func(a, b int) { w.targets[a], w.targets[b] = w.targets[b], w.targets[a] })
		r.Shuffle(len(w.grants), func(a, b int) { w.grants[a], w.grants[b] = w.grants[b], w.grants[a] })
		r.Shuffle(len(w.observations), func(a, b int) { w.observations[a], w.observations[b] = w.observations[b], w.observations[a] })
		for j := range w.contracts {
			w.contracts[j].Spec = shuffled(w.contracts[j].Spec, r)
		}
		for j := range w.targets {
			ab := w.targets[j].Spec.AssertionBindings
			r.Shuffle(len(ab), func(a, b int) { ab[a], ab[b] = ab[b], ab[a] })
		}
		for n, st := range want {
			if got := w.status(n); !reflect.DeepEqual(got, st) {
				t.Fatalf("iteration %d: %s status depends on input order", i, n)
			}
		}
	}
}

func TestApplyWritesOnlyOnSemanticChange(t *testing.T) {
	w := pipeline()
	desired := w.status("worker")

	first, changed := Apply(opsv1.OperationalTargetStatus{}, desired, at)
	if !changed || first.AssessedAt == nil || !first.AssessedAt.Time.Equal(at) {
		t.Fatalf("first apply: changed=%v %+v", changed, first.AssessedAt)
	}
	ltt := first.Conditions[0].LastTransitionTime

	// Same semantic input later: no write, nothing moves.
	for i := 1; i <= 5; i++ {
		later := at.Add(time.Duration(i) * time.Minute)
		if _, changed := Apply(first, w.status("worker"), later); changed {
			t.Fatalf("repeat %d: equal evaluation reported a change", i)
		}
	}

	// Real capability transition, condition status unchanged: write, LTT kept.
	w.observations[3] = w.observe(w.targets[1], "status-up", operations.Bool(false), fresh)
	second, changed := Apply(first, w.status("worker"), at.Add(time.Hour))
	if !changed || !second.AssessedAt.Time.Equal(at.Add(time.Hour)) {
		t.Fatal("capability transition not written")
	}
	if !second.Conditions[0].LastTransitionTime.Equal(&ltt) {
		t.Fatal("LastTransitionTime moved without a condition transition")
	}

	// Evidence reference change alone is a semantic change.
	o := w.observe(w.targets[1], "status-up", operations.Bool(false), fresh)
	o.EvidenceRef = "ref:new"
	w.observations[3] = o
	third, changed := Apply(second, w.status("worker"), at.Add(2*time.Hour))
	if !changed {
		t.Fatal("evidence reference change not written")
	}

	// Condition transition (target becomes invalid): LTT moves.
	w.resolved = map[types.NamespacedName]Resolution{{Namespace: "a", Name: "worker"}: {Failure: ReasonTargetNotFound}}
	fourth, changed := Apply(third, w.status("worker"), at.Add(3*time.Hour))
	if !changed || !fourth.Conditions[0].LastTransitionTime.Time.Equal(at.Add(3*time.Hour)) || fourth.Conditions[0].Status != "False" {
		t.Fatalf("condition transition: %+v", fourth.Conditions)
	}
}
