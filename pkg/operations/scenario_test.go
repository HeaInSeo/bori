package operations

import (
	"strings"
	"testing"
	"time"
)

func mustEval(t *testing.T, s Snapshot) Assessment {
	t.Helper()
	a, err := Evaluate(s)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return a
}

func targetOf(t *testing.T, a Assessment, uid string) TargetAssessment {
	t.Helper()
	ta, ok := a.Target(uid)
	if !ok {
		t.Fatalf("target %s not assessed", uid)
	}
	return ta
}

func capOf(t *testing.T, a Assessment, uid, name string) CapabilityResult {
	t.Helper()
	ta := targetOf(t, a, uid)
	if !ta.Valid {
		t.Fatalf("target %s invalid: %+v", uid, ta.InvalidReasons)
	}
	c, ok := ta.Capability(name)
	if !ok {
		t.Fatalf("capability %s/%s not assessed", uid, name)
	}
	return c
}

func expectCap(t *testing.T, a Assessment, uid, name string, want CapabilityState) CapabilityResult {
	t.Helper()
	c := capOf(t, a, uid, name)
	if c.State != want {
		t.Fatalf("%s/%s = %s, want %s (reasons %+v)", uid, name, c.State, want, c.Reasons)
	}
	return c
}

func hasReason(rs []Reason, code ReasonCode, subject string) bool {
	for _, r := range rs {
		if r.Code == code && strings.Contains(r.Subject, subject) {
			return true
		}
	}
	return false
}

func expectReason(t *testing.T, rs []Reason, code ReasonCode, subject string) {
	t.Helper()
	if !hasReason(rs, code, subject) {
		t.Fatalf("missing reason %s on %q in %+v", code, subject, rs)
	}
}

func expectNoSubject(t *testing.T, rs []Reason, subject string) {
	t.Helper()
	for _, r := range rs {
		if strings.Contains(r.Subject, subject) || strings.Contains(r.Detail, subject) {
			t.Fatalf("reason %+v wrongly attributed to %q", r, subject)
		}
	}
}

func replaceObs(s *Snapshot, slot string, o Observation) {
	for i := range s.Observations {
		if s.Observations[i].Key.Slot == slot {
			s.Observations[i] = o
			return
		}
	}
	s.Observations = append(s.Observations, o)
}

// ── S01 single target local failure ─────────────────────────────────────────

func TestS01_LocalAssertionFailureAffectsOnlyReferencingCapabilities(t *testing.T) {
	a := mustEval(t, httpServiceSnapshot(false, true, true))
	c := expectCap(t, a, "t-web", "serve-requests", Unavailable)
	expectReason(t, c.Reasons, ReasonPredicateUnmet, "assertion:api-serving")
	expectCap(t, a, "t-web", "serve-within-slo", Unavailable)
	m := expectCap(t, a, "t-web", "export-metrics", Available)
	if len(m.Reasons) != 0 {
		t.Fatalf("unaffected capability carries reasons: %+v", m.Reasons)
	}
}

func TestS01_DeclaredImpactNotUniversal(t *testing.T) {
	a := mustEval(t, httpServiceSnapshot(true, false, true))
	c := expectCap(t, a, "t-web", "serve-within-slo", Degraded)
	expectReason(t, c.Reasons, ReasonPredicateUnmet, "assertion:latency-within-slo")
	expectCap(t, a, "t-web", "serve-requests", Available)
}

func TestAllHealthy(t *testing.T) {
	a := mustEval(t, httpServiceSnapshot(true, true, true))
	for _, n := range []string{"serve-requests", "serve-within-slo", "export-metrics"} {
		expectCap(t, a, "t-web", n, Available)
	}
	if ta := targetOf(t, a, "t-web"); ta.Sync != SyncNotApplicable {
		t.Fatalf("Sync = %q, want NotApplicable", ta.Sync)
	}
}

// ── S03 dependency impact and non-contamination ─────────────────────────────

