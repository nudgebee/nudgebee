// Package usage serves read-only aggregations over the gateway's own metering data
// (llm_gateway_usage) for the AI Gateway dashboard. The response shape mirrors
// llm-server's usage_analytics (UsageMetrics/Totals/GroupRow/TimeSeries) so the
// existing cost-analyser frontend patterns port directly; the queries are the
// gateway's own (all org traffic, not just agent conversations).
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/metering"
)

// Request is the aggregation query. Grain is the tenant (the gateway keys usage by
// tenant); account_ids are accepted for frontend parity but not yet scoped on.
type Request struct {
	TenantID    string
	StartDate   time.Time
	EndDate     time.Time
	Granularity string // "day" (default) | "hour"
}

// Totals are the top-line KPIs. JSON tags match llm-server's UsageTotals.
type Totals struct {
	TotalCostUsd           float64 `json:"total_cost_usd"`
	TotalRequests          int64   `json:"total_requests"`
	TotalInputTokens       int64   `json:"total_input_tokens"`
	TotalOutputTokens      int64   `json:"total_output_tokens"`
	TotalCachedInputTokens int64   `json:"total_cached_input_tokens"`
	CacheHitRatePct        float64 `json:"cache_hit_rate_pct"`
	AvgLatencySeconds      float64 `json:"avg_latency_seconds"`
}

// GroupRow is one breakdown bucket (e.g. a provider, model, or user).
type GroupRow struct {
	Key               string  `json:"key"`
	ID                string  `json:"id,omitempty"` // stable id for drill-down (user_id on the user breakdown)
	CostUsd           float64 `json:"cost_usd"`
	Requests          int64   `json:"requests"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	CacheHitRatePct   float64 `json:"cache_hit_rate_pct"`
	AvgLatencySeconds float64 `json:"avg_latency_seconds"`
}

// TimeSeriesRow is one point of the overall time series.
type TimeSeriesRow struct {
	Bucket   time.Time `json:"bucket"`
	Key      string    `json:"key"` // "overall" for now
	CostUsd  float64   `json:"cost_usd"`
	Requests int64     `json:"requests"`
	Tokens   int64     `json:"tokens"`
}

// TimeSeries mirrors llm-server's shape (a map keyed by dimension).
type TimeSeries struct {
	Granularity string                     `json:"granularity"`
	ByDimension map[string][]TimeSeriesRow `json:"by_dimension"`
}

// ToolRow is one row of the tool breakdown. Phase 1 reports tools the caller
// OFFERED (from the request's tool definitions, captured in attributes.actual.
// tool_names) — how often each was made available and the request latency.
// Per-tool cost/tokens are intentionally omitted: a request can offer several
// tools, so attributing its cost/tokens to one would double-count. Which tools
// were actually CALLED (+ failures) is a later phase.
type ToolRow struct {
	Tool              string   `json:"tool"`
	Requests          int64    `json:"requests"` // requests that offered this tool
	AvgLatencySeconds float64  `json:"avg_latency_seconds"`
	Models            []string `json:"models"` // distinct models that offered this tool
}

// LabelCount is one (label, count) pair for a governance breakdown (a reject
// reason, a routing reason, …).
type LabelCount struct {
	Key      string `json:"key"`
	Requests int64  `json:"requests"`
}

// Outcomes partitions every request by HTTP status class. The four classes are
// exhaustive (a row with no recorded status falls in "other"), so they sum to
// total_requests. ErrorRatePct is the non-success share.
type Outcomes struct {
	Success      int64   `json:"success"`        // 2xx
	ClientError  int64   `json:"client_error"`   // 4xx (incl. rate-limit/blocked rejections)
	ServerError  int64   `json:"server_error"`   // 5xx (provider/gateway faults)
	Other        int64   `json:"other"`          // 1xx/3xx or unrecorded (e.g. aborted stream)
	ErrorRatePct float64 `json:"error_rate_pct"` // 100 * (total - success) / total
}

// ProviderOutcome is one upstream provider's request count + error share — the
// reliability lens split by provider (which upstream is actually failing).
type ProviderOutcome struct {
	Provider     string  `json:"provider"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"` // non-2xx
	ErrorRatePct float64 `json:"error_rate_pct"`
}

// OutcomeBucket is one time bucket's outcome mix, for the reliability trend chart.
type OutcomeBucket struct {
	Bucket      time.Time `json:"bucket"`
	Success     int64     `json:"success"`
	ClientError int64     `json:"client_error"`
	ServerError int64     `json:"server_error"`
	Other       int64     `json:"other"`
}

// LatencyPct is the tail-latency profile (seconds) — avg alone hides the slow tail.
type LatencyPct struct {
	P50Seconds float64 `json:"p50_seconds"`
	P95Seconds float64 `json:"p95_seconds"`
	P99Seconds float64 `json:"p99_seconds"`
}

