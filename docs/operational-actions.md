# ActionProposal, handoff and RecoveryAssessment (O5)

> **Status: CANDIDATE, opt-in, reference profile.** This document fixes the
> generic O5 model and its state machine, and states what remains OPEN under
> API v0.1 §19. A recommendation in this document is the writer's
> recommendation, **not a central decision**. The public CRD shape and
> storage of proposals, the identity system behind approvals and any real
> execution connection stay OPEN. BORI is never the mutation actor: it judges,
> hands off to a declared external ActionProvider and re-evaluates.
>
> The operator ships with **no ActionProvider, no approval verifier and only a
> non-durable journal**, so in a live cluster BORI shows proposals and why
> they wait, but never hands anything off. Handoff, results and recovery are
> exercised only with the fake/reference actor (`pkg/action/actiontest`) in
> L1–L4 tests. Simulated storage and simulated approvals are not evidence of
> real durability or real operational authority.

## 1. Canonical inputs this design obeys

| Source | Rule used here |
|---|---|
| API v0.1 F8, §4.4 | ActionProposal is not part of the installed minimum API. CR edit permission is not approval authority, and a proposal object is not an execution trigger. |
| API v0.1 §12, §13 | The lifecycle is judgement → optional ActionProposal → human/policy decision → external ActionProvider → execution receipt → RecoveryAssessment → capability re-evaluation. An ActionProvider forwards a registered action to the authoritative actor and returns a receipt. |
| API v0.1 F1, F5 | Exact applicability identity; no cross-namespace action references in v0.1. |
| Scenario §2.6, §2.8, §4.3 | Bounded automation; semantic recovery (action success, rollout success or Pod Ready never declare recovery). An automatic action needs exact identity/revision, exact target/binding, preconditions, blast radius, owner/authority, idempotency/retry, completion evidence, post-condition/RecoveryAssessment and the approval requirement. Recommendation text never enters the execution path. |
| Scenario §7 | Hard safety per scenario: unrelated-capability misjudgement 0, unjustified normal/recovery 0, unauthorized action 0, stale/inapplicable reuse 0. |
| R2-B (canonical) | Persist-before-send; every send/resend revalidates current authority; at most one live mutation responsibility; fenced assignment; **ACK = durable acceptance only**; ACK ≠ Applied ≠ Converged ≠ Available ≠ Healthy. |
| R2-C C-N1 | A point-in-time observation is as-of evidence; live claims need fresh authoritative observation. |
| §19 OPEN | ActionProposal as CRD vs external durable store + projection; OperationalPolicy as a CRD. **Not decided here.** |

## 2. Model

```
O1 assessment (exact identity, current evidence)
  └─ declared response with an execution block (operator profile)
       └─ Proposal  (derived, deterministic: ID, digest, basis, gates)
            ├─ Approval  (verified principal ∈ declared approvers, exact ID+digest, TTL)
            │   or Policy approval (no human reason declared or derived)
            └─ Execution record (journal; persist-before-send; fence; idempotency key)
                 ├─ Receipt  = durable acceptance only
                 ├─ Result   = Succeeded | Failed  (or TimedOut = outcome unknown)
                 └─ RecoveryAssessment  = O1 re-run on post-execution evidence only
```

Each arrow is a separate fact. A proposal is not an approval, an approval is
not a handoff, a receipt is not completion, completion is not a result, and a
successful result is not recovery.

### 2.1 Action contract (declared)

An O4 profile response becomes executable only when it has an `execution`
block. Without it the response stays display-only, exactly as in O4.

| Contract element (Scenario §4.3) | Where it is fixed |
|---|---|
| Exact action identity/revision | `action {name, revision}` (O4) |
| Exact target and binding | `target {namespace, name, uid?}` (O4, own namespace only) plus the bound identity captured in the proposal: OperationalTarget UID, resolved workload UID, ContractIdentity (namespace/name/UID/specDigest), every assertion binding's provider identity and every dependency binding |
| Capability it addresses and trigger states | `for`, `states` (O4) |
| Preconditions re-checked right before every send | `preconditions` (O4): same-target capabilities that must be AVAILABLE on **current** evidence |
| Expected capability impact | `execution.expectedImpact` (must include `for`): the capabilities the action is expected to restore. Recovery is judged on exactly these. |
| Blast radius | Same target only (v0.1 has no cross-namespace action). `execution.mayInterrupt` lists the capabilities of this target the action may interrupt. |
| Execution owner and authority | `owner` (O4) is the accountable owner. `execution.provider` names the registered ActionProvider. The registry entry declares the owner it acts for and the namespaces and actions it is authorised for. |
| Human approval and why | Required when `requiresApproval`, any declared `risks`, or a non-empty `mayInterrupt`. It is also required when another declared response (executable or display-only) addresses the same capability of the target now: `no-priority-authority`, the same boundary O4 shows. The same action declared with different owners is `owner-conflict` and is blocked. The reasons are reported as codes. `execution.approvers` are the principals that may approve. Otherwise the declared profile itself is the policy approval. |
| Approval binding | A decision names the proposal ID **and** digest. TTL is `execution.approvalTTL` (default 1h). |
| Idempotency, retry, timeout, budget | Idempotency key = `<proposal ID>/<attempt>`. Fence = journal epoch. `ackTimeout`, `completionTimeout`, `maxSends` (resends of one attempt with the same key), `maxAttempts` + `retryable` (a new attempt with a new key only after a definitive Failed result). |
| Receipt/result linkage | A receipt or result is accepted only when key, fence and proposal digest all equal the current attempt's. Anything else is counted and ignored. |
| Post-condition / RecoveryAssessment | Every `expectedImpact` capability AVAILABLE in an O1 evaluation that uses **only observations made after BORI recorded completion**, for the unchanged identity, within `recoveryWindow`. |

