package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(DynatraceWebhook{})
}

const IntegrationDynatraceWebhook = "dynatrace_webhook"

// dynatraceProblemIDPattern validates that ProblemID is safe to embed in URLs / API calls.
// Dynatrace problem IDs always match P-\d+ (e.g. P-12345).
var dynatraceProblemIDPattern = regexp.MustCompile(`^P-\d+$`)

type DynatraceWebhook struct{}

func (m DynatraceWebhook) Name() string {
	return IntegrationDynatraceWebhook
}

func (m DynatraceWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m DynatraceWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Dynatrace Webhook",
				Default:     "",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:     core.ToolSchemaTypeString,
				Default:  "",
				Priority: 70,
			},
		},
	}
}

func (m DynatraceWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m DynatraceWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// ---------------------------------------------------------------------------
// Dynatrace webhook payload structs
// ---------------------------------------------------------------------------

// DynatraceWebhookPayload is the default payload Dynatrace sends to a webhook.
// Users configure the webhook in Settings → Integrations → Problem notifications.
// Field names match the Dynatrace default Freemarker template keys.
type DynatraceWebhookPayload struct {
	State            string                    `json:"State"`     // OPEN, RESOLVED, MERGED
	ProblemID        string                    `json:"ProblemID"` // P-XXXXX
	ProblemTitle     string                    `json:"ProblemTitle"`
	ProblemSeverity  string                    `json:"ProblemSeverity"` // AVAILABILITY, ERROR, PERFORMANCE, RESOURCE_CONTENTION, CUSTOM_ALERT
	ProblemImpact    string                    `json:"ProblemImpact"`   // APPLICATION, SERVICE, INFRASTRUCTURE
	ProblemURL       string                    `json:"ProblemURL"`
	ProblemDuration  int64                     `json:"ProblemDuration"`  // seconds
	ImpactedEntities []DynatraceImpactedEntity `json:"ImpactedEntities"` // may be absent in older DT versions
	Tags             string                    `json:"Tags"`             // comma-separated tag string
	Timestamp        string                    `json:"Timestamp"`        // ISO 8601 start time
}

// DynatraceImpactedEntity represents a single entity affected by the problem.
type DynatraceImpactedEntity struct {
	EntityId   DynatraceEntityId `json:"entityId"`
	Name       string            `json:"name"`
	EntityType string            `json:"entityType"` // SERVICE, PROCESS_GROUP, HOST, APPLICATION, etc.
}

// DynatraceEntityId is the composite identifier for a Dynatrace entity.
type DynatraceEntityId struct {
	Id   string `json:"id"`
	Type string `json:"type"`
}

// ---------------------------------------------------------------------------
// Dynatrace API v2 response structs (Problem Details)
// ---------------------------------------------------------------------------

// DynatraceProblemDetails is the response body from
// GET /platform/classic/environment-api/v2/problems/{problemId} (or /api/v2/problems/{problemId}).
type DynatraceProblemDetails struct {
	ProblemId        string                    `json:"problemId"`
	DisplayId        string                    `json:"displayId"`
	Title            string                    `json:"title"`
	SeverityLevel    string                    `json:"severityLevel"`
	Status           string                    `json:"status"`      // OPEN, RESOLVED
	ImpactLevel      string                    `json:"impactLevel"` // APPLICATION, SERVICE, INFRASTRUCTURE
	StartTime        int64                     `json:"startTime"`   // epoch ms; -1 when unknown
	EndTime          int64                     `json:"endTime"`     // epoch ms; -1 when still open
	ImpactedEntities []DynatraceImpactedEntity `json:"impactedEntities"`
	Tags             []DynatraceAPITag         `json:"tags"`
	RootCauseEntity  *DynatraceImpactedEntity  `json:"rootCauseEntity"`
}

// DynatraceAPITag is a single tag returned by the Dynatrace v2 API.
type DynatraceAPITag struct {
	Context string `json:"context"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

// ---------------------------------------------------------------------------
// Dynatrace OpenPipeline / Davis Problem payload
// ---------------------------------------------------------------------------

// NumberOrString is a string that also accepts a bare JSON number (e.g. event.severity: 3).
// It stores the raw number as its decimal string representation.
type NumberOrString string

func (s *NumberOrString) UnmarshalJSON(b []byte) error {
	// Try string first (most common case).
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = NumberOrString(str)
		return nil
	}
	// Fall back to number – store as decimal string.
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = NumberOrString(n.String())
	return nil
}

// StringOrSlice accepts either a JSON string or a JSON array of strings.
// Dynatrace DAVIS_EVENT payloads send k8s.* fields as plain strings,
// while DAVIS_PROBLEM payloads send them as arrays. This type handles both.
type StringOrSlice []string

func (s *StringOrSlice) UnmarshalJSON(b []byte) error {
	// JSON null → empty slice (not an error).
	if string(b) == "null" {
		*s = StringOrSlice{}
		return nil
	}
	// Try array first (DAVIS_PROBLEM format).
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*s = arr
		return nil
	}
	// Fall back to plain string (DAVIS_EVENT format).
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	*s = StringOrSlice{str}
	return nil
}

// DynatraceOpenPipelinePayload is sent by Dynatrace via OpenPipeline / Davis problems.
// Keys use dot-notation from the Davis semantic dictionary.
// This format is used when Dynatrace routes Davis problems through OpenPipeline
// (event.kind == "DAVIS_PROBLEM") instead of the classic Freemarker webhook template.
// Fields that can arrive as either a plain string or an array use StringOrSlice.
type DynatraceOpenPipelinePayload struct {
	Timestamp                         string         `json:"timestamp"`
	EventCategory                     string         `json:"event.category"`          // e.g. RESOURCE_CONTENTION
	EventStatus                       string         `json:"event.status"`            // ACTIVE, RESOLVED
	EventStatusTransition             string         `json:"event.status_transition"` // CREATED, REPEATED, RESOLVED
	EventName                         string         `json:"event.name"`              // e.g. "CPU saturation"
	EventDescription                  string         `json:"event.description"`       // markdown
	EventStart                        string         `json:"event.start"`             // ISO 8601 nanosecond precision
	EventID                           string         `json:"event.id"`
	EventKind                         string         `json:"event.kind"` // DAVIS_PROBLEM
	DisplayID                         string         `json:"display_id"` // P-XXXXX
	AffectedEntityIDs                 StringOrSlice  `json:"affected_entity_ids"`
	AffectedEntityTypes               StringOrSlice  `json:"affected_entity_types"`
	RelatedEntityIDs                  StringOrSlice  `json:"related_entity_ids"`
	EntityTags                        StringOrSlice  `json:"entity_tags"`
	HostName                          StringOrSlice  `json:"host.name"`
	K8sClusterName                    StringOrSlice  `json:"k8s.cluster.name"`
	K8sClusterUID                     StringOrSlice  `json:"k8s.cluster.uid"`
	K8sNamespaceName                  StringOrSlice  `json:"k8s.namespace.name"`
	K8sNodeName                       StringOrSlice  `json:"k8s.node.name"`
	K8sWorkloadName                   StringOrSlice  `json:"k8s.workload.name"`
	K8sWorkloadKind                   StringOrSlice  `json:"k8s.workload.kind"`
	AffectedEntityNames               StringOrSlice  `json:"affected_entity_names"`
	GCPProjectID                      StringOrSlice  `json:"gcp.project.id"`
	DavisImpactLevel                  StringOrSlice  `json:"dt.davis.impact_level"`
	DavisMuteStatus                   string         `json:"dt.davis.mute.status"`
	DavisIsFrequentEvent              bool           `json:"dt.davis.is_frequent_event"`
	DavisIsDuplicate                  bool           `json:"dt.davis.is_duplicate"`
	DavisEventIDs                     StringOrSlice  `json:"dt.davis.event_ids"`
	DtEntityHost                      StringOrSlice  `json:"dt.entity.host"`
	DtEntityKubernetesCluster         StringOrSlice  `json:"dt.entity.kubernetes_cluster"`
	DtEntityCloudApplication          StringOrSlice  `json:"dt.entity.cloud_application"`
	DtEntityCloudApplicationNamespace StringOrSlice  `json:"dt.entity.cloud_application_namespace"`
	DtEntityGCPZone                   StringOrSlice  `json:"dt.entity.gcp_zone"`
	EventSeverity                     NumberOrString `json:"event.severity"` // may be a string ("HIGH") or integer (3)
	AnalysisReady                     bool           `json:"dt.analysis.ready"`
	UnderMaintenance                  bool           `json:"maintenance.is_under_maintenance"`
	SmartscapeAffectedEntityIDs       StringOrSlice  `json:"smartscape.affected_entity.ids"`
	SmartscapeAffectedEntityTypes     StringOrSlice  `json:"smartscape.affected_entity.types"`
	SmartscapeRelatedEntityIDs        StringOrSlice  `json:"smartscape.related_entity.ids"`
	SmartscapeRelatedEntityTypes      StringOrSlice  `json:"smartscape.related_entity.types"`
	OpenpipelineSource                string         `json:"dt.openpipeline.source"`
	OpenpipelinePipelines             StringOrSlice  `json:"dt.openpipeline.pipelines"`
}

// ---------------------------------------------------------------------------
// Dynatrace OpenPipeline / DAVIS_EVENT payload structs
// ---------------------------------------------------------------------------

// DynatraceSmartscapeRelatedEntity is a single entity in the
// smartscape.related_entities array, sent by DAVIS_EVENT payloads.
type DynatraceSmartscapeRelatedEntity struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// DynatraceDavisEventPayload is the OpenPipeline payload for event.kind == "DAVIS_EVENT".
// These are transient, infrastructure-level events such as process restarts, config changes,
// and OOM kills. Unlike DAVIS_PROBLEM payloads, they carry no display_id and use event.id
// as their identifier. K8s, host, and GCP metadata fields arrive as plain strings (not arrays).
type DynatraceDavisEventPayload struct {
	Timestamp             string         `json:"timestamp"`
	EventKind             string         `json:"event.kind"`              // DAVIS_EVENT
	EventType             string         `json:"event.type"`              // PROCESS_RESTART, CONFIG_CHANGE, etc.
	EventCategory         string         `json:"event.category"`          // INFO, ERROR, PERFORMANCE, etc.
	EventStatus           string         `json:"event.status"`            // OPEN, CLOSED
	EventStatusTransition string         `json:"event.status_transition"` // CREATED, UPDATED, CLOSED
	EventName             string         `json:"event.name"`
	EventDescription      string         `json:"event.description"`
	EventGroupLabel       string         `json:"event.group_label"`
	EventStart            string         `json:"event.start"` // ISO 8601 nanosecond precision
	EventEnd              string         `json:"event.end"`   // ISO 8601 nanosecond precision
	EventID               string         `json:"event.id"`
	EventProvider         string         `json:"event.provider"` // ONEAGENT, etc.
	EventSeverity         NumberOrString `json:"event.severity"` // may be a string or integer

	// Source entity that triggered the event.
	DtSourceEntity     string `json:"dt.source_entity"`
	DtSourceEntityType string `json:"dt.source_entity.type"`

	// Resolved entity display names (plain strings in DAVIS_EVENT).
	DtEntityHostName                 string `json:"dt.entity.host.name"`
	DtEntityKubernetesClusterName    string `json:"dt.entity.kubernetes_cluster.name"`
	DtEntityProcessGroupName         string `json:"dt.entity.process_group.name"`
	DtEntityProcessGroupInstanceName string `json:"dt.entity.process_group_instance.name"`
	DtEntityGCPZoneName              string `json:"dt.entity.gcp_zone.name"`

	// Entity IDs (plain strings in DAVIS_EVENT).
	DtEntityHost                 string `json:"dt.entity.host"`
	DtEntityKubernetesCluster    string `json:"dt.entity.kubernetes_cluster"`
	DtEntityProcessGroup         string `json:"dt.entity.process_group"`
	DtEntityProcessGroupInstance string `json:"dt.entity.process_group_instance"`
	DtEntityGCPZone              string `json:"dt.entity.gcp_zone"`

	// Smartscape topology references (plain strings in DAVIS_EVENT).
	DtSmartscapeK8sCluster string `json:"dt.smartscape.k8s_cluster"`
	DtSmartscapeK8sNode    string `json:"dt.smartscape.k8s_node"`
	DtSmartscapeHost       string `json:"dt.smartscape.host"`
	DtSmartscapeProcess    string `json:"dt.smartscape.process"`

	// Affected / related entity arrays.
	AffectedEntityIDs            StringOrSlice                      `json:"affected_entity_ids"`
	AffectedEntityTypes          StringOrSlice                      `json:"affected_entity_types"`
	RelatedEntityIDs             StringOrSlice                      `json:"related_entity_ids"`
	SmartscapeRelatedEntities    []DynatraceSmartscapeRelatedEntity `json:"smartscape.related_entities"`
	SmartscapeRelatedEntityIDs   StringOrSlice                      `json:"smartscape.related_entity.ids"`
	SmartscapeRelatedEntityTypes StringOrSlice                      `json:"smartscape.related_entity.types"`

	// Kubernetes metadata (plain strings in DAVIS_EVENT).
	K8sClusterName   string `json:"k8s.cluster.name"`
	K8sClusterUID    string `json:"k8s.cluster.uid"`
	K8sNamespaceName string `json:"k8s.namespace.name"`
	K8sNodeName      string `json:"k8s.node.name"`
	HostName         string `json:"host.name"`

	// GCP metadata.
	GCPProjectID    string `json:"gcp.project.id"`
	GCPRegion       string `json:"gcp.region"`
	GCPZone         string `json:"gcp.zone"`
	GCPInstanceID   string `json:"gcp.instance.id"`
	GCPResourceName string `json:"gcp.resource.name"`

	// Davis AI metadata.
	DavisImpactLevel     string `json:"dt.davis.impact_level"` // plain string in DAVIS_EVENT
	DavisMuteStatus      string `json:"dt.davis.mute.status"`
	DavisIsFrequentEvent bool   `json:"dt.davis.is_frequent_event"`
	DavisTimeout         string `json:"dt.davis.timeout"`

	// Pipeline metadata.
	OpenpipelineSource    string        `json:"dt.openpipeline.source"`
	OpenpipelinePipelines StringOrSlice `json:"dt.openpipeline.pipelines"`

	// Operator version (set by the Dynatrace Operator when applicable).
	OperatorVersion string `json:"OperatorVersion"`

	// Maintenance.
	UnderMaintenance bool `json:"maintenance.is_under_maintenance"`
}

// ---------------------------------------------------------------------------
// DQL execution types (local, to avoid circular import with observability package)
// ---------------------------------------------------------------------------

const (
	dtBearerPrefix             = "Bearer "
	dtContentTypeJSON          = "application/json"
	dtProblemDetailsActionName = "dynatrace_problem_details"
	dtProblemDetailsTitle      = "Dynatrace Problem Details"
	dtDavisEventActionName     = "dynatrace_davis_event"
	dtDavisEventTitle          = "Dynatrace Davis Event"
)

// ---------------------------------------------------------------------------
// ProcessEventWebook — main integration entry point
// ---------------------------------------------------------------------------

func (m DynatraceWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	// Step 1: Detect payload format (classic Freemarker vs OpenPipeline).
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(webhookPayloadString), &probe); err != nil {
		return nil, fmt.Errorf("dynatrace_webhook: failed to parse payload: %w", err)
	}
	if _, isClassic := probe["ProblemID"]; !isClassic {
		// OpenPipeline / Davis Problem format — dot-notation keys.
		return m.processOpenPipelinePayload(sc, settings, accountId, webhookPayloadString)
	}

	// Classic Freemarker template format — parse into DynatraceWebhookPayload.
	var payload DynatraceWebhookPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("dynatrace_webhook: failed to unmarshal payload: %w", err)
	}

	// Step 2: Validate ProblemID.
	if payload.ProblemID == "" {
		return nil, fmt.Errorf("dynatrace_webhook: missing ProblemID in payload")
	}
	// Injection guard — Dynatrace IDs always match P-\d+.
	// Accept non-matching IDs but log a warning to remain compatible with
	// non-standard environments while alerting on potential abuse.
	if !dynatraceProblemIDPattern.MatchString(payload.ProblemID) {
		sc.GetLogger().Warn("dynatrace_webhook: ProblemID does not match expected pattern P-\\d+, proceeding with caution",
			"problem_id", payload.ProblemID)
	}

	// Step 3: Map State → EventStatus.
	status := mapDynatraceStateToStatus(payload.State)

	// Step 4: Try to retrieve Dynatrace credentials for API enrichment.
	// Gracefully degrade when no Dynatrace observability integration is configured.
	apiToken, baseURL, credErr := GetDynatraceConfigs(sc, accountId)
	if credErr != nil {
		sc.GetLogger().Warn("dynatrace_webhook: could not get Dynatrace credentials, skipping API enrichment",
			"account_id", accountId, "error", credErr)
	}

	// Step 5: Try to enrich with full problem details from the Dynatrace API.
	var problemDetails *DynatraceProblemDetails
	if credErr == nil && baseURL != "" {
		var detailErr error
		problemDetails, detailErr = getDynatraceProblemDetails(sc, baseURL, apiToken, payload.ProblemID)
		if detailErr != nil {
			sc.GetLogger().Warn("dynatrace_webhook: failed to fetch problem details from API, using webhook payload",
				"problem_id", payload.ProblemID, "error", detailErr)
		}
	}

	// Step 6: Merge API details with webhook payload (API takes precedence).
	title := payload.ProblemTitle
	severity := payload.ProblemSeverity
	problemURL := payload.ProblemURL
	var startTimeEpochMs int64
	var endTimeEpochMs int64

	if problemDetails != nil {
		if problemDetails.Title != "" {
			title = problemDetails.Title
		}
		if problemDetails.SeverityLevel != "" {
			severity = problemDetails.SeverityLevel
		}
		if problemDetails.StartTime > 0 {
			startTimeEpochMs = problemDetails.StartTime
		}
		if problemDetails.EndTime > 0 {
			endTimeEpochMs = problemDetails.EndTime
		}
		if problemDetails.Status != "" {
			status = mapDynatraceStateToStatus(problemDetails.Status)
		}
	}

	// Step 7: Parse timestamps.
	var createdAt time.Time
	var endsAt time.Time

	if startTimeEpochMs > 0 {
		createdAt = time.UnixMilli(startTimeEpochMs)
	} else if payload.Timestamp != "" {
		var parseErr error
		createdAt, parseErr = time.Parse(time.RFC3339, payload.Timestamp)
		if parseErr != nil {
			// Try without timezone offset (some Dynatrace environments omit it).
			createdAt, parseErr = time.Parse("2006-01-02T15:04:05", payload.Timestamp)
			if parseErr != nil {
				sc.GetLogger().Warn("dynatrace_webhook: failed to parse Timestamp, using time.Now()",
					"timestamp", payload.Timestamp, "error", parseErr)
				createdAt = time.Now()
			}
		}
	} else {
		createdAt = time.Now()
	}

	if endTimeEpochMs > 0 {
		endsAt = time.UnixMilli(endTimeEpochMs)
	}

	// Step 8: Extract entity names (RootCauseEntity first, then ImpactedEntities).
	entityNames := extractDynatraceEntityNames(payload, problemDetails)

	// Step 9: Map severity to internal EventPriority.
	priority := mapDynatraceSeverity(severity)

	// Step 10: Parse tags.
	tags := parseDynatraceTags(payload.Tags, problemDetails)

	// Step 11: Build labels map.
	labels := make(map[string]string)
	if severity != "" {
		labels["severity"] = severity
	}
	if payload.ProblemImpact != "" {
		labels["impact"] = payload.ProblemImpact
	}
	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
		labels["service"] = entityNames[0]
		// Hand the full candidate list to core.MatchWorkloadAndEnrich so it can
		// fall back through the impacted-entity list when EventSubjectName misses.
		labels["nb_workload_candidates"] = strings.Join(entityNames, ",")
	}
	if len(tags) > 0 {
		labels["tags"] = strings.Join(tags, ",")
	}
	if payload.ProblemDuration > 0 {
		labels["problem_duration_seconds"] = fmt.Sprintf("%d", payload.ProblemDuration)
	}

	// EventSubjectName seeds the central workload match. core enrichment will
	// overwrite namespace/kind/cloud_resource_id on a successful match.
	subjectName := ""
	if len(entityNames) > 0 {
		subjectName = entityNames[0]
	}

	// Step 13: Collect observability evidences via Grail DQL (logs + traces).
	evidences := []event.EventEvidence{}

	// Structured evidence: raw problem data.
	evidences = append(evidences, buildDynatraceProblemEvidence(payload, problemDetails))

	// Grail observability evidences when credentials are available.
	if credErr == nil && baseURL != "" && len(entityNames) > 0 {
		fromTs := createdAt.Add(-2 * time.Hour).UnixMilli()
		toTs := createdAt.Add(6 * time.Hour).UnixMilli()
		evidences = append(evidences, fetchDynatraceWebhookEvidences(sc, apiToken, baseURL, entityNames, fromTs, toTs)...)
	}

	// Step 14: Fingerprint = ProblemID (globally unique within a Dynatrace environment).
	fingerprint := payload.ProblemID

	// Step 15: Build description.
	description := fmt.Sprintf("Dynatrace Problem %s: %s (Severity: %s, Impact: %s)",
		payload.ProblemID, title, severity, payload.ProblemImpact)

	// Step 16: Build event URL.
	eventURL := problemURL
	if eventURL == "" {
		eventURL = fmt.Sprintf("https://dt-url.net/%s", payload.ProblemID)
	}

	// Step 17: Build investigation.
	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      payload.ProblemID,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Step 18: Return the webhook event. Subject kind / cloud_resource_id /
	// owner kind are filled in by core.MatchWorkloadAndEnrich on a successful match.
	webhookEvent := core.EventIncomingWebhook{
		WebhookId:         payload.ProblemID,
		EventType:         "dynatrace_problem",
		EventId:           payload.ProblemID,
		EventUrl:          eventURL,
		EventStatus:       status,
		EventPriority:     severity,
		EventCreatedAt:    createdAt,
		EventEndsAt:       endsAt,
		EventTitle:        title,
		EventDescription:  description,
		EventTags:         tags,
		Investigation:     investigation,
		EventSubjectName:  subjectName,
		AccountId:         accountId,
		EventSubjectOwner: subjectName,
	}

	// Upsert event rule for rule management UI visibility — skip for resolved/merged problems.
	if status != string(event.EventStatusResolved) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			detachedSc := security.NewRequestContext(ctx, sc.GetSecurityContext(), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
			defer func() {
				if r := recover(); r != nil {
					detachedSc.GetLogger().Error("dynatrace_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			severityLabel := strings.ToLower(severity)
			if severityLabel == "" {
				severityLabel = "warning"
			}
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: description,
					Summary:     title,
					Runbook:     "",
				},
				Expr: severity,
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severityLabel},
				Alert:         title,
				Duration:      "0",
				AccountID:     accountId,
				Source:        "dynatrace_webhook",
				Category:      "alert",
				Severity:      severityLabel,
				Enabled:       true,
				TriggerParams: []map[string]any{},
				ActionParams:  []map[string]any{},
			}
			if _, err := eventrule.CreateEventRule(detachedSc, eventReq); err != nil {
				detachedSc.GetLogger().Error("dynatrace_webhook: CreateEventRule failed", "error", err)
			}
		}()
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// ---------------------------------------------------------------------------
// OpenPipeline payload processor
// ---------------------------------------------------------------------------

// processOpenPipelinePayload dispatches to the correct handler based on event.kind.
// DAVIS_EVENT → processDavisEventPayload
// DAVIS_PROBLEM (and unknown kinds) → processDavisProblemPayload
func (m DynatraceWebhook) processOpenPipelinePayload(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(webhookPayloadString), &probe); err != nil {
		return nil, fmt.Errorf("dynatrace_webhook: failed to probe OpenPipeline payload: %w", err)
	}
	var eventKind string
	if raw, ok := probe["event.kind"]; ok {
		_ = json.Unmarshal(raw, &eventKind)
	}

	switch strings.ToUpper(eventKind) {
	case "DAVIS_EVENT":
		return m.processDavisEventPayload(sc, settings, accountId, webhookPayloadString)
	default: // DAVIS_PROBLEM and unknown kinds
		return m.processDavisProblemPayload(sc, settings, accountId, webhookPayloadString)
	}
}

// processDavisProblemPayload handles the Dynatrace OpenPipeline / Davis Problem
// webhook format where keys use dot-notation (event.id, host.name, etc.).
// It produces the same EventIncomingWebhook output as the classic path.
func (m DynatraceWebhook) processDavisProblemPayload(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload DynatraceOpenPipelinePayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("dynatrace_webhook: failed to unmarshal OpenPipeline payload: %w", err)
	}

	// Resolve the problem ID: display_id (P-XXXXX) is the canonical identifier.
	problemID := payload.DisplayID
	if problemID == "" {
		// Fall back to the raw event.id when display_id is absent.
		problemID = payload.EventID
	}
	if problemID == "" {
		return nil, fmt.Errorf("dynatrace_webhook: missing display_id and event.id in OpenPipeline payload")
	}
	if !dynatraceProblemIDPattern.MatchString(problemID) {
		sc.GetLogger().Warn("dynatrace_webhook: display_id does not match expected P-\\d+ pattern, proceeding with caution",
			"display_id", problemID)
	}

	// Map event.status → internal EventStatus.
	status := mapDynatraceOpenPipelineStatus(payload.EventStatus)

	// Try to get Dynatrace credentials for API enrichment.
	apiToken, baseURL, credErr := GetDynatraceConfigs(sc, accountId)
	if credErr != nil {
		sc.GetLogger().Warn("dynatrace_webhook: could not get Dynatrace credentials, skipping API enrichment",
			"account_id", accountId, "error", credErr)
	}

	// Attempt API enrichment using display_id (same endpoint as classic path).
	var problemDetails *DynatraceProblemDetails
	if credErr == nil && baseURL != "" && dynatraceProblemIDPattern.MatchString(problemID) {
		var detailErr error
		problemDetails, detailErr = getDynatraceProblemDetails(sc, baseURL, apiToken, problemID)
		if detailErr != nil {
			sc.GetLogger().Warn("dynatrace_webhook: failed to fetch problem details from API, using webhook payload",
				"problem_id", problemID, "error", detailErr)
		}
	}

	// Resolve title, severity, timestamps from API (takes precedence) or payload.
	title := payload.EventName
	severity := payload.EventCategory
	var startTimeEpochMs int64
	var endTimeEpochMs int64

	if problemDetails != nil {
		if problemDetails.Title != "" {
			title = problemDetails.Title
		}
		if problemDetails.SeverityLevel != "" {
			severity = problemDetails.SeverityLevel
		}
		if problemDetails.StartTime > 0 {
			startTimeEpochMs = problemDetails.StartTime
		}
		if problemDetails.EndTime > 0 {
			endTimeEpochMs = problemDetails.EndTime
		}
		if problemDetails.Status != "" {
			status = mapDynatraceStateToStatus(problemDetails.Status)
		}
	}

	// Parse timestamps. event.start uses nanosecond precision ISO 8601.
	var createdAt time.Time
	var endsAt time.Time

	if startTimeEpochMs > 0 {
		createdAt = time.UnixMilli(startTimeEpochMs).UTC()
	} else {
		startStr := payload.EventStart
		if startStr == "" {
			startStr = payload.Timestamp
		}
		if startStr != "" {
			var parseErr error
			createdAt, parseErr = time.Parse(time.RFC3339Nano, startStr)
			if parseErr != nil {
				createdAt, parseErr = time.Parse(time.RFC3339, startStr)
				if parseErr != nil {
					sc.GetLogger().Warn("dynatrace_webhook: failed to parse event.start, using time.Now()",
						"event_start", startStr, "error", parseErr)
					createdAt = time.Now()
				}
			}
		} else {
			createdAt = time.Now()
		}
	}
	if endTimeEpochMs > 0 {
		endsAt = time.UnixMilli(endTimeEpochMs).UTC()
	}

	// Build entity names for workload matching + DQL queries.
	// Priority: k8s.workload.name → k8s.node.name → host.name (deduplicated).
	entityNames := deduplicateStrings(append(append(payload.K8sWorkloadName, payload.K8sNodeName...), payload.HostName...))

	// If API returned impacted entities, prefer those (more accurate names).
	if problemDetails != nil {
		apiNames := extractDynatraceEntityNames(DynatraceWebhookPayload{}, problemDetails)
		if len(apiNames) > 0 {
			entityNames = apiNames
		}
	}

	// Map severity to internal EventPriority.
	priority := mapDynatraceSeverity(severity)

	// Build tags from entity_tags array (already []string, no splitting needed).
	tags := parseDynatraceTags("", problemDetails)
	seen := make(map[string]bool)
	for _, t := range tags {
		seen[t] = true
	}
	for _, t := range payload.EntityTags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}

	// Build labels map.
	labels := make(map[string]string)
	if severity != "" {
		labels["severity"] = severity
	}
	if len(payload.DavisImpactLevel) > 0 {
		labels["impact"] = payload.DavisImpactLevel[0]
	}
	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
		labels["service"] = entityNames[0]
		labels["nb_workload_candidates"] = strings.Join(entityNames, ",")
	}
	if len(tags) > 0 {
		labels["tags"] = strings.Join(tags, ",")
	}
	if len(payload.K8sClusterName) > 0 {
		labels["k8s_cluster_name"] = payload.K8sClusterName[0]
	}
	if len(payload.K8sClusterUID) > 0 {
		labels["k8s_cluster_uid"] = payload.K8sClusterUID[0]
	}
	if len(payload.K8sNamespaceName) > 0 {
		labels["namespace"] = payload.K8sNamespaceName[0]
	}
	if len(payload.K8sWorkloadName) > 0 {
		labels["k8s_workload_name"] = payload.K8sWorkloadName[0]
	}
	if len(payload.K8sWorkloadKind) > 0 {
		labels["kind"] = payload.K8sWorkloadKind[0]
	}
	if len(payload.GCPProjectID) > 0 {
		labels["gcp_project_id"] = payload.GCPProjectID[0]
	}
	if payload.EventStatusTransition != "" {
		labels["dt_status_transition"] = payload.EventStatusTransition
	}
	if payload.EventKind != "" {
		labels["dt_event_kind"] = payload.EventKind
	}
	if payload.DavisMuteStatus != "" {
		labels["dt_mute_status"] = payload.DavisMuteStatus
	}
	if payload.DavisIsFrequentEvent {
		labels["dt_is_frequent"] = "true"
	}
	if payload.DavisIsDuplicate {
		labels["dt_is_duplicate"] = "true"
	}
	if payload.UnderMaintenance {
		labels["under_maintenance"] = "true"
	}
	if len(payload.AffectedEntityIDs) > 0 {
		labels["affected_entity_ids"] = strings.Join(payload.AffectedEntityIDs, ",")
	}
	if len(payload.AffectedEntityTypes) > 0 {
		labels["affected_entity_types"] = strings.Join(payload.AffectedEntityTypes, ",")
	}
	if len(payload.AffectedEntityNames) > 0 {
		labels["affected_entity_names"] = strings.Join(payload.AffectedEntityNames, ",")
	}
	if len(payload.RelatedEntityIDs) > 0 {
		labels["related_entity_ids"] = strings.Join(payload.RelatedEntityIDs, ",")
	}
	if payload.EventCategory != "" {
		labels["event_category"] = payload.EventCategory
	}
	if payload.EventStatus != "" {
		labels["event_status"] = payload.EventStatus
	}
	if payload.EventName != "" {
		labels["event_name"] = payload.EventName
	}
	if payload.EventID != "" {
		labels["event_id"] = payload.EventID
	}
	if payload.DisplayID != "" {
		labels["display_id"] = payload.DisplayID
	}
	if payload.EventStart != "" {
		labels["event_start"] = payload.EventStart
	}
	if payload.EventSeverity != "" {
		labels["event_severity"] = string(payload.EventSeverity)
	}
	if len(payload.HostName) > 0 {
		labels["host_name"] = strings.Join(payload.HostName, ",")
	}
	if len(payload.K8sNodeName) > 0 {
		labels["k8s_node_name"] = strings.Join(payload.K8sNodeName, ",")
	}
	if len(payload.DavisEventIDs) > 0 {
		labels["dt_davis_event_ids"] = strings.Join(payload.DavisEventIDs, ",")
	}
	if len(payload.DtEntityHost) > 0 {
		labels["dt_entity_host"] = strings.Join(payload.DtEntityHost, ",")
	}
	if len(payload.DtEntityKubernetesCluster) > 0 {
		labels["dt_entity_kubernetes_cluster"] = strings.Join(payload.DtEntityKubernetesCluster, ",")
	}
	if len(payload.DtEntityCloudApplication) > 0 {
		labels["dt_entity_cloud_application"] = strings.Join(payload.DtEntityCloudApplication, ",")
	}
	if len(payload.DtEntityCloudApplicationNamespace) > 0 {
		labels["dt_entity_cloud_application_namespace"] = strings.Join(payload.DtEntityCloudApplicationNamespace, ",")
	}
	if len(payload.SmartscapeAffectedEntityIDs) > 0 {
		labels["smartscape_affected_entity_ids"] = strings.Join(payload.SmartscapeAffectedEntityIDs, ",")
	}
	if len(payload.SmartscapeAffectedEntityTypes) > 0 {
		labels["smartscape_affected_entity_types"] = strings.Join(payload.SmartscapeAffectedEntityTypes, ",")
	}
	if len(payload.SmartscapeRelatedEntityIDs) > 0 {
		labels["smartscape_related_entity_ids"] = strings.Join(payload.SmartscapeRelatedEntityIDs, ",")
	}
	if len(payload.SmartscapeRelatedEntityTypes) > 0 {
		labels["smartscape_related_entity_types"] = strings.Join(payload.SmartscapeRelatedEntityTypes, ",")
	}
	if payload.OpenpipelineSource != "" {
		labels["dt_openpipeline_source"] = payload.OpenpipelineSource
	}
	if len(payload.OpenpipelinePipelines) > 0 {
		labels["dt_openpipeline_pipelines"] = strings.Join(payload.OpenpipelinePipelines, ",")
	}

	// EventSubjectName seeds the central workload match. core enrichment will
	// fall back through nb_workload_candidates and overwrite subject fields on
	// a successful match.
	subjectName := ""
	if len(entityNames) > 0 {
		subjectName = entityNames[0]
	}

	// Build event URL: prefer the real tenant UI link when baseURL is known.
	var eventURL string
	if baseURL != "" {
		eventURL = fmt.Sprintf("%s/ui/problems/%s", strings.TrimRight(baseURL, "/"), problemID)
	}

	// Collect observability evidences.
	evidences := []event.EventEvidence{}
	evidences = append(evidences, buildDynatraceOpenPipelineProblemEvidence(payload, problemDetails))

	if credErr == nil && baseURL != "" && len(entityNames) > 0 {
		fromTs := createdAt.Add(-2 * time.Hour).UnixMilli()
		toTs := createdAt.Add(6 * time.Hour).UnixMilli()
		evidences = append(evidences, fetchDynatraceWebhookEvidences(sc, apiToken, baseURL, entityNames, fromTs, toTs)...)
	}

	// Build description from event.description (markdown) or synthesize one.
	description := payload.EventDescription
	if description == "" {
		impactStr := ""
		if len(payload.DavisImpactLevel) > 0 {
			impactStr = payload.DavisImpactLevel[0]
		}
		description = fmt.Sprintf("Dynatrace Problem %s: %s (Severity: %s, Impact: %s)",
			problemID, title, severity, impactStr)
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      problemID,
		Fingerprint: problemID,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Subject kind / cloud_resource_id / owner kind are filled in by
	// core.MatchWorkloadAndEnrich on a successful match.
	webhookEvent := core.EventIncomingWebhook{
		WebhookId:         problemID,
		EventType:         "dynatrace_problem",
		EventId:           problemID,
		EventUrl:          eventURL,
		EventStatus:       status,
		EventPriority:     severity,
		EventCreatedAt:    createdAt,
		EventEndsAt:       endsAt,
		EventTitle:        title,
		EventDescription:  description,
		EventTags:         tags,
		Investigation:     investigation,
		EventSubjectName:  subjectName,
		AccountId:         accountId,
		EventSubjectOwner: subjectName,
	}

	// Upsert event rule for rule management UI visibility — skip for resolved problems.
	if status != string(event.EventStatusResolved) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			detachedSc := security.NewRequestContext(ctx, sc.GetSecurityContext(), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
			defer func() {
				if r := recover(); r != nil {
					detachedSc.GetLogger().Error("dynatrace_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			severityLabel := strings.ToLower(severity)
			if severityLabel == "" {
				severityLabel = "warning"
			}
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: description,
					Summary:     title,
					Runbook:     "",
				},
				Expr: severity,
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severityLabel},
				Alert:         title,
				Duration:      "0",
				AccountID:     accountId,
				Source:        "dynatrace_webhook",
				Category:      "alert",
				Severity:      severityLabel,
				Enabled:       true,
				TriggerParams: []map[string]any{},
				ActionParams:  []map[string]any{},
			}
			if _, err := eventrule.CreateEventRule(detachedSc, eventReq); err != nil {
				detachedSc.GetLogger().Error("dynatrace_webhook: CreateEventRule failed", "error", err)
			}
		}()
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// ---------------------------------------------------------------------------
// Dynatrace Problems API helpers
// ---------------------------------------------------------------------------

// getDynatraceProblemDetails fetches full problem details from the Dynatrace API.
// It detects whether to use the Platform API path (.apps.dynatrace.com) or the
// classic API path.
// Returns (nil, nil) when the token lacks required scope (HTTP 401/403/404)
// so callers can gracefully degrade.
func getDynatraceProblemDetails(sc *security.RequestContext, baseURL, apiToken, problemId string) (*DynatraceProblemDetails, error) {
	apiURL := buildDynatraceAPIURL(baseURL, problemId)

	headers := map[string]string{
		"Authorization": dtBearerPrefix + apiToken,
		"Accept":        dtContentTypeJSON + "; charset=utf-8",
	}

	resp, err := common.HttpGet(apiURL, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("dynatrace problems API request failed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read Dynatrace problems API response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusUnauthorized, http.StatusForbidden:
		// Token lacks environment-api:problems:read scope (common for Platform DQL-only tokens).
		// Fall back to Grail DQL which uses the same token with storage scopes.
		sc.GetLogger().Info("dynatrace_webhook: problems REST API returned auth error, trying DQL fallback",
			"status", resp.StatusCode, "problem_id", problemId)
		return getDynatraceProblemDetailsDQL(baseURL, apiToken, problemId)
	case http.StatusNotFound:
		sc.GetLogger().Warn("dynatrace_webhook: problem not found in API",
			"problem_id", problemId)
		return nil, nil
	default:
		return nil, fmt.Errorf("dynatrace problems API returned status %d: %s", resp.StatusCode, string(body))
	}

	var details DynatraceProblemDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("failed to parse Dynatrace problem details: %w", err)
	}
	return &details, nil
}

// ---------------------------------------------------------------------------
// DAVIS_EVENT DQL enrichment
// ---------------------------------------------------------------------------

// DynatraceDavisEventDetails holds enriched event data fetched via DQL from
// the dt.davis.events table. Used to supplement the DAVIS_EVENT webhook payload.
type DynatraceDavisEventDetails struct {
	EventName           string
	EventDescription    string
	EventType           string
	AffectedEntityNames []string
	StartTime           int64 // epoch ms; 0 when absent
	EndTime             int64 // epoch ms; -1 when still open
}

// getDynatraceDavisEventDetails fetches DAVIS_EVENT details from Grail DQL using event.id.
// Returns (nil, nil) when no matching record is found.
func getDynatraceDavisEventDetails(baseURL, apiToken, eventID string) (*DynatraceDavisEventDetails, error) {
	query := fmt.Sprintf(`fetch dt.davis.events | filter event.id == "%s" | limit 1`, escapeDQLString(eventID))
	records, err := runDynatraceDQL(baseURL, apiToken, query)
	if err != nil {
		return nil, fmt.Errorf("dynatrace DQL davis event fetch failed: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	rec := records[0]

	details := &DynatraceDavisEventDetails{EndTime: -1}
	details.EventName, _ = rec["event.name"].(string)
	details.EventDescription, _ = rec["event.description"].(string)
	details.EventType, _ = rec["event.type"].(string)
	details.StartTime = parseDTNanoTime(rec["event.start"])
	if ms := parseDTNanoTime(rec["event.end"]); ms > 0 {
		details.EndTime = ms
	}

	// affected_entity_names may be an array of strings or a plain string.
	switch v := rec["affected_entity_names"].(type) {
	case []interface{}:
		for _, n := range v {
			if s, ok := n.(string); ok && s != "" {
				details.AffectedEntityNames = append(details.AffectedEntityNames, s)
			}
		}
	case string:
		if v != "" {
			details.AffectedEntityNames = []string{v}
		}
	}

	return details, nil
}

// ---------------------------------------------------------------------------
// DAVIS_PROBLEM DQL enrichment (fallback for REST 403)
// ---------------------------------------------------------------------------

// getDynatraceProblemDetailsDQL fetches problem details via Grail DQL.
// Used as a fallback when the REST problems API returns 403 — common for Platform
// tokens that have storage/DQL scopes but not environment-api:problems:read.
func getDynatraceProblemDetailsDQL(baseURL, apiToken, problemId string) (*DynatraceProblemDetails, error) {
	query := fmt.Sprintf(`fetch dt.davis.problems | filter display_id == "%s" | limit 1`, escapeDQLString(problemId))
	records, err := runDynatraceDQL(baseURL, apiToken, query)
	if err != nil {
		return nil, fmt.Errorf("dynatrace DQL problem fallback failed: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	rec := records[0]
	return dqlRecordToProblemDetails(problemId, rec), nil
}

// dqlRecordToProblemDetails maps a dt.davis.problems DQL record to DynatraceProblemDetails.
func dqlRecordToProblemDetails(problemId string, rec map[string]any) *DynatraceProblemDetails {
	details := &DynatraceProblemDetails{
		DisplayId: problemId,
		EndTime:   -1,
	}
	details.Title, _ = rec["event.name"].(string)
	details.SeverityLevel, _ = rec["event.category"].(string)
	details.Status = mapDQLProblemStatus(rec["event.status"])
	details.ImpactLevel = dqlFirstStringUpper(rec["dt.davis.impact_level"])
	details.StartTime = parseDTNanoTime(rec["event.start"])
	if ms := parseDTNanoTime(rec["event.end"]); ms > 0 {
		details.EndTime = ms
	}
	details.ImpactedEntities = dqlExtractEntities(rec)
	return details
}

// mapDQLProblemStatus converts DQL event.status ("ACTIVE"/"CLOSED") to REST API values ("OPEN"/"RESOLVED").
func mapDQLProblemStatus(v any) string {
	s, _ := v.(string)
	switch s {
	case "ACTIVE":
		return "OPEN"
	case "CLOSED":
		return "RESOLVED"
	default:
		return s
	}
}

// dqlFirstStringUpper returns the first element of a DQL []interface{} field as an uppercase string.
func dqlFirstStringUpper(v any) string {
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return ""
	}
	s, _ := arr[0].(string)
	return strings.ToUpper(s)
}

// parseDTNanoTime parses a DQL nanosecond-precision ISO 8601 timestamp to epoch milliseconds.
// Returns 0 on failure.
func parseDTNanoTime(v any) int64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// dqlExtractEntities builds ImpactedEntities from workload/host/node name arrays in a DQL record.
func dqlExtractEntities(rec map[string]any) []DynatraceImpactedEntity {
	var entities []DynatraceImpactedEntity
	seen := make(map[string]bool)
	for _, pair := range []struct {
		field      string
		entityType string
	}{
		{"k8s.workload.name", "CLOUD_APPLICATION"},
		{"service.name", "SERVICE"},
		{"host.name", "HOST"},
		{"k8s.node.name", "HOST"},
	} {
		arr, ok := rec[pair.field].([]interface{})
		if !ok {
			continue
		}
		for _, n := range arr {
			s, ok := n.(string)
			if !ok || s == "" || seen[s] {
				continue
			}
			seen[s] = true
			entities = append(entities, DynatraceImpactedEntity{Name: s, EntityType: pair.entityType})
		}
	}
	return entities
}

// buildDynatraceAPIURL constructs the correct Dynatrace v2 problems API URL.
//   - SaaS platform:  {env-id}.apps.dynatrace.com  → /platform/classic/environment-api/v2/problems/{id}
//   - Classic SaaS:   {env-id}.live.dynatrace.com  → /api/v2/problems/{id}
//   - Managed:        custom domain                → /api/v2/problems/{id}
func buildDynatraceAPIURL(baseURL, problemId string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.Contains(base, ".apps.dynatrace.com") {
		return fmt.Sprintf("%s/platform/classic/environment-api/v2/problems/%s", base, problemId)
	}
	return fmt.Sprintf("%s/api/v2/problems/%s", base, problemId)
}

// ---------------------------------------------------------------------------
// Grail DQL execution (local copy to avoid circular import with observability package)
// The observability package imports integrations for credential lookup, so
// integrations cannot import observability. We therefore inline the minimal
// Grail execute → poll loop here.
// ---------------------------------------------------------------------------

// runDynatraceDQL executes a Dynatrace Grail DQL query and returns the records.
// Delegates to ExecuteDQLQuery (canonical implementation in dynatrace_grail.go).
func runDynatraceDQL(baseURL, bearerToken, query string) ([]map[string]any, error) {
	result, err := ExecuteDQLQuery(baseURL, bearerToken, query)
	if err != nil {
		return nil, err
	}
	return result.Records, nil
}

// ---------------------------------------------------------------------------
// Observability evidence helpers
// ---------------------------------------------------------------------------

// fetchDynatraceWebhookEvidences fetches logs and traces from Dynatrace Grail
// for the given entity names within the specified time window.
// Errors are logged as warnings and do not stop processing.
func fetchDynatraceWebhookEvidences(sc *security.RequestContext, apiToken, baseURL string, entityNames []string, fromTs, toTs int64) []event.EventEvidence {
	var evidences []event.EventEvidence
	if len(entityNames) == 0 {
		return evidences
	}

	entityName := entityNames[0]
	from := time.UnixMilli(fromTs).UTC().Format(time.RFC3339)
	to := time.UnixMilli(toTs).UTC().Format(time.RFC3339)

	// Escape entity name to prevent DQL injection.
	escapedName := escapeDQLString(entityName)

	// Fetch logs — include host/node filters for host-level events (e.g. CPU saturation).
	logDQL := fmt.Sprintf(
		`fetch logs, from: "%s", to: "%s" | filter k8s.workload.name == "%s" or service.name == "%s" or k8s.pod.name == "%s" or k8s.node.name == "%s" or host.name == "%s" | sort timestamp desc | limit 100`,
		from, to, escapedName, escapedName, escapedName, escapedName, escapedName,
	)
	logRecords, logErr := runDynatraceDQL(baseURL, apiToken, logDQL)
	if logErr != nil {
		sc.GetLogger().Warn("dynatrace_webhook: failed to fetch logs", "error", logErr, "entity", entityName)
	} else if len(logRecords) > 0 {
		normalizedLogs := make([]map[string]any, 0, len(logRecords))
		for _, rec := range logRecords {
			normalizedLogs = append(normalizedLogs, normalizeDTLogRecord(rec))
		}
		evidences = append(evidences, event.EventEvidence{
			Type: "json",
			Data: map[string]any{
				"name": "Dynatrace Logs",
				"data": normalizedLogs,
			},
			Insight: []event.EventEvidenceInsight{
				{
					Message:  fmt.Sprintf("Fetched %d log entries for entity: %s", len(normalizedLogs), entityName),
					Severity: "info",
				},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "logs",
				"actual_action_name":     "logs",
				"action_title":           fmt.Sprintf("Dynatrace Logs: %s", entityName),
				"conditional_expression": "",
			},
		})
	}

	// Fetch traces (spans) — include node/host filters for infrastructure-level events.
	traceDQL := fmt.Sprintf(
		`fetch spans, from: "%s", to: "%s" | filter k8s.workload.name == "%s" or service.name == "%s" or k8s.node.name == "%s" or host.name == "%s" | sort start_time desc | limit 100`,
		from, to, escapedName, escapedName, escapedName, escapedName,
	)
	traceRecords, traceErr := runDynatraceDQL(baseURL, apiToken, traceDQL)
	if traceErr != nil {
		sc.GetLogger().Warn("dynatrace_webhook: failed to fetch traces", "error", traceErr, "entity", entityName)
	} else if len(traceRecords) > 0 {
		normalizedSpans := make([]map[string]any, 0, len(traceRecords))
		for _, rec := range traceRecords {
			normalizedSpans = append(normalizedSpans, normalizeDTSpanRecord(rec))
		}
		evidences = append(evidences, event.EventEvidence{
			Type: "json",
			Data: map[string]any{
				"name": "Dynatrace Traces",
				"data": normalizedSpans,
			},
			Insight: []event.EventEvidenceInsight{
				{
					Message:  fmt.Sprintf("Fetched %d spans for entity: %s", len(normalizedSpans), entityName),
					Severity: "info",
				},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "traces",
				"actual_action_name":     "traces",
				"action_title":           fmt.Sprintf("Dynatrace Traces: %s", entityName),
				"conditional_expression": "",
			},
		})
	}

	return evidences
}

// buildDynatraceOpenPipelineProblemEvidence creates structured JSON evidence for an OpenPipeline problem.
func buildDynatraceOpenPipelineProblemEvidence(payload DynatraceOpenPipelinePayload, details *DynatraceProblemDetails) event.EventEvidence {
	return event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name":    dtProblemDetailsTitle,
			"payload": payload,
			"api":     details,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Dynatrace Problem ID: %s", payload.DisplayID),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            dtProblemDetailsActionName,
			"actual_action_name":     dtProblemDetailsActionName,
			"action_title":           dtProblemDetailsTitle,
			"conditional_expression": "",
		},
	}
}

// buildDynatraceProblemEvidence creates structured JSON evidence for the problem itself.
func buildDynatraceProblemEvidence(payload DynatraceWebhookPayload, details *DynatraceProblemDetails) event.EventEvidence {
	return event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name":    dtProblemDetailsTitle,
			"payload": payload,
			"api":     details,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Dynatrace Problem ID: %s", payload.ProblemID),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            dtProblemDetailsActionName,
			"actual_action_name":     dtProblemDetailsActionName,
			"action_title":           dtProblemDetailsTitle,
			"conditional_expression": "",
		},
	}
}

// ---------------------------------------------------------------------------
// DQL record normalisers
// ---------------------------------------------------------------------------

// normalizeDTLogRecord converts a Grail DQL `fetch logs` record to the
// {timestamp, message, severity, labels} shape that SignozDatadogLogCard expects.
func normalizeDTLogRecord(rec map[string]any) map[string]any {
	skip := map[string]bool{
		"content": true, "message": true,
		"status": true, "loglevel": true, "severity": true,
		"timestamp": true,
	}
	labels := make(map[string]any, len(rec))
	for k, v := range rec {
		if !skip[k] {
			labels[k] = v
		}
	}
	return map[string]any{
		"timestamp": dtFirstString(rec, "timestamp"),
		"message":   dtFirstString(rec, "content", "message"),
		"severity":  dtFirstString(rec, "status", "loglevel", "severity"),
		"labels":    labels,
	}
}

// normalizeDTSpanRecord converts a Grail DQL `fetch spans` record to the
// TracesCard shape {trace_id, span_id, span_name, duration_ns, timestamp,
// ResourceAttributes, SpanAttributes}.
func normalizeDTSpanRecord(rec map[string]any) map[string]any {
	durationNs := int64(0)
	switch d := rec["duration"].(type) {
	case string:
		if parsed, err := strconv.ParseInt(d, 10, 64); err == nil {
			durationNs = parsed
		}
	case float64:
		durationNs = int64(d)
	case int64:
		durationNs = d
	}

	resourceAttrs := dtPickFields(rec, "k8s.namespace.name", "k8s.workload.name",
		"service.name", "host.name", "k8s.pod.name", "k8s.cluster.name")
	spanAttrs := dtPickFields(rec, "span.kind", "exception.type", "exception.message",
		"http.url", "http.method", "http.status_code", "db.statement")

	strField := func(keys ...string) string { return dtFirstString(rec, keys...) }
	return map[string]any{
		"trace_id":           strField("trace.id"),
		"span_id":            strField("span.id"),
		"parent_span_id":     strField("parent_span_id"),
		"span_name":          strField("span.name"),
		"timestamp":          strField("start_time", "timestamp"),
		"duration_ns":        durationNs,
		"status_code":        strField("span.status_code"),
		"status_message":     strField("span.status_message"),
		"ResourceAttributes": resourceAttrs,
		"SpanAttributes":     spanAttrs,
	}
}

// dtPickFields returns a map of the given keys that exist in rec.
func dtPickFields(rec map[string]any, keys ...string) map[string]any {
	m := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := rec[k]; ok {
			m[k] = v
		}
	}
	return m
}

// dtFirstString returns the first non-empty string value among the given keys.
func dtFirstString(rec map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := rec[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

// extractDynatraceEntityNames returns a deduplicated, prioritized list of entity names.
// RootCauseEntity (from API) is placed first when available; ImpactedEntities follow.
func extractDynatraceEntityNames(payload DynatraceWebhookPayload, details *DynatraceProblemDetails) []string {
	seen := make(map[string]bool)
	var names []string

	addName := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	// Root cause entity from API takes priority.
	if details != nil && details.RootCauseEntity != nil {
		addName(details.RootCauseEntity.Name)
	}

	// Impacted entities from API (more complete than webhook payload).
	if details != nil {
		for _, e := range details.ImpactedEntities {
			addName(e.Name)
		}
	}

	// Impacted entities from webhook payload (fallback when API unavailable).
	for _, e := range payload.ImpactedEntities {
		addName(e.Name)
	}

	return names
}

// parseDynatraceTags returns a deduplicated list of tag strings.
// Merges the comma-separated Tags webhook field with structured API tags.
func parseDynatraceTags(tagsString string, details *DynatraceProblemDetails) []string {
	seen := make(map[string]bool)
	var tags []string

	add := func(t string) {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}

	if tagsString != "" {
		for _, t := range strings.Split(tagsString, ",") {
			add(t)
		}
	}

	if details != nil {
		for _, t := range details.Tags {
			var tagStr string
			if t.Context != "" && t.Context != "CONTEXTLESS" {
				tagStr = fmt.Sprintf("%s:%s", t.Context, t.Key)
			} else {
				tagStr = t.Key
			}
			if t.Value != "" {
				tagStr = fmt.Sprintf("%s:%s", tagStr, t.Value)
			}
			add(tagStr)
		}
	}

	return tags
}

// escapeDQLString escapes special characters for safe use in DQL string literals.
// Prevents DQL injection by escaping backslashes first, then double-quotes,
// and stripping control chars that break DQL parsing.
func escapeDQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`) // must be first — prevent double-escaping
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// mapDynatraceOpenPipelineStatus maps an OpenPipeline event.status value to our internal event status.
func mapDynatraceOpenPipelineStatus(status string) string {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return string(event.EventStatusFiring)
	case "RESOLVED", "INACTIVE", "CLOSED":
		return string(event.EventStatusResolved)
	default:
		return strings.ToLower(status)
	}
}