// RouteCount is one from→to substitution pair and how often it fired — where the
// gateway's cross-provider substitution actually sends traffic.
type RouteCount struct {
	FromProvider string `json:"from_provider"`
	FromModel    string `json:"from_model"`
	ToProvider   string `json:"to_provider"`
	ToModel      string `json:"to_model"`
	Requests     int64  `json:"requests"`
}

// Governance is the reliability + intelligence rollup powering the Governance tab.
// Reliability: request Outcomes (overall · per provider · over time), tail Latency,
// and why requests were Rejected. Intelligence: the P2 routing decisions
// (substitute/fallback/deprecated/…), the from→to SubstitutionRoutes, and DLP hits.
// All derived from the same rows the cost/token aggregates use.
type Governance struct {
	Outcomes           Outcomes          `json:"outcomes"`
	OutcomesByProvider []ProviderOutcome `json:"outcomes_by_provider"`
	OutcomeSeries      []OutcomeBucket   `json:"outcome_series"`
	Latency            LatencyPct        `json:"latency"`
	Rejects            []LabelCount      `json:"rejects"`             // by derived.reject_reason (edge rejections)
	RoutingReasons     []LabelCount      `json:"routing_reasons"`     // by routing_reason (passthrough/substitute/…)
	SubstitutionRoutes []RouteCount      `json:"substitution_routes"` // from→to pairs for routing_reason=substitute
	DLPHits            int64             `json:"dlp_hits"`            // requests that tripped the egress secret filter
}

// Metrics is the full aggregation result.
type Metrics struct {
	Totals     Totals                `json:"totals"`
	Breakdowns map[string][]GroupRow `json:"breakdowns"` // "provider", "model", "user"
	Tools      []ToolRow             `json:"tools"`
	Governance *Governance           `json:"governance,omitempty"`
	TimeSeries *TimeSeries           `json:"time_series,omitempty"`
}

func cacheHitPct(cached, input int64) float64 {
	denom := cached + input
	if denom == 0 {
		return 0
	}
	return 100 * float64(cached) / float64(denom)
}

// avgLatencySeconds converts a summed latency (ms) over N requests to average seconds.
func avgLatencySeconds(sumMs, requests int64) float64 {
	if requests == 0 {
		return 0
	}
	return float64(sumMs) / float64(requests) / 1000
}

type pmRow struct {
	Provider   string  `db:"provider"`
	Model      string  `db:"model"`
	Requests   int64   `db:"requests"`
	Input      int64   `db:"input_tokens"`
	Output     int64   `db:"output_tokens"`
	CacheRead  int64   `db:"cache_read_tokens"`
	CacheWrite int64   `db:"cache_write_tokens"`
	LatencySum int64   `db:"latency_ms_sum"`
	Cost       float64 `db:"cost_usd"`
}

type tsRow struct {
	Bucket   time.Time `db:"bucket"`
	Requests int64     `db:"requests"`
	Input    int64     `db:"input_tokens"`
	Output   int64     `db:"output_tokens"`
	Cost     float64   `db:"cost_usd"`
}

type userRow struct {
	UserID      string  `db:"user_id"`
	DisplayName string  `db:"display_name"`
	Username    string  `db:"username"`
	Model       string  `db:"model"`
	Requests    int64   `db:"requests"`
	Input       int64   `db:"input_tokens"`
	Output      int64   `db:"output_tokens"`
	CacheRead   int64   `db:"cache_read_tokens"`
	CacheWrite  int64   `db:"cache_write_tokens"`
	LatencySum  int64   `db:"latency_ms_sum"`
	Cost        float64 `db:"cost_usd"`
}

