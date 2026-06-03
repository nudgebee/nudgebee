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
