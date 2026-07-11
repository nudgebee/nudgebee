package core

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/security"

	"github.com/lib/pq"
)

// tool_usage.go backs ai_aggregate_tool_usage — the Cost Analyser "Tools" tab: a
// per-tool leaderboard aggregated ACROSS conversations from
// llm_conversation_tool_calls. Grain is the tool NAME (one row per distinct tool),
// reporting usage (calls), reliability (success / error counts + error rate),
// latency (avg / p90 / max duration), and reach (distinct agents / conversations).
//
// Tool calls carry NO LLM token cost — the metadata JSONB holds only
// exit_status / execution_duration_ms / stderr / truncation (see
// tools/core.NBToolResponseMetadata). The only cost signal is INDIRECT: a
// sub-agent-spawn tool (tool_type='agent', child_agent_id set) triggers a child
// agent whose model calls DO cost. "Downstream LLM cost" attributes that via
// child_agent_id -> llm_conversation_token_usage, reusing perCallCostExpr so the
// figures reconcile with every other analyser screen. Plain tools (kubectl/shell)
// genuinely have no cost and report 0.
//
// The operational aggregates and the downstream cost are two independent scans
// (joining token_usage into the operational query would fan rows out and corrupt
// the call / duration aggregates); they're merged by tool name in Go, then ranked.

const (
	defaultToolUsageLimit = 50
	maxToolUsageLimit     = 200
)

// toolSortKeys whitelists the metric the leaderboard ranks by. Ordering happens in
// Go after the operational + downstream-cost scans are merged; membership-checking
// the caller's input here keeps the contract explicit (unknown ⇒ "calls").
var toolSortKeys = map[string]bool{
	"calls":    true,
	"errors":   true,
	"duration": true, // p90 duration
	"cost":     true, // downstream LLM cost
}

// toolDurationExpr is one tool call's wall-clock duration in seconds: prefer the
// precise execution_duration_ms recorded in metadata (V751+), falling back to
// updated_at − created_at for rows written before it / tools that don't record it.
// GREATEST(…, 0) clamps the clock-skew / NULL-updated_at cases to 0.
const toolDurationExpr = `COALESCE(
		NULLIF((tc.metadata->>'execution_duration_ms')::float8, 0) / 1000.0,
		GREATEST(EXTRACT(EPOCH FROM (tc.updated_at - tc.created_at)), 0)
	)`

// ToolUsageRow is one tool's rolled-up stats across the filtered window.
type ToolUsageRow struct {
	ToolName              string  `json:"tool_name"`
	ToolType              string  `json:"tool_type"`
	Calls                 int     `json:"calls"`
	SuccessCount          int     `json:"success_count"`
	ErrorCount            int     `json:"error_count"`
	InProgressCount       int     `json:"in_progress_count"`
	ErrorRatePct          float64 `json:"error_rate_pct"`
	AvgDurationSeconds    float64 `json:"avg_duration_seconds"`
	P90DurationSeconds    float64 `json:"p90_duration_seconds"`
	MaxDurationSeconds    float64 `json:"max_duration_seconds"`
	DistinctAgents        int     `json:"distinct_agents"`
	DistinctConversations int     `json:"distinct_conversations"`
	// DownstreamCostUsd is the LLM cost of sub-agents this tool spawned
	// (child_agent_id → token_usage). 0 for non-spawn tools.
	DownstreamCostUsd  float64 `json:"downstream_cost_usd"`
	DownstreamLLMCalls int     `json:"downstream_llm_calls"`
}

// ToolUsageList is the Tools-tab payload: the resolved sort + limit and the ranked rows.
type ToolUsageList struct {
	SortBy string         `json:"sort_by"`
	Limit  int            `json:"limit"`
	Rows   []ToolUsageRow `json:"rows"`
}

