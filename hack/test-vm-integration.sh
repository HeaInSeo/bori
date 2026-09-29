#!/usr/bin/env bash
# test-vm-integration.sh — bori operator VM integration test
#
# 책임:
#   Layer 3: 원격 VM K8s 클러스터에서 전 주기 통합 검증.
#   scripts/regression-check.sh를 확장하여 추가 시나리오와
#   kube-slint SLI 측정(sli-summary.json, slo.v4)을 수행한다.
#
# SLI 측정:
#   `bori verify --target bori-operator`를 VM에서 실행한다. kube-slint producer가
#   fixture smoke(hack/vm-smoke.sh) 전후를 측정해 slo.v4 sli-summary.json을 쓰고,
#   bori가 같은 run에서 slint-gate를 실행한다. summary가 없거나 불완전하면
#   테스트는 실패한다(soft success 없음).
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
#   sli-summary.json, bori/ (bori verify run archive incl. gate summary)

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
VM_BIN_DIR="${REPO_ROOT}/bin/vm"
SLINT_POLICY="test/e2e/.slint/policy.yaml"
# TrustContract window identity: the logical window is "recreate the fixture and
# wait for the new object's reconcile" (hack/vm-smoke.sh). Change it when that
# step changes.
WINDOW_ID="vm-integration/${FIXTURE_NAME}/recreate-reconcile/v2"

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

# ── 1. kube-slint 측정 전제: metrics Service + scraper ServiceAccount ──────
log "applying kube-slint measurement prerequisites..."
ssh "${REMOTE}" "kubectl apply -f -" < test/e2e/manifests/bori-metrics-service.yaml
ssh "${REMOTE}" "kubectl apply -f -" < test/e2e/manifests/slint-sa.yaml

# ── 2. VM용 bori + slint-gate 빌드 ───────────────────────────────────────────
[ -d "${KUBE_SLINT_DIR}" ] || fail "kube-slint checkout not found: ${KUBE_SLINT_DIR}"
command -v go &>/dev/null || fail "go toolchain not found"
case "$(run_remote uname -m)" in
  x86_64)  VM_GOARCH=amd64 ;;
  aarch64) VM_GOARCH=arm64 ;;
  *)       fail "unsupported VM architecture" ;;
esac
mkdir -p "${VM_BIN_DIR}"
log "building bori and slint-gate (linux/${VM_GOARCH})..."
CGO_ENABLED=0 GOOS=linux GOARCH="${VM_GOARCH}" go build -o "${VM_BIN_DIR}/bori" ./cmd/bori \
  || fail "bori build failed"
(cd "${KUBE_SLINT_DIR}" && CGO_ENABLED=0 GOOS=linux GOARCH="${VM_GOARCH}" \
  go build -o "${VM_BIN_DIR}/slint-gate" ./cmd/slint-gate) || fail "slint-gate build failed"

# ── 3. VM에 run 디렉터리 준비 ────────────────────────────────────────────────
REMOTE_DIR=$(run_remote mktemp -d /tmp/bori-vm-integration-XXXXXX)
trap 'ssh "${REMOTE}" rm -rf "${REMOTE_DIR}" || true' EXIT
scp -q "${VM_BIN_DIR}/bori" "${VM_BIN_DIR}/slint-gate" hack/vm-smoke.sh \
  "${REMOTE}:${REMOTE_DIR}/"
scp -q "${FIXTURE}" "${REMOTE}:${REMOTE_DIR}/fixture.yaml"
scp -q "${SLINT_POLICY}" "${REMOTE}:${REMOTE_DIR}/policy.yaml"