### 2.2 Proposal identity

- **Subject** = OperationalTarget namespace/name/UID, resolved workload UID,
  ContractIdentity, action name@revision, `for`, profile revision.
- **Episode** = the number of earlier executions of this subject that ended
  Recovered, plus one. A recurrence after a confirmed recovery is a new
  proposal, so an approval for an earlier episode never applies to it.
- **Proposal ID** = digest(subject, episode).
- **Digest** = digest(proposal ID, semantic basis). The basis is the trigger
  capability's state and reason codes, every precondition's proof state, the
  provider identity of every assertion binding, the dependency bindings, the
  approval requirement and its reasons, and the declared execution
  parameters.

The digest contains no evidence reference and no timestamp. Routine evidence
refreshes therefore do not invalidate an approval, but any change in meaning
does. Evidence **currentness** is never taken from the approval time: every
send re-proves the trigger state and preconditions on current evidence.

### 2.3 State machine

Proposal phase (derived each step, never stored on its own):

| Phase | Meaning |
|---|---|
| `Blocked` | A gate fails. Reasons: `owner-undeclared`, `owner-conflict`, `provider-unregistered`, `authority-insufficient`, `precondition-unproven`, `precondition-unmet`, `journal-not-durable`, `target-busy`, `no-approver`, `executed-not-recovered`, `outcome-unknown`, `attempts-exhausted`, `identity-changed` |
| `AwaitingApproval` | All execution gates pass but a human approval is required. The reasons say why: `approval-declared`, `risk-declared`, `may-interrupt`, `no-priority-authority`. |
| `Rejected` | A verified, allowed approver rejected this exact ID and digest. |
| `Dispatching` | Handoff intent persisted. Sent (possibly) but not acknowledged. |
| `Accepted` | Receipt received: durable acceptance only. |
| `Succeeded` / `Failed` | Result from the provider for the exact key, fence and digest. |
| `TimedOut` | No acknowledgement within `ackTimeout`, or no result within `completionTimeout`. The outcome is **unknown**, never success. |
| `Withdrawn` | A persisted intent whose revalidation failed before any acknowledgement. It is not resent; a late result is still recorded. |

Recovery (only after an execution reached `Succeeded`, `Failed` or `TimedOut`):

| Recovery | Meaning |
|---|---|
| `Pending` | Within the window, not every expected capability is proven AVAILABLE on post-execution evidence |
| `Recovered` | Every expected capability is AVAILABLE on post-execution evidence, for the unchanged identity |
| `Partial` | At the end of the window, some expected capabilities are recovered and others are not |
| `NotRecovered` | At the end of the window, none is recovered and at least one is proven DEGRADED or UNAVAILABLE |
| `Unconfirmed` | At the end of the window, nothing is proven either way |
| `Inapplicable` | The target, workload or contract identity changed; nothing from the old identity is reused |

A final recovery state is persisted; it is an as-of statement (R2-C C-N1),
not a standing claim. The capability's live state is always the current O1
result.

### 2.4 Handoff protocol

1. **Gates**, all re-evaluated on the current O1 assessment at the moment of the decision:
   - the identity is unchanged;
   - the trigger state still holds on current evidence;
   - preconditions are proven AVAILABLE on current evidence;
   - an owner is declared;
   - the provider is registered and authorised for this owner, namespace and action;
   - the journal is durable;
   - no other execution of this target is in flight;
   - the attempt budget allows it;
   - the approval (human or policy) is valid for the exact ID and digest.
