# bori — Build, Orchestration, and Runtime Integration

bori는 Kubernetes VM 환경에서 dataplane 애플리케이션을 개발할 때 사용하는 개발환경 오케스트레이터입니다.

- **DevSpace** (inner-loop dev tool) 와 **kube-slint** (SLI gate tool) 를 연결합니다.
- 각 앱은 `.bori/` 디렉토리를 통해 스스로를 등록합니다. bori는 앱 목록을 하드코딩하지 않습니다.
- kube-slint는 독립 SLI 도구로 그대로 유지됩니다. bori는 `slint-gate` 바이너리를 CLI로 호출합니다.

English document: [README.md](README.md)

---

## 환경 구성

### 개발 장비 vs 실행 장비

| 역할 | 장비 |
|------|------|
| 코드 작성 / Git | 로컬 개발 장비 |
| DevSpace 실행 / K8s 조작 | `100.123.80.48` (K8s VM 호스트) |

DevSpace와 bori 어댑터는 **`100.123.80.48`에서 실행**합니다.

---

## DevSpace 설치 (`100.123.80.48` 에서 실행)

### 1. DevSpace CLI 설치

```bash
curl -sSL https://github.com/loft-sh/devspace/releases/latest/download/devspace-linux-amd64 \
  -o /usr/local/bin/devspace
chmod +x /usr/local/bin/devspace
devspace version
```

### 2. 설치 확인

```bash
devspace version
# DevSpace version: v6.x.x
```

### 3. kubectl 연결 확인

```bash
kubectl cluster-info
kubectl get nodes
```

---

## bori 설치 (`100.123.80.48` 에서 실행)

### 1. 저장소 클론

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
```

### 2. 어댑터 바이너리 빌드

```bash
go build -o bin/bori-devspace ./adapters/devspace
```

### 3. slint-gate 설치 확인

bori 어댑터는 `slint-gate` 바이너리를 PATH에서 찾습니다.

```bash
# kube-slint 저장소에서 빌드
cd ../kube-slint
go build -o /usr/local/bin/slint-gate ./cmd/slint-gate
slint-gate --help
```

---

## 앱 등록 방법 (self-registration)

각 dataplane 앱 저장소에 `.bori/` 디렉토리를 추가합니다.
bori는 이 디렉토리를 자동으로 발견합니다. bori 자체는 수정할 필요 없습니다.

### `.bori/component.yaml`

```yaml
name: my-app            # 앱 이름 (Kubernetes service 이름과 일치해야 함)
port: 8080              # 앱 포트
metrics_path: /metrics  # Prometheus metrics 경로 (기본값: /metrics)
namespace: my-ns        # Kubernetes namespace
```

### `.bori/policy.<profile>.yaml`

slint-gate policy 포맷을 그대로 사용합니다.

```yaml
thresholds:
  - name: "requests processed"
    metric: my_app_requests_total   # /metrics 에서 노출되는 메트릭 이름
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

프로파일별로 별도 파일을 만듭니다:

| 파일 | 환경 |
|------|------|
| `policy.devspace.yaml` | DevSpace inner-loop (주 개발환경) |
| `policy.kind.yaml` | kind cluster |
| `policy.multipass.yaml` | Multipass VM lab |

---

## 사용 방법

### devspace dev 와 함께 사용

```bash
cd bori

# 개발 시작 — DevSpace가 앱을 배포하고 after:deploy hook에서 bori gate 실행
devspace dev --profile devspace
```

### 어댑터 단독 실행

```bash
# 기본 (devspace 프로파일, 10초 대기 후 gate 평가)
./bin/bori-devspace --profile devspace --v

# smoke 커맨드 지정
./bin/bori-devspace \
  --profile devspace \
  --smoke-cmd "kubectl exec -n my-ns deploy/my-app -- /bin/smoke-test" \
  --v

# 앱 디렉토리 명시
./bin/bori-devspace \
  --apps-dir /opt/go/src/github.com/HeaInSeo \
  --profile devspace \
  --v
```

### 어댑터 플래그

| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `--profile` | `devspace` | 프로파일: `devspace`, `kind`, `multipass` |
| `--apps-dir` | bori root의 상위 디렉토리 | 앱 저장소 스캔 대상 디렉토리 |
| `--smoke-cmd` | _(없음)_ | smoke 단계에서 실행할 셸 명령 |
| `--smoke-wait` | `10s` | `--smoke-cmd` 미지정 시 대기 시간 |
| `--out` | `bori-gate-output` | 아티팩트 출력 디렉토리 |
| `--slint-gate` | `slint-gate` | slint-gate 바이너리 경로 |
| `--v` | false | 상세 출력 |

### 출력 예시

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

## 아키텍처

```
앱 저장소 (JUMI, artifact-handoff, tori, sori, ...)
  각각 .bori/component.yaml + .bori/policy.<profile>.yaml 보유
        ↓ bori가 자동 발견
bori/adapters/devspace (Go CLI)
  ① kubectl port-forward → /metrics 수집 (smoke 전)
  ② smoke 실행 (--smoke-cmd 또는 --smoke-wait)
  ③ kubectl port-forward → /metrics 수집 (smoke 후)
  ④ delta → sli-summary.json 생성
  ⑤ slint-gate --measurement-summary ... --policy ... 호출
        ↓
slint-gate (kube-slint 독립 바이너리)
        ↓
gate-summary.json (PASS / FAIL / WARN / NO_GRADE)
        ↓
dev-space observability 페이지 (batch-integration publish)
```

### DevSpace hook 연결

```yaml
# devspace.yaml
hooks:
  - events: ["after:deploy"]
    command: "go"
    args: ["run", "./adapters/devspace", "--profile", "${BORI_PROFILE}", "--v"]
```

### kube-slint와의 관계

bori는 kube-slint를 **Go 라이브러리로 import하지 않습니다.**
`slint-gate` 바이너리를 CLI로 호출하는 방식으로만 연결됩니다.
kube-slint는 bori와 무관하게 독립적으로 사용 가능합니다.

---

## 향후 계획

| 항목 | 상태 |
|------|------|
| DevSpace adapter | 구현 완료 |
| Tilt adapter | 계획 중 |
| kind / multipass 프로파일 | policy 파일 추가로 즉시 지원 |
| 다중 앱 병렬 평가 | 계획 중 |

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
│   │   ├── main.go       # CLI: 앱 discovery → scrape → smoke → gate
│   │   ├── component.go  # .bori/component.yaml 파서
│   │   └── collect.go    # kubectl port-forward + /metrics 스크레이핑
│   └── tilt/             # 향후
├── schema/
│   ├── component.schema.yaml   # .bori/component.yaml 스펙
│   └── policy.schema.yaml      # .bori/policy.<profile>.yaml 스펙
├── example/
│   └── .bori/
│       ├── component.yaml      # 참고 구현체
│       └── policy.yaml
├── devspace.yaml         # DevSpace compose + after:deploy hook
└── docs/
    └── architecture.md
```
