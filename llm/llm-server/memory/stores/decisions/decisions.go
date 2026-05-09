package decisions

import "time"

// Decision records a choice a user made in a specific conversation.
// Immutable once recorded. Corrections are new decisions that reference the
// prior one via ContextJSON so history is auditable.
type Decision struct {
	ID             string    `db:"id" json:"id"`
	TenantID       string    `db:"tenant_id" json:"tenant_id"`
	UserID         string    `db:"user_id" json:"user_id"`
	ConversationID *string   `db:"conversation_id" json:"conversation_id,omitempty"`
	AgentModule    *string   `db:"agent_module" json:"agent_module,omitempty"`
	DecisionType   string    `db:"decision_type" json:"decision_type"`
	Subject        string    `db:"subject" json:"subject"`
	ContextJSON    []byte    `db:"context" json:"-"`
	Context        any       `db:"-" json:"context,omitempty"`
	OutcomeJSON    []byte    `db:"outcome" json:"-"`
	Outcome        any       `db:"-" json:"outcome,omitempty"`
	DecidedAt      time.Time `db:"decided_at" json:"decided_at"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}

// Decision types — narrow vocabulary so the ranker can weight consistently.
const (
	TypeRunbookChosen         = "runbook_chosen"
	TypeRecommendationAccepted = "recommendation_accepted"
	TypeRecommendationDismissed = "recommendation_dismissed"
	TypeToolSelected          = "tool_selected"
	TypeRootCauseAgreed       = "root_cause_agreed"
	TypeRootCauseDisagreed    = "root_cause_disagreed"
	TypeActionApproved        = "action_approved"
	TypeActionDeclined        = "action_declined"
)
