package controllers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// confForbidden counts the four Scenario §7 forbidden outcomes, recomputed by
// the harness from its own ground truth after every reconcile. It never
// reads BORI's reasons or evidence references, only the states BORI claims.
type confForbidden struct {
	unrelated           int // a capability changed state although nothing it references changed
	unjustifiedRecovery int // Recovered while the expected capabilities' evidence is not healthy again
	unauthorized        int // an execution of an undeclared or unapproved action
	staleReuse          int // AVAILABLE on missing, stale or inapplicable evidence
	seen                map[string]bool
	notes               []string
}

func (f *confForbidden) add(n *int, key, format string, a ...any) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return
	}
	f.seen[key] = true
	*n++
	f.notes = append(f.notes, fmt.Sprintf(format, a...))
}

func (f confForbidden) assertZero(t *testing.T) {
	t.Helper()
	if f.unrelated+f.unjustifiedRecovery+f.unauthorized+f.staleReuse != 0 {
		t.Fatalf("forbidden outcomes: unrelated=%d unjustifiedRecovery=%d unauthorized=%d staleReuse=%d\n%v",
			f.unrelated, f.unjustifiedRecovery, f.unauthorized, f.staleReuse, f.notes)
	}
}

// slotFact is the harness's truth about one bound assertion slot.
type slotFact struct {
	fresh bool
	value string
	// subject is the resolved workload UID the evidence is about: a new
	// identity is a changed input even when the value is equal.
	subject string
	// specGeneration is the workload generation and the one its status was
	// recorded for: a status that falls behind its spec is a changed input,
	// which the provider refuses, although BORI may hold its earlier
	// observation until that ages out.
	specGeneration [2]int64
}

// truth is the harness's own model: the pack's declarations, the provider
// configuration and the fixture world.
type truth struct {
	e         *confEnv
	targets   map[string]*opsv1.OperationalTarget // by name
	contracts map[string]*opsv1.OperationalContract
	http      map[string]map[string]httpEP // provider "ns/name@rev" → subject UID → endpoint
	kube      map[string]map[string]string // provider "ns/name@rev" → slot → status field
	baseline  map[string]slotFact          // "<target>/<slot>"
	// touched records every slot that differed from its baseline at any
	// point of the scenario: BORI may lawfully hold an earlier observation
	// until it observes again, so only never-touched inputs are "unchanged".
	touched   map[string]bool
	baseState map[string]string // "<target>/<capability>" state after settling
	// profileRevision is the revision of the response profile the harness
	// loaded: the only one a policy approval may name.
	profileRevision string
}

type httpEP struct {
	path   string
	fields map[string]string
}

func newTruth(e *confEnv) *truth {
	tr := &truth{e: e, targets: map[string]*opsv1.OperationalTarget{}, contracts: map[string]*opsv1.OperationalContract{},
		http: map[string]map[string]httpEP{}, kube: map[string]map[string]string{}}
	if e.p.profile != nil {
		tr.profileRevision = e.p.profile.Revision
	}
	for _, o := range e.p.objects {
		switch v := o.(type) {
		case *opsv1.OperationalTarget:
			tr.targets[v.Name] = v
		case *opsv1.OperationalContract:
			tr.contracts[v.Name] = v
		}
	}
	var cfg providers.Config
	if err := yaml.Unmarshal(e.p.providers, &cfg); err != nil {
		e.t.Fatal(err)
	}
	for _, pc := range cfg.Providers {
		id := pc.Namespace + "/" + pc.Name + "@" + pc.ConfigRevision
		if pc.HTTP != nil {
			tr.http[id] = map[string]httpEP{}
			for _, ep := range pc.HTTP.Endpoints {
				u, err := url.Parse(strings.ReplaceAll(ep.URL, "${EVIDENCE_URL}", "http://x"))
				if err != nil {
					e.t.Fatal(err)
				}
				tr.http[id][ep.SubjectUID] = httpEP{path: u.Path, fields: ep.Fields}
			}
		}
		if pc.KubernetesStatus != nil {
			tr.kube[id] = pc.KubernetesStatus.Fields
		}
	}
	tr.baseline = tr.facts()
	return tr
}

