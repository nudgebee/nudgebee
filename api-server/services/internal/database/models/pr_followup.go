package models

import (
	"encoding/json"
	"time"
)

// AddressedComment is one piece of PR feedback the followup agent has already
// handled — a comment (#36865) or a failing CI run (#39995) — recorded so the
// answer survives independently of GitHub. It is a durable record and a
// FALLBACK skip source — GitHub's own resolved-thread state stays
// authoritative, so a human who un-resolves a thread still gets the comment
// re-raised.
//
// Source is part of the identity alongside CommentID because issue comments,
// review submissions and CI runs are separate id spaces and can collide. This
// service stores and returns the entries as-is; only the agent gives a source
// its meaning.
type AddressedComment struct {
	Source      string    `json:"source"`     // "inline" | "issue_comment" | "review_body" | "check_run"
	CommentID   int64     `json:"comment_id"` // the provider's numeric id for the comment or CI run
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
	// Run liveness (#38860). RunToken identifies the current run and RunConversationID
	// the llm-server conversation it polls; RunHeartbeatAt is stamped while a
	// poller is alive. All are NULL when no run is in flight, or for a run started
	// by a pod that predates them.
	RunToken          *string    `json:"run_token" mapstructure:"run_token" db:"run_token"`
	RunConversationID *string    `json:"run_conversation_id" mapstructure:"run_conversation_id" db:"run_conversation_id"`
	RunHeartbeatAt    *time.Time `json:"run_heartbeat_at" mapstructure:"run_heartbeat_at" db:"run_heartbeat_at"`
	CreatedAt         time.Time  `json:"created_at" mapstructure:"created_at" validate:"required" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" mapstructure:"updated_at" validate:"required" db:"updated_at"`
}