// Aggregate runs the metric queries against the metering store and assembles the
// result. Cost is summed from the per-row cost_usd snapshotted at write time (no
// runtime pricing-catalog join), then rolled up to totals / provider / user /
// time-bucket. Latency is a request-weighted average.
func Aggregate(ctx context.Context, db *common.DatabaseManager, req Request) (*Metrics, error) {
	if req.EndDate.Before(req.StartDate) {
		return nil, fmt.Errorf("usage: end_date must be >= start_date")
	}
	m := &Metrics{Breakdowns: map[string][]GroupRow{"provider": {}, "model": {}, "user": {}}}

	// (provider, model) grouping → totals + by-provider + by-model. Cost is summed
	// from the per-row snapshot stored at write time (no runtime pricing catalog join).
	const pmQuery = `
		SELECT provider, model, count(*) AS requests,
		       COALESCE(sum(input_tokens),0) AS input_tokens,
		       COALESCE(sum(output_tokens),0) AS output_tokens,
		       COALESCE(sum(cache_read_tokens),0) AS cache_read_tokens,
		       COALESCE(sum(cache_write_tokens),0) AS cache_write_tokens,
		       COALESCE(sum(latency_ms),0) AS latency_ms_sum,
		       COALESCE(sum(cost_usd),0) AS cost_usd
		FROM llm_gateway_usage
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY provider, model`
	var pm []pmRow
	if err := db.QueryAndScan(&pm, pmQuery, req.TenantID, req.StartDate, req.EndDate); err != nil {
		return nil, fmt.Errorf("usage: provider/model aggregation: %w", err)
	}

	byProvider := map[string]*GroupRow{}
	provLatency := map[string]int64{}
	var totalLatency int64
	for _, r := range pm {
		cost := r.Cost
		m.Totals.TotalRequests += r.Requests
		m.Totals.TotalInputTokens += r.Input
		m.Totals.TotalOutputTokens += r.Output
		m.Totals.TotalCachedInputTokens += r.CacheRead
		m.Totals.TotalCostUsd += cost
		totalLatency += r.LatencySum

		m.Breakdowns["model"] = append(m.Breakdowns["model"], GroupRow{
			Key: r.Model, CostUsd: cost, Requests: r.Requests,
			InputTokens: r.Input, OutputTokens: r.Output, CachedInputTokens: r.CacheRead,
			CacheHitRatePct:   cacheHitPct(r.CacheRead, r.Input),
			AvgLatencySeconds: avgLatencySeconds(r.LatencySum, r.Requests),
		})
		p := byProvider[r.Provider]
		if p == nil {
			p = &GroupRow{Key: r.Provider}
			byProvider[r.Provider] = p
		}
		p.CostUsd += cost
		p.Requests += r.Requests
		p.InputTokens += r.Input
		p.OutputTokens += r.Output
		p.CachedInputTokens += r.CacheRead
		provLatency[r.Provider] += r.LatencySum
	}
	m.Totals.CacheHitRatePct = cacheHitPct(m.Totals.TotalCachedInputTokens, m.Totals.TotalInputTokens)
	m.Totals.AvgLatencySeconds = avgLatencySeconds(totalLatency, m.Totals.TotalRequests)
	for name, p := range byProvider {
		p.AvgLatencySeconds = avgLatencySeconds(provLatency[name], p.Requests)
		p.CacheHitRatePct = cacheHitPct(p.CachedInputTokens, p.InputTokens)
		m.Breakdowns["provider"] = append(m.Breakdowns["provider"], *p)
	}
	sortByRequests(m.Breakdowns["provider"])
	sortByRequests(m.Breakdowns["model"])

	users, err := byUser(db, req)
	if err != nil {
		return nil, err
	}
	m.Breakdowns["user"] = users

	tools, err := byTool(db, req)
	if err != nil {
		return nil, err
	}
	m.Tools = tools

	gov, err := governance(db, req)
	if err != nil {
		return nil, err
	}
	m.Governance = gov

	ts, err := timeSeries(db, req)
	if err != nil {
		return nil, err
	}
	m.TimeSeries = ts
	return m, nil
}

func sortByRequests(rows []GroupRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Requests > rows[j].Requests })
}

// byUser breaks usage down by the calling user, resolving user_id → display name.
func byUser(db *common.DatabaseManager, req Request) ([]GroupRow, error) {
	const q = `
		SELECT g.user_id, COALESCE(u.display_name,'') AS display_name,
		       COALESCE(u.username::text,'') AS username, g.model, g.requests,
		       g.input_tokens, g.output_tokens, g.cache_read_tokens, g.cache_write_tokens, g.latency_ms_sum, g.cost_usd
		FROM (
			SELECT user_id, model, count(*) AS requests,
			       COALESCE(sum(input_tokens),0) AS input_tokens,
			       COALESCE(sum(output_tokens),0) AS output_tokens,
			       COALESCE(sum(cache_read_tokens),0) AS cache_read_tokens,
			       COALESCE(sum(cache_write_tokens),0) AS cache_write_tokens,
			       COALESCE(sum(latency_ms),0) AS latency_ms_sum,
			       COALESCE(sum(cost_usd),0) AS cost_usd
			FROM llm_gateway_usage
			WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3 AND user_id <> ''
			GROUP BY user_id, model
		) g
		LEFT JOIN users u ON u.id::text = g.user_id`
	var rows []userRow
	if err := db.QueryAndScan(&rows, q, req.TenantID, req.StartDate, req.EndDate); err != nil {
		return nil, fmt.Errorf("usage: by-user aggregation: %w", err)
	}
	byUID := map[string]*GroupRow{}
	latency := map[string]int64{}
	var order []string
	for _, r := range rows {
		label := r.DisplayName
		if label == "" {
			label = r.Username
		}
		if label == "" {
			label = r.UserID
		}
		g := byUID[r.UserID]
		if g == nil {
			g = &GroupRow{Key: label, ID: r.UserID}
			byUID[r.UserID] = g
			order = append(order, r.UserID)
		}
		g.CostUsd += r.Cost
		g.Requests += r.Requests
		g.InputTokens += r.Input
		g.OutputTokens += r.Output
		g.CachedInputTokens += r.CacheRead
		latency[r.UserID] += r.LatencySum
	}
	out := make([]GroupRow, 0, len(order))
	for _, uid := range order {
		g := byUID[uid]
		g.AvgLatencySeconds = avgLatencySeconds(latency[uid], g.Requests)
		g.CacheHitRatePct = cacheHitPct(g.CachedInputTokens, g.InputTokens)
		out = append(out, *g)
	}
	sortByRequests(out)
	return out, nil
}

