package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/security"
)

// conversation_agent_detail.go backs ai_get_conversation_agent — the on-click
// drill-down for ONE agent in the Cost Analyser tree. ai_get_conversation_tree
// is the lightweight overview (structure + cost rollups); this endpoint carries
// the heavy, unbounded execution content for a single agent only, so the payload
// is always bounded to what the user actually expanded:
//
//   - the agent itself: query → thought → response (what it was asked, how it
//     reasoned, what it produced)
//   - every tool call it made: parameters (what it ran) + response (what came
//     back, success body OR error) + status
//   - every model/API call it made: tokens, computed cost, and a per-component
//     cost_breakdown (the justification — input/output/cache split + tier), plus
//     the error/success flags
//
// Cost is computed with the same helpers as the tree and the aggregates
// (CalculateTotalCost / CalculateCostBreakdown), so SUM(model_call.cost_usd)
// equals the agent's cost in the overview and the per-component breakdown sums
// back to each model call's cost_usd.

// CostComponent is one priced slice of a model call: a token category billed at
// one rate. cost_usd = tokens × that rate; the components of a call sum to its
// cost_usd. Unit rates are intentionally not exposed.
type CostComponent struct {
	Kind    string  `json:"kind"` // input | cached_input | cache_creation | output
	Tokens  int64   `json:"tokens"`
	CostUsd float64 `json:"cost_usd"`
}

// CostBreakdown justifies one model call's cost: which tier's rate table applied
// and how the cost splits across token categories. Invariant: the components'
// cost_usd sum to the model call's cost_usd.
type CostBreakdown struct {
	Tier       string          `json:"tier"` // standard | long_context
	Components []CostComponent `json:"components"`
}

// AgentDetailNode is the agent being inspected, with its full execution content.
type AgentDetailNode struct {
	ID                  string    `json:"id"`
	MessageID           string    `json:"message_id"`
	ParentAgentID       string    `json:"parent_agent_id"`
	AgentName           string    `json:"agent_name"`
	Status              string    `json:"status"`
	IsError             bool      `json:"is_error"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	DurationSeconds     float64   `json:"duration_seconds"`
	Query               string    `json:"query"`
	Thought             string    `json:"thought"`
	Response            string    `json:"response"`
	CostUsd             float64   `json:"cost_usd"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	ModelLatencySeconds float64   `json:"model_latency_seconds"`
}

