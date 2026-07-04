package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/security"

	"github.com/lib/pq"
)

// usage_analytics.go backs the AI Cost Analyser screens (Overview / Models /
// Reports). It aggregates cost & tokens ACROSS many conversations with filters
// — the multi-conversation counterpart to the single-conversation
// conversation_usage_metrics.go.
//
// Cost is computed on read from llm_conversation_token_usage joined to
// llm_model_pricing using perCallCostExpr below. That expression is the SQL
// twin of CalculateTotalCost (conversation_dao.go) and the per-call CASE in
// budget/usage.go — all three MUST stay in sync so aggregates reconcile with
// the per-conversation detail. Storage (llm_cache_lifecycle) cost is excluded
// here, matching the per-conversation metrics path.

// perCallCostExpr is the per-call cost in USD for one llm_conversation_token_usage
// row `t` joined to llm_model_pricing `p`. Tier-aware (long-ctx) per row.
const perCallCostExpr = `(CASE
		WHEN p.context_threshold_tokens IS NOT NULL
			 AND (t.input_tokens + COALESCE(t.cache_creation_tokens, 0)) > p.context_threshold_tokens
			 AND p.cost_per_million_input_tokens_long_ctx IS NOT NULL
		THEN
			GREATEST(t.input_tokens - COALESCE(t.cached_input_tokens, 0), 0) * p.cost_per_million_input_tokens_long_ctx
			+ COALESCE(t.cached_input_tokens, 0) * COALESCE(p.cost_per_million_cached_input_tokens_long_ctx, p.cost_per_million_input_tokens_long_ctx)
			+ COALESCE(t.cache_creation_tokens, 0) * COALESCE(p.cost_per_million_cache_creation_tokens_long_ctx, p.cost_per_million_input_tokens_long_ctx)
			+ (t.output_tokens + COALESCE(t.thinking_tokens, 0)) * p.cost_per_million_output_tokens_long_ctx
		ELSE
			GREATEST(t.input_tokens - COALESCE(t.cached_input_tokens, 0), 0) * p.cost_per_million_input_tokens
			+ COALESCE(t.cached_input_tokens, 0) * COALESCE(p.cost_per_million_cached_input_tokens, p.cost_per_million_input_tokens)
			+ COALESCE(t.cache_creation_tokens, 0) * COALESCE(p.cost_per_million_cache_creation_tokens, p.cost_per_million_input_tokens)
			+ (t.output_tokens + COALESCE(t.thinking_tokens, 0)) * p.cost_per_million_output_tokens
	END / 1000000.0)`

// cacheSavingsExpr values cached input tokens at the full (non-cached) input
// rate — i.e. "what the cache avoided paying". Matches the Grafana dashboard.
const cacheSavingsExpr = `(COALESCE(t.cached_input_tokens, 0) / 1000000.0 * COALESCE(p.cost_per_million_input_tokens, 0))`

// usageBaseFrom is the shared FROM/JOIN for every aggregation here.
const usageBaseFrom = `
	FROM llm_conversation_token_usage t
	INNER JOIN llm_conversations c ON c.id = t.conversation_id
	LEFT JOIN llm_model_pricing p ON p.model_name = t.llm_model AND p.provider_name = t.llm_provider`

// usageDimensions whitelists the dimensions a caller may group cost by.
// Membership is the only thing taken from caller input; the SQL column /
// expression for each is hard-coded in breakdownForDimension, never derived
// from the request.
var usageDimensions = map[string]bool{
	"model":    true,
	"provider": true,
	"source":   true,
	"agent":    true,
	"status":   true,
	"user":     true,
	"account":  true,
}

// usageGranularities whitelists the time-bucket units for the over-time series.
// Each is a valid Postgres date_trunc() unit, interpolated only after this check
// (never raw caller input) — see runUsageTimeSeries.
var usageGranularities = map[string]bool{
	"day":   true,
	"week":  true,
	"month": true,
}

// UsageMetricsFilter is the shared filter for all cost-analyser aggregations.
// Reused by the list and filters endpoints as they land.
type UsageMetricsFilter struct {
	AccountIDs []string
	StartDate  time.Time
	EndDate    time.Time
	Sources    []string
	Models     []string
	Providers  []string
	Agents     []string // include: only these agent names
	AgentsExcl []string // exclude: drop these agent names (e.g. infra-debug agents)
	Statuses   []string
	UserID     string
}

// buildWhere renders the WHERE condition (without the WHERE keyword) and the
// positional args. account_id is cast to uuid[] on the param side so the
// existing idx_llm_conversations_account_created_at index serves the scan.
func (f UsageMetricsFilter) buildWhere() (string, []any) {
	args := []any{pq.Array(f.AccountIDs), f.StartDate, f.EndDate}
	n := 4
	clauses := []string{
		"c.account_id = ANY($1::uuid[])",
		"c.created_at >= $2",
		"c.created_at <= $3",
	}
	addArray := func(col string, vals []string) {
		if len(vals) > 0 {
			clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", col, n))
			args = append(args, pq.Array(vals))
			n++
		}
	}
	addArray("c.source", f.Sources)
	// The cost_optimizer's own runs (source=Optimize) must not contaminate — or be
	// recursively optimizable from — the analytics they produce. Hide them by
	// default; if the caller explicitly filters by source they get exactly what they
	// asked for. IS DISTINCT FROM keeps NULL-source rows (treated as not-Optimize).
	if len(f.Sources) == 0 {
		clauses = append(clauses, "c.source IS DISTINCT FROM 'Optimize'")
	}
	addArray("t.llm_model", f.Models)
	addArray("t.llm_provider", f.Providers)
	addArray("t.agent_name", f.Agents)
	// Exclude-list (NULL-safe): drop the named agents but keep NULL/empty-named rows.
	if len(f.AgentsExcl) > 0 {
		clauses = append(clauses, fmt.Sprintf("COALESCE(t.agent_name, '') <> ALL($%d)", n))
		args = append(args, pq.Array(f.AgentsExcl))
		n++
	}
	addArray("t.request_status", f.Statuses)
	if f.UserID != "" {
		clauses = append(clauses, fmt.Sprintf("t.user_id = $%d", n))
		args = append(args, f.UserID)
	}
	return strings.Join(clauses, " AND "), args
}

// buildScopeWhere is the coarse account+date scope only (no per-dimension
// filters). Used by the filters endpoint so dropdowns list every value present
// in the window, independent of which other filters are currently selected.
func (f UsageMetricsFilter) buildScopeWhere() (string, []any) {
	return "c.account_id = ANY($1::uuid[]) AND c.created_at >= $2 AND c.created_at <= $3",
		[]any{pq.Array(f.AccountIDs), f.StartDate, f.EndDate}
}