type toolScan struct {
	Tool       string `db:"tool"`
	Requests   int64  `db:"requests"`
	LatencySum int64  `db:"latency_ms_sum"`
	Models     string `db:"models"` // comma-joined distinct models (split in Go)
}

// byTool breaks usage down by the tools a request OFFERED (attributes.actual.
// tool_names), unnesting the array so each offered tool counts once per request.
// The `attributes LIKE '%tool_names%'` prefilter narrows the jsonb cast to rows
// that actually carry the array (attributes is JSON text; empty/absent rows are
// skipped). Reports offered tools + latency + the distinct models that offered
// each (model names carry no comma, so a comma-join round-trips cleanly). Which
// tools were actually CALLED is a later phase.
func byTool(db *common.DatabaseManager, req Request) ([]ToolRow, error) {
	const q = `
		SELECT tool, count(*) AS requests, COALESCE(sum(latency_ms),0) AS latency_ms_sum,
		       COALESCE(string_agg(DISTINCT NULLIF(model,''), ',' ORDER BY NULLIF(model,'')),'') AS models
		FROM (
			SELECT jsonb_array_elements_text(a #> '{actual,tool_names}') AS tool, latency_ms, model
			FROM (
				SELECT NULLIF(attributes,'')::jsonb AS a, latency_ms, model
				FROM llm_gateway_usage
				WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
				  AND attributes LIKE '%tool_names%'
			) s
			WHERE a #> '{actual,tool_names}' IS NOT NULL
		) t
		GROUP BY tool
		ORDER BY requests DESC`
	var rows []toolScan
	if err := db.QueryAndScan(&rows, q, req.TenantID, req.StartDate, req.EndDate); err != nil {
		return nil, fmt.Errorf("usage: by-tool aggregation: %w", err)
	}
	out := make([]ToolRow, 0, len(rows))
	for _, r := range rows {
		var models []string
		if r.Models != "" {
			models = strings.Split(r.Models, ",")
		}
		out = append(out, ToolRow{Tool: r.Tool, Requests: r.Requests, AvgLatencySeconds: avgLatencySeconds(r.LatencySum, r.Requests), Models: models})
	}
	return out, nil
}

type labelScan struct {
	Label string `db:"label"`
	Count int64  `db:"count"`
}

// governanceStatusFilters is the shared status-class FILTER block — reused by the
// overall/by-provider outcomes and the outcome time series so all three partition
// status_code identically.
const governanceStatusFilters = `count(*) AS total,
		       count(*) FILTER (WHERE status_code BETWEEN 200 AND 299) AS success,
		       count(*) FILTER (WHERE status_code BETWEEN 400 AND 499) AS client_error,
		       count(*) FILTER (WHERE status_code BETWEEN 500 AND 599) AS server_error`

type outcomeScan struct {
	Provider    string `db:"provider"`
	Total       int64  `db:"total"`
	Success     int64  `db:"success"`
	ClientError int64  `db:"client_error"`
	ServerError int64  `db:"server_error"`
}

type seriesScan struct {
	Bucket      time.Time `db:"bucket"`
	Total       int64     `db:"total"`
	Success     int64     `db:"success"`
	ClientError int64     `db:"client_error"`
	ServerError int64     `db:"server_error"`
}

type latencyScan struct {
	P50 float64 `db:"p50"`
	P95 float64 `db:"p95"`
	P99 float64 `db:"p99"`
}

type routeScan struct {
	FromProvider string `db:"from_provider"`
	FromModel    string `db:"from_model"`
	ToProvider   string `db:"to_provider"`
	ToModel      string `db:"to_model"`
	Count        int64  `db:"count"`
}

func pctOf(n, total int64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}

