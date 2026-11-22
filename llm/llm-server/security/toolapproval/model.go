package toolapproval

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type MatchKind string

const (
	MatchTool         MatchKind = "tool"
	MatchExactCommand MatchKind = "exact_command"
	MatchCommandRegex MatchKind = "command_regex"
	MatchArguments    MatchKind = "arguments"
)

type Operator string

const (
	OperatorEquals Operator = "equals"
	OperatorRegex  Operator = "regex"
)

type Condition struct {
	Path     string          `json:"path"`
	Operator Operator        `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

type Rule struct {
	ID       uuid.UUID `json:"id" db:"id"`
	TenantID uuid.UUID `json:"tenant_id" db:"tenant_id"`
	// AccountID is nil for an organisation rule: it applies to every account
	// in the tenant, including accounts added later. Such a rule may pin an
	// integration (ToolConfigID), never a cluster or cloud account.
	AccountID      *uuid.UUID      `json:"account_id" db:"account_id"`
	Name           string          `json:"name" db:"name"`
	Enabled        bool            `json:"enabled" db:"enabled"`
	ToolName       string          `json:"tool_name" db:"tool_name"`
	ToolConfigID   *string         `json:"tool_config_id,omitempty" db:"tool_config_id"`
	MatchKind      MatchKind       `json:"match_kind" db:"match_kind"`
	CommandPattern *string         `json:"command_pattern,omitempty" db:"command_pattern"`
	WorkingDir     *string         `json:"working_dir,omitempty" db:"working_dir"`
	Conditions     json.RawMessage `json:"conditions" db:"conditions"`
	// AllowUnattended lets the rule waive confirmation in runs with no
	// signed-in person (system jobs, token-only API calls, unlinked chat
	// users). Off by default: such runs keep the confirmation.
	AllowUnattended bool       `json:"allow_unattended" db:"allow_unattended"`
	Revision        int        `json:"revision" db:"revision"`
	CreatedBy       *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy       *uuid.UUID `json:"updated_by,omitempty" db:"updated_by"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// AllAccounts reports whether the rule applies to every account in the tenant.
func (r Rule) AllAccounts() bool { return r.AccountID == nil }

type Match struct {
	RuleID   uuid.UUID `json:"rule_id"`
	Revision int       `json:"revision"`
	RuleName string    `json:"rule_name"`
	Kind     MatchKind `json:"match_kind"`
}

type Decision struct {
	TenantID         uuid.UUID
	AccountID        uuid.UUID
	RuleID           uuid.UUID
	RuleRevision     int
	RuleName         string
	MatchKind        MatchKind
	ToolName         string
	ToolConfigID     *string
	InputFingerprint string
	UserID           string
	ConversationID   string
	MessageID        string
	AgentID          string
	ToolCallID       string
}
