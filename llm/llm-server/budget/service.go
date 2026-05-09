package budget

import (
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// validateModule checks if the provided module name is valid
func validateModule(module string) error {
	if !validModules[module] {
		return fmt.Errorf("invalid module: %s", module)
	}
	return nil
}

// CheckBudgetLimits checks all budget and count limits for a given module.
// Implements a 10-step sequential check:
//  1. Load tenant config (row or system defaults)
//  2. Tenant budget disabled? → skip all tenant checks
//  3. Tenant daily cost enabled? → check daily cost → 429
//  4. Tenant monthly cost enabled? → check monthly cost → 429
//  5. Tenant daily count enabled? → check daily count → 429
//  6. Tenant monthly count enabled? → check monthly count → 429
//  7. Load account config (row or system defaults)
//  8. Account budget disabled? → skip all account checks
//  9. Account daily cost → monthly cost → daily count → monthly count
//
// Returns true if any limit is exceeded, along with an error message.
// If the check itself fails, it logs the error but returns false to not block the request.
func CheckBudgetLimits(tenantId, accountId, module string, logger *slog.Logger) (exceeded bool, errorMessage string) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		logger.Error("budget: error getting database manager for budget check", "error", err)
		return false, ""
	}

	// Fetch configs — batch when both IDs present, individual otherwise
	var configs []BudgetConfig
	if tenantId != "" && accountId != "" {
		query := `SELECT * FROM llm_budget_config
		          WHERE (entity_type = $1 AND entity_id = $2 AND module = $3)
		             OR (entity_type = $4 AND entity_id = $5 AND module = $3)`
		err = dbManager.Db.Select(&configs, query, EntityTypeTenant, tenantId, module, EntityTypeAccount, accountId)
		if err != nil {
			logger.Error("budget: error loading budget configs batch", "error", err, "tenant_id", tenantId, "account_id", accountId)
			return false, ""
		}
	} else if tenantId != "" {
		cfg, err := GetBudgetConfig(dbManager, EntityTypeTenant, tenantId, module)
		if err == nil && cfg != nil {
			configs = append(configs, *cfg)
		}
	} else if accountId != "" {
		cfg, err := GetBudgetConfig(dbManager, EntityTypeAccount, accountId, module)
		if err == nil && cfg != nil {
			configs = append(configs, *cfg)
		}
	}

	// Map configs for easy lookup, defaulting to system defaults if not found in DB
	tenantConfig := buildSystemDefaultConfig(EntityTypeTenant, module)
	accountConfig := buildSystemDefaultConfig(EntityTypeAccount, module)

	for i := range configs {
		switch configs[i].EntityType {
		case EntityTypeTenant:
			tenantConfig = &configs[i]
		case EntityTypeAccount:
			accountConfig = &configs[i]
		}
	}

	// === TENANT CHECKS ===
	if tenantConfig.BudgetDisabled {
		logger.Warn("budget: all checks disabled for tenant",
			"tenant_id", tenantId, "module", module,
			"disabled_by", tenantConfig.DisabledBy)
		// Skip all tenant checks, but still check account
	} else {
		// Step 1: Tenant daily cost
		if tenantConfig.DailyCostEnabled {
			limit := resolveLimit(tenantConfig.DailyCostLimit, getSystemDefaultDailyCostLimit(EntityTypeTenant))
			usage, err := GetTenantDailyTokenUsage(dbManager, tenantId, module)
			if err != nil {
				logger.Error("budget: error checking tenant daily cost", "error", err, "tenant_id", tenantId, "module", module)
			} else if usage >= limit {
				logger.Warn("budget: tenant daily cost limit exceeded",
					"tenant_id", tenantId, "module", module, "usage", usage, "limit", limit)
				return true, "budget: daily budget limit exceeded for your organization"
			}
		}

		// Step 2: Tenant monthly cost
		if tenantConfig.MonthlyCostEnabled {
			limit := resolveLimit(tenantConfig.MonthlyCostLimit, getSystemDefaultMonthlyCostLimit(EntityTypeTenant, module))
			usage, err := GetTenantTokenUsage(dbManager, tenantId, module)
			if err != nil {
				logger.Error("budget: error checking tenant monthly cost", "error", err, "tenant_id", tenantId, "module", module)
			} else if usage >= limit {
				logger.Warn("budget: tenant monthly cost limit exceeded",
					"tenant_id", tenantId, "module", module, "usage", usage, "limit", limit)
				return true, "budget: monthly budget limit exceeded for your organization"
			}
		}

		// Step 3: Tenant daily count
		if tenantConfig.DailyCountEnabled {
			limit := resolveCountLimit(tenantConfig.DailyCountLimit, getSystemDefaultDailyCountLimit(EntityTypeTenant))
			count, err := GetTenantDailyConversationCount(dbManager, tenantId, module)
			if err != nil {
				logger.Error("budget: error checking tenant daily count", "error", err, "tenant_id", tenantId, "module", module)
			} else if count >= limit {
				logger.Warn("budget: tenant daily count limit exceeded",
					"tenant_id", tenantId, "module", module, "count", count, "limit", limit)
				return true, "budget: daily investigation count limit exceeded for your organization"
			}
		}

		// Step 4: Tenant monthly count
		if tenantConfig.MonthlyCountEnabled {
			limit := resolveCountLimit(tenantConfig.MonthlyCountLimit, getSystemDefaultMonthlyCountLimit(EntityTypeTenant, module))
			count, err := GetTenantConversationCount(dbManager, tenantId, module)
			if err != nil {
				logger.Error("budget: error checking tenant monthly count", "error", err, "tenant_id", tenantId, "module", module)
			} else if count >= limit {
				logger.Warn("budget: tenant monthly count limit exceeded",
					"tenant_id", tenantId, "module", module, "count", count, "limit", limit)
				return true, "budget: monthly investigation count limit exceeded for your organization"
			}
		}
	}

	// === ACCOUNT CHECKS ===
	if accountConfig.BudgetDisabled {
		logger.Warn("budget: all checks disabled for account",
			"account_id", accountId, "module", module,
			"disabled_by", accountConfig.DisabledBy)
		return false, ""
	}

	// Step 5: Account daily cost
	if accountConfig.DailyCostEnabled {
		limit := resolveLimit(accountConfig.DailyCostLimit, getSystemDefaultDailyCostLimit(EntityTypeAccount))
		usage, err := GetAccountDailyTokenUsage(dbManager, accountId, module)
		if err != nil {
			logger.Error("budget: error checking account daily cost", "error", err, "account_id", accountId, "module", module)
		} else if usage >= limit {
			logger.Warn("budget: account daily cost limit exceeded",
				"account_id", accountId, "module", module, "usage", usage, "limit", limit)
			return true, "budget: daily budget limit exceeded for this account"
		}
	}

	// Step 6: Account monthly cost
	if accountConfig.MonthlyCostEnabled {
		limit := resolveLimit(accountConfig.MonthlyCostLimit, getSystemDefaultMonthlyCostLimit(EntityTypeAccount, module))
		usage, err := GetAccountTokenUsage(dbManager, accountId, module)
		if err != nil {
			logger.Error("budget: error checking account monthly cost", "error", err, "account_id", accountId, "module", module)
		} else if usage >= limit {
			logger.Warn("budget: account monthly cost limit exceeded",
				"account_id", accountId, "module", module, "usage", usage, "limit", limit)
			return true, "budget: monthly budget limit exceeded for this account"
		}
	}

	// Step 7: Account daily count
	if accountConfig.DailyCountEnabled {
		limit := resolveCountLimit(accountConfig.DailyCountLimit, getSystemDefaultDailyCountLimit(EntityTypeAccount))
		count, err := GetAccountDailyConversationCount(dbManager, accountId, module)
		if err != nil {
			logger.Error("budget: error checking account daily count", "error", err, "account_id", accountId, "module", module)
		} else if count >= limit {
			logger.Warn("budget: account daily count limit exceeded",
				"account_id", accountId, "module", module, "count", count, "limit", limit)
			return true, "budget: daily investigation count limit exceeded for this account"
		}
	}

	// Step 8: Account monthly count
	if accountConfig.MonthlyCountEnabled {
		limit := resolveCountLimit(accountConfig.MonthlyCountLimit, getSystemDefaultMonthlyCountLimit(EntityTypeAccount, module))
		count, err := GetAccountConversationCount(dbManager, accountId, module)
		if err != nil {
			logger.Error("budget: error checking account monthly count", "error", err, "account_id", accountId, "module", module)
		} else if count >= limit {
			logger.Warn("budget: account monthly count limit exceeded",
				"account_id", accountId, "module", module, "count", count, "limit", limit)
			return true, "budget: monthly investigation count limit exceeded for this account"
		}
	}

	return false, ""
}

