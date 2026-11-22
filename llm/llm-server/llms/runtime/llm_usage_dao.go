package runtime

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ModelPricing captures all rate dimensions billed by an LLM provider.
// Standard rates apply when total prompt ≤ ContextThresholdTokens (or when
// the threshold is NULL → no tiering). LongCtx rates apply above that.
//
// Storage rate is per-token-hour and only Vertex/Gemini bills storage; for
// other providers the column is NULL → storage cost = 0 in the formula
// (which is correct, since they don't bill it).
//
// CacheCostPerMillionTokensPerHour is the legacy column kept for backward
// compatibility with old data. It will be dropped after backfill into
// CostPerMillionCachedStoragePerHour.
type ModelPricing struct {
	CostPerMillionInput  float64
	CostPerMillionOutput float64

	CostPerMillionCachedInput          sql.NullFloat64 // discounted read rate
	CostPerMillionCacheCreation        sql.NullFloat64 // cache write rate; NULL → use input rate
	CostPerMillionCachedStoragePerHour sql.NullFloat64 // storage rate; NULL → no storage cost
	ContextThresholdTokens             sql.NullInt64   // long-ctx threshold (e.g. 200K for Gemini)

	CostPerMillionInputLongCtx         sql.NullFloat64
	CostPerMillionOutputLongCtx        sql.NullFloat64
	CostPerMillionCachedInputLongCtx   sql.NullFloat64
	CostPerMillionCacheCreationLongCtx sql.NullFloat64

	// Legacy — to be removed after backfill into CostPerMillionCachedStoragePerHour.
	CacheCostPerMillionTokensPerHour sql.NullFloat64
}

// effectiveInputRate returns the input rate for the given total prompt size,
// applying the long-ctx tier when the threshold is configured AND exceeded
// AND a long-ctx rate is populated.
func (p *ModelPricing) effectiveInputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return p.CostPerMillionInputLongCtx.Float64
	}
	return p.CostPerMillionInput
}

// effectiveOutputRate is the rate billed for output tokens AND thinking tokens.
func (p *ModelPricing) effectiveOutputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return p.CostPerMillionOutputLongCtx.Float64
	}
	return p.CostPerMillionOutput
}

// effectiveCachedInputRate is the rate billed for tokens served from cache.
// NULL means we don't know — fall back to input rate (overestimates slightly,
// conservative for cost reporting).
func (p *ModelPricing) effectiveCachedInputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return nullableFloatOr(p.CostPerMillionCachedInputLongCtx, p.CostPerMillionInputLongCtx.Float64)
	}
	return nullableFloatOr(p.CostPerMillionCachedInput, p.CostPerMillionInput)
}

// effectiveCacheCreationRate is the rate billed when tokens are written into
// the cache for the first time. NULL → fall back to the input rate (matches
// Vertex/Gemini); Anthropic populates this column with 1.25x input.
func (p *ModelPricing) effectiveCacheCreationRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return nullableFloatOr(p.CostPerMillionCacheCreationLongCtx, p.CostPerMillionInputLongCtx.Float64)
	}
	return nullableFloatOr(p.CostPerMillionCacheCreation, p.CostPerMillionInput)
}

func (p *ModelPricing) useLongCtx(totalPrompt int) bool {
	return p.ContextThresholdTokens.Valid &&
		int64(totalPrompt) > p.ContextThresholdTokens.Int64 &&
		p.CostPerMillionInputLongCtx.Valid
}

// CalculateTotalCost computes the per-call cost in USD for a single LLM API
// call using the redesigned formula. Storage cost is NOT included here —
// it lives in llm_cache_lifecycle and is computed separately on read.
//
// Formula:
//
//	cost =   non_cached_input  × input_rate
//	       + cached_input      × cached_input_rate
//	       + cache_creation    × cache_creation_rate
//	       + (output + thinking) × output_rate
//
// All four rates are tier-aware: when total prompt size exceeds
// pricing.ContextThresholdTokens AND a long-ctx variant is populated, the
// long-ctx rate is used for every component of this call.
//
// Returns 0 if pricing is nil so callers don't have to nil-check.
func CalculateTotalCost(p *ModelPricing, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int) float64 {
	if p == nil {
		return 0
	}

	totalPrompt := nonCachedInputTokens + cachedInputTokens + cacheCreationTokens

	inputRate := p.effectiveInputRate(totalPrompt)
	outputRate := p.effectiveOutputRate(totalPrompt)
	cachedRate := p.effectiveCachedInputRate(totalPrompt)
	creationRate := p.effectiveCacheCreationRate(totalPrompt)

	const million = 1_000_000.0

	return (float64(nonCachedInputTokens)/million)*inputRate +
		(float64(cachedInputTokens)/million)*cachedRate +
		(float64(cacheCreationTokens)/million)*creationRate +
		(float64(outputTokens+thinkingTokens)/million)*outputRate
}

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

