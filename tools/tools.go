//go:build tools

// Package tools pins code-generation binaries so they appear in go.mod/go.sum
// and can be invoked via `go run`. To regenerate CRDs and DeepCopy methods:
//
//	make generate
package tools

import (
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
