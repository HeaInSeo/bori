package investigate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// EpisodeState is the lifecycle state of an episode.
type EpisodeState string

const (
	Active EpisodeState = "Active"
	// Terminal states.
	Resolved       EpisodeState = "Resolved"       // every candidate decided
	Refreshed      EpisodeState = "Refreshed"      // freshness work done
	NoAllowedQuery EpisodeState = "NoAllowedQuery" // open candidates, nothing queryable
	ExhaustedCalls EpisodeState = "ExhaustedCalls"
	ExhaustedSteps EpisodeState = "ExhaustedSteps"
	DeadlineHit    EpisodeState = "Deadline"
	Cancelled      EpisodeState = "Cancelled"
	Superseded     EpisodeState = "Superseded" // identity/binding changed
	TargetInvalid  EpisodeState = "TargetInvalid"
	CapacityHit    EpisodeState = "Capacity"
)

// EpisodeKind distinguishes investigation from periodic freshness.
type EpisodeKind string

const (
	KindInvestigate EpisodeKind = "investigate"
	KindRefresh     EpisodeKind = "refresh"
)

// TraceEntry is one bounded, replayable planner record. It names only the
// target's own slots and candidates.
type TraceEntry struct {
	Step   int
	Action string // select | result | late | candidate | end
	Slot   string
	Detail string
}

// Episode is one bounded investigation of one target identity.
//
// Key: OperationalTarget UID + resolved UID + exact contract identity + the
// authorized (slot, provider identity) bindings. Any change supersedes the
// episode; held evidence for the old key is purged and never applied.
type Episode struct {
	Key        string
	TargetUID  string
	Kind       EpisodeKind
	Started    time.Time
	Deadline   time.Time
	Calls      int
	Steps      int
	State      EpisodeState
	Candidates []Candidate
	Trace      []TraceEntry
	Truncated  int
	dispatched map[string]bool
}

func (e *Episode) active() bool { return e.State == Active }

func (e *Episode) trace(lim int, t TraceEntry) {
	if len(e.Trace) >= lim {
		e.Truncated++
		return
	}
	t.Step = e.Steps
	e.Trace = append(e.Trace, t)
}

// Stats are cumulative I/O and admission counters.
type Stats struct {
	Dispatched      int
	LateDiscarded   int
	AdmissionDenied int
	CapacityDenied  int
	EpisodesStarted int
}

// Investigator runs episodes and holds evidence. It is process-local: a
// restart starts from no held evidence (UNKNOWN) and no episode history; it
// claims no durable incident or budget continuity across restarts.
type Investigator struct {
	limits Limits
	clock  func() time.Time

	mu   sync.Mutex
	held map[operations.ApplicabilityKey][]operations.Observation
	// marks is the ObservedAt of the last accepted value per full key. It
	// survives provider outages so an older or replayed value can never
	// clear an outage, and it is purged with the key's held evidence.
	marks    map[operations.ApplicabilityKey]time.Time
	episodes map[string]*Episode // by OperationalTarget UID
	stats    Stats
}

// New returns an investigator with the given limits and clock.
func New(l Limits, clock func() time.Time) *Investigator {
	return &Investigator{
		limits:   l,
		clock:    clock,
		held:     map[operations.ApplicabilityKey][]operations.Observation{},
		marks:    map[operations.ApplicabilityKey]time.Time{},
		episodes: map[string]*Episode{},
	}
}

// Run advances investigation for every target with authorized queries.
// base is the O1 snapshot from opswire.Build (no observations); only targets
// O1 assesses as valid are investigated. Calls are strictly sequential.
//
// Snapshot boundary: grants, identities and bindings are those of base,
// taken at the start of the reconcile. A grant revoked or a target replaced
// while a call is in flight takes effect on the next Run, which purges every
// held observation whose key is no longer authorized; a result for a
// superseded key is therefore never applied. Remote side effects of a call
// already sent cannot be undone and none are expected (all calls are reads).
func (iv *Investigator) Run(ctx context.Context, base operations.Snapshot, qs Queries) {
	iv.mu.Lock()
	defer iv.mu.Unlock()

	// Only O1-valid targets authorize held evidence. An invalid target's
	// keys and watermarks are purged here, so correcting its spec later
	// needs a fresh provider call and it never holds cache capacity.
	qs = iv.validOnly(base, qs)
	iv.purge(qs)
	runCtx, cancel := context.WithTimeout(ctx, iv.limits.RunSlice)
	defer cancel()

	uids := make([]string, 0, len(qs))
	for uid := range qs {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		if ctx.Err() != nil || runCtx.Err() != nil {
			break
		}
		iv.investigate(ctx, runCtx, base, uid, qs[uid])
	}
}

