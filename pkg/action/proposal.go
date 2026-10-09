package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/interaction"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Proposal is a deterministic ActionProposal derived from the O1 assessment
// and one declared response with an execution block. It is not stored and
// authorises nothing.
type Proposal struct {
	ID      string
	Digest  string
	Subject string
	Episode int

	Response        interaction.Response
	ProfileRevision string
	Binding         Binding
	For             operations.CapabilityType
	Expected        []operations.CapabilityType
	Trigger         operations.CapabilityState
	TriggerReasons  []string
	// Preconditions carry the proof state as Code (proven, unproven, unmet).
	Preconditions []Reason
	// Human are the reasons a person must decide: the response's declared
	// ones plus no-priority-authority when another declared response
	// (executable or not) addresses the same capability of the target now.
	// Empty means the declared profile is the policy approval.
	Human []string
	// OwnerConflict: the same action is declared for this capability with
	// different owners; execution authority is ambiguous.
	OwnerConflict bool
}

// basis is everything a proposal's meaning depends on. It holds no evidence
// reference and no time, so evidence refreshes do not change the digest.
type basis struct {
	ID              string
	Trigger         operations.CapabilityState
	TriggerReasons  []string
	Preconditions   []Reason
	Binding         Binding
	Human           []string
	OwnerConflict   bool
	Owner           string
	Risks           []string
	Execution       interaction.Execution
	ProfileRevision string
}

type subject struct {
	Target      operations.TargetIdentity
	ResolvedUID string
	Contract    operations.ContractIdentity
	Action      interaction.ActionRef
	For         operations.CapabilityType
	Profile     string
}

func digestOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain data only
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func capType(c opsv1.CapabilityType) operations.CapabilityType {
	return operations.CapabilityType{Domain: c.Domain, Name: c.Name, Revision: c.Revision}
}

// bindingOf captures a target's exact identity and bindings.
func bindingOf(t operations.Target) Binding {
	b := Binding{Target: t.Identity, ResolvedUID: t.ResolvedUID, Contract: t.ContractRef}
	for _, ab := range t.AssertionBindings {
		b.Providers = append(b.Providers, ab.Slot+"="+ab.Provider.String())
	}
	for _, db := range t.DependencyBindings {
		b.Dependencies = append(b.Dependencies, db.Slot+"="+db.TargetUID+"/"+db.Contract.String())
	}
	sort.Strings(b.Providers)
	sort.Strings(b.Dependencies)
	return b
}

func targetOf(s operations.Snapshot, uid string) (operations.Target, bool) {
	for _, t := range s.Targets {
		if t.Identity.UID == uid {
			return t, true
		}
	}
	return operations.Target{}, false
}

func capState(ta operations.TargetAssessment, c operations.CapabilityType) (operations.CapabilityResult, bool) {
	for _, r := range ta.Capabilities {
		if r.Type == c {
			return r, true
		}
	}
	return operations.CapabilityResult{}, false
}

// SubjectKey identifies the proposal subject of a response for a target.
func subjectKey(t operations.Target, r interaction.Response, profile string) string {
	return digestOf(subject{
		Target: t.Identity, ResolvedUID: t.ResolvedUID, Contract: t.ContractRef,
		Action: r.Action, For: capType(r.For), Profile: profile,
	})
}

