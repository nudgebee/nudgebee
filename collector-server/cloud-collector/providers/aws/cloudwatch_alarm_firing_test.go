package aws

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlarmIdentity(t *testing.T) {
	cases := []struct {
		name      string
		arn       string
		wantScope string
		wantKey   string
		wantOK    bool
	}{
		{"plain", "arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high",
			"us-east-1/123456789012", "us-east-1/123456789012/orders-cpu-high", true},
		{"name keeps its colons and slashes", "arn:aws:cloudwatch:us-east-1:123456789012:alarm:team/orders: cpu:high",
			"us-east-1/123456789012", "us-east-1/123456789012/team/orders: cpu:high", true},
		{"another partition is the same alarm", "arn:aws-us-gov:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high",
			"us-east-1/123456789012", "us-east-1/123456789012/orders-cpu-high", true},
		{"not an alarm", "arn:aws:ecs:us-east-1:123456789012:task/prod/abc", "", "", false},
		{"not cloudwatch", "arn:aws:logs:us-east-1:123456789012:alarm:x", "", "", false},
		{"no name", "arn:aws:cloudwatch:us-east-1:123456789012:alarm:", "", "", false},
		{"no region", "arn:aws:cloudwatch::123456789012:alarm:x", "", "", false},
		{"not an arn", "evt-1234", "", "", false},
		{"empty", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, key, ok := AlarmIdentity(tc.arn)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantScope, scope)
			assert.Equal(t, tc.wantKey, key)
		})
	}
}

func alarmArn(name string) *string {
	return aws.String("arn:aws:cloudwatch:us-east-1:123456789012:alarm:" + name)
}

// The listing of what is in ALARM must record every alarm it sees, including
// the ones no event is built for: a pushed firing of a composite alarm is ended
// by this listing no longer showing it.
func TestListCloudwatchAlarmEvents_RecordsEveryAlarmInAlarm(t *testing.T) {
	since := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	client := &fakeDescribeAlarms{
		errAt: -1,
		pages: []*cloudwatch.DescribeAlarmsOutput{
			{
				MetricAlarms: []cwtypes.MetricAlarm{
					{AlarmName: aws.String("orders-cpu-high"), AlarmArn: alarmArn("orders-cpu-high"),
						Namespace: aws.String("AWS/EC2"), MetricName: aws.String("CPUUtilization"),
						StateValue: cwtypes.StateValueAlarm, StateTransitionedTimestamp: &since},
					// Metric math: no namespace of its own; it gets an event too.
					{AlarmName: aws.String("orders-error-ratio"), AlarmArn: alarmArn("orders-error-ratio"),
						StateValue: cwtypes.StateValueAlarm, StateTransitionedTimestamp: &since},
				},
				NextToken: aws.String("p2"),
			},
			{
				MetricAlarms: []cwtypes.MetricAlarm{
					// No transition time: an event as before, but not an open condition.
					{AlarmName: aws.String("legacy-disk"), AlarmArn: alarmArn("legacy-disk"),
						Namespace: aws.String("AWS/EC2"), MetricName: aws.String("DiskUsage"),
						StateValue: cwtypes.StateValueAlarm},
				},
				CompositeAlarms: []cwtypes.CompositeAlarm{
					{AlarmName: aws.String("orders-degraded"), AlarmArn: alarmArn("orders-degraded"),
						StateValue: cwtypes.StateValueAlarm, StateTransitionedTimestamp: &since},
				},
			},
		},
	}

	resp, err := listCloudwatchAlarmEvents(providers.NewCloudProviderContext(context.Background()), client, nil,
		providers.Account{AccountNumber: "123456789012"}, AlarmsFilter{Status: AlarmStatusAlarm, Region: "us-east-1"}, "us-east-1")
	require.NoError(t, err)

	assert.ElementsMatch(t, []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}, client.alarmTypes)

	require.NotNil(t, resp.Ongoing)
	assert.Equal(t, []string{"us-east-1/123456789012"}, resp.Ongoing.Scopes)
	assert.Equal(t, map[string]time.Time{
		"us-east-1/123456789012/orders-cpu-high":    since,
		"us-east-1/123456789012/orders-error-ratio": since,
		"us-east-1/123456789012/orders-degraded":    since,
		"us-east-1/123456789012/legacy-disk":        {},
	}, resp.Ongoing.Since)

	scopes := map[string]string{}
	for _, ev := range resp.Items {
		scopes[ev.EventName] = ev.OngoingScope
	}
	assert.Equal(t, map[string]string{
		"orders-cpu-high":    "us-east-1/123456789012",
		"orders-error-ratio": "us-east-1/123456789012",
		"legacy-disk":        "",
	}, scopes, "an event per metric alarm, none for a composite; open only with a transition time")
}