// validOnly drops queries of targets O1 assesses as invalid (validity is
// structural; it does not depend on evidence) and ends their active
// episodes as TargetInvalid.
func (iv *Investigator) validOnly(base operations.Snapshot, qs Queries) Queries {
	a := iv.evaluate(base, iv.clock())
	out := Queries{}
	for uid, slots := range qs {
		if ta, ok := a.Target(uid); ok && ta.Valid {
			out[uid] = slots
			continue
		}
		if e, ok := iv.episodes[uid]; ok && e.active() {
			iv.end(e, TargetInvalid, "")
		}
	}
	return out
}

// Observations returns all held evidence in a deterministic order.
func (iv *Investigator) Observations() []operations.Observation {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	keys := make([]operations.ApplicabilityKey, 0, len(iv.held))
	for k := range iv.held {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keyString(keys[i]) < keyString(keys[j]) })
	var out []operations.Observation
	for _, k := range keys {
		out = append(out, iv.held[k]...)
	}
	return out
}

// Stats returns a copy of the counters.
func (iv *Investigator) Stats() Stats {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	return iv.stats
}

// Episode returns a copy of the latest episode of a target.
func (iv *Investigator) Episode(targetUID string) (Episode, bool) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	e, ok := iv.episodes[targetUID]
	if !ok {
		return Episode{}, false
	}
	c := *e
	c.Candidates = append([]Candidate(nil), e.Candidates...)
	c.Trace = append([]TraceEntry(nil), e.Trace...)
	return c, true
}

// NextWake is the earliest future time at which a reconcile can do new work:
// held evidence of a queryable target becoming due for refresh — or, when
// that refresh falls due inside the target's cooldown, the cooldown end, the
// first moment the refresh can be admitted — or the cooldown end of a
// queryable target whose last episode stopped with work left (budget,
// deadline, cancellation or capacity). Elapsed times, targets that are no
// longer queryable and episodes that finished their work never produce a
// wake, so they cannot pin the requeue to its floor. Zero means nothing is
// pending.
func (iv *Investigator) NextWake(qs Queries, now time.Time) time.Time {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	var next time.Time
	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for uid, slots := range qs {
		var admissible time.Time
		if e, ok := iv.episodes[uid]; ok {
			admissible = e.Started.Add(iv.limits.EpisodeCooldown)
		}
		for _, q := range slots {
			if due, ok := iv.refreshDue(q); ok {
				if admissible.After(due) {
					due = admissible
				}
				consider(due)
			}
		}
	}
	for uid, e := range iv.episodes {
		if _, queryable := qs[uid]; queryable && workLeft(e.State) {
			consider(e.Started.Add(iv.limits.EpisodeCooldown))
		}
	}
	return next
}

// workLeft reports whether an episode ended before its work was done, so a
// new episode after the cooldown may make progress.
func workLeft(s EpisodeState) bool {
	switch s {
	case ExhaustedCalls, ExhaustedSteps, DeadlineHit, Cancelled, CapacityHit:
		return true
	}
	return false
}

// purge drops held evidence whose key is not currently authorized and
// supersedes active episodes of targets that are no longer queryable.
func (iv *Investigator) purge(qs Queries) {
	authorized := map[operations.ApplicabilityKey]bool{}
	for _, slots := range qs {
		for _, q := range slots {
			authorized[q.Request.Key] = true
		}
	}
	for k := range iv.held {
		if !authorized[k] {
			delete(iv.held, k)
			delete(iv.marks, k)
		}
	}
	now := iv.clock()
	for uid, e := range iv.episodes {
		if _, ok := qs[uid]; ok {
			continue
		}
		if e.active() {
			iv.end(e, Superseded, "target no longer queryable")
		}
		// A record whose cooldown has elapsed no longer restricts admission;
		// forgetting it grants no allowance and bounds memory.
		if !now.Before(e.Started.Add(iv.limits.EpisodeCooldown)) {
			delete(iv.episodes, uid)
		}
	}
}

func (iv *Investigator) evaluate(base operations.Snapshot, now time.Time) operations.Assessment {
	s := base
	s.At = now
	var obs []operations.Observation
	for _, list := range iv.held {
		obs = append(obs, list...)
	}
	s.Observations = obs
	a, _ := operations.Evaluate(s)
	return a
}