type toolUsageScan struct {
	ToolName              string  `db:"tool_name"`
	ToolType              string  `db:"tool_type"`
	Calls                 int     `db:"calls"`
	SuccessCount          int     `db:"success_count"`
	ErrorCount            int     `db:"error_count"`
	InProgressCount       int     `db:"in_progress_count"`
	AvgDurationSeconds    float64 `db:"avg_duration_seconds"`
	P90DurationSeconds    float64 `db:"p90_duration_seconds"`
	MaxDurationSeconds    float64 `db:"max_duration_seconds"`
	DistinctAgents        int     `db:"distinct_agents"`
	DistinctConversations int     `db:"distinct_conversations"`
}

type toolCostScan struct {
	ToolName           string  `db:"tool_name"`
	DownstreamCostUsd  float64 `db:"downstream_cost_usd"`
	DownstreamLLMCalls int     `db:"downstream_llm_calls"`
}

// ListToolUsage returns the per-tool usage/reliability/latency leaderboard for the
// filter, with downstream LLM cost attributed to sub-agent-spawn tools, ranked by
// the chosen metric and capped at limit.
func (chat *ConversationDao) ListToolUsage(filter UsageMetricsFilter, sortBy string, limit int) (ToolUsageList, error) {
	if len(filter.AccountIDs) == 0 {
		return ToolUsageList{Rows: []ToolUsageRow{}}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return ToolUsageList{}, fmt.Errorf("ListToolUsage: end_date must be >= start_date")
	}
	if !toolSortKeys[sortBy] {
		sortBy = "calls"
	}
	if limit <= 0 {
		limit = defaultToolUsageLimit
	}
	if limit > maxToolUsageLimit {
		limit = maxToolUsageLimit
	}

	where, args := filter.buildToolWhere()

	// Operational aggregates: one row per tool name. status values are lowercased on
	// write (success / fail / error / in_progress / waiting / waiting_for_client /
	// terminated); error = a settled failure, in-progress = not yet settled.
	opQuery := fmt.Sprintf(`
		SELECT
			tc.tool_name                                                                   AS tool_name,
			COALESCE(MAX(NULLIF(tc.tool_type, '')), '')                                    AS tool_type,
			COUNT(*)                                                                       AS calls,
			COUNT(*) FILTER (WHERE lower(tc.status) = 'success')                           AS success_count,
			COUNT(*) FILTER (WHERE lower(tc.status) IN ('fail', 'error', 'terminated'))    AS error_count,
			COUNT(*) FILTER (WHERE lower(tc.status) IN ('in_progress', 'waiting', 'waiting_for_client')) AS in_progress_count,
			COALESCE(AVG(d.duration_seconds), 0)                                           AS avg_duration_seconds,
			COALESCE(percentile_cont(0.9) WITHIN GROUP (ORDER BY d.duration_seconds), 0)   AS p90_duration_seconds,
			COALESCE(MAX(d.duration_seconds), 0)                                           AS max_duration_seconds,
			COUNT(DISTINCT tc.agent_id)                                                    AS distinct_agents,
			COUNT(DISTINCT tc.conversation_id)                                             AS distinct_conversations
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		CROSS JOIN LATERAL (SELECT %s AS duration_seconds) d
		WHERE %s AND NULLIF(tc.tool_name, '') IS NOT NULL
		GROUP BY tc.tool_name`, toolDurationExpr, where)

	var opScans []toolUsageScan
	if err := chat.dbManager.Db.Select(&opScans, opQuery, args...); err != nil {
		slog.Error("ListToolUsage: operational query failed", "error", err)
		return ToolUsageList{}, fmt.Errorf("ListToolUsage: %w", err)
	}

	// Downstream LLM cost: for sub-agent-spawn tools only (child_agent_id set),
	// sum the spawned child agent's model-call cost. perCallCostExpr is the exact
	// expression every other analyser screen uses, so these costs reconcile. The
	// join keeps the indexed t.agent_id (uuid) unmodified and casts the small side —
	// tc.child_agent_id (text) — to uuid, guarded by a uuid-shape regex so a
	// malformed value yields NULL (no match) instead of a cast error. Casting the
	// indexed column to text instead would force a scan of the large usage table.
	costQuery := fmt.Sprintf(`
		SELECT
			tc.tool_name              AS tool_name,
			COALESCE(SUM(%s), 0)      AS downstream_cost_usd,
			COUNT(t.id)               AS downstream_llm_calls
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		INNER JOIN llm_conversation_token_usage t
			ON t.agent_id = CASE
				WHEN tc.child_agent_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
				THEN tc.child_agent_id::uuid
				ELSE NULL
			END
		LEFT JOIN llm_model_pricing p ON p.model_name = t.llm_model AND p.provider_name = t.llm_provider
		WHERE %s AND NULLIF(tc.child_agent_id, '') IS NOT NULL AND NULLIF(tc.tool_name, '') IS NOT NULL
		GROUP BY tc.tool_name`, perCallCostExpr, where)

	var costScans []toolCostScan
	if err := chat.dbManager.Db.Select(&costScans, costQuery, args...); err != nil {
		slog.Error("ListToolUsage: downstream-cost query failed", "error", err)
		return ToolUsageList{}, fmt.Errorf("ListToolUsage cost: %w", err)
	}

	costByTool := make(map[string]toolCostScan, len(costScans))
	for _, c := range costScans {
		costByTool[c.ToolName] = c
	}

	rows := make([]ToolUsageRow, 0, len(opScans))
	for _, s := range opScans {
		row := ToolUsageRow{
			ToolName:              s.ToolName,
			ToolType:              s.ToolType,
			Calls:                 s.Calls,
			SuccessCount:          s.SuccessCount,
			ErrorCount:            s.ErrorCount,
			InProgressCount:       s.InProgressCount,
			AvgDurationSeconds:    s.AvgDurationSeconds,
			P90DurationSeconds:    s.P90DurationSeconds,
			MaxDurationSeconds:    s.MaxDurationSeconds,
			DistinctAgents:        s.DistinctAgents,
			DistinctConversations: s.DistinctConversations,
		}
		if s.Calls > 0 {
			row.ErrorRatePct = float64(s.ErrorCount) / float64(s.Calls) * 100
		}
		if c, ok := costByTool[s.ToolName]; ok {
			row.DownstreamCostUsd = c.DownstreamCostUsd
			row.DownstreamLLMCalls = c.DownstreamLLMCalls
		}
		rows = append(rows, row)
	}

	sortToolRows(rows, sortBy)
	if len(rows) > limit {
		rows = rows[:limit]
	}

	return ToolUsageList{SortBy: sortBy, Limit: limit, Rows: rows}, nil
}

