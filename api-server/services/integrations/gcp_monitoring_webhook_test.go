package integrations

import (
	"testing"

	"nudgebee/services/event"

	"github.com/stretchr/testify/assert"
)

func TestMapGCPSeverityToPriority(t *testing.T) {
	cases := []struct {
		in   string
		want event.EventPriority
	}{
		// GCP native policy severities
		{"CRITICAL", event.EventPriorityHigh},
		{"ERROR", event.EventPriorityHigh},
		{"WARNING", event.EventPriorityMedium},
		// severity user-label style values
		{"high", event.EventPriorityHigh},
		{"medium", event.EventPriorityMedium},
		{"low", event.EventPriorityLow},
		{"info", event.EventPriorityInfo},
		{"informational", event.EventPriorityInfo},
		// case/whitespace insensitivity
		{"  Critical ", event.EventPriorityHigh},
		// unset / unknown must default to MEDIUM, not HIGH — an alert with no
		// configured severity is still a real alert but should not flood HIGH.
		{"", event.EventPriorityMedium},
		{"SEVERITY_UNSPECIFIED", event.EventPriorityMedium},
		{"garbage", event.EventPriorityMedium},
	}
	for _, c := range cases {
		if got := mapGCPSeverityToPriority(c.in); got != c.want {
			t.Errorf("mapGCPSeverityToPriority(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGCPLogEnrichmentLabels pins the labels the cloud_logs enricher reads. Before
// #36870 the webhook path set none of them, so a log-based metric alert ingested by
// webhook could not narrow its log query the way the same alert does when polled.
// Values are synthetic — real project/instance names must not be committed.
func TestGCPLogEnrichmentLabels(t *testing.T) {
	// Log-based metric alert on a Cloud SQL instance. gcpLogRawParams needs
	// gcp_project_id AND metric_log together to build the log_group_name that narrows
	// the query to the log the metric counts.
	logMetric := GCPMonitoringIncident{
		Metric: GCPMonitoringMetric{
			Type:   "logging.googleapis.com/user/slow_queries",
			Labels: map[string]string{"log": "cloudsql.googleapis.com/postgres.log"},
		},
	}
	got := gcpLogEnrichmentLabels(logMetric, "example-project",
		"projects/example-project/alertPolicies/1234567890123456789")
	assert.Equal(t, map[string]string{
		"gcp_project_id": "example-project",
		"metric_log":     "cloudsql.googleapis.com/postgres.log",
		"gcp_policy_id":  "projects/example-project/alertPolicies/1234567890123456789",
	}, got)

	// A plain metric alert names no log, so nothing narrows the query and the
	// collector scopes by the resource instead.
	plain := GCPMonitoringIncident{
		Metric: GCPMonitoringMetric{Type: "cloudsql.googleapis.com/database/cpu/utilization"},
	}
	got = gcpLogEnrichmentLabels(plain, "example-project", "projects/example-project/alertPolicies/1")
	assert.NotContains(t, got, "metric_log")
	assert.Equal(t, "example-project", got["gcp_project_id"])

	// Nothing identifiable: emit no labels rather than empty ones the enricher would
	// then have to treat as present.
	assert.Empty(t, gcpLogEnrichmentLabels(GCPMonitoringIncident{}, "", ""))
}
