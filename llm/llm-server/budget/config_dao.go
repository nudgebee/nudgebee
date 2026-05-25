package budget

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// budgetConfigColumns lists every column scanned into BudgetConfig.
// Keep in sync with the BudgetConfig struct (types.go).
const budgetConfigColumns = `id, entity_type, entity_id, module, budget_disabled, disabled_by, disabled_at,
	monthly_cost_limit, monthly_cost_enabled, monthly_count_limit, monthly_count_enabled,
	daily_cost_limit, daily_cost_enabled, daily_count_limit, daily_count_enabled,
	updated_by, updated_at, created_at`

// GetBudgetConfig fetches a single budget config by entity+module
func GetBudgetConfig(dbManager *common.DatabaseManager, entityType, entityID, module string) (*BudgetConfig, error) {
	var cfg BudgetConfig
	query := `SELECT ` + budgetConfigColumns + ` FROM llm_budget_config WHERE entity_type = $1 AND entity_id = $2 AND module = $3`
	err := dbManager.Db.Get(&cfg, query, entityType, entityID, module)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetBudgetConfig: %w", err)
	}
	return &cfg, nil
}

// GetBudgetConfigByID fetches a single budget config by its ID
func GetBudgetConfigByID(dbManager *common.DatabaseManager, id string) (*BudgetConfig, error) {
	var cfg BudgetConfig
	query := `SELECT ` + budgetConfigColumns + ` FROM llm_budget_config WHERE id = $1`
	err := dbManager.Db.Get(&cfg, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetBudgetConfigByID: %w", err)
	}
	return &cfg, nil
}

// ListBudgetConfigs lists budget configs with optional filters
func ListBudgetConfigs(dbManager *common.DatabaseManager, entityType, entityID, module string) ([]BudgetConfig, error) {
	query := `SELECT ` + budgetConfigColumns + ` FROM llm_budget_config WHERE 1=1`
	args := make([]interface{}, 0)
	argIdx := 1

	if entityType != "" {
		query += fmt.Sprintf(" AND entity_type = $%d", argIdx)
		args = append(args, entityType)
		argIdx++
	}
	if entityID != "" {
		query += fmt.Sprintf(" AND entity_id = $%d", argIdx)
		args = append(args, entityID)
		argIdx++
	}
	if module != "" {
		query += fmt.Sprintf(" AND module = $%d", argIdx)
		args = append(args, module)
	}

	query += " ORDER BY entity_type, entity_id, module"

	var configs []BudgetConfig
	err := dbManager.Db.Select(&configs, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ListBudgetConfigs: %w", err)
	}
	return configs, nil
}

// ValidateMaxCaps checks that the given limit values do not exceed system max caps.
// Returns an error message describing the first violation, or empty string if all valid.
func ValidateMaxCaps(req *BudgetConfigUpsertRequest) string {
	cfg := config.Config

	if req.MonthlyCostLimit != nil {
		var maxCap float64
		if req.EntityType == EntityTypeTenant {
			maxCap = cfg.MaxMonthlyCostLimitTenant
		} else {
			maxCap = cfg.MaxMonthlyCostLimitAccount
		}
		if *req.MonthlyCostLimit > maxCap {
			return fmt.Sprintf("monthly_cost_limit (%.2f) exceeds maximum allowed (%.2f)", *req.MonthlyCostLimit, maxCap)
		}
		if *req.MonthlyCostLimit < 0 {
			return "monthly_cost_limit cannot be negative"
		}
	}

	if req.DailyCostLimit != nil {
		var maxCap float64
		if req.EntityType == EntityTypeTenant {
			maxCap = cfg.MaxDailyCostLimitTenant
		} else {
			maxCap = cfg.MaxDailyCostLimitAccount
		}
		if *req.DailyCostLimit > maxCap {
			return fmt.Sprintf("daily_cost_limit (%.2f) exceeds maximum allowed (%.2f)", *req.DailyCostLimit, maxCap)
		}
		if *req.DailyCostLimit < 0 {
			return "daily_cost_limit cannot be negative"
		}
	}

	if req.MonthlyCountLimit != nil {
		maxCap := cfg.MaxMonthlyCountLimit
		if *req.MonthlyCountLimit > maxCap {
			return fmt.Sprintf("monthly_count_limit (%d) exceeds maximum allowed (%d)", *req.MonthlyCountLimit, maxCap)
		}
		if *req.MonthlyCountLimit < 0 {
			return "monthly_count_limit cannot be negative"
		}
	}

	if req.DailyCountLimit != nil {
		maxCap := cfg.MaxDailyCountLimit
		if *req.DailyCountLimit > maxCap {
			return fmt.Sprintf("daily_count_limit (%d) exceeds maximum allowed (%d)", *req.DailyCountLimit, maxCap)
		}
		if *req.DailyCountLimit < 0 {
			return "daily_count_limit cannot be negative"
		}
	}

	return ""
}

