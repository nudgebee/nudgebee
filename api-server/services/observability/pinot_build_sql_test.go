package observability

import (
	"strings"
	"testing"
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
