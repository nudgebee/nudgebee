package observability

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var logSpikeTestStart = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func logSpikeTestSeries(metric map[string]string, startOffsetBuckets int, values ...float64) Result {
	timestamps := make([]int64, len(values))
	for i := range values {
		timestamps[i] = logSpikeTestStart.Unix() + int64(startOffsetBuckets+i)*300
	}
	return Result{Metric: metric, Timestamps: timestamps, Values: values}
}

// logSpikeTestMeasured is the workload's count of lines of every level over
// the baseline hours, read at the window start: it was being counted.
var logSpikeTestMeasured = []Result{{Metric: map[string]string{}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{5400}}}

func logSpikeTestInput() ([]Result, []Result) {
	relay := "ERROR relay query failed"
	current := []Result{
		// One pattern across two containers, plus a replica that recorded a
		// different sample line for the same pattern.
		logSpikeTestSeries(map[string]string{"pattern_hash": "aaa", "level": "error", "sample": relay, "container_id": "/k8s/shop/api-1/api"}, 0, 2, 300, 400, 10),
		logSpikeTestSeries(map[string]string{"pattern_hash": "aaa", "level": "error", "sample": relay, "container_id": "/k8s/shop/api-2/api"}, 0, 1, 100, 283, 5),
		logSpikeTestSeries(map[string]string{"pattern_hash": "aaa", "level": "error", "sample": relay + " (variant)", "container_id": "/k8s/shop/api-3/api"}, 2, 17),
		// First appears in the last bucket.
		logSpikeTestSeries(map[string]string{"pattern_hash": "bbb", "level": "error", "sample": "ERROR discovery failed", "container_id": "/k8s/shop/api-1/api"}, 3, 264),
		// Steady: logging exactly its normal.
		logSpikeTestSeries(map[string]string{"pattern_hash": "ccc", "level": "critical", "sample": "CRITICAL heartbeat late", "container_id": "/k8s/shop/api-2/api"}, 0, 5, 5, 5, 5),
	}
	normal := []Result{
		// Aligned a minute before the window start; the later point already
		// holds part of the spike and must not be read.
		{Metric: map[string]string{"pattern_hash": "aaa", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix() - 60, logSpikeTestStart.Unix() + 240}, Values: []float64{2.8, 9}},
		// Only present once the pattern has appeared: outside the tolerance.
		{Metric: map[string]string{"pattern_hash": "bbb", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix() + 900}, Values: []float64{44}},
		{Metric: map[string]string{"pattern_hash": "ccc", "level": "critical"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{5}},
		// Quiet in the window, but part of what is normal for the workload.
		{Metric: map[string]string{"pattern_hash": "ddd", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{1.2}},
	}
	return current, normal
}

// logPatternBreakdownCardJSON is the card llm-server decodes. The same literal
// is in llm/llm-server/api/event_analyzer_log_patterns_test.go; keep the two
// identical so a change to the shape fails on both sides.
const logPatternBreakdownCardJSON = `{
  "window_start": "2026-01-15T10:00:00Z",
  "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5,
  "baseline_hours": 6,
  "baseline_evaluated_at": "2026-01-15T09:59:00Z",
  "baseline_known": true,
  "counted_levels": ["error", "critical"],
  "counting_note": "Counts only log lines the log agent tagged error or critical. The tag comes from the line's own level field, or from a level word at the start of the line. A warning, or a line that only mentions \"error\" further in, is not counted.",
  "total": {"peak": 705, "peak_at": "2026-01-15T10:10:00Z", "normal": 9},
  "patterns_seen": 3,
  "patterns": [
    {"pattern_hash": "aaa", "level": "error", "peak": 700, "peak_at": "2026-01-15T10:10:00Z", "normal": 2.8, "example": "ERROR relay query failed", "container_id": "/k8s/shop/api-1/api"},
    {"pattern_hash": "bbb", "level": "error", "peak": 264, "peak_at": "2026-01-15T10:15:00Z", "normal": 0, "example": "ERROR discovery failed", "container_id": "/k8s/shop/api-1/api"},
    {"pattern_hash": "ccc", "level": "critical", "peak": 5, "peak_at": "2026-01-15T10:00:00Z", "normal": 5, "example": "CRITICAL heartbeat late", "container_id": "/k8s/shop/api-2/api"}
  ]
}`

func TestBuildLogPatternBreakdown(t *testing.T) {
	current, normal := logSpikeTestInput()
	breakdown := buildLogPatternBreakdown(current, normal, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute))
	require.NotNil(t, breakdown)

	got, err := json.Marshal(breakdown)
	require.NoError(t, err)
	assert.JSONEq(t, logPatternBreakdownCardJSON, string(got))

	assert.Equal(t, map[string]any{
		"container_id": "/k8s/shop/api-1/api",
		"sample":       "ERROR relay query failed",
	}, breakdown.topLabels(), "labels come from the pattern that rose most")
	assert.Equal(t,
		"Error and critical log lines peaked at 705 per 5 min against 9 normal. The pattern that rose most is error-level: 700 per 5 min against 2.8 normal.",
		breakdown.summary())
}

func TestBuildLogPatternBreakdown_NothingLoggedInTheWindow(t *testing.T) {
	_, normal := logSpikeTestInput()
	assert.Nil(t, buildLogPatternBreakdown(nil, normal, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute)))
}