// A region that could not be read to the end must not look like a region with
// no alarms: it returns no snapshot, so it never joins the scopes.
func TestListCloudwatchAlarmEvents_FailedPageGivesNoSnapshot(t *testing.T) {
	since := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	client := &fakeDescribeAlarms{
		errAt: 1,
		pages: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{AlarmName: aws.String("orders-cpu-high"), AlarmArn: alarmArn("orders-cpu-high"),
				Namespace: aws.String("AWS/EC2"), StateValue: cwtypes.StateValueAlarm, StateTransitionedTimestamp: &since}},
			NextToken: aws.String("p2"),
		}},
	}

	resp, err := listCloudwatchAlarmEvents(providers.NewCloudProviderContext(context.Background()), client, nil,
		providers.Account{AccountNumber: "123456789012"}, AlarmsFilter{Status: AlarmStatusAlarm, Region: "us-east-1"}, "us-east-1")
	require.Error(t, err)
	assert.Nil(t, resp.Ongoing)
	assert.Empty(t, resp.Items)

	snapshot := newAlarmSnapshot(time.Now())
	mergeAlarmRegion(snapshot, resp.Ongoing)
	assert.Empty(t, snapshot.Scopes)
}

// Any other listing (alarms in every state, for resource annotations) is not a
// statement about what is firing.
func TestListCloudwatchAlarmEvents_OtherListingsCarryNoSnapshot(t *testing.T) {
	since := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	client := &fakeDescribeAlarms{
		errAt: -1,
		pages: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{AlarmName: aws.String("orders-cpu-high"), AlarmArn: alarmArn("orders-cpu-high"),
				Namespace: aws.String("AWS/EC2"), StateValue: cwtypes.StateValueOk, StateTransitionedTimestamp: &since}},
		}},
	}

	resp, err := listCloudwatchAlarmEvents(providers.NewCloudProviderContext(context.Background()), client, nil,
		providers.Account{AccountNumber: "123456789012"}, AlarmsFilter{Region: "us-east-1"}, "us-east-1")
	require.NoError(t, err)
	assert.Nil(t, resp.Ongoing)
	assert.Empty(t, client.alarmTypes)
	require.Len(t, resp.Items, 1)
	assert.Empty(t, resp.Items[0].OngoingScope)
}

func TestMergeAlarmRegion(t *testing.T) {
	listedAt := time.Date(2026, 9, 28, 10, 5, 0, 0, time.UTC)
	since := listedAt.Add(-time.Hour)
	snapshot := newAlarmSnapshot(listedAt)

	mergeAlarmRegion(snapshot, &providers.OngoingSnapshot{
		Scopes: []string{"us-east-1/123456789012"},
		Since:  map[string]time.Time{"us-east-1/123456789012/orders-cpu-high": since},
	})
	mergeAlarmRegion(snapshot, nil) // a region that failed to list
	mergeAlarmRegion(snapshot, &providers.OngoingSnapshot{Scopes: []string{"eu-west-1/123456789012"}, Since: map[string]time.Time{}})

	assert.Equal(t, []string{"us-east-1/123456789012", "eu-west-1/123456789012"}, snapshot.Scopes)
	assert.Equal(t, map[string]time.Time{"us-east-1/123456789012/orders-cpu-high": since}, snapshot.Since)
	assert.Equal(t, listedAt, snapshot.ListedAt)
	assert.Equal(t, alarmFiringSources, snapshot.Sources)

	scope, key, ok := snapshot.Identify("arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high")
	assert.True(t, ok)
	assert.Equal(t, "us-east-1/123456789012", scope)
	assert.Contains(t, snapshot.Since, key)
}

