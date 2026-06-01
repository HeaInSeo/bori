# bori × kube-slint 통합 가이드

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## 결론

kube-slint는 bori 내부로 흡수하지 않는다. kube-slint는 bori의 공식 verification backend 중 하나다.

```text
bori verify
  -> verification policy 해석
  -> provider: kube-slint 선택
  -> kube-slint summary 또는 slint-gate 실행
  -> gate result 수집
  -> BoriVerificationRun 저장
  -> promotion 가능 여부 판단
```

---

## 역할 분리

```text
bori:
  - source 선택 / 정책 연결
  - promotion decision
  - artifact 보존
  - BoriVerificationRun wrapping

kube-slint:
  - SLI measurement summary 생성
  - policy / baseline / regression / reliability 평가
  - slint-gate-summary.json 생성
  - summary schema 소유 (단일 출처)
```

---

## Summary Schema 소유권

bori는 kube-slint summary 구조를 수동 복제하지 않는다.

```text
summary schema owner: kube-slint

bori:
  kube-slint summary를 생성하거나 소비할 수는 있지만,
  schema 정의의 단일 출처가 되면 안 된다.
```

### Schema 공유 방식

초기에는 방식 B로 시작한다. schema drift가 반복되면 방식 A를 검토한다.

```text
방식 A: Go library import
  bori가 github.com/.../kube-slint/pkg/slo/summary를 import한다.

방식 B: CLI contract (초기)
  bori는 kube-slint가 문서화한 summary schemaVersion만 생성한다.
  slint-gate는 입력 schemaVersion을 검증한다.
```

---

## kube-slint가 제공하는 기반

bori가 자체 구현하면 안 되는 영역이다.

```text
- metric key canonicalization
- MetricsFetcher / SnapshotFetcher 계열 추상화
- Reliability 진단 필드
- baseline comparison
- slint-gate CLI
- SLI spec registry
- JUMI/AH 관련 일부 SLI spec
```

---

## slint-gate 호출 방식

### 권장: --fail-on NEVER + bori-side JSON 판정

```bash
slint-gate \
  --measurement-summary sli-summary.json \
  --policy .slint/policy.yaml \
  --output slint-gate-summary.json \
  --fail-on NEVER
```

bori가 `slint-gate-summary.json`을 읽고 `VerificationPolicy.failOn` 기준으로 promotionDecision을 계산한다.

장점:
- profile마다 NO_GRADE 처리 기준을 bori가 직접 제어한다.
- slint-gate exit code에 무관하게 run artifact를 항상 생성한다.
- BoriVerificationRun JSON을 안정적으로 남길 수 있다.

### 현재 코드 문제

```go
// pkg/adapter/gate_runner.go
// --fail-on FAIL 고정 호출 → NO_GRADE를 조용히 통과시킬 위험
cmd := exec.CommandContext(ctx, "slint-gate",
    "--measurement-summary", summaryPath,
    "--policy", req.PolicyPath,
    "--output", gatePath,
    "--fail-on", "FAIL",  // 이 값은 VerificationPolicy에서 주입해야 함
)
```

Phase 1.5에서 `VerificationPolicy.failOn` 주입 또는 `--fail-on NEVER` + bori-side 판정으로 전환한다.

---

## kube-slint Track 요약

kube-slint repo에서 진행할 보완 작업이다.

### K0 — schemaVersion strictness (06-03~06-10)

```text
목표: slint-gate가 잘못된 summary schema를 조용히 통과시키지 않게 한다.

작업:
  - summary.SchemaVersion 상수 공개
  - summary.Validate() 또는 ValidateSchemaVersion() 추가
  - loadMeasurement 단계에서 schemaVersion 검증
  - unknown/empty schemaVersion => NO_GRADE
  - 테스트 추가

완료 기준:
  - schemaVersion 누락 시 NO_GRADE
  - 알 수 없는 schemaVersion 시 NO_GRADE
  - gate summary에 reason이 남음
```

### K1 — SLIResult.Status propagation (06-11~06-21)

```text
목표: SLIResult.Status를 gate_result에 반영하고, engine status와 gate policy 결과의 우선순위 규칙을 구현한다.

Severity order: PASS < WARN < NO_GRADE < FAIL

engine status mapping:
  pass        -> PASS
  warn        -> WARN
  skip        -> NO_GRADE
  fail/block  -> FAIL

final gate result: max(engine-derived result, policy-derived result)

완료 기준:
  - Status=skip인 결과가 gate NO_GRADE로 반영된다.
  - Status=fail인 결과가 gate FAIL로 반영된다.
  - policy 결과와 engine status가 충돌하면 더 심각한 결과가 최종이 된다.
```

### K2 — Counter reset policy (K1에 포함)

```text
목표: ComputeDelta의 counter reset 처리를 spec/profile별로 조정 가능하게 한다.

정책 후보:
  warn      기존 기본값
  no_grade  promotion gate용 (JUMI churn gate 권장)
  fail      강한 gate용
  skip      측정 제외

JUMI churn gate 권장:
  onCounterReset: no_grade
```

### K3 — curlpod security and cleanup (07-01~07-14)

```text
목표: curlpod fetcher가 만드는 임시 Pod의 권한, label, cleanup을 명확히 한다.

작업:
  - curlpod에 run-id label 추가
  - cleanup 실패 시 warning/evidence 기록
  - 최소 RBAC 문서화
  - verification helper resource를 churn 측정에서 제외할 수 있게 label 표준화
```

### K4 — Evidence redaction (07-01~07-14)

```text
목표: evidence artifact에 secret/token/password가 남지 않게 한다.

작업:
  - RedactString / RedactMap utility 추가
  - Authorization, token, password, secret, key 패턴 마스킹
  - evidence copy helper 또는 writer에서 redaction 적용
  - 테스트 추가
```

### K5 — k8s_object_snapshot MVP (08-01~08-21)

```text
목표: JUMI object churn을 metric 외부에서도 관측한다.

MVP 결정:
  새로운 engine 경로를 만들지 않는다.
  MetricsFetcher-compatible output을 유지한다.
  object list/diff 결과를 map[string]float64로 환원한다.
  raw object list는 evidence/*.json으로 저장한다.

완료 기준:
  - before/after object snapshot이 가능하다.
  - created/remaining/orphan/stuck/ownerRefMissing count가 metric으로 나온다.
  - selector로 verification helper resource를 제외할 수 있다.
```

---

## Phase와 Track 의존성

| bori Phase | 의존 Track | Fallback |
|-----------|-----------|---------|
| Phase 1.5 | K0 | K0 지연 시: `--fail-on NEVER` + bori-side JSON 판정 먼저 구현 |
| Phase 1.5 | K1 | K1 지연 시: gate summary의 top-level gate_result 우선 사용 |
| Phase 3.5 | K1/K2 | K2 지연 시: counter reset을 NO_GRADE로 취급하는 bori-side 임시 정책 |
| Phase 3.5 | K5 | K5 지연 시: metric 기반 JUMI churn gate만 먼저 완료 |

---

## VerificationPolicy provider 선택 흐름

```text
BoriVerificationPolicy.spec.provider: kube-slint

bori가 kube-slint provider를 선택하면:
  mode: cli         -> slint-gate CLI shell-out
  mode: library     -> go library import (방식 A, 향후)
```

---

## 참고 문서

- [architecture.md](architecture.md)
- [verification-model.md](verification-model.md)
- [control-plane-roadmap.md](control-plane-roadmap.md) §4, §7
