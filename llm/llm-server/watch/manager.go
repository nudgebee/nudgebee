package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrTenantQuotaExceeded is returned when a tenant has reached its
// concurrent active-watch cap. The watch_resource tool surfaces this back
// to the agent so it can apologize gracefully instead of retrying blindly.
var ErrTenantQuotaExceeded = errors.New("watch: tenant has reached active watch cap")

// ErrWatchNotFound is returned by Get/Cancel when no row matches.
var ErrWatchNotFound = errors.New("watch: not found")

// Manager is the persistence + validation surface for watch rows. It owns
// all SQL against llm_watch_tasks and is the only place watch creation
// rules (quotas, interval clamps, predicate compilation) live.
type Manager struct {
	now func() time.Time // optional override for tests
	db  *sqlx.DB         // optional override for tests; if set, dbHandle returns this directly
}

// NewManager builds a Manager backed by the metastore.
func NewManager() *Manager {
	return &Manager{now: time.Now}
}

// NewManagerForTesting wires a Manager to an externally-supplied *sqlx.DB
// (typically a sqlmock connection wrapped via sqlx.NewDb). Exported so
// callers outside this package — notably the api package's handler tests —
// can build a mockable manager without poking the unexported `db` field.
// Not for production use; the production constructor remains NewManager.
func NewManagerForTesting(db *sqlx.DB) *Manager {
	return &Manager{now: time.Now, db: db}
}

// dbHandle returns the *sqlx.DB to use for queries. Centralized so the
// test path can swap the connection by setting Manager.db, while
// production reaches through common.GetDatabaseManager.
func (m *Manager) dbHandle() (*sqlx.DB, error) {
	if m.db != nil {
		return m.db, nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("watch: metastore unavailable: %w", err)
	}
	return dbms.Db, nil
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Create validates the input, enforces tenant cap + interval clamps, and
// persists the row. The watch is left in PENDING state with next_poll_at
// set so the dispatcher picks it up on its next tick.
func (m *Manager) Create(ctx context.Context, in CreateInput) (*Watch, error) {
	if err := validateCreateInput(in); err != nil {
		return nil, err
	}

	src, ok := GetSource(in.SourceKind)
	if !ok {
		return nil, fmt.Errorf("watch: unknown source_kind %q", in.SourceKind)
	}
	if err := src.ValidateConfig(in.SourceConfig); err != nil {
		return nil, fmt.Errorf("watch: invalid source config: %w", err)
	}

	if _, err := CompilePredicate(in.PredicateKind, in.PredicateExpr); err != nil {
		return nil, err
	}

	clamped := clampLimits(in)

	db, err := m.dbHandle()
	if err != nil {
		return nil, err
	}

	// Dedup: watch_resource is injected into every agent, so an orchestrator and
	// its sub-agent can each register a watch for the same observation. Reuse it.
	if existing, derr := m.findDuplicate(ctx, db, in); derr != nil {
		return nil, derr
	} else if existing != nil {
		slog.Info("watch: deduped duplicate registration; reusing existing watch",
			"conversation_id", in.ConversationID.String(), "watch_id", existing.ID.String())
		return existing, nil
	}

	now := m.clock()
	// Fast pre-check: surfaces the typed quota error early (before we build
	// the row) for the common, uncontended case. The INSERT below ALSO
	// re-checks the count in the same statement, which tightens the window
	// considerably — but note this is BEST-EFFORT, not a hard guarantee.
	// Under READ COMMITTED the `COUNT(*) < cap` subquery reads a snapshot that
	// does not see other in-flight *uncommitted* inserts, and there is no
	// FOR UPDATE / unique constraint to serialize on, so two concurrent
	// Creates at count = cap-1 can both pass and land at cap+1. The overshoot
	// is bounded by concurrency (small) and the cap is a soft abuse-guard, not
	// a correctness invariant — a strict bound would need a per-tenant
	// advisory lock or SERIALIZABLE-with-retry, deliberately not taken here.
	maxPerTenant := config.Config.WatchMaxPerTenant
	if err := assertTenantQuota(ctx, db, in.TenantID, maxPerTenant); err != nil {
		return nil, err
	}

	w := &Watch{
		ID:              uuid.New(),
		ConversationID:  in.ConversationID,
		AccountID:       in.AccountID,
		TenantID:        in.TenantID,
		UserID:          in.UserID,
		ParentMessageID: in.ParentMessageID,
		SourceKind:      in.SourceKind,
		SourceConfig:    string(in.SourceConfig),
		PredicateKind:   in.PredicateKind,
		PredicateExpr:   in.PredicateExpr,
		PredicateNegate: in.PredicateNegate,
		PollIntervalSec: clamped.PollIntervalSec,
		MaxDurationSec:  clamped.MaxDurationSec,
		Status:          StatusPending,
		NextPollAt:      now.Add(time.Duration(clamped.PollIntervalSec) * time.Second),
		ExpiresAt:       now.Add(time.Duration(clamped.MaxDurationSec) * time.Second),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if strings.TrimSpace(in.NotifyTemplate) != "" {
		t := in.NotifyTemplate
		w.NotifyTemplate = &t
	}

	// INSERT … SELECT … WHERE (count) < cap folds the quota check into the
	// insert statement so the COUNT and the write share one snapshot — this
	// removes the wide check-then-insert gap but is NOT a hard serialization
	// (see the BEST-EFFORT note above: concurrent uncommitted inserts under
	// READ COMMITTED can still overshoot by the concurrency count).
	// RowsAffected == 0 means the cap was already met when this ran.
	const insertCols = `
INSERT INTO llm_watch_tasks (
  id, conversation_id, account_id, tenant_id, user_id,
  source_kind, source_config,
  predicate_kind, predicate_expr, predicate_negate,
  notify_template, poll_interval_sec, max_duration_sec,
  status, next_poll_at, expires_at, created_at, updated_at,
  parent_message_id
)
SELECT
  $1, $2, $3, $4, $5,
  $6, $7::jsonb,
  $8, $9, $10,
  $11, $12, $13,
  $14, $15, $16, $17, $18,
  $19`

	args := []any{
		w.ID, w.ConversationID, w.AccountID, w.TenantID, w.UserID,
		string(w.SourceKind), w.SourceConfig,
		string(w.PredicateKind), w.PredicateExpr, w.PredicateNegate,
		w.NotifyTemplate, w.PollIntervalSec, w.MaxDurationSec,
		string(w.Status), w.NextPollAt, w.ExpiresAt, w.CreatedAt, w.UpdatedAt,
		w.ParentMessageID,
	}
	query := insertCols
	if maxPerTenant > 0 {
		// $4 is tenant_id (reused); $20 is the cap.
		query += `
WHERE (
  SELECT COUNT(*) FROM llm_watch_tasks
  WHERE tenant_id = $4 AND status IN ('PENDING','ACTIVE')
) < $20`
		args = append(args, maxPerTenant)
	}

	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("watch: insert failed: %w", err)
	}
	if maxPerTenant > 0 {
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrTenantQuotaExceeded
		}
	}
	return w, nil
}