// A pushed alarm is marked as an open condition only when it enters ALARM. The
// mark carries the scope a poll would list it under; whether the row is then
// stored open is decided where it is stored.
func TestPushedAlarm_MarkedOngoingOnlyInAlarm(t *testing.T) {
	seedAccountTenantForTest(t)
	rules, err := GetEventRules("")
	require.NoError(t, err)

	cases := []struct {
		name      string
		body      string
		wantScope string
	}{
		{"generic alarm entering ALARM",
			alarmSample("orders-cpu-high", "ALARM", "AWS/EC2", "CPUUtilization", `"InstanceId": "i-0abc"`),
			"us-east-1/123456789012"},
		{"service alarm entering ALARM",
			alarmSample("prod-cluster-checkout-HighCPUUtilization-Auto", "ALARM", "AWS/ECS", "CPUUtilization", `"ClusterName": "prod-cluster", "ServiceName": "checkout"`),
			"us-east-1/123456789012"},
		{"alarm back to OK",
			alarmSample("orders-cpu-high", "OK", "AWS/EC2", "CPUUtilization", `"InstanceId": "i-0abc"`),
			""},
		{"a notification, not an alarm", ec2StateSample("stopped"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
			pCtx := providers.NewCloudProviderContext(context.Background())
			ev, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, tc.body, processor, &dummyProcessedEventHandler{})
			require.NoError(t, err)
			require.NotEmpty(t, ev.EventId, "no emitting rule matched")
			assert.Equal(t, tc.wantScope, ev.OngoingScope)
		})
	}
}

// alarmPush is one "CloudWatch Alarm State Change" delivery. Empty fields are
// left out of the delivery, to stand for the shapes a real one can lack.
type alarmPush struct {
	name      string // detail.alarmName
	state     string // detail.state.value
	stateTime string // detail.state.timestamp
	sentAt    string // envelope time
	id        string // delivery id
	arn       string // resources[0]
	region    string
}

func (p alarmPush) body() string {
	if p.region == "" {
		p.region = "us-east-1"
	}
	var envelope, detail, state []string
	if p.id != "" {
		envelope = append(envelope, fmt.Sprintf(`"id": %q`, p.id))
	}
	if p.arn != "" {
		envelope = append(envelope, fmt.Sprintf(`"resources": [%q]`, p.arn))
	}
	if p.name != "" {
		detail = append(detail, fmt.Sprintf(`"alarmName": %q`, p.name))
	}
	state = append(state, fmt.Sprintf(`"value": %q`, p.state), `"reason": "Threshold Crossed"`)
	if p.stateTime != "" {
		state = append(state, fmt.Sprintf(`"timestamp": %q`, p.stateTime))
	}
	detail = append(detail,
		fmt.Sprintf(`"state": {%s}`, strings.Join(state, ", ")),
		`"previousState": {"value": "OK"}`,
		`"configuration": {"metrics": [{"id": "m1", "returnData": true,
			"metricStat": {"metric": {"namespace": "AWS/EC2", "name": "CPUUtilization", "dimensions": {"InstanceId": "i-0abc"}}, "period": 60, "stat": "Average"}}]}`)
	envelope = append(envelope,
		`"detail-type": "CloudWatch Alarm State Change"`, `"source": "aws.cloudwatch"`, `"account": "123456789012"`,
		fmt.Sprintf(`"time": %q`, p.sentAt), fmt.Sprintf(`"region": %q`, p.region),
		fmt.Sprintf(`"detail": {%s}`, strings.Join(detail, ", ")))
	return "{" + strings.Join(envelope, ", ") + "}"
}

// processAlarmPush runs a delivery through the real rule processor and returns
// the event it emits and everything it logged.
func processAlarmPush(t *testing.T, push alarmPush) (providers.Event, string) {
	t.Helper()
	rules, err := GetEventRules("")
	require.NoError(t, err)
	var logged bytes.Buffer
	pCtx := providers.NewCloudProviderContextWithLogger(context.Background(), slog.New(slog.NewTextHandler(&logged, nil)))
	processor := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
	event, _, err := processSQSMessageBodyForEventBridgeEvent(pCtx, push.body(), processor, &dummyProcessedEventHandler{})
	require.NoError(t, err)
	require.NotEmpty(t, event.EventId, "no emitting rule matched")
	return event, logged.String()
}

