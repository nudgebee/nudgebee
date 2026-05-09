package aws

import (
	"context"
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAwsCloudwatchAlarms(t *testing.T) {
	r, err := getAwsCloudwatchAlarms(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789",
	}, AlarmsFilter{
		Status: "OK",
	})
	assert.Nil(t, err)
	assert.NotNil(t, r)
}

func TestGetAwsCloudwatchAlarmsForResource(t *testing.T) {
	r, err := getAwsCloudwatchAlarms(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789"}, AlarmsFilter{
		ResourceIds: []string{"main"},
		ServiceName: "AmazonRDS",
	})
	assert.Nil(t, err)
	assert.NotNil(t, r)
}

func TestEnrichCloudWatchAlarm(t *testing.T) {
	ctx := context.Background()

	t.Run("custom namespace with only generic dimensions falls back to alarm name", func(t *testing.T) {
		// Reproduces the rds-deadlock-log-alert scenario (event 818f5d90)
		info := CloudWatchAlarmInfo{
			AlarmName:       "rds-deadlock-log-alert",
			MetricName:      "rds-deadlock-metric",
			MetricNamespace: "rds", // custom namespace, NOT in cloudwatchNamespaceServiceMap
			Statistic:       "Sum",
			Threshold:       "0",
			Region:          "us-east-1",
			AccountNumber:   "740395098545",
			AlarmArn:        "arn:aws:cloudwatch:us-east-1:740395098545:alarm:rds-deadlock-log-alert",
			StateValue:      "ALARM",
			Dimensions: []AlarmDimension{
				{Name: "@aws.account", Value: "740395098545"},
				{Name: "@aws.region", Value: "us-east-1"},
			},
		}

		enrichment := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)

		// Should NOT use the account number as resource ID
		assert.Equal(t, "rds-deadlock-log-alert", enrichment.ResourceId, "should fall back to alarm name, not account number")
		assert.Equal(t, "AlarmName", enrichment.InstanceType)
		assert.Equal(t, "CloudWatch", enrichment.ResourceServiceName, "custom namespace should map to CloudWatch")
		assert.Equal(t, "alarm", enrichment.ResourceType)

		// Individual dimension labels (@aws.account → aws_account, @aws.region → aws_region)
		assert.Equal(t, "740395098545", enrichment.Labels["aws_dim_aws_account"])
		assert.Equal(t, "us-east-1", enrichment.Labels["aws_dim_aws_region"])
	})

	t.Run("known namespace AWS/RDS picks DBInstanceIdentifier", func(t *testing.T) {
		info := CloudWatchAlarmInfo{
			AlarmName:       "rds-cpu-alarm",
			MetricName:      "CPUUtilization",
			MetricNamespace: "AWS/RDS",
			Statistic:       "Average",
			Threshold:       "80",
			Region:          "us-east-1",
			AccountNumber:   "123456789012",
			AlarmArn:        "arn:aws:cloudwatch:us-east-1:123456789012:alarm:rds-cpu-alarm",
			StateValue:      "ALARM",
			Dimensions: []AlarmDimension{
				{Name: "DBInstanceIdentifier", Value: "my-db-instance"},
			},
		}

		enrichment := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)

		assert.Equal(t, "my-db-instance", enrichment.ResourceId)
		assert.Equal(t, "DBInstanceIdentifier", enrichment.InstanceType)
		assert.Equal(t, ServiceNameRDS, enrichment.ResourceServiceName)
		assert.Equal(t, "db", enrichment.ResourceType)
		assert.Equal(t, "my-db-instance", enrichment.Labels["aws_dim_dbinstanceidentifier"])
	})

	t.Run("no dimensions uses alarm name", func(t *testing.T) {
		info := CloudWatchAlarmInfo{
			AlarmName:       "math-expression-alarm",
			MetricName:      "",
			MetricNamespace: "AWS/EC2",
			Region:          "eu-west-1",
			AccountNumber:   "123456789012",
			AlarmArn:        "arn:aws:cloudwatch:eu-west-1:123456789012:alarm:math-expression-alarm",
			StateValue:      "ALARM",
			Dimensions:      nil,
		}

		enrichment := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)

		assert.Equal(t, "math-expression-alarm", enrichment.ResourceId)
		assert.Equal(t, "AlarmName", enrichment.InstanceType)
	})

	t.Run("multiple dimensions picks namespace target", func(t *testing.T) {
		info := CloudWatchAlarmInfo{
			AlarmName:       "ec2-cpu-alarm",
			MetricName:      "CPUUtilization",
			MetricNamespace: "AWS/EC2",
			Region:          "us-east-1",
			AccountNumber:   "123456789012",
			AlarmArn:        "arn:aws:cloudwatch:us-east-1:123456789012:alarm:ec2-cpu-alarm",
			StateValue:      "ALARM",
			Dimensions: []AlarmDimension{
				{Name: "AutoScalingGroupName", Value: "my-asg"},
				{Name: "InstanceId", Value: "i-0abc123def"},
			},
		}

		enrichment := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)

		assert.Equal(t, "i-0abc123def", enrichment.ResourceId, "should pick InstanceId (namespace target), not AutoScalingGroupName")
		assert.Equal(t, "InstanceId", enrichment.InstanceType)
		assert.Equal(t, ServiceNameEc2, enrichment.ResourceServiceName)
	})

	t.Run("unknown namespace with non-generic dimension uses that dimension", func(t *testing.T) {
		info := CloudWatchAlarmInfo{
			AlarmName:       "custom-alarm",
			MetricName:      "MyMetric",
			MetricNamespace: "MyApp/Backend",
			Region:          "us-west-2",
			AccountNumber:   "123456789012",
			AlarmArn:        "arn:aws:cloudwatch:us-west-2:123456789012:alarm:custom-alarm",
			StateValue:      "ALARM",
			Dimensions: []AlarmDimension{
				{Name: "Environment", Value: "production"},
			},
		}

		enrichment := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)

		assert.Equal(t, "production", enrichment.ResourceId, "should use the non-generic dimension")
		assert.Equal(t, "Environment", enrichment.InstanceType)
		assert.Equal(t, "CloudWatch", enrichment.ResourceServiceName)
	})
}

