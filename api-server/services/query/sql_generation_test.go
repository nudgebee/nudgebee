package query

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Helpers ----

func superAdminCtx() *security.RequestContext {
	return security.NewRequestContextForSuperAdmin(nil, nil, nil)
}

func newNormalTable() TableDefinition {
	return TableDefinition{
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "users",
		Name:                "users_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":         {Type: ColumnDefinitionTypeString},
			"tenant_id":  {Type: ColumnDefinitionTypeString},
			"account_id": {Type: ColumnDefinitionTypeString},
			"name":       {Type: ColumnDefinitionTypeString},
			"age":        {Type: ColumnDefinitionTypeInt},
			"score":      {Type: ColumnDefinitionTypeFloat},
			"created_at": {Type: ColumnDefinitionTypeDatetime},
			"is_active":  {Type: ColumnDefinitionTypeBoolean},
			"tags":       {Type: ColumnDefinitionTypeJson},
		},
	}
}

func newAggregateTable() TableDefinition {
	return TableDefinition{
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "orders",
		Name:                "order_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id":   {Type: ColumnDefinitionTypeString},
			"account_id":  {Type: ColumnDefinitionTypeString},
			"status":      {Type: ColumnDefinitionTypeString},
			"category":    {Type: ColumnDefinitionTypeString},
			"created_at":  {Type: ColumnDefinitionTypeDatetime},
			"order_count": {Type: ColumnDefinitionTypeInt, Def: "count(*)", IsAggregated: true},
			"total_value": {Type: ColumnDefinitionTypeFloat, Def: "sum(value)", IsAggregated: true},
			"avg_value":   {Type: ColumnDefinitionTypeFloat, Def: "avg(value)", IsAggregated: true},
			"max_value":   {Type: ColumnDefinitionTypeFloat, Def: "max(value)", IsAggregated: true},
			"min_value":   {Type: ColumnDefinitionTypeFloat, Def: "min(value)", IsAggregated: true},
		},
	}
}

func cols(names ...string) []QueryColumn {
	out := make([]QueryColumn, len(names))
	for i, n := range names {
		out[i] = QueryColumn{Name: n}
	}
	return out
}

// ---- Normal Table SELECT Tests ----

func TestSQLGen_NormalTable_BasicSelect(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id", "name"),
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "SELECT")
	assert.Contains(t, sql, "AS id")
	assert.Contains(t, sql, "AS name")
	assert.Contains(t, sql, "FROM users")
	assert.Contains(t, sql, "LIMIT 10")
	// Normal table with no aggregated columns => no GROUP BY
	assert.NotContains(t, sql, "GROUP BY")
}

func TestSQLGen_NormalTable_DefaultLimit(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "LIMIT 1000")
}

func TestSQLGen_NormalTable_Offset(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Limit:   10,
		Offset:  20,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "LIMIT 10")
	assert.Contains(t, sql, "OFFSET 20")
}

// ---- WHERE Clause Tests ----

func TestSQLGen_Where_Eq(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Eq: "alice"},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name = 'alice'")
}

func TestSQLGen_Where_Neq(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Nq: "bob"},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name != 'bob'")
}

func TestSQLGen_Where_Lt_Gt_Lte_Gte(t *testing.T) {
	td := newNormalTable()
	tests := []struct {
		op       BinaryWhereClauseType
		expected string
	}{
		{Lt, "age < 30"},
		{Gt, "age > 10"},
		{Lte, "age <= 50"},
		{Gte, "age >= 18"},
	}
	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			var val any
			switch tt.op {
			case Lt:
				val = 30
			case Gt:
				val = 10
			case Lte:
				val = 50
			case Gte:
				val = 18
			}
			req := QueryRequest{
				Table:   "users_v2",
				Columns: cols("id"),
				Where: QueryWhereClause{
					Binary: BinaryWhereClause{
						"age": {tt.op: val},
					},
				},
			}
			sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
			require.NoError(t, err)
			assert.Contains(t, sql, tt.expected)
		})
	}
}

func TestSQLGen_Where_In(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {In: []string{"alice", "bob", "charlie"}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name IN ('alice','bob','charlie')")
}

func TestSQLGen_Where_NotIn(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {NotIn: []string{"deleted", "banned"}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name NOT IN ('deleted','banned')")
}

func TestSQLGen_Where_Like(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Like: "%alice%"},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name LIKE '%alice%'")
}

func TestSQLGen_Where_ILike(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {ILike: "%alice%"},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name ILIKE '%alice%'")
}

func TestSQLGen_Where_IsNull(t *testing.T) {
	td := newNormalTable()
	t.Run("is_null_true", func(t *testing.T) {
		req := QueryRequest{
			Table:   "users_v2",
			Columns: cols("id"),
			Where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"name": {IsNull: true},
				},
			},
		}
		sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
		require.NoError(t, err)
		assert.Contains(t, sql, "name IS NULL")
	})
	t.Run("is_null_false", func(t *testing.T) {
		req := QueryRequest{
			Table:   "users_v2",
			Columns: cols("id"),
			Where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"name": {IsNull: false},
				},
			},
		}
		sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
		require.NoError(t, err)
		assert.Contains(t, sql, "name IS NOT NULL")
	})
}

