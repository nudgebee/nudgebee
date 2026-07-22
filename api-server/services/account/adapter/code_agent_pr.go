package adapter

import (
	"context"
	"fmt"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/security"

	"go.opentelemetry.io/otel/trace"
)

// code_agent_pr.go holds the shared machinery for driving a "fix + raise PR"
// conversation through the code-analysis agent (@agent_code_2). Every caller
// (rightsizing, security-fix, alert-threshold) differs only in the prompt it
// builds; the goroutine wrapping, panic recovery, LLM dispatch, PR extraction,
// and resolution status / lifecycle bookkeeping are identical and live here.

// codeAgentPRParams carries everything dispatchCodeAgentPR needs to run one
// @agent_code_2 "fix + raise PR" conversation asynchronously and record its
// outcome on a resolution row. The caller supplies a prompt builder; everything
// else — the JSON envelope, the LLM call, PR extraction, and status updates —
// is shared.
type codeAgentPRParams struct {
	AccountID         string // cloud account the conversation runs against
	RecommendationID  string // id echoed into the agent envelope + Config (branch/traceability)
	ResolutionID      string // resolution row id to update with status/PR
	IsEventResolution bool   // true → event_resolution, false → recommendation_resolution
	Org               string
	Repo              string
	Branch            string
	// RepoURL, when set, is used verbatim as the git repository passed to the
	// agent (needed for GitLab / self-hosted hosts). Empty → the GitHub URL is
	// built from Org/Repo for backward compatibility.
	RepoURL        string
	Provider       string // "github" (default) or "gitlab"; recorded in PR lifecycle metadata
	Label          string // used in log lines + failure messages (e.g. "code agent")
	SuccessMessage string // user-facing status_message when the PR is raised
}

