package opswire

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
)

// unresolvedWorker: the worker in namespace "a" binds its dependency to the
// resolver in namespace "b" and its status-up provider to namespace "b". The
// worker's own targetRef is unresolved with the given failure.
func unresolvedWorker(failure string, depGranted, providerGranted bool) *world {
	w := crossNS()
	w.targets[1].Spec.AssertionBindings[1].Provider.Namespace = "b"
	if depGranted {
		w.grants = append(w.grants, depGrant("a", "resolver"))
	}
	if providerGranted {
		w.grants = append(w.grants, grant("b", "a", opsv1.ReferenceGrantTo{Type: opsv1.ReferenceEvidenceProvider, Name: "kubernetes-workload-status"}))
	}
	w.resolved = map[types.NamespacedName]Resolution{{Namespace: "a", Name: "worker"}: {Failure: failure}}
	return w
}

func TestUnresolvedTargetStillReportsDeniedReferences(t *testing.T) {
	depDenied := opsv1.DeniedReference{Type: opsv1.ReferenceDependency, Slot: "resolver", Namespace: "b"}
	provDenied := opsv1.DeniedReference{Type: opsv1.ReferenceEvidenceProvider, Slot: "status-up", Namespace: "b"}
	cases := []struct {
		name                        string
		depGranted, providerGranted bool
		want                        []opsv1.DeniedReference
	}{
		{"dependency and provider without grant", false, false, []opsv1.DeniedReference{depDenied, provDenied}},
		{"dependency without grant, provider granted", false, true, []opsv1.DeniedReference{depDenied}},
		{"dependency granted, provider without grant", true, false, []opsv1.DeniedReference{provDenied}},
		{"dependency and provider granted", true, true, nil},
	}
	for _, failure := range []string{ReasonTargetNotFound, ReasonTargetKindUnsupported} {
		for _, tc := range cases {
			t.Run(failure+"/"+tc.name, func(t *testing.T) {
				w := unresolvedWorker(failure, tc.depGranted, tc.providerGranted)
				st := w.status("worker")

				if st.Valid || len(st.InvalidReasons) != 1 || st.InvalidReasons[0].Code != failure {
					t.Fatalf("unresolved target status %+v", st)
				}
				if len(st.Capabilities) != 0 || len(st.Envelopes) != 0 || len(st.Evidence) != 0 {
					t.Fatalf("unresolved target was assessed: %+v", st)
				}
				if !reflect.DeepEqual(st.DeniedReferences, tc.want) {
					t.Fatalf("deniedReferences = %+v, want %+v", st.DeniedReferences, tc.want)
				}

				// Still excluded from the O1 evaluation input.
				w.resolveAll()
				for _, ot := range Build(w.input()).Snapshot.Targets {
					if ot.Identity.UID == "t-wrk" {
						t.Fatal("unresolved target handed to the O1 evaluator")
					}
				}

				// The report does not depend on whether the targetRef resolves.
				resolved := unresolvedWorker(failure, tc.depGranted, tc.providerGranted)
				resolved.resolved = nil
				if got := resolved.status("worker").DeniedReferences; !reflect.DeepEqual(got, st.DeniedReferences) {
					t.Fatalf("denied references differ once resolved: %+v vs %+v", got, st.DeniedReferences)
				}

				// No existence oracle: identical whether the denied referent exists.
				gone := unresolvedWorker(failure, tc.depGranted, tc.providerGranted)
				gone.targets = gone.targets[1:]
				if !reflect.DeepEqual(gone.status("worker"), st) {
					t.Fatal("status of an unresolved target reveals whether a referent exists")
				}

				// Repeated equal evaluation is not a semantic change.
				first, changed := Apply(opsv1.OperationalTargetStatus{}, st, at)
				if !changed {
					t.Fatal("first status not written")
				}
				for i := 1; i <= 5; i++ {
					again := unresolvedWorker(failure, tc.depGranted, tc.providerGranted).status("worker")
					if _, changed := Apply(first, again, at.Add(time.Duration(i)*time.Minute)); changed {
						t.Fatalf("repeat %d rewrote status", i)
					}
				}
			})
		}
	}
}