func (iv *Investigator) investigate(ctx, runCtx context.Context, base operations.Snapshot, uid string, slots map[string]Query) {
	now := iv.clock()
	ta, ok := iv.evaluate(base, now).Target(uid)
	ep := iv.episodes[uid]
	if !ok || !ta.Valid {
		if ep != nil && ep.active() {
			iv.end(ep, TargetInvalid, "")
		}
		return
	}
	contract, ok := contractOf(base, ta.Contract)
	if !ok {
		return
	}
	key := episodeKey(ta, slots)

	if ep != nil && ep.active() && ep.Key != key {
		iv.end(ep, Superseded, "identity or bindings changed")
	}
	if ep == nil || !ep.active() {
		cands := deriveCandidates(ta, contract, iv.limits.MaxCandidates)
		q, class := iv.selectQuery(ta, contract, cands, slots, nil, now)
		if q == nil {
			return
		}
		// Admission: one episode start per target UID per cooldown, whatever
		// the identity or prior outcome; repeated reconciles, self status
		// events or new timestamps grant no new allowance.
		if ep != nil && now.Before(ep.Started.Add(iv.limits.EpisodeCooldown)) {
			iv.stats.AdmissionDenied++
			return
		}
		if ep == nil && len(iv.episodes) >= iv.limits.MaxResidentEpisodes {
			iv.pruneEpisodes(now)
			if len(iv.episodes) >= iv.limits.MaxResidentEpisodes {
				iv.stats.CapacityDenied++
				return
			}
		}
		kind := KindInvestigate
		if class == classRefresh {
			kind = KindRefresh
		}
		ep = &Episode{
			Key: key, TargetUID: uid, Kind: kind, State: Active,
			Started: now, Deadline: now.Add(iv.limits.EpisodeDeadline),
			dispatched: map[string]bool{},
		}
		iv.episodes[uid] = ep
		iv.stats.EpisodesStarted++
	}

	for ep.active() {
		now = iv.clock()
		switch {
		case ctx.Err() != nil:
			iv.end(ep, Cancelled, "")
			return
		case runCtx.Err() != nil:
			return // slice over: keep the episode and its remaining budget
		case !now.Before(ep.Deadline):
			iv.end(ep, DeadlineHit, "")
			return
		case ep.Steps >= iv.limits.MaxStepsPerEpisode:
			iv.end(ep, ExhaustedSteps, "")
			return
		}
		ta, ok = iv.evaluate(base, now).Target(uid)
		if !ok || !ta.Valid {
			iv.end(ep, TargetInvalid, "")
			return
		}
		iv.updateCandidates(ep, deriveCandidates(ta, contract, iv.limits.MaxCandidates))
		q, class := iv.selectQuery(ta, contract, ep.Candidates, slots, ep.dispatched, now)
		if q == nil {
			switch {
			case hasOpen(ep.Candidates):
				iv.end(ep, NoAllowedQuery, "")
			case ep.Kind == KindRefresh:
				iv.end(ep, Refreshed, "")
			default:
				iv.end(ep, Resolved, "")
			}
			return
		}
		if ep.Calls >= iv.limits.MaxCallsPerEpisode {
			iv.end(ep, ExhaustedCalls, "")
			return
		}
		slot := q.Request.Key.Slot
		ep.Steps++
		ep.Calls++
		ep.dispatched[slot] = true
		iv.stats.Dispatched++
		ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "select", Slot: slot, Detail: class.reason(ep.Candidates, slot)})

		timeout := iv.limits.PerCallTimeout
		if rem := ep.Deadline.Sub(now); rem < timeout {
			timeout = rem
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		res := q.Provider.Observe(callCtx, q.Request)
		callErr := callCtx.Err()
		cancel()
		after := iv.clock()

		if ctx.Err() != nil {
			// Shutdown: whatever came back is discarded, nothing recorded.
			iv.stats.LateDiscarded++
			ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "late", Slot: slot, Detail: "cancelled"})
			iv.end(ep, Cancelled, "")
			return
		}
		// Timed out by the context (real I/O) or by the evaluation clock.
		if callErr != nil || after.Sub(now) > timeout || !after.Before(ep.Deadline) {
			// The provider did not answer in time: any value it returned is
			// late and never applied; the slot's latest statement becomes
			// provider-unavailable so no older value is fallen back to.
			if res.Unavailable == "" {
				iv.stats.LateDiscarded++
				ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "late", Slot: slot, Detail: "result after deadline discarded"})
			}
			res = providers.Result{Unavailable: "timeout"}
		}
		// A producer stamp later than our own receipt is not trusted: it
		// would shadow every later statement and turn current only once the
		// clock caught up. It is rejected, never clamped or restamped.
		if res.Unavailable == "" && res.ObservedAt.After(after) {
			res = providers.Result{Unavailable: "future-observedAt"}
		}
		obs := providers.Observation(q.Request, res, after)
		if !iv.admit(obs) {
			iv.end(ep, CapacityHit, "")
			return
		}
		ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "result", Slot: slot, Detail: resultDetail(res)})
	}
}

