# Operator interaction summary (O4)

> **Status: CANDIDATE, opt-in.** The summary is a derived, **non-authoritative**
> projection. It never feeds back into capability truth, the R0 axes or any
> action authority. The levels are not a severity order of capability states,
> and there is no overall platform health. Response candidates are **display
> only**: nothing here requests, approves or runs an action. Approval, external
> execution and RecoveryAssessment belong to O5 and are not implemented.

## What the canon fixes and what this implementation chooses

| Fixed by the canon (API v0.1 §3/§10/§18 O4, interaction contract §2–4, §7, §11) | Chosen by this implementation (packet O4, 2026-10-08) |
|---|---|
| Four semantic levels NO_ACTION / AWARENESS / DECISION_REQUIRED / IMMEDIATE_INTERVENTION | The decision table below, its reason codes and the order of checks |
| The level is a non-authoritative projection over capability truth plus the human-decision boundary | It is an optional `OperationalTarget.status.interaction` field; no new CRD |
| A declared affected capability that is UNKNOWN is never NO_ACTION | Field names, list bounds and the 256-character summary line |
| Minimum information: problem, affected/unaffected, facts and currentness, missing evidence, cause candidates, what was already checked, response candidates with conditions/target/owner, why a person is needed, post-conditions | The human-decision boundary comes from an explicit operator **reference profile** file, not from an `OperationalPolicy` CRD (that stays OPEN, API §19) |
| No feedback into truth, no actuation, Pod Ready or action success ≠ recovery | Fingerprint-based dedup with a 4-entry recurrence history per identity |
| Dedup/grouping must not allow a notification storm; exact algorithm left to implementation | `bori ops interaction` renders kubectl JSON; kubectl printer columns |

## Pipeline

```
O3 investigation (unchanged) → O1 Evaluate (unchanged) → opswire.Project (unchanged,
redacted) → interaction.Project(status, current episode, profile, previous summary)
→ status.interaction → opswire.Apply (written only on semantic change)
```

`pkg/interaction` is pure: it reads only the target's own projected status,
the investigation record of the target's **exact current identity**
(`Investigator.Current` returns nothing for a replaced target, workload,
contract or provider binding) and the profile. It makes no provider call,
reads no other object and consumes no investigation budget. All
cross-namespace, denied-reference and cycle redaction is inherited from the
status it reads, so the summary cannot disclose more than the status already
does.

## Enabling

```
bori-operator --enable-operational-assessment \
  --operational-provider-config /etc/bori/providers.yaml \
  --enable-operational-interaction \
  --operational-interaction-profile /etc/bori/interaction-profile.json
```

Without `--enable-operational-interaction` the status is exactly the O2/O3
status (`interaction` is absent). Without a profile, no response is declared
and the summary reports that lack. The profile flag requires the enable flag.

### Reference profile

```json
{
  "revision": "p1",
  "responses": [{
    "action": {"name": "failover", "revision": "r1"},
    "target": {"namespace": "apps", "name": "y", "uid": "optional-pin"},
    "for": {"domain": "example.io", "name": "persist", "revision": "v1"},
    "states": ["UNAVAILABLE"],
    "owner": "storage-oncall",
    "requiresApproval": true,
    "risks": ["data-loss"],
    "preconditions": [{"domain": "example.io", "name": "serve", "revision": "v1"}]
  }]
}
```

- Strict decoding: unknown fields are rejected. At most 128 responses, 8
  preconditions and 4 risks each. Risks are `data-loss`,
  `service-interruption` or `security-scope-expansion`.
- A response applies only to the exact `namespace/name` (and `uid` when
  pinned) and only while its capability is in one of `states` (default
  DEGRADED, UNAVAILABLE). Preconditions name capabilities **of the same
  target**; they are proven only when AVAILABLE on current evidence.
- The profile is operator configuration. It is not inferred from provider
  text or a model, and it grants no execution authority. Changing
  `revision` is a new identity for every summary.

## Output

