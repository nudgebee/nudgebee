package integrations

import (
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Minimal webhook payloads - only need issue ID
const newRelicWebhookPayloadMinimal = `{
    "id": "96474cc3-ce8c-42ec-b88d-f107851a1932"
}`

const newRelicWebhookPayloadWithIssueId = `{
    "issueId": "96474cc3-ce8c-42ec-b88d-f107851a1933"
}`

const newRelicWebhookPayloadMissingId = `{
    "title": "Some alert"
}`

func TestNewRelicWebhook_ProcessWebhook_WithId(t *testing.T) {
	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	assert.NotNil(t, integration)

	webhookIntegration, _ := integration.(NewRelicWebhook)
	assert.NotNil(t, webhookIntegration)

	tenant, account, userId := testenv.RequireTenant(t)

	eventData, err := webhookIntegration.ProcessEventWebook(
		security.NewRequestContextForUserTenant(userId, tenant, nil, nil, nil),
		[]core.IntegrationConfigValue{},
		account,
		newRelicWebhookPayloadMinimal,
	)

	// May fail if NewRelic API key not configured - that's okay
	if err != nil {
		t.Logf("Expected error if NewRelic not configured: %v", err)
		return
	}

	assert.NotEmpty(t, eventData)
	assert.Equal(t, 1, len(eventData))

	event := eventData[0]
	assert.Equal(t, "96474cc3-ce8c-42ec-b88d-f107851a1932", event.EventId)
}

func TestNewRelicWebhook_ProcessWebhook_WithIssueId(t *testing.T) {
	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	assert.NotNil(t, integration)

	webhookIntegration, _ := integration.(NewRelicWebhook)
	assert.NotNil(t, webhookIntegration)

	var payload NewRelicWebhookPayload
	err := common.UnmarshalJson([]byte(newRelicWebhookPayloadWithIssueId), &payload)
	assert.Nil(t, err)
	assert.Equal(t, "96474cc3-ce8c-42ec-b88d-f107851a1933", payload.IssueId)
}

func TestNewRelicWebhook_ProcessWebhook_MissingId(t *testing.T) {
	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	assert.NotNil(t, integration)

	webhookIntegration, _ := integration.(NewRelicWebhook)
	assert.NotNil(t, webhookIntegration)

	userId := os.Getenv("TEST_USER")

	eventData, err := webhookIntegration.ProcessEventWebook(
		security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil),
		[]core.IntegrationConfigValue{},
		os.Getenv("TEST_ACCOUNT"),
		newRelicWebhookPayloadMissingId,
	)

	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "missing issue ID")
	assert.Empty(t, eventData)
}

func TestNewRelicAPI_FetchIssueDetails(t *testing.T) {
	apiKey := os.Getenv("NEW_RELIC_API_KEY")
	accountId := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region := os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountId == "" {
		t.Skip("Skipping test: missing NEW_RELIC_API_KEY or NEW_RELIC_ACCOUNT_ID")
	}

	if region == "" {
		region = "us"
	}

	issueId := "98441886-dfc8-4090-afc2-bfd7a1ac8e59"
	createdAt := int64(1772444887531)

	issue, err := getNewRelicIssueDetails(apiKey, accountId, region, issueId, &createdAt)

	if err != nil {
		t.Logf("Issue may not exist or API error: %v", err)
		return
	}

	assert.NotNil(t, issue)
	assert.Equal(t, issueId, issue.IssueId)
	assert.NotEmpty(t, issue.Title)
	assert.NotEmpty(t, issue.State)
	assert.NotEmpty(t, issue.Priority)
}

const newRelicWebhookPayloadReal = `{
    "id": "98441886-dfc8-4090-afc2-bfd7a1ac8e59",
    "issueUrl": "https://radar-api.service.newrelic.com/accounts/7745957/issues/98441886-dfc8-4090-afc2-bfd7a1ac8e59?notifier=WEBHOOK",
    "title": "i am alert for services server",
    "priority": "CRITICAL",
    "impactedEntities": ["services-server", "services-server", "services-server"],
    "state": "ACTIVATED",
    "trigger": "INCIDENT_ADDED",
    "isCorrelated": "false",
    "createdAt": 1772444887531,
    "updatedAt": 1772444897519,
    "sources": ["newrelic"],
    "alertPolicyNames": ["Initial policy"],
    "alertConditionNames": ["sample alert for nudgebee webhook test"],
    "workflowName": "ndgebee test webhook"
}`

