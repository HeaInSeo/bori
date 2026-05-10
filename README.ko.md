# bori — Build, Orchestration, and Runtime Integration

bori는 **파일 2개만 추가**하면 K8s 앱에 SLI 게이트를 붙여주는 도구입니다.

앱 저장소에 `.bori/` 디렉터리를 추가하면, bori가 자동으로 발견하고
smoke 전후 `/metrics`를 스크레이핑한 뒤 `slint-gate`로 회귀 여부를 판단합니다.

- **Self-registration**: 각 앱이 자신의 policy를 소유합니다. bori는 앱 목록을 하드코딩하지 않습니다.
- **DevSpace 연동**: `after:deploy` hook으로 실행 — 배포 흐름을 변경할 필요 없습니다.
- **kube-slint 독립성**: bori는 `slint-gate` 바이너리만 호출합니다. kube-slint는 bori 없이도 완전히 독립적으로 사용 가능합니다.

English document: [README.md](README.md)

---

## 동작 방식

```
앱 저장소
  └── .bori/
        ├── component.yaml          # 메트릭 스크레이핑 위치
        └── policy.devspace.yaml    # 회귀 판단 기준

devspace dev  (앱 디렉터리에서 실행)
  └── after:deploy hook → bin/bori-devspace
        ① /metrics 수집  (smoke 전)
        ② smoke 실행 (또는 대기)
        ③ /metrics 수집  (smoke 후)
        ④ delta 계산 → sli-summary.json 생성
        ⑤ slint-gate 평가 → PASS / FAIL / WARN
```

bori는 **상위 디렉터리**를 스캔하여 `.bori/component.yaml`이 있는 형제 저장소를 찾습니다.
발견된 모든 앱을 한 번의 실행에서 평가합니다.

---

## 사전 준비

아래 작업은 **K8s VM 호스트** (클러스터가 실행 중인 장비)에서 수행합니다.

### DevSpace 설치

```bash
curl -sSL https://github.com/loft-sh/devspace/releases/latest/download/devspace-linux-amd64 \
  -o /usr/local/bin/devspace
chmod +x /usr/local/bin/devspace
devspace version
```

### bori 빌드

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
make build          # bin/bori-devspace 생성
```

### slint-gate 설치

bori는 PATH에서 `slint-gate` 바이너리를 찾습니다.

```bash
cd ../kube-slint
go build -o /usr/local/bin/slint-gate ./cmd/slint-gate
```

---

## 앱에 bori 추가하기 (self-registration)

### Step 1 — `.bori/component.yaml`

```yaml
name: my-app        # Kubernetes Service 이름과 일치해야 함
port: 8080
metrics_path: /metrics
namespace: my-ns
```

### Step 2 — `.bori/policy.devspace.yaml`

slint-gate policy 포맷을 그대로 사용합니다.

```yaml
thresholds:
  - name: "요청 처리 확인"
    metric: my_app_requests_total
    operator: ">="
    value: 0
  - name: "에러율 허용 범위"
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

프로파일별 파일:

| 파일 | 환경 |
|------|------|
| `policy.devspace.yaml` | DevSpace inner-loop |
| `policy.kind.yaml` | kind cluster |
| `policy.multipass.yaml` | Multipass VM |

### Step 3 — `devspace.yaml`에 hook 추가

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

`bori-devspace`는 PATH에 있어야 합니다. 또는 전체 경로로 지정하세요 (`/path/to/bori/bin/bori-devspace`).

### 이후 사용법

```bash
cd your-app
devspace dev
# DevSpace가 앱을 배포하고, bori가 SLI 게이트를 자동으로 평가합니다.
```

---

## bori 단독 실행

```bash
# 기본: 상위 디렉터리 스캔, devspace 프로파일, 10초 대기
./bin/bori-devspace --profile devspace --v

# smoke 커맨드 지정
./bin/bori-devspace \
  --profile devspace \
  --smoke-cmd "kubectl exec -n my-ns deploy/my-app -- /bin/smoke-test" \
  --v

# 앱 디렉터리 명시
./bin/bori-devspace \
  --apps-dir /path/to/repos \
  --profile devspace \
  --v
```

### 플래그

| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `--profile` | `devspace` | 프로파일: `devspace`, `kind`, `multipass` |
| `--apps-dir` | bori root 상위 디렉터리 | 앱 저장소 스캔 경로 |
| `--smoke-cmd` | _(없음)_ | smoke 단계 셸 명령 |
| `--smoke-wait` | `10s` | `--smoke-cmd` 미지정 시 대기 시간 |
| `--out` | `bori-gate-output` | 게이트 아티팩트 출력 경로 |
| `--slint-gate` | `slint-gate` | slint-gate 바이너리 경로 |
| `--v` | false | 상세 출력 |

### 출력 예시

```
[bori] found 2 registered app(s)
[bori] pre-smoke scrape: my-app
[bori] waiting 15s for my-app
[bori] post-smoke scrape: my-app
[bori] my-app                PASS — Policy checks passed.
[bori] overall: PASS
```

---

## kube-slint와의 관계

bori는 kube-slint를 **Go 라이브러리로 import하지 않습니다.**
`sli-summary.json` (slint.summary.v4 스키마)을 작성하고 `slint-gate`를 서브프로세스로 호출합니다.
kube-slint는 bori 없이도 완전히 독립적으로 사용 가능합니다.

---

## 향후 계획

| 항목 | 상태 |
|------|------|
| DevSpace adapter | 구현 완료 |
| Tilt adapter | 계획 중 |
| 다중 앱 병렬 평가 | 계획 중 |
| kind / multipass 프로파일 | policy 파일 추가로 지원 |

---

## 저장소 구조

```
bori/
├── pkg/adapter/
│   ├── adapter.go        # Runner interface, AppSnapshot / RunRequest / RunResult
│   ├── gate_runner.go    # slint-gate shell-out 구현
│   └── summary.go        # sli-summary.json builder (slint.summary.v4 호환)
├── adapters/
│   ├── devspace/
│   │   ├── main.go       # CLI: discovery → scrape → smoke → gate
│   │   ├── component.go  # .bori/component.yaml 파서
│   │   └── collect.go    # kubectl port-forward + /metrics 스크레이핑
│   └── tilt/             # 향후
├── schema/
│   ├── component.schema.yaml
│   └── policy.schema.yaml
├── example/
│   └── .bori/
│       ├── component.yaml
│       └── policy.yaml
└── Makefile
```