| Field | Content | Bound |
|---|---|---|
| `level` | one of the four levels | enum |
| `summary` | one deterministic line; never says "recovered" | 256 chars |
| `levelReasons` | decision-table reason codes with subject | 8 |
| `affected` / `unaffected` / `unknown` | capabilities by O1 state (DEGRADED or UNAVAILABLE / AVAILABLE / UNKNOWN), each with ≤4 already-redacted O1 reasons | 64 each |
| `confirmedFacts` | own slots with `Current` evidence, provider, config revision, permitted evidence reference | 64 |
| `missingEvidence` | own slots without current evidence (state only, no reference) and `BindingDenied:<type>` | 64 |
| `causeCandidates` | O3 hypotheses (Maintained, Open, Refuted) about capabilities not AVAILABLE now; never facts | 16 |
| `investigation` | outcome class of the identity's latest episode (NotRun, InProgress, Concluded, NoAllowedQuery, BudgetExhausted, Cancelled, CapacityLimited, Superseded) and hypothesis counts | — |
| `responses` | declared responses with status Ready, ApprovalRequired, SafetyUnproven, OwnerUndeclared, OwnerConflict or Inapplicable; exact target `ns/name#uid`, owner, approval, risks, preconditions (proven/unproven/unmet) | 8 |
| `humanReasons` | why a person is needed; empty below DECISION_REQUIRED | 8 |
| `pendingPostConditions` | `capability-available-on-current-evidence` / `envelope-satisfied-on-current-evidence`, always `unconfirmed` while not proven | 16 |
| `identityDigest`, `fingerprint`, `recentFingerprints`, `recurring`, `recurrences` | dedup (below) | 64 chars, 4 entries |

The investigation summary has no counters, kinds or times. A finished
investigation and a finished freshness refresh both read `Concluded`, so
routine refreshes do not change it.

## Decision table

Checks are evaluated together; the level is the highest one raised.

