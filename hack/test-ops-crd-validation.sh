#!/usr/bin/env bash
# test-ops-crd-validation.sh — live API-server checks for the ops.bori.dev
# (O2 candidate) CRDs and the non-actuating operational controller.
#
# Verifies on a real kind API server what unit tests cannot:
#   - CRD schemas are accepted (CEL compiles, within cost budget)
#   - OperationalContract spec immutability (CEL self == oldSelf)
#   - bounded grammar: exactly-one requirement/operand, enums
#   - OperationalReferenceGrant: typed, exact, no Secret/Action, no wildcard
#   - contractRef has no namespace field (strict field validation)
#   - zero-churn: repeated evaluations do not write status
#
# Usage: hack/test-ops-crd-validation.sh [--keep]
set -euo pipefail

CLUSTER="${CLUSTER:-bori-ops-crd}"
KUBE_VERSION="${KUBE_VERSION:-v1.30.0}"
KEEP=false
[[ "${1:-}" == "--keep" ]] && KEEP=true
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
OP_PID=""
FAILURES=0

cleanup() {
  if [[ -n "$OP_PID" ]]; then kill "$OP_PID" 2>/dev/null || true; fi
  if ! $KEEP; then kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

pass() { echo "PASS  $*"; }
# check DESCRIPTION COMMAND... — pass if COMMAND succeeds, fail otherwise.
check() { local d="$1"; shift; if "$@"; then pass "$d"; else fail "$d"; fi; }
eq() { [[ "$1" == "$2" ]]; }
fail() { echo "FAIL  $*"; FAILURES=$((FAILURES + 1)); }

# expect_accept NAME — applies stdin (never fed through a pipe, so that
# FAILURES updates stay in this shell) and requires success.
expect_accept() {
  local name="$1" out
  if out="$(kubectl apply --validate=strict -f - 2>&1)"; then pass "accept: $name"; else fail "accept: $name: $out"; fi
}

# expect_reject NAME PATTERN — applies stdin and requires a rejection whose
# message matches PATTERN.
expect_reject() {
  local name="$1" pattern="$2" out
  if out="$(kubectl apply --validate=strict -f - 2>&1)"; then
    fail "reject: $name: unexpectedly accepted"
  elif grep -Eqi -- "$pattern" <<<"$out"; then
    pass "reject: $name"
  else
    fail "reject: $name: wrong error: $out"
  fi
}

echo "── cluster"
kind create cluster --name "$CLUSTER" --image "kindest/node:${KUBE_VERSION}" --wait 120s
kubectl create namespace apps
kubectl create namespace other

echo "── CRDs"
kubectl apply --server-side -f "$ROOT/config/crd/"
for crd in operationalcontracts operationaltargets operationalreferencegrants; do
  kubectl wait --for=condition=Established "crd/${crd}.ops.bori.dev" --timeout=60s
done

contract() { # contract NAME MAXAGE
  cat <<EOF
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalContract
metadata: {name: $1, namespace: apps}
spec:
  assertions:
    - {name: serving, type: Boolean, maxAge: $2}
    - {name: replicas, type: Integer, maxAge: 30s}
  capabilities:
    - type: {domain: example.io, name: serve, revision: v1}
      requirements:
        - predicate: {assertion: serving, operator: IsTrue}
          onUnmet: UNAVAILABLE
  envelopes:
    - name: minimum
      class: Minimum
      requirements:
        - {assertion: replicas, operator: Gte, operand: {integer: 1}}
EOF
}

echo "── OperationalContract"
expect_accept "valid contract" < <(contract svc-v1 30s)
expect_reject "spec update (immutability)" "immutable" < <(contract svc-v1 60s)
check "metadata update allowed" \
  kubectl patch operationalcontract svc-v1 -n apps --type merge -p '{"metadata":{"labels":{"team":"x"}}}' -o name

bad_requirement() { # NAME REQUIREMENT-YAML
  cat <<EOF
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalContract
metadata: {name: $1, namespace: apps}
spec:
  assertions:
    - {name: serving, type: Boolean, maxAge: 30s}
  envelopes:
    - {name: minimum, class: Minimum, requirements: [{assertion: serving, operator: IsTrue}]}
  capabilities:
    - type: {domain: example.io, name: serve, revision: v1}
      requirements:
        - $2
EOF
}
expect_reject "requirement with two inputs" "exactly one of predicate" < <(bad_requirement bad-two-kinds '{predicate: {assertion: serving, operator: IsTrue}, envelope: minimum, onUnmet: DEGRADED}')
expect_reject "requirement with no input" "exactly one of predicate" < <(bad_requirement bad-no-kind '{onUnmet: DEGRADED}')
expect_reject "non-canonical operand" "exactly one typed value" < <(bad_requirement bad-operand '{predicate: {assertion: serving, operator: Eq, operand: {boolean: true, integer: 1}}, onUnmet: DEGRADED}')
expect_reject "unknown operator" "Unsupported value" < <(bad_requirement bad-op '{predicate: {assertion: serving, operator: Matches}, onUnmet: DEGRADED}')
expect_reject "onUnmet UNKNOWN" "Unsupported value" < <(bad_requirement bad-impact '{predicate: {assertion: serving, operator: IsTrue}, onUnmet: UNKNOWN}')

echo "── OperationalReferenceGrant"
grant() { # NAME FROM-KIND FROM-NS TO-TYPE TO-NAME
  cat <<EOF
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalReferenceGrant
metadata: {name: $1, namespace: other}
spec:
  from: [{kind: $2, namespace: "$3"}]
  to: [{type: $4, name: "$5"}]
EOF
}
expect_accept "dependency grant" < <(grant g-dep OperationalTarget apps Dependency resolver)
expect_accept "evidence-provider grant" < <(grant g-prov OperationalTarget apps EvidenceProvider generic-http-probe)
expect_reject "Secret reference type" "Unsupported value" < <(grant g-secret OperationalTarget apps Secret creds)
expect_reject "Action reference type" "Unsupported value" < <(grant g-action OperationalTarget apps Action restart)
expect_reject "wildcard from namespace" "should match" < <(grant g-wild-ns OperationalTarget '*' Dependency resolver)
expect_reject "wildcard name" "should match" < <(grant g-wild-name OperationalTarget apps Dependency '*')
expect_reject "from kind other than OperationalTarget" "Unsupported value" < <(grant g-kind Pod apps Dependency resolver)

echo "── OperationalTarget"
expect_reject "cross-namespace contractRef" "unknown field" <<EOF
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalTarget
metadata: {name: bad-ref, namespace: apps}
spec:
  targetRef: {apiVersion: apps/v1, kind: Deployment, name: web}
  contractRef: {name: svc-v1, namespace: other}
EOF

echo "── controller (non-actuating, zero-churn)"
kubectl create deployment web -n apps --image=registry.k8s.io/pause:3.9 >/dev/null
expect_accept "valid target" <<EOF
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalTarget
metadata: {name: web, namespace: apps}
spec:
  targetRef: {apiVersion: apps/v1, kind: Deployment, name: web}
  contractRef: {name: svc-v1}
  assertionBindings:
    - slot: serving
      provider: {name: generic-http-probe, configRevision: r1}
EOF
deploy_before="$(kubectl get deployment web -n apps -o jsonpath='{.metadata.resourceVersion}')"

(cd "$ROOT" && go build -o "$WORK/bori-operator" ./cmd/bori-operator)
mkdir -p "$WORK/root"
"$WORK/bori-operator" --bori-root "$WORK/root" --bori-dir "$WORK/root/.bori" \
  --metrics-bind-address 0 --health-probe-bind-address 0 \
  --enable-operational-assessment --operational-requeue-interval 2s >"$WORK/operator.log" 2>&1 &
OP_PID=$!

for _ in $(seq 1 60); do
  [[ -n "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.assessedAt}' 2>/dev/null)" ]] && break
  sleep 1
done
state="$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.capabilities[0].state}')"
check "no evidence source → UNKNOWN (got '$state')" eq "$state" "UNKNOWN"
check "sync NotApplicable" eq "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.sync}')" "NotApplicable"
digest="$(kubectl get operationalcontract svc-v1 -n apps -o jsonpath='{.status.specDigest}')"
check "contract status digest ($digest)" eq "${digest%%:*}" "sha256"

rv1="$(kubectl get operationaltarget web -n apps -o jsonpath='{.metadata.resourceVersion}')"
crv1="$(kubectl get operationalcontract svc-v1 -n apps -o jsonpath='{.metadata.resourceVersion}')"
sleep 12 # ≥ 5 requeue intervals
rv2="$(kubectl get operationaltarget web -n apps -o jsonpath='{.metadata.resourceVersion}')"
crv2="$(kubectl get operationalcontract svc-v1 -n apps -o jsonpath='{.metadata.resourceVersion}')"
check "zero status writes across repeated evaluations (target $rv1→$rv2, contract $crv1→$crv2)" \
  eq "$rv1/$crv1" "$rv2/$crv2"

check "workload not mutated" eq "$(kubectl get deployment web -n apps -o jsonpath='{.metadata.resourceVersion}')" "$deploy_before"

kubectl delete deployment web -n apps >/dev/null
for _ in $(seq 1 30); do
  [[ "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.valid}')" == "false" ]] && break
  sleep 1
done
check "workload deletion → target-not-found" \
  eq "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.invalidReasons[0].code}')" "target-not-found"

if ((FAILURES > 0)); then
  echo "── operator log (tail)"; tail -50 "$WORK/operator.log"
  echo "$FAILURES check(s) failed"; exit 1
fi
echo "all checks passed"
