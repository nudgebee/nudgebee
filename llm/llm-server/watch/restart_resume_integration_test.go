package watch

import (
	"os"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // postgres driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartResume_Integration exercises the load-bearing acceptance
// criterion from issue #28294: "watches resume across llm-server restart".
//
// What the test proves:
//   - dispatcher #1 picks up an ACTIVE row whose next_poll_at is in the past
//     and the executor processes it through to a terminal state;
//   - after dispatcher #1 is discarded (the "restart"), a freshly-built
//     dispatcher #2 — with no shared in-memory state — can pick up another
//     due row from the same DB and process it correctly.
//
// Gated on env var so `go test ./...` skips it when no DB is reachable.
// To run locally:
//
//	WATCH_INTEGRATION_DB_URL='postgres://postgres:postgres@localhost:5434/llm?sslmode=disable' \
//	  go test -count=1 -run TestRestartResume_Integration -v ./watch/...
//
// Requires the local stack from docs/watch_local_testing.md (Postgres on
// :5434 with the V710 migration applied + the seed tenant/account rows).
func TestRestartResume_Integration(t *testing.T) {
	dbURL := os.Getenv("WATCH_INTEGRATION_DB_URL")
	if dbURL == "" {
		t.Skip("WATCH_INTEGRATION_DB_URL not set; skipping restart-resume integration test")
	}

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err, "could not connect to integration DB")
	t.Cleanup(func() { _ = db.Close() })

	// Use the seed tenant/account/conversation that watch_local_testing.md
	// creates. Fail fast with an actionable message if the seeds aren't in
	// place — the test relies on FK targets existing.
	tenant := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	account := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	conversation := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	requireSeedRow(t, db, "tenant", tenant)
	requireSeedRow(t, db, "cloud_accounts", account)
	requireSeedRow(t, db, "llm_conversations", conversation)

	// Clean any rows left behind by previous runs of this test, so each run
	// starts from a known empty slate. Filter is narrow enough to never
	// touch other test data.
	_, err = db.Exec(`
		DELETE FROM llm_watch_tasks
		 WHERE tenant_id = $1
		   AND predicate_expr = $2`,
		tenant, integrationMarker)
	require.NoError(t, err)

	// Wire test-only globals: a no-op security context builder so the
	// executor doesn't error on every poll, and a fake source registered
	// under the "tool" SourceKind. The CHECK constraint on
	// llm_watch_tasks.source_kind only allows 'tool' or 'sql'; we override
	// the production toolSource for the test's lifetime and restore at the
	// end via Cleanup.
	prevBuilder := getSecurityContextBuilder()
	SetSecurityContextBuilder(func(_ Watch) (*security.SecurityContext, error) {
		return security.NewSecurityContextForSuperAdmin(), nil
	})
	t.Cleanup(func() { SetSecurityContextBuilder(prevBuilder) })

	source := &fakeSource{
		kind:        SourceKindTool,
		observation: Observation{Data: "rolled out " + integrationMarker},
	}
	RegisterSource(source)
	// Restore the real toolSource after the test so subsequent tests in
	// the same `go test` run see the production behavior.
	t.Cleanup(func() { RegisterSource(&toolSource{}) })

	// One Manager wired to the integration DB. Reusing the same Manager
	// across phases is intentional — Manager is the persistence layer and
	// it's stateless across calls; what we're testing is that a new
	// Dispatcher + WorkerPool on top of the same Manager picks up rows.
	mgr := &Manager{now: time.Now, db: db}

	// -------------------------------------------------------------------
	// PHASE 1 — dispatcher #1 picks up a due row and processes it.
	// -------------------------------------------------------------------

	id1 := insertActiveWatch(t, db, tenant, account, conversation, source.kind)

	pool1 := common.NewWorkerPool("test_restart_resume_pool_1", 5, 20)
	notifier1 := &capturingNotifier{}
	exec1 := NewExecutor(mgr)
	exec1.SetNotifier(notifier1)
	disp1 := &Dispatcher{manager: mgr, executor: exec1, pool: pool1}

	require.NoError(t, disp1.tick(), "dispatcher #1 tick must succeed")

	waitForCallCount(t, notifier1, 1, 10*time.Second)

	final := readWatchStatus(t, db, id1)
	assert.Equal(t, string(StatusCompleted), final.status,
		"dispatcher #1 should have driven the row to COMPLETED")

	// Drain pool #1 — this is the "shutdown" half of the restart.
	pool1.Stop()

	// -------------------------------------------------------------------
	// PHASE 2 — discard dispatcher #1 entirely; a brand-new dispatcher
	// (different pool, different executor instance) must independently
	// pick up another due row from the same DB.
	// -------------------------------------------------------------------

	id2 := insertActiveWatch(t, db, tenant, account, conversation, source.kind)

	pool2 := common.NewWorkerPool("test_restart_resume_pool_2", 5, 20)
	t.Cleanup(pool2.Stop)
	notifier2 := &capturingNotifier{}
	exec2 := NewExecutor(mgr)
	exec2.SetNotifier(notifier2)
	disp2 := &Dispatcher{manager: mgr, executor: exec2, pool: pool2}

	require.NoError(t, disp2.tick(), "dispatcher #2 tick must succeed")

	waitForCallCount(t, notifier2, 1, 10*time.Second)

	final2 := readWatchStatus(t, db, id2)
	assert.Equal(t, string(StatusCompleted), final2.status,
		"dispatcher #2 should have driven the row to COMPLETED after restart")

	// Sanity: dispatcher #2's notifier must not have been called for id1
	// (post-terminal rows are no longer due). And dispatcher #1's notifier
	// must not have seen id2.
	for _, c := range notifier1.calls {
		assert.NotEqual(t, id2, c.WatchID, "dispatcher #1 must not see id2")
	}
	for _, c := range notifier2.calls {
		assert.NotEqual(t, id1, c.WatchID, "dispatcher #2 must not re-process the already-terminal id1")
	}

	// -------------------------------------------------------------------
	// PHASE 3 — confirm the resume contract on the *same* row.
	// Reset id1 back to ACTIVE with an overdue next_poll_at and ensure
	// dispatcher #2 picks it up. This is the strongest form of
	// "resume across restart": the row's state alone, not the
	// dispatcher's identity, drives correctness.
	// -------------------------------------------------------------------

	resetWatchToActive(t, db, id1)
	require.NoError(t, disp2.tick())
	waitForCallCount(t, notifier2, 2, 10*time.Second) // first id2, then resumed id1

	final3 := readWatchStatus(t, db, id1)
	assert.Equal(t, string(StatusCompleted), final3.status,
		"resumed row must reach COMPLETED on dispatcher #2's tick")
}

