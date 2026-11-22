package triage

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func promEvent(_ *testing.T, accountID string, labels map[string]any) models.Event {
	source := "prometheus"
	eventLabels := models.NewJsonObject(labels)
	return models.Event{
		Source:         &source,
		CloudAccountId: &accountID,
		Labels:         &eventLabels,
	}
}

// The Prometheus branch is reachable in a unit test for the first time here: it
// used to reach relay-server directly, so it could only be exercised against a
// live cluster agent. The fetcher is now passed in, which is also what lets it
// serve an account whose Prometheus is connected directly.
func TestFetchPrometheusMetricHistoryUsesTheInjectedFetcher(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-7 * 24 * time.Hour)

	var gotAccount, gotQuery string
	var gotStart, gotEnd time.Time
	fetcher := func(_ *security.RequestContext, accountID, query string, s, e time.Time) ([]*MetricHistory, error) {
		gotAccount, gotQuery, gotStart, gotEnd = accountID, query, s, e
		return []*MetricHistory{
			{Labels: map[string]string{"datname": "postgres"}, Timestamps: []int64{100, 130}, Values: []float64{0.99, 0.99}},
			{Labels: map[string]string{"datname": "temporal_visibility"}, Timestamps: []int64{100, 130}, Values: []float64{0.97, 0.96}},
		}, nil
	}

	ev := promEvent(t, "acc-1", map[string]any{"datname": "temporal_visibility"})
	alertDef := &AlertDefinition{MetricName: `pg_cache_hit_ratio`, CurrentThreshold: 0.98, Operator: "LessThanThreshold"}

	history, err := fetchPrometheusMetricHistory(context.Background(), ev, alertDef, "tenant-1", fetcher, start, end)

	require.NoError(t, err)
	require.NotNil(t, history)
	assert.False(t, history.otherInstance, "the alert's own series was found")
	assert.Equal(t, "temporal_visibility", history.Labels["datname"],
		"the series whose labels match the firing event must be the one analysed")
	assert.Equal(t, "acc-1", gotAccount)
	assert.Equal(t, `pg_cache_hit_ratio`, gotQuery, "the rule's own expression is sent unmodified")
	assert.Equal(t, start, gotStart)
	assert.Equal(t, end, gotEnd)
	assert.Equal(t, start.Format(time.RFC3339), history.StartTime, "the window is stamped onto the series")
	assert.Equal(t, end.Format(time.RFC3339), history.EndTime)
}

// A backend that cannot answer must yield no suggestion rather than a panic or a
// bogus one built from an empty series — and must say it failed, which is a different
// fact from answering with no data.
func TestFetchPrometheusMetricHistoryHandlesFailureAndEmptyResults(t *testing.T) {
	ev := promEvent(t, "acc-1", map[string]any{"datname": "postgres"})
	alertDef := &AlertDefinition{MetricName: "up", CurrentThreshold: 1, Operator: "LessThanThreshold"}
	now := time.Now().UTC()

	failing := func(*security.RequestContext, string, string, time.Time, time.Time) ([]*MetricHistory, error) {
		return nil, assert.AnError
	}
	history, err := fetchPrometheusMetricHistory(context.Background(), ev, alertDef, "t", failing, now.Add(-time.Hour), now)
	assert.Nil(t, history)
	assert.ErrorIs(t, err, errMetricQueryFailed, "a failed query is reported as a failure")

	empty := func(*security.RequestContext, string, string, time.Time, time.Time) ([]*MetricHistory, error) {
		return nil, nil
	}
	history, err = fetchPrometheusMetricHistory(context.Background(), ev, alertDef, "t", empty, now.Add(-time.Hour), now)
	assert.Nil(t, history)
	assert.NoError(t, err, "an answer with no series is not a failure")

	// Nothing wired at all (the zero value a caller that has no fetcher passes).
	history, err = fetchPrometheusMetricHistory(context.Background(), ev, alertDef, "t", nil, now.Add(-time.Hour), now)
	assert.Nil(t, history)
	assert.NoError(t, err)
}

// When no series carries the alert's own labels the most-violating one is analysed. That is
// another instance of the rule, and the history says so.
func TestFetchPrometheusMetricHistoryMarksAnotherInstance(t *testing.T) {
	fetcher := func(*security.RequestContext, string, string, time.Time, time.Time) ([]*MetricHistory, error) {
		return []*MetricHistory{
			{Labels: map[string]string{"pod": "cart-1"}, Timestamps: []int64{100, 130}, Values: []float64{5, 6}},
			{Labels: map[string]string{"pod": "cart-2"}, Timestamps: []int64{100, 130}, Values: []float64{1, 1}},
		}, nil
	}
	ev := promEvent(t, "acc-1", map[string]any{"pod": "orders-9"})
	now := time.Now().UTC()
	history, err := fetchPrometheusMetricHistory(context.Background(), ev, &AlertDefinition{MetricName: "restarts", CurrentThreshold: 2, Operator: ">"}, "t", fetcher, now.Add(-time.Hour), now)
	require.NoError(t, err)
	require.NotNil(t, history)
	assert.True(t, history.otherInstance, "the series analysed is not the alert's own")
	assert.Equal(t, "pod=cart-1", describeSeries(history.Labels))
}

// Guards the account guard: without a cloud account there is nothing to query,
// and the fetcher must not be called at all.
func TestFetchPrometheusMetricHistoryRequiresAnAccount(t *testing.T) {
	source := "prometheus"
	noLabels := models.NewJsonObject(map[string]any{})
	ev := models.Event{Source: &source, Labels: &noLabels}
	called := false
	fetcher := func(*security.RequestContext, string, string, time.Time, time.Time) ([]*MetricHistory, error) {
		called = true
		return nil, nil
	}
	now := time.Now().UTC()
	history, err := fetchPrometheusMetricHistory(context.Background(), ev, &AlertDefinition{MetricName: "up"}, "t", fetcher, now.Add(-time.Hour), now)
	assert.Nil(t, history)
	assert.NoError(t, err)
	assert.False(t, called, "no account id means no query")
}
