package observability

import (
	"strings"
	"testing"

	"nudgebee/services/query"
)

// Regression for the logs_get_query "BETWEEN 0 AND 0" bug: when callers don't
// have a time window (e.g. SQL-preview path), buildPinotSQL must emit no time
// filter rather than a tautologically empty range that matches no rows.
func TestBuildPinotSQL_OmitsTimeFilter_WhenBothBoundsZero(t *testing.T) {
	sql := buildPinotSQL("k8s_logs_v2", "timestamp", `"ghgh" = '788'`, 0, 0, pinotTsMode{ScaleFactor: 1}, 1000, 0, nil)

	if strings.Contains(sql, "BETWEEN") {
		t.Fatalf("expected no BETWEEN when bounds are zero, got: %s", sql)
	}
	if !strings.Contains(sql, `WHERE "ghgh" = '788'`) {
		t.Fatalf("expected user where clause to appear unwrapped after WHERE, got: %s", sql)
	}
}

// When both time bounds and where clause are empty, the query must omit the
// WHERE clause entirely — emitting "WHERE " with nothing after it is invalid SQL.
func TestBuildPinotSQL_OmitsWhereClause_WhenBoundsAndClauseAllEmpty(t *testing.T) {
	sql := buildPinotSQL("k8s_logs_v2", "timestamp", "", 0, 0, pinotTsMode{ScaleFactor: 1}, 1000, 0, nil)

	if strings.Contains(sql, "WHERE") {
		t.Fatalf("expected no WHERE clause when bounds and clause are empty, got: %s", sql)
	}
	if !strings.Contains(sql, `SELECT * FROM "k8s_logs_v2"`) {
		t.Fatalf("expected SELECT * FROM table, got: %s", sql)
	}
	if !strings.Contains(sql, `ORDER BY "timestamp" DESC`) {
		t.Fatalf("expected default ORDER BY, got: %s", sql)
	}
}

// Sanity check: non-zero bounds still produce the BETWEEN predicate AND-combined
// with the where clause (the documented happy path).
func TestBuildPinotSQL_EmitsBetweenAndUserClause_WhenBoundsSet(t *testing.T) {
	sql := buildPinotSQL("k8s_logs_v2", "timestamp", `"ghgh" = '788'`, 1_700_000_000_000, 1_700_000_060_000, pinotTsMode{ScaleFactor: 1}, 1000, 0, nil)

	if !strings.Contains(sql, `"timestamp" BETWEEN 1700000000000 AND 1700000060000`) {
		t.Fatalf("expected numeric BETWEEN predicate, got: %s", sql)
	}
	if !strings.Contains(sql, `AND ("ghgh" = '788')`) {
		t.Fatalf("expected user where clause AND-combined and parenthesized, got: %s", sql)
	}
}

// Half-bounded query (start only) must use >= rather than BETWEEN X AND 0,
// which would zero-match and recreate the original bug class.
func TestBuildPinotSQL_EmitsGte_WhenOnlyStartBoundSet(t *testing.T) {
	sql := buildPinotSQL("k8s_logs_v2", "timestamp", "", 1_700_000_000_000, 0, pinotTsMode{ScaleFactor: 1}, 1000, 0, nil)

	if strings.Contains(sql, "BETWEEN") {
		t.Fatalf("expected no BETWEEN when only start bound set, got: %s", sql)
	}
	if !strings.Contains(sql, `"timestamp" >= 1700000000000`) {
		t.Fatalf("expected >= predicate for start-only bound, got: %s", sql)
	}
}

// Half-bounded query (end only) must use <= rather than BETWEEN 0 AND X,
// which would include rows from epoch zero.
func TestBuildPinotSQL_EmitsLte_WhenOnlyEndBoundSet(t *testing.T) {
	sql := buildPinotSQL("k8s_logs_v2", "timestamp", "", 0, 1_700_000_060_000, pinotTsMode{ScaleFactor: 1}, 1000, 0, nil)

	if strings.Contains(sql, "BETWEEN") {
		t.Fatalf("expected no BETWEEN when only end bound set, got: %s", sql)
	}
	if !strings.Contains(sql, `"timestamp" <= 1700000060000`) {
		t.Fatalf("expected <= predicate for end-only bound, got: %s", sql)
	}
}

// trace_id is rarely a real column in log tables — when the schema lacks it,
// the condition must be rewritten to a message-column LIKE so trace
// correlation works against the message text (mirrors the Loki line filter).
func TestRewritePinotTraceIDFilter_NoColumn_RewritesToMessageContains(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"trace_id": {query.Eq: "bf2edd20410b130854122b3cbe75e002"},
		},
	}
	rewritten := rewritePinotTraceIDFilter(where, nil, "message")
	sql, err := buildPinotWhereClause(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if sql != `"message" LIKE '%bf2edd20410b130854122b3cbe75e002%'` {
		t.Fatalf("expected message LIKE rewrite, got: %s", sql)
	}
}

// A table that declares a real trace_id column keeps the direct condition.
func TestRewritePinotTraceIDFilter_ColumnExists_KeepsDirectCondition(t *testing.T) {
	schema := &pinotSchemaResponse{
		DimensionFieldSpecs: []pinotFieldSpec{{Name: "trace_id", DataType: "STRING"}},
	}
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"trace_id": {query.Eq: "bf2edd20410b130854122b3cbe75e002"},
		},
	}
	rewritten := rewritePinotTraceIDFilter(where, schema, "message")
	sql, err := buildPinotWhereClause(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if sql != `"trace_id" = 'bf2edd20410b130854122b3cbe75e002'` {
		t.Fatalf("expected direct trace_id condition, got: %s", sql)
	}
}

// The rewrite must reach trace_id conditions nested under And clauses (the
// shape buildWorkloadLogWhereClause-style composites produce).
func TestRewritePinotTraceIDFilter_RewritesNestedAnd(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"kaas_namespace": {query.Eq: "shop"}}},
			{Binary: query.BinaryWhereClause{"trace_id": {query.Eq: "abc123"}}},
		},
	}
	rewritten := rewritePinotTraceIDFilter(where, nil, "message")
	sql, err := buildPinotWhereClause(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `"kaas_namespace" = 'shop'`) || !strings.Contains(sql, `"message" LIKE '%abc123%'`) {
		t.Fatalf("expected namespace condition plus message LIKE, got: %s", sql)
	}
	if strings.Contains(sql, `"trace_id"`) {
		t.Fatalf("trace_id column must not survive the rewrite, got: %s", sql)
	}
}

// The rewrite must not mutate the caller's clause — req.QueryRequest.Where is
// logged and reused by later pipeline stages after GetQuery runs.
func TestRewritePinotTraceIDFilter_DoesNotMutateInput(t *testing.T) {
	original := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"kaas_namespace": {query.Eq: "shop"}}},
			{Binary: query.BinaryWhereClause{"trace_id": {query.Eq: "abc123"}}},
		},
	}
	_ = rewritePinotTraceIDFilter(original, nil, "message")

	if _, ok := original.And[1].Binary["trace_id"]; !ok {
		t.Fatal("input clause lost its trace_id condition — rewrite mutated the caller's maps")
	}
	if _, ok := original.And[1].Binary["message"]; ok {
		t.Fatal("input clause gained a message condition — rewrite mutated the caller's maps")
	}
}
