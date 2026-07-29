package integrations

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type IncidentResponse struct {
	Incident Incident `json:"incident"`
}

type PagerDutyAlert struct {
	ID   string `json:"id"`
	Body Body   `json:"body"`
}

type IncidentAlertsResponse struct {
	Alerts []PagerDutyAlert `json:"alerts"`
}

// Incident holds the detailed information about a PagerDuty incident.
// Note: This struct includes common fields. You can add more fields
// from the API documentation as needed.
type Incident struct {
	ID                   string            `json:"id"`
	IncidentNumber       int               `json:"incident_number"`
	Title                string            `json:"title"`
	Summary              string            `json:"summary"`
	Description          string            `json:"description"`
	CreatedAt            string            `json:"created_at"`
	UpdatedAt            string            `json:"updated_at"`
	ResolvedAt           string            `json:"resolved_at"`
	LastStatusChangeAt   string            `json:"last_status_change_at"`
	Status               string            `json:"status"`
	Urgency              string            `json:"urgency"`
	HTMLURL              string            `json:"html_url"`
	Self                 string            `json:"self"`
	Type                 string            `json:"type"`
	IncidentKey          string            `json:"incident_key"`
	Service              APIObject         `json:"service"`
	Assignments          []Assignment      `json:"assignments"`
	Acknowledgements     []Acknowledgement `json:"acknowledgements"`
	LastStatusChangeBy   APIObject         `json:"last_status_change_by"`
	Teams                []APIObject       `json:"teams"`
	EscalationPolicy     APIObject         `json:"escalation_policy"`
	Priority             Priority          `json:"priority"`
	Body                 Body              `json:"body"`
	CustomFields         []CustomField     `json:"custom_fields"`
	AlertCounts          AlertCounts       `json:"alert_counts"`
	ImpactedServices     []APIObject       `json:"impacted_services"`
	IncidentsResponders  []APIObject       `json:"incidents_responders"`
	AssignedVia          string            `json:"assigned_via"`
	PendingActions       []APIObject       `json:"pending_actions"`
	ResponderRequests    []APIObject       `json:"responder_requests"`
	SubscriberRequests   []APIObject       `json:"subscriber_requests"`
	FirstTriggerLogEntry APIObject         `json:"first_trigger_log_entry"`
	ResolveReason        interface{}       `json:"resolve_reason"`
	IsMergeable          bool              `json:"is_mergeable"`
	IncidentType         IncidentType      `json:"incident_type"`
	AlertGrouping        interface{}       `json:"alert_grouping"`
	BasicAlertGrouping   interface{}       `json:"basic_alert_grouping"`
}

// APIObject is a generic struct for linked PagerDuty objects like users, services, etc.
type APIObject struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
	Self    string `json:"self"`
	HTMLURL string `json:"html_url"`
}

// Assignment represents an assignment of an incident to a user.
type Assignment struct {
	At       string    `json:"at"`
	Assignee APIObject `json:"assignee"`
}

// Acknowledgement represents an acknowledgement of an incident by a user.
type Acknowledgement struct {
	At           string    `json:"at"`
	Acknowledger APIObject `json:"acknowledger"`
}

// Body contains the detailed content of the incident.
type Body struct {
	Type       string      `json:"type"`
	Details    interface{} `json:"details"`
	CefDetails interface{} `json:"cef_details,omitempty"`
}

// GetBodyDetails safely extracts BodyDetails from the Details interface{}.
// Returns nil if Details cannot be converted to BodyDetails structure.
func (b *Body) GetBodyDetails() *BodyDetails {
	if b.Details == nil {
		return nil
	}

	// First, convert to map[string]interface{} if it's not already
	var detailsMap map[string]interface{}
	switch v := b.Details.(type) {
	case map[string]interface{}:
		detailsMap = v
	case string:
		// If it's a string, we can't convert it to BodyDetails
		return nil
	default:
		return nil
	}

	// Convert map to BodyDetails using JSON marshaling/unmarshaling
	jsonBytes, err := json.Marshal(detailsMap)
	if err != nil {
		return nil
	}

	var bodyDetails BodyDetails
	if err := json.Unmarshal(jsonBytes, &bodyDetails); err != nil {
		return nil
	}

	return &bodyDetails
}

// getStringDetailsFromBody extracts string content from body details when it's in string format
func getStringDetailsFromBody(incident *Incident) string {
	if incident == nil {
		return ""
	}

	// Check if details is a string. This also handles the case where incident.Body.Details is nil.
	if stringDetails, ok := incident.Body.Details.(string); ok {
		return stringDetails
	}

	return ""
}

// applyStringDetailsSubjectLabels mines the declared subject from an unstructured
// markdown body.details string ("**Subject Name**: <pod>", "**Subject Namespace**:
// <ns>", "**Subject Type**: <kind>") into structured labels, without overwriting
// labels already populated by extractBodyDetails. PagerDuty delivers this shape for
// rendered alerts that carry no __pd_cef_payload map (e.g. NudgeBee "Investigate
// Event -" re-raises), which extractBodyDetails classifies AlertSourceUnknown and
// leaves without subject labels. resolveSubjectFromLabels then keys off subject_name/
// subject_namespace/subject_type. No-op unless a Subject Name is present.
func applyStringDetailsSubjectLabels(stringDetails string, labels map[string]string) {
	if stringDetails == "" || labels == nil {
		return
	}
	name := extractMarkdownField(stringDetails, "Subject Name")
	if name == "" {
		return
	}
	if labels["subject_name"] == "" {
		labels["subject_name"] = name
	}
	if ns := extractMarkdownField(stringDetails, "Subject Namespace"); ns != "" && labels["subject_namespace"] == "" {
		labels["subject_namespace"] = ns
	}
	if st := extractMarkdownField(stringDetails, "Subject Type"); st != "" && labels["subject_type"] == "" {
		labels["subject_type"] = strings.ToLower(st)
	}
}

const (
	// cloudOptimizationSubjectType marks a subject that is a cloud resource rather
	// than a k8s object. Only needs to be non-pod (see
	// applyCloudOptimizationSubjectLabels); linkCloudResourceId treats any non-pod
	// type as a workload name, misses k8s_workloads, and no-ops — the same path
	// cloudsql_database already takes.
	cloudOptimizationSubjectType = "cloud-resource"
	// cloudOptimizationSubjectMatch is the nb_subject_match value for subjects mined
	// out of a Cloud Optimization body. Also gates auto-learn, whose title → subject
	// mapping is invalid for this alert family.
	cloudOptimizationSubjectMatch = "cloud_optimization_details"
)

// extractPlainField reads a `<key>: <value>` line out of an unstructured body.details
// string. Anchored to the start of a line, unlike extractMarkdownField's substring
// search: the Cloud Optimization block ends with a free-text `Details:` line whose
// prose can contain any of these key names, and a substring match would read the
// value out of the middle of that sentence.
func extractPlainField(text, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if val := strings.TrimSpace(strings.TrimPrefix(line, prefix)); val != "" {
			return val
		}
	}
	return ""
}

// applyCloudOptimizationSubjectLabels mines the subject from a NudgeBee Cloud
// Optimization body.details string:
//
//	Recommendation: aws_ec2_orphaned_volume
//	Service: AmazonEC2
//	Instance: vol-060625d190b54ab95
//	Severity: Medium
//
// The frontend raises these incidents itself (getTicketDescription in
// app/src/components/cloudaccount/common.tsx) and PagerDuty hands the description
// back as an unstructured string, so there is no __pd_cef_payload map: GetBodyDetails
// returns nil, detectAlertSource says AlertSourceUnknown, extractGenericAlertFields
// yields nothing, and applyStringDetailsSubjectLabels does not match because these
// are bare `key: value` lines rather than **bold** markdown fields. The alert then
// reached the LLM, which answered not_found for a resource it cannot know about
// (observed: incident Q03KT7QRZ82X20). `Instance` is the recommendation's own
// resource id, so this is fully deterministic — no LLM.
//
// subject_type is a deliberate constant: `Service` names the AWS service of the
// recommendation *catalog entry* (AmazonEC2 covers both instances and volumes), so
// it cannot be trusted as the resource kind, and the string carries no other kind
// signal. Any non-pod value is enough to stop resolveSubjectFromLabels fabricating
// a `pod=<volume-id>` label, which would point the pod metric/log enrichers at a
// pod that does not exist.
//
// Runs before resolveSubjectFromLabels, which keys off subject_name/subject_type.
// No-op unless the details carry both a Recommendation and a usable Instance.
func applyCloudOptimizationSubjectLabels(stringDetails string, labels map[string]string) {
	if stringDetails == "" || labels == nil {
		return
	}
	// `Recommendation` is the format discriminator: it is the first line of every
	// Cloud Optimization description and appears in no other PagerDuty body shape.
	// Only its presence matters — the rule name itself is not retained.
	if extractPlainField(stringDetails, "Recommendation") == "" {
		return
	}
	// objectName falls back to the literal "N/A" when the recommendation carries no
	// resolvable resource, which must never become a subject.
	instance := extractPlainField(stringDetails, "Instance")
	if instance == "" || strings.EqualFold(instance, "N/A") {
		return
	}
	if labels["subject_name"] != "" {
		return
	}
	labels["subject_name"] = instance
	if labels["subject_type"] == "" {
		labels["subject_type"] = cloudOptimizationSubjectType
	}
	labels["nb_subject_match"] = cloudOptimizationSubjectMatch
	labels["nb_subject_resolution"] = "cloud-optimization"
}

// BodyDetails contains the nested details structure from PagerDuty
type BodyDetails struct {
	PdCefPayload   PdCefPayload   `json:"__pd_cef_payload"`
	Client         string         `json:"client"`
	ClientURL      string         `json:"client_url"`
	Description    string         `json:"description"`
	EventType      string         `json:"event_type"`
	IncidentKey    string         `json:"incident_key"`
	ServiceKey     string         `json:"service_key"`
	Contexts       []Context      `json:"contexts"`
	DedupKey       string         `json:"dedup_key"`
	Details        PayloadDetails `json:"details"`
	Firing         string         `json:"firing"`       // Root-level firing text (flat body format)
	NumFiring      string         `json:"num_firing"`   // Root-level num_firing (flat body format)
	NumResolved    string         `json:"num_resolved"` // Root-level num_resolved (flat body format)
	EreAccountID   int            `json:"ere_account_id"`
	EventAction    string         `json:"event_action"`
	EventStorageID string         `json:"event_storage_id"`
	Payload        PayloadSection `json:"payload"`
	RoutingKey     string         `json:"routing_key"`
	Severity       string         `json:"severity"`
}

// PdCefPayload contains the CEF payload details
type PdCefPayload struct {
	Client          string                 `json:"client"`
	ClientURL       string                 `json:"client_url"`
	Description     string                 `json:"description"`
	DedupKey        string                 `json:"dedup_key"`
	EventAction     string                 `json:"event_action"`
	EventClass      string                 `json:"event_class"`
	EventID         string                 `json:"event_id"`
	Message         string                 `json:"message"`
	RoutingKey      string                 `json:"routing_key"`
	Severity        string                 `json:"severity"`
	SourceComponent string                 `json:"source_component"`
	SourceOrigin    string                 `json:"source_origin"`
	ServiceGroup    string                 `json:"service_group"`
	CreationTime    string                 `json:"creation_time"`
	Details         map[string]interface{} `json:"details"`
	Contexts        []Context              `json:"contexts"`
}

// Context represents a context link in PagerDuty
type Context struct {
	Href string `json:"href"`
	Text string `json:"text"`
	Type string `json:"type"`
}

// PayloadDetails contains the details section with firing information
type PayloadDetails struct {
	Firing      string `json:"firing"`
	NumFiring   string `json:"num_firing"`
	NumResolved string `json:"num_resolved"`
	Resolved    string `json:"resolved"`
}

// PayloadSection contains the payload section from PagerDuty
type PayloadSection struct {
	CustomDetails PayloadDetails `json:"custom_details"`
	Severity      string         `json:"severity"`
	Source        string         `json:"source"`
	Summary       string         `json:"summary"`
}

// Priority represents the priority level of an incident with detailed information.
type Priority struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Summary       string `json:"summary"`
	Self          string `json:"self"`
	HTMLURL       string `json:"html_url"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Color         string `json:"color"`
	Order         int    `json:"order"`
	AccountID     string `json:"account_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	SchemaVersion int    `json:"schema_version"`
}

// CustomField represents a custom field in a PagerDuty incident.
type CustomField struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	DisplayName string      `json:"display_name"`
	Description string      `json:"description"`
	Type        string      `json:"type"`
	FieldType   string      `json:"field_type"`
	DataType    string      `json:"data_type"`
	Value       interface{} `json:"value"`
	Enabled     bool        `json:"enabled"`
}

// AlertCounts represents the count of alerts in different states.
type AlertCounts struct {
	All       int `json:"all"`
	Resolved  int `json:"resolved"`
	Triggered int `json:"triggered"`
}

// IncidentType represents the type of incident.
type IncidentType struct {
	Name string `json:"name"`
}

func init() {
	core.RegisterIntegration(PagerDutyWebhook{})
}

type PagerDutyWebhook struct {
}

const IntegrationPagerdutyWebhook = "pagerduty_webhook"

func (m PagerDutyWebhook) Name() string {
	return IntegrationPagerdutyWebhook
}

func (m PagerDutyWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m PagerDutyWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of PagerDuty Webhook",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         70,
			},
		},
	}
}

