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
#   - O3 reference profile: real Kubernetes status + HTTP typed-response
#     providers through controller -> O1 -> status; refresh with zero writes,
#     real transitions, provider outage != application failure, unrelated
#     capability not contaminated, no workload mutation
#   - O4 interaction summary: derived level/summary in status and the
#     human-readable query, zero churn across refreshes, a declared approval
#     boundary → DECISION_REQUIRED (display only), outage ≠ app failure,
#     no workload mutation
#   - O5 action proposals (shipped wiring: no provider, non-durable journal,
#     no verifier): proposal and its blocking reasons in status, accepted by
#     the schema at maximum-length names, nothing handed off, no workload
#     mutation, zero churn, the proposal disappears on recovery without any
#     recovery claim, and the flag guard
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
APP_PID=""
APP4_PID=""
FAILURES=0

cleanup() {
  if [[ -n "$OP_PID" ]]; then kill "$OP_PID" 2>/dev/null || true; fi
  if [[ -n "$APP_PID" ]]; then kill "$APP_PID" 2>/dev/null || true; fi
  if [[ -n "$APP4_PID" ]]; then kill "$APP4_PID" 2>/dev/null || true; fi
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
# Deployment status is updated by kube-controller-manager, so resourceVersion
# is not a mutation signal; spec generation and field managers are.
deploy_gen_before="$(kubectl get deployment web -n apps -o jsonpath='{.metadata.generation}')"

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

check "workload spec not mutated (generation)" \
  eq "$(kubectl get deployment web -n apps -o jsonpath='{.metadata.generation}')" "$deploy_gen_before"
managers="$(kubectl get deployment web -n apps -o jsonpath='{.metadata.managedFields[*].manager}')"
check "no bori field manager on workload ($managers)" eq "${managers//bori/}" "$managers"

kubectl delete deployment web -n apps >/dev/null
for _ in $(seq 1 30); do
  [[ "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.valid}')" == "false" ]] && break
  sleep 1
done
check "workload deletion → target-not-found" \
  eq "$(kubectl get operationaltarget web -n apps -o jsonpath='{.status.invalidReasons[0].code}')" "target-not-found"

echo "── O3 reference providers (Kubernetes status + HTTP typed response)"
kill "$OP_PID" 2>/dev/null || true
wait "$OP_PID" 2>/dev/null || true
OP_PID=""

# wait_eq DESCRIPTION EXPECTED TIMEOUT COMMAND... — poll until COMMAND prints EXPECTED.
wait_eq() {
  local d="$1" want="$2" timeout="$3" got=""
  shift 3
  for _ in $(seq 1 "$timeout"); do
    got="$("$@" 2>/dev/null || true)"
    if [[ "$got" == "$want" ]]; then
      pass "$d"
      return
    fi
    sleep 1
  done
  fail "$d (got '$got', want '$want')"
}
cap_state() { kubectl get operationaltarget svc -n apps -o jsonpath="{.status.capabilities[?(@.type.name==\"$1\")].state}"; }
slot_state() { kubectl get operationaltarget svc -n apps -o jsonpath="{.status.evidence[?(@.slot==\"$1\")].state}"; }

kubectl create deployment svc -n apps --image=registry.k8s.io/pause:3.9 --replicas=1 >/dev/null
kubectl rollout status deployment/svc -n apps --timeout=120s >/dev/null
svc_uid="$(kubectl get deployment svc -n apps -o jsonpath='{.metadata.uid}')"

# Controlled HTTP producer of typed responses. Its payload also carries a URL,
# a namespace and an instruction, all of which must be ignored.
echo true >"$WORK/value"
: >"$WORK/hits"
cp "$ROOT/hack/ops-typed-producer.py" "$WORK/app.py"
python3 "$WORK/app.py" 18080 "$WORK/value" "$WORK/hits" "$svc_uid" &
APP_PID=$!

expect_accept "O3 contract" <<YAML
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalContract
metadata: {name: svc-o3-v1, namespace: apps}
spec:
  assertions:
    - {name: api-serving, type: Boolean, maxAge: 30s}
    - {name: available-replicas, type: Integer, maxAge: 30s}
    - {name: ready-replicas, type: Integer, maxAge: 30s}
  capabilities:
    - type: {domain: example.io, name: provide, revision: v1}
      requirements:
        - {predicate: {assertion: api-serving, operator: IsTrue}, onUnmet: UNAVAILABLE}
        - {predicate: {assertion: available-replicas, operator: Gte, operand: {integer: 1}}, onUnmet: UNAVAILABLE}
    - type: {domain: example.io, name: count, revision: v1}
      requirements:
        - {predicate: {assertion: ready-replicas, operator: Gte, operand: {integer: 1}}, onUnmet: DEGRADED}
YAML
expect_accept "O3 target" <<YAML
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalTarget
metadata: {name: svc, namespace: apps}
spec:
  targetRef: {apiVersion: apps/v1, kind: Deployment, name: svc}
  contractRef: {name: svc-o3-v1}
  assertionBindings:
    - {slot: api-serving, provider: {name: app-http, configRevision: r1}}
    - {slot: available-replicas, provider: {name: kube-status, configRevision: r1}}
    - {slot: ready-replicas, provider: {name: kube-status, configRevision: r1}}
YAML
cat >"$WORK/providers.yaml" <<YAML
http: {allowInsecureHTTP: true, allowPrivateNetworks: true}
providers:
  - namespace: apps
    name: kube-status
    configRevision: r1
    kubernetesStatus:
      fields: {available-replicas: availableReplicas, ready-replicas: readyReplicas}
  - namespace: apps
    name: app-http
    configRevision: r1
    http:
      endpoints:
        - subjectUID: $svc_uid
          url: http://127.0.0.1:18080/bori/assertions
          fields: {api-serving: serving}
YAML
"$WORK/bori-operator" --bori-root "$WORK/root" --bori-dir "$WORK/root/.bori" \
  --metrics-bind-address 0 --health-probe-bind-address 0 \
  --enable-operational-assessment --operational-requeue-interval 2s \
  --operational-provider-config "$WORK/providers.yaml" >"$WORK/operator-o3.log" 2>&1 &
OP_PID=$!

wait_eq "both providers → provide AVAILABLE" AVAILABLE 60 cap_state provide
wait_eq "count AVAILABLE" AVAILABLE 10 cap_state count
check "HTTP evidence current" eq "$(slot_state api-serving)" "Current"
check "Kubernetes evidence current" eq "$(slot_state available-replicas)" "Current"

rv1="$(kubectl get operationaltarget svc -n apps -o jsonpath='{.metadata.resourceVersion}')"
hits1="$(wc -l <"$WORK/hits")"
sleep 25 # spans at least one freshness refresh (due 15s before the 30s maxAge)
rv2="$(kubectl get operationaltarget svc -n apps -o jsonpath='{.metadata.resourceVersion}')"
hits2="$(wc -l <"$WORK/hits")"
check "evidence refreshed (HTTP calls $hits1→$hits2)" test "$hits2" -gt "$hits1"
check "refreshed identical facts write no status (rv $rv1→$rv2)" eq "$rv1" "$rv2"
check "only the configured path was ever requested" eq "$(grep -vc '^/bori/assertions$' "$WORK/hits" || true)" "0"

echo false >"$WORK/value"
wait_eq "application fact false → provide UNAVAILABLE" UNAVAILABLE 45 cap_state provide
check "unrelated count stays AVAILABLE" eq "$(cap_state count)" "AVAILABLE"

echo down >"$WORK/value"
wait_eq "HTTP provider outage → provide UNKNOWN (not UNAVAILABLE)" UNKNOWN 45 cap_state provide
check "outage evidence is ProviderUnavailable" eq "$(slot_state api-serving)" "ProviderUnavailable"
check "count not contaminated by the HTTP outage" eq "$(cap_state count)" "AVAILABLE"

kubectl scale deployment svc -n apps --replicas=0 >/dev/null # test driver, not the operator
wait_eq "Kubernetes fact 0 ready → count DEGRADED" DEGRADED 60 cap_state count

managers="$(kubectl get deployment svc -n apps -o jsonpath='{.metadata.managedFields[*].manager}')"
check "no bori field manager on the O3 workload ($managers)" eq "${managers//bori/}" "$managers"
check "workload generation changed only by the driver scale" \
  eq "$(kubectl get deployment svc -n apps -o jsonpath='{.metadata.generation}')" "2"

echo "── O4 operator interaction summary"
kill "$OP_PID" 2>/dev/null || true
wait "$OP_PID" 2>/dev/null || true
OP_PID=""

itr() { kubectl get operationaltarget "$1" -n apps -o jsonpath="{.status.interaction.$2}"; }

kubectl create deployment web4 -n apps --image=registry.k8s.io/pause:3.9 --replicas=1 >/dev/null
kubectl rollout status deployment/web4 -n apps --timeout=120s >/dev/null
web4_uid="$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.uid}')"
web4_gen="$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.generation}')"
echo true >"$WORK/value4"
: >"$WORK/hits4"
python3 "$WORK/app.py" 18081 "$WORK/value4" "$WORK/hits4" "$web4_uid" &
APP4_PID=$!

expect_accept "O4 target" <<YAML
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalTarget
metadata: {name: web4, namespace: apps}
spec:
  targetRef: {apiVersion: apps/v1, kind: Deployment, name: web4}
  contractRef: {name: svc-o3-v1}
  assertionBindings:
    - {slot: api-serving, provider: {name: app-http4, configRevision: r1}}
    - {slot: available-replicas, provider: {name: kube-status, configRevision: r1}}
    - {slot: ready-replicas, provider: {name: kube-status, configRevision: r1}}
YAML
cat "$WORK/providers.yaml" >"$WORK/providers4.yaml"
cat >>"$WORK/providers4.yaml" <<YAML
  - namespace: apps
    name: app-http4
    configRevision: r1
    http:
      endpoints:
        - subjectUID: $web4_uid
          url: http://127.0.0.1:18081/bori/assertions
          fields: {api-serving: serving}
YAML
# Maximum-length legal names: the O3 candidate ID for the envelope predicate
# is 528 characters; the summary carrying it must be accepted by the API server.
L63a="$(printf 'a%.0s' $(seq 63))"; L63b="$(printf 'b%.0s' $(seq 63))"; L63c="$(printf 'c%.0s' $(seq 63))"
MAXDOM="$L63a.$L63b.$L63c.$(printf 'd%.0s' $(seq 61))"
MAXCAP="$(printf 'n%.0s' $(seq 63))"; MAXREV="$(printf 'r%.0s' $(seq 63))"
MAXENV="$(printf 'e%.0s' $(seq 63))"; MAXSLOT="$(printf 's%.0s' $(seq 63))"
expect_accept "O4 max-length contract" <<YAML
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalContract
metadata: {name: max-v1, namespace: apps}
spec:
  assertions:
    - {name: $MAXSLOT, type: Integer, maxAge: 30s}
  capabilities:
    - type: {domain: $MAXDOM, name: $MAXCAP, revision: $MAXREV}
      requirements:
        - {envelope: $MAXENV, onUnmet: DEGRADED}
  envelopes:
    - name: $MAXENV
      class: Recommended
      requirements:
        - {assertion: $MAXSLOT, operator: Gte, operand: {integer: 5}}
YAML
expect_accept "O4 max-length target" <<YAML
apiVersion: ops.bori.dev/v1alpha1
kind: OperationalTarget
metadata: {name: max4, namespace: apps}
spec:
  targetRef: {apiVersion: apps/v1, kind: Deployment, name: web4}
  contractRef: {name: max-v1}
  assertionBindings:
    - {slot: $MAXSLOT, provider: {name: kube-max, configRevision: r1}}
YAML
cat >>"$WORK/providers4.yaml" <<YAML
  - namespace: apps
    name: kube-max
    configRevision: r1
    kubernetesStatus:
      fields: {$MAXSLOT: readyReplicas}
YAML
cat >"$WORK/profile.json" <<JSON
{"revision": "p1", "responses": [{
  "action": {"name": "restart", "revision": "r1"},
  "target": {"namespace": "apps", "name": "web4"},
  "for": {"domain": "example.io", "name": "provide", "revision": "v1"},
  "owner": "app-oncall", "requiresApproval": true, "risks": ["service-interruption"]}]}
JSON
"$WORK/bori-operator" --bori-root "$WORK/root" --bori-dir "$WORK/root/.bori" \
  --metrics-bind-address 0 --health-probe-bind-address 0 \
  --enable-operational-assessment --operational-requeue-interval 2s \
  --operational-provider-config "$WORK/providers4.yaml" \
  --enable-operational-interaction --operational-interaction-profile "$WORK/profile.json" \
  >"$WORK/operator-o4.log" 2>&1 &
OP_PID=$!

wait_eq "healthy web4 → NO_ACTION on current evidence" NO_ACTION 60 itr web4 level
check "web4 summary states current proof ($(itr web4 summary))" eq "$(itr web4 summary)" "all 2 capabilities AVAILABLE on current evidence"
check "web4 investigation concluded" eq "$(itr web4 investigation.outcome)" "Concluded"
check "printer column shows the level" grep -q "INTERACTION" <(kubectl get operationaltargets -n apps 2>&1)
(cd "$ROOT" && go build -o "$WORK/bori" ./cmd/bori)
kubectl get operationaltargets -n apps -o json >"$WORK/targets.json"
check "bori ops interaction renders the summary" grep -q "level:      NO_ACTION" <("$WORK/bori" ops interaction -f "$WORK/targets.json")

wait_eq "max-length target summary written (envelope gap → AWARENESS)" AWARENESS 60 itr max4 level
longest_id() { kubectl get operationaltarget max4 -n apps -o json | python3 -c 'import json,sys; c=json.load(sys.stdin)["status"]["interaction"].get("causeCandidates",[]); print(max([len(x["id"]) for x in c] or [0]))'; }
wait_eq "API server accepts the 528-character candidate ID" 528 30 longest_id

rv1="$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')"
hits1="$(wc -l <"$WORK/hits4")"
sleep 25 # spans at least one freshness refresh
rv2="$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')"
hits2="$(wc -l <"$WORK/hits4")"
check "O4 evidence refreshed (HTTP calls $hits1→$hits2)" test "$hits2" -gt "$hits1"
check "refresh writes no status with the summary (rv $rv1→$rv2)" eq "$rv1" "$rv2"

echo false >"$WORK/value4"
wait_eq "declared approval boundary → DECISION_REQUIRED" DECISION_REQUIRED 45 itr web4 level
check "affected capability is provide" eq "$(itr web4 'affected[*].type.name')" "provide"
check "human reason approval-required" eq "$(itr web4 'humanReasons[0].code')" "approval-required"
check "response shown, not run ($(itr web4 'responses[0].status'))" eq "$(itr web4 'responses[0].status')" "ApprovalRequired"
check "post-condition unconfirmed" eq "$(itr web4 'pendingPostConditions[0].detail')" "unconfirmed"
check "svc (outage + scaled down) is not NO_ACTION ($(itr svc level))" test "$(itr svc level)" != "NO_ACTION"
check "svc outage is unknown, not affected" eq "$(itr svc 'unknown[*].type.name')" "provide"
rv3="$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')"
sleep 12
check "steady decision writes nothing ($rv3)" eq "$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')" "$rv3"

managers="$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.managedFields[*].manager}')"
check "no bori field manager on the O4 workload ($managers)" eq "${managers//bori/}" "$managers"
check "O4 workload generation unchanged" eq "$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.generation}')" "$web4_gen"

echo "── O5 action proposals (shipped wiring: display and gating only)"
kill "$OP_PID" 2>/dev/null || true
wait "$OP_PID" 2>/dev/null || true
OP_PID=""

if "$WORK/bori-operator" --bori-root "$WORK/root" --bori-dir "$WORK/root/.bori" \
  --metrics-bind-address 0 --health-probe-bind-address 0 \
  --enable-operational-assessment --enable-operational-actions >"$WORK/operator-guard.log" 2>&1; then
  fail "--enable-operational-actions without interaction must refuse to start"
else
  check "--enable-operational-actions requires interaction" grep -q "requires --enable-operational-interaction" "$WORK/operator-guard.log"
fi

cat >"$WORK/profile5.json" <<JSON
{"revision": "p5", "responses": [
 {"action": {"name": "restart", "revision": "r1"},
  "target": {"namespace": "apps", "name": "web4"},
  "for": {"domain": "example.io", "name": "provide", "revision": "v1"},
  "owner": "app-oncall", "requiresApproval": true, "risks": ["service-interruption"],
  "execution": {"provider": "deployment-actor", "approvers": ["alice"],
    "expectedImpact": [{"domain": "example.io", "name": "provide", "revision": "v1"}],
    "ackTimeout": "30s", "completionTimeout": "5m", "recoveryWindow": "2m"}},
 {"action": {"name": "$(printf 'm%.0s' $(seq 63))", "revision": "$(printf 'v%.0s' $(seq 63))"},
  "target": {"namespace": "apps", "name": "max4"},
  "for": {"domain": "$MAXDOM", "name": "$MAXCAP", "revision": "$MAXREV"},
  "owner": "app-oncall", "states": ["DEGRADED"],
  "preconditions": [{"domain": "$MAXDOM", "name": "$MAXCAP", "revision": "$MAXREV"}],
  "execution": {"provider": "deployment-actor",
    "expectedImpact": [{"domain": "$MAXDOM", "name": "$MAXCAP", "revision": "$MAXREV"}],
    "ackTimeout": "30s", "completionTimeout": "5m", "recoveryWindow": "2m"}}]}
JSON
objects_before="$(kubectl get deployments,replicasets,pods,jobs,configmaps,leases -n apps -o name | sort | sha256sum)"
"$WORK/bori-operator" --bori-root "$WORK/root" --bori-dir "$WORK/root/.bori" \
  --metrics-bind-address 0 --health-probe-bind-address 0 \
  --enable-operational-assessment --operational-requeue-interval 2s \
  --operational-provider-config "$WORK/providers4.yaml" \
  --enable-operational-interaction --operational-interaction-profile "$WORK/profile5.json" \
  --enable-operational-actions >"$WORK/operator-o5.log" 2>&1 &
OP_PID=$!

act() { kubectl get operationaltarget "$1" -n apps -o jsonpath="{.status.interaction.actions[0].$2}"; }
acodes() { kubectl get operationaltarget "$1" -n apps -o jsonpath='{.status.interaction.actions[0].reasons[*].code}'; }
wait_eq "web4 proposal shown and Blocked" Blocked 60 act web4 phase
codes="$(acodes web4)"
check "blocked: journal not durable ($codes)" grep -qw journal-not-durable <<<"$codes"
check "blocked: no provider registered ($codes)" grep -qw provider-unregistered <<<"$codes"
check "a person must decide: approval and risk declared ($codes)" bash -c "grep -qw approval-declared <<<'$codes' && grep -qw risk-declared <<<'$codes'"
check "proposal identity and digest exposed for an approver" eq "$(act web4 proposal | tr -d "\n" | wc -c)/$(act web4 digest | tr -d "\n" | wc -c)" "32/64"
check "no recovery claimed for a proposal ($(act web4 recovery))" eq "$(act web4 recovery)" ""
check "level stays DECISION_REQUIRED" eq "$(itr web4 level)" "DECISION_REQUIRED"
wait_eq "max-length proposal accepted by the API server" Blocked 60 act max4 phase
check "max-length proposal names the 253-character domain" eq "$(act max4 for.domain | tr -d "\n" | wc -c)" "253"
check "operator log states no handoff" grep -q "no handoff" "$WORK/operator-o5.log"
kubectl get operationaltargets -n apps -o json >"$WORK/targets5.json"
check "bori ops interaction renders the proposal" grep -q "actions:    restart@r1 for provide: Blocked" <("$WORK/bori" ops interaction -f "$WORK/targets5.json")

rv5="$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')"
sleep 12
check "steady blocked proposal writes nothing ($rv5)" eq "$(kubectl get operationaltarget web4 -n apps -o jsonpath='{.metadata.resourceVersion}')" "$rv5"

echo true >"$WORK/value4"
wait_eq "recovered on current evidence → NO_ACTION" NO_ACTION 60 itr web4 level
check "proposal withdrawn with the trigger, nothing executed or claimed" eq "$(itr web4 actions)" ""

check "no new object in the namespace" eq "$(kubectl get deployments,replicasets,pods,jobs,configmaps,leases -n apps -o name | sort | sha256sum)" "$objects_before"
managers="$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.managedFields[*].manager}')"
check "no bori field manager on the O5 workload ($managers)" eq "${managers//bori/}" "$managers"
check "O5 workload generation unchanged" eq "$(kubectl get deployment web4 -n apps -o jsonpath='{.metadata.generation}')" "$web4_gen"

if ((FAILURES > 0)); then
  echo "── operator log (tail)"; tail -50 "$WORK/operator.log"
  echo "── O3 operator log (tail)"; tail -50 "$WORK/operator-o3.log" 2>/dev/null || true
  echo "── O4 operator log (tail)"; tail -50 "$WORK/operator-o4.log" 2>/dev/null || true
  echo "── O5 operator log (tail)"; tail -50 "$WORK/operator-o5.log" 2>/dev/null || true
  echo "$FAILURES check(s) failed"; exit 1
fi
echo "all checks passed"
