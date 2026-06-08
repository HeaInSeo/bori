.PHONY: build build-bori clean test install-crds uninstall-crds install-rbac regression

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
