package prompts

import (
	"time"

	"github.com/google/uuid"
)

// PromptCategory defines the category of a prompt
type PromptCategory string

const (
	CategoryAgents    PromptCategory = "agents"
	CategoryPlanners  PromptCategory = "planners"
	CategoryTools     PromptCategory = "tools"
	CategoryUtilities PromptCategory = "utilities"
)

// ConfigSource indicates where the prompt configuration came from
type ConfigSource string

const (
	ConfigSourceExperiment ConfigSource = "experiment"
	ConfigSourceDatabase   ConfigSource = "database"
	ConfigSourceDefault    ConfigSource = "default"
)

// PromptRequest contains parameters for loading a prompt
type PromptRequest struct {
	Name      string         // Prompt name (e.g., "k8s_debug")
	Category  PromptCategory // Category (agents, planners, tools, utilities)
	Provider  string         // LLM provider (bedrock, azure, openai, etc.)
	AccountID string         // Account ID for experiments and overrides
}

// PromptResponse contains the loaded prompt and metadata
type PromptResponse struct {
	Content  string         // The prompt text
	Metadata PromptMetadata // Metadata about resolution
}

// PromptMetadata contains information about how the prompt was resolved
type PromptMetadata struct {
	Version        string         // Version used (e.g., "v1", "v2")
	Provider       string         // Provider used
	Category       PromptCategory // Category
	ConfigSource   ConfigSource   // Where config came from
	ExperimentID   *uuid.UUID     // Experiment ID if from experiment
	ExperimentName *string        // Experiment name if from experiment
	CacheHit       bool           // Whether this was served from cache
	LoadTimeMs     int64          // Time taken to load (milliseconds)
}

// DBConfig represents a configuration entry in the database
type DBConfig struct {
	ID            uuid.UUID      `db:"id"`
	PromptName    string         `db:"prompt_name"`
	Category      PromptCategory `db:"category"`
	Provider      string         `db:"provider"`
	ActiveVersion string         `db:"active_version"`
	AccountID     *string        `db:"account_id"` // NULL for global
	Enabled       bool           `db:"enabled"`
	Priority      int            `db:"priority"`
	Notes         *string        `db:"notes"`
	UpdatedAt     time.Time      `db:"updated_at"`
	UpdatedBy     *string        `db:"updated_by"`
}

// DBExperiment represents an experiment entry in the database
type DBExperiment struct {
	ID             uuid.UUID      `db:"id"`
	Name           string         `db:"name"`
	PromptName     string         `db:"prompt_name"`
	Category       PromptCategory `db:"category"`
	TestVersion    string         `db:"test_version"`
	ControlVersion string         `db:"control_version"`
	TargetAccounts []string       `db:"target_accounts"` // PostgreSQL array
	Providers      []string       `db:"providers"`       // PostgreSQL array
	StartDate      *time.Time     `db:"start_date"`
	EndDate        *time.Time     `db:"end_date"`
	Enabled        bool           `db:"enabled"`
	Description    *string        `db:"description"`
	CreatedAt      time.Time      `db:"created_at"`
	CreatedBy      *string        `db:"created_by"`
	UpdatedAt      time.Time      `db:"updated_at"`
	UpdatedBy      *string        `db:"updated_by"`
}

// DBAuditLog represents an audit log entry
type DBAuditLog struct {
	ID           uuid.UUID      `db:"id"`
	PromptName   string         `db:"prompt_name"`
	Category     PromptCategory `db:"category"`
	Provider     *string        `db:"provider"`
	AccountID    *string        `db:"account_id"`
	Action       string         `db:"action"`
	OldVersion   *string        `db:"old_version"`
	NewVersion   *string        `db:"new_version"`
	ExperimentID *uuid.UUID     `db:"experiment_id"`
	ChangedBy    string         `db:"changed_by"`
	ChangedAt    time.Time      `db:"changed_at"`
	Reason       *string        `db:"reason"`
	Metadata     map[string]any `db:"metadata"` // JSONB
}

// DBMetrics represents a metrics entry
type DBMetrics struct {
	ID             uuid.UUID      `db:"id"`
	PromptName     string         `db:"prompt_name"`
	Category       PromptCategory `db:"category"`
	Provider       string         `db:"provider"`
	Version        string         `db:"version"`
	AccountID      *string        `db:"account_id"`
	ConversationID *string        `db:"conversation_id"`
	AgentName      *string        `db:"agent_name"`
	LoadTimeMs     *int           `db:"load_time_ms"`
	CacheHit       *bool          `db:"cache_hit"`
	ConfigSource   *string        `db:"config_source"`
	ExperimentID   *uuid.UUID     `db:"experiment_id"`
	ExperimentName *string        `db:"experiment_name"`
	Error          bool           `db:"error"`
	ErrorMessage   *string        `db:"error_message"`
	Timestamp      time.Time      `db:"timestamp"`
}

// ResolvedConfig represents a resolved configuration (version + provider)
type ResolvedConfig struct {
	Version        string
	Provider       string
	ConfigSource   ConfigSource
	ExperimentID   *uuid.UUID
	ExperimentName *string
}

// ExperimentCreateRequest represents a request to create an experiment
type ExperimentCreateRequest struct {
	Name           string   `json:"name" binding:"required"`
	PromptName     string   `json:"prompt_name" binding:"required"`
	Category       string   `json:"category" binding:"required"`
	TestVersion    string   `json:"test_version" binding:"required"`
	ControlVersion string   `json:"control_version" binding:"required"`
	TargetAccounts []string `json:"target_accounts" binding:"required,min=1"`
	Providers      []string `json:"providers,omitempty"`
	StartDate      *string  `json:"start_date,omitempty"`
	EndDate        *string  `json:"end_date,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// ExperimentUpdateAccountsRequest represents a request to update experiment accounts
type ExperimentUpdateAccountsRequest struct {
	Action   string   `json:"action" binding:"required,oneof=add remove set"`
	Accounts []string `json:"accounts" binding:"required,min=1"`
}

// ConfigVersionUpdateRequest represents a request to update active version
type ConfigVersionUpdateRequest struct {
	PromptName string  `json:"prompt_name" binding:"required"`
	Category   string  `json:"category" binding:"required"`
	Provider   string  `json:"provider"`
	AccountID  *string `json:"account_id"`
	NewVersion string  `json:"new_version" binding:"required"`
	Reason     string  `json:"reason,omitempty"`
}

// ExperimentMetricsResponse represents experiment metrics
type ExperimentMetricsResponse struct {
	ExperimentName string            `json:"experiment_name"`
	TestVersion    string            `json:"test_version"`
	ControlVersion string            `json:"control_version"`
	Metrics        ExperimentMetrics `json:"metrics"`
	TimeRange      TimeRange         `json:"time_range"`
}

// ExperimentMetrics contains aggregated metrics for an experiment
type ExperimentMetrics struct {
	TotalRequests       int     `json:"total_requests"`
	TestVersionRequests int     `json:"test_version_requests"`
	AvgLoadTimeMs       float64 `json:"avg_load_time_ms"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	ErrorRate           float64 `json:"error_rate"`
	AccountsServed      int     `json:"accounts_served"`
}

// TimeRange represents a time range
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
