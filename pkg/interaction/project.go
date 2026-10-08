// Package interaction derives the O4 operator interaction summary of one
// OperationalTarget.
//
// It is a pure, deterministic projection over three inputs that already
// exist: the target's semantic status as projected from the O1 assessment
// (with every cross-namespace and cycle redaction already applied), the
// target's current O3 investigation record, and the operator's declared
// reference profile. It evaluates no capability, performs no I/O, reads no
// other object and grants no authority. Its level is non-authoritative and
// never feeds back into capability truth.
package interaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
)

// Level reason codes: the decision table (see docs/operational-interaction.md).
const (
	ReasonTargetNotAssessed     = "target-not-assessed"
	ReasonNoCapabilities        = "no-capabilities-assessed"
	ReasonCapabilityUnknown     = "capability-unknown"
	ReasonEnvelopeUnsatisfied   = "envelope-unsatisfied"
	ReasonEnvelopeUnknown       = "envelope-unknown"
	ReasonInvestigationActive   = "investigation-in-progress"
	ReasonImpactFromDependency  = "impact-from-dependency"
	ReasonNoDeclaredResponse    = "no-declared-response"
	ReasonDeclaredResponseReady = "declared-response-ready"
	ReasonApprovalRequired      = "approval-required"
	ReasonSafetyUnproven        = "safety-condition-unproven"
	ReasonOwnerUndeclared       = "owner-undeclared"
	ReasonOwnerConflict         = "owner-conflict"
	ReasonNoPriorityAuthority   = "no-priority-authority"
	ReasonNoViableResponse      = "no-viable-response"
	ReasonNoSafePath            = "no-safe-path"
	ReasonAllAvailable          = "all-capabilities-available"
	ReasonSpecCorrection        = "spec-correction-required"
)

// Response statuses.
const (
	ResponseReady            = "Ready"
	ResponseApprovalRequired = "ApprovalRequired"
	ResponseSafetyUnproven   = "SafetyUnproven"
	ResponseOwnerUndeclared  = "OwnerUndeclared"
	ResponseOwnerConflict    = "OwnerConflict"
	ResponseInapplicable     = "Inapplicable"
)

// Post-condition and precondition codes.
const (
	PostCapabilityAvailable = "capability-available-on-current-evidence"
	PostEnvelopeSatisfied   = "envelope-satisfied-on-current-evidence"
	PostUnconfirmed         = "unconfirmed"
	PreProven               = "proven"
	PreUnproven             = "unproven"
	PreUnmet                = "unmet"
)

// Bounds of the summary (mirrored by the CRD schema).
const (
	maxCaps       = 64
	maxFacts      = 64
	maxCandidates = 16
	maxResponses  = 8
	maxReasons    = 8
	maxCapReasons = 4
	maxPost       = 16
	maxHistory    = 4
	maxSummary    = 256
)

// Investigation is the target's current O3 episode record, already checked
// to belong to the target's exact current identity.
type Investigation struct {
	Kind       string
	Outcome    string
	Candidates []Candidate
}

// Candidate is one O3 hypothesis.
type Candidate struct {
	ID, Kind, Ref, Status string
}

// Input is everything one projection reads.
type Input struct {
	Namespace, Name, UID string
	// Status is the target's semantic status from opswire.Project, without
	// an interaction summary.
	Status opsv1.OperationalTargetStatus
	// Investigation is nil when no investigation record exists for the
	// target's current identity.
	Investigation *Investigation
	// Profile is nil when no reference profile is configured.
	Profile *Profile
}

var rank = map[opsv1.InteractionLevel]int{
	opsv1.LevelNoAction:              0,
	opsv1.LevelAwareness:             1,
	opsv1.LevelDecisionRequired:      2,
	opsv1.LevelImmediateIntervention: 3,
}

type decision struct {
	level   opsv1.InteractionLevel
	reasons []opsv1.StatusReason
	human   []opsv1.StatusReason
}

func (d *decision) raise(l opsv1.InteractionLevel, code, subject string) {
	if rank[l] > rank[d.level] {
		d.level = l
	}
	d.reasons = append(d.reasons, opsv1.StatusReason{Code: code, Subject: subject})
}

