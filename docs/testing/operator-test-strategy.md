# Operator Test Strategy

## 계층 구조

```
┌─────────────────────────────────────────────────────┐
│  Layer 3: VM Integration                             │
│  hack/test-vm-integration.sh                         │
│  Remote: seoy@100.123.80.48                          │
│  공식 churn baseline / SLI sli-summary.json 기준     │
├─────────────────────────────────────────────────────┤
│  Layer 2: kind Smoke                                 │
│  hack/test-kind-smoke.sh                             │
│  Local kind cluster (bori-smoke)                     │
│  PR / 로컬 사전 검증                                 │
├─────────────────────────────────────────────────────┤
│  Layer 1: Fake Unit Tests                            │
│  make test  (GOPROXY=off go test ./...)              │
│  controller-runtime fake client                      │
│  항상 실행, CI에서 기본 gate                         │
└─────────────────────────────────────────────────────┘
```

## 계층별 책임

### Layer 1 — Fake Unit Tests

- **위치**: `controllers/*_test.go`, `pkg/**/*_test.go`
- **실행**: `make test`
- **의존성**: 없음 (GOPROXY=off, 네트워크 차단)
- **검증 대상**:
  - Reconciler 분기 로직 (status patch, 이벤트 발송 경로)
  - helper 함수 (revisionToCR, shadow, security, release 파싱)
  - CRD 타입 DeepCopy (zz_generated.deepcopy.go)
- **검증하지 않는 것**: 실제 API 서버 동작, CRD 설치, /metrics 응답

### Layer 2 — kind Smoke

- **위치**: `test/e2e/kind_smoke_test.go` (`//go:build kind`)
- **실행**: `hack/test-kind-smoke.sh [--keep]`
- **목적**: PR merge 전 구조적 회귀를 빠르게 탐지
- **검증 대상**:
  - CRD 설치 및 API 서버 인식
  - Operator pod 기동 및 readiness
  - BoriRelease + BoriDataPlane sample → reconcile 발생 확인
  - BoriDataPlane.status.conditions 설정 (NoPromotedRevision 등)
  - BoriRelease.status.activeDataPlanes 카운트
  - `/metrics` 엔드포인트 접근 가능 여부
  - kube-slint SLI 측정 (controller-runtime 기본 메트릭) — **summary-only, hard fail 없음**
- **검증하지 않는 것**:
  - 실제 이미지 배포 (digest-based deploy)
  - network verification, promote
  - BoriRevision provenance 체인
  - infra-lab 환경 의존 시나리오

#### kind Smoke — Artifact 수집 (실패 시 자동)

```
test/e2e/artifacts/
  operator-logs.txt
  events.txt
  boridataplanes.yaml
  borireleases.yaml
  borirevisions.yaml
  metrics-raw.txt
  sli-summary.json          # kube-slint 측정 결과 (항상 생성)
```

#### kind Baseline 원칙

- kind smoke에는 churn baseline을 적용하지 않는다.
- sli-summary.json은 생성하지만 gate 평가(FAIL/WARN)를 hard fail로 연결하지 않는다.
- 이 계층의 SLI 값은 참고용이다.

---

### Layer 3 — VM Integration

- **위치**: `hack/test-vm-integration.sh`
- **실행**: `./hack/test-vm-integration.sh [--update-baseline]`
- **원격 대상**: `seoy@100.123.80.48` (Tailscale, SSH)
- **목적**: 실제 운영과 동등한 환경에서 전 주기 검증
- **검증 대상**:
  - `scripts/regression-check.sh`와 동일한 BoriDataPlane conditions 회귀 체크
  - digest-based deploy (imageDigest 필드 검증)
  - BoriRevision 생성 및 provenance 확인
  - BoriRelease cross-watch — activeDataPlanes churn 기준 검증
  - kube-slint SLI 측정 — **공식 churn baseline은 이 환경 기준**
  - sli-summary.json artifact 생성
