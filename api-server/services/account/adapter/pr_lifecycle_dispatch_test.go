package adapter

import (
	"fmt"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPRLifecycleDispatchSQL_DB exercises the concurrency-critical dispatch SQL
// (claimOrMarkResolution + applyFollowupOutcome) against real Postgres, so the
// CTE syntax and the state transitions are verified, not just assumed. It runs
// against a throwaway regular table (not TEMP — TEMP is per-connection and the
// db handle is a pool) mirroring the columns the dispatch SQL touches, so it
// needs neither the V750 migration nor the recommendation FK fixtures.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestPRLifecycleDispatchSQL_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const tbl = "zz_pr_lifecycle_dispatch_test"
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
		last_pr_check_at timestamptz
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

	t.Run("claimOrMark", func(t *testing.T) {
		cases := []struct {
			id, state             string
			iters                 int
			wantOld, wantNewState string
			wantPending           bool
		}{
			{"c", "created", 0, "created", "addressing", false},                   // claim
			{"n", "needs_followup", 0, "needs_followup", "addressing", false},     // claim
			{"a", "addressing", 0, "addressing", "addressing", true},              // mark pending (run in flight)
			{"cap", "created", followupIterationCap, "created", "created", false}, // at cap -> no-op
			{"closed", "closed", 0, "closed", "closed", false},                    // terminal -> no-op
		}
		for _, c := range cases {
			seed(c.id, c.state, c.iters, false)
			old, iters, e := claimOrMarkResolution(dbms, tbl, c.id)
			require.NoError(t, e, c.id)
			assert.Equal(t, c.wantOld, old, "old_state for %s", c.id)
			assert.Equal(t, c.iters, iters, "old_iters for %s", c.id)
			st, _, pend := get(c.id)
			assert.Equal(t, c.wantNewState, st, "new state for %s", c.id)
			assert.Equal(t, c.wantPending, pend, "pending for %s", c.id)
		}
	})

	t.Run("finalize", func(t *testing.T) {
		ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		row := func(id string) prResolutionRow { return prResolutionRow{ID: id, TableName: tbl} }

		// success, with a signal that arrived mid-run -> reports pending, resets to
		// created, clears the flag (the caller will re-dispatch once).
		seed("fin_success", "addressing", 2, true)
		assert.True(t, applyFollowupOutcome(ctx, dbms, row("fin_success"), followupOutcomeSuccess))
		st, it, pend := get("fin_success")
		assert.Equal(t, "created", st)
		assert.Equal(t, 0, it)
		assert.False(t, pend)

		// failed, no mid-run signal -> no re-dispatch, needs_followup, counter +1.
		seed("fin_failed", "addressing", 1, false)
		assert.False(t, applyFollowupOutcome(ctx, dbms, row("fin_failed"), followupOutcomeFailed))
		st, it, _ = get("fin_failed")
		assert.Equal(t, "needs_followup", st)
		assert.Equal(t, 2, it)

		// no_op -> needs_followup, counter unchanged (so a late reviewer isn't capped).
		seed("fin_noop", "addressing", 1, false)
		assert.False(t, applyFollowupOutcome(ctx, dbms, row("fin_noop"), followupOutcomeNoOp))
		st, it, _ = get("fin_noop")
		assert.Equal(t, "needs_followup", st)
		assert.Equal(t, 1, it)

		// terminal guard: a PR-close flipped the row to 'closed' mid-run; finalize
		// must NOT resurrect it to an open state, but still clears pending.
		seed("fin_closed", "closed", 3, true)
		assert.True(t, applyFollowupOutcome(ctx, dbms, row("fin_closed"), followupOutcomeSuccess))
		st, it, pend = get("fin_closed")
		assert.Equal(t, "closed", st, "terminal state must be preserved")
		assert.Equal(t, 3, it, "counter must be untouched on terminal")
		assert.False(t, pend, "pending must be cleared")
	})
}
