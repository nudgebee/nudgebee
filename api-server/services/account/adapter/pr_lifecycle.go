package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
)

type prResolutionRow struct {
	ID               string          `db:"id"`
	TypeReferenceID  string          `db:"type_reference_id"`
	Data             json.RawMessage `db:"data"`
	PRIterationCount int             `db:"pr_iteration_count"`
	PRLifecycleState string          `db:"pr_lifecycle_state"`
	TenantID         string          `db:"tenant"`
	TableName        string
}

type prMetadata struct {
	PRURL       string `json:"pr_url"`
	PRNumber    any    `json:"pr_number"`
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch"`
	Provider    string `json:"provider"`
	Org         string `json:"org"`
	Repo        string `json:"repo"`
	PRBranch    string `json:"pr_branch"`
	ProjectPath string `json:"project_path"`
	// TenantID is stored by agent_code_2 for conversation-originated PRs where
	// the events LEFT JOIN returns no tenant (no event row exists).
	TenantID string `json:"tenant_id"`
	// AccountID is stored by agent_code_2 and echoed back on the followup
	// request so llm-server can resolve account-scoped state (conversation,
	// workspace, budget) instead of rejecting the request for missing account.
	AccountID string `json:"account_id"`
}

// CheckAndFollowupOpenPRs polls resolution tables for open agent PRs and triggers followup
// via llm-server. Called from the RPC cron job every 15 minutes.
func CheckAndFollowupOpenPRs(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	rows, err := queryOpenPRResolutions(dbms)
	if err != nil {
		return fmt.Errorf("failed to query open PR resolutions: %w", err)
	}

	if len(rows) == 0 {
		ctx.GetLogger().Info("pr_lifecycle: no open PRs need attention")
		return nil
	}

	ctx.GetLogger().Info("pr_lifecycle: found open PRs to check", "count", len(rows))

	for _, row := range rows {
		if err := processResolution(ctx, dbms, row, maxRedispatchChain); err != nil {
			ctx.GetLogger().Error("pr_lifecycle: failed to process resolution",
				"id", row.ID, "table", row.TableName, "error", err)
			continue
		}
	}

	return nil
}

// followupIterationCap and followupRecoveryInterval govern how aggressively
// the cron retries. The cron is now a safety net behind GitHub App webhooks
// (see /api/webhooks/github in public_webhooks.go), so the base cooldown is
// long: real PR events fire ProcessOpenPRResolution directly, and the cron
// only catches cases where a webhook delivery was missed. Once capped, the
// recovery clause re-checks every 6 hours — a backstop for late reviewer
// feedback (Gemini can take >2h after PR creation), or where downstream bugs
// inflate the count beyond what real failures justify. Capped rows that
// produce a no-op or success on a recovery run will not advance further.
const (
	followupIterationCap      = 5
	followupCooldown          = "60 minutes"
	followupRecoveryInterval  = "6 hours"
	followupRecoveryHardLimit = 20
	// maxRedispatchChain bounds how many times a single completion can chain a
	// re-dispatch for signals that arrived mid-run (pr_followup_pending). Each
	// link is a fresh full run, so this caps a worst-case "signal during every
	// run" loop; beyond it the cron remains the backstop. Coalescing (a single
	// boolean, not a queue) already collapses N concurrent signals into one
	// re-run, so this only bites pathological continuous-signal sources.
	maxRedispatchChain = 5
)

// prDBOpTimeout bounds the small claim / finalize / terminal UPDATEs. They run
// on an independent context.Background() (NOT the caller's request context) on
// purpose: the finalize is a must-complete write — if it inherited the dispatch
// goroutine's cancellation (e.g. the 35-min LLM bound expiring, or a webhook
// handler returning), a canceled run would fail to record its outcome and leave
// the row stuck in 'addressing' forever. The timeout still prevents an
// unresponsive DB from hanging/leaking the goroutine.
const prDBOpTimeout = 10 * time.Second

