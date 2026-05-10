# bori — Build, Orchestration, and Runtime Integration

bori connects your K8s dataplane app to an SLI gate with **two files**.

Add a `.bori/` directory to any app repository. bori discovers it automatically,
scrapes `/metrics` before and after a smoke step, and invokes `slint-gate` to
evaluate whether the deployment regressed.

- **Self-registration**: each app owns its own policy. bori has no hardcoded app list.
- **DevSpace integration**: runs as an `after:deploy` hook — zero changes to your deploy flow.
- **kube-slint independence**: bori shell-outs to the `slint-gate` binary only. kube-slint is fully usable without bori.

한국어 문서: [README.ko.md](README.ko.md)

---

## How it works

```
Your app repository
  └── .bori/
        ├── component.yaml          # where to scrape metrics
        └── policy.devspace.yaml    # what counts as a regression

devspace dev  (run from your app directory)
  └── after:deploy hook → bin/bori-devspace
        ① scrape /metrics  (pre-smoke)
        ② run smoke command (or wait)
        ③ scrape /metrics  (post-smoke)
        ④ compute deltas → sli-summary.json
        ⑤ slint-gate evaluate → PASS / FAIL / WARN
```

bori scans the **parent directory** for sibling repos that contain `.bori/component.yaml`.
All discovered apps are evaluated in the same run.

---

## Prerequisites

Run the following on the **K8s VM host** (the machine running your cluster).

### Install DevSpace

```bash
curl -sSL https://github.com/loft-sh/devspace/releases/latest/download/devspace-linux-amd64 \
  -o /usr/local/bin/devspace
chmod +x /usr/local/bin/devspace
devspace version
```

### Build bori

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
make build          # produces bin/bori-devspace
```

### Install slint-gate

bori resolves `slint-gate` from PATH.

```bash
cd ../kube-slint
go build -o /usr/local/bin/slint-gate ./cmd/slint-gate
```

---

## Adding bori to your app (self-registration)

### Step 1 — `.bori/component.yaml`

```yaml
name: my-app        # must match the Kubernetes Service name
port: 8080
metrics_path: /metrics
namespace: my-ns
```

### Step 2 — `.bori/policy.devspace.yaml`

Uses the standard slint-gate policy format directly.

```yaml
thresholds:
  - name: "requests served"
    metric: my_app_requests_total
    operator: ">="
    value: 0
  - name: "error rate acceptable"
    metric: my_app_errors_total
    operator: "<="
    value: 5

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
| `policy.devspace.yaml` | DevSpace inner-loop |
| `policy.kind.yaml` | kind cluster |
| `policy.multipass.yaml` | Multipass VM |

### Step 3 — add the hook to your `devspace.yaml`

```yaml
vars:
  BORI_SMOKE_CMD:
    source: env
    default: ""
  BORI_SMOKE_WAIT:
    source: env
    default: "15s"

hooks:
  - events: ["after:deploy"]
    command: "bori-devspace"
    args:
      - "--profile"
      - "devspace"
      - "--apps-dir"
      - ".."
      - "--smoke-cmd"
      - "${BORI_SMOKE_CMD}"
      - "--smoke-wait"
      - "${BORI_SMOKE_WAIT}"
      - "--v"
```

`bori-devspace` must be on PATH, or specify the full path (`/path/to/bori/bin/bori-devspace`).

### That's it

```bash
cd your-app
devspace dev
# DevSpace deploys your app, then bori evaluates the SLI gate automatically.
```

---

## Running bori standalone

```bash
# default: scan parent directory, devspace profile, 10s wait
./bin/bori-devspace --profile devspace --v

# with a smoke command
./bin/bori-devspace \
  --profile devspace \
  --smoke-cmd "kubectl exec -n my-ns deploy/my-app -- /bin/smoke-test" \
  --v

# explicit apps directory
./bin/bori-devspace \
  --apps-dir /path/to/repos \
  --profile devspace \
  --v
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | `devspace` | Profile: `devspace`, `kind`, `multipass` |
| `--apps-dir` | parent of bori root | Directory to scan for app repos |
| `--smoke-cmd` | _(none)_ | Shell command to run as smoke step |
| `--smoke-wait` | `10s` | Wait duration when `--smoke-cmd` is not set |
| `--out` | `bori-gate-output` | Output directory for gate artifacts |
| `--slint-gate` | `slint-gate` | Path to the slint-gate binary |
| `--v` | false | Verbose output |

### Sample output

```
[bori] found 2 registered app(s)
[bori] pre-smoke scrape: my-app
[bori] waiting 15s for my-app
[bori] post-smoke scrape: my-app
[bori] my-app                PASS — Policy checks passed.
[bori] overall: PASS
```

---

## Relationship with kube-slint

bori does **not** import kube-slint as a Go library.
It writes a `sli-summary.json` (slint.summary.v4 schema) and invokes `slint-gate` as a subprocess.
kube-slint remains fully usable without bori.

---

## Roadmap

| Item | Status |
|------|--------|
| DevSpace adapter | done |
| Tilt adapter | planned |
| Parallel multi-app evaluation | planned |
| kind / multipass profiles | supported via policy file |

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
│   ├── component.schema.yaml
│   └── policy.schema.yaml
├── example/
│   └── .bori/
│       ├── component.yaml
│       └── policy.yaml
└── Makefile
```
