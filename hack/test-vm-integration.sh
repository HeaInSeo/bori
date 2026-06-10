#!/usr/bin/env bash
# test-vm-integration.sh — bori operator VM integration test
#
# 책임:
#   Layer 3: 원격 VM K8s 클러스터에서 전 주기 통합 검증.
#   scripts/regression-check.sh를 확장하여 추가 시나리오와
#   kube-slint SLI 측정(sli-summary.json)을 수행한다.
#
# 원격 대상: seoy@100.123.80.48 (Tailscale, SSH)
#
# 사용법:
#   ./hack/test-vm-integration.sh                  # 통합 검증 + 회귀 비교
#   ./hack/test-vm-integration.sh --update-baseline  # conditions baseline 갱신
#
# 실패 시 자동 수집 (artifacts/vm/):
#   conditions-snapshot.json, operator-logs.txt, events.txt,
#   boridataplanes.yaml, borirevisions.yaml,
#   metrics-pre.txt, metrics-post.txt, sli-summary.json,
#   slint-gate-summary.json

set -euo pipefail

# ── 설정 ─────────────────────────────────────────────────────────────────────
# BORI_VM_REMOTE: SSH target for the VM (required).
# GitHub Actions: set as a repository variable (vars.BORI_VM_REMOTE).
# Local: export BORI_VM_REMOTE=user@your-vm-ip before running.
REMOTE="${BORI_VM_REMOTE:-}"
if [ -z "${REMOTE}" ]; then
  echo "[vm-integration] error: BORI_VM_REMOTE is not set" >&2
  echo "  Set the SSH target before running:" >&2
  echo "    BORI_VM_REMOTE=user@your-vm-ip ./hack/test-vm-integration.sh" >&2
  echo "  GitHub Actions: configure vars.BORI_VM_REMOTE in repository settings." >&2
  exit 1
fi
NAMESPACE="bori-system"
FIXTURE_NAME="infra-lab-smoke"
FIXTURE="testdata/fixtures/bdp-infra-lab-smoke.yaml"
BASELINE="testdata/baseline/infra-lab-smoke-conditions.json"
UPDATE_BASELINE="${1:-}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARTIFACTS_DIR="${REPO_ROOT}/artifacts/vm"
KUBE_SLINT_DIR="${KUBE_SLINT_DIR:-${REPO_ROOT}/../kube-slint}"
SLI_SUMMARY_PATH="${ARTIFACTS_DIR}/sli-summary.json"

cd "${REPO_ROOT}"

log()  { echo "[vm-integration] $*"; }
fail() { echo "[vm-integration] FAIL: $*" >&2; collect_artifacts; exit 1; }

# ── 원격 실행 헬퍼 ────────────────────────────────────────────────────────────
run_kubectl() { ssh "${REMOTE}" kubectl "$@"; }
run_remote()  { ssh "${REMOTE}" "$@"; }

capture_conditions() {
  ssh "${REMOTE}" \
    "kubectl get boridataplane ${FIXTURE_NAME} -n ${NAMESPACE} -o json \
     | jq '{resource: .metadata.name, namespace: .metadata.namespace,
            release: .spec.release, environment: .spec.environment,
            conditions: [.status.conditions[] | {type: .type, status: .status, reason: .reason}]}'"
}

capture_metrics() {
  # operator pod에서 wget으로 /metrics 수집
  local POD
  POD=$(run_kubectl get pod -n "${NAMESPACE}" \
    -l app.kubernetes.io/name=bori-operator \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -n "${POD}" ]; then
    run_kubectl exec -n "${NAMESPACE}" "${POD}" \
      -- wget -qO- http://localhost:8080/metrics 2>/dev/null || true
  fi
}

# ── SSH 연결 확인 ─────────────────────────────────────────────────────────────
log "checking SSH connectivity to ${REMOTE}..."
if ! ssh -o ConnectTimeout=10 -o BatchMode=yes "${REMOTE}" true 2>/dev/null; then
  echo "[vm-integration] error: cannot reach ${REMOTE}" >&2
  echo "  Tailscale 연결 또는 SSH 키를 확인하세요." >&2
  exit 1
