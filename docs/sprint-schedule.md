# bori Control Plane 전환 스프린트 일정 (v0.5 기준)

작성일: 2026-06-01  
기준 문서: [control-plane-roadmap.md](control-plane-roadmap.md)

---

## Sprint 1 — 2026-06-01 ~ 2026-06-07 (1주)

**Phase 0: 설계/보안 기준선**

| 저장소 | 작업 | 산출물 |
|--------|------|--------|
| bori | control-plane 방향 문서화 | `docs/control-plane-roadmap.md` |
| bori | architecture.md 업데이트 | `docs/architecture.md` |
| bori | agent-contract.md 추가 | `docs/agent-contract.md` |
| bori | security-model.md 추가 | `docs/security-model.md` |
| bori | verification-model.md 추가 | `docs/verification-model.md` |
| bori | kube-slint-integration.md 추가 | `docs/kube-slint-integration.md` |
| bori | migration-inventory.md 추가 | `docs/migration-inventory.md` |

**완료 기준:** PR-1 merge

---

## Sprint 2 — 2026-06-08 ~ 2026-06-21 (2주)

**병렬 진행**

| 저장소 | 트랙/페이즈 | 작업 | 산출물 |
|--------|------------|------|--------|
| kube-slint | **K0** (06-03~06-10) | schemaVersion strictness, Validate() 추가, unknown → NO_GRADE | `summary.Validate()`, tests |
| bori | **Phase 1** | `cmd/bori` CLI skeleton, `bori plan/verify/status` | `cmd/bori/main.go`, `pkg/model`, `pkg/artifact` |
| bori | **Phase 1** | 실패 시 status.json 생성, run archive 레이아웃 | `.bori/runs/<run-id>/status.json` |
| bori | **PR-2** | schema/component parser 정합성, HTTP timeout 개선, port-forward cleanup | parser fixes, redaction skeleton |

**완료 기준:**
- kube-slint K0: schemaVersion 누락/unknown 시 NO_GRADE 표시
- bori Phase 1: `bori verify` 호출 가능, 실패해도 artifact 생성

---

## Sprint 3 — 2026-06-22 ~ 2026-07-05 (2주)

**병렬 진행**

| 저장소 | 트랙/페이즈 | 작업 | 산출물 |
|--------|------------|------|--------|
| kube-slint | **K1** (06-11~06-21, 이월) | SLIResult.Status → gate_result 반영, severity order 구현 | status propagation, counter reset policy |
| bori | **Phase 1.5** | kube-slint provider 정렬, slint-gate output → BoriVerificationRun wrapping | `pkg/verification/provider.go`, `pkg/verification/kubeslint.go` |
| bori | **Phase 1.5** | `--fail-on NEVER` + JSON 기반 bori-side promotion decision | `verification/policies/example.yaml` |

**완료 기준:**
- K1: `Status=skip` → NO_GRADE, `Status=fail` → FAIL gate 반영, counter reset 정책 적용 가능
- Phase 1.5: `bori verify`가 slint-gate를 provider로 호출, NO_GRADE가 promotion gate를 통과하지 않음

---

## Sprint 4 — 2026-07-06 ~ 2026-07-19 (2주)

**병렬 진행**

| 저장소 | 트랙/페이즈 | 작업 | 산출물 |
|--------|------------|------|--------|
| kube-slint | **K3** (07-01~07-14) | curlpod run-id label, cleanup warning/evidence 기록 | label 표준화, cleanup evidence |
| kube-slint | **K4** (07-01~07-14) | RedactString/RedactMap, Authorization/token 패턴 마스킹 | redaction utility, tests |
| bori | **Phase 2** (1주차) | component registry 초안, environment overlay 초안 | `components/*/component.yaml`, `environments/*/environment.yaml` |

**완료 기준:**
- K3/K4: verification helper resource 식별 가능, secret 평문 없음
- Phase 2 (1주차): JUMI/AH/nan/tori component.yaml 초안 존재

---

## Sprint 5 — 2026-07-20 ~ 2026-08-02 (2주)

**Phase 2 (2주차) + 마무리**

| 저장소 | 작업 | 산출물 |
|--------|------|--------|
| bori | adapter 인터페이스 정리 | `adapters/devspace`, `adapters/ko`, `adapters/kustomize`, `adapters/shell` |
| bori | app repo vs bori 소유권 경계 분리 | deploy script ↔ verification input 분리 |
| bori | PR-3: `bori CLI skeleton` merge | run archive 생성, provider interface skeleton |

**완료 기준:** Phase 2 완료 — component/environment model로 JUMI/AH/nan/tori 표현 가능, deploy script와 verification input이 섞이지 않음

---

## Sprint 6 — 2026-08-03 ~ 2026-08-16 (2주)

**병렬 진행**

| 저장소 | 트랙/페이즈 | 작업 | 산출물 |
|--------|------------|------|--------|
| kube-slint | **K5** (08-01~08-21, 1주차) | namespace + label selector 설계, before/after object list 수집 | K5 MVP 설계 |
| bori | **Phase 3** | artifact-handoff를 bori 경유 배포/검증 | `components/artifact-handoff/component.yaml`, bori plan/deploy/verify 실행 |
| bori | **PR-4/PR-5** | kube-slint verification provider 완성, first app absorption | 실제 bori verify 실행 결과 artifact |

