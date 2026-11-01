package aws

import (
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// RecordOnlyLabel marks an event that is stored and counted but not acted on:
// the event service runs triage on it (repeat counts, grouping, alert tuning
// see it) and sends no notification, starts no investigation and runs no
// workflow. The event service reads the same label name.
const RecordOnlyLabel = "nb_record_only"

// clearedFiringLookback is how far back alarm history is read on every poll,
// at least. The poll lists only alarms in ALARM at that moment, so a firing that
// starts and clears between two polls is seen only in the alarm's history.
//
// The window is deliberately stateless and wider than one poll interval rather
// than resuming from a stored watermark: the history is matched to stored rows
// by the firing's identity, so reading a cleared firing again on the next two
// polls costs one history page and one DescribeAlarms call per region and is
// dropped by the store's finding_id check, while a watermark would have to agree
// with each region's listing time, which the poll does not record.
const clearedFiringLookback = 30 * time.Minute

// clearedFiringMaxLookback bounds the window after a long gap between polls.
const clearedFiringMaxLookback = 24 * time.Hour

// alarmHistoryClient is what reading cleared firings needs from CloudWatch.
type alarmHistoryClient interface {
	cloudwatch.DescribeAlarmHistoryAPIClient
	cloudwatch.DescribeAlarmsAPIClient
}

// clearedFiring is one firing of an alarm that started and ended inside the
// history window.
type clearedFiring struct {
	alarmName string
	start     time.Time // when the alarm entered ALARM
	end       time.Time // when it left ALARM
	reason    string    // the state reason it entered ALARM with
}

type alarmStateChange struct {
	at       time.Time
	oldState string
	newState string
	reason   string
	order    int // position in the history as returned, newest first
}

// alarmHistoryData is the part of a StateUpdate history item's HistoryData that
// pairing reads. HistorySummary is free text and is not parsed.
type alarmHistoryData struct {
	OldState struct {
		StateValue string `json:"stateValue"`
	} `json:"oldState"`
	NewState struct {
		StateValue  string `json:"stateValue"`
		StateReason string `json:"stateReason"`
	} `json:"newState"`
}

// clearedFirings pairs each entry into ALARM with the alarm's next state change.
// An entry with no later change is still on: the listing reports it, so it is
// left out. A change out of ALARM whose entry is before the window is left out
// too: the firing was stored when it was listed, and the store ends it.
func clearedFirings(items []types.AlarmHistoryItem) []clearedFiring {
	byAlarm := map[string][]alarmStateChange{}
	for order, item := range items {
		if item.HistoryItemType != types.HistoryItemTypeStateUpdate || item.Timestamp == nil || item.AlarmName == nil {
			continue
		}
		var data alarmHistoryData
		if err := json.Unmarshal([]byte(aws.ToString(item.HistoryData)), &data); err != nil || data.NewState.StateValue == "" {
			continue
		}
		name := aws.ToString(item.AlarmName)
		byAlarm[name] = append(byAlarm[name], alarmStateChange{
			at:       *item.Timestamp,
			oldState: data.OldState.StateValue,
			newState: data.NewState.StateValue,
			reason:   data.NewState.StateReason,
			order:    order,
		})
	}

	var out []clearedFiring
	for name, changes := range byAlarm {
		// Oldest first. The history comes newest first, so of two changes with
		// the same timestamp the one returned later happened earlier.
		sort.Slice(changes, func(i, j int) bool {
			if !changes[i].at.Equal(changes[j].at) {
				return changes[i].at.Before(changes[j].at)
			}
			return changes[i].order > changes[j].order
		})
		// A firing runs from the change that entered ALARM to the first change
		// that left it. An update while already in ALARM is the same firing and
		// never starts one: a firing's identity is when it began, which is what
		// the listing reports too, and one that began before the window was
		// stored when it was listed.
		alarm := string(types.StateValueAlarm)
		var entered *alarmStateChange
		for i := range changes {
			inAlarm := changes[i].newState == alarm
			switch {
			case inAlarm && entered == nil && changes[i].oldState != alarm:
				entered = &changes[i]
			case !inAlarm && entered != nil:
				out = append(out, clearedFiring{alarmName: name, start: entered.at, end: changes[i].at, reason: entered.reason})
				entered = nil
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}

// clearedFiringWindowStart is where the history read starts: a little before
// the window the poll was asked for, and never less than clearedFiringLookback.
func clearedFiringWindowStart(requested *time.Time, now time.Time) time.Time {
	start := now.Add(-clearedFiringLookback)
	if requested != nil && !requested.IsZero() {
		if r := requested.Add(-ongoingListingLagPad); r.Before(start) {
			start = r
		}
	}
	if floor := now.Add(-clearedFiringMaxLookback); start.Before(floor) {
		start = floor
	}
	return start
}

// ongoingListingLagPad covers history items that become visible after the poll
// that should have read them.
const ongoingListingLagPad = 2 * time.Minute

// listClearedAlarmFirings returns an event for each firing in one region that
// started and cleared inside [since, until]. Each is stored closed, with the
// time it ended, under the same identity the poll and the push give that firing,
// and marked record-only. A firing the listing in this batch reports as still
// on is left to the listing.
func listClearedAlarmFirings(ctx providers.CloudProviderContext, client alarmHistoryClient, cfg *aws.Config, account providers.Account, region string, since, until time.Time, listed map[string]bool) ([]providers.Event, error) {
	var items []types.AlarmHistoryItem
	pages := cloudwatch.NewDescribeAlarmHistoryPaginator(client, &cloudwatch.DescribeAlarmHistoryInput{
		HistoryItemType: types.HistoryItemTypeStateUpdate,
		AlarmTypes:      []types.AlarmType{types.AlarmTypeMetricAlarm},
		StartDate:       aws.Time(since),
		EndDate:         aws.Time(until),
		MaxRecords:      aws.Int32(100),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx.GetContext())
		if err != nil {
			return nil, err
		}
		items = append(items, page.AlarmHistoryItems...)
	}

	firings := clearedFirings(items)
	if len(firings) == 0 {
		return nil, nil
	}

	names := map[string]bool{}
	for _, f := range firings {
		names[f.alarmName] = true
	}
	alarms, err := describeMetricAlarmsByName(ctx, client, keysOf(names))
	if err != nil {
		return nil, err
	}

	mfCache := MetricFilterCache{}
	var events []providers.Event
	gone := 0
	for _, f := range firings {
		alarm, ok := alarms[f.alarmName]
		if !ok {
			gone++
			continue
		}
		event := metricAlarmEvent(ctx, alarm, cfg, account, region, mfCache)
		setAlarmFiring(&event, aws.ToString(alarm.AlarmArn), f.start)
		if listed[event.FiringFindingID()] {
			continue
		}
		end := f.end
		event.EndsAt = &end
		event.EventStatus = providers.EventStatusClosed
		// The alarm is described as it is now; the event is about how it was
		// when it fired.
		if f.reason != "" {
			event.Description = f.reason
			event.Title = event.ResourceServiceName + "::" + event.ResourceId + "::" + f.reason
			event.Raw["stateReason"] = f.reason
		}
		event.Raw["stateValue"] = string(types.StateValueAlarm)
		event.Raw["stateTransitionedTimestamp"] = f.start.Format(time.RFC3339)
		delete(event.Raw, "stateReasonData")
		delete(event.Raw, "stateUpdatedTimestamp")
		labels := make(map[string]string, len(event.Labels)+1)
		for k, v := range event.Labels {
			labels[k] = v
		}
		labels[RecordOnlyLabel] = "true"
		event.Labels = labels
		events = append(events, event)
	}
	if gone > 0 {
		ctx.GetLogger().Warn("cloudwatch alarm history: firings of alarms that no longer exist were skipped",
			"count", gone, "region", region, "accountNumber", account.AccountNumber)
	}
	return events, nil
}

// describeMetricAlarmsByName returns the current definition of each named
// metric alarm. Names of alarms deleted since are absent from the result.
func describeMetricAlarmsByName(ctx providers.CloudProviderContext, client cloudwatch.DescribeAlarmsAPIClient, names []string) (map[string]types.MetricAlarm, error) {
	out := map[string]types.MetricAlarm{}
	const perCall = 100
	for i := 0; i < len(names); i += perCall {
		batch := names[i:min(i+perCall, len(names))]
		pages := cloudwatch.NewDescribeAlarmsPaginator(client, &cloudwatch.DescribeAlarmsInput{
			AlarmNames: batch,
			AlarmTypes: []types.AlarmType{types.AlarmTypeMetricAlarm},
			MaxRecords: aws.Int32(100),
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx.GetContext())
			if err != nil {
				return nil, err
			}
			for _, alarm := range page.MetricAlarms {
				out[aws.ToString(alarm.AlarmName)] = alarm
			}
		}
	}
	return out, nil
}

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
