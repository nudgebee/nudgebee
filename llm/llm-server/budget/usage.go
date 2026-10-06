package budget

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// enforcedUsageCacheNS holds the cost totals CheckBudgetLimits enforces against.
// That check runs before every chat message and investigation, with a cost
// query per enabled limit (0.1-0.5s warm, over 10s cold on dev), and it used to
// block the chat accept on every cache miss. The budget status reads stay live.
const enforcedUsageCacheNS = "llm_budget_enforced_usage"

// Staleness policy for enforced totals (see docs/architecture-decisions.md):
//   - under enforcedUsageTTL old: served as is;
//   - under enforcedUsageMaxStale old: served immediately while one background
//     refresh per key updates it, so enforcement can lag real spend by up to
//     enforcedUsageMaxStale, and only right after a quiet period on that key;
//   - older, or absent: queried synchronously.
const (
	enforcedUsageTTL      = 60 * time.Second
	enforcedUsageMaxStale = 5 * time.Minute
	// A hung query must not hold a key's refresh (and every check that joins it) forever.
	// On timeout the check fails open, as it already does on any query error.
	enforcedUsageQueryTimeout = 30 * time.Second
)

// usageNow is the clock for the staleness policy; tests replace it.
var usageNow = time.Now

// usageRefreshes collapses concurrent queries for the same key on this pod,
// both synchronous misses and background refreshes.
var usageRefreshes singleflight.Group

// backgroundRefreshes marks keys with a background refresh in flight on this pod, so a
// burst of stale reads starts one goroutine per key instead of one per request.
var backgroundRefreshes sync.Map

// cachedUsage stamps its own times: the default in_memory backend (bigcache)
// ignores per-entry expiration, so the store's TTL alone can't be relied on.
type cachedUsage struct {
	Cost      float64   `json:"cost"`
	ExpiresAt time.Time `json:"expires_at"`
	FetchedAt time.Time `json:"fetched_at"`
}

func init() {
	common.CacheCreateNamespace(enforcedUsageCacheNS, common.CacheNamespaceWithExpiration(enforcedUsageMaxStale))
}

// enforcedUsageKey includes the current UTC day or month, so a total cached
// just before a period rolls over is not reused after it (DATE_TRUNC runs in
// the database's UTC session timezone).
func enforcedUsageKey(entityType, entityId, module, period string, now time.Time) string {
	bucket := now.UTC().Format("2006-01")
	if period == "day" {
		bucket = now.UTC().Format("2006-01-02")
	}
	return entityType + ":" + entityId + ":" + module + ":" + period + ":" + bucket
}

// cachedEntityTokenUsage is getEntityTokenUsage behind the staleness policy above.
// Only a successful result is cached, so a failed query is retried next time,
// and a failed background refresh leaves the previous total in place.
func cachedEntityTokenUsage(dbManager *common.DatabaseManager, entityType, entityId, module, period string) (float64, error) {
	now := usageNow()
	key := enforcedUsageKey(entityType, entityId, module, period, now)
	if data, ok := common.CacheGet(enforcedUsageCacheNS, key); ok {
		var cached cachedUsage
		if err := common.UnmarshalJson(data, &cached); err == nil {
			if now.Before(cached.ExpiresAt) {
				return cached.Cost, nil
			}
			// Entries without FetchedAt predate this policy and are only served fresh.
			if !cached.FetchedAt.IsZero() && now.Before(cached.FetchedAt.Add(enforcedUsageMaxStale)) {
				refreshUsageInBackground(dbManager, key, entityType, entityId, module, period)
				return cached.Cost, nil
			}
		}
	}
	return refreshUsage(dbManager, key, entityType, entityId, module, period)
}

// refreshUsage queries and caches one total, sharing an in-flight query for the same key.
func refreshUsage(dbManager *common.DatabaseManager, key, entityType, entityId, module, period string) (float64, error) {
	v, err, _ := usageRefreshes.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), enforcedUsageQueryTimeout)
		defer cancel()
		cost, err := getEntityTokenUsageContext(ctx, dbManager, entityType, entityId, module, period)
		if err != nil {
			return 0.0, err
		}
		fetchedAt := usageNow()
		data, err := common.MarshalJson(cachedUsage{Cost: cost, ExpiresAt: fetchedAt.Add(enforcedUsageTTL), FetchedAt: fetchedAt})
		if err != nil {
			slog.Debug("budget: unable to serialize usage for cache", "error", err, "key", key)
			return cost, nil
		}
		if err := common.CacheSet(enforcedUsageCacheNS, key, data, common.CacheSetWithExpiration(enforcedUsageMaxStale)); err != nil {
			slog.Debug("budget: unable to cache usage", "error", err, "key", key)
		}
		return cost, nil
	})
	if err != nil {
		return 0, err
	}
	return v.(float64), nil
}

