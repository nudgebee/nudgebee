package account

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
	awsprovider "nudgebee/collector/cloud/providers/aws"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAlarmScope = "us-east-1/123456789012"

func testAlarmFingerprint(name string) string {
	return "arn:aws:cloudwatch:us-east-1:123456789012:alarm:" + name
}

func TestEndedFirings(t *testing.T) {
	listedAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	firedAt := listedAt.Add(-time.Hour)
	refiredAt := listedAt.Add(-4 * time.Minute)

	snapshot := func(since map[string]time.Time, scopes ...string) providers.OngoingSnapshot {
		keyed := map[string]time.Time{}
		for fingerprint, at := range since {
			_, key, ok := awsprovider.AlarmIdentity(fingerprint)
			require.True(t, ok, fingerprint)
			keyed[key] = at
		}
		return providers.OngoingSnapshot{ListedAt: listedAt, Scopes: scopes, Since: keyed, Identify: awsprovider.AlarmIdentity}
	}
	firing := func(id, fingerprint string, startsAt time.Time) openFiring {
		return openFiring{ID: id, Fingerprint: fingerprint, StartsAt: startsAt}
	}

	cases := []struct {
		name     string
		open     []openFiring
		snapshot providers.OngoingSnapshot
		want     map[string]time.Time // id -> ended at
		reason   string
	}{
		{
			name:     "still in ALARM since the same transition",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), firedAt)},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("cpu"): firedAt}, testAlarmScope),
		},
		{
			name:     "a pushed row starts on the second, the listing has the milliseconds",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), firedAt)},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("cpu"): firedAt.Add(985 * time.Millisecond)}, testAlarmScope),
		},
		{
			name:     "no longer in ALARM: ended at the poll",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), firedAt)},
			snapshot: snapshot(nil, testAlarmScope),
			want:     map[string]time.Time{"a": listedAt},
			reason:   endedNotListed,
		},
		{
			name: "fired again between two polls: the earlier firing ended by then, the current one stays",
			open: []openFiring{
				firing("old", testAlarmFingerprint("cpu"), firedAt),
				firing("current", testAlarmFingerprint("cpu"), refiredAt),
			},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("cpu"): refiredAt}, testAlarmScope),
			want:     map[string]time.Time{"old": refiredAt},
			reason:   endedFiredAgain,
		},
		{
			name:     "region not listed (it failed, or the poll does not cover it): unknown, kept",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), firedAt)},
			snapshot: snapshot(nil, "eu-west-1/123456789012"),
		},
		{
			name:     "alarm of another account forwarded onto this bus: kept",
			open:     []openFiring{firing("a", "arn:aws:cloudwatch:us-east-1:999999999999:alarm:cpu", firedAt)},
			snapshot: snapshot(nil, testAlarmScope),
		},
		{
			name:     "started just before the listing: may not be in it yet, kept",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), listedAt.Add(-time.Minute))},
			snapshot: snapshot(nil, testAlarmScope),
		},
		{
			name:     "in ALARM but the source gave no transition time: kept",
			open:     []openFiring{firing("a", testAlarmFingerprint("cpu"), firedAt)},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("cpu"): {}}, testAlarmScope),
		},
		{
			name:     "a fingerprint that is not an alarm: kept",
			open:     []openFiring{firing("a", "arn:aws:ecs:us-east-1:123456789012:task/prod/abc", firedAt)},
			snapshot: snapshot(nil, testAlarmScope),
		},
		{
			name: "the push writes arn:aws: in every partition; the listing has the real one",
			open: []openFiring{firing("a", "arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu", firedAt)},
			snapshot: snapshot(map[string]time.Time{
				"arn:aws-us-gov:cloudwatch:us-east-1:123456789012:alarm:cpu": firedAt,
			}, testAlarmScope),
		},
		{
			name:     "an alarm name with colons and slashes, still in ALARM",
			open:     []openFiring{firing("a", testAlarmFingerprint("team/orders: cpu:high"), firedAt)},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("team/orders: cpu:high"): firedAt}, testAlarmScope),
		},
		{
			name: "an alarm name with colons and slashes, cleared; its near namesake is not",
			open: []openFiring{
				firing("cleared", testAlarmFingerprint("team/orders: cpu:high"), firedAt),
				firing("on", testAlarmFingerprint("team/orders: cpu"), firedAt),
			},
			snapshot: snapshot(map[string]time.Time{testAlarmFingerprint("team/orders: cpu"): firedAt}, testAlarmScope),
			want:     map[string]time.Time{"cleared": listedAt},
			reason:   endedNotListed,
		},
		{
			name: "both sources of one alarm, and one row per region",
			open: []openFiring{
				firing("pushed", testAlarmFingerprint("cpu"), firedAt),
				firing("polled", testAlarmFingerprint("cpu"), firedAt),
				firing("other-region", "arn:aws:cloudwatch:eu-west-1:123456789012:alarm:cpu", firedAt),
			},
			snapshot: snapshot(nil, testAlarmScope),
			want:     map[string]time.Time{"pushed": listedAt, "polled": listedAt},
			reason:   endedNotListed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]time.Time{}
			for _, ended := range endedFirings(tc.open, tc.snapshot) {
				got[ended.ID] = ended.EndedAt
				assert.Equal(t, tc.reason, ended.Reason)
				assert.False(t, ended.EndedAt.Before(ended.StartsAt), "a firing cannot end before it starts")
			}
			if tc.want == nil {
				tc.want = map[string]time.Time{}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEndedFirings_NothingWithoutAnIdentifier(t *testing.T) {
	open := []openFiring{{ID: "a", Fingerprint: testAlarmFingerprint("cpu"), StartsAt: time.Now().Add(-time.Hour)}}
	assert.Empty(t, endedFirings(open, providers.OngoingSnapshot{ListedAt: time.Now(), Scopes: []string{testAlarmScope}}))
}

func TestKeepOngoingWhereListed(t *testing.T) {
	events := []providers.Event{
		{EventId: "listed", OngoingScope: testAlarmScope},
		{EventId: "not-listed", OngoingScope: "eu-west-1/123456789012"},
		{EventId: "a-moment"},
	}
	keepOngoingWhereListed(events, listedScopes(&providers.OngoingSnapshot{Scopes: []string{testAlarmScope}}))
	assert.Equal(t, testAlarmScope, events[0].OngoingScope)
	assert.Empty(t, events[1].OngoingScope)
	assert.Empty(t, events[2].OngoingScope)

	// A poll that returned no snapshot listed nothing: nothing stays open.
	events = []providers.Event{{EventId: "listed", OngoingScope: testAlarmScope}}
	keepOngoingWhereListed(events, listedScopes(nil))
	assert.Empty(t, events[0].OngoingScope)
}

func TestBatchClosableSources(t *testing.T) {
	items := []providers.Event{
		{EventSource: "AWS_CloudWatch_Alarm"},
		{EventSource: "AWS_CloudTrail"},
		{EventSource: "AWS_CloudTrail"},
	}
	// Alarm firings are ended by the snapshot, not by absence from the batch.
	assert.Equal(t, []string{"AWS_CloudTrail"}, batchClosableSources(providers.ListEventResponse{
		Items:   items,
		Ongoing: &providers.OngoingSnapshot{Sources: []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}},
	}))
	// A poll with no snapshot closes as before.
	assert.ElementsMatch(t, []string{"AWS_CloudWatch_Alarm", "AWS_CloudTrail"}, batchClosableSources(providers.ListEventResponse{Items: items}))
	assert.Empty(t, batchClosableSources(providers.ListEventResponse{}))
}

