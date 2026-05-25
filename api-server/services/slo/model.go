package slo

import "time"

type DBSLOConfig struct {
	Id              string     `json:"id" validate:"required" db:"id"`
	Name            string     `json:"name" validate:"required" db:"name,omitempty"`
	Description     string     `json:"description,omitempty" db:"description,omitempty"`
	Window          int        `json:"window,omitempty" db:"window,omitempty"`
	Goal            float64    `json:"goal,omitempty" db:"goal,omitempty"`
	Schedule        string     `json:"schedule,omitempty" db:"schedule,omitempty"`
	CreatedBy       string     `json:"created_by,omitempty" db:"created_by,omitempty"`
	UpdatedBy       *string    `json:"updated_by,omitempty" db:"updated_by,omitempty"`
	Method          string     `json:"method,omitempty" db:"method,omitempty"`
	Expression      *string    `json:"expression,omitempty" db:"histogram_query,omitempty"`
	FilterGood      string     `json:"filter_good,omitempty" db:"filter_good_query,omitempty"`
	FilterBad       string     `json:"filter_bad,omitempty" db:"filter_bad_query,omitempty"`
	FilterValid     string     `json:"filter_valid,omitempty" db:"filter_valid_query,omitempty"`
	StartTime       string     `json:"start_time,omitempty" db:"start_time,omitempty"`
	EndTime         string     `json:"end_time,omitempty" db:"end_time,omitempty"`
	ThresholdBucket float64    `json:"error_budget_burn_rate_threshold,omitempty" db:"threshold,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty" db:"created_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty" db:"updated_at,omitempty"`
	CloudAccountId  string     `json:"cloud_account_id,omitempty" db:"cloud_account_id,omitempty"`
	TenantId        string     `json:"tenant_id,omitempty" db:"tenant_id,omitempty"`
	Enabled         bool       `json:"enabled,omitempty" db:"enabled,omitempty"`
	WorkloadName    string     `json:"workload_name" db:"workload_name,omitempty"`
	Namespace       string     `json:"workload_namespace" db:"workload_namespace,omitempty"`
	WorkloadId      string     `json:"workload_id" db:"workload_id,omitempty"`
}

type SLOConfig struct {
	Name            string  `json:"name" validate:"required" db:"name,omitempty"`
	Description     string  `json:"description,omitempty" db:"description,omitempty"`
	Window          int     `json:"window,omitempty" db:"window,omitempty"`
	Goal            float64 `json:"goal,omitempty" db:"goal,omitempty"`
	Schedule        string  `json:"schedule,omitempty" db:"schedule,omitempty"`
	Method          string  `json:"method,omitempty" db:"method,omitempty"`
	Expression      *string `json:"expression,omitempty" db:"histogram_query,omitempty"`
	FilterGood      string  `json:"filter_good,omitempty" db:"filter_good_query,omitempty"`
	FilterBad       string  `json:"filter_bad,omitempty" db:"filter_bad_query,omitempty"`
	FilterValid     string  `json:"filter_valid,omitempty" db:"filter_valid_query,omitempty"`
	StartTime       string  `json:"start_time,omitempty" db:"start_time,omitempty"`
	EndTime         string  `json:"end_time,omitempty" db:"end_time,omitempty"`
	ThresholdBucket float64 `json:"threshold_bucket,omitempty" db:"threshold,omitempty"`
}

type SLOReport struct {
	Workload  string `json:"workload"`
	Namespace string `json:"namespace"`
	// SLO
	Name string  `json:"name"`
	Goal float64 `json:"goal"`

	// SLI
	Gap float64 `json:"gap"`

	// Error budget
	ErrorBudgetTarget            float64 `json:"error_budget_target"`
	ErrorBudgetMeasurement       float64 `json:"error_budget_measurement"`
	ErrorBudgetBurnRate          float64 `json:"error_budget_burn_rate"`
	ErrorBudgetBurnRateThreshold float64 `json:"error_budget_burn_rate_threshold"`
	ErrorBudgetMinutes           float64 `json:"error_budget_minutes"`
	ErrorBudgetRemainingMinutes  float64 `json:"error_budget_remaining_minutes"`
	ErrorMinutes                 float64 `json:"error_minutes"`
	ErrorBudgetConsumedRatio     float64 `json:"error_budget_consumed_ratio"`

	// Data validation
	Valid  bool `json:"valid"`
	Window int  `json:"window"`
	Alert  bool `json:"alert"`

	// SLO
	ErrorBudgetPolicy string `json:"error_budget_policy"`

	// SLI
	SLIMeasurement  float64 `json:"sli_measurement"`
	EventsCount     int     `json:"events_count"`
	BadEventsCount  int     `json:"bad_events_count"`
	GoodEventsCount int     `json:"good_events_count"`
	StartTime       float64 `json:"start_time,omitempty" db:"start_time,omitempty"`
	EndTime         float64 `json:"end_time,omitempty" db:"end_time,omitempty"`
}

