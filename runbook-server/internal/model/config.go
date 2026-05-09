package model

import (
	"time"
)

type ConfigType string

const (
	ConfigTypeConfig ConfigType = "config"
	ConfigTypeSecret ConfigType = "secret"
)

type Config struct {
	ID        string            `json:"id" db:"id"`
	Key       string            `json:"key" db:"key" validate:"required"`
	Value     string            `json:"value" db:"value" validate:"required"`
	Type      ConfigType        `json:"type" db:"type" validate:"required,oneof=config secret"`
	Labels    map[string]string `json:"labels,omitempty" db:"labels"`
	Metadata  map[string]any    `json:"metadata,omitempty" db:"metadata"`
	TenantID  string            `json:"tenant_id" db:"tenant_id"`
	AccountID string            `json:"account_id" db:"account_id"`
	CreatedAt time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt time.Time         `json:"updated_at" db:"updated_at"`
	CreatedBy string            `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy string            `json:"updated_by,omitempty" db:"updated_by"`
}
