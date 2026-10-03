package playbooks

import (
	"log/slog"
	"time"
)

type PlaybookEvent struct {
	EventId     string            `json:"event_id"`
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartedAt   *time.Time        `json:"started_at"`
	EndedAt     *time.Time        `json:"ended_at"`
	// IncidentAt is when the failure actually happened, as opposed to when we
	// noticed it. Set only when something authoritative told us — currently the
	// container termination time read off the pod object for the
	// terminated-container classes. When present it is the anchor every
	// observability window is centred on; StartedAt stays the detection time,
	// which is what triage, dedup and the inbox key on.
	IncidentAt   *time.Time `json:"incident_at,omitempty"`
	SubjectName  string     `json:"subject_name"`
	SubjectType  string     `json:"subject_type"`
	SubjectOwner string     `json:"subject_owner"`
	// SubjectOwnerKind is the kind of SubjectOwner (the pod's top-level
	// controller), e.g. "Deployment"/"StatefulSet"/"DaemonSet". Casing is not
	// normalized upstream.
	SubjectOwnerKind string `json:"subject_owner_kind"`
	SubjectNamespace string `json:"subject_namespace"`
	// SubjectNode is the K8s node hosting the pod-subject. Populated by the
	// trigger engine (kubewatch payload) for pod events, by `node` label for
	// alert-driven events. Enrichers that key on the pod's host node
	// (noisy_neighbours, pod_node_metrics) read this directly to avoid a
	// relay round-trip to re-fetch the pod.
	SubjectNode             string          `json:"subject_node"`
	AggregationKey          string          `json:"aggregation_key"`
	Fingerprint             string          `json:"fingerprint"`
	ExistingEvidenceActions map[string]bool `json:"existing_evidence_actions,omitempty"`
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

// PlaybookActionVariant is implemented by actions registered under several
// names with *different* configuration — e.g. the pod metric enricher's cpu and
// memory variants, which are the same Go type differing only in an unexported
// field. Auto-discovery dedups by implementation type (so that pure aliases like
// cloud_metrics / cloud_list_metrics run once); without a variant key the first
// configured instance to run would silently suppress every other one.
//
// Actions registered under a single name, or under several names with identical
// configuration, must NOT implement this.
type PlaybookActionVariant interface {
	VariantKey() string
}
