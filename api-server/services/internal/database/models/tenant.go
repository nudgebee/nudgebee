package models

import "time"

type Tenant struct {
	Id        string    `json:"id" mapstructure:"id" db:"id" validate:"required"`
	Name      string    `json:"name" mapstructure:"name" db:"name" validate:"required"`
	CreatedAt time.Time `json:"created_at" mapstructure:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" mapstructure:"updated_at" db:"updated_at"`
	CreatedBy *string   `json:"created_by" mapstructure:"created_by" db:"created_by"`
	UpdatedBy *string   `json:"updated_by" mapstructure:"updated_by" db:"updated_by"`
	Type      string    `json:"type" mapstructure:"type" db:"type" validate:"required"`
}

type TenantAttributes struct {
	Id        string    `json:"id" mapstructure:"id" db:"id" validate:"required"`
	Name      string    `json:"name" mapstructure:"name" db:"name" validate:"required"`
	Value     string    `json:"value" mapstructure:"value" db:"value" validate:"required"`
	CreatedAt time.Time `json:"created_at" mapstructure:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" mapstructure:"updated_at" db:"updated_at"`
	TenantId  string    `json:"tenant_id" mapstructure:"tenant_id" db:"tenant_id" validate:"required"`
}
