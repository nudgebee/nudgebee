package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIncidentProject = "example-project"

func testIncident(name string, state AlertState, openTime, closeTime string, log bool) *Alert {
	alert := &Alert{
		Name:      "projects/" + testIncidentProject + "/alerts/" + name,
		State:     state,
		OpenTime:  openTime,
		CloseTime: closeTime,
		Resource: &MonitoredResource{
			Type:   "cloudsql_database",
			Labels: map[string]string{"project_id": testIncidentProject, "database_id": testIncidentProject + ":db-1"},
		},
		Policy: &PolicySnapshot{Name: "projects/" + testIncidentProject + "/alertPolicies/111", DisplayName: "Disk high"},
	}
	if log {
		alert.Log = &LogMetadata{}
	} else {
		alert.Metric = &Metric{Type: "cloudsql.googleapis.com/database/disk/utilization"}
	}
	return alert
}

func convertIncidents(alerts []*Alert) []providers.Event {
	account := &providers.Account{AccountNumber: testIncidentProject}
	events := make([]providers.Event, 0, len(alerts))
	for _, alert := range alerts {
		events = append(events, convertAlertToEvent(nil, nil, alert, account))
	}
	return events
}

func TestIncidentKey(t *testing.T) {
	for _, tc := range []struct {
		fingerprint string
		ok          bool
	}{
		{"projects/p/alertPolicies/1:gce_instance:vm-1", true},
		{"projects/p/alertPolicies/1:gke_cluster", true},
		{"projects/p/alertPolicies/1", true},
		{"projects/p/alerts/0.abc", false},
		{"projects//alertPolicies/1", false},
		{"projects/p/alertPolicies/", false},
		{"arn:aws:cloudwatch:us-east-1:123456789012:alarm:x", false},
	} {
		key, ok := incidentKey(tc.fingerprint)
		assert.Equal(t, tc.ok, ok, tc.fingerprint)
		if ok {
			assert.Equal(t, tc.fingerprint, key)
		}
	}
}

func TestOpenIncidentSnapshot(t *testing.T) {
	listedAt := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	alerts := []*Alert{
		// Two time series of one policy on one resource, both open.
		testIncident("a", AlertStateOpen, "2026-10-29T10:00:00Z", "", false),
		testIncident("b", AlertStateOpen, "2026-10-29T09:00:00Z", "", false),
		// A closed one of the same fingerprint.
		testIncident("c", AlertStateClosed, "2026-10-29T08:00:00Z", "2026-10-29T08:30:00Z", false),
		// A log-match incident: one log entry, not a condition.
		testIncident("d", AlertStateOpen, "2026-10-29T11:00:00Z", "", true),
	}
	alerts[3].Policy.Name = "projects/" + testIncidentProject + "/alertPolicies/222"
	events := convertIncidents(alerts)

	snapshot := openIncidentSnapshot(testIncidentProject, listedAt, alerts, events)

	assert.Equal(t, testIncidentProject, events[0].OngoingScope)
	assert.Equal(t, testIncidentProject, events[1].OngoingScope)
	assert.Empty(t, events[2].OngoingScope, "a closed incident is not ongoing")
	assert.Empty(t, events[3].OngoingScope, "a log-match incident is stored as a moment")

	require.NotNil(t, snapshot)
	assert.Equal(t, []string{gcpIncidentSource}, snapshot.Sources)
	assert.Equal(t, []string{testIncidentProject}, snapshot.Scopes)
	assert.Equal(t, listedAt, snapshot.ListedAt)
	// The earliest open incident of the fingerprint is its start, so the later
	// one cannot end the earlier one while both are open.
	assert.Equal(t, map[string]time.Time{events[0].EventId: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}, snapshot.Since)

	scope, key, ok := snapshot.Identify(events[0].EventId)
	assert.True(t, ok)
	assert.Equal(t, testIncidentProject, scope)
	assert.Equal(t, events[0].EventId, key)
	_, _, ok = snapshot.Identify(alerts[0].Name)
	assert.False(t, ok, "a fingerprint without a policy cannot be placed")
}

func TestOpenIncidentSnapshot_NothingOpen(t *testing.T) {
	alerts := []*Alert{testIncident("c", AlertStateClosed, "2026-10-29T08:00:00Z", "2026-10-29T08:30:00Z", false)}
	snapshot := openIncidentSnapshot(testIncidentProject, time.Now(), alerts, convertIncidents(alerts))
	// The project was listed and nothing is open: every stored firing of it ends.
	assert.Equal(t, []string{testIncidentProject}, snapshot.Scopes)
	assert.Empty(t, snapshot.Since)
}

func TestUnlistedIncidentSnapshot(t *testing.T) {
	snapshot := unlistedIncidentSnapshot(time.Now())
	assert.Equal(t, []string{gcpIncidentSource}, snapshot.Sources, "keeps the batch-wide close off GCP firings")
	assert.Empty(t, snapshot.Scopes, "ends nothing")
}

func TestConvertAlertToEvent_ClosedIsDatedByClose(t *testing.T) {
	events := convertIncidents([]*Alert{
		testIncident("c", AlertStateClosed, "2026-10-29T08:00:00Z", "2026-10-29T08:30:00Z", false),
		testIncident("a", AlertStateOpen, "2026-10-29T10:00:00Z", "", false),
	})
	assert.Equal(t, providers.EventStatusResolved, events[0].EventStatus)
	assert.Equal(t, time.Date(2026, 10, 29, 8, 30, 0, 0, time.UTC), events[0].Date)
	assert.Equal(t, "projects/"+testIncidentProject+"/alerts/c", events[0].FindingId)
	assert.Equal(t, providers.EventStatusFiring, events[1].EventStatus)
	assert.Equal(t, time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), events[1].Date)
}
