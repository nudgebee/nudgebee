// Package core — resume_v2.go provides a clean single-entry-point flow for
// followup submissions, replacing the tangled legacy path (#28141).
//
// Key design properties:
//  1. Conversation-level mutex serializes concurrent followup submissions,
//     eliminating the need for IN_PROGRESS special-case handling.
//  2. message_id is resolved from the agent record in DB — the request's
//     message_id is untrusted (the benchmark/UI may send a followup message_id
//     instead of the original generation message_id).
//  3. Parent agent bubble-up is recursive: when a child completes, count
//     siblings; if all done, resume parent; repeat up the tree.
//  4. No executor-state blob manipulation; existing executeAgent path is reused.
//
// Enabled per-deployment via FollowupResumeV2Enabled config flag for incremental
// rollout. Legacy path remains intact.
package core

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
)

// Sharded mutex pool for per-conversation serialization.
//
// Previous implementation used an unbounded “map[string]*sync.Mutex“ that
// grew for the lifetime of the server — one entry per conversation ever
// seen — which is a memory leak in long-running deployments.
//
// Instead of carrying per-entry bookkeeping (reference counts, TTL sweeper,
// eviction races), we pre-allocate a fixed pool of mutexes and pick one by
// hashing the conversation ID. Two unrelated conversations may share a mutex
// and briefly serialize against each other — with 256 shards, collision
// probability is 1/256 per concurrent pair (~0.4%), and the critical section
// (DB lookups + handing off to executeAgent) is short, so the tradeoff is
// negligible against the benefit of zero bookkeeping and provably bounded
// memory.
const conversationLockShards = 256

var conversationLockPool [conversationLockShards]sync.Mutex

// acquireConversationLock returns a held mutex for the shard corresponding
// to the given conversation. Caller must Unlock via releaseConversationLock.
func acquireConversationLock(conversationID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(conversationID))
	idx := h.Sum32() % conversationLockShards
	lock := &conversationLockPool[idx]
	lock.Lock()
	return lock
}

// releaseConversationLock unlocks the shard mutex. Retained as a function
// (rather than calling lock.Unlock() directly at call sites) so future
// bookkeeping (metrics, tracing) has a single choke point.
func releaseConversationLock(conversationID string, lock *sync.Mutex) {
	lock.Unlock()
}

// isResumableAgentStatus reports whether an agent in the given status is
// eligible to be resumed by HandleFollowupAndResumeV2. Both legitimate
// waiting states qualify:
//   - "waiting": agent is paused on a user-facing followup question
//   - "waiting_for_client_tool": agent is paused on a client-tool result
//     (shell_execute / tbench / any NBAgentRequest.ClientTools tool)
//
// All other statuses (success, failure, in_progress) mean the prior
// submission already advanced the agent past the wait point — the V2
// idempotency guard short-circuits.
func isResumableAgentStatus(status AgentExecutionStatus) bool {
	return strings.EqualFold(string(status), string(AgentExecutionStatusWaiting)) ||
		strings.EqualFold(string(status), string(AgentExecutionStatusWaitingForClientTool))
}