// pollAlarm runs the real poll builder over a listing that holds one alarm in
// ALARM since transitionedAt, and returns the event it builds.
func pollAlarm(t *testing.T, arn, region string, transitionedAt time.Time) providers.Event {
	t.Helper()
	client := &fakeDescribeAlarms{
		errAt: -1,
		pages: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName: aws.String("orders-cpu-high"), AlarmArn: aws.String(arn),
				Namespace: aws.String("AWS/EC2"), MetricName: aws.String("CPUUtilization"),
				StateValue: cwtypes.StateValueAlarm, StateTransitionedTimestamp: &transitionedAt,
			}},
		}},
	}
	resp, err := listCloudwatchAlarmEvents(providers.NewCloudProviderContext(context.Background()), client, nil,
		providers.Account{AccountNumber: "123456789012"}, AlarmsFilter{Status: AlarmStatusAlarm, Region: region}, region)
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	return resp.Items[0]
}

// One firing of an alarm must get the same per-firing id from the poll and from
// the push, or it is stored twice. Both real paths run here on the same state
// change. The cases are the ways the two have differed: the push is stamped
// with the time it was sent, which is often the second after the state change;
// a delivery can carry its own id; the push rules write a fixed partition.
func TestAlarmFiring_PollAndPushAgreeOnTheFiring(t *testing.T) {
	seedAccountTenantForTest(t)
	const arn = "arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high"
	changedAt := time.Date(2026, 9, 28, 10, 0, 4, 894_000_000, time.UTC)
	const sentAt = "2026-09-28T10:00:05Z" // the second after the state change

	cases := []struct {
		name      string
		push      alarmPush
		pollArn   string
		changedAt time.Time
	}{
		{name: "sent in the second after the state change",
			push:    alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: sentAt, arn: arn},
			pollArn: arn, changedAt: changedAt},
		{name: "the delivery carries its own id",
			push:    alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: sentAt, arn: arn, id: "6a7e8feb-b491-4cf7-a9f1-bf3703467718"},
			pollArn: arn, changedAt: changedAt},
		{name: "another partition: the rule's own fingerprint says arn:aws:",
			push: alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: sentAt, region: "us-gov-west-1",
				arn: "arn:aws-us-gov:cloudwatch:us-gov-west-1:123456789012:alarm:orders-cpu-high"},
			pollArn: "arn:aws-us-gov:cloudwatch:us-gov-west-1:123456789012:alarm:orders-cpu-high", changedAt: changedAt},
		{name: "state time without a fraction",
			push:    alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04+0000", sentAt: sentAt, arn: arn},
			pollArn: arn, changedAt: changedAt.Truncate(time.Second)},
		{name: "state time written with Z",
			push:    alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894Z", sentAt: sentAt, arn: arn},
			pollArn: arn, changedAt: changedAt},
		{name: "no alarm ARN in the event, default partition: the rule's fingerprint is the same ARN",
			push:    alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: sentAt},
			pollArn: arn, changedAt: changedAt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pushed, _ := processAlarmPush(t, tc.push)
			region := tc.push.region
			if region == "" {
				region = "us-east-1"
			}
			polled := pollAlarm(t, tc.pollArn, region, tc.changedAt)

			assert.Equal(t, polled.EventId, pushed.EventId, "one alarm, one fingerprint")
			assert.Equal(t, polled.FiringFindingID(), pushed.FiringFindingID(), "one firing, one id")
			assert.Empty(t, pushed.FindingId, "the delivery's id is not the firing's identity")
			assert.True(t, pushed.Date.Equal(tc.changedAt), "a pushed firing starts when the alarm changed state, got %s", pushed.Date)
			assert.Equal(t, polled.OngoingScope, pushed.OngoingScope)
			assert.NotEmpty(t, pushed.OngoingScope)
		})
	}
}

// A pushed OK ends the firing at the time the alarm went back to OK, not at the
// time the event was sent.
func TestAlarmFiring_PushedOKCarriesItsOwnStateTime(t *testing.T) {
	seedAccountTenantForTest(t)
	const arn = "arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high"
	pushed, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "OK", stateTime: "2026-09-28T10:07:59.912+0000", sentAt: "2026-09-28T10:08:00Z", arn: arn, id: "0d0c2e1f-1111-4222-8333-444455556666"})

	assert.Equal(t, providers.EventStatusResolved, pushed.EventStatus)
	assert.Equal(t, arn, pushed.EventId)
	assert.True(t, pushed.Date.Equal(time.Date(2026, 9, 28, 10, 7, 59, 912_000_000, time.UTC)), "got %s", pushed.Date)
	assert.Empty(t, pushed.OngoingScope)
	assert.NotContains(t, logged, "alarm identity falls back")
}

