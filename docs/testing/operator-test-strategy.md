# Operator Test Strategy

## 계층 구조

```
┌─────────────────────────────────────────────────────────────────┐
│  Layer 3: VM Integration                                         │
│  hack/test-vm-integration.sh                                     │
│  Remote: seoy@100.123.80.48                                      │
│  공식 churn baseline / SLI sli-summary.json 기준                 │
├─────────────────────────────────────────────────────────────────┤
│  Layer 2-K1: kind Functional Smoke          (다음 PR)            │
│  hack/test-kind-functional-smoke.sh                              │
│  ConfigMap release fixture 주입 → BoriRevision 생성 확인        │
├─────────────────────────────────────────────────────────────────┤
│  Layer 2-K0: kind Boot Smoke                (현재 구현)          │
│  hack/test-kind-boot-smoke.sh                                    │
│  operator 기동 + /metrics + conditions 기록                      │
├─────────────────────────────────────────────────────────────────┤
│  Layer 1: Fake Unit Tests                                        │
│  make test  (GOPROXY=off go test ./...)                          │
│  항상 실행, CI에서 기본 gate                                     │
└─────────────────────────────────────────────────────────────────┘
```

---

## 계층별 책임

### Layer 1 — Fake Unit Tests

- **위치**: `controllers/*_test.go`, `pkg/**/*_test.go`
- **실행**: `make test`
- **의존성**: 없음 (GOPROXY=off, 네트워크 차단)
- **검증 대상**:
  - Reconciler 분기 로직 (status patch, 이벤트 발송 경로)
  - helper 함수 (revisionToCR, shadow, security, release 파싱)
  - CRD 타입 DeepCopy

---

### Layer 2-K0 — kind Boot Smoke ✅ 현재 구현

- **위치**: `test/e2e/kind_smoke_test.go` (`//go:build kind`)
- **실행**: `hack/test-kind-boot-smoke.sh [--keep]`
- **목적**: GitHub-hosted runner kind 환경에서 operator가 기동하는지 확인
- **검증 대상**:
  - CRD 설치 및 API 서버 인식
  - Operator pod 기동 및 readiness
  - BoriDataPlane sample 적용 → reconcile 발생 확인
  - BoriDataPlane.status.conditions 설정 (ReleaseNotFound 등)
  - BoriRelease.status.activeDataPlanes 카운트
  - `/metrics` 엔드포인트 접근 가능 여부
  - kube-slint SLI snapshot (summary artifact, hard fail 없음)

> **참고**: emptyDir 방식이므로 operator는 release 파일을 찾지 못하고
> `ReleaseNotFound` 조건을 기록한다. 이는 예상된 동작이며 K0 smoke가 검증하는
> 핵심 중 하나다.

---

### Layer 2-K1 — kind Functional Smoke ✅ 현재 구현

- **위치**: `test/e2e/kind_functional_smoke_test.go` (`//go:build kindfunc`)
- **실행**: `hack/test-kind-functional-smoke.sh [--keep]`
- **목적**: bori-root를 ConfigMap으로 주입, shell adapter no-op deploy → full reconcile cycle 확인
- **검증 대상**:
  - `bori-func-config` ConfigMap → init container → `/bori-config/{environments,components}/` 구성
  - `bori-deploy-scripts` ConfigMap → init container → `/apps/jumi/deploy.sh` 구성
  - Runner.Run() 완료 → `BoriDataPlane.status.observedGeneration >= 1`
  - shadow reconcile → `conditions.Installed = True`
  - `upsertBoriRevision` → BoriRevision CR 생성 확인
  - BoriRelease.status.activeDataPlanes >= 1
  - kube-slint SLI measurement (BeforeSuite/AfterSuite 패턴)

**구현 결정:**
- bori-root 주입: ConfigMap + busybox init container (파일 시스템 구조 구성)
- 컴포넌트 adapter: shell (no-op `exit 0` 스크립트)
- 테스트 프레임워크: Ginkgo/Gomega (TD-001 이행)

---

### Layer 3 — VM Integration

- **위치**: `hack/test-vm-integration.sh`
- **실행**: `./hack/test-vm-integration.sh [--update-baseline]`
- **원격 대상**: `seoy@100.123.80.48` (Tailscale, SSH)
- **검증 대상**:
  - conditions 회귀 비교 (testdata/baseline/)
  - BoriRevision 생성 및 provenance 확인
  - BoriRelease cross-watch activeDataPlanes churn 검증
  - kube-slint SLI 측정 — **공식 churn baseline은 이 환경 기준**
  - sli-summary.json artifact 생성

---

## hostPath 정책

