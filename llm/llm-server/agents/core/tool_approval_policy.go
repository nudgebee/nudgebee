package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"nudgebee/llm/audit"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/toolapproval"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// matchToolApprovalRule is approvedByToolPolicy; a var so tests can stand in for the rule store.
var matchToolApprovalRule = (*plannerExecutor).approvedByToolPolicy

// approvedByToolPolicy returns a durable policy decision for this invocation.
// Lookup or decision-write failures fall back to the normal confirmation
// prompt. The decision table is the canonical, idempotent audit record; the
// shared audit stream is a best-effort projection emitted once for new rows.
func (e *plannerExecutor) approvedByToolPolicy(action NBAgentPlannerToolAction, canonicalToolName, executionInput string, target toolApprovalTarget) *toolapproval.Match {
	toolConfigID := target.ConfigID
	if !config.Config.LlmServerToolApprovalPolicyEnabled || e.ctx == nil || e.ctx.GetSecurityContext() == nil {
		return nil
	}
	tenantID, err := uuid.Parse(e.ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		e.ctx.GetLogger().Warn("tool approval: invalid tenant id; requiring confirmation", "error", err)
		return nil
	}
	// Rules are matched on the account the call runs on, which differs from
	// the conversation's account when the call targets another account.
	accountID, err := uuid.Parse(target.AccountID)
	if err != nil {
		e.ctx.GetLogger().Warn("tool approval: invalid account id; requiring confirmation", "error", err)
		return nil
	}
	// A run with no signed-in person (system job, token-only API call,
	// unlinked chat user) only uses rules that explicitly allow it.
	attended := approvedByPerson(e.ctx)
	match, err := toolapproval.FindMatch(e.ctx.GetContext(), tenantID, accountID, canonicalToolName, executionInput, toolConfigID, attended)
	if err != nil {
		e.ctx.GetLogger().Warn("tool approval: rule lookup failed; requiring confirmation", "tool", action.Tool, "error", err)
		return nil
	}
	if match == nil {
		return nil
	}

	fingerprintBytes := sha256.Sum256([]byte(executionInput))
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	inserted, err := toolapproval.RecordDecision(e.ctx.GetContext(), toolapproval.Decision{
		TenantID: tenantID, AccountID: accountID, RuleID: match.RuleID,
		RuleRevision: match.Revision, RuleName: match.RuleName, MatchKind: match.Kind,
		ToolName: canonicalToolName, ToolConfigID: nullableString(toolConfigID), InputFingerprint: fingerprint,
		UserID: decisionRequester(e.agentRequest.UserId, attended), ConversationID: e.agentRequest.ConversationId,
		MessageID: e.agentRequest.MessageId, AgentID: e.agentRequest.AgentId, ToolCallID: action.ToolID,
	})
	if err != nil {
		e.ctx.GetLogger().Error("tool approval: matched rule decision could not be persisted; requiring confirmation", "rule_id", match.RuleID, "tool", action.Tool, "error", err)
		return nil
	}
	if !inserted {
		return match
	}
	auditRequest := &audit.AuditRequest{Audits: []audit.Audit{{
		UserId:        decisionRequester(e.agentRequest.UserId, attended),
		TenantId:      tenantID.String(),
		AccountId:     accountID.String(),
		EventTime:     time.Now().UTC(),
		EventCategory: audit.EventCategoryChatActions,
		EventType:     audit.EventTypeChatToolPolicyApprove,
		EventAction:   audit.EventActionUpdate,
		EventActor:    audit.EventActorApiService,
		EventTarget:   e.agentRequest.ConversationId,
		EventStatus:   audit.EventStatusSuccess,
		EventState: map[string]any{
			"decision":          "approved",
			"decision_source":   "policy_rule",
			"rule_id":           match.RuleID.String(),
			"rule_revision":     match.Revision,
			"rule_name":         match.RuleName,
			"match_kind":        string(match.Kind),
			"tool_name":         canonicalToolName,
			"tool_config_id":    toolConfigID,
			"input_fingerprint": fingerprint,
			"unattended":        !attended,
		},
		TransactionId: e.ctx.GetTraceId(),
		EventAttr: map[string]any{
			"conversation_id": e.agentRequest.ConversationId,
			"message_id":      e.agentRequest.MessageId,
			"agent_id":        e.agentRequest.AgentId,
			"tool_call_id":    action.ToolID,
		},
	}}}
	if err := audit.CreateAudit(e.ctx, auditRequest); err != nil {
		e.ctx.GetLogger().Error("tool approval: durable decision saved but shared audit projection failed", "rule_id", match.RuleID, "tool", action.Tool, "error", err)
	}
	e.ctx.GetLogger().Info("tool approval: invocation approved by policy", "rule_id", match.RuleID, "rule_revision", match.Revision, "tool", action.Tool, "input_fingerprint", fingerprint)
	return match
}

// toolApprovalTarget is where a call runs: the execution configuration it
// uses and the account that configuration belongs to.
type toolApprovalTarget struct {
	ConfigID  string
	AccountID string
}

