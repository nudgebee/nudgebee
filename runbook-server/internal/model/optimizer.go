package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AutoOptimizeStatus string

const (
	AutoOptimizeStatusActive   AutoOptimizeStatus = "Active"
	AutoOptimizeStatusDisabled AutoOptimizeStatus = "Disabled"
	AutoOptimizeStatusDraft    AutoOptimizeStatus = "Draft"
	AutoOptimizeStatusDryrun   AutoOptimizeStatus = "Dryrun"
)

type ResourceStatus string

const (
	ResourceStatusActive ResourceStatus = "Active"
)

type AutopilotExecutionStatus string

const (
	AutopilotExecutionStatusIdle       AutopilotExecutionStatus = "Idle"
	AutopilotExecutionStatusInProgress AutopilotExecutionStatus = "InProgress"
)

type RecommendationResolutionType string

const (
	RecommendationResolutionTypeDeploymentChange RecommendationResolutionType = "DeploymentChange"
)

type RecommendationResolutionStatus string

const (
	RecommendationResolutionStatusInProgress RecommendationResolutionStatus = "InProgress"
	RecommendationResolutionStatusSuccess    RecommendationResolutionStatus = "Success"
	RecommendationResolutionStatusFailed     RecommendationResolutionStatus = "Failed"
)

type RecommendationStatus string

const (
	RecommendationStatusOpen       RecommendationStatus = "Open"
	RecommendationStatusInProgress RecommendationStatus = "InProgress"
	RecommendationStatusClosed     RecommendationStatus = "Closed"
	RecommendationStatusDismissed  RecommendationStatus = "Dismissed"
)

type AutopilotTaskStatus string

const (
	AutopilotTaskStatusScheduled AutopilotTaskStatus = "Scheduled"
	AutopilotTaskStatusComplete  AutopilotTaskStatus = "Complete"
	AutopilotTaskStatusFailed    AutopilotTaskStatus = "Failed"
	AutopilotTaskStatusSkipped   AutopilotTaskStatus = "Skipped"
)

// AutoOptimizeResourceFilter represents a filter for resources.
type AutoOptimizeResourceFilter struct {
	Name      *string `json:"name,omitempty"`
	Namespace *string `json:"namespace,omitempty"`
	Type      *string `json:"type,omitempty"`
}

func (f AutoOptimizeResourceFilter) String() string {
	res := ""
	if f.Namespace != nil {
		res += *f.Namespace + "/"
	}
	if f.Type != nil {
		res += *f.Type + "/"
	}
	if f.Name != nil {
		res += *f.Name
	}
	return res
}

func (f AutoOptimizeResourceFilter) OnlyNamespace() bool {
	return f.Namespace != nil && f.Name == nil && f.Type == nil
}

func (f AutoOptimizeResourceFilter) Matches(namespace, kind, name string) bool {
	if f.Namespace != nil && *f.Namespace != "" && *f.Namespace != namespace {
		return false
	}
	if f.Type != nil && *f.Type != "" && !strings.EqualFold(*f.Type, kind) {
		return false
	}
	if f.Name != nil && *f.Name != "" && *f.Name != name {
		return false
	}
	return true
}

// GitOpsConfig configuration for GitOps.
type GitOpsConfig struct {
	Enabled        bool    `json:"enabled"`
	RepositoryName *string `json:"repository_name,omitempty"`
	Provider       string  `json:"provider"` // Default "github"
}

// TicketConfig configuration for ticket creation.
type TicketConfig struct {
	Enabled          bool           `json:"enabled"`
	ConfigurationID  *uuid.UUID     `json:"configuration_id,omitempty"`
	Assignee         *string        `json:"assignee,omitempty"`
	ProjectKey       string         `json:"project_key"`
	Severity         *string        `json:"severity,omitempty"`
	TicketType       string         `json:"ticket_type"` // Default "Task"
	AdditionalFields map[string]any `json:"additional_fields,omitempty"`
	Source           string         `json:"source,omitempty"`
	Description      string         `json:"description,omitempty"`
	Platform         string         `json:"platform,omitempty"`
}

// AutoOptimizeAttributes extra attributes for AutoOptimize.
type AutoOptimizeAttributes struct {
	GitOpsConfig GitOpsConfig `json:"git_ops_config"`
	TicketConfig TicketConfig `json:"ticket_config"`
}

