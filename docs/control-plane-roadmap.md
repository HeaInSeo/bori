# bori Control Plane 전환 개발 기획서 v0.8

상태: Draft v0.8  
작성일: 2026-06-01  
최종 업데이트: 2026-06-06  
대상 저장소: `bori`  
관련 프로젝트: `JUMI`, `artifact-handoff`, `node-artifact-runtime(nan)`, `tori`, `kube-slint`

---

## 0. 변경 이력

### v0.8에서 바뀐 점 (2026-06-06)

Phase 9가 완료됐다. `BoriRelease`가 파일시스템 YAML에서 Kubernetes CR로 승격됐다.

v0.8의 핵심 변경점은 다음이다.

1. Phase 9 완료 표시 (2026-06-06).
   - `BoriRelease` CRD 추가 — `releases/<name>/release.yaml`의 Kubernetes CR 버전.
   - operator가 K8s API에서 BoriRelease를 읽고, 없으면 파일시스템 fallback (CLI 호환).
   - `BoriRelease` 변경 시 참조하는 `BoriDataPlane` 자동 enqueue.
   - `bori release apply` CLI 서브커맨드 추가 — 기존 YAML을 CR로 변환.
   - `network-baseline` 저장소 통합 가능성 확인 (별도 트랙으로 관리 예정).

2. Phase 9 산출물 및 완료 기준을 §12(개발 로드맵) 섹션에 추가.

---

### v0.7에서 바뀐 점 (2026-06-05)

Phase 8이 완료됐다. operator를 실제 클러스터에 배포 가능한 수준으로 hardening했다.

v0.7의 핵심 변경점은 다음이다.

1. Phase 8 완료 표시 (2026-06-05).
   - Finalizer(`bori.dev/cleanup`) 구현으로 graceful deletion 보장.
   - Generation-aware reconcile로 불필요한 full reconcile 방지.
   - `ViolationError` 타입 분리로 namespace 위반을 에러 backoff 없이 condition으로 처리.
   - `security.RedactString()` 을 모든 event 메시지에 적용.
   - `config/operator/` 배포 매니페스트(`make deploy`) 추가.

2. Phase 8 산출물 및 완료 기준을 §12(개발 로드맵) 섹션에 추가.

---

### v0.6에서 바뀐 점 (2026-06-05)

Phase 6과 Phase 7이 예상보다 빠르게 완료됐다. 두 Phase의 실제 구현 결과와 완료 기준 달성 여부를 반영했다.

v0.6의 핵심 변경점은 다음이다.

1. Phase 6 완료 표시 (2026-06-03).
   - `pkg/shadow`, `pkg/reconcile`, `bori shadow status`, `bori reconcile` 구현 완료.
   - CRD 등록은 Phase 7로 이관됐으나 Phase 7에서 완료됨.

2. Phase 7 완료 표시 (2026-06-05).
   - `apis/bori/v1alpha1` Kubernetes 타입 확정 (`Condition = metav1.Condition`).
   - `controllers/DataPlaneReconciler` controller-runtime v0.24 기반 구현 완료.
   - `cmd/bori-operator` 엔트리포인트 완료.
   - CLI와 operator가 동일한 `pkg/reconcile.Reconciler.Run()` 경로를 사용하는 것 확인.
   - `config/crd/`, `config/rbac/` YAML 준비 완료.

3. Phase 문서 업데이트 형식 변경.
   - 완료된 Phase는 실제 산출물 목록과 체크된 완료 기준으로 교체.
   - 예상 기간은 취소선으로 표시하고 실제 완료일을 기재.

---

### v0.5에서 바뀐 점 (2026-06-01)

v0.5는 v0.4의 방향을 유지하면서, 구현 직전에 반드시 고정해야 할 의존성·판정 규칙·baseline 흐름·JUMI churn gate MVP 범위를 명확히 한다.

v0.5의 핵심 변경점은 다음이다.

1. Phase와 kube-slint Track 사이의 의존성을 명시한다.
   - Phase 1.5는 Track K0의 schemaVersion strictness에 의존한다.
   - Phase 1.5와 Phase 3.5는 Track K1/K2의 status propagation 및 counter reset policy와 연결된다.
   - Phase 3.5의 `k8s_object_snapshot` 경로는 Track K5의 진행 상황에 영향을 받는다.
   - 단, kube-slint Track이 지연되더라도 bori는 fallback 경로로 먼저 진행할 수 있게 한다.

2. engine status와 gate policy 결과가 충돌할 때의 우선순위를 정의한다.
   - engine status는 measurement reliability 신호다.
   - gate policy result는 threshold/baseline/regression 위반 신호다.
   - 최종 gate result는 더 심각한 쪽을 선택한다.
   - 기본 severity order는 `PASS < WARN < NO_GRADE < FAIL`이다.

3. baseline과 BoriRevision snapshot의 관계를 명확히 한다.
   - baseline은 임의 파일이 아니라, promoted BoriRevision의 verification evidence에서 파생된다.
   - 특정 revision의 `sli-summary.json`이 다음 regression 비교의 baseline 후보가 된다.
   - bori는 어떤 revision이 어떤 baseline을 만들었는지 기록한다.

4. `k8s_object_snapshot` MVP 범위를 축소하고 명확히 한다.
   - MVP에서는 별도 engine을 만들지 않는다.
   - Kubernetes object snapshot을 수집한 뒤 scalar SLI metric으로 환원한다.
   - raw object metadata는 evidence로 보존한다.
   - 따라서 MVP는 기존 kube-slint 2점 engine과 호환되는 경로로 시작한다.

5. revision content hash의 의미를 정의한다.
   - content hash는 image digest, component spec digest, environment digest, config digest, verification policy digest, baseline reference를 canonical form으로 묶어 계산한다.
   - 이후 hash chain/signature로 확장할 수 있다.

6. kube-slint Track 번호를 정리한다.
   - K0: schemaVersion strictness
   - K1: SLIResult.Status propagation
   - K2: Counter reset policy
   - K3: curlpod security and cleanup
   - K4: Evidence redaction
   - K5: k8s_object_snapshot MVP

v0.5 이후 이 문서는 단순 방향 문서가 아니라, bori와 kube-slint의 초기 PR을 자를 수 있는 기준 문서로 사용한다.

---

## 1. 최종 목표

bori의 최종 목표는 다음이다.

> bori는 genomic dataplane app set을 설치, 버전 관리, 검증, 롤아웃, 관측, 승격하는 Knative-style control plane으로 발전한다.

관리 대상은 다음과 같다.

- JUMI
- artifact-handoff
- node-artifact-runtime / nan
- tori
- NodeSentinel (2026-06-02 추가)
- 이후 추가될 genomic dataplane app

하지만 현재 단계의 목표는 operator 구현이 아니다.

현재 단계의 현실적인 목표는 다음이다.

> 여러 agent가 각각의 dataplane app을 개발하더라도, 배포와 검증은 bori라는 통일된 entrypoint를 통해 수행하게 만드는 것.

즉, bori v0.x의 핵심 정체성은 다음이다.

```text
bori v0.x = agent-facing unified deploy/verify gateway
```

나중에 operator가 만들어지면 이 모델이 다음처럼 승격된다.

```text
현재:
  agent -> app별 shell/ko/kustomize/devspace -> Kubernetes

bori v0.x:
  agent -> bori CLI -> component/environment/release/verification model -> adapters -> Kubernetes

bori v1.x 후보:
  agent/user -> BoriDataPlane/BoriRelease CR -> bori operator -> Kubernetes
```

bori의 장기 목표는 단순한 배포 자동화가 아니라, 다음 조건을 만족하는 control plane이다.

```text
- 어떤 component version set이 배포되었는지 추적한다.
- 어떤 environment에서 어떤 verification을 통과했는지 기록한다.
- 어떤 revision이 promotion 가능한지 판단한다.
- 실패/NO_GRADE/WARN을 운영자가 이해할 수 있는 artifact로 남긴다.
- JUMI처럼 Kubernetes object churn을 유발하는 component는 별도 churn gate를 통과해야 한다.
- 충분히 안정된 모델을 나중에 operator reconcile loop로 승격한다.
```

---

## 2. 현재 bori 코드 상태 요약

현재 bori는 작고 명확한 구조를 갖고 있다.

주요 구조는 다음과 같다.

```text
adapters/devspace/
  main.go
  collect.go
  component.go

pkg/adapter/
  adapter.go
  gate_runner.go
  summary.go

schema/
  component.schema.yaml
  policy.schema.yaml

example/.bori/
  component.yaml
  policy.yaml
```

현재 동작은 다음에 가깝다.

```text
bori-devspace
  -> apps-dir 아래에서 .bori/component.yaml 탐색
  -> policy.<profile>.yaml 탐색
  -> kubectl port-forward로 /metrics scrape
  -> smoke command 또는 wait 실행
  -> 다시 /metrics scrape
  -> before/after delta summary 생성
  -> slint-gate 호출
  -> gate summary 출력
```

