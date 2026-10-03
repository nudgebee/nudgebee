package core

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An account can hold two integrations of one type — the k8s agent registers
// its own Prometheus row beside a connection somebody added by hand. The
// metrics/logs/traces resolver must pick the one the account selected for that
// telemetry kind, not guess by source, and must never let a caller-supplied
// column name reach the SQL.
var (
	matchDefaultOrdered = regexp.MustCompile(`(?s)SELECT i\.id, i\.name, i\.source, i\.type.*FROM integrations i.*ORDER BY \(ica\.default_metrics_provider = true\) DESC,\s*CASE WHEN i\.source = 'user' THEN 0 ELSE 1 END.*LIMIT 1`)
	matchPlainByType    = regexp.MustCompile(`(?s)SELECT i\.id, i\.name, i\.source, i\.type.*FROM integrations i.*AND i\.status != 'disabled'\s*LIMIT 1`)
)

func integrationRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "source", "type"}).
		AddRow("int-user", "prometheus SAAS", "user", "prometheus")
}

// Each subtest uses its own account id: the shared GetIntegrationByType caches
// per (account, type, tenant), so reusing one id would serve a cached row and
// the query expectation would never be met.
func TestGetIntegrationByTypePreferringDefault(t *testing.T) {
	t.Run("orders by the account's chosen provider, then hand-added as tie-break", func(t *testing.T) {
		pkgMock.ExpectQuery(matchDefaultOrdered.String()).WillReturnRows(integrationRows())

		got, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-ordered", "prometheus", "default_metrics_provider")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "int-user", got.Id)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("an unknown column never reaches the SQL — falls back to the shared lookup", func(t *testing.T) {
		// The fallback must be the plain by-type query: no ORDER BY, and above
		// all no interpolation of the caller's string.
		pkgMock.ExpectQuery(matchPlainByType.String()).WillReturnRows(integrationRows())

		got, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-inject", "prometheus", "1=1; DROP TABLE integrations--")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("an empty column falls back rather than emitting a broken ORDER BY", func(t *testing.T) {
		pkgMock.ExpectQuery(matchPlainByType.String()).WillReturnRows(integrationRows())

		_, err := GetIntegrationByTypePreferringDefault(ctxForTenant(t, testTenant), "acc-empty-col", "prometheus", "")
		require.NoError(t, err)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("no matching integration is not an error", func(t *testing.T) {
		pkgMock.ExpectQuery(matchDefaultOrdered.String()).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "source", "type"}))

		got, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-none", "prometheus", "default_metrics_provider")
		require.NoError(t, err)
		assert.Nil(t, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("a second call for the same account is served from the cache", func(t *testing.T) {
		// The knowledge-graph sweep resolves a provider per load balancer and per
		// DNS record, so one uncached join per call would be a real DB load.
		pkgMock.ExpectQuery(matchDefaultOrdered.String()).WillReturnRows(integrationRows())
		first, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-cached", "prometheus", "default_metrics_provider")
		require.NoError(t, err)
		require.NotNil(t, first)

		// No second ExpectQuery is registered: another DB round trip would fail here.
		second, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-cached", "prometheus", "default_metrics_provider")
		require.NoError(t, err)
		assert.Equal(t, first, second)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("the cache key includes the ordering column, so variants never collide", func(t *testing.T) {
		pkgMock.ExpectQuery(matchDefaultOrdered.String()).WillReturnRows(integrationRows())
		_, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-variant", "prometheus", "default_metrics_provider")
		require.NoError(t, err)

		// Same account and type, different ordering column: must re-query rather
		// than serve the row cached under the metrics ordering.
		pkgMock.ExpectQuery(`(?s)ORDER BY \(ica\.default_log_provider = true\) DESC`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "source", "type"}).
				AddRow("int-logs", "loki", "agent", "loki"))
		got, err := GetIntegrationByTypePreferringDefault(
			ctxForTenant(t, testTenant), "acc-variant", "prometheus", "default_log_provider")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "int-logs", got.Id, "must not serve the metrics-ordered cache entry")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("every telemetry column is accepted", func(t *testing.T) {
		for _, col := range []string{"default_log_provider", "default_traces_provider", "default_metrics_provider", "default_llm_provider"} {
			assert.True(t, defaultProviderColumns[col], "%s must be allow-listed", col)
		}
		assert.False(t, defaultProviderColumns["tenant_id"], "only the default_* flags may be ordered on")
	})
}