func (m PagerDutyWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m PagerDutyWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

var (
	reNamespace   = regexp.MustCompile(`namespace=\"([^\"]+)\"`)
	rePod         = regexp.MustCompile(`pod=\"([^\"]+)\"`)
	reServiceName = regexp.MustCompile(`service_name=\"([^\"]+)\"`)
	// /k8s/<namespace>/<pod>/<workload-or-container> — Alertmanager embeds this in
	// the incident title. The 4th segment (the workload/container name) is optional
	// and, when present, is the resolvable subject; the pod carries a ReplicaSet hash.
	reK8sPath = regexp.MustCompile(`/k8s/([^/\s]+)/([^/\s]+)(?:/([^/\s]+))?`)

	// GCP Cloud Monitoring alert-title parsing (for alerts routed to PagerDuty).
	// The `for <project> <resource…>` clause runs up to the metric-labels, threshold,
	// or metric-absence boundary; the project id is read from the View-incident URL.
	reGCPMonitoringFor = regexp.MustCompile(`(?i)\bfor\s+(.+?)\s+(?:with metric labels\b|is (?:above|below) the threshold\b|is absent\b)`)
	reGCPProject       = regexp.MustCompile(`[?&]project=([A-Za-z0-9:_.-]+)`)
)

func (m PagerDutyWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	err := common.UnmarshalJson([]byte(webhookPayloadString), &payload)
	if err != nil {
		return []core.EventIncomingWebhook{}, err
	}
	if payload["event"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event not found")
	}
	parsedPayload := core.EventIncomingWebhook{}

	eventMap := payload["event"].(map[string]any)

	if eventMap["event_type"] == nil || (eventMap["event_type"] != "incident.resolved" && eventMap["event_type"] != "incident.triggered") {
		return nil, core.ErrEventNotSupported
	}

	if eventMap["id"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.id not found")
	}
	parsedPayload.WebhookId = eventMap["id"].(string)

	if eventMap["data"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data not found")
	}

	htmlURL := ""
	if payloadAgent, ok := eventMap["agent"].(map[string]any); ok {
		if url, ok := payloadAgent["html_url"].(string); ok && url != "" {
			htmlURL = url
		}
	} else {
		if payloadClient, ok := eventMap["client"].(map[string]any); ok {
			if clientURL, ok := payloadClient["url"].(string); ok && clientURL != "" {
				htmlURL = clientURL
			}
		}
	}

	payloadData := eventMap["data"].(map[string]any)

	if htmlURL == "" && payloadData["html_url"] != nil {
		htmlURL = payloadData["html_url"].(string)
	}

	if payloadData["type"] != "incident" {
		sc.GetLogger().Error("pagerdutywebhook, payload is not an incident", "payload", webhookPayloadString)
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, only incident is supported for event.data.type")
	}

	if payloadData["id"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data.id not found")
	}
	parsedPayload.EventId = payloadData["id"].(string)

	if payloadData["title"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data.title not found")
	}
	parsedPayload.EventTitle = payloadData["title"].(string)

	var namespace, pod string
	if match := reNamespace.FindStringSubmatch(parsedPayload.EventTitle); len(match) == 2 {
		namespace = match[1]
	}
	if match := rePod.FindStringSubmatch(parsedPayload.EventTitle); len(match) == 2 {
		pod = match[1]
	} else if match := reServiceName.FindStringSubmatch(parsedPayload.EventTitle); len(match) == 2 {
		pod = match[1]
	}
	parsedPayload.EventSubjectName = pod
	parsedPayload.EventSubjectNamespace = namespace

	if payloadData["description"] != nil {
		parsedPayload.EventDescription = payloadData["description"].(string)
	}

	if payloadData["created_at"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data.created_at not found")
	}

	parsedTime, err := time.Parse("2006-01-02T15:04:05Z", payloadData["created_at"].(string))
	if err != nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data.created_at not valid - " + payloadData["created_at"].(string))
	}
	parsedPayload.EventCreatedAt = parsedTime

	if payloadData["priority"] != nil {
		if ps, ok := payloadData["priority"].(string); ok {
			parsedPayload.EventPriority = ps
		}
	} else if payloadData["urgency"] != nil {
		if ps, ok := payloadData["urgency"].(string); ok {
			parsedPayload.EventPriority = ps
		}
	}

	if payloadData["status"] == nil {
		return []core.EventIncomingWebhook{}, errors.New("pagerdutywebhook: invalid payload, event.data.status not found")
	}
	parsedPayload.EventStatus = payloadData["status"].(string)

	parsedPayload.EventType = payloadData["type"].(string)
	parsedPayload.EventUrl = payloadData["self"].(string)

	parsedPayload.EventTags = []string{}

	eventClient := map[string]any{}
	if eventMap["client"] != nil {
		eventClient = eventMap["client"].(map[string]any)
	}

	alert := core.EventIncomingWebhookInvestigation{}
	alert.SourceUrl = payloadData["html_url"].(string)

	alertRuleTypeSource := "url"
	if value, found := core.GetSettingValue(settings, "ruletype_source"); found {
		alertRuleTypeSource = value
	}

	if alertRuleTypeSource == "url" {
		if urlStr, ok := eventClient["url"].(string); ok && urlStr != "" {
			parsedURL, err := url.Parse(urlStr)
			if err != nil {
				sc.GetLogger().Error("pagerdutywebhook: failed to parse event URL", "url", urlStr, "error", err)
				return []core.EventIncomingWebhook{parsedPayload}, nil
			}

			// Decode query parameters into a map
			queryParams := parsedURL.Query()
			if strings.Contains(urlStr, "last9.io") {
				labelSet := queryParams.Get("label_set")
				if labelSet != "" {
					labelPairs := strings.Split(labelSet, ",")
					alert.Labels = make(map[string]string) // Initialize the Labels map
					for _, pair := range labelPairs {
						kv := strings.SplitN(pair, "=", 2)
						if len(kv) == 2 {
							key := strings.TrimSpace(kv[0])
							value := strings.Trim(strings.TrimSpace(kv[1]), `"`) // Remove extra quotes
							alert.Labels[key] = value
						}
					}
				}
				// Add additional fields to labels
				alert.RuleId = queryParams.Get("rule_id")
				alert.RuleName = queryParams.Get("rule_name")
				alert.RuleType = queryParams.Get("rule_type")
				alert.SourceUrl = urlStr
				alert.Severity = event.EventPriorityHigh
				alert.Fingerprint = queryParams.Get("alert_hash")

				if parsedPayload.EventStatus == "triggered" {
					alert.Status = event.EventStatusFiring
				} else {
					alert.Status = event.EventStatusResolved
				}
			} else if strings.Contains(urlStr, "chronosphere.io") {
				alert.RuleId = strings.TrimPrefix(parsedURL.Path, "/monitors/")
				alert.RuleName = alert.RuleId
				alert.SourceUrl = urlStr
				alert.Severity = event.EventPriorityHigh
				alert.Fingerprint = base64.StdEncoding.EncodeToString([]byte(queryParams.Get("signal")))
				alert.RuleType = "static_threshold"
				alert.Labels = make(map[string]string)
				if queryParams.Get("signal") != "" {
					signalMap := map[string]any{}
					err := common.UnmarshalJson([]byte(queryParams.Get("signal")), &signalMap)
					if err != nil {
						sc.GetLogger().Error("pagerdutywebhook: failed to parse signal", "signal", queryParams.Get("signal"))
					}
					for k, v := range signalMap {
						alert.Labels[k] = fmt.Sprintf("%v", v)
					}
				}
				if queryParams.Get("start") != "" {
					alert.Labels["start"] = queryParams.Get("start")
				}
				if queryParams.Get("end") != "" {
					alert.Labels["end"] = queryParams.Get("end")
				}
				if queryParams.Get("receiver") != "" {
					alert.Labels["receiver"] = queryParams.Get("receiver")
				}
				if queryParams.Get("receiver-type") != "" {
					alert.Labels["receiver-type"] = queryParams.Get("receiver-type")
				}
				if queryParams.Get("status") != "" {
					alert.Labels["status"] = queryParams.Get("status")
				}
			} else {
				alert.Labels = make(map[string]string)
				for k, v := range queryParams {
					if len(v) > 0 {
						alert.Labels[k] = v[0]
					}
				}
			}
		}
	}

	if parsedPayload.EventDescription == "" {
		name := eventClient["name"]
		url := eventClient["url"]
		if name == nil {
			name = ""
		} else {
			name = name.(string)
		}
		if url == nil {
			url = ""
		} else {
			url = url.(string)
		}

		parsedPayload.EventDescription = fmt.Sprintf("**Agent URL -** %s \n **Client -** %s \n **Client URL -** %s", htmlURL, name, url)
	}

	// A stable fingerprint (last9 alert_hash / Chronosphere signal above, or the
	// CEF dedup_key applied during enrichment below) is preferred. When none is
	// available we deliberately leave it empty here and derive a canonical one
	// from the alert's identity after subject resolution — hashing the raw
	// per-delivery payload would give every firing of the same alert a new
	// fingerprint and fragment it into separate occurrence chains.

	// ruleIdFromService records that RuleId was filled from the PagerDuty service
	// display name (a generic fallback), not a real alert identity. The canonical
	// fingerprint below uses this so subject-less alerts from one service don't all
	// collapse into a single occurrence chain via the shared service name.
	ruleIdFromService := false
	if alert.RuleId == "" {
		if payloadData["service"] != nil {
			if payloadService, ok := payloadData["service"].(map[string]any); ok {
				if payloadService["summary"] != nil {
					alert.RuleId = payloadService["summary"].(string)
					ruleIdFromService = true
					if alert.RuleName == "" {
						alert.RuleName = alert.RuleId
					}
				}
			}
		}
	}

	if alert.RuleType == "" {
		alert.RuleType = "static_threshold"
	}

	if alert.Status == "" {
		alert.Status = event.EventStatusFiring
	}

	parsedPayload.Investigation = alert

	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = make(map[string]string)
	}

	// set service_discovery from payload service summary
	if payloadData["service"] != nil {
		if payloadService, ok := payloadData["service"].(map[string]any); ok {
			if summary, ok := payloadService["summary"].(string); ok && summary != "" {
				parsedPayload.Investigation.Labels["service_discovery"] = summary
			}
		}
	}

	// recheck time.
	if parsedPayload.Investigation.Labels["start"] != "" {
		t, err := common.ParseTimeValue(parsedPayload.Investigation.Labels["start"])
		if err == nil {
			parsedPayload.EventCreatedAt = t
		}
	}
	if parsedPayload.Investigation.Labels["end"] != "" {
		t, err := common.ParseTimeValue(parsedPayload.Investigation.Labels["end"])
		if err == nil {
			parsedPayload.EventEndsAt = t
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to process request", "error", err)
		return []core.EventIncomingWebhook{parsedPayload}, nil
	}

	//lookup DB for token — use ORDER BY + LIMIT 1 for deterministic selection
	// when multiple pagerduty integrations exist (picks most recently updated)
	var username, encryptedPassword string
	err = dbms.Db.QueryRowx(`
		SELECT
			COALESCE(MAX(CASE WHEN icv.name = 'username' THEN icv.value END), '') as username,
			COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') as password
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.status = 'enabled' AND i.type = 'pagerduty' AND i.tenant_id = $1
		GROUP BY i.id
		ORDER BY i.updated_at DESC
		LIMIT 1
	`, sc.GetSecurityContext().GetTenantId()).Scan(&username, &encryptedPassword)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		sc.GetLogger().Error("integrations: unable to query pagerduty integration", "error", err)
		return []core.EventIncomingWebhook{parsedPayload}, nil
	}

	// Enrich with PagerDuty incident data if config exists
	EnrichWithPagerDutyIncident(sc, &parsedPayload, username, encryptedPassword)

	// Override RuleId/RuleName with actual alertname extracted from firing text
	if alertname, ok := parsedPayload.Investigation.Labels["alertname"]; ok && alertname != "" {
		parsedPayload.Investigation.RuleId = alertname
		parsedPayload.Investigation.RuleName = alertname
		ruleIdFromService = false // now a real alert identity, not the service name
	}

	// Improve title: Alertmanager default title is a concatenation of all label values.
	// Prefer a human-readable summary; fall back to alertname.
	if title := resolveEventTitleFromLabels(parsedPayload.Investigation.Labels); title != "" {
		// Preserve the original title before overwriting it: it carries the
		// /k8s/<ns>/<pod>/<workload> path that the deterministic subject fallback in
		// resolveSubjectFromLabels relies on, which the human-readable title drops.
		if parsedPayload.EventTitle != "" && parsedPayload.Investigation.Labels != nil {
			parsedPayload.Investigation.Labels["nb_pd_raw_title"] = parsedPayload.EventTitle
		}
		parsedPayload.EventTitle = title
	}

	// Resolve subject name/namespace from enriched labels
	resolveSubjectFromLabels(&parsedPayload)

	// If no stable fingerprint was set (no last9/Chronosphere/CEF dedup_key),
	// derive a canonical one from the alert's identity so repeat incidents for
	// the same alert collapse into one occurrence chain instead of fabricating a
	// fake correlated incident. Require enough identity (rule-type or subject) to
	// avoid over-merging distinct alerts; otherwise keep it per-incident (the
	// incident id) — under-collapsing is safer than merging unrelated alerts.
	if parsedPayload.Investigation.Fingerprint == "" {
		// A service-derived RuleId is not a real alert identity, so require a
		// subject in that case — otherwise every subject-less alert from the same
		// PagerDuty service would collapse into one chain via the shared service
		// name, which is the merge-unrelated-alerts failure this guard prevents.
		hasAlertIdentity := parsedPayload.Investigation.RuleId != "" && !ruleIdFromService
		if hasAlertIdentity || parsedPayload.EventSubjectName != "" {
			parsedPayload.Investigation.Fingerprint = core.CanonicalFingerprint(
				"pagerduty",
				parsedPayload.Investigation.RuleId,
				parsedPayload.EventSubjectNamespace,
				parsedPayload.EventSubjectName,
			)
		} else {
			parsedPayload.Investigation.Fingerprint = parsedPayload.EventId
		}
	}

	// Remap account before enrichment so workload lookups target the correct account
	accountMapping := core.ParseAccountMapping(settings, sc.GetLogger())
	accountId = core.ApplyAccountMapping(accountId, parsedPayload.Investigation.Labels, accountMapping)

	// A pod-typed subject (e.g. a rendered "Subject Type: pod" body) carries a
	// ReplicaSet hash that never matches k8s_workloads by name; resolve it to its
	// owning workload first so the core matcher validates the controller, not the
	// hash-suffixed pod. Mirrors the Datadog webhook path (lookupPodOwner).
	if parsedPayload.EventSubjectName != "" && parsedPayload.EventSubjectKind == "pod" {
		if dbms, dbErr := database.GetDatabaseManager(database.Metastore); dbErr == nil {
			tenantId := sc.GetSecurityContext().GetTenantId()
			if owner, kind, ns, ok := lookupPodOwner(sc, dbms, tenantId, []string{accountId},
				parsedPayload.EventSubjectNamespace, parsedPayload.EventSubjectName); ok {
				parsedPayload.Investigation.Labels["pod"] = parsedPayload.EventSubjectName
				parsedPayload.EventSubjectName = owner
				parsedPayload.EventSubjectKind = kind
				if parsedPayload.EventSubjectNamespace == "" {
					parsedPayload.EventSubjectNamespace = ns
				}
			}
		}
	}

	// Subject validation against k8s_workloads is NOT done here. Every webhook event
	// goes through core.MatchWorkloadAndEnrich in enrichEventsWithSubjectResolution
	// after account mapping, and that matcher scopes the lookup to the event's own
	// namespace and refuses ambiguous rows when the namespace is unknown.

	// GCP Cloud Monitoring alerts routed via PagerDuty carry the resource only in
	// the incident title (no structured labels survive). Parse it deterministically
	// before the LLM fallback so a Cloud SQL / GCE / GKE resource resolves to itself
	// instead of an LLM-hallucinated k8s workload. Only fills a subject that is still
	// empty, so a subject recovered from labels always wins.
	applyGCPMonitoringSubject(&parsedPayload)

	// LLM fallback: if no subject found after deterministic parsing, use LLM
	if parsedPayload.EventSubjectName == "" {
		tenantId := sc.GetSecurityContext().GetTenantId()
		if tenant.IsFeatureEnabled(sc, tenantId, tenant.FEATURE_WEBHOOK_LLM_RESOLUTION) {
			resolveSubjectUsingLLM(sc, &parsedPayload, accountId)
		} else {
			parsedPayload.Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}

	// Auto-learn: save confirmed title → service mapping for future LLM prompts.
	// Skipped for Cloud Optimization subjects: their title is "Cloud Optimization -
	// <rule_name>", identical for every resource the rule fires on, while the subject
	// is per-resource. Learning it would make each incident overwrite the last one's
	// mapping and teach the LLM that the rule name means whichever volume it saw most
	// recently.
	if parsedPayload.EventSubjectName != "" && parsedPayload.EventTitle != "" &&
		parsedPayload.Investigation.Labels["nb_subject_match"] != cloudOptimizationSubjectMatch {
		LearnSubjectMapping(sc, sc.GetSecurityContext().GetTenantId(), TenantAttrPagerDutyIncidentsKey, parsedPayload.EventTitle, parsedPayload.EventSubjectName)
	}

	// Add alert rule details evidence if we have any rule metadata
	if alertRuleEvidence := buildAlertRuleEvidence(parsedPayload.Investigation.Labels); alertRuleEvidence != nil {
		parsedPayload.Investigation.Evidences = append(parsedPayload.Investigation.Evidences, *alertRuleEvidence)
	}

	return []core.EventIncomingWebhook{parsedPayload}, nil
}

// enrichWithPagerDutyIncident enriches the webhook payload with PagerDuty incident data
// if valid configuration exists. It maps custom fields to event labels.
func EnrichWithPagerDutyIncident(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, username, encryptedPassword string) {
	// Check if PagerDuty config exists
	if encryptedPassword == "" || username == "" {
		sc.GetLogger().Info("pagerdutywebhook: no PagerDuty config found, skipping incident enrichment")
		return
	}

	password, err := common.Decrypt(encryptedPassword)
	if err != nil {
		sc.GetLogger().Error("pagerdutywebhook: failed to decrypt password", "error", err)
		return
	}

	incident, err := GetPagerDutyIncident(password, parsedPayload.EventId)
	if err != nil {
		sc.GetLogger().Error("pagerdutywebhook: failed to get PagerDuty incident", "error", err, "incident_id", parsedPayload.EventId)
		return
	}

	// PagerDuty attaches the CEF (body.details, which carries the Alertmanager Labels
	// block used for deterministic subject resolution) asynchronously — it is often
	// still null when this webhook fires (~1s after incident creation) and can take
	// several seconds to populate. Poll until it does: refetch the incident (the
	// __pd_cef_payload format extractBodyDetails prefers), and if still null fall back
	// to the incident's alerts endpoint, which carries the same firing text in
	// body.details / body.cef_details.
	//
	// This runs on the async webhook processor (not the PagerDuty-facing HTTP handler,
	// which has already stored the payload and returned), so the wait never delays
	// webhook delivery or risks PD re-delivery. The backoff sleeps total ~6s across the
	// attempts; the true worst case is longer when the PD API itself is slow, since each
	// attempt makes up to two GETs that each carry a 10s client timeout. When the CEF
	// never arrives in time, the LLM subject resolver remains the final fallback.
	const maxBodyDetailsRetries = 3
	for attempt := 1; incident.Body.Details == nil && attempt <= maxBodyDetailsRetries; attempt++ {
		sc.GetLogger().Info("pagerdutywebhook: incident body.details is null, retrying",
			"incident_id", parsedPayload.EventId, "attempt", attempt)
		time.Sleep(time.Duration(attempt) * time.Second)

		retryIncident, retryErr := GetPagerDutyIncident(password, parsedPayload.EventId)
		if retryErr != nil {
			sc.GetLogger().Warn("pagerdutywebhook: incident refetch failed", "attempt", attempt, "error", retryErr)
		} else if retryIncident.Body.Details != nil {
			incident = retryIncident
			break
		}

		alerts, alertErr := GetPagerDutyIncidentAlerts(password, parsedPayload.EventId)
		if alertErr != nil {
			sc.GetLogger().Warn("pagerdutywebhook: alerts endpoint failed", "attempt", attempt, "error", alertErr)
			continue
		}
		for _, alert := range alerts {
			// PD alerts endpoint returns CEF data in both body.details and body.cef_details.
			// Check details first, then fall back to cef_details.
			if alert.Body.Details != nil {
				incident.Body = alert.Body
				break
			}
			if alert.Body.CefDetails != nil {
				incident.Body.Details = alert.Body.CefDetails
				if incident.Body.Type == "" {
					incident.Body.Type = alert.Body.Type
				}
				break
			}
		}
	}

	// Initialize labels map if needed
	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = make(map[string]string)
	}

	// Extract data from body details using generic approach
	extractBodyDetails(incident, parsedPayload)

	// PagerDuty sometimes delivers body.details as an unstructured markdown string
	// instead of the structured __pd_cef_payload map, in which case extractBodyDetails
	// yields no subject labels. Mine the declared subject from the string before the
	// downstream resolveSubjectFromLabels / LLM fallback consume the labels.
	stringDetails := getStringDetailsFromBody(incident)
	applyStringDetailsSubjectLabels(stringDetails, parsedPayload.Investigation.Labels)

	// NudgeBee's own Cloud Optimization incidents come back as a plain `key: value`
	// block that the markdown miner above does not match. Runs second so a declared
	// **Subject Name** always wins.
	applyCloudOptimizationSubjectLabels(stringDetails, parsedPayload.Investigation.Labels)

	if incident.Body.Details == nil {
		sc.GetLogger().Warn("pagerdutywebhook: body.details is null after all fetch attempts",
			"incident_id", parsedPayload.EventId)
	}

	// Create dynamic markdown description based on structured alert data
	alertData := extractAlertData(incident)
	parsedPayload.EventDescription = createDynamicDescriptionFromAlertData(alertData, incident)

	// Set fingerprint using dedup key for better alert correlation
	if alertData.Common.DedupKey != "" {
		parsedPayload.Investigation.Fingerprint = alertData.Common.DedupKey
	}

	parsedPayload.Investigation.Severity = mapSeverityToEventPriority(alertData.Common.Severity)

	// If CEF severity was empty, fall back to source-specific severity labels
	if alertData.Common.Severity == "" {
		var fallbackSeverity string
		switch alertData.Source {
		case AlertSourceGrafana:
			if alertData.Grafana != nil {
				fallbackSeverity = alertData.Grafana.Labels["severity"]
			}
		case AlertSourceSigNoz:
			if alertData.SigNoz != nil {
				fallbackSeverity = alertData.SigNoz.Severity
			}
		}
		// Check already-merged labels
		if fallbackSeverity == "" {
			fallbackSeverity = parsedPayload.Investigation.Labels["severity"]
		}
		// Last resort: use PD incident urgency (already parsed into EventPriority)
		if fallbackSeverity == "" {
			fallbackSeverity = parsedPayload.EventPriority
		}
		if fallbackSeverity != "" {
			parsedPayload.Investigation.Severity = mapSeverityToEventPriority(fallbackSeverity)
		}
	}

	// Map custom fields to event labels
	if len(incident.CustomFields) > 0 {
		for _, field := range incident.CustomFields {
			if field.Value != nil {
				parsedPayload.Investigation.Labels[field.Name] = fmt.Sprintf("%v", field.Value)
			}
		}
	}

	incidentByte, err := common.MarshalJson(incident)
	if err != nil {
		sc.GetLogger().Error("pagerdutywebhook: failed to marshal incident for evidence", "error", err)
		return
	}
	rawPayloadEvidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "PagerDuty Alert",
			"data": string(incidentByte), // Store the entire parsed incident as json string
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("PagerDuty Incident ID: %s (Number: %d)", incident.ID, incident.IncidentNumber),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "pagerduty_alert",
			"actual_action_name":     "pagerduty_alert",
			"action_title":           "PagerDuty Alert",
			"conditional_expression": "",
		},
	}
	parsedPayload.Investigation.Evidences = append(parsedPayload.Investigation.Evidences, rawPayloadEvidence)
}

// AlertSource represents different alert sources
type AlertSource string

const (
	AlertSourceAWS          AlertSource = "aws"
	AlertSourceGrafana      AlertSource = "grafana"
	AlertSourceSigNoz       AlertSource = "signoz"
	AlertSourceAzure        AlertSource = "azure"
	AlertSourceChronosphere AlertSource = "chronosphere"
	AlertSourceUnknown      AlertSource = "unknown"
)

// AlertData represents extracted alert information in a structured format
type AlertData struct {
	Source       AlertSource
	Common       CommonAlertFields
	AWS          *AWSAlertFields
	Grafana      *GrafanaAlertFields
	SigNoz       *SigNozAlertFields
	Azure        *AzureAlertFields
	Chronosphere *ChronosphereAlertFields
	Generic      *GenericAlertFields
}

// CommonAlertFields contains fields common to all alert sources
type CommonAlertFields struct {
	DedupKey        string
	Severity        string
	EventID         string
	RoutingKey      string
	Client          string
	SourceComponent string
	EreAccountID    int
	EventStorageID  string
}

// AWSAlertFields contains AWS CloudWatch specific fields
type AWSAlertFields struct {
	AlarmName        string
	Region           string
	AWSAccountId     string
	NewStateValue    string
	OldStateValue    string
	AlarmArn         string
	NewStateReason   string
	StateChangeTime  string
	AlarmDescription string
	ClientURL        string

	// Trigger details
	MetricName         string
	Namespace          string
	Statistic          string
	ComparisonOperator string
	Threshold          float64
	Period             int
	EvaluationPeriods  int

	// Actions
	AlarmActions []string
	OKActions    []string

	// Dimensions
	Dimensions map[string]string
}