// HandleFollowupAndResumeV2 is the single entry point for processing a followup
// response submission when FollowupResumeV2Enabled is true.
//
// Responsibilities:
//   - Serialize with a conv-level lock.
//   - Resolve the correct message_id from the agent record (DB).
//   - Persist the user's answer as the tool's response (via HandleFollowupResponse).
//   - Invoke executeAgent to resume the agent with its saved state.
//   - On child completion, check sibling sub-agents; when all done, resume parent.
//   - Update conversation status to reflect the terminal outcome.
//
// Preconditions: request has non-empty AgentId, Query, ConversationId.
func HandleFollowupAndResumeV2(ctx *security.RequestContext, req NBAgentRequest) (NBAgentResponse, error) {
	if req.AgentId == "" || req.Query == "" || req.ConversationId == "" {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: agent_id, query, and conversation_id are required")
	}

	lock := acquireConversationLock(req.ConversationId)
	defer releaseConversationLock(req.ConversationId, lock)

	logger := ctx.GetLogger()
	logger.Info("resume_v2: handling followup",
		"conversation_id", req.ConversationId,
		"agent_id", req.AgentId,
		"query_preview", truncateStr(req.Query, 80))

	dao := GetConversationDao()

	// 1. Look up the target agent and its correct message_id.
	agents, err := dao.ListConversationAgents("", req.AgentId)
	if err != nil {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: unable to load agent: %w", err)
	}
	if len(agents) == 0 {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: agent %s not found", req.AgentId)
	}
	agent := agents[0]
	correctMessageID := agent.MessageID.String()
	if correctMessageID == "" || correctMessageID == uuid.Nil.String() {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: agent %s has no message_id", req.AgentId)
	}

	// 2. Re-point the request to the generation message where agents were created.
	// The request may carry a followup message_id (from UI/benchmark); replace it
	// with the agent's own message_id so downstream DB lookups find the agent tree.
	if req.MessageId != correctMessageID {
		logger.Info("resume_v2: correcting message_id from agent record",
			"request_message_id", req.MessageId,
			"agent_message_id", correctMessageID)
		req.MessageId = correctMessageID
	}

	// Idempotency guard: if agent already progressed past a waiting state (e.g.
	// a duplicate retry-click or a prior submission already claimed the lock
	// and processed it), don't re-execute. Return a benign response so the
	// client can poll conversation status for the actual outcome.
	//
	// We accept BOTH `waiting` (user-question followups) and
	// `waiting_for_client_tool` (shell_execute / custom client tool resumes).
	// The legacy guard only checked `waiting`, which silently swallowed every
	// client-tool-result resume — leaving conversations stuck IN_PROGRESS until
	// the client-side task timeout. Both statuses share the same resume contract
	// (saved state + executeAgent re-entry), so the same path applies.
	if !isResumableAgentStatus(agent.Status) {
		logger.Info("resume_v2: agent not in waiting state, skipping re-execution",
			"agent_id", req.AgentId, "status", agent.Status)
		return NBAgentResponse{
			ConversationId: req.ConversationId,
			MessageId:      req.MessageId,
			AgentId:        req.AgentId,
			Status:         ConversationStatusInProgress,
			Response:       []string{"Followup already being processed."},
		}, nil
	}

	// NOTE: We intentionally do NOT call HandleFollowupResponse here. That call
	// flips agent.status from WAITING → InProgress, which would cause the
	// downstream refineAgentQuestionAndHandleFollowups (inside executeAgent) to
	// skip its critical restoration block. That block is what:
	//   1. Writes the user's choice into QueryConfig.ToolConfigs[toolName]
	//      (so the tool runs with the right instance/credentials on resume)
	//   2. Replaces request.Query with the ORIGINAL task query (from the
	//      followup message's MessageContext snapshot), preventing the planner
	//      from treating the raw answer ("test-pg", "redis-test-1") as a
	//      brand-new user request
	// By deferring to executeAgent → refine, we keep the legacy semantics.
	// The conv-level lock above still serializes concurrent submissions.

	// IMPORTANT: merge existing message_config into req.QueryConfig. refine
	// only merges from request.QueryConfig + previousQuestion's MessageContext
	// snapshot; it does NOT re-read the current message_config. If a sibling
	// sub-agent's refine already wrote its config, we must surface it here or
	// refine will overwrite message_config losing the sibling's choice (#28141).
	req.QueryConfig = mergeLatestMessageConfig(dao, req.MessageId, req.AccountId, req.ConversationId, req.QueryConfig, logger, "resume_v2: merged existing message_config into request QueryConfig")

	// 3. Resume the agent with its saved state.
	resumeResp, resumeErr := runAgentResumeV2(ctx, req, agent)
	if resumeErr != nil {
		// Persist FAILED status so the conversation doesn't hang.
		_ = dao.UpdateConversationStatus(req.ConversationId, ConversationStatusFailed)
		return resumeResp, resumeErr
	}

	// 4. Resume any sibling agents bound to the SAME tool_config followup
	// (#30515 Part 2). The lateral dedup in GenerateFollowup collapses
	// parallel siblings onto a single followup_message_id; without this
	// step, only the agent the UI targets advances — bound siblings stay
	// in 'waiting' status and CountWaitingSubAgents keeps the parent stuck.
	//
	// Each sibling has its own saved state. The primary's refine has already
	// populated QueryConfig.ToolConfigs[toolName] via message_config; siblings
	// re-read it through mergeLatestMessageConfig and proceed normally.
	//
	// Failures are logged and continue: a sibling that errors out leaves the
	// conversation in WAITING (bubble-up will see that sibling still waiting
	// and not advance the parent), but other siblings still make progress.
	if agent.FollowupMessageID != uuid.Nil {
		boundSiblings, listErr := dao.ListAgentsByFollowupMessageId(req.AccountId, agent.FollowupMessageID.String())
		if listErr != nil {
			logger.Warn("resume_v2: failed to list bound siblings", "error", listErr,
				"followupMessageId", agent.FollowupMessageID)
		} else {
			for _, sib := range boundSiblings {
				if sib.ID.String() == req.AgentId {
					continue // already resumed above
				}
				if !isResumableAgentStatus(sib.Status) {
					continue // sibling not in a waiting state — skip
				}
				sibReq := req
				sibReq.AgentId = sib.ID.String()
				sibReq.MessageId = sib.MessageID.String()
				// Re-read QueryConfig from message_config — the primary's
				// refine wrote ToolConfigs[toolName] there. preResolveToolConfigs
				// in the sibling's executor will see it and skip raising a new
				// followup.
				sibReq.QueryConfig = mergeLatestMessageConfig(dao, sibReq.MessageId,
					sibReq.AccountId, sibReq.ConversationId, sibReq.QueryConfig,
					logger, "resume_v2: sibling resume — merging populated message_config")
				logger.Info("resume_v2: resuming bound sibling",
					"sibling_agent_id", sib.ID.String(),
					"agent_name", sib.AgentName,
					"primary_agent_id", req.AgentId)
				if _, sibErr := runAgentResumeV2(ctx, sibReq, sib); sibErr != nil {
					logger.Warn("resume_v2: bound sibling resume failed — bubble-up will keep conversation WAITING",
						"sibling_agent_id", sib.ID.String(),
						"error", sibErr)
				}
			}
		}
	}

	// 5. When the agent completes, bubble up: check siblings, maybe resume parent.
	if strings.EqualFold(string(resumeResp.Status), string(ConversationStatusCompleted)) {
		bubbleResp, bubbleErr := bubbleUpIfSiblingsDone(ctx, req, resumeResp, agent)
		if bubbleErr != nil {
			logger.Error("resume_v2: bubble-up failed", "error", bubbleErr)
			return resumeResp, bubbleErr
		}
		return bubbleResp, nil
	}

	// 6. Agent returned WAITING (another followup) or in-progress. Persist
	// conversation status to match.
	finalStatus := resumeResp.Status
	if finalStatus == "" {
		finalStatus = ConversationStatusInProgress
	}
	if err := dao.UpdateConversationStatus(req.ConversationId, finalStatus); err != nil {
		logger.Warn("resume_v2: unable to persist conversation status", "error", err)
	}
	return resumeResp, nil
}

