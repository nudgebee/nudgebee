package adapter

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/require"
)

// TestMarkAbandonedPRCreationsInTables_DB verifies the reaper that retires
// resolutions stuck at "creating a pull request" with no URL recorded.
//
// These rows cannot self-heal by any other path: with no URL,
// GetRecommendationResolutionStatus answers InProgress forever, and the followup
// cron never selects them because their lifecycle state is not
// created/needs_followup. Left alone they block their recommendation from every
// later run — dev carried two from February and March 2026, one of which blocked
// ml-k8s-server for five months (#34959 follow-up).
//
// Uses throwaway tables mirroring the columns the SQL touches, so it needs no FK
// fixtures. DB-gated: skips when no database is reachable.
func TestMarkAbandonedPRCreationsInTables_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		// REQUIRE_DB_TESTS is set only by the job that provisions a database, so a
		// missing one there is a failure rather than a silent skip. The general
		// unit job has no database and must still skip.
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}

	tables := []string{"zz_pr_abandoned_test_a", "zz_pr_abandoned_test_b"}
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e)
	}
	for _, tbl := range tables {
		mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
		// Timestamps are `timestamp without time zone`, matching production. Using
		// timestamptz here would hide the session-timezone bug the SQL guards
		// against, exactly as pr_lifecycle_dispatch_test.go already notes.
		//
		// pr_lifecycle_state is nullable here, unlike the stale-sweep fixture: a
		// row that never got a URL never got a lifecycle state either, and NULL is
		// exactly the case that has to be retired.
		mustExec(fmt.Sprintf(`CREATE TABLE %s (
			id text PRIMARY KEY,
			type text NOT NULL,
			status text NOT NULL,
			type_reference_id text,
			pr_lifecycle_state text,
			pr_followup_pending boolean NOT NULL DEFAULT false,
			status_message text,
			last_pr_check_at timestamp,
			created_at timestamp NOT NULL
		)`, tbl))
		tblName := tbl
		t.Cleanup(func() { _, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tblName)) })
	}

	// created_at is seeded as naive UTC because that is what production writes
	// (time.Now().UTC() from Go). Seeding with bare now() instead stores session-
	// local time, which shifts both sides of the age comparison together and hides
	// a timezone-dependent predicate entirely — the bug this fixture exists to
	// catch. Note it only bites when the session timezone is ahead of UTC, so on a
	// UTC runner this case passes either way; it earns its keep on developer
	// machines, which is where it was caught.
	seed := func(tbl, id, typ, status, refID string, state *string, ageHours int) {
		mustExec(fmt.Sprintf(`INSERT INTO %s
			(id, type, status, type_reference_id, pr_lifecycle_state, created_at)
			VALUES ($1,$2,$3,$4,$5, (now() AT TIME ZONE 'UTC') - ($6 || ' hours')::interval)`, tbl),
			id, typ, status, refID, state, ageHours)
	}
	read := func(tbl, id string) (state *string, status string) {
		require.NoError(t, dbms.Db.QueryRow(
			fmt.Sprintf(`SELECT pr_lifecycle_state, status FROM %s WHERE id=$1`, tbl), id).Scan(&state, &status))
		return
	}
	lifecycle := func(s string) *string { return &s }

	// Should retire: a PR creation with no URL, older than the window.
	seed(tables[0], "abandoned_null_state", "PullRequest", "InProgress", "", nil, 24)
	seed(tables[1], "abandoned_created_state", "PullRequest", "InProgress", "", lifecycle("created"), 24*90)
	// The exact shape of dev's two stuck rows: the lifecycle already says the
	// system gave up, but status never left InProgress, so the row keeps being
	// polled and keeps reading as work in flight. Keying idempotence off the
	// lifecycle state would skip precisely these.
	seed(tables[0], "given_up_but_still_in_progress", "PullRequest", "InProgress", "", lifecycle("unresolvable"), 24*160)

	// Should NOT retire:
	seed(tables[0], "has_url", "PullRequest", "InProgress",
		"https://github.com/acme/infra/pull/900", lifecycle("created"), 24*90) // a real open PR
	seed(tables[0], "still_creating", "PullRequest", "InProgress", "", nil, 1) // inside the window
	// Three hours old: comfortably inside the 6h window, but nearest the edge.
	// A bare now() comparison against these naive-UTC columns retires this row
	// once the session timezone is ahead of UTC — under Asia/Kolkata it dies at
	// 3h instead of 6h, killing a creation that is still in flight.
	seed(tables[0], "half_way_through_window", "PullRequest", "InProgress", "", nil, 3)
	seed(tables[0], "not_in_progress", "PullRequest", "Failed", "", nil, 24*90)
	seed(tables[1], "merged_without_url", "PullRequest", "InProgress", "", lifecycle("merged"), 24*90)
	seed(tables[1], "ticket", "Ticket", "InProgress", "35838", nil, 24*90)

	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	retired := markAbandonedPRCreationsInTables(ctx, dbms, tables)
	require.Equal(t, int64(3), retired, "exactly the three abandoned creations should be retired")

	for _, c := range []struct{ tbl, id string }{
		{tables[0], "abandoned_null_state"},
		{tables[1], "abandoned_created_state"},
		{tables[0], "given_up_but_still_in_progress"},
	} {
		state, status := read(c.tbl, c.id)
		require.NotNil(t, state, "%s: lifecycle state must be set", c.id)
		require.Equal(t, "unresolvable", *state,
			"%s: no pull request was ever opened, so 'unresolvable' — not 'closed'", c.id)
		require.Equal(t, string(RecommendationResolutionStatusFailed), status,
			"%s: status must leave InProgress or the row keeps being polled and keeps blocking", c.id)
	}

	// Untouched means "exactly as seeded", so each case carries the status it went
	// in with — asserting InProgress for all of them would let a row that was
	// already settled pass for the wrong reason.
	for _, c := range []struct{ tbl, id, wantStatus, why string }{
		{tables[0], "has_url", "InProgress", "a pull request that actually exists must be left to the normal lifecycle"},
		{tables[0], "still_creating", "InProgress", "creation inside the window may still be in flight"},
		{tables[0], "half_way_through_window", "InProgress", "the window must not shrink with the session timezone"},
		{tables[0], "not_in_progress", "Failed", "the row is already settled"},
		{tables[1], "merged_without_url", "InProgress", "a recorded provider outcome is not an abandoned creation"},
		{tables[1], "ticket", "InProgress", "a ticket is not a pull request creation"},
	} {
		_, status := read(c.tbl, c.id)
		require.Equal(t, c.wantStatus, status, "%s: %s", c.id, c.why)
	}

	// Idempotent: a second sweep must find nothing left to do.
	require.Equal(t, int64(0), markAbandonedPRCreationsInTables(ctx, dbms, tables),
		"the sweep must not re-retire rows it already retired")
}