// Each way the push can fail to read the alarm's own facts keeps the value it
// used before, and says so in a warning: a silent fallback is how one firing
// came to be stored twice. None of them may use the delivery's id.
func TestAlarmFiring_PushFallbacksAreLoud(t *testing.T) {
	seedAccountTenantForTest(t)
	const arn = "arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high"
	const deliveryID = "6a7e8feb-b491-4cf7-a9f1-bf3703467718"
	sentAt := time.Date(2026, 9, 28, 10, 0, 5, 0, time.UTC)

	t.Run("nothing to fall back from: no warning", func(t *testing.T) {
		_, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: "2026-09-28T10:00:05Z", arn: arn, id: deliveryID})
		assert.NotContains(t, logged, "alarm identity falls back")
	})

	t.Run("no state time: the time the event was sent", func(t *testing.T) {
		pushed, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "ALARM", sentAt: "2026-09-28T10:00:05Z", arn: arn, id: deliveryID})
		assert.Contains(t, logged, "no readable state time in the event")
		assert.Contains(t, logged, arn)
		assert.Equal(t, arn, pushed.EventId)
		assert.True(t, pushed.Date.Equal(sentAt), "got %s", pushed.Date)
		assert.Equal(t, fmt.Sprintf("%s-%d", arn, sentAt.Unix()), pushed.FiringFindingID())
	})

	t.Run("unreadable state time: the time the event was sent", func(t *testing.T) {
		pushed, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "28/09/2026 10:00", sentAt: "2026-09-28T10:00:05Z", arn: arn, id: deliveryID})
		assert.Contains(t, logged, "no readable state time in the event")
		assert.True(t, pushed.Date.Equal(sentAt), "got %s", pushed.Date)
		assert.Empty(t, pushed.FindingId)
	})

	t.Run("no alarm ARN: the rule's fingerprint", func(t *testing.T) {
		pushed, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: "2026-09-28T10:00:05Z", id: deliveryID})
		assert.Contains(t, logged, "no alarm ARN in the event")
		assert.Contains(t, logged, "orders-cpu-high")
		assert.Equal(t, arn, pushed.EventId, "the rule builds the same ARN in the default partition")
		assert.Empty(t, pushed.FindingId)
	})

	t.Run("something that is not an alarm ARN in resources: the rule's fingerprint", func(t *testing.T) {
		pushed, logged := processAlarmPush(t, alarmPush{name: "orders-cpu-high", state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: "2026-09-28T10:00:05Z",
			arn: "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc", id: deliveryID})
		assert.Contains(t, logged, "no alarm ARN in the event")
		assert.Equal(t, arn, pushed.EventId)
	})

	t.Run("no alarm name: the rule's fingerprint and the time the event was sent, still not the delivery id", func(t *testing.T) {
		pushed, logged := processAlarmPush(t, alarmPush{state: "ALARM", stateTime: "2026-09-28T10:00:04.894+0000", sentAt: "2026-09-28T10:00:05Z", arn: arn, id: deliveryID})
		assert.Contains(t, logged, "detail unreadable or without an alarm name")
		assert.Empty(t, pushed.FindingId)
		assert.NotEqual(t, deliveryID, pushed.FiringFindingID())
		assert.True(t, pushed.Date.Equal(sentAt), "got %s", pushed.Date)
	})
}

func TestParseAlarmStateTime(t *testing.T) {
	want := time.Date(2026, 9, 28, 10, 0, 4, 894_000_000, time.UTC)
	for value, wantTime := range map[string]time.Time{
		"2026-09-28T10:00:04.894+0000":  want,
		"2026-09-28T10:00:04.894Z":      want,
		"2026-09-28T10:00:04.894+00:00": want,
		"2026-09-28T15:30:04.894+0530":  want,
		"2026-09-28T10:00:04+0000":      want.Truncate(time.Second),
		"2026-09-28T10:00:04Z":          want.Truncate(time.Second),
	} {
		got, ok := parseAlarmStateTime(value)
		assert.True(t, ok, value)
		assert.True(t, got.Equal(wantTime), "%s parsed as %s", value, got)
		assert.Equal(t, time.UTC, got.Location(), value)
	}
	for _, value := range []string{"", "28/09/2026 10:00", "1790589604", "2026-09-28"} {
		_, ok := parseAlarmStateTime(value)
		assert.False(t, ok, value)
	}
}