// buildToolWhere renders the WHERE for the tool-usage aggregation (alias tc on
// llm_conversation_tool_calls, c on llm_conversations). The tool-calls table has no
// model / provider / request_status columns, so only the dimensions it can reach —
// account + date + source — are honoured here; the bar's model/provider/status
// filters do NOT apply to tools (mirrors buildCacheStorageWhere ignoring the
// dimensions the cache table lacks). Date scopes on c.created_at so the Tools tab
// covers the same conversations as every other analyser screen.
func (f UsageMetricsFilter) buildToolWhere() (string, []any) {
	args := []any{pq.Array(f.AccountIDs), f.StartDate, f.EndDate}
	n := 4
	clauses := []string{
		"c.account_id = ANY($1::uuid[])",
		"c.created_at >= $2",
		"c.created_at <= $3",
	}
	if len(f.Sources) > 0 {
		clauses = append(clauses, fmt.Sprintf("c.source = ANY($%d)", n))
		args = append(args, pq.Array(f.Sources))
	} else {
		// Hide the cost_optimizer's own runs by default (parity with buildWhere).
		clauses = append(clauses, "c.source IS DISTINCT FROM 'Optimize'")
	}
	return strings.Join(clauses, " AND "), args
}

// buildCacheStorageWhere renders the WHERE for the llm_cache_lifecycle storage
// scan (alias cl). Scoped by account + window OVERLAP (a cache counts if it was
// alive at any point inside [start,end]) and, when present, model/provider —
// the only token-side filters the cache table can honour. $2/$3 are start/end
// (reused by the proration EXTRACT in the caller).
func (f UsageMetricsFilter) buildCacheStorageWhere() (string, []any) {
	args := []any{pq.Array(f.AccountIDs), f.StartDate, f.EndDate}
	n := 4
	clauses := []string{
		"cl.account_id = ANY($1::uuid[])",
		"cl.created_at <= $3",                              // created before the window ends
		"COALESCE(cl.invalidated_at, cl.expires_at) >= $2", // still alive at/after the window starts
	}
	if len(f.Models) > 0 {
		clauses = append(clauses, fmt.Sprintf("cl.llm_model = ANY($%d)", n))
		args = append(args, pq.Array(f.Models))
		n++
	}
	if len(f.Providers) > 0 {
		clauses = append(clauses, fmt.Sprintf("cl.llm_provider = ANY($%d)", n))
		args = append(args, pq.Array(f.Providers))
	}
	return strings.Join(clauses, " AND "), args
}

// UsageTotals is the KPI-card block: filter-wide rollup (no grouping).
type UsageTotals struct {
	TotalCostUsd           float64 `json:"total_cost_usd"`
	CacheSavingsUsd        float64 `json:"cache_savings_usd"`
	TotalTasks             int     `json:"total_tasks"`
	TotalInputTokens       int64   `json:"total_input_tokens"`
	TotalOutputTokens      int64   `json:"total_output_tokens"`
	TotalCachedInputTokens int64   `json:"total_cached_input_tokens"`
	CacheHitRatePct        float64 `json:"cache_hit_rate_pct"`
	TotalRequests          int64   `json:"total_requests"`
}

