# bori — Build, Orchestration, and Runtime Integration

bori is a developer environment orchestrator for Kubernetes-native dataplane applications.

- Bridges **DevSpace** (inner-loop dev tool) with **kube-slint** (SLI gate tool).
- Each app self-registers via a `.bori/` directory. bori maintains no hardcoded app list.
- kube-slint remains an independent SLI tool. bori invokes `slint-gate` as a CLI binary only.

한국어 문서: [README.ko.md](README.ko.md)

---

## Environment

### Development machine vs execution machine

| Role | Machine |
|------|---------|
| Code authoring / Git | Local development machine |
| DevSpace execution / K8s operations | `100.123.80.48` (K8s VM host) |

DevSpace and the bori adapter must be run **on `100.123.80.48`**.

---

## Install DevSpace (run on `100.123.80.48`)

### 1. Install DevSpace CLI

```bash
curl -sSL https://github.com/loft-sh/devspace/releases/latest/download/devspace-linux-amd64 \
  -o /usr/local/bin/devspace
chmod +x /usr/local/bin/devspace
devspace version
```

### 2. Verify installation

```bash
devspace version
# DevSpace version: v6.x.x
```

### 3. Verify kubectl connectivity

```bash
kubectl cluster-info
kubectl get nodes
```

---

## Install bori (run on `100.123.80.48`)

### 1. Clone the repository

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
```

### 2. Build the adapter binary

```bash
go build -o bin/bori-devspace ./adapters/devspace
```

### 3. Verify slint-gate is available

The bori adapter resolves `slint-gate` from PATH.

```bash
# Build from the kube-slint repository
cd ../kube-slint
go build -o /usr/local/bin/slint-gate ./cmd/slint-gate
slint-gate --help
```

---

## App registration (self-registration)

Add a `.bori/` directory to each dataplane app repository.
bori discovers these directories automatically. bori itself requires no changes.

### `.bori/component.yaml`

```yaml
name: my-app            # app name — must match the Kubernetes service name
port: 8080              # app port
metrics_path: /metrics  # Prometheus metrics path (default: /metrics)
namespace: my-ns        # Kubernetes namespace
```

### `.bori/policy.<profile>.yaml`

Uses the standard slint-gate policy format directly.

```yaml
thresholds:
  - name: "requests processed"
    metric: my_app_requests_total   # metric name as exposed by /metrics
    operator: ">="
    value: 1
  - name: "no errors"
    metric: my_app_errors_total
    operator: "<="
    value: 0

regression:
  enabled: false
  tolerance_percent: 10

reliability:
  required: false

fail_on:
  - threshold_miss
```

One file per profile:

| File | Environment |
|------|-------------|
| `policy.devspace.yaml` | DevSpace inner-loop (primary dev environment) |
| `policy.kind.yaml` | kind cluster |
| `policy.multipass.yaml` | Multipass VM lab |

---

## Usage

### With `devspace dev`

```bash
cd bori

# Start development — DevSpace deploys apps, after:deploy hook runs bori gate
devspace dev --profile devspace
```

### Run the adapter standalone

```bash
# Default (devspace profile, 10s wait before post-smoke scrape)
./bin/bori-devspace --profile devspace --v

# With a smoke command
./bin/bori-devspace \
  --profile devspace \
  --smoke-cmd "kubectl exec -n my-ns deploy/my-app -- /bin/smoke-test" \
  --v

# Specify the apps directory explicitly
./bin/bori-devspace \
  --apps-dir /opt/go/src/github.com/HeaInSeo \
  --profile devspace \
  --v
```

### Adapter flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | `devspace` | Profile name: `devspace`, `kind`, `multipass` |
| `--apps-dir` | parent of bori root | Directory to scan for app repos |
| `--smoke-cmd` | _(none)_ | Shell command to run as smoke step |
| `--smoke-wait` | `10s` | Wait duration when `--smoke-cmd` is not set |
| `--out` | `bori-gate-output` | Output directory for artifacts |
| `--slint-gate` | `slint-gate` | Path to the slint-gate binary |
| `--v` | false | Verbose output |

### Sample output

```
[bori] found 2 registered app(s)
[bori] pre-smoke scrape: jumi
[bori] waiting 10s for jumi
[bori] post-smoke scrape: jumi
[bori] jumi                 PASS — Policy checks passed.
[bori] pre-smoke scrape: artifact-handoff
[bori] waiting 10s for artifact-handoff
[bori] post-smoke scrape: artifact-handoff
[bori] artifact-handoff     PASS — Policy checks passed.
[bori] overall: PASS
```

---

## Architecture

```
App repositories (JUMI, artifact-handoff, tori, sori, ...)
  each with .bori/component.yaml + .bori/policy.<profile>.yaml
        ↓ discovered automatically by bori
bori/adapters/devspace (Go CLI)
  ① kubectl port-forward → scrape /metrics (pre-smoke)
  ② run smoke (--smoke-cmd or --smoke-wait)
  ③ kubectl port-forward → scrape /metrics (post-smoke)
  ④ compute deltas → write sli-summary.json
  ⑤ invoke slint-gate --measurement-summary ... --policy ...
        ↓
slint-gate (kube-slint standalone binary)
        ↓
gate-summary.json  (PASS / FAIL / WARN / NO_GRADE)
        ↓
dev-space observability page (published by batch-integration)
```

### DevSpace hook

```yaml
# devspace.yaml
hooks:
  - events: ["after:deploy"]
    command: "go"
    args: ["run", "./adapters/devspace", "--profile", "${BORI_PROFILE}", "--v"]
```

### Relationship with kube-slint

bori does **not** import kube-slint as a Go library.
It invokes the `slint-gate` binary via shell-out only.
kube-slint remains a fully independent SLI tool usable without bori.

---

## Roadmap

| Item | Status |
|------|--------|
| DevSpace adapter | done |
| Tilt adapter | planned |
| kind / multipass profiles | supported via policy file |
| Parallel multi-app evaluation | planned |

---

## Repository structure

```
bori/
├── pkg/adapter/
│   ├── adapter.go        # Runner interface, AppSnapshot / RunRequest / RunResult
│   ├── gate_runner.go    # slint-gate shell-out implementation
│   └── summary.go        # sli-summary.json builder (slint.summary.v4 compatible)
├── adapters/
│   ├── devspace/
│   │   ├── main.go       # CLI: discovery → scrape → smoke → gate
│   │   ├── component.go  # .bori/component.yaml parser
│   │   └── collect.go    # kubectl port-forward + /metrics scraping
│   └── tilt/             # planned
├── schema/
│   ├── component.schema.yaml   # .bori/component.yaml spec
│   └── policy.schema.yaml      # .bori/policy.<profile>.yaml spec
├── example/
│   └── .bori/
│       ├── component.yaml      # reference implementation
│       └── policy.yaml
├── devspace.yaml         # DevSpace compose + after:deploy hook
└── docs/
    └── architecture.md
```