func TestSQLGen_Where_Between(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"age": {Between: map[string]any{"_gte": 18, "_lte": 65}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "age >=")
	assert.Contains(t, sql, "age <=")
}

func TestSQLGen_Where_DatetimeComparison(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"created_at": {Gte: "2025-01-01T00:00:00Z"},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "created_at >=")
	assert.Contains(t, sql, "2025-01-01T00:00:00Z")
}

func TestSQLGen_Where_EmptyInClause(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {In: []string{}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	// Empty IN should produce "true" rather than an empty IN clause
	assert.Contains(t, sql, "true")
}

// ---- Composite WHERE (AND / OR / NOT) ----

func TestSQLGen_Where_And(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"name": {Eq: "alice"}}},
				{Binary: BinaryWhereClause{"age": {Gte: 18}}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "AND")
	assert.Contains(t, sql, "name = 'alice'")
	assert.Contains(t, sql, "age >= 18")
}

func TestSQLGen_Where_Or(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Or: []QueryWhereClause{
				{Binary: BinaryWhereClause{"name": {Eq: "alice"}}},
				{Binary: BinaryWhereClause{"name": {Eq: "bob"}}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "OR")
}

func TestSQLGen_Where_Not(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Not: &QueryWhereClause{
				Binary: BinaryWhereClause{"name": {Eq: "banned"}},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "NOT")
	assert.Contains(t, sql, "name = 'banned'")
}

func TestSQLGen_Where_NestedAndOr(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"tenant_id": {Eq: "t1"}}},
				{
					Or: []QueryWhereClause{
						{Binary: BinaryWhereClause{"name": {Eq: "alice"}}},
						{Binary: BinaryWhereClause{"name": {Eq: "bob"}}},
					},
				},
			},
		},
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "AND")
	assert.Contains(t, sql, "OR")
}

// ---- Aggregate Table Tests ----

func TestSQLGen_AggregateTable_AutoGroupBy(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count", "total_value"),
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "GROUP BY")
	assert.Contains(t, sql, "count(*) AS order_count")
	assert.Contains(t, sql, "sum(value) AS total_value")
	// status should be in GROUP BY (non-aggregated), but order_count/total_value should NOT
	groupByIdx := strings.Index(sql, "GROUP BY")
	groupByClause := sql[groupByIdx:]
	assert.Contains(t, groupByClause, "status")
	assert.NotContains(t, groupByClause, "count(*)")
	assert.NotContains(t, groupByClause, "sum(value)")
}

func TestSQLGen_AggregateTable_ExplicitGroupBy(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "category", "order_count"),
		GroupBy: []string{"status", "category"},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "GROUP BY")
}

func TestSQLGen_AggregateTable_OrderByAggregatedColumn(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		OrderBy: []QueryOrderBy{{Column: "order_count", Order: Desc}},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "ORDER BY order_count")
	assert.Contains(t, sql, "DESC")
}

func TestSQLGen_AggregateTable_OrderByNonGroupedColumn(t *testing.T) {
	td := newAggregateTable()
	// When sorting by a column not in GROUP BY and not aggregated, it should be wrapped in MAX()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		OrderBy: []QueryOrderBy{{Column: "category", Order: Asc}},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "MAX(category)")
}

