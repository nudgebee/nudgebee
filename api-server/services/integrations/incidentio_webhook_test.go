package integrations

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
)

// process runs the integration exactly as core.ProcessEventWebook does. The
// security context is unused by this handler, so a nil context keeps the tests
// free of database wiring.
func processIncidentIO(t *testing.T, body string) ([]core.EventIncomingWebhook, error) {
	t.Helper()
	return IncidentIOWebhook{}.ProcessEventWebook(nil, nil, "acct-1", body)
}

// incidentIOIncidentBody renders the incident object shipped in incident.io's
// published OpenAPI example, with the status and severity parameterised.
func incidentIOIncidentBody(statusName, statusCategory, severityName string) string {
	return fmt.Sprintf(`{
    "id": "01FDAG4SAP5TYPT98WGR2N7W91",
    "reference": "INC-123",
    "name": "Our database is sad",
    "summary": "Our database is really really sad, and we don't know why yet.",
    "permalink": "https://app.incident.io/incidents/123",
    "visibility": "public",
    "mode": "standard",
    "created_at": "2026-08-17T13:28:57.801578Z",
    "updated_at": "2026-08-17T13:30:00.000000Z",
    "most_recent_update_message": "We're working on a fix",
    "slack_channel_url": "https://slack.com/app_redirect?team=T1234&channel=C5678",
    "slack_channel_name": "inc-165-green-parrot",
    "severity": {"id": "01FCNDV6P870EA6S7TK1DSYDG0", "name": %q, "rank": 1},
    "incident_status": {"id": "01FCNDV6P870EA6S7TK1DSYD5H", "name": %q, "category": %q, "rank": 4},
    "incident_type": {"id": "01FCNDV6P870EA6S7TK1DSYD5H", "name": "Production Outage"},
    "custom_field_entries": [
      {
        "custom_field": {"id": "01FCNDV6P870EA6S7TK1DSYDG0", "name": "Affected Team", "field_type": "single_select"},
        "values": [{"value_option": {"id": "01FCNDV6P870EA6S7TK1DSYDG0", "value": "Product"}}]
      },
      {
        "custom_field": {"id": "01FCNDV6P870EA6S7TK1DSYDG1", "name": "namespace", "field_type": "text"},
        "values": [{"value_text": "prod"}]
      }
    ],
    "incident_role_assignments": [
      {
        "role": {"id": "01FCNDV6P870EA6S7TK1DSYD5H", "name": "Incident Lead", "short_form": "lead"},
        "assignee": {"id": "01FCNDV6P870EA6S7TK1DSYDG0", "name": "Lisa Karlin Curtis", "email": "lisa@incident.io"}
      }
    ]
  }`, severityName, statusName, statusCategory)
}

func incidentIOCreatedBody(statusName, statusCategory, severityName string) string {
	return fmt.Sprintf(`{"event_type": %q, %q: %s}`,
		incidentIOEventIncidentCreated, incidentIOEventIncidentCreated,
		incidentIOIncidentBody(statusName, statusCategory, severityName))
}

func incidentIOUpdatedBody(statusName, statusCategory string) string {
	return fmt.Sprintf(`{"event_type": %q, %q: %s}`,
		incidentIOEventIncidentUpdated, incidentIOEventIncidentUpdated,
		incidentIOIncidentBody(statusName, statusCategory, "Major"))
}

func incidentIOStatusUpdatedBody(prevCategory, newCategory string) string {
	prev := "null"
	if prevCategory != "" {
		prev = fmt.Sprintf(`{"id": "prev", "name": "Previous", "category": %q, "rank": 1}`, prevCategory)
	}
	return fmt.Sprintf(`{"event_type": %q, %q: {
    "incident": %s,
    "message": "We're working on a fix",
    "new_status": {"id": "new", "name": "New", "category": %q, "rank": 2},
    "previous_status": %s
  }}`, incidentIOEventIncidentStatusUpdated, incidentIOEventIncidentStatusUpdated,
		incidentIOIncidentBody("New", newCategory, "Major"), newCategory, prev)
}

