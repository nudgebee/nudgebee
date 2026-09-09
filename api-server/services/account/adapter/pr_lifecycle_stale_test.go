package adapter

import (
	"fmt"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPRURLFromCandidate checks the PR-URL extraction used to group resolution
// candidates by PR (#36457). Pure (no DB): a missing/blank pr_url returns ""
// so such a candidate is never grouped (it's handled per-row instead — see
// markResolutionRowUnresolvable).
func TestPRURLFromCandidate(t *testing.T) {
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
			row := prResolutionCandidate{}
			if c.data != "" {
				row.Data = []byte(c.data)
			}
			assert.Equal(t, c.want, prURLFromCandidate(row))
		})
	}
}

// TestGroupCandidatesByPRURL pins two behaviors central to #36457's fix:
// candidates sharing a pr_url collapse into one group (this is what stops one
// PR from running two independent followup loops), and a candidate with no
// derivable pr_url is returned as an orphan rather than silently dropped —
// the caller retires orphans directly (markResolutionRowUnresolvable) so they
// don't get re-selected by every cron sweep forever.
func TestGroupCandidatesByPRURL(t *testing.T) {
	event := prResolutionCandidate{ID: "ev-1", TableName: "event_resolution", Data: []byte(`{"pr_url":"https://github.com/acme/infra/pull/1"}`)}
	rec := prResolutionCandidate{ID: "rec-1", TableName: "recommendation_resolution", Data: []byte(`{"pr_url":"https://github.com/acme/infra/pull/1"}`)}
	other := prResolutionCandidate{ID: "ev-2", TableName: "event_resolution", Data: []byte(`{"pr_url":"https://github.com/acme/infra/pull/2"}`)}
	orphan := prResolutionCandidate{ID: "ev-3", TableName: "event_resolution", Data: []byte(`{"branch":"main"}`)}

	groups, orphans := groupCandidatesByPRURL([]prResolutionCandidate{event, rec, other, orphan})

	require.Len(t, groups, 2, "two distinct PR URLs")
	require.Len(t, groups["https://github.com/acme/infra/pull/1"], 2, "an event row and a recommendation row for the same PR collapse into one group")
	assert.Equal(t, "ev-1", groups["https://github.com/acme/infra/pull/1"][0].ID, "event_resolution sorts first, matching the pre-#36457 tie-break")
	require.Len(t, groups["https://github.com/acme/infra/pull/2"], 1)

	require.Len(t, orphans, 1, "the pr_url-less candidate is not silently dropped")
	assert.Equal(t, "ev-3", orphans[0].ID)
}

// TestMarkStaleResolutionsInTable_DB verifies the stale-sweep UPDATE against real
// Postgres: it retires every open pr_followup row older than followupStaleAfter
// regardless of pr_iteration_count (#37472 — the counter only moves on `failed`,
// which never happens, so gating the exit on it never retired anything), while
// leaving recent rows, in-flight ('addressing') rows, and already-terminal rows
// untouched. Uses a throwaway table mirroring the columns the SQL touches, so it
// needs no FK fixtures and no real pr_followup migration applied.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestMarkStaleResolutionsInTable_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const tbl = "zz_pr_stale_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id text PRIMARY KEY,
		pr_lifecycle_state text NOT NULL,
		pr_iteration_count int NOT NULL DEFAULT 0,
		pr_followup_pending boolean NOT NULL DEFAULT false,
		status_message text,
		last_pr_check_at timestamptz,
		created_at timestamptz NOT NULL
	)`, tbl))
	t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl)) })

	// ageDays controls created_at; an open row older than followupStaleAfter
	// retires whatever its iteration count.
	seed := func(id, state string, iters, ageDays int) {
		mustExec(fmt.Sprintf(`INSERT INTO %s
			(id, pr_lifecycle_state, pr_iteration_count, created_at)
			VALUES ($1,$2,$3, now() - ($4 || ' days')::interval)`, tbl),
			id, state, iters, ageDays)
	}
	state := func(id string) (s string) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state FROM %s WHERE id=$1`, tbl), id).Scan(&s))
		return
	}

	// Should retire: open and older than 3 days, at any iteration count. The
	// zero_iters row is the exact #37472 case — the counter never moved off 0.
	seed("stale_zero_iters", "needs_followup", 0, 30)
	seed("stale_undercap", "needs_followup", followupIterationCap-1, 9)
	seed("stale_capped", "created", followupIterationCap, 5)
	// Should NOT retire:
	seed("recent", "needs_followup", 0, 1)                    // too new
	seed("addressing", "addressing", followupIterationCap, 9) // in flight
	seed("merged", "merged", followupIterationCap, 9)         // already terminal

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	n := markStaleResolutionsInTable(ctx, dbms, tbl)
	assert.Equal(t, int64(3), n, "every open row older than followupStaleAfter is retired")

	assert.Equal(t, "stale", state("stale_zero_iters"), "a row stuck at count 0 is still retired on age")
	assert.Equal(t, "stale", state("stale_undercap"))
	assert.Equal(t, "stale", state("stale_capped"))

	assert.Equal(t, "needs_followup", state("recent"), "recent row untouched")
	assert.Equal(t, "addressing", state("addressing"), "in-flight row untouched")
	assert.Equal(t, "merged", state("merged"), "terminal row untouched")
}

