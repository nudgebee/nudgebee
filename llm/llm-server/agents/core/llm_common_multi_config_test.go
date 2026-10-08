package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// mockLLMConfigDB returns a DatabaseManager backed by sqlmock so the LLM
// integration-config queries run against canned rows. The default sqlmock
// matcher is regexp, so query expectations match on substrings.
func mockLLMConfigDB(t *testing.T) (*common.DatabaseManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, mock
}

// expectSelect queues the candidate query (step 1). Each row is
// {id, linked to the account, marked default on the account's link, tenant default}.
func expectSelect(mock sqlmock.Sqlmock, candidates ...[4]any) {
	rows := sqlmock.NewRows([]string{"id", "linked", "account_default", "tenant_default"})
	for _, c := range candidates {
		rows.AddRow(c[0], c[1], c[2], c[3])
	}
	mock.ExpectQuery("integrations_cloud_accounts").WillReturnRows(rows)
}

// expectConfig queues the config-value query (step 2) for a single integration.
func expectConfig(mock sqlmock.Sqlmock, integrationId string, kv ...[2]string) {
	rows := sqlmock.NewRows([]string{"id", "name", "value", "is_encrypted"})
	for _, pair := range kv {
		rows.AddRow(integrationId, pair[0], pair[1], false)
	}
	mock.ExpectQuery("integration_config_values").WillReturnRows(rows)
}

// The account's own default wins, and its credential is the one returned whole —
// no values from the other linked config leak into it.
func TestFetchLLMIntegrationConfigByAccount_PrefersAccountDefault(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, false, false}, [4]any{"int-b", true, true, false})
	expectConfig(mock, "int-b",
		[2]string{"llm_provider", "huggingface"},
		[2]string{"llm_provider_api_key", "key-hf"},
		[2]string{"llm_provider_api_endpoint", "https://example.endpoints.huggingface.cloud"})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-multi")

	require.NoError(t, err)
	require.Equal(t, "huggingface", cfg["llm_provider"])
	require.Equal(t, "key-hf", cfg["llm_provider_api_key"])
	require.NotContains(t, cfg, "llm_model_name", "keys from the non-default config must not appear")
}

// An account's only config is not its default unless it is marked: picking never
// depends on how many configs an account has. With no tenant default the
// account falls through to ENV and no config values are read.
func TestFetchLLMIntegrationConfigByAccount_SingleUnmarkedConfigIsNotPicked(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, false, false})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-single")

	require.NoError(t, err)
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchLLMIntegrationConfigByAccount_SingleMarkedConfigResolves(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, true, false})
	expectConfig(mock, "int-a",
		[2]string{"llm_provider", "googleai"},
		[2]string{"llm_model_name", "gemini-3-flash-preview"},
		[2]string{"llm_provider_api_key", "key-google"})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-single")

	require.NoError(t, err)
	require.Equal(t, "googleai", cfg["llm_provider"])
	require.Equal(t, "gemini-3-flash-preview", cfg["llm_model_name"])
	require.Equal(t, "key-google", cfg["llm_provider_api_key"])
}

// No own default and no tenant default means ENV: a quiet (nil, nil) that reads
// no config values.
func TestFetchLLMIntegrationConfigByAccount_NoDefaultFallsThrough(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, false, false}, [4]any{"int-b", true, false, false})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-no-default")

	require.NoError(t, err, "no DB default means ENV, which is a normal state")
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet(), "config values must not be read when no integration is chosen")
}

// The tenant default is used by an account without its own default even when it
// is linked only to other accounts.
func TestFetchLLMIntegrationConfigByAccount_UnlinkedTenantDefaultIsUsed(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, false, false}, [4]any{"int-t", false, false, true})
	expectConfig(mock, "int-t",
		[2]string{"llm_provider", "googleai"},
		[2]string{"llm_provider_api_key", "key-google"})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-tenant-default")

	require.NoError(t, err)
	require.Equal(t, "googleai", cfg["llm_provider"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// The account's own default beats the tenant default, so changing the tenant
// default never re-routes an account that picked a provider.
func TestFetchLLMIntegrationConfigByAccount_AccountDefaultBeatsTenantDefault(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, true, false}, [4]any{"int-b", true, false, true})
	expectConfig(mock, "int-a",
		[2]string{"llm_provider", "openai"},
		[2]string{"llm_provider_api_key", "key-openai"})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-own-default")

	require.NoError(t, err)
	require.Equal(t, "openai", cfg["llm_provider"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// Two own defaults can only happen if something raced the exclusivity update.
// Neither is chosen; the account uses the tenant default, or ENV without one.
func TestFetchLLMIntegrationConfigByAccount_TwoAccountDefaultsUseTenantDefault(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock, [4]any{"int-a", true, true, false}, [4]any{"int-b", true, true, false})

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-two-defaults")

	require.NoError(t, err)
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An account with no LLM integration and no tenant default reads nothing further.
func TestFetchLLMIntegrationConfigByAccount_NoIntegrations(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	expectSelect(mock)

	cfg, err := fetchLLMIntegrationConfigByAccount(nil, dbManager, "acct-none")

	require.NoError(t, err, "no config is a normal state, not an error")
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Defence in depth for the reader itself. Selection above should mean a result
// set never spans two integrations, but if a future caller hands it a wider
// query, collapsing the rows would invent a credential nobody configured:
// integration A's api key paired with integration B's endpoint and api type,
// pointing a Google key at a HuggingFace endpoint. Refusing sends the caller to
// ENV instead; guessing would send real requests to the wrong provider.
func TestExecLLMIntegrationConfigQuery_RefusesToBlendTwoIntegrations(t *testing.T) {
	dbManager, mock := mockLLMConfigDB(t)

	rows := sqlmock.NewRows([]string{"id", "name", "value", "is_encrypted"}).
		AddRow("int-a", "llm_provider", "googleai", false).
		AddRow("int-a", "llm_provider_api_key", "key-google", false).
		AddRow("int-b", "llm_provider", "huggingface", false).
		AddRow("int-b", "llm_provider_api_endpoint", "https://example.endpoints.huggingface.cloud", false)
	mock.ExpectQuery("integration_config_values").WillReturnRows(rows)

	// Mirrors the real reader's query. sqlmock never parses this, but a query
	// that wouldn't run against Postgres would misrepresent what is under test.
	cfg, err := execLLMIntegrationConfigQuery(nil, dbManager,
		`SELECT i.id, icv.name, icv.value, icv.is_encrypted FROM integrations i
		 JOIN integration_config_values icv ON i.id = icv.integration_id`,
		map[string]any{}, "acct-blend")

	require.ErrorIs(t, err, ErrAmbiguousLLMConfig)
	require.Nil(t, cfg, "no config may be returned when the result set is ambiguous")
}