현재 코드의 장점은 다음이다.

- 이미 DevSpace after-deploy adapter 성격을 갖고 있다.
- `.bori/component.yaml` 기반 self-registration 구조가 있다.
- `slint-gate`를 외부 바이너리로 호출한다.
- bori가 gate rule engine을 직접 구현하지 않는 방향이 이미 일부 반영되어 있다.
- 코드가 작아서 전환 리팩터링이 쉽다.

현재 한계는 다음이다.

- deploy orchestration 모델이 없다.
- `bori plan/deploy/verify/status` 같은 일반 CLI가 없다.
- component/environment/release/adapters/verification 모델이 아직 없다.
- 현재 `parsePromText`는 운영 관측용 parser로 부적합하다.
- 현재 `buildDeltaSummary`는 모든 metric을 단순 delta 중심으로 만든다.
- bori가 summary schema를 직접 정의하면 kube-slint와 schema drift가 발생할 수 있다.
- `slint-gate --fail-on FAIL` 고정 호출은 `NO_GRADE`를 promotion gate에서 조용히 통과시킬 위험이 있다.
- 실패 시에도 구조화된 run artifact를 항상 남기는 모델이 약하다.
- smoke command가 `sh -c`로 실행되어 agent-facing 모델에서는 신뢰 경계 문제가 생긴다.
- operator로 갈 때 필요한 RBAC/namespace/secret/redaction 모델이 아직 없다.

---

## 3. 핵심 원칙

### 3.1 operator 먼저 금지

bori의 최종 형태는 operator일 수 있다.
하지만 지금 operator부터 만들면 안 된다.

이유는 다음과 같다.

- 아직 component ownership이 정리되지 않았다.
- app repo와 bori 사이의 경계가 확정되지 않았다.
- verification source 모델이 정리되지 않았다.
- kube-slint와 bori의 역할 분리가 완전히 고정되지 않았다.
- JUMI/AH/nan/tori의 deploy 방식이 아직 migration source 상태다.

따라서 초기 구현은 CLI + adapter + artifact 기반으로 간다.

### 3.2 script dump 금지

bori는 기존 shell script를 모아두는 저장소가 아니다.

나쁜 방향:

```text
bori/scripts/deploy-jumi.sh
bori/scripts/deploy-ah.sh
bori/scripts/deploy-nan.sh
```

좋은 방향:

```text
component.yaml
+ environment.yaml
+ release.yaml
+ verification-policy.yaml
  -> BoriDeployPlan
  -> adapter 실행
  -> BoriVerificationRun 저장
```

기존 script, ko, kustomize, devspace는 당장 버리지 않는다.
다만 모두 bori adapter 아래로 들어와야 한다.

### 3.3 kube-slint 대체 금지

bori는 kube-slint를 대체하지 않는다.

역할은 다음처럼 나눈다.

```text
bori:
  - 어떤 app set을 어떤 environment에 배포할지 결정
  - component/release/revision/rollout/promotion 관리
  - 어떤 verification policy를 실행할지 결정
  - verification result를 promotion decision에 연결
  - run artifact와 상태를 저장

kube-slint:
  - SLI measurement summary 생성
  - policy/baseline/regression/reliability 평가
  - slint-gate-summary.json 생성
  - shift-left 및 운영 관측 SLI gate engine 역할
```

### 3.4 summary schema 단일 출처 원칙

bori는 kube-slint summary 구조를 수동 복제하지 않는다.

원칙은 다음이다.

```text
summary schema owner:
  kube-slint

bori:
  kube-slint summary를 생성하거나 소비할 수는 있지만,
  schema 정의의 단일 출처가 되면 안 된다.
```

bori가 shell-out 방식으로 slint-gate를 호출하더라도, summary schema만큼은 kube-slint와 공유해야 한다.
가능한 방식은 두 가지다.

```text
방식 A: Go library import
  bori가 github.com/.../kube-slint/pkg/slo/summary 를 import한다.

방식 B: CLI contract
  bori는 kube-slint가 문서화한 summary schemaVersion만 생성한다.
  slint-gate는 입력 schemaVersion을 검증한다.
```

초기에는 방식 B로 시작할 수 있다.
하지만 schema drift가 반복되면 방식 A를 검토한다.

### 3.5 app business logic 침범 금지

bori는 JUMI/AH/nan/tori 내부 동작을 소유하지 않는다.

```text
JUMI:
  execution lifecycle, executor, spawner integration

artifact-handoff:
  artifact registration, resolve, placement/materialization intent

nan:
  runtime artifact helper, materialization/fetch contract

tori:
  pipeline authoring/execution domain logic

bori:
  version set, deploy, verification, rollout, promotion
```

---

## 4. bori와 kube-slint 통합 모델

### 4.1 결론

kube-slint는 bori 내부로 흡수하지 않는다.

대신 kube-slint는 bori의 공식 verification backend 중 하나가 된다.

```text
bori verify
  -> verification policy 해석
  -> provider: kube-slint 선택
  -> kube-slint summary 또는 slint-gate 실행
  -> gate result 수집
  -> BoriVerificationRun 저장
  -> promotion 가능 여부 판단
```

### 4.2 kube-slint가 이미 제공하는 기반

kube-slint는 bori가 자체 구현하려던 일부 기능을 이미 더 나은 형태로 갖고 있다.

중요한 기반은 다음이다.

```text
- metric key canonicalization
- MetricsFetcher / SnapshotFetcher 계열 추상화
- Reliability 진단 필드
- baseline comparison
- slint-gate CLI
- SLI spec registry
- JUMI/AH 관련 일부 SLI spec
```

따라서 bori가 metric parser, delta 계산, summary schema, gate engine을 다시 구현하면 안 된다.

### 4.3 현재 bori의 Prometheus parser/delta 모델에 대한 판단

현재 bori에는 다음 흐름이 있다.

```text
scrapeMetrics()
  -> parsePromText()
  -> map[string]float64
  -> buildDeltaSummary()
  -> sli-summary.json
  -> slint-gate
```

이 흐름은 v0 개발 초기에는 유용하다.
하지만 장기적으로 bori가 이 로직을 소유하면 안 된다.

이유는 다음과 같다.

- Prometheus exposition format을 제대로 파싱하려면 label canonicalization, histogram/summary, timestamp, family, type 정보를 다뤄야 한다.
- 운영 관측은 단순 before/after delta가 아니라 rate, ratio, quantile, burn rate, baseline comparison이 필요하다.
- bori가 metric parser와 SLI engine까지 소유하면 책임 범위가 너무 커진다.
- kube-slint가 이미 summary schema, reliability 상태, policy/baseline 평가, slint-gate를 담당하고 있다.

따라서 v0.4 기준 결정은 다음이다.

```text
bori의 parsePromText/buildDeltaSummary:
  - 임시 compatibility shim으로 유지 가능
  - 신규 기능의 중심으로 삼지 않음
  - kube-slint backend 정렬 후 축소/삭제 후보

정식 검증 경로:
  - kube-slint summary schema
  - slint-gate CLI
  - 향후 kube-slint source abstraction
```

### 4.4 bori VerificationPolicy 예시

초기에는 다음 형태를 목표로 한다.

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
  baseline: docs/baselines/jumi-ah-sli-summary.json
  failOn: FAIL_OR_NOGRADE
  artifacts:
    gateSummary: artifacts/slint-gate-summary.json
```

아직 CRD가 아니어도 된다.
처음에는 bori repo의 YAML contract로 시작한다.

### 4.5 slint-gate 호출 방식

초기 bori는 slint-gate의 exit code에 직접 의존하기보다, JSON 결과를 읽고 bori가 정책적으로 판정하는 방향을 권장한다.

권장 방식:

```text
bori
  -> slint-gate --fail-on NEVER --output slint-gate-summary.json
  -> slint-gate-summary.json 읽기
  -> VerificationPolicy.failOn 기준으로 bori가 promotionDecision 계산
```

이유는 다음과 같다.

- local/dev, integration, promotion profile마다 `NO_GRADE` 처리 기준이 다르다.
- slint-gate exit code에 의존하면 bori run artifact 생성이 흐트러질 수 있다.
- bori는 `BoriVerificationRun`을 반드시 남겨야 하므로 JSON 결과를 읽는 흐름이 더 안정적이다.

대안 방식:

```text
bori
  -> slint-gate --fail-on <VerificationPolicy.failOn>
  -> exit code + JSON 결과 모두 수집
```

다만 이 방식에서도 실패 시 JSON 결과를 반드시 보존해야 한다.

### 4.6 slint-gate 결과를 BoriVerificationRun으로 감싸기

kube-slint/slint-gate가 생성하는 결과는 bori가 그대로 저장하되, bori 관점의 metadata를 덧붙인다.

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

### 4.7 NO_GRADE 정책

운영으로 갈수록 `NO_GRADE` 처리가 중요하다.

초기 정책은 다음을 권장한다.

```text
local/dev:
  NO_GRADE = WARN 또는 non-blocking