// UsageGroupRow is one bucket of a grouped aggregation (by model/source/etc).
type UsageGroupRow struct {
	Key               string  `json:"key"`
	CostUsd           float64 `json:"cost_usd"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	CacheHitRatePct   float64 `json:"cache_hit_rate_pct"`
	Requests          int64   `json:"requests"`
	Conversations     int     `json:"conversations"`
	AvgLatencySeconds float64 `json:"avg_latency_seconds"`
}

// maxUsageTopN caps each per-dimension breakdown; top_n <= 0 means unlimited.
const maxUsageTopN = 100

// systemUserID is the synthetic user automation/system runs are attributed to;
// excluded from the user breakdown (mirrors the Grafana dashboard).
const systemUserID = "00000000-0000-0000-0000-000000000000"

// UsageMetrics is the DAO result: filter-wide KPI totals plus one cost breakdown
// per requested dimension, keyed by dimension name. Subsumes both the former
// Overview (many dimensions at once) and the single-cut aggregate (one).
// TimeSeries is populated only when a granularity is requested (the cost/calls
// over-time charts); nil otherwise.
type UsageMetrics struct {
	Totals     UsageTotals                `json:"totals"`
	Breakdowns map[string][]UsageGroupRow `json:"breakdowns"`
	TimeSeries *UsageTimeSeries           `json:"time_series,omitempty"`
	// Storage is the cache-lifecycle storage cost (separate from token cost),
	// prorated to the report window. Always populated for the account-wide
	// aggregation; nil only on error-free empty-scope calls.
	Storage *CacheStorage `json:"storage,omitempty"`
}

// CacheStorageScope is one cache scope's prorated storage cost over the window.
type CacheStorageScope struct {
	Scope        string  `json:"scope" db:"scope"`
	CostUsd      float64 `json:"cost_usd" db:"cost_usd"`
	CachedTokens int64   `json:"cached_tokens" db:"cached_tokens"`
	Entries      int64   `json:"entries" db:"entries"`
}

// CacheStorage is the cache-lifecycle storage-cost block: cost = cached_tokens ×
// per-hour rate × hours the cache was alive INSIDE the report window (prorated),
// summed from llm_cache_lifecycle and broken down by scope (account/global/
// conversation). This lives at a different grain from token usage (per cache
// entry, not per LLM call) and the cache table carries no source/user/agent/
// status — so storage is scoped only by account + date (+ model/provider), NOT
// by those dimensions. Token cost and storage cost are reported as separate
// lines; the UI sums them for the all-in total.
type CacheStorage struct {
	TotalUsd float64             `json:"total_usd"`
	ByScope  []CacheStorageScope `json:"by_scope"`
}

// UsageTimeSeriesRow is one (time bucket, stack key) cell: the cost and request
// count for one stack value (e.g. one model) in one period. Carries both metrics
// so the UI builds the cost-share and calls-over-time charts from one payload.
type UsageTimeSeriesRow struct {
	Bucket   time.Time `json:"bucket"`
	Key      string    `json:"key"`
	CostUsd  float64   `json:"cost_usd"`
	Requests int64     `json:"requests"`
}

// UsageTimeSeries is the over-time payload: the granularity used, plus the
// bucketed series stacked by EACH stackable dimension (model/source/agent) — all
// returned together so the UI's stack-by toggle re-pivots client-side without a
// refetch. Buckets with no rows are simply absent (caller fills gaps).
type UsageTimeSeries struct {
	Granularity string                          `json:"granularity"`
	ByDimension map[string][]UsageTimeSeriesRow `json:"by_dimension"`
}

// totalsScan / groupScan carry db tags for sqlx column mapping.
type totalsScan struct {
	CostUsd           float64 `db:"cost_usd"`
	CacheSavingsUsd   float64 `db:"cache_savings_usd"`
	TotalTasks        int     `db:"total_tasks"`
	InputTokens       int64   `db:"input_tokens"`
	OutputTokens      int64   `db:"output_tokens"`
	CachedInputTokens int64   `db:"cached_input_tokens"`
	Requests          int64   `db:"requests"`
}

type groupScan struct {
	GroupKey          string  `db:"group_key"`
	CostUsd           float64 `db:"cost_usd"`
	InputTokens       int64   `db:"input_tokens"`
	OutputTokens      int64   `db:"output_tokens"`
	CachedInputTokens int64   `db:"cached_input_tokens"`
	Requests          int64   `db:"requests"`
	Conversations     int     `db:"conversations"`
	AvgLatencySeconds float64 `db:"avg_latency_seconds"`
}

func cacheHitPct(cached, input int64) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cached) / float64(input) * 100
}

// GetUsageMetrics computes filter-wide KPI totals plus a cost breakdown for each
// requested dimension. dims must be members of usageDimensions; topN bounds each
// breakdown (<= 0 = unlimited, capped at maxUsageTopN). Pass several dims for the
// Overview screen, one for a single cut, none for KPI cards only. When
// granularity is non-empty (a member of usageGranularities) the over-time series
// is computed too, in its own concurrent scan.
func (chat *ConversationDao) GetUsageMetrics(filter UsageMetricsFilter, dims []string, topN int, granularity string) (UsageMetrics, error) {
	result := UsageMetrics{Breakdowns: map[string][]UsageGroupRow{}}
	if len(filter.AccountIDs) == 0 {
		return result, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return UsageMetrics{}, fmt.Errorf("GetUsageMetrics: end_date must be >= start_date")
	}
	if topN > maxUsageTopN {
		topN = maxUsageTopN
	}

	where, args := filter.buildWhere()

	// Partition the requested dims. The five "join-free" dims (model/provider/
	// source/agent/status) are plain columns on the base scan, so they + the KPI
	// totals are computed in ONE pass via GROUP BY GROUPING SETS — collapsing the
	// old 1+N separate scans into a single scan (~6x less DB work; EXPLAIN-verified).
	// user/account need name joins (users / cloud_accounts), so they stay as their
	// own breakdown queries, run concurrently. top_n is applied in Go.
	joinFreeReq := map[string]bool{}
	joinDims := []string{}
	seen := map[string]bool{}
	for _, dim := range dims {
		if dim == "" || seen[dim] {
			continue
		}
		seen[dim] = true
		switch dim {
		case "model", "provider", "source", "agent", "status":
			joinFreeReq[dim] = true
		case "user", "account":
			joinDims = append(joinDims, dim)
		}
	}

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	// Totals (+ join-free breakdowns when any are requested) in one query.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if len(joinFreeReq) == 0 {
			// No join-free breakdowns needed → cheap totals-only query (also the
			// path the prev-period KPI comparison takes).
			totals, err := chat.runUsageTotals(where, args)
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			result.Totals = totals
			mu.Unlock()
			return
		}
		totals, gmap, err := chat.runGroupedUsageSets(where, args)
		if err != nil {
			fail(err)
			return
		}
		mu.Lock()
		result.Totals = totals
		for dim := range joinFreeReq {
			result.Breakdowns[dim] = sortAndLimit(gmap[dim], topN)
		}
		mu.Unlock()
	}()

	// user/account breakdowns — separate scans (name joins), concurrent.
	for _, dim := range joinDims {
		dim := dim
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := chat.breakdownForDimension(dim, where, args, topN)
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			result.Breakdowns[dim] = rows
			mu.Unlock()
		}()
	}

	// Over-time series (cost + calls bucketed by period × model/source/agent) —
	// own scan, concurrent. Only when a granularity is requested.
	if granularity != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts, err := chat.runUsageTimeSeries(where, args, granularity)
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			result.TimeSeries = ts
			mu.Unlock()
		}()
	}

	// Cache-lifecycle storage cost (prorated, by scope) — separate table/grain,
	// own scan, concurrent. Account+date(+model/provider) scoped only.
	wg.Add(1)
	go func() {
		defer wg.Done()
		st, err := chat.runCacheStorageCost(filter)
		if err != nil {
			fail(err)
			return
		}
		mu.Lock()
		result.Storage = st
		mu.Unlock()
	}()

	wg.Wait()
	if firstErr != nil {
		return UsageMetrics{}, firstErr
	}
	return result, nil
}

// sortAndLimit orders a breakdown by cost desc (matching the old SQL ORDER BY
// cost_usd DESC) and applies top_n (limit <= 0 = unlimited). Done in Go because
// the GROUPING SETS scan returns every group unordered.
func sortAndLimit(rows []UsageGroupRow, limit int) []UsageGroupRow {
	if rows == nil {
		rows = []UsageGroupRow{}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CostUsd > rows[j].CostUsd })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

// gsScan is one GROUPING SETS row: the per-set GROUPING() flags (1 = column
// rolled up in this set, 0 = it's the active group key), the coalesced keys, and
// the shared aggregate block. The all-1 row is the grand total.
type gsScan struct {
	GModel    int `db:"g_model"`
	GProvider int `db:"g_provider"`
	GSource   int `db:"g_source"`
	GAgent    int `db:"g_agent"`
	GStatus   int `db:"g_status"`

	KModel    string `db:"k_model"`
	KProvider string `db:"k_provider"`
	KSource   string `db:"k_source"`
	KAgent    string `db:"k_agent"`
	KStatus   string `db:"k_status"`

	CostUsd           float64 `db:"cost_usd"`
	CacheSavingsUsd   float64 `db:"cache_savings_usd"`
	InputTokens       int64   `db:"input_tokens"`
	OutputTokens      int64   `db:"output_tokens"`
	CachedInputTokens int64   `db:"cached_input_tokens"`
	Requests          int64   `db:"requests"`
	Conversations     int     `db:"conversations"`
	AvgLatencySeconds float64 `db:"avg_latency_seconds"`
}

// runGroupedUsageSets computes the KPI totals AND the five join-free breakdowns
// (model/provider/source/agent/status) in a SINGLE scan via GROUP BY GROUPING
// SETS. Returns the totals (the grand-total set) and a map dim→rows (unordered;
// caller sorts + applies top_n). Replaces 1 totals + up to 5 breakdown scans.
func (chat *ConversationDao) runGroupedUsageSets(where string, args []any) (UsageTotals, map[string][]UsageGroupRow, error) {
	query := fmt.Sprintf(`
		SELECT
			GROUPING(t.llm_model)      AS g_model,
			GROUPING(t.llm_provider)   AS g_provider,
			GROUPING(c.source)         AS g_source,
			GROUPING(t.agent_name)     AS g_agent,
			GROUPING(t.request_status) AS g_status,
			COALESCE(t.llm_model::text, 'unknown')      AS k_model,
			COALESCE(t.llm_provider::text, 'unknown')   AS k_provider,
			COALESCE(c.source::text, 'unknown')         AS k_source,
			COALESCE(t.agent_name::text, 'unknown')     AS k_agent,
			COALESCE(t.request_status::text, 'unknown') AS k_status,
			COALESCE(SUM(%s), 0)                              AS cost_usd,
			COALESCE(SUM(%s), 0)                              AS cache_savings_usd,
			COALESCE(SUM(t.input_tokens), 0)                 AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)                AS output_tokens,
			COALESCE(SUM(COALESCE(t.cached_input_tokens, 0)), 0) AS cached_input_tokens,
			COUNT(*)                                         AS requests,
			COUNT(DISTINCT c.id)                             AS conversations,
			COALESCE(AVG(t.latency_seconds), 0)              AS avg_latency_seconds
		%s
		WHERE %s
		GROUP BY GROUPING SETS ( (), (t.llm_model), (t.llm_provider), (c.source), (t.agent_name), (t.request_status) )`,
		perCallCostExpr, cacheSavingsExpr, usageBaseFrom, where)

	var scans []gsScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runGroupedUsageSets: query failed", "error", err)
		return UsageTotals{}, nil, fmt.Errorf("runGroupedUsageSets: %w", err)
	}

	gmap := map[string][]UsageGroupRow{
		"model": {}, "provider": {}, "source": {}, "agent": {}, "status": {},
	}
	var totals UsageTotals
	for _, s := range scans {
		// Grand total: every column rolled up.
		if s.GModel == 1 && s.GProvider == 1 && s.GSource == 1 && s.GAgent == 1 && s.GStatus == 1 {
			totals = UsageTotals{
				TotalCostUsd:           s.CostUsd,
				CacheSavingsUsd:        s.CacheSavingsUsd,
				TotalTasks:             s.Conversations,
				TotalInputTokens:       s.InputTokens,
				TotalOutputTokens:      s.OutputTokens,
				TotalCachedInputTokens: s.CachedInputTokens,
				CacheHitRatePct:        cacheHitPct(s.CachedInputTokens, s.InputTokens),
				TotalRequests:          s.Requests,
			}
			continue
		}
		var dim, key string
		switch {
		case s.GModel == 0:
			dim, key = "model", s.KModel
		case s.GProvider == 0:
			dim, key = "provider", s.KProvider
		case s.GSource == 0:
			dim, key = "source", s.KSource
		case s.GAgent == 0:
			dim, key = "agent", s.KAgent
		case s.GStatus == 0:
			dim, key = "status", s.KStatus
		default:
			continue
		}
		gmap[dim] = append(gmap[dim], UsageGroupRow{
			Key:               key,
			CostUsd:           s.CostUsd,
			InputTokens:       s.InputTokens,
			OutputTokens:      s.OutputTokens,
			CachedInputTokens: s.CachedInputTokens,
			CacheHitRatePct:   cacheHitPct(s.CachedInputTokens, s.InputTokens),
			Requests:          s.Requests,
			Conversations:     s.Conversations,
			AvgLatencySeconds: s.AvgLatencySeconds,
		})
	}
	return totals, gmap, nil
}

// breakdownForDimension runs the cost breakdown for the name-joined dimensions
// (user/account). The join-free dims (model/provider/source/agent/status) are
// computed by runGroupedUsageSets instead. `dim` only selects a branch; the SQL
// column/expression is hard-coded, never interpolated from caller input.
func (chat *ConversationDao) breakdownForDimension(dim, where string, args []any, limit int) ([]UsageGroupRow, error) {
	switch dim {
	case "user":
		return chat.runUsageBreakdown(
			"COALESCE(u.display_name, u.username, t.user_id::text)",
			"LEFT JOIN users u ON u.id = t.user_id",
			fmt.Sprintf(" AND t.user_id <> '%s'", systemUserID),
			where, args, limit)
	case "account":
		return chat.runUsageBreakdown(
			"COALESCE(ca.account_name, c.account_id::text)",
			"LEFT JOIN cloud_accounts ca ON ca.id = c.account_id",
			"", where, args, limit)
	default:
		return nil, fmt.Errorf("breakdownForDimension: invalid dimension %q", dim)
	}
}

// runUsageTotals computes the filter-wide KPI block (no grouping).
func (chat *ConversationDao) runUsageTotals(where string, args []any) (UsageTotals, error) {
	query := fmt.Sprintf(`
		SELECT
			COALESCE(SUM(%s), 0)                              AS cost_usd,
			COALESCE(SUM(%s), 0)                              AS cache_savings_usd,
			COUNT(DISTINCT c.id)                             AS total_tasks,
			COALESCE(SUM(t.input_tokens), 0)                 AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)                AS output_tokens,
			COALESCE(SUM(COALESCE(t.cached_input_tokens, 0)), 0) AS cached_input_tokens,
			COUNT(*)                                         AS requests
		%s
		WHERE %s`, perCallCostExpr, cacheSavingsExpr, usageBaseFrom, where)

	var ts totalsScan
	if err := chat.dbManager.Db.Get(&ts, query, args...); err != nil {
		slog.Error("runUsageTotals: query failed", "error", err)
		return UsageTotals{}, fmt.Errorf("runUsageTotals: %w", err)
	}
	return UsageTotals{
		TotalCostUsd:           ts.CostUsd,
		CacheSavingsUsd:        ts.CacheSavingsUsd,
		TotalTasks:             ts.TotalTasks,
		TotalInputTokens:       ts.InputTokens,
		TotalOutputTokens:      ts.OutputTokens,
		TotalCachedInputTokens: ts.CachedInputTokens,
		CacheHitRatePct:        cacheHitPct(ts.CachedInputTokens, ts.InputTokens),
		TotalRequests:          ts.Requests,
	}, nil
}

// runCacheStorageCost sums llm_cache_lifecycle storage cost over the report
// window, PRORATED: each cache is billed only for the hours it was alive inside
// [start,end] — overlap = LEAST(alive_end, end) − GREATEST(created_at, start),
// where alive_end = invalidated_at, else min(now, expires_at). Grouped by scope.
// Pricing's per-hour storage column (with the legacy fallback) supplies the rate.
func (chat *ConversationDao) runCacheStorageCost(filter UsageMetricsFilter) (*CacheStorage, error) {
	where, args := filter.buildCacheStorageWhere()
	query := fmt.Sprintf(`
		SELECT
			cl.scope AS scope,
			COALESCE(SUM(
				cl.cached_tokens / 1000000.0
				* COALESCE(p.cost_per_million_cached_storage_per_hour, p.cache_cost_per_million_tokens_per_hour, 0)
				* GREATEST(0, EXTRACT(EPOCH FROM (
					LEAST(COALESCE(cl.invalidated_at, LEAST(now(), cl.expires_at)), $3)
					- GREATEST(cl.created_at, $2)
				))) / 3600.0
			), 0)                              AS cost_usd,
			COALESCE(SUM(cl.cached_tokens), 0) AS cached_tokens,
			COUNT(*)                           AS entries
		FROM llm_cache_lifecycle cl
		LEFT JOIN llm_model_pricing p
			ON p.model_name = cl.llm_model AND p.provider_name = cl.llm_provider
		WHERE %s
		GROUP BY cl.scope
		ORDER BY cost_usd DESC`, where)

	rows := []CacheStorageScope{}
	if err := chat.dbManager.Db.Select(&rows, query, args...); err != nil {
		slog.Error("runCacheStorageCost: query failed", "error", err)
		return nil, fmt.Errorf("runCacheStorageCost: %w", err)
	}
	total := 0.0
	for _, r := range rows {
		total += r.CostUsd
	}
	return &CacheStorage{TotalUsd: total, ByScope: rows}, nil
}

// runUsageBreakdown is the generic grouped-aggregation primitive. keyExpr is a
// trusted SQL expression selected AS the group key (col 1, so GROUP BY 1
// applies). extraJoins / extraWhere let callers resolve display names
// (users / cloud_accounts) or exclude rows; both must be trusted SQL — never
// raw caller input. limit <= 0 means no LIMIT. Ordered by cost desc.
func (chat *ConversationDao) runUsageBreakdown(keyExpr, extraJoins, extraWhere, where string, args []any, limit int) ([]UsageGroupRow, error) {
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	query := fmt.Sprintf(`
		SELECT
			%s                                               AS group_key,
			COALESCE(SUM(%s), 0)                             AS cost_usd,
			COALESCE(SUM(t.input_tokens), 0)                 AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)                AS output_tokens,
			COALESCE(SUM(COALESCE(t.cached_input_tokens, 0)), 0) AS cached_input_tokens,
			COUNT(*)                                         AS requests,
			COUNT(DISTINCT c.id)                             AS conversations,
			COALESCE(AVG(t.latency_seconds), 0)              AS avg_latency_seconds
		%s
		%s
		WHERE %s%s
		GROUP BY 1
		ORDER BY cost_usd DESC%s`,
		keyExpr, perCallCostExpr, usageBaseFrom, extraJoins, where, extraWhere, limitClause)

	var scans []groupScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runUsageBreakdown: query failed", "error", err, "key", keyExpr)
		return nil, fmt.Errorf("runUsageBreakdown(%s): %w", keyExpr, err)
	}

	rows := make([]UsageGroupRow, 0, len(scans))
	for _, s := range scans {
		rows = append(rows, UsageGroupRow{
			Key:               s.GroupKey,
			CostUsd:           s.CostUsd,
			InputTokens:       s.InputTokens,
			OutputTokens:      s.OutputTokens,
			CachedInputTokens: s.CachedInputTokens,
			CacheHitRatePct:   cacheHitPct(s.CachedInputTokens, s.InputTokens),
			Requests:          s.Requests,
			Conversations:     s.Conversations,
			AvgLatencySeconds: s.AvgLatencySeconds,
		})
	}
	return rows, nil
}

// tsScan is one GROUPING SETS row of the over-time query: the bucket, the
// per-dimension GROUPING() flags (0 = this dim is the active stack key in this
// set), the coalesced keys, and the shared metrics.
type tsScan struct {
	Bucket   time.Time `db:"bucket"`
	GModel   int       `db:"g_model"`
	GSource  int       `db:"g_source"`
	GAgent   int       `db:"g_agent"`
	KModel   string    `db:"k_model"`
	KSource  string    `db:"k_source"`
	KAgent   string    `db:"k_agent"`
	CostUsd  float64   `db:"cost_usd"`
	Requests int64     `db:"requests"`
}

// runUsageTimeSeries buckets cost + request count by date_trunc(granularity,
// c.created_at) crossed with EACH stackable dimension (model/source/agent) in a
// SINGLE scan via GROUP BY GROUPING SETS — the time analogue of
// runGroupedUsageSets. granularity is interpolated only after the
// usageGranularities whitelist check (a fixed date_trunc unit), never raw input;
// every other value comes from the shared where/args. Bucketed on c.created_at
// (conversation start) so the series reconciles with the KPI totals, which
// filter on the same column.
func (chat *ConversationDao) runUsageTimeSeries(where string, args []any, granularity string) (*UsageTimeSeries, error) {
	bucketExpr := fmt.Sprintf("date_trunc('%s', c.created_at)", granularity)
	query := fmt.Sprintf(`
		SELECT
			%s                                      AS bucket,
			GROUPING(t.llm_model)                   AS g_model,
			GROUPING(c.source)                      AS g_source,
			GROUPING(t.agent_name)                  AS g_agent,
			COALESCE(t.llm_model::text, 'unknown')  AS k_model,
			COALESCE(c.source::text, 'unknown')     AS k_source,
			COALESCE(t.agent_name::text, 'unknown') AS k_agent,
			COALESCE(SUM(%s), 0)                    AS cost_usd,
			COUNT(*)                                AS requests
		%s
		WHERE %s
		GROUP BY GROUPING SETS ( (%s, t.llm_model), (%s, c.source), (%s, t.agent_name) )
		ORDER BY bucket`,
		bucketExpr, perCallCostExpr, usageBaseFrom, where, bucketExpr, bucketExpr, bucketExpr)

	var scans []tsScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runUsageTimeSeries: query failed", "error", err)
		return nil, fmt.Errorf("runUsageTimeSeries: %w", err)
	}

	ts := &UsageTimeSeries{
		Granularity: granularity,
		ByDimension: map[string][]UsageTimeSeriesRow{"model": {}, "source": {}, "agent": {}},
	}
	for _, s := range scans {
		var dim, key string
		switch {
		case s.GModel == 0:
			dim, key = "model", s.KModel
		case s.GSource == 0:
			dim, key = "source", s.KSource
		case s.GAgent == 0:
			dim, key = "agent", s.KAgent
		default:
			continue
		}
		ts.ByDimension[dim] = append(ts.ByDimension[dim], UsageTimeSeriesRow{
			Bucket:   s.Bucket,
			Key:      key,
			CostUsd:  s.CostUsd,
			Requests: s.Requests,
		})
	}
	return ts, nil
}

// --- API handler -----------------------------------------------------------

// UsageMetricsRequest is the request for ai_aggregate_usage_metrics. All filters AND
// together; group_by lists the dimensions to break cost down by (empty = totals
// only). top_n bounds each breakdown (0 = unlimited).
type UsageMetricsRequest struct {
	AccountIds  []string `json:"account_ids"`
	UserId      string   `json:"user_id"`
	StartDate   string   `json:"start_date" validate:"required"`
	EndDate     string   `json:"end_date" validate:"required"`
	Sources     []string `json:"sources,omitempty"`
	Models      []string `json:"models,omitempty"`
	Providers   []string `json:"providers,omitempty"`
	Agents      []string `json:"agents,omitempty"`
	Statuses    []string `json:"statuses,omitempty"`
	GroupBy     []string `json:"group_by,omitempty"` // model|provider|source|agent|status|user|account
	TopN        int      `json:"top_n,omitempty"`
	Granularity string   `json:"granularity,omitempty"` // day|week|month (empty = no over-time series)
}

// UsageMetricsResponse echoes the resolved dimensions alongside the metrics.
type UsageMetricsResponse struct {
	GroupBy []string `json:"group_by"`
	UsageMetrics
}

// HandleUsageMetricsApi backs ai_aggregate_usage_metrics — the Overview KPI cards
// (empty group_by) and every cost-by-dimension breakdown. Pass several
// dimensions for the whole Overview screen in one call, or one for a single cut.
func HandleUsageMetricsApi(ctx *security.RequestContext, request UsageMetricsRequest) (UsageMetricsResponse, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return UsageMetricsResponse{}, fmt.Errorf("HandleUsageMetricsApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return UsageMetricsResponse{}, fmt.Errorf("HandleUsageMetricsApi: invalid end_date: %w", err)
	}

	for _, dim := range request.GroupBy {
		if !usageDimensions[dim] {
			return UsageMetricsResponse{}, fmt.Errorf("HandleUsageMetricsApi: invalid group_by %q", dim)
		}
	}
	if request.Granularity != "" && !usageGranularities[request.Granularity] {
		return UsageMetricsResponse{}, fmt.Errorf("HandleUsageMetricsApi: invalid granularity %q", request.Granularity)
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return UsageMetricsResponse{}, err
	}

	filter := UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		Sources:    request.Sources,
		Models:     request.Models,
		Providers:  request.Providers,
		Agents:     request.Agents,
		Statuses:   request.Statuses,
		UserID:     request.UserId,
	}

	metrics, err := GetConversationDao().GetUsageMetrics(filter, request.GroupBy, request.TopN, request.Granularity)
	if err != nil {
		return UsageMetricsResponse{}, err
	}
	return UsageMetricsResponse{GroupBy: request.GroupBy, UsageMetrics: metrics}, nil
}

// resolveAccessibleAccounts intersects the requested account_ids with what the
// caller may read; an empty request falls back to all readable accounts. Shared
// by every cost-analyser handler.
func resolveAccessibleAccounts(ctx *security.RequestContext, requested []string) ([]string, error) {
	sec := ctx.GetSecurityContext()
	if len(requested) == 0 {
		return sec.ListAccountIds(), nil
	}
	allowed := make([]string, 0, len(requested))
	for _, id := range requested {
		if id == "" {
			continue
		}
		if !sec.HasAccountAccess(id, security.SecurityAccessTypeRead) {
			return nil, fmt.Errorf("resolveAccessibleAccounts: forbidden account_id %s", id)
		}
		allowed = append(allowed, id)
	}
	return allowed, nil
}

// --- Filters (dropdown option-sets) ----------------------------------------

// usageFilterFrom is the light FROM for distinct-value lookups — no pricing
// join needed when we only want which dimensions appear in the window.
const usageFilterFrom = `
	FROM llm_conversation_token_usage t
	INNER JOIN llm_conversations c ON c.id = t.conversation_id`

// UsageFilterOption is an id+display-name pair for dimensions the UI shows by
// name but filters by id (users, accounts).
type UsageFilterOption struct {
	ID   string `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

