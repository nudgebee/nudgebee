package integrations

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(ServiceNowWebhook{})
}

type ServiceNowWebhook struct {
}

const IntegrationServicenowWebhook = "servicenow_webhook"

func (m ServiceNowWebhook) Name() string {
	return IntegrationServicenowWebhook
}

func (m ServiceNowWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m ServiceNowWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of ServiceNow Webhook",
				Default:          "",
				AutoGenerateFunc: "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Inbound webhook auth token (shared secret used by SNOW Business Rule)",
				Default:          "",
				AutoGenerateFunc: "",
			},
			"instance_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "ServiceNow instance URL (e.g. https://mycompany.service-now.com). Used as the event_url base; enrichment credentials come from the separate 'servicenow' integration when configured.",
				Default:     "",
			},
		},
	}
}

func (m ServiceNowWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

type ServicenowEvent struct {
	// em_event fields (Event Management)
	ResolutionState    string      `json:"resolution_state"`
	ProcessingSNNode   interface{} `json:"processing_sn_node"`
	Description        string      `json:"description"`
	Source             string      `json:"source"`
	SysUpdatedOn       string      `json:"sys_updated_on"`
	Type               interface{} `json:"type"`
	CiIdentifier       interface{} `json:"ci_identifier"`
	SysUpdatedBy       string      `json:"sys_updated_by"`
	CiType             interface{} `json:"ci_type"`
	MetricName         interface{} `json:"metric_name"`
	ProcessingNotes    interface{} `json:"processing_notes"`
	Alert              interface{} `json:"alert"`
	SysCreatedOn       string      `json:"sys_created_on"`
	SysDomain          string      `json:"sys_domain"`
	State              string      `json:"state"`
	MessageKey         string      `json:"message_key"`
	SysCreatedBy       string      `json:"sys_created_by"`
	TimeOfEvent        string      `json:"time_of_event"`
	Severity           string      `json:"severity"`
	ErrorMsg           interface{} `json:"error_msg"`
	Resource           interface{} `json:"resource"`
	CmdbCI             interface{} `json:"cmdb_ci"`
	SysModCount        string      `json:"sys_mod_count"`
	EventRule          interface{} `json:"event_rule"`
	Classification     string      `json:"classification"`
	SysTags            string      `json:"sys_tags"`
	Bucket             string      `json:"bucket"`
	Node               string      `json:"node"`
	Processed          string      `json:"processed"`
	AdditionalInfo     string      `json:"additional_info"`
	ProcessingDuration interface{} `json:"processing_duration"`
	EventClass         string      `json:"event_class"`
	Table              string      `json:"table"`
	SysID              string      `json:"sys_id"`
	DisplayValue       string      `json:"display_value"`

	// Incident fields (Business Rules / Flow Designer)
	Number           string `json:"number"`
	ShortDescription string `json:"short_description"`
	IncidentState    string `json:"incident_state"`
	Priority         string `json:"priority"`
	Impact           string `json:"impact"`
	Urgency          string `json:"urgency"`
	Category         string `json:"category"`
	Subcategory      string `json:"subcategory"`
	AssignedTo       string `json:"assigned_to"`
	AssignmentGroup  string `json:"assignment_group"`
	CallerID         string `json:"caller_id"`
	OpenedBy         string `json:"opened_by"`
	OpenedAt         string `json:"opened_at"`
	ResolvedAt       string `json:"resolved_at"`
	ClosedAt         string `json:"closed_at"`
	CloseCode        string `json:"close_code"`
	CloseNotes       string `json:"close_notes"`
	CmdbCIDisplay    string `json:"cmdb_ci_display"`
	BusinessService  string `json:"business_service"`
	Company          string `json:"company"`
	Location         string `json:"location"`
	ContactType      string `json:"contact_type"`
	CorrelationID    string `json:"correlation_id"`
	SysClassName     string `json:"sys_class_name"`
	Active           string `json:"active"`
	WorkNotes        string `json:"work_notes"`
	Comments         string `json:"comments"`
}

func (m ServiceNowWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var servicenowEvent ServicenowEvent
	err := json.Unmarshal([]byte(webhookPayloadString), &servicenowEvent)
	if err != nil {
		return nil, err
	}

	// Also decode into a generic map so every SNOW field — including custom
	// u_* fields and the legacy camelCase payload shape — is preserved as
	// evidence and reachable by downstream consumers via JSONPath. Mirrors
	// the pattern used by pagerduty_webhook / grafana_webhook / datadog_webhook.
	rawPayload := make(map[string]any)
	if err := json.Unmarshal([]byte(webhookPayloadString), &rawPayload); err != nil {
		return nil, err
	}

	// Backfill the typed struct from the legacy camelCase payload shape
	// (shortDescription / incidentId / recordSysId) so existing routing
	// logic doesn't fall through to the em_event path.
	applyShapeCMapping(&servicenowEvent, rawPayload)

	var parsedPayload core.EventIncomingWebhook
	isIncident := false

	switch {
	case servicenowEvent.Source == "prometheus":
		parsedPayload, err = processPrometheusEvent(servicenowEvent)
	case servicenowEvent.Number != "":
		parsedPayload, err = processIncidentEvent(servicenowEvent, rawPayload, settings)
		isIncident = true
	default:
		parsedPayload, err = processEmEvent(servicenowEvent, rawPayload, settings)
	}
	if err != nil {
		return nil, err
	}

	// Post-receipt enrichment: most SNOW Business Rules send a stripped 15-field
	// payload that omits cmdb_ci, custom u_* fields, and the original alarm
	// payload. When the tenant has the "servicenow" ticketing integration
	// configured (single source of truth for API creds), fetch the full record
	// by sys_id and merge it into labels/subject/evidence. Failures are logged
	// and swallowed — never block ingestion.
	if isIncident && servicenowEvent.SysID != "" {
		if creds, ok := resolveServiceNowAPICreds(sc); ok {
			EnrichWithServiceNowIncident(sc, &parsedPayload, creds, servicenowEvent.SysID)
		}
	}

	return []core.EventIncomingWebhook{parsedPayload}, nil
}

// processPrometheusEvent handles Prometheus alerts forwarded through ServiceNow Event Management.
func processPrometheusEvent(servicenowEvent ServicenowEvent) (core.EventIncomingWebhook, error) {
	parsedPayload := core.EventIncomingWebhook{
		WebhookId:        servicenowEvent.SysID,
		EventType:        servicenowEvent.EventClass,
		EventId:          servicenowEvent.MessageKey,
		EventDescription: servicenowEvent.Description,
		EventStatus:      servicenowEvent.State,
		EventPriority:    servicenowEvent.Severity,
		EventCreatedAt:   time.Now().UTC(),
		EventTitle:       servicenowEvent.Description,
	}

	additionalInfo := make(map[string]interface{})
	_ = json.Unmarshal([]byte(servicenowEvent.AdditionalInfo), &additionalInfo)

	if pod, ok := additionalInfo["flattened.labels.pod"].(string); ok {
		parsedPayload.EventSubjectName = pod
	}
	if ns, ok := additionalInfo["flattened.labels.namespace"].(string); ok {
		parsedPayload.EventSubjectNamespace = ns
	}

	investigation := core.EventIncomingWebhookInvestigation{}
	if labels, ok := additionalInfo["commonLabels"].(string); ok {
		var labelsMap map[string]interface{}
		if err := json.Unmarshal([]byte(labels), &labelsMap); err == nil {
			labelsStringMap := make(map[string]string)
			for k, v := range labelsMap {
				if strVal, ok := v.(string); ok {
					labelsStringMap[k] = strVal
				}
			}
			investigation.Labels = labelsStringMap
		}
	}
	if alertName, ok := additionalInfo["flattened.labels.alertname"].(string); ok {
		investigation.RuleName = alertName
		investigation.RuleId = alertName
		investigation.RuleType = "prometheus"
	}
	if annotations, ok := additionalInfo["commonAnnotations"].(string); ok {
		_ = json.Unmarshal([]byte(annotations), &investigation.Annotations)
	}
	if url, ok := additionalInfo["flattened.generatorURL"].(string); ok {
		parsedPayload.EventUrl = url
	}
	if fingerPrint, ok := additionalInfo["flattened.fingerprint"].(string); ok {
		investigation.Fingerprint = fingerPrint
	}
	if sourceUrl, ok := additionalInfo["externalURL"].(string); ok {
		investigation.SourceUrl = sourceUrl
	}
	if status, ok := additionalInfo["flattened.status"].(string); ok {
		parsedPayload.EventStatus = status
		switch status {
		case "firing":
			investigation.Status = event.EventStatusFiring
		case "resolved":
			investigation.Status = event.EventStatusResolved
		case "closed":
			investigation.Status = event.EventStatusClosed
		}
	}
	if severity, ok := additionalInfo["flattened.labels.severity"].(string); ok {
		switch severity {
		case "critical", "high":
			investigation.Severity = event.EventPriortiyHigh
		case "warning", "medium":
			investigation.Severity = event.EventPriortiyMedium
		case "info", "low":
			investigation.Severity = event.EventPriortiyLow
		}
	}

	parsedPayload.Investigation = investigation
	return parsedPayload, nil
}

// processIncidentEvent handles native ServiceNow incident payloads (from Business Rules or Flow Designer).
func processIncidentEvent(servicenowEvent ServicenowEvent, rawPayload map[string]any, settings []core.IntegrationConfigValue) (core.EventIncomingWebhook, error) {
	// Determine incident state — prefer incident_state, fall back to state
	incidentState := servicenowEvent.IncidentState
	if incidentState == "" {
		incidentState = servicenowEvent.State
	}

	title := servicenowEvent.ShortDescription
	if title == "" {
		title = servicenowEvent.Number
	}

	// Build labels from all available incident fields
	labels := make(map[string]string)
	addLabel(labels, "number", servicenowEvent.Number)
	addLabel(labels, "state", incidentState)
	addLabel(labels, "priority", servicenowEvent.Priority)
	addLabel(labels, "impact", servicenowEvent.Impact)
	addLabel(labels, "urgency", servicenowEvent.Urgency)
	addLabel(labels, "severity", servicenowEvent.Severity)
	addLabel(labels, "category", servicenowEvent.Category)
	addLabel(labels, "subcategory", servicenowEvent.Subcategory)
	addLabel(labels, "assigned_to", servicenowEvent.AssignedTo)
	addLabel(labels, "assignment_group", servicenowEvent.AssignmentGroup)
	addLabel(labels, "caller_id", servicenowEvent.CallerID)
	addLabel(labels, "opened_by", servicenowEvent.OpenedBy)
	addLabel(labels, "cmdb_ci", servicenowEvent.CmdbCIDisplay)
	addLabel(labels, "business_service", servicenowEvent.BusinessService)
	addLabel(labels, "company", servicenowEvent.Company)
	addLabel(labels, "location", servicenowEvent.Location)
	addLabel(labels, "contact_type", servicenowEvent.ContactType)
	addLabel(labels, "correlation_id", servicenowEvent.CorrelationID)
	addLabel(labels, "sys_class_name", servicenowEvent.SysClassName)
	addLabel(labels, "close_code", servicenowEvent.CloseCode)
	addLabel(labels, "close_notes", servicenowEvent.CloseNotes)
	addLabel(labels, "source", servicenowEvent.Source)

	// Build event URL from instance URL in settings
	eventURL := buildServiceNowURL(settings, "incident", servicenowEvent.SysID)

	// Build evidence. Use the raw payload map so every SNOW field — including
	// custom u_* fields not declared on the typed struct — is preserved.
	insightMsg := fmt.Sprintf("ServiceNow Incident %s: %s", servicenowEvent.Number, title)
	evidences := []event.EventEvidence{
		{
			Type: "json",
			Data: map[string]any{
				"name": "ServiceNow Incident",
				"data": rawPayload,
			},
			Insight: []event.EventEvidenceInsight{
				{Message: insightMsg, Severity: "info"},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "servicenow_incident",
				"actual_action_name":     "servicenow_incident",
				"action_title":           "ServiceNow Incident Details",
				"conditional_expression": "",
			},
		},
	}

	fingerprint := servicenowEvent.SysID
	if servicenowEvent.CorrelationID != "" {
		fingerprint = servicenowEvent.CorrelationID
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      servicenowEvent.Number,
		RuleType:    "servicenow_incident",
		Fingerprint: fingerprint,
		Status:      mapServiceNowIncidentState(incidentState),
		Severity:    mapServiceNowPriority(servicenowEvent.Priority, servicenowEvent.Urgency, servicenowEvent.Impact),
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Subject resolution
	subjectName := servicenowEvent.CmdbCIDisplay
	if subjectName == "" {
		subjectName = servicenowEvent.BusinessService
	}
	subjectKind := ""
	if subjectName != "" {
		subjectKind = "service"
	}

	parsedPayload := core.EventIncomingWebhook{
		WebhookId:             servicenowEvent.SysID,
		EventType:             "servicenow_incident",
		EventId:               servicenowEvent.Number,
		EventUrl:              eventURL,
		EventDescription:      servicenowEvent.Description,
		EventStatus:           string(mapServiceNowIncidentState(incidentState)),
		EventPriority:         string(mapServiceNowPriority(servicenowEvent.Priority, servicenowEvent.Urgency, servicenowEvent.Impact)),
		EventCreatedAt:        parseServiceNowTime(servicenowEvent.OpenedAt),
		EventTitle:            title,
		Investigation:         investigation,
		EventSubjectName:      subjectName,
		EventSubjectKind:      subjectKind,
		EventSubjectNamespace: servicenowEvent.Category,
		EventSubjectOwner:     servicenowEvent.AssignedTo,
		EventSubjectOwnerKind: "user",
	}

	return parsedPayload, nil
}

// processEmEvent handles generic ServiceNow em_event payloads (non-Prometheus sources).
func processEmEvent(servicenowEvent ServicenowEvent, rawPayload map[string]any, settings []core.IntegrationConfigValue) (core.EventIncomingWebhook, error) {
	// Build labels from em_event fields
	labels := make(map[string]string)
	addLabel(labels, "source", servicenowEvent.Source)
	addLabel(labels, "event_class", servicenowEvent.EventClass)
	addLabel(labels, "node", servicenowEvent.Node)
	addLabel(labels, "classification", servicenowEvent.Classification)
	addLabel(labels, "severity", servicenowEvent.Severity)
	addLabel(labels, "state", servicenowEvent.State)
	addLabel(labels, "bucket", servicenowEvent.Bucket)
	addLabel(labels, "table", servicenowEvent.Table)
	addLabel(labels, "processed", servicenowEvent.Processed)
	if resource, ok := interfaceToString(servicenowEvent.Resource); ok {
		addLabel(labels, "resource", resource)
	}
	if metricName, ok := interfaceToString(servicenowEvent.MetricName); ok {
		addLabel(labels, "metric_name", metricName)
	}

	// Parse additional_info JSON into labels
	if servicenowEvent.AdditionalInfo != "" {
		var additionalInfo map[string]interface{}
		if err := json.Unmarshal([]byte(servicenowEvent.AdditionalInfo), &additionalInfo); err == nil {
			for k, v := range additionalInfo {
				if strVal, ok := v.(string); ok {
					labels["additional_info."+k] = strVal
				}
			}
		}
	}

	// Parse ci_identifier JSON into labels
	if ciIdent, ok := servicenowEvent.CiIdentifier.(string); ok && ciIdent != "" {
		var ciMap map[string]interface{}
		if err := json.Unmarshal([]byte(ciIdent), &ciMap); err == nil {
			for k, v := range ciMap {
				if strVal, ok := v.(string); ok {
					labels["ci."+k] = strVal
				}
			}
		}
	}

	ruleName := servicenowEvent.EventClass
	if ruleName == "" {
		ruleName = servicenowEvent.Description
	}

	fingerprint := servicenowEvent.MessageKey
	if fingerprint == "" {
		fingerprint = servicenowEvent.SysID
	}

	// Build evidence
	insightMsg := fmt.Sprintf("ServiceNow Event: %s", servicenowEvent.Description)
	if servicenowEvent.Node != "" {
		insightMsg = fmt.Sprintf("ServiceNow Event on %s: %s", servicenowEvent.Node, servicenowEvent.Description)
	}
	evidences := []event.EventEvidence{
		{
			Type: "json",
			Data: map[string]any{
				"name": "ServiceNow Event",
				"data": rawPayload,
			},
			Insight: []event.EventEvidenceInsight{
				{Message: insightMsg, Severity: "info"},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "servicenow_event",
				"actual_action_name":     "servicenow_event",
				"action_title":           "ServiceNow Event Details",
				"conditional_expression": "",
			},
		},
	}

	// Build event URL from instance URL in settings
	tableName := servicenowEvent.Table
	if tableName == "" {
		tableName = "em_event"
	}
	eventURL := buildServiceNowURL(settings, tableName, servicenowEvent.SysID)

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    ruleName,
		RuleId:      fingerprint,
		RuleType:    "servicenow_event",
		Fingerprint: fingerprint,
		Status:      mapServiceNowEventState(servicenowEvent.State),
		Severity:    mapServiceNowSeverity(servicenowEvent.Severity),
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Subject resolution: prefer Node (hostname), fall back to Resource
	subjectName := servicenowEvent.Node
	subjectKind := ""
	if subjectName != "" {
		subjectKind = "host"
	} else if resource, ok := interfaceToString(servicenowEvent.Resource); ok && resource != "" {
		subjectName = resource
		subjectKind = "resource"
	}

	parsedPayload := core.EventIncomingWebhook{
		WebhookId:             servicenowEvent.SysID,
		EventType:             servicenowEvent.EventClass,
		EventId:               fingerprint,
		EventUrl:              eventURL,
		EventDescription:      servicenowEvent.Description,
		EventStatus:           string(mapServiceNowEventState(servicenowEvent.State)),
		EventPriority:         string(mapServiceNowSeverity(servicenowEvent.Severity)),
		EventCreatedAt:        time.Now().UTC(),
		EventTitle:            servicenowEvent.Description,
		Investigation:         investigation,
		EventSubjectName:      subjectName,
		EventSubjectKind:      subjectKind,
		EventSubjectOwner:     subjectName,
		EventSubjectOwnerKind: subjectKind,
	}

	return parsedPayload, nil
}

// addLabel adds a key-value pair to the labels map if the value is non-empty.
func addLabel(labels map[string]string, key, value string) {
	if value != "" {
		labels[key] = value
	}
}

// interfaceToString attempts to convert an interface{} to a string.
func interfaceToString(v interface{}) (string, bool) {
	if v == nil {
		return "", false
	}
	if s, ok := v.(string); ok && s != "" {
		return s, true
	}
	return "", false
}

// applyShapeCMapping backfills the typed ServicenowEvent struct from the
// legacy camelCase payload shape some SNOW configurations send
// (e.g. {"shortDescription":..., "incidentId":..., "recordSysId":...}).
// Without this, those payloads have empty Number/SysID/ShortDescription,
// the routing switch in ProcessEventWebook falls through to processEmEvent,
// and the resulting event has no SNOW context.
func applyShapeCMapping(s *ServicenowEvent, raw map[string]any) {
	if s.Number == "" {
		if v, ok := raw["incidentId"].(string); ok {
			s.Number = v
		}
	}
	if s.SysID == "" {
		if v, ok := raw["recordSysId"].(string); ok {
			s.SysID = v
		}
	}
	if s.ShortDescription == "" {
		if v, ok := raw["shortDescription"].(string); ok {
			s.ShortDescription = v
		}
	}
}

// buildServiceNowURL constructs a ServiceNow record URL from settings.
// table should be e.g. "incident" or "em_event".
func buildServiceNowURL(settings []core.IntegrationConfigValue, table, sysID string) string {
	if sysID == "" {
		return ""
	}
	instanceURL, found := core.GetSettingValue(settings, "instance_url")
	if !found || instanceURL == "" {
		instanceURL, _ = core.GetSettingValue(settings, "url")
	}
	instanceURL = strings.TrimRight(instanceURL, "/")
	if instanceURL != "" {
		return fmt.Sprintf("%s/%s.do?sys_id=%s", instanceURL, table, sysID)
	}
	return ""
}

// parseServiceNowTime parses a ServiceNow datetime string, falling back to current time.
func parseServiceNowTime(timeStr string) time.Time {
	if timeStr == "" {
		return time.Now().UTC()
	}
	// ServiceNow uses "2006-01-02 15:04:05" format
	t, err := time.Parse("2006-01-02 15:04:05", timeStr)
	if err != nil {
		return time.Now().UTC()
	}
	return t.UTC()
}

func (m ServiceNowWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// mapServiceNowSeverity maps em_event severity to EventPriortiy.
// Accepts both raw codes ("2") and display strings ("2 - Critical") since
// different SNOW configurations send one or the other.
// SNOW em_event severity: 1=Clear, 2=Critical, 3=Major, 4=Minor, 5=Warning, 0=Info
func mapServiceNowSeverity(severity string) event.EventPriortiy {
	rank, ok := snowPriorityRank(severity)
	if !ok {
		return event.EventPriortiyInfo
	}
	switch rank {
	case 2, 3: // Critical, Major
		return event.EventPriortiyHigh
	case 4: // Minor
		return event.EventPriortiyLow
	case 5: // Warning
		return event.EventPriortiyMedium
	default: // 0=Info, 1=Clear, anything else
		return event.EventPriortiyInfo
	}
}

// mapServiceNowPriority maps incident priority (with urgency/impact fallback) to EventPriortiy.
// Accepts both raw codes ("3") and display strings ("3 - Moderate") since SNOW
// Business Rules / Flow Designer typically send the latter.
// Priority uses a 1-5 scale: 1=Critical, 2=High, 3=Moderate, 4=Low, 5=Planning
// Urgency/Impact use a 1-3 scale: 1=High, 2=Medium, 3=Low
func mapServiceNowPriority(priority, urgency, impact string) event.EventPriortiy {
	if rank, ok := snowPriorityRank(priority); ok {
		switch rank {
		case 1, 2: // Critical, High
			return event.EventPriortiyHigh
		case 3: // Moderate
			return event.EventPriortiyMedium
		case 4, 5: // Low, Planning
			return event.EventPriortiyLow
		}
	}

	// Fall back to urgency, then impact (1-3 scale).
	value := urgency
	if value == "" {
		value = impact
	}
	if rank, ok := snowPriorityRank(value); ok {
		switch rank {
		case 1: // High
			return event.EventPriortiyHigh
		case 2: // Medium
			return event.EventPriortiyMedium
		case 3: // Low
			return event.EventPriortiyLow
		}
	}
	return event.EventPriortiyMedium
}

// snowPriorityRank extracts the leading numeric rank from a SNOW
// priority / urgency / impact / severity value. Accepts both raw codes
// ("3") and display strings ("3 - Moderate"). Returns false when the
// value is empty or has no leading integer.
func snowPriorityRank(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	head := s
	for i, r := range s {
		if r == ' ' || r == '-' || r == '\t' {
			head = s[:i]
			break
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil {
		return 0, false
	}
	return n, true
}

// mapServiceNowEventState maps em_event state string to EventStatus.
// ServiceNow em_event states: "Ready"=open, "Reopen"=open, "Closing"=resolved, "Closed"=closed
func mapServiceNowEventState(state string) event.EventStatus {
	lower := strings.ToLower(state)
	switch lower {
	case "closing", "resolved":
		return event.EventStatusResolved
	case "closed":
		return event.EventStatusClosed
	default:
		return event.EventStatusFiring
	}
}

// mapServiceNowIncidentState maps incident state to EventStatus.
// Accepts both numeric codes (1=New, 2=In Progress, 3=On Hold, 6=Resolved,
// 7=Closed, 8=Canceled) and the corresponding display strings sent by SNOW
// Business Rules / Flow Designer payloads ("New", "In Progress", "Resolved", …).
func mapServiceNowIncidentState(state string) event.EventStatus {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "6", "resolved":
		return event.EventStatusResolved
	case "7", "8", "closed", "canceled", "cancelled":
		return event.EventStatusClosed
	default: // New, In Progress, On Hold, …
		return event.EventStatusFiring
	}
}