shared dev / integration:
  NO_GRADE = FAIL_OR_NOGRADE 선택 가능

promotion / release gate:
  NO_GRADE = blocking
```

즉, 메트릭을 못 가져온 상태를 PASS로 보면 안 된다.
단, inner-loop 개발에서는 너무 엄격하면 개발 속도를 떨어뜨리므로 profile별로 다르게 둔다.

### 4.8 engine status와 gate policy 결과 병합 규칙

kube-slint integration에서 가장 중요한 판정 규칙은 다음이다.

```text
engine status:
  measurement reliability 신호
  예: pass / warn / fail / block / skip

gate policy result:
  threshold / baseline / regression 평가 신호
  예: PASS / WARN / FAIL / NO_GRADE
```

두 신호는 서로 다른 의미를 갖는다.
따라서 하나가 다른 하나를 무조건 덮어쓰면 안 된다.

권장 규칙은 다음이다.

```text
Severity order:
  PASS < WARN < NO_GRADE < FAIL

engine status mapping:
  pass        -> PASS
  warn        -> WARN
  skip        -> NO_GRADE
  fail/block  -> FAIL

final gate result:
  max(engine-derived result, policy-derived result)
```

예를 들어 engine이 `skip`을 냈다면 measurement input이 부족하다는 뜻이다.
이때 gate policy threshold가 없어서 PASS처럼 보이더라도 최종 결과는 `NO_GRADE`가 되어야 한다.

반대로 engine은 `pass`였지만 baseline regression이 FAIL이면 최종 결과는 `FAIL`이어야 한다.

이 규칙은 kube-slint Track K1의 구현 기준이며, bori Phase 1.5의 promotion decision에도 동일하게 적용한다.

---

## 5. Verification Source 모델

bori의 verification은 단순한 smoke/gate 실행이 아니라 source 기반 모델로 확장되어야 한다.

다만 v0.5에서는 source를 세 그룹으로 명확히 나눈다.

### 5.1 현재 kube-slint 2점 engine으로 가능한 source

현재 kube-slint는 기본적으로 다음 모델에 가깝다.

```text
start snapshot/fetch
  -> smoke 또는 scenario 실행
end snapshot/fetch
  -> SLI 계산
  -> policy/baseline 평가
```

이 모델로 비교적 빨리 가능한 source는 다음이다.

```text
point_scrape:
  before/after /metrics 수집
  port-forward 또는 curlpod 기반

kube_slint_summary:
  외부에서 생성된 sli-summary.json을 slint-gate로 평가

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

### 5.2 kube-slint engine 확장이 필요한 source

다음 source는 단순 fetcher 추가만으로 끝나지 않는다.
engine이 시간 구간, N개 시점, window aggregation을 다뤄야 한다.

```text
promql_query:
  운영 Prometheus/Thanos/Mimir에서 PromQL로 측정
  query latency, query range, window, step 개념 필요

soak_analysis_run:
  배포 후 N분 동안 지속 관측
  burn rate, error ratio, latency regression 확인

progressive_rollout_analysis:
  traffic 5% -> 25% -> 50% -> 100% 단계별 verification

windowed_quantile:
  p95/p99 over time window 계산
```

이들은 bori 문서에서는 수용 가능하게 설계하되, kube-slint의 별도 엔진 확장 트랙으로 다룬다.

### 5.3 신규 source: k8s_object_snapshot MVP 결정

JUMI churn gate를 위해 `k8s_object_snapshot` source가 필요하다.

v0.5에서는 이 source의 MVP 범위를 다음처럼 제한한다.

> `k8s_object_snapshot` MVP는 별도 engine 경로를 만들지 않는다. Kubernetes object를 수집한 뒤 scalar SLI metric으로 환원하고, raw object metadata는 evidence로 보존한다.

즉, MVP는 다음 구조다.

```text
start object snapshot
  -> object list 수집
  -> scalar metrics 생성 후보

scenario 실행
  -> synthetic pipeline 또는 upgrade smoke

end object snapshot
  -> object list 수집
  -> diff 계산
  -> scalar SLI metrics 생성
  -> raw object metadata를 evidence/*.json으로 저장
```

MVP에서 생성할 수 있는 scalar SLI 예시는 다음이다.

```text
jumi_k8s_jobs_created_delta
jumi_k8s_pods_created_delta
jumi_k8s_objects_remaining_end
jumi_k8s_orphan_objects_end
jumi_k8s_stuck_terminating_end
jumi_k8s_ownerref_missing_end
```

이 방식의 장점은 기존 kube-slint 2점 engine을 재사용할 수 있다는 점이다.

다만 evidence에는 raw object 목록을 남겨야 한다.
그래야 나중에 orphan/stuck/ownerReference 누락이 발생했을 때 어떤 object가 문제였는지 추적할 수 있다.

```text
metric path:
  map[string]float64
  기존 engine/gate 재사용

evidence path:
  raw Kubernetes object metadata JSON
  debugging / audit / bori run archive에서 사용
```

장기적으로 object metadata를 1급 데이터로 다루는 별도 source/engine을 만들 수 있지만, 그것은 MVP 범위가 아니다.

### 5.4 bori와 kube-slint의 책임 분리

```text
source 수집/SLI 계산:
  kube-slint 또는 kube-slint-compatible provider

source 선택/정책 연결:
  bori

promotion decision:
  bori

artifact 보존:
  bori
```

### 5.5 baseline과 BoriRevision의 관계

baseline은 임의로 관리되는 별도 파일이 아니다.
bori 관점에서 baseline은 promoted revision의 verification evidence에서 파생된다.

권장 흐름은 다음이다.

```text
BoriRevision 생성
  -> deploy / verify 실행
  -> kube-slint sli-summary.json 생성
  -> slint-gate PASS
  -> bori promotion decision = promoted
  -> 해당 revision의 sli-summary.json이 다음 비교의 baseline 후보가 됨
```

bori는 baseline 자체의 JSON schema를 소유하지 않는다.
summary schema와 baseline evaluation은 kube-slint가 소유한다.

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

따라서 `verification/baselines/*.json`은 단순 수동 파일이 아니라, 특정 promoted BoriRevision의 evidence snapshot으로 취급한다.

나중에 kube-slint의 baseline update helper가 있다면, bori promotion flow는 다음처럼 연결한다.

```text
promoted BoriRevision
  -> accepted sli-summary.json
  -> baseline update proposal
  -> review/approve
  -> new baseline registered
```

### 5.6 운영 관측으로의 전환

현재는 after-deploy gate 중심이다.
하지만 운영형 bori로 가려면 다음 전환이 필요하다.

```text
before/after scrape
  -> kube-slint summary
  -> PromQL source
  -> baseline comparison
  -> soak/bake analysis run
  -> rollout step별 verification
```

중요한 점은 bori가 Prometheus 서버를 직접 대체하지 않는다는 것이다.
운영에서는 이미 운영 중인 Prometheus/Thanos/Mimir 같은 backend를 query source로 사용해야 한다.

---

## 6. JUMI Churn Gate

### 6.1 문제 정의

JUMI는 executor에서 spawner를 사용한다.
spawner는 client-go를 사용해 Kubernetes API object를 동적으로 생성한다.

예상되는 object는 다음과 같다.

- Pod
- Job
- ConfigMap
- Secret reference
- ServiceAccount/RBAC reference
- runtime container 관련 object
- future runtime support object

문제는 JUMI 버전업 시 단순히 JUMI Pod가 Ready인지 보는 것만으로는 부족하다는 것이다.

JUMI 새 버전이 다음 문제를 만들 수 있다.

- 같은 pipeline에 대해 생성 object 수가 급증한다.
- retry/attempt가 과도하게 증가한다.
- 실패 후 cleanup이 되지 않는다.
- orphan Pod/Job이 남는다.
- ownerReference가 빠진다.
- terminating stuck object가 늘어난다.
- Kubernetes API write pressure가 증가한다.
- warning event가 늘어난다.
- 이전 revision 대비 object churn이 커진다.

따라서 JUMI revision promotion에는 별도의 churn gate가 필요하다.

### 6.2 kube-slint 현황 반영

kube-slint에는 이미 JUMI/AH 관련 SLI spec의 토대가 일부 존재한다.

따라서 JUMI churn gate를 완전히 새로 설계하는 것이 아니라, 기존 kube-slint SLI spec을 확장하는 방향으로 간다.

```text
이미 활용 가능한 방향:
  - jobs created delta
  - cleanup backlog end gauge
  - fast fail trigger delta
  - baseline comparison

추가가 필요한 방향:
  - k8s object snapshot source
  - ownerReference/orphan/stuck terminating 판정
  - warning event delta
  - counter reset 정책 세분화
```

### 6.3 JUMI churn gate의 위치

JUMI churn gate는 bori가 직접 계산하는 것이 아니다.

추천 구조는 다음이다.

