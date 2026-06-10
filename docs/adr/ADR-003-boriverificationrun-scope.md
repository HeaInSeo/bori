# ADR-003 — BoriVerificationRun 범위 제한 + Bori Ingestion API 설계 방향

| 항목 | 내용 |
|------|------|
| **상태** | 결정됨 |
| **등록일** | 2026-06-10 |
| **결정일** | 2026-06-10 |
| **관련 파일** | `apis/bori/v1alpha1/verificationrun_types.go` (Phase 11 예정), `docs/control-plane-roadmap.md` |

---

## 배경

Phase 11에서 `BoriVerificationRun` CRD를 추가하면서 두 가지 설계 결정이 필요했다.

**첫째**, BoriVerificationRun의 범위를 어디까지 허용할지.
Bori가 앞으로 다룰 데이터 종류는 늘어날 수 있다.

```text
배포 검증 결과
데이터플레인 app 건강 상태
network-baseline 측정 결과
네트워크 연결성 체크
SLI/SLO 관측
runtime drift
component liveness
gateway 상태
```

이 모든 것을 `BoriVerificationRun`에 추가하면 CRD 의미가 흐려진다.

**둘째**, 외부 agent(bori verify CLI, CI, network-baseline agent)가 검증 결과를 어떻게 Bori에 제출하는지.
현재 Phase 11 계획에서 `bori verify` CLI는 KUBECONFIG가 있을 때 BoriVerificationRun CR을 직접 생성한다.
이 kubeconfig 기반 접근은 초기 lab 환경에서는 단순하지만, 장기적으로 문제를 일으킨다.

```text
kubeconfig 기반 직접 K8s API 접근의 문제점:
  - 외부 agent마다 cluster 접근 권한 부여 필요
  - 권한 범위 관리 어려움 (ClusterRole 과잉 권한 위험)
  - CI pipeline에 kubeconfig 시크릿 배포 필요
  - Gateway API / gRPC / HTTP 기반 외부 연동 방향과 충돌
  - network-baseline agent, health agent 등 외부 컴포넌트가 늘어날수록 배가됨
```

---

## 결정

### 결정 1 — BoriVerificationRun 범위 제한

**BoriVerificationRun은 특정 BoriRevision에 대한 verification gate 결과 전용으로 제한한다.**

허용:

```text
BoriVerificationRun에 담을 것:
  - provider (kube-slint 등 verification backend)
  - release, environment, revisionId (gate 대상 식별)
  - gateResult: PASS | WARN | FAIL | NO_GRADE
  - promotionDecision: eligible | blocked
  - startedAt, finishedAt
  - measurementSummaryPath, gateSummaryPath (artifact 경로)
```

금지:

```text
BoriVerificationRun에 넣지 않을 것:
  - 지속 health 상태 (degraded, liveness, readiness)
  - network-baseline 측정 결과
  - runtime drift 관측
  - component SLI/SLO 장기 추세
  - 외부 agent의 일반 관측 결과
```

**의미 요약**: BoriVerificationRun은 "이 revision을 배포해도 되는가?"에 답하는 point-in-time gate 기록이다.
지속적인 운영 관측이나 health 집계가 아니다.

---

### 결정 2 — 지속 health 상태는 BoriDataPlane.status.conditions에 요약

데이터플레인 app의 건강 상태는 BoriVerificationRun이 아니라 `BoriDataPlane.status.conditions`에 요약 condition으로 표현한다.

```yaml
status:
  conditions:
    - type: Installed
      status: "True"
    - type: Verified
      status: "True"
    - type: Degraded
      status: "False"
    - type: NetworkHealthy     # 향후 추가 후보
      status: "True"
```

원본 관측 데이터(network-baseline measurement, SLI snapshot 등)는 별도 evidence artifact 또는 specific CR로 관리한다.
BoriDataPlane.status에는 요약된 condition만 올린다.

---

### 결정 3 — BoriObservation (generic observation CR) 설계 금지

`BoriObservation` 또는 이와 유사한 범용 관측 CR은 만들지 않는다.

이유: generic 관측 CR은 새로운 junk drawer가 될 위험이 높다.
BoriVerificationRun이 빠져나간 문제가 BoriObservation에 그대로 다시 쌓인다.

대신:

```text
concrete use case가 준비됐을 때 그 목적에 맞는 이름의 CR을 별도로 설계한다.
예:
  network-baseline agent 결과 제출이 실제로 필요해지면 → BoriNetworkObservation 또는 BoriConnectivityCheck
  health report 제출이 실제로 필요해지면 → BoriHealthReport
```