// findDuplicate returns a non-terminal watch in this conversation observing the
// same target (same source tool_input + predicate) as `in`, ignoring which tool
// runs the poll (tool_name / tool_config_name); nil if none. First-registered
// wins. sql sources (no tool_input) are not deduped.
func (m *Manager) findDuplicate(ctx context.Context, db *sqlx.DB, in CreateInput) (*Watch, error) {
	const q = `
SELECT id FROM llm_watch_tasks
WHERE conversation_id = $1 AND tenant_id = $2
  AND status IN ('PENDING','ACTIVE')
  AND ($3::jsonb) -> 'tool_input' IS NOT NULL
  AND source_config -> 'tool_input' = ($3::jsonb) -> 'tool_input'
  AND predicate_kind = $4 AND predicate_expr = $5 AND predicate_negate = $6
ORDER BY created_at ASC
LIMIT 1`
	var id uuid.UUID
	err := db.GetContext(ctx, &id, q,
		in.ConversationID, in.TenantID, string(in.SourceConfig), string(in.PredicateKind), in.PredicateExpr, in.PredicateNegate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watch: dedup check failed: %w", err)
	}
	return m.Get(ctx, in.TenantID, id)
}

func validateCreateInput(in CreateInput) error {
	if in.ConversationID == uuid.Nil {
		return fmt.Errorf("watch: conversation_id is required")
	}
	if in.AccountID == uuid.Nil {
		return fmt.Errorf("watch: account_id is required")
	}
	if in.TenantID == uuid.Nil {
		return fmt.Errorf("watch: tenant_id is required")
	}
	if in.SourceKind == "" {
		return fmt.Errorf("watch: source_kind is required")
	}
	if len(in.SourceConfig) == 0 {
		return fmt.Errorf("watch: source_config is required")
	}
	if !json.Valid(in.SourceConfig) {
		return fmt.Errorf("watch: source_config is not valid JSON")
	}
	if in.PredicateKind == "" {
		return fmt.Errorf("watch: predicate_kind is required")
	}
	if strings.TrimSpace(in.PredicateExpr) == "" {
		return fmt.Errorf("watch: predicate_expr is required")
	}
	if in.PollIntervalSec <= 0 {
		return fmt.Errorf("watch: poll_interval_sec must be > 0")
	}
	if in.MaxDurationSec <= 0 {
		return fmt.Errorf("watch: max_duration_sec must be > 0")
	}
	return nil
}

