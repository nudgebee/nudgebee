package model

import (
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/nikolalohinski/gonja/v2"
)

type WorkflowDefinition struct {
	Version  string         `yaml:"version,omitempty" json:"version,omitempty" validate:"workflowversion"`
	Inputs   []Input        `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Triggers []Trigger      `yaml:"triggers" json:"triggers" validate:"required,min=1,dive"`
	Tasks    []Task         `yaml:"tasks" json:"tasks" validate:"required,min=1,dive"`
	Hooks    *Hooks         `yaml:"hooks,omitempty" json:"hooks,omitempty"`
	Output   map[string]any `yaml:"output,omitempty" json:"output,omitempty"`
	// SetExecutionTags list of template strings to generate tags for the execution (e.g. "service:{{ Inputs.service }}")
	SetExecutionTags []string `yaml:"set_execution_tags,omitempty" json:"set_execution_tags,omitempty"`
	// Workflow-level retry policy (overrides per-task if set)
	RetryPolicy *WorkflowRetryPolicy `yaml:"retry_policy,omitempty" json:"retry_policy,omitempty"`
	// Workflow-level timeout (overrides per-task if set), e.g. "30m"
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty" validate:"omitempty,duration"`
	// Presentation-only canvas layout (per-task coords live on Task.Layout / Trigger.Layout).
	Layout *WorkflowDefinitionLayout `yaml:"layout,omitempty" json:"layout,omitempty"`
}

// WorkflowDefinitionLayout holds workflow-canvas presentation state.
type WorkflowDefinitionLayout struct {
	Viewport *WorkflowViewport `yaml:"viewport,omitempty" json:"viewport,omitempty"`
}

// WorkflowViewport stores ReactFlow camera state (pan + zoom).
type WorkflowViewport struct {
	X    float64 `yaml:"x" json:"x"`
	Y    float64 `yaml:"y" json:"y"`
	Zoom float64 `yaml:"zoom" json:"zoom"`
}

// WorkflowTaskLayout stores a single node's canvas coordinates.
type WorkflowTaskLayout struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

// WorkflowRetryPolicy defines retry policy for the whole workflow or as a template for tasks.
type WorkflowRetryPolicy struct {
	InitialInterval        string   `yaml:"initial_interval,omitempty" json:"initial_interval,omitempty"` // e.g. "1s"
	BackoffCoefficient     float64  `yaml:"backoff_coefficient,omitempty" json:"backoff_coefficient,omitempty"`
	MaximumInterval        string   `yaml:"maximum_interval,omitempty" json:"maximum_interval,omitempty"` // e.g. "1m"
	MaximumAttempts        int32    `yaml:"maximum_attempts,omitempty" json:"maximum_attempts,omitempty"`
	NonRetryableErrorTypes []string `yaml:"non_retryable_error_types,omitempty" json:"non_retryable_error_types,omitempty"`
}

// FailurePolicy defines how to handle failures for a task.
type FailurePolicy struct {
	Retry  *WorkflowRetryPolicy `yaml:"retry,omitempty" json:"retry,omitempty"`
	Action string               `yaml:"action,omitempty" json:"action,omitempty"` // e.g. "continue", "fail" (default)
}