type SLORequest struct {
	AccountId    string             `json:"cloud_account_id" validate:"required"`
	WorkloadId   string             `json:"workload_id" validate:"required"`
	WorkloadName string             `json:"workload_name" validate:"required"`
	Namespace    string             `json:"namespace" validate:"required"`
	Config       []SLOConfigRequest `json:"config" mapstructure:"config" validate:"required"`
}

type SLOConfigRequest struct {
	Name      string  `json:"name" validate:"required"`
	Threshold float64 `json:"threshold" validate:"required"`
	Goal      float64 `json:"goal" validate:"required"`
	Enabled   bool    `json:"enabled,omitempty"`
}

type SLOListRequest struct {
	AccountId    string   `json:"cloud_account_id" validate:"required"`
	WorkloadId   []string `json:"workload_id" validate:"required"`
	WorkloadName []string `json:"workload_name" validate:"required"`
	Namespace    []string `json:"namespace" validate:"required"`
}

type SLOResponse struct {
	Id           string              `json:"id" validate:"required"`
	AccountId    string              `json:"cloud_account_id" validate:"required"`
	WorkloadId   string              `json:"workload_id" validate:"required"`
	WorkloadName string              `json:"workload_name" validate:"required"`
	Namespace    string              `json:"namespace" validate:"required"`
	Config       []SLOConfigResponse `json:"config" mapstructure:"config" validate:"required"`
}

type SLOConfigResponse struct {
	Id        string  `json:"id" validate:"required"`
	Name      string  `json:"name" validate:"required"`
	Threshold float64 `json:"threshold" validate:"required"`
	Enabled   bool    `json:"enabled,omitempty"`
	Goal      float64 `json:"goal,omitempty"`
}

type DBSLOReport struct {
	Id                           string     `json:"id" db:"id"`
	ConfigId                     string     `json:"config_id" db:"config_id"`
	Gap                          float64    `json:"gap" db:"gap"`
	ErrorBudgetTarget            float64    `json:"error_budget_target" db:"error_budget_target"`
	ErrorBudgetMeasurement       float64    `json:"error_budget_measurement" db:"error_budget_measurement"`
	ErrorBudgetBurnRate          float64    `json:"error_budget_burn_rate" db:"error_budget_burn_rate"`
	ErrorBudgetBurnRateThreshold float64    `json:"error_budget_burn_rate_threshold" db:"error_budget_burn_rate_threshold"`
	ErrorBudgetMinutes           float64    `json:"error_budget_minutes" db:"error_budget_minutes"`
	ErrorBudgetRemainingMinutes  float64    `json:"error_budget_remaining_minutes" db:"error_budget_remaining_minutes"`
	ErrorMinutes                 float64    `json:"error_minutes" db:"error_minutes"`
	ErrorBudgetConsumedRatio     float64    `json:"error_budget_consumed_ratio" db:"error_budget_consumed_ratio"`
	Status                       string     `json:"status" db:"status"`
	BadEventsCount               int        `json:"bad_events_count" db:"bad_events_count"`
	GoodEventsCount              int        `json:"good_events_count" db:"good_events_count"`
	EventsCount                  int        `json:"events_count" db:"events_count"`
	SLIMeasurement               float64    `json:"sli_measurement" db:"sli_measurement"`
	TenantId                     string     `json:"tenant_id" db:"tenant_id"`
	CloudAccountId               string     `json:"cloud_account_id" db:"cloud_account_id"`
	WorkloadName                 string     `json:"workload_name" db:"workload_name"`
	WorkloadNamespace            string     `json:"workload_namespace" db:"workload_namespace"`
	Timestamp                    *time.Time `json:"timestamp" db:"timestamp"`
	CreatedAt                    *time.Time `json:"created_at,omitempty" db:"created_at,omitempty"`
	UpdatedAt                    *time.Time `json:"updated_at,omitempty" db:"updated_at,omitempty"`
}