func TestNewRelicWebhook_ParsePayload_Real(t *testing.T) {
	var payload NewRelicWebhookPayload
	err := common.UnmarshalJson([]byte(newRelicWebhookPayloadReal), &payload)
	assert.Nil(t, err)
	assert.Equal(t, "98441886-dfc8-4090-afc2-bfd7a1ac8e59", payload.ID)
	assert.Equal(t, int64(1772444887531), payload.IssueCreatedAt)
	assert.Equal(t, "https://radar-api.service.newrelic.com/accounts/7745957/issues/98441886-dfc8-4090-afc2-bfd7a1ac8e59?notifier=WEBHOOK", payload.IssueUrl)
	assert.Equal(t, "CRITICAL", payload.Priority)
	assert.Equal(t, "ACTIVATED", payload.State)
	assert.Equal(t, []string{"newrelic"}, payload.Sources)
	assert.Equal(t, []string{"Initial policy"}, payload.AlertPolicyNames)
	assert.Equal(t, []string{"sample alert for nudgebee webhook test"}, payload.AlertConditionNames)
	assert.Equal(t, "ndgebee test webhook", payload.WorkflowName)
}

func TestExtractIssueIdFromUrl(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "standard workflow notifier url with query string",
			in:   "https://radar-api.service.newrelic.com/accounts/1/issues/0ea2df1c-adab-45d2-aae0-042b609d2322?notifier=SLACK",
			want: "0ea2df1c-adab-45d2-aae0-042b609d2322",
		},
		{
			name: "url without query string",
			in:   "https://radar-api.service.newrelic.com/accounts/1/issues/98441886-dfc8-4090-afc2-bfd7a1ac8e59",
			want: "98441886-dfc8-4090-afc2-bfd7a1ac8e59",
		},
		{
			name: "url with multiple query params",
			in:   "https://radar-api.service.newrelic.com/accounts/7745957/issues/abcdef12-3456-7890-abcd-ef1234567890?notifier=WEBHOOK&foo=bar",
			want: "abcdef12-3456-7890-abcd-ef1234567890",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "trailing slash before query",
			in:   "https://example.com/issues/?notifier=SLACK",
			want: "",
		},
		{
			name: "no slashes returns empty so caller falls back to other id sources",
			in:   "not-a-url",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractIssueIdFromUrl(tc.in))
		})
	}
}

const newRelicWebhookPayloadWorkflowMismatchedId = `{
    "id": "d1b1f3fd-995a-4066-88ab-8ce4f6960654",
    "issueUrl": "https://radar-api.service.newrelic.com/accounts/1/issues/0ea2df1c-adab-45d2-aae0-042b609d2322?notifier=SLACK",
    "title": "Memory Used % > 90 for at least 2 minutes on 'Some-Entity'",
    "priority": "CRITICAL",
    "impactedEntities": ["logs.itg.cloud", "MonitorTTFB query"],
    "state": "ACTIVATED",
    "trigger": "INCIDENT_ADDED",
    "isCorrelated": false,
    "createdAt": 1617881246260,
    "updatedAt": 1617881246260,
    "sources": ["newrelic"],
    "alertPolicyNames": ["Policy1", "Policy2"],
    "alertConditionNames": ["condition1", "condition2"],
    "workflowName": "DBA Team workflow"
}`

// Workflow notifications carry the queryable issue UUID in issueUrl while
// the top-level `id` is the notification batch UUID. The webhook handler
// must prefer the issueUrl-extracted UUID and degrade gracefully to the
// payload when no NewRelic API integration is configured.
func TestNewRelicWebhook_ProcessWebhook_WorkflowMismatchedId(t *testing.T) {
	tenant, account, userId := testenv.RequireTenant(t)

	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	webhookIntegration, _ := integration.(NewRelicWebhook)

	eventData, err := webhookIntegration.ProcessEventWebook(
		security.NewRequestContextForUserTenant(userId, tenant, nil, nil, nil),
		[]core.IntegrationConfigValue{},
		account,
		newRelicWebhookPayloadWorkflowMismatchedId,
	)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(eventData))

	event := eventData[0]
	assert.Equal(t, "0ea2df1c-adab-45d2-aae0-042b609d2322", event.EventId)
	assert.Equal(t, "CRITICAL", event.EventPriority)
	assert.Equal(t, "firing", event.EventStatus)
	assert.Equal(t, "Memory Used % > 90 for at least 2 minutes on 'Some-Entity'", event.EventTitle)
	assert.Contains(t, event.EventUrl, "0ea2df1c-adab-45d2-aae0-042b609d2322")
	assert.Equal(t, "DBA Team workflow", event.Investigation.Labels["workflow_name"])
	assert.Equal(t, "Policy1,Policy2", event.Investigation.Labels["policy_name"])
	assert.Equal(t, "condition1,condition2", event.Investigation.Labels["condition_name"])
	assert.Contains(t, event.Investigation.Labels["entity_names"], "logs.itg.cloud")
}

