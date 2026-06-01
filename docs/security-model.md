# bori Security Model

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## 개요

보안 모델은 operator 시점에 미루지 않는다. bori가 agent-facing gateway가 되는 순간부터 다음 위험이 존재한다.

```text
- smoke command를 통한 arbitrary code execution
- run archive에 secret이 평문으로 남는 경우
- 허용되지 않은 namespace에 배포되는 경우
- verification helper resource가 churn 측정을 오염시키는 경우
- revision snapshot이 위변조되는 경우
```

---

## 1. Smoke Command 신뢰 경계

현재 `--smoke-cmd`는 shell string으로 실행될 수 있다. agent-facing gateway로 가면 이 방식은 위험하다.

### Profile별 정책

```text
local / dev:
  shell smoke 허용 가능
  단, 명시적으로 unsafe / developer mode로 표시해야 한다.

shared / integration:
  구조화된 smoke spec 우선
  shell string은 기본 금지 또는 allowlist 필요

promotion:
  arbitrary sh -c 금지
  검증된 smoke primitive만 허용
```

### 권장 smoke spec

```yaml
smoke:
  type: exec
  command: ["go", "test", "./test/smoke", "-run", "TestJUMIAHSmoke"]
  timeout: 2m
  workingDir: ../jumi
```

향후 후보:

```yaml
smoke:
  type: kubernetes-job
  image: ghcr.io/.../jumi-smoke@sha256:...
  command: ["/smoke"]
  timeout: 5m
```

### 금지 패턴

```yaml
# 금지: arbitrary shell string
smoke:
  type: shell
  cmd: "curl http://... && kubectl apply -f ..."
```

---

## 2. Secret Redaction

bori는 run archive를 남긴다. run archive에는 request, rendered manifest, verification result, logs, evidence가 포함될 수 있다.

### Redaction 대상

```text
- kubeconfig token
- registry password
- docker auth config
- Secret.data / Secret.stringData
- bearer token
- API key
- cloud credential
- signed URL
- private endpoint credential
- evidence file 내부의 Authorization / token / password 값
```

### Redaction 정책

```text
run archive에는 secret raw value를 저장하지 않는다.
Secret object는 name / reference만 저장한다.
rendered manifest 저장 시 Secret.data / stringData는 REDACTED 처리한다.
환경 변수 이름에 TOKEN / PASSWORD / SECRET / KEY가 포함되면 기본 redaction한다.
evidence 복사 시에도 redaction을 적용한다.
```

### 구현 후보

```go
// pkg/security/redact.go
func RedactString(s string) string
func RedactMap(m map[string]string) map[string]string
func RedactManifest(manifest []byte) []byte
```

redaction은 run archive 저장 직전에 적용한다.

---

## 3. RBAC / Namespace Authorization

operator apply mode 전까지도 authorization 모델은 문서화되어야 한다.

### 초기 원칙

```text
BoriEnvironment는 쓸 수 있는 namespace 범위를 명시한다.
BoriRelease는 허용된 namespace 밖으로 apply할 수 없다.
bori CLI는 apply 전 plan 단계에서 namespace violation을 감지한다.
operator shadow mode에서는 권한 부족을 status로 표현한다.
operator apply mode에서는 최소 권한 ServiceAccount를 사용한다.
```

### environment.yaml namespace 정책 예시

```yaml
name: kind
cluster:
  kubeconfig: ${KUBECONFIG}
namespacePolicy:
  allowed:
    - jumi-system
    - artifact-system
  allowClusterScopedResources: false
```

### Namespace violation 처리

```text
plan 단계에서 namespace violation 감지 시:
  -> 배포 차단
  -> status.json에 violation reason 기록

operator shadow mode:
  -> status.conditions에 NamespaceViolation condition 추가
```

---

## 4. Verification Helper Resource 권한

kube-slint의 curlpod, JUMI churn synthetic pipeline, smoke job은 클러스터에 임시 리소스를 만들 수 있다. 이 리소스는 측정 오염을 막기 위해 명확히 식별되어야 한다.

### Label 표준

```text
verification helper resource 필수 label:
  bori.dev/run-id=<run-id>
  kube-slint.dev/run-id=<run-id>
  bori.dev/verification-helper=true
  app.kubernetes.io/managed-by=bori
```

### Churn 측정에서 제외

```text
측정 대상 selector:
  include: jumi.dev/run-id=<target-run-id>
  exclude: bori.dev/verification-helper=true
  exclude: kube-slint.dev/run-id exists
```

### Cleanup 책임

```text
- verification helper resource는 run 완료 후 cleanup 책임이 명확해야 한다.
- cleanup 실패 시 evidence에 warning을 기록한다.
- cleanup 실패는 다음 run의 측정을 오염시킬 수 있으므로 반드시 기록한다.
```

### ServiceAccount 권장

```text
- verification helper는 별도 ServiceAccount를 사용한다.
- app deploy에 사용하는 ServiceAccount와 분리한다.
- 최소 권한 원칙을 적용한다.
```

---

## 5. Revision Snapshot 무결성

revision snapshot은 rollback과 promotion의 근거가 된다.

### 필수 필드

```text
revisionId
component version
image digest           (tag보다 digest 우선)
component spec digest
config digest
environment digest
verification policy digest
baseline reference
createdAt
parent revision
content hash
```

### Content Hash 계산

```text
contentHash = hash(
  image digest
  + component spec digest
  + config digest
  + environment digest
  + verification policy digest
  + baseline reference
)
```

canonical form으로 정렬·직렬화한 뒤 계산한다. 동일한 입력은 항상 동일한 hash를 낸다.

### 향후 확장

```text
- snapshot hash chain: 이전 revision의 hash를 다음 revision의 input으로 포함
- signature: 신뢰할 수 있는 key로 revision snapshot에 서명
```

---

## 6. Run Archive 보안 정책

```text
.bori/runs/<run-id>/
  request.yaml          # 입력 파라미터 (secret 제외)
  plan.json             # 배포 계획 (rendered manifest 경로만 참조)
  deploy-result.json    # 배포 결과 (secret 값 없음)
  verification-result.json
  status.json
  logs/
    adapter.log         # secret 패턴 redaction 후 저장
    smoke.log           # secret 패턴 redaction 후 저장
    slint-gate.log
  evidence/
    sli-summary.json
    slint-gate-summary.json
    k8s-object-before.json   # Secret.data/stringData 제외
    k8s-object-after.json    # Secret.data/stringData 제외
  rendered/
    manifest.yaml       # Secret kind는 REDACTED 처리 후 저장
```

---

## 참고 문서

- [architecture.md](architecture.md)
- [agent-contract.md](agent-contract.md)
- [verification-model.md](verification-model.md)
- [kube-slint-integration.md](kube-slint-integration.md)