// workloadOf returns the current object a target refers to, if it exists
// and its kind is resolvable by this reconciler.
func (tr *truth) workloadOf(t *opsv1.OperationalTarget) (map[string]any, string, bool) {
	gvk := runtimeGVK(t.Spec.TargetRef.APIVersion, t.Spec.TargetRef.Kind)
	if !tr.e.r.supported(gvk) {
		return nil, "", false
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := tr.e.c.Get(tr.e.driver(), types.NamespacedName{Namespace: t.Namespace, Name: t.Spec.TargetRef.Name}, u); err != nil {
		return nil, "", false
	}
	return u.Object, string(u.GetUID()), true
}

// facts is the current truth of every bound slot of every target.
func (tr *truth) facts() map[string]slotFact {
	out := map[string]slotFact{}
	ev := tr.e.ev
	for name := range tr.targets {
		t := tr.e.target(name) // current spec: bindings may have changed
		obj, uid, ok := tr.workloadOf(t)
		c := tr.contracts[t.Spec.ContractRef.Name]
		maxAge := map[string]time.Duration{}
		if c != nil {
			for _, s := range c.Spec.Assertions {
				maxAge[s.Name] = s.MaxAge.Duration
			}
		}
		for _, b := range t.Spec.AssertionBindings {
			id := t.Namespace + "/" + b.Provider.Name + "@" + b.Provider.ConfigRevision
			f := slotFact{}
			switch {
			case !ok:
			case tr.http[id] != nil:
				ep, found := tr.http[id][uid]
				if !found {
					break
				}
				key := ep.fields[b.Slot]
				ev.mu.Lock()
				v, has := ev.values[ep.path][key]
				frozenAt, frozen := ev.frozen[ep.path]
				code := ev.status[ep.path]
				ev.mu.Unlock()
				stale := frozen && tr.e.clk.now().Sub(frozenAt) > maxAge[b.Slot]
				f = slotFact{fresh: has && code == 0 && !stale && key != "", value: fmt.Sprint(v), subject: uid}
			case tr.kube[id] != nil:
				field := tr.kube[id][b.Slot]
				v, has, _ := unstructured.NestedFieldNoCopy(obj, "status", field)
				// An absent integer status field reads as zero.
				if !has && field != "" {
					v, has = int64(0), true
				}
				gen, _, _ := unstructured.NestedInt64(obj, "metadata", "generation")
				og, _, _ := unstructured.NestedInt64(obj, "status", "observedGeneration")
				f = slotFact{fresh: has && field != "", value: fmt.Sprint(v), subject: uid, specGeneration: [2]int64{gen, og}}
			}
			out[name+"/"+b.Slot] = f
		}
	}
	return out
}

func runtimeGVK(apiVersion, kind string) schema.GroupVersionKind {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}
	}
	return gv.WithKind(kind)
}

// refs walks a capability's requirements, through envelopes and bound
// dependencies, to every "<target>/<slot>" it rests on. ok is false when a
// dependency cannot be followed (unbound, missing target or capability).
func (tr *truth) refs(target, capName string, seen map[string]bool) (out []string, ok bool) {
	key := target + "/" + capName
	if seen[key] {
		return nil, false
	}
	seen[key] = true
	if tr.targets[target] == nil {
		return nil, false
	}
	t := tr.e.target(target)
	c := tr.contracts[t.Spec.ContractRef.Name]
	if c == nil {
		return nil, false
	}
	ok = true
	for _, cp := range c.Spec.Capabilities {
		if cp.Type.Name != capName {
			continue
		}
		for _, r := range cp.Requirements {
			switch {
			case r.Predicate != nil:
				out = append(out, target+"/"+r.Predicate.Assertion)
			case r.LocalCapability != "":
				sub, subOK := tr.refs(target, r.LocalCapability, seen)
				out = append(out, sub...)
				ok = ok && subOK
			case r.Envelope != "":
				for _, env := range c.Spec.Envelopes {
					if env.Name == r.Envelope {
						for _, p := range env.Requirements {
							out = append(out, target+"/"+p.Assertion)
						}
					}
				}
			case r.Dependency != nil:
				bound := ""
				for _, db := range t.Spec.DependencyBindings {
					if db.Slot == r.Dependency.Slot {
						for n, x := range tr.targets {
							if x.Name == db.Target.Name && (db.Target.Namespace == "" || db.Target.Namespace == x.Namespace) {
								bound = n
							}
						}
					}
				}
				if bound == "" {
					ok = false
					continue
				}
				sub, subOK := tr.refs(bound, r.Dependency.Capability.Name, seen)
				out = append(out, sub...)
				ok = ok && subOK
			}
		}
	}
	return out, ok
}

// claims is what BORI states, per target: capability states and actions.
type claims map[string]opsv1.OperationalTargetStatus

func (e *confEnv) claims() claims {
	out := claims{}
	for name := range e.truth.targets {
		out[name] = e.target(name).Status
	}
	return out
}

// check runs the independent checker over BORI's current claims.
func (e *confEnv) check() {
	e.noteEnded()
	facts := e.truth.facts()
	for k, f := range facts {
		if f != e.truth.baseline[k] {
			e.truth.touched[k] = true
		}
	}
	e.judge(e.claims(), facts)
}

