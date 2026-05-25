package api

import (
	"encoding/json"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simulateQueryPipeline replicates the handleQueryAction pipeline
// without needing a Gin context or DB connection. It returns the final QueryRequest
// and generated SQL (for tables that support it).
func simulateQueryPipeline(t *testing.T, payload ActionRequest) (query.QueryRequest, string) {
	t.Helper()

	queryMap := payload.Input

	// Step 1: Fix where clause (same as handler)
	if queryMap["where"] != nil {
		whereMap := queryMap["where"].(map[string]any)
		w, err := fixWhereClause(whereMap)
		require.NoError(t, err, "fixWhereClause failed")
		queryMap["where"] = w
	}

	// Step 2: Parse columns from GQL or from explicit input
	if queryMap["columns"] == nil || len(queryMap["columns"].([]any)) == 0 {
		colsFromGql, err := parseSelectColumns(&payload)
		require.NoError(t, err, "parseSelectColumns failed")
		queryMap["columns"] = colsFromGql
	} else if queryMap["columns"] != nil {
		cols := queryMap["columns"].([]any)
		queryMap["columns"] = lo.Map(cols, func(item any, index int) query.QueryColumn {
			return query.QueryColumn{Name: item.(string)}
		})
	}

	// Step 3: Apply column transformations
	if queryMap["column_transformations"] != nil {
		transformations := queryMap["column_transformations"].([]any)
		for _, transformation := range transformations {
			transformationMap := transformation.(map[string]any)
			require.NotNil(t, transformationMap["name"], "missing name in column transformation")
			require.NotNil(t, transformationMap["expr"], "missing expr in column transformation")

			name := transformationMap["name"].(string)
			expr := transformationMap["expr"].(string)
			args := []string{}
			if transformationMap["args"] != nil {
				for _, arg := range transformationMap["args"].([]any) {
					args = append(args, arg.(string))
				}
			}

			for i, col := range queryMap["columns"].([]query.QueryColumn) {
				if col.Name == name {
					col.Expr = expr
					col.Args = args
					queryMap["columns"].([]query.QueryColumn)[i] = col
					break
				}
			}
		}
	}

	// Step 4: Unmarshal to QueryRequest
	var queryRequest query.QueryRequest
	err := common.UnmarshalMapToStruct(queryMap, &queryRequest)
	require.NoError(t, err, "UnmarshalMapToStruct failed")

	queryRequest.Table = payload.Action.Name

	// Step 5: Look up table metadata and generate SQL
	tableDef, ok := query.GetTableMetadata(queryRequest.Table)
	if !ok {
		t.Fatalf("table %q not found in metadata", queryRequest.Table)
	}

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	sql, err := query.GenerateSqlQuery(ctx, "", queryRequest, tableDef)
	require.NoError(t, err, "GenerateSqlQuery failed")

	return queryRequest, sql
}

// parsePostmanJSON simulates parsing the Postman-style JSON payload
func parsePostmanJSON(t *testing.T, jsonStr string) ActionRequest {
	t.Helper()
	var payload ActionRequest
	err := json.Unmarshal([]byte(jsonStr), &payload)
	require.NoError(t, err, "failed to parse JSON payload")
	return payload
}

// ---- End-to-End Tests ----

func TestE2E_LLMConversationList_BasicQuery(t *testing.T) {
	// Exact Postman payload from user
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "llm_conversation_list_v2"
		},
		"input": {
			"where": {
				"account_id": {
					"_eq": "a2a30b02-0f67-42e5-a2ab-c658230fd798"
				}
			}
		},
		"request_query": "query GetLLMConversation ($limit:Int)  {\n llm_conversation_list_v2(where: {}, limit: $limit, offset: $offset) {\n      rows { title source user_display_name for_status is_saved  total_count}\n    }\n}"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	// Verify QueryRequest structure
	assert.Equal(t, "llm_conversation_list_v2", req.Table)
	assert.NotEmpty(t, req.Columns)

	// Verify columns were parsed from GQL
	colNames := lo.Map(req.Columns, func(c query.QueryColumn, _ int) string { return c.Name })
	assert.Contains(t, colNames, "title")
	assert.Contains(t, colNames, "source")
	assert.Contains(t, colNames, "user_display_name")
	assert.Contains(t, colNames, "for_status")
	assert.Contains(t, colNames, "is_saved")
	assert.Contains(t, colNames, "total_count")

	// Verify WHERE clause was parsed
	assert.NotNil(t, req.Where.Binary)

	// Verify generated SQL
	assert.Contains(t, sql, "SELECT")
	assert.Contains(t, sql, "FROM")
	assert.Contains(t, sql, "llm_conversation_list_v2")
	assert.Contains(t, sql, "account_id")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_DwQueryGroupings_WithOrderByAndLimit(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "dw_query_groupings_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "tenant_1" },
				"account_id": { "_eq": "acc_1" }
			},
			"limit": 20,
			"offset": 0,
			"order_by": [
				{ "column": "avg_query_exec_duration_micro", "order": "desc" }
			]
		},
		"request_query": "query Q { dw_query_groupings_v2 { rows { tenant_id account_id database_name avg_query_exec_duration_micro sum_bill } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "dw_query_groupings_v2", req.Table)
	assert.Equal(t, 20, req.Limit)
	assert.Equal(t, 0, req.Offset)
	assert.Len(t, req.OrderBy, 1)
	assert.Equal(t, "avg_query_exec_duration_micro", req.OrderBy[0].Column)

	assert.Contains(t, sql, "FROM dw_queries")
	assert.Contains(t, sql, "avg(query_exec_duration_micro)")
	assert.Contains(t, sql, "GROUP BY")
	assert.Contains(t, sql, "ORDER BY")
	assert.Contains(t, sql, "LIMIT 20")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_EventGroupings_ComplexWhere(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "event_groupings_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "t1" },
				"_and": [
					{ "status": { "_in": ["firing", "resolved"] } },
					{ "priority": { "_eq": "HIGH" } }
				]
			},
			"limit": 50
		},
		"request_query": "query Q { event_groupings_v2 { rows { account_id status event_count max_created_at } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "event_groupings_v2", req.Table)

	// Verify columns
	colNames := lo.Map(req.Columns, func(c query.QueryColumn, _ int) string { return c.Name })
	assert.Contains(t, colNames, "event_count")
	assert.Contains(t, colNames, "max_created_at")

	// Verify composite WHERE was parsed
	assert.NotEmpty(t, req.Where.And)

	// Verify SQL
	assert.Contains(t, sql, "count(*) AS event_count")
	assert.Contains(t, sql, "max(events.created_at) AS max_created_at")
	assert.Contains(t, sql, "GROUP BY")
	assert.Contains(t, sql, "AND")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_TicketGroupings_SimpleCount(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "ticket_groupings_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "t1" }
			},
			"limit": 100
		},
		"request_query": "query Q { ticket_groupings_v2 { rows { status count } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "ticket_groupings_v2", req.Table)
	assert.Contains(t, sql, "count(*) AS count")
	assert.Contains(t, sql, "GROUP BY")
	assert.Contains(t, sql, "LIMIT 100")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_Recommendations_WithDateBetween(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "recommendations_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "t1" },
				"account_id": { "_eq": "acc1" },
				"created_at": {
					"_between": {
						"_gte": "2025-01-01T00:00:00Z",
						"_lte": "2025-03-01T00:00:00Z"
					}
				}
			},
			"limit": 50
		},
		"request_query": "query Q { recommendations_v2 { rows { id severity status category estimated_savings } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "recommendations_v2", req.Table)
	assert.Contains(t, sql, "SELECT")
	assert.Contains(t, sql, "WHERE")
	assert.Contains(t, sql, "2025-01-01T00:00:00Z")
	assert.Contains(t, sql, "2025-03-01T00:00:00Z")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_ExplicitColumns(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "dw_queries_v2"
		},
		"input": {
			"columns": ["id", "tenant_id", "database_name", "query_type"],
			"where": {
				"tenant_id": { "_eq": "t1" }
			},
			"limit": 10
		},
		"request_query": "query Q { dw_queries_v2 { rows { id } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "dw_queries_v2", req.Table)
	// Columns should come from explicit input, not GQL
	colNames := lo.Map(req.Columns, func(c query.QueryColumn, _ int) string { return c.Name })
	assert.Contains(t, colNames, "id")
	assert.Contains(t, colNames, "database_name")
	assert.Contains(t, colNames, "query_type")

	assert.Contains(t, sql, "FROM dw_queries")
	assert.Contains(t, sql, "LIMIT 10")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_ColumnTransformations(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "dw_query_groupings_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "t1" }
			},
			"column_transformations": [
				{ "name": "query_started_at", "expr": "date_unit", "args": ["hour"] }
			],
			"group_by": ["query_started_at"],
			"limit": 100
		},
		"request_query": "query Q { dw_query_groupings_v2 { rows { query_started_at avg_query_exec_duration_micro } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	// Verify the transformation was applied
	for _, col := range req.Columns {
		if col.Name == "query_started_at" {
			assert.Equal(t, "date_unit", col.Expr)
			assert.Equal(t, []string{"hour"}, col.Args)
		}
	}

	assert.Contains(t, sql, "DATE_TRUNC('hour'")
	assert.Contains(t, sql, "GROUP BY")

	t.Logf("Generated SQL:\n%s", sql)
}

