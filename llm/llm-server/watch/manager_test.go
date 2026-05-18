package watch

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"

	"nudgebee/llm/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// newMockManager builds a Manager whose DB is a fresh sqlmock connection
// and whose clock is frozen. Each test gets its own *sqlmock so expectations
// don't leak (common.GetDatabaseManager caches at the package level, which
// would defeat per-test mocks if we routed through it).
func newMockManager(t *testing.T) (*Manager, sqlmock.Sqlmock, time.Time) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })

	frozen := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	m := &Manager{
		now: func() time.Time { return frozen },
		db:  sqlx.NewDb(rawDB, "postgresql"),
	}
	mock.MatchExpectationsInOrder(false)
	return m, mock, frozen
}

func validCreateInput() CreateInput {
	return CreateInput{
		ConversationID:  uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		AccountID:       uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		TenantID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		SourceKind:      SourceKindTool,
		SourceConfig:    json.RawMessage(`{"tool_name":"shell_execute","tool_input":{"command":"echo ok"}}`),
		PredicateKind:   PredicateKindRegex,
		PredicateExpr:   "ok",
		PollIntervalSec: 60,
		MaxDurationSec:  3600,
	}
}

// withConfig snapshots and restores the SPECIFIC Watch* fields that tests in
// this package mutate. Previously this saved/restored the whole config.Config
// struct via `prev := config.Config` / `config.Config = prev` — that whole-
// struct memcpy on cleanup raced under `-race -count=N` with the gocron
// `worker_heartbeat` job that common.init() starts (the heartbeat reads
// unrelated DB-config fields concurrently, and a whole-struct write happens
// to touch those addresses).
//
// Restoring only the Watch* fields the tests touch closes that race: the
// gocron heartbeat reads DB-config fields, never Watch* fields, so a field-
// by-field restore touches a disjoint memory set.
//
// If a future test mutates a Watch* field not enumerated below, add it here
// rather than reverting to whole-struct save.
func withConfig(t *testing.T, mutate func()) {
	t.Helper()
	prev := struct {
		MaxFailures      int
		MinIntervalSec   int
		MaxDurationSec   int
		MaxPerTenant     int
		SubmitTimeoutSec int
		Enabled          bool
		SqlSourceEnabled bool
	}{
		MaxFailures:      config.Config.WatchMaxFailures,
		MinIntervalSec:   config.Config.WatchMinIntervalSec,
		MaxDurationSec:   config.Config.WatchMaxDurationSec,
		MaxPerTenant:     config.Config.WatchMaxPerTenant,
		SubmitTimeoutSec: config.Config.WatchSubmitTimeoutSec,
		Enabled:          config.Config.WatchEnabled,
		SqlSourceEnabled: config.Config.WatchSqlSourceEnabled,
	}
	mutate()
	t.Cleanup(func() {
		config.Config.WatchMaxFailures = prev.MaxFailures
		config.Config.WatchMinIntervalSec = prev.MinIntervalSec
		config.Config.WatchMaxDurationSec = prev.MaxDurationSec
		config.Config.WatchMaxPerTenant = prev.MaxPerTenant
		config.Config.WatchSubmitTimeoutSec = prev.SubmitTimeoutSec
		config.Config.WatchEnabled = prev.Enabled
		config.Config.WatchSqlSourceEnabled = prev.SqlSourceEnabled
	})
}

// ---------------------------------------------------------------------------
// Create — input validation
// ---------------------------------------------------------------------------

