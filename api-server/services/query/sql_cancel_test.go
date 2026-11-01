package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setCancelOnDisconnect flips app_database_query_cancel_on_disconnect for one test.
func setCancelOnDisconnect(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.ServiceDBQueryCancelOnDisconnect
	config.Config.ServiceDBQueryCancelOnDisconnect = on
	t.Cleanup(func() { config.Config.ServiceDBQueryCancelOnDisconnect = prev })
}

// useMetastore points the query executor at db for one test.
func useMetastore(t *testing.T, db *sqlx.DB) {
	t.Helper()
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
}

func TestSqlQueryParent(t *testing.T) {
	gone, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("off: the caller's context is ignored, as before", func(t *testing.T) {
		setCancelOnDisconnect(t, false)
		assert.NoError(t, sqlQueryParent(gone).Err())
	})

	t.Run("on: the query runs under the caller's context", func(t *testing.T) {
		setCancelOnDisconnect(t, true)
		assert.ErrorIs(t, sqlQueryParent(gone).Err(), context.Canceled)
	})

	t.Run("on: a caller with no context still gets one", func(t *testing.T) {
		setCancelOnDisconnect(t, true)
		//nolint:staticcheck // a nil context is exactly the input under test
		assert.NoError(t, sqlQueryParent(nil).Err())
	})

	t.Run("no ceiling configured still honours the caller", func(t *testing.T) {
		prev := config.Config.ServiceDBQueryTimeoutSeconds
		config.Config.ServiceDBQueryTimeoutSeconds = 0
		t.Cleanup(func() { config.Config.ServiceDBQueryTimeoutSeconds = prev })

		ctx, release := sqlQueryContext(gone)
		defer release()
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	})
}

// A cancelled query must come back as ErrQueryCancelled whatever error the driver
// chose to report it with. sqlmock, like lib/pq, answers with its own error rather
// than context.Canceled, which is the case errors.Is(err, context.Canceled) misses.
func TestExecuteSqlQueryClassifiesCancellation(t *testing.T) {
	newMock := func(t *testing.T) sqlmock.Sqlmock {
		t.Helper()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		useMetastore(t, sqlx.NewDb(db, "postgresql"))
		return mock
	}

	t.Run("on: the caller leaving mid-query stops it and is reported as cancelled", func(t *testing.T) {
		setCancelOnDisconnect(t, true)
		mock := newMock(t)
		mock.ExpectQuery("select 1").WillDelayFor(5 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))

		caller, leave := context.WithCancel(context.Background())
		time.AfterFunc(30*time.Millisecond, leave)

		started := time.Now()
		rows, err := executeSqlQuery(caller, database.Metastore, "select 1", nil, 1)

		assert.Nil(t, rows)
		require.ErrorIs(t, err, ErrQueryCancelled)
		assert.ErrorIs(t, err, context.Canceled, "a generic context check must still recognise it")
		assert.Less(t, time.Since(started), 2*time.Second, "the query must stop when the caller leaves, not run to the end")
	})

	t.Run("on: a query that fails while the caller is still there is a plain failure", func(t *testing.T) {
		setCancelOnDisconnect(t, true)
		mock := newMock(t)
		mock.ExpectQuery("select 1").WillReturnError(fmt.Errorf("relation does not exist"))

		_, err := executeSqlQuery(context.Background(), database.Metastore, "select 1", nil, 1)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrQueryCancelled)
	})

	t.Run("off: the caller leaving does not stop the query", func(t *testing.T) {
		setCancelOnDisconnect(t, false)
		mock := newMock(t)
		mock.ExpectQuery("select 1").WillDelayFor(150 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))

		caller, leave := context.WithCancel(context.Background())
		time.AfterFunc(30*time.Millisecond, leave)

		rows, err := executeSqlQuery(caller, database.Metastore, "select 1", nil, 1)

		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})
}

// ExecuteQuery must hand a cancellation to its caller as ErrQueryCancelled, so the
// RPC handler can answer 499 instead of the 400 it gives a failed query.
func TestExecuteQueryReturnsCancellation(t *testing.T) {
	setCancelOnDisconnect(t, true)
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(string, string) error { return nil })))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	useMetastore(t, sqlx.NewDb(db, "postgresql"))
	mock.ExpectQuery("").WillDelayFor(5 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"account_id"}))

	caller, leave := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, leave)
	reqCtx := security.NewRequestContext(caller, security.NewSecurityContextForSuperAdmin(), nil, nil, nil)

	started := time.Now()
	resp, err := ExecuteQuery(reqCtx, QueryRequest{
		Table:   "events_v2",
		Columns: fromStringColsToQueryCols([]string{"account_id"}),
		Limit:   10,
	})

	require.ErrorIs(t, err, ErrQueryCancelled)
	assert.NotEmpty(t, resp.Errors, "a caller that only reads resp.Errors must still see the query did not answer")
	assert.Empty(t, resp.Rows)
	assert.Less(t, time.Since(started), 2*time.Second)
}

// TestExecuteSqlQueryCancelStopsPostgres_DB checks the part no mock can: that
// lib/pq really cancels the statement on the server, that the error it returns
// for that is still classified as a cancellation, and that the pool keeps working
// afterwards. DB-gated: skips when no database is reachable.
func TestExecuteSqlQueryCancelStopsPostgres_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	useMetastore(t, db)
	setCancelOnDisconnect(t, true)

	// A marker only this run's statement carries, to find it in pg_stat_activity.
	marker := "nb39740_" + uuid.NewString()[:8]
	slow := fmt.Sprintf("select pg_sleep(20) /* %s */", marker)
	stillRunning := func() int {
		var n int
		require.NoError(t, db.Get(&n,
			`select count(*) from pg_stat_activity where pid <> pg_backend_pid() and state = 'active' and query like '%' || $1 || '%'`, marker))
		return n
	}

	caller, leave := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, leave)

	started := time.Now()
	rows, err := executeSqlQuery(caller, database.Metastore, slow, nil, 1)

	assert.Nil(t, rows)
	require.ErrorIs(t, err, ErrQueryCancelled, "got: %v", err)
	assert.Less(t, time.Since(started), 10*time.Second, "the statement must stop when the caller leaves, not sleep for 20 s")
	assert.False(t, errors.Is(err, context.DeadlineExceeded))

	// The server must have stopped it too, not just the client.
	assert.Eventually(t, func() bool { return stillRunning() == 0 }, 5*time.Second, 100*time.Millisecond,
		"Postgres is still running the statement after the caller left")

	// And the pool must still serve the next caller.
	again, err := executeSqlQuery(context.Background(), database.Metastore, "select 1 as n", nil, 1)
	require.NoError(t, err)
	assert.Len(t, again, 1)
}
