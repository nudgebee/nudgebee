package query

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateWhereClause_SQLInjection(t *testing.T) {
	// Setup a table definition with an integer column
	// We use database.Metastore which maps to postgresDialect (standard SQL quoting)
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Name:   "test_table",
		Columns: map[string]ColumnDefinition{
			"id":    {Type: ColumnDefinitionTypeString},
			"count": {Type: ColumnDefinitionTypeInt},
			"score": {Type: ColumnDefinitionTypeFloat},
		},
	}

	tests := []struct {
		name          string
		where         QueryWhereClause
		expectedQuery string
	}{
		{
			name: "Eq: integer column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						Eq: "1 OR 1=1",
					},
				},
			},
			// Expecting the string to be quoted, treating it as a string literal value comparison, NOT raw SQL
			// Before fix: count = 1 OR 1=1
			expectedQuery: "(count = '1 OR 1=1')",
		},
		{
			name: "Nq: float column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"score": {
						Nq: "1.0; DROP TABLE users",
					},
				},
			},
			// Before fix: score != 1.0; DROP TABLE users
			expectedQuery: "(score != '1.0; DROP TABLE users')",
		},
		{
			name: "Nq: string column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"id": {
						Nq: "'; DROP TABLE users; --",
					},
				},
			},
			// Expecting the string to be quoted, treating it as a string literal value comparison, NOT raw SQL
			expectedQuery: "(id != '''; DROP TABLE users; --')",
		},
		{
			name: "Gt: integer column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						Gt: "10 UNION SELECT * FROM users",
					},
				},
			},
			expectedQuery: "(count > '10 UNION SELECT * FROM users')",
		},
		{
			name: "In: integer column with mixed valid/malicious types",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						In: []any{1, "2 OR 1=1"},
					},
				},
			},
			// Mixed types in IN clause. 1 remains 1, the string gets quoted.
			expectedQuery: "(count IN (1,'2 OR 1=1'))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, err := generateWhereClause(tt.where, tableDef)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedQuery, sql)
		})
	}

	t.Run("Between: integer column with malicious bounds", func(t *testing.T) {
		where := QueryWhereClause{
			Binary: BinaryWhereClause{
				"count": {
					Between: map[string]any{
						"_gte": "1 OR 1=1",
						"_lte": 100,
					},
				},
			},
		}
		sql, err := generateWhereClause(where, tableDef)
		assert.NoError(t, err)
		// Map iteration order is random, check for both possibilities
		// Note the double parens from generateWhereClause wrapping: ((cond1 AND cond2))
		option1 := "((count >= '1 OR 1=1' AND count <= 100))"
		option2 := "((count <= 100 AND count >= '1 OR 1=1'))"
		assert.Contains(t, []string{option1, option2}, sql)
	})
}

// --- Identifier-allowlist regression tests ---
// These lock in the column-allowlist defense: every SQL identifier slot
// (column in SELECT/WHERE/GROUP BY/ORDER BY, plus the WHERE operator)
// must be rejected when the caller passes something not in tableDef.