// WorkflowUser represents user information for created_by/updated_by fields.
type WorkflowUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type Workflow struct {
	ID                         string                  `json:"id" yaml:"id"`
	TenantID                   string                  `json:"tenant_id" yaml:"tenant_id"`
	AccountID                  string                  `json:"account_id" yaml:"account_id"`
	Definition                 WorkflowDefinition      `json:"definition" yaml:"definition" validate:"required"`
	Tags                       map[string]any          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Status                     WorkflowStatus          `json:"status,omitempty" yaml:"status,omitempty"`
	LastExecutionStatus        WorkflowExecutionStatus `json:"last_execution_status,omitempty" yaml:"last_execution_status,omitempty"`
	LastExecutionStatusMessage *string                 `json:"last_execution_status_message,omitempty" yaml:"last_execution_status_message,omitempty"`
	LastExecutionTime          *time.Time              `json:"last_execution_time,omitempty" yaml:"last_execution_time,omitempty"`
	Name                       string                  `json:"name" yaml:"name" validate:"required,workflowname"`
	CreatedBy                  string                  `json:"created_by,omitempty" yaml:"created_by,omitempty"`
	CreatedByUser              *WorkflowUser           `json:"created_by_user,omitempty" yaml:"created_by_user,omitempty"`
	UpdatedBy                  string                  `json:"updated_by,omitempty" yaml:"updated_by,omitempty"`
	UpdatedByUser              *WorkflowUser           `json:"updated_by_user,omitempty" yaml:"updated_by_user,omitempty"`
	CreatedAt                  time.Time               `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt                  time.Time               `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	// CreatedFromSessionID is the LLM conversation session_id that produced this
	// workflow. Set on create only; never overwritten by Update so the UI can always
	// deep-link back to the original chat. NULL for UI/manual workflows.
	CreatedFromSessionID *string      `json:"created_from_session_id,omitempty" yaml:"created_from_session_id,omitempty"`
	TriggerDetails       *TriggerInfo `json:"trigger_details,omitempty" yaml:"trigger_details,omitempty"`
	DryRun               bool         `json:"dry_run,omitempty" yaml:"dry_run,omitempty"`
	// TriggeredByUser is a transient field populated before workflow execution.
	// Identifies the user who triggered this run (manual/retrigger). Empty for
	// scheduled/webhook/event-rule triggers.
	TriggeredByUser *WorkflowUser `json:"triggered_by_user,omitempty" yaml:"triggered_by_user,omitempty"`
	// LiveVersionID is the workflow_versions.id pointed at by
	// workflows.live_version_id. Determines which snapshot Execute runs against;
	// independent of Status. Null only for legacy rows without any published
	// version (post-V736 backfill should leave none in that state).
	LiveVersionID *string `json:"live_version_id,omitempty" yaml:"live_version_id,omitempty"`
	// LiveVersionNumber is the denormalized version_number for the live version.
	// Surfaced so the UI can render badges ("Live: v3") without a second roundtrip.
	LiveVersionNumber *int `json:"live_version_number,omitempty" yaml:"live_version_number,omitempty"`
	// LiveVersionName is the optional user-facing label for the live version.
	LiveVersionName *string `json:"live_version_name,omitempty" yaml:"live_version_name,omitempty"`
	// DraftVersionID is the workflow_versions.id the current draft (workflows.definition)
	// was last branched off — set on create, on publish (= new version), and on restore.
	// Plain draft edits (Update) do NOT mutate it. The editor uses this for accurate
	// lineage rendering ("Draft based on v2, Live is v3") so the strip no longer lies
	// when the user has rolled back to an older version.
	DraftVersionID *string `json:"draft_version_id,omitempty" yaml:"draft_version_id,omitempty"`
	// DraftVersionNumber / DraftVersionName are denormalized projections of the
	// version DraftVersionID points at, surfaced so the editor can render badges
	// without a second roundtrip.
	DraftVersionNumber *int    `json:"draft_version_number,omitempty" yaml:"draft_version_number,omitempty"`
	DraftVersionName   *string `json:"draft_version_name,omitempty" yaml:"draft_version_name,omitempty"`
	// LiveVersionStatus mirrors workflow_versions.status for the live version.
	// Surfaced so the listing / editor can render the per-version state without a
	// second query. Kept in lockstep with workflows.status by the DAO.
	LiveVersionStatus WorkflowStatus `json:"live_version_status,omitempty" yaml:"live_version_status,omitempty"`
}

// TriggerInfo holds dynamic, non-persisted information about a workflow's triggers.
type TriggerInfo struct {
	NextScheduledRun *time.Time `json:"next_scheduled_run,omitempty"`
}

