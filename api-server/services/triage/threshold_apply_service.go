package triage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	OverrideRisk   bool     `json:"override_risk"` // required unless the suggestion is safe with medium or high confidence
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
	if reason := applyBlockReason(sug, req.OverrideRisk); reason != "" {
		return ApplyThresholdResult{}, errors.New(reason)
	}

	rule, err := LoadSourceRule(ctx.GetContext(), db, sc.GetTenantId(), req.CloudAccountID, sug.AlertName, sug.Source, req.AlertRuleKey)
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

	// An explicit user-entered threshold/duration overrides the engine's recommendation type,
	// so a "do not retune" verdict (investigate_signal / insufficient_data) can still be
	// applied when an operator deliberately edits the value.
	sugForApply := *sug
	sugForApply.RecommendationType = applyRecommendationType(sug, rule, req.AcceptValue, req.AcceptDuration)

	rewritten, err := BuildRewrittenRule(&sugForApply, rule, newThreshold, newDuration)
	if err != nil {
		_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
		return ApplyThresholdResult{}, err
	}
	// Nothing to write — the recommendation changes neither threshold nor duration and the
	// user did not override it. Fail loudly for both methods rather than raising an empty PR
	// or reporting a no-op direct write as "applied".
	if !rewritten.Disable && !rewritten.ChangedThreshold && !rewritten.ChangedDuration {
		err := fmt.Errorf("nothing to apply: this suggestion does not change the threshold or duration (recommendation: %q) — enter a different threshold value to override it", sug.RecommendationType)
		_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
		return ApplyThresholdResult{Method: req.Method, Status: "failed", Error: err.Error()}, err
	}

	result := ApplyThresholdResult{
		Method:            req.Method,
		AppliedExpr:       rewritten.Expr,
		PreviousThreshold: rewritten.PreviousThreshold,
		AppliedThreshold:  newThreshold,
	}

	switch req.Method {
	case "direct":
		previous, err := applyDirect(ctx, &sugForApply, rule, rewritten)
		if err != nil {
			_ = markApplyFailed(ctx.GetContext(), db, req, err.Error())
			result.Status = "failed"
			result.Error = err.Error()
			return result, err
		}
		result.Status = "applied"
		if merr := markApplied(ctx.GetContext(), db, req, sc.GetUserId(), "applied", rewritten, newThreshold, nil); merr != nil {
			ctx.GetLogger().Error("failed to record threshold apply state", "error", merr, "alert_rule_key", req.AlertRuleKey)
		}
		storePreviousProviderConfig(ctx, db, req.AlertRuleKey, req.CloudAccountID, previous)

	case "pr":
		resolutionID, err := applyViaPR(ctx, &sugForApply, rule, rewritten, req)
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
	rule, err := LoadSourceRule(ctx.GetContext(), db, sc.GetTenantId(), cloudAccountID, sug.AlertName, sug.Source, alertRuleKey)
	if err != nil {
		return ApplyThresholdResult{}, fmt.Errorf("source rule not found: %w", err)
	}

	plan, err := planRevert(sug, rule, loadPreviousProviderConfig(ctx, db, alertRuleKey, cloudAccountID))
	if err != nil {
		return ApplyThresholdResult{}, err
	}
	if plan.reEnable {
		if _, err := eventrule.DisableEventRule(ctx, eventrule.DisableEventConfig{
			Alert:     sug.AlertName,
			AccountID: sug.CloudAccountID,
			TenantID:  sug.TenantID,
			Id:        rule.ID,
			Enable:    true,
		}); err != nil {
			return ApplyThresholdResult{}, err
		}
	} else if _, err := applyDirect(ctx, plan.sug, rule, plan.rewritten); err != nil {
		return ApplyThresholdResult{}, err
	}
	rewritten := plan.rewritten
	appliedThreshold := plan.appliedThreshold

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
		AppliedThreshold:  appliedThreshold,
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
		RequiresOverride: sug.RiskLevel == "dangerous" || applyNeedsOverride(sug),
		CurrentThreshold: sug.CurrentThreshold,
		NewThreshold:     sug.SuggestedThreshold,
		Operator:         sug.Operator,
	}

	// A stored 'disable' is never applied from here (see applyBlockReason).
	if sug.RecommendationType == "disable" {
		out.Options = []ApplyOption{
			{Method: "direct", Available: false, Reason: disableNotAppliedReason},
			{Method: "pr", Available: false, Reason: disableNotAppliedReason},
		}
		return out, nil
	}

	// quality_only rows have no numeric suggestion (metric not fetchable), so there is nothing
	// mechanical to apply — mark both methods unavailable.
	if sug.Status == "quality_only" {
		reason := "no numeric suggestion — verdict from firing history only; review the rule in the provider console"
		out.Options = []ApplyOption{
			{Method: "direct", Available: false, Reason: reason},
			{Method: "pr", Available: false, Reason: reason},
		}
		return out, nil
	}

	rule, ruleErr := LoadSourceRule(ctx.GetContext(), db, sc.GetTenantId(), cloudAccountID, sug.AlertName, sug.Source, alertRuleKey)

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

	// Direct availability comes from the same resolver Alert Manager uses, so the
	// two screens never disagree about whether a change can reach the source.
	direct := ApplyOption{Method: "direct"}
	if ruleErr != nil {
		direct.Reason = "no editable rule found for this alert"
	} else if target, terr := eventrule.ResolveRuleTarget(ctx, rule.ID, eventrule.ResolveOptions{AlertRuleKey: sug.AlertRuleKey}); terr != nil {
		direct.Reason = fmt.Sprintf("cannot resolve the source rule: %v", terr)
	} else {
		c := directCapability(target.Operations, sug.RecommendationType)
		direct.Available = c.Available
		direct.Reason = c.Reason
		if c.Available && c.Warning != "" {
			direct.Reason = c.Warning
		}
	}
	if out.PreviewError != "" && direct.Available {
		direct.Available = false
		direct.Reason = "cannot build the rewritten rule: " + out.PreviewError
	}

	// PR availability — PromQL rule files only, and a git integration must be configured.
	pr := ApplyOption{Method: "pr"}
	switch {
	case !promQLSources[sug.Source]:
		pr.Reason = fmt.Sprintf("PR apply is supported only for Prometheus rule files, not %q", sug.Source)
	case eventrule.HasGitIntegration(ctx.GetContext(), db, sc.GetTenantId()):
		pr.Available = true
	default:
		pr.Reason = "no GitHub/GitLab integration configured"
	}

	out.Options = []ApplyOption{direct, pr}
	return out, nil
}

