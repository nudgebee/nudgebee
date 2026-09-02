package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
)

// Tenant-supplied model pricing.
//
// llm_model_pricing carries both the built-in rates that migrations seed
// (tenant_id IS NULL) and each tenant's own rates (tenant_id set) — see V843.
// A tenant row wins over the built-in for the same (model_name, provider_name),
// which is what lets a tenant price a custom OpenAI-compatible endpoint or
// record a negotiated rate on a built-in provider.
//
// These live outside IConversationDao on purpose: they are an administrative
// surface used by two RPC handlers, not part of the conversation read path that
// every agent depends on, and keeping them separate avoids widening an
// interface that several fakes implement.

// ModelPriceRow is one row as the pricing UI sees it. IsBuiltIn distinguishes a
// rate this tenant set from the shipped default, so the UI can label them
// differently and offer "override" rather than "edit" on the latter.
type ModelPriceRow struct {
	ModelName    string  `json:"model_name"`
	ProviderName string  `json:"provider_name"`
	InputPerM    float64 `json:"cost_per_million_input_tokens"`
	OutputPerM   float64 `json:"cost_per_million_output_tokens"`

	CachedInputPerM   *float64 `json:"cost_per_million_cached_input_tokens,omitempty"`
	CacheCreationPerM *float64 `json:"cost_per_million_cache_creation_tokens,omitempty"`

	// Long-context tier. Providers like Gemini charge a higher rate once a
	// prompt crosses a threshold; when ContextThresholdTokens is set and the
	// long-ctx input rate is populated, CalculateTotalCost switches every
	// component of the call to the long rates (see modelPricing.useLongCtx).
	ContextThresholdTokens *int64   `json:"context_threshold_tokens,omitempty"`
	InputPerMLongCtx       *float64 `json:"cost_per_million_input_tokens_long_ctx,omitempty"`
	OutputPerMLongCtx      *float64 `json:"cost_per_million_output_tokens_long_ctx,omitempty"`

	IsBuiltIn bool    `json:"is_built_in"`
	UpdatedAt *string `json:"pricing_updated_at,omitempty"`

	// HasBuiltIn says whether a built-in row exists for this (provider, model).
	// On a tenant row it answers "what happens if this override is removed?" —
	// true means it reverts to the shipped rate, false means the model becomes
	// unpriced and its spend silently reports as zero.
	HasBuiltIn bool `json:"has_built_in"`
}

// ModelPriceInput is one price a tenant is setting. Rates are per million
// tokens, matching how every provider publishes them and how the table stores
// them — converting at the boundary would only invite unit confusion.
type ModelPriceInput struct {
	ModelName    string  `json:"model_name"`
	ProviderName string  `json:"provider_name"`
	InputPerM    float64 `json:"cost_per_million_input_tokens"`
	OutputPerM   float64 `json:"cost_per_million_output_tokens"`

	CachedInputPerM   *float64 `json:"cost_per_million_cached_input_tokens,omitempty"`
	CacheCreationPerM *float64 `json:"cost_per_million_cache_creation_tokens,omitempty"`

	// Optional long-context tier. Leave all three unset for flat pricing.
	ContextThresholdTokens *int64   `json:"context_threshold_tokens,omitempty"`
	InputPerMLongCtx       *float64 `json:"cost_per_million_input_tokens_long_ctx,omitempty"`
	OutputPerMLongCtx      *float64 `json:"cost_per_million_output_tokens_long_ctx,omitempty"`
}

// Validate rejects input that would produce a nonsensical bill rather than
// storing it and discovering the problem in a cost report weeks later.
func (in ModelPriceInput) Validate() error {
	if strings.TrimSpace(in.ModelName) == "" {
		return fmt.Errorf("model_name is required")
	}
	if strings.TrimSpace(in.ProviderName) == "" {
		return fmt.Errorf("provider_name is required")
	}
	// Zero is allowed — a self-hosted model genuinely can be free — but a
	// negative rate would silently subtract from the tenant's spend.
	if in.InputPerM < 0 || in.OutputPerM < 0 {
		return fmt.Errorf("token rates must not be negative (got input=%v output=%v)", in.InputPerM, in.OutputPerM)
	}
	if in.CachedInputPerM != nil && *in.CachedInputPerM < 0 {
		return fmt.Errorf("cached input rate must not be negative")
	}
	if in.CacheCreationPerM != nil && *in.CacheCreationPerM < 0 {
		return fmt.Errorf("cache creation rate must not be negative")
	}

	// The long-context tier only takes effect when the threshold AND the
	// long input rate are both present (modelPricing.useLongCtx requires
	// both). Accepting one without the other would store a tier that never
	// fires and silently bill long prompts at the short rate — the exact
	// failure this validation exists to prevent.
	hasThreshold := in.ContextThresholdTokens != nil
	hasLongRates := in.InputPerMLongCtx != nil || in.OutputPerMLongCtx != nil
	if hasThreshold != hasLongRates {
		return fmt.Errorf("long-context pricing needs both a threshold and long-context rates, or neither")
	}
	if hasThreshold {
		if *in.ContextThresholdTokens <= 0 {
			return fmt.Errorf("context threshold must be a positive token count")
		}
		if in.InputPerMLongCtx == nil || in.OutputPerMLongCtx == nil {
			return fmt.Errorf("long-context pricing needs both an input and an output rate")
		}
		if *in.InputPerMLongCtx < 0 || *in.OutputPerMLongCtx < 0 {
			return fmt.Errorf("long-context rates must not be negative")
		}
	}
	return nil
}

