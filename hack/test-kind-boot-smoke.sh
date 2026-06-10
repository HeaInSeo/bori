#!/usr/bin/env bash
# test-kind-boot-smoke.sh — bori operator K0 boot smoke (kind cluster)
#
# ═══ K0 boot smoke ════════════════════════════════════════════════════════════
# 목적: GitHub-hosted runner kind 환경에서 operator가 기동하는지 확인.
#       release 파일 없이 emptyDir로 실행 — operator는 ReleaseNotFound 조건을 기록.
#
# 검증 대상:
#   - CRD apply
#   - operator Deployment Ready
#   - /metrics 응답
#   - BoriDataPlane.status.conditions 설정 (ReleaseNotFound 등)
#   - BoriRelease.status.activeDataPlanes 카운트
#   - kube-slint SLI snapshot (sli-summary.json artifact)
#
# 검증하지 않는 것 (K1 functional smoke의 범위):
#   - release 파일 발견 → BoriRevision 생성
#   - deploy/verify/promote happy path
#
# K1 (functional smoke) 구현 계획:
#   - ConfigMap 또는 projected volume으로 최소 release fixture 주입
#   - BoriRevision 1개 생성 확인
#   - hack/test-kind-functional-smoke.sh (다음 PR)
# ══════════════════════════════════════════════════════════════════════════════
#
# 전제 조건:
#   kind, docker, kubectl, go
#   kube-slint: $KUBE_SLINT_DIR 또는 ../kube-slint (선택)
#
# 사용법:
#   ./hack/test-kind-smoke.sh          # 완료 후 클러스터 삭제
#   ./hack/test-kind-smoke.sh --keep   # 클러스터 유지 (디버그)
#
# 실패 시 artifacts/kind/ 아래에 자동 수집:
#   operator-logs.txt, events.txt, boridataplanes.yaml,
#   borireleases.yaml, borirevisions.yaml, metrics-raw.txt,
#   sli-summary.json (kube-slint 측정 시)

set -euo pipefail

# ── 설정 ─────────────────────────────────────────────────────────────────────
CLUSTER_NAME="bori-smoke"
IMAGE_NAME="bori-operator:dev"
NAMESPACE="bori-system"
KUBE_VERSION="${KUBE_VERSION:-v1.30.0}"
KEEP_CLUSTER="${1:-}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARTIFACTS_DIR="${REPO_ROOT}/artifacts/kind"
KUBE_SLINT_DIR="${KUBE_SLINT_DIR:-${REPO_ROOT}/../kube-slint}"
CURL_IMAGE="${CURL_IMAGE:-curlimages/curl:8.6.0}"

cd "${REPO_ROOT}"

log()  { echo "[kind-smoke] $*"; }
fail() { echo "[kind-smoke] FAIL: $*" >&2; collect_artifacts; exit 1; }

# ── container runtime 감지 (docker 우선, 없으면 podman) ─────────────────────
if command -v docker &>/dev/null; then
  CONTAINER_RUNTIME="docker"
elif command -v podman &>/dev/null; then
  CONTAINER_RUNTIME="podman"
  export KIND_EXPERIMENTAL_PROVIDER=podman
  log "podman detected — setting KIND_EXPERIMENTAL_PROVIDER=podman"
else
  echo "[kind-smoke] error: docker or podman not found in PATH" >&2
  exit 1
fi

# ── 전제 조건 확인 ────────────────────────────────────────────────────────────
for cmd in kind kubectl go; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "[kind-smoke] error: '$cmd' not found in PATH" >&2
    exit 1
  fi
done

