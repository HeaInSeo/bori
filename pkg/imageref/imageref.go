// Package imageref constructs container image references for deploy planning.
package imageref

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// DigestQualifiedRef constructs a digest-qualified image reference by extracting
// the repository from imageRef and appending imageDigest.
//
// imageRef must be a fully-qualified image reference with an explicit registry
// (e.g. "harbor.lab.local:5000/bori/sori:v0.3.1"). Bare names like "sori:latest"
// are rejected so that component.yaml always names the runtime registry explicitly.
//
// Any existing tag or digest in imageRef is replaced by imageDigest:
//
//	harbor.lab.local:5000/bori/sori:v0.3.1 + sha256:abc123...
//	→ harbor.lab.local:5000/bori/sori@sha256:abc123...
//
// When imageDigest is empty, imageRef is returned unchanged (tag-based fallback).
// When imageRef is empty and imageDigest is non-empty, an error is returned.
func DigestQualifiedRef(imageRef, imageDigest string) (string, error) {
	if imageDigest == "" {
		return imageRef, nil
	}
	if imageRef == "" {
		return "", fmt.Errorf("image.ref is required when imageDigest is set")
	}
	if !strings.HasPrefix(imageDigest, "sha256:") {
		return "", fmt.Errorf("imageDigest must start with \"sha256:\": %q", imageDigest)
	}

	ref, err := name.ParseReference(imageRef, name.StrictValidation)
	if err != nil {
		return "", fmt.Errorf("parse image.ref %q: %w", imageRef, err)
	}

	digestRef := ref.Context().Digest(imageDigest)
	return digestRef.Name(), nil
}
