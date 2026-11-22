package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	RecommendationResolutionTypePullRequest      RecommendationResolutionType = "PullRequest"
)

// PRLifecycleTerminalStates mirrors the terminal set used by the api-server's
// open-PR guard (findOpenPRResolution in recommendation/service.go). A PR
// resolution outside this set is still open on the provider, and the api-server
// answers a fresh apply by handing back that existing resolution instead of
// raising a second PR.
//
// The optimizer must therefore treat those recommendations as busy. When the two
// sides disagreed — the optimizer keying off status while the guard keyed off
// lifecycle — a run would execute a task, be handed a resolution it had already
// recorded, and abort on a duplicate key before writing any status, wedging the
// task in Scheduled and silently disabling that workload forever (#34943). Keep
// this list in sync with the api-server guard.
var PRLifecycleTerminalStates = []string{"merged", "closed", "unresolvable"}

// ValueRefreshCooldown mirrors valueRefreshCooldown in the api-server's
// recommendation/pr_value_refresh.go. It is duplicated for the same reason
// PRLifecycleTerminalStates above is: the two services are separate Go modules.
//
// It is used here to decide whether to RESELECT a recommendation that is only
// InProgress because of its own open pull request. The api-server guard remains
// authoritative — if these two ever drift, the guard still refuses early and the
// only cost is a wasted run, never a duplicate refresh.
const ValueRefreshCooldown = 6 * time.Hour

// ValueRefreshCap mirrors valueRefreshCap in the api-server's
// recommendation/pr_value_refresh.go, for the same reason ValueRefreshCooldown
// above is mirrored.
//
// Selection has to know it, not just the guard. Once a pull request has used its
// budget the guard refuses at valueRefreshBlocked BEFORE any claim is made, so
// nothing re-stamps last_value_refresh_at — the stamp then ages past the cooldown
// (or is absent entirely) and reads as "due" forever. Without this the exhausted
// workload is reselected for a full generate-execute-apply on every run for the
// rest of that pull request's life, which is exactly the churn the cooldown
// clause exists to prevent.
const ValueRefreshCap = 5

type RecommendationResolutionStatus string

const (
	RecommendationResolutionStatusInProgress RecommendationResolutionStatus = "InProgress"
	RecommendationResolutionStatusSuccess    RecommendationResolutionStatus = "Success"
	RecommendationResolutionStatusFailed     RecommendationResolutionStatus = "Failed"
)

// RecommendationResolutionResolverTypeAutoOptimize mirrors the api-server
// resolver type (models.RecommendationResolutionResolverTypeAutoOptimize) for a
// resolution a scheduled auto optimize raised and therefore owns end to end.
// Only these pull requests are eligible for an in-place value refresh; one a
// person raised by hand is left alone (#34959).
const RecommendationResolutionResolverTypeAutoOptimize = "AutoOptimize"

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
	// AutopilotTaskStatusPendingApproval is a task planned by a run that requires
	// approval. It is never executed in this status; approving the run moves it to
	// Scheduled.
	AutopilotTaskStatusPendingApproval AutopilotTaskStatus = "Pending_Approval"
)

// AutoOptimizeApprovalStatus mirrors the auto_pilot_approval_status enum table.
type AutoOptimizeApprovalStatus string

const (
	AutoOptimizeApprovalStatusPending   AutoOptimizeApprovalStatus = "PENDING"
	AutoOptimizeApprovalStatusApproved  AutoOptimizeApprovalStatus = "APPROVED"
	AutoOptimizeApprovalStatusRejected  AutoOptimizeApprovalStatus = "REJECTED"
	AutoOptimizeApprovalStatusExpired   AutoOptimizeApprovalStatus = "EXPIRED"
	AutoOptimizeApprovalStatusCancelled AutoOptimizeApprovalStatus = "CANCELLED"
)

// AutoOptimizeApprovalType is the auto_pilot_type of an auto optimize approval.
const AutoOptimizeApprovalType = "auto_optimize"

// AutoOptimizeApprovalWindow is how long a run's approval stays open. After it,
// the run's planned tasks are skipped and the next due run plans afresh.
const AutoOptimizeApprovalWindow = 24 * time.Hour

// AutoOptimizeApprovalAttributes is the attributes column of a run's approval.
type AutoOptimizeApprovalAttributes struct {
	TaskIDs   []uuid.UUID `json:"task_ids"`
	ExpiresAt time.Time   `json:"expires_at"`
	// PlanFingerprint is AutoOptimize.PlanFingerprint at planning time. Approving
	// is refused once the automation's plan-affecting settings no longer match.
	PlanFingerprint string `json:"plan_fingerprint"`
}

// AutoOptimizeApproval is one gated run waiting for, or past, a decision.
type AutoOptimizeApproval struct {
	ID               uuid.UUID                      `json:"id"`
	AutoPilotID      uuid.UUID                      `json:"autopilot_id"`
	TenantID         uuid.UUID                      `json:"tenant_id"`
	AccountID        uuid.UUID                      `json:"account_id"`
	Status           AutoOptimizeApprovalStatus     `json:"status"`
	ReviewerID       *uuid.UUID                     `json:"reviewer_id,omitempty"`
	ReviewerComments *string                        `json:"reviewer_comments,omitempty"`
	CreatedAt        time.Time                      `json:"created_at"`
	UpdatedAt        *time.Time                     `json:"updated_at,omitempty"`
	Attributes       AutoOptimizeApprovalAttributes `json:"attributes"`
}