| Condition (from status, investigation record and declared profile) | Level | Reason code | Human reason |
|---|---|---|---|
| Target not assessed (invalid, unresolved, cycle) — no capability result is produced | DECISION_REQUIRED | `target-not-assessed` | `spec-correction-required` |
| Valid target with no capability | AWARENESS | `no-capabilities-assessed` | — |
| Any capability UNKNOWN | AWARENESS | `capability-unknown` | — |
| Selected or Minimum-class envelope UNSATISFIED / UNKNOWN | AWARENESS | `envelope-unsatisfied` / `envelope-unknown` | — |
| Investigation episode still active | AWARENESS | `investigation-in-progress` | — |
| Affected only through dependencies, nothing declared (the dependency's own target carries the decision) | AWARENESS | `impact-from-dependency` | — |
| Affected locally, nothing declared (the lack is reported) | AWARENESS | `no-declared-response` | — |
| Exactly one viable response, status Ready | AWARENESS | `declared-response-ready` | — |
| Viable response needs approval or declares a risk | DECISION_REQUIRED | `approval-required` | `approval-required` |
| Viable response's precondition is unproven | DECISION_REQUIRED | `safety-condition-unproven` | same |
| Viable response has no owner / two owners for one action | DECISION_REQUIRED | `owner-undeclared` / `owner-conflict` | same |
| Two or more viable responses, no priority authority | DECISION_REQUIRED | `no-priority-authority` | same |
| Every declared response inapplicable (precondition unmet), capability DEGRADED or UNKNOWN | DECISION_REQUIRED | `no-viable-response` | same |
| Every declared response inapplicable, capability UNAVAILABLE | IMMEDIATE_INTERVENTION | `no-safe-path` | same |
| None of the above: every capability AVAILABLE on current evidence and every considered envelope SATISFIED | NO_ACTION | `all-capabilities-available` | — |

Guarantees encoded by the table:

- UNKNOWN is at least AWARENESS and never reaches IMMEDIATE_INTERVENTION by
  itself.
- Invalid or unassessed targets, zero targets and unbound required slots are
  never NO_ACTION.
- Escalation above AWARENESS needs a declared boundary; nothing is inferred.

## Dedup, grouping and flapping

- `identityDigest` hashes the target UID, resolved workload UID, contract UID
  and spec digest, every provider binding with its config revision, denied
  bindings and the profile revision.
- `fingerprint` hashes the meaning: identity, level and reasons, capabilities
  by state, missing evidence, response status and human reasons. Evidence
  references, investigation progress and wording are excluded.
- Same fingerprint ⇒ identical summary ⇒ no status write. Timestamp-only
  refreshes, repeated reconciles and queries therefore write nothing.
- A changed fingerprint is always written: real transitions are never hidden.
  If the new meaning is among the last 4 of the same identity, `recurring` is
  true and `recurrences` increments. A consumer raises at most one request
  per fingerprint and none for a recurring one.
- A changed identity starts an empty history, so no earlier meaning,
  decision, investigation or response is reused for it.
- Fan-out: a capability affected only through a dependency is AWARENESS at
  the dependent; the decision stays at the dependency's own target.

## Querying

```
kubectl get operationaltargets -n apps                 # INTERACTION column
kubectl get operationaltargets -n apps -o wide         # + SUMMARY column
kubectl get operationaltarget y -n apps -o jsonpath='{.status.interaction}'
kubectl get operationaltargets -A -o json | bori ops interaction            # text
kubectl get operationaltargets -A -o json | bori ops interaction -o json    # JSON + counts
```

`bori ops interaction` only formats what the statuses hold. Its overview gives
counts per level, never a single overall level. An empty list prints "nothing
is assessed (this is not NO_ACTION)". Example (from the unit fixture):

```
3 targets: IMMEDIATE_INTERVENTION=0 DECISION_REQUIRED=1 AWARENESS=2 NO_ACTION=0, without summary=0 (counts only; no overall health)

apps/x
  level:      AWARENESS
  problem:    1 affected (submit UNAVAILABLE); 1 AVAILABLE on current evidence
  why:        impact-from-dependency example.io/submit@v1
  affected:   example.io/submit@v1 UNAVAILABLE: dependency-unmet
  available:  example.io/observe@v1 AVAILABLE
  confirmed:  status-up Current via apps/kube-status@r1 ref ref:apps/x/status-up
              submit-up Current via apps/http-probe@r1 ref ref:apps/x/submit-up
  checked:    NotRun; hypotheses refuted=0 maintained=0 open=0
  pending:    capability-available-on-current-evidence example.io/submit@v1 (unconfirmed)

apps/y
  level:      DECISION_REQUIRED
  problem:    1 affected (persist UNAVAILABLE); 2 AVAILABLE on current evidence
  why:        approval-required example.io/persist@v1
  affected:   example.io/persist@v1 UNAVAILABLE: predicate-unmet
  available:  example.io/count@v1 AVAILABLE
              example.io/serve@v1 AVAILABLE
  confirmed:  api-serving Current via apps/http-probe@r1 ref ref:apps/y/api-serving
              ...
  candidates: Maintained (hypothesis): example.io/persist@v1/assertion:storage-writable
  checked:    Concluded; hypotheses refuted=0 maintained=1 open=0
  responses:  failover@r1 for persist on apps/y#t-y, owner storage-oncall: ApprovalRequired; preconditions example.io/serve@v1=proven (display only)
  human:      approval-required failover@r1
  pending:    capability-available-on-current-evidence example.io/persist@v1 (unconfirmed)
```

## Requirement → code → test

| Requirement | Code | Tests |
|---|---|---|
| NO_ACTION only with current proof | `Project`, `finish` | `TestNoActionOnlyWithCurrentProof`, controller `TestO4VerticalSlice` |
| UNKNOWN ≥ AWARENESS, never IMMEDIATE alone | `Project`, `decideCapability` | `TestUnknownIsAwarenessNotFailure`, `TestUnknownNeverImmediate` |
| Invalid / zero targets / missing check ≠ NO_ACTION; no fabricated results | `Project` (invalid branch), `OverviewOf`, `RenderText` | `TestInvalidTargetIsDecisionWithoutCapabilities`, `TestZeroTargetsIsNothingAssessed`, `TestUnboundRequiredSlotIsNotNoAction`, CLI `TestOpsInteractionJSONAndEmpty` |
| A→X only; unrelated Y on its own evidence (current/stale) | inherits O1 + `fromDependencyOnly` | `TestDependencyImpactIsLocalized`, controller `TestO4VerticalSlice` |
| Partial vs independent failures not merged | per-target projection, per-capability lists | `TestPartialAndIndependentFailuresStaySeparate` |
| Outage / stale / conflict ≠ app failure; no reuse of old AVAILABLE | `confirmed`, `missing` | `TestEvidenceFailuresAreNotAppFailures`, controller `TestO4ProviderFailureIsNotAppFailure` |
| O3 record (checked, missing, refuted, budget end) reaches output | `Investigator.Current`, `investigationOf`, `candidates` | controller `TestO4VerticalSlice`, `TestO4ProviderFailureIsNotAppFailure` |
| Human boundary: approval, priority, owner conflict/absence, unproven safety, no safe path | `declared`, `decideCapability` | `TestDeclaredBoundaryLevels`, `TestResponsesBindToExactTarget`, controller `TestO4DeclaredApprovalIsDisplayOnly` |
| Repeat / refresh / fan-out / flapping dedup; transitions kept | `fingerprint`, `finish` | `TestRepeatAndRefreshDoNotChurn`, `TestFlappingIsShownButRequestedOnce`, `TestBurdenAgainstBaseline`, controller `TestO4ZeroChurn`, `TestO4SteadyFailureDoesNotChurn` |
| Identity / contract / provider / profile revision change reuses nothing | `identityDigest`, `Investigator.Current` | `TestIdentityChangeResetsHistory`, controller `TestO4IdentityChangeReusesNothing` |
| Cross-namespace denied / existence / cycle non-disclosure | reads redacted status only | `TestCrossNamespaceNonDisclosure`, `TestCrossNamespaceCycleStaysRedacted` |
| Deterministic, bounded, order-independent | sorting, `limit`, `clip` | `TestDeterministicUnderInputOrder`, `TestOutputIsBounded`, `TestInteractionSummaryIsBoundedStatus` (CRD) |
| Read-only, no extra provider I/O, no actuation | controller wiring | controller `TestO4AddsNoProviderIO`, `otherWrites == 0` in every controller test, live kind checks |
| No recovery claim from readiness or action success | `summaryLine`, `pendingPostConditions` | `TestReadinessAloneIsNotRecovery`, forbidden checker in every scenario |
| No scenario/product branching | — | `TestProjectionIsAppAndScenarioNeutral` |
| Forbidden outcomes all 0 | `check()` per reconcile in every pure scenario | `TestForbiddenCheckerDetectsViolations` proves the checker catches each class |

## Burden simulation (reproducible, not a measurement of people)

`TestBurdenAgainstBaseline` replays one script through the real path:

- 6 workers depend on one resolver.
- The resolver fails and then flaps twice.
- An unrelated service fails independently.
- Timestamp-only refreshes run between the events.

The baseline raises one item per non-AVAILABLE capability on every reconcile.

| | Baseline | Projection |
|---|---|---|
| Decision requests | 91 | 1 |
| Items to a person (decisions + notices) | 95 | 8 |
| Duplicate notices | 87 | 0 |
| Manual slot look-ups | 194 | 0 |
| Forbidden outcomes | — | 0 in every step |

Status writes are 52, one per real transition per target. No time saving or
operational SLO is claimed; there was no human experiment.

## Not included

- ActionProposal, approval, external ActionProvider, execution and
  RecoveryAssessment (O5).
- Real-world packs (O6).
- `OperationalPolicy` CRD.
- Notification channels.
- Provider transport/discovery.
- Paid models.

The summary never states that a capability recovered. It states only what is
AVAILABLE on current evidence.
