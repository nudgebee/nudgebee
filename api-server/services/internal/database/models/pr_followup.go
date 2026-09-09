package models

import "time"

type PRFollowup struct {
	Id                string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	PRURL             string     `json:"pr_url" mapstructure:"pr_url" validate:"required" db:"pr_url"`
	TenantId          string     `json:"tenant_id" mapstructure:"tenant_id" validate:"required" db:"tenant_id"`
	PRLifecycleState  string     `json:"pr_lifecycle_state" mapstructure:"pr_lifecycle_state" validate:"required" db:"pr_lifecycle_state"`
	PRIterationCount  int        `json:"pr_iteration_count" mapstructure:"pr_iteration_count" validate:"required" db:"pr_iteration_count"`
	LastPRCheckAt     *time.Time `json:"last_pr_check_at" mapstructure:"last_pr_check_at" db:"last_pr_check_at"`
	PRFollowupPending bool       `json:"pr_followup_pending" mapstructure:"pr_followup_pending" validate:"required" db:"pr_followup_pending"`
	StatusMessage     *string    `json:"status_message" mapstructure:"status_message" db:"status_message"`
	CreatedAt         time.Time  `json:"created_at" mapstructure:"created_at" validate:"required" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" mapstructure:"updated_at" validate:"required" db:"updated_at"`
}
