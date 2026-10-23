# Gaps found by O6

Each gap is either shown by a pack scenario (named below) or explained from the product source
([SOURCES.md](SOURCES.md)). None was made to pass with a product-specific exception, and none is closed by
this change.

## Evidence and binding gaps (products and deployment)

**G1. No product publishes facts in a form a shipped provider reads, except replica counts.**
- The reference HTTP provider needs a typed response, `{subject, observedAt, values}`.
- JUMI `/statusz` returns plain JSON without subject or observation time. `/healthz` and `/readyz` are effectively constant.
- artifact-handoff `/healthz` is a constant 200.
- Tori publishes nothing.

Only Deployment replica counts (JUMI, artifact-handoff) are readable today, through `kube-status`. Real
integration needs either an adapter per product, or products publishing typed facts. The provider
transport itself remains §19 OPEN. Shown by `jumi-artifact-handoff/published-facts-only` and the
providers' comments.

**G2. Facts the contracts need do not exist in the products.**
- JUMI: registry writability and durability.
- artifact-handoff: store readability, writability and durability, and HTTP allowlist configuration.
- Tori: every fact.

BORI reports the capabilities resting on them as UNKNOWN, which is the honest result, never a guess.
Shown by `jumi-artifact-handoff/published-facts-only`. There, `accept-runs`, `resolve-node-inputs`,
`register-node-outputs`, `complete-runs` and the resolver's store capabilities are UNKNOWN, while
`query-runs` and `execute-unbound-nodes` are judged from derivable JUMI facts.

**G3. The shipped operator cannot bind Tori.**
- Targets resolve only Deployment, StatefulSet and DaemonSet (`controllers.DefaultTargetKinds`).
- Tori runs no workload (`serve` is a no-op), and its root is an external mount.
- With the shipped kinds both targets are not assessed: `target-kind-unsupported`, DECISION_REQUIRED for a spec correction (`tori/shipped-operator-cannot-bind`).

The other Tori scenarios use a declared, non-shipped extension of the target kinds (CronJob,
PersistentVolumeClaim) and an assumed deployment shape. Closing this needs either a Tori deployment
shape, or an "adapter-owned target" (API v0.1 §4.2): a decision for central.

**G4. Freshness without a heartbeat.**
- Tori's only timestamp is `DataBlock.updated_at`, and an `unchanged` sync does not refresh it.
- Without a published per-run result carrying its observation time, the catalog facts age out to UNKNOWN (`tori/sync-not-run`).
- A `maxAge` on `updated_at` would wrongly call a stable catalog stale.

## Model expressiveness limits (operational contract v0.1)

These are requirements found in the products that the current predicate grammar (typed value compared
with a constant, AllOf, per-requirement impact) cannot express directly.

**G5a. Counter deltas.**
- JUMI's node execution health exists only as cumulative counters (`jumi_k8s_node_start_total`, `_errors_total`).
- "Started nodes increased since the action and errors did not" needs two samples or a rate.
- A provider could derive a Boolean, but the contract cannot state the delta itself.

**G5b. Cross-fact comparison.**
- Tori's clean acceptance is `accepted_version == target_version`; predicates compare a fact with a constant only.
- The pack uses `acceptance-state = clean` instead, which Tori also records.

**G5c. Persistence across an action.**
- "Data written before the restart is still readable after it" is artifact-handoff's real recovery criterion: same artifact, same digest, same source state.
- RecoveryAssessment judges capabilities on fresh facts observed after the execution ended. It cannot compare against pre-action data unless a provider publishes a probe result as a fact.

**G5d. Set membership** works only as a conjunction of NotEq over a closed enum, as for Tori's healthy outcomes. Open-ended sets are not expressible.

**G5e. Static deployment preconditions** (below) can be modelled as assertions, but only if some provider publishes them. None does.

## Premise discrepancy

**G6.** API v0.1 §15 lists "Tori ↔ JUMI/AH — multi-dependency capability graph". At Tori `0b198d1` there is no JUMI or artifact-handoff client code, so no such dependency is declared; inventing one would be a false mapping. The multi-dependency capability graph is exercised instead by:
- JUMI → artifact-handoff: three capabilities through one slot, with three independent capabilities;
- Tori → shared root: read vs write.

## Product findings (reported, not fixed: other repositories are out of scope)

**G7. JUMI `e6a4573` deployment.**
- `deploy/k8s/jumi.yaml` sets no `JUMI_NAMING_SALT`, which `cmd/jumi/main.go:54-57` requires (`log.Fatal`), so the manifest as-is crash-loops.
- The Role (`jumi.yaml:12-21`) has no `pods/exec`, but output collection uses the exec subresource (`pkg/backend/spawner_k8s.go:494`).
- `deploy/k8s/network-policy.yaml` selects `app.kubernetes.io/name: jumi`, while the pods are labelled `app: jumi` (`jumi.yaml:47`).