use case 없이 선행 설계하지 않는다.

---

### 결정 4 — kubeconfig 기반 CR 생성은 lab fallback 전용

Phase 11에서 `bori verify` CLI가 KUBECONFIG 환경 변수가 있을 때 BoriVerificationRun CR을 직접 생성하는 경로는 **lab/dev 환경 fallback 전용**으로만 허용한다.

구체적인 제약:

```text
- 문서에 "lab fallback" 또는 "개발용 경로"로 명시한다.
- 이 경로를 공식 통합 가이드에 권장 방법으로 쓰지 않는다.
- CI pipeline에서 이 경로를 기본 설정으로 안내하지 않는다.
- 장기적으로 이 경로는 Bori Ingestion API(결정 5)로 대체한다.
```

---

### 결정 5 — 장기 공식 입력 경로: Gateway API + Bori Ingestion API (Phase 12 후보)

외부 agent가 Bori에 결과를 제출하는 장기 공식 경로는 **Gateway API 뒤의 Bori Ingestion API**로 설계한다.

```text
bori verify CLI / network-baseline agent / health agent / CI pipeline
  ↓
HTTP 또는 gRPC
  ↓
Gateway API
  ↓
bori-ingest service
  ↓
Bori internal controller → CR 생성
  ↓
BoriDataPlane status aggregation
```

이 구조에서 외부 agent는 kubeconfig 없이 Bori Ingestion API만 호출한다.

API 후보 엔드포인트:

```text
POST /v1/verification-runs     — bori verify 결과 제출
POST /v1/network-observations  — network-baseline agent 결과 (향후)
POST /v1/health-reports        — health agent 결과 (향후)
```

Phase 12 구현 범위는 별도로 결정한다. 이 ADR은 방향만 고정한다.

---

### 결정 6 — network-baseline / health 통합은 실제 agent 결과 형식이 준비된 뒤 설계

network-baseline 통합과 data plane health observation 통합은 아직 설계하지 않는다.

조건:

```text
다음이 모두 충족됐을 때 설계를 시작한다:
  1. network-baseline agent의 결과 형식이 확정됨
  2. Bori Ingestion API 설계가 시작됨 (Phase 12)
  3. "이 결과를 어떤 BoriDataPlane condition에 반영할 것인지" use case가 명확함
```

그 전까지는 disk artifact와 CLI 결과 출력으로 운영한다.

---

## 대안으로 고려했던 방향

### 대안 A — BoriVerificationRun을 확장 가능한 범용 CR로 설계

예:

```yaml
kind: BoriVerificationRun
spec:
  category: gate | health | network | observation   # 유형 확장
  payload: ...                                      # 유형별 비정형 데이터
```

**기각 이유**: 범용 container CR은 스키마 검증이 무력화되고, controller 로직이 category별 분기로 복잡해진다.
각 목적에 맞는 별도 CRD가 설계를 더 명확하게 유지한다.

### 대안 B — 검증 결과를 BoriRevision.status에 인라인으로 포함

```yaml
kind: BoriRevision
status:
  verificationResult:
    gateResult: PASS
    provider: kube-slint
    ...
```

**기각 이유**: BoriRevision은 write-once 이력 리소스다. 검증이 배포 이후에 별도로 실행되므로 status를 두 번 패치해야 한다.
또한 복수의 verification run(policy A, policy B)을 하나의 BoriRevision에 인라인으로 표현하면 구조가 복잡해진다.

---

## 이 결정이 Phase 11에 미치는 영향

| 항목 | 변경 전 | 변경 후 |
|------|---------|---------|
| BoriVerificationRun 범위 | 정의만 있었음 | release gate 결과 전용으로 명시 |
| kubeconfig 경로 | 선택적 기능 | lab fallback으로 제한, 문서에 명시 |
| BoriObservation | 언급 없음 | 만들지 않기로 결정 |
| 장기 입력 경로 | 미정 | Phase 12 Ingestion API로 고정 |
| network-baseline 통합 | 미정 | use case 준비 후 specific CR로 별도 설계 |

---

## 참조

- [Phase 11 계획](../control-plane-roadmap.md#phase-11--boriverificationrun-crd)
- [Phase 12 계획](../control-plane-roadmap.md#phase-12--bori-ingestion-api-후보) (Phase 12 후보)
- ADR-001 — BoriRevision.failReason 위치
- ADR-002 — controller-gen 도입
