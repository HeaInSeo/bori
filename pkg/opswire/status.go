package opswire

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// ConditionAssessmentReady is the only status condition. It reports whether
// the controller produced an assessment (protocol state), never operational
// truth.
const ConditionAssessmentReady = "AssessmentReady"

// Project builds the semantic status of one target from the O1 assessment.
// It copies O1 results and adds only wire-level facts; it computes no state.
// The result has no timestamps and no conditions; see Apply.
func Project(t *opsv1.OperationalTarget, b Built, a operations.Assessment) opsv1.OperationalTargetStatus {
	tw := b.Wire[string(t.UID)]
	st := opsv1.OperationalTargetStatus{
		ObservedGeneration: t.Generation,
		Identity: opsv1.ObservedIdentity{
			TargetUID:          string(t.UID),
			ResolvedUID:        tw.ResolvedUID,
			ContractName:       tw.Contract.Name,
			ContractUID:        tw.Contract.UID,
			ContractSpecDigest: tw.Contract.SpecDigest,
		},
		Sync:              string(operations.SyncNotApplicable),
		EnvelopeSelection: t.Spec.EnvelopeSelection,
		DeniedReferences:  tw.Denied,
	}
	if tw.Failure != "" {
		st.InvalidReasons = []opsv1.StatusReason{{Code: tw.Failure, Subject: "targetRef:" + t.Spec.TargetRef.Kind + "/" + t.Spec.TargetRef.Name}}
		return st
	}
	ta, ok := a.Target(string(t.UID))
	if !ok {
		st.InvalidReasons = []opsv1.StatusReason{{Code: "not-assessed"}}
		return st
	}
	st.Valid = ta.Valid
	st.InvalidReasons = limit(reasons(ta.InvalidReasons, tw, t.Namespace, b.namespaceOf), maxInvalidReasons)
	for _, e := range ta.Evidence {
		ref := e.EvidenceRef
		if len(ref) > maxEvidenceRef {
			ref = ""
		}
		st.Evidence = append(st.Evidence, opsv1.EvidenceStatus{
			Slot:           e.Slot,
			State:          string(e.Status),
			Provider:       e.Provider.Name,
			ConfigRevision: e.Provider.ConfigRevision,
			EvidenceRef:    ref,
		})
	}
	for _, c := range ta.Capabilities {
		st.Capabilities = append(st.Capabilities, opsv1.CapabilityStatus{
			Type:    opsv1.CapabilityType{Domain: c.Type.Domain, Name: c.Type.Name, Revision: c.Type.Revision},
			State:   string(c.State),
			Reasons: limit(reasons(c.Reasons, tw, t.Namespace, b.namespaceOf), maxReasons),
		})
	}
	for _, e := range ta.Envelopes {
		st.Envelopes = append(st.Envelopes, opsv1.EnvelopeStatus{
			Name:    e.Name,
			Class:   string(e.Class),
			State:   string(e.State),
			Reasons: limit(reasons(e.Reasons, tw, t.Namespace, b.namespaceOf), maxReasons),
		})
	}
	return st
}

// reasons copies O1 reasons into bounded wire reasons. For a dependency bound
// into another namespace only the reason code, subject and the dependency's
// capability state are exposed, never details about the other namespace's
// objects or evidence. A dependency-cycle detail is kept only when every
// cycle member is a target in the projecting target's own namespace;
// otherwise it would disclose targets the projecting target never bound.
func reasons(rs []operations.Reason, tw TargetWire, ns string, namespaceOf map[string]string) []opsv1.StatusReason {
	var out []opsv1.StatusReason
	for _, r := range rs {
		detail := r.Detail
		switch {
		case r.Code == operations.ReasonDependencyCycle:
			if !cycleWithinNamespace(detail, ns, namespaceOf) {
				detail = ""
			}
		case strings.HasPrefix(string(r.Code), "dependency-") && r.Code != operations.ReasonDependencyUnmet &&
			tw.CrossNamespaceSlots[dependencySlot(r.Subject)]:
			detail = ""
		}
		out = append(out, opsv1.StatusReason{
			Code:    string(r.Code),
			Subject: truncate(r.Subject, maxSubject),
			Detail:  truncate(detail, maxDetail),
		})
	}
	return out
}

// cycleWithinNamespace reports whether an O1 cycle detail ("cycle members:
// <uid>/<capability>,...") names only targets in ns. Anything it cannot parse
// is treated as foreign (fail closed).
func cycleWithinNamespace(detail, ns string, namespaceOf map[string]string) bool {
	members, ok := strings.CutPrefix(detail, "cycle members: ")
	if !ok || members == "" {
		return false
	}
	for _, m := range strings.Split(members, ",") {
		uid, _, ok := strings.Cut(m, "/")
		if !ok || uid == "" {
			return false
		}
		if got, known := namespaceOf[uid]; !known || got != ns {
			return false
		}
	}
	return true
}

func dependencySlot(subject string) string {
	s := strings.TrimPrefix(subject, "dependency:")
	if i := strings.Index(s, "->"); i >= 0 {
		return s[:i]
	}
	return s
}

func limit(rs []opsv1.StatusReason, n int) []opsv1.StatusReason {
	if len(rs) > n {
		return rs[:n]
	}
	return rs
}

// Apply merges a freshly projected semantic status into the current status.
// It returns changed=false when nothing semantic differs, in which case the
// caller must not write. Otherwise AssessedAt is set to now, and the
// AssessmentReady condition's LastTransitionTime moves only if its status
// actually transitions.
func Apply(current, desired opsv1.OperationalTargetStatus, now time.Time) (opsv1.OperationalTargetStatus, bool) {
	next := *desired.DeepCopy()
	next.Conditions = append([]metav1.Condition(nil), current.Conditions...)
	cond := metav1.Condition{
		Type:               ConditionAssessmentReady,
		Status:             metav1.ConditionFalse,
		Reason:             "InvalidTarget",
		ObservedGeneration: desired.ObservedGeneration,
		LastTransitionTime: metav1.NewTime(now),
	}
	if desired.Valid {
		cond.Status, cond.Reason = metav1.ConditionTrue, "Assessed"
	}
	meta.SetStatusCondition(&next.Conditions, cond)
	next.AssessedAt = current.AssessedAt

	if SemanticEqual(current, next) {
		return current, false
	}
	at := metav1.NewTime(now)
	next.AssessedAt = &at
	return next, true
}

// SemanticEqual compares two statuses ignoring AssessedAt and condition
// transition times; nil and empty lists are equal.
func SemanticEqual(a, b opsv1.OperationalTargetStatus) bool {
	return equality.Semantic.DeepEqual(stripTimes(a), stripTimes(b))
}

func stripTimes(s opsv1.OperationalTargetStatus) opsv1.OperationalTargetStatus {
	c := *s.DeepCopy()
	c.AssessedAt = nil
	for i := range c.Conditions {
		c.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return c
}

// ContractStatusEqual reports whether two contract statuses are semantically
// equal.
func ContractStatusEqual(a, b opsv1.OperationalContractStatus) bool {
	return equality.Semantic.DeepEqual(a, b)
}