// governance rolls up request outcomes + the P2 routing/DLP signals for the
// Governance tab, all over the same (tenant, window) the cost queries use:
// outcomes (overall · per provider · over time), tail latency, rejections and
// routing decisions by reason, the from→to substitution routes, and the DLP count.
// The reject-reason and DLP signals live in attributes.derived (JSON text); the
// `LIKE` prefilters narrow the jsonb cast to rows that carry them (same pattern as
// byTool). Postgres-shaped, like the rest of this package.
func governance(db *common.DatabaseManager, req Request) (*Governance, error) {
	g := &Governance{
		Rejects: []LabelCount{}, RoutingReasons: []LabelCount{},
		OutcomesByProvider: []ProviderOutcome{}, OutcomeSeries: []OutcomeBucket{}, SubstitutionRoutes: []RouteCount{},
	}
	args := []any{req.TenantID, req.StartDate, req.EndDate}
	const win = "tenant_id = $1 AND created_at >= $2 AND created_at < $3"

	// Outcomes per provider — rolled up to the overall Outcomes in one pass (which
	// upstream is failing, plus the top-line success/error mix).
	var provRows []outcomeScan
	if err := db.QueryAndScan(&provRows, `
		SELECT provider, `+governanceStatusFilters+`
		FROM llm_gateway_usage WHERE `+win+` GROUP BY provider ORDER BY total DESC`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance outcomes by provider: %w", err)
	}
	for _, r := range provRows {
		g.Outcomes.Success += r.Success
		g.Outcomes.ClientError += r.ClientError
		g.Outcomes.ServerError += r.ServerError
		g.Outcomes.Other += r.Total - r.Success - r.ClientError - r.ServerError
		errs := r.Total - r.Success
		g.OutcomesByProvider = append(g.OutcomesByProvider, ProviderOutcome{
			Provider: r.Provider, Requests: r.Total, Errors: errs, ErrorRatePct: pctOf(errs, r.Total),
		})
	}
	total := g.Outcomes.Success + g.Outcomes.ClientError + g.Outcomes.ServerError + g.Outcomes.Other
	g.Outcomes.ErrorRatePct = pctOf(total-g.Outcomes.Success, total)

	// Outcome trend per bucket (reliability chart). trunc is from a fixed allowlist.
	trunc := "day"
	if req.Granularity == "hour" {
		trunc = "hour"
	}
	var seriesRows []seriesScan
	if err := db.QueryAndScan(&seriesRows, fmt.Sprintf(`
		SELECT date_trunc('%s', created_at) AS bucket, %s
		FROM llm_gateway_usage WHERE `+win+` GROUP BY bucket ORDER BY bucket`, trunc, governanceStatusFilters), args...); err != nil {
		return nil, fmt.Errorf("usage: governance outcome series: %w", err)
	}
	for _, r := range seriesRows {
		g.OutcomeSeries = append(g.OutcomeSeries, OutcomeBucket{
			Bucket: r.Bucket, Success: r.Success, ClientError: r.ClientError,
			ServerError: r.ServerError, Other: r.Total - r.Success - r.ClientError - r.ServerError,
		})
	}

	// Tail latency over recorded latencies (avg hides the slow tail).
	var latRows []latencyScan
	if err := db.QueryAndScan(&latRows, `
		SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms),0) AS p50,
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),0) AS p95,
		       COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms),0) AS p99
		FROM llm_gateway_usage WHERE `+win+` AND latency_ms > 0`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance latency: %w", err)
	}
	if len(latRows) > 0 {
		g.Latency = LatencyPct{P50Seconds: latRows[0].P50 / 1000, P95Seconds: latRows[0].P95 / 1000, P99Seconds: latRows[0].P99 / 1000}
	}

	// Substitution routes: where cross-provider substitution actually sends traffic.
	var routeRows []routeScan
	if err := db.QueryAndScan(&routeRows, `
		SELECT COALESCE(requested_provider,'') AS from_provider, COALESCE(requested_model,'') AS from_model,
		       provider AS to_provider, model AS to_model, count(*) AS count
		FROM llm_gateway_usage
		WHERE `+win+` AND routing_reason = 'substitute'
		GROUP BY requested_provider, requested_model, provider, model
		ORDER BY count DESC LIMIT 50`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance substitution routes: %w", err)
	}
	for _, r := range routeRows {
		g.SubstitutionRoutes = append(g.SubstitutionRoutes, RouteCount{
			FromProvider: r.FromProvider, FromModel: r.FromModel, ToProvider: r.ToProvider, ToModel: r.ToModel, Requests: r.Count,
		})
	}

	// Routing decisions by reason (empty routing_reason = plain passthrough).
	var reasonRows []labelScan
	if err := db.QueryAndScan(&reasonRows, `
		SELECT COALESCE(NULLIF(routing_reason,''),'passthrough') AS label, count(*) AS count
		FROM llm_gateway_usage WHERE `+win+` GROUP BY label ORDER BY count DESC`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance routing reasons: %w", err)
	}
	for _, r := range reasonRows {
		g.RoutingReasons = append(g.RoutingReasons, LabelCount{Key: r.Label, Requests: r.Count})
	}

	// Rejections by reason (attributes.derived.reject_reason on edge-rejected rows).
	var rejectRows []labelScan
	if err := db.QueryAndScan(&rejectRows, `
		SELECT (NULLIF(attributes,'')::jsonb #>> '{derived,reject_reason}') AS label, count(*) AS count
		FROM llm_gateway_usage
		WHERE `+win+` AND attributes LIKE '%reject_reason%'
		  AND COALESCE(NULLIF(attributes,'')::jsonb #>> '{derived,reject_reason}','') <> ''
		GROUP BY label ORDER BY count DESC`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance rejects: %w", err)
	}
	for _, r := range rejectRows {
		g.Rejects = append(g.Rejects, LabelCount{Key: r.Label, Requests: r.Count})
	}

	// DLP hits (attributes.derived.dlp present — detect/redact rows and enforce blocks).
	if err := db.QueryRowAndScan(&g.DLPHits, `
		SELECT count(*) FROM llm_gateway_usage
		WHERE `+win+` AND attributes LIKE '%"dlp"%'
		  AND (NULLIF(attributes,'')::jsonb #> '{derived,dlp}') IS NOT NULL`, args...); err != nil {
		return nil, fmt.Errorf("usage: governance dlp: %w", err)
	}
	return g, nil
}