// applyAutoEnable sets enabled=true when a limit value is provided but enabled is not explicitly set
func applyAutoEnable(req *BudgetConfigUpsertRequest) {
	if req.MonthlyCostLimit != nil && req.MonthlyCostEnabled == nil {
		enabled := true
		req.MonthlyCostEnabled = &enabled
	}
	if req.DailyCostLimit != nil && req.DailyCostEnabled == nil {
		enabled := true
		req.DailyCostEnabled = &enabled
	}
	if req.MonthlyCountLimit != nil && req.MonthlyCountEnabled == nil {
		enabled := true
		req.MonthlyCountEnabled = &enabled
	}
	if req.DailyCountLimit != nil && req.DailyCountEnabled == nil {
		enabled := true
		req.DailyCountEnabled = &enabled
	}
}

// UpsertBudgetConfig creates or updates a budget config row.
// Validates max caps before writing. Auto-enables limits when values are set.
func UpsertBudgetConfig(dbManager *common.DatabaseManager, req *BudgetConfigUpsertRequest, updatedBy string) (*BudgetConfig, error) {
	// Validate entity type
	if !validEntityTypes[req.EntityType] {
		return nil, fmt.Errorf("invalid entity_type: %s", req.EntityType)
	}
	// Validate module
	if !validModules[req.Module] {
		return nil, fmt.Errorf("invalid module: %s", req.Module)
	}

	// Validate max caps
	if violation := ValidateMaxCaps(req); violation != "" {
		return nil, fmt.Errorf("max cap violation: %s", violation)
	}

	// Auto-enable when value is set
	applyAutoEnable(req)

	// Build the upsert — only update non-nil fields
	now := time.Now()

	// Check if row exists
	existing, err := GetBudgetConfig(dbManager, req.EntityType, req.EntityID, req.Module)
	if err != nil {
		return nil, fmt.Errorf("UpsertBudgetConfig: failed to check existing: %w", err)
	}

	if existing == nil {
		// INSERT new row
		cfg := &BudgetConfig{
			EntityType: req.EntityType,
			EntityID:   req.EntityID,
			Module:     req.Module,
			UpdatedBy:  &updatedBy,
			UpdatedAt:  now,
			CreatedAt:  now,
		}

		// Apply provided fields (nil means use DB defaults)
		if req.BudgetDisabled != nil {
			cfg.BudgetDisabled = *req.BudgetDisabled
			if *req.BudgetDisabled {
				cfg.DisabledBy = &updatedBy
				cfg.DisabledAt = &now
			}
		}
		if req.MonthlyCostLimit != nil {
			cfg.MonthlyCostLimit = req.MonthlyCostLimit
		}
		if req.MonthlyCostEnabled != nil {
			cfg.MonthlyCostEnabled = *req.MonthlyCostEnabled
		} else {
			cfg.MonthlyCostEnabled = true // default
		}
		if req.MonthlyCountLimit != nil {
			cfg.MonthlyCountLimit = req.MonthlyCountLimit
		}
		if req.MonthlyCountEnabled != nil {
			cfg.MonthlyCountEnabled = *req.MonthlyCountEnabled
		}
		if req.DailyCostLimit != nil {
			cfg.DailyCostLimit = req.DailyCostLimit
		}
		if req.DailyCostEnabled != nil {
			cfg.DailyCostEnabled = *req.DailyCostEnabled
		}
		if req.DailyCountLimit != nil {
			cfg.DailyCountLimit = req.DailyCountLimit
		}
		if req.DailyCountEnabled != nil {
			cfg.DailyCountEnabled = *req.DailyCountEnabled
		}

		query := `INSERT INTO llm_budget_config (
			entity_type, entity_id, module,
			budget_disabled, disabled_by, disabled_at,
			monthly_cost_limit, monthly_cost_enabled,
			monthly_count_limit, monthly_count_enabled,
			daily_cost_limit, daily_cost_enabled,
			daily_count_limit, daily_count_enabled,
			updated_by, updated_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		) RETURNING id`

		var id string
		err = dbManager.Db.Get(&id, query,
			cfg.EntityType, cfg.EntityID, cfg.Module,
			cfg.BudgetDisabled, cfg.DisabledBy, cfg.DisabledAt,
			cfg.MonthlyCostLimit, cfg.MonthlyCostEnabled,
			cfg.MonthlyCountLimit, cfg.MonthlyCountEnabled,
			cfg.DailyCostLimit, cfg.DailyCostEnabled,
			cfg.DailyCountLimit, cfg.DailyCountEnabled,
			cfg.UpdatedBy, cfg.UpdatedAt, cfg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("UpsertBudgetConfig: insert failed: %w", err)
		}
		cfg.ID = id
		slog.Info("UpsertBudgetConfig: created new config", "id", id, "entity_type", cfg.EntityType, "entity_id", cfg.EntityID, "module", cfg.Module)
		return cfg, nil
	}

	// UPDATE existing row — only update non-nil fields
	if req.BudgetDisabled != nil {
		existing.BudgetDisabled = *req.BudgetDisabled
		if *req.BudgetDisabled {
			existing.DisabledBy = &updatedBy
			existing.DisabledAt = &now
		} else {
			existing.DisabledBy = nil
			existing.DisabledAt = nil
		}
	}
	if req.MonthlyCostLimit != nil {
		existing.MonthlyCostLimit = req.MonthlyCostLimit
	}
	if req.MonthlyCostEnabled != nil {
		existing.MonthlyCostEnabled = *req.MonthlyCostEnabled
	}
	if req.MonthlyCountLimit != nil {
		existing.MonthlyCountLimit = req.MonthlyCountLimit
	}
	if req.MonthlyCountEnabled != nil {
		existing.MonthlyCountEnabled = *req.MonthlyCountEnabled
	}
	if req.DailyCostLimit != nil {
		existing.DailyCostLimit = req.DailyCostLimit
	}
	if req.DailyCostEnabled != nil {
		existing.DailyCostEnabled = *req.DailyCostEnabled
	}
	if req.DailyCountLimit != nil {
		existing.DailyCountLimit = req.DailyCountLimit
	}
	if req.DailyCountEnabled != nil {
		existing.DailyCountEnabled = *req.DailyCountEnabled
	}
	existing.UpdatedBy = &updatedBy
	existing.UpdatedAt = now

	query := `UPDATE llm_budget_config SET
		budget_disabled = $1, disabled_by = $2, disabled_at = $3,
		monthly_cost_limit = $4, monthly_cost_enabled = $5,
		monthly_count_limit = $6, monthly_count_enabled = $7,
		daily_cost_limit = $8, daily_cost_enabled = $9,
		daily_count_limit = $10, daily_count_enabled = $11,
		updated_by = $12, updated_at = $13
		WHERE id = $14`

	_, err = dbManager.Db.Exec(query,
		existing.BudgetDisabled, existing.DisabledBy, existing.DisabledAt,
		existing.MonthlyCostLimit, existing.MonthlyCostEnabled,
		existing.MonthlyCountLimit, existing.MonthlyCountEnabled,
		existing.DailyCostLimit, existing.DailyCostEnabled,
		existing.DailyCountLimit, existing.DailyCountEnabled,
		existing.UpdatedBy, existing.UpdatedAt,
		existing.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("UpsertBudgetConfig: update failed: %w", err)
	}

	slog.Info("UpsertBudgetConfig: updated config", "id", existing.ID, "entity_type", existing.EntityType, "entity_id", existing.EntityID, "module", existing.Module)
	return existing, nil
}