// runAgentResumeV2 instantiates the agent implementation and calls executeAgent
// with the saved state from DB. Isolated for clarity and testability.
func runAgentResumeV2(ctx *security.RequestContext, req NBAgentRequest, agent ConversationAgent) (NBAgentResponse, error) {
	dao := GetConversationDao()

	agentName, err := dao.GetAgentNameFromAgentId(req.AgentId)
	if err != nil || agentName == "" {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: cannot resolve agent name: %w", err)
	}
	agentImpl, ok := GetNBAgent(ctx, agentName, req.AccountId, AgentStatusEnabled)
	if !ok {
		return NBAgentResponse{}, fmt.Errorf("resume_v2: agent impl not registered: %s", agentName)
	}

	parentAgentID, previousState := dao.GetConversationAgentParentAgentIdAndPreviousState(req.AgentId)
	if parentAgentID == "" || parentAgentID == uuid.Nil.String() {
		parentAgentID = req.AgentId // top-level agent is its own parent
	}

	// ``req.QueryConfig`` was already merged with the latest message_config by
	// the caller (``HandleFollowupAndResumeV2``) before reaching this function,
	// so no additional DB lookup is needed here. The in-request config is the
	// source of truth for resume.
	queryConfig := req.QueryConfig

	// IMPORTANT: pass the user's raw answer as Query. HandleFollowupResponse
	// (called inside executeAgent → refine) writes request.Query as the
	// followup message's response, which refine then reads to populate
	// ToolConfigs[toolName]. If we override Query here with agent.Query,
	// the config value gets set to the task description instead of the
	// actual choice like "test-pg" — breaking tool execution.
	// After HandleFollowupResponse runs, refine's restoration block replaces
	// request.Query with the original query from MessageContext snapshot
	// (previousQuery). So the sequence is: answer → stored as response →
	// refine uses it as config → refine replaces Query → planner sees
	// original task. We must not short-circuit this sequence.
	resumeReq := NBAgentRequest{
		Query:                 req.Query,
		AccountId:             req.AccountId,
		UserId:                req.UserId,
		ConversationId:        req.ConversationId,
		MessageId:             req.MessageId,
		AgentId:               req.AgentId,
		ParentAgentId:         parentAgentID,
		PreviousState:         previousState,
		QueryConfig:           queryConfig,
		QueryContext:          req.QueryContext,
		SessionId:             req.SessionId,
		ConversationSource:    req.ConversationSource,
		EnableQueryRefinement: req.EnableQueryRefinement,
	}

	ctx.GetLogger().Info("resume_v2: executing agent resume",
		"agent_name", agentName,
		"agent_id", req.AgentId,
		"parent_agent_id", parentAgentID,
		"has_previous_state", previousState != "")

	// NOTE: do NOT flip conversation status here. In multi-followup flows
	// (e.g. concurrent redis + postgres), siblings may still be waiting
	// for user input. Setting IN_PROGRESS at this point would make the UI
	// hide all followup panels including unanswered ones → user can't
	// answer the rest. Status transitions are owned by
	// ``bubbleUpIfSiblingsDone`` (after this child completes), which
	// correctly considers sibling state.

	return executeAgent(ctx, agentImpl, resumeReq)
}