| 환경 | bori-repo 볼륨 | 비고 |
|------|--------------|------|
| **VM integration** | `hostPath` 허용 | `/opt/go/src/github.com/HeaInSeo/bori` |
| **kind K0 boot smoke** | `emptyDir` | release 파일 없음 → ReleaseNotFound 조건 |
| **kind K1 functional smoke** | ConfigMap/projected volume | 최소 release fixture 주입 |
| **production** | ConfigMap/Secret/OCI/registry | hostPath 사용 금지 |

hostPath는 VM 로컬 개발 루프에서만 허용한다.
CI(kind)와 production에서는 명시적 volume source를 사용한다.

---

## CI 워크플로우 구조

| Workflow | 계층 | 트리거 | Runner |
|---------|------|--------|--------|
| `ci.yml` | Layer 1 | PR / main push | ubuntu-latest |
| `generate-check.yaml` | CRD/DeepCopy drift | apis/** config/** 등 변경 | ubuntu-latest |
| `golangci-lint.yaml` | 정적 분석 | `**/*.go` 변경 | ubuntu-latest |
| `kubeconform.yaml` | manifest 검증 | `config/**` 변경 | ubuntu-latest |
| `kubelint.yaml` | kube-linter | `config/**` 변경 | ubuntu-latest |
| `kind-boot-smoke.yml` | Layer 2-K0 | workflow_dispatch + paths | ubuntu-latest |
| `kind-functional-smoke.yml` | Layer 2-K1 | workflow_dispatch + paths | ubuntu-latest |
| `vm-integration.yml` | Layer 3 | nightly + workflow_dispatch + main push | self-hosted, bori-vm |

**책임 분리 원칙:**
- `ci.yml`: `go test + go build` — PR 기본 gate, 빠른 깨짐 감지
- `generate-check.yaml`: controller-gen drift 검사 — `ci.yml`에 중복하지 않음
- kind-boot-smoke: operator boot 구조 회귀 감지
- vm-integration: 실제 운영 환경 기준선

---

## kube-slint 연동 방침

| 항목 | K0 Boot | K1 Functional | VM Integration |
|------|---------|---------------|----------------|
| SLI 측정 | O (BeforeSuite/AfterSuite) | O (BeforeSuite/AfterSuite) | O |
| sli-summary.json | O (artifacts/kind/) | O (artifacts/kind-func/) | O (공식 baseline) |
| gate 평가 | summary-only | summary-only | summary-only |
| hard fail on FAIL | X | X (현재) | X (현재) |
| baseline 비교 | X | X | 추후 단계 |

**4단계 kube-slint 도입:**
1. `sli-summary.json` artifact만 생성 ← **현재 단계**
2. 여러 번 baseline 수집
3. 명백한 폭주만 soft warning
4. regression gate → 안정 후 hard fail 허용

kube-slint는 operator 프로덕션 코드(`cmd/`, `controllers/`, `pkg/`)에 포함하지 않는다.
`test/e2e/` 패키지의 `//go:build kind` 파일에만 import한다.

---

## envtest 방침

현재 단계에서 envtest(controller-runtime/pkg/envtest)는 도입하지 않는다.
Layer 1(fake client)과 Layer 2(kind)가 각 역할을 충분히 담당한다.
도입 시 별도 ADR을 작성한다.

---

## 기술 부채

### TD-001 — e2e 테스트 프레임워크: 표준 testing → Ginkgo/Gomega 전환 필요

| 항목 | 내용 |
|------|------|
| **현재 상태** | `test/e2e/` 전체가 표준 `testing` 패키지 사용 |
| **발생 이유** | K0 boot smoke 단계에서 빠르게 구현하면서 표준 testing으로 작성 |
| **부채 내용** | kube-slint SLI 측정은 Ginkgo/Gomega 패턴에서 사용하는 것이 바람직하다. 현재 표준 testing 기반 구현은 임시 상태다. |

**전환 방향:**
- `test/e2e/` 전체를 Ginkgo/Gomega 기반으로 재작성
- kube-slint `sess.Start()` / `sess.End()` 를 `BeforeSuite` / `AfterSuite` 또는 `BeforeEach` / `AfterEach`에 배치
- `Describe` / `It` 구조로 K0/K1 시나리오를 명확히 구분
- `--focus` / `--skip` 으로 선택적 시나리오 실행 가능

**전환 완료 (이 PR):**
K0 (`kind_smoke_test.go`)와 K1 (`kind_functional_smoke_test.go`) 모두 Ginkgo/Gomega로 작성됐다.
`BeforeSuite`/`AfterSuite`에서 `sess.Start()`/`sess.End()` 호출 — TD-001 해소.