// GrafanaAlertFields contains Grafana specific fields
type GrafanaAlertFields struct {
	AlertName      string
	GrafanaFolder  string
	Instance       string
	Job            string
	RuleID         string
	RuleSource     string
	AlertValue     string
	Description    string
	Summary        string
	RelatedLogs    string
	SourceURL      string
	ClientURL      string
	DedupKey       string
	EventID        string
	EventStorageID string
	SourceOrigin   string
	RoutingKey     string
	ServiceGroup   string
	AlertRuleUID   string
	OrgID          string
	DashboardURL   string
	PanelURL       string
	SilenceURL     string

	// Firing details
	NumFiring   int
	NumResolved int

	// Labels and annotations from firing text
	Labels      map[string]string
	Annotations map[string]string
}

// SigNozAlertFields contains SigNoz specific fields
type SigNozAlertFields struct {
	AlertName      string
	RuleID         string
	Tag            string
	Description    string
	Summary        string
	SourceURL      string
	ClientURL      string
	DedupKey       string
	EventID        string
	EventStorageID string
	SourceOrigin   string
	RoutingKey     string
	EreAccountID   int
	PayloadSource  string
	Severity       string
	Threshold      string
	CurrentValue   string
	RelatedLogs    string

	// Firing details
	NumFiring   int
	NumResolved int

	// Labels and annotations
	Labels      map[string]string
	Annotations map[string]string
}

// AzureAlertFields contains Azure Monitor specific fields
type AzureAlertFields struct {
	ResourceName       string
	ResourceType       string
	ResourceGroupName  string
	SubscriptionId     string
	MetricName         string
	MetricNamespace    string
	Threshold          float64
	Operator           string
	TimeAggregation    string
	MetricValue        float64
	WindowSize         string
	PortalLink         string
	CreationTime       string
	DedupKey           string
	Severity           string
	ConditionType      string
	ContextDescription string
	Timestamp          string
	Dimensions         map[string]string
	MinFailingPeriods  int
	EvaluationPeriods  int
}

// ChronosphereAlertFields contains Chronosphere specific fields
type ChronosphereAlertFields struct {
	MonitorName            string
	MonitorSlug            string
	NotificationPolicySlug string
	AlertID                string
	Environment            string
	Job                    string
	Type                   string
	Severity               string
	Signal                 string
	SourceURL              string
	ClientURL              string
	DedupKey               string
	EventID                string
	EventStorageID         string
	SourceOrigin           string
	RoutingKey             string
	EreAccountID           int
	PayloadSource          string
	PayloadSummary         string

	// Alert series counts
	NumAlertingSeries int
	NumResolvedSeries int

	// Labels and annotations parsed from details
	Labels      map[string]string
	Annotations map[string]string
}

// GenericAlertFields contains fields for unknown alert sources
type GenericAlertFields struct {
	Description string
	Message     string

	// Any firing text found
	FiringText  string
	Labels      map[string]string
	Annotations map[string]string
}

// extractBodyDetails extracts information using struct-based approach
func extractBodyDetails(incident *Incident, parsedPayload *core.EventIncomingWebhook) {
	// Extract structured alert data
	alertData := extractAlertData(incident)

	// Convert to labels map for backward compatibility
	labels := alertDataToLabels(alertData)

	// Merge efficiently
	for key, value := range labels {
		if value != "" { // Only add non-empty values
			parsedPayload.Investigation.Labels[key] = value
		}
	}
}

// extractAlertData extracts structured alert data based on detected source
func extractAlertData(incident *Incident) *AlertData {
	// Parse body details once to avoid redundant parsing
	bodyDetails := incident.Body.GetBodyDetails()

	source := detectAlertSource(incident, bodyDetails)

	alertData := &AlertData{
		Source: source,
		Common: extractCommonAlertFields(incident, bodyDetails),
	}

	switch source {
	case AlertSourceAWS:
		alertData.AWS = extractAWSAlertFields(incident, bodyDetails)
	case AlertSourceGrafana:
		alertData.Grafana = extractGrafanaAlertFields(incident, bodyDetails)
	case AlertSourceSigNoz:
		alertData.SigNoz = extractSigNozAlertFields(incident, bodyDetails)
	case AlertSourceAzure:
		alertData.Azure = extractAzureAlertFields(incident, bodyDetails)
	case AlertSourceChronosphere:
		alertData.Chronosphere = extractChronosphereAlertFields(incident, bodyDetails)
	default:
		alertData.Generic = extractGenericAlertFields(incident, bodyDetails)
	}

	return alertData
}

// detectAlertSource identifies the alert source type
func detectAlertSource(incident *Incident, bodyDetails *BodyDetails) AlertSource {
	// Check for AWS CloudWatch alarms in plain text email format (email-based alerts)
	if incident != nil {
		emailText := getStringDetailsFromBody(incident)
		if emailText != "" {
			// Check for AWS CloudWatch alarm indicators in email body
			if strings.Contains(emailText, "Amazon CloudWatch Alarm") ||
				strings.Contains(emailText, "AWS Notifications") ||
				strings.Contains(emailText, "sns.amazonaws.com") ||
				strings.Contains(emailText, "cloudwatch") {
				return AlertSourceAWS
			}
		}
	}

	if bodyDetails == nil {
		return AlertSourceUnknown
	}
	cef := bodyDetails.PdCefPayload

	switch {
	case cef.Client == "Grafana" || cef.SourceComponent == "Grafana":
		return AlertSourceGrafana
	case cef.Client == "SigNoz Alert Manager":
		return AlertSourceSigNoz
	case strings.Contains(cef.Client, "AWS") || cef.EventClass == "NetworkIn":
		return AlertSourceAWS
	case cef.EventClass == "AzureMonitorMetricAlert":
		return AlertSourceAzure
	case cef.Client == "Chronosphere" || cef.SourceOrigin == "Chronosphere":
		return AlertSourceChronosphere
	default:
		// Check if firing text uses the standard Alertmanager format (Labels:/Annotations:/Source:)
		// This covers any datasource that routes alerts through Alertmanager
		firingText := findFiringText(incident, bodyDetails)
		if firingText != "" && strings.Contains(firingText, "Labels:") {
			return AlertSourceGrafana
		}
		return AlertSourceUnknown
	}
}

// getExtractor returns the appropriate field extractor for the source
// extractSigNozAlertFields extracts SigNoz specific fields
func extractSigNozAlertFields(incident *Incident, bodyDetails *BodyDetails) *SigNozAlertFields {
	signozFields := &SigNozAlertFields{
		Labels:      make(map[string]string),
		Annotations: make(map[string]string),
	}

	// Extract from firing field (similar to Grafana)
	firingText := findFiringText(incident, bodyDetails)
	if firingText != "" {
		firingLabels := parseFiringLabels(firingText)

		// Separate labels and annotations
		for key, value := range firingLabels {
			if strings.HasPrefix(key, "annotation_") {
				signozFields.Annotations[strings.TrimPrefix(key, "annotation_")] = value
			} else {
				signozFields.Labels[key] = value
			}
		}

		// Extract specific SigNoz fields
		signozFields.AlertName = signozFields.Labels["alertname"]
		signozFields.RuleID = signozFields.Labels["ruleId"]
		signozFields.Tag = signozFields.Labels["Tag"]
		signozFields.SourceURL = signozFields.Labels["source_url"]
		signozFields.Description = signozFields.Annotations["description"]
		signozFields.Summary = signozFields.Annotations["summary"]
	}

	// Extract firing counts using the bodyDetails parameter
	if bodyDetails == nil {
		return signozFields
	}
	cef := bodyDetails.PdCefPayload
	if cef.Details != nil {
		if numFiring, ok := cef.Details["num_firing"].(string); ok {
			if count, err := strconv.Atoi(numFiring); err == nil {
				signozFields.NumFiring = count
			}
		}
		if numResolved, ok := cef.Details["num_resolved"].(string); ok {
			if count, err := strconv.Atoi(numResolved); err == nil {
				signozFields.NumResolved = count
			}
		}
	}

	// Extract CEF-level fields
	signozFields.ClientURL = cef.ClientURL
	signozFields.DedupKey = cef.DedupKey
	signozFields.EventID = cef.EventID
	signozFields.SourceOrigin = cef.SourceOrigin
	signozFields.Severity = cef.Severity

	// Extract body-level fields
	signozFields.EventStorageID = bodyDetails.EventStorageID
	signozFields.RoutingKey = bodyDetails.RoutingKey
	signozFields.EreAccountID = bodyDetails.EreAccountID

	// Extract payload fields
	signozFields.PayloadSource = bodyDetails.Payload.Source
	if signozFields.Severity == "" {
		signozFields.Severity = bodyDetails.Payload.Severity
	}

	// Extract threshold and current value from annotations
	if desc := signozFields.Annotations["description"]; desc != "" {
		// Parse "current value: 26" and "threshold (2)"
		if strings.Contains(desc, "current value:") {
			if parts := strings.Split(desc, "current value:"); len(parts) > 1 {
				if valueParts := strings.Split(parts[1], ")"); len(valueParts) > 0 {
					signozFields.CurrentValue = strings.TrimSpace(valueParts[0])
				}
			}
		}
		if strings.Contains(desc, "threshold (") {
			if parts := strings.Split(desc, "threshold ("); len(parts) > 1 {
				if thresholdParts := strings.Split(parts[1], ")"); len(thresholdParts) > 0 {
					signozFields.Threshold = strings.TrimSpace(thresholdParts[0])
				}
			}
		}
	}

	// Extract related logs from annotations
	signozFields.RelatedLogs = signozFields.Annotations["related_logs"]

	return signozFields
}

