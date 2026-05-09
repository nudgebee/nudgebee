package billing

import (
	"time"
)

type Billing struct {
	ID               string    `json:"id" validate:"required" db:"id"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
	TenantID         string    `json:"tenant_id" validate:"required" db:"tenant_id"`
	Tier             string    `json:"tier" db:"tier"`
	LastBilledDate   time.Time `json:"last_billed_date" db:"last_billed_date"`
	LastBilledAmount float32   `json:"last_billed_amount" db:"last_billed_amount"`
	AmountDue        float32   `json:"amount_due" db:"amount_due"`
	TotalPaid        float32   `json:"total_paid" db:"total_paid"`
}

type UsageCostPerUnit struct {
	ID          string    `json:"id" db:"id" validate:"required"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
	TenantID    string    `json:"tenant_id" validate:"required" db:"tenant_id"`
	AccountId   string    `json:"account_id" db:"account_id"`
	BillingDate time.Time `json:"billing_date" validate:"required" db:"billing_date"`
	ServiceName string    `json:"service_name" validate:"required" db:"service_name"`
	Name        string    `json:"name" validate:"required" db:"name"`
	Units       int       `json:"units" validate:"required" db:"units"`
	CostPerUnit float32   `json:"cost_per_unit" validate:"required" db:"cost_per_unit"`
	TotalCost   float32   `json:"total_cost" validate:"required" db:"total_cost"`
}

type PastAutomationRuns struct {
	TotalUnits int     `db:"total_units"`
	TotalCost  float32 `db:"total_cost"`
}

type AccountAutomationRuns struct {
	AccountId string `db:"account_id"`
	Count     int    `db:"count"`
}

type GenerateChargePayload struct {
	TenantID string    `json:"tenant_id" validate:"required"`
	Days     int       `json:"days"`
	FromDate time.Time `json:"from_date"`
	ToDate   time.Time `json:"to_date"`
}
