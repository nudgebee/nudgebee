package entitlement

import (
	"time"
)

// Feature constants for entitlement features
const (
	FeatureTroubleshoot = "TROUBLESHOOT"
	FeatureOptimize     = "OPTIMIZE"
	FeatureWorkflows    = "WORKFLOWS"
)

// Dimension constants for usage metering
const (
	DimensionIncidents          = "incidents"
	DimensionCostOptimization   = "cost_optimization"
	DimensionWorkflowExecutions = "workflow_executions"
	DimensionAIWorkflowSteps    = "ai_workflow_steps"
)

// PlanType represents the source of a subscription plan
type PlanType string

const (
	PlanTypeWebsite PlanType = "website"
)

// SubscriptionStatus represents the status of a tenant subscription
type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusSuspended SubscriptionStatus = "suspended"
	SubscriptionStatusTrial     SubscriptionStatus = "trial"
)

// FeatureMapping maps a feature to its dimension and AWS metering info
type FeatureMapping struct {
	ID                   string   `db:"id" json:"id"`
	FeatureID            string   `db:"feature_id" json:"feature_id"`
	Dimension            string   `db:"dimension" json:"dimension"`
	AWSMeteredDimension  *string  `db:"aws_metered_dimension" json:"aws_metered_dimension,omitempty"`
	OverageRate          *float64 `db:"overage_rate" json:"overage_rate,omitempty"`
	IncludedLimitDefault *int     `db:"included_limit_default" json:"included_limit_default,omitempty"`
	Description          *string  `db:"description" json:"description,omitempty"`
}

