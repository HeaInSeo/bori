# Product contract mapping and sources

I read every product at one exact commit, without modifying its repository:

| Product | Repository | Commit |
|---|---|---|
| JUMI | HeaInSeo/JUMI | `e6a4573ed79db40911524397361b06bfea2be712` |
| artifact-handoff | HeaInSeo/artifact-handoff | `9d07eddf837fbd778f4909e1fb23d43ba172ec02` |
| Tori | HeaInSeo/tori | `0b198d1297b5ba70b913802963ad2b2a420184c9` |

Paths are relative to each repository, and line numbers are at that commit.

The **Published** column says whether the product makes a fact available today:
- **kube**: the shipped Kubernetes-status provider can read it from the product's real Deployment;
- **derivable**: the product exposes it, but an adapter must translate it into the typed-response form;
- **absent**: the product exposes no such fact.

Every fact is served by the fixture evidence server in the packs (README, evidence classes).

## JUMI → `jumi-runtime-v1` (pack `jumi-artifact-handoff`)

### Facts

| Assertion | Type | Source | Published |
|---|---|---|---|
| `ready-replicas` | Integer | Deployment `status.readyReplicas`; manifest `replicas: 1` (`deploy/k8s/jumi.yaml:40`) | kube |
| `registry-readable` | Boolean | `/statusz` returns 500 when a registry `List*` call fails (`cmd/jumi/main.go:212-232`) | derivable |
| `backend-ready` | Boolean | `/statusz` `backendReady` (`pkg/observe/status.go`). It is `a.rt != nil` (`pkg/backend/spawner_k8s.go:98-101`), so in practice it is constantly true | derivable, weak |
| `registry-writable` | Boolean | SQLite registry, single connection (`pkg/registry/sqlite.go:20-40`) | absent |
| `registry-durable` | Boolean | `JUMI_REGISTRY_PATH` (`cmd/jumi/main.go:152-155`) on an `emptyDir` (`deploy/k8s/jumi.yaml:109-113`) | absent |

### Capabilities

| Capability | Requirements | Source of the meaning | Independent of the resolver? |
|---|---|---|---|
| `accept-runs` | ready ≥ 1, `registry-writable` | `SubmitRun` persists and admits; execution is asynchronous | yes |
| `query-runs` | ready ≥ 1, `registry-readable` | `GetRun`/`List*`, `/statusz` | yes |
| `execute-unbound-nodes` | ready ≥ 1, `backend-ready` | nodes become Jobs through the embedded spawner runtime; the resolver is called only per artifact binding (`pkg/executor/executor.go:765`) | yes |
| `resolve-node-inputs` | `execute-unbound-nodes` + resolver `resolve-artifact` | `ResolveHandoff` per binding (`executor.go:765-`) | no |
| `register-node-outputs` | `execute-unbound-nodes` + resolver `register-artifact` | `RegisterArtifact` failure fails the node (`executor.go:1818-1836`) | no |
| `complete-runs` | `registry-writable` + resolver `finalize-run` | `FinalizeSampleRun`/`EvaluateGC` run for every run; their failure turns a succeeded run into Failed (`executor.go:481-530`) | no |

### Envelopes and recovery

- **minimum:** `ready-replicas` exactly 1 (Gte 1 and Lte 1). The registry design is "a single authoritative JUMI writer; multi-replica leader/fencing is future work" (`pkg/registry/sqlite.go:20-24`).
- **recommended:** minimum plus `registry-durable`.
- **recovery proof:** fresh registry facts after the action (`registry-readable`). A pod becoming Ready is not proof (pack scenario `restart-without-registry-recovery`).

## artifact-handoff → `artifact-handoff-v1` (pack `jumi-artifact-handoff`)

### Facts

| Assertion | Type | Source | Published |
|---|---|---|---|
| `ready-replicas` | Integer | Deployment `status.readyReplicas`; manifest `replicas: 1` (`deploy/k8s/artifact-handoff.yaml:13`) | kube |
| `store-readable` | Boolean | store opened in-process (`cmd/artifact-handoff-resolver/main.go:22-29`, `pkg/inventory/store.go:58-71`). `/healthz` is a constant 200 (`pkg/resolver/http.go:15-18`) | absent |
| `store-writable` | Boolean | SQLite, one connection (`pkg/inventory/sqlite.go:27`) | absent |
| `store-durable` | Boolean | `AH_STORE_DSN=sqlite:/data/ah.db` (`artifact-handoff.yaml:44-45`) on an `emptyDir` (`artifact-handoff.yaml:66-69`); the default DSN is `memory` (`main.go:22-24`) | absent |
| `http-source-allowlist-configured` | Boolean | `AH_ALLOWED_HTTP_SOURCE_HOSTS` / `AH_ALLOW_ANY_HTTP_SOURCE` (`pkg/resolver/service.go:41-42`); without them every HTTP source is rejected (`service.go:855`) | absent |