// UpsertModelPricing writes a tenant's own rates for one or more models.
//
// Every row is written in a single transaction: a partial write would leave the
// tenant's cost reporting internally inconsistent, with some models priced from
// the new set and some from the old.
//
// tenantId is required — an empty one would write a built-in row visible to
// every tenant, which is exactly the collision V843 exists to prevent.
func UpsertModelPricing(dbManager *common.DatabaseManager, tenantId, userId string, prices []ModelPriceInput) error {
	if dbManager == nil {
		return fmt.Errorf("UpsertModelPricing: database manager unavailable")
	}
	if strings.TrimSpace(tenantId) == "" {
		return fmt.Errorf("UpsertModelPricing: tenant is required — refusing to write a global price")
	}
	if len(prices) == 0 {
		return nil
	}
	for i, p := range prices {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("UpsertModelPricing: price %d (%s/%s): %w", i, p.ProviderName, p.ModelName, err)
		}
	}

	// ON CONFLICT targets the partial unique index for tenant rows, so a
	// repeated save updates in place instead of erroring.
	const q = `
		INSERT INTO llm_model_pricing (
			model_name, provider_name, tenant_id,
			cost_per_million_input_tokens, cost_per_million_output_tokens,
			cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
			context_threshold_tokens,
			cost_per_million_input_tokens_long_ctx, cost_per_million_output_tokens_long_ctx,
			pricing_updated_at, pricing_updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), NULLIF($11, '')::uuid)
		ON CONFLICT (model_name, provider_name, tenant_id) WHERE tenant_id IS NOT NULL
		DO UPDATE SET
			cost_per_million_input_tokens          = EXCLUDED.cost_per_million_input_tokens,
			cost_per_million_output_tokens         = EXCLUDED.cost_per_million_output_tokens,
			cost_per_million_cached_input_tokens   = EXCLUDED.cost_per_million_cached_input_tokens,
			cost_per_million_cache_creation_tokens = EXCLUDED.cost_per_million_cache_creation_tokens,
			context_threshold_tokens               = EXCLUDED.context_threshold_tokens,
			cost_per_million_input_tokens_long_ctx  = EXCLUDED.cost_per_million_input_tokens_long_ctx,
			cost_per_million_output_tokens_long_ctx = EXCLUDED.cost_per_million_output_tokens_long_ctx,
			pricing_updated_at                     = now(),
			pricing_updated_by                     = EXCLUDED.pricing_updated_by`

	_, err := dbManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		for _, p := range prices {
			if _, err := tx.Exec(q,
				strings.TrimSpace(p.ModelName), strings.TrimSpace(p.ProviderName), tenantId,
				p.InputPerM, p.OutputPerM,
				nullableFloat(p.CachedInputPerM), nullableFloat(p.CacheCreationPerM),
				nullableInt(p.ContextThresholdTokens),
				nullableFloat(p.InputPerMLongCtx), nullableFloat(p.OutputPerMLongCtx),
				userId,
			); err != nil {
				return nil, fmt.Errorf("upsert %s/%s: %w", p.ProviderName, p.ModelName, err)
			}
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("UpsertModelPricing: %w", err)
	}
	slog.Info("pricing: tenant model prices saved", "tenant_id", tenantId, "count", len(prices))
	return nil
}

