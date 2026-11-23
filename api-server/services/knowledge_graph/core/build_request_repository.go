package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/services/internal/database"
)

// Build-request queue.
//
// Why this exists: the previous claim (FilterRepository.TryClaimTenantProcessing)
// was a one-hour time window stamped on the tenant's filter rows. Any second
// message for the tenant inside that window was DROPPED. That was tolerable when
// every build was tenant-wide and identical, and is wrong as soon as builds carry
// a scope: a request to rebuild account B must not be discarded because account A
// was rebuilt ten minutes ago.
//
// Instead every request is durably enqueued, and a worker claims all of a
// tenant's pending rows at once — so a burst collapses into ONE build whose scope
// is the union of the queued scopes. Serialization per tenant comes from a
// Postgres transaction-scoped advisory lock, which works across api-server
// replicas (there is no distributed lock helper in this service; the precedent is
// ownership/store.go's pg_advisory_xact_lock).

// Build request statuses. Mirrors the CHECK constraint in migration V938.
// BuildRequestTriggerManual marks a build a person or API caller explicitly asked
// for. Manual requests bypass the tenant-wide rebuild interval: somebody pressed
// "rebuild now" and expects it to run, not to be folded into a collector's
// throttled request.
const BuildRequestTriggerManual = "manual"

const (
	BuildRequestStatusPending = "pending"
	BuildRequestStatusRunning = "running"
	BuildRequestStatusDone    = "done"
	BuildRequestStatusFailed  = "failed"
)

// BuildRequestSpec is one queued request for a knowledge-graph build.
// Empty AccountIDs means tenant-wide (every account); likewise for the source
// lists, where empty means "whatever the tenant filter says".
type BuildRequestSpec struct {
	TenantID      string
	AccountIDs    []string
	Sources       []string
	FlowSources   []string
	Trigger       string
	CorrelationID string
}

// BuildClaim is an exclusive, time-bounded right to build one tenant's graph.
// RequestIDs are the rows the claim covers; Finish resolves exactly those.
type BuildClaim struct {
	TenantID    string
	RequestIDs  []string
	AccountIDs  []string
	Sources     []string
	FlowSources []string
	Triggers    []string
}

// Partial reports whether the claim is account-scoped. A claim that includes any
// tenant-wide request has no account list and rebuilds everything.
func (c *BuildClaim) Partial() bool {
	return c != nil && len(c.AccountIDs) > 0
}

// BuildRequestRepository owns all SQL against knowledge_graph_build_requests.
type BuildRequestRepository struct {
	dbManager *database.DatabaseManager
	logger    *slog.Logger
	policy    BuildRequestPolicy
}

// BuildRequestPolicy holds the timing rules the claim logic applies.
type BuildRequestPolicy struct {
	// StaleAfter is how long a 'running' row may go without reporting completion
	// before it is treated as abandoned (its worker died) and superseded. It must
	// exceed the longest legitimate build.
	StaleAfter time.Duration

	// MinTenantWideInterval is the minimum gap between the end of one tenant-wide
	// build and the start of the next for the same tenant. Collectors publish
	// tenant-wide requests several times an hour; without this, busy tenants
	// rebuild back to back. Pending tenant-wide requests inside the window are
	// kept, not dropped, and are picked up by the first message after it.
	// Account-scoped requests are never deferred. Zero disables the throttle.
	MinTenantWideInterval time.Duration
}

// defaultBuildRequestStaleAfter matches the default kg_update_processing_timeout_minutes.
const defaultBuildRequestStaleAfter = 30 * time.Minute

// NewBuildRequestRepository constructs a repository bound to a database manager.
func NewBuildRequestRepository(dbManager *database.DatabaseManager, logger *slog.Logger) *BuildRequestRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &BuildRequestRepository{
		dbManager: dbManager,
		logger:    logger,
		policy:    BuildRequestPolicy{StaleAfter: defaultBuildRequestStaleAfter},
	}
}

// WithPolicy sets the timing rules and returns the repository for chaining. A
// non-positive StaleAfter keeps the default.
func (r *BuildRequestRepository) WithPolicy(policy BuildRequestPolicy) *BuildRequestRepository {
	if policy.StaleAfter <= 0 {
		policy.StaleAfter = defaultBuildRequestStaleAfter
	}
	r.policy = policy
	return r
}

