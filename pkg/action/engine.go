package action

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Engine runs one step of the O5 flow per call. It is the only writer of
// its journal records while it holds the newest epoch; an engine with an
// older epoch fails compare-and-swap and is rejected by fenced providers.
type Engine struct {
	Journal   Journal
	Providers Registry
	// Decisions and Verifier supply human approvals. With either nil, no
	// human decision is ever counted.
	Decisions DecisionSource
	Verifier  Verifier

	mu    sync.Mutex
	epoch uint64
	stats Stats
}

// Stats counts what the engine did; tests use them as attack evidence.
type Stats struct {
	Submits          int
	Resends          int
	Adopted          int
	IgnoredResponses int
	JournalFailures  int
	SendFailures     int
	Takeovers        int
	DecisionFailures int
}

// Stats returns a copy of the counters.
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// Input is one step's input: the snapshot that was evaluated (with its
// evaluation instant and observations), its O1 assessment, the declared
// profile and the current time.
type Input struct {
	Snapshot   operations.Snapshot
	Assessment operations.Assessment
	Profile    *interaction.Profile
	Now        time.Time
}

// Output is one step's result.
type Output struct {
	// Views are the proposals and executions of each target UID.
	Views map[string][]View
	// NextWake is when a deadline, resend or recovery window needs a step;
	// zero if none.
	NextWake time.Time
}

type step struct {
	e         *Engine
	ctx       context.Context
	in        Input
	recs      map[string]*Record // by key
	props     map[string]Proposal
	decisions []Decision
	wake      time.Time
}

// Step advances every execution and proposal once.
func (e *Engine) Step(ctx context.Context, in Input) (Output, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.epoch == 0 {
		ep, err := e.Journal.Epoch(ctx)
		if err != nil {
			return Output{}, fmt.Errorf("journal epoch: %w", err)
		}
		e.epoch = ep
	}
	list, err := e.Journal.List(ctx)
	if err != nil {
		return Output{}, fmt.Errorf("journal list: %w", err)
	}
	st := &step{e: e, ctx: ctx, in: in, recs: map[string]*Record{}, props: map[string]Proposal{}}
	for i := range list {
		r := list[i]
		st.recs[r.Key] = &r
	}
	if e.Decisions != nil {
		if st.decisions, err = e.Decisions.Decisions(ctx); err != nil {
			e.stats.DecisionFailures++
			st.decisions = nil
		}
	}
	sort.Slice(st.decisions, func(i, j int) bool { return st.decisions[i].ID < st.decisions[j].ID })

	props := derive(in.Snapshot, in.Assessment, in.Profile, st.episode)
	for _, p := range props {
		st.props[p.ID] = p
	}
	for _, k := range st.keys() {
		if r := st.recs[k]; r.Phase.inFlight() {
			st.advance(r)
		}
	}
	for _, k := range st.keys() {
		if r := st.recs[k]; r.Phase.ended() && !r.Recovery.final() {
			st.recover(r)
		}
	}

	out := Output{Views: map[string][]View{}}
	shown := map[string]bool{} // subjects with a current proposal
	for _, p := range props {
		v := st.decide(p)
		out.Views[p.Binding.Target.UID] = append(out.Views[p.Binding.Target.UID], v)
		shown[p.Subject] = true
	}
	for _, r := range st.latestPerSubject() {
		if shown[r.Subject] {
			continue
		}
		if _, ok := targetOf(in.Snapshot, r.TargetUID); !ok {
			continue
		}
		out.Views[r.TargetUID] = append(out.Views[r.TargetUID], st.recordView(r))
	}
	for uid := range out.Views {
		vs := out.Views[uid]
		sort.SliceStable(vs, func(i, j int) bool { return vs[i].ProposalID < vs[j].ProposalID })
	}
	out.NextWake = st.wake
	return out, nil
}

func (st *step) keys() []string {
	ks := make([]string, 0, len(st.recs))
	for k := range st.recs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// episode is one plus the number of earlier proposals of the subject whose
// execution was confirmed Recovered.
func (st *step) episode(subject string) int {
	done := map[string]bool{}
	for _, r := range st.recs {
		if r.Subject == subject && r.Recovery == RecoveryRecovered {
			done[r.ProposalID] = true
		}
	}
	return len(done) + 1
}

func (st *step) attempts(proposalID string) []*Record {
	var out []*Record
	for _, k := range st.keys() {
		if r := st.recs[k]; r.ProposalID == proposalID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attempt < out[j].Attempt })
	return out
}

