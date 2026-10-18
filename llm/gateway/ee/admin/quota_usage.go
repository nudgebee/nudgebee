package admin

import (
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/ratelimit"
)

// windowUsageSQL sums a quota row r's settled use from llm_gateway_usage in its current UTC
// window: requests are rows the gateway didn't refuse (a refusal row carries
// derived.reject_reason and costs nothing), tokens are total_tokens, cost is cost_usd. It reads
// usage rows, not the limiter's counter, which also holds in-flight estimates and is per pod
// without Redis. The model test mirrors ratelimit.ModelMatches against served and requested model.
const windowUsageSQL = `
	SELECT CASE r.metric WHEN 'cost' THEN COALESCE(sum(u.cost_usd), 0)
	                     WHEN 'tokens' THEN COALESCE(sum(u.total_tokens), 0)
	                     ELSE count(*) FILTER (WHERE u.attributes NOT LIKE '%reject_reason%'
	                          OR (NULLIF(u.attributes, '')::jsonb #> '{derived,reject_reason}') IS NULL) END AS used
	  FROM llm_gateway_usage u
	 WHERE u.tenant_id = r.tenant_id
	   AND u.created_at >= date_trunc(r.period, now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
	   AND (r.user_id = '' OR u.user_id = r.user_id)
	   AND (r.api_key_id = '' OR u.api_key_id = r.api_key_id)
	   AND (r.model = '' OR lower(r.model) IN (lower(u.model), lower(u.requested_model),
	        lower(u.provider || '/' || u.model), lower(u.requested_provider || '/' || u.requested_model)))`

// usageByLimit returns each windowed quota's settled use for the tenant, keyed by row id.
func usageByLimit(db *common.DatabaseManager, tenantID string) (map[string]float64, error) {
	var rows []struct {
		ID   string  `db:"id"`
		Used float64 `db:"used"`
	}
	if err := db.QueryAndScan(&rows,
		`SELECT r.id, x.used
		   FROM llm_gateway_rate_limits r
		  CROSS JOIN LATERAL (`+windowUsageSQL+`) x
		  WHERE r.tenant_id = $1 AND r.metric <> 'concurrent' AND r.period IN ('minute', 'hour', 'day', 'month')`,
		tenantID); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Used
	}
	return out, nil
}

// fillUsage sets used and resets_at on each windowed row; rows missing from used keep both unset.
func fillUsage(rows []rateLimitRow, used map[string]float64, now time.Time) {
	for i := range rows {
		v, ok := used[rows[i].ID]
		if !ok || rows[i].Metric == ratelimit.MetricConcurrent {
			continue
		}
		reset := ratelimit.ResetAt(rows[i].Period, now)
		rows[i].Used, rows[i].ResetsAt = &v, &reset
	}
}
