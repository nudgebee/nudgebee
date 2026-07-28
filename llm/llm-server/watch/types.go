// Package watch implements background "watch" tasks for the llm-server: an
// agent calls the watch_resource tool to register an observation that should
// be checked periodically; a leader-elected dispatcher polls each watch's
// configured source on a fixed cadence, evaluates a predicate against the
// observation, and notifies the user's conversation when the predicate is
// met or the watch expires.
//
// The package is deliberately decoupled from the agent execution path:
// polls run under a fresh, isolated context — they never re-enter the user's
// conversation history. The conversation ID stored on a watch is only the
// destination for the single terminal notification.
package watch

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a watch row.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusActive    Status = "ACTIVE"
	StatusCompleted Status = "COMPLETED"
	StatusExpired   Status = "EXPIRED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

// IsTerminal reports whether the status is a final state (no more polling).
func (s Status) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusExpired, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// SourceKind identifies which Source implementation observes the watched
// resource on each poll. New kinds (timer, listen, webhook, composite) can be
// added without a schema change — the configuration is JSON.
type SourceKind string

const (
	SourceKindTool SourceKind = "tool"
	SourceKindSQL  SourceKind = "sql"
)

// PredicateKind identifies how an observation is evaluated for termination.
// Each kind has a single predicate_expr string interpreted differently.
type PredicateKind string

const (
	PredicateKindRegex     PredicateKind = "regex"
	PredicateKindSubstring PredicateKind = "substring"
	PredicateKindLLMJudge  PredicateKind = "llm_judge"
)

// Watch is the persisted form of a watch task — one row in llm_watch_tasks.
//
// Field tags match the SQL columns directly for use with sqlx. The two
// JSON-bodied columns (source_config) are stored as a string here and parsed
// on demand by the Source registry; this keeps the DAO ignorant of source
// shape, which is the whole point of the source abstraction.
type Watch struct {
	ID             uuid.UUID  `db:"id"`
	ConversationID uuid.UUID  `db:"conversation_id"`
	AccountID      uuid.UUID  `db:"account_id"`
	TenantID       uuid.UUID  `db:"tenant_id"`
	UserID         *uuid.UUID `db:"user_id"`
	// ParentMessageID points at the agent reply the watch was registered
	// against. The responder targets this specific message instead of "the
	// latest generation row", which would silently land the terminal
	// follow-up on an unrelated reply if the user kept chatting while the
	// watch was running. Nullable for legacy rows; the responder falls
	// back to the latest-generation heuristic when nil.
	ParentMessageID *uuid.UUID `db:"parent_message_id"`

	SourceKind   SourceKind `db:"source_kind"`
	SourceConfig string     `db:"source_config"`

	PredicateKind   PredicateKind `db:"predicate_kind"`
	PredicateExpr   string        `db:"predicate_expr"`
	PredicateNegate bool          `db:"predicate_negate"`

	NotifyTemplate  *string `db:"notify_template"`
	PollIntervalSec int     `db:"poll_interval_sec"`
	MaxDurationSec  int     `db:"max_duration_sec"`

	Status       Status `db:"status"`
	PollCount    int    `db:"poll_count"`
	FailureCount int    `db:"failure_count"`

	NextPollAt     time.Time  `db:"next_poll_at"`
	LastPollAt     *time.Time `db:"last_poll_at"`
	LastPollResult *string    `db:"last_poll_result"`
	FinalResult    *string    `db:"final_result"`
	Error          *string    `db:"error"`
	ExpiresAt      time.Time  `db:"expires_at"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// CreateInput is the in-memory request shape used by callers (the
// watch_resource tool, future API handlers) to register a new watch. The
// manager validates and clamps values, then persists a Watch row.
type CreateInput struct {
	ConversationID uuid.UUID
	AccountID      uuid.UUID
	TenantID       uuid.UUID
	UserID         *uuid.UUID
	// ParentMessageID — see Watch.ParentMessageID. Optional; recommended for
	// any caller that has a parent reply (the watch_resource tool sets it
	// from the surrounding tool-call context).
	ParentMessageID *uuid.UUID

	SourceKind   SourceKind
	SourceConfig json.RawMessage

	PredicateKind   PredicateKind
	PredicateExpr   string
	PredicateNegate bool

	NotifyTemplate  string
	PollIntervalSec int
	MaxDurationSec  int
}

// Observation is what a Source.Observe returns for a single poll. Data is the
// raw textual representation of the observation (JSON, plain text, etc.) that
// the predicate evaluator consumes.
type Observation struct {
	Data string
	// Truncated indicates the data was clipped to fit storage limits;
	// callers may still evaluate the predicate but should be aware that
	// matches against tail content may be missed.
	Truncated bool
}

// PredicateResult is the outcome of evaluating a single observation. Met
// drives state transitions; Summary becomes the body of the notification (or
// is fed into a notify_template) when Met is true.
type PredicateResult struct {
	Met     bool
	Summary string
}

// MaxObservationStorageBytes caps how much of an observation we persist on
// the row in last_poll_result. Polls themselves can return larger payloads —
// the predicate sees the full observation; we only truncate for storage.
const MaxObservationStorageBytes = 8 * 1024
