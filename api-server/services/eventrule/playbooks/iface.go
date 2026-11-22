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
	// StartedAt is the start of the observability window, NOT the alert's start:
	// the event service sets it 10 minutes before the alert so metric and log
	// queries cover the lead-up. Anything asking "what happened before the
	// alert" must use AlertStart instead, or it misses those 10 minutes.
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	// DetectedAt is the alert's own start time, without the lead-in applied to
	// StartedAt. Read it through AlertStart.
	DetectedAt *time.Time `json:"detected_at,omitempty"`
	// IncidentAt is when the failure actually happened, as opposed to when we
	// noticed it. Set only when something authoritative told us — currently the
	// container termination time read off the pod object for the
	// terminated-container classes. When present it is the anchor every
	// observability window is centred on; DetectedAt stays the detection time,
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
	// CloudResourceId is the cloud_resourses row the event is already linked to,
	// when the emitter linked it. Enrichers that describe the event's resource read
	// this row rather than re-matching it from labels, which can hit another row
	// that shares the resource's id (a cost-report or Systems Manager record).
	CloudResourceId string `json:"cloud_resource_id,omitempty"`
	// Deadline, when non-zero, bounds the whole playbook run. ExecutePlaybook checks
	// it before starting each action and stops early, returning the results it has
	// so far rather than abandoning them. Actions themselves are not cancellable
	// (PlaybookActionContext carries no context.Context), so the real overrun is
	// bounded by this deadline plus one action's own HTTP timeout — worst case the
	// 180s cloud_service_map call.
	//
	// Set by the async enrichment consumer so one event cannot outlive the broker's
	// consumer_timeout and get its message redelivered forever. Zero means no bound,
	// which is the inline request path's behaviour.
	Deadline time.Time `json:"deadline,omitempty"`
}

// AlertStart is when the alert started: DetectedAt when the caller set it,
// otherwise StartedAt (callers that build a PlaybookEvent by hand set StartedAt
// to the alert start). Nil when neither is set.
func (e PlaybookEvent) AlertStart() *time.Time {
	if e.DetectedAt != nil && !e.DetectedAt.IsZero() {
		return e.DetectedAt
	}
	return e.StartedAt
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

// PlaybookActionAgentOptional is implemented by a k8s_*-prefixed action that no
// longer needs a connected agent.
//
// Auto-discovery skips every k8s_* action when the account has no agent, because
// those actions used to reach the cluster through relay-server and would only
// produce "agent not connected" errors. An action that now reads from the
// account's own metrics provider — which may be a Prometheus connected directly,
// with no agent anywhere — is no longer in that category, and says so here.
// Implement it only after the action has genuinely stopped calling the relay.
type PlaybookActionAgentOptional interface {
	AgentOptional() bool
}