func (st *step) latestPerSubject() []*Record {
	latest := map[string]*Record{}
	for _, k := range st.keys() {
		r := st.recs[k]
		if cur, ok := latest[r.Subject]; !ok || r.CreatedAt.After(cur.CreatedAt) ||
			(r.CreatedAt.Equal(cur.CreatedAt) && r.Key > cur.Key) {
			latest[r.Subject] = r
		}
	}
	var out []*Record
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (st *step) busy(targetUID, proposalID string) bool {
	for _, r := range st.recs {
		if r.TargetUID == targetUID && r.ProposalID != proposalID && r.Phase.inFlight() {
			return true
		}
	}
	return false
}

func (st *step) wakeAt(t time.Time) {
	if !t.After(st.in.Now) {
		return
	}
	if st.wake.IsZero() || t.Before(st.wake) {
		st.wake = t
	}
}

// put persists r by compare-and-swap. On failure r is left unchanged in
// the step and the caller must not act on the change (no send).
func (st *step) put(r *Record, next Record) bool {
	stored, err := st.e.Journal.Put(st.ctx, next)
	if err != nil {
		st.e.stats.JournalFailures++
		return false
	}
	*r = stored
	return true
}

// own rewrites an in-flight record with this engine's epoch before acting
// on it (takeover after a restart). False if another writer got there.
func (st *step) own(r *Record) bool {
	if r.Fence >= st.e.epoch {
		return r.Fence == st.e.epoch
	}
	next := *r
	next.Fence = st.e.epoch
	if !st.put(r, next) {
		return false
	}
	st.e.stats.Takeovers++
	return true
}

func matches(r *Record, key string, fence uint64, digest string) bool {
	return key == r.Key && fence == r.SentFence && digest == r.Digest
}

// advance moves one in-flight record using only keyed provider answers and
// BORI's clock.
func (st *step) advance(r *Record) {
	if !st.own(r) {
		return
	}
	now := st.in.Now
	reg, registered := st.e.Providers[r.Provider]
	if registered {
		res, err := reg.Provider.Result(st.ctx, r.Key)
		switch {
		case err != nil || res.State == ResultUnknown:
		case !matches(r, res.Key, res.Fence, res.Digest):
			st.e.stats.IgnoredResponses++
		default:
			if st.adopt(r, res) {
				return
			}
		}
	}
	next := *r
	switch r.Phase {
	case PhaseDispatching:
		if !now.Before(r.CreatedAt.Add(r.AckTimeout)) {
			next.Phase, next.CompletedAt = PhaseTimedOut, now
			st.put(r, next)
			return
		}
		st.wakeAt(r.CreatedAt.Add(r.AckTimeout))
		interval := r.AckTimeout / time.Duration(r.MaxSends)
		if r.Sends >= r.MaxSends || now.Before(r.LastSentAt.Add(interval)) {
			st.wakeAt(r.LastSentAt.Add(interval))
			return
		}
		p, ok := st.props[r.ProposalID]
		if !ok || p.Digest != r.Digest || !registered || len(st.gates(p, true)) > 0 || !st.stillApproved(p, r.Approval) {
			next.Phase, next.CompletedAt = PhaseWithdrawn, now
			st.put(r, next)
			return
		}
		next.Sends++
		next.LastSentAt = now
		next.SentFence = next.Fence
		if !st.put(r, next) {
			return
		}
		st.e.stats.Resends++
		st.send(r, reg)
	case PhaseAccepted:
		if !now.Before(r.AckedAt.Add(r.CompletionTimeout)) {
			next.Phase, next.CompletedAt = PhaseTimedOut, now
			st.put(r, next)
			return
		}
		st.wakeAt(r.AckedAt.Add(r.CompletionTimeout))
	}
}

// adopt applies a matching provider answer; Pending acknowledges.
func (st *step) adopt(r *Record, res Result) bool {
	now := st.in.Now
	next := *r
	switch res.State {
	case ResultPending:
		if r.Phase == PhaseAccepted {
			return false
		}
		next.Phase, next.AckedAt = PhaseAccepted, now
	case ResultSucceeded, ResultFailed:
		next.Phase = PhaseSucceeded
		if res.State == ResultFailed {
			next.Phase = PhaseFailed
		}
		if next.AckedAt.IsZero() {
			next.AckedAt = now
		}
		next.CompletedAt = now
	default:
		return false
	}
	next.ProviderRef = res.Ref
	if !st.put(r, next) {
		return false
	}
	st.e.stats.Adopted++
	if r.Phase == PhaseAccepted {
		st.wakeAt(r.AckedAt.Add(r.CompletionTimeout))
	}
	return true
}

func (st *step) send(r *Record, reg Registration) {
	st.e.stats.Submits++
	rc, err := reg.Provider.Submit(st.ctx, Handoff{
		Key: r.Key, Fence: r.SentFence, ProposalID: r.ProposalID, Digest: r.Digest, Action: r.Action,
		Owner: r.Owner, Target: r.Binding.Target, ResolvedUID: r.Binding.ResolvedUID, Contract: r.Binding.Contract,
		For: r.For, ExpectedImpact: append([]operations.CapabilityType(nil), r.Expected...), Approval: r.Approval,
	})
	if err != nil {
		st.e.stats.SendFailures++ // stays Dispatching: polled, resent or timed out later
		st.wakeAt(r.LastSentAt.Add(r.AckTimeout / time.Duration(r.MaxSends)))
		return
	}
	if !matches(r, rc.Key, rc.Fence, rc.Digest) {
		st.e.stats.IgnoredResponses++
		return
	}
	next := *r
	now := st.in.Now
	if rc.Accepted {
		next.Phase, next.AckedAt, next.ProviderRef = PhaseAccepted, now, rc.Ref
	} else {
		// An explicit refusal is definitive: nothing was accepted.
		next.Phase, next.AckedAt, next.CompletedAt, next.ProviderRef = PhaseFailed, now, now, rc.Ref
	}
	if st.put(r, next) && r.Phase == PhaseAccepted {
		st.wakeAt(now.Add(r.CompletionTimeout))
	}
}

func (st *step) recover(r *Record) {
	rec, detail, err := assessRecovery(*r, st.in.Snapshot, st.in.Now)
	if err != nil {
		return
	}
	if !rec.final() {
		r.Recovery, r.RecoveryDetail = rec, detail // shown, not persisted
		st.wakeAt(r.CompletedAt.Add(r.RecoveryWindow))
		return
	}
	next := *r
	next.Recovery, next.RecoveryDetail = rec, detail
	st.put(r, next)
}

// gates are the execution conditions, all on the current assessment.
// resend skips target-busy (the record itself is the in-flight one).
func (st *step) gates(p Proposal, resend bool) []Reason {
	var out []Reason
	r := p.Response
	ex := r.Execution
	if r.Owner == "" {
		out = append(out, Reason{Code: ReasonOwnerUndeclared})
	}
	if reg, ok := st.e.Providers[ex.Provider]; !ok {
		out = append(out, Reason{Code: ReasonProviderUnregistered, Subject: ex.Provider})
	} else if !reg.authorises(r.Owner, p.Binding.Target.Namespace, r.Action) {
		out = append(out, Reason{Code: ReasonAuthorityInsufficient, Subject: ex.Provider})
	}
	for _, pc := range p.Preconditions {
		switch pc.Code {
		case "unmet":
			out = append(out, Reason{Code: ReasonPreconditionUnmet, Subject: pc.Subject})
		case "unproven":
			out = append(out, Reason{Code: ReasonPreconditionUnproven, Subject: pc.Subject})
		}
	}
	if !st.e.Journal.Durable() {
		out = append(out, Reason{Code: ReasonJournalNotDurable})
	}
	if !resend && st.busy(p.Binding.Target.UID, p.ID) {
		out = append(out, Reason{Code: ReasonTargetBusy})
	}
	return out
}

// approval evaluates submitted decisions for exactly this proposal.
func (st *step) approval(p Proposal) (approved *Approval, rejected bool, notes []Reason) {
	if len(p.Human) == 0 {
		return &Approval{Kind: "policy", Principal: "policy:" + p.ProfileRevision}, false, nil
	}
	ex := p.Response.Execution
	for _, d := range st.decisions {
		if d.ProposalID != p.ID {
			continue
		}
		var code string
		switch {
		case st.e.Verifier == nil || !st.e.Verifier.Verify(d):
			code = ReasonApprovalUnverified
		case !contains(ex.Approvers, d.Principal):
			code = ReasonApproverNotAllowed
		case d.Digest != p.Digest:
			code = ReasonApprovalDigestMismatch
		case d.DecidedAt.After(st.in.Now):
			code = ReasonApprovalFuture
		case st.in.Now.Sub(d.DecidedAt) > ex.ApprovalAge():
			code = ReasonApprovalExpired
		}
		if code != "" {
			// The decision's own fields are unverified input: only the code
			// is reported, once.
			if !containsReason(notes, code) {
				notes = append(notes, Reason{Code: code})
			}
			continue
		}
		if d.Verdict == Reject {
			rejected = true
			continue
		}
		if d.Verdict == Approve && approved == nil {
			approved = &Approval{Kind: "human", Principal: d.Principal, DecisionID: d.ID}
			st.wakeAt(d.DecidedAt.Add(ex.ApprovalAge()))
		}
	}
	if rejected {
		approved = nil
	}
	return approved, rejected, notes
}

func (st *step) stillApproved(p Proposal, a Approval) bool {
	got, rejected, _ := st.approval(p)
	return !rejected && got != nil && got.Kind == a.Kind && got.Principal == a.Principal
}

// decide derives the proposal's phase and, when every gate passes and the
// approval is valid, persists and sends a new attempt.
func (st *step) decide(p Proposal) View {
	v := View{ProposalID: p.ID, Digest: p.Digest, Action: p.Response.Action, For: p.For, Attempt: 1}
	attempt := 1
	if rs := st.attempts(p.ID); len(rs) > 0 {
		last := rs[len(rs)-1]
		if last.Phase.inFlight() || last.Phase != PhaseFailed ||
			!last.Retryable || last.Attempt >= last.MaxAttempts {
			return st.recordView(last)
		}
		attempt = last.Attempt + 1
		v.Attempt = attempt
	}
	gates := st.gates(p, false)
	approved, rejected, notes := st.approval(p)
	human := make([]Reason, 0, len(p.Human))
	for _, h := range p.Human {
		human = append(human, Reason{Code: h})
	}
	switch {
	case rejected:
		v.Phase = PhaseRejected
		return v
	case len(gates) > 0:
		v.Phase, v.Reasons = PhaseBlocked, gates
		if approved == nil {
			v.Reasons = append(v.Reasons, human...)
		}
		return v
	case approved == nil:
		v.Phase, v.Reasons = PhaseAwaitingApproval, append(human, notes...)
		return v
	}

	ex := p.Response.Execution
	now := st.in.Now
	r := Record{
		Key: fmt.Sprintf("%s/%d", p.ID, attempt), ProposalID: p.ID, Subject: p.Subject,
		TargetUID: p.Binding.Target.UID, Digest: p.Digest, Attempt: attempt, Phase: PhaseDispatching,
		Fence: st.e.epoch, SentFence: st.e.epoch, Sends: 1, Approval: *approved, Binding: p.Binding,
		Action: p.Response.Action, Provider: ex.Provider, Owner: p.Response.Owner, For: p.For,
		Expected: p.Expected, CreatedAt: now, LastSentAt: now,
		AckTimeout: ex.AckTimeout.Duration, CompletionTimeout: ex.CompletionTimeout.Duration,
		RecoveryWindow: ex.RecoveryWindow.Duration, Retryable: ex.Retryable,
		MaxAttempts: ex.AttemptBudget(), MaxSends: ex.SendBudget(),
	}
	stored := &Record{}
	if !st.put(stored, r) { // persist before send; on failure nothing is sent
		v.Phase, v.Reasons = PhaseBlocked, []Reason{{Code: ReasonJournalWriteFailed}}
		return v
	}
	st.recs[stored.Key] = stored
	st.send(stored, st.e.Providers[ex.Provider])
	st.wakeAt(stored.CreatedAt.Add(stored.AckTimeout / time.Duration(stored.MaxSends)))
	return st.recordView(stored)
}

func (st *step) recordView(r *Record) View {
	v := View{ProposalID: r.ProposalID, Digest: r.Digest, Action: r.Action, For: r.For, Phase: r.Phase,
		Attempt: r.Attempt, Recovery: r.Recovery, Detail: r.RecoveryDetail}
	if p, ok := st.props[r.ProposalID]; ok && p.Digest != r.Digest && r.Phase.inFlight() {
		v.Reasons = append(v.Reasons, Reason{Code: ReasonIdentityChanged})
	}
	switch {
	case r.Recovery == RecoveryInapplicable:
		v.Reasons = append(v.Reasons, Reason{Code: ReasonIdentityChanged})
	case r.Phase == PhaseTimedOut || r.Phase == PhaseWithdrawn:
		v.Reasons = append(v.Reasons, Reason{Code: ReasonOutcomeUnknown})
	case r.Recovery.final() && r.Recovery != RecoveryRecovered:
		v.Reasons = append(v.Reasons, Reason{Code: ReasonExecutedNotRecovered})
	}
	if r.Phase == PhaseFailed && (!r.Retryable || r.Attempt >= r.MaxAttempts) {
		v.Reasons = append(v.Reasons, Reason{Code: ReasonAttemptsExhausted})
	}
	return v
}

func containsReason(rs []Reason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}