func TestIncidentIOWebhook_IncidentCreated_MapsCanonicalPayload(t *testing.T) {
	events, err := processIncidentIO(t, incidentIOCreatedBody("Investigating", incidentIOCategoryLive, "Major"))
	require.NoError(t, err)
	require.Len(t, events, 1)

	got := events[0]
	assert.Equal(t, "acct-1", got.AccountId)
	assert.Equal(t, "incident", got.EventType)
	assert.Equal(t, "01FDAG4SAP5TYPT98WGR2N7W91", got.EventId)
	assert.Equal(t, "Our database is sad", got.EventTitle)
	assert.Equal(t, "Our database is really really sad, and we don't know why yet.", got.EventDescription)
	assert.Equal(t, "https://app.incident.io/incidents/123", got.EventUrl)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)
	assert.Equal(t, string(event.EventPriorityHigh), got.EventPriority)
	assert.Equal(t, time.Date(2026, 8, 17, 13, 28, 57, 801578000, time.UTC), got.EventCreatedAt.UTC())
	assert.Contains(t, got.WebhookId, "incidentio-01FDAG4SAP5TYPT98WGR2N7W91-")

	assert.Equal(t, "Our database is sad", got.Investigation.RuleName)
	assert.Equal(t, "incidentio_incident", got.Investigation.RuleType)
	assert.Equal(t, "01FDAG4SAP5TYPT98WGR2N7W91", got.Investigation.RuleId)
	assert.Equal(t, event.EventStatusFiring, got.Investigation.Status)
	assert.Equal(t, event.EventPriorityHigh, got.Investigation.Severity)
	assert.NotEmpty(t, got.Investigation.Fingerprint)
}

func TestIncidentIOWebhook_IncidentCreated_BuildsLabels(t *testing.T) {
	events, err := processIncidentIO(t, incidentIOCreatedBody("Investigating", incidentIOCategoryLive, "Major"))
	require.NoError(t, err)
	require.Len(t, events, 1)

	labels := events[0].Investigation.Labels

	// incident.io's own metadata is nb_-prefixed so it stays out of subject resolution.
	assert.Equal(t, "INC-123", labels["nb_incidentio_reference"])
	assert.Equal(t, "Investigating", labels["nb_incidentio_status"])
	assert.Equal(t, incidentIOCategoryLive, labels["nb_incidentio_status_category"])
	assert.Equal(t, "Major", labels["nb_incidentio_severity"])
	assert.Equal(t, "Production Outage", labels["nb_incidentio_incident_type"])
	assert.Equal(t, "public", labels["nb_incidentio_visibility"])
	assert.Equal(t, "standard", labels["nb_incidentio_mode"])
	assert.Equal(t, "inc-165-green-parrot", labels["nb_incidentio_slack_channel"])
	assert.Equal(t, "Lisa Karlin Curtis", labels["nb_incidentio_role_lead"])

	// Operator-defined custom fields are NOT prefixed — a tenant with a
	// `namespace` custom field means it to drive subject resolution.
	assert.Equal(t, "Product", labels["affected_team"])
	assert.Equal(t, "prod", labels["namespace"])
	assert.Equal(t, "prod", events[0].EventSubjectNamespace)
}

func TestIncidentIOWebhook_FingerprintStableAcrossLifecycle(t *testing.T) {
	firing, err := processIncidentIO(t, incidentIOCreatedBody("Investigating", incidentIOCategoryLive, "Major"))
	require.NoError(t, err)
	require.Len(t, firing, 1)

	resolved, err := processIncidentIO(t, incidentIOStatusUpdatedBody(incidentIOCategoryLive, incidentIOCategoryClosed))
	require.NoError(t, err)
	require.Len(t, resolved, 1)

	assert.Equal(t, string(event.EventStatusFiring), firing[0].EventStatus)
	assert.Equal(t, string(event.EventStatusResolved), resolved[0].EventStatus)
	assert.Equal(t, firing[0].Investigation.Fingerprint, resolved[0].Investigation.Fingerprint,
		"firing and resolved events for one incident must chain onto the same occurrence")
}

