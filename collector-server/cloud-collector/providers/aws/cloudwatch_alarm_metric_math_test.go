package aws

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An alarm on an expression has no namespace of its own. The poll used to skip
// it, so it was never stored unless the account also pushed its state changes.
func metricStatQuery(id, namespace, metric string, dims ...cwtypes.Dimension) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{
		Id: aws.String(id),
		MetricStat: &cwtypes.MetricStat{
			Metric: &cwtypes.Metric{Namespace: aws.String(namespace), MetricName: aws.String(metric), Dimensions: dims},
			Period: aws.Int32(60),
			Stat:   aws.String("Sum"),
		},
		ReturnData: aws.Bool(false),
	}
}

func expressionQuery(id, expression string) cwtypes.MetricDataQuery {
	return cwtypes.MetricDataQuery{Id: aws.String(id), Expression: aws.String(expression), ReturnData: aws.Bool(true)}
}

func TestAlarmMetric(t *testing.T) {
	lb := cwtypes.Dimension{Name: aws.String("LoadBalancer"), Value: aws.String("app/web/abc123")}
	fn := cwtypes.Dimension{Name: aws.String("FunctionName"), Value: aws.String("orders")}

	t.Run("single-metric alarm names its metric directly", func(t *testing.T) {
		name, ns, stat, dims := alarmMetric(cwtypes.MetricAlarm{
			Namespace: aws.String("AWS/Lambda"), MetricName: aws.String("Errors"), Statistic: cwtypes.StatisticSum, Dimensions: []cwtypes.Dimension{fn},
		})
		assert.Equal(t, []string{"Errors", "AWS/Lambda", "Sum"}, []string{name, ns, stat})
		assert.Equal(t, []AlarmDimension{{Name: "FunctionName", Value: "orders"}}, dims)
	})

	t.Run("expression alarm is about the first metric of its expression", func(t *testing.T) {
		name, ns, stat, dims := alarmMetric(cwtypes.MetricAlarm{Metrics: []cwtypes.MetricDataQuery{
			expressionQuery("e1", "m1/m2*100"),
			metricStatQuery("m1", "AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", lb),
			metricStatQuery("m2", "AWS/ApplicationELB", "RequestCount", lb),
		}})
		assert.Equal(t, []string{"HTTPCode_Target_5XX_Count", "AWS/ApplicationELB", "Sum"}, []string{name, ns, stat})
		assert.Equal(t, []AlarmDimension{{Name: "LoadBalancer", Value: "app/web/abc123"}}, dims)
	})

	t.Run("expression with no metric of its own has none", func(t *testing.T) {
		name, ns, _, dims := alarmMetric(cwtypes.MetricAlarm{Metrics: []cwtypes.MetricDataQuery{expressionQuery("e1", "SEARCH('{AWS/EC2} CPUUtilization', 'Average', 300)")}})
		assert.Empty(t, name)
		assert.Empty(t, ns)
		assert.Empty(t, dims)
	})
}

func TestMetricAlarmEvent_ExpressionAlarms(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: "111122223333"}
	const arn = "arn:aws:cloudwatch:us-east-1:111122223333:alarm:orders-invocations-anomaly"

	t.Run("anomaly-detection alarm is filed under the function it watches", func(t *testing.T) {
		event := metricAlarmEvent(ctx, cwtypes.MetricAlarm{
			AlarmName: aws.String("orders-invocations-anomaly"), AlarmArn: aws.String(arn), StateValue: cwtypes.StateValueAlarm,
			StateReason:       aws.String("Thresholds Crossed"),
			ThresholdMetricId: aws.String("ad1"),
			Metrics: []cwtypes.MetricDataQuery{
				metricStatQuery("m1", "AWS/Lambda", "Invocations", cwtypes.Dimension{Name: aws.String("FunctionName"), Value: aws.String("orders")}),
				expressionQuery("ad1", "ANOMALY_DETECTION_BAND(m1, 2)"),
			},
		}, nil, account, "us-east-1", MetricFilterCache{})
		assert.Equal(t, "orders", event.ResourceId)
		assert.Equal(t, ServiceNameLambda, event.ResourceServiceName)
		assert.Equal(t, "AWS/Lambda", event.Labels["aws_event_metric_namespace"])
		assert.Equal(t, "Invocations", event.Labels["aws_event_metric_name"])
		assert.Empty(t, event.Labels["aws_event_threshold"], "a band is not a number")
	})

	t.Run("expression with no metric is filed under the alarm itself", func(t *testing.T) {
		event := metricAlarmEvent(ctx, cwtypes.MetricAlarm{
			AlarmName: aws.String("fleet-cpu"), AlarmArn: aws.String(arn), StateValue: cwtypes.StateValueAlarm,
			Metrics: []cwtypes.MetricDataQuery{expressionQuery("e1", "SEARCH('{AWS/EC2} CPUUtilization', 'Average', 300)")},
		}, nil, account, "us-east-1", MetricFilterCache{})
		assert.Equal(t, "fleet-cpu", event.ResourceId)
		assert.Equal(t, "alarm", event.ResourceType)
	})
}

