package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Dispatcher coverage. Until this file existed the dispatcher had ZERO unit
// tests — the integration test in restart_resume_integration_test.go was
// the only safety net, and it requires a live Postgres + seed data.
//
// What we pin here:
//   - tick() with empty FetchDue is a no-op (no Submit, no error, no DB
//     traffic beyond the SELECT)
//   - tick() with one due row submits to the pool successfully
//   - tick() on a SATURATED pool (Submit times out) invokes
//     RescheduleNextPollAt with next_poll_at = now + poll_interval — this
//     is the hsundar finding #3 fix that previously had no behavioral test
//   - tick() does NOT panic when FetchDue itself errors

// fetchDueRows is the column set FetchDue's RETURNING clause emits. Kept
// in sync with manager.go's FetchDue query — when the column list changes
// in production, every test that builds rows needs to update too.
func fetchDueRows() []string {
	return []string{
		"id", "conversation_id", "account_id", "tenant_id", "user_id",
		"source_kind", "source_config", "predicate_kind", "predicate_expr",
		"predicate_negate", "notify_template", "poll_interval_sec",
		"max_duration_sec", "status", "poll_count", "failure_count",
		"next_poll_at", "last_poll_at", "last_poll_result", "final_result",
		"error", "expires_at", "created_at", "updated_at", "parent_message_id",
	}
}

// newDispatcherWithMocks wires a dispatcher to a sqlmock-backed Manager
// and the supplied pool so each test can construct a saturated or healthy
// pool depending on what it's exercising.
func newDispatcherWithMocks(t *testing.T, pool *common.WorkerPool) (*Dispatcher, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })

	mgr := &Manager{
		now: func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) },
		db:  sqlx.NewDb(rawDB, "postgresql"),
	}
	mock.MatchExpectationsInOrder(false)
	exec := NewExecutor(mgr)
	exec.SetNotifier(&capturingNotifier{})
	return &Dispatcher{manager: mgr, executor: exec, pool: pool}, mock
}

func TestDispatcher_Tick_NoDueRows_NoOp(t *testing.T) {
	pool := common.NewWorkerPool("test_disp_empty", 2, 10)
	t.Cleanup(pool.Stop)
	d, mock := newDispatcherWithMocks(t, pool)

	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(fetchDueRows()))

	require.NoError(t, d.tick())
	require.NoError(t, mock.ExpectationsWereMet(),
		"empty FetchDue must not emit any further SQL")
}

func TestDispatcher_Tick_FetchDueErrors_Returns(t *testing.T) {
	pool := common.NewWorkerPool("test_disp_fetch_err", 2, 10)
	t.Cleanup(pool.Stop)
	d, mock := newDispatcherWithMocks(t, pool)

	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WillReturnError(errors.New("connection refused"))

	err := d.tick()
	require.Error(t, err, "FetchDue failure must surface as a tick error so the leader job's log carries it")
	assert.Contains(t, err.Error(), "fetch due")
}