```text
bori:
  - JUMI revision upgrade plan 생성
  - synthetic pipeline 실행 요청
  - kube-slint churn policy 실행
  - result를 promotion decision에 연결

kube-slint:
  - metric 또는 k8s object snapshot 수집
  - churn SLI 계산
  - policy/baseline/regression 평가

JUMI/spawner:
  - 필요한 runtime metric 노출
  - object label/ownerReference/correlation ID 제공
```

### 6.4 MVP 경로 A: metric 기반 churn gate

가장 빠른 MVP는 JUMI/spawner가 metric을 노출하고, kube-slint가 이를 측정하는 방식이다.

후보 metric:

```text
jumi_runs_started_total
jumi_nodes_submitted_total
jumi_attempts_started_total
jumi_attempt_retries_total
jumi_spawner_requests_total
jumi_spawner_errors_total
jumi_k8s_objects_created_total
jumi_k8s_objects_deleted_total
jumi_k8s_apply_errors_total
jumi_orphan_objects_detected_total
jumi_cleanup_backlog_objects
jumi_ownerref_missing_total
jumi_stuck_terminating_objects
jumi_k8s_api_request_duration_seconds_bucket
```

kube-slint SLI 후보:

```text
jumi_attempt_retry_delta
jumi_k8s_objects_created_delta
jumi_k8s_apply_error_delta
jumi_cleanup_backlog_end
jumi_orphan_objects_end
jumi_ownerref_missing_end
jumi_stuck_terminating_end
jumi_k8s_api_p99_latency
```

이 방식의 장점은 구현이 빠르다는 것이다.
단점은 실제 Kubernetes object 상태와 metric이 불일치할 수 있다는 것이다.

### 6.5 Counter reset 정책

JUMI churn gate에서는 counter reset 처리가 중요하다.

일반적인 delta counter에서는 다음 동작이 자연스럽다.

```text
end - start < 0
  -> counter reset 의심
  -> WARN 처리
```

하지만 JUMI promotion gate에서는 이 동작이 약할 수 있다.
JUMI가 재시작되거나 metric이 리셋되어 실제 churn 위반을 놓칠 수 있기 때문이다.

따라서 JUMI churn gate profile에서는 다음 정책을 권장한다.

```text
counter reset 의심:
  local/dev: WARN
  integration: NO_GRADE
  promotion: NO_GRADE 또는 FAIL
```

즉, promotion gate에서는 counter reset을 단순 WARN으로만 두지 않는다.
`failOn: FAIL_OR_NOGRADE` 정책과 결합해 promotion을 차단할 수 있어야 한다.

kube-slint 보완 후보:

```text
ComputeSpec.OnCounterReset:
  - warn
  - no_grade
  - fail
  - skip
```

### 6.6 MVP 경로 B: Kubernetes object snapshot source

중기적으로는 kube-slint에 `k8s_object_snapshot` source를 추가하는 것이 좋다.

v0.5의 결정은 다음이다.

> K5 MVP는 Kubernetes object metadata를 별도 engine으로 흘려보내지 않는다. object를 list/diff한 뒤 scalar SLI metric으로 환원하고, raw object list는 evidence로 남긴다.

source 동작:

```text
before snapshot:
  - namespace object list
  - label selector 기반 Pod/Job 수집
  - ownerReference 수집
  - deletionTimestamp 확인
  - event 수집 후보

synthetic pipeline 실행:
  - JUMI가 spawner를 통해 object 생성

워치/대기:
  - pipeline completion 또는 timeout
  - cleanup grace period

after snapshot:
  - 생성/삭제/잔존 object diff
  - orphan 판정
  - stuck terminating 판정
  - ownerReference 누락 판정

metric reduction:
  - diff 결과를 scalar SLI metric으로 변환
  - 기존 kube-slint 2점 engine으로 평가

evidence:
  - raw object metadata JSON 저장
  - orphan/stuck/ownerRef missing object 목록 저장
```

SLI 후보:

```text
jumi_k8s_jobs_created_delta
jumi_k8s_pods_created_delta
jumi_k8s_configmaps_created_delta
jumi_k8s_objects_remaining_end
jumi_k8s_orphan_objects_end
jumi_k8s_stuck_terminating_end
jumi_k8s_ownerref_missing_end
jumi_k8s_cleanup_duration_seconds
jumi_k8s_warning_events_delta
```

이 방식은 JUMI/spawner metric만 믿지 않고 실제 Kubernetes API 상태를 확인할 수 있다.
또한 MVP 단계에서는 kube-slint engine을 크게 바꾸지 않아도 된다.

### 6.7 synthetic pipeline과 측정 오염 방지

JUMI churn gate는 synthetic pipeline을 실행할 가능성이 높다.
이때 verification 자체가 만든 Kubernetes object가 churn 측정을 오염시킬 수 있다.

따라서 다음 label 정책이 필요하다.

```text
검증용 리소스 label:
  bori.dev/run-id=<run-id>
  kube-slint.dev/run-id=<run-id>
  app.kubernetes.io/managed-by=bori 또는 kube-slint

측정 대상 selector:
  include: jumi.dev/run-id=<target-run-id>
  exclude: kube-slint.dev/run-id exists
  exclude: bori.dev/verification-helper=true
```

### 6.8 bori에서의 JUMI upgrade gate 예시

```yaml
apiVersion: bori.dev/v1alpha1
kind: BoriVerificationPolicy
metadata:
  name: jumi-upgrade-churn-gate
spec:
  provider: kube-slint
  source: k8s_object_snapshot
  scenario:
    type: synthetic-pipeline
    name: fanout-handoff-collect-smoke
  policy: verification/policies/jumi-churn-policy.yaml
  baseline: baselines/jumi-v0.3.0-churn.json
  failOn: FAIL_OR_NOGRADE
  blocking: true
```

bori promotion rule:

```text
JUMI revision promotion 조건:
  - health gate PASS
  - smoke gate PASS
  - kube-slint SLI gate PASS
  - JUMI churn gate PASS
  - no blocking security findings
```

---

## 7. kube-slint 보완 트랙

bori가 kube-slint를 공식 verification backend로 쓰려면 kube-slint 쪽에도 몇 가지 보완 작업이 필요하다.
이 섹션은 bori 내부 구현이 아니라 kube-slint repo에서 진행할 작업이다.

### K0 — schemaVersion strictness

목표:

```text
slint-gate가 알 수 없는 summary schema를 조용히 통과시키지 않게 한다.
```

작업:

```text
- summary.SchemaVersion 상수 공개
- summary.Validate() 또는 ValidateSchemaVersion() 추가
- slint-gate loadMeasurement 단계에서 schemaVersion 검증
- unknown/empty schemaVersion => NO_GRADE 또는 unsupported_schema
- 테스트 추가
```

완료 기준:

```text
- schemaVersion 누락 시 NO_GRADE
- 알 수 없는 schemaVersion 시 NO_GRADE
- 지원 schemaVersion은 기존처럼 평가
- gate summary에 reason이 남음
```

### K1 — SLIResult.Status propagation

목표:

- `SLIResult.Status`를 `gate_result`에 반영한다.
- engine status와 gate policy 결과가 충돌할 때의 우선순위 규칙을 구현한다.

기본 mapping:

```text
engine status:
  pass        -> PASS
  warn        -> WARN
  skip        -> NO_GRADE
  fail/block  -> FAIL
```

우선순위 규칙:

```text
Severity order:
  PASS < WARN < NO_GRADE < FAIL

Final result:
  max(engine-derived result, policy-derived result)
```

이 규칙이 필요한 이유:

```text
engine status:
  measurement reliability를 나타냄

gate policy result:
  threshold / baseline / regression 위반을 나타냄
```

둘 중 하나만 사용하면 안 된다.
예를 들어 engine이 `skip`이면 입력이 부족한 것이므로 policy threshold가 없어도 PASS로 보면 안 된다.

완료 기준:

- `Status=warn`인 결과가 gate WARN으로 반영된다.
- `Status=skip`인 결과가 gate NO_GRADE로 반영된다.
- `Status=fail/block`인 결과가 gate FAIL로 반영된다.
- policy 결과와 engine status가 충돌하면 더 심각한 결과가 최종 gate result가 된다.

### K2 — Counter reset policy

목표:

```text
ComputeDelta의 counter reset 처리를 spec/profile별로 조정할 수 있게 한다.
```

정책 후보:

```text
warn      기존 기본값
no_grade  promotion gate용
fail      강한 gate용
skip      측정 제외
```

JUMI churn gate 권장:

```text
jumi_*_delta 중 promotion blocking에 쓰는 SLI:
  onCounterReset: no_grade
```

### K3 — curlpod security and cleanup

목표:

```text
curlpod fetcher가 만드는 임시 Pod의 권한, label, cleanup을 명확히 한다.
```

작업:

```text
- curlpod에 run-id label 추가
- cleanup 실패 시 warning/evidence 기록
- 최소 RBAC 문서화
- NetworkPolicy 예시 후보 작성
- verification helper resource를 churn 측정에서 제외할 수 있게 label 표준화
```

### K4 — Evidence redaction

목표:

