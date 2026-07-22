package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseDatadogRelativeTime(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		token string
		want  time.Time
		ok    bool
	}{
		{"now", "now", now, true},
		{"now-24h", "now-24h", now.Add(-24 * time.Hour), true},
		{"now-1h", "now-1h", now.Add(-time.Hour), true},
		{"now+15m", "now+15m", now.Add(15 * time.Minute), true},
		{"custom unit now-2d", "now-2d", now.Add(-48 * time.Hour), true},
		{"case insensitive NOW-1H", "NOW-1H", now.Add(-time.Hour), true},
		{"quoted RFC3339 absolute", `"2026-01-01T00:00:00Z"`, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"non-time facet value", "someuser", time.Time{}, false},
		{"bad duration", "now-xyz", time.Time{}, false},
		{"empty offset", "now-", time.Time{}, false},
		{"empty", "", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDatadogRelativeTime(tc.token, now)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.True(t, tc.want.Equal(got), "want %s, got %s", tc.want, got)
			}
		})
	}
}

func TestExtractDatadogFacetTime(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	t.Run("from+to investigation window stripped and hoisted", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("service:my-api status:error from:now-24h to:now", args, now)
		assert.Equal(t, "service:my-api status:error", got)
		assert.Equal(t, now.Add(-24*time.Hour).UnixMilli(), args["start_time"])
		assert.Equal(t, now.UnixMilli(), args["end_time"])
	})

	t.Run("tokens at start of query", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("from:now-1h to:now service:api", args, now)
		assert.Equal(t, "service:api", got)
		assert.Equal(t, now.Add(-time.Hour).UnixMilli(), args["start_time"])
		assert.Equal(t, now.UnixMilli(), args["end_time"])
	})

	t.Run("lone from defaults end to now", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("service:api from:now-6h", args, now)
		assert.Equal(t, "service:api", got)
		assert.Equal(t, now.Add(-6*time.Hour).UnixMilli(), args["start_time"])
		assert.Equal(t, now.UnixMilli(), args["end_time"])
	})

	t.Run("no time tokens left untouched", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("service:my-api status:error", args, now)
		assert.Equal(t, "service:my-api status:error", got)
		assert.NotContains(t, args, "start_time")
		assert.NotContains(t, args, "end_time")
	})

	t.Run("real to: facet with non-time value is preserved", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("service:api to:someuser", args, now)
		assert.Equal(t, "service:api to:someuser", got)
		assert.NotContains(t, args, "start_time")
	})

	t.Run("attribute facet @from is not matched", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime("@from:now-1h service:api", args, now)
		assert.Equal(t, "@from:now-1h service:api", got)
		assert.NotContains(t, args, "start_time")
	})

	t.Run("quoted phrase with spaces is preserved", func(t *testing.T) {
		args := map[string]any{}
		got := extractDatadogFacetTime(`message:"connection refused" from:now-1h to:now`, args, now)
		assert.Equal(t, `message:"connection refused"`, got)
		assert.Equal(t, now.Add(-time.Hour).UnixMilli(), args["start_time"])
		assert.Equal(t, now.UnixMilli(), args["end_time"])
	})

	t.Run("explicit args win over query tokens but tokens still stripped", func(t *testing.T) {
		args := map[string]any{"start_time": int64(123), "end_time": int64(456)}
		got := extractDatadogFacetTime("service:api from:now-24h to:now", args, now)
		assert.Equal(t, "service:api", got)
		assert.Equal(t, int64(123), args["start_time"])
		assert.Equal(t, int64(456), args["end_time"])
	})
}
