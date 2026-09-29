#!/usr/bin/env bash
# vm-smoke.sh — VM integration smoke step, run ON the VM by `bori verify
# --smoke-cmd`, inside the kube-slint measurement window.
#
# Recreates the BoriDataPlane fixture and waits (max 90s by default) until the
# operator has reconciled that new object. Status left by an earlier run must
# never count: the fixture is deleted and created again, so it has a new UID,
# and only that UID's status.observedGeneration equal to its
# metadata.generation is accepted. A non-zero exit fails the measurement.
#
# Deleting the fixture only removes the operator's finalizer; bori keeps its
# shadow state and revision history on disk.
#
# Usage: vm-smoke.sh <namespace> <fixture-name> <fixture-file>
# Env:   VM_SMOKE_ATTEMPTS (default 18), VM_SMOKE_POLL_SECONDS (default 5)

set -euo pipefail

NAMESPACE="$1"
FIXTURE_NAME="$2"
FIXTURE="$3"
ATTEMPTS="${VM_SMOKE_ATTEMPTS:-18}"
POLL_SECONDS="${VM_SMOKE_POLL_SECONDS:-5}"

echo "[vm-smoke] deleting fixture ${FIXTURE_NAME} left by an earlier run..."
kubectl delete boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
  --ignore-not-found --wait=true --timeout=60s

echo "[vm-smoke] creating fixture ${FIXTURE_NAME}..."
# create (not apply) fails if the old object still exists.
UID_NEW="" GEN_NEW=""
read -r UID_NEW GEN_NEW < <(kubectl create -f "${FIXTURE}" \
  -o jsonpath='{.metadata.uid}{" "}{.metadata.generation}{"\n"}') || true
if [ -z "${UID_NEW:-}" ] || [ -z "${GEN_NEW:-}" ]; then
  echo "[vm-smoke] could not read uid/generation of the created fixture" >&2
  exit 1
fi
echo "[vm-smoke] created uid=${UID_NEW} generation=${GEN_NEW}"

echo "[vm-smoke] waiting for operator to reconcile generation ${GEN_NEW}..."
for _ in $(seq 1 "${ATTEMPTS}"); do
  UID_SEEN="" OBS=""
  read -r UID_SEEN OBS < <(kubectl get boridataplane "${FIXTURE_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.metadata.uid}{" "}{.status.observedGeneration}{"\n"}' 2>/dev/null || true) || true
  if [ "${UID_SEEN}" = "${UID_NEW}" ] && [ "${OBS}" = "${GEN_NEW}" ]; then
    echo "[vm-smoke] reconciled: uid=${UID_NEW} observedGeneration=${OBS}"
    exit 0
  fi
  sleep "${POLL_SECONDS}"
done
echo "[vm-smoke] operator did not reconcile uid=${UID_NEW} generation=${GEN_NEW}" \
  "within $((ATTEMPTS * POLL_SECONDS))s (last seen uid=${UID_SEEN:-none}" \
  "observedGeneration=${OBS:-none})" >&2
exit 1
