package analytics

import (
	"database/sql"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	llmruntime "nudgebee/llm/llms/runtime"
)

type TokenMetrics struct {
	AgentId              string          `json:"id"`
	AgentName            string          `json:"agent_name"`              // Human-readable agent name
	InputTokens          int             `json:"input_tokens"`            // Fresh input + cache reads. Cache writes are NOT included for Anthropic/Bedrock (see PromptTokens)
	OutputTokens         int             `json:"output_tokens"`           // Total output
	CachedInputTokens    int             `json:"cached_input_tokens"`     // Tokens served from cache
	NonCachedInputTokens int             `json:"non_cached_input_tokens"` // New input tokens (full input rate)
	CacheCreationTokens  int             `json:"cache_creation_tokens"`   // Tokens written to cache (Anthropic/Vertex)
	ThinkingTokens       int             `json:"thinking_tokens"`         // Hidden chain-of-thought (Gemini 2.5+)
	CacheHitRate         sql.NullFloat64 `json:"cache_hit_rate"`          // Cache hit rate percentage (0-100)
	PromptTokens         int             `json:"-"`                       // Whole prompt sent (input + cache writes for providers that keep writes outside input); hit-rate denominator
	Cost                 float64         `json:"cost"`
	ModelProviderName    sql.NullString  `json:"model_provider_name"`
	ModelName            sql.NullString  `json:"model_name"`
	ModelTier            string          `json:"model_tier"` // reasoning / retrieval / summary; empty when the call was not tier-tagged
	LLMConfigSource      string          `json:"llm_config_source"`
	Requests             int             `json:"requests"`          // calls in this group
	FailedRequests       int             `json:"failed_requests"`   // of which failed — a failure spends no tokens, so 0/0 rows are real work, not noise
	UnpricedRequests     int             `json:"unpriced_requests"` // calls that spent tokens on a model with no llm_model_pricing row
	MessageId            string          `json:"message_id"`
	ConversationId       string          `json:"conversation_id"`
}

func (chat *Store) fetchModelPricing(tenantId string) (map[string]llmruntime.ModelPricing, error) {
	return llmruntime.NewUsageStore(chat.dbManager).GetConversationCosts(nil, tenantId)
}