// A workload with no error or critical line in the baseline hours has no normal
// series at all. That is "everything here is new", not "nothing to compare".
func TestBuildLogPatternBreakdown_NoErrorsBeforeTheWindow(t *testing.T) {
	current, _ := logSpikeTestInput()

	breakdown := buildLogPatternBreakdown(current, nil, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute))
	require.NotNil(t, breakdown)
	assert.True(t, breakdown.BaselineKnown, "the workload was being counted; it logged no error")
	assert.Equal(t, 0.0, breakdown.Total.Normal)
	assert.Equal(t, "2026-01-15T10:00:00Z", breakdown.BaselineEvaluatedAt)
	for _, p := range breakdown.Patterns {
		assert.Equal(t, 0.0, p.Normal)
	}
	assert.Equal(t,
		"Error and critical log lines peaked at 705 per 5 min; none were logged in the 6 h before. The busiest pattern is error-level: 700 per 5 min.",
		breakdown.summary())
}

// No normal series and no log counts of any level in the baseline hours: the
// workload was not being counted (it is new, or the metric has a gap). Saying
// "none were logged before" would be a claim the data cannot support.
func TestBuildLogPatternBreakdown_WorkloadNotCountedBeforeTheWindow(t *testing.T) {
	current, _ := logSpikeTestInput()
	end := logSpikeTestStart.Add(20 * time.Minute)
	measuredLater := []Result{{Metric: map[string]string{}, Timestamps: []int64{logSpikeTestStart.Unix() + 900}, Values: []float64{5400}}}
	measuredZero := []Result{{Metric: map[string]string{}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{0}}}

	for name, measured := range map[string][]Result{"no series": nil, "read at another time": measuredLater, "zero lines": measuredZero} {
		t.Run(name, func(t *testing.T) {
			breakdown := buildLogPatternBreakdown(current, nil, measured, logSpikeTestStart, end)
			require.NotNil(t, breakdown)
			assert.False(t, breakdown.BaselineKnown)
			assert.Equal(t,
				"Error and critical log lines peaked at 705 per 5 min. The workload has no log counts in the 6 h before, so what is normal for it is unknown. The busiest pattern is error-level: 700 per 5 min.",
				breakdown.summary())
		})
	}
}

