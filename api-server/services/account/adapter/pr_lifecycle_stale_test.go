package adapter

import (
	"fmt"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPRURLFromRow checks the PR-URL extraction used by the cron's per-URL dedup.
// Pure (no DB): a missing/blank pr_url returns "" so such rows are never deduped.
func TestPRURLFromRow(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"valid", `{"pr_url":"https://github.com/acme/infra/pull/42"}`, "https://github.com/acme/infra/pull/42"},
		{"no_pr_url", `{"branch":"main"}`, ""},
		{"empty_object", `{}`, ""},
		{"invalid_json", `not json`, ""},
		{"nil_data", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := prResolutionRow{}
			if c.data != "" {
				row.Data = []byte(c.data)
			}
			assert.Equal(t, c.want, prURLFromRow(row))
		})
	}
}

// TestMarkStaleResolutionsInTables_DB verifies the stale-sweep UPDATE against real
// Postgres: it retires only PR rows that are open, past the iteration cap, and
// older than followupStaleAfter — leaving recent rows, under-cap rows, in-flight
// ('addressing') rows, already-terminal rows, and non-PR rows untouched. Uses two
// throwaway tables mirroring the columns the SQL touches, so it needs no FK fixtures.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestMarkStaleResolutionsInTables_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	tables := []string{"zz_pr_stale_test_a", "zz_pr_stale_test_b"}
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	for _, tbl := range tables {
		mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
		mustExec(fmt.Sprintf(`CREATE TABLE %s (
			id text PRIMARY KEY,
			type text NOT NULL,
			status text NOT NULL,
			pr_lifecycle_state text NOT NULL,
			pr_iteration_count int NOT NULL DEFAULT 0,
			pr_followup_pending boolean NOT NULL DEFAULT false,
			status_message text,
			last_pr_check_at timestamptz,
			created_at timestamptz NOT NULL
		)`, tbl))
		tblName := tbl
		t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tblName)) })
	}

	// ageDays controls created_at; capped+old is the only combination that retires.
	seed := func(tbl, id, typ, status, state string, iters, ageDays int) {
		mustExec(fmt.Sprintf(`INSERT INTO %s
			(id, type, status, pr_lifecycle_state, pr_iteration_count, created_at)
			VALUES ($1,$2,$3,$4,$5, now() - ($6 || ' days')::interval)`, tbl),
			id, typ, status, state, iters, ageDays)
	}
	state := func(tbl, id string) (s string) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state FROM %s WHERE id=$1`, tbl), id).Scan(&s))
		return
	}

	// Should retire: open, at/over cap, older than 3 days.
	seed(tables[0], "stale_capped", "PullRequest", "InProgress", "needs_followup", followupIterationCap, 5)
	seed(tables[1], "stale_overcap", "PullRequest", "InProgress", "created", followupIterationCap+10, 9)
	// Should NOT retire:
	seed(tables[0], "recent", "PullRequest", "InProgress", "needs_followup", followupIterationCap, 1)     // too new
	seed(tables[0], "undercap", "PullRequest", "InProgress", "needs_followup", followupIterationCap-1, 9) // under cap
	seed(tables[0], "addressing", "PullRequest", "InProgress", "addressing", followupIterationCap, 9)     // in flight
	seed(tables[0], "merged", "PullRequest", "InProgress", "merged", followupIterationCap, 9)             // already terminal
	seed(tables[1], "not_pr", "Recommendation", "InProgress", "needs_followup", followupIterationCap, 9)  // wrong type
	seed(tables[1], "done", "PullRequest", "Success", "needs_followup", followupIterationCap, 9)          // not InProgress

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	n := markStaleResolutionsInTables(ctx, dbms, tables)
	assert.Equal(t, int64(2), n, "exactly the two open+capped+old PR rows are retired")

	assert.Equal(t, "stale", state(tables[0], "stale_capped"))
	assert.Equal(t, "stale", state(tables[1], "stale_overcap"))

	assert.Equal(t, "needs_followup", state(tables[0], "recent"), "recent row untouched")
	assert.Equal(t, "needs_followup", state(tables[0], "undercap"), "under-cap row untouched")
	assert.Equal(t, "addressing", state(tables[0], "addressing"), "in-flight row untouched")
	assert.Equal(t, "merged", state(tables[0], "merged"), "terminal row untouched")
	assert.Equal(t, "needs_followup", state(tables[1], "not_pr"), "non-PR row untouched")
	assert.Equal(t, "needs_followup", state(tables[1], "done"), "non-InProgress row untouched")
}

// TestResurrectStaleResolution_DB verifies the webhook-only resurrection: a 'stale'
// row is reset to needs_followup with a fresh (0) iteration budget and the pending
// flag cleared, while a non-stale row is left untouched (the guard prevents a
// concurrent terminal from being overwritten back to active).
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestResurrectStaleResolution_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const tbl = "zz_pr_resurrect_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id text PRIMARY KEY,
		pr_lifecycle_state text NOT NULL,
		pr_iteration_count int NOT NULL DEFAULT 0,
		pr_followup_pending boolean NOT NULL DEFAULT false
	)`, tbl))
	t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl)) })

	seed := func(id, state string, iters int, pending bool) {
		mustExec(fmt.Sprintf(`INSERT INTO %s (id, pr_lifecycle_state, pr_iteration_count, pr_followup_pending)
			VALUES ($1,$2,$3,$4)`, tbl), id, state, iters, pending)
	}
	get := func(id string) (state string, iters int, pending bool) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state, pr_iteration_count, pr_followup_pending FROM %s WHERE id=$1`, tbl), id).
			Scan(&state, &iters, &pending))
		return
	}

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)

	// stale row -> resurrected with a fresh budget.
	seed("stale_row", "stale", followupIterationCap+3, false)
	require.NoError(t, resurrectStaleResolution(ctx, dbms, "stale_row", tbl))
	st, it, pend := get("stale_row")
	assert.Equal(t, "needs_followup", st)
	assert.Equal(t, 0, it, "iteration budget reset")
	assert.False(t, pend)

	// non-stale row -> guard leaves it as-is.
	seed("merged_row", "merged", 2, false)
	require.NoError(t, resurrectStaleResolution(ctx, dbms, "merged_row", tbl))
	st, it, _ = get("merged_row")
	assert.Equal(t, "merged", st, "non-stale row must not be resurrected")
	assert.Equal(t, 2, it)
}