func TestSQLGen_AggregateTable_Having(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		Having: QueryWhereClause{
			Binary: BinaryWhereClause{
				"status": {Eq: "active"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "HAVING")
	assert.Contains(t, sql, "status = 'active'")
}

func TestSQLGen_AggregateTable_Having_AggregatedColumn(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		Having: QueryWhereClause{
			Binary: BinaryWhereClause{
				"order_count": {Eq: 5},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "HAVING")
	assert.Contains(t, sql, "count(*)")
}

func TestSQLGen_AggregateTable_CountDistinct(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table: "order_groupings_v2",
		Columns: []QueryColumn{
			{Name: "status"},
			{Name: "order_count", Expr: "distinct", Args: []string{"category"}},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "count(DISTINCT")
}

// ---- Column Def Mapping Tests ----

func TestSQLGen_ColumnDef_MapsVirtualToActual(t *testing.T) {
	td := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Def:    "cloud_resources",
		Name:   "test_v2",
		Columns: map[string]ColumnDefinition{
			"account_id": {Type: ColumnDefinitionTypeString, Def: "cloud_account_id"},
			"name":       {Type: ColumnDefinitionTypeString, Def: "resource_name"},
		},
	}
	req := QueryRequest{
		Table:   "test_v2",
		Columns: cols("account_id", "name"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"account_id": {Eq: "acc-123"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "cloud_account_id")
	assert.Contains(t, sql, "resource_name")
	assert.Contains(t, sql, "AS account_id")
	assert.Contains(t, sql, "AS name")
}

func TestSQLGen_ColumnDef_WhereDef(t *testing.T) {
	td := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Def:    "test_table",
		Name:   "test_v2",
		Columns: map[string]ColumnDefinition{
			"search": {Type: ColumnDefinitionTypeString, Def: "display_text", WhereDef: "lower(search_index)"},
		},
	}
	req := QueryRequest{
		Table:   "test_v2",
		Columns: cols("search"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"search": {Like: "%test%"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	// SELECT should use Def
	assert.Contains(t, sql, "display_text")
	// WHERE should use WhereDef
	assert.Contains(t, sql, "lower(search_index)")
}

// ---- Date Truncation Tests ----

func TestSQLGen_DateTruncation_InGroupBy(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table: "order_groupings_v2",
		Columns: []QueryColumn{
			{Name: "created_at"},
			{Name: "order_count"},
		},
		GroupBy: []string{"created_at"},
		Limit:   10,
	}
	// When a datetime column is in GroupBy without explicit expr, it should default to date_unit=day
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "DATE_TRUNC")
}

func TestSQLGen_DateTruncation_ExplicitHour(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table: "order_groupings_v2",
		Columns: []QueryColumn{
			{Name: "created_at", Expr: "date_unit", Args: []string{"hour"}},
			{Name: "order_count"},
		},
		GroupBy: []string{"created_at"},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "DATE_TRUNC('hour'")
}

// ---- OrderBy Tests ----

func TestSQLGen_OrderBy_Asc(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id", "name"),
		OrderBy: []QueryOrderBy{{Column: "name", Order: Asc}},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "ORDER BY name  ASC")
}

func TestSQLGen_OrderBy_DescNullsLast(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id", "name"),
		OrderBy: []QueryOrderBy{{Column: "name", Order: DescNullsLast}},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "DESC")
	assert.Contains(t, sql, "NULLS LAST")
}

func TestSQLGen_OrderBy_MultipleColumns(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id", "name", "age"),
		OrderBy: []QueryOrderBy{
			{Column: "name", Order: Asc},
			{Column: "age", Order: Desc},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	nameIdx := strings.Index(sql, "name  ASC")
	ageIdx := strings.Index(sql, "age  DESC")
	assert.Greater(t, ageIdx, nameIdx, "name should appear before age in ORDER BY")
}

// ---- Error Cases ----

func TestSQLGen_Error_UnknownColumn(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id", "nonexistent_column"),
		Limit:   10,
	}
	_, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent_column")
}

func TestSQLGen_Error_AggregatedColumnInWhere(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"order_count": {Gt: 5},
			},
		},
		Limit: 10,
	}
	_, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aggregated")
}

func TestSQLGen_Error_AggregatedColumnInGroupBy(t *testing.T) {
	td := newAggregateTable()
	req := QueryRequest{
		Table:   "order_groupings_v2",
		Columns: cols("status", "order_count"),
		GroupBy: []string{"order_count"},
		Limit:   10,
	}
	_, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aggregated")
}

func TestSQLGen_Error_UnknownColumnInWhere(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"nonexistent": {Eq: "val"},
			},
		},
		Limit: 10,
	}
	_, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	assert.Error(t, err)
}

func TestSQLGen_Error_UnknownColumnInOrderBy(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		OrderBy: []QueryOrderBy{{Column: "nonexistent", Order: Asc}},
		Limit:   10,
	}
	_, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	assert.Error(t, err)
}

// ---- ClickHouse Dialect Tests ----

