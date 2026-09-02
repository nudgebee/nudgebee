package alertrule

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trimmed but real-shaped GET /api/v1/monitor element.
const datadogMonitorJSON = `[
  {
    "id": 123456789,
    "name": "High CPU on checkout",
    "type": "metric alert",
    "query": "avg(last_5m):avg:system.cpu.user{service:checkout} > 90",
    "message": "CPU is above 90% @slack-oncall",
    "tags": ["env:prod", "service:checkout", "team", "severity:critical"],
    "priority": 2,
    "options": {"silenced": {}}
  }
]`

func TestDatadogMonitorToExternalRule(t *testing.T) {
	var monitors []datadogMonitor
	require.NoError(t, json.Unmarshal([]byte(datadogMonitorJSON), &monitors))
	require.Len(t, monitors, 1)

	got := datadogMonitorToExternalRule(monitors[0])

	assert.Equal(t, "123456789", got.ExternalRuleId)
	assert.Equal(t, "High CPU on checkout", got.Name)
	assert.Equal(t, "metric", got.AlertType)
	assert.Equal(t, "avg(last_5m):avg:system.cpu.user{service:checkout} > 90", got.Query)
	assert.Equal(t, "critical", got.Severity)
	assert.True(t, got.Enabled)
	assert.Equal(t, "CPU is above 90% @slack-oncall", got.Annotations["description"])
	assert.Equal(t, "prod", got.Labels["env"])
	assert.Equal(t, "checkout", got.Labels["service"])
	// A bare tag keeps its key with an empty value rather than being dropped.
	assert.Equal(t, "", got.Labels["team"])
	assert.Contains(t, got.Labels, "team")
	assert.Equal(t, int64(123456789), got.ProviderConfig["datadog_monitor_id"])
}

// event_rules.alert_type is FK-constrained to 'log' / 'metric'.
func TestDatadogMonitorAlertType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"metric alert", "metric"},
		{"query alert", "metric"},
		{"log alert", "log"},
		{"error-tracking alert", "metric"},
		{"", "metric"},
	} {
		assert.Equal(t, tc.want, datadogMonitorAlertType(tc.in), "monitor type %q", tc.in)
	}
}

// event_rules.severity is FK-constrained to exactly 'critical' / 'warning', so
// every path must land on one of those two.
func TestDatadogPriorityToSeverity(t *testing.T) {
	p := func(i int) *int { return &i }
	allowed := map[string]bool{"critical": true, "warning": true}

	for _, tc := range []struct {
		name     string
		priority *int
		tag      string
		want     string
	}{
		{"explicit critical tag wins", p(5), "critical", "critical"},
		{"explicit warning tag wins over P1", p(1), "warning", "warning"},
		{"P1 with no tag", p(1), "", "critical"},
		{"P2 with no tag", p(2), "", "critical"},
		{"P3 with no tag", p(3), "", "warning"},
		{"nil priority, no tag", nil, "", "warning"},
		{"zero priority", p(0), "", "warning"},
		{"unrecognised tag", nil, "banana", "warning"},
	} {
		got := datadogPriorityToSeverity(tc.priority, tc.tag)
		assert.Equal(t, tc.want, got, tc.name)
		assert.True(t, allowed[got], "%s produced %q", tc.name, got)
	}
}

// Datadog has no "disabled" flag; an indefinite silence is the closest thing.
func TestDatadogMonitorMutedIndefinitely(t *testing.T) {
	until := int64(1786700000)

	var indefinite datadogMonitor
	indefinite.Options.Silenced = map[string]*int64{"*": nil}
	assert.True(t, datadogMonitorMutedIndefinitely(indefinite))

	var temporary datadogMonitor
	temporary.Options.Silenced = map[string]*int64{"*": &until}
	assert.False(t, temporary.Options.Silenced["*"] == nil)
	assert.False(t, datadogMonitorMutedIndefinitely(temporary))

	var none datadogMonitor
	none.Options.Silenced = map[string]*int64{}
	assert.False(t, datadogMonitorMutedIndefinitely(none))
}

func TestDatadogTagsToLabels(t *testing.T) {
	labels := datadogTagsToLabels([]string{"env:prod", "team", "k:v:extra", "", ":novalue"})
	assert.Equal(t, "prod", labels["env"])
	assert.Equal(t, "", labels["team"])
	// Only the first colon separates; the rest belongs to the value.
	assert.Equal(t, "v:extra", labels["k"])
	// An empty key is dropped rather than creating a "" entry.
	assert.NotContains(t, labels, "")
}