func TestS03_DependencyFailureAffectsOnlyReferencingCapability(t *testing.T) {
	a := mustEval(t, pipelineSnapshot(false))
	expectCap(t, a, "t-resolver", "resolve-artifact", Unavailable)
	s := expectCap(t, a, "t-worker", "submit-work", Unavailable)
	expectReason(t, s.Reasons, ReasonDependencyUnmet, "t-resolver")
	o := expectCap(t, a, "t-worker", "observe-running-work", Available)
	expectNoSubject(t, o.Reasons, "t-resolver")
	// Transitive: declared impact of the producer's own requirement applies.
	p := expectCap(t, a, "t-producer", "produce-work", Degraded)
	expectReason(t, p.Reasons, ReasonDependencyUnmet, "t-worker")
}

func TestS03b_UnrelatedCapabilityOwnStaleEvidenceIsUnknownAttributedToItself(t *testing.T) {
	s := pipelineSnapshot(false)
	w := pipelineTargets().worker
	replaceObs(&s, "status-api-serving", observe(w, "status-api-serving", Bool(true), 45*time.Second))
	a := mustEval(t, s)
	o := expectCap(t, a, "t-worker", "observe-running-work", Unknown)
	expectReason(t, o.Reasons, ReasonEvidenceStale, "assertion:status-api-serving")
	expectNoSubject(t, o.Reasons, "t-resolver")
	expectNoSubject(t, o.Reasons, "resolve-artifact")
}

func TestS03_HealthyDependencyYieldsAvailable(t *testing.T) {
	a := mustEval(t, pipelineSnapshot(true))
	expectCap(t, a, "t-worker", "submit-work", Available)
	expectCap(t, a, "t-producer", "produce-work", Available)
}

func TestDependencyUnknownPropagatesAsUnknown(t *testing.T) {
	s := pipelineSnapshot(true)
	r := pipelineTargets().resolver
	replaceObs(&s, "resolve-api-serving", observe(r, "resolve-api-serving", Bool(true), time.Minute))
	a := mustEval(t, s)
	c := expectCap(t, a, "t-worker", "submit-work", Unknown)
	expectReason(t, c.Reasons, ReasonDependencyUnknown, "t-resolver")
	expectCap(t, a, "t-worker", "observe-running-work", Available)
	expectCap(t, a, "t-producer", "produce-work", Unknown)
}

// ── S04 provider outage ─────────────────────────────────────────────────────

func TestS04_ProviderOutageIsUnknownNotUnavailable(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	tg := httpServiceTarget()
	replaceObs(&s, "api-serving", providerDown(tg, "api-serving", fresh))
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonProviderUnavailable, "assertion:api-serving")
	expectCap(t, a, "t-web", "export-metrics", Available)
}

func TestS04_MissingEvidenceIsUnknown(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Observations = s.Observations[1:] // drop api-serving
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceMissing, "assertion:api-serving")
}

// ── S05 stale evidence ──────────────────────────────────────────────────────

func TestS05_PastHealthyBeyondMaxAgeIsUnknown(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	replaceObs(&s, "api-serving", observe(httpServiceTarget(), "api-serving", Bool(true), 31*time.Second))
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceStale, "assertion:api-serving")
}

func TestS05_ProviderValidUntilIsHonoured(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	o := observe(httpServiceTarget(), "api-serving", Bool(true), fresh)
	o.ValidUntil = at.Add(-time.Second)
	replaceObs(&s, "api-serving", o)
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Unknown)
}

func TestS05_StaleRedAfterFreshRecoveryDoesNotOverwrite(t *testing.T) {
	tg := httpServiceTarget()
	for name, red := range map[string]Observation{
		"late-arriving older red within maxAge": observe(tg, "api-serving", Bool(false), 20*time.Second),
		"expired red":                           observe(tg, "api-serving", Bool(false), 2*time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			s := httpServiceSnapshot(true, true, true)
			// Fresh recovery first, the old red delivered afterwards.
			s.Observations = append(s.Observations, red)
			a := mustEval(t, s)
			expectCap(t, a, "t-web", "serve-requests", Available)
		})
	}
}