// extractAzureAlertFields extracts Azure Monitor specific fields
func extractAzureAlertFields(incident *Incident, bodyDetails *BodyDetails) *AzureAlertFields {
	azureFields := &AzureAlertFields{}
	if bodyDetails == nil {
		return nil
	}
	cef := bodyDetails.PdCefPayload

	// Extract from nested Azure data structure
	if cef.Details != nil {
		if data, ok := cef.Details["data"].(map[string]interface{}); ok {
			if context, ok := data["context"].(map[string]interface{}); ok {
				if resourceName, ok := context["resourceName"].(string); ok {
					azureFields.ResourceName = resourceName
				}
				if resourceType, ok := context["resourceType"].(string); ok {
					azureFields.ResourceType = resourceType
				}
				if resourceGroupName, ok := context["resourceGroupName"].(string); ok {
					azureFields.ResourceGroupName = resourceGroupName
				}
				if subscriptionId, ok := context["subscriptionId"].(string); ok {
					azureFields.SubscriptionId = subscriptionId
				}
				if portalLink, ok := context["portalLink"].(string); ok {
					azureFields.PortalLink = portalLink
				}

				// Extract condition details
				if condition, ok := context["condition"].(map[string]interface{}); ok {
					if windowSize, ok := condition["windowSize"].(string); ok {
						azureFields.WindowSize = windowSize
					}

					if allOf, ok := condition["allOf"].([]interface{}); ok && len(allOf) > 0 {
						if metric, ok := allOf[0].(map[string]interface{}); ok {
							if metricName, ok := metric["metricName"].(string); ok {
								azureFields.MetricName = metricName
							}
							if metricNamespace, ok := metric["metricNamespace"].(string); ok {
								azureFields.MetricNamespace = metricNamespace
							}
							if threshold, ok := metric["threshold"].(float64); ok {
								azureFields.Threshold = threshold
							}
							if operator, ok := metric["operator"].(string); ok {
								azureFields.Operator = operator
							}
							if timeAggregation, ok := metric["timeAggregation"].(string); ok {
								azureFields.TimeAggregation = timeAggregation
							}
							if metricValue, ok := metric["metricValue"].(float64); ok {
								azureFields.MetricValue = metricValue
							}
						}
					}

					// Extract additional context fields
					if conditionType, ok := context["conditionType"].(string); ok {
						azureFields.ConditionType = conditionType
					}
					if description, ok := context["description"].(string); ok {
						azureFields.ContextDescription = description
					}
					if severity, ok := context["severity"].(string); ok {
						azureFields.Severity = severity
					}
					if timestamp, ok := context["timestamp"].(string); ok {
						azureFields.Timestamp = timestamp
					}

					// Extract condition threshold settings
					if condition, ok := context["condition"].(map[string]interface{}); ok {
						if failingPeriods, ok := condition["staticThresholdFailingPeriods"].(map[string]interface{}); ok {
							if min, ok := failingPeriods["minFailingPeriodsToAlert"].(float64); ok {
								azureFields.MinFailingPeriods = int(min)
							}
							if eval, ok := failingPeriods["numberOfEvaluationPeriods"].(float64); ok {
								azureFields.EvaluationPeriods = int(eval)
							}
						}

						// Extract dimensions from the first condition
						if allOf, ok := condition["allOf"].([]interface{}); ok && len(allOf) > 0 {
							if metric, ok := allOf[0].(map[string]interface{}); ok {
								if dimensions, ok := metric["dimensions"].([]interface{}); ok {
									azureFields.Dimensions = make(map[string]string)
									for _, dim := range dimensions {
										if dimMap, ok := dim.(map[string]interface{}); ok {
											if name, nameOk := dimMap["name"].(string); nameOk {
												if value, valueOk := dimMap["value"].(string); valueOk {
													azureFields.Dimensions[name] = value
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Extract CEF-level fields
	if cef.CreationTime != "" {
		azureFields.CreationTime = cef.CreationTime
	}
	if cef.DedupKey != "" {
		azureFields.DedupKey = cef.DedupKey
	}

	return azureFields
}

// extractChronosphereAlertFields extracts Chronosphere specific fields
func extractChronosphereAlertFields(incident *Incident, bodyDetails *BodyDetails) *ChronosphereAlertFields {
	chronosphereFields := &ChronosphereAlertFields{
		Labels:      make(map[string]string),
		Annotations: make(map[string]string),
	}

	if bodyDetails == nil {
		return nil
	}
	cef := bodyDetails.PdCefPayload

	// Extract CEF-level fields
	chronosphereFields.ClientURL = cef.ClientURL
	chronosphereFields.DedupKey = cef.DedupKey
	chronosphereFields.EventID = cef.EventID
	chronosphereFields.SourceOrigin = cef.SourceOrigin
	chronosphereFields.Severity = cef.Severity

	// Extract body-level fields
	chronosphereFields.EventStorageID = bodyDetails.EventStorageID
	chronosphereFields.RoutingKey = bodyDetails.RoutingKey
	chronosphereFields.EreAccountID = bodyDetails.EreAccountID

	// Extract payload fields
	chronosphereFields.PayloadSource = bodyDetails.Payload.Source
	chronosphereFields.PayloadSummary = bodyDetails.Payload.Summary

	// Parse Chronosphere-specific fields from details
	if cef.Details != nil {
		// Extract monitor config details
		if monitorConfig, ok := cef.Details["\u2061\nMonitor Config"].(string); ok {
			monitorConfig = strings.Trim(monitorConfig, "\u2061\n")
			parseChronosphereMonitorConfig(chronosphereFields, monitorConfig)
		}

		// Extract labels from the labels field
		if labels, ok := cef.Details["\u2061\nLabels"].(string); ok {
			labels = strings.Trim(labels, "\u2061\n")
			parseChronosphereLabels(chronosphereFields, labels)
		}

		// Extract signal
		if signal, ok := cef.Details["\u2061\nSignal"].(string); ok {
			chronosphereFields.Signal = strings.Trim(signal, "\u2061\n")
			parseChronosphereSignal(chronosphereFields, signal)
		}

		// Extract source URL
		if source, ok := cef.Details["\u2061\nSource"].(string); ok {
			chronosphereFields.SourceURL = strings.Trim(source, "\u2061\n")
			parseChronosphereAlertID(chronosphereFields, source)
		}

		// Extract severity
		if severity, ok := cef.Details["\u2061\nSeverity"].(string); ok && chronosphereFields.Severity == "" {
			chronosphereFields.Severity = strings.Trim(severity, "\u2061\n")
		}

		// Extract alert series counts
		if alertingSeries, ok := cef.Details["\u2061\nNumber of Alerting Series"].(string); ok {
			if count, err := strconv.Atoi(alertingSeries); err == nil {
				chronosphereFields.NumAlertingSeries = count
			}
		}

		if resolvedSeries, ok := cef.Details["\u2061\nNumber of Resolved Series"].(string); ok {
			if count, err := strconv.Atoi(resolvedSeries); err == nil {
				chronosphereFields.NumResolvedSeries = count
			}
		}
	}

	return chronosphereFields
}

// parseChronosphereMonitorConfig parses the monitor config string
func parseChronosphereMonitorConfig(fields *ChronosphereAlertFields, config string) {
	lines := strings.Split(config, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name: ") {
			fields.MonitorName = strings.TrimPrefix(line, "Name: ")
		} else if strings.HasPrefix(line, "Slug: ") {
			fields.MonitorSlug = strings.TrimPrefix(line, "Slug: ")
		} else if strings.HasPrefix(line, "Notification Policy Slug: ") {
			fields.NotificationPolicySlug = strings.TrimPrefix(line, "Notification Policy Slug: ")
		}
	}
}

// parseChronosphereLabels parses labels from the Chronosphere labels string
func parseChronosphereLabels(fields *ChronosphereAlertFields, labelsStr string) {
	// Parse format like "{environment=prod, job=shipment-event-worker, type=client}"
	labelsStr = strings.Trim(labelsStr, "{}")
	if labelsStr == "" {
		return
	}

	pairs := strings.Split(labelsStr, ", ")
	for _, pair := range pairs {
		if kv := strings.Split(pair, "="); len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			value := strings.TrimSpace(kv[1])
			fields.Labels[key] = value

			// Extract specific fields
			switch key {
			case "environment":
				fields.Environment = value
			case "job":
				fields.Job = value
			case "type":
				fields.Type = value
			}
		}
	}
}

// parseChronosphereSignal parses the signal field to extract environment and job
func parseChronosphereSignal(fields *ChronosphereAlertFields, signal string) {
	// parse it as JSON
	signalMap := map[string]any{}
	err := common.UnmarshalJson([]byte(signal), &signalMap)
	if err != nil {
		return
	}
	// map to labels
	for k, v := range signalMap {
		fields.Labels[k] = fmt.Sprintf("%v", v)
	}
	// Extract specific fields if not already set
	if env, ok := signalMap["environment"].(string); ok && fields.Environment == "" {
		fields.Environment = env
	}
	if job, ok := signalMap["job"].(string); ok && fields.Job == "" {
		fields.Job = job
	}
}

// parseChronosphereAlertID extracts the alert ID from the source URL
func parseChronosphereAlertID(fields *ChronosphereAlertFields, sourceURL string) {
	// Extract alert_id from URL parameter
	if strings.Contains(sourceURL, "alert_id=") {
		if parts := strings.Split(sourceURL, "alert_id="); len(parts) > 1 {
			if idParts := strings.Split(parts[1], "&"); len(idParts) > 0 {
				fields.AlertID = idParts[0]
			}
		}
	}
}

// extractGenericAlertFields extracts fields for unknown alert sources
func extractGenericAlertFields(incident *Incident, bodyDetails *BodyDetails) *GenericAlertFields {
	genericFields := &GenericAlertFields{
		Labels:      make(map[string]string),
		Annotations: make(map[string]string),
	}

	if bodyDetails == nil {
		return nil
	}
	cef := bodyDetails.PdCefPayload
	genericFields.Description = cef.Description
	genericFields.Message = cef.Message

	// Try to find firing text in any location
	firingText := findFiringText(incident, bodyDetails)
	if firingText != "" {
		genericFields.FiringText = firingText
		firingLabels := parseFiringLabels(firingText)

		// Separate labels and annotations
		for key, value := range firingLabels {
			if strings.HasPrefix(key, "annotation_") {
				genericFields.Annotations[strings.TrimPrefix(key, "annotation_")] = value
			} else {
				genericFields.Labels[key] = value
			}
		}
	}

	return genericFields
}

// alertDataToLabels converts structured AlertData back to labels map for backward compatibility
func alertDataToLabels(alertData *AlertData) map[string]string {
	labels := make(map[string]string)

	// Add common fields
	common := alertData.Common
	setIfNotEmpty(labels, "dedup_key", common.DedupKey)
	setIfNotEmpty(labels, "severity", common.Severity)
	setIfNotEmpty(labels, "event_id", common.EventID)
	setIfNotEmpty(labels, "routing_key", common.RoutingKey)
	setIfNotEmpty(labels, "client", common.Client)
	setIfNotEmpty(labels, "source_component", common.SourceComponent)
	setIfNotEmpty(labels, "event_storage_id", common.EventStorageID)
	if common.EreAccountID != 0 {
		labels["ere_account_id"] = strconv.Itoa(common.EreAccountID)
	}

	// Add normalized cloud provider labels
	addNormalizedCloudLabels(alertData, labels)

	// Add source-specific fields
	switch alertData.Source {
	case AlertSourceAWS:
		if aws := alertData.AWS; aws != nil {
			// Original field labels (for backward compatibility)
			setIfNotEmpty(labels, "AlarmName", aws.AlarmName)
			setIfNotEmpty(labels, "Region", aws.Region)
			setIfNotEmpty(labels, "AWSAccountId", aws.AWSAccountId)
			setIfNotEmpty(labels, "NewStateValue", aws.NewStateValue)
			setIfNotEmpty(labels, "OldStateValue", aws.OldStateValue)
			setIfNotEmpty(labels, "AlarmArn", aws.AlarmArn)
			setIfNotEmpty(labels, "NewStateReason", aws.NewStateReason)
			setIfNotEmpty(labels, "StateChangeTime", aws.StateChangeTime)
			setIfNotEmpty(labels, "AlarmDescription", aws.AlarmDescription)
			setIfNotEmpty(labels, "ClientURL", aws.ClientURL)
			setIfNotEmpty(labels, "MetricName", aws.MetricName)
			setIfNotEmpty(labels, "Namespace", aws.Namespace)
			setIfNotEmpty(labels, "Statistic", aws.Statistic)
			setIfNotEmpty(labels, "ComparisonOperator", aws.ComparisonOperator)

			if aws.Threshold != 0 {
				labels["Threshold"] = fmt.Sprintf("%.1f", aws.Threshold)
			}
			if aws.Period != 0 {
				labels["Period"] = strconv.Itoa(aws.Period)
			}
			if aws.EvaluationPeriods != 0 {
				labels["EvaluationPeriods"] = strconv.Itoa(aws.EvaluationPeriods)
			}

			// Add aws_event_* labels for cloud actions compatibility
			setIfNotEmpty(labels, "aws_event_name", aws.AlarmName)
			setIfNotEmpty(labels, "aws_event_arn", aws.AlarmArn)
			setIfNotEmpty(labels, "aws_region", aws.Region)
			setIfNotEmpty(labels, "aws_account", aws.AWSAccountId)
			setIfNotEmpty(labels, "aws_event_metric_name", aws.MetricName)
			setIfNotEmpty(labels, "aws_event_metric_namespace", aws.Namespace)
			setIfNotEmpty(labels, "aws_event_metric_statistic", aws.Statistic)

			// Map state values
			if aws.NewStateValue != "" {
				// Convert ALARM/OK to firing/resolved for consistency
				state := strings.ToLower(aws.NewStateValue)
				switch state {
				case "alarm":
					labels["aws_event_state"] = "firing"
				case "ok":
					labels["aws_event_state"] = "resolved"
				default:
					labels["aws_event_state"] = state
				}
			}

			if aws.Threshold != 0 {
				labels["aws_event_threshold"] = fmt.Sprintf("%.1f", aws.Threshold)
			}

			// Extract aws_event_instance from dimensions
			// First pass: add all dimension labels and build dimensions array for CloudWatch API
			dimensionsArr := []map[string]string{}
			for key, value := range aws.Dimensions {
				labels[fmt.Sprintf("dimension_%s", key)] = value
				// Build dimensions array in CloudWatch API format
				dimensionsArr = append(dimensionsArr, map[string]string{
					"Name":  key,
					"Value": value,
				})
			}

			// Set aws_event_alarm_dimensions as JSON for cloud actions
			if len(dimensionsArr) > 0 {
				if dimensionsJSON, err := json.Marshal(dimensionsArr); err == nil {
					labels["aws_event_alarm_dimensions"] = string(dimensionsJSON)
				}
			}

			// Second pass: determine aws_event_instance with proper prioritization
			// For VPN, prioritize VpnConnectionId over TunnelIpAddress
			// Priority order (highest to lowest):
			priorityKeys := []string{
				"VpnConnectionId", "VpnId", // VPN (highest priority)
				"InstanceId",                                  // EC2
				"DBInstanceIdentifier", "DBClusterIdentifier", // RDS
				"CacheClusterId", "ReplicationGroupId", // ElastiCache
				"ClusterIdentifier", "ClusterName", "ServiceName", // ECS/EKS
				"LoadBalancer", "LoadBalancerName", "TargetGroup", // ELB
				"FunctionName",         // Lambda
				"TableName",            // DynamoDB
				"BucketName",           // S3
				"QueueName",            // SQS
				"AutoScalingGroupName", // Auto Scaling
				"NatGatewayId",         // NAT Gateway
				"ApiName", "ApiId",     // API Gateway
				"FileSystemId",    // EFS
				"TunnelIpAddress", // VPN Tunnel (lowest priority for VPN)
			}

			for _, priorityKey := range priorityKeys {
				if value, exists := aws.Dimensions[priorityKey]; exists {
					labels["aws_event_instance"] = value
					break // Use the first (highest priority) match
				}
			}

			// Derive aws_service_name from Namespace
			if aws.Namespace != "" {
				labels["aws_service_name"] = deriveAWSServiceName(aws.Namespace)
			}

			// Parse and set timestamp labels for time-range queries
			// AWS timestamp format: "Saturday 25 October, 2025 12:57:51 UTC"
			if aws.StateChangeTime != "" {
				if timestamp, err := parseAWSTimestamp(aws.StateChangeTime); err == nil {
					// Set end_time as the alarm trigger time
					endTime := timestamp
					labels["ended_at"] = endTime.Format(time.RFC3339)

					// Calculate start_time based on evaluation period
					// Use a time window of (Period * EvaluationPeriods * 2) to capture context
					// Default to 1 hour if period info not available
					lookbackSeconds := 3600 // Default: 1 hour
					if aws.Period > 0 && aws.EvaluationPeriods > 0 {
						// Use 2x the evaluation window to capture more context
						lookbackSeconds = aws.Period * aws.EvaluationPeriods * 2
					} else if aws.Period > 0 {
						// Use 10 periods if EvaluationPeriods not set
						lookbackSeconds = aws.Period * 10
					}

					startTime := endTime.Add(-time.Duration(lookbackSeconds) * time.Second)
					labels["started_at"] = startTime.Format(time.RFC3339)
				}
			}

			// Add actions as comma-separated strings
			if len(aws.AlarmActions) > 0 {
				labels["AlarmActions"] = strings.Join(aws.AlarmActions, ",")
			}
			if len(aws.OKActions) > 0 {
				labels["OKActions"] = strings.Join(aws.OKActions, ",")
			}
		}

	case AlertSourceGrafana:
		if grafana := alertData.Grafana; grafana != nil {
			setIfNotEmpty(labels, "alertname", grafana.AlertName)
			setIfNotEmpty(labels, "grafana_folder", grafana.GrafanaFolder)
			setIfNotEmpty(labels, "instance", grafana.Instance)
			setIfNotEmpty(labels, "job", grafana.Job)
			setIfNotEmpty(labels, "ruleId", grafana.RuleID)
			setIfNotEmpty(labels, "ruleSource", grafana.RuleSource)
			setIfNotEmpty(labels, "alert_value", grafana.AlertValue)
			setIfNotEmpty(labels, "source_url", grafana.SourceURL)
			setIfNotEmpty(labels, "description", grafana.Description)
			setIfNotEmpty(labels, "summary", grafana.Summary)
			setIfNotEmpty(labels, "related_logs", grafana.RelatedLogs)
			setIfNotEmpty(labels, "clientURL", grafana.ClientURL)
			setIfNotEmpty(labels, "dedupKey", grafana.DedupKey)
			setIfNotEmpty(labels, "eventID", grafana.EventID)
			setIfNotEmpty(labels, "eventStorageID", grafana.EventStorageID)
			setIfNotEmpty(labels, "sourceOrigin", grafana.SourceOrigin)
			setIfNotEmpty(labels, "routingKey", grafana.RoutingKey)
			setIfNotEmpty(labels, "serviceGroup", grafana.ServiceGroup)
			setIfNotEmpty(labels, "alertRuleUID", grafana.AlertRuleUID)
			setIfNotEmpty(labels, "orgID", grafana.OrgID)
			setIfNotEmpty(labels, "dashboardURL", grafana.DashboardURL)
			setIfNotEmpty(labels, "panelURL", grafana.PanelURL)
			setIfNotEmpty(labels, "silenceURL", grafana.SilenceURL)

			if grafana.NumFiring > 0 {
				labels["num_firing"] = strconv.Itoa(grafana.NumFiring)
			}
			if grafana.NumResolved > 0 {
				labels["num_resolved"] = strconv.Itoa(grafana.NumResolved)
			}

			// Add all labels and annotations
			for key, value := range grafana.Labels {
				labels[key] = value
			}
			for key, value := range grafana.Annotations {
				labels[fmt.Sprintf("annotation_%s", key)] = value
			}
		}

	case AlertSourceSigNoz:
		if signoz := alertData.SigNoz; signoz != nil {
			setIfNotEmpty(labels, "alertname", signoz.AlertName)
			setIfNotEmpty(labels, "ruleId", signoz.RuleID)
			setIfNotEmpty(labels, "Tag", signoz.Tag)
			setIfNotEmpty(labels, "source_url", signoz.SourceURL)
			setIfNotEmpty(labels, "description", signoz.Description)
			setIfNotEmpty(labels, "summary", signoz.Summary)
			setIfNotEmpty(labels, "clientURL", signoz.ClientURL)
			setIfNotEmpty(labels, "dedupKey", signoz.DedupKey)
			setIfNotEmpty(labels, "eventID", signoz.EventID)
			setIfNotEmpty(labels, "eventStorageID", signoz.EventStorageID)
			setIfNotEmpty(labels, "sourceOrigin", signoz.SourceOrigin)
			setIfNotEmpty(labels, "routingKey", signoz.RoutingKey)
			setIfNotEmpty(labels, "payloadSource", signoz.PayloadSource)
			setIfNotEmpty(labels, "severity", signoz.Severity)
			setIfNotEmpty(labels, "threshold", signoz.Threshold)
			setIfNotEmpty(labels, "currentValue", signoz.CurrentValue)
			setIfNotEmpty(labels, "relatedLogs", signoz.RelatedLogs)
			if signoz.Labels["related_logs"] != "" {
				// related log is api link so lets extract only params as json string

				var relatedLogsParams map[string]any
				// parse url and extract params
				parsedUrl, _ := url.Parse(signoz.Labels["related_logs"])
				queryParams := parsedUrl.Query()
				relatedLogsParams = make(map[string]any)
				for key, values := range queryParams {
					if len(values) == 1 {
						relatedLogsParams[key] = values[0]
					} else {
						relatedLogsParams[key] = values
					}
				}
				// convert to json string
				relatedLogsParamsJson, _ := json.Marshal(relatedLogsParams)
				labels["relatedLogsParams"] = string(relatedLogsParamsJson)
			}

			if signoz.EreAccountID != 0 {
				labels["ereAccountID"] = strconv.Itoa(signoz.EreAccountID)
			}
			if signoz.NumFiring > 0 {
				labels["num_firing"] = strconv.Itoa(signoz.NumFiring)
			}
			if signoz.NumResolved > 0 {
				labels["num_resolved"] = strconv.Itoa(signoz.NumResolved)
			}

			// Add all labels and annotations
			for key, value := range signoz.Labels {
				labels[key] = value
			}
			for key, value := range signoz.Annotations {
				labels[fmt.Sprintf("annotation_%s", key)] = value
			}
		}

	case AlertSourceAzure:
		if azure := alertData.Azure; azure != nil {
			setIfNotEmpty(labels, "resourceName", azure.ResourceName)
			setIfNotEmpty(labels, "resourceType", azure.ResourceType)
			setIfNotEmpty(labels, "resourceGroupName", azure.ResourceGroupName)
			setIfNotEmpty(labels, "subscriptionId", azure.SubscriptionId)
			setIfNotEmpty(labels, "metricName", azure.MetricName)
			setIfNotEmpty(labels, "metricNamespace", azure.MetricNamespace)
			setIfNotEmpty(labels, "operator", azure.Operator)
			setIfNotEmpty(labels, "timeAggregation", azure.TimeAggregation)
			setIfNotEmpty(labels, "windowSize", azure.WindowSize)
			setIfNotEmpty(labels, "portalLink", azure.PortalLink)
			setIfNotEmpty(labels, "creationTime", azure.CreationTime)
			setIfNotEmpty(labels, "dedupKey", azure.DedupKey)
			setIfNotEmpty(labels, "severity", azure.Severity)
			setIfNotEmpty(labels, "conditionType", azure.ConditionType)
			setIfNotEmpty(labels, "contextDescription", azure.ContextDescription)
			setIfNotEmpty(labels, "timestamp", azure.Timestamp)

			if azure.Threshold != 0 {
				labels["threshold"] = fmt.Sprintf("%.1f", azure.Threshold)
			}
			if azure.MetricValue != 0 {
				labels["metricValue"] = fmt.Sprintf("%.1f", azure.MetricValue)
			}
			if azure.MinFailingPeriods != 0 {
				labels["minFailingPeriods"] = strconv.Itoa(azure.MinFailingPeriods)
			}
			if azure.EvaluationPeriods != 0 {
				labels["evaluationPeriods"] = strconv.Itoa(azure.EvaluationPeriods)
			}

			// Add dimensions
			for key, value := range azure.Dimensions {
				labels[fmt.Sprintf("dimension_%s", key)] = value
			}
		}

	case AlertSourceChronosphere:
		if chronosphere := alertData.Chronosphere; chronosphere != nil {
			setIfNotEmpty(labels, "monitorName", chronosphere.MonitorName)
			setIfNotEmpty(labels, "monitorSlug", chronosphere.MonitorSlug)
			setIfNotEmpty(labels, "alertID", chronosphere.AlertID)
			setIfNotEmpty(labels, "environment", chronosphere.Environment)
			setIfNotEmpty(labels, "job", chronosphere.Job)
			setIfNotEmpty(labels, "type", chronosphere.Type)
			setIfNotEmpty(labels, "severity", chronosphere.Severity)
			setIfNotEmpty(labels, "signal", chronosphere.Signal)
			setIfNotEmpty(labels, "sourceURL", chronosphere.SourceURL)
			setIfNotEmpty(labels, "clientURL", chronosphere.ClientURL)
			setIfNotEmpty(labels, "dedupKey", chronosphere.DedupKey)
			setIfNotEmpty(labels, "eventID", chronosphere.EventID)
			setIfNotEmpty(labels, "eventStorageID", chronosphere.EventStorageID)
			setIfNotEmpty(labels, "sourceOrigin", chronosphere.SourceOrigin)
			setIfNotEmpty(labels, "routingKey", chronosphere.RoutingKey)
			setIfNotEmpty(labels, "payloadSource", chronosphere.PayloadSource)
			setIfNotEmpty(labels, "payloadSummary", chronosphere.PayloadSummary)
			setIfNotEmpty(labels, "notificationPolicySlug", chronosphere.NotificationPolicySlug)

			if chronosphere.EreAccountID != 0 {
				labels["ereAccountID"] = strconv.Itoa(chronosphere.EreAccountID)
			}
			if chronosphere.NumAlertingSeries > 0 {
				labels["numAlertingSeries"] = strconv.Itoa(chronosphere.NumAlertingSeries)
			}
			if chronosphere.NumResolvedSeries >= 0 {
				labels["numResolvedSeries"] = strconv.Itoa(chronosphere.NumResolvedSeries)
			}

			// Add all parsed labels
			for key, value := range chronosphere.Labels {
				labels[key] = value
			}
			for key, value := range chronosphere.Annotations {
				labels[fmt.Sprintf("annotation_%s", key)] = value
			}
		}

	case AlertSourceUnknown:
		if generic := alertData.Generic; generic != nil {
			setIfNotEmpty(labels, "description", generic.Description)
			setIfNotEmpty(labels, "message", generic.Message)
			setIfNotEmpty(labels, "firing_text", generic.FiringText)

			// Add all labels and annotations
			for key, value := range generic.Labels {
				labels[key] = value
			}
			for key, value := range generic.Annotations {
				labels[fmt.Sprintf("annotation_%s", key)] = value
			}
		}
	}

	return labels
}

// formatAWSActions formats AWS action ARNs into human-readable descriptions
func formatAWSActions(actions []string) string {
	if len(actions) == 0 {
		return "None"
	}

	var descriptions []string
	for _, action := range actions {
		descriptions = append(descriptions, formatSingleAWSAction(action))
	}

	return strings.Join(descriptions, ", ")
}

// addNormalizedCloudLabels adds normalized labels that are consistent across all cloud providers
func addNormalizedCloudLabels(alertData *AlertData, labels map[string]string) {
	switch alertData.Source {
	case AlertSourceAWS:
		if aws := alertData.AWS; aws != nil {
			// Normalized AWS labels
			labels["nb_alert_source"] = "aws"
			labels["nb_cloud_provider"] = "aws"
			setIfNotEmpty(labels, "nb_cloud_account_id", aws.AWSAccountId)
			setIfNotEmpty(labels, "nb_cloud_region", aws.Region)
			setIfNotEmpty(labels, "nb_alert_name", aws.AlarmName)
			setIfNotEmpty(labels, "nb_alert_state", aws.NewStateValue)
			setIfNotEmpty(labels, "nb_alert_previous_state", aws.OldStateValue)
			setIfNotEmpty(labels, "nb_metric_name", aws.MetricName)
			setIfNotEmpty(labels, "nb_metric_namespace", aws.Namespace)

			if aws.Threshold != 0 {
				labels["nb_alert_threshold"] = fmt.Sprintf("%.1f", aws.Threshold)
			}

			// Add resource information from dimensions
			for key, value := range aws.Dimensions {
				switch strings.ToLower(key) {
				case "instanceid":
					labels["nb_resource_id"] = value
					labels["nb_resource_type"] = "ec2-instance"
				case "autoscalinggroupname":
					labels["nb_resource_id"] = value
					labels["nb_resource_type"] = "autoscaling-group"
				case "loadbalancer":
					labels["nb_resource_id"] = value
					labels["nb_resource_type"] = "load-balancer"
				case "dbinstanceidentifier":
					labels["nb_resource_id"] = value
					labels["nb_resource_type"] = "rds-instance"
				}
			}
		}

	case AlertSourceAzure:
		if azure := alertData.Azure; azure != nil {
			// Normalized Azure labels
			labels["nb_alert_source"] = "azure"
			labels["nb_cloud_provider"] = "azure"
			setIfNotEmpty(labels, "nb_cloud_account_id", azure.SubscriptionId)
			setIfNotEmpty(labels, "nb_alert_name", azure.MetricName)
			setIfNotEmpty(labels, "nb_resource_id", azure.ResourceName)
			setIfNotEmpty(labels, "nb_resource_type", azure.ResourceType)
			setIfNotEmpty(labels, "nb_resource_group", azure.ResourceGroupName)
			setIfNotEmpty(labels, "nb_metric_name", azure.MetricName)
			setIfNotEmpty(labels, "nb_metric_namespace", azure.MetricNamespace)

			if azure.Threshold != 0 {
				labels["nb_alert_threshold"] = fmt.Sprintf("%.1f", azure.Threshold)
			}
			if azure.MetricValue != 0 {
				labels["nb_alert_current_value"] = fmt.Sprintf("%.1f", azure.MetricValue)
			}

			// Note: Azure region is not directly available in this payload structure
			// It would need to be extracted from the full resource ID if available in future enhancements
		}

	case AlertSourceChronosphere:
		if chronosphere := alertData.Chronosphere; chronosphere != nil {
			// Normalized Chronosphere labels
			labels["nb_alert_source"] = "chronosphere"
			setIfNotEmpty(labels, "nb_alert_name", chronosphere.MonitorName)
			setIfNotEmpty(labels, "nb_alert_environment", chronosphere.Environment)
			setIfNotEmpty(labels, "nb_alert_job", chronosphere.Job)
			setIfNotEmpty(labels, "nb_alert_severity", chronosphere.Severity)
			setIfNotEmpty(labels, "nb_monitor_slug", chronosphere.MonitorSlug)
			setIfNotEmpty(labels, "nb_alert_id", chronosphere.AlertID)

			if chronosphere.NumAlertingSeries > 0 {
				labels["nb_alert_firing_count"] = strconv.Itoa(chronosphere.NumAlertingSeries)
			}

			// Extract cloud info from environment if available
			if chronosphere.Environment != "" {
				switch chronosphere.Environment {
				case "prod", "production":
					labels["nb_environment"] = "production"
				case "stage", "staging":
					labels["nb_environment"] = "staging"
				case "dev", "development":
					labels["nb_environment"] = "development"
				default:
					labels["nb_environment"] = chronosphere.Environment
				}
			}
		}

	case AlertSourceGrafana:
		if grafana := alertData.Grafana; grafana != nil {
			// Normalized Grafana labels
			labels["nb_alert_source"] = "grafana"
			setIfNotEmpty(labels, "nb_alert_name", grafana.AlertName)
			setIfNotEmpty(labels, "nb_alert_instance", grafana.Instance)
			setIfNotEmpty(labels, "nb_alert_job", grafana.Job)
			setIfNotEmpty(labels, "nb_alert_rule_id", grafana.RuleID)

			if grafana.NumFiring > 0 {
				labels["nb_alert_firing_count"] = strconv.Itoa(grafana.NumFiring)
			}

			// Merge raw firing labels so resolveSubjectFromLabels can use them
			// (app_id, namespace, deployment, statefulset, pod, etc.)
			for key, value := range grafana.Labels {
				if _, exists := labels[key]; !exists && value != "" {
					labels[key] = value
				}
			}

			// Extract cloud info from labels if available
			if cloudProvider, exists := grafana.Labels["cloud_provider"]; exists {
				labels["nb_cloud_provider"] = cloudProvider
			}
			if accountId, exists := grafana.Labels["account_id"]; exists {
				labels["nb_cloud_account_id"] = accountId
			}
			if region, exists := grafana.Labels["region"]; exists {
				labels["nb_cloud_region"] = region
			}
		}

	case AlertSourceSigNoz:
		if signoz := alertData.SigNoz; signoz != nil {
			// Normalized SigNoz labels
			labels["nb_alert_source"] = "signoz"
			setIfNotEmpty(labels, "nb_alert_name", signoz.AlertName)
			setIfNotEmpty(labels, "nb_alert_rule_id", signoz.RuleID)
			setIfNotEmpty(labels, "nb_alert_tag", signoz.Tag)

			if signoz.NumFiring > 0 {
				labels["nb_alert_firing_count"] = strconv.Itoa(signoz.NumFiring)
			}

			// Extract cloud info from labels if available
			if cloudProvider, exists := signoz.Labels["cloud_provider"]; exists {
				labels["nb_cloud_provider"] = cloudProvider
			}
			if accountId, exists := signoz.Labels["account_id"]; exists {
				labels["nb_cloud_account_id"] = accountId
			}
			if region, exists := signoz.Labels["region"]; exists {
				labels["nb_cloud_region"] = region
			}
		}

	case AlertSourceUnknown:
		// For unknown sources, try to extract from generic labels
		if generic := alertData.Generic; generic != nil {
			labels["nb_alert_source"] = "unknown"

			// Merge raw firing labels so resolveSubjectFromLabels can use them
			for key, value := range generic.Labels {
				if _, exists := labels[key]; !exists && value != "" {
					labels[key] = value
				}
			}

			// Try to detect cloud provider from available labels
			for key, value := range generic.Labels {
				switch strings.ToLower(key) {
				case "cloud_provider", "provider":
					labels["nb_cloud_provider"] = value
				case "account_id", "accountid", "subscription_id":
					labels["nb_cloud_account_id"] = value
				case "region":
					labels["nb_cloud_region"] = value
				case "alertname", "alert_name":
					labels["nb_alert_name"] = value
				}
			}
		}
	}
}

// formatSingleAWSAction converts an AWS ARN into a readable description
func formatSingleAWSAction(arn string) string {
	// Parse AWS ARN: arn:aws:service:region:account:resource
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return arn // Return as-is if not a valid ARN
	}

	service := parts[2]
	resource := parts[5]

	switch service {
	case "sns":
		return fmt.Sprintf("SNS: %s", resource)
	case "lambda":
		return fmt.Sprintf("Lambda: %s", resource)
	case "autoscaling":
		// Extract policy name from resource like "scalingPolicy:policyId:autoScalingGroupName/groupName:policyName/policyName"
		if strings.Contains(resource, "policyName/") {
			policyParts := strings.Split(resource, "policyName/")
			if len(policyParts) > 1 {
				return fmt.Sprintf("Auto Scaling: %s", policyParts[1])
			}
		}
		return fmt.Sprintf("Auto Scaling: %s", resource)
	case "ec2":
		return fmt.Sprintf("EC2: %s", resource)
	case "ssm":
		return fmt.Sprintf("Systems Manager: %s", resource)
	default:
		return fmt.Sprintf("%s: %s", strings.ToUpper(service), resource)
	}
}

// extractCommonAlertFields extracts fields common to all alert sources
func extractCommonAlertFields(incident *Incident, bodyDetails *BodyDetails) CommonAlertFields {
	if bodyDetails == nil {
		return CommonAlertFields{}
	}
	cef := bodyDetails.PdCefPayload

	return CommonAlertFields{
		DedupKey:        getFirstNonEmpty(cef.DedupKey, bodyDetails.DedupKey),
		Severity:        getFirstNonEmpty(cef.Severity, bodyDetails.Severity),
		EventID:         cef.EventID,
		RoutingKey:      getFirstNonEmpty(cef.RoutingKey, bodyDetails.RoutingKey),
		Client:          getFirstNonEmpty(cef.Client, bodyDetails.Client),
		SourceComponent: cef.SourceComponent,
		EreAccountID:    bodyDetails.EreAccountID,
		EventStorageID:  bodyDetails.EventStorageID,
	}
}

// Pre-compiled regexes for parseAWSCloudWatchEmail – hoisted to package level
// to avoid recompiling on every webhook call.
var (
	reAWSAlarmName       = regexp.MustCompile(`(?m)^- Name:\s+(.+?)\r?\n`)
	reAWSVpnID           = regexp.MustCompile(`\[(vpn-[a-f0-9]+)\]`)
	reAWSDescription     = regexp.MustCompile(`(?m)^- Description:\s+(.+?)\r?\n`)
	reAWSStateChange     = regexp.MustCompile(`(?m)^- State Change:\s+(.+?)(?:\s*->\s*(.+?))?\r?\n`)
	reAWSStateReason     = regexp.MustCompile(`(?m)^- Reason for State Change:\s+(.+?)\r?\n`)
	reAWSTimestamp       = regexp.MustCompile(`(?m)^- Timestamp:\s+(.+?)\r?\n`)
	reAWSAccount         = regexp.MustCompile(`(?m)^- AWS Account:\s+(\d+)\r?\n`)
	reAWSAlarmArn        = regexp.MustCompile(`(?m)^- Alarm Arn:\s+(arn:aws:cloudwatch:([^:]+):[^:]+:alarm:[^\r\n]+)\r?\n`)
	reAWSThresholdInfo   = regexp.MustCompile(`(?m)when the metric is (\w+)(?:\s+([\d.]+))?`)
	reAWSPeriod          = regexp.MustCompile(`(?m)period\(s\) of (\d+) seconds`)
	reAWSEvalPeriods     = regexp.MustCompile(`(?m)for at least (\d+) of the last (\d+) period`)
	reAWSMetricNamespace = regexp.MustCompile(`(?m)^- MetricNamespace:\s+(.+?)\r?\n`)
	reAWSMetricName      = regexp.MustCompile(`(?m)^- MetricName:\s+(.+?)\r?\n`)
	reAWSDimensions      = regexp.MustCompile(`(?m)^- Dimensions:\s+\[([^\]]+)\]`)
	reAWSStatistic       = regexp.MustCompile(`(?m)^- Statistic:\s+(.+?)\r?\n`)
	reAWSAlarmActions    = regexp.MustCompile(`(?m)^- ALARM:\s+\[(.+?)\]`)
	reAWSOKActions       = regexp.MustCompile(`(?m)^- OK:\s+\[(.+?)\]`)
	reAWSConsoleURL      = regexp.MustCompile(`(?m)https://[^\s]+console\.aws\.amazon\.com[^\s]+`)
)

// extractAWSAlertFields extracts AWS CloudWatch specific fields
// parseAWSCloudWatchEmail parses AWS CloudWatch alarm notification email text
func parseAWSCloudWatchEmail(emailText string) *AWSAlertFields {
	if emailText == "" {
		return nil
	}

	awsFields := &AWSAlertFields{
		Dimensions: make(map[string]string),
	}

	// Parse alarm details using pre-compiled regex patterns (see var block above).
	// Note: Using \r?\n instead of $ to handle both \n and \r\n line endings

	// Extract Alarm Name
	if matches := reAWSAlarmName.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.AlarmName = strings.TrimSpace(matches[1])

		// Extract VPN connection ID from alarm name if present (e.g., "vpn-029fb8df945ac1a52")
		// This is useful for VPN tunnel alerts where the VPN ID is the resource, not the tunnel IP
		if vpnMatches := reAWSVpnID.FindStringSubmatch(awsFields.AlarmName); len(vpnMatches) > 1 {
			// Store VPN ID in dimensions for later use
			awsFields.Dimensions["VpnConnectionId"] = vpnMatches[1]
		}
	}

	// Extract Description
	if matches := reAWSDescription.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.AlarmDescription = strings.TrimSpace(matches[1])
	}

	// Extract State Change
	if matches := reAWSStateChange.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.OldStateValue = strings.TrimSpace(matches[1])
		if len(matches) > 2 && matches[2] != "" {
			awsFields.NewStateValue = strings.TrimSpace(matches[2])
		}
	}

	// Extract Reason for State Change
	if matches := reAWSStateReason.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.NewStateReason = strings.TrimSpace(matches[1])
	}

	// Extract Timestamp
	if matches := reAWSTimestamp.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.StateChangeTime = strings.TrimSpace(matches[1])
	}

	// Extract AWS Account
	if matches := reAWSAccount.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.AWSAccountId = strings.TrimSpace(matches[1])
	}

	// Extract Alarm ARN and Region from it
	if matches := reAWSAlarmArn.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.AlarmArn = strings.TrimSpace(matches[1])
		if len(matches) > 2 {
			awsFields.Region = strings.TrimSpace(matches[2])
		}
	}

	// Extract Threshold and Comparison Operator in a single pass (both appear on the same line)
	if matches := reAWSThresholdInfo.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.ComparisonOperator = strings.TrimSpace(matches[1])
		if len(matches) > 2 && matches[2] != "" {
			if threshold, err := strconv.ParseFloat(matches[2], 64); err == nil {
				awsFields.Threshold = threshold
			}
		}
	}

	// Extract Period
	if matches := reAWSPeriod.FindStringSubmatch(emailText); len(matches) > 1 {
		if period, err := strconv.Atoi(matches[1]); err == nil {
			awsFields.Period = period
		}
	}

	// Extract Evaluation Periods
	if matches := reAWSEvalPeriods.FindStringSubmatch(emailText); len(matches) > 2 {
		if evalPeriods, err := strconv.Atoi(matches[2]); err == nil {
			awsFields.EvaluationPeriods = evalPeriods
		}
	}

	// Extract MetricNamespace
	if matches := reAWSMetricNamespace.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.Namespace = strings.TrimSpace(matches[1])
	}

	// Extract MetricName
	if matches := reAWSMetricName.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.MetricName = strings.TrimSpace(matches[1])
	}

	// Extract Dimensions
	// Handle both \n and \r\n line endings
	if matches := reAWSDimensions.FindStringSubmatch(emailText); len(matches) > 1 {
		// Parse dimensions like "TunnelIpAddress = 52.44.137.246" or "InstanceId = i-12345, Name = test"
		dimensionsText := strings.TrimSpace(matches[1])
		dimensionPairs := strings.Split(dimensionsText, ",")
		for _, pair := range dimensionPairs {
			if parts := strings.Split(pair, "="); len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				awsFields.Dimensions[key] = value
			}
		}
	}

	// Extract Statistic
	if matches := reAWSStatistic.FindStringSubmatch(emailText); len(matches) > 1 {
		awsFields.Statistic = strings.TrimSpace(matches[1])
	}

	// Extract State Change Actions
	// Extract ALARM actions
	if matches := reAWSAlarmActions.FindStringSubmatch(emailText); len(matches) > 1 {
		actionsText := strings.TrimSpace(matches[1])
		if actionsText != "" {
			// Actions can be separated by commas or newlines
			actions := strings.Split(actionsText, ",")
			for _, action := range actions {
				trimmedAction := strings.TrimSpace(action)
				if trimmedAction != "" {
					awsFields.AlarmActions = append(awsFields.AlarmActions, trimmedAction)
				}
			}
		}
	}

	// Extract OK actions
	if matches := reAWSOKActions.FindStringSubmatch(emailText); len(matches) > 1 {
		actionsText := strings.TrimSpace(matches[1])
		if actionsText != "" {
			// Actions can be separated by commas or newlines
			actions := strings.Split(actionsText, ",")
			for _, action := range actions {
				trimmedAction := strings.TrimSpace(action)
				if trimmedAction != "" {
					awsFields.OKActions = append(awsFields.OKActions, trimmedAction)
				}
			}
		}
	}

	// Extract console URL
	if matches := reAWSConsoleURL.FindStringSubmatch(emailText); len(matches) > 0 {
		awsFields.ClientURL = strings.TrimSpace(matches[0])
	}

	return awsFields
}

