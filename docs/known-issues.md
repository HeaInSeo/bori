# bori Known Issues / 기술 부채

코드 리뷰에서 확인됐지만 이번 PR 범위에서 수정하지 않은 항목을 기록한다.
해결 시 해당 항목을 이 파일에서 제거하고 커밋 메시지에 "closes KI-NNN" 형식으로 참조한다.

---

## KI-001 — FailReason spec/status 혼재

| 항목 | 내용 |
|------|------|
| **위치** | `pkg/revision/revision.go` — `BoriRevision.FailReason` |
| **심각도** | Nit (현재 동작에는 영향 없음) |
| **발견 시점** | 2026-06-08 코드 리뷰 (DESIGN-02) |

### 문제

`BoriRevision`은 현재 CRD의 spec + status를 단일 Go 구조체로 표현한다.
`FailReason`은 런타임 상태(status 영역)에 해당하지만, `PromotionStatus`와 같은 레벨에 선언되어 있다.

```go
// 현재 — spec/status 미분리
type BoriRevision struct {
    ...
    PromotionStatus string `json:"promotionStatus"`
    FailReason      string `json:"failReason,omitempty"`  // ← status 영역
    ...
}
```

controller-runtime으로 전환 시 `spec`/`status` 서브구조체로 분리해야 한다:

```go
// 목표 구조 (controller-runtime 전환 이후)
type BoriRevisionStatus struct {
    PromotionStatus string `json:"promotionStatus"`
    FailReason      string `json:"failReason,omitempty"`
    ...
}
```

### 해결 시점

Phase 11 이상에서 controller-runtime `status` 서브리소스로 전환할 때 함께 처리한다.
그 전까지는 현재 단일 구조체 방식으로 운영한다.

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
