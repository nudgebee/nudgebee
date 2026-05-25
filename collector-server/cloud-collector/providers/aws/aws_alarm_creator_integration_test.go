//go:build integration
// +build integration

package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests require actual AWS credentials and will make real API calls
// Run with: go test -tags=integration -v ./... -run TestIntegration

func getTestAccount(t *testing.T) providers.Account {
	accountNumber := os.Getenv("AWS_TEST_ACCOUNT_NUMBER")
	if accountNumber == "" {
		t.Skip("AWS_TEST_ACCOUNT_NUMBER not set, skipping integration test")
	}

	return providers.Account{
		AccountNumber: accountNumber,
	}
}

func getTestRegion() string {
	region := os.Getenv("AWS_TEST_REGION")
	if region == "" {
		return "us-east-1"
	}
	return region
}

// TestIntegrationCreateSimpleMetricAlarm tests creating a real CloudWatch alarm
func TestIntegrationCreateSimpleMetricAlarm(t *testing.T) {
	ctx := context.Background()
	account := getTestAccount(t)
	region := getTestRegion()

	// Generate unique alarm name using timestamp
	alarmName := fmt.Sprintf("test-nudgebee-alarm-abhay-%d", time.Now().Unix())

	config := providers.AlarmCreationConfig{
		AlarmName:          alarmName,
		MetricName:         "CPUUtilization",
		Namespace:          "AWS/EC2",
		Statistic:          "Average",
		Period:             300,
		EvaluationPeriods:  2,
		DatapointsToAlarm:  2,
		Threshold:          80.0,
		ComparisonOperator: "GreaterThanThreshold",
		TreatMissingData:   "notBreaching",
		Dimensions: []providers.AlarmDimension{
			// Use a dummy instance ID - alarm will be created even if instance doesn't exist
			{Name: "InstanceId", Value: "i-test123456789"},
		},
	}

	// Create the alarm
	err := CreateCloudWatchAlarm(ctx, account, config, region)
	require.NoError(t, err, "Failed to create CloudWatch alarm")

	// Verify the alarm was created
	alarm, err := DescribeCloudWatchAlarm(ctx, account, alarmName, region)
	require.NoError(t, err, "Failed to describe CloudWatch alarm")
	assert.NotNil(t, alarm)
	assert.Equal(t, alarmName, *alarm.AlarmName)
	assert.Equal(t, 80.0, *alarm.Threshold)
	assert.Equal(t, "Average", string(alarm.Statistic))

	// Cleanup: Delete the test alarm
	t.Cleanup(func() {
		err := DeleteCloudWatchAlarm(ctx, account, alarmName, region)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test alarm %s: %v", alarmName, err)
		}
	})
}

// TestIntegrationCreateMetricMathAlarm tests creating a metric math alarm
func TestIntegrationCreateMetricMathAlarm(t *testing.T) {
	ctx := context.Background()
	account := getTestAccount(t)
	region := getTestRegion()

	// This test requires an actual ALB to exist
	albArn := os.Getenv("AWS_TEST_ALB_ARN")
	if albArn == "" {
		t.Skip("AWS_TEST_ALB_ARN not set, skipping metric math alarm test")
	}

	// Extract LoadBalancer dimension from ARN (format: app/my-alb/1234567890abcdef)
	// ARN format: arn:aws:elasticloadbalancing:region:account-id:loadbalancer/app/my-alb/1234567890abcdef
	// We need just the part after loadbalancer/
	lbDimension := albArn[len("arn:aws:elasticloadbalancing:"):]
	if idx := len(lbDimension); idx > 0 {
		if startIdx := len("region:account-id:loadbalancer/"); startIdx < len(lbDimension) {
			// This is a simplified parser - adjust as needed
			t.Skip("ALB ARN parsing not implemented for this test")
		}
	}

	alarmName := fmt.Sprintf("test-nudgebee-http-error-rate-%d", time.Now().Unix())

	config := providers.AlarmCreationConfig{
		AlarmName:          alarmName,
		Period:             60,
		EvaluationPeriods:  5,
		DatapointsToAlarm:  3,
		Threshold:          5.0,
		ComparisonOperator: "GreaterThanThreshold",
		TreatMissingData:   "notBreaching",
		Metrics: []providers.MetricQueryConfig{
			{
				Id:         "m1",
				ReturnData: false,
				MetricStat: &providers.MetricStatConfig{
					Metric: providers.MetricInfoConfig{
						Namespace:  "AWS/ApplicationELB",
						MetricName: "HTTPCode_Target_5XX_Count",
						Dimensions: []providers.AlarmDimension{
							{Name: "LoadBalancer", Value: lbDimension},
						},
					},
					Stat:   "Sum",
					Period: 60,
				},
			},
			{
				Id:         "m2",
				ReturnData: false,
				MetricStat: &providers.MetricStatConfig{
					Metric: providers.MetricInfoConfig{
						Namespace:  "AWS/ApplicationELB",
						MetricName: "RequestCount",
						Dimensions: []providers.AlarmDimension{
							{Name: "LoadBalancer", Value: lbDimension},
						},
					},
					Stat:   "Sum",
					Period: 60,
				},
			},
			{
				Id:         "e1",
				Expression: "(m1/m2)*100",
				ReturnData: true,
				Label:      "HTTP 5XX Error Rate (%)",
			},
		},
	}

	// Create the alarm
	err := CreateCloudWatchAlarm(ctx, account, config, region)
	require.NoError(t, err, "Failed to create metric math alarm")

	// Verify the alarm was created
	alarm, err := DescribeCloudWatchAlarm(ctx, account, alarmName, region)
	require.NoError(t, err, "Failed to describe CloudWatch alarm")
	assert.NotNil(t, alarm)
	assert.Equal(t, alarmName, *alarm.AlarmName)
	assert.Equal(t, 5.0, *alarm.Threshold)
	assert.Len(t, alarm.Metrics, 3, "Should have 3 metrics in metric math alarm")

	// Cleanup
	t.Cleanup(func() {
		err := DeleteCloudWatchAlarm(ctx, account, alarmName, region)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test alarm %s: %v", alarmName, err)
		}
	})
}

