package pr_raise

import (
	"fmt"
	"time"

	"nudgebee/services/account/adapter"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

// AlertThresholdPRRequest is the input to ApplyAlertThresholdPR.
type AlertThresholdPRRequest struct {
	AccountID       string
	TenantID        string
	AlertRuleKey    string
	AlertName       string
	Provider        string // "github" | "gitlab"
	IntegrationName string
	Org             string
	Repo            string
	Branch          string
	FilePath        string
	OldContent      string // exact substring to replace in the rule file (old PromQL expr)
	NewContent      string // replacement (rewritten PromQL expr)
	ReferenceLink   string
}

// ApplyAlertThresholdPR opens a GitOps PR that updates an alert rule's threshold expression in
// its rule file, and records a tracking row in event_resolution. Returns the resolution id.
func ApplyAlertThresholdPR(ctx *security.RequestContext, req AlertThresholdPRRequest) (string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountID, security.SecurityAccessTypeCreate) {
		return "", common.ErrorUnauthorized("error: account access not found")
	}
	if req.Org == "" || req.Repo == "" || req.FilePath == "" {
		return "", fmt.Errorf("git org, repo and file path are required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}

	resolutionID := common.GenerateUUID()
	now := time.Now().UTC()
	statusMessage := "Raising PR"
	dataObj := models.NewJsonObject(map[string]any{
		"account_id":     req.AccountID,
		"tenant_id":      req.TenantID,
		"alert_rule_key": req.AlertRuleKey,
		"alert_name":     req.AlertName,
		"file_path":      req.FilePath,
		"repo":           fmt.Sprintf("%s/%s", req.Org, req.Repo),
	})

	// event_id is uuid NOT NULL; alert rules have no event, so use a synthetic id.
	// resolver_id (text) carries the alert_rule_key for traceability.
	_, err = dbms.Db.Exec(`
		INSERT INTO event_resolution
		  (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		resolutionID,
		common.GenerateUUID(),
		models.RecommendationResolutionTypePullRequest,
		dataObj,
		models.RecommendationResolutionStatusInProgress,
		"",
		"User",
		req.AlertRuleKey,
		statusMessage,
		now.Format(time.RFC3339),
	)
	if err != nil {
		ctx.GetLogger().Error("alert threshold PR: failed to insert resolution", "error", err)
		return "", common.ErrorInternal("failed to record PR resolution")
	}

	err = adapter.ApplyAlertThresholdUsingCodeAgent(ctx, adapter.AlertThresholdPRParams{
		Provider:      req.Provider,
		AccountID:     req.AccountID,
		Org:           req.Org,
		Repo:          req.Repo,
		Branch:        req.Branch,
		FilePath:      req.FilePath,
		OldContent:    req.OldContent,
		NewContent:    req.NewContent,
		AlertName:     req.AlertName,
		ReferenceLink: req.ReferenceLink,
	}, resolutionID)
	if err != nil {
		_, uerr := dbms.Db.Exec(`UPDATE event_resolution SET status = $2, status_message = $3, updated_at = $4 WHERE id = $1`,
			resolutionID, models.RecommendationResolutionStatusFailed, err.Error(), time.Now().UTC().Format(time.RFC3339))
		if uerr != nil {
			ctx.GetLogger().Error("alert threshold PR: failed to mark resolution failed", "error", uerr)
		}
		return "", err
	}

	// The code agent runs asynchronously and updates the resolution row's terminal
	// status (PR url on success, or the failure reason) as it progresses. The row is
	// already InProgress ("Raising PR"), so return its id for the caller to poll.
	return resolutionID, nil
}
