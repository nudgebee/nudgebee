package egressfilter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"nudgebee/llm/common"
)

// DAO functions for the llm_egressfilter_tenant_config table. Mirrors the
// pattern in memory/stores/preferences/preferences_dao.go — package-level
// functions, common.GetDatabaseManager(common.Metastore), parameterized
// SQL. The egressfilter package owns the DAO because the data is only
// meaningful in the context of this package's wrapper.
//
// Stage 2a focus: read path (GetTenantConfigByID, used by the loader the
// cache delegates to) and write path (Upsert/Delete, used by the admin
// API). List exists for ops debugging; no production read path goes
// through it.

const tenantConfigSelectColumns = `tenant_id, mode, enabled, allowlist, custom_rules, disabled_rules, pii_enabled, pii_mode, pii_ner_enabled, pii_disabled_categories, updated_at`

// GetTenantConfigByID returns the override row for one tenant, or (nil, nil)
// when no row exists. The "no row" case is normal — most tenants run at
// env defaults; only those an admin has explicitly configured have a row.
//
// Returns (nil, err) on DB error so the cache layer can fail-open to env
// defaults (Resolve logs and returns nil). Context cancellation propagates
// to the DB query — request timeouts won't leak DB connections.
func GetTenantConfigByID(ctx context.Context, tenantID uuid.UUID) (*TenantConfig, error) {
	if tenantID == uuid.Nil {
		return nil, fmt.Errorf("egressfilter.GetTenantConfigByID: tenantID is required")
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("egressfilter.GetTenantConfigByID: db: %w", err)
	}

	query := `
		SELECT ` + tenantConfigSelectColumns + `
		FROM public.llm_egressfilter_tenant_config
		WHERE tenant_id = $1
	`
	var (
		mode            string
		enabled         bool
		allowlistJSON   []byte
		customRules     []byte
		disabledJSON    []byte
		piiEnabled      sql.NullBool
		piiMode         sql.NullString
		piiNerEnabled   sql.NullBool
		piiDisabledCats pq.StringArray
		updatedAt       time.Time
		tenantIDOut     uuid.UUID
	)
	row := db.Db.QueryRowContext(ctx, query, tenantID)
	err = row.Scan(
		&tenantIDOut, &mode, &enabled, &allowlistJSON, &customRules, &disabledJSON,
		&piiEnabled, &piiMode, &piiNerEnabled, &piiDisabledCats,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("egressfilter.GetTenantConfigByID: scan: %w", err)
	}

	cfg := &TenantConfig{
		TenantID:              tenantIDOut,
		Mode:                  Mode(mode),
		Enabled:               enabled,
		CustomRules:           customRules,
		PIIDisabledCategories: []string(piiDisabledCats),
		UpdatedAt:             updatedAt,
	}
	if piiEnabled.Valid {
		v := piiEnabled.Bool
		cfg.PIIEnabled = &v
	}
	if piiMode.Valid {
		cfg.PIIMode = piiMode.String
	}
	if piiNerEnabled.Valid {
		v := piiNerEnabled.Bool
		cfg.PIINerEnabled = &v
	}
	if err := json.Unmarshal(allowlistJSON, &cfg.Allowlist); err != nil {
		return nil, fmt.Errorf("egressfilter.GetTenantConfigByID: parse allowlist: %w", err)
	}
	if err := json.Unmarshal(disabledJSON, &cfg.DisabledRules); err != nil {
		return nil, fmt.Errorf("egressfilter.GetTenantConfigByID: parse disabled_rules: %w", err)
	}
	return cfg, nil
}

// UpsertTenantConfig writes the override row for one tenant. Either insert
// or update — ON CONFLICT(tenant_id) DO UPDATE on every non-PK column.
// Caller is responsible for validating cfg before calling (regex compile
// checks for CustomRules, mode whitelist, length budgets) — DAO does only
// the DB-shape checks (non-nil tenant id, JSON-encodable arrays).
//
// Context cancellation propagates to the DB exec — request timeouts won't
// leak DB connections.
func UpsertTenantConfig(ctx context.Context, cfg *TenantConfig) error {
	if cfg == nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: cfg is nil")
	}
	if cfg.TenantID == uuid.Nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: tenantID is required")
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeDetect
	}

	// Marshal the array-shaped JSONB columns. nil → "[]" so the DB never
	// receives NULL (the column is NOT NULL DEFAULT '[]'::jsonb, and a NULL
	// here would be a contract violation).
	allowlistJSON, err := jsonOrEmpty(cfg.Allowlist)
	if err != nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: marshal allowlist: %w", err)
	}
	disabledJSON, err := jsonOrEmpty(cfg.DisabledRules)
	if err != nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: marshal disabled_rules: %w", err)
	}
	customRules := cfg.CustomRules
	if len(customRules) == 0 {
		customRules = []byte("[]")
	}

	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: db: %w", err)
	}
	// PII columns: pointer + string map cleanly to Postgres NULL / value
	// via sql.NullBool / sql.NullString. pii_disabled_categories is NOT
	// NULL DEFAULT '{}' at the DB level, so a nil slice serializes as an
	// empty array via pq.Array(nil-slice) → OK.
	var (
		piiEnabledArg    any
		piiModeArg       any
		piiNerEnabledArg any
	)
	if cfg.PIIEnabled != nil {
		piiEnabledArg = *cfg.PIIEnabled
	} else {
		piiEnabledArg = nil // -> SQL NULL
	}
	if cfg.PIIMode != "" {
		piiModeArg = cfg.PIIMode
	} else {
		piiModeArg = nil
	}
	if cfg.PIINerEnabled != nil {
		piiNerEnabledArg = *cfg.PIINerEnabled
	} else {
		piiNerEnabledArg = nil
	}

	query := `
		INSERT INTO public.llm_egressfilter_tenant_config
			(tenant_id, mode, enabled, allowlist, custom_rules, disabled_rules,
			 pii_enabled, pii_mode, pii_ner_enabled, pii_disabled_categories,
			 updated_at)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb,
		        $7, $8, $9, $10,
		        NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			mode                     = EXCLUDED.mode,
			enabled                  = EXCLUDED.enabled,
			allowlist                = EXCLUDED.allowlist,
			custom_rules             = EXCLUDED.custom_rules,
			disabled_rules           = EXCLUDED.disabled_rules,
			pii_enabled              = EXCLUDED.pii_enabled,
			pii_mode                 = EXCLUDED.pii_mode,
			pii_ner_enabled          = EXCLUDED.pii_ner_enabled,
			pii_disabled_categories  = EXCLUDED.pii_disabled_categories,
			updated_at               = NOW()
	`
	// pq.Array(nil-slice) marshals to SQL NULL, but pii_disabled_categories
	// is NOT NULL DEFAULT '{}'. A nil slice from either "field absent on
	// PUT" or "explicit empty on PATCH" would violate the constraint on
	// upsert — coerce to a non-nil empty slice so we always send '{}'.
	cats := cfg.PIIDisabledCategories
	if cats == nil {
		cats = []string{}
	}
	if _, err := db.Db.ExecContext(ctx, query,
		cfg.TenantID, string(cfg.Mode), cfg.Enabled,
		allowlistJSON, customRules, disabledJSON,
		piiEnabledArg, piiModeArg, piiNerEnabledArg, pq.Array(cats),
	); err != nil {
		return fmt.Errorf("egressfilter.UpsertTenantConfig: exec: %w", err)
	}
	return nil
}