func queryOpenPRResolutions(dbms *database.DatabaseManager) ([]prResolutionRow, error) {
	var results []prResolutionRow

	// The OR clause re-admits rows past the cap once every recovery interval
	// so post-cap signals (review submitted late, new CI failure on a fresh
	// commit) still get an attempt. The hard limit prevents indefinite loops
	// on PRs that nobody is going to merge.
	eventQuery := fmt.Sprintf(`
		SELECT er.id, er.type_reference_id, er.data,
		       er.pr_iteration_count, er.pr_lifecycle_state,
		       COALESCE(e.tenant::text, '') AS tenant
		FROM event_resolution er
		LEFT JOIN events e ON er.event_id = e.id
		WHERE er.type = 'PullRequest'
		  AND er.status = 'InProgress'
		  AND er.pr_lifecycle_state IN ('created', 'needs_followup')
		  AND er.pr_iteration_count < %d
		  AND (
		    (er.pr_iteration_count < %d AND (er.last_pr_check_at IS NULL OR er.last_pr_check_at < now() - interval '%s'))
		    OR
		    (er.pr_iteration_count >= %d AND er.last_pr_check_at < now() - interval '%s')
		  )
	`,
		followupRecoveryHardLimit,
		followupIterationCap, followupCooldown,
		followupIterationCap, followupRecoveryInterval,
	)
	eventRows, err := dbms.Db.Queryx(eventQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query event_resolution: %w", err)
	}
	defer func() { _ = eventRows.Close() }()

	for eventRows.Next() {
		var row prResolutionRow
		row.TableName = "event_resolution"
		if err := eventRows.StructScan(&row); err != nil {
			return nil, fmt.Errorf("failed to scan event_resolution row: %w", err)
		}
		results = append(results, row)
	}

	recQuery := fmt.Sprintf(`
		SELECT rr.id, rr.type_reference_id, rr.data,
		       rr.pr_iteration_count, rr.pr_lifecycle_state,
		       '' AS tenant
		FROM recommendation_resolution rr
		WHERE rr.type = 'PullRequest'
		  AND rr.status = 'InProgress'
		  AND rr.pr_lifecycle_state IN ('created', 'needs_followup')
		  AND rr.pr_iteration_count < %d
		  AND (
		    (rr.pr_iteration_count < %d AND (rr.last_pr_check_at IS NULL OR rr.last_pr_check_at < now() - interval '%s'))
		    OR
		    (rr.pr_iteration_count >= %d AND rr.last_pr_check_at < now() - interval '%s')
		  )
	`,
		followupRecoveryHardLimit,
		followupIterationCap, followupCooldown,
		followupIterationCap, followupRecoveryInterval,
	)
	recRows, err := dbms.Db.Queryx(recQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query recommendation_resolution: %w", err)
	}
	defer func() { _ = recRows.Close() }()

	for recRows.Next() {
		var row prResolutionRow
		row.TableName = "recommendation_resolution"
		if err := recRows.StructScan(&row); err != nil {
			return nil, fmt.Errorf("failed to scan recommendation_resolution row: %w", err)
		}
		results = append(results, row)
	}

	return results, nil
}

// FindOpenPRResolutionByURL looks up a PR resolution row across both
// event_resolution and recommendation_resolution by exact pr_url match.
// Returns ("", "", nil) if no matching open row exists — that's not an error,
// it just means the webhook is for a PR we don't own. Callers should treat
// the empty resolution ID as "drop and 200".
func FindOpenPRResolutionByURL(prURL string) (resolutionID, tableName string, err error) {
	if prURL == "" {
		return "", "", nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", fmt.Errorf("failed to get database connection: %w", err)
	}

	// Prefer event_resolution so we don't hit recommendation_resolution for
	// PRs created off events (which would be a stale match if both tables
	// happened to carry the same URL). The two tables never share a PR URL
	// in practice, but the order makes the intent explicit.
	eventQuery := `
		SELECT id FROM event_resolution
		WHERE type = 'PullRequest'
		  AND status = 'InProgress'
		  AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing')
		  AND data->>'pr_url' = $1
		ORDER BY id
		LIMIT 1
	`
	if err := dbms.Db.QueryRowx(eventQuery, prURL).Scan(&resolutionID); err == nil {
		return resolutionID, "event_resolution", nil
	}

	recQuery := `
		SELECT id FROM recommendation_resolution
		WHERE type = 'PullRequest'
		  AND status = 'InProgress'
		  AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing')
		  AND data->>'pr_url' = $1
		ORDER BY id
		LIMIT 1
	`
	if err := dbms.Db.QueryRowx(recQuery, prURL).Scan(&resolutionID); err == nil {
		return resolutionID, "recommendation_resolution", nil
	}

	return "", "", nil
}

