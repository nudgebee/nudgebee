package triage

import (
	"context"
	"fmt"

	"nudgebee/services/eventrule"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability/alertrule"
	"nudgebee/services/pr_raise"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

// threshold_apply_service.go orchestrates applying (and reverting) a cached threshold
// suggestion. It builds the mutated rule via threshold_apply.go and routes the write to
// either the direct path (eventrule.UpdateEventRule / DisableEventRule) or a GitOps PR
// (pr_raise), then records apply state on the suggestion row for audit + revert.

// ApplyThresholdRequest is the input to ApplyThresholdSuggestion.
type ApplyThresholdRequest struct {
	AlertRuleKey   string   `json:"alert_rule_key"`
	CloudAccountID string   `json:"cloud_account_id"`
	Method         string   `json:"method"`        // "direct" | "pr"
	OverrideRisk   bool     `json:"override_risk"` // required to apply a "dangerous" suggestion
	AcceptValue    *float64 `json:"accept_value"`  // optional user-edited threshold
	AcceptDuration *int     `json:"accept_duration"`
	// PR-path inputs (ignored for direct).
	GitProvider    string `json:"git_provider"`    // "github" | "gitlab"
	GitIntegration string `json:"git_integration"` // integration name (provider_config name)
	GitFilePath    string `json:"git_file_path"`   // path of the rule file in the repo
	GitOrg         string `json:"git_org"`         // repo owner/org
	GitRepo        string `json:"git_repo"`        // repo name
	GitBranch      string `json:"git_branch"`      // base branch
	ReferenceLink  string `json:"reference_link"`
}

// ApplyThresholdResult is the result of an apply/revert.
type ApplyThresholdResult struct {
	Status            string  `json:"status"` // applied | pending | failed
	Method            string  `json:"method"`
	AppliedExpr       string  `json:"applied_expr,omitempty"`
	PreviousThreshold float64 `json:"previous_threshold"`
	AppliedThreshold  float64 `json:"applied_threshold"`
	ResolutionID      *string `json:"resolution_id,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// ApplyThresholdSuggestion applies a cached suggestion's recommendation to the source rule.
func ApplyThresholdSuggestion(ctx *security.RequestContext, req ApplyThresholdRequest) (ApplyThresholdResult, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" {
		return ApplyThresholdResult{}, fmt.Errorf("unauthorized")
	}
	if !sc.HasAccountAccess(req.CloudAccountID, security.SecurityAccessTypeUpdate) {
		return ApplyThresholdResult{}, fmt.Errorf("account unauthorized")
	}
	if req.AlertRuleKey == "" || req.CloudAccountID == "" {
		return ApplyThresholdResult{}, fmt.Errorf("alert_rule_key and cloud_account_id are required")
	}
	if req.Method != "direct" && req.Method != "pr" {
		return ApplyThresholdResult{}, fmt.Errorf("method must be 'direct' or 'pr'")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("database connection error: %w", err)
	}
	db := dbms.Db

	sug, err := getSuggestionForApply(ctx.GetContext(), db, req.AlertRuleKey, req.CloudAccountID)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("suggestion not found: %w", err)
	}

	// Risk gate — server-enforced, not just UI.
	if sug.RiskLevel == "dangerous" && !req.OverrideRisk {
		return ApplyThresholdResult{}, fmt.Errorf("blocked: this is a high-risk (dangerous) suggestion and requires explicit override")
	}

	rule, err := LoadSourceRule(ctx.GetContext(), db, req.CloudAccountID, sug.AlertName)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("source rule not found for alert %q: %w", sug.AlertName, err)
	}

	newThreshold := sug.SuggestedThreshold
	if req.AcceptValue != nil {
		newThreshold = *req.AcceptValue
	}
	newDuration := sug.SuggestedDuration
	if req.AcceptDuration != nil {
		newDuration = *req.AcceptDuration
	}

	rewritten, err := BuildRewrittenRule(sug, rule, newThreshold, newDuration)
	if err != nil {
		_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
		return ApplyThresholdResult{}, err
	}

	result := ApplyThresholdResult{
		Method:            req.Method,
		AppliedExpr:       rewritten.Expr,
		PreviousThreshold: rewritten.PreviousThreshold,
		AppliedThreshold:  newThreshold,
	}

	switch req.Method {
	case "direct":
		if err := applyDirect(ctx, sug, rule, rewritten); err != nil {
			_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
			result.Status = "failed"
			result.Error = err.Error()
			return result, err
		}
		result.Status = "applied"
		if merr := markApplied(ctx.GetContext(), db, req, sc.GetUserId(), "applied", rewritten, newThreshold, nil); merr != nil {
			ctx.GetLogger().Error("failed to record threshold apply state", "error", merr, "alert_rule_key", req.AlertRuleKey)
		}

	case "pr":
		resolutionID, err := applyViaPR(ctx, sug, rule, rewritten, req)
		if err != nil {
			_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
			result.Status = "failed"
			result.Error = err.Error()
			return result, err
		}
		result.Status = "pending"
		result.ResolutionID = &resolutionID
		if merr := markApplied(ctx.GetContext(), db, req, sc.GetUserId(), "pending", rewritten, newThreshold, &resolutionID); merr != nil {
			ctx.GetLogger().Error("failed to record threshold apply state", "error", merr, "alert_rule_key", req.AlertRuleKey)
		}
	}

	return result, nil
}

// RevertThresholdSuggestion reapplies the stored previous threshold/duration.
func RevertThresholdSuggestion(ctx *security.RequestContext, alertRuleKey, cloudAccountID string) (ApplyThresholdResult, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" {
		return ApplyThresholdResult{}, fmt.Errorf("unauthorized")
	}
	if alertRuleKey == "" || cloudAccountID == "" {
		return ApplyThresholdResult{}, fmt.Errorf("alert_rule_key and cloud_account_id are required")
	}
	if !sc.HasAccountAccess(cloudAccountID, security.SecurityAccessTypeUpdate) {
		return ApplyThresholdResult{}, fmt.Errorf("account unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("database connection error: %w", err)
	}
	db := dbms.Db

	sug, err := getSuggestionForApply(ctx.GetContext(), db, alertRuleKey, cloudAccountID)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("suggestion not found: %w", err)
	}
	if sug.ApplyStatus != "applied" {
		return ApplyThresholdResult{}, fmt.Errorf("nothing to revert: apply_status is %q", sug.ApplyStatus)
	}
	if sug.ApplyMethod == "pr" {
		return ApplyThresholdResult{}, fmt.Errorf("revert via PR is not supported automatically; open a revert PR manually")
	}
	if sug.PreviousThreshold == nil {
		return ApplyThresholdResult{}, fmt.Errorf("no previous threshold recorded; cannot revert")
	}

	rule, err := LoadSourceRule(ctx.GetContext(), db, cloudAccountID, sug.AlertName)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("source rule not found: %w", err)
	}

	prevDuration := 0
	if sug.PreviousDuration != nil {
		prevDuration = *sug.PreviousDuration
	}
	// Force a threshold rewrite for revert regardless of the original recommendation type.
	sugCopy := *sug
	if sugCopy.RecommendationType == "disable" || sugCopy.RecommendationType == "increase_duration" || sugCopy.RecommendationType == "tune_both" {
		// Anything that originally changed the duration must restore both threshold
		// and duration on revert (tune_both included — otherwise the duration change
		// is left applied).
		sugCopy.RecommendationType = "tune_both"
	} else {
		sugCopy.RecommendationType = "tune_threshold"
	}

	rewritten, err := BuildRewrittenRule(&sugCopy, rule, *sug.PreviousThreshold, prevDuration)
	if err != nil {
		return ApplyThresholdResult{}, err
	}
	if err := applyDirect(ctx, &sugCopy, rule, rewritten); err != nil {
		return ApplyThresholdResult{}, err
	}

	_, err = db.ExecContext(ctx.GetContext(), `
		UPDATE event_threshold_suggestions
		SET apply_status = 'reverted', applied_at = NOW(), applied_by = $3, updated_at = NOW()
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`,
		alertRuleKey, cloudAccountID, sc.GetUserId())
	if err != nil {
		ctx.GetLogger().Error("threshold revert: failed to update apply state", "error", err)
	}

	return ApplyThresholdResult{
		Status:            "reverted",
		Method:            "direct",
		AppliedThreshold:  *sug.PreviousThreshold,
		PreviousThreshold: sug.CurrentThreshold,
		AppliedExpr:       rewritten.Expr,
	}, nil
}

// ApplyOption describes one apply method's availability for a suggestion.
type ApplyOption struct {
	Method    string `json:"method"` // "direct" | "pr"
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // why unavailable
}

// ApplyOptionsResult is returned by GetThresholdApplyOptions.
type ApplyOptionsResult struct {
	Options          []ApplyOption `json:"options"`
	RiskLevel        string        `json:"risk_level"`
	RequiresOverride bool          `json:"requires_override"`
	PreviewExpr      string        `json:"preview_expr,omitempty"`
	PreviewError     string        `json:"preview_error,omitempty"`
	CurrentThreshold float64       `json:"current_threshold"`
	NewThreshold     float64       `json:"new_threshold"`
	Operator         string        `json:"operator,omitempty"`
}

// GetThresholdApplyOptions returns, per source, which apply methods are available (with a
// reason when not), the risk level, and a dry-run preview of the rewritten rule. No writes.
func GetThresholdApplyOptions(ctx *security.RequestContext, alertRuleKey, cloudAccountID string) (ApplyOptionsResult, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" {
		return ApplyOptionsResult{}, fmt.Errorf("unauthorized")
	}
	if alertRuleKey == "" || cloudAccountID == "" {
		return ApplyOptionsResult{}, fmt.Errorf("alert_rule_key and cloud_account_id are required")
	}
	if !sc.HasAccountAccess(cloudAccountID, security.SecurityAccessTypeRead) {
		return ApplyOptionsResult{}, fmt.Errorf("account unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ApplyOptionsResult{}, fmt.Errorf("database connection error: %w", err)
	}
	db := dbms.Db

	sug, err := getSuggestionForApply(ctx.GetContext(), db, alertRuleKey, cloudAccountID)
	if err != nil {
		return ApplyOptionsResult{}, fmt.Errorf("suggestion not found: %w", err)
	}

	out := ApplyOptionsResult{
		RiskLevel:        sug.RiskLevel,
		RequiresOverride: sug.RiskLevel == "dangerous",
		CurrentThreshold: sug.CurrentThreshold,
		NewThreshold:     sug.SuggestedThreshold,
		Operator:         sug.Operator,
	}

	rule, ruleErr := LoadSourceRule(ctx.GetContext(), db, cloudAccountID, sug.AlertName)

	// Dry-run preview (no write).
	if ruleErr == nil {
		if rewritten, perr := BuildRewrittenRule(sug, rule, sug.SuggestedThreshold, sug.SuggestedDuration); perr == nil {
			out.PreviewExpr = rewritten.Expr
		} else {
			out.PreviewError = perr.Error()
		}
	} else {
		out.PreviewError = fmt.Sprintf("source rule not found: %v", ruleErr)
	}

	// Direct availability per source.
	direct := ApplyOption{Method: "direct"}
	switch {
	case ruleErr != nil:
		direct.Reason = "no editable rule found for this alert"
	case promQLSources[sug.Source]:
		if eventrule.IsK8sAgentConnected(cloudAccountID) {
			direct.Available = true
		} else {
			direct.Reason = "k8s agent not connected — cannot push the PrometheusRule change"
		}
	case sug.Source == "AWS_CloudWatch_Alarm":
		direct.Available = true // IAM denials surface at apply time
	case azureSources[sug.Source], sug.Source == "GCP_Metric_Alert":
		// Cloud-collector write path for Azure/GCP is best-effort; offer direct but warn.
		direct.Available = true
		direct.Reason = "direct apply for this provider is best-effort; verify after applying"
	default:
		direct.Reason = fmt.Sprintf("direct apply not supported for source %q", sug.Source)
	}
	if out.PreviewError != "" && direct.Available {
		direct.Available = false
		if direct.Reason == "" {
			direct.Reason = "cannot build the rewritten rule: " + out.PreviewError
		}
	}

	// PR availability — a git integration must be configured for the tenant.
	pr := ApplyOption{Method: "pr"}
	if hasGitIntegration(ctx.GetContext(), db, sc.GetTenantId()) {
		pr.Available = true
	} else {
		pr.Reason = "no GitHub/GitLab integration configured"
	}

	out.Options = []ApplyOption{direct, pr}
	return out, nil
}

// hasGitIntegration reports whether the tenant has an enabled github/gitlab integration.
func hasGitIntegration(ctx context.Context, db *sqlx.DB, tenantID string) bool {
	var count int
	err := db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM integrations
		WHERE tenant_id = $1 AND status = 'enabled' AND type IN ('github', 'gitlab')`, tenantID)
	if err != nil {
		return false
	}
	return count > 0
}