// parseAWSTimestamp parses AWS CloudWatch timestamp format
// Example: "Saturday 25 October, 2025 12:57:51 UTC"
func parseAWSTimestamp(timestampStr string) (time.Time, error) {
	// AWS CloudWatch uses format: "Monday 02 January, 2006 15:04:05 UTC"
	layouts := []string{
		"Monday 02 January, 2006 15:04:05 MST", // Full format with timezone
		"Monday 2 January, 2006 15:04:05 MST",  // Day without leading zero
		"Monday 02 January 2006 15:04:05 MST",  // Without comma after year
		"Monday 2 January 2006 15:04:05 MST",   // Day without leading zero, no comma
		time.RFC3339,                           // Fallback to RFC3339
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, timestampStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", timestampStr)
}

func extractAWSAlertFields(incident *Incident, bodyDetails *BodyDetails) *AWSAlertFields {
	awsFields := &AWSAlertFields{
		Dimensions: make(map[string]string),
	}

	// Try to parse from plain text email format first (for email-based alerts)
	if incident != nil {
		emailText := getStringDetailsFromBody(incident)
		if emailText != "" && strings.Contains(emailText, "Amazon CloudWatch Alarm") {
			parsedFields := parseAWSCloudWatchEmail(emailText)
			if parsedFields != nil {
				return parsedFields
			}
		}
	}

	// Fall back to structured JSON format (for API-based alerts)
	if bodyDetails == nil {
		return awsFields
	}
	cef := bodyDetails.PdCefPayload

	// Extract AWS alarm details from CEF details section
	if cef.Details != nil {
		if alarmName, ok := cef.Details["AlarmName"].(string); ok {
			awsFields.AlarmName = alarmName
		}
		if region, ok := cef.Details["Region"].(string); ok {
			awsFields.Region = region
		}
		if accountId, ok := cef.Details["AWSAccountId"].(string); ok {
			awsFields.AWSAccountId = accountId
		}
		if newState, ok := cef.Details["NewStateValue"].(string); ok {
			awsFields.NewStateValue = newState
		}
		if oldState, ok := cef.Details["OldStateValue"].(string); ok {
			awsFields.OldStateValue = oldState
		}
		if alarmArn, ok := cef.Details["AlarmArn"].(string); ok {
			awsFields.AlarmArn = alarmArn
		}
		if newStateReason, ok := cef.Details["NewStateReason"].(string); ok {
			awsFields.NewStateReason = newStateReason
		}
		if stateChangeTime, ok := cef.Details["StateChangeTime"].(string); ok {
			awsFields.StateChangeTime = stateChangeTime
		}
		if alarmDesc, ok := cef.Details["AlarmDescription"].(string); ok {
			awsFields.AlarmDescription = alarmDesc
		}
	}

	// Extract client_url from the main body details section
	if bodyDetails.ClientURL != "" {
		awsFields.ClientURL = bodyDetails.ClientURL
	}

	if cef.Details != nil {
		// Extract alarm actions
		if actions, ok := cef.Details["AlarmActions"].([]interface{}); ok {
			for _, action := range actions {
				if actionStr, ok := action.(string); ok {
					awsFields.AlarmActions = append(awsFields.AlarmActions, actionStr)
				}
			}
		}

		// Extract OK actions
		if okActions, ok := cef.Details["OKActions"].([]interface{}); ok {
			for _, action := range okActions {
				if actionStr, ok := action.(string); ok {
					awsFields.OKActions = append(awsFields.OKActions, actionStr)
				}
			}
		}

		// Extract trigger details
		if trigger, ok := cef.Details["Trigger"].(map[string]interface{}); ok {
			if metricName, ok := trigger["MetricName"].(string); ok {
				awsFields.MetricName = metricName
			}
			if namespace, ok := trigger["Namespace"].(string); ok {
				awsFields.Namespace = namespace
			}
			if statistic, ok := trigger["Statistic"].(string); ok {
				awsFields.Statistic = statistic
			}
			if compOp, ok := trigger["ComparisonOperator"].(string); ok {
				awsFields.ComparisonOperator = compOp
			}
			if threshold, ok := trigger["Threshold"].(float64); ok {
				awsFields.Threshold = threshold
			}
			if period, ok := trigger["Period"].(float64); ok {
				awsFields.Period = int(period)
			}
			if evalPeriods, ok := trigger["EvaluationPeriods"].(float64); ok {
				awsFields.EvaluationPeriods = int(evalPeriods)
			}

			// Extract dimensions
			if dimensions, ok := trigger["Dimensions"].([]interface{}); ok {
				for _, dim := range dimensions {
					if dimMap, ok := dim.(map[string]interface{}); ok {
						if name, ok := dimMap["name"].(string); ok {
							if value, ok := dimMap["value"].(string); ok {
								awsFields.Dimensions[name] = value
							}
						}
					}
				}
			}
		}
	}

	return awsFields
}

// extractGrafanaAlertFields extracts Grafana specific fields
func extractGrafanaAlertFields(incident *Incident, bodyDetails *BodyDetails) *GrafanaAlertFields {
	grafanaFields := &GrafanaAlertFields{
		Labels:      make(map[string]string),
		Annotations: make(map[string]string),
	}

	// Extract from firing field
	firingText := findFiringText(incident, bodyDetails)
	if firingText != "" {
		firingLabels := parseFiringLabels(firingText)

		// Separate labels and annotations
		for key, value := range firingLabels {
			if strings.HasPrefix(key, "annotation_") {
				grafanaFields.Annotations[strings.TrimPrefix(key, "annotation_")] = value
			} else {
				grafanaFields.Labels[key] = value
			}
		}

		// Extract specific Grafana fields
		grafanaFields.AlertName = grafanaFields.Labels["alertname"]
		grafanaFields.GrafanaFolder = grafanaFields.Labels["grafana_folder"]
		grafanaFields.Instance = grafanaFields.Labels["instance"]
		grafanaFields.Job = grafanaFields.Labels["job"]
		grafanaFields.RuleID = grafanaFields.Labels["ruleId"]
		grafanaFields.RuleSource = grafanaFields.Labels["ruleSource"]
		grafanaFields.AlertValue = grafanaFields.Labels["alert_value"]
		grafanaFields.SourceURL = grafanaFields.Labels["source_url"]
		grafanaFields.Description = grafanaFields.Annotations["description"]
		grafanaFields.Summary = grafanaFields.Annotations["summary"]
		grafanaFields.RelatedLogs = grafanaFields.Annotations["related_logs"]
	}

	// Extract firing counts using the bodyDetails parameter
	if bodyDetails == nil {
		return grafanaFields
	}
	cef := bodyDetails.PdCefPayload
	if cef.Details != nil {
		if numFiring, ok := cef.Details["num_firing"].(string); ok {
			if count, err := strconv.Atoi(numFiring); err == nil {
				grafanaFields.NumFiring = count
			}
		}
		if numResolved, ok := cef.Details["num_resolved"].(string); ok {
			if count, err := strconv.Atoi(numResolved); err == nil {
				grafanaFields.NumResolved = count
			}
		}
	}

	// Extract CEF-level fields
	grafanaFields.ClientURL = cef.ClientURL
	grafanaFields.DedupKey = cef.DedupKey
	grafanaFields.EventID = cef.EventID
	grafanaFields.SourceOrigin = cef.SourceOrigin
	grafanaFields.RoutingKey = cef.RoutingKey
	grafanaFields.ServiceGroup = cef.ServiceGroup

	// Extract additional fields from body details
	grafanaFields.EventStorageID = bodyDetails.EventStorageID

	// Parse URLs from firing text annotations
	extractGrafanaURLsFromFiringText(grafanaFields, firingText)

	return grafanaFields
}

// extractGrafanaURLsFromFiringText extracts URLs from Grafana firing text annotations
func extractGrafanaURLsFromFiringText(grafanaFields *GrafanaAlertFields, firingText string) {
	// Extract specific URLs from annotations section
	lines := strings.Split(firingText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Source: ") {
			grafanaFields.SourceURL = strings.TrimPrefix(line, "Source: ")
			// Extract alert rule UID and org ID from source URL
			if strings.Contains(grafanaFields.SourceURL, "orgId=") {
				if parts := strings.Split(grafanaFields.SourceURL, "orgId="); len(parts) > 1 {
					if orgParts := strings.Split(parts[1], "&"); len(orgParts) > 0 {
						grafanaFields.OrgID = orgParts[0]
					}
				}
			}
			if strings.Contains(grafanaFields.SourceURL, "/grafana/") {
				if parts := strings.Split(grafanaFields.SourceURL, "/"); len(parts) > 0 {
					for i, part := range parts {
						if part == "grafana" && i+1 < len(parts) && parts[i+1] != "" && parts[i+1] != "view" {
							grafanaFields.AlertRuleUID = parts[i+1]
							break
						}
					}
				}
			}
		} else if strings.HasPrefix(line, "Silence: ") {
			grafanaFields.SilenceURL = strings.TrimPrefix(line, "Silence: ")
		} else if strings.HasPrefix(line, "Dashboard: ") {
			grafanaFields.DashboardURL = strings.TrimPrefix(line, "Dashboard: ")
		} else if strings.HasPrefix(line, "Panel: ") {
			grafanaFields.PanelURL = strings.TrimPrefix(line, "Panel: ")
		}
	}
}