func (d *decision) needHuman(code, subject string) {
	d.human = append(d.human, opsv1.StatusReason{Code: code, Subject: subject})
}

// Project derives the interaction summary. prev is the summary currently in
// the target's status (nil if none); it is used only for the bounded
// recurrence history of the same identity.
func Project(in Input, prev *opsv1.InteractionStatus) *opsv1.InteractionStatus {
	st := in.Status
	out := &opsv1.InteractionStatus{IdentityDigest: identityDigest(in)}
	d := &decision{level: opsv1.LevelNoAction}

	if !st.Valid {
		// Not assessed: no capability result is fabricated.
		subject := ""
		if len(st.InvalidReasons) > 0 {
			subject = st.InvalidReasons[0].Code
		}
		d.raise(opsv1.LevelDecisionRequired, ReasonTargetNotAssessed, subject)
		d.needHuman(ReasonSpecCorrection, subject)
		out.MissingEvidence = missing(st)
		out.Investigation = investigationOf(nil, nil)
		out.Summary = clip("not assessed (" + joinCodes(st.InvalidReasons, 3) + "); no capability result is reported")
		return finish(out, d, prev)
	}

	caps := map[string]opsv1.CapabilityStatus{}
	for _, c := range st.Capabilities {
		caps[typeKey(c.Type)] = c
		ic := opsv1.InteractionCapability{Type: c.Type, State: c.State, Reasons: limit(c.Reasons, maxCapReasons)}
		switch c.State {
		case "AVAILABLE":
			ic.Reasons = nil
			out.Unaffected = append(out.Unaffected, ic)
		case "DEGRADED", "UNAVAILABLE":
			out.Affected = append(out.Affected, ic)
		default:
			out.Unknown = append(out.Unknown, ic)
		}
	}
	sortCaps(out.Affected)
	sortCaps(out.Unaffected)
	sortCaps(out.Unknown)

	if len(st.Capabilities) == 0 {
		d.raise(opsv1.LevelAwareness, ReasonNoCapabilities, "")
	}
	for _, c := range out.Unknown {
		d.raise(opsv1.LevelAwareness, ReasonCapabilityUnknown, typeKey(c.Type))
	}
	for _, e := range st.Envelopes {
		if e.Name != st.EnvelopeSelection && e.Class != "Minimum" {
			continue
		}
		switch e.State {
		case "SATISFIED":
		case "UNSATISFIED":
			d.raise(opsv1.LevelAwareness, ReasonEnvelopeUnsatisfied, "envelope:"+e.Name)
			out.PendingPostConditions = append(out.PendingPostConditions, opsv1.StatusReason{Code: PostEnvelopeSatisfied, Subject: "envelope:" + e.Name, Detail: PostUnconfirmed})
		default:
			d.raise(opsv1.LevelAwareness, ReasonEnvelopeUnknown, "envelope:"+e.Name)
			out.PendingPostConditions = append(out.PendingPostConditions, opsv1.StatusReason{Code: PostEnvelopeSatisfied, Subject: "envelope:" + e.Name, Detail: PostUnconfirmed})
		}
	}
	if in.Investigation != nil && in.Investigation.Outcome == "Active" {
		d.raise(opsv1.LevelAwareness, ReasonInvestigationActive, "")
	}

	responses := declared(in, caps)
	for _, c := range append(append([]opsv1.InteractionCapability(nil), out.Affected...), out.Unknown...) {
		decideCapability(d, c, responses[typeKey(c.Type)])
		out.PendingPostConditions = append(out.PendingPostConditions, opsv1.StatusReason{Code: PostCapabilityAvailable, Subject: typeKey(c.Type), Detail: PostUnconfirmed})
	}
	for _, k := range sortedKeys(responses) {
		out.Responses = append(out.Responses, responses[k]...)
	}
	if len(out.Responses) > maxResponses {
		out.Responses = out.Responses[:maxResponses]
	}
	if d.level == opsv1.LevelNoAction {
		d.reasons = append(d.reasons, opsv1.StatusReason{Code: ReasonAllAvailable})
	}

	out.ConfirmedFacts = confirmed(st)
	out.MissingEvidence = missing(st)
	open := map[string]bool{}
	for _, c := range append(append([]opsv1.InteractionCapability(nil), out.Affected...), out.Unknown...) {
		open[typeKey(c.Type)] = true
	}
	out.CauseCandidates = candidates(in.Investigation, open)
	out.Investigation = investigationOf(in.Investigation, open)
	out.PendingPostConditions = limit(out.PendingPostConditions, maxPost)
	out.Affected = limitCaps(out.Affected)
	out.Unaffected = limitCaps(out.Unaffected)
	out.Unknown = limitCaps(out.Unknown)
	out.Summary = clip(summaryLine(out, len(st.Capabilities)))
	return finish(out, d, prev)
}