func (chat *Store) GetConversationTokenUsage(conversationId, accountId string) ([]TokenMetrics, error) {

	// Fetch Pricing Map upfront to avoid N+1 queries
	pricingMap, err := chat.fetchModelPricing(llmruntime.TenantForPricing(accountId))
	// A failed lookup says nothing about whether a price exists, so no call is flagged unpriced.
	pricingLoaded := err == nil
	if err != nil {
		slog.Error("Failed to fetch model pricing", "error", err)
		// Proceed with empty map, costs will be 0
		pricingMap = make(map[string]llmruntime.ModelPricing)
	}

	// Query the new llm_conversation_token_usage table at its native
	// per-API-call granularity (multiple rows per agent). Pricing is tiered
	// on each call's own prompt size (see CalculateTotalCost), so cost MUST
	// be computed per row before summing — SUMing tokens across many calls
	// first and pricing the aggregate once would push a large conversation's
	// combined volume over the long-ctx threshold even when no single call
	// did, wildly inflating cost. Group by agent_name instead of agent_id to
	// avoid duplicate agent names in UI when the same agent executes
	// multiple times (retries, fallbacks, multiple LLM calls).
	// NOTE: AgentId field removed from query - UI should use AgentName as identifier
	query := `
		SELECT
			COALESCE(t.agent_name, '') as AgentName,
			t.input_tokens as InputTokens,
			t.output_tokens as OutputTokens,
			COALESCE(t.cached_input_tokens, 0) as CachedInputTokens,
			COALESCE(t.cache_creation_tokens, 0) as CacheCreationTokens,
			COALESCE(t.thinking_tokens, 0) as ThinkingTokens,
			t.llm_model as ModelName,
			t.llm_provider as ModelProviderName,
			COALESCE(t.model_tier, '') as ModelTier,
			COALESCE(t.llm_config_source, '') as LLMConfigSource,
			COALESCE(t.request_status, '') as RequestStatus,
			COALESCE(t.message_id::text, '') as MessageId,
			t.conversation_id::text as ConversationId,
			t.cost_usd as CostUsd
		FROM llm_conversation_token_usage t
		INNER JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE c.session_id = $1 AND c.account_id = $2::uuid;`

	rows, err := chat.dbManager.Db.Queryx(query, conversationId, accountId)

	if err != nil {
		slog.Error("executing query", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	type rawCall struct {
		AgentName           string
		InputTokens         int
		OutputTokens        int
		CachedInputTokens   int
		CacheCreationTokens int
		ThinkingTokens      int
		ModelName           sql.NullString
		ModelProviderName   sql.NullString
		ModelTier           string
		LLMConfigSource     string
		RequestStatus       string
		MessageId           string
		ConversationId      string
		CostUsd             sql.NullFloat64
	}

	// A comparable struct rather than a delimiter-joined string: model names reach
	// us from provider APIs, so a separator appearing in one would silently merge
	// two distinct groups.
	type groupKeyT struct {
		AgentName, ModelName, ModelProviderName, ModelTier, LLMConfigSource, MessageId, ConversationId string
	}
	groups := make(map[groupKeyT]*TokenMetrics)
	var order []groupKeyT
	for rows.Next() {
		var call rawCall
		if err := rows.StructScan(&call); err != nil {
			slog.Error("Scan error", "error", err)
			return nil, err
		}

		nonCached := call.InputTokens - call.CachedInputTokens
		if nonCached < 0 {
			nonCached = 0
		}

		// Prefer the cost persisted at insert time (reconciles with every other
		// cost surface — see cost_usd column comment). Legacy rows written before
		// that column existed, or calls with no matching pricing entry at insert
		// time, fall back to a live recompute here.
		var cost float64
		priced := call.CostUsd.Valid
		if call.CostUsd.Valid {
			cost = call.CostUsd.Float64
		} else if call.ModelProviderName.Valid && call.ModelName.Valid {
			key := call.ModelProviderName.String + ":" + call.ModelName.String
			if pricing, ok := pricingMap[key]; ok {
				priced = true
				cost = llmruntime.CalculateTotalCost(
					&pricing,
					nonCached,
					call.CachedInputTokens,
					call.CacheCreationTokens,
					call.OutputTokens,
					call.ThinkingTokens,
				)
			}
		}

		groupKey := groupKeyT{
			AgentName:         call.AgentName,
			ModelName:         call.ModelName.String,
			ModelProviderName: call.ModelProviderName.String,
			ModelTier:         call.ModelTier,
			LLMConfigSource:   call.LLMConfigSource,
			MessageId:         call.MessageId,
			ConversationId:    call.ConversationId,
		}
		stat := groups[groupKey]
		if stat == nil {
			stat = &TokenMetrics{
				AgentName:         call.AgentName,
				ModelName:         call.ModelName,
				ModelProviderName: call.ModelProviderName,
				ModelTier:         call.ModelTier,
				LLMConfigSource:   call.LLMConfigSource,
				MessageId:         call.MessageId,
				ConversationId:    call.ConversationId,
			}
			groups[groupKey] = stat
			order = append(order, groupKey)
		}
		stat.Requests++
		if call.RequestStatus == "failure" {
			stat.FailedRequests++
		}
		stat.InputTokens += call.InputTokens
		stat.OutputTokens += call.OutputTokens
		stat.CachedInputTokens += call.CachedInputTokens
		stat.NonCachedInputTokens += nonCached
		stat.CacheCreationTokens += call.CacheCreationTokens
		stat.PromptTokens += llmruntime.PromptTokens(call.ModelProviderName.String, call.InputTokens, call.CacheCreationTokens)
		stat.ThinkingTokens += call.ThinkingTokens
		stat.Cost += cost
		if pricingLoaded && !priced && call.InputTokens+call.OutputTokens > 0 {
			stat.UnpricedRequests++
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("rows iteration error", "error", err)
		return nil, err
	}

	agents := make([]TokenMetrics, len(order))
	for i, key := range order {
		stat := groups[key]
		if stat.PromptTokens > 0 {
			rate := (float64(stat.CachedInputTokens) / float64(stat.PromptTokens)) * 100
			stat.CacheHitRate = sql.NullFloat64{Float64: rate, Valid: true}
		}
		agents[i] = *stat
	}
	return agents, nil
}

// TokenUsageDetailedRecord represents a single API call record with all details
type TokenUsageDetailedRecord struct {
	ConversationID      string
	MessageID           string
	AgentID             sql.NullString
	AgentName           string
	LLMProvider         string
	LLMModel            string
	InputTokens         int
	OutputTokens        int
	CachedInputTokens   int
	CacheCreationTokens int
	ThinkingTokens      int
	TTFTMs              sql.NullInt64
	ITLMsAvg            sql.NullFloat64
	RequestStatus       string
	LatencySeconds      sql.NullFloat64
	CreatedAt           time.Time
	// Cost persisted at insert time. NULL on legacy rows (and on calls with no
	// pricing entry then), which fall back to a live recompute — same preference
	// as GetConversationTokenUsage and usage_analytics.perCallCostExpr.
	CostUsd sql.NullFloat64
}

// GetConversationTokenUsageDetailed returns all individual token usage records for detailed analysis
func (chat *Store) GetConversationTokenUsageDetailed(conversationId, accountId string) ([]TokenUsageDetailedRecord, error) {
	query := `
		SELECT
			t.conversation_id::text as ConversationID,
			COALESCE(t.message_id::text, '') as MessageID,
			t.agent_id::text as AgentID,
			t.agent_name as AgentName,
			t.llm_provider as LLMProvider,
			t.llm_model as LLMModel,
			t.input_tokens as InputTokens,
			t.output_tokens as OutputTokens,
			COALESCE(t.cached_input_tokens, 0) as CachedInputTokens,
			COALESCE(t.cache_creation_tokens, 0) as CacheCreationTokens,
			COALESCE(t.thinking_tokens, 0) as ThinkingTokens,
			t.request_status as RequestStatus,
			t.latency_seconds as LatencySeconds,
			t.ttft_ms as TTFTMs,
			t.itl_ms_avg as ITLMsAvg,
			t.created_at as CreatedAt,
			t.cost_usd as CostUsd
		FROM llm_conversation_token_usage t
		INNER JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE c.session_id = $1 AND c.account_id = $2::uuid
		ORDER BY t.created_at ASC;`

	rows, err := chat.dbManager.Db.Queryx(query, conversationId, accountId)
	if err != nil {
		slog.Error("executing detailed token usage query", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	records := []TokenUsageDetailedRecord{}
	for rows.Next() {
		var record TokenUsageDetailedRecord
		err := rows.StructScan(&record)
		if err != nil {
			slog.Error("Scan error", "error", err)
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		slog.Error("detailed token usage rows iteration error", "error", err)
		return nil, err
	}
	return records, nil
}

// ToolCallAgent links a tool call to the agent that issued it and when it ran, so
// each agent's reasoning calls can be time-split onto the specific tool call they
// produced. No db tags — sqlx's default mapper lowercases field names to match the
// lowercased column aliases, matching the other detailed-record queries in this file.
type ToolCallAgent struct {
	ToolCallID string
	AgentID    string
	CreatedAt  time.Time
}

// ResolveSessionId normalizes a usage-metrics lookup key. The metrics queries all match on
// session_id, but callers may pass a conversation id instead (e.g. workflow/autopilot
// conversations are opened by conversation_id, which differs from their wf__… session_id).
// If the value matches a conversation's id, that conversation's session_id is returned;
// otherwise the value is assumed to already be a session_id and returned unchanged.
func (chat *Store) ResolveSessionId(idOrSessionId string) string {
	// Only a conversation id is a UUID; a non-UUID value (e.g. a wf__… workflow session id)
	// can never match a conversation id, so skip the query entirely — saving a round-trip.
	// Matching on the parsed uuid (not id::text) keeps the primary-key index in play.
	id, err := uuid.Parse(idOrSessionId)
	if err != nil {
		return idOrSessionId
	}
	var sessionId string
	if err := chat.dbManager.Db.Get(&sessionId,
		`SELECT session_id FROM llm_conversations WHERE id = $1 LIMIT 1`, id); err != nil || sessionId == "" {
		return idOrSessionId
	}
	return sessionId
}

// GetConversationToolCallAgents returns the tool_call_id, agent_id and created_at for
// each tool call in a conversation (looked up by session_id, matching the other
// usage-metric queries), ordered chronologically for time-based reasoning attribution.
func (chat *Store) GetConversationToolCallAgents(conversationId, accountId string) ([]ToolCallAgent, error) {
	query := `
		SELECT tc.id::text as ToolCallID, tc.agent_id::text as AgentID, tc.created_at as CreatedAt
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		WHERE c.session_id = $1 AND c.account_id = $2::uuid AND tc.agent_id IS NOT NULL
		  AND tc.metadata->>'parent_tool_call_id' IS NULL
		ORDER BY tc.created_at ASC;`

	rows, err := chat.dbManager.Db.Queryx(query, conversationId, accountId)
	if err != nil {
		slog.Error("executing tool call agents query", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	pairs := []ToolCallAgent{}
	for rows.Next() {
		var pair ToolCallAgent
		if err := rows.StructScan(&pair); err != nil {
			slog.Error("Scan error", "error", err)
			return nil, err
		}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		slog.Error("tool call agents rows iteration error", "error", err)
		return nil, err
	}
	return pairs, nil
}

// ToolCallsStats represents tool call statistics for a conversation
type ToolCallsStats struct {
	TotalToolCalls      int
	SuccessfulToolCalls int
	FailedToolCalls     int
	InProgressToolCalls int
}

type TimeBreakdown struct {
	WallTimeSeconds        float64
	AgentActiveTimeSeconds float64
	ToolTimeSeconds        float64
}

// MaxMeasuredDurationSeconds caps any single wall / agent / tool duration in
// the time rollup. These tables record no end-of-work timestamp, so a duration
// is (updated_at - created_at) — row mtime, which anything touching the row
// later re-stamps. Uncapped, one such row buries the sum: on dev the median
// root-agent duration is 42 seconds while a single re-touched row measured
// 4,460 hours. One hour is far above any real investigation and far below the
// re-stamp artifacts, so it discards the artifacts without truncating real work.
const MaxMeasuredDurationSeconds = 3600

// escapeLikePattern neutralises the LIKE metacharacters in a literal prefix so
// it matches only itself. Postgres LIKE uses backslash as the default escape
// character, so the backslash has to be escaped first.
func escapeLikePattern(literal string) string {
	replaced := strings.ReplaceAll(literal, `\`, `\\`)
	replaced = strings.ReplaceAll(replaced, "%", `\%`)
	return strings.ReplaceAll(replaced, "_", `\_`)
}

// ConversationTimeAggregatesFilter selects which conversations to roll up.
// AccountIDs scopes the rollup to the caller's accessible accounts (matches
// RPC RLS behavior for the existing GraphQL widgets); empty means "no
// accessible accounts" and short-circuits to a zero result. Empty Sources
// means "any source"; EventScoped narrows to conversations whose title
// contains a UUID, matching the auto-investigation tab in the UI.
// ExcludedTitles filters out infrastructure conversations that never represent
// real work, by exact title. ExcludedTitlePrefixes does the same for the ones
// that carry the event id in the title, so no exact string can match them —
// "Get the details of Event with id - <uuid>" is both infrastructure AND a
// UUID-bearing title, so without a prefix filter it passes EventScoped and gets
// counted as an investigation.
type ConversationTimeAggregatesFilter struct {
	AccountIDs            []string
	StartDate             time.Time
	EndDate               time.Time
	Sources               []string
	ExcludedTitles        []string
	ExcludedTitlePrefixes []string
	EventScoped           bool
}

// ConversationTimeAggregates rolls up TimeBreakdown numbers across many
// conversations for dashboard widgets. CompletedCount drives per-investigation
// averaging; TotalCount is the unfiltered total in the same window so the UI
// can show progress vs. completion separately.
type ConversationTimeAggregates struct {
	CompletedCount              int     `json:"completed_count"`
	TotalCount                  int     `json:"total_count"`
	TotalWallTimeSeconds        float64 `json:"total_wall_time_seconds"`
	TotalAgentActiveTimeSeconds float64 `json:"total_agent_active_time_seconds"`
	TotalToolTimeSeconds        float64 `json:"total_tool_time_seconds"`
}

// GetConversationLifecycleStorageCost returns the total per-hour storage cost
// in USD accrued by conversation-scoped caches for the given session.
// Account/tenant/global-scoped caches are intentionally excluded — their
// storage cost belongs to the account/tenant budget rollup, not to any
// individual conversation. Storage time is bounded by invalidated_at if set,
// else min(now, expires_at) — matches the formula used by budget/usage.go.
//
// tenantId is required for defense-in-depth: session_id is unique per
// conversation but a tenant filter ensures cross-tenant isolation even if
// a session_id collision were ever to occur.
func (chat *Store) GetConversationLifecycleStorageCost(conversationId string, tenantId string) (float64, error) {
	if strings.TrimSpace(tenantId) == "" {
		return 0, fmt.Errorf("GetConversationLifecycleStorageCost: tenantId is required")
	}
	query := `
		SELECT COALESCE(SUM(
			(cl.cached_tokens / 1000000.0)
			* COALESCE(p.cost_per_million_cached_storage_per_hour, 0)
			* GREATEST(0, EXTRACT(EPOCH FROM (
				COALESCE(cl.invalidated_at, LEAST(now(), cl.expires_at)) - cl.created_at
			))) / 3600
		), 0) AS storage_cost
		FROM llm_cache_lifecycle cl
		INNER JOIN llm_conversations c ON c.id = cl.conversation_id
		LEFT JOIN llm_model_pricing p
			ON p.model_name = cl.llm_model AND p.provider_name = cl.llm_provider AND p.tenant_id IS NULL
		WHERE c.session_id = $1
		  AND c.tenant_id = $2
		  AND cl.scope = 'conversation';`

	var cost sql.NullFloat64
	if err := chat.dbManager.Db.Get(&cost, query, conversationId, tenantId); err != nil {
		return 0, fmt.Errorf("GetConversationLifecycleStorageCost: %w", err)
	}
	if !cost.Valid {
		return 0, nil
	}
	return cost.Float64, nil
}

// GetConversationToolCallsStats returns tool call statistics for a conversation
func (chat *Store) GetConversationToolCallsStats(conversationId, accountId string) (ToolCallsStats, error) {
	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE tc.status = 'success') as successful,
			COUNT(*) FILTER (WHERE tc.status = 'fail') as failed,
			COUNT(*) FILTER (WHERE tc.status = 'in_progress') as in_progress
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		WHERE c.session_id = $1 AND c.account_id = $2::uuid AND tc.metadata->>'parent_tool_call_id' IS NULL;`

	var stats struct {
		Total      int `db:"total"`
		Successful int `db:"successful"`
		Failed     int `db:"failed"`
		InProgress int `db:"in_progress"`
	}

	err := chat.dbManager.Db.Get(&stats, query, conversationId, accountId)
	if err != nil {
		slog.Error("getting tool calls stats", "error", err)
		return ToolCallsStats{}, err
	}

	return ToolCallsStats{
		TotalToolCalls:      stats.Total,
		SuccessfulToolCalls: stats.Successful,
		FailedToolCalls:     stats.Failed,
		InProgressToolCalls: stats.InProgress,
	}, nil
}

// GetConversationTimeBreakdown calculates time breakdown for a conversation
func (chat *Store) GetConversationTimeBreakdown(conversationId, accountId string) (TimeBreakdown, error) {
	query := `
		WITH conversation_ids AS (
			SELECT id FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid
		),
		-- Message activity per conversation with long idle gaps removed (see
		-- ConversationWallClockSQL), not the row's (updated_at - created_at):
		-- updated_at is re-stamped by anything that touches the row later. Same
		-- definition the conversation list uses. Falls back to the row timestamps
		-- only when a conversation has no messages.
		wall_time AS (
			SELECT COALESCE(SUM(COALESCE(__WALL_CLOCK_SECONDS__, EXTRACT(EPOCH FROM (c.updated_at - c.created_at)))), 0) as seconds
			FROM llm_conversations c
			WHERE c.session_id = $1 AND c.account_id = $2::uuid
		),
		agent_time AS (
			SELECT
				COALESCE(SUM(EXTRACT(EPOCH FROM (updated_at - created_at))), 0) as seconds
			FROM llm_conversation_agent
			WHERE conversation_id IN (SELECT id FROM conversation_ids)
				AND updated_at IS NOT NULL
				AND created_at IS NOT NULL
				AND (parent_agent_id IS NULL OR parent_agent_id = '00000000-0000-0000-0000-000000000000')
		),
		-- Union of tool-call intervals, not their sum: an agent-type call encloses
		-- the tool-type calls its sub-agent makes, and parallel calls overlap, so
		-- summing double-counts. The containment is only visible in the timestamps
		-- (parent_tool_call_id is NULL on both sides), hence a gaps-and-islands merge
		-- rather than a structural filter.
		tool_spans AS (
			SELECT created_at, updated_at,
				CASE WHEN created_at > MAX(updated_at) OVER (
						ORDER BY created_at, updated_at
						ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)
					THEN 1 ELSE 0 END as is_new_span
			FROM llm_conversation_tool_calls
			WHERE conversation_id IN (SELECT id FROM conversation_ids)
				AND updated_at IS NOT NULL
				AND created_at IS NOT NULL
		),
		tool_islands AS (
			SELECT created_at, updated_at,
				SUM(is_new_span) OVER (ORDER BY created_at, updated_at) as island
			FROM tool_spans
		),
		tool_time AS (
			SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (span_end - span_start))), 0) as seconds
			FROM (
				SELECT MIN(created_at) as span_start, MAX(updated_at) as span_end
				FROM tool_islands GROUP BY island
			) merged
		)
		SELECT
			COALESCE((SELECT seconds FROM wall_time), 0) as wall_time,
			COALESCE((SELECT seconds FROM agent_time), 0) as agent_time,
			COALESCE((SELECT seconds FROM tool_time), 0) as tool_time;`

	var result struct {
		WallTime  float64 `db:"wall_time"`
		AgentTime float64 `db:"agent_time"`
		ToolTime  float64 `db:"tool_time"`
	}

	query = strings.Replace(query, "__WALL_CLOCK_SECONDS__", core.ConversationWallClockSQL("c.id"), 1)
	err := chat.dbManager.Db.Get(&result, query, conversationId, accountId)
	if err != nil {
		slog.Error("getting conversation time breakdown", "error", err)
		return TimeBreakdown{}, err
	}

	return TimeBreakdown{
		WallTimeSeconds:        result.WallTime,
		AgentActiveTimeSeconds: result.AgentTime,
		ToolTimeSeconds:        result.ToolTime,
	}, nil
}

// GetConversationTimeAggregates rolls up wall / agent-active / tool time over a
// date range across one or more accounts. Designed for dashboard widgets that
// previously computed (updated_at - created_at) averages on the frontend —
// moving the math here gives a single source of truth so future refinements
// (excluding user-input wait, complexity-aware baselines) only land in one
// place.
//
// The window is on created_at — when the work STARTED — not updated_at.
// updated_at is row mtime: a cleanup job, a resume, or a status backfill
// re-stamps it, which sweeps months-old conversations into a seven-day window
// and drags their equally stale durations in with them. On dev one conversation
// created in February and touched in August contributed 4,460 hours to a 7-day
// rollup, 98% of the total, which drove the frontend's time-saved widget
// negative. created_at cannot be re-stamped, so the population is stable.
//
// For the same reason every per-row duration below is capped at
// MaxMeasuredDurationSeconds: the only end-of-work timestamp these tables carry
// is updated_at, so a single re-touched row would otherwise dominate a SUM.
//
// All three time totals (wall / agent / tool) are scoped to COMPLETED
// conversations so that any future caller doing `time / completed_count`
// gets a true average over finished work — never inflated by in-progress
// rows. Use TotalCount when you need the raw window size including
// in-progress conversations.
func (chat *Store) GetConversationTimeAggregates(filter ConversationTimeAggregatesFilter) (ConversationTimeAggregates, error) {
	if len(filter.AccountIDs) == 0 {
		// No accessible accounts means no rows; return zero rather than running
		// an unbounded query.
		return ConversationTimeAggregates{}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return ConversationTimeAggregates{}, fmt.Errorf("GetConversationTimeAggregates: end_date must be >= start_date")
	}

	args := []any{pq.Array(filter.AccountIDs), filter.StartDate, filter.EndDate}
	argCounter := 4

	sourceClause := ""
	if len(filter.Sources) > 0 {
		sourceClause = fmt.Sprintf(" AND c.source = ANY($%d)", argCounter)
		args = append(args, pq.Array(filter.Sources))
		argCounter++
	}

	excludedTitleClause := ""
	if len(filter.ExcludedTitles) > 0 {
		excludedTitleClause = fmt.Sprintf(" AND (c.title IS NULL OR c.title <> ALL($%d))", argCounter)
		args = append(args, pq.Array(filter.ExcludedTitles))
		argCounter++
	}

	// LIKE patterns are built here rather than taken from the caller so a
	// prefix containing % or _ cannot silently widen the exclusion.
	if len(filter.ExcludedTitlePrefixes) > 0 {
		patterns := make([]string, 0, len(filter.ExcludedTitlePrefixes))
		for _, prefix := range filter.ExcludedTitlePrefixes {
			patterns = append(patterns, escapeLikePattern(prefix)+"%")
		}
		excludedTitleClause += fmt.Sprintf(" AND (c.title IS NULL OR c.title NOT LIKE ALL($%d))", argCounter)
		args = append(args, pq.Array(patterns))
	}

	eventScopedClause := ""
	if filter.EventScoped {
		eventScopedClause = ` AND c.title ~ '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'`
	}

	// All three time CTEs scope to COMPLETED conversations to keep the
	// numerator/denominator semantics consistent: any caller doing
	// `wall|agent|tool / completed_count` gets a true average over finished
	// work rather than one inflated by in-progress rows.
	//
	// account_id is cast to `uuid[]` on the parameter side (not the column
	// side) so the existing index on llm_conversations.account_id can serve
	// the lookup.
	query := fmt.Sprintf(`
		WITH filtered_conversations AS (
			SELECT c.id, c.status, c.created_at, c.updated_at
			FROM llm_conversations c
			WHERE c.account_id = ANY($1::uuid[])
				AND c.created_at >= $2
				AND c.created_at <= $3
				AND EXISTS (
					SELECT 1 FROM llm_conversation_messages m
					WHERE m.conversation_id = c.id
						AND m.message_type = 'generation'
						AND m.role = 'human'
				)%s%s%s
		),
		completed_conversations AS (
			SELECT id, created_at, updated_at
			FROM filtered_conversations
			WHERE status = 'COMPLETED'
		),
		wall_time AS (
			SELECT COALESCE(SUM(LEAST(EXTRACT(EPOCH FROM (updated_at - created_at)), %[4]d)), 0) AS seconds
			FROM completed_conversations
		),
		agent_time AS (
			SELECT COALESCE(SUM(LEAST(EXTRACT(EPOCH FROM (a.updated_at - a.created_at)), %[4]d)), 0) AS seconds
			FROM llm_conversation_agent a
			WHERE a.conversation_id IN (SELECT id FROM completed_conversations)
				AND a.updated_at IS NOT NULL
				AND a.created_at IS NOT NULL
				AND (a.parent_agent_id IS NULL OR a.parent_agent_id = '00000000-0000-0000-0000-000000000000')
		),
		tool_time AS (
			SELECT COALESCE(SUM(LEAST(EXTRACT(EPOCH FROM (tc.updated_at - tc.created_at)), %[4]d)), 0) AS seconds
			FROM llm_conversation_tool_calls tc
			WHERE tc.conversation_id IN (SELECT id FROM completed_conversations)
				AND tc.metadata->>'parent_tool_call_id' IS NULL
				AND tc.tool_type = 'tool'
				AND tc.updated_at IS NOT NULL
				AND tc.created_at IS NOT NULL
		),
		counts AS (
			SELECT
				(SELECT COUNT(*) FROM completed_conversations) AS completed_count,
				(SELECT COUNT(*) FROM filtered_conversations) AS total_count
		)
		SELECT
			(SELECT completed_count FROM counts) AS completed_count,
			(SELECT total_count FROM counts) AS total_count,
			(SELECT seconds FROM wall_time) AS wall_time,
			(SELECT seconds FROM agent_time) AS agent_time,
			(SELECT seconds FROM tool_time) AS tool_time;`,
		sourceClause, excludedTitleClause, eventScopedClause, MaxMeasuredDurationSeconds)

	var result struct {
		CompletedCount int     `db:"completed_count"`
		TotalCount     int     `db:"total_count"`
		WallTime       float64 `db:"wall_time"`
		AgentTime      float64 `db:"agent_time"`
		ToolTime       float64 `db:"tool_time"`
	}

	if err := chat.dbManager.Db.Get(&result, query, args...); err != nil {
		slog.Error("GetConversationTimeAggregates: query failed", "error", err, "account_ids", filter.AccountIDs)
		return ConversationTimeAggregates{}, fmt.Errorf("GetConversationTimeAggregates: %w", err)
	}

	return ConversationTimeAggregates{
		CompletedCount:              result.CompletedCount,
		TotalCount:                  result.TotalCount,
		TotalWallTimeSeconds:        result.WallTime,
		TotalAgentActiveTimeSeconds: result.AgentTime,
		TotalToolTimeSeconds:        result.ToolTime,
	}, nil
}
