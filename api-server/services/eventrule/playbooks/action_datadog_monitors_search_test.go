package playbooks

import (
	"database/sql"
	"os"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/services/internal/database"
)

const (
	testServiceName = "api-gateway"
)

func TestDatadogMonitorsSearchAction(t *testing.T) {
	action := &datadogMonitorsSearchAction{}
	ctx := NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, PlaybookEvent{})

	// Test with default parameters (status: alert)
	params := map[string]any{}

	// This will fail with Datadog configuration error, which is expected
	// (requires database connection to fetch integration config)
	_, err := action.Execute(ctx, params)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Datadog configuration")

	// Test with custom status, env, and service
	params = map[string]any{
		"status":   "alert",
		"env":      "production",
		"service":  testServiceName,
		"limit":    10,
		"duration": 2,
	}

	_, err = action.Execute(ctx, params)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "Datadog configuration")
}

func TestParseDatadogMonitorsParams(t *testing.T) {
	// Test with all parameters
	ctx := NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, PlaybookEvent{})
	rawParams := map[string]any{
		"status":   "alert",
		"env":      "production",
		"query":    "status:alert env:prod",
		"limit":    float64(50),
		"duration": 2,
	}

	params, err := parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "alert", params.Status)
	assert.Equal(t, "production", params.Env)
	assert.Equal(t, "status:alert env:prod", params.Query)
	assert.Equal(t, 50, params.Limit)
	assert.Equal(t, 2, params.Duration)

	// Test with missing parameters
	rawParams = map[string]any{}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "", params.Status)
	assert.Equal(t, "", params.Env)
	assert.Equal(t, "", params.Service)
	assert.Equal(t, 0, params.Limit)
	assert.Equal(t, 0, params.Duration)

	// Test with event labels (env and service from context)
	event := PlaybookEvent{
		Labels: map[string]string{
			"env":     "staging",
			"service": testServiceName,
		},
	}
	ctx = NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil, event)
	rawParams = map[string]any{
		"status": "warn",
	}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "warn", params.Status)
	assert.Equal(t, "staging", params.Env)
	assert.Equal(t, testServiceName, params.Service)

	// Test that rawParams override event labels
	rawParams = map[string]any{
		"env": "production",
	}
	params, err = parseDatadogMonitorsParams(ctx, rawParams)
	assert.Nil(t, err)
	assert.Equal(t, "production", params.Env) // rawParams should override
	assert.Equal(t, testServiceName, params.Service)
}

func TestBuildDatadogQuery(t *testing.T) {
	// Test with status and env
	params := datadogMonitorsSearchParams{
		Status: "alert",
		Env:    "production",
	}
	query := buildDatadogQuery(params)
	assert.Contains(t, query, "status:alert")
	assert.Contains(t, query, "env:production")

	// Test with status, env, and service
	params = datadogMonitorsSearchParams{
		Status:  "alert",
		Env:     "production",
		Service: "web-api",
	}
	query = buildDatadogQuery(params)
	assert.Contains(t, query, "status:alert")
	assert.Contains(t, query, "env:production")
	assert.Contains(t, query, "service:web-api")

	// Test with custom query
	params = datadogMonitorsSearchParams{
		Query: "custom query string",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "custom query string", query)

	// Test with only status
	params = datadogMonitorsSearchParams{
		Status: "warn",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "status:warn", query)

	// Test with only service
	params = datadogMonitorsSearchParams{
		Service: "payment-service",
	}
	query = buildDatadogQuery(params)
	assert.Equal(t, "service:payment-service", query)
}

func TestGetDatadogConfigsForAccount_FiltersDisabledAndScopesTenant(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	// Case 1: Account has disabled Datadog, query matches active only -> not found
	expectedQuery := `SELECT i.id::text FROM integrations i JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id WHERE i.type = $1 AND ica.cloud_account_id = $2 AND i.status != 'disabled' AND i.tenant_id = $3 LIMIT 1`
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("datadog", "acc-1", "tenant-1").
		WillReturnError(sql.ErrNoRows)

	_, _, _, err = getDatadogConfigsForAccount("tenant-1", "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datadog integration not found for account acc-1")

	// Case 2: Active Datadog exists with plain keys and custom site
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("datadog", "acc-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("dd-active-1"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT name::text, value::text, is_encrypted FROM integration_config_values WHERE integration_id = $1`)).
		WithArgs("dd-active-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("api_key", "plain-api-key", false).
			AddRow("app_key", "plain-app-key", false).
			AddRow("site", "datadoghq.eu", false))

	apiKey, appKey, site, err := getDatadogConfigsForAccount("tenant-1", "acc-1")
	require.NoError(t, err)
	assert.Equal(t, "plain-api-key", apiKey)
	assert.Equal(t, "plain-app-key", appKey)
	assert.Equal(t, "app.datadoghq.eu", site)

	// Case 3: Missing tenant scope must not allow an unscoped credential lookup.
	_, _, _, err = getDatadogConfigsForAccount("", "acc-2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")

	// Case 4: Empty accountId returns error immediately
	_, _, _, err = getDatadogConfigsForAccount("tenant-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDatadogMonitorsSearchAction_FailsWhenActiveIntegrationMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	action := &datadogMonitorsSearchAction{}
	ctx := NewPlaybookActionContext("tenant-1", "acc-disabled", nil, PlaybookEvent{})

	expectedQuery := `SELECT i.id::text FROM integrations i JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id WHERE i.type = $1 AND ica.cloud_account_id = $2 AND i.status != 'disabled' AND i.tenant_id = $3 LIMIT 1`
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("datadog", "acc-disabled", "tenant-1").
		WillReturnError(sql.ErrNoRows)

	_, err = action.Execute(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Datadog configuration")
	assert.Contains(t, err.Error(), "datadog integration not found")
	require.NoError(t, mock.ExpectationsWereMet())
}