# ── 4. kube-slint producer 측정 (Start → fixture smoke → End → gate) ────────
# SubjectID: the exact operator image under test, read from the running pod.
IMAGE_ID=$(run_kubectl get pod -n "${NAMESPACE}" \
  -l app.kubernetes.io/name=bori-operator \
  -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' 2>/dev/null || true)
[ -n "${IMAGE_ID}" ] || fail "cannot read bori-operator imageID"
SUBJECT_ID="bori-operator@${IMAGE_ID}"
log "measuring bori-operator (subject=${SUBJECT_ID}, window=${WINDOW_ID})..."
# --fail-on NEVER keeps the gate summary-only (see ${SLINT_POLICY}); a failed or
# incomplete measurement still fails bori verify.
set +e
ssh "${REMOTE}" "cd '${REMOTE_DIR}' && \
  SLINT_SA_TOKEN=\$(kubectl -n '${NAMESPACE}' create token kube-slint --duration=1h) \
  ./bori verify --target bori-operator --policy policy.yaml \
    --subject-id '${SUBJECT_ID}' --window-id '${WINDOW_ID}' \
    --smoke-cmd \"bash ./vm-smoke.sh '${NAMESPACE}' '${FIXTURE_NAME}' fixture.yaml\" \
    --fail-on NEVER --slint-gate ./slint-gate --bori-dir ./.bori -v"
BORI_RC=$?
set -e

# The run archive is copied back whatever the outcome. A summary left by an
# earlier run must never be reported as this run's evidence. The archive may be
# missing (bori failed before writing it, or the copy failed); the search must
# not abort the script then, so the BORI_RC check and diagnostics below run.
rm -rf "${ARTIFACTS_DIR}/bori" "${SLI_SUMMARY_PATH}"
scp -q -r "${REMOTE}:${REMOTE_DIR}/.bori" "${ARTIFACTS_DIR}/bori" || true
SUMMARY_SRC=""
if [ -d "${ARTIFACTS_DIR}/bori" ]; then
  SUMMARY_SRC=$(find "${ARTIFACTS_DIR}/bori" -path '*/evidence/bori-operator/sli-summary.json' 2>/dev/null \
    | head -1) || true
fi
if [ -n "${SUMMARY_SRC}" ]; then
  cp "${SUMMARY_SRC}" "${SLI_SUMMARY_PATH}"
fi

[ "${BORI_RC}" -eq 0 ] || fail "bori verify --target bori-operator failed (rc=${BORI_RC})"
[ -f "${SLI_SUMMARY_PATH}" ] || fail "kube-slint sli-summary.json was not produced"
log "sli-summary.json saved: ${SLI_SUMMARY_PATH}"

# ── 5. conditions 스냅샷 ──────────────────────────────────────────────────────
log "capturing conditions snapshot..."
SNAPSHOT=$(capture_conditions) || fail "failed to capture conditions"
echo "${SNAPSHOT}" > "${ARTIFACTS_DIR}/conditions-snapshot.json"

# ── 6. BoriRevision 존재 확인 ────────────────────────────────────────────────
log "checking BoriRevision..."
REV_COUNT=$(run_kubectl get borirevisions -n "${NAMESPACE}" \
  --no-headers 2>/dev/null | wc -l || echo 0)
log "BoriRevision count: ${REV_COUNT}"
# count가 0이어도 fail 아님 — release 정의에 따라 달라짐

# ── 7. BoriRelease.status.activeDataPlanes 확인 ───────────────────────────────
log "checking BoriRelease.status.activeDataPlanes..."
RELEASE=$(run_kubectl get boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.release}' 2>/dev/null || true)
if [ -n "${RELEASE}" ]; then
  ACTIVE=$(run_kubectl get borirelease "${RELEASE}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.activeDataPlanes}' 2>/dev/null || echo "N/A")
  log "  BoriRelease(${RELEASE}).status.activeDataPlanes=${ACTIVE}"
fi

# ── 8. baseline 갱신 모드 ─────────────────────────────────────────────────────
if [ "${UPDATE_BASELINE}" = "--update-baseline" ]; then
  echo "${SNAPSHOT}" > "${BASELINE}"
  log "conditions baseline updated: ${BASELINE}"
  collect_artifacts
  exit 0
fi

# ── 9. 회귀 비교 ─────────────────────────────────────────────────────────────
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
  echo "  sli-summary : ${SLI_SUMMARY_PATH}"
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