func TestGenerateOrderByClause_RejectsUnknownColumn(t *testing.T) {
	td := TableDefinition{
		Type:    Normal,
		Source:  database.Metastore,
		Name:    "test_table",
		Columns: map[string]ColumnDefinition{"id": {Type: ColumnDefinitionTypeString}},
	}
	req := QueryRequest{
		Table: "test_table",
		OrderBy: []QueryOrderBy{
			{Column: "id; DROP TABLE users", Order: Asc},
		},
	}
	sql, err := generateOrderByClause(req, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Empty(t, sql)
}

func TestGenerateGroupClause_RejectsUnknownColumn(t *testing.T) {
	// Aggregate table type — bypasses the Normal-table early-return so the
	// GroupBy allowlist check actually fires.
	td := TableDefinition{
		Type:   Aggregate,
		Source: database.Metastore,
		Name:   "test_table",
		Columns: map[string]ColumnDefinition{
			"id":  {Type: ColumnDefinitionTypeString},
			"agg": {Type: ColumnDefinitionTypeInt, Def: "count(*)", IsAggregated: true},
		},
	}
	req := QueryRequest{
		Table:   "test_table",
		GroupBy: []string{"id; DROP TABLE users"},
		Columns: []QueryColumn{{Name: "agg"}},
	}
	_, err := generateGroupClause(req, td, "", superAdminCtx())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGenerateWhereClause_RejectsUnknownColumn(t *testing.T) {
	td := TableDefinition{
		Type:    Normal,
		Source:  database.Metastore,
		Name:    "test_table",
		Columns: map[string]ColumnDefinition{"id": {Type: ColumnDefinitionTypeString}},
	}
	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"id; DROP TABLE users": {Eq: "x"},
		},
	}
	sql, err := generateWhereClause(where, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Empty(t, sql)
}

func TestGenerateWhereClause_RejectsUnknownOperator(t *testing.T) {
	td := TableDefinition{
		Type:    Normal,
		Source:  database.Metastore,
		Name:    "test_table",
		Columns: map[string]ColumnDefinition{"id": {Type: ColumnDefinitionTypeString}},
	}
	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"id": {
				BinaryWhereClauseType("_evil; DROP TABLE users"): "x",
			},
		},
	}
	sql, err := generateWhereClause(where, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Empty(t, sql)
}

func TestQuoteIdentifier_EscapesQuotingCharacter(t *testing.T) {
	t.Run("ClickHouse backtick", func(t *testing.T) {
		d := &clickhouseDialect{}
		assert.Equal(t, "```test``col```", d.QuoteIdentifier("`test`col`"))
	})
	t.Run("ClickHouse backslash literal", func(t *testing.T) {
		d := &clickhouseDialect{}
		assert.Equal(t, "`col\\name`", d.QuoteIdentifier("col\\name"))
	})
	t.Run("BigQuery backtick", func(t *testing.T) {
		d := &bigQueryDialect{}
		assert.Equal(t, "`\\`test\\`col\\``", d.QuoteIdentifier("`test`col`"))
	})
	t.Run("BigQuery backslash", func(t *testing.T) {
		d := &bigQueryDialect{}
		assert.Equal(t, "`col\\\\name`", d.QuoteIdentifier("col\\name"))
	})
	t.Run("Postgres double-quote", func(t *testing.T) {
		d := &postgresDialect{}
		assert.Equal(t, `"""test""col"""`, d.QuoteIdentifier(`"test"col"`))
	})
}

func TestKqlQuoteLiteral_EscapesSingleQuotesAndBackslashes(t *testing.T) {
	d := &kqlDialect{}
	assert.Equal(t, "'safe'", d.QuoteLiteral("safe"))
	assert.Equal(t, "'it\\'s a test'", d.QuoteLiteral("it's a test"))
	assert.Equal(t, "'\\'; DROP TABLE users; --'", d.QuoteLiteral("'; DROP TABLE users; --"))
	assert.Equal(t, "'C:\\\\Users\\\\admin'", d.QuoteLiteral("C:\\Users\\admin"))
}

func TestKqlWhereClause_SQLInjection(t *testing.T) {
	tableDef := TableDefinition{
		Def: "TestTable",
		Columns: map[string]ColumnDefinition{
			"name": {Type: "string"},
		},
	}
	gen := KqlGenerator{}
	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"name": {Eq: "O'Malley"},
		},
	}
	result, err := gen.generateKqlWhereClause(where, tableDef)
	assert.NoError(t, err)
	assert.Equal(t, "where name == 'O\\'Malley'", result)
}

