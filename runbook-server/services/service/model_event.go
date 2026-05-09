package service

import "time"

type EventPriority string

const (
	EventPriorityDebug  EventPriority = "DEBUG"
	EventPriorityInfo   EventPriority = "INFO"
	EventPriorityLow    EventPriority = "LOW"
	EventPriorityMedium EventPriority = "MEDIUM"
	EventPriorityHigh   EventPriority = "HIGH"
)

type EventStatus string

const (
	EventStatusFiring   EventStatus = "FIRING"
	EventStatusResolved EventStatus = "RESOLVED"
	EventStatusClosed   EventStatus = "CLOSED"
)

type Event struct {
	AccountId        string            `json:"account_id" db:"cloud_account_id"`
	Tenant           string            `json:"tenant" db:"tenant"`
	FindingId        string            `json:"finding_id" db:"finding_id"`
	Title            string            `json:"title" db:"title"`
	Description      string            `json:"description" db:"description"`
	Source           string            `json:"source" db:"source"`
	AggregationKey   string            `json:"aggregation_key" db:"aggregation_key"`
	Failure          string            `json:"failure" db:"failure"`
	FindingType      string            `json:"finding_type" db:"finding_type"`
	Category         string            `json:"category" db:"category"`
	Priority         EventPriority     `json:"priority" db:"priority"`
	SubjectType      string            `json:"subject_type" db:"subject_type,omitempty"`
	SubjectName      string            `json:"subject_name" db:"subject_name,omitempty"`
	SubjectNamespace string            `json:"subject_namespace" db:"subject_namespace,omitempty"`
	SubjectNode      string            `json:"subject_node" db:"subject_node,omitempty"`
	ServiceKey       string            `json:"service_key" db:"service_key,omitempty"`
	Cluster          string            `json:"cluster" db:"cluster,omitempty"`
	EndsAt           *time.Time        `json:"ends_at" db:"ends_at,omitempty"`
	StartsAt         *time.Time        `json:"starts_at" db:"starts_at,omitempty"`
	CreatedAt        *time.Time        `json:"created_at" db:"created_at,omitempty"`
	Fingerprint      string            `json:"fingerprint" db:"fingerprint,omitempty"`
	Evidences        []any             `json:"evidences" db:"evidences,omitempty"`
	CloudResourceId  string            `json:"cloud_resource_id" db:"cloud_resource_id,omitempty"`
	Status           EventStatus       `json:"status" db:"status,omitempty"`
	Principal        string            `json:"principal" db:"principal,omitempty"`
	SubjectOwner     string            `json:"subject_owner" db:"subject_owner,omitempty"`
	SubjectOwnerKind string            `json:"subject_owner_kind" db:"subject_owner_kind,omitempty"`
	Labels           map[string]string `json:"labels" db:"labels,omitempty"`
}

type EventEvidenceInsight struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
type EventEvidence struct {
	Type           string                 `json:"type"`
	Insight        []EventEvidenceInsight `json:"insight"`
	Data           any                    `json:"data"`
	FileName       string                 `json:"filename"`
	AdditionalInfo map[string]any         `json:"additional_info,omitempty"`
}
