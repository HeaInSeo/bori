package action_test

import (
	"context"
	"reflect"
	"testing"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/action"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// The journal stores and returns copies: changing the slices of a written
// or returned record never changes the stored execution contract.
func TestJournalCopiesTheExecutionContract(t *testing.T) {
	ctx := context.Background()
	j := action.NewSimulatedDurableJournal()
	fence, err := j.Epoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contract := func() interaction.Execution {
		return interaction.Execution{Provider: "actor", Approvers: []string{"alice"},
			ExpectedImpact: []opsv1.CapabilityType{opsCap("persist")}, MayInterrupt: []opsv1.CapabilityType{opsCap("serve")}}
	}
	scribble := func(e *interaction.Execution) {
		e.Approvers[0] = "mallory"
		e.ExpectedImpact[0] = opsCap("other")
		e.MayInterrupt[0] = opsCap("other")
	}
	in := action.Record{Key: "p/1", ProposalID: "p", Fence: fence, Execution: contract()}
	got, err := j.Put(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	scribble(&in.Execution)  // the caller's input
	scribble(&got.Execution) // the returned record
	list, err := j.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scribble(&list[0].Execution) // a read
	list, _ = j.List(ctx)
	if len(list) != 1 || !reflect.DeepEqual(list[0].Execution, contract()) {
		t.Fatalf("stored contract changed: %+v", list)
	}
}