func TestKqlWhereClause_TypeCasting(t *testing.T) {
	tableDef := TableDefinition{
		Def: "TestTable",
		Columns: map[string]ColumnDefinition{
			"age":     {Type: "integer"},
			"score":   {Type: "float"},
			"created": {Type: "datetime"},
		},
	}
	gen := KqlGenerator{}
	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"age":     {Eq: 42, Between: map[string]any{"_gte": 18, "_lte": 65}},
			"score":   {Gt: 95.5},
			"created": {Lt: "2026-01-01"},
		},
	}
	result, err := gen.generateKqlWhereClause(where, tableDef)
	assert.NoError(t, err)
	assert.Contains(t, result, "age >= toint('18')")
	assert.Contains(t, result, "age <= toint('65')")
	assert.Contains(t, result, "age == toint('42')")
	assert.Contains(t, result, "created < todatetime('2026-01-01')")
	assert.Contains(t, result, "score > todouble('95.5')")
}

func TestGenerateWhereClause_NilDialect(t *testing.T) {
	// Setup a table definition with an invalid/unknown source
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.DatabaseManagerType("unknown_source"),
		Name:   "test_table",
		Columns: map[string]ColumnDefinition{
			"id": {Type: ColumnDefinitionTypeString},
		},
	}

	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"id": {
				Eq: "some_value",
			},
		},
	}

	// Should return an error, not panic
	sql, err := generateWhereClause(where, tableDef)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dialect not found")
	assert.Empty(t, sql)
}

func TestDateUnit_RejectsUnsafeInput(t *testing.T) {
	payloads := []string{
		"day', created_at), (select count(*)::text from llm_conversation_tool_calls where parameters::text like '%akia%'), date_trunc('day",
		"day') || (select credential from user_auths limit 1) || ('",
		"DAY, created_at) UNION SELECT 1 --",
		"day'; select pg_sleep(5); --",
	}
	dialectsUnderTest := map[string]sqlDialect{
		"postgres":   &postgresDialect{},
		"clickhouse": &clickhouseDialect{},
		"bigquery":   &bigQueryDialect{},
	}
	for name, d := range dialectsUnderTest {
		for _, p := range payloads {
			out := d.FuncDateTruncate(p, "created_at")
			assert.NotContains(t, out, "select", "%s rendered payload: %s", name, out)
			assert.NotContains(t, out, "SELECT", "%s rendered payload: %s", name, out)
			assert.NotContains(t, out, ";", "%s rendered payload: %s", name, out)
			assert.Contains(t, strings.ToLower(out), "day", "%s should fall back to day: %s", name, out)
		}
	}

	assert.Equal(t, "DATE_TRUNC('month', created_at)", (&postgresDialect{}).FuncDateTruncate("Month", "created_at"))
	assert.Equal(t, "DATE_TRUNC('hour', created_at)", (&clickhouseDialect{}).FuncDateTruncate("hour", "created_at"))
	assert.Equal(t, "DATE_TRUNC(created_at, WEEK)", (&bigQueryDialect{}).FuncDateTruncate("week", "created_at"))
}

func TestGenerateSelectColumnsClause_RejectsInjectedDateUnit(t *testing.T) {
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Name:   "knowledge_base",
		Columns: map[string]ColumnDefinition{
			"created_at": {Type: ColumnDefinitionTypeDatetime},
		},
	}
	bad := QueryRequest{Table: "knowledge_base", Columns: []QueryColumn{{
		Name: "created_at", Expr: "date_unit",
		Args: []string{"day', created_at), (select count(*)::text from user_auths), date_trunc('day"},
	}}}
	_, err := generateSelectColumnsClause(bad, tableDef, "", nil)
	assert.Error(t, err)

	empty := QueryRequest{Table: "knowledge_base", Columns: []QueryColumn{{Name: "created_at", Expr: "date_unit"}}}
	_, err = generateSelectColumnsClause(empty, tableDef, "", nil)
	assert.Error(t, err)

	for _, unit := range []string{"second", "minute", "hour", "day", "week", "month", "quarter", "year", "DAY"} {
		ok := QueryRequest{Table: "knowledge_base", Columns: []QueryColumn{{Name: "created_at", Expr: "date_unit", Args: []string{unit}}}}
		sql, err := generateSelectColumnsClause(ok, tableDef, "", nil)
		assert.NoError(t, err, unit)
		assert.Equal(t, "DATE_TRUNC('"+strings.ToLower(unit)+"', created_at) AS created_at", sql)
	}
}