// The push path reads the listing back from the job status, which is stored as
// JSON, so the round trip is what has to hold.
func TestListedScopesFromStatus(t *testing.T) {
	listedAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	stored := func(eventsStatus map[string]any) map[string]any {
		raw, err := json.Marshal(map[string]any{"events": eventsStatus})
		require.NoError(t, err)
		var status map[string]any
		require.NoError(t, json.Unmarshal(raw, &status))
		return status
	}
	coverage := ongoingCoverageStatus(&providers.OngoingSnapshot{ListedAt: listedAt, Scopes: []string{testAlarmScope}})

	assert.Equal(t, map[string]bool{testAlarmScope: true}, listedScopesFromStatus(stored(coverage), listedAt.Add(10*time.Minute)))
	assert.Equal(t, map[string]bool{testAlarmScope: true}, listedScopesFromStatus(stored(coverage), listedAt.Add(ongoingCoverageMaxAge)))
	assert.Empty(t, listedScopesFromStatus(stored(coverage), listedAt.Add(ongoingCoverageMaxAge+time.Second)), "a listing older than the window opens nothing")

	// A poll that failed, or ran with the alarm listing off, records no listing.
	assert.Empty(t, listedScopesFromStatus(stored(map[string]any{"err": "access denied"}), listedAt))
	assert.Empty(t, listedScopesFromStatus(stored(ongoingCoverageStatus(nil)), listedAt))
	assert.Empty(t, listedScopesFromStatus(nil, listedAt))
	assert.Empty(t, listedScopesFromStatus(map[string]any{"events": "not-an-object"}, listedAt))

	// A listing in which every region failed lists no scope.
	none := ongoingCoverageStatus(&providers.OngoingSnapshot{ListedAt: listedAt})
	assert.Empty(t, listedScopesFromStatus(stored(none), listedAt))
}

func TestPrepareEventForDB_EndTime(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("11111111-1111-1111-1111-111111111111")
	account := providers.Account{AccountNumber: "123456789012", CloudProvider: "AWS"}
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		event   providers.Event
		wantEnd bool
	}{
		{"an alarm a poll can end is sent with no end time",
			providers.Event{EventId: testAlarmFingerprint("cpu"), Date: at, EventStatus: providers.EventStatusFiring, OngoingScope: testAlarmScope}, false},
		{"a notification is a moment",
			providers.Event{EventId: "evt-1", Date: at, EventStatus: providers.EventStatusFiring}, true},
		{"an event that is not firing keeps its end time",
			providers.Event{EventId: testAlarmFingerprint("cpu"), Date: at, EventStatus: providers.EventStatusClosed, OngoingScope: testAlarmScope}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eventMap, err := prepareEventForDB(ctx, tc.event, account, "22222222-2222-2222-2222-222222222222")
			require.NoError(t, err)
			_, hasEnd := eventMap["ends_at"]
			assert.Equal(t, tc.wantEnd, hasEnd)
			assert.Equal(t, at.Format(time.RFC3339), eventMap["starts_at"])
		})
	}
}

func TestPrepareEventForDB_EndedBeforeRead(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("11111111-1111-1111-1111-111111111111")
	account := providers.Account{AccountNumber: "123456789012", CloudProvider: "AWS"}
	start := time.Date(2026, 10, 13, 12, 1, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)

	eventMap, err := prepareEventForDB(ctx, providers.Event{
		EventId: testAlarmFingerprint("cpu"), Date: start, EventStatus: providers.EventStatusClosed, EndsAt: &end,
	}, account, "22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)
	assert.Equal(t, start.Format(time.RFC3339), eventMap["starts_at"])
	assert.Equal(t, end.Format(time.RFC3339), eventMap["ends_at"], "stored with the time it ended, not as a moment")
	assert.Equal(t, string(providers.EventStatusClosed), eventMap["status"])
}