// DetailToolCall is one tool invocation under the agent: what it ran, what came
// back, and whether it succeeded. child_agent_id (when set) links to a sub-agent
// the tool spawned — the UI fetches that agent's detail on a further click.
type DetailToolCall struct {
	ID              string    `json:"id"`
	ToolName        string    `json:"tool_name"`
	ToolID          string    `json:"tool_id"`
	ToolType        string    `json:"tool_type"`
	Status          string    `json:"status"`
	IsError         bool      `json:"is_error"`
	Parameters      string    `json:"parameters"`
	Response        string    `json:"response"`
	Thought         string    `json:"thought"`
	ChildAgentID    string    `json:"child_agent_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	DurationSeconds float64   `json:"duration_seconds"`
}

// DetailModelCall is one billable LLM API call under the agent, with its cost
// justification and error/success detail.
type DetailModelCall struct {
	ID                  string        `json:"id"`
	Model               string        `json:"model"`
	Provider            string        `json:"provider"`
	Status              string        `json:"status"`
	IsError             bool          `json:"is_error"`
	ErrorMessage        string        `json:"error_message"`
	StopReason          string        `json:"stop_reason"`
	IsCacheHit          bool          `json:"is_cache_hit"`
	RetryAttempt        int           `json:"retry_attempt"`
	InputTokens         int64         `json:"input_tokens"`
	OutputTokens        int64         `json:"output_tokens"`
	CachedInputTokens   int64         `json:"cached_input_tokens"`
	CacheCreationTokens int64         `json:"cache_creation_tokens"`
	ThinkingTokens      int64         `json:"thinking_tokens"`
	CostUsd             float64       `json:"cost_usd"`
	CostBreakdown       CostBreakdown `json:"cost_breakdown"`
	LatencySeconds      float64       `json:"latency_seconds"`
	TtftMs              int           `json:"ttft_ms"`
	CreatedAt           time.Time     `json:"created_at"`
	// HasTrace is true when the stored prompt/response trace exists for this call
	// (LLM_TRACE_ENABLED was on). Always returned (cheap) so the UI can show a
	// "view prompt" action without transferring the (large) trace text.
	HasTrace bool `json:"has_trace"`
	// PromptMessages / ResponseContent carry the raw trace and are populated ONLY
	// when the request targets this call by model_call_id (the on-click lazy fetch);
	// omitted from the normal per-agent listing to keep that payload light.
	PromptMessages  string `json:"prompt_messages,omitempty"`
	ResponseContent string `json:"response_content,omitempty"`
}

// AgentDetail is the ai_get_conversation_agent payload: one agent and the flat
// lists of tool calls and model calls it produced (ordered by created_at).
type AgentDetail struct {
	Agent      AgentDetailNode   `json:"agent"`
	ToolCalls  []DetailToolCall  `json:"tool_calls"`
	ModelCalls []DetailModelCall `json:"model_calls"`
}

// CalculateCostBreakdown splits a single model call's cost into its priced token
// components, using the SAME tier-aware effective rates as CalculateTotalCost, so
// the components sum exactly to CalculateTotalCost(...). The output component
// folds in thinking tokens (billed at the output rate), matching the formula.
func CalculateCostBreakdown(p *modelPricing, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int) CostBreakdown {
	if p == nil {
		return CostBreakdown{Tier: "standard", Components: []CostComponent{}}
	}

	totalPrompt := nonCachedInputTokens + cachedInputTokens + cacheCreationTokens
	tier := "standard"
	if p.useLongCtx(totalPrompt) {
		tier = "long_context"
	}

	const million = 1_000_000.0
	inputRate := p.effectiveInputRate(totalPrompt)
	cachedRate := p.effectiveCachedInputRate(totalPrompt)
	creationRate := p.effectiveCacheCreationRate(totalPrompt)
	outputRate := p.effectiveOutputRate(totalPrompt)

	outputBilled := outputTokens + thinkingTokens
	return CostBreakdown{
		Tier: tier,
		Components: []CostComponent{
			{Kind: "input", Tokens: int64(nonCachedInputTokens), CostUsd: float64(nonCachedInputTokens) / million * inputRate},
			{Kind: "cached_input", Tokens: int64(cachedInputTokens), CostUsd: float64(cachedInputTokens) / million * cachedRate},
			{Kind: "cache_creation", Tokens: int64(cacheCreationTokens), CostUsd: float64(cacheCreationTokens) / million * creationRate},
			{Kind: "output", Tokens: int64(outputBilled), CostUsd: float64(outputBilled) / million * outputRate},
		},
	}
}

// agentScopeWhere scopes a detail query to one agent within the session+account.
// $1 = session_id, $2 = account_id, $3 = agent_id. The conversation_id IN (...)
// subquery is the same scope guard the tree uses, so an agent_id from another
// account/session simply returns no rows.
const agentScopeWhere = `agent_id = $3::uuid AND conversation_id IN (
	SELECT id FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid)`

// GetConversationAgentDetail returns one agent's execution content + its tool and
// model calls with cost justification. Returns an empty AgentDetail (no error) if
// the agent does not exist within the caller's session/account scope.
func (chat *ConversationDao) GetConversationAgentDetail(sessionID, accountID, agentID, modelCallID string) (AgentDetail, error) {
	if sessionID == "" || accountID == "" || agentID == "" {
		return AgentDetail{}, fmt.Errorf("GetConversationAgentDetail: session_id, account_id and agent_id are required")
	}

	detail := AgentDetail{
		ToolCalls:  []DetailToolCall{},
		ModelCalls: []DetailModelCall{},
	}
	args := []any{sessionID, accountID, agentID}

	// --- The agent itself (must exist within scope) ---
	var ag struct {
		ID              string       `db:"id"`
		MessageID       string       `db:"message_id"`
		ParentAgentID   string       `db:"parent_agent_id"`
		AgentName       string       `db:"agent_name"`
		Status          string       `db:"status"`
		Query           string       `db:"query"`
		Thought         string       `db:"thought"`
		Response        string       `db:"response"`
		CreatedAt       time.Time    `db:"created_at"`
		UpdatedAt       sql.NullTime `db:"updated_at"`
		DurationSeconds float64      `db:"duration_seconds"`
	}
	agentQuery := `
		SELECT id::text, COALESCE(message_id::text, '') AS message_id,
			COALESCE(parent_agent_id::text, '') AS parent_agent_id,
			COALESCE(agent_name, '') AS agent_name, COALESCE(status, '') AS status,
			COALESCE(query, '') AS query, COALESCE(thought, '') AS thought,
			COALESCE(response, '') AS response,
			created_at, updated_at,
			COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at)), 0) AS duration_seconds
		FROM llm_conversation_agent
		WHERE id = $3::uuid AND conversation_id IN (
			SELECT id FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid)`
	if err := chat.dbManager.Db.Get(&ag, agentQuery, args...); err != nil {
		if err == sql.ErrNoRows {
			return detail, nil // unknown / out-of-scope agent → empty, not an error
		}
		slog.Error("GetConversationAgentDetail: agent query failed", "error", err)
		return AgentDetail{}, fmt.Errorf("GetConversationAgentDetail agent: %w", err)
	}

	node := AgentDetailNode{
		ID:              ag.ID,
		MessageID:       ag.MessageID,
		ParentAgentID:   ag.ParentAgentID,
		AgentName:       ag.AgentName,
		Status:          ag.Status,
		IsError:         isAgentError(ag.Status),
		CreatedAt:       ag.CreatedAt,
		DurationSeconds: ag.DurationSeconds,
		Query:           ag.Query,
		Thought:         ag.Thought,
		Response:        ag.Response,
	}
	if ag.UpdatedAt.Valid {
		node.UpdatedAt = ag.UpdatedAt.Time
	}

	// --- Tool calls under this agent ---
	var toolScans []struct {
		ID              string       `db:"id"`
		ToolName        string       `db:"tool_name"`
		ToolID          string       `db:"tool_id"`
		ToolType        string       `db:"tool_type"`
		Status          string       `db:"status"`
		Parameters      string       `db:"parameters"`
		Response        string       `db:"response"`
		Thought         string       `db:"thought"`
		ChildAgentID    string       `db:"child_agent_id"`
		CreatedAt       time.Time    `db:"created_at"`
		UpdatedAt       sql.NullTime `db:"updated_at"`
		DurationSeconds float64      `db:"duration_seconds"`
	}
	toolQuery := fmt.Sprintf(`
		SELECT id::text,
			COALESCE(tool_name, '') AS tool_name, COALESCE(tool_id, '') AS tool_id,
			COALESCE(tool_type, '') AS tool_type, COALESCE(status, '') AS status,
			COALESCE(parameters, '') AS parameters, COALESCE(response, '') AS response,
			COALESCE(thought, '') AS thought, COALESCE(child_agent_id::text, '') AS child_agent_id,
			created_at, updated_at,
			COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at)), 0) AS duration_seconds
		FROM llm_conversation_tool_calls
		WHERE %s
		ORDER BY created_at`, agentScopeWhere)
	if err := chat.dbManager.Db.Select(&toolScans, toolQuery, args...); err != nil {
		slog.Error("GetConversationAgentDetail: tool_calls query failed", "error", err)
		return AgentDetail{}, fmt.Errorf("GetConversationAgentDetail tool_calls: %w", err)
	}
	for _, t := range toolScans {
		dtc := DetailToolCall{
			ID:              t.ID,
			ToolName:        t.ToolName,
			ToolID:          t.ToolID,
			ToolType:        t.ToolType,
			Status:          t.Status,
			IsError:         isToolError(t.Status),
			Parameters:      t.Parameters,
			Response:        t.Response,
			Thought:         t.Thought,
			ChildAgentID:    t.ChildAgentID,
			CreatedAt:       t.CreatedAt,
			DurationSeconds: t.DurationSeconds,
		}
		if t.UpdatedAt.Valid {
			dtc.UpdatedAt = t.UpdatedAt.Time
		}
		detail.ToolCalls = append(detail.ToolCalls, dtc)
	}

	// --- Model calls under this agent (billable leaves) ---
	var mcScans []struct {
		ID                  string    `db:"id"`
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
		HasTrace            bool      `db:"has_trace"`
		PromptMessages      string    `db:"prompt_messages"`
		ResponseContent     string    `db:"response_content"`
	}
	// has_trace is always cheap (a NULL check). The raw trace text is selected
	// ONLY in lazy mode (model_call_id set), where we also narrow to that one row.
	traceCols := "(prompt_messages IS NOT NULL) AS has_trace, '' AS prompt_messages, '' AS response_content"
	mcWhere := agentScopeWhere
	mcArgs := []any{sessionID, accountID, agentID}
	if modelCallID != "" {
		traceCols = "(prompt_messages IS NOT NULL) AS has_trace, COALESCE(prompt_messages, '') AS prompt_messages, COALESCE(response_content, '') AS response_content"
		mcWhere = agentScopeWhere + " AND id = $4::uuid"
		mcArgs = append(mcArgs, modelCallID)
	}
	mcQuery := fmt.Sprintf(`
		SELECT id::text, llm_model, llm_provider,
			input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
			COALESCE(thinking_tokens, 0) AS thinking_tokens,
			COALESCE(latency_seconds, 0) AS latency_seconds,
			COALESCE(ttft_ms, 0) AS ttft_ms,
			retry_attempt, is_cache_hit, request_status,
			COALESCE(stop_reason, '') AS stop_reason,
			COALESCE(error_message, '') AS error_message,
			created_at,
			%s
		FROM llm_conversation_token_usage
		WHERE %s
		ORDER BY created_at`, traceCols, mcWhere)
	if err := chat.dbManager.Db.Select(&mcScans, mcQuery, mcArgs...); err != nil {
		slog.Error("GetConversationAgentDetail: model_calls query failed", "error", err)
		return AgentDetail{}, fmt.Errorf("GetConversationAgentDetail model_calls: %w", err)
	}

	pricing, err := chat.GetConversationCosts(nil)
	if err != nil {
		return AgentDetail{}, fmt.Errorf("GetConversationAgentDetail pricing: %w", err)
	}

	var agentCost, agentLatency float64
	var agentIn, agentOut int64
	for _, m := range mcScans {
		p := pricing[m.Provider+":"+m.Model]
		nonCached := m.InputTokens - m.CachedInputTokens
		if nonCached < 0 {
			nonCached = 0
		}
		cost := CalculateTotalCost(&p, nonCached, m.CachedInputTokens, m.CacheCreationTokens, m.OutputTokens, m.ThinkingTokens)
		breakdown := CalculateCostBreakdown(&p, nonCached, m.CachedInputTokens, m.CacheCreationTokens, m.OutputTokens, m.ThinkingTokens)

		detail.ModelCalls = append(detail.ModelCalls, DetailModelCall{
			ID:                  m.ID,
			Model:               m.Model,
			Provider:            m.Provider,
			Status:              m.RequestStatus,
			IsError:             isModelError(m.RequestStatus, m.ErrorMessage),
			ErrorMessage:        m.ErrorMessage,
			StopReason:          m.StopReason,
			IsCacheHit:          m.IsCacheHit,
			RetryAttempt:        m.RetryAttempt,
			InputTokens:         int64(m.InputTokens),
			OutputTokens:        int64(m.OutputTokens),
			CachedInputTokens:   int64(m.CachedInputTokens),
			CacheCreationTokens: int64(m.CacheCreationTokens),
			ThinkingTokens:      int64(m.ThinkingTokens),
			CostUsd:             cost,
			CostBreakdown:       breakdown,
			LatencySeconds:      m.LatencySeconds,
			TtftMs:              m.TtftMs,
			CreatedAt:           m.CreatedAt,
			HasTrace:            m.HasTrace,
			PromptMessages:      m.PromptMessages,
			ResponseContent:     m.ResponseContent,
		})

		agentCost += cost
		agentLatency += m.LatencySeconds
		agentIn += int64(m.InputTokens)
		agentOut += int64(m.OutputTokens)
	}

	node.CostUsd = agentCost
	node.InputTokens = agentIn
	node.OutputTokens = agentOut
	node.ModelLatencySeconds = agentLatency
	detail.Agent = node

	return detail, nil
}

// isToolError maps a stored tool status to a simple error flag. Terminal
// non-success states (ERROR / TERMINATED) are errors; SUCCESS and the various
// pending states (WAITING / IN_PROGRESS) are not.
func isToolError(status string) bool {
	switch status {
	case "ERROR", "TERMINATED":
		return true
	default:
		return false
	}
}

// isAgentError maps a stored agent execution status to an error flag.
func isAgentError(status string) bool {
	switch status {
	case "fail", "terminated":
		return true
	default:
		return false
	}
}

// isModelError treats a non-success request_status, or any populated
// error_message, as an error.
func isModelError(requestStatus, errorMessage string) bool {
	if errorMessage != "" {
		return true
	}
	switch requestStatus {
	case "", "success", "SUCCESS", "completed", "COMPLETED":
		return false
	default:
		return true
	}
}

// ConversationAgentDetailRequest selects one agent within a session/account.
// conversation_id is the session_id (historical naming, matching the tree).
type ConversationAgentDetailRequest struct {
	ConversationId string `json:"conversation_id" validate:"required"`
	AccountId      string `json:"account_id" validate:"required"`
	AgentId        string `json:"agent_id" validate:"required"`
	UserId         string `json:"user_id"`
	// ModelCallId, when set, switches to the lazy-trace mode: return only that one
	// model call WITH its prompt_messages/response_content populated. Empty = the
	// normal per-agent listing (trace text omitted, only has_trace flags).
	ModelCallId string `json:"model_call_id"`
}

// HandleConversationAgentDetailApi backs ai_get_conversation_agent — the detail a
// node in ai_get_conversation_tree opens into on click.
func HandleConversationAgentDetailApi(ctx *security.RequestContext, request ConversationAgentDetailRequest) (AgentDetail, error) {
	if request.ConversationId == "" || request.AccountId == "" || request.AgentId == "" {
		return AgentDetail{}, fmt.Errorf("HandleConversationAgentDetailApi: conversation_id, account_id and agent_id are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		return AgentDetail{}, fmt.Errorf("HandleConversationAgentDetailApi: forbidden account_id")
	}
	return GetConversationDao().GetConversationAgentDetail(request.ConversationId, request.AccountId, request.AgentId, request.ModelCallId)
}