// DeleteModelPricing removes a tenant's override so the model falls back to the
// built-in rate. Built-in rows are never deletable through this path — the
// tenant_id predicate makes that structural rather than a check that could be
// forgotten.
// It returns the number of rows removed so the caller can distinguish "override
// dropped" from "there was nothing to drop" — the latter means someone else got
// there first, and silently reporting success would leave the UI claiming a
// change it did not make.
//
// tenant_id = $1 is what makes this safe: a built-in row has a NULL tenant and
// can never match, so no tenant can delete the rates every other tenant reads.
func DeleteModelPricing(dbManager *common.DatabaseManager, tenantId, provider, model string) (int64, error) {
	if dbManager == nil {
		return 0, fmt.Errorf("DeleteModelPricing: database manager unavailable")
	}
	if strings.TrimSpace(tenantId) == "" {
		return 0, fmt.Errorf("DeleteModelPricing: tenant is required")
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return 0, fmt.Errorf("DeleteModelPricing: provider_name and model_name are required")
	}
	// Rows are stored trimmed (see UpsertModelPricing), so the delete has to trim
	// too — otherwise " openai " passes validation above and then matches nothing.
	res, err := dbManager.Db.Exec(
		`DELETE FROM llm_model_pricing WHERE tenant_id = $1 AND provider_name = $2 AND model_name = $3`,
		tenantId, strings.TrimSpace(provider), strings.TrimSpace(model))
	if err != nil {
		return 0, fmt.Errorf("DeleteModelPricing: %w", err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("DeleteModelPricing: reading rows affected: %w", err)
	}
	return removed, nil
}

// ListModelPricing returns one row per (provider, model): the rate that is
// actually billed, with is_built_in saying whose it is. Where a tenant has
// overridden a built-in, only the tenant row comes back — returning both would
// list the model twice at two different prices.
//
// has_built_in reports whether a built-in row still sits underneath, which is
// what removing an override falls back to. Without it the UI cannot tell a
// revert-to-list-price from leaving the model entirely unpriced.
func ListModelPricing(dbManager *common.DatabaseManager, tenantId string) ([]ModelPriceRow, error) {
	if dbManager == nil {
		return nil, fmt.Errorf("ListModelPricing: database manager unavailable")
	}
	args := []any{}
	where := `tenant_id IS NULL`
	if strings.TrimSpace(tenantId) != "" {
		where = `(tenant_id IS NULL OR tenant_id = $1)`
		args = append(args, tenantId)
	}

	// DISTINCT ON ... tenant_id NULLS LAST collapses an overridden model to the
	// single rate it is actually billed at — the same precedence GetConversationCosts
	// applies. A plain OR would surface the tenant row and the built-in it replaced.
	rows, err := dbManager.Db.Queryx(`
		SELECT DISTINCT ON (provider_name, model_name)
		       model_name, provider_name,
		       cost_per_million_input_tokens, cost_per_million_output_tokens,
		       cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
		       context_threshold_tokens,
		       cost_per_million_input_tokens_long_ctx, cost_per_million_output_tokens_long_ctx,
		       tenant_id IS NULL AS is_built_in,
		       pricing_updated_at,
		       EXISTS (
		           SELECT 1 FROM llm_model_pricing b
		           WHERE b.tenant_id IS NULL
		             AND b.provider_name = p.provider_name
		             AND b.model_name = p.model_name
		       ) AS has_built_in
		FROM llm_model_pricing p
		WHERE `+where+`
		ORDER BY provider_name, model_name, tenant_id NULLS LAST`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListModelPricing: query failed: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("ListModelPricing: failed to close rows", "error", err)
		}
	}()

	out := []ModelPriceRow{}
	for rows.Next() {
		var (
			r             ModelPriceRow
			cachedInput   sql.NullFloat64
			cacheCreation sql.NullFloat64
			threshold     sql.NullInt64
			inputLong     sql.NullFloat64
			outputLong    sql.NullFloat64
			updatedAt     sql.NullTime
		)
		if err := rows.Scan(&r.ModelName, &r.ProviderName, &r.InputPerM, &r.OutputPerM,
			&cachedInput, &cacheCreation, &threshold, &inputLong, &outputLong,
			&r.IsBuiltIn, &updatedAt, &r.HasBuiltIn); err != nil {
			return nil, fmt.Errorf("ListModelPricing: scan failed: %w", err)
		}
		if cachedInput.Valid {
			r.CachedInputPerM = &cachedInput.Float64
		}
		if cacheCreation.Valid {
			r.CacheCreationPerM = &cacheCreation.Float64
		}
		if threshold.Valid {
			r.ContextThresholdTokens = &threshold.Int64
		}
		if inputLong.Valid {
			r.InputPerMLongCtx = &inputLong.Float64
		}
		if outputLong.Valid {
			r.OutputPerMLongCtx = &outputLong.Float64
		}
		if updatedAt.Valid {
			s := updatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			r.UpdatedAt = &s
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListModelPricing: %w", err)
	}
	return out, nil
}

func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