// MaxWorkflowVersionsPerWorkflow is the retention cap for workflow_versions
// rows per workflow. The live version is always protected from pruning, so the
// effective non-live retention is N-1. Single place to bump if product needs
// deeper history.
const MaxWorkflowVersionsPerWorkflow = 50

// WorkflowVersionSource describes why a version snapshot was written.
type WorkflowVersionSource string

const (
	WorkflowVersionSourceCreate  WorkflowVersionSource = "create"
	WorkflowVersionSourcePublish WorkflowVersionSource = "publish"
	WorkflowVersionSourceRestore WorkflowVersionSource = "restore"
)

// WorkflowVersion is one snapshot of a workflow's definition at a point in time.
type WorkflowVersion struct {
	ID                  string                `json:"id"`
	WorkflowID          string                `json:"workflow_id"`
	VersionNumber       int                   `json:"version_number"`
	Definition          WorkflowDefinition    `json:"definition"`
	Source              WorkflowVersionSource `json:"source"`
	RestoredFromVersion *int                  `json:"restored_from_version,omitempty"`
	Name                *string               `json:"name,omitempty"`
	Description         *string               `json:"description,omitempty"`
	IsLive              bool                  `json:"is_live"`
	// Status is the per-version runtime gate (ACTIVE / PAUSED / INACTIVE). When this
	// version is live, workflows.status mirrors it. Defaults to PAUSED on insert so a
	// brand-new workflow does not silently start firing scheduled / event triggers.
	Status        WorkflowStatus `json:"status,omitempty"`
	CreatedBy     string         `json:"created_by,omitempty"`
	CreatedByUser *WorkflowUser  `json:"created_by_user,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// WorkflowStatus defines the administrative status of a workflow definition.
type WorkflowStatus string

const (
	// WorkflowStatusActive means the workflow is enabled and can be triggered.
	WorkflowStatusActive WorkflowStatus = "ACTIVE"
	// WorkflowStatusInactive means the workflow is disabled and cannot be triggered.
	WorkflowStatusInactive WorkflowStatus = "INACTIVE"
	// WorkflowStatusPaused means a scheduled workflow is paused, but other triggers may be allowed.
	WorkflowStatusPaused WorkflowStatus = "PAUSED"
)

// WorkflowExecutionStatus defines the possible statuses of a workflow execution.
type WorkflowExecutionStatus string

const (
	WorkflowExecutionStatusRunning        WorkflowExecutionStatus = "RUNNING"
	WorkflowExecutionStatusCompleted      WorkflowExecutionStatus = "COMPLETED"
	WorkflowExecutionStatusFailed         WorkflowExecutionStatus = "FAILED"
	WorkflowExecutionStatusCanceled       WorkflowExecutionStatus = "CANCELED"
	WorkflowExecutionStatusTerminated     WorkflowExecutionStatus = "TERMINATED"
	WorkflowExecutionStatusTimedOut       WorkflowExecutionStatus = "TIMED_OUT"
	WorkflowExecutionStatusContinuedAsNew WorkflowExecutionStatus = "CONTINUED_AS_NEW"
	WorkflowExecutionStatusUnspecified    WorkflowExecutionStatus = "UNSPECIFIED"
)

// TaskStatus defines the possible statuses of a task.
type TaskStatus string

const (
	TaskStatusScheduled TaskStatus = "SCHEDULED"
	TaskStatusStarted   TaskStatus = "STARTED"
	TaskStatusCompleted TaskStatus = "COMPLETED"
	TaskStatusFailed    TaskStatus = "FAILED"
	TaskStatusSkipped   TaskStatus = "SKIPPED"
	TaskStatusTimedOut  TaskStatus = "TIMED_OUT"
	TaskStatusCanceled  TaskStatus = "CANCELED"
)

type Input struct {
	ID          string `yaml:"id" json:"id" validate:"required"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Type        string `yaml:"type,omitempty" json:"type,omitempty"`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

type WorkflowTrigger string

const (
	WorkflowTriggerSchedule     WorkflowTrigger = "schedule"
	WorkflowTriggerManual       WorkflowTrigger = "manual"
	WorkflowTriggerWebhook      WorkflowTrigger = "webhook"
	WorkflowTriggerEvent        WorkflowTrigger = "event"
	WorkflowTriggerOptimization WorkflowTrigger = "optimization"
)

type TriggerInternal struct {
	Name string `json:"name,omitempty"`
}

type Trigger struct {
	Type     WorkflowTrigger     `yaml:"type" json:"type" validate:"required,workflowtrigger"`
	Params   map[string]any      `yaml:"params" json:"params,omitempty"`
	Internal *TriggerInternal    `yaml:"-" json:"internal,omitempty"`
	Layout   *WorkflowTaskLayout `yaml:"layout,omitempty" json:"layout,omitempty"`
}

// Validate performs struct-level validation for Trigger.
func (t Trigger) Validate(sl validator.StructLevel) {
	// For most trigger types a missing params block means "no params to validate".
	// Event and optimization triggers are exceptions: event requires at least one of
	// event_type or filter, and optimization requires at least one of categories,
	// rule_names, clusters, or filter — both must run their case below even when
	// params is nil/empty so the missing-params error surfaces.
	if t.Params == nil && t.Type != WorkflowTriggerEvent && t.Type != WorkflowTriggerOptimization {
		return
	}

	switch t.Type {
	case WorkflowTriggerSchedule:
		cronVal, cronOk := t.Params["cron"]
		if !cronOk {
			sl.ReportError(t.Params, "params", "Params", "cron_missing", "")
			return
		}
		cronStr, cronStrOk := cronVal.(string)
		if !cronStrOk || cronStr == "" {
			sl.ReportError(t.Params, "params", "Params", "cron_invalid", "")
			return
		}
		if overlapVal, ok := t.Params["overlap_policy"]; ok {
			overlapStr, ok := overlapVal.(string)
			if !ok {
				sl.ReportError(t.Params, "params", "Params", "overlap_policy_invalid_type", "")
				return
			}
			switch overlapStr {
			case "Skip", "BufferOne", "BufferAll", "CancelOther", "TerminateOther", "AllowAll":
				// Valid
			default:
				sl.ReportError(t.Params, "params", "Params", "overlap_policy_invalid_value", "")
				return
			}
		}
		if catchupVal, ok := t.Params["catchup_window"]; ok {
			catchupStr, ok := catchupVal.(string)
			if !ok {
				sl.ReportError(t.Params, "params", "Params", "catchup_window_invalid_type", "")
				return
			}
			if _, err := time.ParseDuration(catchupStr); err != nil {
				sl.ReportError(t.Params, "params", "Params", "catchup_window_invalid_duration", "")
				return
			}
		}

		// Allow arbitrary params for inputs to support parameter mapping
		// We no longer strictly validate unsupported parameters for schedule triggers

	case WorkflowTriggerWebhook:
		integrationIDVal, integrationIDOk := t.Params["integration_name"]
		if !integrationIDOk {
			sl.ReportError(t.Params, "params", "Params", "integration_name_missing", "")
			return
		}
		integrationIDStr, integrationIDStrOk := integrationIDVal.(string)
		if !integrationIDStrOk || integrationIDStr == "" {
			sl.ReportError(t.Params, "params", "Params", "integration_name_invalid", "")
			return
		}

		// Check for unsupported parameters
		allowedParams := map[string]struct{}{
			"integration_name": {},
			"secret":           {},
			"filter":           {},
		}
		for param := range t.Params {
			if _, exists := allowedParams[param]; !exists {
				sl.ReportError(t.Params, "params", "Params", "unsupported_webhook_param", "Unsupported parameter: "+param)
				return
			}
		}

		// Validate Jinja filter syntax
		if filterVal, ok := t.Params["filter"]; ok {
			filterStr, ok := filterVal.(string)
			if !ok {
				sl.ReportError(t.Params, "params", "Params", "filter_invalid_type", "Filter must be a string")
				return
			}
			if filterStr != "" {
				if _, err := gonja.FromString(filterStr); err != nil {
					sl.ReportError(t.Params, "params", "Params", "filter_invalid_syntax", "Invalid Jinja (Gonja) filter: "+err.Error())
					return
				}
			}
		}
	case WorkflowTriggerEvent:
		allowedParams := map[string]struct{}{
			"event_type": {},
			"filter":     {},
		}
		for param := range t.Params {
			if _, exists := allowedParams[param]; !exists {
				sl.ReportError(t.Params, "params", "Params", "unsupported_event_param", "Unsupported parameter: "+param)
				return
			}
		}

		var eventTypes []string
		if raw, ok := t.Params["event_type"]; ok && raw != nil {
			switch v := raw.(type) {
			case string:
				if v != "" {
					eventTypes = []string{v}
				}
			case []any:
				for _, item := range v {
					s, strOk := item.(string)
					if !strOk || s == "" {
						sl.ReportError(t.Params, "params", "Params", "event_type_invalid_item", "event_type items must be non-empty strings")
						return
					}
					eventTypes = append(eventTypes, s)
				}
			case []string:
				for _, s := range v {
					if s == "" {
						sl.ReportError(t.Params, "params", "Params", "event_type_invalid_item", "event_type items must be non-empty strings")
						return
					}
					eventTypes = append(eventTypes, s)
				}
			default:
				sl.ReportError(t.Params, "params", "Params", "event_type_invalid_type", "event_type must be a string or array of strings")
				return
			}
		}

		filterStr := ""
		if filterVal, ok := t.Params["filter"]; ok {
			s, ok := filterVal.(string)
			if !ok {
				sl.ReportError(t.Params, "params", "Params", "filter_invalid_type", "Filter must be a string")
				return
			}
			if s != "" {
				if _, err := gonja.FromString(s); err != nil {
					sl.ReportError(t.Params, "params", "Params", "filter_invalid_syntax", "Invalid Jinja (Gonja) filter: "+err.Error())
					return
				}
			}
			filterStr = strings.TrimSpace(s)
		}

		if len(eventTypes) == 0 && filterStr == "" {
			sl.ReportError(t.Params, "params", "Params", "event_trigger_needs_filter", "Event trigger requires at least one of: event_type, or filter")
			return
		}
	case WorkflowTriggerManual:
		// Manual trigger accepts optional input parameters
	case WorkflowTriggerOptimization:
		if len(t.Params) == 0 {
			sl.ReportError(t.Params, "params", "Params", "optimization_trigger_needs_params", "Optimization trigger requires params with at least one of: categories, rule_names, clusters, filter")
			return
		}
		// Check for unsupported parameters
		allowedParams := map[string]struct{}{
			"categories": {},
			"rule_names": {},
			"clusters":   {},
			"filter":     {},
		}
		for param := range t.Params {
			if _, exists := allowedParams[param]; !exists {
				sl.ReportError(t.Params, "params", "Params", "unsupported_optimization_param", "Unsupported parameter: "+param)
				return
			}
		}

		// Validate that array params contain strings
		for _, arrayParam := range []string{"categories", "rule_names", "clusters"} {
			if val, ok := t.Params[arrayParam]; ok {
				arr, arrOk := val.([]any)
				if !arrOk {
					sl.ReportError(t.Params, "params", "Params", arrayParam+"_invalid_type", arrayParam+" must be an array")
					return
				}
				for _, item := range arr {
					if _, strOk := item.(string); !strOk {
						sl.ReportError(t.Params, "params", "Params", arrayParam+"_invalid_item", arrayParam+" items must be strings")
						return
					}
				}
			}
		}

		// Validate Jinja filter syntax if provided
		if filterVal, ok := t.Params["filter"]; ok {
			filterStr, ok := filterVal.(string)
			if !ok {
				sl.ReportError(t.Params, "params", "Params", "filter_invalid_type", "Filter must be a string")
				return
			}
			if filterStr != "" {
				if _, err := gonja.FromString(filterStr); err != nil {
					sl.ReportError(t.Params, "params", "Params", "filter_invalid_syntax", "Invalid Jinja (Gonja) filter: "+err.Error())
					return
				}
			}
		}
	}
}

type Task struct {
	ID            string         `yaml:"id" json:"id" validate:"required,taskid"`
	Type          string         `yaml:"type" json:"type" validate:"required"`
	Params        map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
	Tasks         []Task         `yaml:"tasks,omitempty" json:"tasks,omitempty"`
	SetVars       map[string]any `yaml:"set_vars,omitempty" json:"set_vars,omitempty" validate:"setvars"`
	SetState      map[string]any `yaml:"set_state,omitempty" json:"set_state,omitempty" validate:"setstate"`
	DependsOn     []string       `yaml:"depends_on,omitempty" json:"depends_on,omitempty" validate:"dive"`
	If            string         `yaml:"if,omitempty" json:"if,omitempty"`
	Matrix        map[string]any `yaml:"matrix,omitempty" json:"matrix,omitempty"`
	FailurePolicy *FailurePolicy `yaml:"failure_policy,omitempty" json:"failure_policy,omitempty"` // advanced failure policy for this task
	Timeout       string         `yaml:"timeout,omitempty" json:"timeout,omitempty" validate:"omitempty,duration"`
	Hooks         *Hooks         `yaml:"hooks,omitempty" json:"hooks,omitempty"`
	Disabled      bool           `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// PrevEdges is opaque UI metadata, written by the frontend toggle-disable
	// flow and round-tripped through the workflow definition. Nothing in the
	// executor, validator, or any other backend code path reads it, which is
	// why the type is `any`: the frontend has shipped two shapes
	// (`[]StashedEdge` from the original disable PR and `{originals, splices?}`
	// from the splice-on-disable PR), and `app/src/components1/workflow/utils/
	// toggleTaskDisable.ts` (`unpackStash`) is the canonical reader that
	// normalizes both. Do not narrow the type here without first converging
	// the frontend on a single canonical shape and migrating saved workflows.
	PrevEdges any                 `yaml:"_prev_edges,omitempty" json:"_prev_edges,omitempty"`
	Layout    *WorkflowTaskLayout `yaml:"layout,omitempty" json:"layout,omitempty"`
}

// SetVarConfig is a helper struct for validating the polymorphic set_vars field.
type SetVarConfig struct {
	Value any `yaml:"value" json:"value" validate:"required"`
	// Future: Sensitive bool
}

// SetStateConfig is a helper struct for validating the polymorphic set_state field.
type SetStateConfig struct {
	Value any    `yaml:"value" json:"value" validate:"required"`
	TTL   string `yaml:"ttl,omitempty" json:"ttl,omitempty" validate:"omitempty,duration"`
}

// WorkflowStateItem represents a single persistent state entry with metadata.
type WorkflowStateItem struct {
	Key                      string     `json:"key"`
	Value                    any        `json:"value"`
	UpdatedAt                time.Time  `json:"updated_at"`
	ExpiresAt                *time.Time `json:"expires_at,omitempty"`
	LastUpdatedByExecutionID string     `json:"last_updated_by_execution_id,omitempty"`
	LastUpdatedByTaskID      string     `json:"last_updated_by_task_id,omitempty"`
}

// TaskDefinition represents the metadata for a registered task type.
type TaskDefinition struct {
	Name         string         `json:"name"`
	DisplayName  string         `json:"display_name,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	RuntimeNotes []string       `json:"runtime_notes,omitempty"`
	Aliases      []string       `json:"aliases,omitempty"`
}

type ListTaskDefinitionResponse struct {
	Tasks []TaskDefinition `json:"tasks"`
}

type Action struct {
	Type   string         `yaml:"type" json:"type" validate:"required"`
	Params map[string]any `yaml:"params" json:"params"`
}

type Hooks struct {
	Success []Action `yaml:"success" json:"success"`
	Failure []Action `yaml:"failure" json:"failure"`
	Always  []Action `yaml:"always" json:"always"`
}

type ListWorkflowRequest struct {
	NextPageToken       string                  `json:"next_page_token,omitempty"`
	Limit               int                     `json:"limit,omitempty"`
	Tags                map[string]string       `json:"tags,omitempty"`
	TagsText            []string                `json:"tags_text,omitempty"`
	Name                string                  `json:"name,omitempty"`
	Status              WorkflowStatus          `json:"status,omitempty"`
	LastExecutionStatus WorkflowExecutionStatus `json:"last_execution_status,omitempty"`
	TriggerType         string                  `json:"trigger_type,omitempty"`
	CreatedBy           string                  `json:"created_by,omitempty"`
}

type ListWorkflowResponse struct {
	Workflows     []Workflow `json:"workflows"`
	NextPageToken string     `json:"next_page_token,omitempty"`
	TotalCount    int        `json:"total_count"`
}

// WorkflowCaller is a lightweight projection returned by ListCallers — just
// enough to render a "where used" warning before deleting / renaming a workflow
// without serialising the entire calling workflow's definition.
type WorkflowCaller struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Status WorkflowStatus `json:"status"`
}

type ListWorkflowCallersResponse struct {
	Callers []WorkflowCaller `json:"callers"`
}

// UpdateWorkflowExecutionSignal defines the payload for updating a workflow execution.
type UpdateWorkflowExecutionSignal struct {
	Inputs map[string]any `json:"inputs"`
}

// StateUpdateDTO is used to pass state updates from Executor to Activity
type StateUpdateDTO struct {
	Value any    `json:"value"`
	TTL   string `json:"ttl,omitempty"` // Duration string, e.g., "1h"
}

// TaskExecutionDetails holds details about a single task's execution within a workflow run.
type TaskExecutionDetails struct {
	ID               string                 `json:"id"`                    // Our internal Task ID
	ActivityID       string                 `json:"activity_id,omitempty"` // Temporal Activity ID
	Type             string                 `json:"type"`
	Status           TaskStatus             `json:"status"` // e.g., "SCHEDULED", "STARTED", "COMPLETED", "FAILED", "TIMED_OUT" (can map from Temporal enums)
	ScheduledEventID int64                  `json:"scheduled_event_id,omitempty"`
	StartedEventID   int64                  `json:"started_event_id,omitempty"`
	CompletedEventID int64                  `json:"completed_event_id,omitempty"`
	StartTime        *time.Time             `json:"start_time,omitempty"`
	EndTime          *time.Time             `json:"end_time,omitempty"`
	Input            map[string]any         `json:"input,omitempty"`
	RenderedParams   map[string]any         `json:"rendered_params,omitempty"`
	Output           any                    `json:"output,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Attempt          int                    `json:"attempt,omitempty"`
	RetryPolicy      *WorkflowRetryPolicy   `json:"retry_policy,omitempty"` // Details about retry policy
	Children         []TaskExecutionDetails `json:"children,omitempty"`     // For group tasks
}

// WorkflowExecutionDetails holds comprehensive information about a workflow execution.
type WorkflowExecutionDetails struct {
	WorkflowId       string                  `json:"workflow_id"`
	Id               string                  `json:"id"`
	ParentWorkflowID string                  `json:"parent_workflow_id,omitempty"`
	TriggeredBy      string                  `json:"triggered_by,omitempty"`
	Status           WorkflowExecutionStatus `json:"status"` // Overall workflow status
	StartTime        *time.Time              `json:"start_time"`
	CloseTime        *time.Time              `json:"close_time"`
	HistoryLength    int64                   `json:"history_length"`
	Inputs           map[string]any          `json:"inputs,omitempty"` // Workflow inputs
	TaskQueue        string                  `json:"task_queue,omitempty"`
	HistorySizeBytes int64                   `json:"history_size_bytes,omitempty"`
	SearchAttributes map[string]any          `json:"search_attributes,omitempty"`
	Memo             map[string]any          `json:"memo,omitempty"`
	WorkflowResult   any                     `json:"workflow_result"` // New field for overall workflow result
	Error            string                  `json:"error,omitempty"` // Failure reason if the workflow failed
	Tasks            []TaskExecutionDetails  `json:"tasks"`           // Details for each task
	// Definition is the workflow definition snapshot captured when this run started,
	// reconstructed from the Temporal WORKFLOW_EXECUTION_STARTED event input. This may
	// differ from the current saved definition if the workflow was edited after the run.
	// Use this (not the live saved def) when diagnosing what actually failed.
	Definition *WorkflowDefinition `json:"definition,omitempty"`
	// VersionID / VersionNumber / VersionName link this execution back to the
	// workflow_versions row that was live at trigger time. Sourced from the
	// Temporal Memo keys stamped by ExecuteWorkflow. Nil for legacy runs that
	// predate execution-to-version linkage.
	VersionID     *string `json:"version_id,omitempty"`
	VersionNumber *int    `json:"version_number,omitempty"`
	VersionName   *string `json:"version_name,omitempty"`
	// DefinitionSource explains where Definition came from:
	//   "version"       — loaded from workflow_versions by VersionID (preferred)
	//   "live_fallback" — version row missing or no Memo keys; loaded from
	//                     workflows.definition (current draft). UI should warn.
	//   "history"       — reconstructed from Temporal history (non-persisted
	//                     workflows: inline / dry-run).
	DefinitionSource string `json:"definition_source,omitempty"`
}

// WorkflowExecutionSummary provides a concise overview of a workflow execution.
// WorkflowID is the automation definition ID (stable across runs) — same ID
// callers pass to endpoints like GET /workflows/{id}/runs/{execution_id}.
// TemporalWorkflowID is Temporal's internal per-run workflow ID; it is not
// serialized because it is only meaningful inside server-side Temporal calls
// (e.g. TerminateWorkflow) and leaking it has previously confused clients.
type WorkflowExecutionSummary struct {
	WorkflowID         string                  `json:"workflow_id"`
	TemporalWorkflowID string                  `json:"-"`
	ID                 string                  `json:"id"` // This is the RunID
	Status             WorkflowExecutionStatus `json:"status"`
	StartTime          *time.Time              `json:"start_time,omitempty"`
	CloseTime          *time.Time              `json:"close_time,omitempty"`
	TriggeredBy        string                  `json:"triggered_by,omitempty"`
	TriggerType        string                  `json:"trigger_type,omitempty"`
	ParentWorkflowID   string                  `json:"parent_workflow_id,omitempty"`
	WorkflowName       string                  `json:"workflow_name,omitempty"`
}

// ListWorkflowExecutionResponse contains a list of workflow executions.
type ListWorkflowExecutionResponse struct {
	Executions    []WorkflowExecutionSummary `json:"executions"`
	NextPageToken string                     `json:"next_page_token,omitempty"`
}

type WorkflowEventTriggerRule struct {
	WorkflowID  string          `db:"id"`
	TenantID    string          `db:"tenant_id"`
	AccountID   string          `db:"account_id"`
	EventType   string          `db:"event_type"`
	Filter      string          `db:"filter"`
	TriggerType WorkflowTrigger `db:"trigger_type"`
}

// DryRunWorkflowRequest defines the input for a workflow dry-run.
type DryRunWorkflowRequest struct {
	Definition WorkflowDefinition `json:"definition" validate:"required"`
	Inputs     map[string]any     `json:"inputs"`
	// Name is the automation name, surfaced in notifications (e.g. approval
	// emails). Falls back to a synthetic placeholder when empty.
	Name string `json:"name,omitempty"`
}

// DryRunWorkflowResponse defines the output for a workflow dry-run.
type DryRunWorkflowResponse struct {
	Status WorkflowExecutionStatus `json:"status"`
	Output any                     `json:"output,omitempty"`
	Error  string                  `json:"error,omitempty"`
	Tasks  []TaskExecutionDetails  `json:"tasks,omitempty"`
	Inputs map[string]any          `json:"inputs,omitempty"`
}
