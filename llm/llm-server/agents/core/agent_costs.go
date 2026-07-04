package core

import (
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/security"

	"github.com/lib/pq"
)

// agent_costs.go backs ai_list_agent_costs — the Cost Analyser "Agents" tab: a
// cross-conversation leaderboard of individual agent INVOCATIONS (one row per
// llm_conversation_agent instance, i.e. one agent_id in one conversation), ranked
// by cost / latency / errors. The SAME agent name recurs across rows, each linked
// to its own conversation, so you can spot the specific invocations that cost a
// lot, ran long, or failed — then click through to that conversation/agent.
//
// Grain is the invocation, not the agent name (no GROUP BY name). Cost is DIRECT
// (this invocation's own model calls; children are their own rows) and uses the
// same perCallCostExpr as the conversation/aggregate APIs so numbers reconcile.

const (
	defaultAgentCallLimit = 100
	maxAgentCallLimit     = 200
)

// agentSortColumns whitelists ORDER BY targets — trusted aliases, never caller input.
var agentSortColumns = map[string]string{
	"cost":    "cost_usd",
	"latency": "latency_sum_seconds",
	"errors":  "error_count",
}

// agentLatencyPercentiles maps the UI's allowed percentile selections to the
// fraction percentile_cont expects. Membership is the only thing taken from
// caller input; the fraction is a trusted constant interpolated into the SQL
// (never raw input). 0 / absent means "no latency-outlier filter".
var agentLatencyPercentiles = map[int]float64{
	80: 0.80,
	85: 0.85,
	90: 0.90,
	95: 0.95,
	99: 0.99,
}

// agentLatencyBaselineWindow is the trailing window the pXX latency threshold is
// computed over — a recent "normal" baseline, independent of the report range.
const agentLatencyBaselineWindow = 24 * time.Hour

// agentCostFrom is the FROM/JOIN for the invocation leaderboard. The INNER JOIN on
// llm_conversation_agent both attaches instance metadata and excludes unattributed
// usage (t.agent_id IS NULL) — those have no invocation to link or drill.
const agentCostFrom = `
	FROM llm_conversation_token_usage t
	INNER JOIN llm_conversations c ON c.id = t.conversation_id
	INNER JOIN llm_conversation_agent a ON a.id = t.agent_id
	LEFT JOIN llm_model_pricing p ON p.model_name = t.llm_model AND p.provider_name = t.llm_provider`

// AgentCallRow is one agent invocation in one conversation, with its rolled-up
// direct cost/latency/error stats. conversation_id is the session_id (for the
// cross-link); agent_id deep-opens that invocation in the conversation detail.
type AgentCallRow struct {
	AgentID              string    `json:"agent_id"`
	AgentName            string    `json:"agent_name"`
	ConversationID       string    `json:"conversation_id"` // session_id — cross-link target
	ConversationTitle    string    `json:"conversation_title"`
	AccountID            string    `json:"account_id"`
	Status               string    `json:"status"`
	StartedAt            time.Time `json:"started_at"`
	CostUsd              float64   `json:"cost_usd"`
	LatencySumSeconds    float64   `json:"latency_sum_seconds"`    // total model time (summed; parallel)
	LatencyMaxSeconds    float64   `json:"latency_max_seconds"`    // slowest single call
	LatencyMedianSeconds float64   `json:"latency_median_seconds"` // baseline to judge if max is an outlier
	LLMCallCount         int       `json:"llm_call_count"`
	ErrorCount           int       `json:"error_count"`
	InputTokens          int64     `json:"input_tokens"`
	OutputTokens         int64     `json:"output_tokens"`
	ModelsUsed           []string  `json:"models_used"`
}

// AgentCallList is the leaderboard payload. When a latency-outlier filter is
// applied, LatencyPercentile echoes the requested pXX and LatencyThresholdSeconds
// is the resolved threshold (total invocation latency, over the trailing 24h);
// LatencyThresholdSeconds == 0 with a non-zero percentile means no 24h baseline
// existed, so no filtering was applied.
type AgentCallList struct {
	SortBy                  string                `json:"sort_by"`
	Limit                   int                   `json:"limit"`
	LatencyPercentile       int                   `json:"latency_percentile"`        // 0 = no latency filter
	LatencyThresholdSeconds float64               `json:"latency_threshold_seconds"` // pXX of 24h invocation latency
	LatencyByAgent          []AgentLatencyProfile `json:"latency_by_agent"`          // per-agent latency profile (graph)
	Rows                    []AgentCallRow        `json:"rows"`
}

