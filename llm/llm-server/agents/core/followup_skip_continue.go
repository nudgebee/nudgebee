package core

import (
	"fmt"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/google/uuid"
)

// followupSkipContinueTypes are the FollowupTypes eligible for
// skip-and-continue (v1) — informational questions only. tool_confirmation,
// tool_config, and account_select keep the hard-terminate path unchanged:
// their "answer" field is interpreted as structured data downstream, not
// free text, and a synthetic sentinel there has no safe, well-defined
// meaning.
var followupSkipContinueTypes = map[FollowupType]bool{
	FollowupTypeText:         true,
	FollowupTypeSingleSelect: true,
	FollowupTypeMultiSelect:  true,
	FollowupTypeUserInput:    true,
}

// followupSkipSentinel is fed as the resumed agent's query when the user
// skips an informational follow-up instead of answering it. It reaches the
// planner through the exact same path a real typed answer does (see
// runAgentResumeV2's comment on why Query must pass through unmodified).
const followupSkipSentinel = "[User skipped answering this question. Proceed with the investigation using only information already available, or ask a different, non-blocking question if you can't continue without more input.]"

// TrySkipAndContinueFollowup attempts to resolve a pending follow-up by
// letting the agent continue without an answer, instead of the caller
// terminating the conversation. handled=false means the caller
// (handleFollowupCancel) should fall back to its existing terminate path —
// either because the follow-up's type is out of v1's scope, or because a
// sibling agent is still waiting on the same follow-up message (v1 does not
// resume multiple siblings from this path; staying safe beats resuming one
// and stranding the rest). (#27582 skip-and-continue v1)
func TrySkipAndContinueFollowup(ctx *security.RequestContext, accountId, conversationId, agentId, reason string) (handled bool, resp NBAgentResponse, err error) {
	dao := GetConversationDao()

	agents, err := dao.ListConversationAgents("", agentId)
	if err != nil {
		return false, NBAgentResponse{}, fmt.Errorf("skip-continue: lookup agent: %w", err)
	}
	if len(agents) == 0 || agents[0].FollowupMessageID == uuid.Nil {
		return false, NBAgentResponse{}, nil
	}
	agent := agents[0]
	// Cross-account guard — see agentBelongsToAccount's doc comment
	// (followup.go). Treated the same as "no active followup" above: fall
	// through to the caller's terminate path, which independently re-checks
	// and rejects with its own "not found" — avoids reading this account's
	// followup message/siblings at all on a mismatch.
	if !agentBelongsToAccount(agent, accountId) {
		return false, NBAgentResponse{}, nil
	}

	followupMessage, err := dao.GetConversationMessage(agent.FollowupMessageID.String(), accountId, conversationId)
	if err != nil {
		return false, NBAgentResponse{}, fmt.Errorf("skip-continue: load followup message: %w", err)
	}
	followupType := FollowupTypeText
	if followupMessage.MessageConfig != nil {
		cfg := map[string]any{}
		if unmarshalErr := common.UnmarshalJson([]byte(*followupMessage.MessageConfig), &cfg); unmarshalErr != nil {
			// Fail closed: an unparsable config means we can't confirm this
			// is actually one of the in-scope informational types — never
			// guess by keeping the FollowupTypeText default, since the real
			// type could be tool_confirmation. Decline (not an error) so the
			// caller falls back to its already-working terminate path,
			// exactly like every other "not applicable" case in this
			// function, rather than surfacing a 500 that blocks the user's
			// skip entirely over what's likely just corrupted data.
			ctx.GetLogger().Warn("skip-continue: failed to parse followup message config, declining skip-and-continue",
				"error", unmarshalErr, "agent_id", agentId)
			return false, NBAgentResponse{}, nil
		}
		if t, ok := cfg["followupType"].(string); ok && t != "" {
			followupType = FollowupType(t)
		}
	}
	if !followupSkipContinueTypes[followupType] {
		return false, NBAgentResponse{}, nil
	}

	// Hold the same lock resumeFollowupLocked's callers use, across both the
	// sibling check and the resume itself — closes the TOCTOU race a
	// separately-locked check would leave open, without deadlocking against
	// resumeFollowupLocked's own (non-reentrant) lock requirement, since we
	// call the already-locked variant directly rather than through
	// HandleFollowupAndResumeV2.
	lock := acquireConversationLock(conversationId)
	defer releaseConversationLock(conversationId, lock)

	if hasBlockingSibling(dao, agent) {
		ctx.GetLogger().Info("skip-continue: sibling still waiting on shared followup, falling back to terminate",
			"agent_id", agentId, "followup_message_id", agent.FollowupMessageID.String())
		return false, NBAgentResponse{}, nil
	}

	if mErr := dao.UpdateConversationMessageMetadata(agent.FollowupMessageID.String(), map[string]any{
		"skip_and_continue": map[string]any{
			"resolution": "dismissed_continued",
			"reason":     reason,
		},
	}); mErr != nil {
		// Best-effort audit marker — don't block the user's skip on it.
		ctx.GetLogger().Warn("skip-continue: failed to write audit marker, proceeding anyway",
			"error", mErr, "agent_id", agentId)
	}

	resumeResp, resumeErr := resumeFollowupLocked(ctx, NBAgentRequest{
		Query:          followupSkipSentinel,
		AccountId:      accountId,
		ConversationId: conversationId,
		AgentId:        agentId,
		MessageId:      agent.MessageID.String(),
	})
	return true, resumeResp, resumeErr
}

// hasBlockingSibling reports whether any TRUE sibling (same parent) of
// agent, bound to the same followup_message_id, is still in a resumable
// status. Mirrors the check HandleFollowupAndResumeV2's own bound-sibling
// loop uses (isTrueSibling + isResumableAgentStatus) — kept as a read-only
// check here rather than resuming siblings, since v1 doesn't implement
// multi-sibling skip-and-continue.
func hasBlockingSibling(dao IConversationDao, agent ConversationAgent) bool {
	if agent.FollowupMessageID == uuid.Nil {
		return false
	}
	siblings, err := dao.ListAgentsByFollowupMessageId(agent.AccountID.String(), agent.FollowupMessageID.String())
	if err != nil {
		// Fail closed: an error here means we can't rule out a stranded
		// sibling, so treat it as blocking and let the caller terminate.
		return true
	}
	for _, sib := range siblings {
		if sib.ID == agent.ID {
			continue
		}
		if isTrueSibling(agent, sib) && isResumableAgentStatus(sib.Status) {
			return true
		}
	}
	return false
}
