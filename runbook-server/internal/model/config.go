package model

import (
	"time"
)

type ConfigType string

const (
	ConfigTypeConfig ConfigType = "config"
	ConfigTypeSecret ConfigType = "secret"
)

// Config represents a workflow config or secret.
//
// AccountID is a pointer to support tenant-scoped rows: a nil/empty AccountID
// means the row is shared across all accounts in the tenant (a "tenant-level"
// config). When AccountID is set, the row is account-scoped and overrides any
// tenant-level row with the same key for that account at runtime.
type Config struct {
	ID        string            `json:"id" db:"id"`
	Key       string            `json:"key" db:"key" validate:"required"`
	Value     string            `json:"value" db:"value" validate:"required"`
	Type      ConfigType        `json:"type" db:"type" validate:"required,oneof=config secret"`
	Labels    map[string]string `json:"labels,omitempty" db:"labels"`
	Metadata  map[string]any    `json:"metadata,omitempty" db:"metadata"`
	TenantID  string            `json:"tenant_id" db:"tenant_id"`
	AccountID *string           `json:"account_id,omitempty" db:"account_id"`
	CreatedAt time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt time.Time         `json:"updated_at" db:"updated_at"`
	CreatedBy string            `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy string            `json:"updated_by,omitempty" db:"updated_by"`
}

// IsTenantScoped reports whether this config is a tenant-level (shared) row.
func (c *Config) IsTenantScoped() bool {
	return c.AccountID == nil || *c.AccountID == ""
}