// applyDirect writes the rewritten rule back through the existing eventrule write-back path.
func applyDirect(ctx *security.RequestContext, sug *ApplySuggestionRow, rule *SourceRule, rewritten *RewrittenRule) error {
	if rewritten.Disable {
		_, err := eventrule.DisableEventRule(ctx, eventrule.DisableEventConfig{
			Alert:     sug.AlertName,
			AccountID: sug.CloudAccountID,
			TenantID:  sug.TenantID,
			Id:        rule.ID,
			Enable:    false,
		})
		return err
	}

	// Cloud-provider sources (GCP/AWS/Azure) must be written back to the provider's
	// monitoring API. The generic UpdateEventRule path only pushes Prometheus rules (via
	// relay) and otherwise just upserts the internal event_rules cache — for cloud sources
	// its external-provider branch never fires (the source names differ from the canonical
	// provider keys and event_rules.external_rule_id is empty), so the real alert was left
	// untouched while the apply was still reported as successful. Route cloud sources to
	// the provider write explicitly, using the suggestion's alert_rule_key as the external
	// rule id, and surface any failure so apply/revert status is never faked.
	if provider, ok := cloudAlertProvider(rule.Source); ok {
		_, err := alertrule.UpdateAlertRule(ctx, provider, "user", sug.AlertRuleKey, alertrule.AlertRuleConfig{
			AccountId:      sug.CloudAccountID,
			Name:           sug.AlertName,
			AlertType:      rule.AlertType,
			Query:          rewritten.Expr,
			Severity:       rule.Severity,
			Duration:       rewritten.Duration,
			Enabled:        true,
			ProviderConfig: rewritten.ProviderConfig,
		})
		return err
	}

	cfg := eventrule.EventConfig{
		Alert:                sug.AlertName,
		AccountID:            sug.CloudAccountID,
		Source:               rule.Source,
		Expr:                 rewritten.Expr,
		Category:             rule.Category,
		Severity:             rule.Severity,
		Enabled:              true,
		AlertType:            rule.AlertType,
		MetricProvider:       rule.MetricProvider,
		MetricProviderSource: rule.MetricProviderSource,
		ProviderConfig:       rewritten.ProviderConfig,
	}
	cfg.Duration = rule.Duration
	if rewritten.Duration != "" {
		cfg.Duration = rewritten.Duration
	}
	// Preserve existing annotations/labels so the write-back does not wipe them.
	cfg.Annotations.Description = rule.AnnDescription
	cfg.Annotations.Summary = rule.AnnSummary
	cfg.Annotations.Runbook = rule.AnnRunbook
	cfg.Labels.Severity = rule.LabelSeverity
	if cfg.Severity == "" {
		cfg.Severity = rule.LabelSeverity
	}

	_, err := eventrule.UpdateEventRule(ctx, cfg)
	return err
}