// Plan represents a subscription plan definition
type Plan struct {
	ID               string    `db:"id" json:"id"`
	Name             string    `db:"name" json:"name"`
	DisplayName      string    `db:"display_name" json:"display_name"`
	PlanType         PlanType  `db:"plan_type" json:"plan_type"`
	ProductCode      *string   `db:"product_code" json:"product_code,omitempty"`
	BasePriceMonthly *float64  `db:"base_price_monthly" json:"base_price_monthly,omitempty"`
	IsActive         bool      `db:"is_active" json:"is_active"`
	CostTier         *string   `db:"cost_tier" json:"cost_tier,omitempty"`
	MaxAnnualSpend   *int      `db:"max_annual_spend" json:"max_annual_spend,omitempty"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

// PlanFeature links a plan to a feature
type PlanFeature struct {
	PlanID    string    `db:"plan_id" json:"plan_id"`
	FeatureID string    `db:"feature_id" json:"feature_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// PlanDimensionLimit stores plan-specific dimension limit overrides
type PlanDimensionLimit struct {
	PlanID        string    `db:"plan_id" json:"plan_id"`
	Dimension     string    `db:"dimension" json:"dimension"`
	IncludedLimit int       `db:"included_limit" json:"included_limit"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

// Subscription represents a tenant's active subscription
type Subscription struct {
	ID                   string             `db:"id" json:"id"`
	TenantID             string             `db:"tenant_id" json:"tenant_id"`
	PlanID               string             `db:"plan_id" json:"plan_id"`
	Status               SubscriptionStatus `db:"status" json:"status"`
	SubscriptionStart    time.Time          `db:"subscription_start" json:"subscription_start"`
	SubscriptionEnd      *time.Time         `db:"subscription_end" json:"subscription_end,omitempty"`
	EntitlementOverrides *string            `db:"entitlement_overrides" json:"entitlement_overrides,omitempty"` // JSONB
	BillingCycleStart    *time.Time         `db:"billing_cycle_start" json:"billing_cycle_start,omitempty"`
	IsPrivateContract    bool               `db:"is_private_contract" json:"is_private_contract"`
	CreatedAt            time.Time          `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time          `db:"updated_at" json:"updated_at"`
}

// Usage represents aggregated usage for a dimension in a billing period
type Usage struct {
	ID                  string    `db:"id" json:"id"`
	TenantID            string    `db:"tenant_id" json:"tenant_id"`
	BillingPeriod       time.Time `db:"billing_period" json:"billing_period"`
	Dimension           string    `db:"dimension" json:"dimension"`
	UsageCount          int       `db:"usage_count" json:"usage_count"`
	IncludedLimit       int       `db:"included_limit" json:"included_limit"`
	OverageCount        int       `db:"overage_count" json:"overage_count"`
	LastReportedOverage int       `db:"last_reported_overage" json:"last_reported_overage"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time `db:"updated_at" json:"updated_at"`
}

// UsageEvent represents a detailed usage event for audit
type UsageEvent struct {
	ID            string    `db:"id" json:"id"`
	TenantID      string    `db:"tenant_id" json:"tenant_id"`
	Dimension     string    `db:"dimension" json:"dimension"`
	ReferenceID   string    `db:"reference_id" json:"reference_id"`
	ReferenceType *string   `db:"reference_type" json:"reference_type,omitempty"`
	SessionID     *string   `db:"session_id" json:"session_id,omitempty"`
	IsBillable    bool      `db:"is_billable" json:"is_billable"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

// EntitlementStatus represents the result of an entitlement check
type EntitlementStatus struct {
	Allowed         bool   `json:"allowed"`
	Remaining       int    `json:"remaining"` // -1 for unlimited
	Used            int    `json:"used"`
	Limit           int    `json:"limit"` // -1 for unlimited
	OverageEnabled  bool   `json:"overage_enabled"`
	OverageCount    int    `json:"overage_count"`
	GracefulDegrade bool   `json:"graceful_degrade"` // At limit, no overage
	Message         string `json:"message,omitempty"`
}

// CheckEntitlementRequest is the request body for entitlement check API
type CheckEntitlementRequest struct {
	TenantID  string `json:"tenant_id" binding:"required"`
	Dimension string `json:"dimension" binding:"required"`
}

// CheckEntitlementResponse is the response for entitlement check API
type CheckEntitlementResponse struct {
	EntitlementStatus
}

// RecordUsageRequest is the request body for recording usage
type RecordUsageRequest struct {
	TenantID      string  `json:"tenant_id" binding:"required"`
	Dimension     string  `json:"dimension" binding:"required"`
	ReferenceID   string  `json:"reference_id" binding:"required"`
	ReferenceType *string `json:"reference_type,omitempty"`
	SessionID     *string `json:"session_id,omitempty"`
	IsBillable    *bool   `json:"is_billable,omitempty"` // defaults to true
}

// RecordUsageResponse is the response for recording usage
type RecordUsageResponse struct {
	Recorded        bool `json:"recorded"`
	IsOverage       bool `json:"is_overage"`
	NewUsageCount   int  `json:"new_usage_count"`
	NewOverageCount int  `json:"new_overage_count"`
}

// IsNewIncidentRequest checks if this is a new incident or a follow-up
type IsNewIncidentRequest struct {
	TenantID  string `json:"tenant_id" binding:"required"`
	EventID   string `json:"event_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
}

// IsNewIncidentResponse indicates if this is a new billable incident
type IsNewIncidentResponse struct {
	IsNew bool `json:"is_new"`
}

// GetStatusRequest is the request for getting tenant entitlement status
type GetStatusRequest struct {
	TenantID string `json:"tenant_id" binding:"required"`
}

// DimensionStatus represents status for a single dimension
type DimensionStatus struct {
	Dimension      string `json:"dimension"`
	Feature        string `json:"feature"`
	Used           int    `json:"used"`
	Limit          int    `json:"limit"` // -1 for unlimited
	Remaining      int    `json:"remaining"`
	OverageCount   int    `json:"overage_count"`
	OverageEnabled bool   `json:"overage_enabled"`
}

// GetStatusResponse is the response for tenant entitlement status
type GetStatusResponse struct {
	TenantID      string            `json:"tenant_id"`
	PlanName      string            `json:"plan_name"`
	PlanType      PlanType          `json:"plan_type"`
	Status        string            `json:"status"`
	BillingPeriod string            `json:"billing_period"`
	Dimensions    []DimensionStatus `json:"dimensions"`
	Features      []string          `json:"features"`
}

// OverageReport represents overage data for AWS metering
type OverageReport struct {
	TenantID            string `json:"tenant_id"`
	Dimension           string `json:"dimension"`
	AWSMeteredDimension string `json:"aws_metered_dimension"`
	NewOverage          int    `json:"new_overage"`
	TotalOverage        int    `json:"total_overage"`
	LastReportedOverage int    `json:"last_reported_overage"`
}

// EntitlementOverrides represents per-tenant limit overrides (for private contracts)
type EntitlementOverrides struct {
	Incidents          *int  `json:"incidents,omitempty"`
	WorkflowExecutions *int  `json:"workflow_executions,omitempty"`
	AIWorkflowSteps    *int  `json:"ai_workflow_steps,omitempty"`
	OverageEnabled     *bool `json:"overage_enabled,omitempty"`
}