func TestIncidentIOWebhook_StatusUpdated_Transitions(t *testing.T) {
	tests := []struct {
		name       string
		prev       string
		next       string
		wantStatus event.EventStatus
		wantSkip   bool
	}{
		{name: "live to closed resolves", prev: incidentIOCategoryLive, next: incidentIOCategoryClosed, wantStatus: event.EventStatusResolved},
		{name: "live to learning resolves", prev: incidentIOCategoryLive, next: incidentIOCategoryLearning, wantStatus: event.EventStatusResolved},
		{name: "triage to declined resolves", prev: incidentIOCategoryTriage, next: incidentIOCategoryDeclined, wantStatus: event.EventStatusResolved},
		{name: "live to merged resolves", prev: incidentIOCategoryLive, next: incidentIOCategoryMerged, wantStatus: event.EventStatusResolved},
		{name: "closed reopened to live fires", prev: incidentIOCategoryClosed, next: incidentIOCategoryLive, wantStatus: event.EventStatusFiring},
		{name: "no previous status still emits", prev: "", next: incidentIOCategoryClosed, wantStatus: event.EventStatusResolved},

		// Within-category moves do not change how NudgeBee sees the incident.
		{name: "triage to live is a no-op", prev: incidentIOCategoryTriage, next: incidentIOCategoryLive, wantSkip: true},
		{name: "live to paused is a no-op", prev: incidentIOCategoryLive, next: incidentIOCategoryPaused, wantSkip: true},
		{name: "closed to learning is a no-op", prev: incidentIOCategoryClosed, next: incidentIOCategoryLearning, wantSkip: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := processIncidentIO(t, incidentIOStatusUpdatedBody(tc.prev, tc.next))
			if tc.wantSkip {
				assert.True(t, errors.Is(err, core.ErrEventNotSupported), "want ErrEventNotSupported, got %v", err)
				assert.Empty(t, events)
				return
			}
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, string(tc.wantStatus), events[0].EventStatus)
		})
	}
}

// incident_updated_v2 fires on every field edit. Emitting a firing event for each
// would re-investigate one incident dozens of times an hour, so it is only
// honoured as a resolution safety net.
func TestIncidentIOWebhook_IncidentUpdated_OnlyEmitsOnResolution(t *testing.T) {
	for _, category := range []string{incidentIOCategoryTriage, incidentIOCategoryLive, incidentIOCategoryPaused} {
		t.Run("skips "+category, func(t *testing.T) {
			events, err := processIncidentIO(t, incidentIOUpdatedBody("Investigating", category))
			assert.True(t, errors.Is(err, core.ErrEventNotSupported), "want ErrEventNotSupported, got %v", err)
			assert.Empty(t, events)
		})
	}

	for _, category := range []string{incidentIOCategoryClosed, incidentIOCategoryLearning, incidentIOCategoryCanceled} {
		t.Run("resolves "+category, func(t *testing.T) {
			events, err := processIncidentIO(t, incidentIOUpdatedBody("Closed", category))
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, string(event.EventStatusResolved), events[0].EventStatus)
		})
	}
}

func TestIncidentIOWebhook_UnmodelledEventTypesAreSkipped(t *testing.T) {
	// Every event type on the subscription lands here, not just the three modelled
	// ones. They must record as `skipped`, not `failed`.
	for _, eventType := range []string{
		"public_alert.alert_created_v1",
		"public_alert.alert_resolved_v1",
		"public_incident.action_created_v1",
		"public_incident.follow_up_created_v2",
		"public_escalation.escalation_created_v1",
		"schedule.shift_change_v1",
		"private_incident.incident_created_v2",
		"status_page_incident.update_shared_v1",
	} {
		t.Run(eventType, func(t *testing.T) {
			body := fmt.Sprintf(`{"event_type": %q, %q: {"id": "01GW2G3V0S59R238FAHPDS1R66"}}`, eventType, eventType)
			events, err := processIncidentIO(t, body)
			assert.True(t, errors.Is(err, core.ErrEventNotSupported), "want ErrEventNotSupported, got %v", err)
			assert.Empty(t, events)
		})
	}
}