// sortToolRows ranks the leaderboard by the chosen metric (desc), tie-broken by
// call count then tool name so the order is stable across requests.
func sortToolRows(rows []ToolUsageRow, sortBy string) {
	metric := func(r ToolUsageRow) float64 {
		switch sortBy {
		case "errors":
			return float64(r.ErrorCount)
		case "duration":
			return r.P90DurationSeconds
		case "cost":
			return r.DownstreamCostUsd
		default:
			return float64(r.Calls)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		mi, mj := metric(rows[i]), metric(rows[j])
		if mi != mj {
			return mi > mj
		}
		if rows[i].Calls != rows[j].Calls {
			return rows[i].Calls > rows[j].Calls
		}
		return rows[i].ToolName < rows[j].ToolName
	})
}

// ListToolUsageRequest is the Tools-tab request. Only the filters the tool-calls
// table can honour are accepted (account + date + source); model/provider/status
// are LLM-call concepts that don't exist on a tool call.
type ListToolUsageRequest struct {
	AccountIds []string `json:"account_ids"`
	UserId     string   `json:"user_id"`
	StartDate  string   `json:"start_date" validate:"required"`
	EndDate    string   `json:"end_date" validate:"required"`
	Sources    []string `json:"sources,omitempty"`
	SortBy     string   `json:"sort_by,omitempty"` // calls|errors|duration|cost (default calls)
	Limit      int      `json:"limit,omitempty"`
}

