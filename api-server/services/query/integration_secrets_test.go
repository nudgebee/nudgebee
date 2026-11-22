package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The query test binary does not link integrations/core, which registers the
// real lookup; register a small one so the integrations list can be generated.
func init() {
	if integrationSecretFieldsFn == nil {
		RegisterIntegrationSecretFields(testIntegrationSecretFields)
	}
}

func testIntegrationSecretFields() IntegrationSecretFields {
	return IntegrationSecretFields{
		Exact: map[string][]string{
			"jira":     {"password", "password", ""},
			"datadog":  {"app_key", "api_key"},
			"o'reilly": {"it's"},
		},
		Prefix: map[string][]string{"llm": {"llm_provider_api_key"}},
	}
}

func withIntegrationSecretFields(t *testing.T, fn func() IntegrationSecretFields) {
	t.Helper()
	prev := integrationSecretFieldsFn
	integrationSecretFieldsFn = fn
	t.Cleanup(func() { integrationSecretFieldsFn = prev })
}

func TestIntegrationSecretFieldSQL(t *testing.T) {
	withIntegrationSecretFields(t, testIntegrationSecretFields)

	sql, err := integrationSecretFieldSQL("ii.type", "icv.name")
	require.NoError(t, err)
	assert.Equal(t,
		"((LOWER(ii.type), icv.name) IN (('datadog', 'api_key'), ('datadog', 'app_key'), ('jira', 'password'), ('o''reilly', 'it''s'))"+
			" OR (LOWER(ii.type) = 'llm' AND starts_with(icv.name, 'llm_provider_api_key')))",
		sql, "sorted, de-duplicated, blank names dropped, literals quoted")
}

func TestIntegrationSecretFieldSQL_FailsClosed(t *testing.T) {
	withIntegrationSecretFields(t, nil)
	_, err := integrationSecretFieldSQL("ii.type", "icv.name")
	require.ErrorIs(t, err, errIntegrationSecretFieldsUnset)

	withIntegrationSecretFields(t, func() IntegrationSecretFields { return IntegrationSecretFields{} })
	_, err = integrationSecretFieldSQL("ii.type", "icv.name")
	require.ErrorIs(t, err, errIntegrationSecretFieldsUnset, "an empty lookup must not mean nothing is secret")

	_, _, err = table_metadata["admin_get_integrations_v2"].DefGenerator(nil, "", QueryRequest{})
	require.ErrorIs(t, err, errIntegrationSecretFieldsUnset, "the list must not be served unredacted")
}

func TestAdminGetIntegrationsV2_RedactsSecretValues(t *testing.T) {
	withIntegrationSecretFields(t, testIntegrationSecretFields)

	sql, _, err := table_metadata["admin_get_integrations_v2"].DefGenerator(nil, "", QueryRequest{})
	require.NoError(t, err)
	assert.Contains(t, sql, "'value', CASE WHEN COALESCE(icv.is_encrypted, false) OR ((LOWER(ii.type), icv.name) IN (('datadog', 'api_key'),")
	assert.Contains(t, sql, "THEN '' ELSE icv.value END,")
	assert.Contains(t, sql, "'has_value', icv.value IS NOT NULL AND icv.value <> ''",
		"the UI decides the saved mask from has_value")
	assert.NotContains(t, sql, "LIKE 'llm_", "the hard-coded LLM list is replaced by the registered lookup")
	assert.Equal(t, 1, strings.Count(sql, "icv.value END"), "exactly one place returns a value")
}

func TestAdminGetIntegrationsV2_ConfigValueFilterSkipsSecretFields(t *testing.T) {
	withIntegrationSecretFields(t, testIntegrationSecretFields)

	request := QueryRequest{Where: QueryWhereClause{Binary: BinaryWhereClause{
		"config_value_name":  {Eq: "workflow_id"},
		"config_value_value": {Eq: "wf-1"},
	}}}
	sql, _, err := table_metadata["admin_get_integrations_v2"].DefGenerator(nil, "", request)
	require.NoError(t, err)
	assert.Contains(t, sql,
		"WHERE icv_filter.integration_id = i.id AND icv_filter.name = 'workflow_id' AND icv_filter.value = 'wf-1'"+
			" AND NOT COALESCE(icv_filter.is_encrypted, false) AND NOT ((LOWER(i.type), icv_filter.name) IN (('datadog', 'api_key'),")

	nameOnly := QueryRequest{Where: QueryWhereClause{Binary: BinaryWhereClause{"config_value_name": {Eq: "password"}}}}
	sql, _, err = table_metadata["admin_get_integrations_v2"].DefGenerator(nil, "", nameOnly)
	require.NoError(t, err)
	assert.NotContains(t, sql, "AND NOT COALESCE(icv_filter.is_encrypted", "filtering by name alone reveals no value")
}