// ProcessOpenPRResolution dispatches a followup for a single PR resolution row,
// identified by its `id` and which table it lives in. Used by the GitHub
// webhook handler (api/public_webhooks.go) to react to PR events without
// waiting for the next cron tick. The iteration cap and tenant resolution
// rules are identical to the cron path.
func ProcessOpenPRResolution(ctx *security.RequestContext, resolutionID, tableName string) error {
	if tableName != "event_resolution" && tableName != "recommendation_resolution" {
		return fmt.Errorf("invalid table name: %s", tableName)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	row, err := fetchResolutionRow(dbms, resolutionID, tableName)
	if err != nil {
		return fmt.Errorf("failed to fetch %s row %s: %w", tableName, resolutionID, err)
	}

	// State / cap / race gating is no longer a read-then-check here: the atomic
	// claim-or-mark inside processResolution decides, under a row lock, whether
	// to claim (created/needs_followup → addressing), mark pending (addressing →
	// re-dispatch after the in-flight run), or skip (terminal / capped). This
	// closes the lost-update window where an event arriving during 'addressing'
	// was silently dropped.
	return processResolution(ctx, dbms, row, maxRedispatchChain)
}

// fetchResolutionRow loads the metadata a followup needs (data, iteration count,
// lifecycle state, tenant) for one resolution row in either table.
func fetchResolutionRow(dbms *database.DatabaseManager, resolutionID, tableName string) (prResolutionRow, error) {
	var row prResolutionRow
	row.TableName = tableName

	var query string
	if tableName == "event_resolution" {
		query = `
			SELECT er.id, er.type_reference_id, er.data,
			       er.pr_iteration_count, er.pr_lifecycle_state,
			       COALESCE(e.tenant::text, '') AS tenant
			FROM event_resolution er
			LEFT JOIN events e ON er.event_id = e.id
			WHERE er.id = $1
		`
	} else {
		query = `
			SELECT rr.id, rr.type_reference_id, rr.data,
			       rr.pr_iteration_count, rr.pr_lifecycle_state,
			       '' AS tenant
			FROM recommendation_resolution rr
			WHERE rr.id = $1
		`
	}

	if err := dbms.Db.QueryRowx(query, resolutionID).StructScan(&row); err != nil {
		return row, err
	}
	return row, nil
}

// MarkPRResolutionTerminal retires a resolution when its PR is closed or merged,
// so the cron and future webhooks stop dispatching followups against a PR that
// can no longer receive them. Only open/active rows are transitioned — terminal
// or unresolvable rows are left untouched, and the WHERE clause means a close
// event landing mid-run leaves the in-flight finalize free to no-op (it
// preserves terminal states; see applyFollowupOutcome).
func MarkPRResolutionTerminal(ctx *security.RequestContext, resolutionID, tableName string, merged bool) error {
	if tableName != "event_resolution" && tableName != "recommendation_resolution" {
		return fmt.Errorf("invalid table name: %s", tableName)
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	state, msg := "closed", "PR closed without merge — no further followup"
	if merged {
		state, msg = "merged", "PR merged — followup complete"
	}

	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	_, err = dbms.Db.ExecContext(dbCtx,
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, status_message = $2,
			pr_followup_pending = false, last_pr_check_at = $3
			WHERE id = $4 AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing')`, tableName),
		state, msg, time.Now(), resolutionID)
	if err != nil {
		return fmt.Errorf("failed to mark resolution terminal: %w", err)
	}
	ctx.GetLogger().Info("pr_lifecycle: marked resolution terminal",
		"id", resolutionID, "table", tableName, "merged", merged, "state", state)
	return nil
}

// claimOrMarkResolution performs the atomic claim-or-mark and returns the row's
// state and iteration count as they were BEFORE the statement, so the caller can
// tell what happened:
//   - created/needs_followup & under cap -> claimed (row is now 'addressing'); run it.
//   - addressing                          -> a run is in flight; pr_followup_pending was set
//     so that run re-dispatches for this newer signal.
//   - created/needs_followup & at cap      -> nothing changed; caller skips (recovery sweep handles it).
//   - terminal/unresolvable                -> nothing changed; caller skips.
//
// It is a single statement against the live row under a per-row lock (FOR UPDATE),
// so it linearizes with applyFollowupOutcome's finalize: no interleaving can both
// miss the re-dispatch and skip the claim, so a signal arriving any time during a
// run is never lost and the pending flag is never orphaned.
func claimOrMarkResolution(dbms *database.DatabaseManager, tableName, id string) (oldState string, oldIters int, err error) {
	query := fmt.Sprintf(`
		WITH cur AS (
			SELECT pr_lifecycle_state AS old_state, pr_iteration_count AS old_iters
			FROM %s WHERE id = $1 FOR UPDATE
		),
		upd AS (
			UPDATE %s t SET
				pr_lifecycle_state = CASE
					WHEN cur.old_state IN ('created', 'needs_followup') AND cur.old_iters < $2
						THEN 'addressing'
					ELSE t.pr_lifecycle_state END,
				pr_followup_pending = CASE
					WHEN cur.old_state = 'addressing' THEN true
					ELSE t.pr_followup_pending END,
				last_pr_check_at = $3
			FROM cur WHERE t.id = $1
			RETURNING cur.old_state, cur.old_iters
		)
		SELECT old_state, old_iters FROM upd`, tableName, tableName)
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	err = dbms.Db.QueryRowContext(dbCtx, query, id, followupIterationCap, time.Now()).
		Scan(&oldState, &oldIters)
	return oldState, oldIters, err
}

func processResolution(ctx *security.RequestContext, dbms *database.DatabaseManager, row prResolutionRow, redispatchBudget int) error {
	oldState, oldIters, err := claimOrMarkResolution(dbms, row.TableName, row.ID)
	if err != nil {
		return fmt.Errorf("failed to claim resolution row: %w", err)
	}

	claimed := (oldState == "created" || oldState == "needs_followup") && oldIters < followupIterationCap
	if !claimed {
		switch oldState {
		case "addressing":
			ctx.GetLogger().Info("pr_lifecycle: followup in flight, marked pending for re-dispatch",
				"id", row.ID, "table", row.TableName)
		case "created", "needs_followup":
			// reached here only when !claimed, i.e. iteration cap hit.
			ctx.GetLogger().Info("pr_lifecycle: skipping, iteration cap reached",
				"id", row.ID, "iteration_count", oldIters)
		default:
			ctx.GetLogger().Info("pr_lifecycle: skipping, state not actionable",
				"id", row.ID, "state", oldState)
		}
		return nil
	}

	// Claimed. From here on the row is 'addressing'; on any early return below we
	// retire it via markFollowupUnresolvable so it can't get stuck addressing.
	var meta prMetadata
	if err := json.Unmarshal(row.Data, &meta); err != nil {
		markFollowupUnresolvable(ctx, dbms, row, "bad_metadata")
		return fmt.Errorf("failed to parse PR metadata: %w", err)
	}

	if meta.PRURL == "" || meta.RepoURL == "" {
		ctx.GetLogger().Warn("pr_lifecycle: skipping resolution with missing PR metadata", "id", row.ID)
		markFollowupUnresolvable(ctx, dbms, row, "missing_metadata")
		return nil
	}

	// For conversation-originated PRs (Slack flow), the events LEFT JOIN returns
	// empty tenant because event_id holds a conversation UUID, not an event UUID.
	// Fall back to tenant_id stored in the PR metadata by agent_code_2.
	tenantID := row.TenantID
	if tenantID == "" && meta.TenantID != "" {
		tenantID = meta.TenantID
	}

	if tenantID == "" {
		// No way to scope a followup without a tenant — neither the events join
		// nor the metadata fallback gave us one. Older rows (esp. AutoRunbook)
		// were inserted before metadata.tenant_id was populated; retrying every
		// 15 min won't recover them. Mark unresolvable and stop.
		ctx.GetLogger().Warn("pr_lifecycle: no tenant for resolution, marking unresolvable",
			"id", row.ID, "pr_url", meta.PRURL, "resolver_table", row.TableName)
		markFollowupUnresolvable(ctx, dbms, row, "missing_tenant")
		return nil
	}

	gitToken, err := getGitTokenForTenant(dbms, tenantID, meta.Provider)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to get git token",
			"tenant", tenantID, "error", err)
		markFollowupUnresolvable(ctx, dbms, row, "missing_git_token")
		return fmt.Errorf("failed to get git token: %w", err)
	}

	ctx.GetLogger().Info("pr_lifecycle: triggering followup for PR",
		"id", row.ID, "pr_url", meta.PRURL, "current_iteration_count", oldIters)

	prBranch := meta.PRBranch
	if prBranch == "" {
		prBranch = meta.Branch
	}

	// Build JSON query that agent_code_2 will unmarshal into CodeAgent2Request
	followupQuery := map[string]any{
		"query":     fmt.Sprintf("Follow up on PR %s — address CI failures and review comments", meta.PRURL),
		"followup":  true,
		"pr_url":    meta.PRURL,
		"git_repo":  meta.RepoURL,
		"pr_branch": prBranch,
		"git_token": gitToken,
	}
	followupQueryJSON, _ := json.Marshal(followupQuery)

	chatRequest := llm.ConversationApiRequest{
		Query:     "@agent_code_2 " + string(followupQueryJSON),
		Source:    "pr_lifecycle",
		AccountId: meta.AccountID,
	}

	// llm-server requires x-tenant-id for auth; the cron ctx has no
	// tenant so ChatCompletion would fail 401. Build a tenant-scoped context from
	// the tenant stored on the resolution row (or in the PR metadata fallback).
	tenantCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())

	go func() {
		// Recover so a panic in the LLM client or response parser doesn't
		// take down the api-server process. The cron has many goroutines in
		// flight at once; one bad followup must not kill the rest.
		defer func() {
			if r := recover(); r != nil {
				tenantCtx.GetLogger().Error("pr_lifecycle: panic in followup goroutine",
					"id", row.ID, "recover", r)
			}
		}()

		// Bound the followup so a stuck llm-server cannot leak this goroutine
		// indefinitely. 35 min is slightly larger than llm-server
		// executeFollowup's maxPollDuration of 30 min, so we never preempt a
		// legitimate poll loop but still cap unbounded hangs. Built on
		// context.Background() because the caller's ctx may be the cron
		// handler's request ctx — we don't want this background work to die
		// when the handler returns.
		bgCtx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
		defer cancel()
		boundedCtx := security.NewRequestContext(bgCtx, tenantCtx.GetSecurityContext(),
			tenantCtx.GetLogger(), tenantCtx.GetTracer(), tenantCtx.GetMeter())

		response, err := llm.ChatCompletion(boundedCtx, chatRequest)
		var outcome followupOutcome
		if err != nil {
			tenantCtx.GetLogger().Error("pr_lifecycle: followup failed", "id", row.ID, "error", err)
			outcome = followupOutcomeFailed
		} else {
			outcome = classifyFollowupOutcome(response.Response)
			tenantCtx.GetLogger().Info("pr_lifecycle: followup completed",
				"id", row.ID,
				"response_status", response.Status,
				"outcome", outcome.name,
				"new_state", outcome.newState)
		}

		// Finalize atomically and learn whether a new actionable signal arrived
		// while this run was in flight (pr_followup_pending). If so, re-dispatch
		// once for it rather than waiting up to a full cron cooldown. Bounded by
		// redispatchBudget so a continuous-signal source can't loop forever.
		wasPending := applyFollowupOutcome(tenantCtx, dbms, row, outcome)
		if wasPending && redispatchBudget > 0 {
			tenantCtx.GetLogger().Info("pr_lifecycle: signal arrived during run, re-dispatching",
				"id", row.ID, "remaining_budget", redispatchBudget-1)
			next, ferr := fetchResolutionRow(dbms, row.ID, row.TableName)
			if ferr != nil {
				tenantCtx.GetLogger().Error("pr_lifecycle: re-dispatch fetch failed", "id", row.ID, "error", ferr)
				return
			}
			if perr := processResolution(tenantCtx, dbms, next, redispatchBudget-1); perr != nil {
				tenantCtx.GetLogger().Error("pr_lifecycle: re-dispatch failed", "id", row.ID, "error", perr)
			}
		}
	}()

	return nil
}

