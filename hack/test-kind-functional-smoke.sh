#!/usr/bin/env bash
# test-kind-functional-smoke.sh — bori operator K1 functional smoke (kind cluster)
#
# ═══ K1 functional smoke ══════════════════════════════════════════════════════
# 목적: bori-root = ConfigMap (environment + component), shell adapter no-op.
#       Runner.Run() 완료 → BoriRevision CR 생성 → Installed/Promoted 조건 확인.
#
# 검증 대상:
#   - CRD apply
#   - operator Deployment Ready (init container로 bori-root 구성)
#   - BoriDataPlane.status.observedGeneration >= 1  (Runner.Run() 완료)
#   - BoriDataPlane.status.conditions.Installed = True
#   - BoriRevision CR 생성
#   - BoriRelease.status.activeDataPlanes >= 1
#   - kube-slint SLI snapshot (sli-summary.json artifact)
#
# K0와의 차이:
#   - ConfigMap으로 environment + component 파일 주입
#   - busybox init container로 /bori-config/, /apps/ 구성
#   - shell adapter가 jumi/deploy.sh (exit 0) 실행 → deploy 성공
# ══════════════════════════════════════════════════════════════════════════════
#
# 전제 조건:
#   kind, docker, kubectl, go
#
# 사용법:
#   ./hack/test-kind-functional-smoke.sh          # 완료 후 클러스터 삭제
#   ./hack/test-kind-functional-smoke.sh --keep   # 클러스터 유지 (디버그)

set -euo pipefail

CLUSTER_NAME="bori-func-smoke"
IMAGE_NAME="bori-operator:dev"
NAMESPACE="bori-system"
KUBE_VERSION="${KUBE_VERSION:-v1.30.0}"
KEEP_CLUSTER="${1:-}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARTIFACTS_DIR="${REPO_ROOT}/artifacts/kind-func"
KUBE_SLINT_DIR="${KUBE_SLINT_DIR:-${REPO_ROOT}/../kube-slint}"

cd "${REPO_ROOT}"

log()  { echo "[kind-func-smoke] $*"; }
fail() { echo "[kind-func-smoke] FAIL: $*" >&2; collect_artifacts; exit 1; }

for cmd in kind docker kubectl go; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "[kind-func-smoke] error: '$cmd' not found in PATH" >&2
    exit 1
  fi
done

collect_artifacts() {
  log "collecting artifacts → ${ARTIFACTS_DIR}"
  mkdir -p "${ARTIFACTS_DIR}"
  export KUBECONFIG
  kubectl -n "${NAMESPACE}" logs -l app.kubernetes.io/name=bori-operator \
    --tail=500 > "${ARTIFACTS_DIR}/operator-logs.txt" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" logs -l app.kubernetes.io/name=bori-operator \
    --previous --tail=200 > "${ARTIFACTS_DIR}/operator-logs-prev.txt" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get events --sort-by='.lastTimestamp' \
    > "${ARTIFACTS_DIR}/events.txt" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get boridataplanes -o yaml \
    > "${ARTIFACTS_DIR}/boridataplanes.yaml" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get borireleases -o yaml \
    > "${ARTIFACTS_DIR}/borireleases.yaml" 2>/dev/null || true
  kubectl -n "${NAMESPACE}" get borirevisions -o yaml \
    > "${ARTIFACTS_DIR}/borirevisions.yaml" 2>/dev/null || true
  POD_NAME=$(kubectl -n "${NAMESPACE}" get pod -l app.kubernetes.io/name=bori-operator \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -n "${POD_NAME}" ]; then
    kubectl -n "${NAMESPACE}" exec "${POD_NAME}" \
      -- wget -qO- http://localhost:8080/metrics 2>/dev/null \
      > "${ARTIFACTS_DIR}/metrics-raw.txt" || true
  fi
  log "artifacts saved to ${ARTIFACTS_DIR}"
}

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

# ── 1. kind 클러스터 생성 ──────────────────────────────────────────────────────
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  log "reusing existing kind cluster '${CLUSTER_NAME}'"
else
  log "creating kind cluster '${CLUSTER_NAME}' (k8s ${KUBE_VERSION})..."
  kind create cluster --name "${CLUSTER_NAME}" \
    --image "kindest/node:${KUBE_VERSION}"
fi

KUBECONFIG_FILE="$(mktemp /tmp/bori-func-smoke-kubeconfig.XXXXXX)"
kind get kubeconfig --name "${CLUSTER_NAME}" > "${KUBECONFIG_FILE}"
export KUBECONFIG="${KUBECONFIG_FILE}"
log "KUBECONFIG=${KUBECONFIG}"

# ── 2. operator 이미지 빌드 + kind load ─────────────────────────────────────
log "building bori-operator image (${IMAGE_NAME})..."
docker build --quiet -t "${IMAGE_NAME}" "${REPO_ROOT}"
log "loading image into kind cluster..."
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# ── 3. CRD / RBAC / ConfigMaps / manifests 설치 ─────────────────────────────
log "installing CRDs..."
kubectl apply -f config/crd/ --server-side

log "creating namespace and RBAC..."
kubectl apply -f config/operator/namespace.yaml
kubectl apply -f config/rbac/

log "applying operator configmap..."
kubectl apply -f config/operator/configmap.yaml

log "applying K1-specific ConfigMaps (bori-func-config, bori-deploy-scripts)..."
kubectl apply -f test/e2e/manifests/bori-func-config.yaml
kubectl apply -f test/e2e/manifests/bori-deploy-scripts.yaml

log "deploying operator (K1 functional deployment)..."
kubectl apply -f test/e2e/manifests/bori-deployment-kind-func.yaml
kubectl apply -f test/e2e/manifests/bori-metrics-service.yaml
kubectl apply -f test/e2e/manifests/slint-sa.yaml

log "waiting for operator pod to be ready..."
kubectl -n "${NAMESPACE}" rollout status deployment/bori-operator --timeout=120s \
  || fail "operator deployment did not become ready"

# ── 4. K1 fixtures 적용 ─────────────────────────────────────────────────────
log "applying K1 smoke fixtures..."
kubectl apply -f test/e2e/fixtures/borirelease-functional.yaml
kubectl apply -f test/e2e/fixtures/boridataplane-functional.yaml

# ── 5. kube-slint SA token ───────────────────────────────────────────────────
SLINT_SA_TOKEN=$(kubectl -n "${NAMESPACE}" create token kube-slint --duration=1h 2>/dev/null || true)
export SLINT_SA_TOKEN

# ── 6. Go 테스트 실행 (Ginkgo K1) ─────────────────────────────────────────────
mkdir -p "${ARTIFACTS_DIR}"
log "running K1 functional smoke tests (Ginkgo)..."
if ! GOPROXY=off GOTMPDIR=/home/heain/gotmp \
    go test -tags kindfunc -v -timeout 300s \
    ./test/e2e/ 2>&1 | tee "${ARTIFACTS_DIR}/go-test.log"; then
  fail "K1 functional smoke test failed — see ${ARTIFACTS_DIR}/go-test.log"
fi

# ── 7. artifact 수집 ─────────────────────────────────────────────────────────
collect_artifacts

echo ""
echo "════════════════════════════════════════════"
echo "  K1 functional smoke PASSED"
echo ""
echo "  artifacts : ${ARTIFACTS_DIR}/"
if [ -f "${ARTIFACTS_DIR}/sli-summary.json" ]; then
  echo "  sli       : ${ARTIFACTS_DIR}/sli-summary.json"
fi
echo "════════════════════════════════════════════"
