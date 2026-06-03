package models

import (
	"time"

	"github.com/jmoiron/sqlx/types"
)

type EventHistory struct {
	Id             string          `json:"id" db:"id"`
	EventId        string          `json:"event_id" db:"event_id"`
	ChangedAt      time.Time       `json:"changed_at" db:"changed_at"`
	ChangedBy      *string         `json:"changed_by" db:"changed_by"`
	ChangeType     string          `json:"change_type" db:"change_type"`
	OldValue       *types.JSONText `json:"old_value" db:"old_value"`
	NewValue       types.JSONText  `json:"new_value" db:"new_value"`
	ChangeReason   string          `json:"change_reason" db:"change_reason"`
	Metadata       *types.JSONText `json:"metadata" db:"metadata"`
	TenantId       string          `json:"tenant_id" db:"tenant_id"`
	CloudAccountId string          `json:"cloud_account_id" db:"cloud_account_id"`
}
