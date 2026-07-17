package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeLogGroupsByPattern(t *testing.T) {
	t.Run("Rows sharing a pattern hash in the same place merge", func(t *testing.T) {
		// What Elasticsearch/Dynatrace/Pinot/New Relic produce: they group by the
		// raw message, so one pattern arrives as a row per message variant, each
		// carrying the same pattern_hash.
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Sample: "timeout after 30.1ms", Count: 3, Timestamps: []int64{100}, Values: []float64{3}},
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Sample: "timeout after 29.7ms", Count: 2, Timestamps: []int64{100}, Values: []float64{2}},
		}})

		require.Len(t, out.Groups, 1)
		assert.Equal(t, int64(5), out.Groups[0].Count)
		assert.Equal(t, "timeout after 30.1ms", out.Groups[0].Sample, "first sample is kept")
		assert.Equal(t, []float64{5}, out.Groups[0].Values)
	})

	t.Run("A hash identifies exactly one row", func(t *testing.T) {
		// pattern_hash is the ticket reference id and the UI resolves it with a
		// findIndex, so a repeated hash silently binds a ticket to the wrong row.
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
			{PatternHash: "h2", Namespace: "prod", Workload: "api", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
		}})

		seen := map[string]int{}
		for _, g := range out.Groups {
			seen[g.PatternHash]++
		}
		for hash, n := range seen {
			assert.Equal(t, 1, n, "hash %s appears on %d rows", hash, n)
		}
	})

	t.Run("Same pattern in a different place stays separate", func(t *testing.T) {
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
			{PatternHash: "h1", Namespace: "staging", Workload: "api", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
			{PatternHash: "h1", Namespace: "prod", Workload: "worker", Level: "error", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "critical", Count: 1, Timestamps: []int64{100}, Values: []float64{1}},
		}})

		assert.Len(t, out.Groups, 4)
	})

	t.Run("Multi-point series sum per timestamp and stay chronological", func(t *testing.T) {
		// The Prometheus source returns a real series rather than a single point.
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 3, Timestamps: []int64{300, 100}, Values: []float64{1, 2}},
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 9, Timestamps: []int64{200, 100}, Values: []float64{4, 5}},
		}})

		require.Len(t, out.Groups, 1)
		assert.Equal(t, int64(12), out.Groups[0].Count)
		assert.Equal(t, []int64{100, 200, 300}, out.Groups[0].Timestamps)
		assert.Equal(t, []float64{7, 4, 1}, out.Groups[0].Values, "values stay aligned with their timestamps")
	})

	t.Run("Already-unique groups are passed through", func(t *testing.T) {
		// Loki/SolarWinds/Loggly group in-process and arrive merged.
		in := []LogGroup{
			{PatternHash: "h1", Namespace: "prod", Workload: "api", Level: "error", Count: 5, Timestamps: []int64{100}, Values: []float64{5}},
			{PatternHash: "h2", Namespace: "prod", Workload: "api", Level: "error", Count: 3, Timestamps: []int64{100}, Values: []float64{3}},
		}
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: in})

		assert.Len(t, out.Groups, 2)
		assert.Equal(t, int64(5), out.Groups[0].Count)
		assert.Equal(t, int64(3), out.Groups[1].Count)
	})

	t.Run("Merged rows are re-sorted by count", func(t *testing.T) {
		// Sources sort before returning, but merging changes the counts.
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "big", Namespace: "prod", Workload: "api", Level: "error", Count: 6, Timestamps: []int64{100}, Values: []float64{6}},
			{PatternHash: "split", Namespace: "prod", Workload: "api", Level: "error", Count: 5, Timestamps: []int64{100}, Values: []float64{5}},
			{PatternHash: "split", Namespace: "prod", Workload: "api", Level: "error", Count: 5, Timestamps: []int64{100}, Values: []float64{5}},
		}})

		require.Len(t, out.Groups, 2)
		assert.Equal(t, "split", out.Groups[0].PatternHash)
		assert.Equal(t, int64(10), out.Groups[0].Count)
	})

	t.Run("Empty output is handled", func(t *testing.T) {
		assert.Empty(t, mergeLogGroupsByPattern(LogGroupOutput{}).Groups)
	})

	t.Run("SigNoz pod-level rows collapse to one row per workload", func(t *testing.T) {
		// SigNoz groups by pod and derives pattern_hash from the workload, so every
		// pod of a workload already shares a hash. Those rows were never separately
		// addressable by ticket; merging them makes the count workload-wide.
		out := mergeLogGroupsByPattern(LogGroupOutput{Groups: []LogGroup{
			{PatternHash: "hash-of-api", Namespace: "prod", Workload: "api", Level: "error", Sample: "api-abc12", Count: 4, Timestamps: []int64{100}, Values: []float64{4}},
			{PatternHash: "hash-of-api", Namespace: "prod", Workload: "api", Level: "error", Sample: "api-xyz89", Count: 6, Timestamps: []int64{100}, Values: []float64{6}},
		}})

		require.Len(t, out.Groups, 1)
		assert.Equal(t, int64(10), out.Groups[0].Count)
	})
}

func TestESBucketLastSeenUnix(t *testing.T) {
	// req.EndTime is epoch millis (the range filter declares epoch_millis), while a
	// group's Timestamps are seconds.
	const windowEndMs = int64(1784210110000)
	const windowEndSec = windowEndMs / 1000

	// 1784200000000ms == 2026-07-16T11:06:40Z, inside the window ending 13:55:10Z.
	const lastSeenMs = float64(1784200000000)
	const lastSeenSec = int64(1784200000)

	t.Run("Reads the max aggregation as seconds", func(t *testing.T) {
		got := esBucketLastSeenUnix(map[string]any{
			"last_seen": map[string]any{"value": lastSeenMs, "value_as_string": "2026-07-16T11:06:40.000Z"},
		}, windowEndMs)

		assert.Equal(t, lastSeenSec, got)
	})

	t.Run("Falls back to value_as_string when value is null", func(t *testing.T) {
		got := esBucketLastSeenUnix(map[string]any{
			"last_seen": map[string]any{"value": nil, "value_as_string": "2026-07-16T11:06:40Z"},
		}, windowEndMs)

		assert.Equal(t, lastSeenSec, got)
	})

	t.Run("Falls back to the window end in seconds, not millis", func(t *testing.T) {
		// The old code emitted req.EndTime verbatim, so the UI (which multiplies by
		// 1000) rendered a date tens of thousands of years out.
		assert.Equal(t, windowEndSec, esBucketLastSeenUnix(map[string]any{}, windowEndMs))
		assert.Equal(t, windowEndSec, esBucketLastSeenUnix(map[string]any{
			"last_seen": map[string]any{"value": nil},
		}, windowEndMs))
	})
}
