// Package operations is the app-neutral semantic core of BORI's operational
// contract model (Sprint O1).
//
// It evaluates, as a pure deterministic function, which capabilities of a set
// of bound operational targets are AVAILABLE, DEGRADED, UNAVAILABLE or UNKNOWN
// given an explicit set of current observations and an explicit evaluation
// instant, and whether each declared operational envelope is satisfied.
//
// Scope and boundaries:
//
//   - This package realizes only the R2-D evidence + capability composition
//     layer. It does not implement Intent, Sync derivation, release,
//     installation, verification or qualification semantics. Sync is always
//     reported as NotApplicable because no authoritative Sync source is bound.
//   - It never mutates workloads and has no Kubernetes, network, clock or
//     storage dependency. Identities (UIDs, spec digests, provider config
//     revisions) are supplied as plain inputs; their enforcement belongs to a
//     later Kubernetes realization.
//   - The types here are semantic types, not the public CRD wire format.
//   - There is no global health ordering and no target- or platform-level
//     health scalar. Every capability is derived only from the requirements it
//     explicitly references.
package operations
