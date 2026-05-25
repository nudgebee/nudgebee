package integrations

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/integrations/core"
)

// Test configuration - set these environment variables to run integration tests:
// NEW_RELIC_API_KEY - Your New Relic User API Key
// NEW_RELIC_ACCOUNT_ID - Your New Relic Account ID
// NEW_RELIC_REGION - "us" or "eu" (defaults to "us")

func getTestConfig() (apiKey, accountId, region string, skip bool) {
	apiKey = os.Getenv("NEW_RELIC_API_KEY")
	accountId = os.Getenv("NEW_RELIC_ACCOUNT_ID")
	region = os.Getenv("NEW_RELIC_REGION")

	if apiKey == "" || accountId == "" {
		return "", "", "", true
	}

	if region == "" {
		region = NewRelicRegionUS
	}

	return apiKey, accountId, region, false
}

func TestNewRelic_Name(t *testing.T) {
	nr := NewRelic{}
	assert.Equal(t, IntegrationNewRelic, nr.Name())
}

func TestNewRelic_Category(t *testing.T) {
	nr := NewRelic{}
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, nr.Category())
}

func TestNewRelic_ConfigSchema(t *testing.T) {
	nr := NewRelic{}
	schema := nr.ConfigSchema()

	// Check required fields
	assert.Contains(t, schema.Required, NewRelicConfigApiKey)
	assert.Contains(t, schema.Required, NewRelicConfigAccountId)
	assert.Contains(t, schema.Required, NewRelicConfigRegion)

	// Check properties exist
	assert.Contains(t, schema.Properties, NewRelicConfigApiKey)
	assert.Contains(t, schema.Properties, NewRelicConfigAccountId)
	assert.Contains(t, schema.Properties, NewRelicConfigRegion)
	assert.Contains(t, schema.Properties, core.DefaultLogProvider)
	assert.Contains(t, schema.Properties, core.DefaultTraceProvider)
	assert.Contains(t, schema.Properties, core.DefaultMetricsProvider)

	// Check API key is encrypted
	assert.True(t, schema.Properties[NewRelicConfigApiKey].IsEncrypted)

	// Check region has enum values
	assert.Contains(t, schema.Properties[NewRelicConfigRegion].Enum, NewRelicRegionUS)
	assert.Contains(t, schema.Properties[NewRelicConfigRegion].Enum, NewRelicRegionEU)
}

func TestNewRelic_ValidateConfig_EmptyApiKey(t *testing.T) {
	nr := NewRelic{}
	err := nr.ValidateNewRelicConfig("", "12345", NewRelicRegionUS)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API key must not be empty")
}

func TestNewRelic_ValidateConfig_EmptyAccountId(t *testing.T) {
	nr := NewRelic{}
	err := nr.ValidateNewRelicConfig("test-api-key", "", NewRelicRegionUS)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Account ID must not be empty")
}

func TestNewRelic_ValidateConfig_InvalidRegion(t *testing.T) {
	nr := NewRelic{}
	err := nr.ValidateNewRelicConfig("test-api-key", "12345", "invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid region")
}

func TestGetNewRelicEndpoint(t *testing.T) {
	tests := []struct {
		region   string
		expected string
	}{
		{NewRelicRegionUS, NewRelicAPIEndpointUS},
		{NewRelicRegionEU, NewRelicAPIEndpointEU},
		{"", NewRelicAPIEndpointUS}, // Default to US
		{"invalid", NewRelicAPIEndpointUS},
	}

	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			endpoint := GetNewRelicEndpoint(tt.region)
			assert.Equal(t, tt.expected, endpoint)
		})
	}
}

func TestBuildNRQLGraphQL(t *testing.T) {
	accountId := "12345"
	query := "SELECT count(*) FROM Transaction SINCE 1 hour ago"

	graphql := buildNRQLGraphQL(accountId, query)

	assert.Contains(t, graphql, "actor")
	assert.Contains(t, graphql, "account(id: 12345)")
	assert.Contains(t, graphql, "nrql(query:")
	assert.Contains(t, graphql, "results")
}

func TestBuildNRQLGraphQL_EscapesQuotes(t *testing.T) {
	accountId := "12345"
	query := `SELECT count(*) FROM Transaction WHERE name = "test"`

	graphql := buildNRQLGraphQL(accountId, query)

	// Double quotes should be escaped
	assert.Contains(t, graphql, `\"test\"`)
}

// Integration tests - require real API credentials

func TestNewRelic_ValidateConfig_Integration(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	nr := NewRelic{}
	err := nr.ValidateNewRelicConfig(apiKey, accountId, region)
	assert.NoError(t, err, "Validation should succeed with valid credentials")
}

func TestExecuteNRQL_SimpleQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Simple count query that should work on any New Relic account
	query := "SELECT count(*) FROM Transaction SINCE 1 hour ago"

	results, err := ExecuteNRQL(apiKey, accountId, region, query)
	require.NoError(t, err, "NRQL query should succeed")
	require.NotNil(t, results, "Results should not be nil")

	t.Logf("Query results: %+v", results)
}

