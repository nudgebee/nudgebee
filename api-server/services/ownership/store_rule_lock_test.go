package ownership

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSingleConnMock returns a pool capped at exactly one connection.
//
// withTenantRuleLock opens a transaction purely to take pg_advisory_xact_lock, so
// that connection is checked out for the whole callback. Capping the pool at one
// turns "the callback also wants a connection" from latent pool pressure into a
// deterministic hang, which is what makes the defect in #35944 observable.
func newSingleConnMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	db := sqlx.NewDb(raw, "postgres")
	db.SetMaxOpenConns(1)
	return db, mock
}

func lockTestRequest() UpsertRuleRequest {
	return UpsertRuleRequest{
		Name:       "payments-owner",
		MatchScope: MatchScopeNamespace,
		MatchValue: "payments",
		OwnerType:  "team",
		OwnerId:    "22222222-2222-2222-2222-222222222222",
	}
}

// ownershipRuleColumns mirrors listRuleRows' projection so an empty result set
// still type-checks through StructScan.
var ownershipRuleColumns = []string{
	"id", "tenant_id", "name", "resource_domain", "match_scope", "match_key", "match_value",
	"cloud_account_id", "owner_type", "owner_id", "priority", "enabled", "created_by",
	"updated_by", "created_at", "updated_at",
}

// The production shape: the conflict check and the write both run on the locking
// tx, so UpsertRule holds exactly one connection and leaves none idle in
// transaction. On a single-connection pool this completes; it could not if either
// statement went to the pooled handle.
func TestWithTenantRuleLockRunsWorkOnTheLockingTx(t *testing.T) {
	db, mock := newSingleConnMock(t)
	const tenantID = "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FROM ownership_rules WHERE tenant_id`).
		WillReturnRows(sqlmock.NewRows(ownershipRuleColumns))
	mock.ExpectQuery(`INSERT INTO ownership_rules`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333333"))
	mock.ExpectCommit()

	var id string
	errCh := make(chan error, 1)
	go func() {
		errCh <- withTenantRuleLock(db, tenantID, func(tx *sqlx.Tx) error {
			conflict, cErr := findConflictingRule(tx, tenantID, lockTestRequest(), "")
			if cErr != nil {
				return cErr
			}
			assert.Nil(t, conflict) // assert, not require: FailNow is unsafe off the test goroutine
			newID, uErr := upsertRuleRow(tx, tenantID, lockTestRequest(), "44444444-4444-4444-4444-444444444444")
			id = newID
			return uErr
		})
	}()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("withTenantRuleLock blocked: the callback asked the pool for a second connection while the lock tx held the only one")
	}

	assert.Equal(t, "33333333-3333-3333-3333-333333333333", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The defect this PR removes, pinned so it cannot come back: running the callback's
// work against the pooled handle instead of the tx leaves the lock connection idle
// in transaction while a second connection does the work. Here the second
// connection can never come free, so the call hangs — the same starvation that
// happens on a real pool once enough tenant-admins upsert concurrently.
func TestRuleLockCallbackOnPooledHandleStarvesTheConnection(t *testing.T) {
	db, mock := newSingleConnMock(t)
	const tenantID = "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FROM ownership_rules WHERE tenant_id`).
		WillReturnRows(sqlmock.NewRows(ownershipRuleColumns))
	mock.ExpectCommit()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Deliberately uses db, not the tx handed to the callback.
		_ = withTenantRuleLock(db, tenantID, func(_ *sqlx.Tx) error {
			_, err := findConflictingRule(db, tenantID, lockTestRequest(), "")
			return err
		})
	}()

	select {
	case <-done:
		t.Fatal("expected the pooled-handle callback to starve on the single-connection pool; it completed, so this test no longer proves anything")
	case <-time.After(2 * time.Second):
		// Still blocked, as expected. t.Cleanup closes the DB, which releases it.
	}
}