// DeleteTenantConfig removes a tenant's override row. The next Resolve
// returns nil → env defaults take over for that tenant. Context
// cancellation propagates to the DB exec.
func DeleteTenantConfig(ctx context.Context, tenantID uuid.UUID) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("egressfilter.DeleteTenantConfig: tenantID is required")
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("egressfilter.DeleteTenantConfig: db: %w", err)
	}
	if _, err := db.Db.ExecContext(ctx,
		`DELETE FROM public.llm_egressfilter_tenant_config WHERE tenant_id = $1`,
		tenantID,
	); err != nil {
		return fmt.Errorf("egressfilter.DeleteTenantConfig: exec: %w", err)
	}
	return nil
}

// ListTenantConfigs returns up to `limit` rows ordered by updated_at desc.
// For ops/admin debugging only — not on the LLM call path. Context
// cancellation propagates to the DB query.
func ListTenantConfigs(ctx context.Context, limit int) ([]TenantConfig, error) {
	if limit <= 0 {
		limit = 100
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("egressfilter.ListTenantConfigs: db: %w", err)
	}
	query := `
		SELECT ` + tenantConfigSelectColumns + `
		FROM public.llm_egressfilter_tenant_config
		ORDER BY updated_at DESC
		LIMIT $1
	`
	rows, err := db.Db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("egressfilter.ListTenantConfigs: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []TenantConfig
	for rows.Next() {
		var (
			tID             uuid.UUID
			mode            string
			enabled         bool
			allowlistJSON   []byte
			customRules     []byte
			disabledJSON    []byte
			piiEnabled      sql.NullBool
			piiMode         sql.NullString
			piiNerEnabled   sql.NullBool
			piiDisabledCats pq.StringArray
			updatedAt       time.Time
		)
		if err := rows.Scan(
			&tID, &mode, &enabled, &allowlistJSON, &customRules, &disabledJSON,
			&piiEnabled, &piiMode, &piiNerEnabled, &piiDisabledCats,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("egressfilter.ListTenantConfigs: scan: %w", err)
		}
		cfg := TenantConfig{
			TenantID:              tID,
			Mode:                  Mode(mode),
			Enabled:               enabled,
			CustomRules:           customRules,
			PIIDisabledCategories: []string(piiDisabledCats),
			UpdatedAt:             updatedAt,
		}
		if piiEnabled.Valid {
			v := piiEnabled.Bool
			cfg.PIIEnabled = &v
		}
		if piiMode.Valid {
			cfg.PIIMode = piiMode.String
		}
		if piiNerEnabled.Valid {
			v := piiNerEnabled.Bool
			cfg.PIINerEnabled = &v
		}
		// Log unmarshal failures so corrupted JSONB rows are visible during
		// ops triage. Continue with empty defaults — caller is debugging,
		// not running the LLM path, so degraded output is preferable to a
		// hard error that hides the rest of the list.
		if err := json.Unmarshal(allowlistJSON, &cfg.Allowlist); err != nil {
			slog.Error("egressfilter.ListTenantConfigs: failed to unmarshal allowlist",
				"tenant_id", tID, "error", err)
		}
		if err := json.Unmarshal(disabledJSON, &cfg.DisabledRules); err != nil {
			slog.Error("egressfilter.ListTenantConfigs: failed to unmarshal disabled_rules",
				"tenant_id", tID, "error", err)
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// InitTenantConfigLoader wires the DAO-backed loader into the cache.
// Idempotent: safe to call from a single startup site. Without this call,
// Resolve returns nil for every tenant (env defaults everywhere).
func InitTenantConfigLoader() {
	SetTenantConfigLoader(func(ctx context.Context, tenantID uuid.UUID) (*TenantConfig, error) {
		return GetTenantConfigByID(ctx, tenantID)
	})
}

func jsonOrEmpty(v []string) ([]byte, error) {
	if len(v) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(v)
}
