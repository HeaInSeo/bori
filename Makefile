.PHONY: build build-bori clean test install-crds uninstall-crds install-rbac

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
	kubectl create namespace bori-system --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f config/rbac/

uninstall-rbac:
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete namespace bori-system --ignore-not-found
