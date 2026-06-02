# JUMI Churn Gate

작성일: 2026-06-02  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md) §6  
관련 문서: [verification-model.md](verification-model.md), [k5-design.md](k5-design.md)

---

## 왜 필요한가

JUMI는 executor에서 spawner를 통해 client-go로 Kubernetes API object를 동적으로 생성한다. JUMI Pod가 Ready 상태라는 사실만으로는 다음 문제를 감지할 수 없다.

```text
- 같은 pipeline에 대해 생성 object 수가 급증한다
- retry / attempt가 과도하게 증가한다
- 실패 후 cleanup이 되지 않는다 (orphan Pod/Job 남음)
- ownerReference가 빠진다
- terminating stuck object가 늘어난다
- Kubernetes API write pressure가 증가한다
- 이전 revision 대비 object churn이 커진다
```

따라서 JUMI revision promotion에는 별도의 churn gate가 필요하다.

---

## Gate 위치

churn gate는 bori가 직접 계산하지 않는다. kube-slint가 measurement를 담당한다.

```text
bori:
  - JUMI revision upgrade plan 생성
  - synthetic pipeline 실행 요청 (smoke 경유)
  - kube-slint churn policy 실행
  - result를 promotion decision에 연결

kube-slint:
  - metric 또는 k8s object snapshot 수집
  - churn SLI 계산
  - policy / baseline / regression 평가

JUMI / spawner:
  - runtime metric 노출
  - object label / ownerReference / correlation ID 제공
```

---

## bori verification policy

`verification/policies/jumi-upgrade-churn-gate.yaml`

```yaml
name: jumi-upgrade-churn-gate
provider: kube-slint
mode: cli
policy: .bori/churn-policy.{profile}.yaml
blocking: true
failOn: FAIL_OR_NOGRADE
```

- `blocking: true` — FAIL 또는 NO_GRADE 시 bori가 verification 전체를 즉시 중단하고 promotion을 차단한다.
- `failOn: FAIL_OR_NOGRADE` — counter reset이 발생해 데이터를 신뢰할 수 없는 경우에도 blocking이 적용된다.

---

## 다중 policy 실행 순서

`components/jumi/component.yaml`의 `verificationPolicies` 순서대로 실행된다.

```yaml
verificationPolicies:
  - jumi-ah-smoke          # 1. standard SLI smoke gate
  - jumi-upgrade-churn-gate # 2. churn gate (blocking)
```

sli-summary.json은 한 번만 생성되고, 각 policy가 같은 summary를 다른 kube-slint policy 파일로 평가한다.

---

## MVP 경로 A: metric 기반 churn gate

JUMI / spawner가 노출하는 Prometheus metric을 before/after로 측정한다.

### JUMI/spawner가 노출해야 할 metric

```text
jumi_k8s_objects_created_total{kind}
jumi_k8s_objects_deleted_total{kind}
jumi_k8s_apply_errors_total
jumi_orphan_objects_detected_total
jumi_cleanup_backlog_objects
jumi_ownerref_missing_total
jumi_stuck_terminating_objects
jumi_attempt_retries_total
```

### kube-slint SLI 후보 (MVP)

```text
jumi_k8s_jobs_created_delta
jumi_k8s_pods_created_delta
jumi_k8s_objects_remaining_end      # after 시점 잔존 object count
jumi_k8s_orphan_objects_end
jumi_k8s_stuck_terminating_end
jumi_k8s_ownerref_missing_end
```

### app-local churn policy 예시 (`.bori/churn-policy.kind.yaml`)

```yaml
slis:
  - id: jumi_k8s_jobs_created_delta
    threshold:
      max: 10        # 단일 pipeline smoke에서 10개 이상 → WARN
  - id: jumi_k8s_orphan_objects_end
    threshold:
      max: 0         # orphan이 1개라도 있으면 FAIL
  - id: jumi_k8s_stuck_terminating_end
    threshold:
      max: 0
  - id: jumi_k8s_ownerref_missing_end
    threshold:
      max: 0
```

---

## Counter reset 정책

JUMI가 재시작되거나 metric이 리셋되면 before/after delta가 음수가 된다.

| profile | counter reset 처리 | 이유 |
|---------|-------------------|------|
| local/dev | WARN | 개발 속도 우선 |
| integration | NO_GRADE | 데이터 신뢰 불가 |
| promotion | NO_GRADE (blocking) | 실제 churn 위반을 놓칠 수 있음 |

kube-slint K2 (`onCounterReset: no_grade`) 완료 전 bori 임시 정책:
- `failOn: FAIL_OR_NOGRADE` + `blocking: true` 조합으로 NO_GRADE를 blocking으로 처리한다.

---

## MVP 경로 B: k8s_object_snapshot source

중기 목표. kube-slint K5 완료 후 사용 가능. 설계는 [k5-design.md](k5-design.md) 참조.

---

## Baseline 관리

`verification/baselines/jumi-churn-baseline.json`은 promoted BoriRevision의 evidence에서 파생된다.

```text
BoriRevision jumi-v0.3.0 promoted
  -> churn gate PASS
  -> sli-summary.json → jumi-churn-baseline.json으로 등록
  -> bori가 source revision / promotedAt 기록
```

baseline이 없는 상태에서 regression 비교는 skip된다. kube-slint policy에 absolute threshold만 설정하면 baseline 없이도 gate는 동작한다.

---

## Promotion 조건

JUMI revision은 다음 조건을 모두 만족해야 promotion된다.

```text
health gate PASS         (ready / liveness probe)
smoke gate PASS          jumi-ah-smoke (standard SLI)
churn gate PASS          jumi-upgrade-churn-gate (blocking)
no blocking security findings
```

churn gate가 NO_GRADE 또는 FAIL이면 bori가 즉시 verification을 중단하고 `status.json`에 `phase: Failed`를 기록한다.

---

## 측정 오염 방지

verification 자체가 만드는 Kubernetes object가 churn 측정을 오염시키지 않도록 label을 붙인다.

```text
bori.dev/verification-helper=true    → churn 측정에서 제외
bori.dev/run-id=<run-id>
kube-slint.dev/run-id=<run-id>
```

---

## 참고 문서

- [control-plane-roadmap.md](control-plane-roadmap.md) §6
- [k5-design.md](k5-design.md) — k8s_object_snapshot MVP 설계
- [verification-model.md](verification-model.md) §NO_GRADE 정책
- [kube-slint-integration.md](kube-slint-integration.md) §K2