// Helper functions (DRY utilities)

// setIfNotEmpty sets the first non-empty value for a key
func setIfNotEmpty(labels map[string]string, key string, values ...string) {
	for _, value := range values {
		if value != "" {
			labels[key] = value
			return
		}
	}
}

// deriveAWSServiceName converts AWS namespace to service name format
func deriveAWSServiceName(namespace string) string {
	// Map AWS CloudWatch namespaces to service names
	// Format: AWS/ServiceName -> amazonservicename
	namespaceMap := map[string]string{
		"AWS/EC2":               "amazonec2",
		"AWS/RDS":               "amazonrds",
		"AWS/ELB":               "elasticloadbalancing",
		"AWS/ApplicationELB":    "elasticloadbalancing",
		"AWS/NetworkELB":        "elasticloadbalancing",
		"AWS/Lambda":            "awslambda",
		"AWS/DynamoDB":          "amazondynamodb",
		"AWS/S3":                "amazons3",
		"AWS/CloudFront":        "amazoncloudfront",
		"AWS/ECS":               "amazonecs",
		"AWS/EKS":               "amazoneks",
		"AWS/ElastiCache":       "amazonelasticache",
		"AWS/Redshift":          "amazonredshift",
		"AWS/SQS":               "amazonsqs",
		"AWS/SNS":               "amazonsns",
		"AWS/Kinesis":           "amazonkinesis",
		"AWS/VPN":               "ec2", // VPN is managed via EC2 API
		"AWS/VPC":               "ec2", // VPC resources managed via EC2 API
		"AWS/Route53":           "amazonroute53",
		"AWS/ApiGateway":        "apigateway",
		"AWS/AutoScaling":       "autoscaling",
		"AWS/CloudWatch":        "cloudwatch",
		"AWS/Events":            "events",
		"AWS/Logs":              "logs",
		"AWS/States":            "states",
		"AWS/ES":                "es",
		"AWS/ElasticBeanstalk":  "elasticbeanstalk",
		"AWS/NATGateway":        "ec2", // NAT Gateway managed via EC2 API
		"AWS/TransitGateway":    "ec2", // Transit Gateway managed via EC2 API
		"AWS/NetworkFirewall":   "network-firewall",
		"AWS/GlobalAccelerator": "globalaccelerator",
		"AWS/AppSync":           "appsync",
		"AWS/Cognito":           "cognito",
		"AWS/IoT":               "iot",
		"AWS/KMS":               "kms",
		"AWS/SES":               "ses",
		"AWS/WorkSpaces":        "workspaces",
		"AWS/FSx":               "fsx",
		"AWS/EFS":               "elasticfilesystem",
		"AWS/Backup":            "backup",
		"AWS/StorageGateway":    "storagegateway",
		"AWS/Transfer":          "transfer",
		"AWS/MediaConnect":      "mediaconnect",
		"AWS/MediaLive":         "medialive",
		"AWS/MediaPackage":      "mediapackage",
		"AWS/Glue":              "glue",
		"AWS/EMR":               "elasticmapreduce",
		"AWS/SageMaker":         "sagemaker",
		"AWS/Batch":             "batch",
		"AWS/Step Functions":    "states",
		"AWS/DirectConnect":     "directconnect",
		"AWS/DMS":               "dms",
		"AWS/DAX":               "dax",
		"AWS/DocDB":             "docdb",
		"AWS/Neptune":           "neptune",
		"AWS/QLDB":              "qldb",
		"AWS/Timestream":        "timestream",
		"AWS/Cassandra":         "cassandra",
	}

	if serviceName, ok := namespaceMap[namespace]; ok {
		return serviceName
	}

	// Fallback: convert AWS/ServiceName to lowercase amazonservicename
	if strings.HasPrefix(namespace, "AWS/") {
		serviceName := strings.TrimPrefix(namespace, "AWS/")
		return "amazon" + strings.ToLower(serviceName)
	}

	return strings.ToLower(namespace)
}

// getFirstNonEmpty returns the first non-empty string from the given values
func getFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// findFiringText searches for firing text in common locations
func findFiringText(incident *Incident, bodyDetails *BodyDetails) string {
	if bodyDetails == nil {
		return ""
	}

	// Check CEF details
	if bodyDetails.PdCefPayload.Details != nil {
		if firing, ok := bodyDetails.PdCefPayload.Details["firing"].(string); ok && firing != "" {
			return firing
		}
	}

	// Check nested body details (details.details.firing)
	if bodyDetails.Details.Firing != "" {
		return bodyDetails.Details.Firing
	}

	// Check root-level firing (flat body format where firing is at body.details.firing)
	if bodyDetails.Firing != "" {
		return bodyDetails.Firing
	}

	// Check payload custom details
	if bodyDetails.Payload.CustomDetails.Firing != "" {
		return bodyDetails.Payload.CustomDetails.Firing
	}

	return ""
}

// parseFiringLabels parses labels from the firing field text
func parseFiringLabels(firingText string) map[string]string {
	labels := make(map[string]string)

	lines := strings.Split(firingText, "\n")
	inLabelsSection := false
	inAnnotationsSection := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "Labels:" {
			inLabelsSection = true
			inAnnotationsSection = false
			continue
		}

		if line == "Annotations:" {
			inLabelsSection = false
			inAnnotationsSection = true
			continue
		}

		if line == "Source:" || strings.HasPrefix(line, "Source:") {
			inLabelsSection = false
			inAnnotationsSection = false
			// Extract source URL
			if sourceURL, found := strings.CutPrefix(line, "Source:"); found {
				sourceURL = strings.TrimSpace(sourceURL)
				if sourceURL != "" {
					labels["source_url"] = sourceURL
				}
			}
			continue
		}

		// Parse label/annotation lines (format: " - key = value")
		if (inLabelsSection || inAnnotationsSection) && strings.HasPrefix(line, "- ") {
			labelLine := strings.TrimPrefix(line, "- ")
			parts := strings.SplitN(labelLine, " = ", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				labels[key] = value
			}
		}

		// Extract Value line
		if strings.HasPrefix(line, "Value:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Value:"))
			if value != "" {
				labels["alert_value"] = value
			}
		}
	}

	return labels
}

// createDynamicDescriptionFromAlertData creates efficient, source-aware markdown description from structured data
func createDynamicDescriptionFromAlertData(alertData *AlertData, incident *Incident) string {
	var desc strings.Builder

	// Header - use incident title if source is unknown, otherwise use source type
	var title string
	if alertData.Source == AlertSourceUnknown {
		title = incident.Title
	} else {
		title = fmt.Sprintf("%s Alert", string(alertData.Source))
	}
	fmt.Fprintf(&desc, "# %s - Incident #%d\n\n", title, incident.IncidentNumber)

	// Basic info
	writeSection(&desc, "Incident Details", buildBasicInfoFromAlertData(alertData, incident))

	// Source-specific content
	switch alertData.Source {
	case AlertSourceAWS:
		if alertData.AWS != nil {
			writeSection(&desc, "AWS CloudWatch Details", buildAWSDetailsFromStruct(alertData.AWS))
			if alertData.AWS.NewStateReason != "" {
				writeSection(&desc, "State Change Details", []string{
					fmt.Sprintf("- **Reason**: %s\n", alertData.AWS.NewStateReason),
				})
			}
		}
	case AlertSourceGrafana:
		if alertData.Grafana != nil {
			writeSection(&desc, "Grafana Alert Details", buildGrafanaDetailsFromStruct(alertData.Grafana))
			if len(alertData.Grafana.Annotations) > 0 {
				writeSection(&desc, "Alert Annotations", buildAnnotationsFromMap(alertData.Grafana.Annotations))
			}
		}
	case AlertSourceSigNoz:
		if alertData.SigNoz != nil {
			writeSection(&desc, "SigNoz Alert Details", buildSigNozDetailsFromStruct(alertData.SigNoz))
			if len(alertData.SigNoz.Annotations) > 0 {
				writeSection(&desc, "Alert Annotations", buildAnnotationsFromMap(alertData.SigNoz.Annotations))
			}
		}
	case AlertSourceAzure:
		if alertData.Azure != nil {
			writeSection(&desc, "Azure Monitor Details", buildAzureDetailsFromStruct(alertData.Azure))
		}
	case AlertSourceChronosphere:
		if alertData.Chronosphere != nil {
			writeSection(&desc, "Chronosphere Monitor Details", buildChronosphereDetailsFromStruct(alertData.Chronosphere))
		}
	case AlertSourceUnknown:
		if alertData.Generic != nil {
			writeSection(&desc, "Alert Details", buildGenericDetailsFromStruct(alertData.Generic))
		}
		// Include raw string content when available
		if stringDetails := getStringDetailsFromBody(incident); stringDetails != "" {
			writeSection(&desc, "Raw Alert Content", []string{
				fmt.Sprintf("```\n%s\n```", stringDetails),
			})
		}
	}

	// Links
	writeSection(&desc, "Links", buildLinksFromAlertData(alertData, incident))

	// Custom fields (if any)
	if len(incident.CustomFields) > 0 {
		writeSection(&desc, "Custom Fields", buildCustomFields(incident.CustomFields))
	}

	return desc.String()
}

// Helper functions for DRY description building

func writeSection(desc *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(desc, "## %s\n", title)
	for _, item := range items {
		desc.WriteString(item)
	}
	desc.WriteString("\n")
}

// New struct-based description builders

func buildBasicInfoFromAlertData(alertData *AlertData, incident *Incident) []string {
	items := []string{
		fmt.Sprintf("- **Status**: %s\n", incident.Status),
		fmt.Sprintf("- **Urgency**: %s\n", incident.Urgency),
		fmt.Sprintf("- **Created**: %s\n", incident.CreatedAt),
		fmt.Sprintf("- **Service**: %s\n", incident.Service.Summary),
	}

	if incident.ResolvedAt != "" {
		items = append(items, fmt.Sprintf("- **Resolved**: %s\n", incident.ResolvedAt))
	}
	if incident.Priority.Name != "" {
		items = append(items, fmt.Sprintf("- **Priority**: %s\n", incident.Priority.Name))
	}
	if alertData.Common.Severity != "" {
		items = append(items, fmt.Sprintf("- **Severity**: %s\n", alertData.Common.Severity))
	}

	return items
}

func buildAWSDetailsFromStruct(aws *AWSAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Alarm", aws.AlarmName)
	addIfNotEmpty("Region", aws.Region)
	addIfNotEmpty("Account ID", aws.AWSAccountId)
	addIfNotEmpty("Current State", aws.NewStateValue)
	addIfNotEmpty("Previous State", aws.OldStateValue)
	addIfNotEmpty("Metric", aws.MetricName)
	addIfNotEmpty("Namespace", aws.Namespace)
	addIfNotEmpty("Statistic", aws.Statistic)
	addIfNotEmpty("Condition", aws.ComparisonOperator)
	addIfNotEmpty("State Changed At", aws.StateChangeTime)

	if aws.ClientURL != "" {
		items = append(items, fmt.Sprintf("- **AWS Console**: [View Alarm](%s)\n", aws.ClientURL))
	}

	if aws.Threshold != 0 {
		items = append(items, fmt.Sprintf("- **Threshold**: %.1f\n", aws.Threshold))
	}
	if aws.Period != 0 {
		items = append(items, fmt.Sprintf("- **Period**: %d seconds\n", aws.Period))
	}
	if aws.EvaluationPeriods != 0 {
		items = append(items, fmt.Sprintf("- **Evaluation Periods**: %d\n", aws.EvaluationPeriods))
	}

	// Add dimensions
	for key, value := range aws.Dimensions {
		items = append(items, fmt.Sprintf("- **%s**: %s\n", key, value))
	}

	// Add actions with details
	if len(aws.AlarmActions) > 0 {
		items = append(items, fmt.Sprintf("- **Alarm Actions**: %s\n", formatAWSActions(aws.AlarmActions)))
	}
	if len(aws.OKActions) > 0 {
		items = append(items, fmt.Sprintf("- **OK Actions**: %s\n", formatAWSActions(aws.OKActions)))
	}

	return items
}

func buildGrafanaDetailsFromStruct(grafana *GrafanaAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Alert Name", grafana.AlertName)
	addIfNotEmpty("Folder", grafana.GrafanaFolder)
	addIfNotEmpty("Instance", grafana.Instance)
	addIfNotEmpty("Job", grafana.Job)
	addIfNotEmpty("Rule ID", grafana.RuleID)
	addIfNotEmpty("Alert Value", grafana.AlertValue)
	addIfNotEmpty("Source Origin", grafana.SourceOrigin)
	addIfNotEmpty("Service Group", grafana.ServiceGroup)
	addIfNotEmpty("Alert Rule UID", grafana.AlertRuleUID)
	addIfNotEmpty("Organization ID", grafana.OrgID)

	if grafana.NumFiring > 0 {
		items = append(items, fmt.Sprintf("- **Firing Alerts**: %d\n", grafana.NumFiring))
	}
	if grafana.NumResolved > 0 {
		items = append(items, fmt.Sprintf("- **Resolved Alerts**: %d\n", grafana.NumResolved))
	}

	// Add URLs as clickable links
	if grafana.ClientURL != "" {
		items = append(items, fmt.Sprintf("- **Grafana**: [View Dashboard](%s)\n", grafana.ClientURL))
	}
	if grafana.DashboardURL != "" {
		items = append(items, fmt.Sprintf("- **Dashboard**: [View Alert Dashboard](%s)\n", grafana.DashboardURL))
	}
	if grafana.PanelURL != "" {
		items = append(items, fmt.Sprintf("- **Panel**: [View Alert Panel](%s)\n", grafana.PanelURL))
	}
	if grafana.SilenceURL != "" {
		items = append(items, fmt.Sprintf("- **Silence**: [Silence Alert](%s)\n", grafana.SilenceURL))
	}

	return items
}

func buildSigNozDetailsFromStruct(signoz *SigNozAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Alert Name", signoz.AlertName)
	addIfNotEmpty("Description", signoz.Description)
	addIfNotEmpty("Summary", signoz.Summary)
	addIfNotEmpty("Rule ID", signoz.RuleID)
	addIfNotEmpty("Tag", signoz.Tag)
	addIfNotEmpty("Severity", signoz.Severity)
	addIfNotEmpty("Source Origin", signoz.SourceOrigin)
	addIfNotEmpty("Payload Source", signoz.PayloadSource)
	addIfNotEmpty("Threshold", signoz.Threshold)
	addIfNotEmpty("Current Value", signoz.CurrentValue)

	if signoz.EreAccountID != 0 {
		items = append(items, fmt.Sprintf("- **Account ID**: %d\n", signoz.EreAccountID))
	}
	if signoz.NumFiring > 0 {
		items = append(items, fmt.Sprintf("- **Firing Alerts**: %d\n", signoz.NumFiring))
	}
	if signoz.NumResolved > 0 {
		items = append(items, fmt.Sprintf("- **Resolved Alerts**: %d\n", signoz.NumResolved))
	}

	// Add URLs as clickable links
	if signoz.ClientURL != "" {
		items = append(items, fmt.Sprintf("- **SigNoz**: [View Dashboard](%s)\n", signoz.ClientURL))
	}
	if signoz.RelatedLogs != "" {
		items = append(items, fmt.Sprintf("- **Related Logs**: [View Logs](%s)\n", signoz.RelatedLogs))
	}

	return items
}