// HandleListToolUsageApi backs ai_aggregate_tool_usage — the Tools leaderboard.
func HandleListToolUsageApi(ctx *security.RequestContext, request ListToolUsageRequest) (ToolUsageList, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return ToolUsageList{}, fmt.Errorf("HandleListToolUsageApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return ToolUsageList{}, fmt.Errorf("HandleListToolUsageApi: invalid end_date: %w", err)
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return ToolUsageList{}, err
	}

	filter := UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		Sources:    request.Sources,
		UserID:     request.UserId,
	}
	return GetConversationDao().ListToolUsage(filter, request.SortBy, request.Limit)
}

// --- Per-tool invocation explorer (drill-in / failures) --------------------

const (
	defaultToolCallLimit = 100
	maxToolCallLimit     = 500
	// toolCallTextCap bounds the params / output / error snippets returned per row.
	// The full transcript is one click away (the conversation link), so the list
	// only needs enough to triage; this keeps the payload small for big outputs.
	toolCallTextCap = 1200
)

// ToolCallRow is one tool invocation, with its conversation cross-link and the
// snippets needed to triage a failure (error / stderr / params) without opening
// the full conversation. conversation_id is the session_id (cross-link target);
// agent_id deep-opens that invocation in the conversation detail.
type ToolCallRow struct {
	ID                string    `json:"id"`
	ToolName          string    `json:"tool_name"`
	ToolType          string    `json:"tool_type"`
	Status            string    `json:"status"`
	AgentID           string    `json:"agent_id"`
	AgentName         string    `json:"agent_name"`
	ConversationID    string    `json:"conversation_id"` // session_id — cross-link target
	ConversationTitle string    `json:"conversation_title"`
	AccountID         string    `json:"account_id"`
	DurationSeconds   float64   `json:"duration_seconds"`
	CreatedAt         time.Time `json:"created_at"`
	Parameters        string    `json:"parameters"` // input args snippet
	Response          string    `json:"response"`   // output snippet
	Stderr            string    `json:"stderr"`     // separate stderr stream when present
}

// ToolCallList is the per-tool invocation payload.
type ToolCallList struct {
	ToolName string        `json:"tool_name"`
	Statuses []string      `json:"statuses"`
	Limit    int           `json:"limit"`
	Rows     []ToolCallRow `json:"rows"`
}

type toolCallRowScan struct {
	ID                string    `db:"id"`
	ToolName          string    `db:"tool_name"`
	ToolType          string    `db:"tool_type"`
	Status            string    `db:"status"`
	AgentID           string    `db:"agent_id"`
	AgentName         string    `db:"agent_name"`
	ConversationID    string    `db:"conversation_id"`
	ConversationTitle string    `db:"conversation_title"`
	AccountID         string    `db:"account_id"`
	DurationSeconds   float64   `db:"duration_seconds"`
	CreatedAt         time.Time `db:"created_at"`
	Parameters        string    `db:"parameters"`
	Response          string    `db:"response"`
	Stderr            string    `db:"stderr"`
}