func TestS05_NewestStaleDoesNotFallBackToOlderValue(t *testing.T) {
	tg := httpServiceTarget()
	s := httpServiceSnapshot(true, true, true)
	newest := observe(tg, "api-serving", Bool(true), 10*time.Second)
	newest.ValidUntil = at.Add(-time.Second)
	replaceObs(&s, "api-serving", observe(tg, "api-serving", Bool(true), 20*time.Second))
	s.Observations = append(s.Observations, newest)
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Unknown)
}

func TestFutureObservationIsNotUsed(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	replaceObs(&s, "api-serving", observe(httpServiceTarget(), "api-serving", Bool(true), -10*time.Second))
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Unknown)
}

// ── S06 conflicting authoritative evidence ──────────────────────────────────

func TestS06_ConflictingAuthoritativeEvidenceIsUnknown(t *testing.T) {
	tg := httpServiceTarget()
	for name, second := range map[string]Observation{
		"differing values":              observe(tg, "api-serving", Bool(false), fresh),
		"value vs provider-unavailable": providerDown(tg, "api-serving", fresh),
	} {
		t.Run(name, func(t *testing.T) {
			s := httpServiceSnapshot(true, true, true)
			s.Observations = append(s.Observations, second)
			a := mustEval(t, s)
			c := expectCap(t, a, "t-web", "serve-requests", Unknown)
			expectReason(t, c.Reasons, ReasonConflictingEvidence, "assertion:api-serving")
			expectCap(t, a, "t-web", "export-metrics", Available)
		})
	}
}

func TestS06_IdenticalDuplicateObservationIsNotConflict(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Observations = append(s.Observations, observe(httpServiceTarget(), "api-serving", Bool(true), fresh))
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Available)
}

func TestUnboundProviderEvidenceIsNotAuthoritative(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	rogue := observe(httpServiceTarget(), "api-serving", Bool(false), time.Second)
	rogue.Key.Provider = ProviderIdentity{Name: "some-other-probe", ConfigRevision: "r1"}
	s.Observations = append(s.Observations, rogue)
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Available)
}

func TestConflictingImpactIsUnknown(t *testing.T) {
	c := httpServiceContract()
	c.Capabilities = append(c.Capabilities, Capability{Type: capType("mixed"), Requirements: []Requirement{
		isTrue("api-serving", ImpactUnavailable),
		isTrue("latency-within-slo", ImpactDegraded),
	}})
	s := httpServiceSnapshot(false, false, true)
	s.Contracts = []Contract{c}
	a := mustEval(t, s)
	r := expectCap(t, a, "t-web", "mixed", Unknown)
	expectReason(t, r.Reasons, ReasonConflictingImpact, "")
	// Same declared impact from several unmet requirements is not a conflict.
	expectCap(t, a, "t-web", "serve-requests", Unavailable)
}

func TestUnknownRequirementWinsOverProvenUnmetWithoutGuessing(t *testing.T) {
	s := httpServiceSnapshot(true, false, true)
	replaceObs(&s, "api-serving", observe(httpServiceTarget(), "api-serving", Bool(true), time.Minute))
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-within-slo", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceStale, "assertion:api-serving")
	expectReason(t, c.Reasons, ReasonPredicateUnmet, "assertion:latency-within-slo")
}

func TestEvidenceTypeMismatchIsUnknownWithoutCoercion(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	replaceObs(&s, "api-serving", observe(httpServiceTarget(), "api-serving", Int(1), fresh))
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceTypeMismatch, "assertion:api-serving")

	sw := singleWriterSnapshot(1, "leader") // not a declared enum value
	a = mustEval(t, sw)
	c = expectCap(t, a, "t-db", "accept-writes", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceTypeMismatch, "assertion:role")
}

// ── S17 target replacement ──────────────────────────────────────────────────

func TestS17_TargetRecreatedWithSameNameInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Targets[0].Identity.UID = "t-web-2" // same namespace/name, new instance
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web-2", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceTargetReplaced, "assertion:api-serving")
}

func TestS17_ResolvedWorkloadReplacedInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Targets[0].ResolvedUID = "workload-new"
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceTargetReplaced, "assertion:api-serving")
}

func TestS17b_DependencyRecreatedWithDifferentContractIsNotSilentlyRebound(t *testing.T) {
	// Replacement resolver: new instance, new contract that also has a
	// capability displayed as "resolve-artifact" but a different qualified type.
	other := resolverContract()
	other.Identity = contractID("resolver-v1", "c-resolver-2", "sha256:res2")
	other.Capabilities[0].Type = CapabilityType{Domain: "other.example", Name: "resolve-artifact", Revision: "v1"}
	p := pipelineTargets()
	newResolver := target("resolver", "t-resolver-2", other.Identity, bind("resolve-api-serving", httpProbe))

	base := func() Snapshot {
		return Snapshot{
			At:        at,
			Contracts: []Contract{other, workerContract(), producerContract()},
			Targets:   []Target{newResolver, p.worker, p.producer},
			Observations: []Observation{
				observe(newResolver, "resolve-api-serving", Bool(true), fresh),
				observe(p.worker, "submit-api-serving", Bool(true), fresh),
				observe(p.worker, "status-api-serving", Bool(true), fresh),
				observe(p.producer, "producer-up", Bool(true), fresh),
			},
		}
	}

	cases := []struct {
		name    string
		binding DependencyBinding
		code    ReasonCode
	}{
		{"binding still pins old instance", bindDep("resolver", p.resolver), ReasonDependencyTargetMissing},
		{"new instance but old contract identity", DependencyBinding{Slot: "resolver", TargetUID: "t-resolver-2", Contract: resolverContract().Identity}, ReasonDependencyContractMismatch},
		{"rebound to new contract without the qualified capability", bindDep("resolver", newResolver), ReasonDependencyCapabilityAbsent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			s.Targets[1].DependencyBindings = []DependencyBinding{tc.binding}
			a := mustEval(t, s)
			expectCap(t, a, "t-resolver-2", "resolve-artifact", Available)
			c := expectCap(t, a, "t-worker", "submit-work", Unknown)
			expectReason(t, c.Reasons, tc.code, "resolver")
			expectCap(t, a, "t-worker", "observe-running-work", Available)
		})
	}
}

func TestS19_DependencyContractRevisedWithSameCapabilityIsNotSilentlyRebound(t *testing.T) {
	s := pipelineSnapshot(true)
	revised := resolverContract()
	revised.Identity.SpecDigest = "sha256:res2" // same qualified capability type
	s.Contracts[0] = revised
	s.Targets[0].ContractRef = revised.Identity
	// Fresh evidence under the revised contract: the resolver itself is fine.
	s.Observations[0] = observe(s.Targets[0], "resolve-api-serving", Bool(true), fresh)
	a := mustEval(t, s)
	expectCap(t, a, "t-resolver", "resolve-artifact", Available)
	// The worker is still pinned to the old digest and must not follow.
	c := expectCap(t, a, "t-worker", "submit-work", Unknown)
	expectReason(t, c.Reasons, ReasonDependencyContractMismatch, "t-resolver")
	expectCap(t, a, "t-worker", "observe-running-work", Available)
}

// ── S18 cycles ──────────────────────────────────────────────────────────────

func TestS18_LocalCapabilityCycleIsInvalidContract(t *testing.T) {
	c := httpServiceContract()
	c.Capabilities = append(c.Capabilities,
		Capability{Type: capType("x"), Requirements: []Requirement{{LocalCapability: "y", OnUnmet: ImpactUnavailable}}},
		Capability{Type: capType("y"), Requirements: []Requirement{{LocalCapability: "x", OnUnmet: ImpactUnavailable}}},
	)
	if errs := c.Validate(); !containsErr(errs, "cycle") {
		t.Fatalf("Validate did not reject local cycle: %v", errs)
	}
	s := httpServiceSnapshot(true, true, true)
	s.Contracts = []Contract{c}
	a := mustEval(t, s)
	ta := targetOf(t, a, "t-web")
	if ta.Valid || len(ta.Capabilities) != 0 {
		t.Fatalf("cyclic contract target assessed: %+v", ta)
	}
	expectReason(t, ta.InvalidReasons, ReasonInvalidContract, "")
}

