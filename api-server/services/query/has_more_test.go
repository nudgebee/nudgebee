package query

import (
	"context"
	"testing"

	"nudgebee/services/security"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteQueryHasMore checks the #26060 pagination-truncation fix: ExecuteQuery
// asks for one row beyond the effective limit so it can tell a full page apart
// from a truncated one, and trims that extra row back off before returning.
func TestExecuteQueryHasMore(t *testing.T) {
	newMock := func(t *testing.T, rows int) sqlmock.Sqlmock {
		t.Helper()
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(string, string) error { return nil })))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		useMetastore(t, sqlx.NewDb(db, "postgresql"))

		result := sqlmock.NewRows([]string{"account_id"})
		for i := 0; i < rows; i++ {
			result.AddRow("acct")
		}
		mock.ExpectQuery("").WillReturnRows(result)
		return mock
	}
	reqCtx := func() *security.RequestContext {
		return security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), nil, nil, nil)
	}

	t.Run("more rows than the explicit limit: truncated and flagged", func(t *testing.T) {
		newMock(t, 3) // limit 2 + 1 overfetch row
		resp, err := ExecuteQuery(reqCtx(), QueryRequest{
			Table:   "events_v2",
			Columns: fromStringColsToQueryCols([]string{"account_id"}),
			Limit:   2,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Rows, 2, "the overfetch row must not reach the caller")
		assert.True(t, resp.HasMore)
	})

	t.Run("fewer rows than the explicit limit: not flagged", func(t *testing.T) {
		newMock(t, 2)
		resp, err := ExecuteQuery(reqCtx(), QueryRequest{
			Table:   "events_v2",
			Columns: fromStringColsToQueryCols([]string{"account_id"}),
			Limit:   5,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Rows, 2)
		assert.False(t, resp.HasMore)
	})

	t.Run("exactly the explicit limit: not flagged", func(t *testing.T) {
		newMock(t, 2)
		resp, err := ExecuteQuery(reqCtx(), QueryRequest{
			Table:   "events_v2",
			Columns: fromStringColsToQueryCols([]string{"account_id"}),
			Limit:   2,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Rows, 2)
		assert.False(t, resp.HasMore)
	})

	t.Run("no limit given: falls back to the 1000-row default for the has_more check", func(t *testing.T) {
		newMock(t, 1001)
		resp, err := ExecuteQuery(reqCtx(), QueryRequest{
			Table:   "events_v2",
			Columns: fromStringColsToQueryCols([]string{"account_id"}),
		})
		require.NoError(t, err)
		assert.Len(t, resp.Rows, 1000)
		assert.True(t, resp.HasMore)
	})
}