// GetBudgetStatus retrieves comprehensive budget status for a tenant and account
func GetBudgetStatus(tenantId, accountId string, logger *slog.Logger) (*BudgetStatusResponse, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetBudgetStatus: failed to get database manager: %w", err)
	}

	now := time.Now()
	response := &BudgetStatusResponse{
		TenantID:  tenantId,
		AccountID: accountId,
		Period:    now.Format("2006-01"),
		Today:     now.Format("2006-01-02"),
	}

	investigationInfo, err := getModuleBudgetStatus(dbManager, tenantId, accountId, ModuleInvestigation, logger)
	if err != nil {
		logger.Error("GetBudgetStatus: failed to get investigation budget info", "error", err)
		return nil, err
	}
	response.Investigation = *investigationInfo

	userInvestigationInfo, err := getModuleBudgetStatus(dbManager, tenantId, accountId, ModuleUserInvestigation, logger)
	if err != nil {
		logger.Error("GetBudgetStatus: failed to get user_investigation budget info", "error", err)
		return nil, err
	}
	response.UserInvestigation = *userInvestigationInfo

	return response, nil
}

// getModuleBudgetStatus retrieves budget status for a specific module at both tenant and account levels
func getModuleBudgetStatus(dbManager *common.DatabaseManager, tenantId, accountId, module string, logger *slog.Logger) (*ModuleBudgetStatus, error) {
	info := &ModuleBudgetStatus{}

	tenantStatus, err := getEntityBudgetStatus(dbManager, EntityTypeTenant, tenantId, module, logger)
	if err != nil {
		return nil, fmt.Errorf("getModuleBudgetStatus: tenant: %w", err)
	}
	info.Tenant = *tenantStatus

	accountStatus, err := getEntityBudgetStatus(dbManager, EntityTypeAccount, accountId, module, logger)
	if err != nil {
		return nil, fmt.Errorf("getModuleBudgetStatus: account: %w", err)
	}
	info.Account = *accountStatus

	return info, nil
}

