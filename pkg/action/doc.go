// Package action is the O5 reference model of ActionProposal, approval,
// handoff to an external ActionProvider and RecoveryAssessment.
//
// BORI never mutates a workload here. A proposal is derived
// deterministically from the O1 assessment and an operator-declared
// response with an execution block. A human or policy approval is accepted
// only for the exact proposal ID and digest, and only from a verified,
// declared approver. A handoff is persisted to a journal before it is sent,
// fenced by the journal epoch and keyed for idempotency. A receipt is
// durable acceptance only, a result is not recovery, and recovery is judged
// only by re-running O1 on evidence observed after BORI recorded the
// execution's end, for the unchanged identity.
//
// The storage, approval-identity and provider-transport questions of API
// v0.1 §19 stay OPEN (see docs/operational-actions.md). This package defines
// the interfaces those decisions must satisfy and ships only a non-durable
// in-memory journal, which disables handoff.
package action