fi
log "SSH OK"

# ── artifact 수집 함수 ────────────────────────────────────────────────────────
collect_artifacts() {
  log "collecting artifacts → ${ARTIFACTS_DIR}"
  mkdir -p "${ARTIFACTS_DIR}"
  # operator logs
  { local POD
    POD=$(run_kubectl get pod -n "${NAMESPACE}" \
      -l app.kubernetes.io/name=bori-operator \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    [ -n "${POD}" ] && run_kubectl logs -n "${NAMESPACE}" "${POD}" --tail=500
  } > "${ARTIFACTS_DIR}/operator-logs.txt" 2>/dev/null || true
  # events
  run_kubectl get events -n "${NAMESPACE}" --sort-by='.lastTimestamp' \
    > "${ARTIFACTS_DIR}/events.txt" 2>/dev/null || true
  # CR yaml
  run_kubectl get boridataplanes -n "${NAMESPACE}" -o yaml \
    > "${ARTIFACTS_DIR}/boridataplanes.yaml" 2>/dev/null || true
  run_kubectl get borirevisions -n "${NAMESPACE}" -o yaml \
    > "${ARTIFACTS_DIR}/borirevisions.yaml" 2>/dev/null || true
  # conditions snapshot
  capture_conditions > "${ARTIFACTS_DIR}/conditions-snapshot.json" 2>/dev/null || true
  log "artifacts saved to ${ARTIFACTS_DIR}"
}

mkdir -p "${ARTIFACTS_DIR}"

# ── 1. fixture 적용 ───────────────────────────────────────────────────────────
log "applying fixture ${FIXTURE_NAME}..."
ssh "${REMOTE}" "kubectl apply -f -" < "${FIXTURE}"

# ── 2. pre-workload metrics snapshot ─────────────────────────────────────────
log "capturing pre-workload metrics..."
capture_metrics > "${ARTIFACTS_DIR}/metrics-pre.txt" || true
log "pre-workload metrics saved"

# ── 3. reconcile 대기 (최대 90초) ─────────────────────────────────────────────
log "waiting for operator to reconcile..."
for i in $(seq 1 18); do
  GEN=$(run_kubectl get boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.observedGeneration}' 2>/dev/null || echo 0)
  [ "${GEN:-0}" -ge 1 ] && break
  sleep 5
done
[ "${GEN:-0}" -ge 1 ] || fail "operator did not reconcile within 90s"
log "reconciled: observedGeneration=${GEN}"

# ── 4. conditions 스냅샷 ──────────────────────────────────────────────────────
log "capturing conditions snapshot..."
SNAPSHOT=$(capture_conditions) || fail "failed to capture conditions"
echo "${SNAPSHOT}" > "${ARTIFACTS_DIR}/conditions-snapshot.json"

# ── 5. BoriRevision 존재 확인 ────────────────────────────────────────────────
log "checking BoriRevision..."
REV_COUNT=$(run_kubectl get borirevisions -n "${NAMESPACE}" \
  --no-headers 2>/dev/null | wc -l || echo 0)
log "BoriRevision count: ${REV_COUNT}"
# count가 0이어도 fail 아님 — release 정의에 따라 달라짐

# ── 6. BoriRelease.status.activeDataPlanes 확인 ───────────────────────────────
log "checking BoriRelease.status.activeDataPlanes..."
RELEASE=$(run_kubectl get boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.release}' 2>/dev/null || true)
if [ -n "${RELEASE}" ]; then
  ACTIVE=$(run_kubectl get borirelease "${RELEASE}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.activeDataPlanes}' 2>/dev/null || echo "N/A")
  log "  BoriRelease(${RELEASE}).status.activeDataPlanes=${ACTIVE}"
fi

# ── 7. post-workload metrics snapshot ─────────────────────────────────────────
log "capturing post-workload metrics..."
capture_metrics > "${ARTIFACTS_DIR}/metrics-post.txt" || true
log "post-workload metrics saved"

# ── 8. kube-slint gate (summary-only) ────────────────────────────────────────
SLINT_AVAILABLE=false
if [ -d "${KUBE_SLINT_DIR}" ] && command -v go &>/dev/null; then
  if [ ! -f "${KUBE_SLINT_DIR}/bin/slint-gate" ]; then
    log "building slint-gate..."
    (cd "${KUBE_SLINT_DIR}" && GOTMPDIR=/home/heain/gotmp go build -o bin/slint-gate ./cmd/slint-gate 2>&1) && \
      SLINT_AVAILABLE=true || log "slint-gate build failed (non-fatal)"
  else
    SLINT_AVAILABLE=true
  fi
fi

if [ "${SLINT_AVAILABLE}" = "true" ]; then
  # sli-summary.json이 있으면 gate 평가, 없으면 skip
  if [ -f "${SLI_SUMMARY_PATH}" ]; then
    log "running slint-gate (summary-only, fail-on=NONE)..."
    "${KUBE_SLINT_DIR}/bin/slint-gate" \
      --measurement-summary "${SLI_SUMMARY_PATH}" \
      --policy "test/e2e/.slint/policy.yaml" \
      --fail-on NONE \
      > "${ARTIFACTS_DIR}/slint-gate-summary.json" 2>/dev/null && \
      log "slint-gate-summary.json saved" || \
      log "slint-gate evaluation skipped (non-fatal)"
  else
    log "kube-slint: sli-summary.json not yet available"
    log "  → kube-slint Go 세션 연동은 추후 단계"
  fi
else
  log "kube-slint: slint-gate binary not available (non-fatal)"
  log "  → kube-slint 활성화: KUBE_SLINT_DIR=${KUBE_SLINT_DIR} 확인"
fi

# ── 9. baseline 갱신 모드 ─────────────────────────────────────────────────────
if [ "${UPDATE_BASELINE}" = "--update-baseline" ]; then
  echo "${SNAPSHOT}" > "${BASELINE}"
  log "conditions baseline updated: ${BASELINE}"
  collect_artifacts
  exit 0
fi

# ── 10. 회귀 비교 ─────────────────────────────────────────────────────────────
if [ ! -f "${BASELINE}" ]; then
  log "no conditions baseline found — saving as initial baseline"
  echo "${SNAPSHOT}" > "${BASELINE}"
  log "saved: ${BASELINE}"
  collect_artifacts
  exit 0
fi

BASELINE_CONDITIONS=$(jq -r \
  '.conditions | map("\(.type)=\(.status)/\(.reason)") | sort | join(",")' \
  "${BASELINE}")
CURRENT_CONDITIONS=$(echo "${SNAPSHOT}" | jq -r \
  '.conditions | map("\(.type)=\(.status)/\(.reason)") | sort | join(",")')

if [ "${BASELINE_CONDITIONS}" = "${CURRENT_CONDITIONS}" ]; then
  log "OK — conditions match baseline"
  log "  ${CURRENT_CONDITIONS}"
  collect_artifacts
  # ── 완료 ──────────────────────────────────────────────────────────────────
  echo ""
  echo "════════════════════════════════════════════"
  echo "  VM integration test PASSED"
  echo ""
  echo "  artifacts   : ${ARTIFACTS_DIR}/"
  [ -f "${SLI_SUMMARY_PATH}" ] && echo "  sli-summary : ${SLI_SUMMARY_PATH}"
  echo "════════════════════════════════════════════"
  exit 0
fi

echo ""
echo "=== REGRESSION DETECTED ==="
echo "baseline : ${BASELINE_CONDITIONS}"
echo "current  : ${CURRENT_CONDITIONS}"
echo ""
diff <(echo "${BASELINE_CONDITIONS}" | tr ',' '\n') \
     <(echo "${CURRENT_CONDITIONS}"  | tr ',' '\n') || true
echo "==========================="
echo ""
fail "BoriDataPlane conditions have changed from baseline"