// DeleteBudgetConfig deletes a budget config by ID (reverts entity to system defaults)
func DeleteBudgetConfig(dbManager *common.DatabaseManager, id string) error {
	result, err := dbManager.Db.Exec("DELETE FROM llm_budget_config WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("DeleteBudgetConfig: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("DeleteBudgetConfig: no config found with id %s", id)
	}

	slog.Info("DeleteBudgetConfig: deleted config", "id", id)
	return nil
}

// getEffectiveConfig returns the resolved budget config for an entity+module.
// If a row exists in llm_budget_config, returns it with source "config".
// If no row exists, returns a config populated with system defaults and source "system_default".
func getEffectiveConfig(dbManager *common.DatabaseManager, entityType, entityID, module string) (*BudgetConfig, string, error) {
	if entityID == "" {
		// Empty entity ID — return system defaults (avoids invalid UUID error in DB)
		defaults := buildSystemDefaultConfig(entityType, module)
		return defaults, "system_default", nil
	}
	cfg, err := GetBudgetConfig(dbManager, entityType, entityID, module)
	if err != nil {
		return nil, "", err
	}

	if cfg != nil {
		return cfg, "config", nil
	}

	// No row — build from system defaults
	defaults := buildSystemDefaultConfig(entityType, module)
	return defaults, "system_default", nil
}