// dispatchCodeAgentPR launches the @agent_code_2 conversation in a background
// goroutine and returns immediately. buildPrompt is invoked inside that
// goroutine (so any lookups it makes stay off the request path) and returns the
// caller-specific natural-language instructions. The resolution row is updated
// in place as the run progresses: to InProgress + PR url on success, or Failed
// with the agent's reported cause otherwise. Credentials are resolved by the
// agent, so the caller never handles a git token.
func dispatchCodeAgentPR(ctx AccountAdapterContext, p codeAgentPRParams, buildPrompt func() (string, error)) {
	go func() {
		// Recover from any panics to prevent crashing the application.
		defer func() {
			if r := recover(); r != nil {
				ctx.GetLogger().Error("recommendation_resolution: panic recovered in "+p.Label+" goroutine", "panic", r, "recommendation_id", p.ResolutionID)

				if dbms, err := database.GetDatabaseManager(database.Metastore); err == nil {
					tableName := resolutionTableIdent(p.IsEventResolution)
					_, _ = dbms.Db.ExecContext(
						context.Background(),
						fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName),
						models.RecommendationResolutionStatusFailed,
						time.Now(),
						fmt.Sprintf("Panic during %s execution: %v", p.Label, r),
						p.ResolutionID,
					)
				}
			}
		}()

		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			ctx.GetLogger().Error("failed recommendation resolution db connection", "error", err)
			return
		}

		tableName := resolutionTableIdent(p.IsEventResolution)

		// Update the resolution row's status. When a PR url is provided, also
		// seed the PR lifecycle columns the followup cron reads.
		updateStatus := func(status models.RecommendationResolutionStatus, statusMessage string, prURL string) {
			query := fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName)
			params := []any{status, time.Now(), statusMessage, p.ResolutionID}

			if prURL != "" {
				query = fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3, type_reference_id = $5, pr_lifecycle_state = $6, pr_iteration_count = $7, last_pr_check_at = $8 WHERE id = $4`, tableName)
				params = append(params, prURL, "created", 0, time.Now())
			}

			if _, err := dbms.Db.ExecContext(context.Background(), query, params...); err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution status", "error", err, "status", status)
			}
		}

		queryText, err := buildPrompt()
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to build code agent prompt", "error", err, "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusFailed, err.Error(), "")
			return
		}

		// Wrap the prompt in a JSON envelope so agent_code_2 receives explicit
		// intent flags (mode + raise_pr). agent_code_2 must not infer intent
		// from the prompt; the entrypoint declares it.
		repoURL := p.RepoURL
		if repoURL == "" {
			repoURL = fmt.Sprintf("https://github.com/%s/%s", p.Org, p.Repo)
		}
		provider := p.Provider
		if provider == "" {
			provider = "github"
		}
		codeAgentPayload := map[string]any{
			"query":             queryText,
			"git_repo":          repoURL,
			"mode":              "fix",
			"raise_pr":          true,
			"recommendation_id": p.RecommendationID,
			"account_id":        p.AccountID,
		}
		codeAgentPayloadJSON, _ := common.MarshalJson(codeAgentPayload)
		prompt := "@agent_code_2 " + string(codeAgentPayloadJSON)

		chatRequest := llm.ConversationApiRequest{
			Query:     prompt,
			AccountId: p.AccountID,
			UserId:    ctx.GetSecurityContext().GetUserId(),
			Async:     false,
			Source:    "recommendation",
			Config: map[string]any{
				"recommendation_id": p.RecommendationID,
				"account_id":        p.AccountID,
				"git_repo":          repoURL,
			},
		}

		// Create a new context with trace propagation for the LLM server call.
		// The HTTP request context is already cancelled, so use a fresh
		// Background context but copy the trace span to keep trace continuity.
		span := trace.SpanFromContext(ctx.GetContext())
		llmCtx := trace.ContextWithSpan(context.Background(), span)

		response, err := llm.ChatCompletion(security.NewRequestContext(llmCtx, ctx.GetSecurityContext(), ctx.GetLogger(), nil, nil), chatRequest)
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to get chat completion request", "error", err, "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusFailed, fmt.Sprintf("Failed to execute %s: %s", p.Label, err.Error()), "")
			return
		}

		if response == nil || len(response.Response) == 0 {
			ctx.GetLogger().Warn("recommendation_resolution: chat completion returned empty response", "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusFailed, "Code agent returned empty response", "")
			return
		}

		var agentResponse map[string]any
		if err := common.UnmarshalJson([]byte(response.Response[0]), &agentResponse); err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to parse agent response", "error", err, "response", response.Response[0], "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusFailed, "Failed to parse code agent response", "")
			return
		}

		prURL, prNumber := extractPRFromAgentResponse(agentResponse)
		executionStatus, _ := agentResponse["execution_status"].(string)

		// Priority: a PR url means success, regardless of the execution_status field.
		switch {
		case prURL != "":
			ctx.GetLogger().Info("recommendation_resolution: PR created successfully", "pr_url", prURL, "pr_number", prNumber, "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusInProgress, p.SuccessMessage, prURL)

			// Store PR metadata for the cron-based lifecycle tracker.
			prMeta := map[string]any{
				"pr_url":    prURL,
				"pr_number": prNumber,
				"repo_url":  repoURL,
				"branch":    p.Branch,
				"provider":  provider,
				"org":       p.Org,
				"repo":      p.Repo,
				// tenant_id lets the pr_lifecycle followup cron scope the run.
				// recommendation_resolution has no tenant column, so the cron's
				// fallback reads it from here (with a recommendation join as backstop).
				"tenant_id": ctx.GetSecurityContext().GetTenantId(),
			}
			if branchName, ok := agentResponse["branch"].(string); ok {
				prMeta["pr_branch"] = branchName
			}
			if prMetaJSON, marshalErr := common.MarshalJson(prMeta); marshalErr == nil {
				_, _ = dbms.Db.ExecContext(context.Background(),
					// Merge (jsonb ||) rather than overwrite: the original apply
					// payload carries provider_config, which the status sync
					// requires. A blind `data = $1` drops it and flips the
					// (successfully raised) PR's resolution to Failed on the next
					// sync. prMeta keys win on conflict; prior keys survive.
					fmt.Sprintf(`UPDATE %s SET data = COALESCE(data, '{}'::jsonb) || $1::jsonb WHERE id = $2`, tableName),
					string(prMetaJSON), p.ResolutionID)
			}

		case executionStatus == "success":
			if reason := prCreationFailureReason(agentResponse); reason != "" {
				// The fix itself succeeded but the PR could not be opened (e.g. a
				// rejected git push). Surface the real cause instead of a
				// misleading "applied changes but no PR" success.
				ctx.GetLogger().Error("recommendation_resolution: code agent applied changes but PR creation failed", "reason", reason, "label", p.Label)
				updateStatus(models.RecommendationResolutionStatusFailed, "Code agent applied changes but PR creation failed: "+reason, "")
			} else {
				ctx.GetLogger().Warn("recommendation_resolution: code agent succeeded but no PR URL found", "label", p.Label)
				updateStatus(models.RecommendationResolutionStatusSuccess, "Code agent applied changes but no PR was created", "")
			}

		default:
			errorMsg := "Code agent execution failed"
			if summary, ok := agentResponse["execution_summary"].(string); ok && summary != "" {
				errorMsg = summary
			} else if msg, ok := agentResponse["error"].(string); ok && msg != "" {
				errorMsg = msg
			} else if executionStatus != "" {
				errorMsg = fmt.Sprintf("Code agent execution status: %s", executionStatus)
			}

			errorMsg = appendAgentResponseDetails(errorMsg, agentResponse)

			ctx.GetLogger().Error("recommendation_resolution: code agent failed", "error", errorMsg, "response", agentResponse, "label", p.Label)
			updateStatus(models.RecommendationResolutionStatusFailed, errorMsg, "")
		}
	}()
}

// extractPRFromAgentResponse pulls the PR url and number out of a code-agent
// response, trying the known response shapes in order.
func extractPRFromAgentResponse(agentResponse map[string]any) (string, any) {
	var prURL string
	var prNumber any

	for _, key := range []string{"automated_fix_pr_info", "fix_pr"} {
		prInfo, ok := agentResponse[key].(map[string]any)
		if !ok || prInfo == nil {
			continue
		}
		if url, ok := prInfo["url"].(string); ok && url != "" {
			prURL = url
		}
		if num, ok := prInfo["number"]; ok {
			prNumber = num
		}
		if prURL != "" {
			return prURL, prNumber
		}
	}

	// Fallback to a direct pr_url field.
	if url, ok := agentResponse["pr_url"].(string); ok {
		prURL = url
	}
	return prURL, prNumber
}