// getEntityBudgetStatus builds EntityBudgetStatus for a single entity+module
func getEntityBudgetStatus(dbManager *common.DatabaseManager, entityType, entityId, module string, logger *slog.Logger) (*EntityBudgetStatus, error) {
	cfg, source, err := getEffectiveConfig(dbManager, entityType, entityId, module)
	if err != nil {
		return nil, err
	}

	status := &EntityBudgetStatus{
		BudgetDisabled: cfg.BudgetDisabled,
		DisabledBy:     cfg.DisabledBy,
		DisabledAt:     cfg.DisabledAt,
	}

	// Monthly cost
	status.MonthlyCost = buildCostLimitInfo(
		dbManager, cfg.MonthlyCostEnabled, cfg.MonthlyCostLimit,
		getSystemDefaultMonthlyCostLimit(entityType, module),
		entityType, entityId, module, "month", source, logger,
	)

	// Daily cost
	status.DailyCost = buildCostLimitInfo(
		dbManager, cfg.DailyCostEnabled, cfg.DailyCostLimit,
		getSystemDefaultDailyCostLimit(entityType),
		entityType, entityId, module, "day", source, logger,
	)

	// Monthly count
	status.MonthlyCount = buildCountLimitInfo(
		dbManager, cfg.MonthlyCountEnabled, cfg.MonthlyCountLimit,
		getSystemDefaultMonthlyCountLimit(entityType, module),
		entityType, entityId, module, "month", source, logger,
	)

	// Daily count
	status.DailyCount = buildCountLimitInfo(
		dbManager, cfg.DailyCountEnabled, cfg.DailyCountLimit,
		getSystemDefaultDailyCountLimit(entityType),
		entityType, entityId, module, "day", source, logger,
	)

	return status, nil
}

