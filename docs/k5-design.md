# K5 — k8s_object_snapshot MVP 설계

작성일: 2026-06-02  
대상 저장소: kube-slint  
bori 연결 Phase: Phase 3.5 (JUMI Churn Gate MVP)  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md) §5.3, §6.6, §7 K5

---

## 목적

JUMI executor → spawner → client-go 경로가 만드는 Kubernetes object churn을 metric 외부에서도 관측한다.

JUMI/spawner metric만으로는 실제 Kubernetes API 상태를 신뢰할 수 없다. object snapshot은 metric이 리셋되거나 누락될 때도 실제 orphan/stuck/cleanup 상태를 직접 확인할 수 있게 한다.

---

## MVP 결정

> K5 MVP는 **별도 engine 경로를 만들지 않는다**.  
> Kubernetes object를 수집한 뒤 scalar SLI metric으로 환원하고,  
> raw object metadata는 evidence로 보존한다.

```text
metric path:
  map[string]float64 → 기존 kube-slint 2점 engine 재사용

evidence path:
  raw Kubernetes object metadata JSON
  orphan / stuck / ownerRef missing object 목록
  debugging / audit / run archive 용도
```

---

## Source 동작 설계

### Input

```text
namespace:        측정 대상 namespace
includeSelector:  label selector — 측정 대상 리소스 필터
excludeSelector:  verification helper resource 제외용
  예: bori.dev/verification-helper=true 제외
resources:
  - pods
  - jobs
  - configmaps
  - events (optional)
```

### 흐름

```text
1. before snapshot
   - namespace object list (label selector 적용)
   - ownerReference 수집
   - deletionTimestamp 확인
   - object count per kind

2. synthetic pipeline 실행 또는 smoke 대기
   - JUMI가 spawner를 통해 object 생성

3. cleanup grace period 대기

4. after snapshot
   - 동일 조건으로 object list
   - diff 계산 (created / deleted / remaining)
   - orphan 판정: ownerReference 없는 Pod/Job
   - stuck terminating 판정: deletionTimestamp 있고 N초 경과
   - ownerReference 누락 판정

5. scalar SLI metric 생성
   - diff/orphan/stuck 결과를 map[string]float64로 환원
   - 기존 kube-slint 2점 engine으로 평가

6. evidence 저장
   - before-snapshot.json: 수집 원본 object 목록
   - after-snapshot.json: 수집 원본 object 목록
   - churn-summary.json: diff 요약
   - orphan-objects.json: orphan 판정된 object 목록
   - stuck-objects.json: stuck terminating 판정된 object 목록
   - ownerref-missing.json: ownerReference 누락 목록
```

---

## 생성할 scalar SLI metric 후보

```text
jumi_k8s_jobs_created_delta         jobs before→after delta
jumi_k8s_pods_created_delta         pods before→after delta
jumi_k8s_configmaps_created_delta   configmaps before→after delta
jumi_k8s_objects_remaining_end      after snapshot 전체 잔존 count
jumi_k8s_orphan_objects_end         orphan 판정 object 수
jumi_k8s_stuck_terminating_end      stuck terminating 판정 수
jumi_k8s_ownerref_missing_end       ownerReference 누락 수
jumi_k8s_cleanup_duration_seconds   before→cleanup completion 시간
jumi_k8s_warning_events_delta       warning event before→after delta
```

---

## Verification helper 제외 label 표준

verification 자체가 만드는 object가 churn 측정을 오염시키지 않도록 label을 표준화한다.

```text
verification helper resource에 붙여야 할 label:
  bori.dev/run-id=<run-id>
  bori.dev/verification-helper=true
  kube-slint.dev/run-id=<run-id>
  app.kubernetes.io/managed-by=bori

k8s_object_snapshot excludeSelector:
  bori.dev/verification-helper=true 존재 시 제외
  kube-slint.dev/run-id 존재 시 제외
```

---

## kube-slint 구현 작업 항목

```text
pkg/source/k8sobject/
  snapshot.go          — object list + ownerRef + deletionTimestamp 수집
  diff.go              — before/after diff 계산
  classify.go          — orphan / stuck / ownerRefMissing 판정
  metrics.go           — scalar SLI metric 생성 (map[string]float64)
  evidence.go          — raw object JSON evidence 저장

pkg/source/k8sobject/selector.go
  — include/exclude label selector 처리
  — verification helper 제외 로직

integration test:
  fake client-go + before/after scenario
  orphan 판정 검증
  stuck terminating 판정 검증
```

---

## bori Phase 3.5 연결

K5 완료 후 bori Phase 3.5에서:

1. JUMI churn BoriVerificationPolicy 작성:

```yaml
name: jumi-upgrade-churn-gate
provider: kube-slint
mode: cli
policy: .bori/churn-policy.{profile}.yaml
failOn: FAIL_OR_NOGRADE
blocking: true
```

2. bori verify가 이 policy를 통해 kube-slint k8s_object_snapshot source를 호출
3. evidence를 run archive에 포함

---

## K5 이전 fallback (metric 기반 churn gate)

K5가 완료되기 전에 Phase 3.5는 metric 기반 JUMI churn gate를 먼저 구현한다.

```text
JUMI/spawner가 노출하는 metric을 before/after로 측정
  → jumi_*_total counter delta SLI
  → kube-slint policy threshold 평가
  → counter reset 발생 시 NO_GRADE (K2 onCounterReset 설정)
```

K5 완료 후 object snapshot source로 점진적 전환한다.

---

## 참고 문서

- [control-plane-roadmap.md](control-plane-roadmap.md) §5.3, §6.6, §7 K5
- [kube-slint-integration.md](kube-slint-integration.md) §K5
- [verification-model.md](verification-model.md) §k8s_object_snapshot MVP 범위