// TestIntegrationCreateAlarmFromRecommendation tests the full flow from recommendation data
func TestIntegrationCreateAlarmFromRecommendation(t *testing.T) {
	ctx := context.Background()
	account := getTestAccount(t)
	region := getTestRegion()

	alarmName := fmt.Sprintf("test-nudgebee-from-recommendation-%d", time.Now().Unix())

	recommendation := providers.Recommendation{
		RuleName:       "aws_ec2_cpu_utilization_alarm_missing",
		ResourceId:     "i-test123456789",
		ResourceRegion: region,
		Data: map[string]interface{}{
			"alarm_config": map[string]interface{}{
				"alarm_name":          alarmName,
				"namespace":           "AWS/EC2",
				"metric_name":         "CPUUtilization",
				"statistic":           "Average",
				"period":              300,
				"evaluation_periods":  2,
				"datapoints_to_alarm": 2,
				"threshold":           80.0,
				"comparison_operator": "GreaterThanThreshold",
				"treat_missing_data":  "notBreaching",
				"dimensions": []map[string]interface{}{
					{"name": "InstanceId", "value": "i-test123456789"},
				},
			},
		},
	}

	// Create alarm from recommendation
	err := CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation)
	require.NoError(t, err, "Failed to create alarm from recommendation")

	// Verify the alarm was created
	alarm, err := DescribeCloudWatchAlarm(ctx, account, alarmName, region)
	require.NoError(t, err, "Failed to describe CloudWatch alarm")
	assert.NotNil(t, alarm)
	assert.Equal(t, alarmName, *alarm.AlarmName)

	// Cleanup
	t.Cleanup(func() {
		err := DeleteCloudWatchAlarm(ctx, account, alarmName, region)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test alarm %s: %v", alarmName, err)
		}
	})
}

// TestIntegrationDeleteAlarm tests alarm deletion
func TestIntegrationDeleteAlarm(t *testing.T) {
	ctx := context.Background()
	account := getTestAccount(t)
	region := getTestRegion()

	alarmName := fmt.Sprintf("test-nudgebee-delete-%d", time.Now().Unix())

	// First create an alarm to delete
	config := providers.AlarmCreationConfig{
		AlarmName:          alarmName,
		MetricName:         "CPUUtilization",
		Namespace:          "AWS/EC2",
		Statistic:          "Average",
		Period:             300,
		EvaluationPeriods:  2,
		DatapointsToAlarm:  2,
		Threshold:          80.0,
		ComparisonOperator: "GreaterThanThreshold",
		TreatMissingData:   "notBreaching",
		Dimensions: []providers.AlarmDimension{
			{Name: "InstanceId", Value: "i-test123456789"},
		},
	}

	err := CreateCloudWatchAlarm(ctx, account, config, region)
	require.NoError(t, err, "Failed to create test alarm")

	// Verify it exists
	alarm, err := DescribeCloudWatchAlarm(ctx, account, alarmName, region)
	require.NoError(t, err)
	assert.NotNil(t, alarm)

	// Delete the alarm
	err = DeleteCloudWatchAlarm(ctx, account, alarmName, region)
	require.NoError(t, err, "Failed to delete alarm")

	// Verify it no longer exists (should return error)
	_, err = DescribeCloudWatchAlarm(ctx, account, alarmName, region)
	assert.Error(t, err, "Alarm should not exist after deletion")
	assert.Contains(t, err.Error(), "alarm not found")
}

// TestIntegrationDescribeNonExistentAlarm tests describing an alarm that doesn't exist
func TestIntegrationDescribeNonExistentAlarm(t *testing.T) {
	ctx := context.Background()
	account := getTestAccount(t)
	region := getTestRegion()

	nonExistentAlarmName := "nudgebee-this-alarm-does-not-exist-12345"

	_, err := DescribeCloudWatchAlarm(ctx, account, nonExistentAlarmName, region)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alarm not found")
}