// UsageFilters is the option-set payload that populates the Cost Analyser
// filter bar. String dimensions are filtered by value directly; users/accounts
// are filtered by id. Every list is scoped to the account+date window so the
// dropdowns only offer values that actually have data — except accounts, which
// lists everything the caller may read so it can be used as the scope itself.
type UsageFilters struct {
	Sources   []string            `json:"sources"`
	Models    []string            `json:"models"`
	Providers []string            `json:"providers"`
	Agents    []string            `json:"agents"`
	Statuses  []string            `json:"statuses"`
	Users     []UsageFilterOption `json:"users"`
	Accounts  []UsageFilterOption `json:"accounts"`
}

// GetUsageFilters returns the distinct filter values present in the account+date
// window, plus the caller's accessible accounts. `col` arguments are trusted
// constants, never caller input.
func (chat *ConversationDao) GetUsageFilters(filter UsageMetricsFilter) (UsageFilters, error) {
	result := UsageFilters{
		Sources:   []string{},
		Models:    []string{},
		Providers: []string{},
		Agents:    []string{},
		Statuses:  []string{},
		Users:     []UsageFilterOption{},
		Accounts:  []UsageFilterOption{},
	}
	if len(filter.AccountIDs) == 0 {
		return result, nil
	}

	scope, args := filter.buildScopeWhere()

	// All five string dimensions in ONE scan via per-column array_agg(DISTINCT …)
	// instead of five separate SELECT DISTINCT queries — ~5x fewer buffer reads and
	// ~3x faster (EXPLAIN-verified). array_agg(DISTINCT) returns sorted values, and
	// COALESCE(…, '{}') keeps the result non-NULL when the window is empty.
	dimQuery := fmt.Sprintf(`
		SELECT
			COALESCE(array_agg(DISTINCT c.source)         FILTER (WHERE NULLIF(c.source, '')         IS NOT NULL), '{}') AS sources,
			COALESCE(array_agg(DISTINCT t.llm_model)      FILTER (WHERE NULLIF(t.llm_model, '')      IS NOT NULL), '{}') AS models,
			COALESCE(array_agg(DISTINCT t.llm_provider)   FILTER (WHERE NULLIF(t.llm_provider, '')   IS NOT NULL), '{}') AS providers,
			COALESCE(array_agg(DISTINCT t.agent_name)     FILTER (WHERE NULLIF(t.agent_name, '')     IS NOT NULL), '{}') AS agents,
			COALESCE(array_agg(DISTINCT t.request_status) FILTER (WHERE NULLIF(t.request_status, '') IS NOT NULL), '{}') AS statuses
		%s
		WHERE %s`, usageFilterFrom, scope)

	var dims struct {
		Sources   pq.StringArray `db:"sources"`
		Models    pq.StringArray `db:"models"`
		Providers pq.StringArray `db:"providers"`
		Agents    pq.StringArray `db:"agents"`
		Statuses  pq.StringArray `db:"statuses"`
	}
	if err := chat.dbManager.Db.Get(&dims, dimQuery, args...); err != nil {
		slog.Error("GetUsageFilters: dimensions query failed", "error", err)
		return UsageFilters{}, fmt.Errorf("GetUsageFilters dimensions: %w", err)
	}
	result.Sources = []string(dims.Sources)
	result.Models = []string(dims.Models)
	result.Providers = []string(dims.Providers)
	result.Agents = []string(dims.Agents)
	result.Statuses = []string(dims.Statuses)

	var err error

	// Users present in the window (system user excluded), resolved to a name.
	userQuery := fmt.Sprintf(`
		SELECT DISTINCT t.user_id::text AS id,
			COALESCE(u.display_name, u.username, t.user_id::text) AS name
		%s
		LEFT JOIN users u ON u.id = t.user_id
		WHERE %s AND t.user_id IS NOT NULL AND t.user_id <> '%s'
		ORDER BY name`, usageFilterFrom, scope, systemUserID)
	users := []UsageFilterOption{}
	if err = chat.dbManager.Db.Select(&users, userQuery, args...); err != nil {
		slog.Error("GetUsageFilters: users query failed", "error", err)
		return UsageFilters{}, fmt.Errorf("GetUsageFilters users: %w", err)
	}
	result.Users = users

	// Accounts the caller may read, resolved to a name — independent of the
	// date window because this dropdown defines the scope. Only ACTIVE accounts
	// are offered (status ∈ active|disabled|inactive); disabled/inactive accounts
	// are hidden from the scope picker.
	accountQuery := `
		SELECT id::text AS id, COALESCE(account_name, id::text) AS name
		FROM cloud_accounts
		WHERE id = ANY($1::uuid[]) AND status = 'active'
		ORDER BY name`
	accounts := []UsageFilterOption{}
	if err = chat.dbManager.Db.Select(&accounts, accountQuery, pq.Array(filter.AccountIDs)); err != nil {
		slog.Error("GetUsageFilters: accounts query failed", "error", err)
		return UsageFilters{}, fmt.Errorf("GetUsageFilters accounts: %w", err)
	}
	result.Accounts = accounts

	return result, nil
}