func buildAzureDetailsFromStruct(azure *AzureAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Resource", azure.ResourceName)
	addIfNotEmpty("Resource Type", azure.ResourceType)
	addIfNotEmpty("Resource Group", azure.ResourceGroupName)
	addIfNotEmpty("Subscription", azure.SubscriptionId)
	addIfNotEmpty("Metric", azure.MetricName)
	addIfNotEmpty("Metric Namespace", azure.MetricNamespace)
	addIfNotEmpty("Condition", azure.Operator)
	addIfNotEmpty("Time Aggregation", azure.TimeAggregation)
	addIfNotEmpty("Window Size", azure.WindowSize)
	addIfNotEmpty("Severity", azure.Severity)
	addIfNotEmpty("Condition Type", azure.ConditionType)
	addIfNotEmpty("Alert Created", azure.CreationTime)
	addIfNotEmpty("Alert Timestamp", azure.Timestamp)

	if azure.Threshold != 0 {
		items = append(items, fmt.Sprintf("- **Threshold**: %.1f\n", azure.Threshold))
	}
	if azure.MetricValue != 0 {
		items = append(items, fmt.Sprintf("- **Current Value**: %.1f\n", azure.MetricValue))
	}
	if azure.MinFailingPeriods != 0 {
		items = append(items, fmt.Sprintf("- **Min Failing Periods**: %d\n", azure.MinFailingPeriods))
	}
	if azure.EvaluationPeriods != 0 {
		items = append(items, fmt.Sprintf("- **Evaluation Periods**: %d\n", azure.EvaluationPeriods))
	}

	// Add description with SOP link if available
	if azure.ContextDescription != "" {
		items = append(items, fmt.Sprintf("- **Alert Description**: %s\n", azure.ContextDescription))
	}

	// Add dimensions
	if len(azure.Dimensions) > 0 {
		items = append(items, "- **Dimensions**:\n")
		for key, value := range azure.Dimensions {
			items = append(items, fmt.Sprintf("  - %s: %s\n", key, value))
		}
	}

	// Add portal link
	if azure.PortalLink != "" {
		items = append(items, fmt.Sprintf("- **Azure Portal**: [View Resource](%s)\n", azure.PortalLink))
	}

	return items
}

func buildChronosphereDetailsFromStruct(chronosphere *ChronosphereAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Monitor Name", chronosphere.MonitorName)
	addIfNotEmpty("Monitor Slug", chronosphere.MonitorSlug)
	addIfNotEmpty("Alert ID", chronosphere.AlertID)
	addIfNotEmpty("Environment", chronosphere.Environment)
	addIfNotEmpty("Job", chronosphere.Job)
	addIfNotEmpty("Type", chronosphere.Type)
	addIfNotEmpty("Severity", chronosphere.Severity)
	addIfNotEmpty("Signal", chronosphere.Signal)
	addIfNotEmpty("Source Origin", chronosphere.SourceOrigin)
	addIfNotEmpty("Payload Source", chronosphere.PayloadSource)
	addIfNotEmpty("Notification Policy", chronosphere.NotificationPolicySlug)

	if chronosphere.EreAccountID != 0 {
		items = append(items, fmt.Sprintf("- **Account ID**: %d\n", chronosphere.EreAccountID))
	}
	if chronosphere.NumAlertingSeries > 0 {
		items = append(items, fmt.Sprintf("- **Alerting Series**: %d\n", chronosphere.NumAlertingSeries))
	}
	if chronosphere.NumResolvedSeries >= 0 {
		items = append(items, fmt.Sprintf("- **Resolved Series**: %d\n", chronosphere.NumResolvedSeries))
	}

	// Add URLs as clickable links
	if chronosphere.ClientURL != "" {
		items = append(items, fmt.Sprintf("- **Chronosphere**: [View Monitor](%s)\n", chronosphere.ClientURL))
	}
	if chronosphere.SourceURL != "" && chronosphere.SourceURL != chronosphere.ClientURL {
		items = append(items, fmt.Sprintf("- **Alert Details**: [View Alert](%s)\n", chronosphere.SourceURL))
	}

	// Add parsed labels if any
	if len(chronosphere.Labels) > 0 {
		items = append(items, "- **Labels**:\n")
		for key, value := range chronosphere.Labels {
			items = append(items, fmt.Sprintf("  - %s: %s\n", key, value))
		}
	}

	return items
}

func buildGenericDetailsFromStruct(generic *GenericAlertFields) []string {
	var items []string

	addIfNotEmpty := func(label, value string) {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", label, value))
		}
	}

	addIfNotEmpty("Description", generic.Description)
	addIfNotEmpty("Message", generic.Message)

	if len(generic.Labels) > 0 {
		items = append(items, fmt.Sprintf("- **Labels**: %d found\n", len(generic.Labels)))
	}
	if len(generic.Annotations) > 0 {
		items = append(items, fmt.Sprintf("- **Annotations**: %d found\n", len(generic.Annotations)))
	}

	return items
}

func buildAnnotationsFromMap(annotations map[string]string) []string {
	var items []string
	for key, value := range annotations {
		if value != "" {
			items = append(items, fmt.Sprintf("- **%s**: %s\n", key, value))
		}
	}
	return items
}

func buildLinksFromAlertData(alertData *AlertData, incident *Incident) []string {
	items := []string{
		fmt.Sprintf("- [PagerDuty Incident](%s)\n", incident.HTMLURL),
	}

	// Add source-specific links
	switch alertData.Source {
	case AlertSourceGrafana:
		if alertData.Grafana != nil {
			if alertData.Grafana.SourceURL != "" {
				items = append(items, fmt.Sprintf("- [Source Dashboard](%s)\n", alertData.Grafana.SourceURL))
			}
			if alertData.Grafana.RuleSource != "" {
				items = append(items, fmt.Sprintf("- [Rule Configuration](%s)\n", alertData.Grafana.RuleSource))
			}
			if alertData.Grafana.RelatedLogs != "" {
				items = append(items, fmt.Sprintf("- [Related Logs](%s)\n", alertData.Grafana.RelatedLogs))
			}
		}
	case AlertSourceSigNoz:
		if alertData.SigNoz != nil {
			if alertData.SigNoz.SourceURL != "" {
				items = append(items, fmt.Sprintf("- [Source Dashboard](%s)\n", alertData.SigNoz.SourceURL))
			}
		}
	case AlertSourceAzure:
		if alertData.Azure != nil {
			if alertData.Azure.PortalLink != "" {
				items = append(items, fmt.Sprintf("- [Azure Portal](%s)\n", alertData.Azure.PortalLink))
			}
		}
	case AlertSourceChronosphere:
		if alertData.Chronosphere != nil {
			if alertData.Chronosphere.ClientURL != "" {
				items = append(items, fmt.Sprintf("- [Chronosphere Monitor](%s)\n", alertData.Chronosphere.ClientURL))
			}
			if alertData.Chronosphere.SourceURL != "" && alertData.Chronosphere.SourceURL != alertData.Chronosphere.ClientURL {
				items = append(items, fmt.Sprintf("- [Alert Details](%s)\n", alertData.Chronosphere.SourceURL))
			}
		}
	}

	return items
}

func buildCustomFields(customFields []CustomField) []string {
	var items []string
	for _, field := range customFields {
		if field.Value != nil {
			items = append(items, fmt.Sprintf("- **%s**: %v\n", field.DisplayName, field.Value))
		}
	}
	return items
}

// GetPagerDutyIncident queries the PagerDuty API for a specific incident by its ID.
// It requires a valid PagerDuty API token and the incident ID.
func GetPagerDutyIncident(apiToken, incidentID string) (*Incident, error) {
	// 1. Construct the API request URL
	url := fmt.Sprintf("https://api.pagerduty.com/incidents/%s?include[]=body", incidentID)

	// 2. Create a new HTTP GET request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// 3. Set the required headers for authorization and API versioning
	req.Header.Set("Authorization", "Token token="+apiToken)
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
	req.Header.Set("Content-Type", "application/json")

	// 4. Execute the request using a default HTTP client
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request to PagerDuty API: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 5. Check for non-successful status codes
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PagerDuty API returned a non-200 status code: %d %s", resp.StatusCode, resp.Status)
	}

	// 6. Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	// 7. Unmarshal the JSON response into our Go structs
	var incidentResponse IncidentResponse
	if err := json.Unmarshal(body, &incidentResponse); err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON response: %w", err)
	}

	// 8. Return the incident object and a nil error
	return &incidentResponse.Incident, nil
}

// GetPagerDutyIncidentAlerts fetches alerts for an incident via the PagerDuty API.
// This is used as a fallback when the incident body.details is null, since the
// alerts endpoint often contains the CEF payload with Alertmanager labels.
func GetPagerDutyIncidentAlerts(apiToken, incidentID string) ([]PagerDutyAlert, error) {
	alertsURL := fmt.Sprintf("https://api.pagerduty.com/incidents/%s/alerts", url.PathEscape(incidentID))

	req, err := http.NewRequest("GET", alertsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating alerts request: %w", err)
	}

	req.Header.Set("Authorization", "Token token="+apiToken)
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending alerts request to PagerDuty API: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PagerDuty alerts API returned non-200 status: %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("error reading alerts response body: %w", err)
	}

	var alertsResponse IncidentAlertsResponse
	if err := json.Unmarshal(body, &alertsResponse); err != nil {
		return nil, fmt.Errorf("error unmarshaling alerts JSON response: %w", err)
	}

	return alertsResponse.Alerts, nil
}

// mapSeverityToEventPriority maps alert severity strings to event priority constants
func mapSeverityToEventPriority(severity string) event.EventPriority {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return event.EventPriorityHigh
	case "high":
		return event.EventPriorityHigh
	case "warning", "warn", "medium":
		return event.EventPriorityMedium
	case "low", "info", "informational":
		return event.EventPriorityLow
	case "debug":
		return event.EventPriorityDebug
	default:
		// Default to low if severity is unrecognized or empty
		return event.EventPriorityLow
	}
}

// resolveEventTitleFromLabels picks a human-readable event title from enriched alert labels.
// The Alertmanager-generated incident title is an unreadable concatenation of every label
// value, so we prefer the alert's summary instead. The summary may arrive either as an
// annotation (annotation_summary) or as a plain label (summary) depending on whether the
// alert rule declares it under Annotations: or Labels: in the firing text — and the firing-text
// parser keys both the same way, so plain "summary" is the common case. Fall back to alertname
// when no summary is present. Returns "" when no better title is available (caller keeps the original).
func resolveEventTitleFromLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	for _, key := range []string{"annotation_summary", "summary", "alertname"} {
		if v, ok := labels[key]; ok && v != "" {
			return v
		}
	}
	return ""
}

// resolveSubjectFromLabels attempts to determine EventSubjectName, EventSubjectNamespace,
// and EventSubjectKind from Investigation.Labels populated by enrichment parsing.
// It checks label keys in priority order and only sets values not already resolved
// from the title regex parsing.
// applyK8sPathSubject sets EventSubjectName / EventSubjectNamespace from a
// /k8s/<namespace>/<pod>/<workload> path — the shape Alertmanager embeds in
// `container_id` labels and in incident titles. Fully deterministic (no LLM). The
// 4th segment (workload / container name — matches k8s_workloads) is preferred over
// the pod, whose ReplicaSet hash never matches the inventory; the pod is kept as a
// label. Returns true when a subject was set.
func applyK8sPathSubject(p *core.EventIncomingWebhook, labels map[string]string, path string) bool {
	match := reK8sPath.FindStringSubmatch(path)
	if len(match) != 4 {
		return false
	}
	namespace, pod, workload := match[1], match[2], match[3]
	if workload != "" {
		p.EventSubjectName = workload
	} else {
		p.EventSubjectName = pod
	}
	if p.EventSubjectNamespace == "" {
		p.EventSubjectNamespace = namespace
	}
	if pod != "" && labels != nil && labels["pod"] == "" {
		labels["pod"] = pod
	}
	return p.EventSubjectName != ""
}

// extractGCPMonitoringSubject parses a GCP Cloud Monitoring alert title that was
// routed through PagerDuty. These arrive with ONLY the human-readable title — the
// PagerDuty Events-API payload carries no structured GCP resource labels, and the
// incident API enrichment's body.details just echoes the same text — so the resource
// must be read from the title. The format is stable:
//
//	<metric/condition> for <project> <resource…> [with metric labels {…}] is
//	(above|below) the threshold of N with a value of N. | Violation started: … |
//	Policy: … | Condition: … | View incident:
//	https://console.cloud.google.com/monitoring/alerting/…?…&project=<project>
//
// It returns the resource display name (the trailing value of the `for` clause — the
// space-joined resource-label values end with the instance/resource name, e.g. a
// Cloud SQL instance), the project id (from the View-incident URL, falling back to
// the leading `for`-clause token), and a kind when the datasource is identifiable
// (Cloud SQL log-based metrics carry `cloudsql.googleapis.com` in their labels).
// All return values are empty when the title is not a GCP Cloud Monitoring alert.
func extractGCPMonitoringSubject(title string) (resource, project, kind string) {
	if title == "" || !strings.Contains(title, "console.cloud.google.com/monitoring/alerting") {
		return "", "", ""
	}
	if m := reGCPProject.FindStringSubmatch(title); m != nil {
		project = m[1]
	}
	// Parse the resource out of the first pipe segment (the summary line) so a
	// stray " for " in a later segment can never be matched.
	summary := strings.SplitN(title, " | ", 2)[0]
	m := reGCPMonitoringFor.FindStringSubmatch(summary)
	if m == nil {
		return "", project, ""
	}
	fields := strings.Fields(strings.TrimSpace(m[1])) // "<project> <resource…>"
	if len(fields) == 0 {
		return "", project, ""
	}
	resource = fields[len(fields)-1]
	if project == "" {
		project = fields[0]
	}
	// Never let the bare project become the subject (e.g. a resource-less clause).
	if resource == project {
		return "", project, ""
	}
	if strings.Contains(title, "cloudsql.googleapis.com") {
		kind = "cloudsql_database"
	}
	return resource, project, kind
}

// applyGCPMonitoringSubject resolves the subject for a GCP Cloud Monitoring alert
// routed via PagerDuty from the incident title (see extractGCPMonitoringSubject).
// Without it the title carries no label the workload parser understands, so the
// alert falls to the LLM, which has no matching workload and hallucinates an
// unrelated one (observed: a Cloud SQL `dev-pg-slow-queries` alert resolved to a
// `nudgebee-agent-prod-node-agent` daemonset). Sets the GCP project as the event
// cluster (`cluster` label) to mirror native GCP events (whose cluster IS the
// project) and satisfy the ingest guard, which drops any event with an empty
// cluster. Must run BEFORE the LLM fallback (so these alerts never reach it).
// No-op unless the subject is still empty and the title is a GCP Cloud Monitoring
// alert, so a subject recovered from labels always wins.
func applyGCPMonitoringSubject(parsedPayload *core.EventIncomingWebhook) {
	if parsedPayload.EventSubjectName != "" {
		return
	}
	labels := parsedPayload.Investigation.Labels
	title := ""
	if labels != nil {
		title = labels["nb_pd_raw_title"]
	}
	if title == "" {
		title = parsedPayload.EventTitle
	}
	resource, project, kind := extractGCPMonitoringSubject(title)
	if resource == "" {
		return
	}
	parsedPayload.EventSubjectName = resource
	if kind != "" {
		parsedPayload.EventSubjectKind = kind
	}
	// Guard against a nil label map: without this a resolved subject would be set
	// but the `cluster` label never written, and the ingest guard drops any event
	// with an empty cluster.
	if labels == nil {
		labels = map[string]string{}
		parsedPayload.Investigation.Labels = labels
	}
	if project != "" {
		labels["project_id"] = project
		if labels["cluster"] == "" {
			labels["cluster"] = project
		}
	}
	labels["nb_subject_match"] = "gcp_monitoring_title"
	labels["nb_subject_resolution"] = "gcp-monitoring"
}

