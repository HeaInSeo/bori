# Operational evidence providers and bounded investigation (O3 reference profile)

> **Status: REFERENCE PROFILE.** This is a bounded, process-local experiment
> that validates the O1/O2 model against two real evidence sources. It does
> **not** define a provider transport, discovery or registration API, an
> evidence URI or integrity standard, or a public schema. Those remain OPEN.
> No CRD, schema or `pkg/operations` semantics are changed.

## What it adds

```
list → resolve targetRef → opswire.Build (grants, pins; no evidence)
     → authorized requests (O1-valid targets, granted + registered bindings only)
     → investigation: pick one query from the evidence → real provider call
       → typed observation → candidates refuted / maintained → O1 re-evaluation
       → next query or stop (decided / nothing useful / budget)
     → evaluation instant taken after all I/O → O1 → bounded status (O2 Apply)
```

| Package | Role |
|---|---|
| `pkg/providers` | `KubeStatus` and `HTTPTyped` adapters, explicit `Registry` built from a config file |
| `pkg/investigate` | evidence-conditioned planner, candidates, episodes, budget, process-local evidence cache |
| `controllers/operational_reconciler.go` | thin wiring: build → requests → investigate → evaluate |

Capability truth is still decided only by `pkg/operations.Evaluate`.
Candidates are hypotheses and never become facts or RCA.

## Enabling and disabling

The profile is off unless both flags are given:

```
bori-operator --enable-operational-assessment \
  --operational-provider-config /etc/bori/providers.yaml \
  --operational-requeue-interval 5s
```

Without `--operational-provider-config` the O2 behaviour is unchanged: there
is no provider I/O, and every evidence-backed capability is UNKNOWN. Remove
the flag to disable the profile.

### Configuration example

```yaml
http:
  allowInsecureHTTP: false      # http:// only for test/lab
  allowPrivateNetworks: false   # loopback/private/link-local only for test/lab/in-cluster
limits:                         # optional, within hard caps
  perCallTimeout: 2s            # cap 10s
  maxResponseBytes: 16384       # cap 256 KiB
providers:
  - namespace: apps
    name: kube-status
    configRevision: r1
    kubernetesStatus:
      fields: {available-replicas: availableReplicas, ready-replicas: readyReplicas}
  - namespace: apps
    name: app-http
    configRevision: r1
    http:
      endpoints:
        - subjectUID: 6f1c...            # the workload UID this endpoint is pinned to
          url: https://web.apps.svc:8443/bori/assertions
          fields: {api-serving: serving}
```

A binding uses the provider whose identity is exactly `<namespace>/<name>` +
`configRevision`. A provider in another namespace also needs a typed
`EvidenceProvider` grant (O2). A binding whose revision differs from the
registered one has no provider. It is never dispatched, and its evidence stays
UNKNOWN.

## Evidence identity and currentness

Every observation is keyed by the full O1 applicability key, built on the
client side from the authorized binding:

```
Target (namespace, name, UID) + resolved workload UID
+ Contract (namespace, name, UID, specDigest) + slot
+ Provider (<namespace>/<name>, configRevision)
```

Nothing a provider reads can change this key, the subject, the destination or
the budget.

When the key changes (target, workload or contract recreated, spec digest
changed, provider or revision changed, grant revoked), the held observations
for the old key are purged at the next reconcile and never applied.

Currentness is O1's. Provider `observedAt`, optional `validUntil` and the slot
`maxAge` decide whether evidence is current. Stale, expired, future, wrong-type
and equal-latest-conflicting evidence is UNKNOWN.

How a new answer for a key relates to what is held:

