package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
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
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})
	return sqlxDB, mock
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

func TestSqlUpdateQueryWithAccountIdFilter_InvalidUUID(t *testing.T) {
	query := "SELECT * FROM recommendation_view WHERE severity = 'high'"
	_, err := SqlUpdateQueryWithAccountIdFilter(nil, query, "not-a-uuid", "recommendation_view")
	assert.Error(t, err, "Invalid UUID should return an error")
	assert.Contains(t, err.Error(), "invalid account_id format")
}

func TestSqlUpdateQueryWithAccountIdFilter_ValidUUID(t *testing.T) {
	query := "SELECT * FROM recommendation_view WHERE severity = 'high'"
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	result, err := SqlUpdateQueryWithAccountIdFilter(nil, query, validUUID, "recommendation_view")
	assert.NoError(t, err)
	assert.Contains(t, result, validUUID, "Valid UUID should be included in filtered query")
	assert.Contains(t, result, "cloud_account_id", "Should filter by cloud_account_id")
}

func TestSqlUpdateQueryWithAccountIdFilter_MultipleTableReferences(t *testing.T) {
	// Self-join: every reference to the table must be wrapped with the tenant
	// filter, otherwise a multi-table query could bypass tenant isolation.
	query := "SELECT a.id FROM recommendation_view a JOIN recommendation_view b ON a.id = b.parent_id"
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	result, err := SqlUpdateQueryWithAccountIdFilter(nil, query, validUUID, "recommendation_view")
	assert.NoError(t, err)
	// Both occurrences should be replaced and aliases must be unique.
	assert.Equal(t, 2, strings.Count(result, "cloud_account_id = '"+validUUID+"'"),
		"both table occurrences must be wrapped with the tenant filter")
	assert.Contains(t, result, ") q0")
	assert.Contains(t, result, ") q1")
}

func TestSqlUpdateQueryWithAccountIdFilter_SkipsEventsAndAnomaly(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"

	query := "SELECT * FROM events WHERE severity = 'high'"
	result, err := SqlUpdateQueryWithAccountIdFilter(nil, query, validUUID, "events")
	assert.NoError(t, err)
	assert.Equal(t, query, result, "events table should skip account filter")

	query2 := "SELECT * FROM anomaly WHERE severity = 'high'"
	result2, err := SqlUpdateQueryWithAccountIdFilter(nil, query2, validUUID, "anomaly")
	assert.NoError(t, err)
	assert.Equal(t, query2, result2, "anomaly table should skip account filter")
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
			errorMsg:     "sql: not allowed",
		},
		{
			name:         "SELECT from allowed table with join (allowed table first)",
			query:        "SELECT e.*, u.name FROM events e JOIN users u ON e.user_id = u.id",
			allowedTable: "events",
			expectError:  false,
		},
		{
			name:         "SELECT from allowed table with join (allowed table second, not caught)",
			query:        "SELECT u.name, e.* FROM users u JOIN events e ON e.user_id = u.id",
			allowedTable: "events",
			expectError:  true, // This should ideally be caught if strict table checking is desired for all parts
			errorMsg:     "sql: not allowed",
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
			name:         "SELECT from allowed table with schema prefix (not supported by current check)",
			query:        "SELECT * FROM public.events WHERE id = 1",
			allowedTable: "events", // The current logic correctly handles "public.events"
			expectError:  false,    // Expect no error as 'events' is the table after schema
		},
		{
			name:         "SELECT from allowed table, but table name is a substring of another",
			query:        "SELECT * FROM allevents WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: not allowed",
		},
		{
			name:         "SELECT from allowed table, query has table name in comments",
			query:        "SELECT * -- from events \n FROM real_table WHERE id = 1",
			allowedTable: "events",
			expectError:  true,
			errorMsg:     "sql: not allowed",
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
		mock.ExpectQuery("select").WillReturnRows(sqlmock.NewRows(columns))

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
		mock.ExpectQuery("select").WillReturnRows(
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
