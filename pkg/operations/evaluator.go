package operations

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrNoEvaluationInstant is returned when the snapshot has no evaluation
// instant. Currentness is never inferred from a wall clock.
var ErrNoEvaluationInstant = errors.New("operations: snapshot.At is required")

// Evaluate derives the capability and envelope assessment of every target in
// the snapshot. It is a pure function of its input: it reads no clock, keeps
// no state between calls and does not modify the snapshot.
//
// Rules, in brief:
//   - Evidence counts only when its ApplicabilityKey equals the target's
//     current binding exactly; anything else is inapplicable.
//   - Within one applicability key the provider's latest statement supersedes
//     its earlier ones. Differing statements at the same latest instant are
//     conflicting evidence. A latest statement that is not current is stale;
//     the evaluator never falls back to an older value.
//   - A capability is AVAILABLE only if every requirement it references is
//     proven accepted; UNKNOWN if any is unproven; otherwise the single
//     declared impact of its unmet requirements, or UNKNOWN when declared
//     impacts disagree.
//   - Envelopes are AllOf and affect only capabilities that reference them.
func Evaluate(s Snapshot) (Assessment, error) {
	if s.At.IsZero() {
		return Assessment{}, ErrNoEvaluationInstant
	}
	e := newEvaluator(s)
	e.resolveTargets()
	e.rejectCycles()

	out := Assessment{Targets: make([]TargetAssessment, 0, len(e.order))}
	for _, ts := range e.order {
		out.Targets = append(out.Targets, e.assess(ts))
	}
	return out, nil
}

type targetState struct {
	t        Target
	contract *Contract
	invalid  []Reason
	evidence map[string]slotEvidence
}

type slotEvidence struct {
	status   EvidenceStatus
	code     ReasonCode
	detail   string
	value    Value
	provider ProviderIdentity
	ref      string
}

type capKey struct {
	uid  string
	name string
}

type obsKey struct {
	namespace, name, slot string
}

type evaluator struct {
	at             time.Time
	contracts      map[ContractIdentity][]*Contract
	byUID          map[string][]*targetState
	order          []*targetState
	obs            map[obsKey][]Observation
	capMemo        map[capKey]CapabilityResult
	envMemo        map[capKey]EnvelopeResult
	validatedCache map[*Contract][]string
}