// deduplicateStrings returns a deduplicated slice preserving first-seen order.
func deduplicateStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mapDynatraceStateToStatus maps a Dynatrace problem State/Status to our internal event status.
func mapDynatraceStateToStatus(state string) string {
	switch strings.ToUpper(state) {
	case "OPEN":
		return string(event.EventStatusFiring)
	case "RESOLVED":
		return string(event.EventStatusResolved)
	case "MERGED":
		// Problem merged into another — treat as resolved for this problem ID.
		return string(event.EventStatusResolved)
	default:
		return strings.ToLower(state)
	}
}

// mapDynatraceSeverity maps a Dynatrace severity level to our internal EventPriority.
func mapDynatraceSeverity(severity string) event.EventPriortiy {
	switch strings.ToUpper(severity) {
	case "AVAILABILITY", "ERROR":
		return event.EventPriortiyHigh
	case "PERFORMANCE", "RESOURCE_CONTENTION":
		return event.EventPriortiyMedium
	case "CUSTOM_ALERT":
		return event.EventPriortiyLow
	default:
		return event.EventPriortiyLow
	}
}

// ---------------------------------------------------------------------------
// DAVIS_EVENT processor
// ---------------------------------------------------------------------------

// processDavisEventPayload handles Dynatrace DAVIS_EVENT payloads from OpenPipeline.
// These are transient infrastructure events (process restarts, config changes, OOM kills, etc.)
// identified by event.kind == "DAVIS_EVENT". Unlike DAVIS_PROBLEM, they carry no display_id
// and use event.id as the fingerprint.
func (m DynatraceWebhook) processDavisEventPayload(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload DynatraceDavisEventPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("dynatrace_webhook: failed to unmarshal DAVIS_EVENT payload: %w", err)
	}

	// event.id is the only stable identifier for DAVIS_EVENTs.
	eventID := payload.EventID
	if eventID == "" {
		return nil, fmt.Errorf("dynatrace_webhook: missing event.id in DAVIS_EVENT payload")
	}

	// Try to get Dynatrace credentials for DQL enrichment.
	apiToken, baseURL, credErr := GetDynatraceConfigs(sc, accountId)
	if credErr != nil {
		sc.GetLogger().Warn("dynatrace_webhook: could not get Dynatrace credentials, skipping DQL enrichment",
			"account_id", accountId, "error", credErr)
	}

	// Best-effort DQL enrichment from dt.davis.events.
	var details *DynatraceDavisEventDetails
	if credErr == nil && baseURL != "" {
		var detailErr error
		details, detailErr = getDynatraceDavisEventDetails(baseURL, apiToken, eventID)
		if detailErr != nil {
			sc.GetLogger().Warn("dynatrace_webhook: failed to fetch DAVIS_EVENT details via DQL, using webhook payload",
				"event_id", eventID, "error", detailErr)
		}
	}

	// Merge: DQL result overrides payload where non-empty.
	title := payload.EventName
	if payload.EventGroupLabel != "" && title == "" {
		title = payload.EventGroupLabel
	}
	if title == "" {
		title = payload.EventType
	}
	description := payload.EventDescription
	eventType := payload.EventType

	if details != nil {
		if details.EventName != "" {
			title = details.EventName
		}
		if details.EventDescription != "" {
			description = details.EventDescription
		}
		if details.EventType != "" {
			eventType = details.EventType
		}
	}

	// Map event.status → internal EventStatus.
	status := mapDavisEventStatus(payload.EventStatus)

	// Parse timestamps: prefer DQL epoch ms, fall back to ISO 8601 payload fields.
	var createdAt time.Time
	var endsAt time.Time

	if details != nil && details.StartTime > 0 {
		createdAt = time.UnixMilli(details.StartTime).UTC()
	} else {
		startStr := payload.EventStart
		if startStr == "" {
			startStr = payload.Timestamp
		}
		if startStr != "" {
			var parseErr error
			createdAt, parseErr = time.Parse(time.RFC3339Nano, startStr)
			if parseErr != nil {
				createdAt, parseErr = time.Parse(time.RFC3339, startStr)
				if parseErr != nil {
					sc.GetLogger().Warn("dynatrace_webhook: failed to parse DAVIS_EVENT start time, using time.Now()",
						"event_start", startStr, "error", parseErr)
					createdAt = time.Now()
				}
			}
		} else {
			createdAt = time.Now()
		}
	}

	if details != nil && details.EndTime > 0 {
		endsAt = time.UnixMilli(details.EndTime).UTC()
	} else if payload.EventEnd != "" {
		if t, err := time.Parse(time.RFC3339Nano, payload.EventEnd); err == nil {
			endsAt = t.UTC()
		} else if t, err := time.Parse(time.RFC3339, payload.EventEnd); err == nil {
			endsAt = t.UTC()
		} else {
			sc.GetLogger().Warn("dynatrace_webhook: failed to parse DAVIS_EVENT end time, endsAt left as zero",
				"event_end", payload.EventEnd)
		}
	}

	// Extract entity names for workload matching and evidence.
	entityNames := extractDavisEventEntityNames(payload, details)

	// Map severity from event category and specific event type.
	priority := mapDavisEventSeverity(payload.EventCategory, eventType)

	// Build labels.
	labels := buildDavisEventLabels(payload, entityNames)
	if len(entityNames) > 0 {
		// Hand the candidate list to core.MatchWorkloadAndEnrich for fallback
		// matching after EventSubjectName.
		labels["nb_workload_candidates"] = strings.Join(entityNames, ",")
	}

	// EventSubjectName seeds central workload match. core enrichment overwrites
	// subject fields (kind, cloud_resource_id, owner kind) on a successful match.
	subjectName := ""
	if len(entityNames) > 0 {
		subjectName = entityNames[0]
	}

	// Build evidences.
	evidences := []event.EventEvidence{buildDavisEventEvidence(payload, details)}

	// Synthesize description if empty.
	if description == "" {
		description = fmt.Sprintf("Dynatrace Event %s: %s (Category: %s, Type: %s)",
			eventID, title, payload.EventCategory, eventType)
	}

	// Build deep-link URL to the Dynatrace Davis AI events screen.
	var eventURL string
	if baseURL != "" {
		eventURL = fmt.Sprintf("%s/ui/davis-ai/events/%s", strings.TrimRight(baseURL, "/"), eventID)
	}

	// Build event tags from event type and category for quick filtering.
	var eventTags []string
	if eventType != "" {
		eventTags = append(eventTags, eventType)
	}
	if payload.EventCategory != "" {
		eventTags = append(eventTags, payload.EventCategory)
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      eventID,
		Fingerprint: eventID,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Subject kind / cloud_resource_id / owner kind are filled in by
	// core.MatchWorkloadAndEnrich on a successful match.
	webhookEvent := core.EventIncomingWebhook{
		WebhookId:         eventID,
		EventType:         "dynatrace_event",
		EventId:           eventID,
		EventUrl:          eventURL,
		EventStatus:       status,
		EventPriority:     string(priority),
		EventCreatedAt:    createdAt,
		EventEndsAt:       endsAt,
		EventTitle:        title,
		EventDescription:  description,
		EventTags:         eventTags,
		Investigation:     investigation,
		EventSubjectName:  subjectName,
		AccountId:         accountId,
		EventSubjectOwner: subjectName,
	}

	// Upsert event rule for rule management UI visibility — skip for resolved events.
	if status != string(event.EventStatusResolved) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			detachedSc := security.NewRequestContext(ctx, sc.GetSecurityContext(), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
			defer func() {
				if r := recover(); r != nil {
					detachedSc.GetLogger().Error("dynatrace_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			severityLabel := strings.ToLower(string(priority))
			if severityLabel == "" {
				severityLabel = "warning"
			}
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: description,
					Summary:     title,
					Runbook:     "",
				},
				Expr: func() string {
					if eventType != "" {
						return eventType
					}
					return title // fallback: use title when event type is absent
				}(),
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severityLabel},
				Alert:         title,
				Duration:      "0",
				AccountID:     accountId,
				Source:        "dynatrace_webhook",
				Category:      "alert",
				Severity:      severityLabel,
				Enabled:       true,
				TriggerParams: []map[string]any{},
				ActionParams:  []map[string]any{},
			}
			if _, err := eventrule.CreateEventRule(detachedSc, eventReq); err != nil {
				detachedSc.GetLogger().Error("dynatrace_webhook: CreateEventRule failed for DAVIS_EVENT", "error", err)
			}
		}()
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// ---------------------------------------------------------------------------
// DAVIS_EVENT helper functions
// ---------------------------------------------------------------------------

// mapDavisEventStatus maps a DAVIS_EVENT event.status value to our internal EventStatus.
// DAVIS_EVENTs use OPEN/CLOSED rather than ACTIVE/RESOLVED.
func mapDavisEventStatus(eventStatus string) string {
	switch strings.ToUpper(eventStatus) {
	case "OPEN", "ACTIVE":
		return string(event.EventStatusFiring)
	case "CLOSED", "RESOLVED", "INACTIVE":
		return string(event.EventStatusResolved)
	default:
		return strings.ToLower(eventStatus)
	}
}

// mapDavisEventSeverity maps a DAVIS_EVENT category and specific type to our internal EventPriority.
// Event type is checked first for precision; category is the fallback.
func mapDavisEventSeverity(category, eventType string) event.EventPriortiy {
	switch strings.ToUpper(eventType) {
	case "PROCESS_CRASH", "OOM_KILL", "APPLICATION_UNEXPECTED_HIGH_LOAD":
		return event.EventPriortiyHigh
	case "PROCESS_RESTART", "HIGH_CPU", "HIGH_MEMORY", "HIGH_NETWORK":
		return event.EventPriortiyMedium
	case "CONFIG_CHANGE", "DEPLOYMENT", "MARKED_FOR_TERMINATION":
		return event.EventPriortiyLow
	}
	switch strings.ToUpper(category) {
	case "AVAILABILITY", "ERROR":
		return event.EventPriortiyHigh
	case "PERFORMANCE", "RESOURCE_CONTENTION":
		return event.EventPriortiyMedium
	case "INFO", "CUSTOM_ALERT":
		return event.EventPriortiyLow
	}
	return event.EventPriortiyLow
}

// extractDavisEventEntityNames returns a deduplicated, prioritized list of entity names
// from a DAVIS_EVENT payload. DQL-enriched names take priority when available.
// Priority order: DQL AffectedEntityNames → ProcessGroupInstance.name →
// ProcessGroup.name → K8sNodeName → HostName → DtEntityHostName
func extractDavisEventEntityNames(payload DynatraceDavisEventPayload, details *DynatraceDavisEventDetails) []string {
	seen := make(map[string]bool)
	var names []string

	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	// DQL-enriched names are most accurate.
	if details != nil {
		for _, n := range details.AffectedEntityNames {
			add(n)
		}
	}

	// Resolved entity display names from the payload.
	add(payload.DtEntityProcessGroupInstanceName)
	add(payload.DtEntityProcessGroupName)
	add(payload.K8sNodeName)
	add(payload.HostName)
	add(payload.DtEntityHostName)

	return names
}

// buildDavisEventLabels builds the labels map for a DAVIS_EVENT webhook event.
func buildDavisEventLabels(payload DynatraceDavisEventPayload, entityNames []string) map[string]string {
	labels := make(map[string]string)

	setLabel := func(key, value string) {
		if value != "" {
			labels[key] = value
		}
	}
	setSliceLabel := func(key string, values StringOrSlice) {
		if len(values) > 0 {
			labels[key] = strings.Join(values, ",")
		}
	}

	setLabel("event_type", payload.EventType)
	setLabel("event_category", payload.EventCategory)
	setLabel("event_provider", payload.EventProvider)
	setLabel("event_status", payload.EventStatus)
	setLabel("event_status_transition", payload.EventStatusTransition)
	setLabel("event_group_label", payload.EventGroupLabel)
	setLabel("event_severity", string(payload.EventSeverity))
	setLabel("event_id", payload.EventID)
	setLabel("event_start", payload.EventStart)
	setLabel("event_end", payload.EventEnd)

	setLabel("dt_source_entity", payload.DtSourceEntity)
	setLabel("dt_source_entity_type", payload.DtSourceEntityType)

	setLabel("k8s_cluster_name", payload.K8sClusterName)
	setLabel("k8s_cluster_uid", payload.K8sClusterUID)
	setLabel("k8s_namespace_name", payload.K8sNamespaceName)
	setLabel("k8s_node_name", payload.K8sNodeName)
	setLabel("host_name", payload.HostName)

	setLabel("gcp_project_id", payload.GCPProjectID)
	setLabel("gcp_region", payload.GCPRegion)
	setLabel("gcp_zone", payload.GCPZone)
	setLabel("gcp_instance_id", payload.GCPInstanceID)
	setLabel("gcp_resource_name", payload.GCPResourceName)

	setLabel("dt_davis_impact_level", payload.DavisImpactLevel)
	setLabel("dt_mute_status", payload.DavisMuteStatus)
	setLabel("dt_davis_timeout", payload.DavisTimeout)
	if payload.DavisIsFrequentEvent {
		labels["dt_is_frequent"] = "true"
	}
	if payload.UnderMaintenance {
		labels["under_maintenance"] = "true"
	}

	setLabel("dt_entity_host", payload.DtEntityHost)
	setLabel("dt_entity_kubernetes_cluster", payload.DtEntityKubernetesCluster)
	setLabel("dt_entity_process_group", payload.DtEntityProcessGroup)
	setLabel("dt_entity_process_group_instance", payload.DtEntityProcessGroupInstance)
	setLabel("dt_entity_gcp_zone", payload.DtEntityGCPZone)
	setLabel("dt_entity_host_name", payload.DtEntityHostName)
	setLabel("dt_entity_kubernetes_cluster_name", payload.DtEntityKubernetesClusterName)
	setLabel("dt_entity_process_group_name", payload.DtEntityProcessGroupName)
	setLabel("dt_entity_process_group_instance_name", payload.DtEntityProcessGroupInstanceName)

	setLabel("dt_smartscape_k8s_cluster", payload.DtSmartscapeK8sCluster)
	setLabel("dt_smartscape_k8s_node", payload.DtSmartscapeK8sNode)
	setLabel("dt_smartscape_host", payload.DtSmartscapeHost)
	setLabel("dt_smartscape_process", payload.DtSmartscapeProcess)

	setSliceLabel("affected_entity_ids", payload.AffectedEntityIDs)
	setSliceLabel("affected_entity_types", payload.AffectedEntityTypes)
	setSliceLabel("related_entity_ids", payload.RelatedEntityIDs)
	setSliceLabel("smartscape_related_entity_ids", payload.SmartscapeRelatedEntityIDs)
	setSliceLabel("smartscape_related_entity_types", payload.SmartscapeRelatedEntityTypes)

	setLabel("dt_openpipeline_source", payload.OpenpipelineSource)
	setSliceLabel("dt_openpipeline_pipelines", payload.OpenpipelinePipelines)

	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
		labels["service"] = entityNames[0]
	}

	return labels
}

// buildDavisEventEvidence creates structured JSON evidence for a DAVIS_EVENT.
func buildDavisEventEvidence(payload DynatraceDavisEventPayload, details *DynatraceDavisEventDetails) event.EventEvidence {
	return event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name":    dtDavisEventTitle,
			"payload": payload,
			"dql":     details,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Dynatrace Event ID: %s (Type: %s)", payload.EventID, payload.EventType),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            dtDavisEventActionName,
			"actual_action_name":     dtDavisEventActionName,
			"action_title":           dtDavisEventTitle,
			"conditional_expression": "",
		},
	}
}
