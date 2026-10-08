package interaction

import (
	"fmt"
	"sort"
	"strings"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
)

// Overview counts targets per interaction level. It is deliberately not a
// single level or health value: there is no worst-of over targets.
type Overview struct {
	Targets int `json:"targets"`
	// NothingAssessed is true when there is no target at all; that is never
	// NO_ACTION.
	NothingAssessed bool                           `json:"nothingAssessed"`
	ByLevel         map[opsv1.InteractionLevel]int `json:"byLevel"`
	// WithoutSummary counts targets whose status has no interaction
	// summary (projection disabled or not yet reconciled).
	WithoutSummary int `json:"withoutSummary"`
}

// Overview of a list of targets as read from the API.
func OverviewOf(ts []opsv1.OperationalTarget) Overview {
	o := Overview{Targets: len(ts), NothingAssessed: len(ts) == 0, ByLevel: map[opsv1.InteractionLevel]int{}}
	for _, t := range ts {
		if t.Status.Interaction == nil {
			o.WithoutSummary++
			continue
		}
		o.ByLevel[t.Status.Interaction.Level]++
	}
	return o
}

// RenderText renders targets for a person. It prints only what the status
// holds; it reads nothing else.
func RenderText(ts []opsv1.OperationalTarget) string {
	var b strings.Builder
	o := OverviewOf(ts)
	if o.NothingAssessed {
		b.WriteString("No OperationalTarget found: nothing is assessed (this is not NO_ACTION).\n")
		return b.String()
	}
	sorted := append([]opsv1.OperationalTarget(nil), ts...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Namespace != sorted[j].Namespace {
			return sorted[i].Namespace < sorted[j].Namespace
		}
		return sorted[i].Name < sorted[j].Name
	})
	var levels []string
	for _, l := range []opsv1.InteractionLevel{opsv1.LevelImmediateIntervention, opsv1.LevelDecisionRequired, opsv1.LevelAwareness, opsv1.LevelNoAction} {
		levels = append(levels, fmt.Sprintf("%s=%d", l, o.ByLevel[l]))
	}
	fmt.Fprintf(&b, "%d targets: %s, without summary=%d (counts only; no overall health)\n", o.Targets, strings.Join(levels, " "), o.WithoutSummary)
	for _, t := range sorted {
		b.WriteString("\n")
		renderTarget(&b, t)
	}
	return b.String()
}

func renderTarget(b *strings.Builder, t opsv1.OperationalTarget) {
	s := t.Status.Interaction
	fmt.Fprintf(b, "%s/%s\n", t.Namespace, t.Name)
	if s == nil {
		b.WriteString("  no interaction summary (projection disabled or not yet reconciled)\n")
		return
	}
	recurring := ""
	if s.Recurring {
		recurring = fmt.Sprintf(" (recurring, %d re-entries)", s.Recurrences)
	}
	fmt.Fprintf(b, "  level:      %s%s\n", s.Level, recurring)
	fmt.Fprintf(b, "  problem:    %s\n", s.Summary)
	line(b, "why", reasonList(s.LevelReasons))
	line(b, "affected", capLines(s.Affected))
	line(b, "unknown", capLines(s.Unknown))
	line(b, "available", capLines(s.Unaffected))
	line(b, "confirmed", factLines(s.ConfirmedFacts, true))
	line(b, "missing", factLines(s.MissingEvidence, false))
	var cands []string
	for _, c := range s.CauseCandidates {
		cands = append(cands, c.Status+" (hypothesis): "+c.ID)
	}
	line(b, "candidates", cands)
	if inv := s.Investigation; inv != nil {
		fmt.Fprintf(b, "  checked:    %s; hypotheses refuted=%d maintained=%d open=%d\n", inv.Outcome, inv.Refuted, inv.Maintained, inv.Open)
	}
	var rs []string
	for _, r := range s.Responses {
		owner := r.Owner
		if owner == "" {
			owner = "<undeclared>"
		}
		rs = append(rs, fmt.Sprintf("%s for %s on %s, owner %s: %s%s (display only)", r.Name, r.For.Name, r.Target, owner, r.Status, preconds(r)))
	}
	line(b, "responses", rs)
	line(b, "human", reasonList(s.HumanReasons))
	line(b, "pending", reasonList(s.PendingPostConditions))
}

func line(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "  %-11s %s\n", label+":", items[0])
	for _, it := range items[1:] {
		fmt.Fprintf(b, "  %-11s %s\n", "", it)
	}
}

func reasonList(rs []opsv1.StatusReason) []string {
	var out []string
	for _, r := range rs {
		s := r.Code
		if r.Subject != "" {
			s += " " + r.Subject
		}
		if r.Detail != "" {
			s += " (" + r.Detail + ")"
		}
		out = append(out, s)
	}
	return out
}

func capLines(cs []opsv1.InteractionCapability) []string {
	var out []string
	for _, c := range cs {
		var codes []string
		for _, r := range c.Reasons {
			codes = append(codes, r.Code)
		}
		s := typeKey(c.Type) + " " + c.State
		if len(codes) > 0 {
			s += ": " + strings.Join(codes, ", ")
		}
		out = append(out, s)
	}
	return out
}

func factLines(es []opsv1.EvidenceStatus, withRef bool) []string {
	var out []string
	for _, e := range es {
		s := e.Slot + " " + e.State
		if e.Provider != "" {
			s += " via " + e.Provider + "@" + e.ConfigRevision
		}
		if withRef && e.EvidenceRef != "" {
			s += " ref " + e.EvidenceRef
		}
		out = append(out, s)
	}
	return out
}

func preconds(r opsv1.InteractionResponse) string {
	if len(r.Preconditions) == 0 {
		return ""
	}
	var p []string
	for _, c := range r.Preconditions {
		p = append(p, c.Subject+"="+c.Code)
	}
	return "; preconditions " + strings.Join(p, ", ")
}
