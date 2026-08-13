package scan_orchestrator

import (
	"errors"
	"fmt"
	"testing"
)

// isImagePullFailure classifies a RunScan error as an image-pull failure so the
// scan is recorded unscannable (long back-off) instead of failed (daily retry).
// The marker string is a cross-repo contract with the agent's failure_reason.
func TestIsImagePullFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{
			"agent image-pull reason",
			// Mirrors the wrapped shape from service.go RunScan:
			// "job <name> ended in Failed: <failure_reason>".
			fmt.Errorf("scan_orchestrator: job trivy-image-scan-abcd1234 ended in Failed: %s",
				`image pull failed for container "scanner": Back-off pulling image "registry.dev.example.com/app:tag"`),
			true,
		},
		{
			"casing variation still classified",
			errors.New(`scan_orchestrator: job x ended in Failed: IMAGE PULL FAILED for container "scanner"`),
			true,
		},
		{"generic scan failure", errors.New("scan_orchestrator: job x ended in Failed: container exited 1"), false},
		{"deadline (older agent)", errors.New("wait: scan_orchestrator: deadline 15m0s exceeded for job x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isImagePullFailure(tc.err); got != tc.want {
				t.Errorf("isImagePullFailure(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
