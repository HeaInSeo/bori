.PHONY: build build-bori clean test generate generate-check install-crds uninstall-crds install-rbac regression

# ── Code generation ─────────────────────────────────────────────────────────

# Generate CRD YAML and root-type DeepCopy from Go types.
# Run after any change to apis/bori/v1alpha1/*.go.
# Note: controller-gen's object generator produces DeepCopyInto only for root
# types; sub-type methods live in apis/bori/v1alpha1/deepcopy_subtypes.go and
# must be updated manually when sub-type fields change.
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
