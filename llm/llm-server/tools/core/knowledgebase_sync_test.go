package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// TestReconcileIntegrationKBs_MatchesOnIntegrationAndAccountPair guards the
// unlink case: an integration still linked to one account must not keep the
// KB row of an account it was unlinked from active. The archive and
// reactivate statements therefore have to correlate on the
// (integration_id, account_id) pair, passed as two positionally paired
// arrays, not on the integration id alone.
func TestReconcileIntegrationKBs_MatchesOnIntegrationAndAccountPair(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	eligible := []integrationKBRow{
		{IntegrationID: "int-1", CloudAccountID: "acct-a"},
		{IntegrationID: "int-1", CloudAccountID: "acct-b"},
		{IntegrationID: "int-2", CloudAccountID: "acct-a"},
	}
	integrationIDs := pq.Array([]string{"int-1", "int-1", "int-2"})
	accountIDs := pq.Array([]string{"acct-a", "acct-b", "acct-a"})

	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'archived'.*NOT EXISTS \(SELECT 1 FROM unnest\(\$1::uuid\[\], \$2::uuid\[\]\).*e\.integration_id = kb\.integration_id AND e\.account_id = kb\.account_id`).
		WithArgs(integrationIDs, accountIDs).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'active'.*status = 'archived' AND EXISTS \(SELECT 1 FROM unnest\(\$1::uuid\[\], \$2::uuid\[\]\)`).
		WithArgs(integrationIDs, accountIDs).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE llm_knowledgebases SET status = 'error'.*status = 'processing'`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	archived, reactivated, recovered := reconcileIntegrationKBs(dbms, eligible)

	assert.Equal(t, 1, archived)
	assert.Equal(t, 2, reactivated)
	assert.Equal(t, 0, recovered)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReconcileIntegrationKBs_EmptyEligibleSetSkipsArchive keeps the
// transient-gap guard: with no eligible rows only the stuck-processing
// recovery runs, nothing is archived or reactivated.
func TestReconcileIntegrationKBs_EmptyEligibleSetSkipsArchive(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	mock.ExpectExec(`UPDATE llm_knowledgebases SET status = 'error'.*status = 'processing'`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 3))

	archived, reactivated, recovered := reconcileIntegrationKBs(dbms, nil)

	assert.Equal(t, 0, archived)
	assert.Equal(t, 0, reactivated)
	assert.Equal(t, 3, recovered)
	assert.NoError(t, mock.ExpectationsWereMet())
}
