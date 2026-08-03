# bori Constitution

<!--
  ②-form: this file does NOT own cross-repo invariants. It references the
  platform canonical constitution and indexes only THIS repo's own enforced
  constraints. SoT for those is the rules themselves (CI workflows / Makefile),
  not this prose.
-->

## Cross-repo invariants live in the platform canonical (NodeVault §4)

Cross-repo invariants — reproducibility, `casHash`, `stableRef`, the artifact
dual-axis (`lifecycle_phase` / `integrity_health`), the sori boundary, and the
image-build / ResolveRecipe rules — are owned solely by the platform canonical:
**`github.com/HeaInSeo/NodeVault` — `docs/PLATFORM_MASTER_DESIGN.md` §4**
(immutable architecture decisions). This document does not restate or fork
them; on any conflict, §4 wins.

## Process discipline (repo-operational — owned by this repo)

- **Deterministic gates are the guarantee.** Merge is decided by deterministic
  checks (the required status checks below). LLM/agent review is **advisory**: a
  passing review never merges alone, and a failing required gate is never
  overridden.
- **Spec-anchored change**; **test-first** for behavioral changes (the CI test
  job runs the `-race` / `-shuffle` variant); **Builder/Critic separation**
  (read-only Critic pass before merge).
- **Generated artifacts stay in sync.** CRD YAML and DeepCopy are generated
  (`make generate`); changes to `apis/...` ship with the regenerated output.
- **Local verify (before a PR):** `make build test` and, when touching
  `config/` or `apis/`, `make generate` to confirm no drift.
- **Branch protection**: `main` lands via PR under the active
  `main-branch-protection` ruleset; no direct pushes, no force-pushes, no branch
  deletion, and review threads must be resolved.
- **Repo agent guidance**: bori does not currently maintain an `AGENTS.md`; this
  constitution therefore does not point to or duplicate any repo-local agent
  workflow prose. If an `AGENTS.md` is later added, agent guidance belongs there,
  not here.

## Repo-local enforced constraints (derived index — NOT canonical)

> Derived index of THIS repo's own gates. Not canonical — SoT is the CI
> workflow / Makefile itself. A gate is **IMPLEMENTED** only when its enforcing
> job is one of the repository's **required status checks** (which genuinely
> block merge under the active ruleset); a gate that runs on PRs but is **not**
> a required check does not block merge, so it is marked **PROPOSED**.

Required status checks (from ruleset `main-branch-protection`): `Unit tests + build`,
`Analyze go`, `Analyze actions`.

**IMPLEMENTED (required-check-backed, blocking):**

1. **Unit tests + build** (IMPLEMENTED — `ci.yml` job `unit-and-build`, required
   check "Unit tests + build"): `go test -race -shuffle=on -count=1 ./...` plus
   `go build ./...`. Concurrency-safety (race) and buildability, blocking.
2. **CodeQL — Go** (IMPLEMENTED — `codeql.yml`, required check "Analyze go"):
   static security/quality analysis of Go code, blocking.
3. **CodeQL — Actions** (IMPLEMENTED — `codeql.yml`, required check
   "Analyze actions"): static analysis of workflow definitions, blocking.

**PROPOSED (runs in CI but NOT a required check → does not block merge; or not
PR-triggered):**

4. **golangci-lint** (PROPOSED — `golangci-lint.yaml`, PR-triggered, path-filtered
   on `**/*.go`): lint gate, runs but not required.
5. **govulncheck** (PROPOSED — `ci.yml` job `vuln-scan`, PR-triggered):
   vulnerability scan, runs but not required.
6. **actionlint** (PROPOSED — `ci.yml` job `actionlint`, PR-triggered): workflow
   linting, runs but not required.
7. **module drift** (PROPOSED — `ci.yml` job `module-drift`, PR-triggered):
   `go mod tidy` cleanliness of `go.mod`/`go.sum`, runs but not required.
8. **generate-check** (PROPOSED — `generate-check.yaml`, PR-triggered,
   path-filtered): CRD + DeepCopy generated files up-to-date, runs but not
   required.
9. **kube-linter** (PROPOSED — `kubelint.yaml`, PR-triggered, path-filtered on
   `config/`): Kubernetes manifest lint, runs but not required.
10. **kubeconform** (PROPOSED — `kubeconform.yaml`, PR-triggered, path-filtered on
    `config/`): manifest schema validation, runs but not required.
11. **kind smoke (K0 boot / K1 functional / K2 digest)** (PROPOSED —
    `kind-boot-smoke.yml`, `kind-functional-smoke.yml`, `kind-digest-smoke.yml`,
    PR-triggered, path-filtered): cluster smoke tests, run but not required.
12. **vm-integration** (PROPOSED — `vm-integration.yml`): nightly schedule /
    manual dispatch only, not PR-triggered and not required.

## §1.10 — "do not record what you did not observe"

**Status: PROPOSED (not enforced in this repo).** §1.10 is a cross-repo rule and
is **not yet part of NodeVault §4** (§4.10 pending). bori has **no deterministic
gate** enforcing it today, so it is marked PROPOSED, not IMPLEMENTED, until such
a gate exists (and until §4.10 lands as the canonical owner).

**Version**: 1.0.0 | **Ratified**: 2026-08-03 | **Last Amended**: 2026-08-03
