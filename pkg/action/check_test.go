package action_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// forbidden counts hard-safety violations (Scenario §7), recomputed
// independently of the engine after every step.
type forbidden struct {
	unrelated           int // a view or recovery about something not declared/expected
	unjustifiedRecovery int // Recovered without post-execution proof for the same identity
	unauthorized        int // an execution without valid approval, owner, authority or preconditions, or a duplicate
	staleReuse          int // an approval, result or evidence of another identity or meaning used
	notes               []string
}

func (f *forbidden) add(n *int, format string, a ...any) {
	*n++
	f.notes = append(f.notes, fmt.Sprintf(format, a...))
}

func (f forbidden) zero() bool {
	return f.unrelated+f.unjustifiedRecovery+f.unauthorized+f.staleReuse == 0
}

// declarations returns every response declared for the named target and
// action, in profile order.
func (w *world) declarations(a interaction.ActionRef, target string) []interaction.Response {
	var out []interaction.Response
	for _, r := range w.profile.Responses {
		if r.Action == a && r.Target.Name == target {
			out = append(out, r)
		}
	}
	return out
}

func opsT(c opsv1.CapabilityType) operations.CapabilityType {
	return operations.CapabilityType{Domain: c.Domain, Name: c.Name, Revision: c.Revision}
}

// o4Identity is the checker's own statement of what the O4 display keeps
// apart (API §18 O4 response boundary): action, capability, owner, approval
// flag, risks and precondition set. It deliberately does not reuse the
// engine's keys.
func o4Identity(r interaction.Response) string {
	risks := append([]string(nil), r.Risks...)
	sort.Strings(risks)
	var pcs []string
	for _, pc := range r.Preconditions {
		pcs = append(pcs, opsT(pc).String())
	}
	sort.Strings(pcs)
	return fmt.Sprintf("%s|%s|%s|%t|%v|%v", r.Action, opsT(r.For), r.Owner, r.RequiresApproval, risks, pcs)
}

func (w *world) targetName(uid string) string {
	for n, t := range w.targets {
		if t.Identity.UID == uid {
			return n
		}
	}
	return ""
}

func (w *world) latestRecord(proposalID string) (action.Record, bool) {
	var best action.Record
	found := false
	for _, r := range w.records() {
		if r.ProposalID == proposalID && (!found || r.Attempt > best.Attempt) {
			best, found = r, true
		}
	}
	return best, found
}

func (w *world) check() {
	f := &w.ck
	s := w.history[len(w.history)-1]

	for uid, vs := range s.out.Views {
		name := w.targetName(uid)
		for _, v := range vs {
			want := map[operations.CapabilityType]bool{}
			declared := false
			for _, r := range w.declarations(v.Action, name) {
				if r.Execution == nil {
					continue
				}
				declared = true
				for _, c := range r.Execution.ExpectedImpact {
					want[opsT(c)] = true
				}
			}
			if !declared {
				f.add(&f.unrelated, "view on %s without a declared executable response: %+v", uid, v)
				continue
			}
			for _, d := range v.Detail {
				if !want[d.Type] {
					f.add(&f.unrelated, "recovery detail on unexpected capability %s", d.Type)
				}
			}
			if v.Recovery == action.RecoveryRecovered {
				w.checkRecovered(f, v, s)
			}
		}
	}
	for _, e := range w.pending {
		w.checkExecution(f, e, s)
	}
}

// checkRecovered recomputes the recovery claim from scratch.
func (w *world) checkRecovered(f *forbidden, v action.View, s stepLog) {
	rec, ok := w.latestRecord(v.ProposalID)
	if !ok || rec.CompletedAt.IsZero() || rec.CompletedAt.After(s.at) {
		f.add(&f.unjustifiedRecovery, "recovered without an ended execution: %+v", v)
		return
	}
	var cur *operations.Target
	for i := range s.snap.Targets {
		if s.snap.Targets[i].Identity.UID == rec.TargetUID {
			cur = &s.snap.Targets[i]
		}
	}
	if cur == nil || cur.ResolvedUID != rec.Binding.ResolvedUID || cur.ContractRef != rec.Binding.Contract {
		f.add(&f.staleReuse, "recovery claimed across an identity change: %+v", v)
		return
	}
	post := s.snap
	post.Observations = nil
	for _, o := range s.snap.Observations {
		if o.ObservedAt.After(rec.CompletedAt) {
			post.Observations = append(post.Observations, o)
		}
	}
	a, err := operations.Evaluate(post)
	if err != nil {
		f.add(&f.unjustifiedRecovery, "post evaluation failed: %v", err)
		return
	}
	ta, _ := a.Target(rec.TargetUID)
	for _, c := range rec.Expected {
		ok := false
		for _, r := range ta.Capabilities {
			ok = ok || (r.Type == c && r.State == operations.Available)
		}
		if !ok {
			f.add(&f.unjustifiedRecovery, "recovered but %s is not AVAILABLE on post-execution evidence", c)
		}
	}
}

