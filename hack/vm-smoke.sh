#!/usr/bin/env bash
# vm-smoke.sh — VM integration smoke step, run ON the VM by `bori verify
# --smoke-cmd`, inside the kube-slint measurement window.
#
# Applies the BoriDataPlane fixture and waits (max 90s) until the operator has
# reconciled it. A non-zero exit fails the measurement.
#
# Usage: vm-smoke.sh <namespace> <fixture-name> <fixture-file>

set -euo pipefail

NAMESPACE="$1"
FIXTURE_NAME="$2"
FIXTURE="$3"

echo "[vm-smoke] applying fixture ${FIXTURE_NAME}..."
kubectl apply -f "${FIXTURE}"

echo "[vm-smoke] waiting for operator to reconcile..."
GEN=0
for _ in $(seq 1 18); do
  GEN=$(kubectl get boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.observedGeneration}' 2>/dev/null || echo 0)
  [ "${GEN:-0}" -ge 1 ] && break
  sleep 5
done
if [ "${GEN:-0}" -lt 1 ]; then
  echo "[vm-smoke] operator did not reconcile within 90s" >&2
  exit 1
fi
echo "[vm-smoke] reconciled: observedGeneration=${GEN}"
