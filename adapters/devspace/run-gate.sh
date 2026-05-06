#!/usr/bin/env bash
set -euo pipefail

# bori DevSpace adapter
# Translates DevSpace lifecycle events into kube-slint gate evaluations.
# Each app's .bori/policy.yaml is discovered and passed to slint-gate.

PROFILE="${1:-devspace}"
BORI_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
POLICY_FILES=()

# Discover policy files from registered app components
while IFS= read -r -d '' policy; do
  POLICY_FILES+=("$policy")
done < <(find "$BORI_ROOT/.." -maxdepth 3 -path "*/.bori/policy.yaml" -print0 2>/dev/null)

if [[ ${#POLICY_FILES[@]} -eq 0 ]]; then
  echo "[bori] no .bori/policy.yaml files found — skipping gate"
  exit 0
fi

echo "[bori] running kube-slint gate (profile: $PROFILE)"
for policy in "${POLICY_FILES[@]}"; do
  echo "[bori]   policy: $policy"
done

# Invoke slint-gate with discovered policies
slint-gate \
  --profile "$PROFILE" \
  "${POLICY_FILES[@]/#/--policy=}"
