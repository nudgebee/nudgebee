package integrations

import (
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The integrations list redacts by the schema: every IsEncrypted field of every
// registered integration, the same rule as the create / update responses.
func TestIntegrationSecretFieldsFollowSchemas(t *testing.T) {
	fields := core.IntegrationSecretFields()

	for integrationType, names := range map[string][]string{
		"jira":       {"password"},
		"servicenow": {"password"},
		"pagerduty":  {"password"},
		"datadog":    {"api_key", "app_key"},
		"github":     {"password"},
		"es":         {"password", "api_key", "bearer_token"},
		"signoz":     {"signoz_password"},
		"mcp":        {"bearer_token", "oauth_client_secret"},
		"llm":        {"llm_provider_access_key", "llm_provider_secret_key", "llm_oauth_client_secret"},
	} {
		for _, name := range names {
			assert.Contains(t, fields.Exact[integrationType], name, "%s.%s must be redacted", integrationType, name)
		}
	}
	assert.ElementsMatch(t, core.LLMSecretFieldPrefixes(), fields.Prefix["llm"])

	// Every registered schema's encrypted fields are covered, under its lowercase type.
	for _, integration := range core.RegisteredIntegrations() {
		for name, prop := range integration.ConfigSchema().Properties {
			if prop.IsEncrypted {
				assert.Contains(t, fields.Exact[strings.ToLower(integration.Name())], name)
			}
		}
	}

	// The generated inbound webhook token is shown on purpose, so users can paste
	// the URL into the source system; it is a write-only ingest credential.
	for _, integrationType := range []string{"workflow_webhook", "gcp_monitoring_webhook", "zenduty_webhook", "datadog_webhook"} {
		assert.NotContains(t, fields.Exact[integrationType], "token", "%s token stays visible", integrationType)
	}
}

func TestIntegrationsListUsesRegisteredSecretFields(t *testing.T) {
	def, ok := query.GetTableMetadata("admin_get_integrations_v2")
	require.True(t, ok)
	sql, _, err := def.DefGenerator(nil, "", query.QueryRequest{})
	require.NoError(t, err)
	assert.Contains(t, sql, "('jira', 'password')")
	assert.Contains(t, sql, "('datadog', 'api_key')")
	for _, prefix := range core.LLMSecretFieldPrefixes() {
		assert.Contains(t, sql, "(LOWER(ii.type) = 'llm' AND starts_with(icv.name, '"+prefix+"'))")
	}
}
