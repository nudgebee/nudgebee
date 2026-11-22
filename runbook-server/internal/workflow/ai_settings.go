package workflow

import (
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/audit"
	"nudgebee/runbook/services/security"
)

// Why an automation is not offered to the AI assistant as a tool. Shown to
// admins, so each names the one thing to change; the order is the order to
// fix them in.
const (
	aiNotEnabled       = "not_enabled"        // the AI automation tools feature is off for the account
	aiNotOptedIn       = "not_opted_in"       // "Nubi can run it" is off
	aiNotActive        = "not_active"         // paused or inactive
	aiNotPublished     = "not_published"      // no live version
	aiNoManualTrigger  = "no_manual_trigger"  // the live version has no manual trigger
	aiNoLLMDescription = "no_llm_description" // the live version has no AI description
)

// aiOfferReason is the single rule for whether the assistant is offered an
// automation, shared by ai-search (what the assistant sees) and the AI settings
// list (what admins see), so the two cannot drift. "" means offered. live is
// the published definition, nil when nothing is published. An agent's
// automations are never candidates; the queries exclude them (aiToolNotBound).
func aiOfferReason(featureEnabled, aiInvocable bool, status model.WorkflowStatus, live *model.WorkflowDefinition) string {
	switch {
	case !featureEnabled:
		return aiNotEnabled
	case !aiInvocable:
		return aiNotOptedIn
	case status != model.WorkflowStatusActive:
		return aiNotActive
	case live == nil:
		return aiNotPublished
	case !live.HasManualTrigger():
		return aiNoManualTrigger
	case strings.TrimSpace(live.LLMDescription) == "":
		return aiNoLLMDescription
	}
	return ""
}

// ListAIToolSettings lists, across the caller's readable accounts, the
// automations that are or could be offered to the AI assistant, with their two
// AI flags and whether each is offered right now (judged on the live version,
// as ai-search does).
func (s *Service) ListAIToolSettings(ctx *security.RequestContext, accountIDs []string) (model.ListAIToolSettingsResponse, error) {
	scoped, err := ResolveReadableAccounts(ctx, accountIDs)
	if err != nil {
		return model.ListAIToolSettingsResponse{}, err
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	candidates, err := s.store.ListAIToolCandidates(ctx.GetContext(), tenantID, scoped)
	if err != nil {
		return model.ListAIToolSettingsResponse{}, err
	}

	enabled := map[string]bool{}
	out := make([]model.AIToolSetting, 0, len(candidates))
	for _, c := range candidates {
		on, seen := enabled[c.AccountID]
		if !seen {
			var flagErr error
			on, flagErr = featureEnabledForAccount(common.FeatureAIWorkflowTools, tenantID, c.AccountID)
			if flagErr != nil {
				// Shown as not enabled, as ai-search treats a failed lookup.
				ctx.GetLogger().Warn("ai settings: feature flag lookup failed", "account_id", c.AccountID, "error", flagErr)
				on = false
			}
			enabled[c.AccountID] = on
		}
		out = append(out, aiToolSetting(c, on))
	}
	return model.ListAIToolSettingsResponse{Workflows: out}, nil
}

// UpdateAISettings sets an automation's two AI flags without saving its
// definition or creating a version, so they can be managed from a list.
//
// Same access as editing the automation, and the same rules as saving it:
// the assistant never changes these grants itself, auto-approve needs the
// invocation grant, and the grant needs the draft to carry an AI description
// and a manual trigger. Whether the assistant is then offered it still depends
// on the live version (see aiOfferReason).
func (s *Service) UpdateAISettings(ctx *security.RequestContext, accountID, id string, aiInvocable, aiAutoApprove bool) (model.AIToolSetting, error) {
	sc := ctx.GetSecurityContext()
	if !sc.HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) && !canWriteWorkflows(sc, accountID) {
		return model.AIToolSetting{}, common.ErrorUnauthorized("not allowed to update workflows on this account")
	}
	if ctx.IsAITriggered() {
		return model.AIToolSetting{}, common.ErrorUnauthorized("the AI assistant cannot change whether it may run an automation")
	}
	userID := sc.GetUserId()
	if userID == "" {
		return model.AIToolSetting{}, common.ErrorUnauthorized("a user is required to change an automation's AI settings")
	}
	if aiAutoApprove && !aiInvocable {
		return model.AIToolSetting{}, common.ErrorBadRequest("running without asking needs the AI assistant to be allowed to run it")
	}

	tenantID := sc.GetTenantId()
	wf, err := s.store.Find(ctx.GetContext(), tenantID, accountID, id)
	if err != nil {
		return model.AIToolSetting{}, err
	}
	if wf.Binding != nil {
		return model.AIToolSetting{}, common.ErrorBadRequest("an agent's automation is never offered to the AI assistant")
	}
	if aiInvocable && !wf.AIInvocable {
		if strings.TrimSpace(wf.Definition.LLMDescription) == "" {
			return model.AIToolSetting{}, common.ErrorBadRequest("add an AI description to the automation first")
		}
		if !wf.Definition.HasManualTrigger() {
			return model.AIToolSetting{}, common.ErrorBadRequest("add a manual trigger to the automation first")
		}
	}

	// Saving what is already stored changes nothing, so it writes nothing and
	// leaves no audit row.
	if aiInvocable != wf.AIInvocable || aiAutoApprove != wf.AIAutoApprove {
		before := map[string]any{"ai_invocable": wf.AIInvocable, "ai_auto_approve": wf.AIAutoApprove}
		if err := s.store.UpdateAISettings(ctx.GetContext(), tenantID, accountID, id, userID, aiInvocable, aiAutoApprove); err != nil {
			return model.AIToolSetting{}, err
		}
		emitWorkflowAudit(
			ctx, accountID,
			audit.EventTypeAutomationUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
			id, before, map[string]any{"ai_invocable": aiInvocable, "ai_auto_approve": aiAutoApprove},
			map[string]any{"change": "ai_settings"},
		)
	}

	enabled, flagErr := featureEnabledForAccount(common.FeatureAIWorkflowTools, tenantID, accountID)
	if flagErr != nil {
		// Shown as not enabled, as ai-search treats a failed lookup.
		ctx.GetLogger().Warn("ai settings: feature flag lookup failed", "account_id", accountID, "error", flagErr)
		enabled = false
	}
	var live *model.WorkflowDefinition
	if v, vErr := s.store.GetLiveWorkflowVersion(ctx.GetContext(), wf.ID); vErr == nil && v != nil {
		live = &v.Definition
	}
	return aiToolSetting(model.AIToolCandidate{
		ID:            wf.ID,
		Name:          wf.Name,
		AccountID:     accountID,
		Status:        wf.Status,
		AIInvocable:   aiInvocable,
		AIAutoApprove: aiAutoApprove,
		CreatedBy:     wf.CreatedByUser,
		Live:          live,
	}, enabled), nil
}

// aiToolSetting is the AI settings row for a candidate, with whether the
// assistant is offered it given the account's feature flag.
func aiToolSetting(c model.AIToolCandidate, featureEnabled bool) model.AIToolSetting {
	reason := aiOfferReason(featureEnabled, c.AIInvocable, c.Status, c.Live)
	return model.AIToolSetting{
		ID:               c.ID,
		Name:             c.Name,
		AccountID:        c.AccountID,
		Status:           c.Status,
		AIInvocable:      c.AIInvocable,
		AIAutoApprove:    c.AIAutoApprove,
		Offered:          reason == "",
		NotOfferedReason: reason,
		CreatedByUser:    c.CreatedBy,
	}
}
