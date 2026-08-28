package core

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchByIdQuery loosely matches ListLinkedCloudAccountIDsByIntegrationID's single
// SQL branch. What matters is that it filters on i.id (not i.name) — a name-based
// lookup silently misses when the same save renames the integration, which is the
// whole reason this function exists.
var matchByIdQuery = regexp.MustCompile(`(?s)SELECT ica\.cloud_account_id::text.*FROM integrations i.*JOIN integrations_cloud_accounts ica.*i\.tenant_id = \$1.*i\.id = \$2`)

func TestListLinkedCloudAccountIDsByIntegrationID(t *testing.T) {
	const integrationId = "11111111-1111-1111-1111-111111111111"

	t.Run("returns every linked account", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"cloud_account_id"}).
			AddRow("acc-1").
			AddRow("acc-2")
		pkgMock.ExpectQuery(matchByIdQuery.String()).
			WithArgs(testTenant, integrationId).
			WillReturnRows(rows)

		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), integrationId)
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-1", "acc-2"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("empty integrationId issues no query (the create case)", func(t *testing.T) {
		// No ExpectQuery is registered: if the function queried anyway, sqlmock
		// would fail the call. This is the create path — nothing linked yet, and
		// paying for a round-trip on every fresh integration save is pure waste.
		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), "")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("zero matches returns empty, not error", func(t *testing.T) {
		pkgMock.ExpectQuery(matchByIdQuery.String()).
			WithArgs(testTenant, integrationId).
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}))

		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), integrationId)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("tenant scoping is applied via SQL arg", func(t *testing.T) {
		// WithArgs fails the test if a different tenant leaks into the query —
		// an integration id from another tenant must resolve to nothing.
		pkgMock.ExpectQuery(matchByIdQuery.String()).
			WithArgs(testTenant, integrationId).
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}).AddRow("acc-of-test-tenant"))

		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), integrationId)
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-of-test-tenant"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		dbErr := errors.New("connection refused")
		pkgMock.ExpectQuery(matchByIdQuery.String()).
			WithArgs(testTenant, integrationId).
			WillReturnError(dbErr)

		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), integrationId)
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query failed")
		assert.ErrorIs(t, err, dbErr, "must wrap the underlying error so callers can errors.Is it")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"cloud_account_id"}).
			AddRow(nil)
		pkgMock.ExpectQuery(matchByIdQuery.String()).
			WithArgs(testTenant, integrationId).
			WillReturnRows(rows)

		got, err := ListLinkedCloudAccountIDsByIntegrationID(ctxForTenant(t, testTenant), integrationId)
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scan failed")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}