// TestDispatcher_Tick_SubmitFails_AdvancesNextPollAt is the behavioral test
// for hsundar finding #3b — Submit-fail must advance next_poll_at so the
// row doesn't recycle every tick. Without this the row stays at the head
// of FetchDue forever, starving the rest of the batch.
//
// We saturate the pool by:
//  1. building a 1-worker / 1-queue pool
//  2. blocking the worker on a never-finishing task
//  3. filling the queue with one extra task
//  4. setting WatchSubmitTimeoutSec=1 so Submit times out fast
func TestDispatcher_Tick_SubmitFails_AdvancesNextPollAt(t *testing.T) {
	pool := common.NewWorkerPool("test_disp_saturated", 1, 1)

	// Block the only worker forever, then fill the only queue slot so the
	// NEXT Submit must wait or time out.
	workerBlocked := make(chan struct{})
	worker := make(chan struct{})
	require.NoError(t, pool.Submit(context.Background(), func() {
		close(workerBlocked)
		<-worker // released by the defer below
	}))

	// Cleanup ordering matters: pool.Stop() blocks until all workers
	// return, and the worker is blocked on <-worker. We MUST close(worker)
	// BEFORE pool.Stop(). t.Cleanup runs in LIFO order, so register Stop
	// first and unblock second — that way unblock runs first, then Stop
	// drains cleanly. Documenting the ordering as load-bearing.
	t.Cleanup(pool.Stop)
	t.Cleanup(func() {
		select {
		case <-worker:
		default:
			close(worker)
		}
	})

	select {
	case <-workerBlocked:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not pick up the blocking task")
	}
	// One more task to fill the queue slot.
	require.NoError(t, pool.Submit(context.Background(), func() {}))

	withConfig(t, func() {
		config.Config.WatchSubmitTimeoutSec = 1 // bound the tick's Submit wait
	})

	d, mock := newDispatcherWithMocks(t, pool)

	// One due row → FetchDue returns it → tick tries Submit → times out →
	// RescheduleNextPollAt must fire with next_poll_at = now + poll_interval.
	dueID := uuid.New()
	dueTenant := uuid.New()
	dueAccount := uuid.New()
	now := time.Now()
	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(fetchDueRows()).
			AddRow(
				dueID, uuid.New(), dueAccount, dueTenant, nil,
				string(SourceKindTool), `{}`, string(PredicateKindRegex), "ok",
				false, nil, 30, 3600, string(StatusActive), 0, 0,
				now, nil, nil, nil, nil, now.Add(1*time.Hour), now, now, nil,
			))

	// RescheduleNextPollAt's UPDATE: tenant-scoped, status-gated, with the
	// new next_poll_at as the FIRST bound arg.
	var capturedNext time.Time
	mock.ExpectExec(`UPDATE llm_watch_tasks\s+SET next_poll_at`).
		WithArgs(
			nextPollAtCapture{out: &capturedNext},
			sqlmock.AnyArg(), // updated_at
			dueID,
			dueTenant,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, d.tick())
	require.NoError(t, mock.ExpectationsWereMet(),
		"Submit-fail must trigger RescheduleNextPollAt — otherwise the row recycles every tick")

	// next_poll_at must have advanced by ~poll_interval_sec (30s) from now.
	assert.WithinDuration(t, time.Now().Add(30*time.Second), capturedNext, 5*time.Second,
		"next_poll_at must advance by poll_interval_sec on Submit-fail")
}

// TestDispatcher_Tick_OneDueRow_SubmitsToPool exercises the happy path:
// the row is submitted to the pool and the test verifies the worker
// actually ran by capturing through the executor's notifier.
func TestDispatcher_Tick_OneDueRow_SubmitsToPool(t *testing.T) {
	pool := common.NewWorkerPool("test_disp_happy", 2, 10)
	t.Cleanup(pool.Stop)

	d, mock := newDispatcherWithMocks(t, pool)

	// Need a registered Source for the executor to run. Predicate not met
	// (regex doesn't match "still running") → UpdatePollResult.
	//
	// The source registry has no Unregister hook, so a hardcoded kind
	// would collide under `-count=N`. Generate a fresh kind each run.
	src := &fakeSource{
		kind:        SourceKind("fake_disp_happy_" + uuid.NewString()[:8]),
		observation: Observation{Data: "still running"},
	}
	RegisterSource(src)
	installSecurityContextBuilder(t)

	dueID := uuid.New()
	tenant := uuid.New()
	now := time.Now()
	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(fetchDueRows()).
			AddRow(
				dueID, uuid.New(), uuid.New(), tenant, nil,
				string(src.kind), `{}`, string(PredicateKindRegex), "completed",
				false, nil, 30, 3600, string(StatusActive), 0, 0,
				now, nil, nil, nil, nil, now.Add(1*time.Hour), now, now, nil,
			))
	// Worker will UpdatePollResult once predicate doesn't match.
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*SET status = 'ACTIVE'.*poll_count = poll_count \+ 1`).
		WithArgs(sqlmock.AnyArg(), "still running", sqlmock.AnyArg(), dueID, tenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, d.tick())

	// Wait for the worker to drain.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		src.mu.Lock()
		calls := src.observeCalls
		src.mu.Unlock()
		if calls >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	assert.Equal(t, 1, src.observeCalls, "dispatcher must dispatch the row to a worker that calls Observe")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRescheduleNextPollAt_BindsTenantIdInWhereClause guards the new
// function added for hsundar #3b. Without `AND tenant_id = $4` in the
// WHERE clause, a forged watch_id could push another tenant's row
// forward. The regex below pins BOTH the WHERE-clause status filter AND
// the tenant predicate so a regression that drops either fails this test
// (sqlmock's regex is anchored against the full statement text).
func TestRescheduleNextPollAt_BindsTenantIdInWhereClause(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	next := time.Now().Add(60 * time.Second)

	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks\s+SET next_poll_at = \$1.*WHERE id = \$3 AND tenant_id = \$4 AND status IN \('PENDING','ACTIVE'\)`).
		WithArgs(next, sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, m.RescheduleNextPollAt(context.Background(), tenant, id, next))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRescheduleNextPollAt_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	err := m.RescheduleNextPollAt(context.Background(), uuid.Nil, uuid.New(), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}