// bubbleUpIfSiblingsDone is the recursive parent-resume helper.
//
// When a sub-agent completes, we check its parent:
//   - If any sibling (same parent, same message) is still waiting/in-progress,
//     leave the conversation WAITING and return the child's response.
//   - If all siblings are done AND the parent has saved state, resume the parent
//     so it can collect all child outputs and run its solver.
//   - Recurse upward: when the parent completes, check its parent, and so on.
//
// Top-level agent (parent_agent_id == self or nil) terminates the recursion.
func bubbleUpIfSiblingsDone(ctx *security.RequestContext, req NBAgentRequest, childResp NBAgentResponse, childAgent ConversationAgent) (NBAgentResponse, error) {
	dao := GetConversationDao()
	logger := ctx.GetLogger()

	parentAgentID := ""
	if childAgent.ParentAgentID != uuid.Nil {
		parentAgentID = childAgent.ParentAgentID.String()
	}

	// Top-level agent: no parent to bubble up to. Persist final message + conversation state.
	if parentAgentID == "" || parentAgentID == uuid.Nil.String() || parentAgentID == childAgent.ID.String() {
		persistFinalMessage(ctx, req.MessageId, childResp)
		if err := dao.UpdateConversationStatus(req.ConversationId, ConversationStatusCompleted); err != nil {
			logger.Warn("resume_v2: failed to mark conversation complete", "error", err)
		}
		return childResp, nil
	}

	// Count siblings still pending under this parent for this message.
	// A DB failure here must NOT be swallowed: assuming 0 and proceeding to
	// parent resume when siblings are actually still waiting produces state
	// corruption — parent re-dispatches the already-in-flight sub-agents,
	// triggers duplicate followups (#28141 class). Surface the error so the
	// caller returns it; the followup message remains in its current state
	// and the next retry (user or reconcile) will re-run the bubble-up.
	waitingCount, countErr := dao.CountWaitingSubAgents(parentAgentID, req.MessageId)
	if countErr != nil {
		logger.Error("resume_v2: sibling count query failed, aborting bubble-up",
			"error", countErr,
			"parent_agent_id", parentAgentID,
			"message_id", req.MessageId)
		return childResp, fmt.Errorf("resume_v2: sibling count query failed: %w", countErr)
	}
	if waitingCount > 0 {
		logger.Info("resume_v2: siblings still waiting, keeping conversation WAITING",
			"parent_agent_id", parentAgentID,
			"waiting_count", waitingCount,
			"completed_agent_id", childAgent.ID.String())
		if err := dao.UpdateConversationStatus(req.ConversationId, ConversationStatusWaiting); err != nil {
			logger.Warn("resume_v2: failed to persist WAITING status", "error", err)
		}
		return childResp, nil
	}

	// All siblings done. Resume parent.
	_, parentState := dao.GetConversationAgentParentAgentIdAndPreviousState(parentAgentID)
	if parentState == "" {
		logger.Info("resume_v2: parent has no saved state, cannot resume — marking complete",
			"parent_agent_id", parentAgentID)
		_ = dao.UpdateConversationStatus(req.ConversationId, ConversationStatusCompleted)
		return childResp, nil
	}

	parentName, nameErr := dao.GetAgentNameFromAgentId(parentAgentID)
	if nameErr != nil || parentName == "" {
		return childResp, fmt.Errorf("resume_v2: parent agent name lookup failed: %w", nameErr)
	}
	parentAgentImpl, ok := GetNBAgent(ctx, parentName, req.AccountId, AgentStatusEnabled)
	if !ok {
		return childResp, fmt.Errorf("resume_v2: parent agent impl not registered: %s", parentName)
	}

	grandparentID, _ := dao.GetConversationAgentParentAgentIdAndPreviousState(parentAgentID)
	if grandparentID == "" || grandparentID == uuid.Nil.String() {
		grandparentID = parentAgentID
	}

	// For parent resume: the parent's followup message is already COMPLETED
	// (it was marked so when the sub-agent's refine ran). HandleFollowupResponse
	// for the parent will return empty (line 420 else branch), so refine's
	// restoration block is skipped. That means whatever Query we pass here is
	// what the parent planner sees. We pass the parent's stored original query
	// since the user's ``req.Query`` is a sub-agent answer (e.g. "test-pg",
	// "redis-test-1") — meaningless at the parent level and would cause the
	// parent planner to hallucinate if surfaced as the user request.
	//
	// Fail fast when the parent agent record can't be loaded: there is no
	// safe fallback. Falling back to ``req.Query`` would hand the parent a
	// sub-agent answer; falling back to the message's query isn't reliable
	// because refine may have mutated it earlier in this flow.
	parentAgents, lookupErr := dao.ListConversationAgents("", parentAgentID)
	if lookupErr != nil || len(parentAgents) == 0 {
		return childResp, fmt.Errorf(
			"resume_v2: failed to load parent agent %s for resume: %w",
			parentAgentID, lookupErr,
		)
	}
	parentResumeQuery := strings.TrimSpace(parentAgents[0].Query)
	if parentResumeQuery == "" {
		// Fail fast. req.Query here is the user's followup answer (e.g. the
		// selected resource name or a yes/no), NOT the parent's task query;
		// feeding it to the parent's planner would corrupt the task and lead
		// to hallucinated output. Parent agent rows always have Query set at
		// creation — an empty value indicates data corruption and should be
		// surfaced to the caller rather than silently patched over.
		logger.Error("resume_v2: parent agent has empty Query, cannot resume safely",
			"parent_agent_id", parentAgentID)
		return childResp, fmt.Errorf("resume_v2: parent agent %s has empty query, cannot resume safely", parentAgentID)
	}

	// Re-read message_config: the child's refine (which just ran inside
	// runAgentResumeV2) updated message_config with its tool_config. Our
	// in-memory req.QueryConfig is from before that refine ran and is stale.
	// Without re-reading, parent resume would miss configs written by prior
	// sibling refines, causing the parent or its re-dispatched sub-agents to
	// fail to find config → duplicate followup loop (#28141).
	parentQueryConfig := mergeLatestMessageConfig(dao, req.MessageId, req.AccountId, req.ConversationId, req.QueryConfig, logger, "resume_v2: refreshed parent QueryConfig from latest message_config")

	parentReq := NBAgentRequest{
		Query:          parentResumeQuery,
		AccountId:      req.AccountId,
		UserId:         req.UserId,
		ConversationId: req.ConversationId,
		MessageId:      req.MessageId,
		AgentId:        parentAgentID,
		ParentAgentId:  grandparentID,
		PreviousState:  parentState,
		QueryConfig:    parentQueryConfig,
		SessionId:      req.SessionId,
	}

	logger.Info("resume_v2: all siblings complete, resuming parent agent",
		"parent_agent_id", parentAgentID,
		"parent_name", parentName)

	// Flip conversation status from WAITING -> IN_PROGRESS before invoking
	// the parent's executeAgent. Without this, clients (UI, benchmark
	// runner, GraphQL subscribers) see the conversation stuck at WAITING
	// for the entire duration of the parent's replan loop — even though
	// no user input is actually pending. The status will be updated again
	// to its terminal value (COMPLETED / FAILED / WAITING-on-new-followup)
	// after executeAgent returns.
	if err := dao.UpdateConversationStatus(req.ConversationId, ConversationStatusInProgress); err != nil {
		logger.Warn("resume_v2: failed to mark conversation IN_PROGRESS before parent resume", "error", err)
	}

	parentResp, parentErr := executeAgent(ctx, parentAgentImpl, parentReq)
	if parentErr != nil {
		logger.Error("resume_v2: parent resume failed", "error", parentErr, "parent_agent_id", parentAgentID)
		return childResp, parentErr
	}

	// If the parent completes, keep walking up.
	if strings.EqualFold(string(parentResp.Status), string(ConversationStatusCompleted)) {
		parentAgents, pErr := dao.ListConversationAgents("", parentAgentID)
		if pErr == nil && len(parentAgents) > 0 {
			return bubbleUpIfSiblingsDone(ctx, req, parentResp, parentAgents[0])
		}
	}

	// Parent returned non-complete (WAITING, failed, etc) — persist that status.
	finalStatus := parentResp.Status
	if finalStatus == "" {
		finalStatus = ConversationStatusInProgress
	}
	// For terminal non-complete states (FAILED), also persist message content.
	// WAITING states leave the message alone — another resume will finalize it.
	if finalStatus == ConversationStatusFailed {
		persistFinalMessage(ctx, req.MessageId, parentResp)
	}
	if err := dao.UpdateConversationStatus(req.ConversationId, finalStatus); err != nil {
		logger.Warn("resume_v2: failed to persist post-parent status", "error", err)
	}
	return parentResp, nil
}