func TestCreate_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CreateInput)
		err    string
	}{
		{"missing conversation_id", func(in *CreateInput) { in.ConversationID = uuid.Nil }, "conversation_id"},
		{"missing account_id", func(in *CreateInput) { in.AccountID = uuid.Nil }, "account_id"},
		{"missing tenant_id", func(in *CreateInput) { in.TenantID = uuid.Nil }, "tenant_id"},
		{"missing source_kind", func(in *CreateInput) { in.SourceKind = "" }, "source_kind"},
		{"missing source_config", func(in *CreateInput) { in.SourceConfig = nil }, "source_config"},
		{"invalid source_config json", func(in *CreateInput) { in.SourceConfig = json.RawMessage("not json") }, "valid JSON"},
		{"missing predicate_kind", func(in *CreateInput) { in.PredicateKind = "" }, "predicate_kind"},
		{"missing predicate_expr", func(in *CreateInput) { in.PredicateExpr = "" }, "predicate_expr"},
		{"poll_interval_sec <= 0", func(in *CreateInput) { in.PollIntervalSec = 0 }, "poll_interval_sec"},
		{"max_duration_sec <= 0", func(in *CreateInput) { in.MaxDurationSec = 0 }, "max_duration_sec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newMockManager(t)
			in := validCreateInput()
			tc.mutate(&in)
			_, err := m.Create(context.Background(), in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestCreate_RejectsUnknownSourceKind(t *testing.T) {
	m, _, _ := newMockManager(t)
	in := validCreateInput()
	in.SourceKind = SourceKind("nonexistent")
	_, err := m.Create(context.Background(), in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown source_kind")
}

func TestCreate_RejectsBadPredicateAtCreateTime(t *testing.T) {
	// Watch users find out their regex is busted in watch_resource, not
	// 30s later in the dispatcher.
	m, _, _ := newMockManager(t)
	in := validCreateInput()
	in.PredicateExpr = "(unclosed"
	_, err := m.Create(context.Background(), in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid regex")
}

// ---------------------------------------------------------------------------
// Create — clamping
// ---------------------------------------------------------------------------

func TestCreate_ClampsPollIntervalUpward(t *testing.T) {
	withConfig(t, func() {
		config.Config.WatchMinIntervalSec = 30
		config.Config.WatchMaxDurationSec = 0 // off — focus on min interval
		config.Config.WatchMaxPerTenant = 0   // disable quota
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	in := validCreateInput()
	in.PollIntervalSec = 5 // way under min
	w, err := m.Create(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, 30, w.PollIntervalSec, "min-interval clamp not applied")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_ClampsMaxDurationDownward(t *testing.T) {
	withConfig(t, func() {
		config.Config.WatchMinIntervalSec = 0
		config.Config.WatchMaxDurationSec = 3600 // 1h cap
		config.Config.WatchMaxPerTenant = 0
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	in := validCreateInput()
	in.MaxDurationSec = 99999 // far over cap
	w, err := m.Create(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, 3600, w.MaxDurationSec, "max-duration clamp not applied")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DoesNotClampWhenConfigZero(t *testing.T) {
	// Zero config means "no clamp" — the agent's value is honored as-is.
	withConfig(t, func() {
		config.Config.WatchMinIntervalSec = 0
		config.Config.WatchMaxDurationSec = 0
		config.Config.WatchMaxPerTenant = 0
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	in := validCreateInput()
	in.PollIntervalSec = 1
	in.MaxDurationSec = 1_000_000
	w, err := m.Create(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, 1, w.PollIntervalSec)
	assert.Equal(t, 1_000_000, w.MaxDurationSec)
}

// ---------------------------------------------------------------------------
// Create — tenant quota
// ---------------------------------------------------------------------------

func TestCreate_RejectsWhenTenantAtCap(t *testing.T) {
	withConfig(t, func() {
		config.Config.WatchMaxPerTenant = 5
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	in := validCreateInput()
	_, err := m.Create(context.Background(), in)
	require.ErrorIs(t, err, ErrTenantQuotaExceeded)
}

func TestCreate_AllowsBelowCap(t *testing.T) {
	withConfig(t, func() {
		config.Config.WatchMaxPerTenant = 5
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	_, err := m.Create(context.Background(), validCreateInput())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_AtomicQuotaGuardRejectsWhenInsertAffectsZeroRows(t *testing.T) {
	// Pre-check passes (count=4 < cap=5) but a concurrent Create filled the
	// last slot before our INSERT … SELECT … WHERE (count) < cap ran, so the
	// guarded INSERT affects 0 rows. The atomic guard must surface this as
	// ErrTenantQuotaExceeded rather than silently returning a phantom watch.
	withConfig(t, func() {
		config.Config.WatchMaxPerTenant = 5
	})
	m, mock, _ := newMockManager(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := m.Create(context.Background(), validCreateInput())
	require.ErrorIs(t, err, ErrTenantQuotaExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_QuotaCheckBypassedWhenConfigIsZero(t *testing.T) {
	// MaxPerTenant=0 disables the cap entirely. assertTenantQuota must NOT
	// even issue the COUNT query in that case.
	withConfig(t, func() {
		config.Config.WatchMaxPerTenant = 0
	})
	m, mock, _ := newMockManager(t)
	// No COUNT expectation — only INSERT.
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	_, err := m.Create(context.Background(), validCreateInput())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_PersistsAllFields(t *testing.T) {
	withConfig(t, func() { config.Config.WatchMaxPerTenant = 0 })
	m, mock, frozen := newMockManager(t)
	in := validCreateInput()
	in.PredicateNegate = true
	in.NotifyTemplate = "Done: {summary}"

	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WithArgs(
			sqlmock.AnyArg(), // id (random)
			in.ConversationID, in.AccountID, in.TenantID, in.UserID,
			string(in.SourceKind), string(in.SourceConfig),
			string(in.PredicateKind), in.PredicateExpr, true,
			"Done: {summary}",
			in.PollIntervalSec, in.MaxDurationSec,
			string(StatusPending),
			frozen.Add(time.Duration(in.PollIntervalSec)*time.Second),
			frozen.Add(time.Duration(in.MaxDurationSec)*time.Second),
			frozen, frozen,
			in.ParentMessageID, // parent_message_id (nil unless set on the input)
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	w, err := m.Create(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, w.Status)
	assert.True(t, w.PredicateNegate)
	require.NotNil(t, w.NotifyTemplate)
	assert.Equal(t, "Done: {summary}", *w.NotifyTemplate)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Get — tenant scoping (the security regression test we need to lock in)
// ---------------------------------------------------------------------------

func TestGet_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	_, err := m.Get(context.Background(), uuid.Nil, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestGet_FiltersByTenant(t *testing.T) {
	m, mock, _ := newMockManager(t)
	wantID := uuid.New()
	wantTenant := uuid.New()

	// Cross-tenant attempt: same id, different tenant — must hit the WHERE
	// clause and return zero rows → ErrWatchNotFound.
	mock.ExpectQuery(`SELECT .* FROM llm_watch_tasks WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs(wantID, wantTenant).
		WillReturnError(errSQLNoRows())

	_, err := m.Get(context.Background(), wantTenant, wantID)
	require.ErrorIs(t, err, ErrWatchNotFound)
}

// ---------------------------------------------------------------------------
// Cancel — tenant scoping + idempotency
// ---------------------------------------------------------------------------

func TestCancel_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	err := m.Cancel(context.Background(), uuid.Nil, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestCancel_HappyPath(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = 'CANCELLED'`).
		WithArgs(sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, m.Cancel(context.Background(), tenant, id))
}

func TestCancel_AlreadyTerminalIsNoopSuccess(t *testing.T) {
	// rows_affected=0 + the row exists for this tenant (status terminal)
	// → treated as silent success per the docstring ("idempotent").
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = 'CANCELLED'`).
		WithArgs(sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT .* FROM llm_watch_tasks WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs(id, tenant).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "account_id", "tenant_id", "user_id",
			"source_kind", "source_config", "predicate_kind", "predicate_expr",
			"predicate_negate", "notify_template", "poll_interval_sec",
			"max_duration_sec", "status", "poll_count", "failure_count",
			"next_poll_at", "last_poll_at", "last_poll_result", "final_result",
			"error", "expires_at", "created_at", "updated_at", "parent_message_id",
		}).AddRow(
			id, uuid.New(), uuid.New(), tenant, nil,
			string(SourceKindTool), `{}`, string(PredicateKindRegex), "ok",
			false, nil, 60, 3600, string(StatusCompleted), 1, 0,
			time.Now(), nil, nil, nil, nil, time.Now(), time.Now(), time.Now(), nil,
		))

	require.NoError(t, m.Cancel(context.Background(), tenant, id))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCancel_NotFoundReturnsErrWatchNotFound(t *testing.T) {
	// rows_affected=0 + the post-check Get also returns no row → not found.
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = 'CANCELLED'`).
		WithArgs(sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT .* FROM llm_watch_tasks WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs(id, tenant).
		WillReturnError(errSQLNoRows())

	require.ErrorIs(t, m.Cancel(context.Background(), tenant, id), ErrWatchNotFound)
}

func TestCancel_CrossTenantBecomesNotFound(t *testing.T) {
	// Same id, but caller supplies tenant B. UPDATE no-ops; post-check Get
	// also no-ops (because Get filters by tenant). Result: NotFound.
	// This is the cross-tenant cancellation regression — must stay fixed.
	m, mock, _ := newMockManager(t)
	id, attackerTenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = 'CANCELLED'`).
		WithArgs(sqlmock.AnyArg(), id, attackerTenant).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT .* FROM llm_watch_tasks WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs(id, attackerTenant).
		WillReturnError(errSQLNoRows())

	require.ErrorIs(t, m.Cancel(context.Background(), attackerTenant, id), ErrWatchNotFound)
}

// ---------------------------------------------------------------------------
// FetchDue
// ---------------------------------------------------------------------------

func TestFetchDue_DefaultLimit(t *testing.T) {
	m, mock, _ := newMockManager(t)
	// FetchDue is now an UPDATE ... FOR UPDATE SKIP LOCKED ... RETURNING.
	// Two bound args: $1 = limit, $2 = lease seconds. The regex pins both
	// the SKIP LOCKED clause (atomic claim is the whole point) and the
	// status filter so a refactor that drops either becomes visible.
	mock.ExpectQuery(`FOR UPDATE SKIP LOCKED`).
		WithArgs(100, fetchDueClaimLeaseSec).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "account_id", "tenant_id", "user_id",
			"source_kind", "source_config", "predicate_kind", "predicate_expr",
			"predicate_negate", "notify_template", "poll_interval_sec",
			"max_duration_sec", "status", "poll_count", "failure_count",
			"next_poll_at", "last_poll_at", "last_poll_result", "final_result",
			"error", "expires_at", "created_at", "updated_at", "parent_message_id",
		}))

	out, err := m.FetchDue(context.Background(), 0) // 0 → use default
	require.NoError(t, err)
	assert.Empty(t, out)
}

// ---------------------------------------------------------------------------
// MarkTerminal
// ---------------------------------------------------------------------------

func TestMarkTerminal_RejectsNonTerminalStatus(t *testing.T) {
	m, _, _ := newMockManager(t)
	_, err := m.MarkTerminal(context.Background(), uuid.New(), uuid.New(), StatusPending, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not terminal")

	_, err = m.MarkTerminal(context.Background(), uuid.New(), uuid.New(), StatusActive, "", "")
	require.Error(t, err)
}

func TestMarkTerminal_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	_, err := m.MarkTerminal(context.Background(), uuid.Nil, uuid.New(), StatusCompleted, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestMarkTerminal_HappyPath(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WithArgs(string(StatusCompleted), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rows, err := m.MarkTerminal(context.Background(), tenant, id, StatusCompleted, "all done", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkTerminal_TruncatesFinalAtBoundary pins the contract: final
// strings longer than the 4000-byte cap get truncated via the rune-safe
// truncate() helper before being persisted. The exact ceiling is
// `4000 (truncate target) + len("…")` = 4003 bytes for a long input.
//
// Why this matters: llm_watch_tasks.final_result is `text` (no engine
// limit), but a runaway summary (e.g., an LLM that emits the entire
// observation payload back) would balloon DB row size and notification
// payloads. The truncation is the safety net — locking it in.
func TestMarkTerminal_TruncatesFinalAtBoundary(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	huge := makeBytes(4001) // one byte over the cap

	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WithArgs(
			string(StatusCompleted),
			// truncatedMatcher checks the bound. Final is wrapped in *string
			// so we use a matcher that handles both `string` and `*string`.
			truncatedStringPtrMatcher{maxBytes: 4000 + len("…")},
			nil,
			sqlmock.AnyArg(),
			id, tenant,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rows, err := m.MarkTerminal(context.Background(), tenant, id, StatusCompleted, huge, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// truncatedStringPtrMatcher is like truncatedMatcher but accepts a
// *string (final/err are stored as *string because they can be nil for
// optional terminal data). The bound must hold for the dereferenced
// value.
type truncatedStringPtrMatcher struct{ maxBytes int }

func (m truncatedStringPtrMatcher) Match(v driver.Value) bool {
	switch tv := v.(type) {
	case string:
		return len(tv) <= m.maxBytes
	case *string:
		if tv == nil {
			return false
		}
		return len(*tv) <= m.maxBytes
	default:
		return false
	}
}

// TestMarkTerminal_LostRaceReturnsZeroRows guards the Cancel+Complete race:
// when Cancel committed first the WHERE-clause status filter matches 0 rows
// and the executor must see rows==0 so it skips summarize/append/notify.
func TestMarkTerminal_LostRaceReturnsZeroRows(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	mock.ExpectExec(`SET status = \$1, final_result = \$2`).
		WithArgs(string(StatusCompleted), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 0))

	rows, err := m.MarkTerminal(context.Background(), tenant, id, StatusCompleted, "all done", "")
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows, "lost race should produce 0 rows affected")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// KNOWN BUG: concurrent quota TOCTOU
//
// assertTenantQuota is `SELECT COUNT(*) ... + INSERT` without a transaction.
// Under concurrent watch_resource calls, the COUNT can read N=19 while
// two other INSERTs are in flight, leading to N+1 rows after the cap.
//
// This documents the bug AND the contract. When the bug is fixed (move
// to `INSERT ... WHERE (SELECT COUNT ...) < cap` or a serializable tx),
// flip the assertion to require concurrency-safe behavior.
//
// Today (no fix) the test passes because each call sees its own COUNT
// query and proceeds independently — a real DB under concurrent load
// would over-allocate. The fix requires real DB integration to verify.
// ---------------------------------------------------------------------------

// TestAssertTenantQuota_BoundaryAtCap documents the boundary: COUNT=cap
// MUST reject. COUNT=cap-1 must accept. The bug is the WINDOW between
// these where N parallel callers all see cap-1.
func TestAssertTenantQuota_BoundaryAtCap(t *testing.T) {
	withConfig(t, func() { config.Config.WatchMaxPerTenant = 5 })

	// At exactly cap → reject.
	m, mock, _ := newMockManager(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	_, err := m.Create(context.Background(), validCreateInput())
	require.ErrorIs(t, err, ErrTenantQuotaExceeded)

	// One below cap → accept.
	m2, mock2, _ := newMockManager(t)
	mock2.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock2.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))
	_, err = m2.Create(context.Background(), validCreateInput())
	require.NoError(t, err)
}

// TestAssertTenantQuota_TOCTOU_DocumentsBug exists ONLY to document the
// known TOCTOU window in assertTenantQuota. The test passes today (the
// SELECT and INSERT are independent statements) — the failure mode only
// manifests under real concurrent DB load. The fix is to combine the
// check + insert into one SQL statement:
//
//	INSERT INTO llm_watch_tasks (...)
//	SELECT $1, $2, ... WHERE (SELECT count(*) FROM llm_watch_tasks
//	  WHERE tenant_id = $tenant AND status IN ('PENDING','ACTIVE')) < $cap
//	RETURNING id;
//
// Then check RowsAffected — 0 means cap was already at the limit. When
// that fix lands, this test should become:
//
//	t.Skip("superseded by atomic INSERT-with-cap check")
//
// or be replaced with an integration test proving concurrent inserts
// never exceed cap. Tagged in the test name so it shows up in grep.
func TestAssertTenantQuota_TOCTOU_DocumentsBug(t *testing.T) {
	// Two sequential Creates: both see count=4 (cap=5), both succeed.
	// In production, this is the racy window — between the SELECT and
	// INSERT another goroutine could also see count=4. With cap=5 and
	// 3 racing creates, all 3 read count=4, all 3 INSERT, final count=7.
	withConfig(t, func() { config.Config.WatchMaxPerTenant = 5 })

	m, mock, _ := newMockManager(t)
	// First create: count=4 → ok, insert.
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Second create: count is STILL 4 (in sqlmock, between calls — but
	// in production this is the racy moment). Both succeed → 6 rows
	// total despite cap=5.
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM llm_watch_tasks`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectExec("INSERT INTO llm_watch_tasks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	_, err1 := m.Create(context.Background(), validCreateInput())
	_, err2 := m.Create(context.Background(), validCreateInput())
	require.NoError(t, err1, "first create succeeds (count=4 < cap=5)")
	require.NoError(t, err2,
		"second create ALSO succeeds despite the system already being past cap — this is the TOCTOU bug. When the fix lands and Create becomes atomic, this assertion flips to require ErrTenantQuotaExceeded.")
}

// ---------------------------------------------------------------------------
// IncFailures + UpdatePollResult — defense-in-depth tenant scoping
// ---------------------------------------------------------------------------

func TestIncFailures_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	err := m.IncFailures(context.Background(), uuid.Nil, uuid.New(), time.Now(), "boom")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestIncFailures_FiltersByTenantInUpdate(t *testing.T) {
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	next := time.Now().Add(60 * time.Second)
	mock.ExpectExec(`(?s)UPDATE llm_watch_tasks.*failure_count = failure_count \+ 1.*WHERE id = \$4 AND tenant_id = \$5`).
		WithArgs(sqlmock.AnyArg(), "boom", next, id, tenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, m.IncFailures(context.Background(), tenant, id, next, "boom"))
}

func TestUpdatePollResult_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	err := m.UpdatePollResult(context.Background(), uuid.Nil, uuid.New(), "obs", time.Now(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestUpdatePollResult_TruncatesLargeObservations(t *testing.T) {
	// 9KB observation; cap is 8KB. The DB write must receive the truncated
	// form — len <= MaxObservationStorageBytes + len("[truncated]"). Use a
	// custom matcher to inspect the actual stored string.
	m, mock, _ := newMockManager(t)
	id, tenant := uuid.New(), uuid.New()
	huge := makeBytes(MaxObservationStorageBytes + 1024)

	mock.ExpectExec(`UPDATE llm_watch_tasks`).
		WithArgs(
			sqlmock.AnyArg(), // now
			truncatedMatcher{maxBytes: MaxObservationStorageBytes + 32},
			sqlmock.AnyArg(), // next_poll_at
			id, tenant,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := m.UpdatePollResult(context.Background(), tenant, id, huge, time.Now(), false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// truncatedMatcher asserts an arg is a string and is at most maxBytes long.
// Used for testing observation truncation without hard-coding the exact
// truncation marker.
type truncatedMatcher struct{ maxBytes int }

func (m truncatedMatcher) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return len(s) <= m.maxBytes
}

// ---------------------------------------------------------------------------
// ListByConversation
// ---------------------------------------------------------------------------

func TestListByConversation_RejectsNilTenant(t *testing.T) {
	m, _, _ := newMockManager(t)
	_, err := m.ListByConversation(context.Background(), uuid.Nil, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id is required")
}

func TestListByConversation_FiltersByTenant(t *testing.T) {
	m, mock, _ := newMockManager(t)
	convID, tenant := uuid.New(), uuid.New()
	mock.ExpectQuery(`FROM llm_watch_tasks WHERE conversation_id = \$1 AND tenant_id = \$2`).
		WithArgs(convID, tenant).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "account_id", "tenant_id", "user_id",
			"source_kind", "source_config", "predicate_kind", "predicate_expr",
			"predicate_negate", "notify_template", "poll_interval_sec",
			"max_duration_sec", "status", "poll_count", "failure_count",
			"next_poll_at", "last_poll_at", "last_poll_result", "final_result",
			"error", "expires_at", "created_at", "updated_at", "parent_message_id",
		}))

	out, err := m.ListByConversation(context.Background(), tenant, convID)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// errSQLNoRows returns the canonical no-rows sentinel so manager.Get's
// errors.Is(err, sql.ErrNoRows) check fires correctly.
func errSQLNoRows() error { return sql.ErrNoRows }

// makeBytes returns a string of n ASCII 'a's, fast.
func makeBytes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