// followupOutcome encapsulates how a single followup result should mutate the
// resolution row. Tri-state because the iteration counter must distinguish:
//
//	success — agent committed/replied; reset count, mark created
//	failed  — real failure or unparseable response; bump count
//	no_op   — nothing actionable was found, or the planner produced no
//	          observable change. Do NOT bump count: the cron must be free to
//	          retry when a reviewer eventually shows up. Without this, a PR
//	          reviewed >2h after creation gets capped before signal arrives.
type followupOutcome struct {
	name     string
	newState string
	// counterDelta is added to pr_iteration_count. 0 = no-op, 1 = failed,
	// -<huge> = success-reset (we clamp to 0 in the UPDATE).
	counterDelta int
	resetCounter bool
}

var (
	followupOutcomeSuccess = followupOutcome{name: "success", newState: "created", counterDelta: 0, resetCounter: true}
	followupOutcomeFailed  = followupOutcome{name: "failed", newState: "needs_followup", counterDelta: 1}
	followupOutcomeNoOp    = followupOutcome{name: "no_op", newState: "needs_followup", counterDelta: 0}
)

// applyFollowupOutcome writes the run's terminal effect on the row and returns
// whether a new actionable signal arrived mid-run (pr_followup_pending was set).
// It does both in ONE row-locked statement so it linearizes with a concurrent
// claim-or-mark in processResolution: either the mark commits first (we read
// was_pending=true and the caller re-dispatches) or this finalize commits first
// (the concurrent dispatch then sees a non-addressing state and claims directly)
// — no interleaving loses the signal or orphans the flag. The terminal guard
// stops a PR-close that landed mid-run from being overwritten back to an open
// state.
func applyFollowupOutcome(ctx *security.RequestContext, dbms *database.DatabaseManager, row prResolutionRow, outcome followupOutcome) (wasPending bool) {
	// counterExpr is built from our own constant outcome (delta 0/1), never user
	// input — no injection surface.
	counterExpr := fmt.Sprintf("pr_iteration_count + %d", outcome.counterDelta)
	if outcome.resetCounter {
		counterExpr = "0"
	}
	query := fmt.Sprintf(`
		WITH cur AS (
			SELECT pr_followup_pending AS was_pending, pr_lifecycle_state AS st
			FROM %s WHERE id = $1 FOR UPDATE
		),
		upd AS (
			UPDATE %s t SET
				pr_lifecycle_state = CASE WHEN cur.st IN ('closed', 'merged', 'unresolvable')
					THEN t.pr_lifecycle_state ELSE $2 END,
				pr_iteration_count = CASE WHEN cur.st IN ('closed', 'merged', 'unresolvable')
					THEN t.pr_iteration_count ELSE %s END,
				last_pr_check_at = $3,
				pr_followup_pending = false
			FROM cur WHERE t.id = $1
			RETURNING cur.was_pending
		)
		SELECT was_pending FROM upd`, row.TableName, row.TableName, counterExpr)
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	if err := dbms.Db.QueryRowContext(dbCtx, query, row.ID, outcome.newState, time.Now()).
		Scan(&wasPending); err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to apply outcome",
			"id", row.ID, "outcome", outcome.name, "error", err)
		return false
	}
	return wasPending
}

