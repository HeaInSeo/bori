# bori — 유전체 데이터플레인 컨트롤 플레인

[![golangci-lint](https://github.com/HeaInSeo/bori/actions/workflows/golangci-lint.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/golangci-lint.yaml)
[![kube-linter](https://github.com/HeaInSeo/bori/actions/workflows/kubelint.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kubelint.yaml)
[![kubeconform](https://github.com/HeaInSeo/bori/actions/workflows/kubeconform.yaml/badge.svg)](https://github.com/HeaInSeo/bori/actions/workflows/kubeconform.yaml)

bori는 유전체 데이터플레인 앱(JUMI, artifact-handoff, nan, tori, NodeSentinel)의 생명주기를 관리하는 Kubernetes 오퍼레이터입니다.

`BoriDataPlane` 커스텀 리소스를 조정하고, `BoriRevision`으로 배포 이력을 추적하며, [kube-slint](https://github.com/HeaInSeo/kube-slint)의 `slint-gate`를 통해 프로모션을 게이팅합니다.

English document: [README.md](README.md)

---

## 아키텍처

```
BoriDataPlane CR  →  bori-operator  →  deploy / verify / promote
                           │
                    BoriRelease (release.yaml)
                    BoriRevision (불변 이력)
                           │
                    slint-gate (kube-slint)
                      └── sli-summary.json → PASS / FAIL / WARN
```

### 커스텀 리소스

| CRD | 역할 |
|-----|------|
| `BoriDataPlane` | 원하는 상태: 어떤 릴리스가 어떤 환경에서 실행되는지 |
| `BoriRelease` | 버전화된 컴포넌트 매니페스트 (jumi, artifact-handoff, nan, …) |
| `BoriRevision` | 불변 배포 스냅샷; kube-slint를 통해 프로모션 게이팅 |

### BoriDataPlane Conditions

| Condition | 의미 |
|-----------|------|
| `Installed` | 모든 릴리스 컴포넌트가 배포됨 |
| `Ready` | 모든 컴포넌트가 준비 상태 통과 |
| `Verified` | slint-gate 평가 결과 PASS |
| `Promoted` | Revision이 active로 승격됨 |
| `Degraded` | 하나 이상의 컴포넌트가 릴리스 정의와 불일치 |

---

## 빠른 시작

### 사전 준비

- Go 1.26+
- 대상 클러스터에 kubectl 설정됨
- PATH에 `slint-gate` 바이너리 ([kube-slint](https://github.com/HeaInSeo/kube-slint))
- 클러스터 호스트에 k8sgpt 설치 (`/usr/bin/k8sgpt`)

### 빌드

```bash
git clone https://github.com/HeaInSeo/bori.git
cd bori
make build          # bin/bori, bin/bori-operator 생성
```

### 오퍼레이터 이미지 빌드 (Docker 없이)

```bash
# buildah (RHEL/CentOS 환경)
buildah build -t localhost/bori-operator:latest .

# 클러스터 노드에 전송
podman save localhost/bori-operator:latest -o /tmp/bori-operator.tar
for node in 192.168.122.99 192.168.122.232 192.168.122.207; do
  scp /tmp/bori-operator.tar ubuntu@$node:/tmp/
  ssh ubuntu@$node "sudo ctr -n k8s.io images import /tmp/bori-operator.tar"
done
```

### 클러스터에 배포

`make deploy`는 CRD, RBAC, ConfigMap, Deployment를 적용한 뒤 자동으로 회귀 검사를 실행합니다.

```bash
make deploy
```

제거할 때:

```bash
make undeploy
```

---

## 릴리스와 환경 정의

`BoriRelease`는 `releases/<name>/release.yaml`에 위치합니다:

```yaml
name: jumi-ah-dev
components:
  - name: jumi
    version: v0.3.0
  - name: artifact-handoff
    version: v0.2.0
  - name: nan
    version: v0.1.5
verification:
  policies:
    - jumi-ah-smoke
```

환경 정의는 `environments/<name>/environment.yaml`에 위치합니다:

```yaml
name: infra-lab
cluster:
  kubeconfig: ${KUBECONFIG}
  context: kubernetes-admin@kubernetes
registry:
  default: ghcr.io/heainseo
```

`BoriDataPlane`을 적용하여 둘을 연결합니다:

```bash
kubectl apply -f testdata/fixtures/bdp-infra-lab-smoke.yaml
```

---

## kube-slint 연동

bori는 kube-slint를 Go 라이브러리로 임포트하지 않습니다. `sli-summary.json`(slint.summary.v4 스키마)을 작성하고 `slint-gate`를 서브프로세스로 호출합니다.

```
bori verify  →  sli-summary.json  →  slint-gate --fail-on NEVER  →  gate_result
```

kube-slint는 bori 없이도 완전히 독립적으로 사용 가능합니다.

---

## 회귀 측정

`make deploy` 이후 자동으로 오퍼레이터가 기대하는 `BoriDataPlane` conditions를 올바르게 쓰고 있는지 확인합니다:

```bash
make regression                       # baseline과 비교
make regression -- --update-baseline  # 현재 상태를 새 baseline으로 저장
```

스크립트(`scripts/regression-check.sh`)는 클러스터 노드에서 실행되는지 로컬 머신에서 실행되는지 자동 감지하고(SSH 폴백), k8sgpt 분석을 실행한 뒤 `testdata/baseline/`과 conditions를 비교합니다.

---

## CI

| 워크플로우 | 트리거 | 검사 항목 |
|-----------|--------|-----------|
| `golangci-lint` | `*.go`, `go.mod` | govet, staticcheck, errcheck, unused, ineffassign, revive |
| `kube-linter` | `config/**` | K8s 매니페스트 best practices |
| `kubeconform` | `config/**` | K8s 1.30 스키마 검증 |

---

## 저장소 구조

```
bori/
├── apis/bori/v1alpha1/     # CRD Go 타입 (BoriDataPlane, BoriRelease, BoriRevision)
├── controllers/            # controller-runtime 리컨사일러
├── cmd/
│   ├── bori/               # CLI: plan / deploy / verify / status / revision …
│   ├── bori-operator/      # Kubernetes 오퍼레이터 엔트리포인트
│   └── bori-devspace/      # DevSpace after:deploy 어댑터
├── pkg/
│   ├── adapter/            # Runner 인터페이스 + slint-gate 호출 + sli-summary 빌더
│   ├── verification/       # kube-slint 정책 평가
│   ├── reconcile/          # 핵심 리컨사일 루프
│   ├── revision/           # BoriRevision 관리
│   └── …
├── adapters/               # 배포 어댑터 (devspace, ko, kustomize, shell)
├── config/
│   ├── crd/                # BoriDataPlane / BoriRelease / BoriRevision CRD YAML
│   ├── rbac/               # ClusterRole, ServiceAccount, 바인딩
│   └── operator/           # Deployment, ConfigMap, Namespace
├── releases/               # BoriRelease 정의 (예: jumi-ah-dev)
├── environments/           # 환경 정의 (infra-lab, kind, multipass, …)
├── components/             # 앱별 component.yaml 스펙
├── verification/policies/  # slint-gate 정책 파일
├── testdata/
│   ├── fixtures/           # 테스트용 BoriDataPlane CR
│   └── baseline/           # 회귀 검사용 conditions 스냅샷
├── scripts/
│   └── regression-check.sh # BoriDataPlane condition 회귀 측정
├── Dockerfile              # 멀티스테이지: golang:1.26 → distroless/static
└── docs/                   # 아키텍처, 로드맵, API 설계, 보안 모델
```

---

## 로드맵

전체 로드맵: [docs/control-plane-roadmap.md](docs/control-plane-roadmap.md)

Phase 0–10 및 kube-slint Track K0–K5 모두 2026-06-07 기준 완료.