func (e *plannerExecutor) toolApprovalTarget(tool toolcore.NBTool, action NBAgentPlannerToolAction) toolApprovalTarget {
	conversation := e.agentRequest.AccountId
	unresolved := toolApprovalTarget{ConfigID: toolapproval.UnresolvedTargetID, AccountID: conversation}
	owner := tool
	if strings.EqualFold(tool.Name(), toolcore.ToolExecuteShellCommand) {
		owner = configOwnerForAction(tool, e.agentRequest.AccountId, action.ToolInput)
		if owner != nil && owner.Name() != tool.Name() {
			// Shell commands owned by a cloud or cluster CLI run against a
			// tenant account; its configuration ID is that account's ID.
			selected, err := e.shellWriteTarget(owner, action)
			targetID := toolcore.ToolConfigID(selected)
			if err != nil || targetID == "" {
				return unresolved
			}
			return toolApprovalTarget{ConfigID: targetID, AccountID: targetID}
		}
	}
	if owner == nil {
		return unresolved
	}
	// An MCP server's tools belong to one integration, so a rule can be pinned
	// to it; the call runs on the conversation's account.
	if scoped, ok := owner.(toolcore.IntegrationTool); ok {
		if id := scoped.IntegrationID(); id != "" {
			return toolApprovalTarget{ConfigID: id, AccountID: conversation}
		}
		return unresolved
	}
	if _, configurable := owner.(toolcore.NBToolConfig); !configurable {
		return toolApprovalTarget{AccountID: conversation}
	}
	// An integration tool's call on another account runs on an integration
	// of that account: match on the integration the call will actually use,
	// as the approval card does, and ask when it cannot be told yet.
	if tools.IsTargetedIntegrationTool(e.ctx, owner) && tools.CallHasTarget(action.ToolInput, e.agentRequest.QueryConfig.DelegatedTarget, conversation) {
		callCtx := toolcore.NbToolContext{Ctx: e.ctx, AccountId: conversation, UserId: e.agentRequest.UserId, QueryConfig: e.agentRequest.QueryConfig}
		resolved, err := tools.ResolveToolCallTarget(callCtx, owner, toolcore.ParseNBToolCallRequestFromInput(action.ToolInput))
		targetID := toolcore.ToolConfigID(resolved.ToolConfig)
		if err != nil || targetID == "" || resolved.TargetAccountId == "" {
			return unresolved
		}
		return toolApprovalTarget{ConfigID: targetID, AccountID: resolved.TargetAccountId}
	}
	// Tools whose targets are tenant accounts (clusters, cloud accounts) run
	// on the account behind the chosen configuration, however it was chosen;
	// other configured tools run on the conversation's account.
	_, accountTargets := owner.(toolcore.PerCallTargetTool)
	targetAccount := func(targetID string) string {
		if accountTargets {
			return targetID
		}
		return conversation
	}
	if scoped, ok := tool.(toolcore.PerCallTargetTool); ok {
		selected, found, err := e.perCallWriteTarget(tool, scoped, action)
		if err != nil {
			return unresolved
		}
		if found {
			// A per-call target is a tenant account; its configuration ID is
			// that account's ID.
			targetID := toolcore.ToolConfigID(selected)
			if targetID == "" {
				return unresolved
			}
			return toolApprovalTarget{ConfigID: targetID, AccountID: targetID}
		}
	}
	selectedName := e.agentRequest.QueryConfig.ToolConfigs[owner.Name()]
	if strings.TrimSpace(selectedName) == "" {
		configs, err := toolcore.ListToolConfigs(e.ctx, e.agentRequest.AccountId, owner)
		if err != nil || len(configs) != 1 {
			return unresolved
		}
		targetID := toolcore.ToolConfigID(configs[0])
		if targetID == "" {
			return unresolved
		}
		return toolApprovalTarget{ConfigID: targetID, AccountID: targetAccount(targetID)}
	}
	selected, err := toolcore.GetToolConfigByName(e.ctx, e.agentRequest.AccountId, owner, selectedName)
	targetID := toolcore.ToolConfigID(selected)
	if err != nil || targetID == "" {
		return unresolved
	}
	return toolApprovalTarget{ConfigID: targetID, AccountID: targetAccount(targetID)}
}

// policyApprovalMetadata is the step metadata recorded when a rule waived the
// call's confirmation; nil when none did.
func policyApprovalMetadata(match *toolapproval.Match) map[string]any {
	if match == nil {
		return nil
	}
	return map[string]any{"rule_id": match.RuleID.String(), "rule_name": match.RuleName, "rule_revision": match.Revision}
}

// decisionRequester names who made the request a rule waived: the person, or
// the system user for a run with nobody signed in, never an empty string.
func decisionRequester(userID string, attended bool) string {
	if attended && strings.TrimSpace(userID) != "" {
		return strings.TrimSpace(userID)
	}
	return security.GetSystemUserId()
}

func nullableString(value string) *string {
	if value == "" || value == toolapproval.UnresolvedTargetID {
		return nil
	}
	return &value
}

func policyApprovalLogValue(match *toolapproval.Match) string {
	if match == nil {
		return ""
	}
	return fmt.Sprintf("%s@%d", match.RuleID, match.Revision)
}