// decideCapability applies the declared response boundary to one affected or
// unknown capability. Without a declaration nothing escalates: the lack is
// reported. UNKNOWN alone never reaches IMMEDIATE_INTERVENTION.
func decideCapability(d *decision, c opsv1.InteractionCapability, rs []opsv1.InteractionResponse) {
	subject := typeKey(c.Type)
	unknown := c.State != "DEGRADED" && c.State != "UNAVAILABLE"
	if len(rs) == 0 {
		switch {
		case unknown:
			// Already raised as capability-unknown.
		case fromDependencyOnly(c):
			d.raise(opsv1.LevelAwareness, ReasonImpactFromDependency, subject)
		default:
			d.raise(opsv1.LevelAwareness, ReasonNoDeclaredResponse, subject)
		}
		return
	}
	var viable []opsv1.InteractionResponse
	for _, r := range rs {
		if r.Status != ResponseInapplicable {
			viable = append(viable, r)
		}
	}
	switch {
	case len(viable) == 0 && c.State == "UNAVAILABLE":
		d.raise(opsv1.LevelImmediateIntervention, ReasonNoSafePath, subject)
		d.needHuman(ReasonNoSafePath, subject)
		return
	case len(viable) == 0:
		d.raise(opsv1.LevelDecisionRequired, ReasonNoViableResponse, subject)
		d.needHuman(ReasonNoViableResponse, subject)
		return
	case len(viable) > 1:
		d.raise(opsv1.LevelDecisionRequired, ReasonNoPriorityAuthority, subject)
		d.needHuman(ReasonNoPriorityAuthority, subject)
	}
	for _, r := range viable {
		code := ""
		switch r.Status {
		case ResponseReady:
			if len(viable) == 1 {
				d.raise(opsv1.LevelAwareness, ReasonDeclaredResponseReady, subject)
			}
			continue
		case ResponseApprovalRequired:
			code = ReasonApprovalRequired
		case ResponseSafetyUnproven:
			code = ReasonSafetyUnproven
		case ResponseOwnerUndeclared:
			code = ReasonOwnerUndeclared
		case ResponseOwnerConflict:
			code = ReasonOwnerConflict
		}
		d.raise(opsv1.LevelDecisionRequired, code, subject)
		d.needHuman(code, r.Name)
	}
}

// fromDependencyOnly reports whether every reason of an affected capability
// names a dependency: the cause and its decision belong to that dependency's
// own target, so this target does not repeat the request (fan-out).
func fromDependencyOnly(c opsv1.InteractionCapability) bool {
	if len(c.Reasons) == 0 {
		return false
	}
	for _, r := range c.Reasons {
		if !strings.HasPrefix(r.Code, "dependency-") {
			return false
		}
	}
	return true
}

