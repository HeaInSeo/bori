package action_test

import (
	"context"
	"fmt"

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

func (w *world) response(a interaction.ActionRef, target string) (interaction.Response, bool) {
	for _, r := range w.profile.Responses {
		if r.Action == a && r.Target.Name == target {
			return r, true
		}
	}
	return interaction.Response{}, false
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
			r, ok := w.response(v.Action, name)
			if !ok {
				f.add(&f.unrelated, "view on %s without a declared response: %+v", uid, v)
				continue
			}
			want := map[operations.CapabilityType]bool{}
			for _, c := range r.Execution.ExpectedImpact {
				want[operations.CapabilityType{Domain: c.Domain, Name: c.Name, Revision: c.Revision}] = true
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

// checkExecution verifies one handoff the actor newly executed during this
// step against the inputs of this step.
func (w *world) checkExecution(f *forbidden, e executed, s stepLog) {
	h := e.h
	name := w.targetName(h.Target.UID)
	r, ok := w.response(h.Action, name)
	if !ok {
		f.add(&f.unauthorized, "executed an undeclared action %s on %s", h.Action, h.Target)
		return
	}
	if r.Owner == "" || h.Owner != r.Owner || h.Owner != "storage-oncall" || h.Target.Namespace != "apps" {
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
	ta, _ := s.a.Target(h.Target.UID)
	state := func(c operations.CapabilityType) operations.CapabilityState {
		for _, x := range ta.Capabilities {
			if x.Type == c {
				return x.State
			}
		}
		return operations.Unknown
	}
	trig := state(operations.CapabilityType{Domain: r.For.Domain, Name: r.For.Name, Revision: r.For.Revision})
	if trig != operations.Degraded && trig != operations.Unavailable && !contains(r.States, string(trig)) {
		f.add(&f.staleReuse, "executed while the trigger capability is %s on current evidence", trig)
	}
	for _, pc := range r.Preconditions {
		if st := state(operations.CapabilityType{Domain: pc.Domain, Name: pc.Name, Revision: pc.Revision}); st != operations.Available {
			f.add(&f.unauthorized, "executed with precondition %s %s", pc.Name, st)
		}
	}
	for _, v := range s.out.Views[h.Target.UID] {
		if v.ProposalID == h.ProposalID && v.Digest != h.Digest {
			f.add(&f.staleReuse, "executed digest %s but the current proposal digest is %s", h.Digest, v.Digest)
		}
	}
	if len(r.HumanDecisionReasons()) == 0 {
		if h.Approval.Kind != "policy" {
			f.add(&f.unauthorized, "policy action without a policy approval: %+v", h.Approval)
		}
		return
	}
	ds, _ := w.decisions.Decisions(context.Background())
	valid := false
	for _, d := range ds {
		if d.ID == h.Approval.DecisionID && d.Verdict == action.Approve && d.ProposalID == h.ProposalID &&
			d.Digest == h.Digest && w.signer.Verify(d) && contains(r.Execution.Approvers, d.Principal) &&
			!d.DecidedAt.After(s.at) && s.at.Sub(d.DecidedAt) <= r.Execution.ApprovalAge() {
			valid = true
		}
	}
	if !valid {
		f.add(&f.unauthorized, "executed without a valid approval for the exact proposal: %+v", h.Approval)
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
	if !w.ck.zero() {
		w.t.Fatalf("forbidden outcomes: unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d\n%v",
			w.ck.unrelated, w.ck.unjustifiedRecovery, w.ck.unauthorized, w.ck.staleReuse, w.ck.notes)
	}
}