func TestS18_SelfLoopIsInvalidContract(t *testing.T) {
	c := httpServiceContract()
	c.Capabilities = append(c.Capabilities,
		Capability{Type: capType("self"), Requirements: []Requirement{{LocalCapability: "self", OnUnmet: ImpactDegraded}}})
	if errs := c.Validate(); !containsErr(errs, "cycle") {
		t.Fatalf("Validate did not reject self loop: %v", errs)
	}
}

func peerContract() Contract {
	return Contract{
		Identity:        contractID("peer-v1", "c-peer", "sha256:peer1"),
		Assertions:      []AssertionSlot{boolSlot("up")},
		DependencySlots: []DependencySlot{{Name: "peer"}},
		Capabilities: []Capability{{Type: capType("replicate"), Requirements: []Requirement{
			isTrue("up", ImpactUnavailable),
			dep("peer", "replicate", ImpactDegraded),
		}}},
	}
}

func TestS18_CrossTargetCycleRejectedWithoutContamination(t *testing.T) {
	pc := peerContract()
	a1 := target("peer-a", "t-a", pc.Identity, bind("up", workloadStat))
	b1 := target("peer-b", "t-b", pc.Identity, bind("up", workloadStat))
	a1.DependencyBindings = []DependencyBinding{bindDep("peer", b1)}
	b1.DependencyBindings = []DependencyBinding{bindDep("peer", a1)}

	web := httpServiceTarget()
	// A consumer of the cyclic set.
	cons := Contract{
		Identity:        contractID("consumer-v1", "c-cons", "sha256:cons1"),
		Assertions:      []AssertionSlot{boolSlot("up")},
		DependencySlots: []DependencySlot{{Name: "src"}},
		Capabilities: []Capability{
			{Type: capType("consume"), Requirements: []Requirement{dep("src", "replicate", ImpactUnavailable)}},
			{Type: capType("idle"), Requirements: []Requirement{isTrue("up", ImpactUnavailable)}},
		},
	}
	ct := target("consumer", "t-cons", cons.Identity, bind("up", workloadStat))
	ct.DependencyBindings = []DependencyBinding{bindDep("src", a1)}

	s := httpServiceSnapshot(true, true, true)
	s.Contracts = append(s.Contracts, pc, cons)
	s.Targets = append(s.Targets, a1, b1, ct)
	s.Observations = append(s.Observations,
		observe(a1, "up", Bool(true), fresh), observe(b1, "up", Bool(true), fresh), observe(ct, "up", Bool(true), fresh))
	a := mustEval(t, s)

	for _, uid := range []string{"t-a", "t-b"} {
		ta := targetOf(t, a, uid)
		if ta.Valid {
			t.Fatalf("%s in cycle assessed as valid: %+v", uid, ta)
		}
		expectReason(t, ta.InvalidReasons, ReasonDependencyCycle, "")
	}
	c := expectCap(t, a, "t-cons", "consume", Unknown)
	expectReason(t, c.Reasons, ReasonDependencyTargetInvalid, "t-a")
	expectCap(t, a, "t-cons", "idle", Available)
	expectCap(t, a, web.Identity.UID, "serve-requests", Available)
}