func TestSQLGen_ClickhouseDialect_StringQuoting(t *testing.T) {
	td := TableDefinition{
		Type:   Normal,
		Source: database.Warehouse,
		Def:    "events",
		Name:   "ch_test_v2",
		Columns: map[string]ColumnDefinition{
			"id":   {Type: ColumnDefinitionTypeString},
			"name": {Type: ColumnDefinitionTypeString},
		},
	}
	req := QueryRequest{
		Table:   "ch_test_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Eq: "it's a test"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	// ClickHouse uses backslash escaping for single quotes
	assert.Contains(t, sql, "it\\'s a test")
}

func TestSQLGen_PostgresDialect_StringQuoting(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Eq: "it's a test"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	// Postgres uses doubled single quotes
	assert.Contains(t, sql, "it''s a test")
}

// ---- Derived Table with DefGenerator Tests ----

func TestSQLGen_DerivedTable_DefGenerator(t *testing.T) {
	td := TableDefinition{
		Type:   Derived,
		Source: database.Metastore,
		Name:   "derived_test_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			return "(SELECT id, name, tenant_id FROM base_table WHERE active = true) AS derived", request, nil
		},
		TenantIdColumnName: "tenant_id",
		Columns: map[string]ColumnDefinition{
			"id":        {Type: ColumnDefinitionTypeString},
			"name":      {Type: ColumnDefinitionTypeString},
			"tenant_id": {Type: ColumnDefinitionTypeString},
			"count":     {Type: ColumnDefinitionTypeInt, Def: "count(*)", IsAggregated: true},
		},
	}
	req := QueryRequest{
		Table:   "derived_test_v2",
		Columns: cols("name", "count"),
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "FROM (SELECT id, name, tenant_id FROM base_table WHERE active = true) AS derived")
	assert.Contains(t, sql, "GROUP BY")
}

// ---- Real table_metadata SQL Generation Tests ----

func TestSQLGen_RealTable_DwQueryGroupings(t *testing.T) {
	td, ok := GetTableMetadata("dw_query_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "dw_query_groupings_v2",
		Columns: cols("tenant_id", "account_id", "database_name", "avg_query_exec_duration_micro"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "tenant_1"},
			},
		},
		OrderBy: []QueryOrderBy{{Column: "avg_query_exec_duration_micro", Order: Desc}},
		Limit:   10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "SELECT")
	assert.Contains(t, sql, "avg(query_exec_duration_micro) AS avg_query_exec_duration_micro")
	assert.Contains(t, sql, "FROM dw_queries")
	assert.Contains(t, sql, "tenant_id = 'tenant_1'")
	assert.Contains(t, sql, "GROUP BY")
	assert.Contains(t, sql, "ORDER BY avg_query_exec_duration_micro")
	assert.Contains(t, sql, "LIMIT 10")
}

func TestSQLGen_RealTable_EventGroupings(t *testing.T) {
	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "event_groupings_v2",
		Columns: cols("account_id", "status", "event_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "t1"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "count(*) AS event_count")
	assert.Contains(t, sql, "GROUP BY")
	// Should NOT include event_duplicates JOIN since we don't reference fingerprint columns
	assert.NotContains(t, sql, "event_duplicates")
}

func TestSQLGen_RealTable_EventGroupings_WithFingerprintJoin(t *testing.T) {
	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "event_groupings_v2",
		Columns: cols("account_id", "fingerprint_event_count", "event_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "t1"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	// SHOULD include event_duplicates JOIN since we reference fingerprint_event_count
	assert.Contains(t, sql, "event_duplicates")
}

func TestSQLGen_RealTable_SpendGroupings(t *testing.T) {
	td, ok := GetTableMetadata("spend_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "spend_groupings_v2",
		Columns: cols("tenant_id", "account_id", "spend_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "t1"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "count(*) AS spend_count")
	assert.Contains(t, sql, "GROUP BY")
}

func TestSQLGen_RealTable_TicketGroupings(t *testing.T) {
	td, ok := GetTableMetadata("ticket_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "ticket_groupings_v2",
		Columns: cols("tenant_id", "status", "count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "t1"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "count(*) AS count")
	assert.Contains(t, sql, "GROUP BY")
}

// ---- SQL Injection Prevention ----

func TestSQLGen_Injection_WhereValue(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {Eq: "'; DROP TABLE users; --"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	// The injection payload should be escaped — the leading ' is doubled
	// so it becomes a literal string value, not executable SQL.
	// Postgres escapes ' as '' → the value becomes '''; DROP TABLE users; --'
	assert.Contains(t, sql, "'''; DROP TABLE users; --'")
	// The key safety check: the single quote before DROP is escaped (doubled),
	// so the SQL parser treats the entire thing as a string literal.
	assert.NotContains(t, sql, "= '; DROP")
}

func TestSQLGen_Injection_InValues(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {In: []string{"safe", "'; DROP TABLE users; --"}},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "IN ('safe','")
}

// ---- NLike operator test ----

func TestSQLGen_Where_NLike(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {NLike: "%bot%"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "NOT LIKE")
}

// ---- Field-to-Field comparison tests ----

func TestSQLGen_Where_EqF(t *testing.T) {
	td := newNormalTable()
	req := QueryRequest{
		Table:   "users_v2",
		Columns: cols("id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"name": {EqF: "account_id"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)
	assert.Contains(t, sql, "name = account_id")
}