type queryClass int

const (
	classInvestigate queryClass = iota
	classRefresh
)

func (c queryClass) reason(cands []Candidate, slot string) string {
	if c == classRefresh {
		return "refresh: held evidence due before expiry"
	}
	var ids []string
	for _, cd := range cands {
		if cd.Kind == KindAssertion && cd.Ref == slot && cd.Status == Open {
			ids = append(ids, cd.ID)
		}
	}
	return fmt.Sprintf("investigate: decides %d open candidate(s) %s", len(ids), strings.Join(ids, ","))
}

// selectQuery picks the next single query, or nil. Investigation queries —
// slots without current evidence that would decide at least one Open
// candidate — rank first by the number of Open candidates they decide, then
// by slot name. Only when none exists is a freshness query chosen: a slot
// with current evidence that is due before expiry. A slot is queried at most
// once per episode. Slots with current, not-yet-due evidence and slots no
// candidate needs are never queried.
func (iv *Investigator) selectQuery(ta operations.TargetAssessment, c operations.Contract, cands []Candidate, slots map[string]Query, dispatched map[string]bool, now time.Time) (*Query, queryClass) {
	evidence := map[string]operations.EvidenceStatus{}
	for _, e := range ta.Evidence {
		evidence[e.Slot] = e.Status
	}
	openBySlot := map[string]int{}
	for _, cd := range cands {
		if cd.Kind == KindAssertion && cd.Status == Open {
			openBySlot[cd.Ref]++
		}
	}
	used := usedSlots(c, ta.EnvelopeSelection)
	names := make([]string, 0, len(slots))
	for s := range slots {
		names = append(names, s)
	}
	sort.Strings(names)

	best, bestScore := "", 0
	for _, s := range names {
		if dispatched[s] || evidence[s] == operations.EvidenceCurrent {
			continue
		}
		if n := openBySlot[s]; n > bestScore {
			best, bestScore = s, n
		}
	}
	if best != "" {
		q := slots[best]
		return &q, classInvestigate
	}

	for _, s := range names {
		if dispatched[s] || evidence[s] != operations.EvidenceCurrent {
			continue
		}
		if !used[s] {
			continue
		}
		q := slots[s]
		if due, ok := iv.refreshDue(q); ok && !now.Before(due) {
			return &q, classRefresh
		}
	}
	return nil, classInvestigate
}

// usedSlots are the slots some capability predicate or the selected
// envelope reads. Only these are ever refreshed; a bound slot nothing reads
// is never queried.
func usedSlots(c operations.Contract, selected string) map[string]bool {
	used := map[string]bool{}
	for _, cp := range c.Capabilities {
		for _, r := range cp.Requirements {
			if r.Predicate != nil {
				used[r.Predicate.Assertion] = true
			}
		}
	}
	for _, e := range c.Envelopes {
		referenced := e.Name == selected
		for _, cp := range c.Capabilities {
			for _, r := range cp.Requirements {
				if r.Envelope == e.Name {
					referenced = true
				}
			}
		}
		if referenced {
			for _, p := range e.Requirements {
				used[p.Assertion] = true
			}
		}
	}
	return used
}

// refreshDue returns when held value evidence for q is due for refresh.
func (iv *Investigator) refreshDue(q Query) (time.Time, bool) {
	list := iv.held[q.Request.Key]
	if len(list) == 0 || list[0].Outcome != operations.OutcomeValue || q.MaxAge <= 0 {
		return time.Time{}, false
	}
	o := list[0]
	expiry := o.ObservedAt.Add(q.MaxAge)
	if !o.ValidUntil.IsZero() && o.ValidUntil.Before(expiry) {
		expiry = o.ValidUntil
	}
	margin := time.Duration(float64(q.MaxAge) * iv.limits.RefreshFraction)
	return expiry.Add(-margin), true
}

