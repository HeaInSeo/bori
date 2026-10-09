package action

import (
	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/operations"
)

// Status bounds (mirrored by the CRD schema).
const (
	maxStatusActions = 8
	maxStatusReasons = 16
	maxStatusDetail  = 8
)

func opsType(c operations.CapabilityType) opsv1.CapabilityType {
	return opsv1.CapabilityType{Domain: c.Domain, Name: c.Name, Revision: c.Revision}
}

// Status projects one target's views into the bounded, non-authoritative
// status form. Views keep their deterministic order.
func Status(vs []View) []opsv1.InteractionAction {
	var out []opsv1.InteractionAction
	for _, v := range vs {
		if len(out) == maxStatusActions {
			break
		}
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
