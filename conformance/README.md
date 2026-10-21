# O6 — Real-world conformance packs

This directory checks that the generic operational model (API v0.1 §14, §15, §18 O6) can express real
products and app-neutral reference services without any product-specific code.

Every pack is data only:

| File | Content |
|---|---|
| `manifests.yaml` | `OperationalContract` and `OperationalTarget` objects in the public `ops.bori.dev/v1alpha1` form |
| `workloads.yaml` | fixture-only stand-ins for the workloads the targets refer to; not part of an installation |
| `providers.yaml` | the reference evidence-provider configuration (`--operational-provider-config` format); `${EVIDENCE_URL}` is the fixture evidence server |
| `profile.yaml` | optional response profile (O4/O5) |
| `pack.yaml` | evidence endpoints, optional reference actor, and scenarios |

| Pack | What it is | Sources |
|---|---|---|
| `simple-http-service` | generic reference pack 1 | — |
| `replicated-service` | generic reference pack 3 | — |
| `stateful-single-writer` | generic reference pack 4 | — |
| `jumi-artifact-handoff` | JUMI workflow runtime + artifact-handoff resolver | JUMI `e6a4573ed79db40911524397361b06bfea2be712`, artifact-handoff `9d07eddf837fbd778f4909e1fb23d43ba172ec02` |
| `tori` | Tori snapshot catalog + its shared filesystem root | tori `0b198d1297b5ba70b913802963ad2b2a420184c9` |

The product mapping, with the exact source of every capability, dependency and fact, is in
[SOURCES.md](SOURCES.md). Requirements the model or the products cannot meet today are in
[GAPS.md](GAPS.md).

## How the packs run

`controllers/conformance_test.go` loads every pack and runs every scenario through the shipped path:
- the operational reconciler, with its fake Kubernetes API in the test;
- the reference Kubernetes-status provider and HTTP typed-response provider, with bounded investigation;
- the interaction summary;
- the action engine with the reference actor, when the pack declares responses.

Scenario steps are generic verbs: publish, change or unset a fact; return an HTTP error; freeze a fact's
observation time; change a workload's replicas; recreate a workload; rebind an assertion to another
provider revision; approve an action; advance the clock. The harness has no product, pack or scenario
names in its code (`TestConformanceHarnessIsProductNeutral`).

After every reconcile an independent checker (`controllers/conformance_check_test.go`) recomputes the
four forbidden outcomes of Scenario §7 from its own ground truth: published values, freshness, workload
identity, provider configuration and its own walk of the contract graph. It never uses BORI's reasons
or evidence references.
- **unrelated:** a capability whose referenced facts were never disturbed in the scenario changes state.
- **staleReuse:** a capability is AVAILABLE while a fact it rests on, directly or through a dependency,
  is missing, stale or bound to another workload.
- **unauthorized:** an execution of an undeclared action or of a display-only response (one without an
  `execution` block). The declaration is the one for the execution's target, action and capability
  (`for`). The execution must also carry exactly that declaration's expected impacts, in any order, and
  the target, its resolved workload and its contract (UID and spec digest) as the harness recorded them
  when the actor executed that execution, not any other. A workload recreated later does not void it. An execution is also unauthorized when its
  approval is not one of these:
  - where a person must decide, a human approval carrying exactly a decision an approve step signed for
    that target, action, proposal ID and digest (decision ID and principal included);
  - where nothing is for a person to decide, the engine's policy approval: kind `policy`, principal
    `policy:<profile revision>` for the revision of the profile the harness loaded, and no decision ID.

  A person must decide when the response declares an approval, a risk or an interruption, or when another
  declaration competes for the same capability of the same target instance (the engine's
  `no-priority-authority`). A declaration whose target UID pins another instance is not that target's and
  never competes. A declaration competes when it is another response, or the same response with another
  execution contract. The response is its action, capability, owner, approval, and its risks and
  preconditions as sets. Its trigger states and target pin are not part of it, so the same declaration
  listed again with other trigger states, another order or pinned to this instance does not compete. A
  display-only declaration has no execution contract, so it competes only when it is another response.
  An empty approval, an unknown kind, or a policy approval naming another or an
  earlier revision counts. So do a policy approval where a person must decide, and a human approval where
  nothing is for a person to decide.
- **unjustifiedRecovery:** `Recovered` without an execution that the reference actor itself reports
  ended, or before every predicate behind the expected capability was met by a value its bound provider
  accepted about the current workload strictly after that end. Healthy is what the contract's predicate
  requires (for example `ready-replicas Gte 1` is met by 1), not the value before the fault. The harness records every result a
  shipped provider returns to BORI. A successful read is not acceptance: a Kubernetes status that lags its
  spec (`status.observedGeneration < metadata.generation`) is refused by the provider and proves nothing,
  however healthy its counts. For an HTTP fact the evidence server must also have served that value at
  that time. The checker uses these records and the harness's own time of the actor's end, never BORI's
  end time or recovery reasoning. A fixture value that is healthy now proves nothing by itself.

### What the checker does not judge

- **Run-time competition.** The checker decides competition from the declarations alone. The engine also
  leaves out a declaration whose trigger states do not match or whose precondition is unmet, at run time.
  The static exclusions (a target UID pinning another instance, a display-only sibling of the same
  response) are mirrored. A pack in
  which a second declaration is excluded only at run time would see a correct policy execution counted as
  unauthorized. No pack has such a declaration today.
- **Combined-time recovery.** Each fact behind a recovered capability must be accepted healthy after the
  execution ended, but not all at the same instant. This mirrors the engine's own assessment, which
  evaluates every post-end observation together. Whether recovery should require one instant at which
  every fact was healthy is an open product question (API §19), not a conformance rule.

Every real scenario must count 0 in every class. `TestConformanceCheckerControls` injects each fault
into BORI's claims and checks that the checker counts it in exactly its own class, and that it counts
nothing on untampered claims.

## Evidence classes — what a passing pack does and does not show

| Class | Meaning | Where it holds |
|---|---|---|
| Fixture | Facts are published by the harness's evidence server, in the reference typed-response form | every pack scenario |
| Real API, available today | The fact can be read from the real product as deployed today, by a shipped provider | Deployment replica counts (JUMI, artifact-handoff) via `kube-status` |
| Real API, derivable | The product exposes it, but an adapter would have to translate it into the typed-response form | JUMI `/statusz`: `registry-readable` from HTTP 200 vs 500, `backendReady` |
| Absent | The product publishes no such fact | see [SOURCES.md](SOURCES.md) and [GAPS.md](GAPS.md) G2 |
| Real installation | The packs applied to a cluster running the products | **not done**: no product cluster was used |

A passing pack shows that the contracts express the products' operating requirements, and that the
shipped evaluation, investigation, interaction and action paths judge them correctly from fixture
facts. It is not evidence of a working product integration. The `published-facts-only` scenario of
`jumi-artifact-handoff` shows what BORI concludes today from the real evidence surface alone.
