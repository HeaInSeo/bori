# bori Known Issues / 기술 부채

코드 리뷰에서 확인됐지만 이번 PR 범위에서 수정하지 않은 항목을 기록한다.
해결 시 해당 항목을 이 파일에서 제거하고 커밋 메시지에 "closes KI-NNN" 형식으로 참조한다.

---

## KI-001 — BoriRevision.FailReason — CR 노출 방법 미결정

| 항목 | 내용 |
|------|------|
| **위치** | `pkg/revision/revision.go`, `apis/bori/v1alpha1/revision_types.go` |
| **심각도** | Minor (CLI 흐름은 정상, kubectl 조회 불가) |
| **발견 시점** | 2026-06-08 코드 리뷰 (DESIGN-02) |
| **ADR** | [docs/adr/ADR-001-borirevision-failreason.md](adr/ADR-001-borirevision-failreason.md) |

### 문제

`pkg/revision.BoriRevision.FailReason`은 디스크(`.bori/revisions/*.json`)에 기록된다.
`v1alpha1.BoriRevisionSpec`과 `v1alpha1.BoriRevisionStatus`에는 이 필드가 없으므로
`kubectl get borirevision -o yaml`로 deploy 실패 원인을 확인할 수 없다.

`config/crd/borirevisions.bori.dev.yaml`에 있던 `spec.failReason` 선언은
Go 타입과 불일치(schema drift) 상태였으므로 제거됐다.

### 현재 상태

- CLI: `.bori/revisions/*.json` 파일로 `failReason` 확인 가능
- BoriRevision CR: `failReason` 없음
- CRD YAML: `spec.failReason` 제거 완료 (schema drift 해소)

### 해결 방향

ADR-001에서 결정한다. 두 가지 선택지:
- **선택지 A** `status.failReason` — CRD 관례 준수
- **선택지 B** `spec.failReason` — write-once history snapshot 의미론 준수

결정이 내려지면 `v1alpha1` 타입에 필드 추가 → `revisionToCR()` 매핑 추가.

---

## KI-002 — imageswap 어댑터: Deployment/컨테이너 이름 == 컴포넌트 이름 가정

| 항목 | 내용 |
|------|------|
| **위치** | `adapters/imageswap/adapter.go` — `Deploy()` |
| **심각도** | Major (이름이 다른 앱에는 배포 불가) |
| **발견 시점** | 2026-06-08 코드 리뷰 (EDGE-01) |

### 문제

현재 imageswap 어댑터는 Deployment 이름과 컨테이너 이름이 컴포넌트 이름과 동일하다고 가정한다.

```go
// adapters/imageswap/adapter.go
fmt.Sprintf("deployment/%s", name),      // Deployment 이름 = comp.Name
fmt.Sprintf("%s=%s", name, imageRef),    // 컨테이너 이름 = comp.Name
```

`kubectl set image deployment/jumi jumi=...` 형식이므로, Deployment 이름이 `jumi-server`이거나
컨테이너 이름이 `app`인 앱은 지원되지 않는다.

### 영향 범위

infra-lab의 현재 앱(JUMI, artifact-handoff, nan, tori, NodeSentinel)은
Deployment 이름과 컨테이너 이름이 컴포넌트 이름과 일치하므로 **현재는 영향 없음**.
새로운 앱 온보딩 시 네이밍 컨벤션이 다를 경우 배포 실패.

### 수정 방향

`pkg/model/component.go`의 `DeployConfig`에 선택적 필드를 추가한다:

```go
type DeployConfig struct {
    Adapter        string `yaml:"adapter,omitempty"`
    Namespace      string `yaml:"namespace,omitempty"`
    DeploymentName string `yaml:"deploymentName,omitempty"` // 비어 있으면 comp.Name 사용
    ContainerName  string `yaml:"containerName,omitempty"`  // 비어 있으면 comp.Name 사용
}
```

imageswap 어댑터에서:

```go
deploymentName := req.Component.Deploy.DeploymentName
if deploymentName == "" {
    deploymentName = name
}
containerName := req.Component.Deploy.ContainerName
if containerName == "" {
    containerName = name
}
```

### 해결 시점

PR-2 (netverify MVP) 이후, 새로운 앱을 imageswap으로 온보딩할 때 함께 추가한다.

---

## 항목 추가 가이드

새로운 기술 부채를 발견하면 이 파일에 추가하고 커밋한다.

```
## KI-NNN — 제목

| 항목 | 내용 |
|------|------|
| **위치** | 파일명:함수명 |
| **심각도** | Critical / Major / Minor / Nit |
| **발견 시점** | YYYY-MM-DD 출처 |

### 문제
...

### 수정 방향
...

### 해결 시점
...
```