**완료 기준:** Phase 3 완료 — 실제 dataplane app이 bori plan/deploy/verify를 탄다, run artifact 생성

---

## Sprint 7 — 2026-08-17 ~ 2026-09-07 (3주)

**병렬 진행**

| 저장소 | 트랙/페이즈 | 작업 | 산출물 |
|--------|------------|------|--------|
| kube-slint | **K5** (08-01~08-21, 2주차) | scalar SLI metric 생성, orphan/stuck/ownerRef count, evidence 저장 | K5 MVP 완료 |
| bori | **Phase 3.5** (metric 기반) | metric 기반 JUMI churn gate MVP, counter reset 정책 연결 | `verification/policies/jumi-upgrade-churn.yaml`, `verification/baselines/jumi-churn-baseline.json` |
| bori | **Phase 3.5** (k8s object) | K5 결과 수신, object-level churn gate 연결 | `docs/jumi-churn-gate.md` |

**완료 기준:** Phase 3.5 완료 — synthetic pipeline 전후 JUMI object churn 관측 가능, counter reset이 promotion gate에서 NO_GRADE/blocking 처리

---

## Sprint 8~9 — 2026-09-08 ~ 2026-09-30 (3주)

**Phase 4: Multi-app Release 모델**

| 저장소 | 작업 | 산출물 |
|--------|------|--------|
| bori | JUMI + AH + nan 조합 BoriRelease | `releases/jumi-ah-dev/release.yaml`, `compatibility/jumi-ah-nan.yaml` |
| bori | version compatibility matrix | `pkg/release` |
| bori | release-level verification 실행 | release-level gate 가능 |

**완료 기준:** multi-component app set 배포/검증, 특정 component 변경 시 영향받는 verification 계산 가능

---

## Sprint 10 — 2026-10-01 ~ 2026-10-31 (4주)

**Phase 5: Revision Snapshot / Rollout Plan**

| 저장소 | 작업 | 산출물 |
|--------|------|--------|
| bori | immutable revision snapshot 모델 | `pkg/revision`, `.bori/revisions/<revision-id>.json` |
| bori | image/config/policy digest 기록, content hash | revision 무결성 |
| bori | rollout plan dry-run | `pkg/rollout`, `.bori/rollouts/<rollout-id>.json` |

**완료 기준:** revision 추적 가능, rollback 후보 식별 가능, baseline provenance 기록

---

## Sprint 11 — 2026-11-01 ~ 2026-11-30 (4주)

**Phase 6: Operator Shadow Mode**

| 저장소 | 작업 | 산출물 |
|--------|------|--------|
| bori | CRD/API 초안 | `apis/bori/v1alpha1`, `config/crd` |
| bori | operator dry-run diff/status만 계산 | `controllers/dataplane_controller.go` skeleton |
| bori | CLI 모델과 operator 모델 정합성 검증 | `docs/api-design.md` |

**완료 기준:** BoriDataPlane으로 JUMI/AH/nan 표현 가능, operator는 status만 기록

---

## Sprint 12+ — 2026-12 이후

**Phase 7: Limited Operator Apply Mode**

제한된 namespace에서 operator apply, RBAC/secret redaction/status condition 검증.

---

## 전체 타임라인 요약

```
Jun W1      Sprint 1  — Phase 0 (문서) + K0 시작
Jun W2~4    Sprint 2  — Phase 1 (CLI) + K0 완료 + K1 시작
Jun W4~     Sprint 3  — Phase 1.5 (kube-slint 정렬) + K1 완료
Jul W1~2    Sprint 4  — Phase 2 (1주차) + K3/K4
Jul W3~Aug W1  Sprint 5  — Phase 2 (2주차) + PR-3
Aug W1~2    Sprint 6  — Phase 3 (AH 흡수) + K5 시작
Aug W3~Sep W1  Sprint 7  — Phase 3.5 (JUMI churn) + K5 완료
Sep W2~4    Sprint 8~9 — Phase 4 (Multi-app Release)
Oct         Sprint 10 — Phase 5 (Revision Snapshot)
Nov         Sprint 11 — Phase 6 (Operator Shadow)
Dec+        Sprint 12+ — Phase 7 (Operator Apply)
```

---

## kube-slint Track 요약

| Track | 기간 | 내용 | bori Phase 의존 |
|-------|------|------|----------------|
| K0 | 06-03~06-10 | schemaVersion strictness | Phase 1.5 |
| K1 | 06-11~06-21 | SLIResult.Status propagation + counter reset | Phase 1.5, 3.5 |
| K2 | K1에 포함 | Counter reset policy | Phase 3.5 |
| K3 | 07-01~07-14 | curlpod security and cleanup | Phase 3 이후 |
| K4 | 07-01~07-14 | Evidence redaction | Phase 3 이후 |
| K5 | 08-01~08-21 | k8s_object_snapshot MVP | Phase 3.5 |
