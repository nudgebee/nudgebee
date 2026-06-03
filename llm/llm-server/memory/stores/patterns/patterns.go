package patterns

import "time"

// Pattern captures an inferred behavioral signal about a user's habits.
// Patterns decay over time; unlike Preferences, they carry a score and are
// retrieved via semantic search rather than key lookup.
type Pattern struct {
	ID          string    `db:"id" json:"id"`
	TenantID    string    `db:"tenant_id" json:"tenant_id"`
	UserID      string    `db:"user_id" json:"user_id"`
	AgentModule *string   `db:"agent_module" json:"agent_module,omitempty"`
	Kind        string    `db:"pattern_kind" json:"pattern_kind"`
	Subject     string    `db:"subject" json:"subject"`
	MetaJSON    []byte    `db:"metadata" json:"-"`
	Metadata    any       `db:"-" json:"metadata,omitempty"`
	Count       int       `db:"count" json:"count"`
	Score       float64   `db:"score" json:"score"`
	LastSeenAt  time.Time `db:"last_seen_at" json:"last_seen_at"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// Pattern kinds — typed so the ranker can weight them differently.
const (
	KindFrequentService         = "frequent_service"
	KindFrequentNamespace       = "frequent_namespace"
	KindPreferredDiagnosticFlow = "preferred_diagnostic_flow"
	KindAcceptedRecommendation  = "accepted_recommendation"
	KindDismissedRecommendation = "dismissed_recommendation"
	KindFrequentResourceType    = "frequent_resource_type"
)

// Decay constants for score computation.
const (
	DefaultDecayLambda = 0.05 // ~50% decay over 14 days
)