// classifyFollowupOutcome reads the agent response and decides how to advance
// the resolution row. The contract with code-analysis (see
// performFollowupAnalysis in agentic_analyze.go) is:
//
//	execution_status="success" — agent committed, edited PR metadata, or
//	                             posted a verified comment reply
//	execution_status="no_op"   — nothing actionable found, or planner ran
//	                             but produced no observable change
//	execution_status="failed"  — agent attempted work and explicitly failed
//
// Anything else (parse failure, missing field, unexpected value) collapses to
// "failed" so the cron retries rather than mistaking noise for a fix.
//
// Historical note: an earlier implementation read agent_resp["success"], a key
// that the followup code path has never set. Every real run was therefore
// classified as a failure, the iteration counter incremented, and PRs hit the
// cap before any reviewer feedback could arrive. The execution_status field
// is what the followup handler actually emits; reading it here closes that
// gap. Do not revert to the bare "success" key without adding a producer.
func classifyFollowupOutcome(responses []string) followupOutcome {
	if len(responses) == 0 {
		return followupOutcomeFailed
	}
	var agentResp map[string]any
	if err := common.UnmarshalJson([]byte(responses[0]), &agentResp); err != nil {
		return followupOutcomeFailed
	}
	status, _ := agentResp["execution_status"].(string)
	switch status {
	case "success", "partial_success":
		return followupOutcomeSuccess
	case "no_op":
		return followupOutcomeNoOp
	default:
		// Backwards compatibility: some legacy code paths emit a top-level
		// `success: true/false` instead of execution_status. Honour it when
		// present so we don't break older agents during a rolling deploy.
		if success, ok := agentResp["success"].(bool); ok && success {
			return followupOutcomeSuccess
		}
		return followupOutcomeFailed
	}
}

