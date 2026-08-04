.PHONY: build build-bori clean test generate generate-check install-crds uninstall-crds install-rbac regression kind-boot-smoke kind-func-smoke vm-integration require-host-repo-path

# ── Code generation ─────────────────────────────────────────────────────────

# Generate CRD YAML and root-type DeepCopy from Go types.
# Run after any change to apis/bori/v1alpha1/*.go.
# All DeepCopy methods (root + sub-types) are generated.
# Run after any change to apis/bori/v1alpha1/*.go.
generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen \
		crd:maxDescLen=0 \
		paths="./apis/..." \
		output:crd:dir=config/crd
	go run sigs.k8s.io/controller-tools/cmd/controller-gen \
		object:headerFile="hack/boilerplate.go.txt" \
		paths="./apis/..."

# CI check: verify generated files are up-to-date.
# Fails if make generate produces any diff (means types changed without regenerating).
generate-check:
	$(MAKE) generate
	git diff --exit-code config/crd/ apis/bori/v1alpha1/zz_generated.deepcopy.go

# ── Build ───────────────────────────────────────────────────────────────────

build: build-bori build-operator

build-bori:
	go build -o bin/bori ./cmd/bori

build-operator:
	go build -o bin/bori-operator ./cmd/bori-operator

build-devspace-adapter:
	go build -o bin/bori-devspace ./cmd/bori-devspace

# ── Test ────────────────────────────────────────────────────────────────────

test:
	GOPROXY=off go test -race -shuffle=on -count=1 ./...

# ── Clean ───────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/

# ── Phase 7: CRD / RBAC ─────────────────────────────────────────────────────

# require-host-repo-path is an explicit prerequisite (not only of deploy) so that
# under `make -j` the guard is ordered before any kubectl call. Without it, a
# parallel `make deploy` could run these kubectl targets concurrently with the
# guard and hit the cluster before a missing BORI_HOST_REPO_PATH fails loudly.
install-crds: require-host-repo-path
	kubectl apply -f config/crd/

uninstall-crds:
	kubectl delete -f config/crd/ --ignore-not-found

install-rbac: require-host-repo-path
	kubectl apply -f config/operator/namespace.yaml
	kubectl apply -f config/rbac/

uninstall-rbac:
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete -f config/operator/namespace.yaml --ignore-not-found

# ── Phase 8: Operator deploy ─────────────────────────────────────────────────

# Guard: the operator manifest's bori-repo hostPath is rendered from
# BORI_HOST_REPO_PATH via envsubst (config/operator/deployment.yaml). This target
# is a prerequisite of every target that renders/applies that manifest, so a
# missing value fails loudly BEFORE any kubectl call instead of applying an
# empty/wrong host path.
require-host-repo-path:
	@: "$${BORI_HOST_REPO_PATH:?set BORI_HOST_REPO_PATH to the host bori checkout path}"

deploy: require-host-repo-path install-crds install-rbac
	kubectl apply -f config/operator/configmap.yaml
	envsubst '$$BORI_HOST_REPO_PATH' < config/operator/deployment.yaml | kubectl apply -f -
	$(MAKE) regression

regression:
	./scripts/regression-check.sh

# ── Test: Layer 2 (kind smoke) ───────────────────────────────────────────────

# K0 boot smoke: operator 기동 + /metrics 확인 (Layer 2).
# 전제: kind, docker, kubectl, go
# 클러스터 유지: make kind-boot-smoke ARGS=--keep
# K1 functional smoke (BoriRevision 생성): 다음 PR
kind-boot-smoke:
	./hack/test-kind-boot-smoke.sh $(ARGS)

# K1 functional smoke: ConfigMap bori-root + shell adapter → BoriRevision 생성 확인 (Layer 2).
# 전제: kind, docker, kubectl, go
# 클러스터 유지: make kind-func-smoke ARGS=--keep
kind-func-smoke:
	./hack/test-kind-functional-smoke.sh $(ARGS)

# ── Test: Layer 3 (VM integration) ──────────────────────────────────────────

# VM integration test (Layer 3).
# 전제: BORI_VM_REMOTE 환경변수로 SSH target 지정 (예: user@your-vm-ip)
# baseline 갱신: make vm-integration ARGS=--update-baseline
vm-integration:
	./hack/test-vm-integration.sh $(ARGS)

# undeploy tears down by resource identity, so it needs no hostPath value: it does
# not depend on require-host-repo-path and does not render the manifest via
# envsubst. Deleting the Deployment by name/namespace avoids requiring
# BORI_HOST_REPO_PATH just to remove an object whose spec is irrelevant to delete.
undeploy:
	kubectl delete deployment bori-operator -n bori-system --ignore-not-found
	kubectl delete -f config/operator/configmap.yaml --ignore-not-found
	$(MAKE) uninstall-rbac
	$(MAKE) uninstall-crds

deploy-dry-run: require-host-repo-path
	# Validates YAML structure against the API server's known schema.
	# NOTE: Does NOT compare Go types with CRD YAML — schema drift must be checked
	# manually. See docs/adr/ADR-002-controller-gen.md for the checklist.
	kubectl apply -f config/crd/           --dry-run=client
	kubectl apply -f config/rbac/          --dry-run=client
	kubectl apply -f config/operator/configmap.yaml --dry-run=client
	# The namespace manifest is validated on its own with --dry-run=server. Note a
	# server dry-run does NOT create the namespace, so this target does not pretend
	# to support a fresh cluster: it validates against one where the target
	# namespace already exists.
	kubectl apply -f config/operator/namespace.yaml --dry-run=server
	# The operator Deployment lives in bori-system; its --dry-run=server below runs
	# against the live API and needs that namespace to already exist. Fail with a
	# clear precondition error if it is absent rather than emitting a confusing
	# server error. (For the fresh-cluster case, use a disposable-cluster
	# integration test that actually provisions the namespace — out of scope here.)
	@kubectl get namespace bori-system >/dev/null 2>&1 || { \
		echo "deploy-dry-run: target namespace 'bori-system' does not exist; deploy-dry-run validates against an existing namespace and does not provision one (run 'make install-rbac' first, or use a disposable-cluster integration test for a fresh cluster)." >&2; \
		exit 1; \
	}
	# The operator manifest is rendered from BORI_HOST_REPO_PATH via envsubst, so a
	# literal placeholder can no longer slip through. --dry-run=server (not client)
	# is required so the API server actually validates the rendered hostPath.
	envsubst '$$BORI_HOST_REPO_PATH' < config/operator/deployment.yaml | kubectl apply -f - --dry-run=server