### Capabilities

| Capability | Requirements | Source |
|---|---|---|
| `register-artifact` | ready ≥ 1, `store-writable` | `RegisterArtifact` (`pkg/resolver/service.go:50`, core `:109`) |
| `resolve-artifact` | ready ≥ 1, `store-readable` (read-only, so it does not need a writable store) | `ResolveHandoff` (`service.go:54`, core `:382`) |
| `manage-sources` | `register-artifact` + `store-readable` | `AddSource`/`UpdateSourceState`/`ListSources` (`service.go:90-98`). A source is visible only for a live Run-keyed artifact (`pkg/inventory/sqlite.go:490`) |
| `verify-http-sources` | ready ≥ 1, allowlist configured | `VerifySource` (`service.go:102`, core `:327`). Node-local sources do not need the allowlist |
| `accept-node-terminal` | ready ≥ 1, `store-writable` | `NotifyNodeTerminal` (`service.go:58`, core `:939`) |
| `finalize-run` | ready ≥ 1, `store-writable` | `FinalizeRun` / `EvaluateRunGC` (`service.go:62-66`, core `:965`, `:1001`) |
| `read-run-lifecycle` | ready ≥ 1, `store-readable` | `GetRunLifecycle` (`service.go:70`) |

### Envelopes and recovery

- **minimum:** ready ≥ 1.
- **recommended:** exactly one replica (one process per SQLite file) plus `store-durable`.
- artifact-handoff calls no other service. Kubernetes is needed only to deploy it.

## Tori → `tori-catalog-v1` + `shared-filesystem-v1` (pack `tori`)

### What Tori is at this commit

Tori is a CLI, `tori-admin`, with the commands `sync`, `snapshot`, `dump` and `reset-db`.
- `serve` is a no-op; its gRPC listener is commented out (`cmd/root.go:71-82`).
- Every command opens `file_monitor.db` relative to the working directory (`cmd/root.go:32-48`).
- It ships no Kubernetes manifests and publishes no facts.
- It calls no JUMI or artifact-handoff API: a repository-wide search finds no such reference.

### Facts

| Assertion | Type | Source | Published |
|---|---|---|---|
| `sync-outcome` | String enum `unchanged`, `accepted-update`, `degraded-hold`, `reclassify-hold`, `incomplete-pending` | `SyncResult.Outcome` (`db/acceptance.go:120-167`) | absent (returned to the CLI and logged only) |
| `source-scope` | String enum `CONFIRMED`, `UNKNOWN` | `SyncResult.Scope` (`db/acceptance.go:75-95`) | absent |
| `coverage` | String enum `COMPLETE`, `PARTIAL`, `UNKNOWN` | `SyncResult.Coverage` (`db/acceptance.go:97-118`) | absent |
| `acceptance-state` | String enum `clean`, `pending` | `snapshot_meta.acceptance_state` (`db/acceptance.go:67-70`, `db/queries/init.sql:21`) | absent |
| `catalog-db-writable` | Boolean | `file_monitor.db` (`cmd/root.go:39`) | absent |
| `datablock-readable` | Boolean | `<rootDir>/datablock.pb` read by `GetDataBlock` (`service/service.go:40`) | absent |
| `root-mounted`, `root-writable` | Boolean | shared filesystem root (NAS/Lustre), an external operator-side dependency | absent |

### Capabilities

| Capability | Requirements | Source |
|---|---|---|
| `accept-snapshot` | outcome not one of the holds (the two holds are UNAVAILABLE, `incomplete-pending` is DEGRADED), scope `CONFIRMED`, catalog database writable, and the root's `write-root` | `SyncFolders` (`service/service.go:85`, `db/acceptance.go`) |
| `catalog-consistent` | `acceptance-state` = `clean` (DEGRADED otherwise) | `incomplete-pending` leaves the snapshot pending (`db/acceptance.go:144-152`) |
| `serve-datablock` | `datablock-readable` and the root's `read-root`; not the catalog database | `GetDataBlock` (`service/service.go:40`) |
| `read-root` / `write-root` | `root-mounted` / plus `root-writable` | external mount |

### Single writer

`source_envelope` is a singleton (`init.sql:31-32`), but no lock enforces one writer. No replica fact applies to a CLI.

## Generic packs

`simple-http-service`, `replicated-service` and `stateful-single-writer` express reference packs 1, 3 and 4 of API v0.1 §15.
They are the same meanings as the O1 evaluator fixtures (`pkg/operations/fixtures_test.go`). Here they run through the shipped path as public manifests.
