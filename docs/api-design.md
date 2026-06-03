# bori API 설계 문서

작성일: 2026-06-02  
Phase: 6 (Operator Shadow Mode)  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md) §Phase 6

---

## 개요

이 문서는 bori의 미래 Kubernetes API 타입 설계를 정의한다.

현재 이 타입들은 Kubernetes CRD로 등록되지 않는다.  
CRD 등록은 Phase 7(Limited Operator Apply Mode)에서 진행한다.

현재 단계(Phase 6)의 목표:
- CLI 모델과 동일한 데이터 구조를 API 타입으로 표현한다.
- Shadow mode reconciler가 이 타입들로 desired/actual state를 계산한다.
- CLI 모델과 operator 모델의 정합성을 검증한다.

---

## 타입 계층

```text
BoriDataPlane          (미래 CRD 후보)
  ├── spec.release      → releases/<name>/release.yaml
  ├── spec.environment  → environments/<name>/environment.yaml
  └── status
        ├── conditions  [Installed, Ready, Verified, Promoted, Degraded]
        ├── currentRevision
        └── components[]

BoriRelease            (현재 releases/<name>/release.yaml로 존재)
  ├── components[]     → ComponentRef (name + version)
  ├── compatibility.matrix
  ├── verification.policies[]
  └── promotion.requiredGateResult

BoriRevision           (현재 .bori/revisions/<id>.json으로 존재)
  ├── revisionId       (release-timestamp-hex6)
  ├── contentHash      (SHA256 of all component inputs)
  ├── components[]     (name + version + digests)
  ├── promotionStatus  (pending | promoted | rejected)
  └── baselineRef

BoriVerificationRun    (현재 evidence/<app>-verification-run.json으로 존재)
  ├── runId
  ├── provider         (kube-slint)
  ├── gateResult       (PASS | WARN | FAIL | NO_GRADE)
  └── promotionDecision (eligible | blocked)
```

---

## Status Conditions

모든 bori 리소스는 다음 표준 condition 집합을 사용한다.

| Type | True 의미 | False 의미 | Unknown 의미 |
|------|-----------|-----------|-------------|
| `Installed` | 모든 컴포넌트가 한 번 이상 배포됨 | 아직 배포되지 않음 | — |
| `Ready` | health gate 통과 | health 실패 | 아직 체크 안됨 |
| `Verified` | 최신 revision이 모든 gate 통과 | gate 실패 | verification run 없음 |
| `Promoted` | 최신 revision이 promotion됨 | — | — |
| `Degraded` | 하나 이상의 컴포넌트가 out-of-sync | 모두 in-sync | — |

---

## Shadow Mode 동작

Shadow mode reconciler는 다음 순서로 동작한다.

```text
1. releases/<name>/release.yaml 로드
   → desired component versions

2. .bori/revisions/ 스캔
   → most recently promoted revision (actual state)

3. 컴포넌트별 version 비교
   → DriftItem: in-sync | out-of-sync | unknown

4. Status Conditions 계산
   → Installed, Verified, Promoted, Degraded

5. ShadowState JSON 출력 또는 .bori/shadow-status.json 저장
```

Shadow mode는 클러스터에 아무것도 apply하지 않는다.  
오직 상태를 읽고 계산한 결과를 출력/저장한다.

---

## CLI 모델 ↔ Operator 모델 매핑

| CLI 명령 | Operator 동작 (Phase 7 후보) |
|---------|---------------------------|
| `bori plan --release X --env Y` | spec.release + spec.environment 읽기 |
| `bori deploy` | reconcile loop — deploy step |
| `bori verify` | reconcile loop — verify step |
| `bori shadow status` | status.conditions 계산 |
| `bori revision list` | status.currentRevision + history |
| `bori rollout plan` | rollout step 생성 |

---

## 미래 CRD 정의 (Phase 7 후보)

Phase 7에서 다음 CRD를 등록할 예정이다.

```text
boridataplanes.bori.dev        (namespaced)
borirevisions.bori.dev         (namespaced)
boriverificiationruns.bori.dev (namespaced)
```

현재는 `apis/bori/v1alpha1/types.go`에 Go 타입만 존재한다.  
Kubernetes `apiextensions.k8s.io/v1` CRD YAML은 Phase 7에 추가한다.

---

---

## Phase 7 — Limited Operator Apply Mode

Phase: 7 (준비 완료, 실구현 대기)  
대상 파일: `config/crd/`, `config/rbac/`, `controllers/`

### 준비 완료 산출물 (Phase 7 준비 커밋)

| 파일 | 내용 |
|------|------|
| `config/crd/boridataplanes.bori.dev.yaml` | BoriDataPlane CRD YAML |
| `config/rbac/service_account.yaml` | bori-operator ServiceAccount (namespace: bori-system) |
| `config/rbac/role.yaml` | ClusterRole — CRD CRUD + namespace 읽기 + leader election |
| `config/rbac/role_binding.yaml` | ClusterRoleBinding |
| `controllers/dataplane_controller.go` | DataPlaneReconciler 스켈레톤 |

### controller-runtime 추가 절차

Phase 7 실구현 시작 전에 한 번만 실행한다.

```bash
go get sigs.k8s.io/controller-runtime@latest
go mod tidy
```

이후 `controllers/dataplane_controller.go`의 stub 타입들을 실제 controller-runtime 타입으로 교체한다.

```go
// stub → real
import ctrl "sigs.k8s.io/controller-runtime"
import "sigs.k8s.io/controller-runtime/pkg/client"
```

### CRD 설치 / 제거

```bash
make install-crds    # kubectl apply -f config/crd/
make uninstall-crds  # kubectl delete -f config/crd/
```

### RBAC 설치

```bash
kubectl create namespace bori-system
kubectl apply -f config/rbac/
```

### DataPlaneReconciler 설계 원칙

- reconcile 루프는 `pkg/reconcile.Reconciler.Run()`을 통해서만 deploy를 수행한다.
- 컨트롤러는 plan/deploy/verify 로직을 재구현하지 않는다.
- 컨트롤러의 역할: CR spec 읽기 → reconcile.Request 변환 → Run() 호출 → CR status 패치.

```text
BoriDataPlane CR
  spec.release      ─┐
  spec.environment  ─┴→ reconcile.Request → Reconciler.Run() → status patch
```

### 완료 기준 (Phase 7)

- `kubectl apply -f` 로 BoriDataPlane CR 생성 시 operator가 deploy를 수행한다.
- `.status.conditions` 가 Kubernetes API 서버에 기록된다.
- NamespacePolicy 위반 시 deploy를 거부하고 Degraded=True condition을 설정한다.
- CLI(`bori deploy`)와 operator가 동일한 결과를 생성한다.

---

## 참고 문서

- [control-plane-roadmap.md](control-plane-roadmap.md) §Phase 6, §Phase 7
- [verification-model.md](verification-model.md)
- [security-model.md](security-model.md)