// AgentLatencyProfile is one agent NAME's invocation-latency distribution over the
// report window (p50/p90/p99 of per-invocation total model latency) — the agent-wise
// latency graph. Grain is the agent name (aggregated across its invocations), unlike
// the per-invocation leaderboard rows.
type AgentLatencyProfile struct {
	AgentName   string  `json:"agent_name" db:"agent_name"`
	P50Seconds  float64 `json:"p50_seconds" db:"p50_seconds"`
	P90Seconds  float64 `json:"p90_seconds" db:"p90_seconds"`
	P99Seconds  float64 `json:"p99_seconds" db:"p99_seconds"`
	Invocations int     `json:"invocations" db:"invocations"`
}

// agentLatencyProfileLimit caps the graph to the slowest-N agents (by p90) so the
// bar chart stays legible.
const agentLatencyProfileLimit = 12

type agentCallScan struct {
	AgentID              string         `db:"agent_id"`
	AgentName            string         `db:"agent_name"`
	ConversationID       string         `db:"conversation_id"`
	ConversationTitle    string         `db:"conversation_title"`
	AccountID            string         `db:"account_id"`
	Status               string         `db:"status"`
	StartedAt            time.Time      `db:"started_at"`
	CostUsd              float64        `db:"cost_usd"`
	LatencySumSeconds    float64        `db:"latency_sum_seconds"`
	LatencyMaxSeconds    float64        `db:"latency_max_seconds"`
	LatencyMedianSeconds float64        `db:"latency_median_seconds"`
	LLMCallCount         int            `db:"llm_call_count"`
	ErrorCount           int            `db:"error_count"`
	InputTokens          int64          `db:"input_tokens"`
	OutputTokens         int64          `db:"output_tokens"`
	ModelsUsed           pq.StringArray `db:"models_used"`
}

