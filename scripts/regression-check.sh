#!/usr/bin/env bash
# regression-check.sh — BoriDataPlane conditions 회귀 측정
#
# 두 가지 실행 컨텍스트를 자동 감지:
#   로컬 머신: kubectl 없음 → seoy@100.123.80.48 에 SSH 후 실행
#   원격 K8s 노드: kubectl 직접 사용 (make deploy 후 자동 호출 시)
#
# 사용법:
#   ./scripts/regression-check.sh                  # 회귀 비교
#   ./scripts/regression-check.sh --update-baseline  # baseline 갱신
#
# 자동화 경로:
#   Tailscale GitHub Action 연동 후 .github/workflows/e2e.yaml 에서 호출 가능

set -euo pipefail

REMOTE="seoy@100.123.80.48"
NAMESPACE="bori-system"
FIXTURE_NAME="infra-lab-smoke"
FIXTURE="testdata/fixtures/bdp-infra-lab-smoke.yaml"
BASELINE="testdata/baseline/infra-lab-smoke-conditions.json"
UPDATE_BASELINE="${1:-}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

log()  { echo "[regression] $*"; }
fail() { echo "[regression] FAIL: $*" >&2; exit 1; }

# ── 컨텍스트 감지: kubectl 직접 접근 가능 여부 ────────────────────────────────
if kubectl cluster-info --request-timeout=3s &>/dev/null; then
  log "context: running on cluster node (direct kubectl)"
  run_kubectl() { kubectl "$@"; }
  run_k8sgpt()  { /usr/bin/k8sgpt "$@"; }
  capture_snapshot() {
    kubectl get boridataplane "$FIXTURE_NAME" -n "$NAMESPACE" -o json \
      | jq '{resource: .metadata.name, namespace: .metadata.namespace,
             release: .spec.release, environment: .spec.environment,
             conditions: [.status.conditions[] | {type: .type, status: .status, reason: .reason}]}'
  }
  apply_fixture() { kubectl apply -f -; }
else
  log "context: running locally (SSH → $REMOTE)"
  run_kubectl() { ssh "$REMOTE" kubectl "$@"; }
  run_k8sgpt()  { ssh "$REMOTE" /usr/bin/k8sgpt "$@"; }
  capture_snapshot() {
    ssh "$REMOTE" \
      "kubectl get boridataplane $FIXTURE_NAME -n $NAMESPACE -o json \
       | jq '{resource: .metadata.name, namespace: .metadata.namespace,
              release: .spec.release, environment: .spec.environment,
              conditions: [.status.conditions[] | {type: .type, status: .status, reason: .reason}]}'"
  }
  apply_fixture() { ssh "$REMOTE" "kubectl apply -f -"; }
fi

# ── 1. fixture 적용 ───────────────────────────────────────────────────────────
log "applying fixture $FIXTURE_NAME..."
apply_fixture < "$FIXTURE"

# ── 2. reconcile 대기 (최대 60초) ────────────────────────────────────────────
log "waiting for operator to reconcile..."
for i in $(seq 1 12); do
  GEN=$(run_kubectl get boridataplane "$FIXTURE_NAME" -n "$NAMESPACE" \
    -o jsonpath='{.status.observedGeneration}' 2>/dev/null || echo 0)
  [ "${GEN:-0}" -ge 1 ] && break
  sleep 5
done
[ "${GEN:-0}" -ge 1 ] || fail "operator did not reconcile within 60s"

# ── 3. k8sgpt 분석 (필수 절차) ───────────────────────────────────────────────
log "running k8sgpt analysis..."
run_k8sgpt analyze --namespace "$NAMESPACE" --explain=false || true

# ── 4. conditions 스냅샷 캡처 ────────────────────────────────────────────────
log "capturing conditions snapshot..."
SNAPSHOT=$(capture_snapshot)

# ── 5. baseline 갱신 모드 ─────────────────────────────────────────────────────
if [ "$UPDATE_BASELINE" = "--update-baseline" ]; then
  echo "$SNAPSHOT" > "$BASELINE"
  log "baseline updated: $BASELINE"
  exit 0
fi

# ── 6. 회귀 비교 ─────────────────────────────────────────────────────────────
if [ ! -f "$BASELINE" ]; then
  log "no baseline found — saving current snapshot as baseline"
  echo "$SNAPSHOT" > "$BASELINE"
  log "saved: $BASELINE"
  exit 0
fi

BASELINE_CONDITIONS=$(jq -r '.conditions | map("\(.type)=\(.status)/\(.reason)") | sort | join(",")' "$BASELINE")
CURRENT_CONDITIONS=$(echo "$SNAPSHOT" | jq -r '.conditions | map("\(.type)=\(.status)/\(.reason)") | sort | join(",")')

if [ "$BASELINE_CONDITIONS" = "$CURRENT_CONDITIONS" ]; then
  log "OK — conditions match baseline"
  log "  $CURRENT_CONDITIONS"
  exit 0
fi

echo ""
echo "=== REGRESSION DETECTED ==="
echo "baseline : $BASELINE_CONDITIONS"
echo "current  : $CURRENT_CONDITIONS"
echo ""
diff <(echo "$BASELINE_CONDITIONS" | tr ',' '\n') \
     <(echo "$CURRENT_CONDITIONS"  | tr ',' '\n') || true
echo "==========================="
echo ""
fail "BoriDataPlane conditions have changed from baseline"