// clampLimits enforces the configured min interval and max duration so an
// agent can't request a 1-second poll or a 30-day watch.
func clampLimits(in CreateInput) CreateInput {
	out := in
	if cfg := config.Config.WatchMinIntervalSec; cfg > 0 && out.PollIntervalSec < cfg {
		out.PollIntervalSec = cfg
	}
	if cfg := config.Config.WatchMaxDurationSec; cfg > 0 && out.MaxDurationSec > cfg {
		out.MaxDurationSec = cfg
	}
	return out
}

func assertTenantQuota(ctx context.Context, db *sqlx.DB, tenantID uuid.UUID, maxActive int) error {
	if maxActive <= 0 {
		return nil
	}
	var n int
	const q = `
SELECT COUNT(*) FROM llm_watch_tasks
WHERE tenant_id = $1 AND status IN ('PENDING','ACTIVE')`
	if err := db.GetContext(ctx, &n, q, tenantID); err != nil {
		return fmt.Errorf("watch: tenant quota query failed: %w", err)
	}
	if n >= maxActive {
		return ErrTenantQuotaExceeded
	}
	return nil
}

// fetchDueClaimLeaseSec is how far into the future a claimed row's
// next_poll_at is bumped on FetchDue. Acts as a short row-level lease so a
// duplicate dispatcher (leader-election failover, or
// WatchBypassLeaderElection=true) cannot re-pick the same row before the
// first dispatcher has finished its poll. Picked to comfortably exceed
// pollTimeout(15s) + summarizerTimeout(30s) without being so long that a
// crashed dispatcher leaves the row stranded.
const fetchDueClaimLeaseSec = 300

// FetchDue claims up to `limit` due watches atomically and returns them.
// Uses `SELECT … FOR UPDATE SKIP LOCKED` inside a single UPDATE so the
// claim and the lease advance commit together — no other dispatcher can
// pick the same row even if leader election briefly elects two leaders or
// the local-mode WatchBypassLeaderElection knob runs the tick loop on
// every replica. Without this, contested ticks doubled LLM-judge costs
// (and arbitrary side-effect tools) by polling the same row twice.
//
// The lease bumps `next_poll_at` to now + fetchDueClaimLeaseSec so the row
// is invisible to subsequent FetchDue calls until the executor either
// terminates it or reschedules it via UpdatePollResult / IncFailures. The
// executor's own UPDATE wins because its `next_poll_at` (interval-based)
// is sooner than the lease — so a slow poll just delays the next poll by
// at most the lease window, never longer.
func (m *Manager) FetchDue(ctx context.Context, limit int) ([]Watch, error) {
	if limit <= 0 {
		limit = 100
	}
	db, err := m.dbHandle()
	if err != nil {
		return nil, err
	}
	const q = `
UPDATE llm_watch_tasks
SET next_poll_at = now() + ($2 || ' seconds')::interval
WHERE id IN (
    SELECT id FROM llm_watch_tasks
    WHERE status IN ('PENDING','ACTIVE') AND next_poll_at <= now()
    ORDER BY next_poll_at ASC
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, conversation_id, account_id, tenant_id, user_id,
       source_kind, source_config::text AS source_config,
       predicate_kind, predicate_expr, predicate_negate,
       notify_template, poll_interval_sec, max_duration_sec,
       status, poll_count, failure_count,
       next_poll_at, last_poll_at, last_poll_result, final_result, error,
       expires_at, created_at, updated_at, parent_message_id`
	out := []Watch{}
	if err := db.SelectContext(ctx, &out, q, limit, fetchDueClaimLeaseSec); err != nil {
		return nil, fmt.Errorf("watch: fetch due failed: %w", err)
	}
	return out, nil
}

