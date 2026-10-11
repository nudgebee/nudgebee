package core

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// An integration has one organisation knowledge base (#40605). The reconcile
// archives and reactivates those rows by integration id alone and never touches
// a per-account row: the migration folds those and copies their status.
func TestReconcileIntegrationKBs_OrganisationRowsByIntegration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	ids := pq.Array([]string{"int-1", "int-2"})
	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'archived'.*kb\.account_id IS NULL.*NOT \(kb\.integration_id = ANY\(\$1::uuid\[\]\)\)`).
		WithArgs(ids, "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'active'.*kb\.account_id IS NULL AND kb\.status = 'archived'.*kb\.integration_id = ANY\(\$1::uuid\[\]\)`).
		WithArgs(ids, "").
		WillReturnResult(sqlmock.NewResult(0, 2))

	archived, reactivated := reconcileIntegrationKBs(context.Background(), dbms, []string{"int-1", "int-2"}, "")

	assert.Equal(t, 1, archived)
	assert.Equal(t, 2, reactivated)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The per-tenant fast path limits the archive and reactivate passes to the
// tenant; with nothing eligible every integration KB of the tenant is archived.
func TestReconcileIntegrationKBs_ScopedToTenant(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	none := pq.Array([]string{})
	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'archived'.*\(\$2 = '' OR kb\.tenant_id::text = \$2\)`).
		WithArgs(none, "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`UPDATE llm_knowledgebases kb SET status = 'active'`).
		WithArgs(none, "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	archived, reactivated := reconcileIntegrationKBs(context.Background(), dbms, []string{}, "tenant-1")

	assert.Equal(t, 3, archived)
	assert.Equal(t, 0, reactivated)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An existing organisation row is looked up by integration with no account, and
// a rename refreshes it in place instead of inserting a second row.
func TestEnsureKBForIntegration_ExistingOrganisationRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	mock.ExpectQuery(`SELECT id FROM llm_knowledgebases\s+WHERE integration_id = \$1 AND account_id IS NULL`).
		WithArgs("int-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kb-1"))
	mock.ExpectExec(`UPDATE llm_knowledgebases\s+SET name = \$1, description = \$2`).
		WithArgs("Docs", "Knowledge Base for integration: Docs", "kb-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	got := ensureKBForIntegration(context.Background(), dbms, integrationKBRow{
		IntegrationID: "int-1", IntegrationType: "confluence", TenantID: "tenant-1", IntegrationName: "Docs",
	})

	assert.Equal(t, kbEnsureSkipped, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureKBForIntegration_RefusesEmptyTenant(t *testing.T) {
	got := ensureKBForIntegration(context.Background(), &common.DatabaseManager{}, integrationKBRow{IntegrationID: "int-1"})
	assert.Equal(t, kbEnsureErrored, got)
}
