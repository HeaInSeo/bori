# bori Agent Contract

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## 개요

bori가 특정 app/environment path를 지원하기 시작하면, 해당 path에서는 bori를 공식 gateway로 사용해야 한다.

전환 기간 동안에는 bori가 아직 지원하지 않는 path에 한해 기존 방식을 계속 사용할 수 있다.

---

## Agent가 할 수 있는 것

```text
- app source code 수정
- app-local minimal base manifest 수정
- Dockerfile / ko build input 수정
- app-specific smoke primitive 추가
- bori plan / deploy / verify / status 실행
- bori run artifact 확인
- .bori/component.yaml 추가 또는 수정
- .bori/policy.yaml 추가 또는 수정
```

---

## bori 지원 path에서 agent가 하면 안 되는 것

```text
- 공식 deploy path로 kubectl apply 직접 실행
- 공식 deploy path로 ko apply 직접 실행
- bori gate 없이 DevSpace deployment 실행
- app repo 안에 stale environment-specific manifest copy 생성
- verification gate 우회
- namespace, image, registry, secret 가정을 ad-hoc script 안에 숨기기
- slint-gate exit code만 믿고 bori run artifact 생성 생략
```

---

## 전환 기간 중 agent가 반드시 남겨야 하는 것

bori가 아직 해당 path를 지원하지 않는 경우, 다음 정보를 문서화해야 한다.

```text
- 어떤 script가 무엇을 배포하는지
- 어떤 environment를 전제하는지
- 어떤 image / ref를 사용하는지
- 어떤 namespace를 사용하는지
- 어떤 smoke / gate가 성공을 검증하는지
- 어떤 Kubernetes object가 생성되는지
- 어떤 output / log가 성공 또는 실패를 증명하는지
```

---

## bori 지원 범위 판단 기준

아래 조건을 모두 만족하면 bori 공식 path 사용 의무가 발생한다.

```text
1. 해당 app의 component.yaml이 components/ 아래에 존재한다.
2. 해당 environment의 environment.yaml이 environments/ 아래에 존재한다.
3. 해당 release의 release.yaml이 releases/ 아래에 존재한다.
4. bori plan / deploy / verify 명령이 해당 path를 지원한다.
```

위 조건을 만족하지 않으면, 기존 방식을 계속 사용할 수 있다.

---

## App self-registration 규칙

각 app repo는 다음 파일을 추가해 bori에 self-register한다.

```text
<app-repo>/
└── .bori/
    ├── component.yaml   # component spec
    └── policy.yaml      # kube-slint SLI 기준값 (profile별)
```

`component.yaml` 최소 필드:

```yaml
name: <app-name>
kind: control-component
version: <semver>
image:
  ref: <registry>/<repo>@sha256:<digest>
ports:
  metrics: 8080
  health: 8081
health:
  path: /healthz
metrics:
  path: /metrics
```

---

## bori CLI 기본 사용법

```bash
# 배포 계획 확인 (apply 없음)
bori plan --release <release-name> --env <env-name>

# 배포 실행
bori deploy --release <release-name> --env <env-name>

# 검증 실행
bori verify --release <release-name> --env <env-name>

# 실행 결과 확인
bori status --run <run-id>
```

---

## Run artifact 보존 의무

bori를 통해 실행한 모든 deploy / verify는 `.bori/runs/<run-id>/` 아래에 artifact를 남긴다.

```text
.bori/runs/<run-id>/
  request.yaml
  plan.json
  deploy-result.json
  verification-result.json
  status.json
  logs/
  evidence/
```

- 성공한 실행만 기록하면 안 된다. 실패한 실행일수록 artifact가 중요하다.
- artifact를 수동으로 삭제하거나 덮어쓰면 안 된다.

---

## JUMI revision upgrade 시 추가 규칙

JUMI는 Kubernetes object churn을 유발하는 component다. 따라서 JUMI revision upgrade 시 다음 gate를 모두 통과해야 promotion된다.

```text
- health gate PASS
- smoke gate PASS
- kube-slint SLI gate PASS
- JUMI churn gate PASS
- no blocking security findings
```

churn gate 없이 JUMI revision을 promotion하면 안 된다.

---

## 참고 문서

- [architecture.md](architecture.md)
- [security-model.md](security-model.md)
- [verification-model.md](verification-model.md)
- [migration-inventory.md](migration-inventory.md)
