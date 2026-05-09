package playbooks

import (
	"log/slog"
	"time"
)

type PlaybookEvent struct {
	EventId                 string            `json:"event_id"`
	Name                    string            `json:"name"`
	Source                  string            `json:"source"`
	Labels                  map[string]string `json:"labels"`
	Annotations             map[string]string `json:"annotations"`
	StartedAt               *time.Time        `json:"started_at"`
	EndedAt                 *time.Time        `json:"ended_at"`
	SubjectName             string            `json:"subject_name"`
	SubjectType             string            `json:"subject_type"`
	SubjectOwner            string            `json:"subject_owner"`
	SubjectNamespace        string            `json:"subject_namespace"`
	AggregationKey          string            `json:"aggregation_key"`
	Fingerprint             string            `json:"fingerprint"`
	ExistingEvidenceActions map[string]bool   `json:"existing_evidence_actions,omitempty"`
}

type PlaybookActionContext interface {
	GetAccountId() string
	GetTenantId() string
	GetLogger() *slog.Logger
	GetEvent() PlaybookEvent
}

type PlaybookActionResponseInsight struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
type PlaybookActionResponse interface {
	GetAdditionalInfo() map[string]any
	GetInsights() []PlaybookActionResponseInsight
	GetFormatName() string
	GetData() any
}

type PlaybookActionResponseLabelExtractor interface {
	ExtractLabels() map[string]any
}

type PlaybookAction interface {
	Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error)
}

type PlaybookAutoAction interface {
	CanAutoExecute(ctx PlaybookActionContext) bool
	AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error)
}
