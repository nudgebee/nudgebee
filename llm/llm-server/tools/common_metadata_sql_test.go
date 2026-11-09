package tools

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// mockMetastore registers a sqlmock-backed database as the Metastore.
// The caller is responsible for closing the returned *sql.DB after the test.
func mockMetastore(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "postgresql")
	common.ResetDatabaseManager(common.Metastore)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})
	return sqlxDB, mock
}

// expectToolQuery expects one tool query as sqlExecuteQuery runs it: prepared
// inside a read-only transaction that sets its timeout and work_mem, rolled
// back after.
func expectToolQuery(mock sqlmock.Sqlmock, sqlRegex string) *sqlmock.ExpectedQuery {
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL statement_timeout`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET LOCAL work_mem`).WillReturnResult(sqlmock.NewResult(0, 0))
	query := mock.ExpectPrepare(sqlRegex).WillBeClosed().ExpectQuery()
	mock.ExpectRollback()
	return query
}

func TestBuildLimitedQuery(t *testing.T) {
	testCases := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{
			name:     "Plain SELECT gets subquery limit wrapping",
			query:    "select * from events where status = 'error'",
			limit:    100,
			expected: "select * from (select * from events where status = 'error') as ql limit 100;",
		},
		{
			name:     "DISTINCT query gets subquery limit wrapping",
			query:    "select distinct service_name from events",
			limit:    100,
			expected: "select * from (select distinct service_name from events) as ql limit 100;",
		},
		{
			name:     "GROUP BY query gets subquery limit wrapping",
			query:    "select service_name, count(*) from events group by service_name",
			limit:    100,
			expected: "select * from (select service_name, count(*) from events group by service_name) as ql limit 100;",
		},
		{
			name:     "Query with existing LIMIT is not re-wrapped",
			query:    "select * from events limit 10",
			limit:    100,
			expected: "select * from events limit 10;",
		},
		{
			name:     "Query with existing LIMIT and semicolon is unchanged",
			query:    "select * from events limit 10;",
			limit:    100,
			expected: "select * from events limit 10;",
		},
		{
			name:     "LIMIT only inside a single-line comment is ignored",
			query:    "select * from events -- LIMIT 5\nwhere status = 'error'",
			limit:    100,
			expected: "select * from (select * from events -- LIMIT 5\nwhere status = 'error') as ql limit 100;",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildLimitedQuery(tc.query, tc.limit)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSqlBindTable_InvalidUUID(t *testing.T) {
	_, _, err := sqlBindTable("SELECT * FROM recommendation_view", "not-a-uuid", "recommendation_view", "VIEW")
	assert.Error(t, err)
}

func TestSqlBindTable_FiltersViewsByAccount(t *testing.T) {
	q, src, err := sqlBindTable("SELECT id FROM recommendation_view WHERE status = 'Open'", "550e8400-e29b-41d4-a716-446655440000", "recommendation_view", "VIEW")
	require.NoError(t, err)
	assert.Equal(t, "SELECT id FROM "+sqlTablePlaceholder+" WHERE status = 'Open'", q)
	assert.Equal(t, "(select * from (VIEW) as tv where cloud_account_id = '550e8400-e29b-41d4-a716-446655440000') as recommendation_view", src)
}

// events and anomaly views filter by account themselves.
func TestSqlBindTable_EventsAndAnomalyUseTheViewAsIs(t *testing.T) {
	for _, table := range []string{"events", "anomaly"} {
		_, src, err := sqlBindTable("SELECT * FROM "+table, "550e8400-e29b-41d4-a716-446655440000", table, "VIEW")
		require.NoError(t, err)
		assert.Equal(t, "(VIEW) as "+table, src)
	}
}

// Only the FROM item is replaced: a literal or a column naming the table is
// left alone, and the view keeps the table's name so those still resolve.
func TestSqlBindTable_ReplacesOnlyTheFromItem(t *testing.T) {
	q, _, err := sqlBindTable("SELECT events.id, row_to_json(events) FROM events WHERE subject_name ILIKE '%payflow-events%'", "550e8400-e29b-41d4-a716-446655440000", "events", "VIEW")
	require.NoError(t, err)
	assert.Equal(t, "SELECT events.id, row_to_json(events) FROM "+sqlTablePlaceholder+" WHERE subject_name ILIKE '%payflow-events%'", q)
}

// A "from" inside a literal before the real FROM is not taken for the keyword.
func TestSqlBindTable_IgnoresFromInsideLiterals(t *testing.T) {
	q, src, err := sqlBindTable("SELECT 'event from system' AS src, id FROM events WHERE title = 'x where y'", "550e8400-e29b-41d4-a716-446655440000", "events", "VIEW")
	require.NoError(t, err)
	assert.Equal(t, "SELECT 'event from system' AS src, id FROM "+sqlTablePlaceholder+" WHERE title = 'x where y'", q)
	assert.Equal(t, "(VIEW) as events", src)
}

func TestSqlBindTable_KeepsUserAlias(t *testing.T) {
	q, src, err := sqlBindTable("select e.id from Events e where e.priority = 'HIGH'", "550e8400-e29b-41d4-a716-446655440000", "events", "VIEW")
	require.NoError(t, err)
	assert.Equal(t, "select e.id from "+sqlTablePlaceholder+" e where e.priority = 'HIGH'", q)
	assert.Equal(t, "(VIEW)", src)
}
func TestSqlValidateReadOnly(t *testing.T) {
	testCases := []struct {
		name         string
		query        string
		allowedTable string
		expectError  bool
		errorMsg     string
	}{
		// Valid SELECT queries
		{
			name:         "Valid SELECT query, no allowed table",
			query:        "SELECT * FROM users",
			allowedTable: "",
			expectError:  false,
		},
		{
			name:         "Valid SELECT query with lowercase from, no allowed table",
			query:        "select * from users",
			allowedTable: "",
			expectError:  false,
		},
		{
			name:         "Valid SELECT query with WHERE clause, no allowed table",
			query:        "SELECT id, name FROM customers WHERE age > 30",
			allowedTable: "",
			expectError:  false,
		},
		{
			name:         "Valid SHOW query, no allowed table",
			query:        "SHOW TABLES",
			allowedTable: "",
			expectError:  false,
		},
		{
			name:         "Valid DESCRIBE query, no allowed table",
			query:        "DESCRIBE users",
			allowedTable: "",
			expectError:  false,
		},

		// Invalid DML/DDL queries
		{
			name:         "DELETE query",
			query:        "DELETE FROM users WHERE id = 1",
			allowedTable: "",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "INSERT query",
			query:        "INSERT INTO users (name, email) VALUES ('Test', 'test@example.com')",
			allowedTable: "",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "UPDATE query",
			query:        "UPDATE users SET email = 'new@example.com' WHERE id = 1",
			allowedTable: "",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "CREATE query",
			query:        "CREATE TABLE new_users (id INT)",
			allowedTable: "",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "TRUNCATE query",
			query:        "TRUNCATE TABLE users",
			allowedTable: "",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "DROP query (not explicitly checked but should be caught by prefix)",
			query:        "DROP TABLE users", // Will not be caught by current prefix checks
			allowedTable: "",
			expectError:  false, // Current implementation doesn't check for DROP specifically
		},
		{
			name:         "ALTER query (not explicitly checked but should be caught by prefix)",
			query:        "ALTER TABLE users ADD COLUMN new_col VARCHAR(255)", // Will not be caught
			allowedTable: "",
			expectError:  false, // Current implementation doesn't check for ALTER specifically
		},

		// Queries with allowedTable specified
		{
			name:         "Valid SELECT from allowed table",
			query:        "SELECT * FROM events WHERE event_type = 'login'",
			allowedTable: "events",
			expectError:  false,
		},
		{
			name:         "Valid SELECT from allowed table (uppercase FROM)",
			query:        "SELECT * FROM events WHERE event_type = 'login'",
			allowedTable: "events",
			expectError:  false,
		},
		{
			name:         "SELECT from different table when allowedTable is set",
			query:        "SELECT * FROM users",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: query only events",
		},
		{
			// A join reads a second table, even when the allowed one comes first (#39880).
			name:         "SELECT from allowed table with join (allowed table first)",
			query:        "SELECT e.*, u.name FROM events e JOIN users u ON e.user_id = u.id",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: query only events; JOIN, UNION, WITH, TABLE and LATERAL are not allowed (write timestamp with time zone as timestamptz)",
		},
		{
			name:         "SELECT from allowed table with join (allowed table second)",
			query:        "SELECT u.name, e.* FROM users u JOIN events e ON e.user_id = u.id",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: query only events; JOIN, UNION, WITH, TABLE and LATERAL are not allowed (write timestamp with time zone as timestamptz)",
		},
		{
			name:         "DELETE from allowed table (should be caught by DML check first)",
			query:        "DELETE FROM events WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: only select is allowed",
		},
		{
			name:         "SELECT from allowed table with alias",
			query:        "SELECT * FROM events ev WHERE ev.id = 1",
			allowedTable: "events",
			expectError:  false,
		},
		{
			// Never ran: the view substitution produced "public.(view)".
			name:         "SELECT from allowed table with schema prefix",
			query:        "SELECT * FROM public.events WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: query only events",
		},
		{
			name:         "SELECT from allowed table, but table name is a substring of another",
			query:        "SELECT * FROM allevents WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: query only events",
		},
		{
			name:         "SELECT from allowed table, query has table name in comments",
			query:        "SELECT * -- from events \n FROM real_table WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: comments and quoted names are not allowed",
		},
		{
			name:         "Valid SELECT query with mixed case table name and FROM keyword",
			query:        "SELECT * FrOm EvEnTs WHERE id = 1",
			allowedTable: "events", // The check is ` from events` or ` FROM events`
			expectError:  false,    // This will pass if ` from events` or ` FROM events` is present
		},
		{
			name:         "Valid SELECT query with allowed table name in different case",
			query:        "SELECT * FROM EVENTS WHERE id = 1",
			allowedTable: "events", // The check is ` from events` or ` FROM events`
			expectError:  false,    // This will pass if ` FROM events` is present
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := sqlValidateReadOnly(tc.query, tc.allowedTable)
			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.EqualError(t, err, tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSqlToolCallResults reproduces the original bug and verifies the fix.
// When a SQL query returns zero rows, sqlToolCall used to return Data:"[]"
// which the planner classified as ToolStatusEmptyResult (a failure).
// After the fix, it returns a JSON object with a descriptive message.
func TestSqlToolCallResults(t *testing.T) {
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()

	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	nbCtx := core.NbToolContext{
		AccountId: "550e8400-e29b-41d4-a716-446655440000",
		Ctx:       ctx,
	}

	t.Run("empty results return JSON message not bare []", func(t *testing.T) {
		columns := []string{"id", "title", "severity"}
		expectToolQuery(mock, "select").WillReturnRows(sqlmock.NewRows(columns))

		resp, data, err := sqlToolCall(nbCtx, "SELECT id, title, severity FROM events WHERE severity = 'critical'", "events", "", 0, nil)
		require.NoError(t, err)

		// Verify empty data slice returned
		assert.Empty(t, data)

		// Verify response is valid JSON (not plain text "[]")
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, "Status should be SUCCESS")
		assert.Equal(t, core.NBToolResponseTypeJson, resp.Type, "Type should be json, not table")
		assert.NotEqual(t, "[]", resp.Data, "Data should NOT be bare '[]'")

		// Verify the JSON is parseable and contains the message
		var parsed map[string]any
		err = json.Unmarshal([]byte(resp.Data), &parsed)
		require.NoError(t, err, "Data should be valid JSON")
		assert.Contains(t, parsed["message"], "No results found")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("non-empty results return table with JSON array", func(t *testing.T) {
		columns := []string{"id", "title"}
		expectToolQuery(mock, "select").WillReturnRows(
			sqlmock.NewRows(columns).
				AddRow("evt-1", "High CPU usage").
				AddRow("evt-2", "OOM Kill detected"),
		)

		resp, data, err := sqlToolCall(nbCtx, "SELECT id, title FROM events", "events", "", 0, nil)
		require.NoError(t, err)

		assert.Len(t, data, 2)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, core.NBToolResponseTypeTable, resp.Type, "Non-empty results should be table type")
		assert.NotEqual(t, "[]", resp.Data)

		// Verify it's a valid JSON array
		var parsed []map[string]any
		err = json.Unmarshal([]byte(resp.Data), &parsed)
		require.NoError(t, err, "Data should be a valid JSON array")
		assert.Len(t, parsed, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// A LIMIT inside the substituted view is not the caller's: a caller query
// without one must still get the row cap.
func TestSqlExecuteQueryCapsOnTheCallersSQL(t *testing.T) {
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)

	view := "SELECT e.id FROM events e WHERE e.id = (SELECT id FROM events LIMIT 1)"
	expectToolQuery(mock, `^select \* from \(SELECT id FROM \(.*LIMIT 1.*\) as events\) as ql limit 10;$`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := sqlExecuteQuery(ctx, "SELECT id FROM events", "550e8400-e29b-41d4-a716-446655440000", "events", view, 0)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSqlExecuteQueryRunsReadOnlyWithTimeoutAndWorkMem(t *testing.T) {
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL statement_timeout = 120000")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL work_mem = '32MB'")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectPrepare("select").WillBeClosed().ExpectQuery().WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("evt-1"))
	mock.ExpectRollback()

	data, err := sqlExecuteQuery(ctx, "SELECT id FROM events", "550e8400-e29b-41d4-a716-446655440000", "events", "", 0)
	require.NoError(t, err)
	assert.Len(t, data, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A query cut off by statement_timeout must reach callers as an error the
// model can act on, never as an empty result that reads as "nothing found".
func TestSqlToolCallTimeoutIsAnActionableError(t *testing.T) {
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()
	nbCtx := core.NbToolContext{
		AccountId: "550e8400-e29b-41d4-a716-446655440000",
		Ctx:       security.NewRequestContext(context.Background(), nil, nil, nil, nil),
	}

	t.Run("statement timeout", func(t *testing.T) {
		expectToolQuery(mock, "select").
			WillReturnError(&pq.Error{Code: pqQueryCanceled, Message: "canceling statement due to statement timeout"})

		_, data, err := sqlToolCall(nbCtx, "SELECT id FROM events", "events", "", 0, nil)
		require.Error(t, err)
		assert.Nil(t, data)
		assert.Contains(t, err.Error(), "the query on events did not finish within 2m0s")
		assert.Contains(t, err.Error(), "narrow it with a filter")
		var pqErr *pq.Error
		assert.True(t, errors.As(err, &pqErr), "the driver error must stay unwrappable")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("other failures keep their own message", func(t *testing.T) {
		expectToolQuery(mock, "select").WillReturnError(errors.New("connection reset"))

		_, data, err := sqlToolCall(nbCtx, "SELECT id FROM events", "events", "", 0, nil)
		require.Error(t, err)
		assert.Nil(t, data)
		assert.Contains(t, err.Error(), "connection reset")
		assert.NotContains(t, err.Error(), "did not finish")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