// ListAgentCalls returns the top-N agent invocations for the filter, ranked by the
// chosen metric. cost is DIRECT per invocation (its own model calls). error_count
// counts failed model calls; the agent's own status is surfaced separately so an
// orchestration-only failure (status=fail, 0 failed calls) is still visible.
func (chat *ConversationDao) ListAgentCalls(filter UsageMetricsFilter, sortBy string, limit, latencyPercentile int) (AgentCallList, error) {
	if len(filter.AccountIDs) == 0 {
		return AgentCallList{Rows: []AgentCallRow{}}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return AgentCallList{}, fmt.Errorf("ListAgentCalls: end_date must be >= start_date")
	}

	sortCol, ok := agentSortColumns[sortBy]
	if !ok {
		sortBy = "cost"
		sortCol = agentSortColumns["cost"]
	}
	if limit <= 0 {
		limit = defaultAgentCallLimit
	}
	if limit > maxAgentCallLimit {
		limit = maxAgentCallLimit
	}

	where, args := filter.buildWhere()

	// Agent-wise latency profile (graph): per-agent p50/p90/p99 over the report
	// window, same filters, independent of the outlier HAVING below. Computed from
	// the clean report-range args before any HAVING threshold is appended.
	profiles, err := chat.agentLatencyProfiles(where, args)
	if err != nil {
		return AgentCallList{}, err
	}

	// Latency-outlier filter: keep only invocations whose TOTAL model latency is
	// at/above the pXX of the trailing-24h invocation-latency distribution (same
	// account/dimension scope). The threshold is computed first, then applied as a
	// HAVING on the report-range query. threshold 0 (no 24h baseline) ⇒ no filter,
	// so the leaderboard never silently empties when recent data is missing.
	var threshold float64
	if frac, want := agentLatencyPercentiles[latencyPercentile]; want {
		var err error
		threshold, err = chat.agentLatencyThreshold(filter, frac)
		if err != nil {
			return AgentCallList{}, err
		}
	}
	havingClause := ""
	if threshold > 0 {
		args = append(args, threshold)
		havingClause = fmt.Sprintf("HAVING COALESCE(SUM(t.latency_seconds), 0) >= $%d", len(args))
	}
	n := len(args)

	// error_count: failed model calls — request_status 'failure' or a populated
	// error_message. (perCallCostExpr / usageBaseFrom twins live in usage_analytics.go.)
	query := fmt.Sprintf(`
		SELECT
			a.id::text                                       AS agent_id,
			COALESCE(a.agent_name, '')                       AS agent_name,
			COALESCE(c.session_id, '')                       AS conversation_id,
			COALESCE(c.title, '')                            AS conversation_title,
			COALESCE(c.account_id::text, '')                 AS account_id,
			COALESCE(a.status, '')                           AS status,
			a.created_at                                     AS started_at,
			COALESCE(SUM(%s), 0)                             AS cost_usd,
			COALESCE(SUM(t.latency_seconds), 0)              AS latency_sum_seconds,
			COALESCE(MAX(t.latency_seconds), 0)              AS latency_max_seconds,
			COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY t.latency_seconds), 0) AS latency_median_seconds,
			COUNT(t.id)                                      AS llm_call_count,
			COUNT(t.id) FILTER (WHERE t.request_status = 'failure' OR COALESCE(t.error_message, '') <> '') AS error_count,
			COALESCE(SUM(t.input_tokens), 0)                 AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)                AS output_tokens,
			COALESCE(ARRAY_AGG(DISTINCT t.llm_model) FILTER (WHERE t.llm_model IS NOT NULL), '{}') AS models_used
		%s
		WHERE %s
		-- a.id and c.id are both PKs, so every selected a.*/c.* column is functionally
		-- dependent on them; grouping by the two UUID PKs instead of the wide text
		-- columns (title, session_id) is ~6x cheaper (EXPLAIN-verified).
		GROUP BY a.id, c.id
		%s
		ORDER BY %s DESC
		LIMIT $%d`,
		perCallCostExpr, agentCostFrom, where, havingClause, sortCol, n+1)

	rowArgs := append(append([]any{}, args...), limit)

	var scans []agentCallScan
	if err := chat.dbManager.Db.Select(&scans, query, rowArgs...); err != nil {
		slog.Error("ListAgentCalls: query failed", "error", err)
		return AgentCallList{}, fmt.Errorf("ListAgentCalls: %w", err)
	}

	rows := make([]AgentCallRow, 0, len(scans))
	for _, s := range scans {
		rows = append(rows, AgentCallRow{
			AgentID:              s.AgentID,
			AgentName:            s.AgentName,
			ConversationID:       s.ConversationID,
			ConversationTitle:    s.ConversationTitle,
			AccountID:            s.AccountID,
			Status:               s.Status,
			StartedAt:            s.StartedAt,
			CostUsd:              s.CostUsd,
			LatencySumSeconds:    s.LatencySumSeconds,
			LatencyMaxSeconds:    s.LatencyMaxSeconds,
			LatencyMedianSeconds: s.LatencyMedianSeconds,
			LLMCallCount:         s.LLMCallCount,
			ErrorCount:           s.ErrorCount,
			InputTokens:          s.InputTokens,
			OutputTokens:         s.OutputTokens,
			ModelsUsed:           []string(s.ModelsUsed),
		})
	}
	return AgentCallList{
		SortBy:                  sortBy,
		Limit:                   limit,
		LatencyPercentile:       latencyPercentile,
		LatencyThresholdSeconds: threshold,
		LatencyByAgent:          profiles,
		Rows:                    rows,
	}, nil
}

// agentLatencyProfiles returns the per-agent-name latency distribution (p50/p90/p99
// of per-invocation total latency) over the given report-range where/args, top-N by
// p90 for chart legibility. Powers the agent-wise latency graph.
func (chat *ConversationDao) agentLatencyProfiles(where string, args []any) ([]AgentLatencyProfile, error) {
	query := fmt.Sprintf(`
		SELECT
			agent_name,
			COALESCE(percentile_cont(0.5)  WITHIN GROUP (ORDER BY inv_latency), 0) AS p50_seconds,
			COALESCE(percentile_cont(0.9)  WITHIN GROUP (ORDER BY inv_latency), 0) AS p90_seconds,
			COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY inv_latency), 0) AS p99_seconds,
			COUNT(*)                                                               AS invocations
		FROM (
			SELECT COALESCE(a.agent_name, '')      AS agent_name,
			       a.id                            AS agent_id,
			       COALESCE(SUM(t.latency_seconds), 0) AS inv_latency
			%s
			WHERE %s
			GROUP BY a.agent_name, a.id
		) s
		GROUP BY agent_name
		ORDER BY p90_seconds DESC
		LIMIT %d`, agentCostFrom, where, agentLatencyProfileLimit)

	profiles := []AgentLatencyProfile{}
	if err := chat.dbManager.Db.Select(&profiles, query, args...); err != nil {
		slog.Error("agentLatencyProfiles: query failed", "error", err)
		return nil, fmt.Errorf("agentLatencyProfiles: %w", err)
	}
	return profiles, nil
}

