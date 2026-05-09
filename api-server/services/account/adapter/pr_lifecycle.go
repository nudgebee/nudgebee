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

func queryOpenPRResolutions(dbms *database.DatabaseManager) ([]prResolutionRow, error) {
	var results []prResolutionRow

	eventQuery := `
		SELECT er.id, er.type_reference_id, er.data,
		       er.pr_iteration_count, er.pr_lifecycle_state,
		       COALESCE(e.tenant::text, '') AS tenant
		FROM event_resolution er
		LEFT JOIN events e ON er.event_id = e.id
		WHERE er.type = 'PullRequest'
		  AND er.status = 'InProgress'
		  AND er.pr_lifecycle_state IN ('created', 'needs_followup')
		  AND er.pr_iteration_count < 5
		  AND (er.last_pr_check_at IS NULL OR er.last_pr_check_at < now() - interval '10 minutes')
	`
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

	recQuery := `
		SELECT rr.id, rr.type_reference_id, rr.data,
		       rr.pr_iteration_count, rr.pr_lifecycle_state,
		       '' AS tenant
		FROM recommendation_resolution rr
		WHERE rr.type = 'PullRequest'
		  AND rr.status = 'InProgress'
		  AND rr.pr_lifecycle_state IN ('created', 'needs_followup')
		  AND rr.pr_iteration_count < 5
		  AND (rr.last_pr_check_at IS NULL OR rr.last_pr_check_at < now() - interval '10 minutes')
	`
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
		"id", row.ID, "pr_url", meta.PRURL, "iteration", row.PRIterationCount+1)

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
		response, err := llm.ChatCompletion(tenantCtx, chatRequest)
		if err != nil {
			ctx.GetLogger().Error("pr_lifecycle: followup failed", "id", row.ID, "error", err)
			_, _ = dbms.Db.ExecContext(context.Background(),
				fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = pr_iteration_count + 1, last_pr_check_at = $2 WHERE id = $3`, row.TableName),
				"needs_followup", time.Now(), row.ID)
			return
		}

		newState, agentSuccess := nextLifecycleState(response.Response)

		ctx.GetLogger().Info("pr_lifecycle: followup completed",
			"id", row.ID,
			"response_status", response.Status,
			"agent_success", agentSuccess,
			"new_state", newState)

		_, _ = dbms.Db.ExecContext(context.Background(),
			fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = pr_iteration_count + 1, last_pr_check_at = $2 WHERE id = $3`, row.TableName),
			newState, time.Now(), row.ID)
	}()

	return nil
}

// nextLifecycleState decides the new pr_lifecycle_state after a followup
// returns. Default is "needs_followup" — only an explicit success=true from
// the agent flips it to "created". Without this, parser failures, success=false,
// and missing-field responses would all collapse into the same "created"
// marker as a healthy iteration, making it impossible to distinguish a stuck
// PR from a real fix landing. Returns (state, agentSuccess) so callers can
// log the decision.
func nextLifecycleState(responses []string) (string, bool) {
	if len(responses) == 0 {
		return "needs_followup", false
	}
	var agentResp map[string]any
	if err := common.UnmarshalJson([]byte(responses[0]), &agentResp); err != nil {
		return "needs_followup", false
	}
	success, ok := agentResp["success"].(bool)
	if !ok || !success {
		return "needs_followup", false
	}
	return "created", true
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
