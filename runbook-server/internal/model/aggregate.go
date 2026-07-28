package model

import "time"

// WorkflowCountRequest - filters for counting workflows
type WorkflowCountRequest struct {
	// AccountIDs narrows the count to specific accounts. Empty means "every
	// account the caller can read" — the tenant-level default (see #35113).
	AccountIDs  []string       `json:"account_ids,omitempty"`
	Status      WorkflowStatus `json:"status,omitempty"`       // ACTIVE, INACTIVE, PAUSED
	TriggerType string         `json:"trigger_type,omitempty"` // schedule, event, manual, webhook
}

// WorkflowCountResponse - workflow count result
type WorkflowCountResponse struct {
	Count int64 `json:"count"`
}

// WorkflowExecutionCountRequest - filters for counting executions
type WorkflowExecutionCountRequest struct {
	AccountID   string                  `json:"account_id"`
	StartDate   *time.Time              `json:"start_date,omitempty"`   // Filter executions after this date
	EndDate     *time.Time              `json:"end_date,omitempty"`     // Filter executions before this date
	Status      WorkflowExecutionStatus `json:"status,omitempty"`       // RUNNING, COMPLETED, FAILED, etc.
	TriggerType string                  `json:"trigger_type,omitempty"` // schedule, event, manual, webhook
	WorkflowID  string                  `json:"workflow_id,omitempty"`  // Filter by specific workflow
}

// WorkflowExecutionCountResponse - execution count result
type WorkflowExecutionCountResponse struct {
	Count int64 `json:"count"`
}