// directCapability is the resolver operation a suggestion's direct apply needs.
func directCapability(ops eventrule.RuleOperations, recommendationType string) eventrule.Capability {
	switch recommendationType {
	case "disable":
		return ops.DisableAtSource
	case "increase_duration":
		return ops.UpdateDuration
	case "tune_both":
		if !ops.UpdateDuration.Available {
			return ops.UpdateDuration
		}
	}
	return ops.UpdateThreshold
}

// applyDirect writes the rewritten rule back to the system that evaluates it. The
// resolver decides the route and whether it is allowed; every failure is returned so
// apply/revert status is never faked.
func applyDirect(ctx *security.RequestContext, sug *ApplySuggestionRow, rule *SourceRule, rewritten *RewrittenRule) (map[string]any, error) {
	target, err := eventrule.ResolveRuleTarget(ctx, rule.ID, eventrule.ResolveOptions{AlertRuleKey: sug.AlertRuleKey})
	if err != nil {
		return nil, fmt.Errorf("resolve source rule: %w", err)
	}
	recommendation := sug.RecommendationType
	if rewritten.Disable {
		recommendation = "disable"
	}
	if c := directCapability(target.Operations, recommendation); !c.Available {
		return nil, fmt.Errorf("%w: %s", eventrule.ErrOperationUnavailable, c.Reason)
	}

	if rewritten.Disable {
		_, err := eventrule.DisableEventRule(ctx, eventrule.DisableEventConfig{
			Alert:     sug.AlertName,
			AccountID: sug.CloudAccountID,
			TenantID:  sug.TenantID,
			Id:        rule.ID,
			Enable:    false,
		})
		return nil, err
	}

	// Cloud-provider sources (GCP/AWS/Azure) are written through cloud-collector with
	// the locator the resolver derived (event ARN / policy name / ARM id).
	if provider, ref := target.CloudWriteRef(); provider != "" {
		cfg := cloudUpdateConfig(target, sug, rule, rewritten, ref)
		result, err := alertrule.UpdateAlertRule(ctx, provider, "user", ref, cfg)
		if err != nil {
			return nil, err
		}
		return result.Previous, nil
	}

	// The stored rule is the base, so the write-back changes only the threshold or
	// duration and keeps annotations, labels and the rule's playbook.
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("threshold apply: database manager: %w", err)
	}
	cfg, err := eventrule.LoadEventConfig(ctx.GetContext(), dbms.Db, sug.TenantID, rule.ID)
	if err != nil {
		return nil, fmt.Errorf("threshold apply: load rule: %w", err)
	}
	cfg.Expr = rewritten.Expr
	if rewritten.ProviderConfig != nil {
		cfg.ProviderConfig = rewritten.ProviderConfig
	}
	if rewritten.Duration != "" {
		cfg.Duration = rewritten.Duration
	}

	_, err = eventrule.UpdateEventRule(ctx, cfg)
	return nil, err
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
	// The PR path rewrites the rule expression in the repo file and nothing else, so a change
	// that leaves the expression untouched (disable, or a duration-only tune) has to go
	// through direct apply.
	if rewritten.Disable {
		return "", fmt.Errorf("disabling a rule is not supported through the PR path; use direct apply")
	}
	if rule.Expr == rewritten.Expr {
		return "", fmt.Errorf("no expression change to write to the PR — this change only affects the evaluation window (for:), which the PR path does not write; use direct apply")
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

// cloudUpdateConfig builds the cloud-collector update for an apply or a revert.
// A revert sends only the recorded values: cloud-collector rejects a restore mixed
// with a new threshold or duration. The AWS region is not such a value — it only
// locates an alarm known by name — so a revert of that alarm still carries it.
func cloudUpdateConfig(target *eventrule.RuleTarget, sug *ApplySuggestionRow, rule *SourceRule, rewritten *RewrittenRule, ref string) alertrule.AlertRuleConfig {
	cfg := alertrule.AlertRuleConfig{
		AccountId: sug.CloudAccountID,
		Name:      sug.AlertName,
		AlertType: rule.AlertType,
		Query:     rewritten.Expr,
		Severity:  rule.Severity,
		Duration:  rewritten.Duration,
		Enabled:   target.Enabled,
	}
	if target.Locator.AWSAlarmName != "" {
		cfg.Name = target.Locator.AWSAlarmName
	}
	if len(rewritten.Restore) > 0 {
		cfg.Restore = rewritten.Restore
	} else {
		cfg.ProviderConfig = map[string]interface{}{}
		for k, v := range rewritten.ProviderConfig {
			cfg.ProviderConfig[k] = v
		}
	}
	if target.Kind == eventrule.TargetAWS && ref == "" {
		if cfg.ProviderConfig == nil {
			cfg.ProviderConfig = map[string]interface{}{}
		}
		cfg.ProviderConfig["region"] = target.Locator.AWSRegion
	}
	return cfg
}

// revertPlan is how a direct apply is undone.
type revertPlan struct {
	// reEnable: the apply disabled the rule, so switch it back on.
	reEnable bool
	// sug is the suggestion as applyDirect should see it (its recommendation
	// type decides which capability gates the write).
	sug              *ApplySuggestionRow
	rewritten        *RewrittenRule
	appliedThreshold float64
}

// planRevert decides how to undo an applied suggestion:
//   - a disable is undone by switching the rule back on, not by rewriting a
//     threshold (which for a quality-only suggestion would be 0);
//   - a cloud apply that recorded what it replaced gets exactly that back;
//   - otherwise the stored previous threshold/duration are written again.
func planRevert(sug *ApplySuggestionRow, rule *SourceRule, previous map[string]any) (revertPlan, error) {
	if sug.RecommendationType == "disable" {
		return revertPlan{reEnable: true, sug: sug, rewritten: &RewrittenRule{Expr: rule.Expr}, appliedThreshold: sug.CurrentThreshold}, nil
	}
	if len(previous) > 0 {
		plan := revertPlan{
			sug:              sug,
			rewritten:        &RewrittenRule{Source: rule.Source, Expr: rule.Expr, Restore: previous},
			appliedThreshold: sug.CurrentThreshold,
		}
		if sug.PreviousThreshold != nil {
			plan.appliedThreshold = *sug.PreviousThreshold
		}
		if v, ok := previous["threshold"].(float64); ok {
			plan.appliedThreshold = v
		}
		return plan, nil
	}
	if sug.PreviousThreshold == nil {
		return revertPlan{}, fmt.Errorf("no previous threshold recorded; cannot revert")
	}
	prevDuration := 0
	if sug.PreviousDuration != nil {
		prevDuration = *sug.PreviousDuration
	}
	// Force a threshold rewrite regardless of the original recommendation type.
	// Anything that changed the duration must restore both threshold and duration
	// (tune_both included — otherwise the duration change is left applied).
	sugCopy := *sug
	if sugCopy.RecommendationType == "increase_duration" || sugCopy.RecommendationType == "tune_both" {
		sugCopy.RecommendationType = "tune_both"
	} else {
		sugCopy.RecommendationType = "tune_threshold"
	}
	rewritten, err := BuildRewrittenRule(&sugCopy, rule, *sug.PreviousThreshold, prevDuration)
	if err != nil {
		return revertPlan{}, err
	}
	return revertPlan{sug: &sugCopy, rewritten: rewritten, appliedThreshold: *sug.PreviousThreshold}, nil
}

// storePreviousProviderConfig records what a cloud apply replaced, for an exact
// revert. Best-effort and separate from markApplied, so a missing column can never
// turn a successful apply into a failure.
func storePreviousProviderConfig(ctx *security.RequestContext, db *sqlx.DB, alertRuleKey, cloudAccountID string, previous map[string]any) {
	var value any
	if len(previous) > 0 {
		b, err := json.Marshal(previous)
		if err != nil {
			ctx.GetLogger().Warn("threshold apply: cannot encode previous provider config", "error", err)
			return
		}
		value = string(b)
	}
	if _, err := db.ExecContext(ctx.GetContext(), `
		UPDATE event_threshold_suggestions SET previous_provider_config = $3::jsonb
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`,
		alertRuleKey, cloudAccountID, value); err != nil {
		ctx.GetLogger().Warn("threshold apply: cannot record previous provider config", "error", err, "alert_rule_key", alertRuleKey)
	}
}

// loadPreviousProviderConfig returns what the last cloud apply replaced, or nil.
func loadPreviousProviderConfig(ctx *security.RequestContext, db *sqlx.DB, alertRuleKey, cloudAccountID string) map[string]any {
	var raw sql.NullString
	if err := db.GetContext(ctx.GetContext(), &raw, `
		SELECT previous_provider_config::text FROM event_threshold_suggestions
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`, alertRuleKey, cloudAccountID); err != nil || !raw.Valid {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil
	}
	return out
}