// declared returns, per capability, the profile responses declared for this
// exact target whose trigger state matches the capability's current state,
// with their status. Preconditions are read from this target's own
// capabilities only.
func declared(in Input, caps map[string]opsv1.CapabilityStatus) map[string][]opsv1.InteractionResponse {
	out := map[string][]opsv1.InteractionResponse{}
	if in.Profile == nil {
		return out
	}
	owners := map[string]map[string]bool{} // capability|action → owners
	for _, r := range in.Profile.Responses {
		if r.Target.Namespace != in.Namespace || r.Target.Name != in.Name || (r.Target.UID != "" && r.Target.UID != in.UID) {
			continue
		}
		k := typeKey(r.For)
		c, ok := caps[k]
		if !ok || !r.triggers(c.State) {
			continue
		}
		ir := opsv1.InteractionResponse{
			Name:             r.Action.String(),
			Target:           in.Namespace + "/" + in.Name + "#" + in.UID,
			For:              r.For,
			Owner:            r.Owner,
			RequiresApproval: r.RequiresApproval,
			Risks:            sortedCopy(r.Risks),
		}
		unproven, unmet := false, false
		for _, p := range r.Preconditions {
			code := PreUnproven
			if pc, ok := caps[typeKey(p)]; ok {
				switch pc.State {
				case "AVAILABLE":
					code = PreProven
				case "DEGRADED", "UNAVAILABLE":
					code = PreUnmet
				}
			}
			unproven = unproven || code == PreUnproven
			unmet = unmet || code == PreUnmet
			ir.Preconditions = append(ir.Preconditions, opsv1.StatusReason{Code: code, Subject: typeKey(p)})
		}
		sort.Slice(ir.Preconditions, func(i, j int) bool { return ir.Preconditions[i].Subject < ir.Preconditions[j].Subject })
		switch {
		case unmet:
			ir.Status = ResponseInapplicable
		case r.Owner == "":
			ir.Status = ResponseOwnerUndeclared
		case unproven:
			ir.Status = ResponseSafetyUnproven
		case r.RequiresApproval || len(r.Risks) > 0:
			ir.Status = ResponseApprovalRequired
		default:
			ir.Status = ResponseReady
		}
		ok2 := k + "|" + ir.Name
		if owners[ok2] == nil {
			owners[ok2] = map[string]bool{}
		}
		owners[ok2][r.Owner] = true
		out[k] = append(out[k], ir)
	}
	for k, rs := range out {
		for i := range rs {
			if len(owners[k+"|"+rs[i].Name]) > 1 && rs[i].Status != ResponseInapplicable {
				rs[i].Status = ResponseOwnerConflict
			}
		}
		sort.SliceStable(rs, func(i, j int) bool {
			if rs[i].Name != rs[j].Name {
				return rs[i].Name < rs[j].Name
			}
			return rs[i].Owner < rs[j].Owner
		})
		out[k] = dedupResponses(rs)
	}
	return out
}

func dedupResponses(rs []opsv1.InteractionResponse) []opsv1.InteractionResponse {
	var out []opsv1.InteractionResponse
	seen := map[string]bool{}
	for _, r := range rs {
		b, _ := json.Marshal(r)
		if seen[string(b)] {
			continue
		}
		seen[string(b)] = true
		out = append(out, r)
	}
	return out
}