// Get loads a single watch by ID, scoped to the caller's tenant. We always
// require tenantID — without it a user who learns another tenant's watch
// UUID could read its observations and termination state.
func (m *Manager) Get(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) (*Watch, error) {
	if tenantID == uuid.Nil {
		return nil, fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return nil, err
	}
	const q = `
SELECT id, conversation_id, account_id, tenant_id, user_id,
       source_kind, source_config::text AS source_config,
       predicate_kind, predicate_expr, predicate_negate,
       notify_template, poll_interval_sec, max_duration_sec,
       status, poll_count, failure_count,
       next_poll_at, last_poll_at, last_poll_result, final_result, error,
       expires_at, created_at, updated_at, parent_message_id
FROM llm_watch_tasks
WHERE id = $1 AND tenant_id = $2`
	w := &Watch{}
	if err := db.GetContext(ctx, w, q, id, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWatchNotFound
		}
		return nil, fmt.Errorf("watch: get failed: %w", err)
	}
	return w, nil
}

// UpdatePollResult records a non-terminal poll outcome and reschedules.
// last is truncated for storage; predicates see the full payload. The
// tenantID is required as defense-in-depth — these write paths are only
// reached today via FetchDue (which carries the right tenant) but adding
// the filter here prevents future misuse if a caller invokes the manager
// with a forged ID directly.
func (m *Manager) UpdatePollResult(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, last string, nextPollAt time.Time, resetFailures bool) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return err
	}
	stored := last
	if len(stored) > MaxObservationStorageBytes {
		// truncate is UTF-8-safe — Postgres `text` rejects invalid UTF-8.
		stored = truncate(stored, MaxObservationStorageBytes) + "[truncated]"
	}
	now := m.clock()
	failureClause := ""
	if resetFailures {
		failureClause = ", failure_count = 0"
	}
	q := fmt.Sprintf(`
UPDATE llm_watch_tasks
SET status = 'ACTIVE',
    poll_count = poll_count + 1,
    last_poll_at = $1,
    last_poll_result = $2,
    next_poll_at = $3,
    updated_at = $1%s
WHERE id = $4 AND tenant_id = $5 AND status IN ('PENDING','ACTIVE')`, failureClause)
	if _, err := db.ExecContext(ctx, q, now, stored, nextPollAt, id, tenantID); err != nil {
		return fmt.Errorf("watch: update poll failed: %w", err)
	}
	return nil
}

// IncFailures bumps failure_count and reschedules with the supplied next
// poll time. Used for transient errors so the watch can retry with backoff
// before being moved to FAILED. Tenant-scoped as defense-in-depth.
func (m *Manager) IncFailures(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, nextPollAt time.Time, errMsg string) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return err
	}
	now := m.clock()
	const q = `
UPDATE llm_watch_tasks
SET failure_count = failure_count + 1,
    poll_count = poll_count + 1,
    last_poll_at = $1,
    error = $2,
    next_poll_at = $3,
    updated_at = $1
WHERE id = $4 AND tenant_id = $5 AND status IN ('PENDING','ACTIVE')`
	if _, err := db.ExecContext(ctx, q, now, truncate(errMsg, 2000), nextPollAt, id, tenantID); err != nil {
		return fmt.Errorf("watch: inc failures failed: %w", err)
	}
	return nil
}