func TestGenerateWhereClause_RejectsNonScalarValues(t *testing.T) {
	payload := []any{"0] OR 1=1 OR [0"}
	for _, src := range []database.DatabaseManagerType{database.Metastore, database.AgentWarehouse, database.AgentWarehouseBigQuery} {
		tableDef := TableDefinition{Type: Normal, Source: src, Name: "t", Columns: map[string]ColumnDefinition{
			"count": {Type: ColumnDefinitionTypeInt},
			"score": {Type: ColumnDefinitionTypeFloat},
		}}
		for _, tc := range []struct {
			col string
			op  BinaryWhereClauseType
			val any
		}{
			{"count", "_eq", payload},
			{"count", "_gt", payload},
			{"score", "_lte", map[string]any{"x": "1 OR 1=1"}},
			{"count", "_in", []any{payload}},
		} {
			w := QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{tc.col: {tc.op: tc.val}}}
			sql, err := generateWhereClause(w, tableDef)
			assert.Error(t, err, "%v %s %s produced %q", src, tc.col, tc.op, sql)
			assert.NotContains(t, sql, "1=1")
		}

		w := QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{"count": {"_in": []any{float64(1), float64(2)}}}}
		sql, err := generateWhereClause(w, tableDef)
		assert.NoError(t, err)
		assert.Equal(t, "(count IN (1,2))", sql)
	}
}

// TestWhereClause_ResolvesWhereDefNotRawKey covers the trace-attribute path:
// dynamically-registered attribute columns carry only WhereDef (the ClickHouse
// map-access expression), and the key itself is caller-influenced. Every WHERE
// operator must emit the WhereDef expression, never the raw key, so a crafted
// key cannot be spliced into the SQL text.
func TestWhereClause_ResolvesWhereDefNotRawKey(t *testing.T) {
	const rawKey = "x') OR ('1'='1"
	const whereDef = "if(mapContains(spanattributes, 'k'), spanattributes['k'], resourceattributes['k'])"
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Warehouse,
		Name:   "traces",
		Columns: map[string]ColumnDefinition{
			rawKey: {Type: ColumnDefinitionTypeString, WhereDef: whereDef},
		},
	}

	cases := map[string]map[BinaryWhereClauseType]any{
		"_is_null": {IsNull: true},
		"_in":      {In: []any{"a", "b"}},
		"_not_in":  {NotIn: []any{"a"}},
		"_neq":     {Nq: "a"},
		"_has_key": {HasKey: "a"},
		"_ilike":   {ILike: "%a%"},
		"_nlike":   {NLike: "%a%"},
	}
	for name, binary := range cases {
		t.Run(name, func(t *testing.T) {
			sql, err := generateWhereClause(QueryWhereClause{Binary: BinaryWhereClause{rawKey: binary}}, tableDef)
			assert.NoError(t, err)
			assert.Contains(t, sql, whereDef, "must resolve via WhereDef")
			assert.NotContains(t, sql, "OR ('1'='1", "raw attribute key must not reach SQL")
		})
	}
}

