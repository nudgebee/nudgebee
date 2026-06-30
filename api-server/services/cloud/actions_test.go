package cloud

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloudResourceAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT")
	cloudResource := cloudResourceAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})
	response, err := cloudResource.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name": "amazonec2",
		"regions":      []string{"us-east-1"},
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestCloudMetricsAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT")
	cloudMetrics := cloudMetricsAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})
	response, err := cloudMetrics.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name": "amazonec2",
		"region":       "us-east-1",
		"metric_names": []string{"CPUUtilization"},
		"statistics":   []string{"Average"},
		"dimensions": []map[string]string{
			{"Name": "InstanceId", "Values": "i-0695d9d318b7bbf30"},
		},
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestCloudLogAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT")
	cloudLog := cloudLogAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})
	response, err := cloudLog.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name":   "amazonec2",
		"region":         "us-east-1",
		"log_group_name": "MyUbuntuLogs",
		"query_string":   "",
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

// TestGCPEnricherGating covers the region-optional gating and the incident-ID guard
// for GCP events. Pure unit test — only reads event labels, no cloud/DB access.
func TestGCPEnricherGating(t *testing.T) {
	ctxWith := func(source string, labels map[string]string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(),
			playbooks.PlaybookEvent{Source: source, Labels: labels})
	}
	logAction := cloudLogAction{}
	resAction := cloudResourceAction{}
	metricsAction := cloudMetricsAction{}

	// Cloud Run metric alert: real service_name, NO region -> enrich (region optional).
	cloudRun := map[string]string{
		"gcp_account": "live-fullspectrum", "gcp_project_id": "live-fullspectrum",
		"gcp_service_name": "Cloud Run", "gcp_event_instance": "frontoffice-pre-alpha",
		"gcp_incident_id": "0.o9i3cz02x353", "gcp_event_resource_type": "cloud_run_revision",
	}
	assert.True(t, logAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", cloudRun)),
		"cloud_logs should enrich a GCP metric alert with a real resource id even without region")
	assert.True(t, resAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", cloudRun)),
		"cloud_resources should enrich a GCP metric alert with a real resource id even without region")

	// Incident-ID fallback (gcp_event_instance == gcp_incident_id), plain metric alert
	// with no log-based metric -> do NOT auto-enrich (filter would match nothing).
	incidentOnly := map[string]string{
		"gcp_account": "full-auth", "gcp_project_id": "full-auth",
		"gcp_service_name":   "Cloud Monitoring",
		"gcp_event_instance": "0.o9abc", "gcp_incident_id": "0.o9abc",
	}
	assert.False(t, logAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", incidentOnly)),
		"cloud_logs should not enrich when the only identifier is the incident id")
	assert.False(t, resAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", incidentOnly)),
		"cloud_resources should not enrich when the only identifier is the incident id")

	// User-defined log-based metric alert (e.g. l7_lb Log4j): no region, no real resource
	// id, but the metric's own filter scopes the logs -> enrich.
	logMetric := map[string]string{
		"gcp_account": "full-auth", "gcp_project_id": "full-auth",
		"gcp_service_name":   "Cloud Monitoring",
		"gcp_event_instance": "0.o9def", "gcp_incident_id": "0.o9def",
		"gcp_metric_type": "logging.googleapis.com/user/log4j_exploits",
	}
	assert.True(t, logAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", logMetric)),
		"cloud_logs should enrich a user-defined log-based metric alert via its filter")

	// Native GCP log alert with no region -> enrich.
	logAlert := map[string]string{
		"gcp_account": "full-auth", "gcp_alert_type": "log",
	}
	assert.True(t, logAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", logAlert)),
		"cloud_logs should enrich a native GCP log alert even without region")

	// cloud_metrics: a GCP metric alert (has gcp_event_metric_type, not log-based) should
	// fetch the metric timeseries even without region (Cloud Monitoring is global).
	metricAlert := map[string]string{
		"gcp_account": "full-auth", "gcp_project_id": "full-auth",
		"gcp_alert_type": "metric", "gcp_service_name": "Cloud Run",
		"gcp_event_metric_type": "run.googleapis.com/request_count",
		"gcp_event_instance":    "0.o9xyz", "gcp_incident_id": "0.o9xyz",
	}
	assert.True(t, metricsAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", metricAlert)),
		"cloud_metrics should fetch the metric timeseries for a GCP metric alert without region")

	// cloud_metrics must skip log-based alerts (no metric to chart) and alerts with no metric type.
	assert.False(t, metricsAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", logAlert)),
		"cloud_metrics should skip log-based GCP alerts")
	assert.False(t, metricsAction.CanAutoExecute(ctxWith("GCP_Metric_Alert", cloudRun)),
		"cloud_metrics should not fire when there is no gcp_event_metric_type")
}

func TestCloudServiceMap(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT", "TEST_AWS_ECS_RESOURCE_ID")
	cloudServiceMap := cloudServiceMapAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})
	response, err := cloudServiceMap.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name": "AmazonECS",
		"region":       "us-east-1",
		"resource_id":  os.Getenv("TEST_AWS_ECS_RESOURCE_ID"),
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestCloudPerformanceInsightsAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT")
	piAction := cloudPerformanceInsightsAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})

	// Test with RDS instance "main" from the actual event
	response, err := piAction.Execute(defaultPlaybookActionContext, map[string]any{
		"db_instance_identifier": "main",
		"region":                 "us-east-1",
	})

	assert.NotNil(t, response)
	// Error is acceptable if PI is not enabled or instance doesn't exist
	if err != nil {
		t.Logf("Performance Insights test returned error (expected if PI not enabled): %v", err)
	} else {
		t.Logf("Performance Insights response: %+v", response)
	}
}

