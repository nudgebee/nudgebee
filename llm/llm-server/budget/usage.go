package budget

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/llm/common"
)

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
			    ON p.model_name = t.llm_model AND p.provider_name = t.llm_provider
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
			    ON p.model_name = cl.llm_model AND p.provider_name = cl.llm_provider
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
	err := dbManager.Db.Get(&totalCost, query, entityId)
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
