package query

import (
	"database/sql"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventGroupingsCountByLatestRank_DB runs the count-by-aggregate SQL of
// event_groupings_v2 against a real Postgres. What it has to get right is
// arithmetic, which the generated text cannot show: triage re-scores an issue
// over time, so the events of one issue carry several ranks and severities, and
// every issue still has to land in exactly one bucket — the one its newest
// event names — for the buckets to add up to the number of issues.
//
// Builds its own table in a throwaway schema (the convention
// TestNotificationRuleScope_DB set). DB-gated: skips when no database is
// reachable.
func TestEventGroupingsCountByLatestRank_DB(t *testing.T) {
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
	db.SetMaxOpenConns(1)

	const schema = "zz_event_groupings_count_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE events (
		id serial PRIMARY KEY, tenant uuid NOT NULL, cloud_account_id uuid NOT NULL,
		created_at timestamp NOT NULL, fingerprint text, priority text, computed_priority text)`)

	const (
		tenant   = "00000000-0000-0000-0000-0000000000aa"
		accountA = "00000000-0000-0000-0000-0000000000a1"
		accountB = "00000000-0000-0000-0000-0000000000b1"
	)
	event := func(account, fingerprint, at, severity string, rank any) {
		mustExec(`INSERT INTO events (tenant, cloud_account_id, created_at, fingerprint, priority, computed_priority)
			VALUES ($1, $2, $3, $4, $5, $6)`, tenant, account, "2026-01-10 "+at, fingerprint, severity, rank)
	}
	// Re-scored down as it kept firing, and its severity dropped with it.
	event(accountA, "rescored", "12:00:00", "HIGH", "P2")
	event(accountA, "rescored", "12:10:00", "HIGH", "P2")
	event(accountA, "rescored", "12:20:00", "MEDIUM", "P3")
	// The same fingerprint under a second account is a second issue, and this
	// one was re-scored the other way.
	event(accountB, "rescored", "12:00:00", "LOW", "P3")
	event(accountB, "rescored", "12:30:00", "HIGH", "P2")
	event(accountA, "steady", "12:00:00", "HIGH", "P1")
	event(accountA, "unranked", "12:00:00", "INFO", nil)
	const issues = 4

	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)
	type bucket struct {
		Severity sql.NullString `db:"latest_priority"`
		Rank     sql.NullString `db:"latest_computed_priority"`
		EachRank sql.NullString `db:"computed_priority"`
		Count    int            `db:"event_count"`
	}
	run := func(req QueryRequest) []bucket {
		req.Table = "event_groupings_v2"
		if req.Where.Binary == nil {
			req.Where.Binary = BinaryWhereClause{}
		}
		req.Where.Binary["tenant_id"] = map[BinaryWhereClauseType]any{Eq: tenant}
		generated, genErr := GenerateSqlQuery(superAdminCtx(), "", req, td)
		require.NoError(t, genErr)
		var rows []bucket
		require.NoError(t, db.Select(&rows, generated), generated)
		return rows
	}
	distinctIssues := QueryColumn{Name: "event_count", Expr: "distinct", Args: []string{"account_id", "fingerprint"}}
	byRank := func(rows []bucket) map[string]int {
		counts := make(map[string]int, len(rows))
		for _, row := range rows {
			counts[row.Rank.String] += row.Count
		}
		return counts
	}
	bySeverityAndRank := func(rows []bucket) map[string]int {
		counts := make(map[string]int, len(rows))
		for _, row := range rows {
			counts[row.Severity.String+">"+row.Rank.String] += row.Count
		}
		return counts
	}
	total := func(counts map[string]int) int {
		sum := 0
		for _, count := range counts {
			sum += count
		}
		return sum
	}

	t.Run("each issue is counted once, under the rank of its newest event", func(t *testing.T) {
		counts := byRank(run(QueryRequest{
			Columns: []QueryColumn{{Name: "latest_computed_priority"}, distinctIssues},
			GroupBy: []string{"latest_computed_priority"},
		}))

		assert.Equal(t, map[string]int{"P1": 1, "P2": 1, "P3": 1, "": 1}, counts, `"" is the issue with no rank`)
		assert.Equal(t, issues, total(counts))

		// What grouping the events by their own rank answers instead: both
		// re-scored issues appear under P2 and again under P3.
		perEventRank := 0
		for _, row := range run(QueryRequest{
			Columns: []QueryColumn{{Name: "computed_priority"}, distinctIssues},
			GroupBy: []string{"computed_priority"},
		}) {
			perEventRank += row.Count
		}
		assert.Equal(t, issues+2, perEventRank)
	})

	t.Run("severity and rank are read off the same newest event", func(t *testing.T) {
		assert.Equal(t, map[string]int{"MEDIUM>P3": 1, "HIGH>P2": 1, "HIGH>P1": 1, "INFO>": 1}, bySeverityAndRank(run(QueryRequest{
			Columns: []QueryColumn{{Name: "latest_priority"}, {Name: "latest_computed_priority"}, distinctIssues},
			GroupBy: []string{"latest_priority", "latest_computed_priority"},
		})))
	})

	t.Run("newest means newest among the events the request selects", func(t *testing.T) {
		// A window that ends before either issue was re-scored.
		assert.Equal(t, map[string]int{"HIGH>P2": 1, "LOW>P3": 1, "HIGH>P1": 1, "INFO>": 1}, bySeverityAndRank(run(QueryRequest{
			Columns: []QueryColumn{{Name: "latest_priority"}, {Name: "latest_computed_priority"}, distinctIssues},
			GroupBy: []string{"latest_priority", "latest_computed_priority"},
			Where:   QueryWhereClause{Binary: BinaryWhereClause{"created_at": {Lte: "2026-01-10 12:15:00"}}},
		})))
	})

	t.Run("a filter on the rank counts the groups that pass it", func(t *testing.T) {
		listTotal := func(rank string) []bucket {
			return run(QueryRequest{
				Columns: []QueryColumn{
					{Name: "latest_computed_priority"},
					{Name: "event_count", Expr: "distinct", Args: []string{"tenant_id", "account_id", "fingerprint"}},
				},
				GroupBy: []string{"latest_computed_priority"},
				Having:  QueryWhereClause{Binary: BinaryWhereClause{"latest_computed_priority": {Eq: rank}}},
			})
		}

		perRank := 0
		for _, rank := range []string{"P1", "P2", "P3"} {
			rows := listTotal(rank)
			require.Len(t, rows, 1, rank)
			assert.Equal(t, 1, rows[0].Count, rank)
			perRank += rows[0].Count
		}
		assert.Equal(t, issues-1, perRank, "every ranked issue is in exactly one filtered list")
		assert.Empty(t, listTotal("P0"), "a rank no group has returns no row")
	})
}
