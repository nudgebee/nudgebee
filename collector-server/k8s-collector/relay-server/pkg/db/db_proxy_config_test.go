package db

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectProxyIntegrationRows scripts QueryProxyDatasources' two statements for one
// integration: the integration list, then that integration's config values.
func expectProxyIntegrationRows(mock sqlmock.Sqlmock, id, typ, name string, config [][3]any) {
	mock.ExpectQuery(`FROM integrations i`).
		WithArgs("acct-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name"}).AddRow(id, typ, name))
	rows := sqlmock.NewRows([]string{"name", "value", "is_encrypted"})
	for _, c := range config {
		rows.AddRow(c[0], c[1], c[2])
	}
	mock.ExpectQuery(`FROM integration_config_values`).WithArgs(id).WillReturnRows(rows)
}

// The sync built on a forager's (re)connect replaces its whole datasource list, so a type
// missing from this query was removed from the forager on every reconnect. Prometheus,
// Loki and SigNoz rows were missing, which dropped a VM account's hand-added metrics and
// logs datasources whenever the forager restarted.
func TestQueryProxyDatasources_ListsHTTPProxyTypes(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectQuery(`i\.type IN \('postgresql'.*'rabbitmq', 'prometheus', 'loki', 'signoz'\)`).
		WithArgs("acct-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name"}))

	_, err := store.QueryProxyDatasources(context.Background(), "acct-1")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryProxyDatasources_Prometheus(t *testing.T) {
	store, mock := newMockStore(t)
	expectProxyIntegrationRows(mock, "int-prom", "prometheus", "vm fleet prom", [][3]any{
		{"connection_mode", "vm_agent", false},
		{"prometheus_url", "https://prom.corp:9090/", false},
		{"auth_type", "basic", false},
		{"prometheus_username", "writer", false},
		{"prometheus_password", "s3cret", false},
		{"prometheus_additional_labels", `cluster="x"`, false},
		{"default_metrics_provider", "true", false},
	})

	got, err := store.QueryProxyDatasources(context.Background(), "acct-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	ds := got[0]
	assert.Equal(t, "int-prom", ds.ID)
	assert.Equal(t, "prometheus", ds.Type)
	assert.Equal(t, "http-proxy", ds.ProxyType)
	assert.Equal(t, map[string]any{"base_url": "https://prom.corp:9090", "auth_type": "basic"}, ds.Config)
	assert.Equal(t, map[string]string{"username": "writer", "password": "s3cret"}, ds.Credentials)
	assert.Equal(t, "cloud_push", ds.CredentialSource)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryProxyDatasources_LokiAndSignozAndRabbitMQ(t *testing.T) {
	cases := []struct {
		name, typ, wantProxy string
		config               [][3]any
		wantConfig           map[string]any
		wantCreds            map[string]string
	}{
		{
			name: "loki", typ: "loki", wantProxy: "http-proxy",
			config: [][3]any{
				{"connection_mode", "vm_agent", false},
				{"loki_url", "https://loki.corp:3100/", false},
				{"loki_auth_type", "bearer_token", false},
				{"loki_bearer_token", "tok", false},
				{"loki_tenant_id", "team-a", false},
				{"default_log_provider", "true", false},
			},
			wantConfig: map[string]any{"base_url": "https://loki.corp:3100", "auth_type": "bearer"},
			wantCreds:  map[string]string{"bearer_token": "tok"},
		},
		{
			name: "signoz", typ: "signoz", wantProxy: "http-proxy",
			config: [][3]any{
				{"connection_mode", "vm_agent", false},
				{"signoz_url", "http://10.0.0.9:3301/", false},
				{"signoz_username", "ops@corp.example", false},
				{"signoz_password", "hunter2", false},
			},
			wantConfig: map[string]any{"base_url": "http://10.0.0.9:3301"},
			wantCreds:  map[string]string{},
		},
		{
			name: "rabbitmq", typ: "rabbitmq", wantProxy: "rabbitmq-proxy",
			config:     [][3]any{{"connection_mode", "vm_agent", false}, {"host", "mq.corp", false}},
			wantConfig: map[string]any{"host": "mq.corp"},
			wantCreds:  map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newMockStore(t)
			expectProxyIntegrationRows(mock, "int-"+tc.name, tc.typ, tc.name, tc.config)

			got, err := store.QueryProxyDatasources(context.Background(), "acct-1")
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, tc.typ, got[0].Type)
			assert.Equal(t, tc.wantProxy, got[0].ProxyType)
			assert.Equal(t, tc.wantConfig, got[0].Config)
			assert.Equal(t, tc.wantCreds, got[0].Credentials)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestNormalizePrometheusProxyDatasource_Auth(t *testing.T) {
	t.Run("bearer", func(t *testing.T) {
		cfg := map[string]any{"prometheus_url": "http://p:9090", "auth_type": "bearer_token", "prometheus_bearer_token": "tok"}
		creds := map[string]string{}
		normalizePrometheusProxyDatasource(cfg, creds)
		assert.Equal(t, "bearer", cfg["auth_type"])
		assert.Equal(t, "tok", creds["bearer_token"])
		assert.NotContains(t, cfg, "prometheus_bearer_token", "a secret must not stay in the plain config")
	})
	t.Run("none sends no auth_type", func(t *testing.T) {
		cfg := map[string]any{"prometheus_url": "http://p:9090", "auth_type": "none"}
		normalizePrometheusProxyDatasource(cfg, map[string]string{})
		assert.NotContains(t, cfg, "auth_type")
	})
}

func TestNormalizeLokiProxyDatasource_NoAuth(t *testing.T) {
	cfg := map[string]any{"loki_url": "http://l:3100", "loki_auth_type": "none"}
	creds := map[string]string{}
	normalizeLokiProxyDatasource(cfg, creds)
	assert.Equal(t, map[string]any{"base_url": "http://l:3100"}, cfg)
	assert.Empty(t, creds)
}
