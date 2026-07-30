package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildLargeMetricsMessage asserts the metrics wrapper's truncation notice tells
// the calling orchestrator to grep the saved file instead of re-querying with a
// narrower time range (NB-27743), and applies uniformly regardless of provider — the
// saved file holds the sub-agent's own answer text either way (PostProcessResponse's
// raw-JSON override never fires through this nested ExecuteAgentToolCall path), so
// the guidance is plain-text search, not a provider-specific data shape.
func TestBuildLargeMetricsMessage(t *testing.T) {
	msg := buildLargeMetricsMessage(12345, "metrics_123.txt", "some preview text")

	assert.Contains(t, msg, "Output large (12345 bytes)")
	assert.Contains(t, msg, "metrics_123.txt")
	assert.Contains(t, msg, "shell_execute")
	assert.Contains(t, msg, "instead of re-querying with a narrower time range")
	assert.Contains(t, msg, "some preview text")

	// Must not assume a specific data shape (JSON/jq) — the saved content is the
	// sub-agent's own prose answer, not raw per-datapoint data.
	assert.NotContains(t, msg, "jq")
	assert.NotContains(t, msg, "map_values(fromjson)")
}