```text
evidence artifact에 secret/token/password가 남지 않게 한다.
```

작업:

```text
- RedactString / RedactMap utility 추가
- Authorization, token, password, secret, key 패턴 마스킹
- evidence copy helper 또는 writer에서 redaction 적용
- 테스트 추가
```

### K5 — k8s_object_snapshot MVP

목표:

- JUMI executor/spawner/client-go object churn을 metric 외부에서도 관측한다.
- MVP에서는 object snapshot을 scalar SLI metric으로 환원한다.
- raw object metadata는 evidence로 보존한다.

MVP 결정:

```text
새로운 engine 경로를 만들지 않는다.
MetricsFetcher-compatible output을 유지한다.
object list/diff 결과를 map[string]float64로 환원한다.
raw object list는 evidence/*.json으로 저장한다.
```

작업:

```text
- namespace + include/exclude label selector 설계
- before/after object list 수집
- created/remaining/orphan/stuck/ownerRefMissing count 계산
- scalar SLI metric 생성
- raw object evidence 저장
```

완료 기준:

- JUMI synthetic pipeline 실행 전후 object churn을 scalar SLI로 볼 수 있다.
- evidence에서 어떤 object가 orphan/stuck/ownerRef missing이었는지 확인할 수 있다.
- verification helper resource는 selector로 제외할 수 있다.

---

## 8. 보안 모델

보안 모델은 v0.6이나 operator 시점에 미루면 안 된다.
초기 문서와 contract에 반드시 들어가야 한다.

### 8.1 smoke command 신뢰 경계

현재 `--smoke-cmd`는 shell string으로 실행될 수 있다.
agent-facing gateway로 가면 이 방식은 위험하다.

초기 정책:

```text
local/dev:
  shell smoke 허용 가능
  단, 명시적으로 unsafe/developer mode로 표시

shared/integration:
  구조화된 smoke spec 우선
  shell string은 기본 금지 또는 allowlist 필요

promotion:
  arbitrary sh -c 금지
  검증된 smoke primitive만 허용
```

권장 smoke spec:

```yaml
smoke:
  type: exec
  command: ["go", "test", "./test/smoke", "-run", "TestJUMIAHSmoke"]
  timeout: 2m
  workingDir: ../jumi
```

향후 후보:

```yaml
smoke:
  type: kubernetes-job
  image: ghcr.io/.../jumi-smoke@sha256:...
  command: ["/smoke"]
  timeout: 5m
```

### 8.2 secret redaction

bori는 run archive를 남긴다.
run archive에는 request, rendered manifest, verification result, logs, evidence가 포함될 수 있다.

따라서 다음은 반드시 redaction 대상이다.

- kubeconfig token
- registry password
- docker auth config
- Secret.data / Secret.stringData
- bearer token
- API key
- cloud credential
- signed URL
- private endpoint credential
- evidence file 내부의 Authorization/token/password 값

정책:

```text
run archive에는 secret raw value를 저장하지 않는다.
Secret object는 name/reference만 저장한다.
rendered manifest 저장 시 Secret.data/stringData는 REDACTED 처리한다.
환경 변수 이름에 TOKEN/PASSWORD/SECRET/KEY가 포함되면 기본 redaction한다.
evidence 복사 시에도 redaction을 적용한다.
```

### 8.3 RBAC / namespace authorization

operator apply mode 전까지도 authorization 모델은 문서화되어야 한다.

초기 원칙:

```text
BoriEnvironment는 쓸 수 있는 namespace 범위를 명시한다.
BoriRelease는 허용된 namespace 밖으로 apply할 수 없다.
bori CLI는 apply 전 plan 단계에서 namespace violation을 감지한다.
operator shadow mode에서는 권한 부족을 status로 표현한다.
operator apply mode에서는 최소 권한 ServiceAccount를 사용한다.
```

### 8.4 verification helper resource 권한

kube-slint의 curlpod, JUMI churn synthetic pipeline, smoke job은 클러스터에 임시 리소스를 만들 수 있다.

따라서 verification resource 권한을 별도로 다뤄야 한다.

```text
verification helper:
  - 별도 ServiceAccount 사용 후보
  - run-id label 필수
  - cleanup 책임 명확화
  - churn 측정 대상에서 제외
  - 실패 시 cleanup warning 기록
```

### 8.5 revision snapshot 무결성

초기에는 파일 기반 snapshot을 사용한다.
그러나 다음 필드는 반드시 포함한다.

```text
revisionId
component version
image digest
component spec digest
config digest
environment digest
verification policy digest
baseline reference
createdAt
parent revision
content hash
```

`content hash`는 다음 항목을 canonical form으로 정렬·직렬화한 뒤 계산한다.

```text
contentHash = hash(
  image digest
  + component spec digest
  + config digest
  + environment digest
  + verification policy digest
  + baseline reference
)
```

이 hash는 revision이 어떤 실행 입력으로 검증되었는지를 추적하기 위한 최소 무결성 기준이다.
중기적으로는 snapshot hash chain 또는 signature를 고려한다.

---

## 9. Run Artifact 모델

bori는 성공한 실행만 기록하면 안 된다.
실패한 실행이 더 중요하다.

초기 run archive 구조:

```text
.bori/runs/<run-id>/
  request.yaml
  plan.json
  deploy-result.json
  verification-result.json
  status.json
  logs/
    adapter.log
    smoke.log
    slint-gate.log
  evidence/
    sli-summary.json
    slint-gate-summary.json
    k8s-object-before.json
    k8s-object-after.json
  rendered/
    manifest.yaml
```

원칙:

```text
성공/실패/스킵/NO_GRADE 모두 status.json을 남긴다.
실패한 app도 artifact를 남긴다.
secret은 redaction 후 저장한다.
artifact에는 runId, release, environment, component, revision, git commit, image digest를 기록한다.
slint-gate exit code와 관계없이 gate summary JSON을 보존한다.
```

---

## 10. 디렉토리 구조 제안

v0.4 기준 목표 구조:

```text
bori/
  cmd/
    bori/
      main.go
    bori-devspace/
      main.go

  components/
    jumi/
      component.yaml
    artifact-handoff/
      component.yaml
    nan/
      component.yaml
    tori/
      component.yaml

  environments/
    kind/
      environment.yaml
    multipass/
      environment.yaml
    jumi-ah-dev/
      environment.yaml
    prod-like/
      environment.yaml

  releases/
    jumi-ah-dev/
      release.yaml

  verification/
    policies/
      jumi-ah-smoke.yaml
      jumi-upgrade-churn.yaml
      ah-materialization-smoke.yaml
    baselines/
      jumi-ah-sli-summary.json
      jumi-churn-baseline.json

  adapters/
    devspace/
    ko/
    kustomize/
    shell/

  pkg/
    model/
    planner/
    adapter/
    verification/
    artifact/
    security/

  docs/
    architecture.md
    control-plane-roadmap.md
    agent-contract.md
    migration-inventory.md
    security-model.md
    verification-model.md
    kube-slint-integration.md
    jumi-churn-gate.md
    operator-future.md
```

기존 `adapters/devspace`는 유지하되, 장기적으로는 `cmd/bori-devspace`에서 호출하는 compatibility adapter로 정리한다.

---

## 11. 핵심 데이터 모델 초안

### 11.1 BoriComponent

```yaml
name: jumi
kind: control-component
version: v0.3.0
image:
  ref: ghcr.io/heainseo/jumi@sha256:...
ports:
  metrics: 8080
  health: 8081
health:
  path: /healthz
metrics:
  path: /metrics
dependencies:
  - artifact-handoff
contracts:
  - jumi-executor-spawner-v1
verificationPolicies:
  - jumi-health
  - jumi-upgrade-churn-gate
```

### 11.2 BoriEnvironment

```yaml
name: kind
cluster:
  kubeconfig: ${KUBECONFIG}
namespacePolicy:
  allowed:
    - jumi-system
    - artifact-system
registry:
  default: ghcr.io/heainseo
secrets:
  mode: reference-only
  redaction: strict
```

### 11.3 BoriRelease

```yaml
name: jumi-ah-dev
components:
  - name: jumi
    version: v0.3.0
  - name: artifact-handoff
    version: v0.2.0
  - name: nan
    version: v0.1.5
compatibility:
  matrix: compatibility/jumi-ah-nan.yaml
verification:
  policies:
    - jumi-ah-smoke
    - jumi-upgrade-churn-gate
promotion:
  requiredGateResult: PASS
  baselinePolicy:
    updateFrom: promoted-revision-evidence
    reviewRequired: true
```

### 11.4 BoriVerificationPolicy

```yaml
name: jumi-upgrade-churn-gate
provider: kube-slint
source: k8s_object_snapshot
scenario:
  type: synthetic-pipeline
policy: verification/policies/jumi-churn-policy.yaml
baseline: verification/baselines/jumi-churn-baseline.json
failOn: FAIL_OR_NOGRADE
blocking: true
```

