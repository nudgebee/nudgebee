package query

import (
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecommendationResolutionGroupingsSavings_DB runs the
// recommendation_resolution_groupings_v2 SQL against a real Postgres, because
// what sum_rec_estimated_savings means is decided in the table's Def: one
// recommendation with several resolution rows of a status contributes its
// savings once to that status, a negative estimate contributes nothing, and
// `count` still counts every resolution row.
//
// Builds its own tables in a throwaway schema (the convention
// TestNotificationRuleScope_DB set). DB-gated: skips when no database is
// reachable.
func TestRecommendationResolutionGroupingsSavings_DB(t *testing.T) {
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

	const schema = "zz_recommendation_resolution_groupings_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE recommendation (
		id uuid PRIMARY KEY, tenant_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
		severity text, estimated_savings double precision)`)
	mustExec(`CREATE TABLE recommendation_resolution (
		id uuid PRIMARY KEY, recommendation_id uuid NOT NULL, type text NOT NULL,
		status text NOT NULL, resolver_type text NOT NULL)`)

	const (
		tenant      = "00000000-0000-0000-0000-0000000000aa"
		otherTenant = "00000000-0000-0000-0000-0000000000bb"
		account     = "00000000-0000-0000-0000-0000000000a1"
		otherAcct   = "00000000-0000-0000-0000-0000000000b1"
		twice       = "00000000-0000-0000-0000-000000000101" // $100, resolved by a ticket AND a pull request
		once        = "00000000-0000-0000-0000-000000000102" // $50
		costUp      = "00000000-0000-0000-0000-000000000103" // -$20: a cost increase, not a saving
		noEstimate  = "00000000-0000-0000-0000-000000000104" // NULL savings
		wip         = "00000000-0000-0000-0000-000000000105" // $30, still in progress
		foreign     = "00000000-0000-0000-0000-000000000106" // $1000, another tenant
	)
	rec := func(id, tenantID, accountID string, savings any) {
		mustExec(`INSERT INTO recommendation VALUES ($1, $2, $3, 'High', $4)`, id, tenantID, accountID, savings)
	}
	n := 0
	resolution := func(recID, typ, status string) {
		n++
		mustExec(`INSERT INTO recommendation_resolution VALUES ($1, $2, $3, $4, 'User')`,
			fmt.Sprintf("00000000-0000-0000-0000-%012d", 500+n), recID, typ, status)
	}
	rec(twice, tenant, account, 100)
	rec(once, tenant, account, 50)
	rec(costUp, tenant, account, -20)
	rec(noEstimate, tenant, account, nil)
	rec(wip, tenant, account, 30)
	rec(foreign, otherTenant, otherAcct, 1000)
	resolution(twice, "Ticket", "Success")
	resolution(twice, "PullRequest", "Success")
	resolution(twice, "PullRequest", "InProgress") // a third row, other status: counted under InProgress
	resolution(once, "PullRequest", "Success")
	resolution(costUp, "PullRequest", "Success")
	resolution(noEstimate, "Ticket", "Success")
	resolution(wip, "PullRequest", "InProgress")
	resolution(foreign, "PullRequest", "Success")

	td, ok := GetTableMetadata("recommendation_resolution_groupings_v2")
	require.True(t, ok)
	type row struct {
		Status  string  `db:"status"`
		Count   int     `db:"count"`
		Savings float64 `db:"sum_rec_estimated_savings"`
	}
	byStatus := func(where BinaryWhereClause) map[string]row {
		where["tenant_id"] = map[BinaryWhereClauseType]any{Eq: tenant}
		generated, genErr := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
			Table:   "recommendation_resolution_groupings_v2",
			Columns: cols("status", "count", "sum_rec_estimated_savings"),
			Where:   QueryWhereClause{Binary: where},
			GroupBy: []string{"status"},
		}, td)
		require.NoError(t, genErr)
		var rows []row
		require.NoError(t, db.Select(&rows, generated), generated)
		out := map[string]row{}
		for _, r := range rows {
			out[r.Status] = r
		}
		return out
	}

	t.Run("each recommendation's savings count once per status", func(t *testing.T) {
		got := byStatus(BinaryWhereClause{})
		require.Len(t, got, 2)
		assert.Equal(t, 5, got["Success"].Count, "count still counts every resolution row")
		assert.InDelta(t, 150, got["Success"].Savings, 1e-9, "100 once + 50; the cost increase and the NULL add nothing")
		assert.Equal(t, 2, got["InProgress"].Count)
		assert.InDelta(t, 130, got["InProgress"].Savings, 1e-9, "a status's dedup is independent of the other statuses")
	})

	t.Run("another tenant's resolutions stay out of the sum", func(t *testing.T) {
		got := byStatus(BinaryWhereClause{"account_id": {In: []string{otherAcct}}})
		assert.Empty(t, got, "the $1000 resolution belongs to another tenant")
	})

	t.Run("a scope that matches nothing sums to no rows, not a null", func(t *testing.T) {
		got := byStatus(BinaryWhereClause{"status": {Eq: "Failed"}})
		assert.Empty(t, got)
	})
}