// admit stores an observation for its key. Future-stamped values never get
// here (rejected at receipt), so every held stamp is at or before the time
// it was received.
//
//   - A provider-unavailable statement (stamped with the local receipt time)
//     always replaces the held evidence: there is no fallback to an older
//     value. It does not touch the key's value watermark.
//   - A value is ordered against the watermark — the ObservedAt of the last
//     accepted value for this key — not against whatever is currently held,
//     so an outage in between cannot erase that ordering (API F9 / O1 S05):
//     a first-ever or strictly newer value replaces the held evidence
//     (clearing an outage) and advances the watermark; an equal stamp with a
//     different value, while that value is still held, is held beside it so
//     O1 reports a conflict; an older value, or an equal replay after an
//     outage, is ignored.
//
// A new key beyond capacity is refused (conservative: the slot stays
// UNKNOWN) — nothing is evicted. The watermark lives and dies with the key.
func (iv *Investigator) admit(o operations.Observation) bool {
	list, ok := iv.held[o.Key]
	if !ok {
		if len(iv.held) >= iv.limits.MaxHeldObservations {
			iv.stats.CapacityDenied++
			return false
		}
		iv.held[o.Key] = []operations.Observation{o}
		if o.Outcome == operations.OutcomeValue {
			iv.marks[o.Key] = o.ObservedAt
		}
		return true
	}
	if o.Outcome != operations.OutcomeValue {
		iv.held[o.Key] = []operations.Observation{o}
		return true
	}
	mark, hasMark := iv.marks[o.Key]
	switch {
	case !hasMark || o.ObservedAt.After(mark):
		iv.held[o.Key] = []operations.Observation{o}
		iv.marks[o.Key] = o.ObservedAt
	case o.ObservedAt.Equal(mark) && list[0].Outcome == operations.OutcomeValue && list[0].ObservedAt.Equal(mark):
		for _, h := range list {
			if h.Value == o.Value {
				return true
			}
		}
		if len(list) < 4 {
			iv.held[o.Key] = append(list, o)
		}
	}
	return true
}

func (iv *Investigator) updateCandidates(ep *Episode, cands []Candidate) {
	prev := map[string]CandidateStatus{}
	for _, c := range ep.Candidates {
		prev[c.ID] = c.Status
	}
	for _, c := range cands {
		if p, ok := prev[c.ID]; ok && p != c.Status {
			ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "candidate", Slot: c.Ref, Detail: c.ID + ": " + string(p) + "→" + string(c.Status)})
		}
	}
	// A candidate whose capability became AVAILABLE disappears from the
	// derived set: every input it could name is now proven accepted.
	for id, p := range prev {
		if !containsID(cands, id) && p != Refuted {
			ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "candidate", Detail: id + ": " + string(p) + "→Refuted (capability proven available)"})
			cands = append(cands, Candidate{ID: id, Status: Refuted})
		} else if !containsID(cands, id) {
			cands = append(cands, Candidate{ID: id, Status: Refuted})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].ID < cands[j].ID })
	if len(cands) > iv.limits.MaxCandidates {
		cands = cands[:iv.limits.MaxCandidates]
	}
	ep.Candidates = cands
}

func containsID(cs []Candidate, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

func hasOpen(cs []Candidate) bool {
	for _, c := range cs {
		if c.Status == Open {
			return true
		}
	}
	return false
}

func (iv *Investigator) end(ep *Episode, st EpisodeState, detail string) {
	ep.State = st
	ep.trace(iv.limits.MaxTraceEntries, TraceEntry{Action: "end", Detail: strings.TrimSpace(string(st) + " " + detail)})
}

// pruneEpisodes forgets terminal episodes whose cooldown has elapsed; they no
// longer restrict admission, so forgetting them grants no extra allowance.
func (iv *Investigator) pruneEpisodes(now time.Time) {
	for uid, e := range iv.episodes {
		if !e.active() && !now.Before(e.Started.Add(iv.limits.EpisodeCooldown)) {
			delete(iv.episodes, uid)
		}
	}
}

func resultDetail(r providers.Result) string {
	if r.Unavailable != "" {
		return "unavailable: " + r.Unavailable
	}
	return "value " + string(r.Value.Type)
}

func episodeKey(ta operations.TargetAssessment, slots map[string]Query) string {
	parts := []string{ta.Target.UID}
	var resolved string
	for _, q := range slots {
		resolved = q.Request.Key.ResolvedUID
		parts = append(parts, q.Request.Key.Slot+"="+q.Request.Key.Provider.String())
	}
	sort.Strings(parts[1:])
	return strings.Join(append([]string{resolved, ta.Contract.String()}, parts...), "|")
}

func contractOf(s operations.Snapshot, id operations.ContractIdentity) (operations.Contract, bool) {
	for _, c := range s.Contracts {
		if c.Identity == id {
			return c, true
		}
	}
	return operations.Contract{}, false
}

func keyString(k operations.ApplicabilityKey) string {
	return k.Target.String() + "|" + k.ResolvedUID + "|" + k.Contract.String() + "|" + k.Slot + "|" + k.Provider.String()
}