func TestS18_CrossContractCycleRejected(t *testing.T) {
	left := Contract{
		Identity:        contractID("left-v1", "c-left", "sha256:l1"),
		Assertions:      []AssertionSlot{boolSlot("up")},
		DependencySlots: []DependencySlot{{Name: "right"}},
		Capabilities:    []Capability{{Type: capType("l"), Requirements: []Requirement{dep("right", "r", ImpactUnavailable)}}},
	}
	right := Contract{
		Identity:        contractID("right-v1", "c-right", "sha256:r1"),
		Assertions:      []AssertionSlot{boolSlot("up")},
		DependencySlots: []DependencySlot{{Name: "left"}},
		Capabilities: []Capability{
			{Type: capType("r"), Requirements: []Requirement{{LocalCapability: "r2", OnUnmet: ImpactUnavailable}}},
			{Type: capType("r2"), Requirements: []Requirement{dep("left", "l", ImpactUnavailable)}},
		},
	}
	lt := target("left", "t-left", left.Identity)
	rt := target("right", "t-right", right.Identity)
	lt.DependencyBindings = []DependencyBinding{bindDep("right", rt)}
	rt.DependencyBindings = []DependencyBinding{bindDep("left", lt)}
	a := mustEval(t, Snapshot{At: at, Contracts: []Contract{left, right}, Targets: []Target{lt, rt}})
	for _, uid := range []string{"t-left", "t-right"} {
		ta := targetOf(t, a, uid)
		if ta.Valid {
			t.Fatalf("%s in cross-contract cycle assessed as valid", uid)
		}
		expectReason(t, ta.InvalidReasons, ReasonDependencyCycle, "")
	}
}

// ── S19 contract revision ───────────────────────────────────────────────────

func TestS19_ContractSpecDigestChangeInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	c := httpServiceContract()
	c.Identity.SpecDigest = "sha256:http2"
	s.Contracts = []Contract{c}
	s.Targets[0].ContractRef = c.Identity
	a := mustEval(t, s)
	r := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, r.Reasons, ReasonEvidenceContractChanged, "assertion:api-serving")
}

func TestS19_ContractRecreatedUnderSameNameInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	c := httpServiceContract()
	c.Identity.UID = "c-http-recreated"
	s.Contracts = []Contract{c}
	s.Targets[0].ContractRef = c.Identity
	a := mustEval(t, s)
	expectCap(t, a, "t-web", "serve-requests", Unknown)
}

func TestS19_TargetPinnedToVanishedContractIsInvalid(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	c := httpServiceContract()
	c.Identity.SpecDigest = "sha256:http2"
	s.Contracts = []Contract{c} // target still references sha256:http1
	a := mustEval(t, s)
	ta := targetOf(t, a, "t-web")
	if ta.Valid {
		t.Fatal("target bound to a contract identity absent from the snapshot must not be assessed")
	}
	expectReason(t, ta.InvalidReasons, ReasonContractUnresolved, "")
}

// ── provider config revision ────────────────────────────────────────────────

func TestProviderConfigRevisionChangeInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Targets[0].AssertionBindings[0].Provider.ConfigRevision = "r2"
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceProviderChanged, "assertion:api-serving")
	expectCap(t, a, "t-web", "export-metrics", Available)
}

func TestProviderRebindInvalidatesOldEvidence(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Targets[0].AssertionBindings[0].Provider = ProviderIdentity{Name: "app-api-provider", ConfigRevision: "r1"}
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceProviderChanged, "assertion:api-serving")
}

// ── target binding validity ─────────────────────────────────────────────────

func TestDuplicateAssertionBindingInvalidatesTarget(t *testing.T) {
	s := pipelineSnapshot(true)
	s.Targets[0].AssertionBindings = append(s.Targets[0].AssertionBindings, bind("resolve-api-serving", workloadStat))
	a := mustEval(t, s)
	ta := targetOf(t, a, "t-resolver")
	if ta.Valid {
		t.Fatal("duplicate authoritative binding accepted")
	}
	expectReason(t, ta.InvalidReasons, ReasonDuplicateAssertionBinding, "resolve-api-serving")
	c := expectCap(t, a, "t-worker", "submit-work", Unknown)
	expectReason(t, c.Reasons, ReasonDependencyTargetInvalid, "t-resolver")
	expectCap(t, a, "t-worker", "observe-running-work", Available)
}

func TestUnboundSlotIsUnknown(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.Targets[0].AssertionBindings = s.Targets[0].AssertionBindings[1:]
	a := mustEval(t, s)
	c := expectCap(t, a, "t-web", "serve-requests", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceUnbound, "assertion:api-serving")
}