// noteEnded records, per executed key, the harness time at which the
// reference actor itself first reports the execution ended.
func (e *confEnv) noteEnded() {
	if e.actor == nil {
		return
	}
	now := e.clk.now()
	for _, h := range e.actor.Handoffs {
		if _, seen := e.endedAt[h.Key]; !seen && e.actor.Executions[h.Key] > 0 && e.actor.Ended(h.Key) {
			e.endedAt[h.Key] = now
		}
	}
}

// observedAfter reports whether a provider accepted an observation of the
// target's slot strictly after t, carrying want, about the target's current
// workload. A provider's refusal (a status lagging the spec, a subject
// mismatch, ...) proves nothing, however healthy the bytes it read. For an
// HTTP slot the evidence server must also have served that value at that
// time: the provider cannot accept what was never delivered.
func (tr *truth) observedAfter(target, slot string, t time.Time, want string) bool {
	tg := tr.e.target(target)
	_, uid, ok := tr.workloadOf(tg)
	if !ok {
		return false
	}
	for _, b := range tg.Spec.AssertionBindings {
		if b.Slot != slot {
			continue
		}
		id := tg.Namespace + "/" + b.Provider.Name + "@" + b.Provider.ConfigRevision
		var served func(at time.Time) bool
		if eps := tr.http[id]; eps != nil {
			ep, found := eps[uid]
			if !found {
				return false
			}
			key := ep.fields[slot]
			served = func(at time.Time) bool {
				tr.e.ev.mu.Lock()
				defer tr.e.ev.mu.Unlock()
				for _, o := range tr.e.ev.served {
					if o.path == ep.path && o.at.Equal(at) && o.values[key] == want {
						return true
					}
				}
				return false
			}
		} else if tr.kube[id] == nil {
			return false
		}
		tr.e.mu.Lock()
		obs := append([]providerObs(nil), tr.e.observed...)
		tr.e.mu.Unlock()
		for _, o := range obs {
			if o.provider == id && o.uid == uid && o.slot == slot && o.unavailable == "" &&
				o.at.After(t) && o.value == want && (served == nil || served(o.at)) {
				return true
			}
		}
		return false
	}
	return false
}

