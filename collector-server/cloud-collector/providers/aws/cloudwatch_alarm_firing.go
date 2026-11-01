package aws

import (
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// alarmFiringSources are the sources a CloudWatch alarm firing is stored under:
// the poll's and the push's.
var alarmFiringSources = []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}

// alarmFingerprintLike matches the fingerprints AlarmIdentity accepts.
const alarmFingerprintLike = "arn:%:cloudwatch:%:alarm:%"

// alarmScope names what one alarm listing covers: one region of one account.
func alarmScope(region, accountNumber string) string {
	return region + "/" + accountNumber
}

// AlarmIdentity splits a CloudWatch alarm ARN into the scope a poll lists it
// under and the alarm's identity inside that scope.
//
// Everything that compares a stored firing with a listing goes through here, on
// both sides, so the two cannot disagree on how an ARN is read:
//   - The partition is left out. The push rules build their fingerprint with a
//     fixed "arn:aws:" while the poll uses the ARN the API returns, so in any
//     other partition the two strings differ for the same alarm.
//   - The name is everything after "alarm:". Alarm names may contain ":" and
//     "/", so the split stops at the seventh field.
func AlarmIdentity(arn string) (scope, key string, ok bool) {
	parts := strings.SplitN(arn, ":", 7)
	if len(parts) != 7 || parts[0] != "arn" || parts[2] != "cloudwatch" || parts[5] != "alarm" {
		return "", "", false
	}
	region, account, name := parts[3], parts[4], parts[6]
	if region == "" || account == "" || name == "" {
		return "", "", false
	}
	scope = alarmScope(region, account)
	return scope, scope + "/" + name, true
}

// setAlarmFiring gives an event the identity of one firing of an alarm: the
// alarm's ARN and the time it changed state.
//
// One firing reaches the collector twice, by the poll and by a push, and the
// store keeps the first and drops the second only when both print the same
// per-firing id (providers.Event.FiringFindingID). The two paths used to derive
// that id from different inputs and drifted apart more than once, so both now
// set it here, from the same two facts, and neither sets FindingId: a pushed
// delivery's own id is not the firing's identity.
func setAlarmFiring(event *providers.Event, alarmArn string, stateChangedAt time.Time) {
	event.EventId = alarmArn
	event.Date = stateChangedAt
	event.FindingId = ""
}

// alarmStateTimeLayouts are the forms the time of a state change arrives in: a
// pushed event writes the offset as "+0000", RFC 3339 writes "Z" or "+00:00".
// When parsing, Go accepts a fractional second after the seconds whether or not
// the layout names one.
var alarmStateTimeLayouts = []string{"2006-01-02T15:04:05-0700", time.RFC3339}

// parseAlarmStateTime reads the time an alarm changed state from a pushed event,
// in UTC whatever offset it was written with: the time is also the event's
// stored start.
func parseAlarmStateTime(value string) (time.Time, bool) {
	for _, layout := range alarmStateTimeLayouts {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// recordAlarmFiring notes that an alarm is in ALARM and since when. A missing
// transition time is recorded as zero: the alarm is on, but which of its stored
// firings is the current one cannot be told.
func recordAlarmFiring(firing map[string]time.Time, arn *string, since *time.Time) {
	_, key, ok := AlarmIdentity(aws.ToString(arn))
	if !ok {
		return
	}
	if since == nil {
		firing[key] = time.Time{}
		return
	}
	firing[key] = *since
}

// newAlarmSnapshot starts the snapshot of one poll over an account's regions.
// listedAt must be taken before the first region is listed.
func newAlarmSnapshot(listedAt time.Time) *providers.OngoingSnapshot {
	return &providers.OngoingSnapshot{
		Sources:         alarmFiringSources,
		FingerprintLike: alarmFingerprintLike,
		ListedAt:        listedAt,
		Since:           map[string]time.Time{},
		Identify:        AlarmIdentity,
	}
}

// mergeAlarmRegion adds one region's listing to the snapshot. region is nil for
// a region whose listing failed, which leaves that region out of the scopes.
func mergeAlarmRegion(snapshot, region *providers.OngoingSnapshot) {
	if region == nil {
		return
	}
	snapshot.Scopes = append(snapshot.Scopes, region.Scopes...)
	for key, since := range region.Since {
		snapshot.Since[key] = since
	}
}