func TestIncidentIOWebhook_MalformedPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "not json", body: `not json at all`},
		{name: "missing event_type", body: `{"public_incident.incident_created_v2": {"id": "x"}}`},
		{name: "event_type not a string", body: `{"event_type": 42}`},
		{
			name: "body key missing",
			body: fmt.Sprintf(`{"event_type": %q}`, incidentIOEventIncidentCreated),
		},
		{
			name: "incident id missing",
			body: fmt.Sprintf(`{"event_type": %q, %q: {"reference": "INC-1"}}`,
				incidentIOEventIncidentCreated, incidentIOEventIncidentCreated),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := processIncidentIO(t, tc.body)
			require.Error(t, err)
			assert.False(t, errors.Is(err, core.ErrEventNotSupported),
				"a malformed payload is a failure, not an unmodelled event")
			assert.Empty(t, events)
		})
	}
}

func TestIncidentIOEventStatusForCategory(t *testing.T) {
	firing := []string{incidentIOCategoryTriage, incidentIOCategoryLive, incidentIOCategoryPaused}
	resolved := []string{
		incidentIOCategoryClosed, incidentIOCategoryLearning,
		incidentIOCategoryDeclined, incidentIOCategoryMerged, incidentIOCategoryCanceled,
	}

	for _, c := range firing {
		assert.Equal(t, event.EventStatusFiring, incidentIOEventStatusForCategory(c), "category %q", c)
	}
	for _, c := range resolved {
		assert.Equal(t, event.EventStatusResolved, incidentIOEventStatusForCategory(c), "category %q", c)
	}

	// An unknown or absent category must never silently swallow a live incident.
	assert.Equal(t, event.EventStatusFiring, incidentIOEventStatusForCategory(""))
	assert.Equal(t, event.EventStatusFiring, incidentIOEventStatusForCategory("some_future_category"))
}

func TestIncidentIOEventPriority(t *testing.T) {
	tests := []struct {
		name     string
		severity *IncidentIOSeverity
		want     event.EventPriority
	}{
		{name: "nil severity defaults to medium", severity: nil, want: event.EventPriorityMedium},
		{name: "critical", severity: &IncidentIOSeverity{Name: "Critical"}, want: event.EventPriorityHigh},
		{name: "major", severity: &IncidentIOSeverity{Name: "Major"}, want: event.EventPriorityHigh},
		{name: "sev1", severity: &IncidentIOSeverity{Name: "SEV1"}, want: event.EventPriorityHigh},
		{name: "moderate", severity: &IncidentIOSeverity{Name: "Moderate"}, want: event.EventPriorityMedium},
		{name: "minor", severity: &IncidentIOSeverity{Name: "Minor"}, want: event.EventPriorityLow},
		{name: "trivial", severity: &IncidentIOSeverity{Name: "Trivial"}, want: event.EventPriorityInfo},
		{name: "name matching is case and space insensitive", severity: &IncidentIOSeverity{Name: "  cRiTiCaL "}, want: event.EventPriorityHigh},

		// Tenants rename severities freely; rank is the fallback.
		{name: "unknown name falls back to high rank", severity: &IncidentIOSeverity{Name: "Code Red", Rank: 4}, want: event.EventPriorityHigh},
		{name: "unknown name falls back to mid rank", severity: &IncidentIOSeverity{Name: "Code Amber", Rank: 2}, want: event.EventPriorityMedium},
		{name: "unknown name falls back to low rank", severity: &IncidentIOSeverity{Name: "Code Green", Rank: 1}, want: event.EventPriorityLow},
		{name: "unknown name and no rank defaults to medium", severity: &IncidentIOSeverity{Name: "Mystery"}, want: event.EventPriorityMedium},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, incidentIOEventPriority(tc.severity))
		})
	}
}

func TestIncidentIOLabelKey(t *testing.T) {
	tests := map[string]string{
		"Affected Team":     "affected_team",
		"namespace":         "namespace",
		"K8s Cluster":       "k8s_cluster",
		"Service / Owner":   "service_owner",
		"  Padded  Field  ": "padded_field",
		"Trailing!!":        "trailing",
		"":                  "",
	}
	for input, want := range tests {
		assert.Equal(t, want, incidentIOLabelKey(input), "input %q", input)
	}
}

