package insight

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestInsightRefreshWrites_DB covers the two statements the 10-minute insight
// refresh writes with.
//
// What they must get right is decided in SQL, not Go: an insight that did not
// change must not be rewritten (that is the whole point — the refresh used to
// rewrite every row on every run), one that did change must be, and closing
// must reach exactly the accounts the run did not report. xmin is the check for
// "not rewritten": Postgres stamps it on every new row version, so it moves on
// any UPDATE, including one that sets a column to the value it already had.
//
// Uses a throwaway table mirroring insight's columns and unique key, so it needs
// no migrations (the convention pr_lifecycle_*_test.go set). DB-gated: skips
// when no database is reachable.
func TestInsightRefreshWrites_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		// REQUIRE_DB_TESTS is set only by the job that provisions a database, so a
		// missing one there is a failure rather than a silent skip.
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const table = "zz_insight_refresh_writes_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		created_at timestamp NOT NULL DEFAULT now(),
		updated_at timestamp NOT NULL DEFAULT now(),
		title text NOT NULL,
		type text NOT NULL,
		source text NOT NULL,
		account_id uuid NOT NULL,
		tenant uuid NOT NULL,
		unique_id text NOT NULL,
		resource_id uuid,
		status text NOT NULL,
		rule jsonb,
		applications jsonb,
		severity text,
		UNIQUE (tenant, account_id, unique_id)
	)`, table))
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
	})

	const (
		tenant   = "00000000-0000-0000-0000-0000000000aa"
		accountA = "00000000-0000-0000-0000-00000000000a"
		accountB = "00000000-0000-0000-0000-00000000000b"
		accountC = "00000000-0000-0000-0000-00000000000c"
		ruleID   = "42"
	)
	upsert := func(account, uniqueID, title, rule string) {
		mustExec(upsertInsightQuery(table), tenant, account, nil, uniqueID, title, "Open", rule,
			"Count", "Event", []byte(`[{"name":"checkout","namespace":"shop"}]`), "High")
	}
	closeUnreported := func(uniqueID string, reported ...string) {
		mustExec(closeUnreportedInsightsQuery(table), uniqueID, pq.Array(reported))
	}
	// rowVersion identifies the stored version of a row; it changes on every write.
	rowVersion := func(account, uniqueID string) (version, status, title string) {
		require.NoError(t, dbms.Db.QueryRow(fmt.Sprintf(
			`SELECT xmin::text, status, title FROM %s WHERE account_id = $1 AND unique_id = $2`, table),
			account, uniqueID).Scan(&version, &status, &title))
		return
	}

	const rule = `{"severity":"High","redirect_url":"/kubernetes/details/a#events"}`
	upsert(accountA, ruleID, "3 pods restarting", rule)
	upsert(accountB, ruleID, "1 pod restarting", rule)
	upsert(accountC, ruleID, "5 pods restarting", rule)

	t.Run("an unchanged insight is not rewritten", func(t *testing.T) {
		before, _, _ := rowVersion(accountA, ruleID)
		upsert(accountA, ruleID, "3 pods restarting", rule)
		after, status, _ := rowVersion(accountA, ruleID)
		require.Equal(t, before, after, "the same insight reported again must leave the row untouched")
		require.Equal(t, "Open", status)
	})

	t.Run("the same rule with its keys in another order is not a change", func(t *testing.T) {
		// The rule is marshalled from a struct on every run; equality has to be on
		// the JSON value, not its text.
		before, _, _ := rowVersion(accountA, ruleID)
		upsert(accountA, ruleID, "3 pods restarting", `{"redirect_url":"/kubernetes/details/a#events","severity":"High"}`)
		after, _, _ := rowVersion(accountA, ruleID)
		require.Equal(t, before, after)
	})

	t.Run("a changed insight is rewritten", func(t *testing.T) {
		before, _, _ := rowVersion(accountA, ruleID)
		upsert(accountA, ruleID, "4 pods restarting", rule)
		after, _, title := rowVersion(accountA, ruleID)
		require.NotEqual(t, before, after)
		require.Equal(t, "4 pods restarting", title)
	})

	t.Run("closing reaches only the accounts the run did not report", func(t *testing.T) {
		// Another rule's insight for the unreported account must stay open.
		upsert(accountB, "other-rule", "disk filling", rule)
		keptBefore, _, _ := rowVersion(accountA, ruleID)

		closeUnreported(ruleID, accountA, accountC)

		keptAfter, keptStatus, _ := rowVersion(accountA, ruleID)
		require.Equal(t, "Open", keptStatus)
		require.Equal(t, keptBefore, keptAfter, "a reported insight must not be touched by the close")
		_, droppedStatus, _ := rowVersion(accountB, ruleID)
		require.Equal(t, "CLOSED", droppedStatus)
		_, otherRuleStatus, _ := rowVersion(accountB, "other-rule")
		require.Equal(t, "Open", otherRuleStatus)
	})

	t.Run("an already closed insight is not rewritten by the next close", func(t *testing.T) {
		before, _, _ := rowVersion(accountB, ruleID)
		closeUnreported(ruleID, accountA, accountC)
		after, status, _ := rowVersion(accountB, ruleID)
		require.Equal(t, "CLOSED", status)
		require.Equal(t, before, after)
	})

	t.Run("a closed insight reported again reopens", func(t *testing.T) {
		upsert(accountB, ruleID, "1 pod restarting", rule)
		_, status, _ := rowVersion(accountB, ruleID)
		require.Equal(t, "Open", status, "status is part of what makes an insight changed")
	})

	t.Run("a run that reports nothing closes every insight of the rule", func(t *testing.T) {
		// What a failed rule looks like to the close: no accounts reported.
		closeUnreported(ruleID)
		for _, account := range []string{accountA, accountB, accountC} {
			_, status, _ := rowVersion(account, ruleID)
			require.Equal(t, "CLOSED", status)
		}
		_, otherRuleStatus, _ := rowVersion(accountB, "other-rule")
		require.Equal(t, "Open", otherRuleStatus)
	})
}