// ScheduledTaskStaleAfter bounds how long a task may sit in Scheduled and still
// hold its recommendation back from a fresh run.
//
// A task in Scheduled is normally one waiting its turn inside a live run, so
// skipping its recommendation is correct. But a run that dies before writing a
// terminal status leaves a row that looks identical and never clears, which used
// to disable that workload permanently (#34943).
//
// The optimizer workflow carries a 1h execution timeout, so a genuinely in-flight
// task cannot outlive that. This sits comfortably above it: long enough that a
// slow-but-live run is never double-scheduled (which would raise a second PR,
// the regression #33523 fixed), short enough that a wedged workload recovers on
// its own within a couple of cycles.
const ScheduledTaskStaleAfter = 2 * time.Hour

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
	// ResizePolicy overrides the in-place resizePolicy the code agent writes
	// into the PR: "" (default cpu/mem NotRequired), "restart-memory", or
	// "disabled". Only applied when the cluster is >= 1.35.
	ResizePolicy string `json:"resize_policy,omitempty"`
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
	// RequireApproval makes each run plan its changes and wait for an account
	// editor to approve them before anything is applied.
	RequireApproval bool `json:"require_approval,omitempty"`
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
	FinopsScore          *float64       `json:"finops_score,omitempty" db:"finops_score"`
}

// RecommendationSnapshot is a recommendation's current state, read when a
// parked run is approved to decide which of its tasks still apply.
type RecommendationSnapshot struct {
	Status      string
	IsDismissed bool
	Content     map[string]any
}

// StillApplicable reports whether a task planned against this recommendation
// may still run: not dismissed, closed or archived.
func (r RecommendationSnapshot) StillApplicable() bool {
	return !r.IsDismissed &&
		(r.Status == string(RecommendationStatusOpen) || r.Status == string(RecommendationStatusInProgress))
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
	PRLifecycleState *string                      `json:"pr_lifecycle_state,omitempty"` // e.g. "created" once the GitOps PR exists
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
	// PRAction is what the apply did to the pull request: "created", "refreshed",
	// "unchanged", or "" when the api-server did not say. A task holding a
	// resolution id is not by itself evidence the run changed anything — the
	// open-PR guard returns the existing resolution untouched — so this is what
	// separates the two. See notifications.go:classifyTask.
	PRAction string `json:"pr_action,omitempty"`
	// PRDecision is the open-PR guard's own words for what it decided —
	// "still within the change threshold", "was updated less than 6h0m0s ago",
	// "recommendation has changed ...; updating it".
	//
	// PRAction says WHICH of the three happened; this says why. They are not
	// redundant on the unchanged path, which is the common one: the generator
	// measures drift against the live cluster allocation and the guard measures
	// it against the values already on the pull request, so a run routinely
	// reports a large change and then correctly does nothing. Without the reason
	// those two readings cannot be reconciled by anyone debugging it.
	PRDecision string `json:"pr_decision,omitempty"`
	// PlannedRecommendationHash is PlannedValuesHash of the recommendation when a
	// gated run planned this task. Approving skips the task if the recommended
	// values have moved since, rather than apply a plan the data no longer backs.
	PlannedRecommendationHash string `json:"planned_recommendation_hash,omitempty"`
}

// PRAction values, mirroring the api-server's recommendation.PRAction. Only the
// one the optimizer branches on is named; the others travel through as data.
const (
	PRActionUnchanged = "unchanged"
)

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
	// RequireApproval is a pointer so an update that omits it keeps the stored
	// value instead of switching the approval gate off.
	RequireApproval *bool `json:"require_approval,omitempty"`
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

// RequiresApproval reports whether a run of this automation must be approved
// before it applies anything. Dry runs apply nothing, and continuous rightsizing
// is decided by the agent at apply time, so there is no plan to review first.
func (ao AutoOptimize) RequiresApproval() bool {
	return ao.Attributes.RequireApproval &&
		ao.Status == AutoOptimizeStatusActive &&
		ao.Category != "continuous_rightsize"
}

// PlanFingerprint hashes the settings that decide what a run plans and how it
// applies it. Notification settings, the schedule and the name are left out:
// changing them does not change a parked plan.
func (ao AutoOptimize) PlanFingerprint() string {
	filters := make([]string, 0, len(ao.ResourceFilters))
	for _, f := range ao.ResourceFilters {
		filters = append(filters, f.String())
	}
	sort.Strings(filters)
	b, _ := json.Marshal(struct {
		Category string         `json:"category"`
		Rule     map[string]any `json:"rule"`
		Filters  []string       `json:"filters"`
		GitOps   GitOpsConfig   `json:"git_ops"`
		Ticket   TicketConfig   `json:"ticket"`
	}{ao.Category, ao.Rule, filters, ao.Attributes.GitOpsConfig, ao.Attributes.TicketConfig})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PlannedValuesHash hashes the part of a recommendation that decides what a
// task of this category applies, so approving can tell a moved recommendation
// from a rescan. It is empty when there is nothing stable to compare: replica
// plans read a time-series forecast for the next hour and volume plans read
// current usage, both of which change every scan by design, so those tasks are
// applied as reviewed.
//
// The whole recommendation is not hashed: each container entry also carries
// scan metrics and notes (metric, info, priority, ...) that change on every
// rescan even when the recommended values don't, and updated_at is bumped by a
// trigger on every write.
func PlannedValuesHash(category string, content map[string]any) string {
	if category != "vertical_rightsize" || len(content) == 0 {
		return ""
	}
	projection := map[string]any{}
	for key, val := range content {
		switch v := val.(type) {
		case []any:
			var entries []any
			for _, e := range v {
				m, ok := e.(map[string]any)
				if !ok {
					continue
				}
				entries = append(entries, map[string]any{
					"resource":    m["resource"],
					"allocated":   m["allocated"],
					"recommended": m["recommended"],
				})
			}
			projection[key] = entries
		default:
			if key == "cpu" || key == "memory" {
				projection[key] = v
			}
		}
	}
	b, _ := json.Marshal(projection)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