// refreshUsageInBackground updates a stale total without making the caller wait.
// singleflight keeps it to one query per key on this pod even under a burst.
func refreshUsageInBackground(dbManager *common.DatabaseManager, key, entityType, entityId, module, period string) {
	if _, running := backgroundRefreshes.LoadOrStore(key, struct{}{}); running {
		return
	}
	go func() {
		defer backgroundRefreshes.Delete(key)
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("budget: panic refreshing usage", "panic", recovered, "key", key, "stack", string(debug.Stack()))
			}
		}()
		if _, err := refreshUsage(dbManager, key, entityType, entityId, module, period); err != nil {
			slog.Warn("budget: background usage refresh failed; keeping the previous total", "error", err, "key", key)
		}
	}()
}

// --- Monthly usage (existing, refactored) ---

// GetTenantTokenUsage calculates the total token usage cost for a tenant in the current month
func GetTenantTokenUsage(dbManager *common.DatabaseManager, tenantId string, module string) (float64, error) {
	return getEntityTokenUsage(dbManager, "tenant", tenantId, module, "month")
}

// GetAccountTokenUsage calculates the total token usage cost for an account in the current month
func GetAccountTokenUsage(dbManager *common.DatabaseManager, accountId string, module string) (float64, error) {
	return getEntityTokenUsage(dbManager, "account", accountId, module, "month")
}

// GetTenantConversationCount counts the total conversations for a tenant in the current month
func GetTenantConversationCount(dbManager *common.DatabaseManager, tenantId string, module string) (int, error) {
	return getEntityConversationCount(dbManager, "tenant", tenantId, module, "month")
}

// --- Daily usage (new) ---

// GetTenantDailyTokenUsage calculates the total token usage cost for a tenant today
func GetTenantDailyTokenUsage(dbManager *common.DatabaseManager, tenantId string, module string) (float64, error) {
	return getEntityTokenUsage(dbManager, "tenant", tenantId, module, "day")
}

// GetAccountDailyTokenUsage calculates the total token usage cost for an account today
func GetAccountDailyTokenUsage(dbManager *common.DatabaseManager, accountId string, module string) (float64, error) {
	return getEntityTokenUsage(dbManager, "account", accountId, module, "day")
}

// GetTenantDailyConversationCount counts the total conversations for a tenant today
func GetTenantDailyConversationCount(dbManager *common.DatabaseManager, tenantId string, module string) (int, error) {
	return getEntityConversationCount(dbManager, "tenant", tenantId, module, "day")
}

// GetAccountDailyConversationCount counts the total conversations for an account today
func GetAccountDailyConversationCount(dbManager *common.DatabaseManager, accountId string, module string) (int, error) {
	return getEntityConversationCount(dbManager, "account", accountId, module, "day")
}

// GetAccountConversationCount counts the total conversations for an account in the current month
func GetAccountConversationCount(dbManager *common.DatabaseManager, accountId string, module string) (int, error) {
	return getEntityConversationCount(dbManager, "account", accountId, module, "month")
}

// --- Internal helpers ---

// validPeriods is a whitelist of allowed period values for SQL DATE_TRUNC
var validPeriods = map[string]bool{
	"month": true,
	"day":   true,
}

// validatePeriod checks if the period is valid (prevents SQL injection in DATE_TRUNC)
func validatePeriod(period string) error {
	if !validPeriods[period] {
		return fmt.Errorf("invalid period: %s", period)
	}
	return nil
}