# ── artifact 수집 (실패 / --keep 시) ──────────────────────────────────────────
collect_artifacts() {
  log "collecting artifacts → ${ARTIFACTS_DIR}"
  mkdir -p "${ARTIFACTS_DIR}"
  export KUBECONFIG
  kubectl -n "${NAMESPACE}" logs -l app.kubernetes.io/name=bori-operator \
    --tail=500 > "${ARTIFACTS_DIR}/operator-logs.txt" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get events --sort-by='.lastTimestamp' \
    > "${ARTIFACTS_DIR}/events.txt" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get boridataplanes -o yaml \
    > "${ARTIFACTS_DIR}/boridataplanes.yaml" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get borireleases -o yaml \
    > "${ARTIFACTS_DIR}/borireleases.yaml" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get borirevisions -o yaml \
    > "${ARTIFACTS_DIR}/borirevisions.yaml" 2>/dev/null || true
  # /metrics 스냅샷
  kubectl -n "${NAMESPACE}" exec \
    "$(kubectl -n "${NAMESPACE}" get pod -l app.kubernetes.io/name=bori-operator \
       -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)" \
    -- wget -qO- http://localhost:8080/metrics 2>/dev/null \
    > "${ARTIFACTS_DIR}/metrics-raw.txt" || true
  log "artifacts saved to ${ARTIFACTS_DIR}"
}

# ── 클러스터 정리 (종료 시) ────────────────────────────────────────────────────
teardown() {
  if [ "${KEEP_CLUSTER}" = "--keep" ]; then
    log "  --keep: cluster '${CLUSTER_NAME}' kept running"
    log "  kubeconfig: ${KUBECONFIG}"
    return
  fi
  log "deleting kind cluster '${CLUSTER_NAME}'"
  kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
  rm -f "${KUBECONFIG_FILE:-}"
}
trap teardown EXIT

# ── 1. kind 클러스터 생성 ─────────────────────────────────────────────────────
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  log "reusing existing kind cluster '${CLUSTER_NAME}'"
else
  log "creating kind cluster '${CLUSTER_NAME}' (k8s ${KUBE_VERSION})..."
  kind create cluster --name "${CLUSTER_NAME}" \
    --image "kindest/node:${KUBE_VERSION}"
fi

KUBECONFIG_FILE="$(mktemp /tmp/bori-smoke-kubeconfig.XXXXXX)"
kind get kubeconfig --name "${CLUSTER_NAME}" > "${KUBECONFIG_FILE}"
export KUBECONFIG="${KUBECONFIG_FILE}"
log "KUBECONFIG=${KUBECONFIG}"

# ── 2. operator 이미지 빌드 + kind load ───────────────────────────────────────
log "building bori-operator image (${IMAGE_NAME}) via ${CONTAINER_RUNTIME}..."
"${CONTAINER_RUNTIME}" build --quiet -t "${IMAGE_NAME}" "${REPO_ROOT}"
log "loading image into kind cluster..."
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# ── 3. CRD / RBAC / manifests 설치 ───────────────────────────────────────────
log "installing CRDs..."
kubectl apply -f config/crd/ --server-side

log "creating namespace and RBAC..."
kubectl apply -f config/operator/namespace.yaml
kubectl apply -f config/rbac/

log "applying operator configmap..."
kubectl apply -f config/operator/configmap.yaml

log "deploying operator (kind-specific deployment)..."
kubectl apply -f test/e2e/manifests/bori-deployment-kind.yaml
kubectl apply -f test/e2e/manifests/bori-metrics-service.yaml
kubectl apply -f test/e2e/manifests/slint-sa.yaml

log "waiting for operator pod to be ready..."
kubectl -n "${NAMESPACE}" rollout status deployment/bori-operator --timeout=120s \
  || fail "operator deployment did not become ready"

# ── 4. fixture 적용 ───────────────────────────────────────────────────────────
log "applying smoke fixtures..."
kubectl apply -f test/e2e/fixtures/borirelease-minimal.yaml
kubectl apply -f test/e2e/fixtures/boridataplane-minimal.yaml

# ── 5. kube-slint: pre-workload metrics snapshot ─────────────────────────────
SLINT_AVAILABLE=false
SLI_SUMMARY_PATH="${ARTIFACTS_DIR}/sli-summary.json"
mkdir -p "${ARTIFACTS_DIR}"

if command -v go &>/dev/null && [ -d "${KUBE_SLINT_DIR}" ]; then
  log "building slint-gate from ${KUBE_SLINT_DIR}..."
  (cd "${KUBE_SLINT_DIR}" && GOTMPDIR=/home/heain/gotmp go build -o bin/slint-gate ./cmd/slint-gate 2>/dev/null) && \
    SLINT_AVAILABLE=true || log "slint-gate build skipped (non-fatal)"
