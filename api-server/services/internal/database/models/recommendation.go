package models

import "time"

type Recommendation struct {
	Id                   string               `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt            *time.Time           `json:"created_at" mapstructure:"created_at"  db:"created_at"`
	UpdatedAt            *time.Time           `json:"updated_at" mapstructure:"updated_at"  db:"updated_at"`
	TenantId             string               `json:"tenant_id" mapstructure:"tenant_id" validate:"required" db:"tenant_id"`
	CloudAccountId       string               `json:"cloud_account_id" mapstructure:"cloud_account_id" validate:"required" db:"cloud_account_id"`
	ResourceId           *string              `json:"resource_id" mapstructure:"resource_id" db:"resource_id"`
	Recommendation       Json                 `json:"recommendation" mapstructure:"recommendation"  db:"recommendation"`
	RecommendationAction *string              `json:"recommendation_action" mapstructure:"recommendation_action"  db:"recommendation_action"`
	Severity             *string              `json:"severity" mapstructure:"severity"  db:"severity"`
	EstimatedSavings     float32              `json:"estimated_savings" mapstructure:"estimated_savings"  db:"estimated_savings"`
	Status               RecommendationStatus `json:"status" mapstructure:"status"  db:"status"`
	Category             string               `json:"category" mapstructure:"category"  db:"category"`
	RuleName             string               `json:"rule_name" mapstructure:"rule_name"  db:"rule_name"`
	AccountObjectId      *string              `json:"account_object_id" mapstructure:"account_object_id"  db:"account_object_id"`
	Note                 *string              `json:"note" mapstructure:"note"  db:"note"`
	DismissedReason      *string              `json:"dismissed_reason" mapstructure:"dismissed_reason"  db:"dismissed_reason"`
	IsDismissed          bool                 `json:"is_dismissed" mapstructure:"is_dismissed"  db:"is_dismissed"`
	UpdatedBy            *string              `json:"updated_by" mapstructure:"updated_by"  db:"updated_by"`
	FinOpsScore          *int                 `json:"finops_score" mapstructure:"finops_score"  db:"finops_score"`
	FinOpsBand           *string              `json:"finops_band" mapstructure:"finops_band"  db:"finops_band"`
	FinOpsScoreBreakdown Json                 `json:"finops_score_breakdown" mapstructure:"finops_score_breakdown"  db:"finops_score_breakdown"`
	LastNudgedAt         *time.Time           `json:"last_nudged_at" mapstructure:"last_nudged_at"  db:"last_nudged_at"`
	DedupeGroup          *string              `json:"dedupe_group" mapstructure:"dedupe_group"  db:"dedupe_group"`
}

type RecommendationStatus string

const (
	RecommendationStatusOpen       RecommendationStatus = "Open"
	RecommendationStatusArchive    RecommendationStatus = "Archive"
	RecommendationStatusClosed     RecommendationStatus = "Closed"
	RecommendationStatusDismissed  RecommendationStatus = "Dismissed"
	RecommendationStatusAssigned   RecommendationStatus = "Assigned"
	RecommendationStatusInProgress RecommendationStatus = "InProgress"
)

type RecommendationResolutionType string
type RecommendationResolutionStatus string
type RecommendationResolutionResolverType string

type RecommendationResolution struct {
	Id               string                               `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt        *time.Time                           `json:"created_at" mapstructure:"created_at"  db:"created_at"`
	UpdatedAt        *time.Time                           `json:"updated_at" mapstructure:"updated_at"  db:"updated_at"`
	RecommendationId string                               `json:"recommendation_id" mapstructure:"recommendation_id" validate:"required" db:"recommendation_id"`
	Type             RecommendationResolutionType         `json:"type" mapstructure:"type"  db:"type"`
	Data             Json                                 `json:"data" mapstructure:"data"  db:"data"`
	Status           RecommendationResolutionStatus       `json:"status" mapstructure:"status"  db:"status"`
	TypeReferenceId  string                               `json:"type_reference_id" mapstructure:"type_reference_id"  db:"type_reference_id"`
	ResolverType     RecommendationResolutionResolverType `json:"resolver_type" mapstructure:"resolver_type"  db:"resolver_type"`
	ResolverId       string                               `json:"resolver_id" mapstructure:"resolver_id"  db:"resolver_id"`
	StatusMessage    *string                              `json:"status_message" mapstructure:"status_message"  db:"status_message"`
	PRIterationCount *int                                 `json:"pr_iteration_count" mapstructure:"pr_iteration_count"  db:"pr_iteration_count"`
	PRLifecycleState *string                              `json:"pr_lifecycle_state" mapstructure:"pr_lifecycle_state"  db:"pr_lifecycle_state"`
	LastPRCheckAt    *time.Time                           `json:"last_pr_check_at" mapstructure:"last_pr_check_at"  db:"last_pr_check_at"`
}

const (
	RecommendationResolutionStatusInProgress RecommendationResolutionStatus = "InProgress"
	RecommendationResolutionStatusFailed     RecommendationResolutionStatus = "Failed"
	RecommendationResolutionStatusSuccess    RecommendationResolutionStatus = "Success"
)

const (
	RecommendationResolutionTypePullRequest      RecommendationResolutionType = "PullRequest"
	RecommendationResolutionTypeTicket           RecommendationResolutionType = "Ticket"
	RecommendationResolutionTypeDeploymentChange RecommendationResolutionType = "DeploymentChange"
	RecommendationEventResolutionType            RecommendationResolutionType = "EventResolution"
	RecommendationResolutionTypeCloudResource       RecommendationResolutionType = "CloudResource"
	RecommendationResolutionTypeWorkflowExecution RecommendationResolutionType = "WorkflowExecution"
)

const (
	RecommendationResolutionResolverTypeUser         RecommendationResolutionResolverType = "User"
	RecommendationResolutionResolverTypeAutoOptimize RecommendationResolutionResolverType = "AutoOptimize"
	RecommendationResolutionResolverTypeAutoRunbook  RecommendationResolutionResolverType = "AutoRunbook"
	RecommendationResolutionResolverTypeNBLLM        RecommendationResolutionResolverType = "NBLLM"
)
