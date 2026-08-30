package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventAnalysisResponseGeneratedAt pins the wire contract for the
// `generated_at` field the investigate UI reads to label an RCA report with
// when it was produced (nudgebee-enterprise#35283, AC-2).
//
// The field is a *time.Time rather than a time.Time on purpose: `omitempty`
// does not omit a zero struct, so a bare time.Time on a response with no
// stored analysis row would serialize as "0001-01-01T00:00:00Z" and the UI
// would render a year-0001 timestamp instead of rendering nothing. These
// tests fail if someone "simplifies" the pointer away.
func TestEventAnalysisResponseGeneratedAt(t *testing.T) {
	t.Run("omits generated_at entirely when unset", func(t *testing.T) {
		raw, err := json.Marshal(EventAnalysisResponse{Status: "IN_PROGRESS"})
		require.NoError(t, err)

		assert.NotContains(t, string(raw), "generated_at",
			"a response with no stored analysis row must not claim a generation time")

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		_, present := decoded["generated_at"]
		assert.False(t, present)
	})

	t.Run("round-trips the stored row timestamp", func(t *testing.T) {
		generatedAt := time.Date(2026, 8, 16, 9, 30, 0, 0, time.UTC)
		raw, err := json.Marshal(EventAnalysisResponse{
			Status:      "COMPLETED",
			Analysis:    "## Root cause\nDisk pressure on node-3.",
			GeneratedAt: &generatedAt,
		})
		require.NoError(t, err)

		var decoded EventAnalysisResponse
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.NotNil(t, decoded.GeneratedAt)
		assert.True(t, generatedAt.Equal(*decoded.GeneratedAt))
	})

	t.Run("carries the failure reason alongside a retained analysis", func(t *testing.T) {
		// A failed run keeps the report written by the last successful run --
		// UpdateEventAnalysisStatus only rewrites status/status_reason. The UI
		// needs both on the same payload to show the reason and the old report.
		failedAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
		raw, err := json.Marshal(EventAnalysisResponse{
			Status:       "FAILED",
			StatusReason: "unable to get rca analysis - context deadline exceeded",
			Analysis:     "## Root cause\nDisk pressure on node-3.",
			GeneratedAt:  &failedAt,
		})
		require.NoError(t, err)

		var decoded EventAnalysisResponse
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, "unable to get rca analysis - context deadline exceeded", decoded.StatusReason)
		assert.NotEmpty(t, decoded.Analysis)
		require.NotNil(t, decoded.GeneratedAt)
	})
}