func TestE2E_WhereWithOrClause(t *testing.T) {
	payload := parsePostmanJSON(t, `{
		"action": {
			"name": "events_v2"
		},
		"input": {
			"where": {
				"tenant_id": { "_eq": "t1" },
				"_or": [
					{ "status": { "_eq": "firing" } },
					{ "status": { "_eq": "resolved" } }
				]
			},
			"limit": 10
		},
		"request_query": "query Q { events_v2 { rows { id status priority created_at } } }"
	}`)

	req, sql := simulateQueryPipeline(t, payload)

	assert.Equal(t, "events_v2", req.Table)
	assert.NotEmpty(t, req.Where.Or)
	assert.Contains(t, sql, "OR")

	t.Logf("Generated SQL:\n%s", sql)
}

// ---- fixWhereClause Unit Tests ----

func TestFixWhereClause_Simple(t *testing.T) {
	input := map[string]any{
		"name":       map[string]any{"_eq": "alice"},
		"account_id": map[string]any{"_in": []any{"a1", "a2"}},
	}
	result, err := fixWhereClause(input)
	require.NoError(t, err)

	// All non-composite fields go into _binary
	binary := result["_binary"].(map[string]any)
	assert.Contains(t, binary, "name")
	assert.Contains(t, binary, "account_id")
}