- **검증하지 않는 것 (현재)**:
  - network verification end-to-end (Phase 미도달)
  - promote 자동화 (수동 프로세스)

#### VM Integration — Artifact 수집 (항상)

```
test/e2e/artifacts/vm/
  conditions-snapshot.json
  operator-logs.txt
  events.txt
  boridataplanes.yaml
  borirevisions.yaml
  sli-summary.json          # 공식 churn 기준 (VM baseline)
  slint-gate-summary.json   # gate 평가 결과 (summary-only)
```

#### VM Baseline 원칙

- `testdata/baseline/infra-lab-smoke-conditions.json`: conditions 회귀 기준선
- `test/e2e/artifacts/vm/sli-summary.json`: kube-slint SLI 기준선 (추후 gate 연동)
- baseline 갱신: `./hack/test-vm-integration.sh --update-baseline`
- **kind와 VM의 baseline을 섞지 않는다.**

---

## kube-slint 연동 방침

| 항목 | kind Smoke | VM Integration |
|------|-----------|----------------|
| SLI 측정 | O | O |
| sli-summary.json | O (artifacts/) | O (artifacts/vm/) |
| gate 평가 | summary-only | summary-only |
| hard fail on FAIL | X | X (현재 단계) |
| baseline 비교 | X | 추후 단계 |

> **현재 단계**: kube-slint는 관찰 및 artifact 생성 목적으로만 사용한다.
> gate → hard fail 연결은 SLI baseline이 충분히 안정화된 후 별도 ADR에서 결정한다.

kube-slint는 operator 프로덕션 코드(`cmd/`, `controllers/`, `pkg/`)에 포함하지 않는다.
의존성은 `test/e2e/` 패키지(`//go:build kind` 또는 `//go:build vmintegration`)에만 존재한다.

---

## 스크립트 실행 방법

### kind Smoke 로컬 실행

```bash
# 전제: kind, docker, kubectl, go 설치됨

# 기본 실행 (클러스터 자동 삭제)
./hack/test-kind-smoke.sh

# 디버그 모드 — 클러스터 유지
./hack/test-kind-smoke.sh --keep

# 직접 Go 테스트 실행 (클러스터가 이미 있을 때)
KUBECONFIG=$(kind get kubeconfig-path --name bori-smoke) \
  SLINT_SA_TOKEN=$(kubectl --context kind-bori-smoke \
    -n bori-system create token kube-slint --duration=1h) \
  go test -tags kind -v -timeout 300s ./test/e2e/
```

### VM Integration 실행

```bash
# 기본 실행 (SSH → seoy@100.123.80.48)
./hack/test-vm-integration.sh

# baseline 갱신
./hack/test-vm-integration.sh --update-baseline
```

---

## CI 현황

| Workflow | 계층 | 트리거 |
|---------|------|--------|
| `golangci-lint.yaml` | Layer 1 정적 분석 | `**/*.go` push/PR |
| `generate-check.yaml` | CRD/DeepCopy drift 검출 | `apis/**` push/PR |
| `kubeconform.yaml` | manifest 구조 검증 | `config/**` push/PR |
| `kubelint.yaml` | kube-linter | `config/**` push/PR |
| kind smoke | Layer 2 (미연동) | 수동 또는 Tailscale action 연동 후 |
| VM integration | Layer 3 (미연동) | 수동 또는 Tailscale action 연동 후 |

> kind smoke CI 연동은 Tailscale GitHub Action + self-hosted runner 구성 후 별도 추가한다.

---

## envtest 방침

현재 단계에서 envtest(controller-runtime/pkg/envtest)는 도입하지 않는다.

- Layer 1(fake client)이 Reconciler 로직을 충분히 커버한다.
- Layer 2(kind)가 실제 API 서버 동작을 검증한다.
- envtest는 두 계층의 중간 공간을 채우는 선택지로 남겨둔다.
  도입 시 별도 ADR을 작성한다.