// ListToolCalls returns the most-recent invocations of one tool (newest first),
// optionally restricted to a set of statuses (e.g. the failure statuses for the
// "view failures" drill-in; empty = all). Scoped by the same account + date +
// source filters as the leaderboard so the list reconciles with the row it opened.
func (chat *ConversationDao) ListToolCalls(filter UsageMetricsFilter, toolName string, statuses []string, limit int) (ToolCallList, error) {
	if len(filter.AccountIDs) == 0 || toolName == "" {
		return ToolCallList{ToolName: toolName, Statuses: statuses, Rows: []ToolCallRow{}}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return ToolCallList{}, fmt.Errorf("ListToolCalls: end_date must be >= start_date")
	}
	if limit <= 0 {
		limit = defaultToolCallLimit
	}
	if limit > maxToolCallLimit {
		limit = maxToolCallLimit
	}

	where, args := filter.buildToolWhere()
	n := len(args)

	args = append(args, toolName)
	extra := fmt.Sprintf(" AND tc.tool_name = $%d", n+1)
	n++

	if len(statuses) > 0 {
		lowered := make([]string, 0, len(statuses))
		for _, s := range statuses {
			if s != "" {
				lowered = append(lowered, strings.ToLower(s))
			}
		}
		if len(lowered) > 0 {
			// status is stored lowercased on write and the input is lowered above, so
			// compare the column directly (no lower(); keeps any index on tc.status usable).
			args = append(args, pq.Array(lowered))
			extra += fmt.Sprintf(" AND tc.status = ANY($%d)", n+1)
			n++
		}
	}

	args = append(args, limit)
	limitClause := fmt.Sprintf(" LIMIT $%d", n+1)

	// LEFT() caps each text snippet to toolCallTextCap so a multi-MB tool output
	// can't bloat the response; agent_name is best-effort (LEFT JOIN — orphan /
	// setup calls have none).
	query := fmt.Sprintf(`
		SELECT
			tc.id::text                                       AS id,
			tc.tool_name                                      AS tool_name,
			COALESCE(tc.tool_type, '')                        AS tool_type,
			COALESCE(tc.status, '')                           AS status,
			COALESCE(tc.agent_id::text, '')                   AS agent_id,
			COALESCE(a.agent_name, '')                        AS agent_name,
			COALESCE(c.session_id, '')                        AS conversation_id,
			COALESCE(c.title, '')                             AS conversation_title,
			COALESCE(c.account_id::text, '')                  AS account_id,
			%s                                                AS duration_seconds,
			tc.created_at                                     AS created_at,
			LEFT(COALESCE(tc.parameters, ''), %d)             AS parameters,
			LEFT(COALESCE(tc.response, ''), %d)               AS response,
			LEFT(COALESCE(tc.metadata->>'stderr', ''), %d)    AS stderr
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		LEFT JOIN llm_conversation_agent a ON a.id = tc.agent_id
		WHERE %s%s
		ORDER BY tc.created_at DESC%s`,
		toolDurationExpr, toolCallTextCap, toolCallTextCap, toolCallTextCap, where, extra, limitClause)

	var scans []toolCallRowScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("ListToolCalls: query failed", "error", err)
		return ToolCallList{}, fmt.Errorf("ListToolCalls: %w", err)
	}

	// toolCallRowScan and ToolCallRow have identical fields/order (only the struct
	// tags differ), so a direct conversion is exact and avoids a field-by-field copy.
	rows := make([]ToolCallRow, 0, len(scans))
	for _, s := range scans {
		rows = append(rows, ToolCallRow(s))
	}
	return ToolCallList{ToolName: toolName, Statuses: statuses, Limit: limit, Rows: rows}, nil
}

// ListToolCallsRequest is the per-tool invocation-explorer request. statuses
// restricts to specific tool statuses (empty = all); the UI passes the failure set
// for the "view failures" view and the in-progress set for the stuck view.
type ListToolCallsRequest struct {
	AccountIds []string `json:"account_ids"`
	UserId     string   `json:"user_id"`
	StartDate  string   `json:"start_date" validate:"required"`
	EndDate    string   `json:"end_date" validate:"required"`
	Sources    []string `json:"sources,omitempty"`
	ToolName   string   `json:"tool_name" validate:"required"`
	Statuses   []string `json:"statuses,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// HandleListToolCallsApi backs ai_list_tool_calls — the per-tool invocation explorer.
func HandleListToolCallsApi(ctx *security.RequestContext, request ListToolCallsRequest) (ToolCallList, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return ToolCallList{}, fmt.Errorf("HandleListToolCallsApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return ToolCallList{}, fmt.Errorf("HandleListToolCallsApi: invalid end_date: %w", err)
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return ToolCallList{}, err
	}

	filter := UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		Sources:    request.Sources,
		UserID:     request.UserId,
	}
	return GetConversationDao().ListToolCalls(filter, request.ToolName, request.Statuses, request.Limit)
}