func newEvaluator(s Snapshot) *evaluator {
	e := &evaluator{
		at:             s.At,
		contracts:      map[ContractIdentity][]*Contract{},
		byUID:          map[string][]*targetState{},
		obs:            map[obsKey][]Observation{},
		capMemo:        map[capKey]CapabilityResult{},
		envMemo:        map[capKey]EnvelopeResult{},
		validatedCache: map[*Contract][]string{},
	}
	for i := range s.Contracts {
		c := &s.Contracts[i]
		e.contracts[c.Identity] = append(e.contracts[c.Identity], c)
	}
	for _, t := range s.Targets {
		ts := &targetState{t: t, evidence: map[string]slotEvidence{}}
		e.byUID[t.Identity.UID] = append(e.byUID[t.Identity.UID], ts)
		e.order = append(e.order, ts)
	}
	sort.SliceStable(e.order, func(i, j int) bool {
		a, b := e.order[i].t.Identity, e.order[j].t.Identity
		if a.UID != b.UID {
			return a.UID < b.UID
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	for _, o := range s.Observations {
		k := obsKey{o.Key.Target.Namespace, o.Key.Target.Name, o.Key.Slot}
		e.obs[k] = append(e.obs[k], o)
	}
	return e
}

// ── target resolution ───────────────────────────────────────────────────────

func (e *evaluator) resolveTargets() {
	for _, ts := range e.order {
		e.resolveTarget(ts)
	}
}

func (e *evaluator) resolveTarget(ts *targetState) {
	t := ts.t
	bad := func(code ReasonCode, subject, detail string) {
		ts.invalid = append(ts.invalid, Reason{Code: code, Subject: subject, Detail: detail})
	}
	if len(e.byUID[t.Identity.UID]) > 1 {
		bad(ReasonDuplicateTargetUID, "target:"+t.Identity.UID, "")
	}
	if t.ContractRef.Namespace != t.Identity.Namespace {
		bad(ReasonCrossNamespaceContract, "contract:"+t.ContractRef.String(), "")
		return
	}
	switch cs := e.contracts[t.ContractRef]; len(cs) {
	case 0:
		bad(ReasonContractUnresolved, "contract:"+t.ContractRef.String(), "")
		return
	case 1:
		ts.contract = cs[0]
	default:
		bad(ReasonContractAmbiguous, "contract:"+t.ContractRef.String(), fmt.Sprintf("%d contracts share this identity", len(cs)))
		return
	}
	if errs := e.validate(ts.contract); len(errs) > 0 {
		bad(ReasonInvalidContract, "contract:"+t.ContractRef.String(), strings.Join(errs, "; "))
		return
	}

	c := ts.contract
	seen := map[string]bool{}
	for _, b := range t.AssertionBindings {
		if _, ok := c.slot(b.Slot); !ok || b.Provider.Name == "" {
			bad(ReasonInvalidBinding, "assertion:"+b.Slot, "undeclared slot or empty provider")
			continue
		}
		if seen[b.Slot] {
			bad(ReasonDuplicateAssertionBinding, "assertion:"+b.Slot, "")
		}
		seen[b.Slot] = true
	}
	seenDep := map[string]bool{}
	for _, b := range t.DependencyBindings {
		if !hasDepSlot(c, b.Slot) || b.TargetUID == "" || !b.Contract.complete() {
			bad(ReasonInvalidBinding, "dependency:"+b.Slot, "undeclared slot, empty target UID or incomplete contract identity")
			continue
		}
		if seenDep[b.Slot] {
			bad(ReasonDuplicateDependencyBinding, "dependency:"+b.Slot, "")
		}
		seenDep[b.Slot] = true
	}
	if t.EnvelopeSelection != "" {
		if _, ok := c.envelope(t.EnvelopeSelection); !ok {
			bad(ReasonUnknownEnvelopeSelection, "envelope:"+t.EnvelopeSelection, "")
		}
	}
}

func (e *evaluator) validate(c *Contract) []string {
	if errs, ok := e.validatedCache[c]; ok {
		return errs
	}
	errs := c.Validate()
	sort.Strings(errs)
	e.validatedCache[c] = errs
	return errs
}

func hasDepSlot(c *Contract, slot string) bool {
	for _, d := range c.DependencySlots {
		if d.Name == slot {
			return true
		}
	}
	return false
}

func (ts *targetState) valid() bool { return len(ts.invalid) == 0 }

func (ts *targetState) depBinding(slot string) (DependencyBinding, bool) {
	for _, b := range ts.t.DependencyBindings {
		if b.Slot == slot {
			return b, true
		}
	}
	return DependencyBinding{}, false
}

func (ts *targetState) assertionBinding(slot string) (AssertionBinding, bool) {
	for _, b := range ts.t.AssertionBindings {
		if b.Slot == slot {
			return b, true
		}
	}
	return AssertionBinding{}, false
}

// uniqueValid returns the single valid target with this UID.
func (e *evaluator) uniqueValid(uid string) (*targetState, bool) {
	ts := e.byUID[uid]
	if len(ts) != 1 || !ts[0].valid() {
		return nil, false
	}
	return ts[0], true
}

// ── cross-target cycle rejection ────────────────────────────────────────────

// edges returns the capability nodes n directly depends on, following only
// dependencies that resolve exactly. Unresolvable dependencies are UNKNOWN at
// evaluation time and cannot close a cycle.
func (e *evaluator) edges(n capKey) []capKey {
	ts, ok := e.uniqueValid(n.uid)
	if !ok {
		return nil
	}
	cp, _ := ts.contract.capability(n.name)
	var out []capKey
	for _, r := range cp.Requirements {
		switch {
		case r.LocalCapability != "":
			out = append(out, capKey{n.uid, r.LocalCapability})
		case r.Dependency != nil:
			b, ok := ts.depBinding(r.Dependency.Slot)
			if !ok {
				continue
			}
			dt, ok := e.uniqueValid(b.TargetUID)
			if !ok || dt.t.ContractRef != b.Contract {
				continue
			}
			if dc, ok := dt.contract.capabilityByType(r.Dependency.Capability); ok {
				out = append(out, capKey{dt.t.Identity.UID, dc.Type.Name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].uid != out[j].uid {
			return out[i].uid < out[j].uid
		}
		return out[i].name < out[j].name
	})
	return out
}

// rejectCycles invalidates every target owning a capability on a dependency
// cycle. No cycle is resolved by assuming health or by fixpoint iteration.
func (e *evaluator) rejectCycles() {
	var nodes []capKey
	for _, ts := range e.order {
		if !ts.valid() {
			continue
		}
		for _, cp := range ts.contract.Capabilities {
			nodes = append(nodes, capKey{ts.t.Identity.UID, cp.Type.Name})
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].uid != nodes[j].uid {
			return nodes[i].uid < nodes[j].uid
		}
		return nodes[i].name < nodes[j].name
	})

	const (
		unvisited = iota
		onStack
		done
	)
	state := map[capKey]int{}
	onCycle := map[capKey]bool{}
	var stack []capKey
	var visit func(capKey)
	visit = func(n capKey) {
		state[n] = onStack
		stack = append(stack, n)
		for _, m := range e.edges(n) {
			switch state[m] {
			case onStack:
				for i := len(stack) - 1; i >= 0; i-- {
					onCycle[stack[i]] = true
					if stack[i] == m {
						break
					}
				}
			case unvisited:
				visit(m)
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, n := range nodes {
		if state[n] == unvisited {
			visit(n)
		}
	}

	members := map[string][]string{}
	for n := range onCycle {
		members[n.uid] = append(members[n.uid], n.name)
	}
	var all []string
	for n := range onCycle {
		all = append(all, n.uid+"/"+n.name)
	}
	sort.Strings(all)
	for uid, names := range members {
		sort.Strings(names)
		for _, ts := range e.byUID[uid] {
			ts.invalid = append(ts.invalid, Reason{
				Code:    ReasonDependencyCycle,
				Subject: "capabilities:" + strings.Join(names, ","),
				Detail:  "cycle members: " + strings.Join(all, ","),
			})
		}
	}
}

// ── evidence ────────────────────────────────────────────────────────────────

func (e *evaluator) evidence(ts *targetState, slot AssertionSlot) slotEvidence {
	if ev, ok := ts.evidence[slot.Name]; ok {
		return ev
	}
	ev := e.resolveEvidence(ts, slot)
	ts.evidence[slot.Name] = ev
	return ev
}

func (e *evaluator) resolveEvidence(ts *targetState, slot AssertionSlot) slotEvidence {
	b, ok := ts.assertionBinding(slot.Name)
	if !ok {
		return slotEvidence{status: EvidenceUnbound, code: ReasonEvidenceUnbound}
	}
	want := ApplicabilityKey{
		Target:      ts.t.Identity,
		ResolvedUID: ts.t.ResolvedUID,
		Contract:    ts.t.ContractRef,
		Slot:        slot.Name,
		Provider:    b.Provider,
	}
	ev := slotEvidence{provider: b.Provider}

	var applicable []Observation
	var inapplicable ReasonCode
	for _, o := range e.obs[obsKey{ts.t.Identity.Namespace, ts.t.Identity.Name, slot.Name}] {
		if o.ObservedAt.After(e.at) {
			continue // not yet observable at the evaluation instant
		}
		if o.Key == want {
			applicable = append(applicable, o)
			continue
		}
		inapplicable = worseMismatch(inapplicable, mismatch(o.Key, want))
	}

	if len(applicable) == 0 {
		if inapplicable != "" {
			ev.status, ev.code = EvidenceInapplicable, inapplicable
			return ev
		}
		ev.status, ev.code = EvidenceMissing, ReasonEvidenceMissing
		return ev
	}

	latest := applicable[0].ObservedAt
	for _, o := range applicable[1:] {
		if o.ObservedAt.After(latest) {
			latest = o.ObservedAt
		}
	}
	var group []Observation
	for _, o := range applicable {
		if o.ObservedAt.Equal(latest) {
			group = append(group, o)
		}
	}
	sort.Slice(group, func(i, j int) bool { return group[i].EvidenceRef < group[j].EvidenceRef })
	ev.ref = group[0].EvidenceRef

	for _, o := range group {
		if !e.current(o, slot) {
			ev.status, ev.code = EvidenceStale, ReasonEvidenceStale
			ev.detail = fmt.Sprintf("latest observation at %s exceeds currentness bound", latest.UTC().Format(time.RFC3339))
			return ev
		}
	}

	type statement struct {
		outcome Outcome
		value   Value
	}
	distinct := map[statement]bool{}
	for _, o := range group {
		st := statement{outcome: o.Outcome}
		if o.Outcome == OutcomeValue {
			st.value = o.Value
		}
		distinct[st] = true
	}
	if len(distinct) > 1 {
		ev.status, ev.code = EvidenceConflicting, ReasonConflictingEvidence
		ev.detail = fmt.Sprintf("%d incompatible statements at the latest instant", len(distinct))
		return ev
	}

	o := group[0]
	switch {
	case o.Outcome == OutcomeProviderUnavailable:
		ev.status, ev.code = EvidenceProviderUnavailable, ReasonProviderUnavailable
	case o.Outcome != OutcomeValue || !slot.admits(o.Value):
		ev.status, ev.code = EvidenceTypeMismatch, ReasonEvidenceTypeMismatch
		ev.detail = fmt.Sprintf("slot type %s, observed %s", slot.Type, o.Value)
	default:
		ev.status, ev.value = EvidenceCurrent, o.Value
	}
	return ev
}

func (e *evaluator) current(o Observation, slot AssertionSlot) bool {
	if e.at.Sub(o.ObservedAt) > slot.MaxAge {
		return false
	}
	return o.ValidUntil.IsZero() || e.at.Before(o.ValidUntil)
}

// mismatch names the first element of the applicability key that differs.
func mismatch(got, want ApplicabilityKey) ReasonCode {
	switch {
	case got.Target != want.Target || got.ResolvedUID != want.ResolvedUID:
		return ReasonEvidenceTargetReplaced
	case got.Contract != want.Contract:
		return ReasonEvidenceContractChanged
	default:
		return ReasonEvidenceProviderChanged
	}
}

// worseMismatch keeps a deterministic attribution when several inapplicable
// observations exist: target replacement, then contract, then provider.
func worseMismatch(a, b ReasonCode) ReasonCode {
	rank := map[ReasonCode]int{
		"":                            0,
		ReasonEvidenceProviderChanged: 1,
		ReasonEvidenceContractChanged: 2,
		ReasonEvidenceTargetReplaced:  3,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ── requirement evaluation ──────────────────────────────────────────────────

type verdict int

const (
	accepted verdict = iota
	unmet
	unproven
)

func (e *evaluator) predicate(ts *targetState, p Predicate) (verdict, Reason) {
	slot, _ := ts.contract.slot(p.Assertion)
	ev := e.evidence(ts, slot)
	subject := "assertion:" + p.Assertion
	if ev.status != EvidenceCurrent {
		return unproven, Reason{Code: ev.code, Subject: subject, Detail: ev.detail}
	}
	if p.eval(ev.value) {
		return accepted, Reason{}
	}
	return unmet, Reason{Code: ReasonPredicateUnmet, Subject: subject, Detail: fmt.Sprintf("%s; observed %s", p, ev.value)}
}

func (e *evaluator) envelopeResult(ts *targetState, env Envelope) EnvelopeResult {
	k := capKey{ts.t.Identity.UID, env.Name}
	if r, ok := e.envMemo[k]; ok {
		return r
	}
	var reasons []Reason
	anyUnmet, anyUnproven := false, false
	for _, p := range env.Requirements {
		v, r := e.predicate(ts, p)
		switch v {
		case unmet:
			anyUnmet = true
			reasons = append(reasons, r)
		case unproven:
			anyUnproven = true
			reasons = append(reasons, r)
		}
	}
	res := EnvelopeResult{Name: env.Name, Class: env.Class, State: Satisfied}
	switch {
	case anyUnmet:
		res.State = Unsatisfied
	case anyUnproven:
		res.State = EnvUnknown
	}
	res.Reasons = normalize(reasons)
	e.envMemo[k] = res
	return res
}

func (e *evaluator) dependency(ts *targetState, d DependencyRequirement) (verdict, Reason) {
	b, ok := ts.depBinding(d.Slot)
	if !ok {
		return unproven, Reason{Code: ReasonDependencyUnbound, Subject: "dependency:" + d.Slot}
	}
	subject := "dependency:" + d.Slot + "->" + b.TargetUID + "/" + d.Capability.String()
	cands := e.byUID[b.TargetUID]
	if len(cands) == 0 {
		return unproven, Reason{Code: ReasonDependencyTargetMissing, Subject: subject}
	}
	dt, ok := e.uniqueValid(b.TargetUID)
	if !ok {
		return unproven, Reason{Code: ReasonDependencyTargetInvalid, Subject: subject}
	}
	if dt.t.ContractRef != b.Contract {
		return unproven, Reason{Code: ReasonDependencyContractMismatch, Subject: subject,
			Detail: "bound " + b.Contract.String() + ", target has " + dt.t.ContractRef.String()}
	}
	dc, ok := dt.contract.capabilityByType(d.Capability)
	if !ok {
		return unproven, Reason{Code: ReasonDependencyCapabilityAbsent, Subject: subject}
	}
	switch r := e.capability(dt, dc); r.State {
	case Available:
		return accepted, Reason{}
	case Unknown:
		return unproven, Reason{Code: ReasonDependencyUnknown, Subject: subject}
	default:
		return unmet, Reason{Code: ReasonDependencyUnmet, Subject: subject, Detail: string(r.State)}
	}
}

func (e *evaluator) capability(ts *targetState, cp Capability) CapabilityResult {
	k := capKey{ts.t.Identity.UID, cp.Type.Name}
	if r, ok := e.capMemo[k]; ok {
		return r
	}

	var reasons []Reason
	impacts := map[Impact]bool{}
	anyUnproven := false
	for _, req := range cp.Requirements {
		var v verdict
		var r Reason
		switch {
		case req.Predicate != nil:
			v, r = e.predicate(ts, *req.Predicate)
		case req.Dependency != nil:
			v, r = e.dependency(ts, *req.Dependency)
		case req.LocalCapability != "":
			local, _ := ts.contract.capability(req.LocalCapability)
			subject := "local:" + req.LocalCapability
			switch lr := e.capability(ts, local); lr.State {
			case Available:
				v = accepted
			case Unknown:
				v, r = unproven, Reason{Code: ReasonLocalUnknown, Subject: subject}
			default:
				v, r = unmet, Reason{Code: ReasonLocalUnmet, Subject: subject, Detail: string(lr.State)}
			}
		case req.Envelope != "":
			env, _ := ts.contract.envelope(req.Envelope)
			subject := "envelope:" + req.Envelope
			switch er := e.envelopeResult(ts, env); er.State {
			case Satisfied:
				v = accepted
			case Unsatisfied:
				v, r = unmet, Reason{Code: ReasonEnvelopeUnmet, Subject: subject}
			default:
				v, r = unproven, Reason{Code: ReasonEnvelopeUnknown, Subject: subject}
			}
		}
		switch v {
		case unmet:
			impacts[req.OnUnmet] = true
			reasons = append(reasons, r)
		case unproven:
			anyUnproven = true
			reasons = append(reasons, r)
		}
	}

	res := CapabilityResult{Type: cp.Type, State: Available}
	switch {
	case anyUnproven:
		res.State = Unknown
	case len(impacts) > 1:
		// No composition rule orders DEGRADED against UNAVAILABLE: fail closed.
		res.State = Unknown
		reasons = append(reasons, Reason{Code: ReasonConflictingImpact, Subject: "capability:" + cp.Type.String()})
	case impacts[ImpactDegraded]:
		res.State = Degraded
	case impacts[ImpactUnavailable]:
		res.State = Unavailable
	}
	res.Reasons = normalize(reasons)
	e.capMemo[k] = res
	return res
}

// ── output ──────────────────────────────────────────────────────────────────

func (e *evaluator) assess(ts *targetState) TargetAssessment {
	out := TargetAssessment{
		Target:            ts.t.Identity,
		Contract:          ts.t.ContractRef,
		Valid:             ts.valid(),
		Sync:              SyncNotApplicable,
		EnvelopeSelection: ts.t.EnvelopeSelection,
	}
	if !out.Valid {
		out.InvalidReasons = normalize(ts.invalid)
		return out
	}
	c := ts.contract

	slots := append([]AssertionSlot(nil), c.Assertions...)
	sort.Slice(slots, func(i, j int) bool { return slots[i].Name < slots[j].Name })
	for _, s := range slots {
		ev := e.evidence(ts, s)
		out.Evidence = append(out.Evidence, SlotEvidence{Slot: s.Name, Status: ev.status, Provider: ev.provider, EvidenceRef: ev.ref})
	}

	for _, cp := range c.Capabilities {
		out.Capabilities = append(out.Capabilities, e.capability(ts, cp))
	}
	sort.Slice(out.Capabilities, func(i, j int) bool { return out.Capabilities[i].Type.less(out.Capabilities[j].Type) })

	for _, env := range c.Envelopes {
		out.Envelopes = append(out.Envelopes, e.envelopeResult(ts, env))
	}
	sort.Slice(out.Envelopes, func(i, j int) bool { return out.Envelopes[i].Name < out.Envelopes[j].Name })
	return out
}

// normalize sorts and de-duplicates reasons so that output is independent of
// declaration order.
func normalize(rs []Reason) []Reason {
	if len(rs) == 0 {
		return nil
	}
	out := append([]Reason(nil), rs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Detail < out[j].Detail
	})
	n := 0
	for i, r := range out {
		if i > 0 && r == out[n-1] {
			continue
		}
		out[n] = r
		n++
	}
	return out[:n]
}