`baseline`은 kube-slint summary schema를 따르는 파일이다.
bori는 baseline 파일의 schema를 재정의하지 않고, 어떤 BoriRevision에서 나온 baseline인지 provenance를 기록한다.

---

## 12. 개발 로드맵과 일정

일정은 현재 추정이다.
실제 JUMI/AH/nan/tori 개발 속도와 bori/kube-slint 코드 상태에 따라 조정될 수 있다.

### 12.1 Phase와 Track 의존성

v0.5에서는 Phase와 kube-slint Track을 독립 일정으로 보지 않는다.
일부 Phase는 kube-slint Track의 결과에 의존한다.

의존성 요약:

```text
Track K0 schemaVersion strictness
  -> Phase 1.5 kube-slint backend alignment

Track K1 SLIResult.Status propagation
  -> Phase 1.5 gate result wrapping
  -> Phase 3.5 JUMI churn gate 판단 정확도

Track K2 Counter reset policy
  -> Phase 3.5 JUMI churn gate promotion blocking

Track K3/K4 curlpod security + evidence redaction
  -> Phase 3 이후 실제 dataplane absorption 안정성

Track K5 k8s_object_snapshot MVP
  -> Phase 3.5 JUMI churn gate object-level 관측
```

Fallback 규칙:

```text
K0가 지연될 경우:
  Phase 1.5는 slint-gate --fail-on NEVER + bori-side JSON 판정을 먼저 구현한다.
  schemaVersion strict validation은 K0 merge 이후 보강한다.

K1이 지연될 경우:
  Phase 1.5는 gate summary의 top-level gate_result를 우선 사용한다.
  SLIResult.Status 병합은 K1 merge 이후 적용한다.

K2가 지연될 경우:
  Phase 3.5에서 counter reset은 보수적으로 NO_GRADE 취급하는 bori-side 임시 정책을 둔다.

K5가 지연될 경우:
  Phase 3.5는 metric 기반 JUMI churn gate만 먼저 완료한다.
  k8s_object_snapshot은 후속 enhancement로 분리한다.
```

이 의존성 때문에 일정은 병렬처럼 보이더라도 실제 acceptance criteria는 Track 진행 상황에 따라 조정될 수 있다.

### Phase 0 — 설계/정합성/보안 기준선

예상 기간: 2026-06-01 ~ 2026-06-07

목표:

- bori의 control plane 전환 방향 문서화
- operator 선행 금지 원칙 고정
- kube-slint integration 방향 고정
- 보안 모델과 verification source 모델 추가
- 현재 schema/parser 불일치 수정 계획 수립
- kube-slint 보완 트랙을 별도 명시

산출물:

```text
docs/architecture.md 업데이트
docs/control-plane-roadmap.md
docs/agent-contract.md
docs/security-model.md
docs/verification-model.md
docs/kube-slint-integration.md
docs/migration-inventory.md
```

완료 기준:

- bori가 DevSpace runner만이 아니라 control-plane transition repo라는 점이 문서화된다.
- kube-slint가 공식 verification backend로 정의된다.
- Prometheus parser/delta 계산은 임시 shim으로 명시된다.
- arbitrary smoke command와 secret redaction 위험이 문서화된다.
- kube-slint의 schemaVersion/failOn/counter reset 보완 필요가 문서화된다.

### Kube-slint Track K0 — Gate strictness hardening

예상 기간: 2026-06-03 ~ 2026-06-10

목표:

- slint-gate가 잘못된 summary schema를 조용히 통과시키지 않게 한다.

작업:

```text
- schemaVersion 검증
- summary.Validate 추가
- invalid schema test
- unknown schema => NO_GRADE
```

완료 기준:

- schemaVersion 누락/unknown이 gate에서 표시된다.
- bori가 잘못된 summary를 넘겨도 PASS로 조용히 통과하지 않는다.

### Kube-slint Track K1 — Result status and counter reset policy

예상 기간: 2026-06-11 ~ 2026-06-21

목표:

- SLIResult.Status를 gate_result에 반영한다.
- ComputeDelta counter reset 정책을 조정 가능하게 한다.

작업:

```text
- warn/fail/block/skip 상태 gate 반영
- OnCounterReset: warn/no_grade/fail/skip 후보
- JUMI churn SLI에서 no_grade 정책 적용 후보
```

완료 기준:

- counter reset이 promotion gate에서 단순 WARN으로만 묻히지 않는다.
- JUMI churn gate에서 reset 의심을 NO_GRADE/blocking으로 처리할 수 있다.

### Phase 1 — Unified Agent Gateway CLI

예상 기간: 2026-06-08 ~ 2026-06-21

목표:

- `bori` CLI skeleton 추가
- agent가 공통으로 사용할 명령 제공

명령 후보:

```bash
bori plan --release jumi-ah-dev --env kind
bori deploy --release jumi-ah-dev --env kind
bori verify --release jumi-ah-dev --env kind
bori status --run <run-id>
```

산출물:

```text
cmd/bori/main.go
pkg/model
pkg/planner
pkg/artifact
.bori/runs/<run-id>/status.json
```

완료 기준:

- 실제 deploy가 아직 단순해도 plan/result artifact가 생성된다.
- 실패한 실행도 status.json을 남긴다.
- agent가 직접 kubectl/ko/devspace를 호출하지 않고 bori entrypoint를 사용할 수 있는 최소 경로가 생긴다.

### Phase 1.5 — kube-slint Backend Alignment

예상 기간: 2026-06-22 ~ 2026-07-05

목표:

- bori의 공식 verification provider로 kube-slint/slint-gate 정렬
- slint-gate output을 BoriVerificationRun으로 wrapping
- 기존 bori delta summary 경로를 compatibility shim으로 격하
- `failOn` 정책을 bori VerificationPolicy에서 주입 또는 bori가 JSON 결과 기반으로 판정

산출물:

```text
pkg/verification/provider.go
pkg/verification/kubeslint.go
verification/policies/example.yaml
verification/baselines/example.json
```

완료 기준:

- bori verify가 slint-gate를 provider로 호출할 수 있다.
- `PASS/WARN/FAIL/NO_GRADE`가 bori status와 promotion decision에 반영된다.
- measurement summary와 gate summary가 run archive에 저장된다.
- `NO_GRADE`가 promotion gate에서 조용히 통과하지 않는다.

### Phase 2 — Component / Environment / Adapter 모델

예상 기간: 2026-07-06 ~ 2026-07-31

목표:

- component registry 초안 작성
- environment overlay 초안 작성
- adapter 인터페이스 정리
- app repo에 남길 것과 bori가 소유할 것 분리

산출물:

```text
components/jumi/component.yaml
components/artifact-handoff/component.yaml
components/nan/component.yaml
components/tori/component.yaml
environments/kind/environment.yaml
environments/multipass/environment.yaml
adapters/devspace
adapters/ko
adapters/kustomize
adapters/shell
```

완료 기준:

- JUMI/AH/nan/tori가 managed component로 표현된다.
- health/metrics/dependencies/contracts가 보인다.
- deploy script와 verification input이 섞이지 않는다.

### Kube-slint Track K3/K4 — Curlpod security and evidence redaction

예상 기간: 2026-07-01 ~ 2026-07-14

목표:

- curlpod fetcher가 만드는 임시 리소스와 evidence를 안전하게 관리한다.

작업:

```text
- curlpod run-id label
- cleanup warning/evidence
- minimal RBAC docs
- redaction utility
```

완료 기준:

- verification helper resource가 식별 가능하다.
- cleanup 실패가 evidence에 남는다.
- secret/token/password가 evidence에 평문으로 남지 않는다.

### Phase 3 — 첫 dataplane app 흡수

예상 기간: 2026-08-01 ~ 2026-08-14

목표:

- JUMI 또는 artifact-handoff 중 하나를 bori 경유로 배포/검증
- 기존 shell/ko/kustomize/devspace 흐름을 adapter로 감싸기

추천 대상:

```text
1순위: artifact-handoff
  이유: JUMI보다 runtime orchestration 위험이 낮고, bori absorption path 검증에 적합

2순위: JUMI
  이유: 최종 중요도는 높지만 churn gate가 필요하므로 조금 더 복잡함
```

완료 기준:

- 하나의 실제 dataplane app이 bori plan/deploy/verify 경로를 탄다.
- 기존 script는 직접 실행되지 않고 adapter 아래에서 호출된다.
- run artifact가 남는다.

### Kube-slint Track K5 — k8s_object_snapshot MVP

예상 기간: 2026-08-01 ~ 2026-08-21

목표:

- Kubernetes object snapshot을 scalar SLI metric으로 환원한다.
- raw object metadata를 evidence로 보존한다.
- JUMI churn gate에서 object-level 관측을 사용할 수 있게 한다.

완료 기준:

- before/after object snapshot이 가능하다.
- created/remaining/orphan/stuck/ownerRefMissing count가 metric으로 나온다.
- raw object evidence가 저장된다.
- selector로 verification helper resource를 제외할 수 있다.

### Phase 3.5 — JUMI Churn Gate MVP