2. **Persist before send:** a `Dispatching` record with key, fence and digest is written by compare-and-swap. If the write fails, nothing is sent.
3. **Send:** `Submit(handoff)`. A receipt with the same key, fence and digest moves the record to `Accepted`. If that write fails, the next step polls the provider by key and adopts its answer, so nothing is sent twice.
4. **Resend** (same key and fence, at most `maxSends`) only while unacknowledged and only after the gates of step 1 pass again. Otherwise the record becomes `Withdrawn`.
5. **Restart and overlap:** a new engine takes a fresh epoch from the journal, and every record it writes carries that epoch. The journal **rejects any write whose fence is below the newest issued epoch**, atomically with the write. Once a newer engine exists, an older one can persist nothing, so it can send nothing; this makes the one-in-flight-per-target rule hold across overlapping engines. A resend carries the new fence. The reference actor rejects lower fences, so an older send that was already on the wire before the takeover is deduplicated by key or refused.
6. **Results:** an accepted result for the current key, fence and digest records BORI's own completion time, never the provider's clock. Duplicate results are no-ops. Results for another key, fence or digest are counted and ignored. A result never becomes an O1 observation.

## 3. OPEN decisions (§19) — options and the writer's recommendation

These are recommendations for central adjudication. Nothing below is decided
by this document.

### 3.1 Where proposals and the execution journal live

| Option | For | Against |
|---|---|---|
| A. `ActionProposal` CRD (namespaced, controller-written status) | Kubernetes-native, watchable, RBAC-scoped | F8: CR edit permission must not become approval authority, which makes CRD-based approval fragile. A CRD looks like a trigger, and etcd is not an audit log. It adds a fourth installed API. |
| B. External durable store (journal) + projection into `OperationalTarget.status.interaction.actions` | Durability and audit stay with a store designed for them. Status shows only a bounded projection. Matches the B0 decision that a physical store is OPEN rather than assumed. | Needs a store dependency and an operational owner. Not watchable by kubectl beyond the projection. |
| C. Journal in a Kubernetes object owned only by the operator (e.g., a ConfigMap/Lease per in-flight execution) with fenced CAS on resourceVersion | No new dependency; CAS + resourceVersion gives fencing | Size and churn limits, and still an object that cluster editors can write. |

**Recommendation: B, behind the `Journal` interface implemented here.** Proposals stay **derived** (they need no storage until handoff). Approval decisions come from an external, authenticated decision source. Only the execution journal needs durability. Until central picks a store, the operator ships only the non-durable in-memory journal, and handoff is disabled (`journal-not-durable`).

### 3.2 Approval identity

**Recommendation:** decisions come from an external decision source and are verified by a `Verifier`, for example a signed decision from the organisation's IdP or a workflow tool. They must name the exact proposal ID and digest, and the verified principal must be one of the response's declared `approvers`. A Kubernetes user's ability to edit a CR, an annotation or a status is never approval. Open questions for central: the IdP/verifier technology, the approver directory and group semantics, and whether a quorum is needed for `data-loss` risks.

### 3.3 Execution owner

**Recommendation:** the declared `owner` is accountable. The ActionProvider registry entry binds a provider to exactly one owner and an allow-list of namespaces and actions, and BORI refuses handoff on any mismatch. The real provider transport (in-process plugin, gRPC/HTTP, or Kubernetes object handoff to a Deployment Authority) stays OPEN, together with the provider transport question of §19.

### 3.4 Failure and restart

**Recommendation** (implemented in the reference engine):
- persist-before-send with compare-and-swap;
- a monotonic journal epoch as the fence;
- one idempotency key per attempt;
- poll by key after a restart before any resend;
- `TimedOut` = unknown, with no automatic retry;
- a new attempt only after a definitive `Failed`, and only when declared retryable.

Open questions for central: the journal technology, its retention/audit period, and whether `TimedOut` should page a person (the reference projection raises DECISION_REQUIRED).

## 4. Operator interaction (O4 linkage)

With `--enable-operational-actions`, `status.interaction.actions` (at most 8) shows for each proposal of the target:
- proposal ID and action;
- `for` and phase;
- waiting/blocking reasons;
- execution outcome;
- recovery state with per-capability detail.

It is a bounded, non-authoritative projection, **not** proposal storage, and it carries no timestamps. O4's level table is unchanged except for one addition: an execution that timed out, or ended without recovery, adds the human reason `action-outcome-unknown` or `action-not-recovered` and raises the level to DECISION_REQUIRED. A recovery never lowers the level: the level follows the current O1 capability states.

## 5. Verification layers

- **L1:** proposal derivation, digest stability, gates.
- **L2:** journal CAS and fencing, and the provider registry's authority checks.
- **L3:** recovery classification over real O1 evaluations.
- **L4:** the full flow with the reference actor, including fault injection. Covered faults:
  - journal write failure;
  - crash after persist and before send;
  - crash after send and before the acknowledgement is persisted;
  - duplicate and mismatched responses;
  - a stale engine;
  - timeouts;
  - target recreation;
  - contract and provider revision changes;
  - unrelated-capability contamination.