// TestReclaimStuckAddressingInTable_DB verifies the addressing-reaper UPDATE
// against real Postgres: it returns to 'needs_followup' only rows that are in
// 'addressing' and last checked longer ago than followupAddressingLease —
// preserving the iteration count and clearing the pending flag. Rows still
// within the lease (a genuinely in-flight run), non-addressing rows, and rows
// with a NULL last_pr_check_at are all left untouched.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestReclaimStuckAddressingInTable_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const tbl = "zz_pr_reclaim_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id text PRIMARY KEY,
		pr_lifecycle_state text NOT NULL,
		pr_iteration_count int NOT NULL DEFAULT 0,
		pr_followup_pending boolean NOT NULL DEFAULT false,
		status_message text,
		last_pr_check_at timestamptz,
		created_at timestamptz NOT NULL
	)`, tbl))
	t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl)) })

	// checkMinsAgo controls last_pr_check_at (negative sentinel -> NULL); older than
	// the 45-min lease is the only combination that reclaims. pending starts true so
	// we can assert it is cleared.
	seed := func(id, state string, iters, checkMinsAgo int) {
		if checkMinsAgo < 0 {
			mustExec(fmt.Sprintf(`INSERT INTO %s
				(id, pr_lifecycle_state, pr_iteration_count, pr_followup_pending, last_pr_check_at, created_at)
				VALUES ($1,$2,$3, true, NULL, now())`, tbl),
				id, state, iters)
			return
		}
		mustExec(fmt.Sprintf(`INSERT INTO %s
			(id, pr_lifecycle_state, pr_iteration_count, pr_followup_pending, last_pr_check_at, created_at)
			VALUES ($1,$2,$3, true, now() - ($4 || ' minutes')::interval, now())`, tbl),
			id, state, iters, checkMinsAgo)
	}
	row := func(id string) (state string, iters int, pending bool) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state, pr_iteration_count, pr_followup_pending FROM %s WHERE id=$1`, tbl), id).
			Scan(&state, &iters, &pending))
		return
	}

	// Should reclaim: addressing, checked longer ago than the lease.
	seed("stuck", "addressing", 3, 60)
	seed("stuck2", "addressing", 0, 120)
	// Should NOT reclaim:
	seed("in_flight", "addressing", 1, 10)  // within lease
	seed("needs", "needs_followup", 1, 60)  // not addressing
	seed("null_check", "addressing", 1, -1) // NULL last_check

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	n := reclaimStuckAddressingInTable(ctx, dbms, tbl)
	assert.Equal(t, int64(2), n, "exactly the two addressing+past-lease rows are reclaimed")

	s, it, p := row("stuck")
	assert.Equal(t, "needs_followup", s, "stuck row reclaimed")
	assert.Equal(t, 3, it, "iteration count preserved (dead attempt not charged)")
	assert.False(t, p, "pending flag cleared")
	s, _, _ = row("stuck2")
	assert.Equal(t, "needs_followup", s, "second stuck row reclaimed")

	assertState := func(id, want string) {
		s, _, _ := row(id)
		assert.Equal(t, want, s, "%s should be untouched", id)
	}
	assertState("in_flight", "addressing")
	assertState("needs", "needs_followup")
	assertState("null_check", "addressing")
}