// buildCostLimitInfo builds a LimitInfo for a cost-based limit
func buildCostLimitInfo(dbManager *common.DatabaseManager, enabled bool, configLimit *float64, systemDefault float64, entityType, entityId, module, period, source string, logger *slog.Logger) LimitInfo {
	limit := resolveLimit(configLimit, systemDefault)
	limitSource := source
	if configLimit == nil {
		limitSource = "system_default"
	}

	var usage float64
	var err error
	if period == "day" {
		if entityType == EntityTypeTenant {
			usage, err = GetTenantDailyTokenUsage(dbManager, entityId, module)
		} else {
			usage, err = GetAccountDailyTokenUsage(dbManager, entityId, module)
		}
	} else {
		if entityType == EntityTypeTenant {
			usage, err = GetTenantTokenUsage(dbManager, entityId, module)
		} else {
			usage, err = GetAccountTokenUsage(dbManager, entityId, module)
		}
	}
	if err != nil {
		logger.Warn("buildCostLimitInfo: failed to get usage", "entity_type", entityType, "period", period, "error", err)
		usage = 0
	}

	remaining := limit - usage
	if remaining < 0 {
		remaining = 0
	}

	return LimitInfo{
		Enabled:     enabled,
		Limit:       limit,
		Usage:       usage,
		Remaining:   remaining,
		LimitSource: limitSource,
	}
}

// buildCountLimitInfo builds a CountLimitInfo for a count-based limit
func buildCountLimitInfo(dbManager *common.DatabaseManager, enabled bool, configLimit *int, systemDefault int, entityType, entityId, module, period, source string, logger *slog.Logger) CountLimitInfo {
	limit := resolveCountLimit(configLimit, systemDefault)
	limitSource := source
	if configLimit == nil {
		limitSource = "system_default"
	}

	var count int
	var err error
	if period == "day" {
		if entityType == EntityTypeTenant {
			count, err = GetTenantDailyConversationCount(dbManager, entityId, module)
		} else {
			count, err = GetAccountDailyConversationCount(dbManager, entityId, module)
		}
	} else {
		if entityType == EntityTypeTenant {
			count, err = GetTenantConversationCount(dbManager, entityId, module)
		} else {
			count, err = GetAccountConversationCount(dbManager, entityId, module)
		}
	}
	if err != nil {
		logger.Warn("buildCountLimitInfo: failed to get count", "entity_type", entityType, "period", period, "error", err)
		count = 0
	}

	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}

	return CountLimitInfo{
		Enabled:     enabled,
		Limit:       limit,
		Usage:       count,
		Remaining:   remaining,
		LimitSource: limitSource,
	}
}

// GetSystemDefaults returns the read-only system defaults and max caps
func GetSystemDefaults() *SystemDefaultsResponse {
	cfg := config.Config

	return &SystemDefaultsResponse{
		Defaults: map[string]map[string]SystemDefaultEntry{
			EntityTypeTenant: {
				ModuleInvestigation: {
					MonthlyCostLimit:  cfg.TenantLlmDefaultBudgetLimitInvestigation,
					DailyCostLimit:    cfg.DailyDefaultCostLimitTenant,
					MonthlyCountLimit: cfg.TenantLlmDefaultCountLimitInvestigation,
					DailyCountLimit:   cfg.DailyDefaultCountLimitTenant,
				},
				ModuleUserInvestigation: {
					MonthlyCostLimit:  cfg.TenantLlmDefaultBudgetLimitUserInvestigation,
					DailyCostLimit:    cfg.DailyDefaultCostLimitTenant,
					MonthlyCountLimit: cfg.TenantLlmDefaultCountLimitUserInvestigation,
					DailyCountLimit:   cfg.DailyDefaultCountLimitTenant,
				},
			},
			EntityTypeAccount: {
				ModuleInvestigation: {
					MonthlyCostLimit:  cfg.AccountLlmDefaultBudgetLimitInvestigation,
					DailyCostLimit:    cfg.DailyDefaultCostLimitAccount,
					MonthlyCountLimit: 0,
					DailyCountLimit:   0,
				},
				ModuleUserInvestigation: {
					MonthlyCostLimit:  cfg.AccountLlmDefaultBudgetLimitUserInvestigation,
					DailyCostLimit:    cfg.DailyDefaultCostLimitAccount,
					MonthlyCountLimit: 0,
					DailyCountLimit:   0,
				},
			},
		},
		MaxCaps: MaxCapsInfo{
			MonthlyCostTenant:  cfg.MaxMonthlyCostLimitTenant,
			MonthlyCostAccount: cfg.MaxMonthlyCostLimitAccount,
			DailyCostTenant:    cfg.MaxDailyCostLimitTenant,
			DailyCostAccount:   cfg.MaxDailyCostLimitAccount,
			MonthlyCount:       cfg.MaxMonthlyCountLimit,
			DailyCount:         cfg.MaxDailyCountLimit,
		},
	}
}