// ListRequest is the paginated recent-request query for the Requests tab. UserID,
// when set, scopes to one user (drill-down from the Users tab or the User filter).
type ListRequest struct {
	TenantID      string
	StartDate     time.Time
	EndDate       time.Time
	UserID        string
	Providers     []string // filter to these providers (empty = all)
	Models        []string // filter to these routed models (empty = all)
	Status        string   // "" (all) | "success" (2xx) | "error" (everything else)
	Tool          string   // filter to requests that offered this tool (drill-in from Tools)
	RoutingReason string   // filter to one routing_reason (drill-in from Governance intelligence)
	RejectReason  string   // filter to one derived.reject_reason (drill-in from Governance rejects)
	Dlp           bool     // filter to requests that tripped the egress filter (drill-in from Governance DLP)
	SessionID     string   // filter to one session/conversation (drill-in from a request's session)
	CallerUserID  string   // the requesting user (x-user-id); a row's body is viewable only by its own user
	Limit         int
	Offset        int
}

// RequestRow is one row of the recent-request list — the gateway analog of the LLM
// Analyser's per-conversation rows (here, one row per forwarded request).
type RequestRow struct {
	ID                string    `json:"id"` // usage row id; the key to fetch the captured body
	CreatedAt         time.Time `json:"created_at"`
	User              string    `json:"user"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	RequestedModel    string    `json:"requested_model"`
	RespondedModel    string    `json:"responded_model"` // the id the provider echoed back
	RoutingReason     string    `json:"routing_reason"`
	Surface           string    `json:"surface"` // "native" (passthrough mount) | "generic" (/v1) | ""
	StatusCode        int       `json:"status_code"`
	Streaming         bool      `json:"streaming"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	LatencyMs         int64     `json:"latency_ms"`
	CostUsd           float64   `json:"cost_usd"`
	SessionID         string    `json:"session_id"`
	SessionSource     string    `json:"session_source"` // header | metadata.user_id | inferred | ""
	// Dlp is set only when this request tripped the egress secret filter — the mode
	// that was active and which rules fired. nil (omitted) for the vast majority.
	Dlp *RequestDLP `json:"dlp,omitempty"`
	// CanViewBody is true only when body-logging is on AND the caller owns this
	// request — the UI shows the "view body" action on those rows. Server-authoritative;
	// the body-fetch endpoint re-checks ownership.
	CanViewBody bool `json:"can_view_body"`
}

// RequestDLP is the egress-filter outcome for a request that tripped it (mirrors
// attributes.derived.dlp): the active mode and the rule ids that fired.
type RequestDLP struct {
	Mode  string   `json:"mode"`  // detect | enforce | redact
	Rules []string `json:"rules"` // e.g. ["aws-access-key-id"]
}

// parseDLP decodes the attributes.derived.dlp object text into a RequestDLP,
// returning nil for the absent/empty/malformed case (a bad row shouldn't drop the
// whole request from the list).
func parseDLP(s string) *RequestDLP {
	if s == "" {
		return nil
	}
	var d RequestDLP
	if err := json.Unmarshal([]byte(s), &d); err != nil || d.Mode == "" {
		return nil
	}
	return &d
}