// TestResurrectStalePRFollowupInTable_DB verifies the webhook-only resurrection: a
// 'stale' row is reset to needs_followup with a fresh (0) iteration budget, the
// pending flag cleared, and created_at bumped to now() so markStaleResolutions
// (which retires on created_at age alone, #37472) does not re-retire it on the
// next sweep. A non-stale row is left untouched (the guard prevents a concurrent
// terminal from being overwritten back to active).
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestResurrectStalePRFollowupInTable_DB(t *testing.T) {
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
		pr_url text NOT NULL,
		pr_lifecycle_state text NOT NULL,
		pr_iteration_count int NOT NULL DEFAULT 0,
		pr_followup_pending boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL DEFAULT now()
	)`, tbl))
	t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl)) })

	seed := func(id, prURL, state string, iters int, pending bool) {
		mustExec(fmt.Sprintf(`INSERT INTO %s (id, pr_url, pr_lifecycle_state, pr_iteration_count, pr_followup_pending, created_at)
			VALUES ($1,$2,$3,$4,$5, now() - interval '30 days')`, tbl), id, prURL, state, iters, pending)
	}
	get := func(id string) (state string, iters int, pending bool) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state, pr_iteration_count, pr_followup_pending FROM %s WHERE id=$1`, tbl), id).
			Scan(&state, &iters, &pending))
		return
	}
	ageDays := func(id string) (days float64) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT EXTRACT(EPOCH FROM now() - created_at) / 86400 FROM %s WHERE id=$1`, tbl), id).Scan(&days))
		return
	}

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)

	// stale row -> resurrected with a fresh budget and a fresh created_at clock.
	seed("stale_row", "https://github.com/acme/infra/pull/1", "stale", followupIterationCap+3, false)
	n, err := resurrectStalePRFollowupInTable(ctx, dbms, tbl, "https://github.com/acme/infra/pull/1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	st, it, pend := get("stale_row")
	assert.Equal(t, "needs_followup", st)
	assert.Equal(t, 0, it, "iteration budget reset")
	assert.False(t, pend)
	assert.Less(t, ageDays("stale_row"), 1.0, "created_at bumped to now() so the row is not re-staled next sweep")

	// non-stale row -> guard leaves it as-is.
	seed("merged_row", "https://github.com/acme/infra/pull/2", "merged", 2, false)
	n, err = resurrectStalePRFollowupInTable(ctx, dbms, tbl, "https://github.com/acme/infra/pull/2")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	st, it, _ = get("merged_row")
	assert.Equal(t, "merged", st, "non-stale row must not be resurrected")
	assert.Equal(t, 2, it)
}

// TestFindOrCreatePRFollowupInTable_DB verifies the lazy find-or-create used
// throughout the followup path (#36457): the first sighting of a PR URL
// creates a row seeded with the given created_at, and a second sighting for
// the same URL returns the SAME row id without changing it — the
// UNIQUE(pr_url) constraint plus ON CONFLICT is what collapses duplicate
// resolution rows for one PR onto a single followup entity.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestFindOrCreatePRFollowupInTable_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const tbl = "zz_pr_find_or_create_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		pr_url text NOT NULL UNIQUE,
		tenant_id text NOT NULL,
		pr_lifecycle_state text NOT NULL DEFAULT 'created',
		pr_iteration_count int NOT NULL DEFAULT 0,
		last_pr_check_at timestamptz,
		pr_followup_pending boolean NOT NULL DEFAULT false,
		status_message text,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	)`, tbl))
	t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl)) })

	const prURL = "https://github.com/acme/infra/pull/99"

	id1, err := findOrCreatePRFollowupInTable(dbms, tbl, prURL, "tenant-a", time.Time{})
	require.NoError(t, err)
	assert.NotEmpty(t, id1)

	var state string
	var iters int
	require.NoError(t, dbms.Db.QueryRow(
		fmt.Sprintf(`SELECT pr_lifecycle_state, pr_iteration_count FROM %s WHERE id=$1`, tbl), id1).
		Scan(&state, &iters))
	assert.Equal(t, "created", state)
	assert.Equal(t, 0, iters)

	// A second sighting for the same PR URL — from a duplicate resolution row,
	// or a later cron sweep — must return the SAME id, not create a sibling.
	id2, err := findOrCreatePRFollowupInTable(dbms, tbl, prURL, "tenant-a", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "duplicate candidates for one PR URL collapse onto one row")

	var count int
	require.NoError(t, dbms.Db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s WHERE pr_url=$1`, tbl), prURL).Scan(&count))
	assert.Equal(t, 1, count, "exactly one row exists for this PR URL")
}

// TestQueryOpenPRResolutionCandidates_SQLIsValid guards against a regression
// caught only by manual testing against a real Postgres: prResolutionOpenClause
// is a self-contained "(pr_lifecycle_state IS NULL OR ...)" predicate, and an
// earlier draft of this file concatenated a table alias directly onto it
// ("er." + prResolutionOpenClause), producing invalid SQL ("er.(...)") that
// Postgres rejects at parse time — CheckAndFollowupOpenPRs failed on every
// call. Nothing here asserts on row contents; the query running without a
// syntax error is the entire point.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestQueryOpenPRResolutionCandidates_SQLIsValid(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}
	_, err = queryOpenPRResolutionCandidates(dbms)
	require.NoError(t, err)
}

// TestFetchResolutionCandidatesByURL_SQLIsValid is the same guard as
// TestQueryOpenPRResolutionCandidates_SQLIsValid, for the webhook path's query
// (fetchResolutionCandidatesByURL), which concatenates prResolutionOpenClause
// the same way.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestFetchResolutionCandidatesByURL_SQLIsValid(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}
	_, err = fetchResolutionCandidatesByURL(dbms, "https://example.com/does-not-exist/pull/0")
	require.NoError(t, err)
}