// UsageFiltersRequest scopes the option-sets to an account+date window.
type UsageFiltersRequest struct {
	AccountIds []string `json:"account_ids"`
	StartDate  string   `json:"start_date" validate:"required"`
	EndDate    string   `json:"end_date" validate:"required"`
}

// HandleUsageFiltersApi backs ai_get_usage_filters — populates the filter bar.
func HandleUsageFiltersApi(ctx *security.RequestContext, request UsageFiltersRequest) (UsageFilters, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return UsageFilters{}, fmt.Errorf("HandleUsageFiltersApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return UsageFilters{}, fmt.Errorf("HandleUsageFiltersApi: invalid end_date: %w", err)
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return UsageFilters{}, err
	}

	return GetConversationDao().GetUsageFilters(UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
	})
}

// --- Conversation cost list (explorer) -------------------------------------

const (
	defaultConversationListLimit = 50
	maxConversationListLimit     = 200
)

// conversationSortColumns whitelists ORDER BY targets. Each value is a single
// trusted column/alias (no expressions), safe to interpolate. Selecting from
// this map is the only way the column reaches the query.
var conversationSortColumns = map[string]string{
	"cost":       "cost_usd",
	"start_time": "c.created_at",
	"duration":   "wall_clock_seconds",
	"llm_calls":  "llm_call_count",
	"tokens":     "input_tokens",
}