// cloudAlertProvider maps a triage suggestion source to the canonical alertrule provider
// key used by alertrule.UpdateAlertRule (which delegates AWS/GCP/Azure to cloud-collector).
// Returns ok=false for non-cloud sources (Prometheus/PromQL), which take the relay path.
func cloudAlertProvider(source string) (string, bool) {
	switch {
	case source == "GCP_Metric_Alert":
		return "gcp_monitoring", true
	case source == "AWS_CloudWatch_Alarm":
		return "aws_cloudwatch", true
	case azureSources[source]:
		return "azure_app_insights", true
	}
	return "", false
}

// applyViaPR routes the rewritten rule through the GitOps PR path.
func applyViaPR(ctx *security.RequestContext, sug *ApplySuggestionRow, rule *SourceRule, rewritten *RewrittenRule, req ApplyThresholdRequest) (string, error) {
	if req.GitFilePath == "" || req.GitRepo == "" || req.GitOrg == "" {
		return "", fmt.Errorf("git_org, git_repo and git_file_path are required for the PR apply method")
	}
	provider := req.GitProvider
	if provider == "" {
		provider = "github"
	}
	branch := req.GitBranch
	if branch == "" {
		branch = "main"
	}

	if !promQLSources[sug.Source] {
		return "", fmt.Errorf("PR apply is currently supported only for Prometheus/PagerDuty rule files; use direct apply for %q", sug.Source)
	}
	if rule.Expr == rewritten.Expr {
		return "", fmt.Errorf("no expression change to write to the PR")
	}

	return pr_raise.ApplyAlertThresholdPR(ctx, pr_raise.AlertThresholdPRRequest{
		AccountID:       sug.CloudAccountID,
		TenantID:        sug.TenantID,
		AlertRuleKey:    sug.AlertRuleKey,
		AlertName:       sug.AlertName,
		Provider:        provider,
		IntegrationName: req.GitIntegration,
		Org:             req.GitOrg,
		Repo:            req.GitRepo,
		Branch:          branch,
		FilePath:        req.GitFilePath,
		OldContent:      rule.Expr,
		NewContent:      rewritten.Expr,
		ReferenceLink:   req.ReferenceLink,
	})
}