// buildSystemDefaultConfig constructs a BudgetConfig from system defaults for a given entity type and module
func buildSystemDefaultConfig(entityType, module string) *BudgetConfig {
	cfg := &BudgetConfig{
		EntityType:          entityType,
		Module:              module,
		BudgetDisabled:      false,
		MonthlyCostEnabled:  true,  // monthly cost enabled by default
		MonthlyCountEnabled: false, // count disabled by default
		DailyCostEnabled:    false, // daily cost disabled by default
		DailyCountEnabled:   false, // daily count disabled by default
	}

	// Set monthly cost default
	monthlyCost := getSystemDefaultMonthlyCostLimit(entityType, module)
	cfg.MonthlyCostLimit = &monthlyCost

	// Set daily cost default
	dailyCost := getSystemDefaultDailyCostLimit(entityType)
	cfg.DailyCostLimit = &dailyCost

	// Set monthly count default
	monthlyCount := getSystemDefaultMonthlyCountLimit(entityType, module)
	cfg.MonthlyCountLimit = &monthlyCount

	// Set daily count default
	dailyCount := getSystemDefaultDailyCountLimit(entityType)
	cfg.DailyCountLimit = &dailyCount

	return cfg
}

// getSystemDefaultMonthlyCostLimit returns the system default monthly cost limit
func getSystemDefaultMonthlyCostLimit(entityType, module string) float64 {
	if entityType == EntityTypeTenant {
		switch module {
		case ModuleInvestigation:
			return config.Config.TenantLlmDefaultBudgetLimitInvestigation
		case ModuleUserInvestigation:
			return config.Config.TenantLlmDefaultBudgetLimitUserInvestigation
		default:
			return TenantDefaultBudgetLimitFallback
		}
	}
	switch module {
	case ModuleInvestigation:
		return config.Config.AccountLlmDefaultBudgetLimitInvestigation
	case ModuleUserInvestigation:
		return config.Config.AccountLlmDefaultBudgetLimitUserInvestigation
	default:
		return AccountDefaultBudgetLimitFallback
	}
}

// getSystemDefaultDailyCostLimit returns the system default daily cost limit
func getSystemDefaultDailyCostLimit(entityType string) float64 {
	if entityType == EntityTypeTenant {
		return config.Config.DailyDefaultCostLimitTenant
	}
	return config.Config.DailyDefaultCostLimitAccount
}

// getSystemDefaultMonthlyCountLimit returns the system default monthly count limit
func getSystemDefaultMonthlyCountLimit(entityType, module string) int {
	if entityType == EntityTypeAccount {
		return 0 // no default count limits for accounts
	}
	switch module {
	case ModuleInvestigation:
		return config.Config.TenantLlmDefaultCountLimitInvestigation
	case ModuleUserInvestigation:
		return config.Config.TenantLlmDefaultCountLimitUserInvestigation
	default:
		return 0
	}
}

// getSystemDefaultDailyCountLimit returns the system default daily count limit
func getSystemDefaultDailyCountLimit(entityType string) int {
	if entityType == EntityTypeAccount {
		return 0 // no default count limits for accounts
	}
	return config.Config.DailyDefaultCountLimitTenant
}

// resolveLimit returns the config value if set, otherwise the system default
func resolveLimit(configValue *float64, systemDefault float64) float64 {
	if configValue != nil {
		return *configValue
	}
	return systemDefault
}

// resolveCountLimit returns the config value if set, otherwise the system default
func resolveCountLimit(configValue *int, systemDefault int) int {
	if configValue != nil {
		return *configValue
	}
	return systemDefault
}