// A pattern that logged once in the baseline hours has a normal of 1/72 per
// bucket. It was seen before, and the card must not round that away.
func TestBuildLogPatternBreakdown_SmallNormalIsNotZero(t *testing.T) {
	current := []Result{logSpikeTestSeries(map[string]string{"pattern_hash": "aaa", "level": "error", "sample": "ERROR relay query failed", "container_id": "/k8s/shop/api-1/api"}, 1, 40)}
	normal := []Result{{Metric: map[string]string{"pattern_hash": "aaa", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{1.0 / 72}}}

	breakdown := buildLogPatternBreakdown(current, normal, nil, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute))
	require.NotNil(t, breakdown)
	assert.True(t, breakdown.BaselineKnown, "normal counts were read, so the baseline is known without the all-levels count")
	assert.Equal(t, 0.014, breakdown.Patterns[0].Normal)
	assert.Equal(t, 0.014, breakdown.Total.Normal)
	assert.Equal(t,
		"Error and critical log lines peaked at 40 per 5 min against 0.014 normal. The pattern that rose most is error-level: 40 per 5 min against 0.014 normal.",
		breakdown.summary())
}

func TestRoundCount(t *testing.T) {
	for value, want := range map[float64]float64{0: 0, -3: 0, 0.0001: 0.001, 1.0 / 72: 0.014, 2.84: 2.84, 8.8889: 8.889, 36.66: 36.7, 705: 705} {
		assert.Equal(t, want, roundCount(value), "roundCount(%v)", value)
	}
}

// Normal counts that came back but were read somewhere other than the window
// start are not a normal: read mid-spike they hold the spike, and treating
// them as absent would call every pattern new.
func TestBuildLogPatternBreakdown_NormalReadAtTheWrongTime(t *testing.T) {
	current, _ := logSpikeTestInput()
	wrongTime := []Result{{Metric: map[string]string{"pattern_hash": "aaa", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix() + 900}, Values: []float64{44}}}

	assert.Nil(t, buildLogPatternBreakdown(current, wrongTime, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute)))
}