// ConversationModelStat is one model's rolled-up calls + cost within a single
// conversation. cost_usd uses the same perCallCostExpr as the row total, so the
// breakdown reconciles with ConversationCostRow.CostUsd.
type ConversationModelStat struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Calls        int64   `json:"calls"`
	CostUsd      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// ConversationCostRow is one explorer row: a conversation with its rolled-up
// cost, tokens, models, and structural counts (messages/agents/llm calls).
type ConversationCostRow struct {
	ConversationID      string                  `json:"conversation_id"`
	SessionID           string                  `json:"session_id"`
	Source              string                  `json:"source"`
	Status              string                  `json:"status"`
	Title               string                  `json:"title"`
	UserID              string                  `json:"user_id"`
	AccountID           string                  `json:"account_id"`
	StartedAt           time.Time               `json:"started_at"`
	EndedAt             time.Time               `json:"ended_at"`
	WallClockSeconds    float64                 `json:"wall_clock_seconds"`
	ModelLatencySeconds float64                 `json:"model_latency_seconds"`
	CostUsd             float64                 `json:"cost_usd"`
	InputTokens         int64                   `json:"input_tokens"`
	OutputTokens        int64                   `json:"output_tokens"`
	CachedInputTokens   int64                   `json:"cached_input_tokens"`
	MessageCount        int                     `json:"message_count"`
	AgentCount          int                     `json:"agent_count"`
	LLMCallCount        int                     `json:"llm_call_count"`
	ModelsUsed          []string                `json:"models_used"`
	ModelBreakdown      []ConversationModelStat `json:"model_breakdown"`
}

