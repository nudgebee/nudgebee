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

	var whereClause string
	switch entityType {
	case "tenant":
		whereClause = "c.tenant_id = $1"
	case "account":
		whereClause = "c.account_id = $1"
	default:
		return 0.0, fmt.Errorf("invalid entity type: %s", entityType)
	}

	// Per-call cost: compute on read using the redesigned formula. Tier
	// (long_ctx) check is applied per row using the call's own prompt size,
	// then aggregated. Storage cost lives in llm_cache_lifecycle, queried
	// separately and summed below.
	query := fmt.Sprintf(`
		WITH per_call_cost AS (
			SELECT SUM(
				CASE
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
				END / 1000000.0
			) AS cost
			FROM llm_conversation_token_usage t
			INNER JOIN llm_conversations c ON c.id = t.conversation_id
			LEFT JOIN llm_model_pricing p
			    ON p.model_name = t.llm_model AND p.provider_name = t.llm_provider AND p.tenant_id IS NULL
			WHERE %s
			AND c.created_at >= DATE_TRUNC('%s', CURRENT_TIMESTAMP)
			AND c.created_at < DATE_TRUNC('%s', CURRENT_TIMESTAMP) + INTERVAL '1 %s'
			%s
		),
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
	`,
		whereClause, period, period, period, filter,
		strings.ReplaceAll(whereClause, "c.", "cl."), period, period, period,
	)

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
