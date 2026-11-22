package cloud

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An alarm's evidence must chart the metric the alarm evaluates. The service
// defaults missed it for alarms on unlisted services, on finer dimension sets than
// the defaults use, and on metrics with no dimensions, so those alerts reached the
// analysis with only the raw alarm. Labels below are the shapes those alarms carry.
func TestAwsAlarmMetricQuery(t *testing.T) {
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{})
	alarm := func(namespace, metric, stat, dims string) map[string]string {
		return map[string]string{
			"aws_region":                 "us-east-1",
			"aws_event_metric_namespace": namespace,
			"aws_event_metric_name":      metric,
			"aws_event_metric_statistic": stat,
			"aws_event_alarm_dimensions": dims,
		}
	}
	entry := func(t *testing.T, q map[string]any) CloudWatchMetricEntry {
		t.Helper()
		require.NotNil(t, q)
		entries, ok := q["metric_queries"].([]CloudWatchMetricEntry)
		require.True(t, ok)
		require.Len(t, entries, 1)
		require.NotNil(t, entries[0].MetricStat)
		return entries[0]
	}

	t.Run("custom metric with no dimensions is queried with none", func(t *testing.T) {
		q := awsAlarmMetricQuery(ctx, alarm("App/Logs", "Tracebacks", "Sum", "[]"))
		e := entry(t, q)
		assert.Equal(t, "CloudWatch", q["service_name"],
			"the collector dispatches on service name; CloudWatch runs metric_queries as given")
		assert.Equal(t, "App/Logs", q["metric_namespace"])
		assert.Equal(t, "App/Logs", e.MetricStat.Metric.Namespace)
		assert.Equal(t, "Tracebacks", e.MetricStat.Metric.Name)
		assert.Equal(t, "Sum", e.MetricStat.Stat)
		assert.Empty(t, e.MetricStat.Metric.Dimensions)
		assert.Positive(t, e.MetricStat.Period)
	})

	t.Run("service with no default metric list keeps the alarm's dimension", func(t *testing.T) {
		arn := "arn:aws:states:us-east-1:111122223333:stateMachine:example"
		e := entry(t, awsAlarmMetricQuery(ctx, alarm("AWS/States", "ExecutionsFailed", "Sum",
			`[{"Name":"StateMachineArn","Value":"`+arn+`"}]`)))
		assert.Equal(t, map[string]string{"StateMachineArn": arn}, e.MetricStat.Metric.Dimensions)
	})

	t.Run("every alarm dimension is kept, not just the resource one", func(t *testing.T) {
		e := entry(t, awsAlarmMetricQuery(ctx, alarm("AWS/FSx", "StorageUsed", "Average",
			`[{"Name":"FileSystemId","Value":"fs-0123"},{"Name":"VolumeId","Value":"fsvol-0456"}]`)))
		assert.Equal(t, map[string]string{"FileSystemId": "fs-0123", "VolumeId": "fsvol-0456"},
			e.MetricStat.Metric.Dimensions)
	})

	t.Run("@-prefixed dimensions are kept", func(t *testing.T) {
		// Dropping them made the query read a dimensionless series that has no data.
		e := entry(t, awsAlarmMetricQuery(ctx, alarm("App/Logs", "Errors", "Sum",
			`[{"Name":"@aws.account","Value":"111122223333"},{"Name":"@aws.region","Value":"us-east-1"}]`)))
		assert.Equal(t, map[string]string{"@aws.account": "111122223333", "@aws.region": "us-east-1"},
			e.MetricStat.Metric.Dimensions)
	})

	t.Run("alarm dimension names are mapped to metric dimension names", func(t *testing.T) {
		e := entry(t, awsAlarmMetricQuery(ctx, alarm("AWS/VPN", "TunnelState", "Maximum",
			`[{"Name":"VpnConnectionId","Value":"vpn-0123"}]`)))
		assert.Equal(t, map[string]string{"VpnId": "vpn-0123"}, e.MetricStat.Metric.Dimensions)
	})

	t.Run("no single metric to chart", func(t *testing.T) {
		assert.Nil(t, awsAlarmMetricQuery(ctx, alarm("AWS/EC2", "CPUUtilization", "", "[]")),
			"a percentile alarm has no statistic label")
		assert.Nil(t, awsAlarmMetricQuery(ctx, alarm("", "", "", "")),
			"an EventBridge event names no metric")
		assert.Nil(t, awsAlarmMetricQuery(ctx, alarm("AWS/EC2", "CPUUtilization", "Average", "not json")),
			"unreadable dimensions would query a different series")
	})
}

func TestMergeAlarmAndDefaultSeries(t *testing.T) {
	alarm := []MetricItem{{Name: "Evictions", Statistics: "Sum", Values: []float64{3}}}
	defaults := []MetricItem{
		{Name: "CPUUtilization", Statistics: "Average", Values: []float64{40}},
		{Name: "Evictions", Statistics: "Sum", ResourceId: "cache-001", Values: []float64{3}},
		{Name: "Evictions", Statistics: "Average", Values: []float64{1}},
	}

	merged := mergeAlarmAndDefaultSeries(alarm, defaults)
	require.Len(t, merged, 3)
	assert.Equal(t, alarm[0], merged[0], "the alarm's own series comes first")
	assert.Equal(t, "CPUUtilization", merged[1].Name)
	assert.Equal(t, "Average", merged[2].Statistics, "the same metric under another statistic is a different chart")

	assert.Equal(t, defaults, mergeAlarmAndDefaultSeries(nil, defaults),
		"with no alarm series the defaults are kept whole")
}

func TestCloudMetricsCanAutoExecuteAwsMetricAlarm(t *testing.T) {
	ctxWith := func(labels map[string]string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(),
			playbooks.PlaybookEvent{Source: "AWS_CloudWatch_Alarm", Labels: labels})
	}
	action := cloudMetricsAction{}

	assert.True(t, action.CanAutoExecute(ctxWith(map[string]string{
		"aws_region": "us-east-1", "aws_event_metric_namespace": "AWS/Usage",
		"aws_event_metric_name": "CallCount", "aws_event_metric_statistic": "Sum",
	})), "a standard-namespace alarm with no resource still has its own metric to chart")

	assert.False(t, action.CanAutoExecute(ctxWith(map[string]string{
		"aws_region": "us-east-1", "aws_event_metric_namespace": "AWS/Usage",
		"aws_event_metric_name": "CallCount",
	})), "no statistic and no resource: nothing to query")
}
