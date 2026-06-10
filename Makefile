.PHONY: build build-bori clean test generate generate-check install-crds uninstall-crds install-rbac regression kind-boot-smoke kind-func-smoke vm-integration

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
	GOPROXY=off go test ./...

# ── Clean ───────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/

# ── Phase 7: CRD / RBAC ─────────────────────────────────────────────────────

install-crds:
	kubectl apply -f config/crd/

uninstall-crds:
	kubectl delete -f config/crd/ --ignore-not-found

install-rbac:
	kubectl apply -f config/operator/namespace.yaml
	kubectl apply -f config/rbac/

uninstall-rbac:
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete -f config/operator/namespace.yaml --ignore-not-found

# ── Phase 8: Operator deploy ─────────────────────────────────────────────────

deploy: install-crds install-rbac
	kubectl apply -f config/operator/configmap.yaml
	kubectl apply -f config/operator/deployment.yaml
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

undeploy:
	kubectl delete -f config/operator/deployment.yaml --ignore-not-found
	kubectl delete -f config/operator/configmap.yaml --ignore-not-found
	$(MAKE) uninstall-rbac
	$(MAKE) uninstall-crds

deploy-dry-run:
	# Validates YAML structure against the API server's known schema.
	# NOTE: Does NOT compare Go types with CRD YAML — schema drift must be checked
	# manually. See docs/adr/ADR-002-controller-gen.md for the checklist.
	kubectl apply -f config/crd/           --dry-run=client
	kubectl apply -f config/operator/namespace.yaml --dry-run=client
	kubectl apply -f config/rbac/          --dry-run=client
	kubectl apply -f config/operator/configmap.yaml --dry-run=client
	kubectl apply -f config/operator/deployment.yaml --dry-run=client