// judge is the pure checker: claims against facts. Positive controls call
// it with tampered claims.
func (e *confEnv) judge(cl claims, facts map[string]slotFact) {
	tr, f := e.truth, &e.ck
	for _, name := range sortedKeys(cl) {
		st := cl[name]
		for _, c := range st.Capabilities {
			refs, ok := tr.refs(name, c.Type.Name, map[string]bool{})
			fresh, unchanged := ok, ok
			for _, r := range refs {
				fresh = fresh && facts[r].fresh
				unchanged = unchanged && facts[r] == tr.baseline[r] && !tr.touched[r]
			}
			if c.State == "AVAILABLE" && !fresh {
				f.add(&f.staleReuse, "stale/"+name+"/"+c.Type.Name, "%s %s AVAILABLE on missing or stale evidence %v", name, c.Type.Name, refs)
			}
			key := name + "/" + c.Type.Name
			if base, known := tr.baseState[key]; known && fresh && unchanged && c.State != base {
				f.add(&f.unrelated, "unrelated/"+key+"/"+c.State, "%s %s is %s although nothing it references changed (baseline %s)", name, c.Type.Name, c.State, base)
			}
		}
		if st.Interaction == nil {
			continue
		}
		for _, a := range st.Interaction.Actions {
			if a.Recovery != "Recovered" {
				continue
			}
			key := "recovered/" + name + "/" + a.Proposal
			// The execution's end is the actor's own fact; without one there
			// is nothing a recovery could follow.
			var end time.Time
			executed := false
			if e.actor != nil {
				for _, h := range e.actor.Handoffs {
					if h.ProposalID == a.Proposal && e.actor.Executions[h.Key] > 0 {
						executed = true
						if t, ok := e.endedAt[h.Key]; ok && t.After(end) {
							end = t
						}
					}
				}
			}
			if !executed || end.IsZero() {
				f.add(&f.unjustifiedRecovery, key, "%s %s Recovered without an execution that ended at the actor", name, a.Name)
				continue
			}
			for _, capName := range tr.expectedImpact(name, a.Name) {
				refs, ok := tr.refs(name, capName, map[string]bool{})
				if !ok {
					f.add(&f.unjustifiedRecovery, key, "%s %s Recovered although %s cannot be traced to its facts", name, a.Name, capName)
					continue
				}
				// Every fact must have been delivered healthy strictly after
				// the actor ended the execution: earlier evidence, or the
				// fixture's current value, proves nothing. Recovered is an
				// as-of statement, so a later regression does not void it.
				for _, r := range refs {
					rt, rs, _ := strings.Cut(r, "/")
					if !tr.observedAfter(rt, rs, end, tr.baseline[r].value) {
						f.add(&f.unjustifiedRecovery, key, "%s %s Recovered before %s was observed healthy after the execution ended at %s", name, a.Name, r, end.Format(time.RFC3339))
					}
				}
			}
		}
	}
	if e.actor == nil {
		return
	}
	for _, h := range e.actor.Handoffs {
		if e.actor.Executions[h.Key] == 0 {
			continue
		}
		name := ""
		for n, t := range tr.targets {
			if t.Namespace == h.Target.Namespace && t.Name == h.Target.Name {
				name = n
			}
		}
		r := tr.response(name, h.Action.String())
		switch {
		case r == nil:
			f.add(&f.unauthorized, "exec/"+h.Key, "executed undeclared %s on %s", h.Action, h.Target)
		case r.Owner != h.Owner:
			f.add(&f.unauthorized, "exec/"+h.Key, "executed %s for owner %s, declared %s", h.Action, h.Owner, r.Owner)
		case !r.Execution:
			f.add(&f.unauthorized, "exec/"+h.Key, "executed display-only %s on %s", h.Action, name)
		case h.Approval.Kind == "human":
			if !e.approvedExactly(name, h) {
				f.add(&f.unauthorized, "exec/"+h.Key, "executed %s on %s under approval %+v that no approve step gave for proposal %s digest %s", h.Action, name, h.Approval, h.ProposalID, h.Digest)
			}
		case r.Human:
			f.add(&f.unauthorized, "exec/"+h.Key, "executed %s on %s, which needs a human decision, under non-human approval %+v", h.Action, name, h.Approval)
		case h.Approval.Kind != "policy":
			f.add(&f.unauthorized, "exec/"+h.Key, "executed %s on %s under an approval of unknown kind %+v", h.Action, name, h.Approval)
		case h.Approval.Principal != "policy:"+tr.profileRevision || h.Approval.DecisionID != "":
			// The engine's policy approval names exactly the profile revision
			// the harness loaded, and no decision.
			f.add(&f.unauthorized, "exec/"+h.Key, "executed %s on %s under policy approval %+v, not that of profile revision %s", h.Action, name, h.Approval, tr.profileRevision)
		}
	}
}

// approvedExactly reports whether an approve step signed exactly the
// decision the handoff carries, for exactly its target, action, proposal
// and digest.
func (e *confEnv) approvedExactly(target string, h action.Handoff) bool {
	for _, a := range e.approvals {
		d := a.decision
		if a.target == target && a.action == h.Action.String() && d.ID == h.Approval.DecisionID &&
			d.Principal == h.Approval.Principal && d.ProposalID == h.ProposalID && d.Digest == h.Digest &&
			d.Verdict == action.Approve {
			return true
		}
	}
	return false
}

func (tr *truth) response(target, act string) *profileResponse {
	if tr.e.p.profile == nil {
		return nil
	}
	t := tr.targets[target]
	for _, r := range tr.e.p.profile.Responses {
		if t != nil && r.Target.Name == t.Name && r.Target.Namespace == t.Namespace && r.Action.String() == act {
			return &profileResponse{Owner: r.Owner, Human: len(r.HumanDecisionReasons()) > 0, Execution: r.Execution != nil}
		}
	}
	return nil
}

type profileResponse struct {
	Owner string
	// Human: the declaration itself calls for a human decision (approval,
	// risks, interruption), so a policy approval cannot authorize it.
	Human     bool
	Execution bool
}

func (tr *truth) expectedImpact(target, act string) []string {
	t := tr.targets[target]
	var out []string
	if tr.e.p.profile == nil || t == nil {
		return nil
	}
	for _, r := range tr.e.p.profile.Responses {
		if r.Target.Name == t.Name && r.Action.String() == act && r.Execution != nil {
			for _, c := range r.Execution.ExpectedImpact {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// settle reconciles the initial world and records each capability's
// baseline state, before any scenario step.
func (e *confEnv) settle() {
	e.t.Helper()
	for r := 0; r < 3; r++ {
		if r > 0 {
			e.clk.add(3 * time.Second)
		}
		e.reconcile()
	}
	e.truth.baseState = map[string]string{}
	for name, st := range e.claims() {
		for _, c := range st.Capabilities {
			e.truth.baseState[name+"/"+c.Type.Name] = c.State
		}
	}
	e.truth.baseline = e.truth.facts()
	e.truth.touched = map[string]bool{}
	e.check()
}
