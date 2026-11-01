package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var historyBase = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// stateItem builds a StateUpdate history item the way CloudWatch returns it:
// the new state lives in the HistoryData JSON, not in the summary text.
func stateItem(alarm string, minute int, oldState, newState string) cwtypes.AlarmHistoryItem {
	data := fmt.Sprintf(`{"version":"1.0","oldState":{"stateValue":%q},"newState":{"stateValue":%q,"stateReason":"reason for %s at %d"}}`,
		oldState, newState, newState, minute)
	return cwtypes.AlarmHistoryItem{
		AlarmName:       aws.String(alarm),
		AlarmType:       cwtypes.AlarmTypeMetricAlarm,
		HistoryItemType: cwtypes.HistoryItemTypeStateUpdate,
		HistorySummary:  aws.String("Alarm updated from " + oldState + " to " + newState),
		HistoryData:     aws.String(data),
		Timestamp:       aws.Time(historyBase.Add(time.Duration(minute) * time.Minute)),
	}
}

func TestClearedFirings(t *testing.T) {
	at := func(m int) time.Time { return historyBase.Add(time.Duration(m) * time.Minute) }
	tests := []struct {
		name  string
		items []cwtypes.AlarmHistoryItem
		want  []clearedFiring
	}{
		{
			name:  "entered and left ALARM inside the window",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 1, "OK", "ALARM"), stateItem("a", 4, "ALARM", "OK")},
			want:  []clearedFiring{{alarmName: "a", start: at(1), end: at(4), reason: "reason for ALARM at 1"}},
		},
		{
			name:  "still in ALARM is left to the listing",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 1, "OK", "ALARM")},
		},
		{
			name:  "left ALARM but entered before the window is left to the store",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 2, "ALARM", "OK")},
		},
		{
			name: "two flaps, items out of order, then on again",
			items: []cwtypes.AlarmHistoryItem{
				stateItem("a", 7, "ALARM", "OK"), stateItem("a", 1, "OK", "ALARM"), stateItem("a", 9, "OK", "ALARM"),
				stateItem("a", 3, "ALARM", "OK"), stateItem("a", 5, "OK", "ALARM"),
			},
			want: []clearedFiring{
				{alarmName: "a", start: at(1), end: at(3), reason: "reason for ALARM at 1"},
				{alarmName: "a", start: at(5), end: at(7), reason: "reason for ALARM at 5"},
			},
		},
		{
			name: "two changes in the same instant keep the order they happened in (history is newest first)",
			items: []cwtypes.AlarmHistoryItem{
				stateItem("a", 1, "ALARM", "OK"), stateItem("a", 1, "OK", "ALARM"),
			},
			want: []clearedFiring{{alarmName: "a", start: at(1), end: at(1), reason: "reason for ALARM at 1"}},
		},
		{
			name: "an update while in ALARM is the same firing, which began at the first",
			items: []cwtypes.AlarmHistoryItem{
				stateItem("a", 1, "OK", "ALARM"), stateItem("a", 3, "ALARM", "ALARM"), stateItem("a", 6, "ALARM", "OK"),
			},
			want: []clearedFiring{{alarmName: "a", start: at(1), end: at(6), reason: "reason for ALARM at 1"}},
		},
		{
			name:  "an update while in ALARM whose entry is before the window starts no firing",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 3, "ALARM", "ALARM"), stateItem("a", 6, "ALARM", "OK")},
		},
		{
			name:  "leaving ALARM for INSUFFICIENT_DATA ends the firing",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 1, "INSUFFICIENT_DATA", "ALARM"), stateItem("a", 2, "ALARM", "INSUFFICIENT_DATA")},
			want:  []clearedFiring{{alarmName: "a", start: at(1), end: at(2), reason: "reason for ALARM at 1"}},
		},
		{
			name:  "changes that never reach ALARM are not firings",
			items: []cwtypes.AlarmHistoryItem{stateItem("a", 1, "OK", "INSUFFICIENT_DATA"), stateItem("a", 2, "INSUFFICIENT_DATA", "OK")},
		},
		{
			name: "alarms are paired separately",
			items: []cwtypes.AlarmHistoryItem{
				stateItem("a", 1, "OK", "ALARM"), stateItem("b", 2, "OK", "ALARM"), stateItem("a", 3, "ALARM", "OK"), stateItem("b", 6, "ALARM", "OK"),
			},
			want: []clearedFiring{
				{alarmName: "a", start: at(1), end: at(3), reason: "reason for ALARM at 1"},
				{alarmName: "b", start: at(2), end: at(6), reason: "reason for ALARM at 2"},
			},
		},
		{
			name: "items without readable state data are ignored",
			items: []cwtypes.AlarmHistoryItem{
				{AlarmName: aws.String("a"), HistoryItemType: cwtypes.HistoryItemTypeStateUpdate, HistoryData: aws.String("not json"), Timestamp: aws.Time(at(1))},
				{AlarmName: aws.String("a"), HistoryItemType: cwtypes.HistoryItemTypeConfigurationUpdate, HistoryData: aws.String(`{"newState":{"stateValue":"OK"}}`), Timestamp: aws.Time(at(2))},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clearedFirings(tc.items))
		})
	}
}

