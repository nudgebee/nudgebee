package budget

import (
	"nudgebee/llm/events"
	"time"
)

// Module name constants for budget tracking
const (
	ModuleInvestigation     = "investigation"
	ModuleUserInvestigation = "user_investigation"
)

// validModules defines the allowed module names for budget checks
var validModules = map[string]bool{
	ModuleInvestigation:     true,
	ModuleUserInvestigation: true,
}

// moduleQueryFilters maps module names to their SQL query filters
// This is a whitelist approach to prevent SQL injection
var moduleQueryFilters = map[string]string{
	ModuleInvestigation:     " AND c.session_id LIKE '" + events.SessionIdPrefixEvent + "%'",
	ModuleUserInvestigation: "", // No additional filter for user investigation
}

// Entity type constants
const (
	EntityTypeTenant  = "tenant"
	EntityTypeAccount = "account"
)

// validEntityTypes defines allowed entity types
var validEntityTypes = map[string]bool{
	EntityTypeTenant:  true,
	EntityTypeAccount: true,
}

// DefaultBudgetLimitFallback is used when module is unknown
const TenantDefaultBudgetLimitFallback = 500.0
const AccountDefaultBudgetLimitFallback = 100.0

// BudgetConfig represents a row in llm_budget_config table
type BudgetConfig struct {
	ID         string `json:"id" db:"id"`
	EntityType string `json:"entity_type" db:"entity_type"`
	EntityID   string `json:"entity_id" db:"entity_id"`
	Module     string `json:"module" db:"module"`

	// Super admin: bypass all checks
	BudgetDisabled bool       `json:"budget_disabled" db:"budget_disabled"`
	DisabledBy     *string    `json:"disabled_by,omitempty" db:"disabled_by"`
	DisabledAt     *time.Time `json:"disabled_at,omitempty" db:"disabled_at"`

	// Monthly limits (NULL = use system default)
	MonthlyCostLimit    *float64 `json:"monthly_cost_limit" db:"monthly_cost_limit"`
	MonthlyCostEnabled  bool     `json:"monthly_cost_enabled" db:"monthly_cost_enabled"`
	MonthlyCountLimit   *int     `json:"monthly_count_limit" db:"monthly_count_limit"`
	MonthlyCountEnabled bool     `json:"monthly_count_enabled" db:"monthly_count_enabled"`

	// Daily limits (NULL = use system default)
	DailyCostLimit    *float64 `json:"daily_cost_limit" db:"daily_cost_limit"`
	DailyCostEnabled  bool     `json:"daily_cost_enabled" db:"daily_cost_enabled"`
	DailyCountLimit   *int     `json:"daily_count_limit" db:"daily_count_limit"`
	DailyCountEnabled bool     `json:"daily_count_enabled" db:"daily_count_enabled"`

	// Audit
	UpdatedBy *string   `json:"updated_by,omitempty" db:"updated_by"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// BudgetConfigUpsertRequest is the API input for creating/updating budget config
type BudgetConfigUpsertRequest struct {
	EntityType string `json:"entity_type" validate:"required"`
	EntityID   string `json:"entity_id" validate:"required"`
	Module     string `json:"module" validate:"required"`

	BudgetDisabled *bool `json:"budget_disabled,omitempty"` // super_admin only

	MonthlyCostLimit    *float64 `json:"monthly_cost_limit,omitempty"`
	MonthlyCostEnabled  *bool    `json:"monthly_cost_enabled,omitempty"`
	MonthlyCountLimit   *int     `json:"monthly_count_limit,omitempty"`
	MonthlyCountEnabled *bool    `json:"monthly_count_enabled,omitempty"`

	DailyCostLimit    *float64 `json:"daily_cost_limit,omitempty"`
	DailyCostEnabled  *bool    `json:"daily_cost_enabled,omitempty"`
	DailyCountLimit   *int     `json:"daily_count_limit,omitempty"`
	DailyCountEnabled *bool    `json:"daily_count_enabled,omitempty"`
}

// BudgetConfigListRequest is the query params for listing budget configs
type BudgetConfigListRequest struct {
	EntityType string `json:"entity_type" form:"entity_type"`
	EntityID   string `json:"entity_id" form:"entity_id"`
	Module     string `json:"module" form:"module"`
}

// BudgetStatusRequest represents the request for budget status
type BudgetStatusRequest struct {
	AccountID string `json:"account_id" validate:"required"`
}

// LimitInfo represents the status of a single cost-based limit
type LimitInfo struct {
	Enabled     bool    `json:"enabled"`
	Limit       float64 `json:"limit"`
	Usage       float64 `json:"usage"`
	Remaining   float64 `json:"remaining"`
	LimitSource string  `json:"limit_source"`
}

// CountLimitInfo represents the status of a single count-based limit
type CountLimitInfo struct {
	Enabled     bool   `json:"enabled"`
	Limit       int    `json:"limit"`
	Usage       int    `json:"usage"`
	Remaining   int    `json:"remaining"`
	LimitSource string `json:"limit_source"`
}

// EntityBudgetStatus contains all limit statuses for one entity+module
type EntityBudgetStatus struct {
	BudgetDisabled bool           `json:"budget_disabled"`
	DisabledBy     *string        `json:"disabled_by,omitempty"`
	DisabledAt     *time.Time     `json:"disabled_at,omitempty"`
	MonthlyCost    LimitInfo      `json:"monthly_cost"`
	DailyCost      LimitInfo      `json:"daily_cost"`
	MonthlyCount   CountLimitInfo `json:"monthly_count"`
	DailyCount     CountLimitInfo `json:"daily_count"`
}

// ModuleBudgetStatus contains tenant + account status for one module
type ModuleBudgetStatus struct {
	Tenant  EntityBudgetStatus `json:"tenant"`
	Account EntityBudgetStatus `json:"account"`
}

// BudgetStatusResponse is the full budget status response
type BudgetStatusResponse struct {
	TenantID          string             `json:"tenant_id"`
	AccountID         string             `json:"account_id"`
	Period            string             `json:"period"`
	Today             string             `json:"today"`
	Investigation     ModuleBudgetStatus `json:"investigation"`
	UserInvestigation ModuleBudgetStatus `json:"user_investigation"`
}

// SystemDefaultsResponse contains read-only system defaults and max caps
type SystemDefaultsResponse struct {
	Defaults map[string]map[string]SystemDefaultEntry `json:"defaults"` // [level][module]
	MaxCaps  MaxCapsInfo                              `json:"max_caps"`
}

// SystemDefaultEntry holds default values for one level+module combination
type SystemDefaultEntry struct {
	MonthlyCostLimit  float64 `json:"monthly_cost_limit"`
	DailyCostLimit    float64 `json:"daily_cost_limit"`
	MonthlyCountLimit int     `json:"monthly_count_limit"`
	DailyCountLimit   int     `json:"daily_count_limit"`
}

// MaxCapsInfo holds maximum values that admins cannot exceed
type MaxCapsInfo struct {
	MonthlyCostTenant  float64 `json:"monthly_cost_tenant"`
	MonthlyCostAccount float64 `json:"monthly_cost_account"`
	DailyCostTenant    float64 `json:"daily_cost_tenant"`
	DailyCostAccount   float64 `json:"daily_cost_account"`
	MonthlyCount       int     `json:"monthly_count"`
	DailyCount         int     `json:"daily_count"`
}