// getEntityConversationCount counts conversations for an entity in the given period
func getEntityConversationCount(dbManager *common.DatabaseManager, entityType, entityId, module, period string) (int, error) {
	if err := validatePeriod(period); err != nil {
		return 0, err
	}
	filter, ok := moduleQueryFilters[module]
	if !ok {
		return 0, fmt.Errorf("invalid module: %s", module)
	}

	var whereClause string
	switch entityType {
	case "tenant":
		whereClause = "c.tenant_id = $1"
	case "account":
		whereClause = "c.account_id = $1"
	default:
		return 0, fmt.Errorf("invalid entity type: %s", entityType)
	}

	query := fmt.Sprintf(`
		SELECT COUNT(DISTINCT c.id) as count
		FROM llm_conversations c
		WHERE %s
		AND c.created_at >= DATE_TRUNC('%s', CURRENT_TIMESTAMP)
		AND c.created_at < DATE_TRUNC('%s', CURRENT_TIMESTAMP) + INTERVAL '1 %s'
	`, whereClause, period, period, period) + filter

	var count int
	err := dbManager.Db.Get(&count, query, entityId)
	if err != nil {
		slog.Error("getEntityConversationCount: error executing query",
			"error", err, "entity_type", entityType, "entity_id", entityId, "module", module, "period", period)
		return 0, err
	}

	slog.Debug("getEntityConversationCount: count retrieved",
		"entity_type", entityType, "entity_id", entityId,
		"module", module, "period", period, "count", count)

	return count, nil
}

// getEntityTokenUsage calculates token usage cost for an entity in the given period.
// Optimization: Cost is calculated in a single SQL query using a JOIN with llm_model_pricing.
func getEntityTokenUsage(dbManager *common.DatabaseManager, entityType string, entityId string, module string, period string) (float64, error) {
	return getEntityTokenUsageContext(context.Background(), dbManager, entityType, entityId, module, period)
}

func getEntityTokenUsageContext(ctx context.Context, dbManager *common.DatabaseManager, entityType string, entityId string, module string, period string) (float64, error) {
	if err := validatePeriod(period); err != nil {
		return 0, err
	}
	filter, ok := moduleQueryFilters[module]
	if !ok {
		return 0, fmt.Errorf("invalid module: %s", module)
	}

	var rollupEntity, rawEntity, storageEntity string
	switch entityType {
	case "tenant":
		rollupEntity, rawEntity, storageEntity = "r.tenant_id = $1", "u.tenant_id = $1", "cl.tenant_id = $1"
	case "account":
		rollupEntity, rawEntity, storageEntity = "r.account_id = $1", "u.account_id = $1", "cl.account_id = $1"
	default:
		return 0.0, fmt.Errorf("invalid entity type: %s", entityType)
	}

	// Spend is counted on the day each call ran. It comes from the daily rollup
	// when every day of the period is built and fresh, and otherwise from the
	// usage rows directly (same attribution, same prices, slower).
	callCost := rawCallCostSQL(rawEntity, period, strings.ReplaceAll(filter, "c.session_id", "u.session_id"))
	if config.Config.BudgetUsageRollupEnabled {
		complete, err := rollupPeriodComplete(ctx, dbManager, period)
		switch {
		case err != nil:
			slog.Warn("getEntityTokenUsage: unable to read rollup state; using the raw query", "error", err, "period", period)
		case !complete:
			slog.Warn("getEntityTokenUsage: usage rollup not complete for the period; using the raw query", "period", period)
		default:
			callCost = rollupCallCostSQL(rollupEntity, period, module)
		}
	}

	// Storage cost lives in llm_cache_lifecycle, queried separately and summed.
	query := fmt.Sprintf(`
		WITH per_call_cost AS (%s),
		lifecycle_storage AS (
			SELECT SUM(
				(cl.cached_tokens / 1000000.0)
				* COALESCE(p.cost_per_million_cached_storage_per_hour, 0)
				* GREATEST(0, EXTRACT(EPOCH FROM (
					COALESCE(cl.invalidated_at, LEAST(now(), cl.expires_at)) - cl.created_at
				))) / 3600
			) AS cost
			FROM llm_cache_lifecycle cl
			LEFT JOIN llm_model_pricing p
			    ON p.model_name = cl.llm_model AND p.provider_name = cl.llm_provider AND p.tenant_id IS NULL
			WHERE %s
			AND cl.created_at >= DATE_TRUNC('%s', CURRENT_TIMESTAMP)
			AND cl.created_at < DATE_TRUNC('%s', CURRENT_TIMESTAMP) + INTERVAL '1 %s'
		)
		SELECT
			COALESCE((SELECT cost FROM per_call_cost), 0)
		  + COALESCE((SELECT cost FROM lifecycle_storage), 0)
		  AS total_cost
	`, callCost, storageEntity, period, period, period)

	var totalCost sql.NullFloat64
	err := dbManager.Db.GetContext(ctx, &totalCost, query, entityId)
	if err != nil {
		slog.Error("getEntityTokenUsage: error executing unified cost query",
			"error", err, "entity_type", entityType, "entity_id", entityId, "period", period)
		return 0.0, err
	}

	if !totalCost.Valid {
		return 0.0, nil
	}

	return totalCost.Float64, nil
}

