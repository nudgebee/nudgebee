package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// processPush runs one delivery through the real processor and embedded rules.
func processPush(t *testing.T, body string) providers.Event {
	t.Helper()
	rules, err := GetEventRules("")
	require.NoError(t, err)
	processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
	ev, _, err := processSQSMessageBodyForEventBridgeEvent(providers.NewCloudProviderContext(context.Background()), body, processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	require.NotEmpty(t, ev.EventId, "no emitting rule matched")
	return ev
}

// A pushed alarm is stored as an alarm, whichever rule emits it, so the
// features that read alarms see it. Every other notification keeps the push's
// own source.
func TestPushedAlarm_StoredAsCloudWatchAlarm(t *testing.T) {
	seedAccountTenantForTest(t)
	alarms := 0
	for _, tc := range precedenceCases {
		t.Run(tc.name, func(t *testing.T) {
			ev := processPush(t, tc.body)
			if strings.Contains(tc.body, `"CloudWatch Alarm State Change"`) {
				alarms++
				assert.Equal(t, "AWS_CloudWatch_Alarm", ev.EventSource, "rule %s", tc.wantRule)
			} else {
				assert.Equal(t, "AWS_EventBridge", ev.EventSource, "rule %s", tc.wantRule)
			}
		})
	}
	assert.Equal(t, 6, alarms, "every alarm rule has a sample above")
}

// alarmPushWithReason is an alarm delivery whose state carries the reason data
// CloudWatch attaches: the threshold and period of the evaluation that fired.
func alarmPushWithReason(reasonData, dimensions string) string {
	return fmt.Sprintf(`{"id": "evt-alarm", "detail-type": "CloudWatch Alarm State Change", "source": "aws.cloudwatch", "account": "123456789012", "time": "2026-09-28T10:00:00Z", "region": "us-east-1",
		"resources": ["arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-queue-depth"],
		"detail": {
			"alarmName": "orders-queue-depth",
			"state": {"value": "ALARM", "reason": "Threshold Crossed", "reasonData": %q, "timestamp": "2026-09-28T09:59:58.123+0000"},
			"previousState": {"value": "OK"},
			"configuration": {"metrics": [{
				"id": "m1",
				"metricStat": {"metric": {"namespace": "AWS/SQS", "name": "ApproximateNumberOfMessagesVisible", "dimensions": {%s}}, "period": 60, "stat": "Average"},
				"returnData": true
			}]}
		}}`, reasonData, dimensions)
}

// The push carries the threshold and period it fired with only in the reason
// data. They are stored where the readers of an alarm look: the threshold label
// and the top of the raw event.
func TestPushedAlarm_ThresholdAndPeriodFromReasonData(t *testing.T) {
	seedAccountTenantForTest(t)
	cases := []struct {
		name       string
		reasonData string
		wantLabel  string
		wantRaw    float64
		wantPeriod int64
	}{
		{"whole number", `{"version":"1.0","period":300,"threshold":80,"recentDatapoints":[95.0]}`, "80", 80, 300},
		{"negative fraction", `{"version":"1.0","period":60,"threshold":-0.75,"recentDatapoints":[-223.0]}`, "-0.75", -0.75, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := processPush(t, alarmPushWithReason(tc.reasonData, `"QueueName": "orders"`))
			assert.Equal(t, tc.wantLabel, ev.Labels["aws_event_threshold"])
			assert.Equal(t, tc.wantRaw, ev.Raw["threshold"])
			assert.EqualValues(t, tc.wantPeriod, ev.Raw["period"])
		})
	}

	t.Run("no reason data", func(t *testing.T) {
		ev := processPush(t, alarmSample("orders-queue-depth", "ALARM", "AWS/SQS", "ApproximateNumberOfMessagesVisible", `"QueueName": "orders"`))
		assert.Empty(t, ev.Labels["aws_event_threshold"])
		assert.NotContains(t, ev.Raw, "threshold")
	})
}

// A dimension the event names twice in different letter case is stored once,
// under the capitalised name CloudWatch uses, so a metric query built from the
// stored dimensions matches the alarm's metric.
func TestPushedAlarm_CaseVariantDimensionsStoredOnce(t *testing.T) {
	seedAccountTenantForTest(t)
	ev := processPush(t, alarmPushWithReason(`{"period":300,"threshold":80}`,
		`"queueName": "orders", "QueueName": "orders", "Region": "us-east-1"`))

	var dims []AlarmDimension
	require.NoError(t, json.Unmarshal([]byte(ev.Labels["aws_event_alarm_dimensions"]), &dims))
	assert.Equal(t, []AlarmDimension{{Name: "QueueName", Value: "orders"}, {Name: "Region", Value: "us-east-1"}}, dims)
}
