package operations

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// shuffle permutes every slice whose order is semantically irrelevant.
func shuffle(r *rand.Rand, s Snapshot) Snapshot {
	perm := func(n int, swap func(i, j int)) { r.Shuffle(n, swap) }
	perm(len(s.Contracts), func(i, j int) { s.Contracts[i], s.Contracts[j] = s.Contracts[j], s.Contracts[i] })
	perm(len(s.Targets), func(i, j int) { s.Targets[i], s.Targets[j] = s.Targets[j], s.Targets[i] })
	perm(len(s.Observations), func(i, j int) { s.Observations[i], s.Observations[j] = s.Observations[j], s.Observations[i] })
	for ci := range s.Contracts {
		c := &s.Contracts[ci]
		perm(len(c.Assertions), func(i, j int) { c.Assertions[i], c.Assertions[j] = c.Assertions[j], c.Assertions[i] })
		perm(len(c.DependencySlots), func(i, j int) {
			c.DependencySlots[i], c.DependencySlots[j] = c.DependencySlots[j], c.DependencySlots[i]
		})
		perm(len(c.Capabilities), func(i, j int) { c.Capabilities[i], c.Capabilities[j] = c.Capabilities[j], c.Capabilities[i] })
		perm(len(c.Envelopes), func(i, j int) { c.Envelopes[i], c.Envelopes[j] = c.Envelopes[j], c.Envelopes[i] })
		for k := range c.Capabilities {
			rq := c.Capabilities[k].Requirements
			perm(len(rq), func(i, j int) { rq[i], rq[j] = rq[j], rq[i] })
		}
		for k := range c.Envelopes {
			rq := c.Envelopes[k].Requirements
			perm(len(rq), func(i, j int) { rq[i], rq[j] = rq[j], rq[i] })
		}
	}
	for ti := range s.Targets {
		t := &s.Targets[ti]
		perm(len(t.AssertionBindings), func(i, j int) {
			t.AssertionBindings[i], t.AssertionBindings[j] = t.AssertionBindings[j], t.AssertionBindings[i]
		})
		perm(len(t.DependencyBindings), func(i, j int) {
			t.DependencyBindings[i], t.DependencyBindings[j] = t.DependencyBindings[j], t.DependencyBindings[i]
		})
	}
	return s
}

func TestDeterminismUnderReorderingAndRepetition(t *testing.T) {
	fixtures := map[string]func() Snapshot{
		"http-degraded":          func() Snapshot { return httpServiceSnapshot(true, false, true) },
		"pipeline-resolver-down": func() Snapshot { return pipelineSnapshot(false) },
		"pipeline-stale": func() Snapshot {
			s := pipelineSnapshot(false)
			replaceObs(&s, "status-api-serving", observe(pipelineTargets().worker, "status-api-serving", Bool(true), time.Minute))
			return s
		},
		"replicated-min-only": func() Snapshot { return replicatedSnapshot(1, fresh) },
		"single-writer-split": func() Snapshot { return singleWriterSnapshot(2, "primary") },
		"conflict": func() Snapshot {
			s := httpServiceSnapshot(true, true, true)
			s.Observations = append(s.Observations, observe(httpServiceTarget(), "api-serving", Bool(false), fresh))
			return s
		},
	}
	r := rand.New(rand.NewSource(1))
	for name, build := range fixtures {
		t.Run(name, func(t *testing.T) {
			want := mustEval(t, build())
			for i := 0; i < 200; i++ {
				got := mustEval(t, shuffle(r, build()))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("iteration %d: nondeterministic assessment\n got %+v\nwant %+v", i, got, want)
				}
			}
		})
	}
}

func TestEvaluateDoesNotMutateInput(t *testing.T) {
	s := pipelineSnapshot(false)
	before := pipelineSnapshot(false)
	mustEval(t, s)
	if !reflect.DeepEqual(s, before) {
		t.Fatal("Evaluate mutated its input")
	}
}
