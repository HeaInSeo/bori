package action

import (
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
)

// assessRecovery judges one execution that its provider definitively
// ended. It re-runs O1 on the current snapshot restricted to observations
// made strictly after BORI recorded that definitive result (EndedAt), so
// neither pre-execution evidence nor the provider's result can prove
// recovery. The target's identity and bindings must be unchanged; otherwise
// nothing of the old identity is reused. An attempt without a definitive
// end (refused, timed out, withdrawn) is never assessed.
func assessRecovery(r Record, s operations.Snapshot, now time.Time) (Recovery, []CapRecovery, error) {
	t, ok := targetOf(s, r.TargetUID)
	if !ok || !bindingOf(t).equal(r.Binding) {
		return RecoveryInapplicable, nil, nil
	}
	post := s
	post.At = now
	post.Observations = nil
	for _, o := range s.Observations {
		if o.ObservedAt.After(r.EndedAt) {
			post.Observations = append(post.Observations, o)
		}
	}
	a, err := operations.Evaluate(post)
	if err != nil {
		return RecoveryNone, nil, err
	}
	ta, ok := a.Target(r.TargetUID)
	if !ok || !ta.Valid {
		return RecoveryInapplicable, nil, nil
	}
	var detail []CapRecovery
	recovered, proven := 0, 0
	for _, c := range r.Expected {
		st := operations.Unknown
		if res, ok := capState(ta, c); ok {
			st = res.State
		}
		detail = append(detail, CapRecovery{Type: c, State: st})
		switch st {
		case operations.Available:
			recovered++
		case operations.Degraded, operations.Unavailable:
			proven++
		}
	}
	switch {
	case recovered == len(r.Expected):
		return RecoveryRecovered, detail, nil
	case now.Before(r.EndedAt.Add(r.RecoveryWindow)):
		return RecoveryPending, detail, nil
	case recovered > 0:
		return RecoveryPartial, detail, nil
	case proven > 0:
		return RecoveryNotRecovered, detail, nil
	default:
		return RecoveryUnconfirmed, detail, nil
	}
}
