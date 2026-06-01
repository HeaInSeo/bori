# bori Architecture

## 정체성

bori는 genomic dataplane app set을 위한 **전환형 control plane gateway**다.

```text
bori v0.x = agent-facing unified deploy/verify gateway
```

현재 단계의 목표는 operator 구현이 아니다. 여러 agent가 각각의 dataplane app을 개발하더라도, 배포와 검증은 bori라는 통일된 entrypoint를 통해 수행하게 만드는 것이다.

## 진화 경로

```text
현재:
  agent -> app별 shell/ko/kustomize/devspace -> Kubernetes

bori v0.x:
  agent -> bori CLI -> component/environment/release/verification model -> adapters -> Kubernetes

bori v1.x 후보:
  agent/user -> BoriDataPlane/BoriRelease CR -> bori operator -> Kubernetes
```

## 현재 동작 (DevSpace adapter 경로)

```text
App repos (JUMI, artifact-handoff, nan, tori, ...)
  each with .bori/component.yaml + .bori/policy.yaml
        ↓
bori-devspace (adapters/devspace/)
        ↓
kubectl port-forward -> /metrics scrape (before)
        ↓
smoke command 또는 wait
        ↓
/metrics scrape (after) -> sli-summary.json
        ↓
slint-gate --fail-on FAIL
        ↓
PASS / FAIL / NO_GRADE -> run artifact
```

## 목표 동작 (bori CLI 경로)

```text
agent
  -> bori plan --release <release> --env <env>
  -> bori deploy --release <release> --env <env>
  -> bori verify --release <release> --env <env>
  -> bori status --run <run-id>
        ↓
BoriDeployPlan 생성
        ↓
adapter 실행 (devspace / ko / kustomize / shell)
        ↓
kube-slint provider -> slint-gate
        ↓
BoriVerificationRun 저장
        ↓
promotion decision
        ↓
.bori/runs/<run-id>/status.json
```

## 역할 분리

```text
bori:
  - 어떤 app set을 어떤 environment에 배포할지 결정
  - component / release / revision / rollout / promotion 관리
  - 어떤 verification policy를 실행할지 결정
  - verification result를 promotion decision에 연결
  - run artifact와 상태를 저장

kube-slint:
  - SLI measurement summary 생성
  - policy / baseline / regression / reliability 평가
  - slint-gate-summary.json 생성

app repos (JUMI, artifact-handoff, nan, tori):
  - app source code
  - Dockerfile / ko build input
  - app-local smoke primitive
  - app business / runtime semantics
```

## Self-registration convention

각 dataplane app repo는 `.bori/component.yaml`을 추가해 bori에 self-register한다.

```text
<app-repo>/
└── .bori/
    ├── component.yaml   # component spec (name, image, ports, health, metrics, policies)
    └── policy.yaml      # kube-slint SLI thresholds per profile
```

bori는 이 파일을 runtime에 탐색한다. 중앙 registry를 수동으로 업데이트할 필요 없다.

## 목표 디렉토리 구조

```text
bori/
  cmd/
    bori/           # unified CLI
    bori-devspace/  # DevSpace compatibility adapter

  components/       # managed component registry
  environments/     # environment overlay
  releases/         # release version set
  verification/
    policies/       # BoriVerificationPolicy
    baselines/      # promoted revision evidence

  adapters/         # devspace / ko / kustomize / shell
  pkg/
    model/
    planner/
    adapter/
    verification/
    artifact/
    security/

  docs/
```

## Profile model

| Profile | Environment | 용도 |
|---------|-------------|------|
| `devspace` | DevSpace inner loop on K8s VM | primary dev target |
| `kind` | local kind cluster | CI / offline |
| `multipass` | Multipass VM | vm-lab |

## batch-integration과의 관계

`batch-integration`은 전환 staging repo이며 장기 소유자가 아니다.

- app-specific DevSpace / `.bori/` asset은 각 app repo에 남는다.
- DevSpace → kube-slint orchestration은 `bori`가 소유한다.
- shared gate semantics는 `kube-slint`가 소유한다.
- observability publish / runtime asset은 `SF Observability` operating path가 소유한다.

bori는 permanent adapter layer이고, `batch-integration`은 temporary staging이다.

## 참고 문서

- [control-plane-roadmap.md](control-plane-roadmap.md) — 전환 기획서 v0.5
- [sprint-schedule.md](sprint-schedule.md) — 스프린트 일정
- [agent-contract.md](agent-contract.md) — agent 행동 규칙
- [security-model.md](security-model.md) — 보안 모델
- [verification-model.md](verification-model.md) — verification source 모델
- [kube-slint-integration.md](kube-slint-integration.md) — kube-slint 통합 가이드
- [migration-inventory.md](migration-inventory.md) — 마이그레이션 현황