// callCostExpr prices one usage row, or one rollup row of summed tokens, with
// the built-in model prices; long-context rows use the long-context rates.
// Every term is linear in the token counts, and the non-cached input is
// clamped per row before it is summed, so pricing a rollup of sums gives the
// same total as pricing the rows one by one.
func callCostExpr(longCtx, nonCached, cached, cacheCreation, output string) string {
	return fmt.Sprintf(`CASE WHEN %[1]s THEN
			%[2]s * p.cost_per_million_input_tokens_long_ctx
			+ %[3]s * COALESCE(p.cost_per_million_cached_input_tokens_long_ctx, p.cost_per_million_input_tokens_long_ctx)
			+ %[4]s * COALESCE(p.cost_per_million_cache_creation_tokens_long_ctx, p.cost_per_million_input_tokens_long_ctx)
			+ %[5]s * p.cost_per_million_output_tokens_long_ctx
		ELSE
			%[2]s * p.cost_per_million_input_tokens
			+ %[3]s * COALESCE(p.cost_per_million_cached_input_tokens, p.cost_per_million_input_tokens)
			+ %[4]s * COALESCE(p.cost_per_million_cache_creation_tokens, p.cost_per_million_input_tokens)
			+ %[5]s * p.cost_per_million_output_tokens
		END / 1000000.0`, longCtx, nonCached, cached, cacheCreation, output)
}

// rawCallCostSQL sums the period's call cost from the usage rows, attributed
// as in usageRowAttributionSQL and bucketed by when each call ran.
func rawCallCostSQL(entity, period, moduleFilter string) string {
	cost := callCostExpr(
		`p.context_threshold_tokens IS NOT NULL
			AND (u.input_tokens + COALESCE(u.cache_creation_tokens, 0)) > p.context_threshold_tokens
			AND p.cost_per_million_input_tokens_long_ctx IS NOT NULL`,
		`GREATEST(u.input_tokens - COALESCE(u.cached_input_tokens, 0), 0)`,
		`COALESCE(u.cached_input_tokens, 0)`,
		`COALESCE(u.cache_creation_tokens, 0)`,
		`(u.output_tokens + COALESCE(u.thinking_tokens, 0))`)
	return fmt.Sprintf(`
			SELECT SUM(%s) AS cost
			FROM (%s
				WHERE t.created_at >= DATE_TRUNC('%s', CURRENT_TIMESTAMP)
				AND t.created_at < DATE_TRUNC('%s', CURRENT_TIMESTAMP) + INTERVAL '1 %s'
			) u
			LEFT JOIN llm_model_pricing p
			    ON p.model_name = u.llm_model AND p.provider_name = u.llm_provider AND p.tenant_id IS NULL
			WHERE u.tenant_id IS NOT NULL AND %s %s`,
		cost, usageRowAttributionSQL, period, period, period, entity, moduleFilter)
}

// rollupCallCostSQL sums the period's call cost from llm_usage_daily.
func rollupCallCostSQL(entity, period, module string) string {
	cost := callCostExpr("r.long_ctx", "r.non_cached_input_tokens", "r.cached_input_tokens",
		"r.cache_creation_tokens", "r.output_tokens")
	return fmt.Sprintf(`
			SELECT SUM(%s) AS cost
			FROM llm_usage_daily r
			LEFT JOIN llm_model_pricing p
			    ON p.model_name = r.llm_model AND p.provider_name = r.llm_provider AND p.tenant_id IS NULL
			WHERE %s AND r.module = '%s'
			AND r.day >= DATE_TRUNC('%s', CURRENT_TIMESTAMP)::date
			AND r.day <= CURRENT_DATE`,
		cost, entity, module, period)
}