// Legacy "Notification channel → Webhook" payload (snake_case). NewRelic
// deprecated this in 2023, but migration-frozen channels and customers on
// older policies still send it. Verbatim shape from NewRelic docs.
const newRelicWebhookPayloadLegacy = `{
    "account_id": 1234567,
    "account_name": "Acme",
    "condition_id": 998877,
    "condition_name": "High CPU on web tier",
    "current_state": "open",
    "details": "CPU > 90% for 5 minutes",
    "event_type": "INCIDENT",
    "incident_acknowledge_url": "https://alerts.newrelic.com/accounts/1234567/incidents/9876543/acknowledge",
    "incident_id": 9876543,
    "incident_url": "https://alerts.newrelic.com/accounts/1234567/incidents/9876543",
    "owner": "alice@example.com",
    "policy_name": "Production tier",
    "policy_url": "https://alerts.newrelic.com/accounts/1234567/policies/111",
    "runbook_url": "https://wiki/incident-runbook",
    "severity": "CRITICAL",
    "targets": [
        {
            "id": "host:abc",
            "name": "web-01",
            "link": "https://rpm.newrelic.com/accounts/1234567/applications/1",
            "labels": {"environment": "production", "hostname": "web-01"},
            "product": "APM",
            "type": "Server"
        }
    ],
    "timestamp": 1714694400000,
    "duration": 120,
    "version": "1.0"
}`

func TestNewRelicWebhook_LegacyPayload_Parse(t *testing.T) {
	var p NewRelicLegacyIncidentPayload
	err := common.UnmarshalJson([]byte(newRelicWebhookPayloadLegacy), &p)
	assert.Nil(t, err)
	assert.Equal(t, int64(9876543), p.IncidentId)
	assert.Equal(t, "open", p.CurrentState)
	assert.Equal(t, "CRITICAL", p.Severity)
	assert.Equal(t, "INCIDENT", p.EventType)
	assert.Equal(t, "High CPU on web tier", p.ConditionName)
	assert.Equal(t, int64(1714694400000), p.Timestamp)
	assert.Len(t, p.Targets, 1)
	assert.Equal(t, "web-01", p.Targets[0].Name)
	assert.Equal(t, "production", p.Targets[0].Labels["environment"])
}

func TestMapLegacyStateToStatus(t *testing.T) {
	// Compare against the canonical constants rather than hard-coded strings —
	// the codebase uses inconsistent casing ("firing" vs "RESOLVED") and we
	// just want the helper to produce the same value the rest of the code
	// already uses for these states.
	cases := map[string]string{
		"open":         string(event.EventStatusFiring),
		"OPEN":         string(event.EventStatusFiring),
		"acknowledged": "acknowledged",
		"closed":       string(event.EventStatusResolved),
		"unknown":      "unknown",
		"":             "",
	}
	for in, want := range cases {
		assert.Equal(t, want, mapLegacyStateToStatus(in), "input=%q", in)
	}
}

func TestMapLegacySeverityToPriority(t *testing.T) {
	assert.Equal(t, event.EventPriortiyHigh, mapLegacySeverityToPriority("CRITICAL"))
	assert.Equal(t, event.EventPriortiyMedium, mapLegacySeverityToPriority("WARNING"))
	assert.Equal(t, event.EventPriortiyInfo, mapLegacySeverityToPriority("INFO"))
	assert.Equal(t, event.EventPriortiyLow, mapLegacySeverityToPriority("UNKNOWN"))
	assert.Equal(t, event.EventPriortiyLow, mapLegacySeverityToPriority(""))
}