func TestFixWhereClause_WithAnd(t *testing.T) {
	input := map[string]any{
		"tenant_id": map[string]any{"_eq": "t1"},
		"_and": []any{
			map[string]any{"status": map[string]any{"_eq": "active"}},
			map[string]any{"age": map[string]any{"_gt": 18}},
		},
	}
	result, err := fixWhereClause(input)
	require.NoError(t, err)

	binary := result["_binary"].(map[string]any)
	assert.Contains(t, binary, "tenant_id")

	andClause := result["_and"].([]any)
	assert.Len(t, andClause, 2)

	// Each AND sub-clause should also have _binary
	sub0 := andClause[0].(map[string]any)
	assert.Contains(t, sub0, "_binary")
}

func TestFixWhereClause_Nested(t *testing.T) {
	input := map[string]any{
		"_and": []any{
			map[string]any{
				"_or": []any{
					map[string]any{"name": map[string]any{"_eq": "alice"}},
					map[string]any{"name": map[string]any{"_eq": "bob"}},
				},
			},
		},
	}
	result, err := fixWhereClause(input)
	require.NoError(t, err)

	andClause := result["_and"].([]any)
	assert.Len(t, andClause, 1)
	sub := andClause[0].(map[string]any)
	assert.Contains(t, sub, "_or")
}

func TestFixWhereClause_InvalidAndType(t *testing.T) {
	input := map[string]any{
		"_and": "not_a_slice",
	}
	_, err := fixWhereClause(input)
	assert.Error(t, err)
}

// ---- GQL Column Parsing Integration Tests ----

func TestE2E_GQLParsing_NestedRows(t *testing.T) {
	payload := ActionRequest{
		Action:       ActionRequestAction{Name: "llm_conversation_list_v2"},
		RequestQuery: `query Q { llm_conversation_list_v2(where: {}) { rows { id title source status } } }`,
	}
	cols, err := parseSelectColumns(&payload)
	require.NoError(t, err)

	names := lo.Map(cols, func(c query.QueryColumn, _ int) string { return c.Name })
	assert.Equal(t, []string{"id", "title", "source", "status"}, names)
}

func TestE2E_GQLParsing_WithExprArgs(t *testing.T) {
	payload := ActionRequest{
		Action:       ActionRequestAction{Name: "dw_query_groupings_v2"},
		RequestQuery: `query Q { dw_query_groupings_v2 { rows { timestamp:query_started_at(date_unit: "hour") avg_query_exec_duration_micro } } }`,
	}
	cols, err := parseSelectColumns(&payload)
	require.NoError(t, err)

	assert.Len(t, cols, 2)
	// First column should have expr and args from the GQL argument
	assert.Equal(t, "query_started_at", cols[0].Name)
	assert.Equal(t, "date_unit", cols[0].Expr)
	assert.Equal(t, []string{"hour"}, cols[0].Args)
}

func TestE2E_GQLParsing_FlatFields(t *testing.T) {
	payload := ActionRequest{
		Action:       ActionRequestAction{Name: "metrics_v2"},
		RequestQuery: `query Q { metrics_v2(where: {}) { id metric value timestamp } }`,
	}
	cols, err := parseSelectColumns(&payload)
	require.NoError(t, err)

	names := lo.Map(cols, func(c query.QueryColumn, _ int) string { return c.Name })
	assert.Equal(t, []string{"id", "metric", "value", "timestamp"}, names)
}
