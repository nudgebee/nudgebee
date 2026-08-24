package models

import (
	"encoding/json"
	"time"
)

// AddressedComment is one PR comment the followup agent has already handled,
// recorded so the answer survives independently of GitHub (#36865). It is a
// durable record and a FALLBACK skip source — GitHub's own resolved-thread
// state stays authoritative, so a human who un-resolves a thread still gets
// the comment re-raised.
//
// Source is part of the identity alongside CommentID because issue comments
// and review submissions are separate GitHub id spaces and can collide.
type AddressedComment struct {
	Source      string    `json:"source"`     // "inline" | "issue_comment" | "review_body"
	CommentID   int64     `json:"comment_id"` // GitHub's numeric comment id
	Action      string    `json:"action"`     // "fixed" | "acknowledged" | "wont_fix"
	AddressedAt time.Time `json:"addressed_at"`
}

type PRFollowup struct {
	Id                string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	PRURL             string     `json:"pr_url" mapstructure:"pr_url" validate:"required" db:"pr_url"`
	TenantId          string     `json:"tenant_id" mapstructure:"tenant_id" validate:"required" db:"tenant_id"`
	PRLifecycleState  string     `json:"pr_lifecycle_state" mapstructure:"pr_lifecycle_state" validate:"required" db:"pr_lifecycle_state"`
	PRIterationCount  int        `json:"pr_iteration_count" mapstructure:"pr_iteration_count" validate:"required" db:"pr_iteration_count"`
	LastPRCheckAt     *time.Time `json:"last_pr_check_at" mapstructure:"last_pr_check_at" db:"last_pr_check_at"`
	PRFollowupPending bool       `json:"pr_followup_pending" mapstructure:"pr_followup_pending" validate:"required" db:"pr_followup_pending"`
	StatusMessage     *string    `json:"status_message" mapstructure:"status_message" db:"status_message"`
	// AddressedComments is a jsonb array of AddressedComment. Kept as raw JSON
	// so the row can be scanned without a custom sql.Scanner; callers that need
	// the entries unmarshal them.
	AddressedComments json.RawMessage `json:"addressed_comments" mapstructure:"addressed_comments" db:"addressed_comments"`
	CreatedAt         time.Time       `json:"created_at" mapstructure:"created_at" validate:"required" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" mapstructure:"updated_at" validate:"required" db:"updated_at"`
}
