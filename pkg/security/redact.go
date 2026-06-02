// Package security provides utilities for redacting secrets from bori run archives.
// Every run artifact that leaves bori must pass through redaction before being written.
package security

import (
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

// sensitiveKeyPattern matches environment variable names and map keys that
// likely contain secret values.
var sensitiveKeyPattern = regexp.MustCompile(
	`(?i)(token|password|passwd|secret|credential|auth|key|apikey|api_key|private)`,
)

// authHeaderPattern matches Authorization header values.
var authHeaderPattern = regexp.MustCompile(`(?i)(bearer|basic|token)\s+\S+`)

// RedactString replaces known secret patterns inside a string.
// Use for log lines, error messages, or free-form text that may contain credentials.
func RedactString(s string) string {
	return authHeaderPattern.ReplaceAllString(s, "$1 "+redacted)
}

// RedactMap returns a copy of m where values for sensitive keys are replaced
// with [REDACTED]. Keys are matched case-insensitively against common secret
// patterns (token, password, secret, credential, key, etc.).
func RedactMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if sensitiveKeyPattern.MatchString(k) {
			out[k] = redacted
		} else {
			out[k] = v
		}
	}
	return out
}

// RedactEnv redacts values from a slice of "KEY=VALUE" environment variable strings.
func RedactEnv(env []string) []string {
	out := make([]string, len(env))
	for i, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 && sensitiveKeyPattern.MatchString(parts[0]) {
			out[i] = parts[0] + "=" + redacted
		} else {
			out[i] = e
		}
	}
	return out
}

// IsSensitiveKey reports whether a key name looks like it holds secret data.
func IsSensitiveKey(key string) bool {
	return sensitiveKeyPattern.MatchString(key)
}
