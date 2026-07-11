package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/security"
)

// conversation_tree.go backs ai_get_conversation_tree — the Cost Analyser's
// detailed drill-down for a single conversation. It returns the full tree as
// FLAT ARRAYS (parity with ai_get_conversation_v3's assembly pipeline) plus the
// model/API-call leaf that v3 lacks, with cost/tokens/latency on every node.
//
// Structure (recursive — not a fixed depth):
//
//	Conversation
//	└── Message                         (followups via parent_agent_id)
//	    └── Agent call                  recursion: agent.parent_agent_id
//	        ├── Tool call               recursion: tool_call.child_agent_id
//	        └── Model / API call  ← billable leaf (llm_conversation_token_usage)
//
// Model calls and tool calls are SIBLINGS under an agent (token usage has no
// tool_id). Leaf cost is computed with CalculateTotalCost (same helper the
// per-conversation metrics use) so the tree reconciles with the basic overview
// and the aggregates. Ordering is by created_at — there is no sequence column.

// ConversationTreeSummary is the header block (mirrors what the basic overview
// shows) computed over the whole conversation.
type ConversationTreeSummary struct {
	ConversationID         string    `json:"conversation_id"`
	SessionID              string    `json:"session_id"`
	Source                 string    `json:"source"`
	Status                 string    `json:"status"`
	Title                  string    `json:"title"`
	StartedAt              time.Time `json:"started_at"`
	EndedAt                time.Time `json:"ended_at"`
	WallClockSeconds       float64   `json:"wall_clock_seconds"`
	ModelLatencySeconds    float64   `json:"model_latency_seconds"`
	TotalCostUsd           float64   `json:"total_cost_usd"`
	TotalInputTokens       int64     `json:"total_input_tokens"`
	TotalOutputTokens      int64     `json:"total_output_tokens"`
	TotalCachedInputTokens int64     `json:"total_cached_input_tokens"`
	CacheHitRatePct        float64   `json:"cache_hit_rate_pct"`
	MessageCount           int       `json:"message_count"`
	AgentCount             int       `json:"agent_count"`
	ToolCallCount          int       `json:"tool_call_count"`
	ModelCallCount         int       `json:"model_call_count"`
}

// TreeMessage is a conversation turn. CostUsd/tokens roll up every model call
// under this message (across all its agents).
type TreeMessage struct {
	ID            string    `json:"id"`
	ParentAgentID string    `json:"parent_agent_id"`
	Role          string    `json:"role"`
	MessageType   string    `json:"message_type"`
	AgentName     string    `json:"agent_name"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CostUsd       float64   `json:"cost_usd"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
}

// TreeAgent is an agent invocation in the overview. parent_agent_id nests agents.
// CostUsd is this agent's DIRECT model calls; SubtreeCostUsd additionally folds in
// every agent it spawned (via its tool calls' child_agent_id, recursively) so the
// UI can rank "most expensive branch" without walking the tree itself. The full
// per-call detail + execution content lives in ai_get_conversation_agent.
type TreeAgent struct {
	ID                  string    `json:"id"`
	MessageID           string    `json:"message_id"`
	ParentAgentID       string    `json:"parent_agent_id"`
	AgentName           string    `json:"agent_name"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	DurationSeconds     float64   `json:"duration_seconds"`
	CostUsd             float64   `json:"cost_usd"`
	SubtreeCostUsd      float64   `json:"subtree_cost_usd"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	ModelLatencySeconds float64   `json:"model_latency_seconds"`
	ToolCallCount       int       `json:"tool_call_count"`
	ModelCallCount      int       `json:"model_call_count"`
}