func TestInvalidTargetBindings(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Snapshot)
		code ReasonCode
	}{
		"cross-namespace contract ref": {func(s *Snapshot) {
			s.Targets[0].Identity.Namespace = "elsewhere"
		}, ReasonCrossNamespaceContract},
		"binding to undeclared slot": {func(s *Snapshot) {
			s.Targets[0].AssertionBindings = append(s.Targets[0].AssertionBindings, bind("nope", httpProbe))
		}, ReasonInvalidBinding},
		"unknown envelope selection": {func(s *Snapshot) {
			s.Targets[0].EnvelopeSelection = "gold"
		}, ReasonUnknownEnvelopeSelection},
		"duplicate target uid": {func(s *Snapshot) {
			s.Targets = append(s.Targets, s.Targets[0])
		}, ReasonDuplicateTargetUID},
		"ambiguous contract identity": {func(s *Snapshot) {
			s.Contracts = append(s.Contracts, s.Contracts[0])
		}, ReasonContractAmbiguous},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := httpServiceSnapshot(true, true, true)
			tc.mut(&s)
			a := mustEval(t, s)
			if len(a.Targets) == 0 {
				t.Fatal("no target assessed")
			}
			for _, ta := range a.Targets {
				if ta.Valid {
					t.Fatalf("target %s assessed despite %s", ta.Target, name)
				}
				expectReason(t, ta.InvalidReasons, tc.code, "")
			}
		})
	}
}

func TestDuplicateDependencyBindingInvalidatesTarget(t *testing.T) {
	s := pipelineSnapshot(true)
	w := &s.Targets[1]
	w.DependencyBindings = append(w.DependencyBindings, w.DependencyBindings[0])
	a := mustEval(t, s)
	ta := targetOf(t, a, "t-worker")
	if ta.Valid {
		t.Fatal("duplicate dependency binding accepted")
	}
	expectReason(t, ta.InvalidReasons, ReasonDuplicateDependencyBinding, "resolver")
}

func TestUnboundDependencySlotIsUnknown(t *testing.T) {
	s := pipelineSnapshot(true)
	s.Targets[1].DependencyBindings = nil
	a := mustEval(t, s)
	c := expectCap(t, a, "t-worker", "submit-work", Unknown)
	expectReason(t, c.Reasons, ReasonDependencyUnbound, "resolver")
}

// ── envelopes ───────────────────────────────────────────────────────────────

func TestEnvelopeMinimumSatisfiedRecommendedUnsatisfied(t *testing.T) {
	a := mustEval(t, replicatedSnapshot(1, fresh))
	ta := targetOf(t, a, "t-store")
	expectEnv(t, ta, "minimum", Satisfied)
	e := expectEnv(t, ta, "recommended", Unsatisfied)
	expectReason(t, e.Reasons, ReasonPredicateUnmet, "assertion:available-replicas")
	// Not a full outage: capabilities that do not reference the envelope are
	// unchanged.
	expectCap(t, a, "t-store", "serve", Available)
	expectCap(t, a, "t-store", "persist", Available)
	expectCap(t, a, "t-store", "run-declared-config", Available)
	c := expectCap(t, a, "t-store", "serve-with-redundancy", Degraded)
	expectReason(t, c.Reasons, ReasonEnvelopeUnmet, "envelope:recommended")
	if ta.EnvelopeSelection != "recommended" {
		t.Fatalf("envelope selection not reported: %q", ta.EnvelopeSelection)
	}
}

func TestEnvelopeSatisfiedDoesNotOverrideCapabilityTruth(t *testing.T) {
	// replicas=3 but storage evidence stale: recommended cannot be proven, and
	// persist stays UNKNOWN even though serve is fine.
	a := mustEval(t, replicatedSnapshot(3, time.Minute))
	ta := targetOf(t, a, "t-store")
	expectEnv(t, ta, "minimum", EnvUnknown)
	expectEnv(t, ta, "recommended", EnvUnknown)
	expectCap(t, a, "t-store", "serve", Available)
	c := expectCap(t, a, "t-store", "persist", Unknown)
	expectReason(t, c.Reasons, ReasonEvidenceStale, "assertion:storage-writable")
	expectCap(t, a, "t-store", "serve-with-redundancy", Unknown)
}