func resolveSubjectFromLabels(parsedPayload *core.EventIncomingWebhook) {
	labels := parsedPayload.Investigation.Labels
	if labels == nil {
		return
	}

	// Explicit declared subject (webhook_subject_mappings markdown fields:
	// "Subject Name/Namespace/Type"). When present this is the authoritative subject,
	// so it wins over the inferred label keys below. A pod-typed subject carries a
	// ReplicaSet hash; lookupPodOwner and the core matcher resolve it to the owning
	// workload downstream.
	if parsedPayload.EventSubjectName == "" {
		if name := labels["subject_name"]; name != "" {
			parsedPayload.EventSubjectName = name
			if parsedPayload.EventSubjectNamespace == "" {
				parsedPayload.EventSubjectNamespace = labels["subject_namespace"]
			}
			if kind := labels["subject_type"]; kind != "" {
				parsedPayload.EventSubjectKind = kind
			}
		}
	}

	// Resolve subject name if not already set from title regex.
	// Priority order: k8s workload labels → app_id → monitoring labels → pod/container
	if parsedPayload.EventSubjectName == "" {
		// High-priority: standard k8s workload labels
		highPriorityKeys := []string{
			"destination_workload_name", // Istio/service mesh (most specific)
			"src_workload_name",         // Some mesh alerts
			"deployment",                // K8s Deployment (trigger.py priority 1)
			"k8s_deployment_name",       // Same, as emitted by kube-state-metrics-derived rules
			"daemonset",                 // K8s DaemonSet (trigger.py priority 2)
			"statefulset",               // K8s StatefulSet (trigger.py priority 3)
		}
		for _, key := range highPriorityKeys {
			val := labels[key]
			if val == "" {
				continue
			}
			// Skip the kube-state-metrics EXPORTER only (exact match). A substring
			// match also drops a helm-prefixed real workload like
			// "victoria-kube-state-metrics" — so a KubeDeploymentReplicasMismatch
			// about the KSM deployment itself lost every deterministic candidate
			// and fell to the LLM. The bare exporter name never names a real
			// workload here, so exact-match keeps the guard while letting the
			// actual subject through.
			if val == "kube-state-metrics" {
				continue
			}
			parsedPayload.EventSubjectName = val
			break
		}
	}

	// Parse app_id (/k8s/{namespace}/{workload}) — more reliable than monitoring job labels
	if parsedPayload.EventSubjectName == "" {
		if appID := labels["app_id"]; appID != "" {
			parts := strings.Split(strings.TrimPrefix(appID, "/"), "/")
			if len(parts) == 3 && parts[0] == "k8s" {
				parsedPayload.EventSubjectName = parts[2]
				if parsedPayload.EventSubjectNamespace == "" {
					parsedPayload.EventSubjectNamespace = parts[1]
				}
			}
		}
	}

	// Parse container_id (/k8s/{namespace}/{pod}/{workload}) — Alertmanager/Grafana
	// attach the full k8s path in this label. Structured and reliable, so it runs
	// ahead of the generic service/pod labels below. Deterministic — no LLM.
	if parsedPayload.EventSubjectName == "" {
		if cid := labels["container_id"]; cid != "" {
			applyK8sPathSubject(parsedPayload, labels, cid)
		}
	}

	// Infrastructure resource labels (e.g., PostgreSQL database name, alert source component)
	if parsedPayload.EventSubjectName == "" {
		infraKeys := []string{
			"datname",          // PostgreSQL database name
			"source_component", // Alert source component
		}
		for _, key := range infraKeys {
			val := labels[key]
			if val == "" {
				continue
			}
			lower := strings.ToLower(val)
			if lower == "grafana" || lower == "alertmanager" || lower == "prometheus" {
				continue
			}
			parsedPayload.EventSubjectName = val
			if key == "datname" {
				// The PostgreSQL database name is a logical database, not a k8s
				// workload. Type it and opt out of the workload name-match so it
				// isn't matched onto an unrelated same-named workload (mirrors the
				// Prometheus datname path). The label is left in place so the core
				// MatchWorkloadAndEnrich running after this handler consumes it.
				parsedPayload.EventSubjectKind = "database"
				if parsedPayload.Investigation.Labels == nil {
					parsedPayload.Investigation.Labels = map[string]string{}
				}
				parsedPayload.Investigation.Labels[core.SkipWorkloadMatchLabel] = "true"
			}
			break
		}
	}

	// Specific k8s resource labels (PVC / HPA / Job). These name the resource
	// the alert is actually about and must win over the generic monitoring/job
	// fallbacks below. E.g. KubePersistentVolumeFillingUp carries
	// persistentvolumeclaim=<pvc> alongside job=kubelet (the scrape target) —
	// the PVC is the subject, not the kubelet scraper. `job_name` (not the
	// prometheus `job`) is the real K8s Job; kube-state-metrics emits it on
	// kube_job_* series. Mirrors robusta's ResourceMapping precedence.
	if parsedPayload.EventSubjectName == "" {
		resourceKeys := []struct{ key, kind string }{
			{"persistentvolumeclaim", "persistentvolumeclaim"},
			{"horizontalpodautoscaler", "horizontalpodautoscaler"},
			{"hpa", "horizontalpodautoscaler"},
			{"job_name", "job"},
		}
		for _, rk := range resourceKeys {
			val := labels[rk.key]
			if val == "" {
				continue
			}
			parsedPayload.EventSubjectName = val
			parsedPayload.EventSubjectKind = rk.kind
			break
		}
	}

	if parsedPayload.EventSubjectName == "" {
		// Lower-priority: monitoring and generic labels
		lowerPriorityKeys := []string{
			"nb_alert_job",   // Chronosphere normalized job
			"service_name",   // Generic service name
			"service.name",   // OpenTelemetry-style service name
			"service",        // Alertmanager/Prometheus bare `service` label (e.g. TargetDown)
			"pod",            // Pod name from labels
			"pod_name",       // Alternative pod name key
			"container",      // Container name (Chronosphere events)
			"nb_resource_id", // Azure cloud resource
			"job",            // LAST: often Prometheus scraper, not target
		}
		for _, key := range lowerPriorityKeys {
			val := labels[key]
			if val == "" {
				continue
			}
			// Exact match, not substring: filter the bare kube-state-metrics
			// exporter without dropping a helm-prefixed real workload
			// (e.g. pod=victoria-kube-state-metrics), which is the deterministic
			// subject when the alert is about the KSM deployment itself. The
			// scrape-target keys below are still guarded by isPrometheusScrapeJob.
			if val == "kube-state-metrics" {
				continue
			}
			// `job`/`nb_alert_job` — and the service keys — can carry the prometheus
			// scrape target on every series (kubelet, node-exporter, …). Never let a
			// bare exporter become the subject: that's how KubePersistentVolumeFillingUp
			// resolved to subject=kubelet instead of the persistentvolumeclaim.
			// isPrometheusScrapeJob matches the exporter exactly, so a real Service
			// name like "nudgebee-prometheus-kube-p-kubelet" still resolves normally.
			switch key {
			case "job", "nb_alert_job", "service", "service_name", "service.name":
				if isPrometheusScrapeJob(val) {
					continue
				}
			}
			parsedPayload.EventSubjectName = val
			break
		}
	}

	// If still no subject, try pipe-delimited title parsing
	if parsedPayload.EventSubjectName == "" {
		if svc := extractServiceFromPipeTitle(parsedPayload.EventTitle); svc != "" {
			parsedPayload.EventSubjectName = svc
		}
	}

	// If still no subject, try SigNoz related_logs URL
	if parsedPayload.EventSubjectName == "" {
		if relatedLogs := labels["related_logs"]; relatedLogs != "" {
			if svc := extractServiceFromSigNozURL(relatedLogs); svc != "" {
				parsedPayload.EventSubjectName = svc
			}
		}
		// Also check the annotation variant
		if parsedPayload.EventSubjectName == "" {
			if relatedLogs := labels["annotation_related_logs"]; relatedLogs != "" {
				if svc := extractServiceFromSigNozURL(relatedLogs); svc != "" {
					parsedPayload.EventSubjectName = svc
				}
			}
		}
	}

	// If still no subject, try monitorName for Chronosphere (pipe-delimited)
	if parsedPayload.EventSubjectName == "" {
		if monitorName := labels["monitorName"]; monitorName != "" {
			if svc := extractServiceFromPipeTitle(monitorName); svc != "" {
				parsedPayload.EventSubjectName = svc
			}
		}
	}

	// If still no subject, try Grafana rulename (pipe-delimited)
	if parsedPayload.EventSubjectName == "" {
		if rulename := labels["rulename"]; rulename != "" {
			if svc := extractServiceFromPipeTitle(rulename); svc != "" {
				parsedPayload.EventSubjectName = svc
			}
		}
	}

	// Last resort: parse the /k8s/{namespace}/{pod}/{workload} path Alertmanager
	// embeds in the incident title. This is fully deterministic (no LLM) and the only
	// subject source present in the raw webhook — the `service`/`namespace` labels
	// arrive later via the PagerDuty API enrichment, which races (body.details null),
	// so relying on them alone sends these alerts to the LLM. Read the ORIGINAL title
	// (preserved before the human-readable override, which strips the path). Prefer
	// the 4th segment (workload/container — matches k8s_workloads) over the pod, whose
	// ReplicaSet hash never matches the inventory; keep the pod as a label.
	if parsedPayload.EventSubjectName == "" {
		k8sPathTitle := labels["nb_pd_raw_title"]
		if k8sPathTitle == "" {
			k8sPathTitle = parsedPayload.EventTitle
		}
		applyK8sPathSubject(parsedPayload, labels, k8sPathTitle)
	}

	// Resolve namespace if not already set
	if parsedPayload.EventSubjectNamespace == "" {
		namespaceKeys := []string{"destination_workload_namespace", "namespace", "k8s_namespace", "k8s.namespace.name"}
		for _, key := range namespaceKeys {
			if val := labels[key]; val != "" {
				parsedPayload.EventSubjectNamespace = val
				break
			}
		}
	}

	// Populate pod/namespace labels for auto-action triggers (pod metrics, deployment history)
	if parsedPayload.EventSubjectName != "" {
		// Only fabricate a `pod` label when the subject is pod-like. For
		// resource subjects that aren't pods (PVC, HPA, Job, node), a
		// pod=<subject> label would make the pod metric/log enrichers query a
		// non-existent pod (e.g. pod=<pvc-name>) and return empty.
		kind := strings.ToLower(parsedPayload.EventSubjectKind)
		isPodLike := kind == "" || kind == "pod"
		if isPodLike && labels["pod"] == "" {
			labels["pod"] = parsedPayload.EventSubjectName
		}
		if parsedPayload.EventSubjectNamespace != "" && labels["namespace"] == "" {
			labels["namespace"] = parsedPayload.EventSubjectNamespace
		}
		if parsedPayload.EventSubjectKind != "" && labels["kind"] == "" {
			labels["kind"] = parsedPayload.EventSubjectKind
		}
	}

	// Mesh and Alertmanager labels frequently name the ReplicaSet
	// ("cloud-collector-server-64945c7bc6") rather than the workload
	// ("cloud-collector-server"), and k8s_workloads only stores the latter — so
	// none of core.MatchWorkloadAndEnrich's predicates match. Offer the owning
	// name as an additional candidate; the matcher tries EventSubjectName first,
	// so a workload genuinely named with a hash-shaped suffix still wins.
	if owner := stripPodTemplateHash(parsedPayload.EventSubjectName); owner != parsedPayload.EventSubjectName {
		addWorkloadCandidate(labels, owner)
	}

	if parsedPayload.EventSubjectName == "" {
		labels["nb_subject_resolution"] = "unresolved"
	}
}

// addWorkloadCandidate appends to the comma-separated list
// core.MatchWorkloadAndEnrich tries after EventSubjectName. The label is
// consumed during matching and never persisted.
func addWorkloadCandidate(labels map[string]string, name string) {
	if labels == nil || name == "" {
		return
	}
	const key = "nb_workload_candidates"
	existing := labels[key]
	if existing == "" {
		labels[key] = name
		return
	}
	for _, c := range strings.Split(existing, ",") {
		if strings.TrimSpace(c) == name {
			return
		}
	}
	labels[key] = existing + "," + name
}

// k8sHashAlphabet is the alphabet Kubernetes uses for pod-template-hash and pod
// name suffixes (rand.SafeEncodeString): digits and consonants only, with the
// vowels and 0/1/3 removed so the generator never produces words. That absence
// is what makes a hash suffix reliably separable from a real name segment —
// "cloud-collector-server-64945c7bc6" strips, while "otel-collector-collector"
// and "redis-master" do not, because their last segments contain vowels.
//
// knowledge_graph/core.ExtractPodOwner solves a nearby problem but is not
// reusable here: it gates on IsAlphanumeric + length alone, so any 8-10 char
// segment reads as a hash and "otel-deployment-collector-collector" (a real
// workload) would be truncated to "otel-deployment-collector". It also guesses
// an owner kind and reads a trailing integer as a StatefulSet ordinal. This path
// needs the opposite bias — strip only when the suffix is certainly generated —
// because a wrong candidate can pin the alert on an unrelated workload.
const k8sHashAlphabet = "bcdfghjklmnpqrstvwxz2456789"

func looksLikePodTemplateHash(s string) bool {
	if len(s) < 5 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(k8sHashAlphabet, r) {
			return false
		}
	}
	return true
}

// stripPodTemplateHash reduces a ReplicaSet or pod name to its owning workload
// name. Removes at most two trailing hash segments, covering both
// "<deploy>-<template-hash>" and "<deploy>-<template-hash>-<pod-suffix>".
// Returns the input unchanged when no segment is certainly a generated hash.
func stripPodTemplateHash(name string) string {
	for range 2 {
		idx := strings.LastIndex(name, "-")
		if idx <= 0 || !looksLikePodTemplateHash(name[idx+1:]) {
			break
		}
		name = name[:idx]
	}
	return name
}

// isPrometheusScrapeJob reports whether a `job`/`nb_alert_job` label value names
// an infrastructure scrape target (exporter) rather than a workload. These jobs
// scrape OTHER resources' metrics — kubelet exposes PVC/volume stats,
// kube-state-metrics exposes every object's state — so their name must never
// become a finding subject. Without this, KubePersistentVolumeFillingUp
// (job=kubelet) resolved to subject=kubelet instead of the persistentvolumeclaim.
func isPrometheusScrapeJob(val string) bool {
	switch strings.ToLower(val) {
	case "kubelet", "kube-state-metrics", "node-exporter", "cadvisor":
		return true
	}
	return strings.Contains(val, "kube-state-metrics") || strings.Contains(val, "node-exporter")
}

// resolveSubjectUsingLLM asks the webhook_subject_name_extractor agent to map the
// alert to a running service, then validates the result against k8s_workloads via
// the shared core matcher. The agent owns the running-services and historical
// context server-side, so only the alert is sent here.
func resolveSubjectUsingLLM(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, accountId string) {
	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = map[string]string{}
	}
	tenantId := sc.GetSecurityContext().GetTenantId()

	name := core.ResolveSubjectNameViaAgent(sc, accountId,
		parsedPayload.EventTitle, parsedPayload.EventDescription,
		parsedPayload.Investigation.SourceUrl, parsedPayload.Investigation.Labels)
	if name == "" {
		common.MetricsSubjectResolution(sc.GetContext(), IntegrationPagerdutyWebhook, "live", "not_found", tenantId)
		return
	}
	common.MetricsSubjectResolution(sc.GetContext(), IntegrationPagerdutyWebhook, "live", "matched", tenantId)

	parsedPayload.EventSubjectName = name
	core.MatchWorkloadAndEnrich(sc, parsedPayload, accountId)
}

// pipeSegmentSkipList contains words that should be skipped when parsing pipe-delimited titles
// for service name extraction. These are severity, environment, and platform keywords.
var pipeSegmentSkipSet = map[string]bool{
	"critical": true, "warn": true, "warning": true, "info": true, "error": true,
	"firing": true, "resolved": true, "alert": true, "alarm": true,
	"prod": true, "production": true, "staging": true, "stage": true,
	"dev": true, "development": true, "test": true, "qa": true,
	"eks": true, "aks": true, "gke": true, "k8s": true, "kubernetes": true,
	"aws": true, "azure": true, "gcp": true,
	"high": true, "medium": true, "low": true, "p1": true, "p2": true, "p3": true, "p4": true,
}

// reServiceNamePattern matches strings that look like k8s service/workload names
// (lowercase letters, digits, hyphens, at least 3 chars)
var reServiceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]+(-[a-z0-9]+)+$`)

// extractServiceFromPipeTitle extracts a service/workload name from pipe-delimited alert titles.
// Examples:
//   - "Critical | Prod | EKS | booking-service | low apdex" → "booking-service"
//   - "Critical | EKS | shipment-master-service | Apdex breached" → "shipment-master-service"
func extractServiceFromPipeTitle(title string) string {
	if !strings.Contains(title, "|") {
		return ""
	}

	segments := strings.Split(title, "|")
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		lower := strings.ToLower(seg)

		// Skip known non-service segments
		if pipeSegmentSkipSet[lower] {
			continue
		}

		// Skip segments with spaces (likely descriptions like "low apdex", "High Error Rate")
		if strings.Contains(seg, " ") {
			continue
		}

		// Check if it matches a k8s service naming pattern
		if reServiceNamePattern.MatchString(lower) {
			return lower
		}
	}

	return ""
}

// extractServiceFromSigNozURL extracts service.name from a SigNoz related_logs URL.
// SigNoz URLs have a compositeQuery parameter with filters containing service.name.
func extractServiceFromSigNozURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	compositeQuery := parsedURL.Query().Get("compositeQuery")
	if compositeQuery == "" {
		return ""
	}

	// Parse the compositeQuery JSON
	var query map[string]any
	if err := json.Unmarshal([]byte(compositeQuery), &query); err != nil {
		return ""
	}

	// SigNoz compositeQuery comes in two shapes:
	//   - URL format:      builder -> queryData[]   (array of query objects)
	//   - internal format: builderQueries{}         (map of name -> query object)
	// Both carry the same filters.items[] structure, so collect the query
	// objects from whichever shape is present and scan them uniformly.
	var queries []any
	if builder, ok := query["builder"].(map[string]any); ok {
		if queryData, ok := builder["queryData"].([]any); ok {
			queries = append(queries, queryData...)
		}
	}
	if builderQueries, ok := query["builderQueries"].(map[string]any); ok {
		// Sort keys for deterministic iteration (Go map order is randomized),
		// so service extraction is stable when multiple queries are present.
		keys := make([]string, 0, len(builderQueries))
		for k := range builderQueries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			queries = append(queries, builderQueries[k])
		}
	}

	for _, bq := range queries {
		bqMap, ok := bq.(map[string]any)
		if !ok {
			continue
		}
		filters, ok := bqMap["filters"].(map[string]any)
		if !ok {
			continue
		}
		items, ok := filters["items"].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			keyObj, ok := itemMap["key"].(map[string]any)
			if !ok {
				continue
			}
			if keyObj["key"] == "service.name" || keyObj["key"] == "serviceName" {
				if val, ok := itemMap["value"].(string); ok && val != "" {
					return stripEnvSuffix(val)
				}
			}
		}
	}

	return ""
}

// stripEnvSuffix removes common environment suffixes from service names
// e.g. "courier-worker-prod" → "courier-worker", "api-staging" → "api"
func stripEnvSuffix(name string) string {
	suffixes := []string{"-prod", "-production", "-staging", "-stage", "-dev", "-development", "-qa", "-test"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

// buildAlertRuleEvidence creates a markdown evidence entry with alert rule details
// extracted from the parsed labels. It aggregates source URLs, rule IDs,
// metric values, and other rule metadata into a single readable evidence block.
func buildAlertRuleEvidence(labels map[string]string) *event.EventEvidence {
	var parts []string

	source := labels["nb_alert_source"]

	// Alert source and name
	if alertName := labels["nb_alert_name"]; alertName != "" {
		parts = append(parts, fmt.Sprintf("**Alert Name:** %s", alertName))
	}
	if source != "" {
		parts = append(parts, fmt.Sprintf("**Alert Source:** %s", source))
	}

	// Source URLs (varies by provider)
	switch source {
	case "grafana":
		if sourceURL := labels["source_url"]; sourceURL != "" {
			parts = append(parts, fmt.Sprintf("**Grafana Alert Rule:** [View Rule](%s)", sourceURL))
		}
		if dashboardURL := labels["dashboardURL"]; dashboardURL != "" {
			parts = append(parts, fmt.Sprintf("**Dashboard:** [View Dashboard](%s)", dashboardURL))
		}
		if panelURL := labels["panelURL"]; panelURL != "" {
			parts = append(parts, fmt.Sprintf("**Panel:** [View Panel](%s)", panelURL))
		}
		if silenceURL := labels["silenceURL"]; silenceURL != "" {
			parts = append(parts, fmt.Sprintf("**Silence:** [Create Silence](%s)", silenceURL))
		}
	case "signoz":
		if ruleSource := labels["ruleSource"]; ruleSource != "" {
			parts = append(parts, fmt.Sprintf("**SigNoz Alert Rule:** [View Rule](%s)", ruleSource))
		} else if sourceURL := labels["source_url"]; sourceURL != "" {
			parts = append(parts, fmt.Sprintf("**SigNoz Alert Rule:** [View Rule](%s)", sourceURL))
		}
		if relatedLogs := labels["related_logs"]; relatedLogs != "" {
			parts = append(parts, fmt.Sprintf("**Related Logs:** [View Logs](%s)", relatedLogs))
		}
	case "chronosphere":
		if sourceURL := labels["sourceURL"]; sourceURL != "" {
			parts = append(parts, fmt.Sprintf("**Chronosphere Monitor:** [View Monitor](%s)", sourceURL))
		}
		if clientURL := labels["clientURL"]; clientURL != "" {
			parts = append(parts, fmt.Sprintf("**Client URL:** [View](%s)", clientURL))
		}
	case "aws":
		if clientURL := labels["ClientURL"]; clientURL != "" {
			parts = append(parts, fmt.Sprintf("**AWS CloudWatch:** [View Alarm](%s)", clientURL))
		}
	case "azure":
		if portalLink := labels["portalLink"]; portalLink != "" {
			parts = append(parts, fmt.Sprintf("**Azure Portal:** [View Alert](%s)", portalLink))
		}
	}

	// Rule ID
	if ruleID := labels["nb_alert_rule_id"]; ruleID != "" {
		parts = append(parts, fmt.Sprintf("**Rule ID:** %s", ruleID))
	} else if ruleID := labels["ruleId"]; ruleID != "" {
		parts = append(parts, fmt.Sprintf("**Rule ID:** %s", ruleID))
	}

	// Metric values
	if alertValue := labels["alert_value"]; alertValue != "" {
		parts = append(parts, fmt.Sprintf("**Metric Values:** %s", alertValue))
	}
	if threshold := labels["threshold"]; threshold != "" {
		parts = append(parts, fmt.Sprintf("**Threshold:** %s", threshold))
	}
	if currentValue := labels["currentValue"]; currentValue != "" {
		parts = append(parts, fmt.Sprintf("**Current Value:** %s", currentValue))
	}

	// Severity and environment
	if severity := labels["nb_alert_severity"]; severity != "" {
		parts = append(parts, fmt.Sprintf("**Severity:** %s", severity))
	}
	if env := labels["nb_environment"]; env != "" {
		parts = append(parts, fmt.Sprintf("**Environment:** %s", env))
	}

	// Firing count
	if firingCount := labels["nb_alert_firing_count"]; firingCount != "" {
		parts = append(parts, fmt.Sprintf("**Firing Alerts:** %s", firingCount))
	}

	if len(parts) == 0 {
		return nil
	}

	markdown := strings.Join(parts, "\n")

	return &event.EventEvidence{
		Type: "markdown",
		Data: map[string]any{
			"name": "Alert Rule Details",
			"data": markdown,
		},
		Insight: []event.EventEvidenceInsight{},
		AdditionalInfo: map[string]any{
			"action_name":            "alert_rule_details",
			"actual_action_name":     "alert_rule_details",
			"action_title":           "Alert Rule Details",
			"conditional_expression": "",
		},
	}
}
