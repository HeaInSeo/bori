package imageref

import (
	"strings"
	"testing"
)

// validDigest is a syntactically valid sha256 digest used across tests.
const validDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const validDigest2 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestDigestQualifiedRef(t *testing.T) {
	tests := []struct {
		name        string
		imageRef    string
		imageDigest string
		want        string
		wantErr     string
	}{
		{
			name:        "no digest — passthrough",
			imageRef:    "harbor.lab.local/bori/sori:v0.3.1",
			imageDigest: "",
			want:        "harbor.lab.local/bori/sori:v0.3.1",
		},
		{
			name:        "tag replaced by digest",
			imageRef:    "harbor.lab.local/bori/sori:v0.3.1",
			imageDigest: validDigest,
			want:        "harbor.lab.local/bori/sori@" + validDigest,
		},
		{
			name:        "port in registry — tag replaced by digest",
			imageRef:    "harbor.lab.local:5000/bori/sori:v0.3.1",
			imageDigest: validDigest,
			want:        "harbor.lab.local:5000/bori/sori@" + validDigest,
		},
		{
			name:        "existing digest in imageRef replaced by new digest",
			imageRef:    "harbor.lab.local/bori/sori@" + validDigest2,
			imageDigest: validDigest,
			want:        "harbor.lab.local/bori/sori@" + validDigest,
		},
		{
			// StrictValidation requires an explicit tag or digest — bare refs are rejected.
			// component.yaml image.ref must always specify a tag (e.g. :latest, :v0.3.1).
			name:        "no tag in imageRef rejected by StrictValidation",
			imageRef:    "harbor.lab.local/bori/sori",
			imageDigest: validDigest,
			wantErr:     "parse image.ref",
		},
		{
			name:        "empty imageRef with digest — error",
			imageRef:    "",
			imageDigest: validDigest,
			wantErr:     "image.ref is required",
		},
		{
			name:        "digest without sha256 prefix — error",
			imageRef:    "harbor.lab.local/bori/sori:latest",
			imageDigest: "abc123",
			wantErr:     `imageDigest must start with "sha256:"`,
		},
		{
			name:        "bare name rejected by StrictValidation",
			imageRef:    "sori:latest",
			imageDigest: validDigest,
			wantErr:     "parse image.ref",
		},
		{
			name:        "partial path without explicit registry rejected",
			imageRef:    "bori/sori:latest",
			imageDigest: validDigest,
			wantErr:     "parse image.ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DigestQualifiedRef(tt.imageRef, tt.imageDigest)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}
