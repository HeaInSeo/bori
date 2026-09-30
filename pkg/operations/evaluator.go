package operations

import "errors"

// ErrNoEvaluationInstant is returned when the snapshot has no evaluation
// instant. Currentness is never inferred from a wall clock.
var ErrNoEvaluationInstant = errors.New("operations: snapshot.At is required")

// Evaluate derives the capability and envelope assessment of every target in
// the snapshot. It is a pure function of its input.
func Evaluate(s Snapshot) (Assessment, error) {
	if s.At.IsZero() {
		return Assessment{}, ErrNoEvaluationInstant
	}
	return Assessment{}, nil
}