// markApplied records a successful (or pending) apply on the suggestion row, capturing the
// previous values for revert.
func markApplied(ctx context.Context, db *sqlx.DB, req ApplyThresholdRequest, userID, status string, rewritten *RewrittenRule, appliedThreshold float64, resolutionID *string) error {
	var prevDuration *int
	if rewritten.PreviousDuration > 0 {
		d := rewritten.PreviousDuration
		prevDuration = &d
	}
	_, err := db.ExecContext(ctx, `
		UPDATE event_threshold_suggestions
		SET apply_status = $3,
		    apply_method = $4,
		    applied_at = NOW(),
		    applied_by = $5,
		    previous_threshold = $6,
		    previous_duration = $7,
		    applied_threshold = $8,
		    resolution_id = $9,
		    apply_error = NULL,
		    updated_at = NOW()
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`,
		req.AlertRuleKey, req.CloudAccountID, status, req.Method, userID,
		rewritten.PreviousThreshold, prevDuration, appliedThreshold, resolutionID)
	return err
}

// markApplyFailed records a failed apply attempt with the error message.
func markApplyFailed(ctx context.Context, db *sqlx.DB, req ApplyThresholdRequest, errMsg string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE event_threshold_suggestions
		SET apply_status = 'failed', apply_method = $3, apply_error = $4, updated_at = NOW()
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`,
		req.AlertRuleKey, req.CloudAccountID, req.Method, errMsg)
	return err
}