// TreeToolCall is a tool invocation under an agent. child_agent_id links to an
// agent the tool spawned (recursion through tools). Tools have no token cost.
type TreeToolCall struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agent_id"`
	MessageID       string    `json:"message_id"`
	ToolName        string    `json:"tool_name"`
	ToolID          string    `json:"tool_id"`
	ToolType        string    `json:"tool_type"`
	ChildAgentID    string    `json:"child_agent_id"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	DurationSeconds float64   `json:"duration_seconds"`
}

// ConversationTree is the OVERVIEW payload: a header summary plus flat arrays the
// UI nests (messages → agents → tool_calls). It is structure + cost only — no raw
// execution content and no per-model-call array. Drilling into a specific agent's
// model calls and execution content (query/params/responses) is a separate call,
// ai_get_conversation_agent, so the overview stays small regardless of size.
type ConversationTree struct {
	Conversation ConversationTreeSummary `json:"conversation"`
	Messages     []TreeMessage           `json:"messages"`
	Agents       []TreeAgent             `json:"agents"`
	ToolCalls    []TreeToolCall          `json:"tool_calls"`
}

// convScopeCTE scopes every tree query to the conversation row(s) for this
// session within the caller's account.
const convScopeCTE = `conversation_id IN (
	SELECT id FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid)`

// GetConversationTree assembles the full tree for one session within an account.
func (chat *ConversationDao) GetConversationTree(sessionID, accountID string) (ConversationTree, error) {
	if sessionID == "" || accountID == "" {
		return ConversationTree{}, fmt.Errorf("GetConversationTree: session_id and account_id are required")
	}

	tree := ConversationTree{
		Messages:  []TreeMessage{},
		Agents:    []TreeAgent{},
		ToolCalls: []TreeToolCall{},
	}

	// --- Summary header (handles multi-row sessions via min/max) ---
	var sum struct {
		ConversationID string       `db:"conversation_id"`
		SessionID      string       `db:"session_id"`
		Source         string       `db:"source"`
		Status         string       `db:"status"`
		Title          string       `db:"title"`
		StartedAt      time.Time    `db:"started_at"`
		EndedAt        sql.NullTime `db:"ended_at"`
	}
	summaryQuery := `
		SELECT
			(array_agg(id::text ORDER BY created_at))[1]                 AS conversation_id,
			session_id                                                   AS session_id,
			(array_agg(COALESCE(source, 'unknown') ORDER BY created_at))[1] AS source,
			(array_agg(COALESCE(status::text, '') ORDER BY created_at DESC))[1] AS status,
			(array_agg(COALESCE(title, '') ORDER BY created_at DESC))[1] AS title,
			MIN(created_at)                                             AS started_at,
			MAX(updated_at)                                             AS ended_at
		FROM llm_conversations
		WHERE session_id = $1 AND account_id = $2::uuid
		GROUP BY session_id`
	if err := chat.dbManager.Db.Get(&sum, summaryQuery, sessionID, accountID); err != nil {
		if err == sql.ErrNoRows {
			return tree, nil
		}
		slog.Error("GetConversationTree: summary query failed", "error", err)
		return ConversationTree{}, fmt.Errorf("GetConversationTree summary: %w", err)
	}
	if sum.ConversationID == "" {
		return tree, nil // no conversation for this session/account
	}

	args := []any{sessionID, accountID}

	// --- Model calls (leaves) — fetch first so rollups are ready ---
	var mcScans []struct {
		ID                  string    `db:"id"`
		AgentID             string    `db:"agent_id"`
		MessageID           string    `db:"message_id"`
		Model               string    `db:"llm_model"`
		Provider            string    `db:"llm_provider"`
		InputTokens         int       `db:"input_tokens"`
		OutputTokens        int       `db:"output_tokens"`
		CachedInputTokens   int       `db:"cached_input_tokens"`
		CacheCreationTokens int       `db:"cache_creation_tokens"`
		ThinkingTokens      int       `db:"thinking_tokens"`
		LatencySeconds      float64   `db:"latency_seconds"`
		TtftMs              int       `db:"ttft_ms"`
		RetryAttempt        int       `db:"retry_attempt"`
		IsCacheHit          bool      `db:"is_cache_hit"`
		RequestStatus       string    `db:"request_status"`
		StopReason          string    `db:"stop_reason"`
		ErrorMessage        string    `db:"error_message"`
		CreatedAt           time.Time `db:"created_at"`
	}
	mcQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(agent_id::text, '') AS agent_id, COALESCE(message_id::text, '') AS message_id,
			llm_model, llm_provider,
			input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
			COALESCE(thinking_tokens, 0) AS thinking_tokens,
			COALESCE(latency_seconds, 0) AS latency_seconds,
			COALESCE(ttft_ms, 0) AS ttft_ms,
			retry_attempt, is_cache_hit, request_status,
			COALESCE(stop_reason, '') AS stop_reason,
			COALESCE(error_message, '') AS error_message,
			created_at
		FROM llm_conversation_token_usage
		WHERE %s
		ORDER BY created_at`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&mcScans, mcQuery, args...); err != nil {
		slog.Error("GetConversationTree: model_calls query failed", "error", err)
		return ConversationTree{}, fmt.Errorf("GetConversationTree model_calls: %w", err)
	}

	pricing, err := chat.GetConversationCosts(nil)
	if err != nil {
		return ConversationTree{}, fmt.Errorf("GetConversationTree pricing: %w", err)
	}

	// Per-agent (direct) and per-message rollups, plus conversation totals.
	type agg struct {
		cost          float64
		input, output int64
		latency       float64
		modelCalls    int
	}
	agentAgg := map[string]*agg{}
	msgAgg := map[string]*agg{}
	toolCountByAgent := map[string]int{}
	var totalCost, totalLatency float64
	var totalIn, totalOut, totalCached int64

	for _, m := range mcScans {
		p := pricing[m.Provider+":"+m.Model]
		nonCached := m.InputTokens - m.CachedInputTokens
		if nonCached < 0 {
			nonCached = 0
		}
		cost := CalculateTotalCost(&p, nonCached, m.CachedInputTokens, m.CacheCreationTokens, m.OutputTokens, m.ThinkingTokens)

		if m.AgentID != "" {
			a := agentAgg[m.AgentID]
			if a == nil {
				a = &agg{}
				agentAgg[m.AgentID] = a
			}
			a.cost += cost
			a.input += int64(m.InputTokens)
			a.output += int64(m.OutputTokens)
			a.latency += m.LatencySeconds
			a.modelCalls++
		}
		if m.MessageID != "" {
			mm := msgAgg[m.MessageID]
			if mm == nil {
				mm = &agg{}
				msgAgg[m.MessageID] = mm
			}
			mm.cost += cost
			mm.input += int64(m.InputTokens)
			mm.output += int64(m.OutputTokens)
		}
		totalCost += cost
		totalLatency += m.LatencySeconds
		totalIn += int64(m.InputTokens)
		totalOut += int64(m.OutputTokens)
		totalCached += int64(m.CachedInputTokens)
	}

	// --- Messages ---
	var msgScans []struct {
		ID            string       `db:"id"`
		ParentAgentID string       `db:"parent_agent_id"`
		Role          string       `db:"role"`
		MessageType   string       `db:"message_type"`
		AgentName     string       `db:"agent_name"`
		Status        string       `db:"status"`
		CreatedAt     time.Time    `db:"created_at"`
		UpdatedAt     sql.NullTime `db:"updated_at"`
	}
	msgQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(parent_agent_id::text, '') AS parent_agent_id,
			COALESCE(role, '') AS role, COALESCE(message_type, '') AS message_type,
			COALESCE(agent_name, '') AS agent_name, COALESCE(status::text, '') AS status,
			created_at, updated_at
		FROM llm_conversation_messages
		WHERE %s
		ORDER BY created_at`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&msgScans, msgQuery, args...); err != nil {
		slog.Error("GetConversationTree: messages query failed", "error", err)
		return ConversationTree{}, fmt.Errorf("GetConversationTree messages: %w", err)
	}
	for _, m := range msgScans {
		tm := TreeMessage{
			ID:            m.ID,
			ParentAgentID: m.ParentAgentID,
			Role:          m.Role,
			MessageType:   m.MessageType,
			AgentName:     m.AgentName,
			Status:        m.Status,
			CreatedAt:     m.CreatedAt,
		}
		if m.UpdatedAt.Valid {
			tm.UpdatedAt = m.UpdatedAt.Time
		}
		if a := msgAgg[m.ID]; a != nil {
			tm.CostUsd = a.cost
			tm.InputTokens = a.input
			tm.OutputTokens = a.output
		}
		tree.Messages = append(tree.Messages, tm)
	}

	// --- Agents ---
	var agentScans []struct {
		ID              string       `db:"id"`
		MessageID       string       `db:"message_id"`
		ParentAgentID   string       `db:"parent_agent_id"`
		AgentName       string       `db:"agent_name"`
		Status          string       `db:"status"`
		CreatedAt       time.Time    `db:"created_at"`
		UpdatedAt       sql.NullTime `db:"updated_at"`
		DurationSeconds float64      `db:"duration_seconds"`
	}
	agentQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(message_id::text, '') AS message_id,
			COALESCE(parent_agent_id::text, '') AS parent_agent_id,
			COALESCE(agent_name, '') AS agent_name, COALESCE(status, '') AS status,
			created_at, updated_at,
			COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at)), 0) AS duration_seconds
		FROM llm_conversation_agent
		WHERE %s
		ORDER BY created_at`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&agentScans, agentQuery, args...); err != nil {
		slog.Error("GetConversationTree: agents query failed", "error", err)
		return ConversationTree{}, fmt.Errorf("GetConversationTree agents: %w", err)
	}
	for _, a := range agentScans {
		ta := TreeAgent{
			ID:              a.ID,
			MessageID:       a.MessageID,
			ParentAgentID:   a.ParentAgentID,
			AgentName:       a.AgentName,
			Status:          a.Status,
			CreatedAt:       a.CreatedAt,
			DurationSeconds: a.DurationSeconds,
		}
		if a.UpdatedAt.Valid {
			ta.UpdatedAt = a.UpdatedAt.Time
		}
		if ag := agentAgg[a.ID]; ag != nil {
			ta.CostUsd = ag.cost
			ta.InputTokens = ag.input
			ta.OutputTokens = ag.output
			ta.ModelLatencySeconds = ag.latency
			ta.ModelCallCount = ag.modelCalls
		}
		tree.Agents = append(tree.Agents, ta)
	}

	// --- Tool calls ---
	var toolScans []struct {
		ID              string       `db:"id"`
		AgentID         string       `db:"agent_id"`
		MessageID       string       `db:"message_id"`
		ToolName        string       `db:"tool_name"`
		ToolID          string       `db:"tool_id"`
		ToolType        string       `db:"tool_type"`
		ChildAgentID    string       `db:"child_agent_id"`
		Status          string       `db:"status"`
		CreatedAt       time.Time    `db:"created_at"`
		UpdatedAt       sql.NullTime `db:"updated_at"`
		DurationSeconds float64      `db:"duration_seconds"`
	}
	toolQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(agent_id::text, '') AS agent_id,
			COALESCE(message_id::text, '') AS message_id,
			COALESCE(tool_name, '') AS tool_name, COALESCE(tool_id, '') AS tool_id,
			COALESCE(tool_type, '') AS tool_type, COALESCE(child_agent_id::text, '') AS child_agent_id,
			COALESCE(status, '') AS status, created_at, updated_at,
			COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at)), 0) AS duration_seconds
		FROM llm_conversation_tool_calls
		WHERE %s
		ORDER BY created_at`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&toolScans, toolQuery, args...); err != nil {
		slog.Error("GetConversationTree: tool_calls query failed", "error", err)
		return ConversationTree{}, fmt.Errorf("GetConversationTree tool_calls: %w", err)
	}
	for _, tc := range toolScans {
		ttc := TreeToolCall{
			ID:              tc.ID,
			AgentID:         tc.AgentID,
			MessageID:       tc.MessageID,
			ToolName:        tc.ToolName,
			ToolID:          tc.ToolID,
			ToolType:        tc.ToolType,
			ChildAgentID:    tc.ChildAgentID,
			Status:          tc.Status,
			CreatedAt:       tc.CreatedAt,
			DurationSeconds: tc.DurationSeconds,
		}
		if tc.UpdatedAt.Valid {
			ttc.UpdatedAt = tc.UpdatedAt.Time
		}
		if tc.AgentID != "" {
			toolCountByAgent[tc.AgentID]++
		}
		tree.ToolCalls = append(tree.ToolCalls, ttc)
	}

	// --- Inclusive (subtree) cost per agent ---
	// An agent's children are agents it spawned: directly (agent.parent_agent_id)
	// or via one of its tool calls (tool_call.child_agent_id). subtree_cost_usd =
	// the agent's direct cost + every descendant's direct cost. A per-walk visited
	// set guards against a malformed parent chain (cycle → no infinite recursion).
	childrenOf := map[string]map[string]bool{}
	addChild := func(parent, child string) {
		if parent == "" || child == "" || parent == child {
			return
		}
		if childrenOf[parent] == nil {
			childrenOf[parent] = map[string]bool{}
		}
		childrenOf[parent][child] = true
	}
	for _, a := range agentScans {
		addChild(a.ParentAgentID, a.ID)
	}
	for _, tc := range toolScans {
		addChild(tc.AgentID, tc.ChildAgentID)
	}
	directCost := func(id string) float64 {
		if ag := agentAgg[id]; ag != nil {
			return ag.cost
		}
		return 0
	}
	var subtreeCost func(id string, seen map[string]bool) float64
	subtreeCost = func(id string, seen map[string]bool) float64 {
		if seen[id] {
			return 0
		}
		seen[id] = true
		total := directCost(id)
		for child := range childrenOf[id] {
			total += subtreeCost(child, seen)
		}
		return total
	}
	for i := range tree.Agents {
		tree.Agents[i].ToolCallCount = toolCountByAgent[tree.Agents[i].ID]
		tree.Agents[i].SubtreeCostUsd = subtreeCost(tree.Agents[i].ID, map[string]bool{})
	}

	// --- Header ---
	tree.Conversation = ConversationTreeSummary{
		ConversationID:         sum.ConversationID,
		SessionID:              sum.SessionID,
		Source:                 sum.Source,
		Status:                 sum.Status,
		Title:                  sum.Title,
		StartedAt:              sum.StartedAt,
		ModelLatencySeconds:    totalLatency,
		TotalCostUsd:           totalCost,
		TotalInputTokens:       totalIn,
		TotalOutputTokens:      totalOut,
		TotalCachedInputTokens: totalCached,
		CacheHitRatePct:        cacheHitPct(totalCached, totalIn),
		MessageCount:           len(tree.Messages),
		AgentCount:             len(tree.Agents),
		ToolCallCount:          len(tree.ToolCalls),
		ModelCallCount:         len(mcScans),
	}
	if sum.EndedAt.Valid {
		tree.Conversation.EndedAt = sum.EndedAt.Time
		tree.Conversation.WallClockSeconds = sum.EndedAt.Time.Sub(sum.StartedAt).Seconds()
	}

	return tree, nil
}

// ConversationTreeRequest mirrors the basic-overview request: conversation_id is
// the session_id (historical naming), scoped by account.
type ConversationTreeRequest struct {
	ConversationId string `json:"conversation_id" validate:"required"`
	AccountId      string `json:"account_id" validate:"required"`
	UserId         string `json:"user_id"`
}

// HandleConversationTreeApi backs ai_get_conversation_tree — the detailed
// drill-down a row in ai_list_conversation_costs opens into.
func HandleConversationTreeApi(ctx *security.RequestContext, request ConversationTreeRequest) (ConversationTree, error) {
	if request.ConversationId == "" || request.AccountId == "" {
		return ConversationTree{}, fmt.Errorf("HandleConversationTreeApi: conversation_id and account_id are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		return ConversationTree{}, fmt.Errorf("HandleConversationTreeApi: forbidden account_id")
	}
	return GetConversationDao().GetConversationTree(request.ConversationId, request.AccountId)
}