func TestEnvelopeAnyFalseIsUnsatisfiedEvenWithUnknown(t *testing.T) {
	a := mustEval(t, replicatedSnapshot(0, time.Minute))
	ta := targetOf(t, a, "t-store")
	expectEnv(t, ta, "minimum", Unsatisfied)
	expectEnv(t, ta, "recommended", Unsatisfied)
	expectCap(t, a, "t-store", "serve", Unavailable)
}

func TestEnvelopeAllSatisfied(t *testing.T) {
	a := mustEval(t, replicatedSnapshot(2, fresh))
	ta := targetOf(t, a, "t-store")
	expectEnv(t, ta, "minimum", Satisfied)
	expectEnv(t, ta, "recommended", Satisfied)
	expectCap(t, a, "t-store", "serve-with-redundancy", Available)
}

func TestSyncIsNeverDerivedFromGitOpsAssertion(t *testing.T) {
	s := replicatedSnapshot(2, fresh)
	replaceObs(&s, "config-in-sync", observe(replicatedTarget(), "config-in-sync", Bool(false), fresh))
	a := mustEval(t, s)
	ta := targetOf(t, a, "t-store")
	if ta.Sync != SyncNotApplicable {
		t.Fatalf("Sync derived from an assertion: %q", ta.Sync)
	}
	expectCap(t, a, "t-store", "run-declared-config", Degraded)
	expectCap(t, a, "t-store", "serve", Available)
}

func expectEnv(t *testing.T, ta TargetAssessment, name string, want EnvelopeState) EnvelopeResult {
	t.Helper()
	e, ok := ta.Envelope(name)
	if !ok {
		t.Fatalf("envelope %s not assessed", name)
	}
	if e.State != want {
		t.Fatalf("envelope %s = %s, want %s (%+v)", name, e.State, want, e.Reasons)
	}
	return e
}

// ── stateful single writer ──────────────────────────────────────────────────

func TestSingleWriterEqOne(t *testing.T) {
	cases := []struct {
		writers int64
		role    string
		cap     CapabilityState
		env     EnvelopeState
	}{
		{1, "primary", Available, Satisfied},
		{2, "primary", Unavailable, Unsatisfied}, // more replicas is not better
		{0, "primary", Unavailable, Unsatisfied},
		{1, "fenced", Unavailable, Satisfied},
	}
	for _, tc := range cases {
		a := mustEval(t, singleWriterSnapshot(tc.writers, tc.role))
		expectCap(t, a, "t-db", "accept-writes", tc.cap)
		expectEnv(t, targetOf(t, a, "t-db"), "minimum", tc.env)
	}
}

// ── product output ──────────────────────────────────────────────────────────

func TestSummaryLocalizesImpact(t *testing.T) {
	s := pipelineSnapshot(false)
	replaceObs(&s, "producer-up", observe(pipelineTargets().producer, "producer-up", Bool(true), time.Hour))
	sum := mustEval(t, s).Summarize()
	names := func(rs []CapabilityRef) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.TargetUID+"/"+r.Capability.Name)
		}
		return out
	}
	assertSet(t, "affected", names(sum.Affected), "t-resolver/resolve-artifact", "t-worker/submit-work")
	assertSet(t, "unaffected", names(sum.Unaffected), "t-worker/observe-running-work")
	assertSet(t, "unknown", names(sum.Unknown), "t-producer/produce-work")
}

func assertSet(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestEvaluateRequiresExplicitInstant(t *testing.T) {
	s := httpServiceSnapshot(true, true, true)
	s.At = time.Time{}
	if _, err := Evaluate(s); err == nil {
		t.Fatal("Evaluate accepted a snapshot without an evaluation instant")
	}
}

func containsErr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