func TestCloudPerformanceInsightsAutoExecute(t *testing.T) {
	piAction := cloudPerformanceInsightsAction{}

	// Test with event labels similar to event b07b90b8-ea86-4c27-a463-8f0c866baa2a
	// This event has a log-based RDS metric
	event := playbooks.PlaybookEvent{
		Labels: map[string]string{
			"aws_region":                   "us-east-1",
			"aws_account":                  os.Getenv("TEST_AWS_ACCOUNT_NUMBER"),
			"metric_filter_log_group_name": "/aws/rds/instance/main/postgresql",
			"aws_event_metric_name":        "rds-error-log-alert",
			"aws_event_metric_namespace":   "rds",
		},
	}

	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), event)

	// Test CanAutoExecute
	canAutoExecute := piAction.CanAutoExecute(defaultPlaybookActionContext)
	assert.True(t, canAutoExecute, "CanAutoExecute should return true for RDS log-based metrics")

	// Test AutoExecute
	response, err := piAction.AutoExecute(defaultPlaybookActionContext)

	if err != nil {
		t.Logf("Performance Insights AutoExecute returned error (expected if PI not enabled): %v", err)
	} else {
		assert.NotNil(t, response)
		t.Logf("Performance Insights AutoExecute response: %+v", response)
	}
}

func TestBuildVpcFlowLogsParsePattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty returns default",
			input:    "",
			expected: `/(?<version>\d+) (?<account_id>\d+) (?<interface_id>\S+) (?<srcaddr>\S+) (?<dstaddr>\S+) (?<srcport>\d+) (?<dstport>\d+) (?<protocol>\d+) (?<packets>\d+) (?<bytes>\d+) (?<start>\d+) (?<end>\d+) (?<action>\S+) (?<log_status>\S+)/`,
		},
		{
			name:     "standard fields",
			input:    "${version} ${account-id} ${srcaddr}",
			expected: `/(?<version>\d+) (?<account_id>\d+) (?<srcaddr>\S+)/`,
		},
		{
			name:     "special chars escaped",
			input:    "${srcaddr}.${dstaddr}",
			expected: `/(?<srcaddr>\S+)\.(?<dstaddr>\S+)/`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildVpcFlowLogsParsePattern(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func BenchmarkBuildVpcFlowLogsParsePattern(b *testing.B) {
	logFormat := "${version} ${account-id} ${interface-id} ${srcaddr} ${dstaddr} ${srcport} ${dstport} ${protocol} ${packets} ${bytes} ${start} ${end} ${action} ${log-status}"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buildVpcFlowLogsParsePattern(logFormat)
	}
}

func TestCloudPerformanceInsightsCanAutoExecute_StandardRDSAlarm(t *testing.T) {
	piAction := cloudPerformanceInsightsAction{}

	// Test with standard AWS/RDS CloudWatch alarm
	event := playbooks.PlaybookEvent{
		Labels: map[string]string{
			"aws_region":                 "us-east-1",
			"aws_account":                os.Getenv("TEST_AWS_ACCOUNT_NUMBER"),
			"aws_event_metric_namespace": "AWS/RDS",
			"aws_event_alarm_dimensions": `[{"Name":"DBInstanceIdentifier","Value":"main"}]`,
			"aws_event_metric_name":      "CPUUtilization",
		},
	}

	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), event)

	// Test CanAutoExecute
	canAutoExecute := piAction.CanAutoExecute(defaultPlaybookActionContext)
	assert.True(t, canAutoExecute, "CanAutoExecute should return true for standard RDS alarms with DBInstanceIdentifier")

	// Extract instance ID
	response, err := piAction.AutoExecute(defaultPlaybookActionContext)

	if err != nil {
		t.Logf("Performance Insights AutoExecute returned error (expected if PI not enabled): %v", err)
	} else {
		assert.NotNil(t, response)
		t.Logf("Performance Insights AutoExecute for standard alarm response: %+v", response)
	}
}

// TestCloudGCPAuditLogGating covers the deploy/change enricher gate: fires for GCP Cloud
// Run events with a project, skips other GCP resources and project-less events.
func TestCloudGCPAuditLogGating(t *testing.T) {
	ctxWith := func(labels map[string]string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(),
			playbooks.PlaybookEvent{Source: "GCP_Metric_Alert", Labels: labels})
	}
	audit := cloudGCPAuditLogAction{}

	assert.True(t, audit.CanAutoExecute(ctxWith(map[string]string{
		"gcp_project_id": "full-auth", "gcp_event_resource_type": "cloud_run_revision",
	})), "Cloud Run event (by resource type) should fetch recent changes")
	assert.True(t, audit.CanAutoExecute(ctxWith(map[string]string{
		"gcp_project_id": "full-auth", "gcp_service_name": "Cloud Run",
	})), "Cloud Run event (by service name) should fetch recent changes")
	assert.False(t, audit.CanAutoExecute(ctxWith(map[string]string{
		"gcp_project_id": "full-auth", "gcp_event_resource_type": "cloudsql_database",
	})), "non-Cloud-Run GCP resource should not run the run.googleapis.com audit query")
	assert.False(t, audit.CanAutoExecute(ctxWith(map[string]string{
		"gcp_service_name": "Cloud Run",
	})), "no project -> skip")
}