// markFollowupUnresolvable retires a resolution row that the cron has no way
// to recover (no tenant, no token, missing metadata). Without this the row
// would stay at pr_lifecycle_state='created' / iteration_count=0 and the
// cron would re-attempt every 15 minutes forever.
func markFollowupUnresolvable(ctx *security.RequestContext, dbms *database.DatabaseManager, row prResolutionRow, reason string) {
	_, err := dbms.Db.ExecContext(ctx.GetContext(),
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = $2, status_message = $3, last_pr_check_at = $4 WHERE id = $5`, row.TableName),
		"unresolvable", 5, "pr_lifecycle followup unresolvable: "+reason, time.Now(), row.ID)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to mark followup unresolvable",
			"id", row.ID, "reason", reason, "error", err)
	}
}

// getGitTokenForTenant retrieves a git token for the given tenant by querying integrations directly.
func getGitTokenForTenant(dbms *database.DatabaseManager, tenantID string, provider string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenant ID is empty")
	}

	integrationType := "github"
	if provider == "gitlab" {
		integrationType = "gitlab"
	}

	var integrationID string
	err := dbms.Db.QueryRowx(`
		SELECT i.id::text
		FROM integrations i
		WHERE i.tenant_id = $1 AND i.type = $2 AND i.status = 'enabled'
		LIMIT 1
	`, tenantID, integrationType).Scan(&integrationID)
	if err != nil {
		return "", fmt.Errorf("no %s integration found for tenant %s: %w", integrationType, tenantID, err)
	}

	var password string
	var isEncrypted bool
	err = dbms.Db.QueryRowx(`
		SELECT value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1 AND name = 'password'
	`, integrationID).Scan(&password, &isEncrypted)
	if err != nil {
		return "", fmt.Errorf("no password config found for integration %s: %w", integrationID, err)
	}

	if isEncrypted && password != "" {
		decrypted, err := common.Decrypt(password)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt token: %w", err)
		}
		return decrypted, nil
	}

	return password, nil
}