// withTenantLock runs fn inside a transaction that holds this tenant's advisory
// lock. The lock is transaction-scoped, so it is released on commit or rollback
// and cannot leak if the process dies mid-call.
func (r *BuildRequestRepository) withTenantLock(tenantID string, fn func(tx *sqlx.Tx) error) error {
	if r.dbManager == nil {
		return fmt.Errorf("database manager not initialized")
	}
	tx, err := r.dbManager.BeginTx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			database.LogRollback(tx, r.logger)
		}
	}()

	// hashtextextended returns bigint, so the lock uses the full 64-bit keyspace.
	// hashtext would return int32 and be widened, leaving enough collisions to
	// serialize two unrelated tenants' builds against each other.
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "kg_build:"+tenantID); err != nil {
		return fmt.Errorf("failed to acquire tenant build lock: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	return nil
}

// Enqueue records a build request. It is a no-op when an identical request is
// already pending for the tenant: re-queueing the same scope would not change
// what the next build does, and the claim coalesces them anyway.
func (r *BuildRequestRepository) Enqueue(spec BuildRequestSpec) error {
	if spec.TenantID == "" {
		return fmt.Errorf("tenant id is required to enqueue a build request")
	}
	accounts := normalizeScopeList(spec.AccountIDs)
	sources := normalizeScopeList(spec.Sources)
	flowSources := normalizeScopeList(spec.FlowSources)

	accountsJSON, err := json.Marshal(accounts)
	if err != nil {
		return fmt.Errorf("failed to marshal account ids: %w", err)
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("failed to marshal sources: %w", err)
	}
	flowSourcesJSON, err := json.Marshal(flowSources)
	if err != nil {
		return fmt.Errorf("failed to marshal flow sources: %w", err)
	}
	trigger := spec.Trigger
	if trigger == "" {
		trigger = "unknown"
	}

	return r.withTenantLock(spec.TenantID, func(tx *sqlx.Tx) error {
		var duplicates int
		if err := tx.Get(&duplicates, `
			SELECT COUNT(*) FROM knowledge_graph_build_requests
			WHERE tenant_id = $1::uuid
			  AND status = 'pending'
			  AND account_ids = $2::jsonb
			  AND sources = $3::jsonb
			  AND flow_sources = $4::jsonb
			  AND (trigger = $5) = $6::boolean
		`, spec.TenantID, accountsJSON, sourcesJSON, flowSourcesJSON,
			BuildRequestTriggerManual, trigger == BuildRequestTriggerManual); err != nil {
			return fmt.Errorf("failed to check for duplicate pending request: %w", err)
		}
		// A manual request is only a duplicate of another manual request: folding
		// it into a pending collector request would inherit that request's
		// throttling and leave "rebuild now" silently waiting.
		if duplicates > 0 {
			r.logger.Debug("kg_build_request: identical request already pending, skipping",
				"tenant_id", spec.TenantID, "account_ids", accounts)
			return nil
		}

		if _, err := tx.Exec(`
			INSERT INTO knowledge_graph_build_requests
				(tenant_id, account_ids, sources, flow_sources, trigger, correlation_id, status)
			VALUES ($1::uuid, $2::jsonb, $3::jsonb, $4::jsonb, $5, $6, 'pending')
		`, spec.TenantID, accountsJSON, sourcesJSON, flowSourcesJSON, trigger, spec.CorrelationID); err != nil {
			return fmt.Errorf("failed to insert build request: %w", err)
		}
		return nil
	})
}

// TryClaim attempts to take exclusive ownership of this tenant's pending build
// requests.
//
// Returns (nil, false, nil) when there is nothing to claim right now: another
// worker is already building this tenant (it will drain the pending rows through
// Finish), or the only pending requests are tenant-wide ones deferred by
// MinTenantWideInterval (the first message after the window picks them up). A
// running row older than StaleAfter is treated as abandoned and superseded.
func (r *BuildRequestRepository) TryClaim(tenantID string) (*BuildClaim, bool, error) {
	if tenantID == "" {
		return nil, false, fmt.Errorf("tenant id is required to claim a build")
	}

	var claim *BuildClaim
	claimed := false

	err := r.withTenantLock(tenantID, func(tx *sqlx.Tx) error {
		var liveRunning int
		if err := tx.Get(&liveRunning, `
			SELECT COUNT(*) FROM knowledge_graph_build_requests
			WHERE tenant_id = $1::uuid
			  AND status = 'running'
			  AND started_at > NOW() - $2::interval
		`, tenantID, intervalString(r.policy.StaleAfter)); err != nil {
			return fmt.Errorf("failed to check running builds: %w", err)
		}
		if liveRunning > 0 {
			return nil
		}

		// Supersede abandoned claims so their rows stop blocking the tenant.
		if _, err := tx.Exec(`
			UPDATE knowledge_graph_build_requests
			SET status = 'failed', finished_at = NOW(), error = 'superseded: build did not report completion'
			WHERE tenant_id = $1::uuid AND status = 'running'
		`, tenantID); err != nil {
			return fmt.Errorf("failed to supersede stale running builds: %w", err)
		}

		next, err := r.claimPending(tx, tenantID)
		if err != nil {
			return err
		}
		if next != nil {
			claim = next
			claimed = true
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return claim, claimed, nil
}

// claimPending flips this tenant's claimable pending rows to running and folds
// them into one claim. It must run inside withTenantLock.
//
// Tenant-wide rows are claimable only when no tenant-wide build for the tenant
// finished within MinTenantWideInterval, or when a manual tenant-wide request is
// pending. When tenant-wide rows are claimable, every pending row is claimed —
// a tenant-wide build covers the account-scoped ones too. Otherwise only
// account-scoped rows are claimed and the tenant-wide ones stay pending.
//
// Only completed builds start the window: a 'done' build, or a 'failed' one that
// finished within StaleAfter (a real failure — retrying it on every message
// would hammer a broken tenant). A superseded or orphaned row ran longer than
// StaleAfter by definition and does not count, so a crash is not followed by a
// second wait.
func (r *BuildRequestRepository) claimPending(tx *sqlx.Tx, tenantID string) (*BuildClaim, error) {
	tenantWideAllowed := true
	if r.policy.MinTenantWideInterval > 0 {
		var manualPending bool
		if err := tx.Get(&manualPending, `
			SELECT EXISTS (
				SELECT 1 FROM knowledge_graph_build_requests
				WHERE tenant_id = $1::uuid
				  AND status = 'pending'
				  AND account_ids = '[]'::jsonb
				  AND trigger = $2
			)
		`, tenantID, BuildRequestTriggerManual); err != nil {
			return nil, fmt.Errorf("failed to check for manual build requests: %w", err)
		}

		if !manualPending {
			var recentTenantWide bool
			if err := tx.Get(&recentTenantWide, `
				SELECT EXISTS (
					SELECT 1 FROM knowledge_graph_build_requests
					WHERE finished_at > NOW() - $2::interval
					  AND tenant_id = $1::uuid
					  AND account_ids = '[]'::jsonb
					  AND (status = 'done'
					       OR (status = 'failed' AND finished_at - started_at < $3::interval))
				)
			`, tenantID, intervalString(r.policy.MinTenantWideInterval), intervalString(r.policy.StaleAfter)); err != nil {
				return nil, fmt.Errorf("failed to check recent tenant-wide builds: %w", err)
			}
			tenantWideAllowed = !recentTenantWide
		}
	}

	rows, err := tx.Query(`
		UPDATE knowledge_graph_build_requests
		SET status = 'running', started_at = NOW()
		WHERE tenant_id = $1::uuid
		  AND status = 'pending'
		  AND ($2::boolean OR account_ids <> '[]'::jsonb)
		RETURNING id::text, account_ids, sources, flow_sources, trigger
	`, tenantID, tenantWideAllowed)
	if err != nil {
		return nil, fmt.Errorf("failed to claim pending build requests: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			r.logger.Warn("kg_build_request: failed to close rows", "error", closeErr)
		}
	}()

	claim, err := scanClaim(tenantID, rows)
	if err != nil {
		return nil, err
	}

	if !tenantWideAllowed {
		r.logger.Info("kg_build_request: tenant-wide rebuild deferred by the minimum interval",
			"tenant_id", tenantID,
			"min_interval", r.policy.MinTenantWideInterval.String(),
			"claimed_account_scoped", claim != nil)
	}
	return claim, nil
}

// Finish resolves the rows a claim covered and immediately claims anything that
// was enqueued while the build was running, returning it as the next claim.
// Both happen under the same tenant lock, so there is no window in which a
// pending row is neither drained here nor claimable by a fresh message.
//
// Fence: a claim is only resolved while its rows are still 'running'. If they
// were superseded because this build outlived StaleAfter, another worker may
// already be building the tenant. Overwriting 'failed' with 'done' would then
// hide that, and draining would start a second concurrent build of the same
// tenant, so both are skipped.
//
// A nil return means there is nothing left to build for this tenant (or the
// claim was no longer held).
func (r *BuildRequestRepository) Finish(claim *BuildClaim, buildErr error) (*BuildClaim, error) {
	if claim == nil || claim.TenantID == "" {
		return nil, fmt.Errorf("a claim is required to finish a build")
	}

	status := BuildRequestStatusDone
	var errText *string
	if buildErr != nil {
		status = BuildRequestStatusFailed
		msg := buildErr.Error()
		errText = &msg
	}

	var next *BuildClaim
	err := r.withTenantLock(claim.TenantID, func(tx *sqlx.Tx) error {
		if len(claim.RequestIDs) > 0 {
			query, args, err := sqlx.In(`
				UPDATE knowledge_graph_build_requests
				SET status = ?, finished_at = NOW(), error = ?
				WHERE id IN (?) AND status = 'running'
			`, status, errText, claim.RequestIDs)
			if err != nil {
				return fmt.Errorf("failed to build finish statement: %w", err)
			}
			result, err := tx.Exec(tx.Rebind(query), args...)
			if err != nil {
				return fmt.Errorf("failed to finish build requests: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("failed to read finish result: %w", err)
			}
			if affected < int64(len(claim.RequestIDs)) {
				r.logger.Warn("kg_build_request: claim no longer held, not resolving or draining",
					"tenant_id", claim.TenantID,
					"claimed_rows", len(claim.RequestIDs),
					"still_running", affected)
				return nil
			}
		}

		drained, err := r.claimPending(tx, claim.TenantID)
		if err != nil {
			return fmt.Errorf("failed to drain pending build requests: %w", err)
		}
		next = drained
		return nil
	})
	if err != nil {
		return nil, err
	}
	return next, nil
}

// ExcludeBuilds runs fn while holding this tenant's build lock — the lock every
// claim takes — so no build of the tenant can be claimed until fn returns. fn
// gets runningCovers, answered inside the same lock, to see builds that were
// already running when it started.
//
// Per-resource updates run inside it, which makes a patch and a build of the
// same account mutually exclusive: either a build is running and the patch
// stands aside, or none can start until the patch has written. Without it a
// build claimed between a patch's check and its write could read the account's
// inventory before the patch and save that older state over it, since node and
// edge upserts do not compare versions.
//
// The lock is held for fn's whole duration, so fn must be short and must not
// claim, enqueue or finish a build itself (those take the same lock).
func (r *BuildRequestRepository) ExcludeBuilds(tenantID string, fn func(runningCovers func(accountID string) (bool, error)) error) error {
	if tenantID == "" {
		return fmt.Errorf("tenant id is required to exclude builds")
	}
	return r.withTenantLock(tenantID, func(tx *sqlx.Tx) error {
		return fn(func(accountID string) (bool, error) {
			return r.runningClaimCovers(tx, tenantID, accountID)
		})
	})
}

// BuildRunningFor reports, without taking the tenant's build lock, whether a
// live build of the tenant covers the account. It answers a question for the
// UI ("a build may overwrite this until the next one"), not a guard: a build
// can start right after it returns.
func (r *BuildRequestRepository) BuildRunningFor(tenantID, accountID string) (bool, error) {
	return r.runningClaimCovers(r.dbManager.Db, tenantID, accountID)
}

// runningClaimCovers reports whether a live build is currently running for the
// tenant with a scope that includes the account: a tenant-wide build, or an
// account-scoped build whose account list contains it. A running row older than
// StaleAfter is abandoned and does not count.
func (r *BuildRequestRepository) runningClaimCovers(q sqlx.Queryer, tenantID, accountID string) (bool, error) {
	var covered bool
	if err := sqlx.Get(q, &covered, `
		SELECT EXISTS (
			SELECT 1 FROM knowledge_graph_build_requests
			WHERE tenant_id = $1::uuid
			  AND status = 'running'
			  AND started_at > NOW() - $2::interval
			  AND (account_ids = '[]'::jsonb OR account_ids ? $3)
		)
	`, tenantID, intervalString(r.policy.StaleAfter), accountID); err != nil {
		return false, fmt.Errorf("failed to check running builds: %w", err)
	}
	return covered, nil
}

// Release hands a claim back without marking its rows terminal — used when the
// caller decides not to run the build after all (e.g. shutdown). The rows go back
// to pending so the next message picks them up.
func (r *BuildRequestRepository) Release(claim *BuildClaim) error {
	if claim == nil || len(claim.RequestIDs) == 0 {
		return nil
	}
	return r.withTenantLock(claim.TenantID, func(tx *sqlx.Tx) error {
		query, args, err := sqlx.In(`
			UPDATE knowledge_graph_build_requests
			SET status = 'pending', started_at = NULL
			WHERE id IN (?) AND status = 'running'
		`, claim.RequestIDs)
		if err != nil {
			return fmt.Errorf("failed to build release statement: %w", err)
		}
		if _, err := tx.Exec(tx.Rebind(query), args...); err != nil {
			return fmt.Errorf("failed to release build requests: %w", err)
		}
		return nil
	})
}

// scanClaim folds a set of claimed rows into one BuildClaim. Returns nil when
// there were no rows.
func scanClaim(tenantID string, rows *sql.Rows) (*BuildClaim, error) {
	claim := &BuildClaim{TenantID: tenantID}
	accountSet := newScopeAccumulator()
	sourceSet := newScopeAccumulator()
	flowSet := newScopeAccumulator()
	triggers := make(map[string]struct{})
	found := false

	for rows.Next() {
		var (
			id              string
			accountsJSON    []byte
			sourcesJSON     []byte
			flowSourcesJSON []byte
			trigger         string
		)
		if err := rows.Scan(&id, &accountsJSON, &sourcesJSON, &flowSourcesJSON, &trigger); err != nil {
			return nil, fmt.Errorf("failed to scan build request: %w", err)
		}
		found = true
		claim.RequestIDs = append(claim.RequestIDs, id)
		triggers[trigger] = struct{}{}

		for _, pair := range []struct {
			raw []byte
			acc *scopeAccumulator
		}{
			{accountsJSON, accountSet},
			{sourcesJSON, sourceSet},
			{flowSourcesJSON, flowSet},
		} {
			var values []string
			if len(pair.raw) > 0 {
				if err := json.Unmarshal(pair.raw, &values); err != nil {
					return nil, fmt.Errorf("failed to unmarshal build request scope: %w", err)
				}
			}
			pair.acc.add(values)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating build requests: %w", err)
	}
	if !found {
		return nil, nil
	}

	claim.AccountIDs = accountSet.result()
	claim.Sources = sourceSet.result()
	claim.FlowSources = flowSet.result()
	for t := range triggers {
		claim.Triggers = append(claim.Triggers, t)
	}
	sort.Strings(claim.Triggers)
	return claim, nil
}

// scopeAccumulator unions scope lists with "empty means everything" semantics: a
// single unbounded request widens the whole claim to unbounded, because a build
// that covers everything also covers every narrower request queued alongside it.
type scopeAccumulator struct {
	unbounded bool
	values    map[string]struct{}
}

func newScopeAccumulator() *scopeAccumulator {
	return &scopeAccumulator{values: make(map[string]struct{})}
}

func (a *scopeAccumulator) add(values []string) {
	if len(values) == 0 {
		a.unbounded = true
		return
	}
	for _, v := range values {
		if v != "" {
			a.values[v] = struct{}{}
		}
	}
}

func (a *scopeAccumulator) result() []string {
	if a.unbounded || len(a.values) == 0 {
		return nil
	}
	out := make([]string, 0, len(a.values))
	for v := range a.values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// normalizeScopeList de-duplicates and orders a scope list so that two requests
// for the same scope compare equal as JSONB (which is what the pending-duplicate
// check relies on).
func normalizeScopeList(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// intervalString renders a duration for Postgres' interval cast. Sub-second
// precision is irrelevant here (the shortest sensible staleness window is
// minutes) and seconds keep the literal readable in query logs.
func intervalString(d time.Duration) string {
	seconds := int64(d / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}
