package events

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// planEventDB is an isolated schema with the tables the duplicate rule reads, on a disposable PostgreSQL database
// (REMEDIATION_RUN_TEST_DB_URL, as the other remediation database tests).
func planEventDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_RUN_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_RUN_TEST_DB_URL to a disposable PostgreSQL database")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "plan_event_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	_, err = db.Exec(`CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, status text, nb_status text, labels jsonb);
		CREATE TABLE event_duplicates (event_id uuid, cloud_account_id uuid, first_event_id uuid, occurrence_number int);
		CREATE TABLE event_log_analysis (id uuid PRIMARY KEY, event_id uuid, cloud_account_id uuid, analysis_type text, status text, analysis text);
		CREATE TABLE event_analysis_mapping (event_id uuid, analysis_id uuid, analysis_type text)`)
	require.NoError(t, err)
	return db
}

type planEventFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
}

func (f planEventFixture) event(status, nbStatus string) string {
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events VALUES ($1, $2, $3, $4, '{}')`, id, f.account, status, nbStatus)
	require.NoError(f.t, err)
	return id
}

// duplicate makes dup the occurrence-2 DUPLICATE of main's chain.
func (f planEventFixture) duplicate(main, dup string) {
	_, err := f.db.Exec(`INSERT INTO event_duplicates VALUES ($1, $3, $1, 1), ($2, $3, $1, 2)`, main, dup, f.account)
	require.NoError(f.t, err)
}

func (f planEventFixture) analysis(event, kind, text string) {
	_, err := f.db.Exec(`INSERT INTO event_log_analysis VALUES ($1, $2, $3, $4, 'COMPLETED', $5)`, uuid.NewString(), event, f.account, kind, text)
	require.NoError(f.t, err)
}

// A duplicate follows its main event only while the main is live; otherwise, and when the main has no plan but
// the duplicate has one, it reads its own (e.g. a main event SUPPRESSED, closed, with no analyses).
func TestPlanEventOfDuplicates(t *testing.T) {
	db := planEventDB(t)
	f := planEventFixture{t: t, db: db, account: uuid.NewString()}
	plan := `{"root_cause":"memory limit"}`

	for _, tc := range []struct {
		name     string
		main     func() string
		dupPlan  bool
		followed bool
	}{
		{"main suppressed, closed, no analyses", func() string { return f.event("CLOSED", "SUPPRESSED") }, true, false},
		{"main suppressed even while firing", func() string { return f.event("FIRING", "SUPPRESSED") }, false, false},
		{"main resolved", func() string {
			m := f.event("CLOSED", "RESOLVED")
			f.analysis(m, "remediation", plan)
			return m
		}, false, false},
		{"main live and firing, with a plan", func() string {
			m := f.event("FIRING", "OPEN")
			f.analysis(m, "remediation", plan)
			return m
		}, false, true},
		{"main live: closed but investigated", func() string {
			m := f.event("CLOSED", "OPEN")
			f.analysis(m, "investigation", "{}")
			return m
		}, false, true},
		{"main closed with nothing done on it", func() string { return f.event("CLOSED", "OPEN") }, false, false},
		{"main live but without a plan, duplicate with one", func() string { return f.event("FIRING", "OPEN") }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := tc.main()
			dup := f.event("FIRING", "DUPLICATE")
			f.duplicate(main, dup)
			if tc.dupPlan {
				f.analysis(dup, "remediation", plan)
			}
			want, wantPrimary := dup, ""
			if tc.followed {
				want, wantPrimary = main, main
			}
			got, err := PlanEventOf(t.Context(), db, f.account, dup)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			primary, err := PrimaryEventOf(t.Context(), db, f.account, dup)
			require.NoError(t, err)
			assert.Equal(t, wantPrimary, primary, "no primary_event_id unless the duplicate follows its main")
			own, err := PrimaryEventOf(t.Context(), db, f.account, main)
			require.NoError(t, err)
			assert.Empty(t, own, "a main event reads its own plan")
		})
	}
}