func TestExecuteNRQL_LogQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Query logs
	query := "SELECT * FROM Log SINCE 30 minutes ago LIMIT 10"

	results, err := ExecuteNRQL(apiKey, accountId, region, query)
	require.NoError(t, err, "Log query should succeed")

	t.Logf("Found %d log entries", len(results))
	if len(results) > 0 {
		t.Logf("Sample log: %+v", results[0])
	}
}

func TestExecuteNRQL_SpanQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Query spans/traces
	query := "SELECT * FROM Span SINCE 1 hour ago LIMIT 10"

	results, err := ExecuteNRQL(apiKey, accountId, region, query)
	require.NoError(t, err, "Span query should succeed")

	t.Logf("Found %d spans", len(results))
	if len(results) > 0 {
		t.Logf("Sample span: %+v", results[0])
	}
}

func TestExecuteNRQL_IncidentQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Query incidents
	query := "SELECT * FROM NrAiIncident SINCE 7 days ago LIMIT 10"

	results, err := ExecuteNRQL(apiKey, accountId, region, query)
	require.NoError(t, err, "Incident query should succeed")

	t.Logf("Found %d incidents", len(results))
	if len(results) > 0 {
		t.Logf("Sample incident: %+v", results[0])
	}
}

func TestExecuteNRQL_MetricsQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Query system metrics
	query := "SELECT average(cpuPercent) FROM SystemSample SINCE 1 hour ago TIMESERIES AUTO"

	results, err := ExecuteNRQL(apiKey, accountId, region, query)
	require.NoError(t, err, "Metrics query should succeed")

	t.Logf("Found %d metric results", len(results))
	if len(results) > 0 {
		t.Logf("Sample metric: %+v", results[0])
	}
}

func TestExecuteNRQL_InvalidQuery(t *testing.T) {
	apiKey, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_API_KEY and NEW_RELIC_ACCOUNT_ID environment variables not set")
	}

	// Invalid NRQL syntax
	query := "SELECT FROM InvalidTable"

	_, err := ExecuteNRQL(apiKey, accountId, region, query)
	assert.Error(t, err, "Invalid query should return an error")
	t.Logf("Expected error: %v", err)
}

func TestExecuteNRQL_InvalidApiKey(t *testing.T) {
	_, accountId, region, skip := getTestConfig()
	if skip {
		t.Skip("Skipping integration test: NEW_RELIC_ACCOUNT_ID environment variable not set")
	}

	// Use invalid API key
	query := "SELECT count(*) FROM Transaction SINCE 1 hour ago"

	_, err := ExecuteNRQL("invalid-api-key", accountId, region, query)
	assert.Error(t, err, "Invalid API key should return an error")
	t.Logf("Expected error: %v", err)
}

// Webhook tests

func TestNewRelicWebhook_Name(t *testing.T) {
	webhook := NewRelicWebhook{}
	assert.Equal(t, IntegrationNewRelicWebhook, webhook.Name())
}

func TestNewRelicWebhook_Category(t *testing.T) {
	webhook := NewRelicWebhook{}
	assert.Equal(t, core.IntegrationCategoryIncidentWebhook, webhook.Category())
}

func TestNewRelicWebhook_ConfigSchema(t *testing.T) {
	webhook := NewRelicWebhook{}
	schema := webhook.ConfigSchema()

	assert.Contains(t, schema.Properties, "integration_config_name")
	assert.Contains(t, schema.Properties, "account_id")
	assert.Contains(t, schema.Properties, "token")
}

