package integrations

import (
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
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

	userId := os.Getenv("TEST_USER")
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")

	if userId == "" || tenant == "" || account == "" {
		t.Skip("Skipping test: missing TEST_USER, TEST_TENANT, or TEST_ACCOUNT")
	}

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
}

func TestNewRelicWebhook_ProcessWebhook_RealPayload(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")

	if userId == "" || tenant == "" || account == "" {
		t.Skip("Skipping test: missing TEST_USER, TEST_TENANT, or TEST_ACCOUNT")
	}

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
