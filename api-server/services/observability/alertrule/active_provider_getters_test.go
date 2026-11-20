package alertrule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

func newTestRequestContext(tenantId string) *security.RequestContext {
	var sc security.SecurityContext
	_ = json.Unmarshal([]byte(`{"TenantId":"`+tenantId+`"}`), &sc)
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

func TestAlertRuleProviderGettersUseActiveIntegrationConfigs(t *testing.T) {
	lookupErr := errors.New("active integration lookup failed")
	ctx := newTestRequestContext("tenant")
	for _, tc := range []struct {
		name     string
		provider string
		source   string
		get      func(*security.RequestContext) error
	}{
		{"Datadog", "datadog", "", func(ctx *security.RequestContext) error {
			_, _, _, err := getDatadogConfigs(ctx, "account")
			return err
		}},
		{"NewRelic", "newrelic", "", func(ctx *security.RequestContext) error {
			_, _, _, err := getNewRelicConfigs(ctx, "account")
			return err
		}},
		{"Dynatrace", "dynatrace", "", func(ctx *security.RequestContext) error {
			_, _, err := getDynatraceConfigs(ctx, "account")
			return err
		}},
		{"Splunk", "splunk_observability_platform", "", func(ctx *security.RequestContext) error {
			_, err := getSplunkO11yConfigs(ctx, "account")
			return err
		}},
		{"Elasticsearch", "ES", "user", func(ctx *security.RequestContext) error {
			_, err := getElasticsearchConfigs(ctx, "account")
			return err
		}},
		{"Loki", "loki", "user", func(ctx *security.RequestContext) error {
			_, err := getLokiConfigs(ctx, "account")
			return err
		}},
		{"SigNoz", "signoz", "user", func(ctx *security.RequestContext) error {
			_, err := getSignozCredentials(ctx, "account")
			return err
		}},
		{"Grafana", "grafana", "", func(ctx *security.RequestContext) error {
			_, _, err := getGrafanaConfigs(ctx, "account")
			return err
		}},
		{"Chronosphere", "chronosphere", "", func(ctx *security.RequestContext) error {
			_, _, err := getChronosphereConfigs(ctx, "account")
			return err
		}},
		{"CubeAPM", "cubeapm", "", func(ctx *security.RequestContext) error {
			_, err := getCubeAPMConfigs(ctx, "account")
			return err
		}},
		{"Prometheus", "prometheus", "user", func(ctx *security.RequestContext) error {
			_, err := getPrometheusUserConfig(ctx, "account")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			patch := gomonkey.NewPatches()
			defer patch.Reset()
			patch.ApplyFunc(listIntegrationConfigValuesWithSource, func(_ *security.RequestContext, accountID, provider, source string) ([]integrationConfigValue, error) {
				calls++
				if accountID != "account" || provider != tc.provider || source != tc.source {
					t.Fatalf("lookup scoped to account=%q provider=%q source=%q, got account=%q provider=%q source=%q",
						"account", tc.provider, tc.source, accountID, provider, source)
				}
				return nil, lookupErr
			})
			if err := tc.get(ctx); !errors.Is(err, lookupErr) || calls != 1 {
				t.Fatalf("got error=%v active lookups=%d, want propagated active lookup error", err, calls)
			}
		})
	}
}

func TestListIntegrationConfigValuesWithSource_EnforcesActiveFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	ctx := newTestRequestContext("tenant-1")

	expectedQuery := `SELECT i.id::text FROM integrations i JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id WHERE i.type = $1 AND i.tenant_id = $2 AND i.status != 'disabled' AND ica.cloud_account_id = $3 LIMIT 1`
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("datadog", "tenant-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("integ-1"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT name::text, value::text, is_encrypted FROM integration_config_values WHERE integration_id = $1`)).
		WithArgs("integ-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("api_key", "active-key", false).
			AddRow("app_key", "active-app", false))

	vals, err := listIntegrationConfigValues(ctx, "acc-1", "datadog")
	require.NoError(t, err)
	require.Len(t, vals, 2)
	assert.Equal(t, "api_key", vals[0].Name)
	assert.Equal(t, "active-key", vals[0].Value)

	// When active query returns no rows (e.g. only disabled row existed in DB)
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("datadog", "tenant-1", "acc-1").
		WillReturnError(sql.ErrNoRows)

	_, err = listIntegrationConfigValues(ctx, "acc-1", "datadog")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integration datadog not found for account acc-1")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHasIntegrationWithSource_EnforcesActiveFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	ctx := newTestRequestContext("tenant-1")

	expectedQuery := `SELECT EXISTS (
			SELECT 1
			FROM integrations i
			JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
			WHERE i.type = $1
			  AND i.tenant_id = $2
			  AND ica.cloud_account_id = $3
			  AND i.source = $4
			  AND i.status != 'disabled'
		)`

	// Active row exists
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("loki", "tenant-1", "acc-1", "user").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	exists, err := hasIntegrationWithSource(ctx, "acc-1", "loki", "user")
	require.NoError(t, err)
	assert.True(t, exists)

	// Disabled row only -> query returns exists=false
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("loki", "tenant-1", "acc-1", "agent").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	exists, err = hasIntegrationWithSource(ctx, "acc-1", "loki", "agent")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveProviderSource_DisabledAgentRoutesToUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	ctx := newTestRequestContext("tenant-1")

	expectedQuery := `SELECT EXISTS (
			SELECT 1
			FROM integrations i
			JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
			WHERE i.type = $1
			  AND i.tenant_id = $2
			  AND ica.cloud_account_id = $3
			  AND i.source = $4
			  AND i.status != 'disabled'
		)`

	// 1st call for "user" -> returns true
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("loki", "tenant-1", "acc-1", "user").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	// 2nd call for "agent" (disabled in DB -> returns false)
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("loki", "tenant-1", "acc-1", "agent").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	src := resolveProviderSource(ctx, "loki", "agent", "acc-1")
	assert.Equal(t, "user", src, "disabled agent integration must allow user-sourced Loki routing")
	require.NoError(t, mock.ExpectationsWereMet())
}