Every scenario runs a forbidden-outcome checker that recomputes independently:
- unrelated misjudgement;
- unjustified recovery;
- unauthorized or duplicate execution;
- stale or inapplicable reuse.

Every scenario must count 0 on each.

## 6. Enabling

```
bori-operator --enable-operational-assessment \
  --operational-provider-config /etc/bori/providers.yaml \
  --enable-operational-interaction \
  --operational-interaction-profile /etc/bori/interaction-profile.json \
  --enable-operational-actions
```

The flag requires the interaction summary. Off (the default), the status is
exactly the O4 status: no `actions`, and the same fingerprints.

On, this build:
- shows proposals and why they wait;
- registers no ActionProvider and has no approval verifier;
- uses only the non-durable journal, so every proposal stays `Blocked` (`journal-not-durable`, `provider-unregistered`) or `AwaitingApproval`;
- **never hands anything off**.

Connecting a real provider, journal or identity system needs the §19
decisions above.

## 7. Scenario traceability (fake/reference actor, L1–L4)

Each scenario runs the independent forbidden-outcome checker after every step.

| # | Scenario (packet) | Canon | Tests |
|---|---|---|---|
| 1 | No execution before approval | S09 | `TestApprovalRequiredIsNotExecutedBeforeApproval`, controller `TestO5ControllerApprovedActionRecoversOnFreshEvidence` |
| 2 | Wrong approver, forged/unsigned, other proposal, pre-change approval, expired/future, rejection | S09, F8 | `TestInvalidApprovalsAreRefused` |
| 3 | Owner absent, provider unregistered/unauthorised, precondition unproven/unmet, journal not durable | S16 | `TestExecutionGatesBlockEvenWhenApproved`, `TestPolicyApprovalStillPassesGates`, controller `TestO5ProductionWiringNeverHandsOff` |
| 4 | Accepted-only or no acknowledgement ends `TimedOut`, never success | S10 | `TestAcceptedOrTimedOutIsNotSuccess` |
| 5 | Failure vs. success without recovery vs. partial | S10, S11, S07 | `TestFailureNonRecoveryAndPartialAreDistinct` |
| 6 | Pod Ready but capability failed | S07, S11 | `TestPodReadyIsNotCapabilityRecovery`, controller `TestO5ControllerPodReadyIsNotRecovery` |
| 7 | Only new, applicable evidence confirms recovery | S05, §2.8 | `TestOnlyFreshApplicableEvidenceConfirmsRecovery`, `TestFlowApprovedSucceededRecovered` |
| 8 | Target recreation, contract/provider revision change | S17, S19 | `TestIdentityChangeReusesNothing` |
| 9 | Journal failure, crash before/after send, lost receipt, duplicate/forged results, stale engine, retry budget, restart every step | R2-B | `TestFaultsNeverDuplicateOrMislink`, `TestResendRevalidates`, `TestNoRetryAfterSuccessOrUnknownOutcome`, `TestOneInFlightExecutionPerTarget` |
| 10 | Unrelated failure/recovery does not contaminate | S03, S12 | `TestUnrelatedFailureAndRecoveryDoNotContaminate` |
| — | Codex P1 r4234734088: competing responses and owner conflicts need a person / block | §4.2, O4 | `TestCompetingResponsesNeedAPerson` |
| — | Codex P1 r4234734092: overlapping engines cannot both occupy a target | R2-B | `TestOverlappingEnginesCannotBothOccupyATarget` |
| — | Determinism and zero churn; checker detects each class; no scenario/product/readiness branch; no cluster client | §2, §10 | `TestDeterministicAndZeroChurn`, `TestForbiddenCheckerDetectsViolations`, `TestActionCodeIsAppScenarioAndReadinessNeutral`, `TestActionCodeHasNoClusterClient` |
| — | O4 linkage: fingerprints unchanged when off, escalation only on unknown outcome/non-recovery, schema at bounds | O4 | `TestFingerprintUnchangedWithoutActions`, `TestActionsEscalateOnlyOnUnknownOutcomeOrNonRecovery`, `TestActionsAtBoundsFitTheSchema`, `TestO5OffEquivalenceAndNoAddedEvidenceIO` |

The live kind section checks, against a real API server and the shipped wiring:
- the proposal and its blocking reasons appear in status;
- the schema accepts maximum-length names;
- nothing is handed off;
- no new object appears and no workload is mutated;
- zero churn;
- the proposal disappears on recovery without any recovery claim;
- the flag guard rejects O5 without the interaction summary.
