# bori — Genomic Dataplane Control Plane

[![golangci-lint](https://github.com/HeaInSeo/bori/actions/workflows/golangci-lint.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/golangci-lint.yaml)
[![kube-linter](https://github.com/HeaInSeo/bori/actions/workflows/kubelint.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kubelint.yaml)
[![kubeconform](https://github.com/HeaInSeo/bori/actions/workflows/kubeconform.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kubeconform.yaml)
[![kind-boot-smoke](https://github.com/HeaInSeo/bori/actions/workflows/kind-boot-smoke.yml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kind-boot-smoke.yml)
[![kind-functional-smoke](https://github.com/HeaInSeo/bori/actions/workflows/kind-functional-smoke.yml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kind-functional-smoke.yml)

bori is a Kubernetes operator that manages the lifecycle of genomic dataplane applications — JUMI, artifact-handoff, nan, tori, and NodeSentinel.

It reconciles `BoriDataPlane` custom resources, tracks deployment history via `BoriRevision`, and gates promotion through [kube-slint](https://github.com/HeaInSeo/kube-slint)'s `slint-gate`.

한국어 문서: [README.ko.md](README.ko.md)

---

## Architecture

```
BoriDataPlane CR  →  bori-operator  →  deploy / verify / promote
                           │
                    BoriRelease (release.yaml)
                    BoriRevision (immutable history)
                           │
                    slint-gate (kube-slint)
                      └── sli-summary.json → PASS / FAIL / WARN
```

### Custom Resources

| CRD | Purpose |
|-----|---------|
| `BoriDataPlane` | Desired state: which release runs in which environment |
| `BoriRelease` | Versioned component manifest (jumi, artifact-handoff, nan, …) |
| `BoriRevision` | Immutable deployment snapshot; gates promotion via kube-slint |

### Conditions on BoriDataPlane

| Condition | Meaning |
|-----------|---------|
| `Installed` | All release components are deployed |
| `Ready` | All components pass readiness checks |
| `Verified` | slint-gate evaluation returned PASS |
| `Promoted` | Revision promoted to active |
| `Degraded` | One or more components are out-of-sync |

---

## Quick Start

### Prerequisites

- Go 1.26+
- kubectl configured for the target cluster
- docker **or** podman (for kind smoke tests — auto-detected)
- `slint-gate` binary on PATH ([kube-slint](https://github.com/HeaInSeo/kube-slint))
- k8sgpt on the cluster host (`/usr/bin/k8sgpt`)

### Build

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
make build          # produces bin/bori and bin/bori-operator
```

### Build operator image (no Docker)

```bash
# buildah (available on RHEL/CentOS)
buildah build -t localhost/bori-operator:latest .

# transfer to cluster nodes
podman save localhost/bori-operator:latest -o /tmp/bori-operator.tar
for node in 192.168.122.99 192.168.122.232 192.168.122.207; do
  scp /tmp/bori-operator.tar ubuntu@$node:/tmp/
  ssh ubuntu@$node "sudo ctr -n k8s.io images import /tmp/bori-operator.tar"
done
```

### Deploy to cluster

`make deploy` applies CRDs, RBAC, ConfigMap, and the operator Deployment — then automatically runs the regression check.

```bash
make deploy
```

To tear down:

```bash
make undeploy
```

---

## Release and Environment definitions

A `BoriRelease` lives in `releases/<name>/release.yaml`:

```yaml
name: jumi-ah-dev
components:
  - name: jumi
    version: v0.3.0
  - name: artifact-handoff
    version: v0.2.0
  - name: nan
    version: v0.1.5
verification:
  policies:
    - jumi-ah-smoke
```

An environment lives in `environments/<name>/environment.yaml`:

```yaml
name: infra-lab
cluster:
  kubeconfig: ${KUBECONFIG}
  context: kubernetes-admin@kubernetes
registry:
  default: ghcr.io/heainseo
```

Apply a `BoriDataPlane` to wire them together:

```bash
kubectl apply -f testdata/fixtures/bdp-infra-lab-smoke.yaml
```

---

## Testing

Tests are organized in three layers:

```
Layer 3 — VM Integration        hack/test-vm-integration.sh
          BORI_VM_REMOTE        real cluster, conditions regression, SLI baseline
─────────────────────────────────────────────────────────────────────────────────
Layer 2-K1 — kind Functional    hack/test-kind-functional-smoke.sh
             Smoke              ConfigMap bori-root + shell adapter → BoriRevision
─────────────────────────────────────────────────────────────────────────────────
Layer 2-K0 — kind Boot Smoke    hack/test-kind-boot-smoke.sh
                                operator boot + /metrics + conditions recorded
─────────────────────────────────────────────────────────────────────────────────
Layer 1 — Unit Tests            make test (GOPROXY=off go test ./...)
                                always runs, primary CI gate
```

### Run tests

```bash
make test                                   # Layer 1: unit tests (no network)
make kind-boot-smoke                        # Layer 2-K0: operator boot in kind
make kind-boot-smoke ARGS=--keep            # keep cluster for debugging
make kind-func-smoke                        # Layer 2-K1: BoriRevision creation in kind
make kind-func-smoke ARGS=--keep
BORI_VM_REMOTE=user@your-vm-ip make vm-integration        # Layer 3: real cluster
BORI_VM_REMOTE=user@your-vm-ip make vm-integration ARGS=--update-baseline
```

> **Layer 3 (VM integration)** requires `BORI_VM_REMOTE` to be set to the SSH target of your VM (e.g. `user@your-vm-ip`). In GitHub Actions the value comes from the `BORI_VM_REMOTE` repository variable (Settings → Variables). The script exits immediately with a clear error if the variable is not set.

Kind smoke tests require docker or podman — the scripts auto-detect which is available.

> **Rootless Podman and kind**: rootless Podman may not work for kind in all environments due to cgroup v2 delegation requirements. If kind tests fail locally, try one of:
> - Use Docker instead of Podman
> - Use rootful Podman (`sudo podman`) or a Docker-compatible socket
> - Verify via GitHub CI — kind smoke tests run on Docker-backed `ubuntu-latest` runners
> - VM integration tests run on a self-hosted runner via `workflow_dispatch` or nightly schedule

### Test framework

`test/e2e/` uses **Ginkgo/Gomega** with build tags to isolate test suites:

| Build tag | Suite | kube-slint |
|-----------|-------|------------|
| `kind` | K0 boot smoke (`kind_smoke_test.go`) | `BeforeSuite`/`AfterSuite` |
| `kindfunc` | K1 functional smoke (`kind_functional_smoke_test.go`) | `BeforeSuite`/`AfterSuite` |

kube-slint (`sess.Start()` / `sess.End()`) is wired to `BeforeSuite` / `AfterSuite` — SLI measurement spans the full test suite.

---

## kube-slint integration

bori does **not** import kube-slint as a Go library in production code. It writes `sli-summary.json` (slint.summary.v4 schema) and invokes `slint-gate` as a subprocess.

```
bori verify  →  sli-summary.json  →  slint-gate --fail-on NEVER  →  gate_result
```

In `test/e2e/`, kube-slint is imported as a Go library (`//go:build kind || kindfunc`) for in-process SLI measurement during smoke tests.

kube-slint is fully usable independently of bori.

---

## Regression testing

After every `make deploy`, bori automatically checks that the operator is still writing the expected `BoriDataPlane` conditions:

```bash
make regression                       # compare against baseline
make regression -- --update-baseline  # accept current state as new baseline
```

The script (`scripts/regression-check.sh`) auto-detects whether it is running on the cluster node or from a local machine (SSH fallback), runs k8sgpt analysis, and diffs conditions against `testdata/baseline/`.

---

## CI

| Workflow | Layer | Trigger | What it checks |
|----------|-------|---------|----------------|
| `ci.yml` | 1 | PR / main push | `go test ./...` + `go build` |
| `golangci-lint` | — | `*.go`, `go.mod` | govet, staticcheck, errcheck, unused, ineffassign, revive |
| `kube-linter` | — | `config/**` | K8s manifest best practices |
| `kubeconform` | — | `config/**` | Schema validation against K8s 1.30 |
| `kind-boot-smoke` | 2-K0 | `workflow_dispatch` + paths | Operator boot, /metrics, conditions |
| `kind-functional-smoke` | 2-K1 | `workflow_dispatch` + paths | BoriRevision creation via shell adapter |
| `vm-integration` | 3 | nightly + `workflow_dispatch` | Real cluster conditions regression |

---

## Repository structure

```
bori/
├── apis/bori/v1alpha1/     # CRD Go types (BoriDataPlane, BoriRelease, BoriRevision)
├── controllers/            # controller-runtime reconcilers
├── cmd/
│   ├── bori/               # CLI: plan / deploy / verify / status / revision …
│   ├── bori-operator/      # Kubernetes operator entrypoint
│   └── bori-devspace/      # DevSpace after:deploy adapter
├── pkg/
│   ├── adapter/            # Runner interface + slint-gate shell-out + sli-summary builder
│   ├── verification/       # kube-slint policy evaluation
│   ├── reconcile/          # core reconcile loop
│   ├── revision/           # BoriRevision management
│   └── …
├── adapters/               # Deploy adapters (devspace, ko, kustomize, shell)
├── config/
│   ├── crd/                # BoriDataPlane / BoriRelease / BoriRevision CRD YAML
│   ├── rbac/               # ClusterRole, ServiceAccount, binding
│   └── operator/           # Deployment, ConfigMap, Namespace
├── releases/               # BoriRelease definitions (e.g. jumi-ah-dev)
├── environments/           # Environment definitions (infra-lab, kind, multipass, …)
├── components/             # Per-app component.yaml specs
├── verification/policies/  # slint-gate policy files
├── test/e2e/               # Ginkgo/Gomega e2e tests (kind + kindfunc build tags)
│   ├── manifests/          # kind-specific operator manifests + ConfigMaps
│   └── fixtures/           # BoriRelease / BoriDataPlane smoke fixtures
├── testdata/
│   ├── fixtures/           # Test BoriDataPlane CRs
│   └── baseline/           # Condition snapshots for regression check
├── hack/
│   ├── test-kind-boot-smoke.sh      # K0 kind smoke runner
│   ├── test-kind-functional-smoke.sh # K1 kind functional smoke runner
│   └── test-vm-integration.sh       # Layer 3 VM integration runner
├── scripts/
│   └── regression-check.sh # BoriDataPlane condition regression check
├── Dockerfile              # Multi-stage: golang:1.26 → distroless/static
└── docs/                   # Architecture, roadmap, API design, security model
```

---

## Roadmap

Full roadmap: [docs/control-plane-roadmap.md](docs/control-plane-roadmap.md)

All Phases 0–10 and kube-slint Tracks K0–K5 are complete.

---

## Known Issues / 기술 부채

코드 리뷰에서 발견됐지만 현재 PR 범위에서 수정하지 않은 항목:
[docs/known-issues.md](docs/known-issues.md)

## Architecture Decision Records (ADR)

설계 결정 및 보류 중인 선택지:

| ADR | 상태 | 내용 |
|-----|------|------|
| [ADR-001](docs/adr/ADR-001-borirevision-failreason.md) | Pending | BoriRevision.failReason 위치 — spec vs status |
| [ADR-002](docs/adr/ADR-002-controller-gen.md) | Review Needed | controller-gen 도입 여부 및 CRD schema drift 방지 |
