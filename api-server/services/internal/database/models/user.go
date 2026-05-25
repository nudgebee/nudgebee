package models

import "time"

type User struct {
	Id          string    `json:"id" mapstructure:"id" db:"id" validate:"required"`
	Username    string    `json:"username" mapstructure:"username" db:"username" validate:"required"`
	DisplayName string    `json:"display_name" mapstructure:"display_name" db:"display_name" validate:"required"`
	Status      string    `json:"status" mapstructure:"status" db:"status" validate:"required"`
	CreatedAt   time.Time `json:"created_at" mapstructure:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" mapstructure:"updated_at" db:"updated_at"`
	CreatedBy   *string   `json:"created_by" mapstructure:"created_by" db:"created_by"`
	UpdatedBy   *string   `json:"updated_by" mapstructure:"updated_by" db:"updated_by"`
}