// CalculateCostBreakdown splits a single model call's cost into its priced token
// components, using the SAME tier-aware effective rates as CalculateTotalCost, so
// the components sum exactly to CalculateTotalCost(...). The output component
// folds in thinking tokens (billed at the output rate), matching the formula.
func CalculateCostBreakdown(p *ModelPricing, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int) CostBreakdown {
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

// GetConversationCosts returns pricing keyed "provider:model".
//
// tenantId selects whose prices apply: a tenant's own row for a
// (model, provider) wins over the built-in row, and an empty tenantId means
// built-in only. Pricing became tenant-scoped in V843 so that two tenants
// running the same model name at different negotiated rates stop overwriting
// each other — see the migration for why the key is two partial indexes.
func (chat *usageDAO) GetConversationCosts(models []string, tenantId string) (map[string]ModelPricing, error) {
	pricingMap := make(map[string]ModelPricing)

	// V726 onwards: pricing has explicit cached_input / creation / storage rates
	// plus tiered (long_ctx) variants for Gemini-style threshold pricing.
	// Old cache_cost_per_million_tokens_per_hour is kept until backfill so
	// reading it here is a temporary belt-and-braces measure.
	// DISTINCT ON keeps exactly one row per (provider, model). Ordering
	// tenant_id NULLS LAST puts the tenant's own row first, so it is the one
	// kept. Doing this in SQL rather than merging two result sets in Go also
	// avoids the row-duplication a plain OR-join would cause.
	query := `SELECT DISTINCT ON (provider_name, model_name)
	    provider_name, model_name,
	    cost_per_million_input_tokens,
	    cost_per_million_output_tokens,
	    cache_cost_per_million_tokens_per_hour,
	    cost_per_million_cached_input_tokens,
	    cost_per_million_cache_creation_tokens,
	    cost_per_million_cached_storage_per_hour,
	    context_threshold_tokens,
	    cost_per_million_input_tokens_long_ctx,
	    cost_per_million_output_tokens_long_ctx,
	    cost_per_million_cached_input_tokens_long_ctx,
	    cost_per_million_cache_creation_tokens_long_ctx
	FROM llm_model_pricing`

	var args []any
	var wheres []string
	if tenantId != "" {
		wheres = append(wheres, `(tenant_id IS NULL OR tenant_id = ?)`)
		args = append(args, tenantId)
	} else {
		wheres = append(wheres, `tenant_id IS NULL`)
	}
	if len(models) > 0 {
		wheres = append(wheres, `model_name IN (?)`)
		args = append(args, models)
	}
	query += ` WHERE ` + strings.Join(wheres, " AND ") +
		` ORDER BY provider_name, model_name, tenant_id NULLS LAST`

	// sqlx.In expands the IN slice and renumbers every placeholder; it is
	// required even without a models filter so the tenant arg gets rebound.
	var err error
	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, err
	}
	query = chat.dbManager.Db.Rebind(query)

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var (
			provider, model      string
			input, output        float64
			legacyCacheStorage   sql.NullFloat64
			cachedInput          sql.NullFloat64
			cacheCreation        sql.NullFloat64
			cachedStoragePerHour sql.NullFloat64
			ctxThreshold         sql.NullInt64
			inputLong            sql.NullFloat64
			outputLong           sql.NullFloat64
			cachedInputLong      sql.NullFloat64
			cacheCreationLong    sql.NullFloat64
		)
		if err := rows.Scan(
			&provider, &model,
			&input, &output,
			&legacyCacheStorage,
			&cachedInput,
			&cacheCreation,
			&cachedStoragePerHour,
			&ctxThreshold,
			&inputLong,
			&outputLong,
			&cachedInputLong,
			&cacheCreationLong,
		); err != nil {
			continue
		}

		// Until backfill drops the legacy column, prefer the new explicit
		// storage column; if NULL there but populated in the legacy column,
		// fall through to that. Same dollar value for now.
		storage := cachedStoragePerHour
		if !storage.Valid && legacyCacheStorage.Valid {
			storage = legacyCacheStorage
		}

		pricingMap[provider+":"+model] = ModelPricing{
			CostPerMillionInput:                input,
			CostPerMillionOutput:               output,
			CostPerMillionCachedInput:          cachedInput,
			CostPerMillionCacheCreation:        cacheCreation,
			CostPerMillionCachedStoragePerHour: storage,
			ContextThresholdTokens:             ctxThreshold,
			CostPerMillionInputLongCtx:         inputLong,
			CostPerMillionOutputLongCtx:        outputLong,
			CostPerMillionCachedInputLongCtx:   cachedInputLong,
			CostPerMillionCacheCreationLongCtx: cacheCreationLong,
			CacheCostPerMillionTokensPerHour:   legacyCacheStorage,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pricingMap, nil
}

// GetImageSupportCatalog returns the explicit vision-capability verdicts recorded
// in llm_model_pricing.supports_image_input, keyed by "provider:model": the
// built-in rows when tenantId is empty, otherwise that tenant's own rows. Rows
// where the column is NULL are omitted — callers must treat a missing key as
// "unknown", not "false", and fall back to their own default heuristic.
func (chat *usageDAO) GetImageSupportCatalog(tenantId string) (map[string]bool, error) {
	query := `SELECT provider_name, model_name, supports_image_input
		FROM llm_model_pricing
		WHERE supports_image_input IS NOT NULL AND tenant_id IS NULL`
	args := []any{}
	if tenantId != "" {
		query = `SELECT provider_name, model_name, supports_image_input
		FROM llm_model_pricing
		WHERE supports_image_input IS NOT NULL AND tenant_id = $1`
		args = append(args, tenantId)
	}
	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("GetImageSupportCatalog: query failed: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	catalog := make(map[string]bool)
	for rows.Next() {
		var provider, model string
		var supportsImage bool
		if err := rows.Scan(&provider, &model, &supportsImage); err != nil {
			return nil, fmt.Errorf("GetImageSupportCatalog: scan failed: %w", err)
		}
		catalog[provider+":"+model] = supportsImage
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return catalog, nil
}

// GetConversationCost computes the per-call cost in USD for a single LLM
// invocation by looking up the (provider, model) row in llm_model_pricing
// and applying CalculateTotalCost. Returns -1 with an error if pricing
// cannot be found for the given (provider, model).
func (chat *usageDAO) GetConversationCost(
	provider string,
	model string,
	nonCachedInputTokens int,
	cachedInputTokens int,
	cacheCreationTokens int,
	outputTokens int,
	thinkingTokens int,
	// Whose prices apply. Empty means built-in only — see GetConversationCosts.
	tenantId string,
) (float64, error) {
	pricingMap, err := chat.GetConversationCosts([]string{model}, tenantId)
	if err != nil {
		return -1, err
	}
	pricing, ok := pricingMap[provider+":"+model]
	if !ok {
		return -1, fmt.Errorf("found no pricing data for model %s and provider %s", model, provider)
	}
	return CalculateTotalCost(
		&pricing,
		nonCachedInputTokens,
		cachedInputTokens,
		cacheCreationTokens,
		outputTokens,
		thinkingTokens,
	), nil
}

func (chat *usageDAO) InsertTokenUsage(record *TokenUsageRecord) error {
	if record == nil {
		return fmt.Errorf("token usage record is nil")
	}

	// conv looks the owning conversation up once. Its tenant, session and
	// account are copied onto the row so the spend still counts toward budgets
	// after the conversation is deleted. If a conversation id is given but that
	// conversation is already gone (a call that finished after the delete), the
	// row is stored unlinked and without its text, like the rows the delete
	// cleared: there is no foreign key to reject it (V986).
	query := `
		WITH conv AS (
			SELECT c.tenant_id, c.session_id, c.account_id FROM llm_conversations c WHERE c.id = $1::uuid
		), live AS (
			SELECT $1::uuid IS NULL OR EXISTS (SELECT 1 FROM conv) AS ok
		)
		INSERT INTO llm_conversation_token_usage (
			conversation_id, message_id, agent_id, agent_name, account_id, user_id,
			llm_provider, llm_model,
			input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
			is_cache_hit, cache_hit_rate,
			retry_attempt, fallback_from_model, fallback_chain,
			latency_seconds, request_status, error_message,
			content_length, stop_reason, cache_ttl_minutes,
			prompt_messages, response_content,
			thinking_tokens,
			ttft_ms, itl_ms_avg, tokens_per_second, was_streaming,
			cost_usd,
			model_tier, task_type,
			llm_config_source, id,
			tenant_id, session_id, conversation_account_id
		) VALUES (
			CASE WHEN (SELECT ok FROM live) THEN $1::uuid END,
			CASE WHEN (SELECT ok FROM live) THEN $2::uuid END,
			$3, $4, $5, $6,
			$7, $8,
			$9, $10, $11, $12,
			$13, $14,
			$15, $16, $17,
			$18, $19, CASE WHEN (SELECT ok FROM live) THEN $20::text END,
			$21, $22, $23,
			CASE WHEN (SELECT ok FROM live) THEN $24::text END,
			CASE WHEN (SELECT ok FROM live) THEN $25::text END,
			$26,
			$27, $28, $29, $30,
			$31,
			$32, $33,
			$34, COALESCE($35::uuid, gen_random_uuid()),
			(SELECT tenant_id FROM conv),
			(SELECT session_id FROM conv),
			(SELECT account_id FROM conv)
		)
	`

	// Background-job rows (memory consolidators, maintenance) have no owning
	// conversation or account — they're system cost, not customer-billable.
	// Pass NULL for those columns so the insert succeeds; V760 made both
	// nullable on llm_conversation_token_usage. Budget rollups that INNER
	// JOIN llm_conversations naturally exclude these rows.
	convID := nullableUUID(record.ConversationID)
	acctID := nullableUUID(record.AccountID)
	msgID := nullableUUID(record.MessageID)
	usrID := nullableUUID(record.UserID)

	_, err := chat.dbManager.Db.Exec(query,
		convID, msgID, record.AgentID, record.AgentName,
		acctID, usrID,
		record.LLMProvider, record.LLMModel,
		record.InputTokens, record.OutputTokens, record.CachedInputTokens, record.CacheCreationTokens,
		record.IsCacheHit, record.CacheHitRate,
		record.RetryAttempt, record.FallbackFromModel, record.FallbackChain,
		record.LatencySeconds, record.RequestStatus, record.ErrorMessage,
		record.ContentLength, record.StopReason, record.CacheTTLMinutes,
		record.PromptMessages, record.ResponseContent,
		record.ThinkingTokens,
		record.TTFTMs, record.ITLMsAvg, record.TokensPerSecond, record.WasStreaming,
		record.CostUsd,
		record.ModelTier, record.TaskType,
		record.LLMConfigSource, nullableUUID(record.ID),
	)

	if err != nil {
		// If the insert failed due to a foreign key constraint on agent_id (agent record doesn't exist),
		// retry with agent_id = NULL to avoid losing token usage data
		if record.AgentID != nil && strings.Contains(err.Error(), "llm_conversation_token_usage_agent_fk") {
			slog.Warn("InsertTokenUsage: agent_id FK violation, retrying with NULL agent_id", "agent_id", *record.AgentID, "agent_name", record.AgentName)
			record.AgentID = nil
			_, retryErr := chat.dbManager.Db.Exec(query,
				convID, msgID, record.AgentID, record.AgentName,
				acctID, usrID,
				record.LLMProvider, record.LLMModel,
				record.InputTokens, record.OutputTokens, record.CachedInputTokens, record.CacheCreationTokens,
				record.IsCacheHit, record.CacheHitRate,
				record.RetryAttempt, record.FallbackFromModel, record.FallbackChain,
				record.LatencySeconds, record.RequestStatus, record.ErrorMessage,
				record.ContentLength, record.StopReason, record.CacheTTLMinutes,
				record.PromptMessages, record.ResponseContent,
				record.ThinkingTokens,
				record.TTFTMs, record.ITLMsAvg, record.TokensPerSecond, record.WasStreaming,
				record.CostUsd,
				record.ModelTier, record.TaskType,
				record.LLMConfigSource, nullableUUID(record.ID),
			)
			if retryErr != nil {
				return fmt.Errorf("failed to insert token usage (retry without agent_id): %w", retryErr)
			}
			return nil
		}
		return fmt.Errorf("failed to insert token usage: %w", err)
	}

	return nil
}

func nullableFloatOr(v sql.NullFloat64, fallback float64) float64 {
	if v.Valid {
		return v.Float64
	}
	return fallback
}

// nullableUUID converts an empty string to a nil interface so the postgres
// driver inserts SQL NULL instead of trying to parse "" as a uuid.
// Returns the original string when non-empty.
func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