func TestParseIncidentIOTime(t *testing.T) {
	assert.Equal(t,
		time.Date(2026, 8, 17, 13, 28, 57, 801578000, time.UTC),
		parseIncidentIOTime("2026-08-17T13:28:57.801578Z").UTC())
	assert.Equal(t,
		time.Date(2026, 8, 17, 13, 28, 57, 0, time.UTC),
		parseIncidentIOTime("2026-08-17T13:28:57Z").UTC())
	assert.True(t, parseIncidentIOTime("").IsZero())
	assert.True(t, parseIncidentIOTime("not a timestamp").IsZero())
}

// An incident raised from an alert route carries the upstream Alertmanager text
// in its summary, which is where the real K8s labels live.
func TestIncidentIOWebhook_ExtractsSubjectFromFiringText(t *testing.T) {
	body := fmt.Sprintf(`{"event_type": %q, %q: {
    "id": "01FDAG4SAP5TYPT98WGR2N7W91",
    "name": "KubePodCrashLooping",
    "created_at": "2026-08-17T13:28:57Z",
    "incident_status": {"name": "Investigating", "category": "live"},
    "summary": "Labels:\n - alertname = KubePodCrashLooping\n - namespace = payments\n - pod = checkout-7d9f-abcde\nAnnotations:\n - description = pod is crash looping"
  }}`, incidentIOEventIncidentCreated, incidentIOEventIncidentCreated)

	events, err := processIncidentIO(t, body)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, "payments", events[0].EventSubjectNamespace)
	assert.Equal(t, "checkout-7d9f-abcde", events[0].EventSubjectName)
	assert.Equal(t, "KubePodCrashLooping", events[0].Investigation.Labels["alertname"])
}

// Structural incident.io metadata must win over anything scraped out of free text.
func TestIncidentIOWebhook_CustomFieldsWinOverFiringText(t *testing.T) {
	body := fmt.Sprintf(`{"event_type": %q, %q: {
    "id": "01FDAG4SAP5TYPT98WGR2N7W91",
    "name": "Database down",
    "created_at": "2026-08-17T13:28:57Z",
    "incident_status": {"name": "Investigating", "category": "live"},
    "summary": "Labels:\n - namespace = scraped-from-text\n",
    "custom_field_entries": [
      {"custom_field": {"name": "namespace"}, "values": [{"value_text": "authoritative"}]}
    ]
  }}`, incidentIOEventIncidentCreated, incidentIOEventIncidentCreated)

	events, err := processIncidentIO(t, body)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "authoritative", events[0].Investigation.Labels["namespace"])
}

func TestIncidentIOWebhook_FallsBackWhenOptionalFieldsAbsent(t *testing.T) {
	body := fmt.Sprintf(`{"event_type": %q, %q: {
    "id": "01FDAG4SAP5TYPT98WGR2N7W91",
    "reference": "INC-9",
    "most_recent_update_message": "still looking",
    "slack_channel_url": "https://slack.com/app_redirect?team=T1&channel=C1"
  }}`, incidentIOEventIncidentCreated, incidentIOEventIncidentCreated)

	events, err := processIncidentIO(t, body)
	require.NoError(t, err)
	require.Len(t, events, 1)

	got := events[0]
	// Title falls back to the human reference, description to the latest update,
	// URL to the Slack channel, and an absent status is treated as firing.
	assert.Equal(t, "INC-9", got.EventTitle)
	assert.Equal(t, "still looking", got.EventDescription)
	assert.Equal(t, "https://slack.com/app_redirect?team=T1&channel=C1", got.EventUrl)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)
	assert.Equal(t, string(event.EventPriorityMedium), got.EventPriority)
	assert.False(t, got.EventCreatedAt.IsZero(), "an absent created_at must fall back to now")
}

func TestIncidentIOWebhook_Registration(t *testing.T) {
	w := IncidentIOWebhook{}
	assert.Equal(t, "incidentio_webhook", w.Name())
	assert.Equal(t, core.IntegrationCategoryIncidentWebhook, w.Category())

	// The webhook is token-authenticated like every other webhook integration.
	schema := w.ConfigSchema()
	assert.Contains(t, schema.Properties, "token")
	assert.Contains(t, schema.Properties, "account_id")
	assert.Contains(t, schema.Properties, "integration_config_name")
	assert.Empty(t, w.ValidateConfig(nil, nil, "acct-1"))
}
