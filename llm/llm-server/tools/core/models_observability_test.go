package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewObservabilityLogCoverage(t *testing.T) {
	start := time.Date(2026, 1, 15, 5, 19, 26, 0, time.UTC)
	end := time.Date(2026, 1, 15, 5, 36, 0, 0, time.UTC)
	// Grouped by stream, as backends return them: not in time order.
	logs := []ObservabilityLog{
		{Timestamp: "2026-01-15T05:34:42.5Z", Message: "c"},
		{Timestamp: "2026-01-15T05:30:48.123456789Z", Message: "a"},
		{Timestamp: "2026-01-15T05:33:10Z", Message: "b"},
	}

	t.Run("a read that reached its limit says what it covers", func(t *testing.T) {
		coverage := NewObservabilityLogCoverage(logs, 3, start.UnixMilli(), end.UnixMilli())
		require.NotNil(t, coverage)
		assert.Equal(t, ObservabilityLogCoverage{
			Limit:          3,
			Returned:       3,
			Oldest:         "2026-01-15T05:30:48Z",
			Newest:         "2026-01-15T05:34:42Z",
			RequestedStart: "2026-01-15T05:19:26Z",
			RequestedEnd:   "2026-01-15T05:36:00Z",
		}, *coverage)
	})

	t.Run("a read under its limit has none", func(t *testing.T) {
		assert.Nil(t, NewObservabilityLogCoverage(logs, 4, start.UnixMilli(), end.UnixMilli()))
		assert.Nil(t, NewObservabilityLogCoverage(nil, 1000, start.UnixMilli(), end.UnixMilli()))
	})

	t.Run("no limit, no claim", func(t *testing.T) {
		assert.Nil(t, NewObservabilityLogCoverage(logs, 0, start.UnixMilli(), end.UnixMilli()))
	})

	t.Run("timestamps that cannot be read leave the span out", func(t *testing.T) {
		unreadable := []ObservabilityLog{{Timestamp: "yesterday"}, {Timestamp: ""}}
		coverage := NewObservabilityLogCoverage(unreadable, 2, 0, 0)
		require.NotNil(t, coverage)
		assert.Equal(t, ObservabilityLogCoverage{Limit: 2, Returned: 2}, *coverage)
	})

	t.Run("a span drawn from only some of the lines would understate it, so it is left out", func(t *testing.T) {
		mixed := []ObservabilityLog{{Timestamp: "2026-01-15T05:34:42Z"}, {Timestamp: "1768455288"}, {Timestamp: "2026-01-15T05:30:48Z"}}
		coverage := NewObservabilityLogCoverage(mixed, 3, 0, 0)
		require.NotNil(t, coverage)
		assert.Equal(t, ObservabilityLogCoverage{Limit: 3, Returned: 3}, *coverage)
	})

	t.Run("other timestamp forms backends return", func(t *testing.T) {
		mixed := []ObservabilityLog{
			{Timestamp: "2026-01-15 05:31:00.918350278"},
			{Timestamp: "2026-01-15T05:32:00.5"},
			{Timestamp: "2026-01-15T11:03:00+05:30"},
		}
		coverage := NewObservabilityLogCoverage(mixed, 3, 0, 0)
		require.NotNil(t, coverage)
		assert.Equal(t, "2026-01-15T05:31:00Z", coverage.Oldest)
		assert.Equal(t, "2026-01-15T05:33:00Z", coverage.Newest)
	})
}
