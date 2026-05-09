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
// via llm-server. Called from the Hasura cron job every 15 minutes.
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
		if err := processResolution(ctx, dbms, row); err != nil {
			ctx.GetLogger().Error("pr_lifecycle: failed to process resolution",
				"id", row.ID, "table", row.TableName, "error", err)
			continue
		}
	}

	return nil
}

// followupIterationCap and followupRecoveryInterval govern how aggressively
// the cron retries. The base path runs every 10+ minutes until the row hits
// the iteration cap. Once capped, the recovery clause re-checks every 6 hours
// — a safety net for cases where the cap is reached before a reviewer ever
// shows up (Gemini can take >2h after PR creation), or where downstream bugs
// inflate the count beyond what real failures justify. Capped rows that
// produce a no-op or success on a recovery run will not advance further.
const (
	followupIterationCap      = 5
	followupCooldown          = "10 minutes"
	followupRecoveryInterval  = "6 hours"
	followupRecoveryHardLimit = 20
)

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

func processResolution(ctx *security.RequestContext, dbms *database.DatabaseManager, row prResolutionRow) error {
	var meta prMetadata
	if err := json.Unmarshal(row.Data, &meta); err != nil {
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

	// Mark as "addressing" before triggering followup
	_, err = dbms.Db.ExecContext(context.Background(),
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, last_pr_check_at = $2 WHERE id = $3`, row.TableName),
		"addressing", time.Now(), row.ID)
	if err != nil {
		return fmt.Errorf("failed to update lifecycle state: %w", err)
	}

	ctx.GetLogger().Info("pr_lifecycle: triggering followup for PR",
		"id", row.ID, "pr_url", meta.PRURL, "current_iteration_count", row.PRIterationCount)

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

	// llm-server requires X-Hasura-User-Tenant-Id for auth; the cron ctx has no
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
		if err != nil {
			tenantCtx.GetLogger().Error("pr_lifecycle: followup failed", "id", row.ID, "error", err)
			applyFollowupOutcome(tenantCtx, dbms, row, followupOutcomeFailed)
			return
		}

		outcome := classifyFollowupOutcome(response.Response)

		tenantCtx.GetLogger().Info("pr_lifecycle: followup completed",
			"id", row.ID,
			"response_status", response.Status,
			"outcome", outcome.name,
			"new_state", outcome.newState)

		applyFollowupOutcome(tenantCtx, dbms, row, outcome)
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

func applyFollowupOutcome(ctx *security.RequestContext, dbms *database.DatabaseManager, row prResolutionRow, outcome followupOutcome) {
	var query string
	var args []any
	if outcome.resetCounter {
		query = fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = 0, last_pr_check_at = $2 WHERE id = $3`, row.TableName)
		args = []any{outcome.newState, time.Now(), row.ID}
	} else {
		query = fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = pr_iteration_count + $2, last_pr_check_at = $3 WHERE id = $4`, row.TableName)
		args = []any{outcome.newState, outcome.counterDelta, time.Now(), row.ID}
	}
	if _, err := dbms.Db.ExecContext(context.Background(), query, args...); err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to apply outcome",
			"id", row.ID, "outcome", outcome.name, "error", err)
	}
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
