# bori Migration Inventory

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## 개요

각 dataplane app의 현재 배포/검증 방식과 bori 흡수 우선순위를 정리한다.

bori가 해당 path를 지원하기 시작하면, 기존 방식은 adapter 아래로 들어와야 한다. 직접 실행은 개발 편의 목적으로만 허용된다.

---

## App별 현황

### JUMI

| 항목 | 현재 상태 |
|------|----------|
| 배포 방식 | DevSpace / ko build + kubectl apply |
| 검증 방식 | bori-devspace (metrics scrape + slint-gate) |
| `.bori/component.yaml` | 있음 |
| `.bori/policy.yaml` | 있음 |
| smoke primitive | shell command (`--smoke-cmd`) |
| bori 흡수 우선순위 | **2순위** (churn gate 필요하므로 AH 이후) |

**흡수 시 주의사항:**
- JUMI는 executor → spawner → client-go 경로로 Kubernetes object를 동적으로 생성한다.
- health gate만으로는 부족하다. churn gate가 반드시 필요하다.
- Phase 3.5에서 JUMI churn gate MVP를 구현한 뒤 promotion gate에 연결한다.

**bori 흡수 후 JUMI revision promotion 조건:**
```text
- health gate PASS
- smoke gate PASS
- kube-slint SLI gate PASS
- JUMI churn gate PASS
- no blocking security findings
```

---

### artifact-handoff (AH)

| 항목 | 현재 상태 |
|------|----------|
| 배포 방식 | DevSpace / ko build + kubectl apply |
| 검증 방식 | bori-devspace (metrics scrape + slint-gate) |
| `.bori/component.yaml` | 있음 |
| `.bori/policy.yaml` | 있음 |
| smoke primitive | shell command |
| bori 흡수 우선순위 | **1순위** |

**흡수 이유:**
- JUMI보다 runtime orchestration 위험이 낮다.
- bori absorption path 검증에 가장 적합하다.
- Phase 3의 pilot 대상으로 추천한다.

---

### node-artifact-runtime (nan)

| 항목 | 현재 상태 |
|------|----------|
| 배포 방식 | 확인 필요 |
| 검증 방식 | 확인 필요 |
| `.bori/component.yaml` | 미확인 |
| `.bori/policy.yaml` | 미확인 |
| smoke primitive | 확인 필요 |
| bori 흡수 우선순위 | **3순위** (AH, JUMI 이후) |

**TODO:** nan 배포/검증 방식 현황 조사 필요.

---

### tori

| 항목 | 현재 상태 |
|------|----------|
| 배포 방식 | 확인 필요 |
| 검증 방식 | 확인 필요 |
| `.bori/component.yaml` | 미확인 |
| `.bori/policy.yaml` | 미확인 |
| smoke primitive | 확인 필요 |
| bori 흡수 우선순위 | **5순위** |

**TODO:** tori 배포/검증 방식 현황 조사 필요.

---

### NodeSentinel

| 항목 | 현재 상태 |
|------|----------|
| 배포 방식 | 확인 필요 |
| 검증 방식 | 확인 필요 |
| `.bori/component.yaml` | 미확인 |
| `.bori/policy.yaml` | 미확인 |
| smoke primitive | 확인 필요 |
| bori 흡수 우선순위 | **4순위** |

NodeSentinel은 2026-06-02에 추가된 Kubernetes 데이터 플레인 app이다.

**TODO:**
- NodeSentinel 배포/검증 방식 현황 조사 필요
- dependencies 확인 필요 (현재 없음으로 가정)
- contracts 확인 필요 (현재 `node-sentinel-v1`으로 임시 지정)

---

## 흡수 우선순위 요약

| 순위 | App | 이유 |
|-----|-----|------|
| 1 | artifact-handoff | runtime orchestration 위험 낮음, pilot 적합 |
| 2 | JUMI | 중요도 높으나 churn gate 필요 (Phase 3.5 이후) |
| 3 | nan | AH/JUMI 흡수 후 진행 |
| 4 | NodeSentinel | 2026-06-02 추가됨, 단독 컴포넌트로 흡수 가능 |
| 5 | tori | 마지막 |

---

## 현재 bori 코드 마이그레이션 항목

### 즉시 수정 필요 (PR-2)

| 항목 | 문제 | 수정 방향 |
|------|------|----------|
| `loadComponent()` | `image` 필드 무시 | image 필드 보존 |
| namespace default | schema에는 app name이 default라 명시되어 있으나 코드 미적용 | default 적용 |
| HTTP client | context/timeout 미적용, status code 미확인 | context 사용, status code 체크 |
| port-forward cleanup | context 취소에만 의존 | 명시적 cleanup |
| 실패 시 artifact | 실패해도 status.json 생성 안 됨 | 항상 status.json 생성 |

### 중기 수정 필요 (Phase 1.5)

| 항목 | 문제 | 수정 방향 |
|------|------|----------|
| `--fail-on FAIL` 고정 | NO_GRADE를 조용히 통과시킬 위험 | `VerificationPolicy.failOn` 주입 |
| `parsePromText` | 운영 관측에 부적합한 임시 parser | kube-slint backend 정렬 후 shim으로 격하 |
| `buildDeltaSummary` | 모든 metric을 단순 delta로 처리 | kube-slint summary schema로 전환 |
| smoke command | `sh -c` 직접 실행 | 구조화된 SmokeSpec으로 전환 |

---

## batch-integration에서 이전할 것

batch-integration은 전환 staging repo다. 다음 asset을 각 소유자에게 이전한다.

| Asset | 현재 위치 | 이전 대상 |
|-------|----------|----------|
| JUMI/AH smoke script | batch-integration | 각 app repo 또는 bori adapters/ |
| DevSpace orchestration | batch-integration | bori adapters/devspace/ |
| shared gate semantics | batch-integration | kube-slint |
| observability publish | batch-integration | SF Observability |

---

## 마이그레이션 완료 기준

각 app에 대해 다음 조건을 만족하면 마이그레이션 완료로 간주한다.

```text
1. components/<app>/component.yaml이 bori에 존재한다.
2. bori plan/deploy/verify 명령이 해당 app을 지원한다.
3. 기존 script는 bori adapter를 통해서만 호출된다.
4. 실패 시에도 .bori/runs/<run-id>/status.json이 생성된다.
5. gate 없이 promotion되지 않는다.
```

---

## 참고 문서

- [architecture.md](architecture.md)
- [agent-contract.md](agent-contract.md)
- [sprint-schedule.md](sprint-schedule.md)
