package cloud

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloudResourceAction(t *testing.T) {
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
	cloudServiceMap := cloudServiceMapAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_CLOUD_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{})
	response, err := cloudServiceMap.Execute(defaultPlaybookActionContext, map[string]any{
		"service_name": "AmazonECS",
		"region":       "us-east-1",
		"resource_id":  "xray-ecs-dev-ecs-cluster/xray-ecs-dev-bff/bff-app",
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestCloudPerformanceInsightsAction(t *testing.T) {
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
			"aws_account":                  "740395098545",
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

func TestCloudPerformanceInsightsCanAutoExecute_StandardRDSAlarm(t *testing.T) {
	piAction := cloudPerformanceInsightsAction{}

	// Test with standard AWS/RDS CloudWatch alarm
	event := playbooks.PlaybookEvent{
		Labels: map[string]string{
			"aws_region":                 "us-east-1",
			"aws_account":                "740395098545",
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