// TestWhereClause_DefAndNameResolutionUnchanged guards the no-op property of the
// WhereDef change: a column with a plain Def still renders by Def, and a column
// with neither Def nor WhereDef still renders by its name.
func TestWhereClause_DefAndNameResolutionUnchanged(t *testing.T) {
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Name:   "t",
		Columns: map[string]ColumnDefinition{
			"by_def":  {Type: ColumnDefinitionTypeString, Def: "lower(real_col)"},
			"by_name": {Type: ColumnDefinitionTypeString},
		},
	}

	sql, err := generateWhereClause(QueryWhereClause{Binary: BinaryWhereClause{"by_def": {Eq: "x"}}}, tableDef)
	assert.NoError(t, err)
	assert.Equal(t, "(lower(real_col) = 'x')", sql)

	sql, err = generateWhereClause(QueryWhereClause{Binary: BinaryWhereClause{"by_name": {Eq: "x"}}}, tableDef)
	assert.NoError(t, err)
	assert.Equal(t, "(by_name = 'x')", sql)
}

// resource_groupings_v2 and cloud_resources_list_v2 fill their templates' filter
// placeholders. Filled one placeholder at a time, a filter value naming a later
// placeholder had that placeholder's quoted fragment spliced into it, which
// unbalanced the quotes and ran the rest of the value as SQL.
func TestDefGenerator_PlaceholderNameInFilterValueStaysALiteral(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)

	resourceGroupingsPlaceholders := []string{
		"__resources_nostatus__where__", "__resources__where__", "__spends__where__",
		"__recommendations__where__", "__spends_acct__where__", "__recommendations_acct__where__",
	}
	// Both template shapes: per-resource rows and the per-service rollup.
	for _, groupBy := range [][]string{
		{"tenant_id", "account_id", "resource_service_name", "resource_id", "resource_name"},
		{"tenant_id", "account_id", "resource_service_name"},
	} {
		for _, placeholder := range resourceGroupingsPlaceholders {
			value := "x" + placeholder + "y"
			request := QueryRequest{
				Table:   "resource_groupings_v2",
				Columns: fromStringColsToQueryCols([]string{"tenant_id", "account_id", "resource_service_name", "count_resource", "sum_spend_amount", "count_recommendation"}),
				Where: QueryWhereClause{Binary: BinaryWhereClause{
					"account_id":            {Eq: testenv.FakeAccountID},
					"resource_service_name": {Eq: value},
					"recommendation_status": {Eq: "Open"},
					"spend_date":            {Between: map[string]any{"_gte": "2025-01-01T00:00:00Z", "_lte": "2025-05-01T00:00:00Z"}},
				}},
				GroupBy: groupBy,
				Limit:   10,
			}
			query, err := GenerateSqlQuery(ctx, uuid.NewString(), request, table_metadata["resource_groupings_v2"])
			require.NoError(t, err)
			assertEveryCopyIntact(t, query, value, "group by %v, placeholder %s", groupBy, placeholder)
		}
	}

	for _, placeholder := range []string{"__cr__where__", "__spends__where__", "__metrics__where__"} {
		value := "x" + placeholder + "y"
		request := QueryRequest{
			Table:   "cloud_resources_list_v2",
			Columns: fromStringColsToQueryCols([]string{"id", "name", "account", "spend_amount"}),
			Where: QueryWhereClause{Binary: BinaryWhereClause{
				"account": {Eq: testenv.FakeAccountID},
				"name":    {Eq: value},
			}},
			Limit: 10,
		}
		query, err := GenerateSqlQuery(ctx, uuid.NewString(), request, table_metadata["cloud_resources_list_v2"])
		require.NoError(t, err)
		assertEveryCopyIntact(t, query, value, "placeholder %s", placeholder)
	}
}

// assertEveryCopyIntact checks that the filter value appears, and that every
// literal starting with its first characters is the whole value. A template can
// hold the value more than once, so one intact copy does not show that another
// was not spliced into.
func assertEveryCopyIntact(t *testing.T, query, value string, msgAndArgs ...any) {
	t.Helper()
	intact := strings.Count(query, "'"+value+"'")
	assert.Positive(t, intact, msgAndArgs...)
	assert.Equal(t, strings.Count(query, "'"+value[:1]), intact, msgAndArgs...)
}