func TestMapNewRelicStateToStatus(t *testing.T) {
	tests := []struct {
		state    string
		expected string
	}{
		{"CREATED", "FIRING"},
		{"ACTIVATED", "FIRING"},
		{"OPEN", "FIRING"},
		{"ACKNOWLEDGED", "acknowledged"},
		{"CLOSED", "RESOLVED"},
		{"RESOLVED", "RESOLVED"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			status := mapNewRelicStateToStatus(tt.state)
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestMapNewRelicPriority(t *testing.T) {
	tests := []struct {
		priority string
		expected string
	}{
		{"CRITICAL", "high"},
		{"HIGH", "high"},
		{"MEDIUM", "medium"},
		{"LOW", "low"},
		{"INFO", "info"},
		{"unknown", "low"},
	}

	for _, tt := range tests {
		t.Run(tt.priority, func(t *testing.T) {
			priority := mapNewRelicPriority(tt.priority)
			assert.Equal(t, tt.expected, string(priority))
		})
	}
}

func TestNewRelicWebhook_ProcessEventWebhook_BasicPayload(t *testing.T) {
	webhook := NewRelicWebhook{}

	// Sample New Relic Workflows payload
	payload := `{
		"id": "test-issue-123",
		"issueUrl": "https://one.newrelic.com/alerts-ai/issues/test-issue-123",
		"title": "[CRITICAL] High CPU Usage on production-server",
		"priority": "CRITICAL",
		"state": "ACTIVATED",
		"activatedAt": 1706540400000,
		"conditionName": "High CPU Usage",
		"conditionId": "condition-456",
		"entityGuids": ["MXxBUE18QVBQTElDQVRJT058MTIzNDU2"],
		"entityNames": ["production-server"],
		"sources": ["Infrastructure"],
		"labels": {
			"environment": "production",
			"team": "platform"
		},
		"policyName": "Infrastructure Policy",
		"policyId": "policy-789",
		"accountId": 4258114,
		"accountName": "Test Account",
		"description": "CPU usage exceeded 90% threshold"
	}`

	events, err := webhook.ProcessEventWebook(nil, nil, "test-account-id", payload)
	require.NoError(t, err, "Should process valid payload")
	require.Len(t, events, 1, "Should return one event")

	event := events[0]
	assert.Equal(t, "test-issue-123", event.WebhookId)
	assert.Equal(t, "test-issue-123", event.EventId)
	assert.Equal(t, "FIRING", event.EventStatus)
	assert.Equal(t, "CRITICAL", event.EventPriority)
	assert.Equal(t, "High CPU Usage on production-server", event.EventTitle)
	assert.Equal(t, "production-server", event.EventSubjectName)
	assert.Contains(t, event.EventUrl, "one.newrelic.com")

	// Check labels
	assert.Equal(t, "production", event.Investigation.Labels["environment"])
	assert.Equal(t, "platform", event.Investigation.Labels["team"])
	assert.Equal(t, "High CPU Usage", event.Investigation.Labels["condition_name"])
	assert.Equal(t, "Infrastructure Policy", event.Investigation.Labels["policy_name"])
}

func TestNewRelicWebhook_ProcessEventWebhook_ResolvedState(t *testing.T) {
	webhook := NewRelicWebhook{}

	payload := `{
		"id": "test-issue-456",
		"title": "Alert resolved",
		"priority": "HIGH",
		"state": "CLOSED",
		"closedAt": 1706544000000,
		"conditionName": "Test Condition"
	}`

	events, err := webhook.ProcessEventWebook(nil, nil, "test-account-id", payload)
	require.NoError(t, err)
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "RESOLVED", event.EventStatus)
}

func TestNewRelicWebhook_ProcessEventWebhook_MissingId(t *testing.T) {
	webhook := NewRelicWebhook{}

	payload := `{
		"title": "Alert without ID",
		"priority": "LOW"
	}`

	_, err := webhook.ProcessEventWebook(nil, nil, "test-account-id", payload)
	assert.Error(t, err, "Should fail when ID is missing")
	assert.Contains(t, err.Error(), "missing issue ID")
}

func TestNewRelicWebhook_ProcessEventWebhook_InvalidJson(t *testing.T) {
	webhook := NewRelicWebhook{}

	payload := `{invalid json}`

	_, err := webhook.ProcessEventWebook(nil, nil, "test-account-id", payload)
	assert.Error(t, err, "Should fail on invalid JSON")
}

func TestNewRelicWebhook_ProcessEventWebhook_ImpactedEntities(t *testing.T) {
	webhook := NewRelicWebhook{}

	payload := `{
		"id": "test-issue-789",
		"title": "Service Alert",
		"priority": "MEDIUM",
		"state": "ACTIVATED",
		"impactedEntities": [
			{
				"guid": "MXxBUE18QVBQTElDQVRJT058MTIzNDU2",
				"name": "my-service",
				"type": "APPLICATION",
				"entityType": "APM_APPLICATION_ENTITY",
				"domain": "APM"
			}
		]
	}`

	events, err := webhook.ProcessEventWebook(nil, nil, "test-account-id", payload)
	require.NoError(t, err)
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "my-service", event.EventSubjectName)
	assert.Equal(t, "application", event.EventSubjectKind)
	assert.Equal(t, "MXxBUE18QVBQTElDQVRJT058MTIzNDU2", event.CloudResourceId)
}

// Benchmark tests

func BenchmarkBuildNRQLGraphQL(b *testing.B) {
	accountId := "12345"
	query := "SELECT count(*) FROM Transaction WHERE name = 'WebTransaction/Go/myHandler' SINCE 1 hour ago"

	for i := 0; i < b.N; i++ {
		buildNRQLGraphQL(accountId, query)
	}
}

func BenchmarkMapNewRelicStateToStatus(b *testing.B) {
	states := []string{"CREATED", "ACTIVATED", "ACKNOWLEDGED", "CLOSED", "RESOLVED"}

	for i := 0; i < b.N; i++ {
		mapNewRelicStateToStatus(states[i%len(states)])
	}
}
