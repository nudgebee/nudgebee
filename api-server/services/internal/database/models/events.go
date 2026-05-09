package models

import "time"

type Event struct {
	Id               string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt        *time.Time `json:"created_at" mapstructure:"created_at"  db:"created_at"`
	UpdatedAt        *time.Time `json:"updated_at" mapstructure:"updated_at"  db:"updated_at"`
	FindingId        *string    `json:"finding_id" mapstructure:"finding_id"  db:"finding_id"`
	Title            string     `json:"title" mapstructure:"title"  db:"title"`
	Description      *string    `json:"description" mapstructure:"description"  db:"description"`
	Source           *string    `json:"source" mapstructure:"source"  db:"source"`
	AggregationKey   *string    `json:"aggregation_key" mapstructure:"aggregation_key"  db:"aggregation_key"`
	Failure          *string    `json:"failure" mapstructure:"failure"  db:"failure"`
	FindingType      *string    `json:"finding_type" mapstructure:"finding_type"  db:"finding_type"`
	Category         *string    `json:"category" mapstructure:"category"  db:"category"`
	Priority         *string    `json:"priority" mapstructure:"priority"  db:"priority"`
	SubjectType      *string    `json:"subject_type" mapstructure:"subject_type"  db:"subject_type"`
	SubjectName      *string    `json:"subject_name" mapstructure:"subject_name"  db:"subject_name"`
	SubjectNamespace *string    `json:"subject_namespace" mapstructure:"subject_namespace"  db:"subject_namespace"`
	SubjectNode      *string    `json:"subject_node" mapstructure:"subject_node"  db:"subject_node"`
	ServiceKey       *string    `json:"service_key" mapstructure:"service_key"  db:"service_key"`
	Cluster          *string    `json:"cluster" mapstructure:"cluster"  db:"cluster"`
	EndsAt           *time.Time `json:"ends_at" mapstructure:"ends_at"  db:"ends_at"`
	StartsAt         *time.Time `json:"starts_at" mapstructure:"starts_at"  db:"starts_at"`
	Fingerprint      *string    `json:"fingerprint" mapstructure:"fingerprint"  db:"fingerprint"`
	Evidences        *Json      `json:"evidences" mapstructure:"evidences"  db:"evidences"`
	Tenant           *string    `json:"tenant" mapstructure:"tenant"  db:"tenant"`
	CloudAccountId   *string    `json:"cloud_account_id" mapstructure:"cloud_account_id"  db:"cloud_account_id"`
	CloudResourceId  *string    `json:"cloud_resource_id" mapstructure:"cloud_resource_id"  db:"cloud_resource_id"`
	Status           *string    `json:"status" mapstructure:"status"  db:"status"`
	// Nudgebee triage status (separate from source status)
	NBStatus          *string    `json:"nb_status" mapstructure:"nb_status" db:"nb_status"`
	NBStatusChangedAt *time.Time `json:"nb_status_changed_at" mapstructure:"nb_status_changed_at" db:"nb_status_changed_at"`
	NBStatusChangedBy *string    `json:"nb_status_changed_by" mapstructure:"nb_status_changed_by" db:"nb_status_changed_by"`
	SnoozedUntil      *time.Time `json:"snoozed_until" mapstructure:"snoozed_until" db:"snoozed_until"`
	Principal         *string    `json:"principal" mapstructure:"principal"  db:"principal"`
	SubjectOwner      *string    `json:"subject_owner" mapstructure:"subject_owner"  db:"subject_owner"`
	SubjectOwnerKind  *string    `json:"subject_owner_kind" mapstructure:"subject_owner_kind"  db:"subject_owner_kind"`
	Labels            *Json      `json:"labels" mapstructure:"labels"  db:"labels"`
	Urgency           *string    `json:"urgency" mapstructure:"urgency"  db:"urgency"`

	// Scoring fields - computed priority based on multi-factor analysis
	ComputedScore    *int     `json:"computed_score" mapstructure:"computed_score" db:"computed_score"`
	ComputedPriority *string  `json:"computed_priority" mapstructure:"computed_priority" db:"computed_priority"`
	ScoreFactors     *Json    `json:"score_factors" mapstructure:"score_factors" db:"score_factors"`
	ScoreConfidence  *float64 `json:"score_confidence" mapstructure:"score_confidence" db:"score_confidence"`
}

type EventResolution struct {
	Id               string                               `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt        *time.Time                           `json:"created_at" mapstructure:"created_at"  db:"created_at"`
	UpdatedAt        *time.Time                           `json:"updated_at" mapstructure:"updated_at"  db:"updated_at"`
	EventId          string                               `json:"event_id" mapstructure:"event_id" validate:"required" db:"event_id"`
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

type UpdateEventRequest struct {
	Urgency          string `json:"urgency" mapstructure:"urgency"`
	EventId          string `json:"event_id" mapstructure:"event_id"`
	SubjectName      string `json:"subject_name" mapstructure:"subject_name"`
	SubjectNamespace string `json:"subject_namespace" mapstructure:"subject_namespace"`
	SubjectType      string `json:"subject_type" mapstructure:"subject_type"`
}