// MarkTerminal moves the watch to a terminal state. final and errMsg are
// optional — only one is meaningful per status. Tenant-scoped as
// defense-in-depth. Returns the number of rows affected so the caller can
// detect the lost-race case: if Cancel commits between the executor's
// observe and this UPDATE, the WHERE-clause status filter matches 0 rows
// and the caller must skip the post-terminal side effects (summarizer,
// responder append, notifier) — otherwise the user sees a "✅ COMPLETED"
// message for a watch they just cancelled.
func (m *Manager) MarkTerminal(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, status Status, final string, errMsg string) (int64, error) {
	if tenantID == uuid.Nil {
		return 0, fmt.Errorf("watch: tenant_id is required")
	}
	if !status.IsTerminal() {
		return 0, fmt.Errorf("watch: status %q is not terminal", status)
	}
	db, err := m.dbHandle()
	if err != nil {
		return 0, err
	}
	now := m.clock()
	var finalPtr, errPtr *string
	if strings.TrimSpace(final) != "" {
		s := truncate(final, 4000)
		finalPtr = &s
	}
	if strings.TrimSpace(errMsg) != "" {
		s := truncate(errMsg, 2000)
		errPtr = &s
	}
	const q = `
UPDATE llm_watch_tasks
SET status = $1,
    final_result = $2,
    error = $3,
    last_poll_at = $4,
    updated_at = $4
WHERE id = $5 AND tenant_id = $6 AND status IN ('PENDING','ACTIVE')`
	res, err := db.ExecContext(ctx, q, string(status), finalPtr, errPtr, now, id, tenantID)
	if err != nil {
		return 0, fmt.Errorf("watch: mark terminal failed: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}

// RescheduleNextPollAt bumps next_poll_at on a non-terminal row without
// changing status, poll_count, or failure_count. Used when the dispatcher
// cannot submit a watch to the worker pool (pool full): without this, the
// row's next_poll_at stays in the past and the same row gets refetched +
// rejected on every tick, starving every other watch in the tenant batch.
// Tenant-scoped as defense-in-depth.
func (m *Manager) RescheduleNextPollAt(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, nextPollAt time.Time) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return err
	}
	const q = `
UPDATE llm_watch_tasks
SET next_poll_at = $1, updated_at = $2
WHERE id = $3 AND tenant_id = $4 AND status IN ('PENDING','ACTIVE')`
	if _, err := db.ExecContext(ctx, q, nextPollAt, m.clock(), id, tenantID); err != nil {
		return fmt.Errorf("watch: reschedule failed: %w", err)
	}
	return nil
}

// Cancel transitions the watch to CANCELLED if it isn't already terminal.
// Scoped to the caller's tenant so a tenant cannot cancel another tenant's
// watch by guessing or replaying its UUID. Returns ErrWatchNotFound if no
// row matches the (id, tenant) pair.
func (m *Manager) Cancel(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return err
	}
	now := m.clock()
	const q = `
UPDATE llm_watch_tasks
SET status = 'CANCELLED', updated_at = $1
WHERE id = $2 AND tenant_id = $3 AND status IN ('PENDING','ACTIVE')`
	res, err := db.ExecContext(ctx, q, now, id, tenantID)
	if err != nil {
		return fmt.Errorf("watch: cancel failed: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Either the row doesn't exist for this tenant OR it's already
		// terminal. Look up under the same tenant scope to distinguish —
		// callers want different signals for the two cases. Surface
		// unexpected lookup errors instead of swallowing them.
		_, gerr := m.Get(ctx, tenantID, id)
		switch {
		case errors.Is(gerr, ErrWatchNotFound):
			return ErrWatchNotFound
		case gerr != nil:
			return fmt.Errorf("watch: cancel post-check failed: %w", gerr)
		}
		// Row exists for this tenant but is already terminal — treat as
		// a no-op success.
	}
	return nil
}

// ListByConversation returns all watches owned by a conversation, oldest
// first. Tenant-scoped — UUID v4 collisions across tenants are practically
// impossible, but we still filter by tenant_id as defense-in-depth so a
// future change to ID generation can't silently leak rows.
func (m *Manager) ListByConversation(ctx context.Context, tenantID uuid.UUID, conversationID uuid.UUID) ([]Watch, error) {
	if tenantID == uuid.Nil {
		return nil, fmt.Errorf("watch: tenant_id is required")
	}
	db, err := m.dbHandle()
	if err != nil {
		return nil, err
	}
	const q = `
SELECT id, conversation_id, account_id, tenant_id, user_id,
       source_kind, source_config::text AS source_config,
       predicate_kind, predicate_expr, predicate_negate,
       notify_template, poll_interval_sec, max_duration_sec,
       status, poll_count, failure_count,
       next_poll_at, last_poll_at, last_poll_result, final_result, error,
       expires_at, created_at, updated_at, parent_message_id
FROM llm_watch_tasks
WHERE conversation_id = $1 AND tenant_id = $2
ORDER BY created_at ASC`
	out := []Watch{}
	if err := db.SelectContext(ctx, &out, q, conversationID, tenantID); err != nil {
		return nil, fmt.Errorf("watch: list by conversation failed: %w", err)
	}
	return out, nil
}