func confirmed(st opsv1.OperationalTargetStatus) []opsv1.EvidenceStatus {
	var out []opsv1.EvidenceStatus
	for _, e := range st.Evidence {
		if e.State == "Current" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	if len(out) > maxFacts {
		out = out[:maxFacts]
	}
	return out
}

// missing lists slots without current evidence (state only: an evidence
// reference of a non-current statement is not a confirmed fact) and bindings
// dropped for lack of a grant.
func missing(st opsv1.OperationalTargetStatus) []opsv1.EvidenceStatus {
	var out []opsv1.EvidenceStatus
	for _, e := range st.Evidence {
		if e.State != "Current" {
			out = append(out, opsv1.EvidenceStatus{Slot: e.Slot, State: e.State, Provider: e.Provider, ConfigRevision: e.ConfigRevision})
		}
	}
	for _, dr := range st.DeniedReferences {
		out = append(out, opsv1.EvidenceStatus{Slot: dr.Slot, State: "BindingDenied:" + string(dr.Type)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		return out[i].State < out[j].State
	})
	if len(out) > maxFacts {
		out = out[:maxFacts]
	}
	return out
}

var candidateOrder = map[string]int{"Maintained": 0, "Open": 1, "Refuted": 2}

func candidates(inv *Investigation, open map[string]bool) []opsv1.InteractionCandidate {
	if inv == nil {
		return nil
	}
	var out []opsv1.InteractionCandidate
	for _, c := range relevant(inv.Candidates, open) {
		out = append(out, opsv1.InteractionCandidate{ID: c.ID, Kind: c.Kind, Ref: c.Ref, Status: c.Status})
	}
	sort.Slice(out, func(i, j int) bool {
		if candidateOrder[out[i].Status] != candidateOrder[out[j].Status] {
			return candidateOrder[out[i].Status] < candidateOrder[out[j].Status]
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxCandidates {
		out = out[:maxCandidates]
	}
	return out
}

// Investigation outcome classes. A completed investigation and a completed
// freshness refresh mean the same to an operator (every input decided), so
// both are Concluded; the episode kind is not shown.
var outcomeClass = map[string]string{
	"Active":         "InProgress",
	"Resolved":       "Concluded",
	"Refreshed":      "Concluded",
	"NoAllowedQuery": "NoAllowedQuery",
	"ExhaustedCalls": "BudgetExhausted",
	"ExhaustedSteps": "BudgetExhausted",
	"Deadline":       "BudgetExhausted",
	"Cancelled":      "Cancelled",
	"Capacity":       "CapacityLimited",
	"Superseded":     "Superseded",
	"TargetInvalid":  "Superseded",
}

func investigationOf(inv *Investigation, open map[string]bool) *opsv1.InteractionInvestigation {
	if inv == nil {
		return &opsv1.InteractionInvestigation{Outcome: "NotRun"}
	}
	out := &opsv1.InteractionInvestigation{Outcome: outcomeClass[inv.Outcome]}
	if out.Outcome == "" {
		out.Outcome = "Superseded"
	}
	for _, c := range relevant(inv.Candidates, open) {
		switch c.Status {
		case "Refuted":
			out.Refuted++
		case "Maintained":
			out.Maintained++
		case "Open":
			out.Open++
		}
	}
	return out
}

// relevant keeps the hypotheses about capabilities that are not AVAILABLE
// now. Hypotheses about a capability proven AVAILABLE are history, not an
// open question, and would otherwise change with every refresh.
func relevant(cs []Candidate, open map[string]bool) []Candidate {
	var out []Candidate
	for _, c := range cs {
		// Candidate IDs start with the capability key: "<domain>/<name>@<rev>/...".
		for k := range open {
			if strings.HasPrefix(c.ID, k+"/") {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// summaryLine states the operational problem in one deterministic line. It
// never claims recovery: AVAILABLE is stated as proven on current evidence.
func summaryLine(out *opsv1.InteractionStatus, total int) string {
	if total == 0 {
		return "no capability is assessed for this target"
	}
	var parts []string
	if n := len(out.Affected); n > 0 {
		parts = append(parts, fmt.Sprintf("%d affected (%s)", n, capList(out.Affected)))
	}
	if n := len(out.Unknown); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown (%s)", n, capList(out.Unknown)))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("all %d capabilities AVAILABLE on current evidence", total)
	}
	parts = append(parts, fmt.Sprintf("%d AVAILABLE on current evidence", len(out.Unaffected)))
	return strings.Join(parts, "; ")
}

func capList(cs []opsv1.InteractionCapability) string {
	var s []string
	for i, c := range cs {
		if i == 3 {
			s = append(s, fmt.Sprintf("+%d more", len(cs)-3))
			break
		}
		s = append(s, c.Type.Name+" "+c.State)
	}
	return strings.Join(s, ", ")
}

// finish sets the level, its reasons and the bounded recurrence history.
// The history belongs to one identity: a changed identity starts empty, so no
// earlier meaning, request or response of another identity is reused.
func finish(out *opsv1.InteractionStatus, d *decision, prev *opsv1.InteractionStatus) *opsv1.InteractionStatus {
	out.Level = d.level
	out.LevelReasons = limit(dedupReasons(d.reasons), maxReasons)
	out.HumanReasons = limit(dedupReasons(d.human), maxReasons)
	out.Fingerprint = fingerprint(out)
	out.RecentFingerprints = []string{out.Fingerprint}
	if prev == nil || prev.IdentityDigest != out.IdentityDigest {
		return out
	}
	out.Recurrences = prev.Recurrences
	if prev.Fingerprint == out.Fingerprint {
		out.RecentFingerprints = append([]string(nil), prev.RecentFingerprints...)
		out.Recurring = prev.Recurring
		return out
	}
	for _, f := range prev.RecentFingerprints {
		if f == out.Fingerprint {
			out.Recurring = true
			if out.Recurrences < 1<<30 {
				out.Recurrences++
			}
			continue
		}
		if len(out.RecentFingerprints) < maxHistory {
			out.RecentFingerprints = append(out.RecentFingerprints, f)
		}
	}
	return out
}

// fingerprint hashes the meaning of the summary: identity, level and its
// reasons, which capabilities are in which state, which evidence is missing,
// the declared responses' status and the human reasons. Evidence references,
// investigation progress and summary wording are excluded.
func fingerprint(out *opsv1.InteractionStatus) string {
	type capState struct{ T, S string }
	m := struct {
		ID       string
		Level    opsv1.InteractionLevel
		Reasons  []opsv1.StatusReason
		Caps     []capState
		Missing  []opsv1.EvidenceStatus
		Response []string
		Human    []opsv1.StatusReason
	}{ID: out.IdentityDigest, Level: out.Level, Reasons: out.LevelReasons, Missing: out.MissingEvidence, Human: out.HumanReasons}
	for _, l := range [][]opsv1.InteractionCapability{out.Affected, out.Unknown, out.Unaffected} {
		for _, c := range l {
			m.Caps = append(m.Caps, capState{typeKey(c.Type), c.State})
		}
	}
	for _, r := range out.Responses {
		m.Response = append(m.Response, typeKey(r.For)+"|"+r.Name+"|"+r.Owner+"|"+r.Status)
	}
	b, _ := json.Marshal(m)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// identityDigest hashes the exact identity the summary is for: target,
// resolved workload, contract identity, every provider binding and its
// configuration revision, dropped bindings, and the profile revision.
func identityDigest(in Input) string {
	st := in.Status
	parts := []string{in.UID, st.Identity.TargetUID, st.Identity.ResolvedUID, st.Identity.ContractUID, st.Identity.ContractSpecDigest}
	var b []string
	for _, e := range st.Evidence {
		b = append(b, "e|"+e.Slot+"|"+e.Provider+"|"+e.ConfigRevision)
	}
	for _, dr := range st.DeniedReferences {
		b = append(b, "d|"+string(dr.Type)+"|"+dr.Slot+"|"+dr.Namespace)
	}
	sort.Strings(b)
	if in.Profile != nil {
		b = append(b, "p|"+in.Profile.Revision)
	}
	sum := sha256.Sum256([]byte(strings.Join(append(parts, b...), "\x00")))
	return hex.EncodeToString(sum[:16])
}

func typeKey(c opsv1.CapabilityType) string {
	return c.Domain + "/" + c.Name + "@" + c.Revision
}

func sortCaps(cs []opsv1.InteractionCapability) {
	sort.Slice(cs, func(i, j int) bool { return typeKey(cs[i].Type) < typeKey(cs[j].Type) })
}

func limitCaps(cs []opsv1.InteractionCapability) []opsv1.InteractionCapability {
	if len(cs) > maxCaps {
		return cs[:maxCaps]
	}
	return cs
}

func limit(rs []opsv1.StatusReason, n int) []opsv1.StatusReason {
	if len(rs) > n {
		return append([]opsv1.StatusReason(nil), rs[:n]...)
	}
	return rs
}

// dedupReasons removes repeats and orders reasons deterministically,
// strongest-first by code then subject.
func dedupReasons(rs []opsv1.StatusReason) []opsv1.StatusReason {
	seen := map[opsv1.StatusReason]bool{}
	var out []opsv1.StatusReason
	for _, r := range rs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func joinCodes(rs []opsv1.StatusReason, n int) string {
	var s []string
	for i, r := range rs {
		if i == n {
			s = append(s, fmt.Sprintf("+%d more", len(rs)-n))
			break
		}
		s = append(s, r.Code)
	}
	if len(s) == 0 {
		return "no reason reported"
	}
	return strings.Join(s, ", ")
}

func clip(s string) string {
	if len(s) <= maxSummary {
		return s
	}
	s = s[:maxSummary-3]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "..."
}

func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