// RequestList is one page of recent requests plus the unpaged total (for the pager).
type RequestList struct {
	Rows   []RequestRow `json:"rows"`
	Total  int64        `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

type reqScan struct {
	ID             string    `db:"id"`
	CreatedAt      time.Time `db:"created_at"`
	UserID         string    `db:"user_id"`
	DisplayName    string    `db:"display_name"`
	Username       string    `db:"username"`
	Provider       string    `db:"provider"`
	Model          string    `db:"model"`
	RequestedModel string    `db:"requested_model"`
	RespondedModel string    `db:"responded_model"`
	RoutingReason  string    `db:"routing_reason"`
	Surface        string    `db:"surface"`
	DlpJSON        string    `db:"dlp_json"` // attributes.derived.dlp object as text ("" when absent)
	StatusCode     int       `db:"status_code"`
	Streaming      bool      `db:"streaming"`
	Input          int64     `db:"input_tokens"`
	Output         int64     `db:"output_tokens"`
	CacheRead      int64     `db:"cache_read_tokens"`
	CacheWrite     int64     `db:"cache_write_tokens"`
	LatencyMs      int64     `db:"latency_ms"`
	Cost           float64   `db:"cost_usd"`
	SessionID      string    `db:"session_id"`
	SessionSource  string    `db:"session_source"`
}

// listRequestsFilter builds the WHERE clause + args for ListRequests (count and
// page queries share it, so the pager total always matches the rows). Filters
// compose with AND; empty values mean "no filter". Extracted for unit testing.
func listRequestsFilter(req ListRequest) (string, []any) {
	where := "g.tenant_id = $1 AND g.created_at >= $2 AND g.created_at < $3"
	args := []any{req.TenantID, req.StartDate, req.EndDate}
	if req.UserID != "" {
		where += fmt.Sprintf(" AND g.user_id = $%d", len(args)+1)
		args = append(args, req.UserID)
	}
	if len(req.Providers) > 0 {
		where += inClause("g.provider", req.Providers, &args)
	}
	if len(req.Models) > 0 {
		where += inClause("g.model", req.Models, &args)
	}
	// The two status classes partition ALL rows — a row with no recorded status
	// (NULL/0, e.g. an aborted stream) counts as "error" — so success + error =
	// everything and no row silently vanishes from both filters. Written without
	// COALESCE so a plain index on status_code stays usable (sargable).
	switch req.Status {
	case "success":
		where += " AND g.status_code BETWEEN 200 AND 299"
	case "error":
		where += " AND (g.status_code IS NULL OR g.status_code NOT BETWEEN 200 AND 299)"
	}
	if req.Tool != "" {
		// Requests that offered this tool (attributes.actual.tool_names contains it).
		// jsonb_exists is the function form of `?` — the `?` operator would clash with
		// the driver's placeholder handling. The LIKE prefilter narrows the jsonb cast.
		where += fmt.Sprintf(" AND g.attributes LIKE '%%tool_names%%' AND jsonb_exists(NULLIF(g.attributes,'')::jsonb #> '{actual,tool_names}', $%d)", len(args)+1)
		args = append(args, req.Tool)
	}
	if req.RoutingReason != "" {
		// 'passthrough' is the synthetic label the Governance rollup gives an empty
		// routing_reason, so match empty rows for it rather than the literal string.
		if req.RoutingReason == "passthrough" {
			where += " AND COALESCE(g.routing_reason,'') = ''"
		} else {
			where += fmt.Sprintf(" AND g.routing_reason = $%d", len(args)+1)
			args = append(args, req.RoutingReason)
		}
	}
	if req.RejectReason != "" {
		where += fmt.Sprintf(" AND g.attributes LIKE '%%reject_reason%%' AND (NULLIF(g.attributes,'')::jsonb #>> '{derived,reject_reason}') = $%d", len(args)+1)
		args = append(args, req.RejectReason)
	}
	if req.Dlp {
		where += " AND g.attributes LIKE '%\"dlp\"%' AND (NULLIF(g.attributes,'')::jsonb #> '{derived,dlp}') IS NOT NULL"
	}
	if req.SessionID != "" {
		where += fmt.Sprintf(" AND g.session_id = $%d", len(args)+1)
		args = append(args, req.SessionID)
	}
	return where, args
}

// inClause appends vals to args and returns an " AND col IN ($n,…)" fragment with
// numbered placeholders (driver-agnostic — no array-binding support needed).
func inClause(col string, vals []string, args *[]any) string {
	ph := make([]string, len(vals))
	for i, v := range vals {
		*args = append(*args, v)
		ph[i] = fmt.Sprintf("$%d", len(*args))
	}
	return fmt.Sprintf(" AND %s IN (%s)", col, strings.Join(ph, ","))
}

// ListRequests returns a page of recent requests, newest first, with the cost that
// was snapshotted per row at write time. Limit is clamped to [1,200]; Total is the
// unpaged count for the pager.
func ListRequests(ctx context.Context, db *common.DatabaseManager, req ListRequest) (*RequestList, error) {
	if req.EndDate.Before(req.StartDate) {
		return nil, fmt.Errorf("usage: end_date must be >= start_date")
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := max(0, req.Offset)

	where, args := listRequestsFilter(req)

	var total int64
	// Scalar scan → QueryRowAndScan (db.Get); QueryAndScan is sqlx.Select and needs a slice.
	if err := db.QueryRowAndScan(&total, "SELECT count(*) FROM llm_gateway_usage g WHERE "+where, args...); err != nil {
		return nil, fmt.Errorf("usage: request count: %w", err)
	}

	q := fmt.Sprintf(`
		SELECT g.id, g.created_at, g.user_id, COALESCE(u.display_name,'') AS display_name,
		       COALESCE(u.username::text,'') AS username, g.provider, g.model,
		       COALESCE(g.requested_model,'') AS requested_model,
		       COALESCE(g.responded_model,'') AS responded_model,
		       COALESCE(g.routing_reason,'') AS routing_reason,
		       COALESCE(NULLIF(g.attributes,'')::jsonb #>> '{derived,surface}','') AS surface,
		       COALESCE((NULLIF(g.attributes,'')::jsonb #> '{derived,dlp}')::text,'') AS dlp_json,
		       COALESCE(g.status_code,0) AS status_code, COALESCE(g.streaming,false) AS streaming,
		       COALESCE(g.input_tokens,0) AS input_tokens, COALESCE(g.output_tokens,0) AS output_tokens,
		       COALESCE(g.cache_read_tokens,0) AS cache_read_tokens, COALESCE(g.cache_write_tokens,0) AS cache_write_tokens,
		       COALESCE(g.latency_ms,0) AS latency_ms,
		       COALESCE(g.cost_usd,0) AS cost_usd,
		       COALESCE(g.session_id,'') AS session_id,
		       COALESCE(NULLIF(g.attributes,'')::jsonb #>> '{derived,session_source}','') AS session_source
		FROM llm_gateway_usage g
		LEFT JOIN users u ON u.id::text = g.user_id
		WHERE %s
		ORDER BY g.created_at DESC
		LIMIT %d OFFSET %d`, where, limit, offset)
	var rows []reqScan
	if err := db.QueryAndScan(&rows, q, args...); err != nil {
		return nil, fmt.Errorf("usage: request list: %w", err)
	}

	// Body-view eligibility is server-authoritative: body-logging on AND the row is
	// the caller's own request. The body-fetch endpoint re-checks ownership.
	bodyEnabled := metering.BodyLoggingEnabled()
	out := make([]RequestRow, 0, len(rows))
	for _, r := range rows {
		user := r.DisplayName
		if user == "" {
			user = r.Username
		}
		if user == "" {
			user = r.UserID
		}
		out = append(out, RequestRow{
			ID:        r.ID,
			CreatedAt: r.CreatedAt, User: user, Provider: r.Provider, Model: r.Model,
			RequestedModel: r.RequestedModel, RespondedModel: r.RespondedModel, RoutingReason: r.RoutingReason, Surface: r.Surface,
			StatusCode: r.StatusCode, Streaming: r.Streaming,
			InputTokens: r.Input, OutputTokens: r.Output, CachedInputTokens: r.CacheRead,
			LatencyMs: r.LatencyMs,
			CostUsd:   r.Cost,
			SessionID: r.SessionID, SessionSource: r.SessionSource,
			Dlp:         parseDLP(r.DlpJSON),
			CanViewBody: bodyEnabled && req.CallerUserID != "" && r.UserID == req.CallerUserID,
		})
	}
	return &RequestList{Rows: out, Total: total, Limit: limit, Offset: offset}, nil
}

func timeSeries(db *common.DatabaseManager, req Request) (*TimeSeries, error) {
	trunc := "day"
	if req.Granularity == "hour" {
		trunc = "hour"
	}
	// Cost is summed from the per-row snapshot, so we group by bucket only (no need to
	// group by model for pricing). trunc is from a fixed allowlist above — safe to interpolate.
	q := fmt.Sprintf(`
		SELECT date_trunc('%s', created_at) AS bucket,
		       count(*) AS requests,
		       COALESCE(sum(input_tokens),0) AS input_tokens,
		       COALESCE(sum(output_tokens),0) AS output_tokens,
		       COALESCE(sum(cost_usd),0) AS cost_usd
		FROM llm_gateway_usage
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY bucket
		ORDER BY bucket`, trunc)
	var rows []tsRow
	if err := db.QueryAndScan(&rows, q, req.TenantID, req.StartDate, req.EndDate); err != nil {
		return nil, fmt.Errorf("usage: time-series aggregation: %w", err)
	}
	byBucket := map[time.Time]*TimeSeriesRow{}
	var order []time.Time
	for _, r := range rows {
		b := byBucket[r.Bucket]
		if b == nil {
			b = &TimeSeriesRow{Bucket: r.Bucket, Key: "overall"}
			byBucket[r.Bucket] = b
			order = append(order, r.Bucket)
		}
		b.Requests += r.Requests
		b.Tokens += r.Input + r.Output
		b.CostUsd += r.Cost
	}
	out := make([]TimeSeriesRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byBucket[t])
	}
	return &TimeSeries{Granularity: trunc, ByDimension: map[string][]TimeSeriesRow{"overall": out}}, nil
}