// persistFinalMessage writes the agent response content + status to the
// message row. The normal request flow (conversation.go:~929) handles this,
// but resume_v2 bubbles up through executeAgent directly and must persist
// the message itself — otherwise the UI sees conversation=COMPLETED but
// message.status=IN_PROGRESS with empty response (bug surfaced in #28141).
func persistFinalMessage(ctx *security.RequestContext, messageID string, resp NBAgentResponse) {
	if messageID == "" {
		return
	}
	status := resp.Status
	if status == "" {
		status = ConversationStatusCompleted
	}
	content := ""
	if len(resp.Response) > 0 {
		content = resp.Response[0]
	}
	if err := GetConversationDao().UpdateConversationMessage(messageID, content, status); err != nil {
		ctx.GetLogger().Warn("resume_v2: failed to persist final message", "error", err, "message_id", messageID)
	}
}

// mergeLatestMessageConfig reads the current message_config JSON off the
// generation message and merges it into `base`. Both HandleFollowupAndResumeV2
// (before runAgentResumeV2) and bubbleUpIfSiblingsDone (before parent
// executeAgent) need this: sibling refines write their resolved tool_config
// into message_config, and any in-memory QueryConfig snapshot from the request
// is stale after a sibling has landed. Missing this merge leads to the
// duplicate-followup loop in #28141.
//
// Returns `base` unchanged when the message has no config or parsing fails —
// this is a best-effort refresh, not a validation step.
func mergeLatestMessageConfig(
	dao IConversationDao,
	messageID, accountID, conversationID string,
	base toolcore.NBQueryConfig,
	logger *slog.Logger,
	logMessage string,
) toolcore.NBQueryConfig {
	msg, err := dao.GetConversationMessage(messageID, accountID, conversationID)
	if err != nil || msg.MessageConfig == nil || *msg.MessageConfig == "" {
		return base
	}
	latest := toolcore.NBQueryConfig{}
	if jErr := common.UnmarshalJson([]byte(*msg.MessageConfig), &latest); jErr != nil {
		return base
	}
	merged := base
	if merged.IsEmpty() {
		merged = latest
	} else {
		merged.MergeFrom(latest)
	}
	logger.Info(logMessage, "tool_configs_count", len(merged.ToolConfigs))
	return merged
}

// truncateStr returns s truncated to n runes, used for log previews.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