// ConversationListPage carries pagination metadata. Total is the full count of
// conversations matching the filter (= filter-wide total_tasks).
type ConversationListPage struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// ConversationCostList is the explorer payload: filter-wide KPI totals (header),
// pagination, and the page of conversation rows.
type ConversationCostList struct {
	Totals UsageTotals           `json:"totals"`
	Page   ConversationListPage  `json:"page"`
	Rows   []ConversationCostRow `json:"rows"`
}

type conversationCostScan struct {
	ConversationID      string         `db:"conversation_id"`
	SessionID           sql.NullString `db:"session_id"`
	Source              string         `db:"source"`
	Status              string         `db:"status"`
	Title               string         `db:"title"`
	UserID              string         `db:"user_id"`
	AccountID           string         `db:"account_id"`
	CreatedAt           time.Time      `db:"created_at"`
	UpdatedAt           sql.NullTime   `db:"updated_at"`
	WallClockSeconds    float64        `db:"wall_clock_seconds"`
	ModelLatencySeconds float64        `db:"model_latency_seconds"`
	CostUsd             float64        `db:"cost_usd"`
	InputTokens         int64          `db:"input_tokens"`
	OutputTokens        int64          `db:"output_tokens"`
	CachedInputTokens   int64          `db:"cached_input_tokens"`
	MessageCount        int            `db:"message_count"`
	AgentCount          int            `db:"agent_count"`
	LLMCallCount        int            `db:"llm_call_count"`
	ModelsUsed          pq.StringArray `db:"models_used"`
}