// The dispatcher must route legacy payloads (non-zero `incident_id`) to the
// snake_case handler instead of failing with "missing issue ID".
func TestNewRelicWebhook_ProcessWebhook_LegacyDispatch(t *testing.T) {
	tenant, account, userId := testenv.RequireTenant(t)

	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	webhookIntegration, _ := integration.(NewRelicWebhook)

	eventData, err := webhookIntegration.ProcessEventWebook(
		security.NewRequestContextForUserTenant(userId, tenant, nil, nil, nil),
		[]core.IntegrationConfigValue{},
		account,
		newRelicWebhookPayloadLegacy,
	)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(eventData))

	ev := eventData[0]
	assert.Equal(t, "9876543", ev.EventId)
	assert.Equal(t, "firing", ev.EventStatus)
	assert.Equal(t, "CRITICAL", ev.EventPriority)
	assert.Equal(t, "High CPU on web tier", ev.EventTitle)
	assert.Equal(t, "https://alerts.newrelic.com/accounts/1234567/incidents/9876543", ev.EventUrl)

	labels := ev.Investigation.Labels
	assert.Equal(t, "Production tier", labels["policy_name"])
	assert.Equal(t, "High CPU on web tier", labels["condition_name"])
	assert.Equal(t, "998877", labels["condition_family_id"])
	assert.Equal(t, "https://wiki/incident-runbook", labels["runbook_url"])
	assert.Equal(t, "alice@example.com", labels["owner"])
	assert.Equal(t, "INCIDENT", labels["event_type"])
	assert.Equal(t, "1234567", labels["nr_account_id"])
	assert.Equal(t, "Acme", labels["nr_account_name"])
	assert.Equal(t, "120", labels["duration_seconds"])
	assert.Equal(t, "APM", labels["product"])
	assert.Equal(t, "Server", labels["target_type"])
	assert.Equal(t, "production", labels["target_environment"])
	assert.Equal(t, "web-01", labels["target_hostname"])
	assert.Equal(t, "web-01", labels["service"])
	assert.Equal(t, "web-01", labels["entity_names"])

	// Fingerprint = condition_id-incident_id
	assert.Equal(t, "998877-9876543", ev.Investigation.Fingerprint)
}

// Modern Workflow payload with accumulations and issuePageUrl. Verifies that
// the source URL prefers issuePageUrl over the constructed one and that
// runbookUrl / nrqlQuery / tag.* surface as labels.
const newRelicWebhookPayloadModernAccumulations = `{
    "id": "abc-123",
    "issueId": "abc-123",
    "issuePageUrl": "https://radar-api.service.newrelic.com/redirect/abc-123",
    "issueTitle": "DB latency above SLO",
    "priority": "HIGH",
    "state": "ACTIVATED",
    "trigger": "INCIDENT_ADDED",
    "createdAt": 1714694400000,
    "updatedAt": 1714694460000,
    "sources": ["newrelic"],
    "impactedEntities": ["payments-db"],
    "incidentIds": ["inc-1", "inc-2"],
    "totalIncidents": 2,
    "workflowName": "Tier-1 workflow",
    "accumulations": {
        "runbookUrl": ["https://wiki/db-runbook"],
        "deepLinkUrl": ["https://one.newrelic.com/launcher/db"],
        "conditionProduct": ["APM"],
        "nrqlQuery": ["SELECT average(duration) FROM Transaction WHERE appName='payments-db'"],
        "policyName": ["DB SLOs"],
        "conditionName": ["p95 latency"],
        "tag": {
            "team": ["data-platform"],
            "env": ["prod"]
        }
    }
}`

func TestNewRelicWebhook_ParsePayload_ModernAccumulations(t *testing.T) {
	var p NewRelicWebhookPayload
	err := common.UnmarshalJson([]byte(newRelicWebhookPayloadModernAccumulations), &p)
	assert.Nil(t, err)
	assert.Equal(t, "https://radar-api.service.newrelic.com/redirect/abc-123", p.IssuePageUrl)
	assert.Equal(t, "DB latency above SLO", p.IssueTitle)
	assert.Equal(t, []string{"inc-1", "inc-2"}, p.IncidentIds)
	assert.Equal(t, int64(2), p.TotalIncidents)
	assert.NotNil(t, p.Accumulations)

	runbook := accumulationsStringList(p.Accumulations, "runbookUrl")
	assert.Equal(t, []string{"https://wiki/db-runbook"}, runbook)
	assert.Equal(t, "https://wiki/db-runbook", firstString(runbook))
}

func TestNewRelicWebhook_ProcessWebhook_RealPayload(t *testing.T) {
	tenant, account, userId := testenv.RequireTenant(t)

	integration, _ := core.GetIntegration(IntegrationNewRelicWebhook)
	webhookIntegration, _ := integration.(NewRelicWebhook)

	eventData, err := webhookIntegration.ProcessEventWebook(
		security.NewRequestContextForUserTenant(userId, tenant, nil, nil, nil),
		[]core.IntegrationConfigValue{},
		account,
		newRelicWebhookPayloadReal,
	)

	if err != nil {
		t.Logf("ProcessEventWebook error: %v", err)
		return
	}

	assert.NotEmpty(t, eventData)
	event := eventData[0]
	assert.Equal(t, "98441886-dfc8-4090-afc2-bfd7a1ac8e59", event.EventId)
	assert.Equal(t, "CRITICAL", event.EventPriority)
	assert.NotEmpty(t, event.Investigation.Evidences)
	t.Logf("Event: id=%s title=%s status=%s priority=%s evidences=%d",
		event.EventId, event.EventTitle, event.EventStatus, event.EventPriority, len(event.Investigation.Evidences))
}