// The push path reads the same alarm from the state-change notification. Both
// must file it under the same resource, or one alarm becomes two subjects.
func TestExpressionAlarm_PollAndPushAgreeOnTheResource(t *testing.T) {
	const arn = "arn:aws:cloudwatch:us-east-1:111122223333:alarm:web-5xx-rate"
	detail := map[string]any{
		"alarmName": "web-5xx-rate",
		"alarmArn":  arn,
		"state":     map[string]any{"value": "ALARM", "timestamp": "2026-10-13T10:00:04.000+0000"},
		"configuration": map[string]any{"metrics": []any{
			map[string]any{"id": "e1", "expression": "m1/m2*100", "returnData": true},
			map[string]any{"id": "m1", "returnData": false, "metricStat": map[string]any{
				"stat": "Sum", "period": 60,
				"metric": map[string]any{"namespace": "AWS/ApplicationELB", "name": "HTTPCode_Target_5XX_Count", "dimensions": map[string]any{"LoadBalancer": "app/web/abc123"}},
			}},
			map[string]any{"id": "m2", "returnData": false, "metricStat": map[string]any{
				"stat": "Sum", "period": 60,
				"metric": map[string]any{"namespace": "AWS/ApplicationELB", "name": "RequestCount", "dimensions": map[string]any{"LoadBalancer": "app/web/abc123"}},
			}},
		}},
	}
	raw, err := json.Marshal(detail)
	require.NoError(t, err)
	info, ok := extractCloudWatchAlarmInfoFromEB(EventBridgeEvent{
		DetailType: "CloudWatch Alarm State Change", Source: "aws.cloudwatch",
		Account: "111122223333", Region: "us-east-1", Resources: []string{arn}, Detail: raw,
	})
	require.True(t, ok)
	pushed := EnrichCloudWatchAlarm(context.Background(), info, false, nil, nil)

	lb := cwtypes.Dimension{Name: aws.String("LoadBalancer"), Value: aws.String("app/web/abc123")}
	polled := metricAlarmEvent(providers.NewCloudProviderContext(context.Background()), cwtypes.MetricAlarm{
		AlarmName: aws.String("web-5xx-rate"), AlarmArn: aws.String(arn), StateValue: cwtypes.StateValueAlarm,
		Metrics: []cwtypes.MetricDataQuery{
			expressionQuery("e1", "m1/m2*100"),
			metricStatQuery("m1", "AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", lb),
			metricStatQuery("m2", "AWS/ApplicationELB", "RequestCount", lb),
		},
	}, nil, providers.Account{AccountNumber: "111122223333"}, "us-east-1", MetricFilterCache{})

	assert.Equal(t, pushed.ResourceId, polled.ResourceId)
	assert.Equal(t, pushed.ResourceType, polled.ResourceType)
	assert.Equal(t, pushed.ResourceServiceName, polled.ResourceServiceName)
	assert.Equal(t, pushed.ResourceLookupIds, polled.ResourceLookupIds)
	assert.Equal(t, "app/web/abc123", polled.ResourceId)
}