// The counts may arrive on a grid finer than the 5-minute bucket; each point is
// then the count over the 5 minutes ending there, and the peak is the largest.
func TestBuildLogPatternBreakdown_FineGrid(t *testing.T) {
	metric := map[string]string{"pattern_hash": "aaa", "level": "error", "sample": "ERROR relay query failed", "container_id": "/k8s/shop/api-1/api"}
	series := Result{Metric: metric}
	for i, value := range []float64{0, 10, 40, 90, 120, 95, 30} {
		series.Timestamps = append(series.Timestamps, logSpikeTestStart.Unix()+int64(i)*8)
		series.Values = append(series.Values, value)
	}
	normal := []Result{{Metric: map[string]string{"pattern_hash": "aaa", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{4}}}

	breakdown := buildLogPatternBreakdown([]Result{series}, normal, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(time.Minute))
	require.NotNil(t, breakdown)
	assert.Equal(t, 120.0, breakdown.Patterns[0].Peak)
	assert.Equal(t, "2026-01-15T10:00:32Z", breakdown.Patterns[0].PeakAt)
	assert.Equal(t, 120.0, breakdown.Total.Peak)
}

func TestBuildLogPatternBreakdown_NewPatternSummary(t *testing.T) {
	current := []Result{logSpikeTestSeries(map[string]string{"pattern_hash": "new", "level": "critical", "sample": "FATAL out of memory", "container_id": "/k8s/shop/api-1/api"}, 1, 40)}
	normal := []Result{{Metric: map[string]string{"pattern_hash": "old", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{3}}}

	breakdown := buildLogPatternBreakdown(current, normal, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute))
	require.NotNil(t, breakdown)
	assert.Equal(t,
		"Error and critical log lines peaked at 40 per 5 min against 3 normal. The pattern that rose most is critical-level: 40 per 5 min, not seen in the 6 h before.",
		breakdown.summary())
}

func TestBuildLogPatternBreakdown_KeepsTopPatternsAndSaysHowManyWereSeen(t *testing.T) {
	var current []Result
	for i := 0; i < logSpikeMaxPatterns+3; i++ {
		current = append(current, logSpikeTestSeries(map[string]string{
			"pattern_hash": fmt.Sprintf("p%02d", i), "level": "error",
			"sample": strings.Repeat("é", logSpikeExampleRunes+50), "container_id": "/k8s/shop/api-1/api",
		}, 0, float64(100-i)))
	}
	normal := []Result{{Metric: map[string]string{"pattern_hash": "p00", "level": "error"}, Timestamps: []int64{logSpikeTestStart.Unix()}, Values: []float64{50}}}

	breakdown := buildLogPatternBreakdown(current, normal, logSpikeTestMeasured, logSpikeTestStart, logSpikeTestStart.Add(20*time.Minute))
	require.NotNil(t, breakdown)
	assert.Len(t, breakdown.Patterns, logSpikeMaxPatterns)
	assert.Equal(t, logSpikeMaxPatterns+3, breakdown.PatternsSeen)
	assert.Equal(t, "p01", breakdown.Patterns[0].PatternHash, "ranked by the rise over normal, not by volume: p00 logs most but only doubled")
	assert.Equal(t, "p"+fmt.Sprintf("%02d", logSpikeMaxPatterns), breakdown.Patterns[logSpikeMaxPatterns-1].PatternHash, "p00 falls out of the top by rise")
	assert.Equal(t, strings.Repeat("é", logSpikeExampleRunes)+"…", breakdown.Patterns[0].Example, "cut on a rune boundary")
	assert.Equal(t, strings.Repeat("é", logSpikeExampleRunes+50), breakdown.topLabels()["sample"], "the event label keeps the whole line")
}

func TestLogSpikeWindow(t *testing.T) {
	alert := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	leadIn := alert.Add(-10 * time.Minute)
	farEnd := alert.Add(5 * time.Hour)
	nearEnd := alert.Add(20 * time.Minute)

	tests := []struct {
		name      string
		event     playbooks.PlaybookEvent
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "anchored on the alert start, not the lead-in start",
			event:     playbooks.PlaybookEvent{StartedAt: &leadIn, DetectedAt: &alert, EndedAt: &farEnd},
			now:       alert.Add(24 * time.Hour),
			wantStart: alert.Add(-30 * time.Minute),
			wantEnd:   alert.Add(time.Hour),
		},
		{
			name:      "a new alert ends now",
			event:     playbooks.PlaybookEvent{StartedAt: &leadIn, DetectedAt: &alert, EndedAt: &farEnd},
			now:       alert.Add(15 * time.Second),
			wantStart: alert.Add(-30 * time.Minute),
			wantEnd:   alert.Add(15 * time.Second),
		},
		{
			name:      "an alert that ended early",
			event:     playbooks.PlaybookEvent{DetectedAt: &alert, EndedAt: &nearEnd},
			now:       alert.Add(24 * time.Hour),
			wantStart: alert.Add(-30 * time.Minute),
			wantEnd:   nearEnd,
		},
		{
			name:      "no times on the event",
			event:     playbooks.PlaybookEvent{},
			now:       alert,
			wantStart: alert.Add(-30 * time.Minute),
			wantEnd:   alert,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := logSpikeWindow(tt.event, tt.now)
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}

func TestLogSpikeQueries(t *testing.T) {
	current, normal, measured := logSpikeQueries(`/k8s/shop/"api"`)
	assert.Equal(t,
		`sum by (pattern_hash, level, sample, container_id) (increase(container_log_messages_total{app_id="/k8s/shop/\"api\"", level=~"error|critical"}[5m]))`,
		current)
	assert.Equal(t,
		`sum by (pattern_hash, level) (increase(container_log_messages_total{app_id="/k8s/shop/\"api\"", level=~"error|critical"}[6h] offset 5m)) / 72`,
		normal, "the alert rule's own normal: the 6h count, 5 minutes back, per 5-minute bucket")
	assert.Equal(t,
		`sum(increase(container_log_messages_total{app_id="/k8s/shop/\"api\""}[6h] offset 5m))`,
		measured, "every level, same hours: was the workload being counted at all")
}