// ListConversationCosts returns a filtered, sorted, paginated page of
// conversations with per-conversation cost/token/model rollups, plus the
// filter-wide KPI totals for the explorer header.
//
// message_count / agent_count are counted over rows that produced LLM calls
// (DISTINCT message_id / agent_id in token usage) — i.e. billable structure,
// not every transcript row. GROUP BY c.id relies on c.id being the PK, so all
// other c.* columns are functionally dependent and selectable.
func (chat *ConversationDao) ListConversationCosts(filter UsageMetricsFilter, sortBy, sortDir string, limit, offset int) (ConversationCostList, error) {
	if len(filter.AccountIDs) == 0 {
		return ConversationCostList{Rows: []ConversationCostRow{}}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return ConversationCostList{}, fmt.Errorf("ListConversationCosts: end_date must be >= start_date")
	}

	sortCol, ok := conversationSortColumns[sortBy]
	if !ok {
		sortCol = conversationSortColumns["start_time"]
	}
	dir := "DESC"
	if strings.EqualFold(sortDir, "asc") {
		dir = "ASC"
	}
	if limit <= 0 {
		limit = defaultConversationListLimit
	}
	if limit > maxConversationListLimit {
		limit = maxConversationListLimit
	}
	if offset < 0 {
		offset = 0
	}

	where, args := filter.buildWhere()

	// Filter-wide KPI header (over the whole match, not just this page). Total
	// conversation count for pagination falls out of totals.TotalTasks. Run it
	// concurrently with the page query below — they're independent over the same
	// filter, so overlapping them saves a round-trip (matters on a slow DB link).
	var (
		totals    UsageTotals
		totalsErr error
		totalsWg  sync.WaitGroup
	)
	totalsWg.Add(1)
	go func() {
		defer totalsWg.Done()
		totals, totalsErr = chat.runUsageTotals(where, args)
	}()

	n := len(args)
	query := fmt.Sprintf(`
		SELECT
			c.id::text                                       AS conversation_id,
			COALESCE(c.session_id, '')                       AS session_id,
			COALESCE(c.source, 'unknown')                    AS source,
			COALESCE(c.status::text, '')                     AS status,
			COALESCE(c.title, '')                            AS title,
			COALESCE(c.user_id::text, '')                    AS user_id,
			COALESCE(c.account_id::text, '')                 AS account_id,
			c.created_at                                     AS created_at,
			c.updated_at                                     AS updated_at,
			COALESCE(EXTRACT(EPOCH FROM (c.updated_at - c.created_at)), 0) AS wall_clock_seconds,
			COALESCE(SUM(t.latency_seconds), 0)              AS model_latency_seconds,
			COALESCE(SUM(%s), 0)                             AS cost_usd,
			COALESCE(SUM(t.input_tokens), 0)                 AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)                AS output_tokens,
			COALESCE(SUM(COALESCE(t.cached_input_tokens, 0)), 0) AS cached_input_tokens,
			COUNT(DISTINCT t.message_id)                     AS message_count,
			COUNT(DISTINCT t.agent_id)                       AS agent_count,
			COUNT(t.id)                                      AS llm_call_count,
			COALESCE(ARRAY_AGG(DISTINCT t.llm_model) FILTER (WHERE t.llm_model IS NOT NULL), '{}') AS models_used
		%s
		WHERE %s
		GROUP BY c.id
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d`,
		perCallCostExpr, usageBaseFrom, where, sortCol, dir, n+1, n+2)

	rowArgs := append(append([]any{}, args...), limit, offset)

	var scans []conversationCostScan
	pageErr := chat.dbManager.Db.Select(&scans, query, rowArgs...)

	// Join the concurrent totals query before using either result.
	totalsWg.Wait()
	if totalsErr != nil {
		return ConversationCostList{}, totalsErr
	}
	if pageErr != nil {
		slog.Error("ListConversationCosts: query failed", "error", pageErr)
		return ConversationCostList{}, fmt.Errorf("ListConversationCosts: %w", pageErr)
	}

	rows := make([]ConversationCostRow, 0, len(scans))
	for _, s := range scans {
		row := ConversationCostRow{
			ConversationID:      s.ConversationID,
			SessionID:           s.SessionID.String,
			Source:              s.Source,
			Status:              s.Status,
			Title:               s.Title,
			UserID:              s.UserID,
			AccountID:           s.AccountID,
			StartedAt:           s.CreatedAt,
			WallClockSeconds:    s.WallClockSeconds,
			ModelLatencySeconds: s.ModelLatencySeconds,
			CostUsd:             s.CostUsd,
			InputTokens:         s.InputTokens,
			OutputTokens:        s.OutputTokens,
			CachedInputTokens:   s.CachedInputTokens,
			MessageCount:        s.MessageCount,
			AgentCount:          s.AgentCount,
			LLMCallCount:        s.LLMCallCount,
			ModelsUsed:          []string(s.ModelsUsed),
		}
		if s.UpdatedAt.Valid {
			row.EndedAt = s.UpdatedAt.Time
		}
		row.ModelBreakdown = []ConversationModelStat{}
		rows = append(rows, row)
	}

	// One additional query for just this page's conversations attaches the
	// per-model breakdown — same perCallCostExpr so SUM(breakdown.cost_usd)
	// reconciles with each row's cost_usd. No per-row N+1.
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ConversationID)
	}
	if len(ids) > 0 {
		byConv, err := chat.modelBreakdownForConversations(where, args, ids)
		if err != nil {
			return ConversationCostList{}, err
		}
		for i := range rows {
			if stats := byConv[rows[i].ConversationID]; stats != nil {
				rows[i].ModelBreakdown = stats
			}
		}
	}

	return ConversationCostList{
		Totals: totals,
		Page:   ConversationListPage{Limit: limit, Offset: offset, Total: totals.TotalTasks},
		Rows:   rows,
	}, nil
}

type conversationModelStatScan struct {
	ConversationID string  `db:"conversation_id"`
	Model          string  `db:"model"`
	Provider       string  `db:"provider"`
	Calls          int64   `db:"calls"`
	CostUsd        float64 `db:"cost_usd"`
	InputTokens    int64   `db:"input_tokens"`
	OutputTokens   int64   `db:"output_tokens"`
}

// modelBreakdownForConversations rolls up calls + cost per (model, provider) for
// the given page of conversation ids in a SINGLE query, keyed by conversation_id.
// It reuses the page's where/args and constrains to the page via c.id = ANY($N).
// perCallCostExpr is identical to the row query so the per-model costs sum back
// to ConversationCostRow.CostUsd.
func (chat *ConversationDao) modelBreakdownForConversations(where string, args []any, ids []string) (map[string][]ConversationModelStat, error) {
	n := len(args)
	query := fmt.Sprintf(`
		SELECT
			c.id::text                          AS conversation_id,
			COALESCE(t.llm_model, '')           AS model,
			COALESCE(t.llm_provider, '')        AS provider,
			COUNT(t.id)                         AS calls,
			COALESCE(SUM(%s), 0)                AS cost_usd,
			COALESCE(SUM(t.input_tokens), 0)    AS input_tokens,
			COALESCE(SUM(t.output_tokens), 0)   AS output_tokens
		%s
		WHERE %s AND c.id = ANY($%d::uuid[])
		GROUP BY c.id, t.llm_model, t.llm_provider
		ORDER BY cost_usd DESC`,
		perCallCostExpr, usageBaseFrom, where, n+1)

	queryArgs := append(append([]any{}, args...), pq.Array(ids))

	var scans []conversationModelStatScan
	if err := chat.dbManager.Db.Select(&scans, query, queryArgs...); err != nil {
		slog.Error("modelBreakdownForConversations: query failed", "error", err)
		return nil, fmt.Errorf("modelBreakdownForConversations: %w", err)
	}

	byConv := make(map[string][]ConversationModelStat, len(ids))
	for _, s := range scans {
		byConv[s.ConversationID] = append(byConv[s.ConversationID], ConversationModelStat{
			Model:        s.Model,
			Provider:     s.Provider,
			Calls:        s.Calls,
			CostUsd:      s.CostUsd,
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
		})
	}
	return byConv, nil
}

// ListConversationCostsRequest is the explorer request: the shared filter plus
// sort + pagination.
type ListConversationCostsRequest struct {
	AccountIds []string `json:"account_ids"`
	UserId     string   `json:"user_id"`
	StartDate  string   `json:"start_date" validate:"required"`
	EndDate    string   `json:"end_date" validate:"required"`
	Sources    []string `json:"sources,omitempty"`
	Models     []string `json:"models,omitempty"`
	Providers  []string `json:"providers,omitempty"`
	Agents     []string `json:"agents,omitempty"`
	Statuses   []string `json:"statuses,omitempty"`
	SortBy     string   `json:"sort_by,omitempty"`  // cost|start_time|duration|llm_calls|tokens
	SortDir    string   `json:"sort_dir,omitempty"` // asc|desc (default desc)
	Limit      int      `json:"limit,omitempty"`
	Offset     int      `json:"offset,omitempty"`
}

// HandleListConversationCostsApi backs ai_list_conversation_costs — the
// conversations explorer (row → basic overview via
// ai_get_conversation_usage_metrics → detailed tree via
// ai_get_conversation_tree).
func HandleListConversationCostsApi(ctx *security.RequestContext, request ListConversationCostsRequest) (ConversationCostList, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return ConversationCostList{}, fmt.Errorf("HandleListConversationCostsApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return ConversationCostList{}, fmt.Errorf("HandleListConversationCostsApi: invalid end_date: %w", err)
	}

	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return ConversationCostList{}, err
	}

	filter := UsageMetricsFilter{
		AccountIDs: accountIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		Sources:    request.Sources,
		Models:     request.Models,
		Providers:  request.Providers,
		Agents:     request.Agents,
		Statuses:   request.Statuses,
		UserID:     request.UserId,
	}

	return GetConversationDao().ListConversationCosts(filter, request.SortBy, request.SortDir, request.Limit, request.Offset)
}
