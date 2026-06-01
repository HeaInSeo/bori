# bori Verification Model

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## 개요

bori의 verification은 단순한 smoke/gate 실행이 아니라 source 기반 모델로 확장된다.

```text
bori verify
  -> verification policy 해석
  -> provider: kube-slint 선택
  -> source에 따라 measurement 수집
  -> gate result 수집
  -> BoriVerificationRun 저장
  -> promotion decision
```

---

## Gate Result 정의

```text
PASS:     측정 성공 + policy 통과
WARN:     측정 성공 + 경고 수준 위반
FAIL:     측정 성공 + policy 위반
NO_GRADE: 측정 실패 또는 데이터 부족
```

---

## Severity Order

```text
PASS < WARN < NO_GRADE < FAIL
```

두 신호(engine status, gate policy result)가 충돌할 때 더 심각한 쪽을 최종 결과로 선택한다.

### Engine Status → Gate Result 매핑

```text
engine status:
  pass        -> PASS
  warn        -> WARN
  skip        -> NO_GRADE
  fail/block  -> FAIL

final gate result:
  max(engine-derived result, policy-derived result)
```

예시:
- engine `skip` + policy threshold 없음 → `NO_GRADE` (PASS가 아님)
- engine `pass` + baseline regression FAIL → `FAIL`

---

## NO_GRADE 정책

측정에 실패한 상태를 PASS로 보면 안 된다.

```text
local / dev:
  NO_GRADE = WARN 또는 non-blocking

shared dev / integration:
  NO_GRADE = FAIL_OR_NOGRADE 선택 가능

promotion / release gate:
  NO_GRADE = blocking (기본값)
```

---

## BoriVerificationPolicy 구조

```yaml
apiVersion: bori.dev/v1alpha1
kind: BoriVerificationPolicy
metadata:
  name: jumi-ah-smoke
spec:
  provider: kube-slint
  mode: cli
  measurementSummary: artifacts/sli-summary.json
  policy: .slint/policy.yaml
  baseline: verification/baselines/jumi-ah-sli-summary.json
  failOn: FAIL_OR_NOGRADE
  artifacts:
    gateSummary: artifacts/slint-gate-summary.json
```

### failOn 옵션

```text
FAIL:           FAIL일 때만 blocking
FAIL_OR_NOGRADE: FAIL 또는 NO_GRADE일 때 blocking (promotion gate 권장)
WARN:           WARN 이상이면 blocking (엄격한 gate용)
NEVER:          bori가 JSON 결과를 읽고 직접 판정 (권장 방식)
```

---

## Verification Source 분류

### Group 1: 현재 kube-slint 2점 engine으로 가능한 source

```text
point_scrape:
  before/after /metrics 수집
  port-forward 또는 curlpod 기반
  현재 bori-devspace 방식

kube_slint_summary:
  외부에서 생성된 sli-summary.json을 slint-gate로 평가
  bori가 정렬해야 할 공식 경로

baseline_compare:
  이전 revision의 summary 또는 baseline과 현재 summary 비교
  regression gate에 사용

metric_based_churn:
  JUMI/spawner가 노출하는 metric을 before/after로 측정

k8s_object_snapshot_mvp:
  Kubernetes object를 before/after로 list
  object metadata를 scalar SLI metric으로 환원
  raw object list는 evidence로 보존
```

### Group 2: kube-slint engine 확장이 필요한 source

```text
promql_query:
  운영 Prometheus/Thanos/Mimir에서 PromQL로 측정
  query range, window, step 개념 필요

soak_analysis_run:
  배포 후 N분 동안 지속 관측
  burn rate, error ratio, latency regression 확인

progressive_rollout_analysis:
  traffic 5% -> 25% -> 50% -> 100% 단계별 verification

windowed_quantile:
  p95/p99 over time window 계산
```

Group 2는 bori 문서에서는 수용 가능하게 설계하되, kube-slint의 별도 엔진 확장 트랙으로 진행한다.

