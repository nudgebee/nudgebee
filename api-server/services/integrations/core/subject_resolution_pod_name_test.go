package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLooksLikeGeneratedPodName pins the shape heuristic that decides which
// title words are worth an exact k8s_pods lookup.
func TestLooksLikeGeneratedPodName(t *testing.T) {
	accept := []string{
		"shipping-7665d7f977-7rsf8", // Deployment/ReplicaSet
		"otel-deployment-collector-collector-5f8b6c9d4-x2k9p",
		"monitoring-daemonset-9c8b7-abcde",
		"backup-1728312000-a1b2c", // CronJob
		"migrate-x7z2q",           // Job
		"shipping-7665d7f977",     // also Job-shaped
		"postgres-0",              // StatefulSet
		"worker-12",               // StatefulSet
	}
	for _, name := range accept {
		assert.True(t, looksLikeGeneratedPodName(name), "expected %q to look like a generated pod name", name)
	}

	reject := []string{
		"CPU-95-alert",              // not lowercase
		"KubePodCrashLooping",       // no hyphens
		"disk-usage-alert",          // hash segment has no digit
		"http-error-rate",           // last segment too short
		"a-b-c",                     // segments too short
		"",                          // empty
		"prod-us-east-1-cluster-01", // "01" too short / zero-padded
		"worker-007",                // zero-padded
	}
	for _, name := range reject {
		assert.False(t, looksLikeGeneratedPodName(name), "expected %q NOT to look like a generated pod name", name)
	}
}