// AutoOptimize represents the main configuration entity.
type AutoOptimize struct {
	ID               uuid.UUID                    `json:"id" db:"id"`
	Name             *string                      `json:"name,omitempty" db:"name"`
	AccountID        uuid.UUID                    `json:"account_id" db:"account_id"`
	Source           *string                      `json:"source,omitempty" db:"source"`
	Rule             map[string]any               `json:"rule" db:"rule"`
	CreationDate     time.Time                    `json:"creation_date" db:"creation_date"`
	UpdateDate       time.Time                    `json:"update_date" db:"update_date"`
	CreatedBy        uuid.UUID                    `json:"created_by" db:"created_by"`
	UpdatedBy        *uuid.UUID                   `json:"update_by,omitempty" db:"update_by"`
	ScheduleTime     string                       `json:"schedule_time" db:"schedule_time"` // Cron format
	LastScheduleTime *time.Time                   `json:"last_schedule_time,omitempty" db:"last_schedule_time"`
	LastExecutedTime *time.Time                   `json:"last_executed_time,omitempty" db:"last_executed_time"`
	Status           AutoOptimizeStatus           `json:"status" db:"status"`
	ExecutionStatus  string                       `json:"execution_status" db:"execution_status"`
	TenantID         uuid.UUID                    `json:"tenant_id" db:"tenant_id"`
	Category         string                       `json:"category" db:"category"`
	StartAt          time.Time                    `json:"start_at" db:"start_at"`
	EndAt            *time.Time                   `json:"end_at,omitempty" db:"end_at"`
	Notification     map[string]any               `json:"notification,omitempty" db:"notification"`
	NextScheduleTime *time.Time                   `json:"next_schedule_time,omitempty" db:"next_schedule_time"`
	Attributes       AutoOptimizeAttributes       `json:"attributes" db:"attributes"`
	ResourceFilters  []AutoOptimizeResourceFilter `json:"resource_filters,omitempty"`
}

// Resource represents a cloud resource.
type Resource struct {
	ID                 uuid.UUID      `json:"id"`
	CreatedAt          time.Time      `json:"created_at"`
	CreatedBy          *uuid.UUID     `json:"created_by,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
	UpdatedBy          *uuid.UUID     `json:"updated_by,omitempty"`
	ResourceID         string         `json:"resourse_id"` // Note: typo in python 'resourse_id' preserved if matching DB
	Name               string         `json:"name"`
	Type               string         `json:"type"`
	Status             string         `json:"status"`
	ResourceCreatedOn  *time.Time     `json:"resourse_created_on,omitempty"`
	Account            uuid.UUID      `json:"account"`
	CloudProvider      string         `json:"cloud_provider"`
	ARN                *string        `json:"arn,omitempty"`
	Tenant             uuid.UUID      `json:"tenant"`
	Tags               map[string]any `json:"tags"`
	Meta               map[string]any `json:"meta"`
	ServiceName        *string        `json:"service_name,omitempty"`
	FirstSeen          *time.Time     `json:"first_seen,omitempty"`
	LastSeen           time.Time      `json:"last_seen"`
	IsActive           bool           `json:"is_active"`
	ExternalResourceID *string        `json:"external_resource_id,omitempty"`
}

// Recommendation represents a recommendation generated by the optimizer.
type Recommendation struct {
	ID                   uuid.UUID      `json:"id"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	TenantID             uuid.UUID      `json:"tenant_id"`
	CloudAccountID       uuid.UUID      `json:"cloud_account_id"`
	ResourceID           *uuid.UUID     `json:"resource_id,omitempty"`
	Recommendation       map[string]any `json:"recommendation"`
	RecommendationAction string         `json:"recommendation_action"`
	Severity             string         `json:"severity"`
	RuleName             string         `json:"rule_name"`
	EstimatedSavings     float64        `json:"estimated_savings"`
	Status               string         `json:"status"`
	Category             string         `json:"category"`
	IsDismissed          bool           `json:"is_dismissed"`
	DismissedReason      *string        `json:"dismissed_reason,omitempty"`
	AccountObjectID      *string        `json:"account_object_id,omitempty"`
	UpdatedBy            *uuid.UUID     `json:"updated_by,omitempty"`
}