---

## slint-gate 호출 방식

### 권장 방식: --fail-on NEVER + bori-side decision

```text
bori
  -> slint-gate --fail-on NEVER --output slint-gate-summary.json
  -> slint-gate-summary.json 읽기
  -> VerificationPolicy.failOn 기준으로 bori가 promotionDecision 계산
```

이유:
- profile마다 NO_GRADE 처리 기준이 다르다.
- slint-gate exit code에만 의존하면 bori run artifact 생성이 흐트러질 수 있다.
- bori는 BoriVerificationRun을 반드시 남겨야 한다.

### 대안 방식: slint-gate exit code + JSON 모두 수집

```text
bori
  -> slint-gate --fail-on <VerificationPolicy.failOn>
  -> exit code + JSON 결과 모두 수집
```

이 방식에서도 실패 시 JSON 결과를 반드시 보존해야 한다.

---

## BoriVerificationRun 구조

```json
{
  "schemaVersion": "bori.verificationRun.v1",
  "runId": "20260601-001",
  "release": "jumi-ah-dev",
  "environment": "kind",
  "provider": "kube-slint",
  "measurementSummaryPath": "artifacts/sli-summary.json",
  "gateSummaryPath": "artifacts/slint-gate-summary.json",
  "gateResult": "PASS",
  "promotionDecision": "eligible",
  "startedAt": "...",
  "finishedAt": "..."
}
```

---

## Baseline과 BoriRevision의 관계

baseline은 임의로 관리되는 별도 파일이 아니다. promoted revision의 verification evidence에서 파생된다.

```text
BoriRevision 생성
  -> deploy / verify 실행
  -> kube-slint sli-summary.json 생성
  -> slint-gate PASS
  -> bori promotion decision = promoted
  -> 해당 revision의 sli-summary.json이 다음 비교의 baseline 후보가 됨
```

bori가 소유하는 것은 baseline의 출처와 승격 이력이다.

```text
bori가 기록해야 할 것:
  - baseline id
  - source revision id
  - source run id
  - image digest
  - config digest
  - verification policy digest
  - summary artifact path
  - promotedAt
```

baseline 자체의 JSON schema는 kube-slint가 소유한다. bori는 schema를 재정의하지 않는다.

---

## 현재 bori의 Prometheus parser 위치

현재 `parsePromText` / `buildDeltaSummary`는 임시 compatibility shim이다.

```text
bori의 parsePromText/buildDeltaSummary:
  - 임시 shim으로 유지 가능
  - 신규 기능의 중심으로 삼지 않는다
  - kube-slint backend 정렬 후 축소/삭제 후보

이유:
  - Prometheus format 파싱에는 label canonicalization,
    histogram/summary, timestamp, type 정보가 필요하다.
  - 운영 관측은 rate / ratio / quantile / burn rate가 필요하다.
  - bori가 metric parser와 SLI engine을 소유하면 책임 범위가 너무 커진다.
```

---

## k8s_object_snapshot MVP 범위

JUMI churn gate를 위한 source. MVP에서는 별도 engine 경로를 만들지 않는다.

```text
scalar metric path:
  map[string]float64
  기존 kube-slint 2점 engine 재사용

evidence path:
  raw Kubernetes object metadata JSON
  orphan / stuck / ownerRef missing object 목록
  debugging / audit / run archive에서 사용
```

MVP SLI 예시:

```text
jumi_k8s_jobs_created_delta
jumi_k8s_pods_created_delta
jumi_k8s_objects_remaining_end
jumi_k8s_orphan_objects_end
jumi_k8s_stuck_terminating_end
jumi_k8s_ownerref_missing_end
```

---

## 참고 문서

- [architecture.md](architecture.md)
- [kube-slint-integration.md](kube-slint-integration.md)
- [security-model.md](security-model.md)
- [jumi-churn-gate.md](jumi-churn-gate.md) (Phase 3.5에서 작성 예정)
