package action

import (
	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Status bounds per action (mirrored by the CRD schema). The number of
// actions shown is bounded by the interaction projection, after it has
// judged escalation on all of them.
const (
	maxStatusReasons = 16
	maxStatusDetail  = 8
)

func opsType(c operations.CapabilityType) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: c.Domain, Name: c.Name, Revision: c.Revision}
}

// Status projects every view of one target into the non-authoritative
// status form, each action bounded. Views keep their deterministic order.
// It does not drop actions: interaction.Project needs all of them to decide
// escalation and shows only the first eight.
func Status(vs []View) []opsv1.InteractionAction {
	var out []opsv1.InteractionAction
	for _, v := range vs {
		a := opsv1.InteractionAction{
			Proposal: v.ProposalID, Digest: v.Digest, Name: v.Action.String(), For: opsType(v.For),
			Phase: string(v.Phase), Attempt: int32(v.Attempt), Recovery: string(v.Recovery),
		}
		for _, r := range v.Reasons {
			if len(a.Reasons) == maxStatusReasons {
				break
			}
			a.Reasons = append(a.Reasons, opsv1.StatusReason{Code: r.Code, Subject: r.Subject})
		}
		for _, d := range v.Detail {
			if len(a.RecoveryDetail) == maxStatusDetail {
				break
			}
			a.RecoveryDetail = append(a.RecoveryDetail, opsv1.StatusReason{Code: string(d.State), Subject: d.Type.String()})
		}
		out = append(out, a)
	}
	return out
}
