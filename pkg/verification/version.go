package verification

import (
	"fmt"
	"os/exec"
	"strings"
)

// RequiredSlintGateVersion is the minimum slint-gate version bori requires.
// kube-slint v1.2.0 is the first release with K0-K5 all implemented:
//   - K0: schemaVersion strictness (v1.1.0)
//   - K1: SLIResult.Status propagation (v1.1.0)
//   - K2: OnCounterReset policy (v1.1.0)
//   - K3/K4: curlpod security + evidence redaction (v1.1.0)
//   - K5: K8sObjectFetcher for object churn (v1.2.0)
const RequiredSlintGateVersion = "1.2.0"

// CheckSlintGateVersion runs `slint-gate version` and returns the detected version.
// Returns an error if the binary is not found or doesn't support the version command.
//
// This check is non-fatal: callers should log the error and continue rather than
// aborting the verification run. The purpose is to surface version mismatches early
// so operators can install the correct binary.
func CheckSlintGateVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", fmt.Errorf("%s version: binary not found or does not support 'version' subcommand — install slint-gate v%s+: %w",
			bin, RequiredSlintGateVersion, err)
	}
	// Expected output: "slint-gate 1.2.0"
	line := strings.TrimSpace(string(out))
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "", fmt.Errorf("slint-gate version: empty output")
	}
	detected := parts[len(parts)-1]
	if !versionSufficient(detected, RequiredSlintGateVersion) {
		return detected, fmt.Errorf("slint-gate %s detected, require v%s+ — K0-K5 features unavailable",
			detected, RequiredSlintGateVersion)
	}
	return detected, nil
}

// versionSufficient returns true when detected >= required using simple semver
// comparison (major.minor.patch). Returns true on any parse error to be non-blocking.
func versionSufficient(detected, required string) bool {
	d := parseSemver(detected)
	r := parseSemver(required)
	if d[0] != r[0] {
		return d[0] > r[0]
	}
	if d[1] != r[1] {
		return d[1] > r[1]
	}
	return d[2] >= r[2]
}

// parseSemver splits "1.2.3" into [1, 2, 3]. Returns [0,0,0] on any error.
func parseSemver(v string) [3]int {
	var major, minor, patch int
	_, _ = fmt.Sscanf(strings.TrimPrefix(v, "v"), "%d.%d.%d", &major, &minor, &patch)
	return [3]int{major, minor, patch}
}