fi

# metrics curl: pre-workload snapshot (slint-gate가 없을 때 raw metrics 저장)
POD_NAME=""
for i in $(seq 1 20); do
  POD_NAME=$(kubectl -n "${NAMESPACE}" get pod -l app.kubernetes.io/name=bori-operator \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  [ -n "${POD_NAME}" ] && break
  sleep 3
done
METRICS_PRE=""
if [ -n "${POD_NAME}" ]; then
  METRICS_PRE=$(kubectl -n "${NAMESPACE}" exec "${POD_NAME}" \
    -- wget -qO- http://localhost:8080/metrics 2>/dev/null || true)
  echo "${METRICS_PRE}" > "${ARTIFACTS_DIR}/metrics-pre.txt"
  log "pre-workload metrics snapshot saved"
fi

# ── 6. Go 테스트 실행 (k8s API assertions) ────────────────────────────────────
log "running Go smoke tests..."
if ! GOPROXY=off GOTMPDIR=/home/heain/gotmp \
    go test -tags kind -v -timeout 300s \
    ./test/e2e/ 2>&1 | tee "${ARTIFACTS_DIR}/go-test.log"; then
  fail "Go smoke test failed — see ${ARTIFACTS_DIR}/go-test.log"
fi

# ── 7. /metrics 접근 가능 여부 확인 ──────────────────────────────────────────
log "checking /metrics endpoint..."
METRICS_POST=""
if [ -n "${POD_NAME}" ]; then
  METRICS_POST=$(kubectl -n "${NAMESPACE}" exec "${POD_NAME}" \
    -- wget -qO- http://localhost:8080/metrics 2>/dev/null || true)
  if [ -z "${METRICS_POST}" ]; then
    log "WARNING: /metrics returned empty response"
  else
    echo "${METRICS_POST}" > "${ARTIFACTS_DIR}/metrics-raw.txt"
    log "/metrics OK ($(echo "${METRICS_POST}" | wc -l) lines)"
  fi
fi

# ── 8. kube-slint gate (summary-only) ────────────────────────────────────────
if [ "${SLINT_AVAILABLE}" = "true" ] && [ -n "${METRICS_PRE}" ] && [ -n "${METRICS_POST}" ]; then
  log "running slint-gate measurement..."
  # slint-gate는 sli-summary.json을 읽어서 gate 평가를 한다.
  # 현재 단계에서는 --fail-on NONE 으로 hard fail 없이 summary만 생성.
  SLINT_SA_TOKEN=$(kubectl -n "${NAMESPACE}" create token kube-slint --duration=1h 2>/dev/null || true)
  export SLINT_SA_TOKEN
  # go test로 kube-slint 세션을 실행 (별도 test 파일이 있을 경우 여기서 호출 가능)
  # 현재는 slint-gate CLI로 artifacts에 있는 raw metrics로 대체 실행
  "${KUBE_SLINT_DIR}/bin/slint-gate" \
    --measurement-summary "${SLI_SUMMARY_PATH}" \
    --policy "test/e2e/.slint/policy.yaml" \
    --fail-on NONE \
    > "${ARTIFACTS_DIR}/slint-gate-summary.json" 2>/dev/null || \
    log "slint-gate: skipped (sli-summary.json not yet available — need kube-slint session)"
else
  log "kube-slint: skipped (slint-gate not built or metrics unavailable)"
  log "  → to enable: ensure kube-slint is at ${KUBE_SLINT_DIR}"
fi

# ── 9. artifact 수집 (성공 시) ───────────────────────────────────────────────
collect_artifacts

# ── 완료 ─────────────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════"
echo "  kind smoke test PASSED"
echo ""
echo "  artifacts : ${ARTIFACTS_DIR}/"
if [ -f "${SLI_SUMMARY_PATH}" ]; then
  echo "  sli       : ${SLI_SUMMARY_PATH}"
fi
echo "════════════════════════════════════════════"
