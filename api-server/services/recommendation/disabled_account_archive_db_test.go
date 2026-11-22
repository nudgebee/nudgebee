package recommendation

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestArchiveOpenRecommendationsOnDisabledAccounts_DB runs the disabled-account
// sweep against a real Postgres. It is a destructive UPDATE whose reach is
// decided by an account-status predicate and a status filter; getting either
// wrong retires recommendations on live accounts or user-owned rows.
//
// Runs against throwaway tables, never the real ones, so it is safe against any
// database it is pointed at. DB-gated: skips with no database, fails in the job
// that provisions one.
func TestArchiveOpenRecommendationsOnDisabledAccounts_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	dbms, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = dbms.Close() })

	const (
		recTable     = "zz_disabled_acct_rec_test"
		accountTable = "zz_disabled_acct_account_test"
	)

	mustExec := func(q string, args ...any) {
		_, e := dbms.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() {
		for _, tbl := range []string{recTable, accountTable} {
			_, _ = dbms.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
		}
	}
	drop()
	t.Cleanup(drop)

	mustExec(fmt.Sprintf(`CREATE TABLE %s (id uuid PRIMARY KEY, status text NOT NULL)`, accountTable))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (id text PRIMARY KEY, cloud_account_id uuid, status text)`, recTable))

	const (
		activeAcct   = "00000000-0000-0000-0000-0000000000a1"
		disabledAcct = "00000000-0000-0000-0000-0000000000d1"
		inactiveAcct = "00000000-0000-0000-0000-0000000000d2"
	)
	mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,'active'),($2,'disabled'),($3,'inactive')`, accountTable),
		activeAcct, disabledAcct, inactiveAcct)

	rec := func(id, acct, status string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,$3)`, recTable), id, acct, status)
	}
	rec("active-open", activeAcct, "Open")
	rec("disabled-open-1", disabledAcct, "Open")
	rec("disabled-open-2", disabledAcct, "Open")
	rec("disabled-inprogress", disabledAcct, "InProgress")
	rec("disabled-assigned", disabledAcct, "Assigned")
	rec("disabled-closed", disabledAcct, "Closed")
	rec("disabled-dismissed", disabledAcct, "Dismissed")
	rec("inactive-open", inactiveAcct, "Open")

	archived, err := archiveOpenRecommendationsOnDisabledAccounts(context.Background(), dbms, recTable, accountTable)
	require.NoError(t, err)
	require.Equal(t, int64(3), archived)

	want := map[string]string{
		"active-open":         "Open",
		"disabled-open-1":     "Archive",
		"disabled-open-2":     "Archive",
		"disabled-inprogress": "InProgress",
		"disabled-assigned":   "Assigned",
		"disabled-closed":     "Closed",
		"disabled-dismissed":  "Dismissed",
		"inactive-open":       "Archive",
	}
	for id, status := range want {
		var got string
		require.NoError(t, dbms.Get(&got, fmt.Sprintf(`SELECT status FROM %s WHERE id = $1`, recTable), id))
		require.Equal(t, status, got, id)
	}

	again, err := archiveOpenRecommendationsOnDisabledAccounts(context.Background(), dbms, recTable, accountTable)
	require.NoError(t, err)
	require.Equal(t, int64(0), again, "a second pass must find nothing left to archive")
}
