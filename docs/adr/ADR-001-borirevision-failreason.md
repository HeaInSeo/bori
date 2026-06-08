# ADR-001 — BoriRevision.failReason 필드 위치

| 항목 | 내용 |
|------|------|
| **상태** | 결정 보류 (Pending) |
| **등록일** | 2026-06-08 |
| **관련 파일** | `apis/bori/v1alpha1/revision_types.go`, `pkg/revision/revision.go`, `config/crd/borirevisions.bori.dev.yaml` |
| **관련 Known Issue** | KI-001 |

---

## 배경

`pkg/revision.BoriRevision` (디스크 기록)에는 `FailReason string` 필드가 있다.
deploy 실패 시 `revision.Fail()` 함수가 이 값을 채우고, `.bori/revisions/*.json`에 기록한다.

현재 `v1alpha1.BoriRevision` CR에는 이 필드가 없다.
따라서 `kubectl get borirevision -o yaml`로 deploy 실패 원인을 확인할 수 없다.

---

## 문제

`failReason`을 BoriRevision CR에 노출하려면 **spec** 또는 **status** 중 하나를 선택해야 한다.
이 두 선택지는 설계 철학이 다르며, 잘못된 선택은 CRD API 오염 또는 이력 보존 원칙 위반으로 이어진다.

---

## 선택지

### 선택지 A — `status.failReason`

```go
type BoriRevisionStatus struct {
    PromotionStatus string `json:"promotionStatus"`
    FailReason      string `json:"failReason,omitempty"`  // 추가
    ...
}
```

**장점:**
- 표준 CRD 패턴 — runtime 상태는 status에 두는 것이 관례
- `kubectl get borirevision -o jsonpath='{.status.failReason}'`으로 접근 가능
- controller-runtime `r.Status().Patch()`로 기록

**단점:**
- BoriRevision은 write-once 이력 리소스다. status 서브리소스는 mutable이므로 "이력은 불변"이라는 설계 원칙과 충돌한다.
- 향후 누군가 `r.Status().Patch()`로 failReason을 덮어쓸 수 있다.
- KI-001: `BoriRevisionStatus`를 status 서브리소스로 분리하는 작업이 아직 완료되지 않았다.

---

### 선택지 B — `spec.failReason`

```go
type BoriRevisionSpec struct {
    Release     string `json:"release"`
    Environment string `json:"environment"`
    ContentHash string `json:"contentHash"`
    FailReason  string `json:"failReason,omitempty"`  // 추가
    ...
}
```

**장점:**
- BoriRevision은 "배포 시점의 스냅샷"이다. 실패 이유는 그 스냅샷의 일부이므로 spec에 기록하는 것이 의미적으로 일관성 있다.
- spec은 immutable 취급이므로 write-once 원칙과 부합한다.
- `upsertBoriRevision()`이 CR 생성 시 한 번 기록하고 이후 수정하지 않는다.

**단점:**
- Kubernetes CRD 관례상 spec은 "desired state"다. 실패 이유가 desired state가 될 수 없다는 반론이 있다.
- API 리뷰어가 spec에 runtime 정보가 있는 것을 거부할 수 있다.

---

## 현재 상태

의사결정 이전까지:
- `pkg/revision.BoriRevision` (디스크)에 `FailReason` 유지 — CLI는 `.bori/revisions/*.json`으로 실패 이유 확인 가능
- `v1alpha1.BoriRevisionSpec`, `v1alpha1.BoriRevisionStatus` 모두에 추가하지 않음
- `config/crd/borirevisions.bori.dev.yaml`에서 `spec.failReason` 제거 (schema drift 방지)

---

## 결정 기준

다음 조건이 충족될 때 결정한다:
1. BoriRevision을 `controller-gen` 기반으로 전환할지 확정된 이후 (ADR-002 참조)
2. "실패한 BoriRevision에 대한 kubectl 조회 요구사항"이 실제 운영에서 발생한 이후

결정이 내려지면 이 ADR을 업데이트하고, `v1alpha1.BoriRevisionSpec` 또는 `BoriRevisionStatus`에 필드를 추가한 뒤 `revisionToCR()`에 매핑을 추가한다.