type RecommendationWithResource struct {
	Recommendation
	ResourceIdentifier string         `json:"resource_identifier"` // The "namespace/Kind/name" string
	ResourceMetadata   map[string]any `json:"resource_metadata"`
}

// RecommendationResolutionData represents the data in a resolution.
type RecommendationResolutionData struct {
	Data map[string]any `json:"data"`
}

// RecommendationResolution represents a resolution task.
type RecommendationResolution struct {
	ID               uuid.UUID                    `json:"id"`
	RecommendationID uuid.UUID                    `json:"recommendation_id"`
	Type             string                       `json:"type"` // e.g., DeploymentChange
	Data             RecommendationResolutionData `json:"data"`
	Status           string                       `json:"status"` // e.g., InProgress
	TypeReferenceID  string                       `json:"type_reference_id"`
	ResolverType     string                       `json:"resolver_type"` // AutoOptimize or AutoRunbook
	ResolverID       uuid.UUID                    `json:"resolver_id"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
	StatusMessage    *string                      `json:"status_message,omitempty"`
}

type Agent struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	CreatedAt      *time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      *time.Time `json:"updated_at" db:"updated_at"`
	Tenant         uuid.UUID  `json:"tenant" db:"tenant"`
	CloudAccountID uuid.UUID  `json:"cloud_account_id" db:"cloud_account_id"`
	Type           string     `json:"type" db:"type"`
	Status         string     `json:"status" db:"status"`
	LastConnected  *time.Time `json:"last_connected_at" db:"last_connected_at"`
	LastSynced     *time.Time `json:"last_synced_at" db:"last_synced_at"`
	Version        *string    `json:"version" db:"version"`
	K8sVersion     *string    `json:"k8s_version" db:"k8s_version"`
}

// TaskPayload represents the payload for an agent task.
type TaskPayload struct {
	Sinks        *string        `json:"sinks,omitempty"`
	Origin       string         `json:"origin"`
	Timestamp    int64          `json:"timestamp"`
	ActionName   string         `json:"action_name"`
	ActionParams map[string]any `json:"action_params"`
}

// AgentTask represents a task assigned to an agent.
type AgentTask struct {
	ID             uuid.UUID      `json:"id"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CloudAccountID uuid.UUID      `json:"cloud_account_id"`
	TenantID       uuid.UUID      `json:"tenant"`
	Action         string         `json:"action"`
	Payload        TaskPayload    `json:"payload"`
	Status         string         `json:"status"`
	Source         string         `json:"source"`
	SourceID       uuid.UUID      `json:"source_id"`
	Response       map[string]any `json:"response,omitempty"`
}

// AutoOptimizeTaskAttributes attributes for a task.
type AutoOptimizeTaskAttributes struct {
	ResolutionID *uuid.UUID     `json:"resolution_id,omitempty"`
	DryRun       bool           `json:"dryrun"`
	Response     map[string]any `json:"response,omitempty"`
	TicketLink   *string        `json:"ticket_link,omitempty"`
	PRLink       *string        `json:"pr_link,omitempty"`
}

// AutoOptimizeTask represents a task execution.
type AutoOptimizeTask struct {
	ID               uuid.UUID                  `json:"id"`
	Reason           *string                    `json:"reason,omitempty"`
	Error            *string                    `json:"error,omitempty"`
	Command          *string                    `json:"command,omitempty"`
	TaskID           *uuid.UUID                 `json:"task_id,omitempty"`
	AutoPilotID      uuid.UUID                  `json:"auto_pilot_id"`
	ScheduledTime    time.Time                  `json:"scheduled_time"`
	CreatedAt        time.Time                  `json:"created_at"`
	UpdatedAt        time.Time                  `json:"updated_at"`
	RecommendationID *uuid.UUID                 `json:"recommendation_id,omitempty"`
	Name             string                     `json:"name"`
	Status           string                     `json:"status"`
	TenantID         uuid.UUID                  `json:"tenant_id"`
	Meta             map[string]any             `json:"meta"`
	ResourceFilter   AutoOptimizeResourceFilter `json:"resource_filter"`
	SkippedBy        *uuid.UUID                 `json:"skipped_by,omitempty"`
	Attributes       AutoOptimizeTaskAttributes `json:"attributes"`
	AccountID        uuid.UUID                  `json:"account_id"`
}

