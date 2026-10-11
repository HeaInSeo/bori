package action_test

import (
	"fmt"
	"testing"

	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// Each projected action is bounded whatever the engine produced, and no
// action is dropped: the interaction projection bounds how many are shown
// after judging escalation on all of them.
func TestStatusIsBounded(t *testing.T) {
	var vs []action.View
	for i := 0; i < 20; i++ {
		v := action.View{ProposalID: fmt.Sprintf("p%02d", i), Digest: "d", Action: interaction.ActionRef{Name: "a", Revision: "r1"},
			For: cap("persist"), Phase: action.PhaseBlocked, Attempt: 1}
		for j := 0; j < 30; j++ {
			v.Reasons = append(v.Reasons, action.Reason{Code: action.ReasonPreconditionUnproven, Subject: fmt.Sprint(j)})
			v.Detail = append(v.Detail, action.CapRecovery{Type: cap("persist")})
		}
		vs = append(vs, v)
	}
	s := action.Status(vs)
	if len(s) != 20 || s[0].Proposal != "p00" || s[19].Proposal != "p19" {
		t.Fatalf("%d actions", len(s))
	}
	for _, a := range s {
		if len(a.Reasons) != 16 || len(a.RecoveryDetail) != 8 || a.Name != "a@r1" {
			t.Fatalf("%+v", a)
		}
	}
}