// agentLatencyThreshold returns the pXX (frac, a trusted constant) of the
// per-invocation TOTAL model latency over the trailing baseline window, scoped by
// the same account/dimension filters as the leaderboard (the date range is
// overridden to the last 24h). Returns 0 when no invocations exist in the window.
func (chat *ConversationDao) agentLatencyThreshold(filter UsageMetricsFilter, frac float64) (float64, error) {
	base := filter
	now := time.Now().UTC()
	base.StartDate = now.Add(-agentLatencyBaselineWindow)
	base.EndDate = now
	where, args := base.buildWhere()

	// frac is whitelisted (0.80..0.99), safe to interpolate; every other value is
	// a bound arg. The inner query is one invocation-latency per agent over 24h;
	// percentile_cont gives the pXX of those totals.
	query := fmt.Sprintf(`
		SELECT COALESCE(percentile_cont(%g) WITHIN GROUP (ORDER BY inv_latency), 0)
		FROM (
			SELECT COALESCE(SUM(t.latency_seconds), 0) AS inv_latency
			%s
			WHERE %s
			GROUP BY a.id
		) s`, frac, agentCostFrom, where)

	var threshold float64
	if err := chat.dbManager.Db.Get(&threshold, query, args...); err != nil {
		slog.Error("agentLatencyThreshold: query failed", "error", err)
		return 0, fmt.Errorf("agentLatencyThreshold: %w", err)
	}
	return threshold, nil
}

// ListAgentCallsRequest is the Agents-tab request: the shared filter + sort + limit.
type ListAgentCallsRequest struct {
	AccountIds    []string `json:"account_ids"`
	UserId        string   `json:"user_id"`
	StartDate     string   `json:"start_date" validate:"required"`
	EndDate       string   `json:"end_date" validate:"required"`
	Sources       []string `json:"sources,omitempty"`
	Models        []string `json:"models,omitempty"`
	Providers     []string `json:"providers,omitempty"`
	Agents        []string `json:"agents,omitempty"`         // include list
	AgentsExclude []string `json:"agents_exclude,omitempty"` // exclude list (e.g. infra-debug agents)
	Statuses      []string `json:"statuses,omitempty"`
	SortBy        string   `json:"sort_by,omitempty"` // cost|latency|errors (default cost)
	Limit         int      `json:"limit,omitempty"`
	LatencyPctile int      `json:"latency_percentile,omitempty"` // 0|80|85|90|95|99 — show only invocations ≥ pXX (24h baseline)
}

// HandleListAgentCostsApi backs ai_list_agent_costs — the Agents leaderboard.
func HandleListAgentCostsApi(ctx *security.RequestContext, request ListAgentCallsRequest) (AgentCallList, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return AgentCallList{}, fmt.Errorf("HandleListAgentCostsApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return AgentCallList{}, fmt.Errorf("HandleListAgentCostsApi: invalid end_date: %w", err)
	}

	if request.LatencyPctile != 0 {
		if _, ok := agentLatencyPercentiles[request.LatencyPctile]; !ok {
			return AgentCallList{}, fmt.Errorf("HandleListAgentCostsApi: invalid latency_percentile %d", request.LatencyPctile)
		}
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return AgentCallList{}, err
	}

	filter := UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		Sources:    request.Sources,
		Models:     request.Models,
		Providers:  request.Providers,
		Agents:     request.Agents,
		AgentsExcl: request.AgentsExclude,
		Statuses:   request.Statuses,
		UserID:     request.UserId,
	}
	return GetConversationDao().ListAgentCalls(filter, request.SortBy, request.Limit, request.LatencyPctile)
}
