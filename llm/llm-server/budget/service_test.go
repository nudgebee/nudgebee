package budget

import (
	"fmt"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

// saveAndRestoreConfig saves the current config and returns a cleanup function
func saveAndRestoreConfig(t *testing.T) {
	t.Helper()
	saved := config.Config
	t.Cleanup(func() {
		config.Config = saved
	})
}

// TestValidateModule tests the module validation function
func TestValidateModule(t *testing.T) {
	tests := []struct {
		name        string
		module      string
		expectError bool
	}{
		{"valid module - investigation", "investigation", false},
		{"valid module - user_investigation", "user_investigation", false},
		{"invalid module - unknown", "unknown_module", true},
		{"invalid module - empty", "", true},
		{"invalid module - sql injection attempt", "event_analysis' OR '1'='1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModule(tt.module)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid module")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidModules tests the valid modules configuration
func TestValidModules(t *testing.T) {
	expectedModules := []string{ModuleInvestigation, ModuleUserInvestigation}
	for _, module := range expectedModules {
		assert.True(t, validModules[module], fmt.Sprintf("%s should be a valid module", module))
	}
	assert.GreaterOrEqual(t, len(validModules), len(expectedModules))
}

// TestValidEntityTypes tests the entity type validation
func TestValidEntityTypes(t *testing.T) {
	assert.True(t, validEntityTypes[EntityTypeTenant])
	assert.True(t, validEntityTypes[EntityTypeAccount])
	assert.False(t, validEntityTypes["invalid"])
	assert.False(t, validEntityTypes[""])
}

// TestGetSystemDefaultMonthlyCostLimit tests monthly cost system defaults
func TestGetSystemDefaultMonthlyCostLimit(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.TenantLlmDefaultBudgetLimitInvestigation = 1000.0
	config.Config.TenantLlmDefaultBudgetLimitUserInvestigation = 1000.0
	config.Config.AccountLlmDefaultBudgetLimitInvestigation = 600.0
	config.Config.AccountLlmDefaultBudgetLimitUserInvestigation = 400.0

	tests := []struct {
		name       string
		entityType string
		module     string
		expected   float64
	}{
		{"tenant - investigation", EntityTypeTenant, ModuleInvestigation, 1000.0},
		{"tenant - user_investigation", EntityTypeTenant, ModuleUserInvestigation, 1000.0},
		{"tenant - unknown module", EntityTypeTenant, "unknown", TenantDefaultBudgetLimitFallback},
		{"account - investigation", EntityTypeAccount, ModuleInvestigation, 600.0},
		{"account - user_investigation", EntityTypeAccount, ModuleUserInvestigation, 400.0},
		{"account - unknown module", EntityTypeAccount, "unknown", AccountDefaultBudgetLimitFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSystemDefaultMonthlyCostLimit(tt.entityType, tt.module)
			assert.Equal(t, tt.expected, result)
		})
	}

	// Account defaults should be lower than tenant defaults
	t.Run("account defaults lower than tenant defaults", func(t *testing.T) {
		for _, module := range []string{ModuleInvestigation, ModuleUserInvestigation} {
			tenantDefault := getSystemDefaultMonthlyCostLimit(EntityTypeTenant, module)
			accountDefault := getSystemDefaultMonthlyCostLimit(EntityTypeAccount, module)
			assert.Less(t, accountDefault, tenantDefault,
				"account default should be less than tenant default for module %s", module)
		}
	})
}

// TestGetSystemDefaultDailyCostLimit tests daily cost system defaults
func TestGetSystemDefaultDailyCostLimit(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.DailyDefaultCostLimitTenant = 50.0
	config.Config.DailyDefaultCostLimitAccount = 30.0

	assert.Equal(t, 50.0, getSystemDefaultDailyCostLimit(EntityTypeTenant))
	assert.Equal(t, 30.0, getSystemDefaultDailyCostLimit(EntityTypeAccount))
}

// TestGetSystemDefaultMonthlyCountLimit tests monthly count system defaults
func TestGetSystemDefaultMonthlyCountLimit(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.TenantLlmDefaultCountLimitInvestigation = 500
	config.Config.TenantLlmDefaultCountLimitUserInvestigation = 500

	assert.Equal(t, 500, getSystemDefaultMonthlyCountLimit(EntityTypeTenant, ModuleInvestigation))
	assert.Equal(t, 500, getSystemDefaultMonthlyCountLimit(EntityTypeTenant, ModuleUserInvestigation))
	assert.Equal(t, 0, getSystemDefaultMonthlyCountLimit(EntityTypeTenant, "unknown"))
	assert.Equal(t, 0, getSystemDefaultMonthlyCountLimit(EntityTypeAccount, ModuleInvestigation))
}

// TestGetSystemDefaultDailyCountLimit tests daily count system defaults
func TestGetSystemDefaultDailyCountLimit(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.DailyDefaultCountLimitTenant = 50
	assert.Equal(t, 50, getSystemDefaultDailyCountLimit(EntityTypeTenant))
	assert.Equal(t, 0, getSystemDefaultDailyCountLimit(EntityTypeAccount))
}

// TestResolveLimit tests the limit resolution helper
func TestResolveLimit(t *testing.T) {
	t.Run("uses config value when set", func(t *testing.T) {
		val := 200.0
		result := resolveLimit(&val, 100.0)
		assert.Equal(t, 200.0, result)
	})

	t.Run("falls back to default when nil", func(t *testing.T) {
		result := resolveLimit(nil, 100.0)
		assert.Equal(t, 100.0, result)
	})
}

// TestResolveCountLimit tests the count limit resolution helper
func TestResolveCountLimit(t *testing.T) {
	t.Run("uses config value when set", func(t *testing.T) {
		val := 200
		result := resolveCountLimit(&val, 100)
		assert.Equal(t, 200, result)
	})

	t.Run("falls back to default when nil", func(t *testing.T) {
		result := resolveCountLimit(nil, 100)
		assert.Equal(t, 100, result)
	})
}

// TestBuildSystemDefaultConfig tests the default config builder
func TestBuildSystemDefaultConfig(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.TenantLlmDefaultBudgetLimitInvestigation = 1000.0
	config.Config.DailyDefaultCostLimitTenant = 50.0
	config.Config.TenantLlmDefaultCountLimitInvestigation = 500
	config.Config.DailyDefaultCountLimitTenant = 50

	cfg := buildSystemDefaultConfig(EntityTypeTenant, ModuleInvestigation)

	assert.Equal(t, EntityTypeTenant, cfg.EntityType)
	assert.Equal(t, ModuleInvestigation, cfg.Module)
	assert.False(t, cfg.BudgetDisabled)
	assert.True(t, cfg.MonthlyCostEnabled)
	assert.False(t, cfg.MonthlyCountEnabled)
	assert.False(t, cfg.DailyCostEnabled)
	assert.False(t, cfg.DailyCountEnabled)

	assert.NotNil(t, cfg.MonthlyCostLimit)
	assert.Equal(t, 1000.0, *cfg.MonthlyCostLimit)

	assert.NotNil(t, cfg.DailyCostLimit)
	assert.Equal(t, 50.0, *cfg.DailyCostLimit)

	assert.NotNil(t, cfg.MonthlyCountLimit)
	assert.Equal(t, 500, *cfg.MonthlyCountLimit)

	assert.NotNil(t, cfg.DailyCountLimit)
	assert.Equal(t, 50, *cfg.DailyCountLimit)
}

// TestValidateMaxCaps tests max cap validation
func TestValidateMaxCaps(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.MaxMonthlyCostLimitTenant = 10000.0
	config.Config.MaxMonthlyCostLimitAccount = 5000.0
	config.Config.MaxDailyCostLimitTenant = 500.0
	config.Config.MaxDailyCostLimitAccount = 250.0
	config.Config.MaxMonthlyCountLimit = 5000
	config.Config.MaxDailyCountLimit = 500

	t.Run("valid limits within caps", func(t *testing.T) {
		cost := 5000.0
		count := 2000
		req := &BudgetConfigUpsertRequest{
			EntityType:        EntityTypeTenant,
			MonthlyCostLimit:  &cost,
			MonthlyCountLimit: &count,
		}
		assert.Empty(t, ValidateMaxCaps(req))
	})

	t.Run("monthly cost exceeds tenant cap", func(t *testing.T) {
		cost := 15000.0
		req := &BudgetConfigUpsertRequest{
			EntityType:       EntityTypeTenant,
			MonthlyCostLimit: &cost,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "monthly_cost_limit")
		assert.Contains(t, msg, "exceeds maximum")
	})

	t.Run("monthly cost exceeds account cap", func(t *testing.T) {
		cost := 6000.0
		req := &BudgetConfigUpsertRequest{
			EntityType:       EntityTypeAccount,
			MonthlyCostLimit: &cost,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "monthly_cost_limit")
		assert.Contains(t, msg, "exceeds maximum")
	})

	t.Run("daily cost exceeds cap", func(t *testing.T) {
		cost := 600.0
		req := &BudgetConfigUpsertRequest{
			EntityType:     EntityTypeTenant,
			DailyCostLimit: &cost,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "daily_cost_limit")
	})

	t.Run("monthly count exceeds cap", func(t *testing.T) {
		count := 6000
		req := &BudgetConfigUpsertRequest{
			EntityType:        EntityTypeTenant,
			MonthlyCountLimit: &count,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "monthly_count_limit")
	})

	t.Run("daily count exceeds cap", func(t *testing.T) {
		count := 600
		req := &BudgetConfigUpsertRequest{
			EntityType:      EntityTypeTenant,
			DailyCountLimit: &count,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "daily_count_limit")
	})

	t.Run("negative cost rejected", func(t *testing.T) {
		cost := -10.0
		req := &BudgetConfigUpsertRequest{
			EntityType:       EntityTypeTenant,
			MonthlyCostLimit: &cost,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "negative")
	})

	t.Run("negative count rejected", func(t *testing.T) {
		count := -5
		req := &BudgetConfigUpsertRequest{
			EntityType:        EntityTypeTenant,
			MonthlyCountLimit: &count,
		}
		msg := ValidateMaxCaps(req)
		assert.Contains(t, msg, "negative")
	})

	t.Run("nil values pass validation", func(t *testing.T) {
		req := &BudgetConfigUpsertRequest{
			EntityType: EntityTypeTenant,
		}
		assert.Empty(t, ValidateMaxCaps(req))
	})
}

// TestApplyAutoEnable tests auto-enable when values are set
func TestApplyAutoEnable(t *testing.T) {
	t.Run("enables monthly cost when limit set", func(t *testing.T) {
		cost := 100.0
		req := &BudgetConfigUpsertRequest{MonthlyCostLimit: &cost}
		applyAutoEnable(req)
		assert.NotNil(t, req.MonthlyCostEnabled)
		assert.True(t, *req.MonthlyCostEnabled)
	})

	t.Run("enables daily cost when limit set", func(t *testing.T) {
		cost := 50.0
		req := &BudgetConfigUpsertRequest{DailyCostLimit: &cost}
		applyAutoEnable(req)
		assert.NotNil(t, req.DailyCostEnabled)
		assert.True(t, *req.DailyCostEnabled)
	})

	t.Run("enables monthly count when limit set", func(t *testing.T) {
		count := 100
		req := &BudgetConfigUpsertRequest{MonthlyCountLimit: &count}
		applyAutoEnable(req)
		assert.NotNil(t, req.MonthlyCountEnabled)
		assert.True(t, *req.MonthlyCountEnabled)
	})

	t.Run("enables daily count when limit set", func(t *testing.T) {
		count := 50
		req := &BudgetConfigUpsertRequest{DailyCountLimit: &count}
		applyAutoEnable(req)
		assert.NotNil(t, req.DailyCountEnabled)
		assert.True(t, *req.DailyCountEnabled)
	})

	t.Run("does not override explicit enabled=false", func(t *testing.T) {
		cost := 100.0
		enabled := false
		req := &BudgetConfigUpsertRequest{
			MonthlyCostLimit:   &cost,
			MonthlyCostEnabled: &enabled,
		}
		applyAutoEnable(req)
		assert.False(t, *req.MonthlyCostEnabled, "should respect explicit enabled=false")
	})

	t.Run("no-op when no limits set", func(t *testing.T) {
		req := &BudgetConfigUpsertRequest{}
		applyAutoEnable(req)
		assert.Nil(t, req.MonthlyCostEnabled)
		assert.Nil(t, req.DailyCostEnabled)
		assert.Nil(t, req.MonthlyCountEnabled)
		assert.Nil(t, req.DailyCountEnabled)
	})
}

// TestGetSystemDefaults tests the system defaults response builder
func TestGetSystemDefaults(t *testing.T) {
	saveAndRestoreConfig(t)
	config.Config.TenantLlmDefaultBudgetLimitInvestigation = 1000.0
	config.Config.TenantLlmDefaultBudgetLimitUserInvestigation = 1000.0
	config.Config.AccountLlmDefaultBudgetLimitInvestigation = 600.0
	config.Config.AccountLlmDefaultBudgetLimitUserInvestigation = 400.0
	config.Config.DailyDefaultCostLimitTenant = 50.0
	config.Config.DailyDefaultCostLimitAccount = 30.0
	config.Config.TenantLlmDefaultCountLimitInvestigation = 500
	config.Config.TenantLlmDefaultCountLimitUserInvestigation = 500
	config.Config.DailyDefaultCountLimitTenant = 50
	config.Config.MaxMonthlyCostLimitTenant = 10000.0
	config.Config.MaxMonthlyCostLimitAccount = 5000.0
	config.Config.MaxDailyCostLimitTenant = 500.0
	config.Config.MaxDailyCostLimitAccount = 250.0
	config.Config.MaxMonthlyCountLimit = 5000
	config.Config.MaxDailyCountLimit = 500

	result := GetSystemDefaults()

	// Check tenant defaults
	tenantInv := result.Defaults[EntityTypeTenant][ModuleInvestigation]
	assert.Equal(t, 1000.0, tenantInv.MonthlyCostLimit)
	assert.Equal(t, 50.0, tenantInv.DailyCostLimit)
	assert.Equal(t, 500, tenantInv.MonthlyCountLimit)
	assert.Equal(t, 50, tenantInv.DailyCountLimit)

	// Check account defaults
	accountInv := result.Defaults[EntityTypeAccount][ModuleInvestigation]
	assert.Equal(t, 600.0, accountInv.MonthlyCostLimit)
	assert.Equal(t, 30.0, accountInv.DailyCostLimit)

	// Check max caps
	assert.Equal(t, 10000.0, result.MaxCaps.MonthlyCostTenant)
	assert.Equal(t, 5000.0, result.MaxCaps.MonthlyCostAccount)
	assert.Equal(t, 500.0, result.MaxCaps.DailyCostTenant)
	assert.Equal(t, 250.0, result.MaxCaps.DailyCostAccount)
	assert.Equal(t, 5000, result.MaxCaps.MonthlyCount)
	assert.Equal(t, 500, result.MaxCaps.DailyCount)
}
