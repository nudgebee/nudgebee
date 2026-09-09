package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Real-Postgres coverage for the predicate in GetActiveResolutionsForRecommendations.
//
// This exists because the sqlmock tests in optimizer_dao_wedge_test.go cannot
// catch a wrong predicate: they assert on the query TEXT and hand back canned
// rows, so they pass whatever the WHERE clause actually does. Both bugs in this
// family shipped under a green suite — #34943's test matched `pr_lifecycle_state
// <> ALL` and #35523 changed api-server's copy of the predicate while leaving
// this one alone. Only executing the SQL against a database catches that.
//
// Set TEST_POSTGRES_DSN to run. The dedicated CI job sets it from a postgres
// service container and also sets REQUIRE_DB_TESTS=true, which turns a missing
// DSN into a hard failure there — a test that silently does not run is how this
// class survived twice. Keying that on an explicit opt-in rather than on CI is
// deliberate: the general unit job runs `go test ./...` on a runner with no
// database and must still skip.

func blockingTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	db, err := sqlx.Connect("postgres", requireTestDSN(t))
	require.NoError(t, err, "connect to TEST_POSTGRES_DSN")

	// One throwaway schema per run so concurrent runs and repeat runs cannot see
	// each other's fixtures.
	schema := fmt.Sprintf("blocking_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`,
		schema, schema, schema))
	require.NoError(t, err)

	// Only the columns getResolutions reads. A faithful copy of the production
	// DDL is not the point — the predicate is.
	_, err = db.Exec(`CREATE TABLE recommendation_resolution (
		id                 uuid PRIMARY KEY,
		recommendation_id  uuid NOT NULL,
		type               text NOT NULL,
		data               jsonb,
		status             text NOT NULL,
		type_reference_id  text NOT NULL DEFAULT '',
		resolver_type      text NOT NULL,
		resolver_id        uuid NOT NULL,
		created_at         timestamp NOT NULL DEFAULT now(),
		updated_at         timestamp,
		status_message     text,
		pr_iteration_count integer NOT NULL DEFAULT 0,
		pr_lifecycle_state text,
		last_pr_check_at   timestamp
	)`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return db
}

// resolutionFixture is one seeded row plus what the gate must decide about it.
type resolutionFixture struct {
	name      string
	status    string
	refID     string
	resolver  string
	lifecycle *string
	rowType   string
	mustBlock bool
	because   string
}

func state(s string) *string { return &s }