- **Provider unavailable** (stamped with BORI's clock at receipt) always
  replaces the held evidence, so there is no fallback to an older value.
- **Values are ordered against a per-key watermark:** the `observedAt` of the
  last accepted value for that exact full key. An outage does not erase it
  (API F9 / O1 S05):
  - A first-ever value, or one strictly newer than the watermark, replaces
    the held evidence. This clears an outage and advances the watermark.
  - An older value, or an equal-stamp replay after an outage, is ignored, so
    a late red can never overwrite a fresher recovery and a stale green can
    never resurface after an outage.
  - While the watermark's value is still held, an equal stamp with a
    different value is held beside it, and O1 reports conflicting evidence
    (UNKNOWN).
- **Rejected results never advance the watermark:** future-stamped,
  malformed and timed-out results are recorded as provider-unavailable.
- **The watermark lives and dies with its key.** It survives episodes,
  cooldowns and reconciles. It counts toward the held-key capacity and is
  purged with the key on identity change, grant revocation, deletion or
  invalidation. Only targets O1 assesses as valid authorize held keys: a
  target that becomes invalid loses its held evidence and watermarks at the
  next reconcile, so correcting its spec needs a fresh provider call and an
  invalid target never holds cache capacity. It is never reused across targets, providers or config
  revisions.

A value stamped **after its local receipt time** is rejected and recorded as
`ProviderUnavailable` (`future-observedAt`). It is never clamped or
restamped. Held, it would shadow every later answer and silently turn
"current" once the clock caught up. A producer whose clock runs ahead of
BORI's therefore yields UNKNOWN until its clock is corrected.

A provider failure is recorded as `ProviderUnavailable` for that exact key. It
is stamped with BORI's clock after the call and replaces the previous value,
so there is no fallback to an older AVAILABLE. It is never evidence that the
application failed.

One authoritative binding per target/slot (O1) still holds. The fixtures bind
the two providers to different slots.

### Kubernetes status provider

- **Reads:** one uncached GET of the target's own `targetRef` in its own
  namespace. Supported kinds are only `apps/v1` Deployment, StatefulSet and
  DaemonSet, and only these Integer status counts:
  - Deployment and StatefulSet: `replicas`, `readyReplicas`,
    `availableReplicas`, `updatedReplicas`.
  - DaemonSet: `desiredNumberScheduled`, `numberReady`, `numberAvailable`,
    `updatedNumberScheduled`.
- **What the fact means:** the workload controller's status as recorded in the
  API server for the object's current generation.
  - A value is emitted only when `status.observedGeneration ≥
    metadata.generation`. While the controller lags the spec, the provider
    reports unavailable instead of presenting a status for an older spec as
    current.
  - An absent count means 0 once the generation is observed.
  - `observedAt` is the read time of that recorded status.
- **Limit:** a stalled workload controller that stops updating status cannot
  be detected from the object.
- **Not evidence:** a UID mismatch (recreated workload), not found, unsupported
  kind and read errors all report unavailable. None of them is a new fact.
- **No interpretation:** readiness, generation equality and Pod Ready are not
  turned into health, Sync or capability truth. The provider supplies a count,
  and the contract's typed predicates decide.
- **Out of scope:** pod logs, exec, Secrets, and other kinds or namespaces.
- **Evidence handle:** `k8s:<Kind>/<ns>/<name>@<uid>#status.<field>,generation=<g>`.
  It is stable for the same object, field and generation, so refreshing an
  unchanged fact does not rewrite status.

### HTTP typed-response provider

- **Request:** one `GET` of the configured URL. There are no retries, and
  redirects are not followed: a 3xx response counts as a non-200, so the
  provider reports unavailable. Proxy environment variables are ignored.
  TLS is verified, and there is no option to disable that. Each call has a
  timeout and a response size cap.
- **Destination policy:** scheme `https`, or `http` only with
  `allowInsecureHTTP`. No userinfo and no fragment. The destination IP is
  checked when the connection is dialed, so DNS can't route a request to a
  loopback, private or link-local address unless `allowPrivateNetworks` is set.
  The configured endpoint list is the allowlist.
- **Subject pin:** an endpoint is queried only for a request whose resolved
  workload UID equals its `subjectUID`, and the response's `subject` must
  match. A recreated workload therefore has no endpoint until the
  configuration (and its `configRevision`) is updated. An old payload can
  never be attached to a new workload.
- **Response format:**

  ```json
  {"subject": "<workload uid>", "observedAt": "<RFC3339>", "validUntil": "<RFC3339, optional>",
   "values": {"serving": true}}
  ```

  - A value must be JSON `true`/`false`, an integral number, or a string,
    with no coercion. A type that doesn't match the slot reaches O1, which
    returns `evidence-type-mismatch`.
  - These all report unavailable: a non-200 status, a non-JSON body, any data after the single typed object other than whitespace (garbage, a second JSON value, a truncated tail), a missing
    subject, `observedAt` or field, a float/null/object value, an oversized
    body, and a subject mismatch.
  - Unknown fields are ignored. URLs, namespaces, tool names, budgets or
    instructions in the payload are data and have no effect.
  - HTTP 200 alone, raw text and legacy `health.path` are never evidence.
- **Evidence handle:** `http:<ns>/<provider>#<field>`.

## Investigation

### Candidates

For each capability that is not AVAILABLE, there is one hypothesis per
requirement it references (assertion, dependency, envelope, local
capability), plus one per predicate slot of a referenced or selected envelope.
Each hypothesis's status is read from O1's own reasons for that input:

- **Maintained:** the input is proven unmet. The facts are consistent with the
  hypothesis. It is still not a confirmed cause.
- **Refuted:** the input is proven accepted.
- **Open:** the input is unproven.

A refuted candidate changes status only on a new fact.

### Choosing the next query

Each step picks exactly one query:

1. **Investigation query.** A slot qualifies when it has no current evidence
   and would decide at least one Open candidate. Among those, the slot that
   decides the most Open candidates wins. Ties go to the slot least recently
   queried across this target identity's episodes, then to the slot name, so
   equal-score slots take turns and a few always-failing slots cannot starve
   the rest. This per-identity history is bounded by the target's bindings,
   reset when the identity changes, and dropped with the target.
2. **Freshness query.** Only if no investigation query exists: a slot with
   current evidence that is due for refresh (within `RefreshFraction × maxAge`
   of expiry) and is read by some capability or a referenced or selected
   envelope.

Each slot is queried at most once per episode. These slots are never queried:
- slots with current, not-yet-due evidence
- slots nothing reads
- bindings without a registered provider
- grant-denied bindings, which never even reach the registry

So the next query depends on the evidence. If the replica fact is already
held, only the HTTP application assertion is queried. If the application
assertion is held, only the Kubernetes replica fact is.

The episode ends with one of these states:
- `Resolved`: every candidate is decided.
- `Refreshed`: freshness work is done.
- `NoAllowedQuery`: Open candidates remain but nothing is queryable.
- `ExhaustedCalls`, `ExhaustedSteps`, `Deadline`, `Cancelled`, `Capacity`:
  a budget or limit ended it.
- `Superseded` or `TargetInvalid`: the identity changed or the target became
  invalid.

Unproven judgements left at the end stay UNKNOWN in O1. Capabilities that are
already proven are not affected.

The bounded trace (select, result, candidate transition, late, end) stays in
the investigator for verification. It names only the target's own slots and
candidates. It is not written to CRD status, and denied bindings never appear
in it.

### Episodes, budget and admission

**Episode key:** OperationalTarget UID + resolved UID + exact contract
identity + authorized (slot, provider identity) bindings. A changed key
supersedes the episode.

| Limit (reference value) | Bound |
|---|---|
| `MaxCallsPerEpisode` 6 | real provider calls; each call is exactly one GET (no retries, redirects or fan-out) |
| `MaxStepsPerEpisode` 8 | planner iterations |
| `EpisodeDeadline` 10s | elapsed time from episode start (absolute) |
| `PerCallTimeout` 2s | one call, also capped by the remaining deadline; enforced on both the context and the evaluation clock. The configured `limits.perCallTimeout` (cap 10s) is applied to the investigator, the HTTP client and Kubernetes-status calls alike |
| concurrency 1 | calls are strictly sequential |
| `MaxCandidates` 32, `MaxTraceEntries` 64 | per episode, for stored/traced state. Selection always scores the complete candidate set derived from O1 at each step (transient, bounded by the contract schema), and the stored set reserves open candidates of queryable assertions first (least recently queried first), so the cap never hides an authorized registered query |
| `MaxResidentEpisodes` 256 | episode records in memory |
| `MaxHeldObservations` 1024 | cached applicability keys |
| `EpisodeCooldown` 10s | minimum time between episode starts per OperationalTarget UID |
| `RefreshFraction` 0.5 | refresh due at half of `maxAge` before expiry |
| target rotation | each run starts at the target the previous slice stopped on (or the one after the last completed), wrapping in UID order; a single cursor, so slow early targets cannot consume every slice while later targets wait |
| `RunSlice` 20s | wall time per reconcile, including in-flight calls: every call derives from the slice, so the earliest of shutdown, slice end, remaining episode deadline and per-call timeout bounds it. A call cut by the slice end is discarded (nothing recorded, still counted). The unfinished episode stays active with its remaining budget |

These values are a reference configuration chosen for the fixtures (a few
slots per target; a 30s maxAge refreshed 15s before expiry). They are not a
public standard.

**What creates a new allowance:**
- Repeated reconciles, the controller's own status events, responses whose
  only change is a timestamp, and cache pruning grant no new allowance.
- A new episode starts only for a target that has an investigation or
  freshness query, and only once its cooldown has passed. The cooldown is
  keyed by OperationalTarget UID, so changing identity or binding does not
  bypass it.

**Limits that end or refuse work:**
- After `ExhaustedCalls`, `ExhaustedSteps`, `Deadline` or `Cancelled`, nothing
  more is dispatched in that episode.
- A result that arrives after its per-call timeout or the episode deadline is
  discarded and recorded as `ProviderUnavailable` ("late"). A result returned
  under parent-context cancellation is discarded without recording anything.
- At capacity, a new key is refused and the slot stays UNKNOWN; nothing is
  evicted. Only terminal episodes whose cooldown has passed are pruned, so
  pruning grants no extra allowance.

**Restart and polling:**
- The profile is process-local. A restart begins with no held evidence
  (UNKNOWN) and no episode history. It claims no durable incident or budget
  continuity.
- Workload status changes and evidence expiry are observed by bounded
  polling. The reconcile requeues at the earlier of these, with a 1s floor:
  - the configured interval
  - the next *future* refresh-due time of a queryable target; when the
    refresh falls due inside the target's cooldown, the cooldown end, which is
    when the refresh can first be admitted
  - the cooldown end of a queryable target whose last episode stopped with
    work left (budget, deadline, cancellation or capacity)
  - immediately (so the floor applies), when a run slice ended while a
    queryable target's episode was still active inside its original
    deadline: the next reconcile resumes that same episode with its
    remaining calls, steps and deadline (no reset, no new allowance)

  Elapsed times, expired active episodes, finished episodes and targets that
  are no longer queryable or have no query produce no wake. Obsolete terminal episode records are forgotten once their
  cooldown has elapsed, so the requeue cannot collapse to the floor. No
  status-update watch is added.

### Snapshot and race boundary

- **Snapshot:** grants, identities and bindings are those listed at the start
  of a reconcile.
- **Late changes:** a grant revoked or a target replaced while a call is in
  flight takes effect at the next reconcile. That reconcile drops the binding
  and purges every held observation for keys that are no longer authorized,
  so such a result is never applied afterwards.
- **No distributed atomicity:** a call that was already sent cannot be
  recalled. All calls are reads.

## Verification

- `go test ./pkg/providers/ ./pkg/investigate/ ./controllers/`:
  - real HTTP transport against controlled servers
  - the Kubernetes status adapter against an API client
  - the planner, budget and episodes with real O1
  - the controller vertical slice with both adapters, which also checks
    write, dispatch and lookup counters and no-actuation
- `hack/test-ops-crd-validation.sh` (CI job `ops-crd-validation`) runs the O3
  path on a real kind API server. It uses `hack/ops-typed-producer.py` as the
  controlled HTTP producer.