예상 기간: 2026-08-15 ~ 2026-09-07

v0.4보다 의존성을 더 명확히 한다.
metric 기반 churn gate는 먼저 진행할 수 있다.
`k8s_object_snapshot`은 Track K5의 진행 상황에 따라 같은 Phase에 포함하거나 후속 enhancement로 분리한다.

목표:

- JUMI executor/spawner/client-go object churn 관측 모델 구현
- metric 기반 MVP 우선
- k8s object snapshot source 설계 또는 MVP 착수
- JUMI revision upgrade gate 초안 작성

산출물:

```text
docs/jumi-churn-gate.md
verification/policies/jumi-upgrade-churn.yaml
verification/baselines/jumi-churn-baseline.json
kube-slint k8s_object_snapshot 설계 또는 PR 후보
```

완료 기준:

- synthetic pipeline 실행 전후 JUMI object churn을 볼 수 있다.
- orphan/stuck/cleanup/ownerReference 관련 최소 SLI가 정의된다.
- counter reset 정책이 promotion gate에서 명확히 처리된다.
- bori가 churn gate 결과를 promotion blocking 조건으로 사용할 수 있다.

### Phase 4 — Multi-app Release 모델

예상 기간: 2026-09-08 ~ 2026-09-30

목표:

- JUMI + AH + nan 조합을 하나의 BoriRelease로 표현
- version compatibility matrix 초안 작성
- release-level verification 실행

산출물:

```text
releases/jumi-ah-dev/release.yaml
compatibility/jumi-ah-nan.yaml
pkg/release
```

완료 기준:

- 단일 app이 아니라 compatible app set을 배포/검증한다.
- release-level gate가 가능하다.
- 특정 component만 바뀌었을 때 영향받는 verification을 계산할 수 있다.

### Phase 5 — Revision Snapshot / Rollout Plan

예상 기간: 2026-10-01 ~ 2026-10-31

목표:

- immutable revision snapshot 모델 도입
- deploy 전후 config/image/policy digest 기록
- rollout plan dry-run 모델 작성

산출물:

```text
pkg/revision
pkg/rollout
.bori/revisions/<revision-id>.json
.bori/rollouts/<rollout-id>.json
```

완료 기준:

- 어떤 revision이 어떤 image/config/policy로 검증되었는지 추적 가능하다.
- rollback 후보 revision을 식별할 수 있다.
- 아직 traffic routing을 구현하지 않아도 rollout plan 개념이 존재한다.

### Phase 6 — Operator Shadow Mode ✅ 완료 (2026-06-03)

~~예상 기간: 2026-11-01 ~ 2026-11-30~~  
실제 완료: 2026-06-03

목표:

- CRD/API 초안 작성
- operator가 실제 apply하지 않고 desired state와 plan/status만 계산
- CLI 모델과 operator 모델의 정합성 검증

산출물:

```text
apis/bori/v1alpha1/types.go       — BoriDataPlane Go 타입 (Kubernetes 등록 전)
pkg/shadow/shadow.go              — Reconcile(): drift 계산 + condition 생성
pkg/shadow/writer.go              — WriteState/ReadState: shadow 상태 파일 persistence
pkg/reconcile/reconciler.go       — 전체 plan→deploy→verify→promote 오케스트레이션
cmd/bori: bori shadow status      — drift + condition 출력, 파일 저장
cmd/bori: bori reconcile          — --dry-run / --skip-if-in-sync / 실제 deploy
docs/api-design.md                — CLI ↔ Operator 타입 매핑 문서
```

완료 기준:

- [x] BoriDataPlane 예시로 JUMI/AH/nan dataplane을 표현할 수 있다.
- [x] operator가 dry-run diff 또는 status만 기록한다.
- [x] 실제 배포는 여전히 CLI/adapter가 담당한다.

### Phase 7 — Limited Operator Apply Mode ✅ 완료 (2026-06-05)

~~예상 기간: 2026-12 이후~~  
실제 완료: 2026-06-05

목표:

- 제한된 namespace에서만 operator apply 수행
- health/verification/status condition 업데이트
- rollback hook 후보 검증

비목표:

```text
- full multi-tenant SaaS control plane
- traffic routing controller
- service mesh control
- full progressive delivery engine
```

산출물:

```text
apis/bori/v1alpha1/types.go     — BoriDataPlane: TypeMeta + ObjectMeta (실제 Kubernetes 오브젝트)
                                  Condition = metav1.Condition (타입 alias)
apis/bori/v1alpha1/deepcopy.go  — DeepCopyObject/DeepCopyInto
apis/bori/v1alpha1/register.go  — GroupVersion=bori.dev/v1alpha1, AddToScheme
config/crd/boridataplanes.bori.dev.yaml  — CRD YAML (kubectl apply 가능)
config/rbac/service_account.yaml         — bori-operator SA (namespace: bori-system)
config/rbac/role.yaml                    — ClusterRole: CRD CRUD + namespace read
config/rbac/role_binding.yaml            — ClusterRoleBinding
controllers/dataplane_controller.go      — DataPlaneReconciler (controller-runtime v0.24)
                                           Reconcile() → Runner.Run() → Status().Patch()
controllers/dataplane_controller_test.go — 4개 테스트: notFound / patchesStatus /
                                           runnerError / mapsSpecToRequest
cmd/bori-operator/main.go  — operator 엔트리포인트
                             flags: --bori-root, --bori-dir, --apps-dir,
                                    --leader-elect, --requeue-interval
                             scheme: clientgoscheme + v1alpha1
                             controller-runtime manager + healthz/readyz
pkg/reconcile/reconciler.go — Runner 인터페이스 추가 (mock 주입용)
```

완료 기준:

- [x] 하나의 lab environment에서 operator가 제한된 dataplane app set을 reconcile한다.
- [x] RBAC/secret redaction/status condition 모델이 작동한다.
- [x] CLI 모델과 operator 모델이 충돌하지 않는다.
      (동일한 pkg/reconcile.Reconciler.Run() 경로 사용)

의존성:

```text
sigs.k8s.io/controller-runtime v0.24.1
k8s.io/apimachinery v0.36.0
k8s.io/client-go v0.36.0
k8s.io/api v0.36.0
```

### Phase 8 — Operator Deployment Hardening ✅ 완료 (2026-06-05)

~~예상 기간: Phase 7 직후~~  
실제 완료: 2026-06-05

목표:

- operator를 실제 kind/lab 클러스터에 배포 가능한 수준으로 hardening
- Finalizer로 graceful deletion 보장
- Generation-aware reconcile로 불필요한 full reconcile 방지
- Namespace 위반을 에러가 아닌 condition으로 처리
- 배포 매니페스트 완성 (`make deploy`)

비목표:

```text
- traffic routing / progressive delivery
- kube-slint Track K0-K5 (별도 트랙 유지)
- BoriRelease CRD
- multi-tenant 분리
```

산출물:

```text
apis/bori/v1alpha1/types.go
  - ConditionViolation = "Violation" 추가
  - BoriDataPlaneStatus.ObservedGeneration int64 추가

pkg/reconcile/reconciler.go
  - ViolationError{Violations []string} 타입 추가
  - Run()이 namespace violations 시 *ViolationError 반환

controllers/dataplane_controller.go
  - Finalizer(bori.dev/cleanup): 첫 reconcile에 추가, 삭제 시 제거
    shadow state + revision history는 디스크에 보존
  - Generation-aware reconcile:
    ObservedGeneration > 0 && ObservedGeneration == Generation && !isUnhealthy
    이면 Runner.Run() skip → 30s RequeueAfter만 반환
  - reconcileViolation(): ViolationError → Violation=True + Degraded=True condition
    에러 backoff 없이 5분 requeue
  - isUnhealthy(): Degraded=True 또는 Violation=True이면 항상 full reconcile
  - setCondition(): Status 변경 시만 LastTransitionTime 갱신
  - 모든 event 메시지에 security.RedactString() 적용

controllers/dataplane_controller_test.go
  - TestReconcile_addsFinalizer: 첫 reconcile → bori.dev/cleanup 추가
  - TestReconcile_handlesDeletion: DeletionTimestamp → finalizer 제거
  - TestReconcile_skipsIfGenerationMatches: Gen 일치 + 정상 → runner skip
  - TestReconcile_namespaceViolation: ViolationError → condition 설정, no error

config/operator/
  - namespace.yaml: bori-system Namespace
  - configmap.yaml: bori-root, bori-dir, requeue-interval 설정
  - deployment.yaml: bori-operator Deployment
    liveness/readiness probe, securityContext, resource limits, hostPath volume

config/crd/boridataplanes.bori.dev.yaml
  - status.observedGeneration 필드 추가
  - ObservedGen printer column 추가

Makefile
  - make deploy:          CRD + RBAC + operator 한 번에 배포
  - make undeploy:        역순 제거
  - make deploy-dry-run:  전체 매니페스트 dry-run 검증
```

완료 기준:

- [x] `make deploy` 후 kind 클러스터에서 bori-operator pod Running 가능
- [x] BoriDataPlane 삭제 → finalizer cleanup → 오브젝트 제거
- [x] 허용되지 않은 namespace → Violation condition, Degraded=True (에러 아님)
- [x] event 메시지에 secrets 미노출
- [x] 컨트롤러 테스트 8개 통과 (4개 Phase 7 + 4개 Phase 8 신규)

### Phase 9 — BoriRelease CRD ✅ 완료 (2026-06-06)

~~예상 기간: Phase 8 직후~~  
실제 완료: 2026-06-06

목표:

- `BoriRelease`를 파일시스템 YAML에서 Kubernetes CR로 승격
- operator가 K8s API에서 릴리즈 정의를 읽음 (파일시스템 fallback 유지)
- `BoriRelease` 변경 → 참조하는 `BoriDataPlane` 자동 reconcile 트리거
- `bori release apply` CLI 서브커맨드 추가

비목표:

```text
- BoriRevision CRD (Phase 10 후보)
- BoriVerificationRun CRD (Phase 10 후보)
- traffic routing / progressive delivery
- kube-slint Track K0-K5 (별도 트랙 유지)
```

산출물:

```text
apis/bori/v1alpha1/release_types.go
  - BoriRelease + BoriReleaseList — CRD 타입
  - BoriReleaseSpec: components[], compatibility, verification, promotion
  - BoriReleaseStatus: observedGeneration, activeDataPlanes, observedAt
  - ToModel(): BoriRelease CR → pkg/model.BoriRelease
  - FromModelRelease(): pkg/model.BoriRelease → BoriRelease CR (CLI 사용)

apis/bori/v1alpha1/release_deepcopy.go
  - DeepCopyObject/DeepCopyInto for BoriRelease + BoriReleaseList

apis/bori/v1alpha1/register.go
  - BoriRelease + BoriReleaseList SchemeBuilder 등록

config/crd/borireleases.bori.dev.yaml
  - BoriRelease CRD (group: bori.dev, scope: Namespaced, shortName: br)

config/rbac/role.yaml
  - borireleases get/list/watch 권한 추가

pkg/reconcile/reconciler.go
  - Request.Release *model.BoriRelease — nil이면 파일시스템 fallback

pkg/planner/planner.go
  - Planner.Release *model.BoriRelease — 동일 override 패턴

controllers/dataplane_controller.go
  - resolveRelease(): K8s API → nil이면 파일시스템 fallback
  - SetupWithManager(): Watches(BoriRelease) → findDataPlanesForRelease
  - findDataPlanesForRelease(): BoriRelease 변경 → 참조 BDP enqueue

controllers/dataplane_controller_test.go
  - TestResolveRelease_fromKubernetesAPI
  - TestResolveRelease_filesystemFallback
  - TestReconcile_injectsResolvedRelease
  - TestReconcile_findDataPlanesForRelease

cmd/bori/main.go
  - bori release apply --name <name> [--namespace <ns>] [--apply]
```

완료 기준:

- [x] `kubectl apply` 로 BoriRelease CR 생성 → operator가 이를 사용해 reconcile
- [x] BoriRelease spec 변경 → 연관 BoriDataPlane 자동 재실행
- [x] 기존 `bori deploy --release <name>` 은 파일시스템 fallback으로 그대로 동작
- [x] `bori release apply` 로 기존 YAML을 CR로 변환 가능
- [x] 컨트롤러 테스트 12개 통과 (8개 Phase 7/8 + 4개 Phase 9 신규)

비고:

- `network-baseline` 저장소(타 agent 개발 중)가 통합 계약 문서를 준비 중임.
  NetworkBaselineRun CRD가 안정화되면 Phase 10 또는 별도 Track으로 추가 예정.

---

## 13. 첫 PR 제안

### PR-1: Establish bori control-plane transition baseline

포함:

```text
- docs/architecture.md 업데이트
- docs/control-plane-roadmap.md 추가
- docs/agent-contract.md 추가
- docs/security-model.md 추가
- docs/verification-model.md 추가
- docs/kube-slint-integration.md 추가
- docs/migration-inventory.md 추가
- README 방향성 업데이트
```

비포함:

```text
- operator 구현
- CRD 구현
- rollout controller 구현
- full deploy controller 구현
```

### PR-2: Align current bori adapter with safe run artifacts

포함:

```text
- schema/component parser 정합성 수정
- image 필드 보존
- namespace default/validation 명시
- http client timeout/context/status code 처리
- port-forward process cleanup 개선
- 실패 시에도 status.json 생성
- redaction utility skeleton
```

### PR-3: Add bori CLI skeleton

포함:

```text
- cmd/bori
- bori plan
- bori verify
- run archive 생성
- provider interface skeleton
```

### PR-4: Add kube-slint verification provider

포함:

```text
- provider: kube-slint
- slint-gate CLI 호출
- measurement summary/gate summary 수집
- PASS/WARN/FAIL/NO_GRADE mapping
- failOn 정책 주입 또는 bori-side decision
- --fail-on NEVER + JSON 판정 경로 검토
```

### PR-5: First app absorption

포함:

```text
- artifact-handoff 또는 JUMI component.yaml
- environment kind/multipass
- 기존 deploy flow adapter wrapping
- 실제 bori verify 실행 결과 artifact
```

### kube-slint PR-K1: Gate strictness hardening

포함:

```text
- schemaVersion 검증
- summary.Validate 추가
- unknown schema => NO_GRADE
- tests
```

### kube-slint PR-K2: Result status and counter reset policy

포함:

```text
- SLIResult.Status gate 반영
- counter reset policy 추가
- JUMI churn spec에 no_grade 적용 후보
```

### kube-slint PR-K3/K4: Curlpod security and evidence redaction

포함:

```text
- run-id label
- cleanup evidence
- redaction utility
- RBAC docs
```

---

## 14. 주요 리스크와 대응

### 리스크 1: bori가 script 저장소로 변질됨

대응:

- 모든 실행은 model → plan → adapter → artifact 순서를 거친다.
- scripts 디렉토리에 직접 deploy script를 쌓지 않는다.

### 리스크 2: bori가 kube-slint를 재구현함

대응:

- bori는 SLI engine이 아니다.
- kube-slint/slint-gate를 공식 verification backend로 사용한다.
- bori 내부 parser/delta는 compatibility shim으로만 둔다.
- summary schema는 kube-slint를 단일 출처로 둔다.

### 리스크 3: NO_GRADE가 promotion gate를 통과함

대응:

- `VerificationPolicy.failOn`을 bori에 명시한다.
- slint-gate exit code만 믿지 않는다.
- 가능하면 `--fail-on NEVER`로 JSON 결과를 생성하고 bori가 직접 판정한다.
- promotion profile에서는 `FAIL_OR_NOGRADE`를 기본값으로 검토한다.

### 리스크 4: operator를 너무 빨리 시작함

대응:

- Phase 6 전까지 operator는 시작하지 않는다.
- Phase 6도 shadow mode로만 시작한다.

### 리스크 5: 보안 모델을 나중에 붙임

대응:

- PR-1에 security-model.md를 포함한다.
- smoke command, secrets, RBAC, namespace authorization, run archive redaction을 초기부터 문서화한다.
- kube-slint curlpod/evidence 보안도 별도 트랙으로 관리한다.

### 리스크 6: JUMI upgrade gate가 health check에 머무름

대응:

- JUMI churn gate를 별도 verification policy로 둔다.
- Kubernetes object churn, cleanup, ownerReference, retry, API write pressure를 본다.
- metric 기반 MVP부터 시작하고, k8s_object_snapshot source로 확장한다.

### 리스크 7: 운영 관측 전환 비용을 과소평가함

대응:

- source 모델을 현재 2점 engine 가능 범위와 engine 확장 필요 범위로 나눈다.
- PromQL/soak/burn-rate는 별도 kube-slint engine 확장 트랙으로 둔다.
- bori 일정에는 운영 관측 확장을 과도하게 앞당겨 넣지 않는다.

---

## 15. 최종 정리

bori의 방향은 다음 문장으로 고정한다.

```text
bori는 여러 genomic dataplane app과 여러 개발 agent 사이에 위치하는
통일된 deploy/verify/control-plane gateway다.

초기에는 CLI + adapter 방식으로 기존 shell/ko/kustomize/devspace 흐름을 흡수한다.

검증 엔진은 kube-slint/slint-gate를 공식 backend로 사용한다.

bori는 SLI 계산기를 재구현하지 않고,
release/rollout/promotion decision에 verification result를 연결한다.

summary schema는 kube-slint가 단일 출처로 소유하고,
bori는 그 결과를 BoriVerificationRun으로 감싸서 보존한다.

JUMI처럼 Kubernetes object churn을 유발하는 dataplane control component는
단순 health gate가 아니라 churn gate를 통과해야 promotion될 수 있다.

이 모델이 충분히 안정되면 operator shadow mode를 거쳐
Knative-style operator control plane으로 승격한다.
```