// ---------------------------------------------------------------------------
// integration-only helpers
// ---------------------------------------------------------------------------

// integrationMarker is the predicate_expr we use to identify rows owned by
// this test (and only this test) so cleanup / dedup is precise.
const integrationMarker = "integration-restart-resume-marker"

func insertActiveWatch(t *testing.T, db *sqlx.DB, tenant, account, conversation uuid.UUID, kind SourceKind) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now()
	_, err := db.Exec(`
		INSERT INTO llm_watch_tasks (
		  id, conversation_id, account_id, tenant_id, user_id,
		  source_kind, source_config,
		  predicate_kind, predicate_expr, predicate_negate,
		  notify_template, poll_interval_sec, max_duration_sec,
		  status, next_poll_at, expires_at, created_at, updated_at
		) VALUES (
		  $1, $2, $3, $4, NULL,
		  $5, $6::jsonb,
		  $7, $8, FALSE,
		  NULL, 30, 3600,
		  'ACTIVE', $9, $10, $11, $11
		)`,
		id, conversation, account, tenant,
		string(kind),
		// Minimal valid source_config for the "tool" kind. The fake source
		// installed by the test ignores it, but the JSONB column must
		// contain valid JSON.
		`{"tool_name":"unused_in_test","tool_input":{}}`,
		string(PredicateKindRegex), integrationMarker,
		now.Add(-1*time.Minute), // next_poll_at: overdue
		now.Add(1*time.Hour),    // expires_at: well in the future
		now,
	)
	require.NoError(t, err, "insert ACTIVE watch")
	return id
}

func resetWatchToActive(t *testing.T, db *sqlx.DB, id uuid.UUID) {
	t.Helper()
	_, err := db.Exec(`
		UPDATE llm_watch_tasks
		   SET status = 'ACTIVE',
		       next_poll_at = now() - interval '1 minute',
		       poll_count = 0,
		       failure_count = 0,
		       last_poll_at = NULL,
		       last_poll_result = NULL,
		       final_result = NULL,
		       error = NULL,
		       updated_at = now()
		 WHERE id = $1`, id)
	require.NoError(t, err)
}

type watchSnapshot struct {
	status    string
	pollCount int
}

func readWatchStatus(t *testing.T, db *sqlx.DB, id uuid.UUID) watchSnapshot {
	t.Helper()
	var s watchSnapshot
	err := db.QueryRow(`SELECT status, poll_count FROM llm_watch_tasks WHERE id = $1`, id).
		Scan(&s.status, &s.pollCount)
	require.NoError(t, err)
	return s
}

func waitForCallCount(t *testing.T, n *capturingNotifier, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n.callCount() >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected at least %d notify calls within %s; got %d", want, timeout, n.callCount())
}

func requireSeedRow(t *testing.T, db *sqlx.DB, table string, id uuid.UUID) {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM " + table + " WHERE id = $1"
	err := db.QueryRow(q, id).Scan(&n)
	require.NoError(t, err)
	if n == 0 {
		t.Fatalf(
			"seed row missing in %s (id=%s) — run docs/watch_local_testing.md "+
				"§3 to create the test tenant/account/conversation before "+
				"running this integration test",
			table, id)
	}
}
