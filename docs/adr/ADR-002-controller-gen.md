# ADR-002 — controller-gen 도입 여부 및 CRD schema drift 방지

| 항목 | 내용 |
|------|------|
| **상태** | 결정됨 — 선택지 A 채택 (controller-gen v0.21.0) |
| **등록일** | 2026-06-08 |
| **결정일** | 2026-06-08 |
| **관련 파일** | `config/crd/`, `apis/bori/v1alpha1/`, `Makefile`, `.github/workflows/kubeconform.yaml` |

---

## 배경

bori는 3개의 CRD를 수동으로 관리한다.

```
config/crd/
  boridataplanes.bori.dev.yaml
  borireleases.bori.dev.yaml
  borirevisions.bori.dev.yaml
```

Go 타입(`apis/bori/v1alpha1/`)에는 `+kubebuilder:*` 마커가 선언되어 있지만,
**controller-gen을 실행하는 CI 단계가 없다.** CRD YAML은 수동으로 작성하고 유지한다.

---

## 현재 문제

### schema drift 위험

Go 타입을 변경할 때 CRD YAML을 수동으로 업데이트하지 않으면 불일치가 발생한다.
실제 사례 (2026-06-08 코드 리뷰에서 발견):

| 항목 | Go 타입 | CRD YAML | 발견 경위 |
|------|---------|----------|-----------|
| `RevisionComponentRef.ImageDigest` | 없음 → 추가 (PR-1 gap fix) | 있음 | 수동 코드 리뷰 |
| `BoriRevisionSpec.FailReason` | 없음 | 있었음 → 제거 (ADR-001 결정 보류) | 수동 코드 리뷰 |
| `BoriRevisionStatus.NetworkVerification` | 없음 | 있었음 → 제거 (PR-2 이전 미구현) | 수동 코드 리뷰 |

### kubeconform CI의 한계

현재 kubeconform CI는 `-skip CustomResourceDefinition` 플래그를 사용한다.
CRD YAML 자체의 schema 유효성은 검증하지 않는다. operator/rbac manifest만 검증된다.

---

## 현재 drift 방지 방법

| 방법 | 적용 중 | 한계 |
|------|---------|------|
| `make deploy-dry-run` | O | `kubectl apply --dry-run=client`는 YAML 구조 유효성 확인, Go 타입과 비교 불가 |
| PR 코드 리뷰 | O | 사람에 의존, 누락 가능 |
| controller-gen | X | 도입 안 됨 |
| kubeconform CRD 검증 | X | `-skip CRD` 플래그로 비활성화 |

---

## 선택지

### 선택지 A — controller-gen 도입

`go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest`

Makefile에 `generate` 타겟 추가:
```makefile
generate:
    controller-gen crd:maxDescLen=0 paths="./apis/..." output:crd:artifacts:config=config/crd
    controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./apis/..."
```

CI에 `make generate && git diff --exit-code config/crd/` 추가.

**장점:**
- Go 타입이 CRD YAML의 단일 소스 — drift 원천 차단
- 마커에서 validation, printer columns 자동 생성
- DeepCopy도 자동 생성

**단점:**
- controller-gen 의존성 추가 (빌드 도구 관리)
- 생성된 CRD YAML의 세부 schema를 직접 통제하기 어려움
  (예: description, enum 값, 커스텀 validation)
- 기존 수동 CRD YAML에서 마이그레이션 작업 필요

---

### 선택지 B — 수동 관리 유지 + drift 검출 CI 추가

kubeconform의 `-skip CustomResourceDefinition`을 제거하거나,
별도 `validate-crd-schema` Makefile 타겟을 추가한다.

```makefile
validate-crd-schema:
    # CRD YAML 구조 유효성 (kubectl dry-run은 API server 없이 불가)
    # kubeconform에서 CRD 검증을 활성화하려면 스키마 레지스트리가 필요
    @echo "Manual process: after Go type changes, update config/crd/ and run make deploy-dry-run"
```

실질적인 자동 검증:
- CI에 `go vet ./apis/...` + `golangci-lint` (이미 있음)
- CRD 변경 PR에서 반드시 수동 diff 확인을 PR 체크리스트에 명시

**장점:**
- 도구 의존성 없음
- CRD YAML 세부 통제 유지

**단점:**
- 사람에 의존하는 프로세스 — 자동 검출 불가
- CRD가 3개에서 늘어나면 유지비용 증가

---

### 선택지 C — 하이브리드: controller-gen 생성 + 수동 보완

controller-gen으로 기본 구조를 생성하고, 특수 annotation(description, enum)은
별도 patch 파일로 유지한다. kustomize patch 활용.

**장점:** 자동화 + 통제 가능
**단점:** 복잡도 증가

---

## 결정 및 구현 내용

**선택지 A (controller-gen 도입)**을 채택한다. 구현 범위:

| 항목 | 내용 |
|------|------|
| 도구 버전 | `sigs.k8s.io/controller-tools v0.21.0` |
| 의존성 고정 | `tools/tools.go` (`//go:build tools`) |
| Makefile | `make generate`, `make generate-check` |
| CI | `.github/workflows/generate-check.yaml` — `apis/**` 변경 시 생성 파일 drift 검증 |
| 생성 파일 | `config/crd/bori.dev_*.yaml`, `apis/bori/v1alpha1/zz_generated.deepcopy.go` |

**알려진 제약:**
- controller-gen v0.21.0의 `object` 제너레이터는 `+kubebuilder:object:root=true` 타입만 DeepCopyInto를 생성한다.
  BoriDataPlaneStatus, BoriReleaseSpec 등 sub-type 메서드는 `apis/bori/v1alpha1/deepcopy_subtypes.go`에 수동 관리한다.
  sub-type 필드 변경 시 이 파일도 업데이트해야 한다.

---

## 이전 완화 조치 (이제 불필요)

**PR 체크리스트 항목 추가 (Go 타입 변경 시):**

```
- [ ] apis/bori/v1alpha1/ 변경이 있는가?
      있다면:
      - [ ] config/crd/ 해당 YAML을 업데이트했는가?
      - [ ] make deploy-dry-run 확인했는가?
      - [ ] CRD YAML과 Go 타입 필드를 수동 비교했는가?
```

이 체크리스트를 `docs/adr/ADR-002-controller-gen.md`(본 문서)에 명시하고,
PR description template이 생기면 포함한다.