// ScheduleConfig used in requests.
type ScheduleConfig struct {
	Frequency string     `json:"frequency"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}

// NotificationConfig for various channels.
type NotificationConfig struct {
	Enabled     bool    `json:"enabled"`
	ChannelID   *string `json:"channel_id,omitempty"`
	TenantID    *string `json:"tenant_id,omitempty"` // UUID as string in request?
	ChannelName *string `json:"channel_name,omitempty"`
	TeamName    *string `json:"team_name,omitempty"`
	TeamID      *string `json:"team_id,omitempty"`
}

// Notification settings.
type Notification struct {
	Slack      NotificationConfig `json:"slack"`
	MSTeams    NotificationConfig `json:"ms_teams"`
	GoogleChat NotificationConfig `json:"google_chat"`
	Email      NotificationConfig `json:"email"`
}

// AutoOptimizeRequestModel represents the input for creating/updating AutoOptimize.
type AutoOptimizeRequestModel struct {
	ID                 *uuid.UUID                   `json:"id,omitempty"`
	AccountID          uuid.UUID                    `json:"account_id"`
	TenantID           uuid.UUID                    `json:"tenant_id"`
	Category           string                       `json:"category" validate:"required"`
	Name               string                       `json:"name"`
	ResourceFilter     []AutoOptimizeResourceFilter `json:"resource_filter" validate:"required,min=1"`
	AutoOptimizeConfig map[string]any               `json:"auto_optimize_config" validate:"required"`
	Schedule           ScheduleConfig               `json:"schedule"`
	Notification       Notification                 `json:"notification"`
	DryRun             bool                         `json:"dryrun"`
	GitOps             GitOpsConfig                 `json:"gitops"`
	Ticket             TicketConfig                 `json:"ticket_config"`
}

// ResourceFilterRequest used for updating resource maps.
type ResourceFilterRequest struct {
	ResourceFilter   []AutoOptimizeResourceFilter `json:"resource_filter"`
	AccountID        uuid.UUID                    `json:"account_id"`
	TenantID         uuid.UUID                    `json:"tenant_id"`
	AutoOptimizeID   uuid.UUID                    `json:"auto_optimize_id"`
	AutoOptimizeType string                       `json:"auto_optimize_type"`
}

// AutoOptimizeResourceMap maps resources to auto-optimize rules.
type AutoOptimizeResourceMap struct {
	ID                 uuid.UUID                  `json:"id" db:"id"`
	ResourceIdentifier AutoOptimizeResourceFilter `json:"resource_identifier" db:"resource_identifier"`
	AutoOptimizeID     uuid.UUID                  `json:"auto_optimize_id" db:"auto_optimize_id"`
	TenantID           uuid.UUID                  `json:"tenant_id" db:"tenant_id"`
	AccountID          uuid.UUID                  `json:"account_id" db:"account_id"`
	AutoOptimizeType   string                     `json:"auto_optimize_type" db:"auto_optimize_type"`
}

// Helper to implement python's get_auto_optimize_name
func (m *AutoOptimizeRequestModel) GetAutoOptimizeName() string {
	hasNamespaceOnly := false
	for _, rf := range m.ResourceFilter {
		if rf.OnlyNamespace() {
			hasNamespaceOnly = true
			break
		}
	}

	if hasNamespaceOnly {
		if len(m.ResourceFilter) > 0 && m.ResourceFilter[0].Namespace != nil {
			return fmt.Sprintf("Auto optimize for namespace %s", *m.ResourceFilter[0].Namespace)
		}
		return "Auto optimize for namespace"
	}

	if len(m.ResourceFilter) > 0 {
		first := m.ResourceFilter[0]
		wkType := ""
		if first.Type != nil {
			wkType = strings.ToLower(*first.Type)
		}
		name := ""
		if first.Name != nil {
			name = *first.Name
		}
		base := fmt.Sprintf("Auto optimize for %s %s", wkType, name)
		if len(m.ResourceFilter) > 1 {
			return fmt.Sprintf("%s +%d other", base, len(m.ResourceFilter)-1)
		}
		return base
	}
	return "Auto optimize"
}
