package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// gcpIncidentSource is the source a GCP Monitoring incident is stored under.
const gcpIncidentSource = "GCP_Metric_Alert"

// gcpIncidentFingerprintLike matches the fingerprints incidentKey accepts.
const gcpIncidentFingerprintLike = "projects/%/alertPolicies/%"

// incidentKey reads a stored incident fingerprint as the condition it belongs
// to: a fingerprint built from the incident's alert policy
// (projects/{project}/alertPolicies/{id}[:{resource type}[:{resource}]]) is one
// condition. A fingerprint without a policy is the incident's own name, which no
// later listing can speak for.
func incidentKey(fingerprint string) (string, bool) {
	parts := strings.SplitN(fingerprint, "/", 4)
	if len(parts) != 4 || parts[0] != "projects" || parts[1] == "" || parts[2] != "alertPolicies" || parts[3] == "" {
		return "", false
	}
	return fingerprint, true
}

// incidentStaysOpen reports whether an incident is a condition that stays open
// until its source closes it. A metric condition is; a log-match incident is one
// log entry, open only until its policy's auto-close, so it is stored as the
// moment it happened.
func incidentStaysOpen(alert *Alert) bool {
	return alert.Metric != nil && alert.Log == nil
}

// openIncidentSnapshot marks the open metric incidents of one project's listing
// as ongoing and returns what the listing saw as on. The scope is the account's
// project, which the listing covers whole: every stored fingerprint of the
// account belongs to it.
//
// A condition's start is the earliest open incident under its fingerprint. One
// policy can hold several incidents open on the same resource at once (one per
// time series), and a later one must not end an earlier one that is still open:
// a stored firing is ended here only when it started before every incident of
// its fingerprint that is open now. One that closed while another stays open is
// ended by its own close, which the listing returns.
func openIncidentSnapshot(project string, listedAt time.Time, alerts []*Alert, events []providers.Event) *providers.OngoingSnapshot {
	since := map[string]time.Time{}
	for i, alert := range alerts {
		if alert.State != AlertStateOpen || !incidentStaysOpen(alert) || parseRFC3339Time(alert.OpenTime) == nil {
			continue
		}
		key, ok := incidentKey(events[i].EventId)
		if !ok {
			continue
		}
		events[i].OngoingScope = project
		if current, seen := since[key]; !seen || events[i].Date.Before(current) {
			since[key] = events[i].Date
		}
	}
	return &providers.OngoingSnapshot{
		Sources:         []string{gcpIncidentSource},
		FingerprintLike: gcpIncidentFingerprintLike,
		ListedAt:        listedAt,
		Scopes:          []string{project},
		Since:           since,
		Identify: func(fingerprint string) (string, string, bool) {
			key, ok := incidentKey(fingerprint)
			return project, key, ok
		},
	}
}

// unlistedIncidentSnapshot is returned when the project's incidents could not be
// listed: it lists no scope, so no stored firing is ended, and it keeps the
// batch-wide close away from the open firings stored before.
func unlistedIncidentSnapshot(listedAt time.Time) *providers.OngoingSnapshot {
	return &providers.OngoingSnapshot{
		Sources:         []string{gcpIncidentSource},
		FingerprintLike: gcpIncidentFingerprintLike,
		ListedAt:        listedAt,
		Since:           map[string]time.Time{},
		Identify: func(string) (string, string, bool) {
			return "", "", false
		},
	}
}