// sameContract is the checker's own comparison of two declared execution
// contracts: field by field, an empty list equal to an absent one.
func sameContract(a, b interaction.Execution) bool {
	norm := func(e interaction.Execution) interaction.Execution {
		if len(e.Approvers) == 0 {
			e.Approvers = nil
		}
		if len(e.ExpectedImpact) == 0 {
			e.ExpectedImpact = nil
		}
		if len(e.MayInterrupt) == 0 {
			e.MayInterrupt = nil
		}
		return e
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// checkExecution verifies one handoff the actor newly executed during this
// step against the inputs of this step. It judges from the profile, the O1
// assessment, the decisions and the journal record of the handoff alone,
// never from the engine's proposals or keys. The record's execution
// contract maps the handoff to its exact declaration(s); only that
// contract's approvers and approval age apply.
func (w *world) checkExecution(f *forbidden, e executed, s stepLog) {
	h := e.h
	name := w.targetName(h.Target.UID)
	declared := false
	for _, r := range w.declarations(h.Action, name) {
		declared = declared || (r.Execution != nil && r.Owner == h.Owner && opsT(r.For) == h.For)
	}
	if !declared {
		f.add(&f.unauthorized, "executed an undeclared action %s on %s", h.Action, h.Target)
	}
	if h.Owner == "" || h.Owner != "storage-oncall" || h.Target.Namespace != "apps" {
		f.add(&f.unauthorized, "executed without the declared owner/authority: %+v", h)
	}
	if n := w.actor.Executions[h.Key]; n > 1 {
		f.add(&f.unauthorized, "key %s executed %d times", h.Key, n)
	}
	for _, k := range w.actor.ExecutedKeys() {
		rec, ok := w.recordByKey(k)
		if k == h.Key || !ok || rec.ProposalID != h.ProposalID {
			continue
		}
		if rec.Phase != action.PhaseFailed {
			f.add(&f.unauthorized, "second execution %s of proposal %s while %s is %s", h.Key, h.ProposalID, k, rec.Phase)
		}
	}

	cur := (*operations.Target)(nil)
	for i := range s.snap.Targets {
		if s.snap.Targets[i].Identity == h.Target {
			cur = &s.snap.Targets[i]
		}
	}
	if cur == nil || cur.ResolvedUID != h.ResolvedUID || cur.ContractRef != h.Contract {
		f.add(&f.staleReuse, "executed for a replaced identity: %+v", h)
		return
	}
	if !declared {
		return
	}

	// The persisted record of the handoff, consistent with what was sent.
	rec, ok := w.recordByKey(h.Key)
	if !ok {
		f.add(&f.unauthorized, "executed %s without a persisted record", h.Key)
		return
	}
	var impact []operations.CapabilityType
	for _, c := range rec.Execution.ExpectedImpact {
		impact = append(impact, opsT(c))
	}
	if rec.ProposalID != h.ProposalID || rec.Digest != h.Digest || rec.Approval != h.Approval ||
		rec.Action != h.Action || rec.Owner != h.Owner || rec.For != h.For ||
		rec.Provider != rec.Execution.Provider || fmt.Sprint(impact) != fmt.Sprint(h.ExpectedImpact) {
		f.add(&f.unauthorized, "handoff %s does not match its record or the record's contract", h.Key)
		return
	}
	for _, o := range w.records() {
		if o.ProposalID == rec.ProposalID && !sameContract(o.Execution, rec.Execution) {
			f.add(&f.unauthorized, "proposal %s recorded under two execution contracts", rec.ProposalID)
			return
		}
	}
	var cands []interaction.Response // the declarations this handoff is
	for _, r := range w.declarations(h.Action, name) {
		if r.Execution != nil && r.Owner == h.Owner && opsT(r.For) == h.For && sameContract(*r.Execution, rec.Execution) {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		f.add(&f.unauthorized, "executed %s under an undeclared execution contract", h.Action)
		return
	}
	contract := *cands[0].Execution // equal for every candidate

	ta, _ := s.a.Target(h.Target.UID)
	state := func(c operations.CapabilityType) operations.CapabilityState {
		for _, x := range ta.Capabilities {
			if x.Type == c {
				return x.State
			}
		}
		return operations.Unknown
	}
	triggered := func(r interaction.Response) bool {
		st := state(opsT(r.For))
		if len(r.States) == 0 {
			return st == operations.Degraded || st == operations.Unavailable
		}
		return contains(r.States, string(st))
	}
	unmet := func(r interaction.Response) bool {
		for _, pc := range r.Preconditions {
			if st := state(opsT(pc)); st == operations.Degraded || st == operations.Unavailable {
				return true
			}
		}
		return false
	}
	proven := func(r interaction.Response) bool {
		for _, pc := range r.Preconditions {
			if state(opsT(pc)) != operations.Available {
				return false
			}
		}
		return true
	}
	var live []interaction.Response // triggered, all preconditions proven
	for _, r := range cands {
		if triggered(r) && proven(r) {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		trig := state(h.For)
		triggeredAny := false
		for _, r := range cands {
			triggeredAny = triggeredAny || triggered(r)
		}
		if !triggeredAny {
			f.add(&f.staleReuse, "executed while the trigger capability is %s on current evidence", trig)
		} else {
			f.add(&f.unauthorized, "executed without proven preconditions on current evidence")
		}
		return
	}
	for _, v := range s.out.Views[h.Target.UID] {
		if v.ProposalID == h.ProposalID && v.Digest != h.Digest {
			f.add(&f.staleReuse, "executed digest %s but the current proposal digest is %s", h.Digest, v.Digest)
		}
	}

	// The O4 human-decision boundary, recomputed: every triggered
	// declaration for this capability of the target (any action, executable
	// or not) whose preconditions are not unmet is a viable response; more
	// than one distinct viable response has no priority authority, and so
	// has one viable response declared with several execution contracts.
	// The same action under several owners is an owner conflict. A
	// declaration's own approval flag, risks or interruption also need a
	// person.
	owners := map[string]bool{}
	viable := map[string]bool{}
	contracts := map[string][]interaction.Execution{} // viable response → distinct contracts
	for _, r := range w.profile.Responses {
		if r.Target.Name != name || opsT(r.For) != h.For || !triggered(r) {
			continue
		}
		if r.Action == h.Action {
			owners[r.Owner] = true
		}
		if unmet(r) {
			continue
		}
		id := o4Identity(r)
		viable[id] = true
		if r.Execution == nil {
			continue
		}
		seen := false
		for _, c := range contracts[id] {
			seen = seen || sameContract(c, *r.Execution)
		}
		if !seen {
			contracts[id] = append(contracts[id], *r.Execution)
		}
	}
	if len(owners) > 1 {
		f.add(&f.unauthorized, "executed %s under an owner conflict %v", h.Action, owners)
	}
	person := len(viable) > 1
	for _, r := range live {
		person = person || len(contracts[o4Identity(r)]) > 1 ||
			r.RequiresApproval || len(r.Risks) > 0 || len(r.Execution.MayInterrupt) > 0
	}
	if !person {
		if h.Approval.Kind != "policy" {
			f.add(&f.unauthorized, "policy action without a policy approval: %+v", h.Approval)
		}
		return
	}
	ds, _ := w.decisions.Decisions(context.Background())
	valid := false
	for _, d := range ds {
		if h.Approval.Kind != "policy" && d.ID == h.Approval.DecisionID && d.Principal == h.Approval.Principal &&
			d.Verdict == action.Approve && d.ProposalID == h.ProposalID && d.Digest == h.Digest && w.signer.Verify(d) &&
			contains(contract.Approvers, d.Principal) &&
			!d.DecidedAt.After(s.at) && s.at.Sub(d.DecidedAt) <= contract.ApprovalAge() {
			valid = true
		}
	}
	if !valid {
		f.add(&f.unauthorized, "executed without a valid approval by an approver of its own contract (person required, %d viable responses): %+v", len(viable), h.Approval)
	}
}

func (w *world) recordByKey(k string) (action.Record, bool) {
	for _, r := range w.records() {
		if r.Key == k {
			return r, true
		}
	}
	return action.Record{}, false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (w *world) assertSafe() {
	w.t.Helper()
	w.t.Logf("forbidden4 unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d steps=%d executions=%d",
		w.ck.unrelated, w.ck.unjustifiedRecovery, w.ck.unauthorized, w.ck.staleReuse, len(w.history), w.actor.TotalExecutions())
	if !w.ck.zero() {
		w.t.Fatalf("forbidden outcomes: unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d\n%v",
			w.ck.unrelated, w.ck.unjustifiedRecovery, w.ck.unauthorized, w.ck.staleReuse, w.ck.notes)
	}
}
