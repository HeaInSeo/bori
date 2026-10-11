# Operational API (`ops.bori.dev/v1alpha1`) — O2 candidate public wire

> **Status: CANDIDATE. Not a compatibility promise.** The group `ops.bori.dev`
> and version `v1alpha1` are a candidate wire namespace. They may be renamed
> or changed without migration support until central and Guardrail
> acceptance. Nothing here has been published or released.

## What it is

Three namespaced kinds express the landed O1 semantic core
(`pkg/operations`) in Kubernetes:

| Kind | Role |
|---|---|
| `OperationalContract` | Reusable, **immutable** app-neutral contract: assertion slots, dependency slots, capabilities with per-requirement `onUnmet`, envelopes. |
| `OperationalTarget` | Binds one workload (`targetRef`) to a same-namespace contract, assertion providers and pinned dependencies. Status carries the assessment. |
| `OperationalReferenceGrant` | Explicit, typed cross-namespace trust from the referent's namespace owner. |

There is no ActionProposal, action, remediation, provider or investigation
kind. The legacy `bori.dev` kinds (BoriDataPlane, BoriRelease, BoriRevision,
BoriVerificationRun) are untouched and unrelated.

## Semantics are owned by O1

```
wire objects → resolve / trust / pin (pkg/opswire) → operations.Snapshot
             → operations.Evaluate (pkg/operations) → bounded status projection
```

`pkg/opswire` translates only. Capability state, UNKNOWN composition,
dependency impact, evidence applicability, supersession, conflicts, cycles and
envelopes are evaluated exclusively by `pkg/operations`.

### Identity mapping

| O1 | Wire source |
|---|---|
| `ContractIdentity` | contract namespace, name, `metadata.uid`, `specDigest` |
| `specDigest` | `sha256` of the contract's O1 semantics with order-free lists sorted and durations by value. It is reported in `OperationalContract.status.specDigest`. |
| `TargetIdentity` | target namespace, name, `metadata.uid` |
| `ResolvedUID` | `metadata.uid` of `spec.targetRef`. Only metadata is read, and only for kinds in the bounded support set (default `apps/v1` Deployment, StatefulSet, DaemonSet). |
| `ProviderIdentity` | `<provider namespace>/<provider name>` + `configRevision` |
| `DependencyBinding` | pinned target UID + pinned contract name/UID/specDigest. The qualified `CapabilityType` comes from the contract requirement. |

Evidence providers must emit O1 observations keyed with the exact identity
shown in `OperationalTarget.status.identity`. If any element changes (target,
workload or contract recreation, spec digest, provider or config revision),
the held evidence is inapplicable. The result is UNKNOWN until fresh evidence
arrives. O2 defines no provider transport: the controller takes an in-process
`ObservationSource`, and the default source supplies no evidence, so results
are UNKNOWN rather than guessed.

## Contract immutability

`spec` carries the CEL rule `self == oldSelf`. New semantics are published as a
new object. Recreating a contract changes its UID, and changing semantics
changes its digest. Either change invalidates held evidence and pinned
dependencies.

## Cross-namespace trust

- `contractRef` is same-namespace only. There is no namespace field.
- A dependency or evidence-provider reference into another namespace needs a
  grant in that namespace whose `from` (kind `OperationalTarget`, exact
  namespace) and `to` (exact `type` and `name`) match. Grant types are
  `Dependency` and `EvidenceProvider` only. One type never authorizes the
  other. There are no wildcards and no selectors.
- Secret references and action references do not exist in v1alpha1. They
  cannot be expressed or granted.
- Without a grant, the binding is dropped before any lookup. The status lists
  it under `deniedReferences` without revealing whether the referent exists,
  and the affected requirements are UNKNOWN.
- A pinned dependency UID resolves only if the named target in the granted
  namespace currently has that UID.
- For cross-namespace dependencies, the status exposes only the dependency
  capability state and reason codes, never the other namespace's details or
  evidence references.

`ReferenceGrant` is semantic permission. RBAC is process permission. Both are
required, and neither replaces the other. The controller reads ops.bori.dev
objects plus `targetRef` metadata of the supported kinds. It writes only
`status`. It has no Secret access.

## Status and zero churn

`OperationalTarget.status` holds identity, validity, invalid reasons, denied
references, per-slot evidence state and reference, capability and envelope
results with bounded reasons, and `sync: NotApplicable`. There is no target
health scalar. The single condition `AssessmentReady` reports controller
protocol state, not operational truth.

A status is written only when its semantic content changes. `assessedAt`
changes only on such a write. `LastTransitionTime` changes only on a real
condition transition. The controller ignores status-only and metadata-only
updates of its own kinds (generation predicate). Every event collapses into
one cluster-wide evaluation. Periodic re-evaluation of evidence currentness
writes nothing while results are unchanged.

With `--enable-operational-interaction` the status also carries the O4
`interaction` summary: a derived, non-authoritative operator projection
(level, affected/unaffected/unknown, facts, missing evidence, what was
checked, declared responses shown only). See
[operational-interaction.md](operational-interaction.md).

With `--enable-operational-actions` (requires the interaction summary) the
summary also carries the O5 `actions` projection: derived ActionProposals,
why they wait or are blocked, and, for an execution, its result and its
RecoveryAssessment. The shipped operator registers no ActionProvider and has
only a non-durable journal, so nothing is ever handed off. See
[operational-actions.md](operational-actions.md).

## Enabling

Evidence providers (O3 reference profile, opt-in via
`--operational-provider-config`) are described in
[operational-evidence.md](operational-evidence.md).

The controller is off by default. Run `bori-operator
--enable-operational-assessment` after installing the CRDs. It never mutates
workloads, scales, restarts or invokes remediation.

## Not mappings

- Legacy `component.yaml` `contracts:` are interface contracts, not
  OperationalContract.
- `verificationPolicies` are not envelopes.
- `health.path` is not health truth.
- KSL/BVR PASS and `promotionDecision` are not live Availability or Quality.
- Generation equality, Deployment readiness, rollout state, promoted revision
  and GitOps sync status are not Sync. Sync stays NotApplicable.