func TestGetActiveResolutions_BlockingPredicate_AgainstPostgres(t *testing.T) {
	db := blockingTestDB(t)
	dao := &OptimizerDao{db: db}

	fixtures := []resolutionFixture{
		{
			name:      "creation the reconciler gave up on",
			status:    "InProgress",
			refID:     "",
			resolver:  "User",
			lifecycle: state("unresolvable"),
			rowType:   "PullRequest",
			mustBlock: false,
			because:   "dev 6204dca2 blocked ml-k8s-server for five months; 'unresolvable' means nothing will ever advance it",
		},
		{
			name:      "foreign PR closed months ago, never terminalised",
			status:    "Failed",
			refID:     "https://github.com/acme/infra/pull/472",
			resolver:  "User",
			lifecycle: nil,
			rowType:   "PullRequest",
			mustBlock: false,
			because:   "dev fae6ace9 blocked workflow-server for five months; the reconciler only selects InProgress so it can never release this",
		},
		{
			name:      "open PR the auto optimize raised itself",
			status:    "InProgress",
			refID:     "https://github.com/acme/infra/pull/900",
			resolver:  "AutoOptimize",
			lifecycle: state("created"),
			rowType:   "PullRequest",
			mustBlock: false,
			because:   "#34959: the run must reach the guard so it can refresh this PR in place",
		},
		{
			name:      "open PR a person raised by hand",
			status:    "InProgress",
			refID:     "https://github.com/acme/infra/pull/901",
			resolver:  "User",
			lifecycle: state("created"),
			rowType:   "PullRequest",
			mustBlock: true,
			because:   "we never rewrite a human's PR, and raising a second one is the duplicate #33523 prevents",
		},
		{
			name:      "human PR the cron retired but is still open on the provider",
			status:    "InProgress",
			refID:     "https://github.com/acme/infra/pull/902",
			resolver:  "User",
			lifecycle: state("stale"),
			rowType:   "PullRequest",
			mustBlock: true,
			because:   "'stale' means stop following up, not PR closed; a webhook can still resurrect it (#34943)",
		},
		{
			name:      "creation genuinely in flight",
			status:    "InProgress",
			refID:     "",
			resolver:  "AutoOptimize",
			lifecycle: nil,
			rowType:   "PullRequest",
			mustBlock: true,
			because:   "no URL yet and nobody has given up: there is nothing to compare against",
		},
		{
			name:      "ticket delegation in flight",
			status:    "InProgress",
			refID:     "35838",
			resolver:  "User",
			lifecycle: nil,
			rowType:   "Ticket",
			mustBlock: true,
			because:   "a ticket reference is not a URL; the first arm must keep covering non-PR resolutions",
		},
		{
			name:      "PR that merged",
			status:    "Success",
			refID:     "https://github.com/acme/infra/pull/903",
			resolver:  "AutoOptimize",
			lifecycle: state("merged"),
			rowType:   "PullRequest",
			mustBlock: false,
			because:   "#35523: a merged PR must not pin the recommendation to itself forever",
		},
	}

	// One recommendation per fixture, so a wrong verdict names the shape it came
	// from instead of collapsing into one ambiguous set.
	recIDs := make([]uuid.UUID, len(fixtures))
	for i, f := range fixtures {
		recIDs[i] = uuid.New()
		_, err := db.Exec(`INSERT INTO recommendation_resolution
			(id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id,
			 created_at, pr_lifecycle_state)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.New(), recIDs[i], f.rowType, f.status, f.refID, f.resolver, uuid.New(),
			time.Now().UTC().Add(-30*24*time.Hour), f.lifecycle)
		require.NoError(t, err, "seed %q", f.name)
	}

	got, err := dao.GetActiveResolutionsForRecommendations(context.Background(), recIDs)
	require.NoError(t, err)

	for i, f := range fixtures {
		blocked := len(got[recIDs[i]]) > 0
		if f.mustBlock {
			require.Truef(t, blocked,
				"%q must block the recommendation but did not — %s", f.name, f.because)
			continue
		}
		require.Falsef(t, blocked,
			"%q must not block the recommendation but did — %s", f.name, f.because)
	}
}

// TestGetActiveResolutions_UnreleasableRowsNeverBlock states the rule the two
// past recurrences broke, independently of the shapes above: this gate may only
// block on a row api-server's findOpenPRResolution would also return. Any row
// outside that set is unreleasable — no code path can advance it — so blocking
// on one strands its recommendation permanently.
func TestGetActiveResolutions_UnreleasableRowsNeverBlock(t *testing.T) {
	db := blockingTestDB(t)
	dao := &OptimizerDao{db: db}

	// findOpenPRResolution requires: URL like http%, status InProgress, lifecycle
	// not terminal. Every combination below fails at least one of those, so
	// api-server cannot see any of them.
	unreleasable := []struct {
		name      string
		status    string
		lifecycle *string
	}{
		{"failed with no lifecycle state", "Failed", nil},
		{"failed and retired stale", "Failed", state("stale")},
		{"success with no lifecycle state", "Success", nil},
		{"success left at created", "Success", state("created")},
		{"failed and already closed", "Failed", state("closed")},
	}

	recIDs := make([]uuid.UUID, len(unreleasable))
	for i, u := range unreleasable {
		recIDs[i] = uuid.New()
		_, err := db.Exec(`INSERT INTO recommendation_resolution
			(id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id,
			 created_at, pr_lifecycle_state)
			VALUES ($1, $2, 'PullRequest', $3, $4, 'User', $5, now() - interval '30 days', $6)`,
			uuid.New(), recIDs[i], u.status,
			fmt.Sprintf("https://github.com/acme/infra/pull/%d", 1000+i), uuid.New(), u.lifecycle)
		require.NoError(t, err, "seed %q", u.name)
	}

	got, err := dao.GetActiveResolutionsForRecommendations(context.Background(), recIDs)
	require.NoError(t, err)

	for i, u := range unreleasable {
		require.Emptyf(t, got[recIDs[i]],
			"%q blocks the recommendation but api-server's guard cannot see it, so nothing can ever release it", u.name)
	}
}

// requireTestDSN returns the scratch-Postgres DSN, or ends the test.
//
// Skips when unset so `go test ./...` on a machine with no database still
// passes, but FAILS when REQUIRE_DB_TESTS is set — that variable is exported
// only by the CI job that provisions a database, so the suite cannot quietly
// stop running in the one place that exists to run it. Keying it on an explicit
// opt-in rather than on CI is deliberate: the general unit job also runs in CI
// and legitimately has no database.
func requireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but TEST_POSTGRES_DSN is not: this suite must not be " +
				"skipped in the job that exists to run it — it is the only coverage that executes " +
				"these predicates against a database")
		}
		t.Skip("TEST_POSTGRES_DSN not set; start a scratch postgres and export it to run this suite")
	}
	return dsn
}