func TestExtractCloudWatchAlarmInfoFromEB(t *testing.T) {
	t.Run("extracts alarm info from EventBridge event", func(t *testing.T) {
		detail := map[string]any{
			"alarmName": "rds-deadlock-log-alert",
			"alarmArn":  "arn:aws:cloudwatch:us-east-1:740395098545:alarm:rds-deadlock-log-alert",
			"state": map[string]any{
				"value":  "ALARM",
				"reason": "Threshold crossed",
			},
			"previousState": map[string]any{
				"value": "OK",
			},
			"configuration": map[string]any{
				"threshold": 0,
				"metrics": []any{
					map[string]any{
						"metricStat": map[string]any{
							"stat": "Sum",
							"metric": map[string]any{
								"name":      "rds-deadlock-metric",
								"namespace": "rds",
								"dimensions": map[string]any{
									"@aws.account": "740395098545",
									"@aws.region":  "us-east-1",
								},
							},
						},
					},
				},
			},
		}
		detailBytes, _ := json.Marshal(detail)

		ebEvent := EventBridgeEvent{
			ID:         "test-event-id",
			DetailType: "CloudWatch Alarm State Change",
			Source:     "aws.cloudwatch",
			Account:    "740395098545",
			Region:     "us-east-1",
			Resources:  []string{"arn:aws:cloudwatch:us-east-1:740395098545:alarm:rds-deadlock-log-alert"},
			Detail:     detailBytes,
		}

		info, ok := extractCloudWatchAlarmInfoFromEB(ebEvent)
		assert.True(t, ok)
		assert.Equal(t, "rds-deadlock-log-alert", info.AlarmName)
		assert.Equal(t, "rds-deadlock-metric", info.MetricName)
		assert.Equal(t, "rds", info.MetricNamespace)
		assert.Equal(t, "Sum", info.Statistic)
		assert.Equal(t, "740395098545", info.AccountNumber)
		assert.Equal(t, "us-east-1", info.Region)
		assert.Len(t, info.Dimensions, 2)

		// Now enrich it — should produce the same result as polling path
		enrichment := EnrichCloudWatchAlarm(context.Background(), info, false, nil, nil)
		assert.Equal(t, "rds-deadlock-log-alert", enrichment.ResourceId, "should fall back to alarm name")
		assert.Equal(t, "CloudWatch", enrichment.ResourceServiceName)
	})

	t.Run("extracts RDS alarm from EventBridge", func(t *testing.T) {
		detail := map[string]any{
			"alarmName": "rds-cpu-high",
			"state":     map[string]any{"value": "ALARM"},
			"configuration": map[string]any{
				"threshold": 80,
				"metrics": []any{
					map[string]any{
						"metricStat": map[string]any{
							"stat": "Average",
							"metric": map[string]any{
								"name":      "CPUUtilization",
								"namespace": "AWS/RDS",
								"dimensions": map[string]any{
									"DBInstanceIdentifier": "prod-db",
								},
							},
						},
					},
				},
			},
		}
		detailBytes, _ := json.Marshal(detail)

		ebEvent := EventBridgeEvent{
			ID:         "test-rds-event",
			DetailType: "CloudWatch Alarm State Change",
			Account:    "123456789012",
			Region:     "us-east-1",
			Resources:  []string{"arn:aws:cloudwatch:us-east-1:123456789012:alarm:rds-cpu-high"},
			Detail:     detailBytes,
		}

		info, ok := extractCloudWatchAlarmInfoFromEB(ebEvent)
		assert.True(t, ok)

		enrichment := EnrichCloudWatchAlarm(context.Background(), info, false, nil, nil)
		assert.Equal(t, "prod-db", enrichment.ResourceId)
		assert.Equal(t, "DBInstanceIdentifier", enrichment.InstanceType)
		assert.Equal(t, ServiceNameRDS, enrichment.ResourceServiceName)
		assert.Equal(t, "db", enrichment.ResourceType)
	})
}

func TestBuildAlarmRawMap(t *testing.T) {
	r, err := getAwsCloudwatchAlarms(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789",
	}, AlarmsFilter{})
	assert.NoError(t, err)
	assert.NotNil(t, r)

	t.Logf("Checking Raw map structure for %d events", len(r.Items))

	for i, event := range r.Items {
		assert.NotNil(t, event.Raw, "event %d Raw should not be nil", i)

		// Verify expected keys from buildAlarmRawMap
		assert.Contains(t, event.Raw, "stateValue", "event %d Raw missing 'stateValue'", i)
		assert.Contains(t, event.Raw, "comparisonOperator", "event %d Raw missing 'comparisonOperator'", i)

		// alarmName and alarmArn should be present for real alarms
		if _, ok := event.Raw["alarmName"]; ok {
			assert.IsType(t, "", event.Raw["alarmName"])
		}

		// Verify Raw is JSON-serializable
		rawJSON, err := json.Marshal(event.Raw)
		assert.NoError(t, err, "event %d Raw should be JSON-serializable", i)
		t.Logf("Event %d (%s) Raw size: %d bytes", i, event.EventName, len(rawJSON))

		if i >= 4 {
			break
		}
	}
}