// derive returns the proposals of every valid target, ordered by target UID
// then proposal ID. episodes maps a subject key to its current episode.
//
// The human-decision boundary is the one O4 shows: several declared
// responses for the same capability of the same target have no priority
// authority, so none of them is approved by policy; the same action
// declared with different owners has ambiguous authority and is blocked.
func derive(s operations.Snapshot, a operations.Assessment, p *interaction.Profile, episodes func(string) int) []Proposal {
	if p == nil {
		return nil
	}
	var out []Proposal
	for _, ta := range a.Targets {
		if !ta.Valid {
			continue
		}
		t, ok := targetOf(s, ta.Target.UID)
		if !ok {
			continue
		}
		actions := map[operations.CapabilityType]map[string]bool{} // capability → declared actions
		owners := map[string]map[string]bool{}                     // capability|action → owners
		var matching []interaction.Response
		for _, r := range p.Responses {
			if r.Target.Namespace != t.Identity.Namespace || r.Target.Name != t.Identity.Name ||
				(r.Target.UID != "" && r.Target.UID != t.Identity.UID) {
				continue
			}
			c, ok := capState(ta, capType(r.For))
			if !ok || !triggers(r, c.State) {
				continue
			}
			k := capType(r.For)
			if actions[k] == nil {
				actions[k] = map[string]bool{}
			}
			actions[k][r.Action.String()] = true
			ok2 := k.String() + "|" + r.Action.String()
			if owners[ok2] == nil {
				owners[ok2] = map[string]bool{}
			}
			owners[ok2][r.Owner] = true
			matching = append(matching, r)
		}
		seen := map[string]bool{}
		for _, r := range matching {
			if r.Execution == nil {
				continue
			}
			k := capType(r.For)
			c, _ := capState(ta, k)
			pr := proposalOf(t, ta, r, p.Revision, c, episodes, len(actions[k]) > 1, len(owners[k.String()+"|"+r.Action.String()]) > 1)
			if seen[pr.ID] { // the same action declared twice: one proposal
				continue
			}
			seen[pr.ID] = true
			out = append(out, pr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Binding.Target.UID != out[j].Binding.Target.UID {
			return out[i].Binding.Target.UID < out[j].Binding.Target.UID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func triggers(r interaction.Response, s operations.CapabilityState) bool {
	if len(r.States) == 0 {
		return s == operations.Degraded || s == operations.Unavailable
	}
	return contains(r.States, string(s))
}

func proposalOf(t operations.Target, ta operations.TargetAssessment, r interaction.Response, profile string,
	c operations.CapabilityResult, episodes func(string) int, competing, ownerConflict bool) Proposal {
	key := subjectKey(t, r, profile)
	ep := episodes(key)
	pr := Proposal{
		Subject: key, Episode: ep, Response: r, ProfileRevision: profile,
		Binding: bindingOf(t), For: c.Type, Trigger: c.State, Human: r.HumanDecisionReasons(),
		OwnerConflict: ownerConflict,
	}
	if competing {
		pr.Human = append(pr.Human, ReasonNoPriorityAuthority)
	}
	pr.ID = digestOf(struct {
		Subject string
		Episode int
	}{key, ep})[:32]
	for _, x := range r.Execution.ExpectedImpact {
		pr.Expected = append(pr.Expected, capType(x))
	}
	for _, rs := range c.Reasons {
		pr.TriggerReasons = append(pr.TriggerReasons, string(rs.Code)+":"+rs.Subject)
	}
	sort.Strings(pr.TriggerReasons)
	for _, pc := range r.Preconditions {
		code := "unproven"
		if res, ok := capState(ta, capType(pc)); ok {
			switch res.State {
			case operations.Available:
				code = "proven"
			case operations.Degraded, operations.Unavailable:
				code = "unmet"
			}
		}
		pr.Preconditions = append(pr.Preconditions, Reason{Code: code, Subject: capType(pc).String()})
	}
	sort.Slice(pr.Preconditions, func(i, j int) bool { return pr.Preconditions[i].Subject < pr.Preconditions[j].Subject })
	pr.Digest = digestOf(basis{
		ID: pr.ID, Trigger: pr.Trigger, TriggerReasons: pr.TriggerReasons, Preconditions: pr.Preconditions,
		Binding: pr.Binding, Human: pr.Human, OwnerConflict: ownerConflict, Owner: r.Owner, Risks: sortedCopy(r.Risks),
		Execution: *r.Execution, ProfileRevision: profile,
	})
	return pr
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