func TestClearedFiringWindowStart(t *testing.T) {
	now := historyBase
	assert.Equal(t, now.Add(-clearedFiringLookback), clearedFiringWindowStart(nil, now), "no requested window")
	recent := now.Add(-10 * time.Minute)
	assert.Equal(t, now.Add(-clearedFiringLookback), clearedFiringWindowStart(&recent, now), "a recent poll still reads the minimum look-back")
	old := now.Add(-3 * time.Hour)
	assert.Equal(t, old.Add(-ongoingListingLagPad), clearedFiringWindowStart(&old, now), "after a gap, from the last poll")
	ancient := now.Add(-72 * time.Hour)
	assert.Equal(t, now.Add(-clearedFiringMaxLookback), clearedFiringWindowStart(&ancient, now), "bounded")
}

type fakeAlarmHistory struct {
	history      []*cloudwatch.DescribeAlarmHistoryOutput
	historyCalls int
	historyIn    *cloudwatch.DescribeAlarmHistoryInput
	alarms       []cwtypes.MetricAlarm
	describedFor [][]string
}

func (f *fakeAlarmHistory) DescribeAlarmHistory(_ context.Context, in *cloudwatch.DescribeAlarmHistoryInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmHistoryOutput, error) {
	f.historyIn = in
	page := f.history[f.historyCalls]
	f.historyCalls++
	return page, nil
}

func (f *fakeAlarmHistory) DescribeAlarms(_ context.Context, in *cloudwatch.DescribeAlarmsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error) {
	f.describedFor = append(f.describedFor, in.AlarmNames)
	out := &cloudwatch.DescribeAlarmsOutput{}
	for _, a := range f.alarms {
		for _, n := range in.AlarmNames {
			if aws.ToString(a.AlarmName) == n {
				out.MetricAlarms = append(out.MetricAlarms, a)
			}
		}
	}
	return out, nil
}

func cpuAlarm(name string) cwtypes.MetricAlarm {
	return cwtypes.MetricAlarm{
		AlarmName:   aws.String(name),
		AlarmArn:    aws.String("arn:aws:cloudwatch:us-east-1:111122223333:alarm:" + name),
		Namespace:   aws.String("AWS/EC2"),
		MetricName:  aws.String("CPUUtilization"),
		Statistic:   cwtypes.StatisticAverage,
		Threshold:   aws.Float64(90),
		StateValue:  cwtypes.StateValueOk,
		StateReason: aws.String("current reason, not the firing's"),
		Dimensions:  []cwtypes.Dimension{{Name: aws.String("InstanceId"), Value: aws.String("i-0abc")}},
	}
}

func TestListClearedAlarmFirings(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: "111122223333"}
	client := &fakeAlarmHistory{
		history: []*cloudwatch.DescribeAlarmHistoryOutput{
			{AlarmHistoryItems: []cwtypes.AlarmHistoryItem{stateItem("cpu", 1, "OK", "ALARM"), stateItem("cpu", 4, "ALARM", "OK")}, NextToken: aws.String("p2")},
			{AlarmHistoryItems: []cwtypes.AlarmHistoryItem{
				stateItem("cpu", 6, "OK", "ALARM"), stateItem("cpu", 8, "ALARM", "OK"),
				stateItem("deleted", 2, "OK", "ALARM"), stateItem("deleted", 3, "ALARM", "OK"),
			}},
		},
		alarms: []cwtypes.MetricAlarm{cpuAlarm("cpu")},
	}
	arn := "arn:aws:cloudwatch:us-east-1:111122223333:alarm:cpu"
	// The listing in the same batch still reports the firing that began at minute 6.
	listed := map[string]bool{fmt.Sprintf("%s-%d", arn, historyBase.Add(6*time.Minute).Unix()): true}

	since, until := historyBase, historyBase.Add(10*time.Minute)
	events, err := listClearedAlarmFirings(ctx, client, nil, account, "us-east-1", since, until, listed)
	require.NoError(t, err)

	assert.Equal(t, 2, client.historyCalls, "every history page is read")
	assert.Equal(t, cwtypes.HistoryItemTypeStateUpdate, client.historyIn.HistoryItemType)
	assert.Equal(t, [][]string{{"cpu", "deleted"}}, client.describedFor, "only alarms with cleared firings are described")

	require.Len(t, events, 1, "the listed firing is left to the listing, the deleted alarm is skipped")
	e := events[0]
	assert.Equal(t, arn, e.EventId)
	assert.Equal(t, fmt.Sprintf("%s-%d", arn, historyBase.Add(time.Minute).Unix()), e.FiringFindingID(), "same identity the poll and the push give this firing")
	assert.Equal(t, providers.EventStatusClosed, e.EventStatus)
	require.NotNil(t, e.EndsAt)
	assert.Equal(t, historyBase.Add(4*time.Minute), *e.EndsAt)
	assert.Empty(t, e.OngoingScope)
	assert.Equal(t, "true", e.Labels[RecordOnlyLabel])
	assert.Equal(t, "reason for ALARM at 1", e.Description, "the reason it fired with, not its current one")
	assert.Equal(t, "i-0abc", e.ResourceId)
	assert.Equal(t, "ALARM", e.Raw["stateValue"], "evidence shows the alarm as it fired")
	assert.Equal(t, "reason for ALARM at 1", e.Raw["stateReason"])
}
