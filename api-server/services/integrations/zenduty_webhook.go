package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
)

// ZenDuty Webhook Payload Structures

// ZenDutyWebhookPayload represents the top-level webhook payload from ZenDuty.
type ZenDutyWebhookPayload struct {
	Payload ZenDutyPayloadInner `json:"payload"`
}

// ZenDutyPayloadInner contains the inner payload from ZenDuty webhook.
type ZenDutyPayloadInner struct {
	EventType string          `json:"event_type"`
	Incident  ZenDutyIncident `json:"incident"`
}

// ZenDutyIncident represents an incident in ZenDuty.
type ZenDutyIncident struct {
	UniqueID       string            `json:"unique_id"`
	Title          string            `json:"title"`
	Summary        string            `json:"summary"`
	Status         int               `json:"status"`
	Urgency        int               `json:"urgency"`
	Priority       int               `json:"priority"`
	CreationDate   string            `json:"creation_date"`
	AcknowledgedAt string            `json:"acknowledged_date,omitempty"`
	ResolvedAt     string            `json:"resolved_date,omitempty"`
	Service        ZenDutyServiceRef `json:"service"`
	AssignedTo     json.RawMessage   `json:"assigned_to,omitempty"` // Can be object or array
	Tags           []string          `json:"tags,omitempty"`
	HTMLURL        string            `json:"html_url,omitempty"`
	AlertSource    string            `json:"alert_source,omitempty"`
	AlertType      string            `json:"alert_type,omitempty"`
	IncidentNumber int               `json:"incident_number,omitempty"`
	IncidentKey    string            `json:"incident_key,omitempty"`
}

// ZenDutyServiceRef represents a service reference in ZenDuty.
type ZenDutyServiceRef struct {
	UniqueID string `json:"unique_id"`
	Name     string `json:"name"`
}

// ZenDutyUserRef represents a user reference in ZenDuty.
type ZenDutyUserRef struct {
	UniqueID  string `json:"unique_id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// ZenDuty Status Constants
const (
	ZenDutyStatusOpen         = -1
	ZenDutyStatusAll          = 0
	ZenDutyStatusTriggered    = 1
	ZenDutyStatusAcknowledged = 2
	ZenDutyStatusResolved     = 3
)

// ZenDuty Urgency Constants
const (
	ZenDutyUrgencyLow    = 0
	ZenDutyUrgencyMedium = 1
	ZenDutyUrgencyHigh   = 2
)

// parseAssignedTo parses the assigned_to field which can be either a single object or an array.
func parseAssignedTo(raw json.RawMessage) []ZenDutyUserRef {
	if len(raw) == 0 {
		return nil
	}

	// Try parsing as array first
	var users []ZenDutyUserRef
	if err := json.Unmarshal(raw, &users); err == nil {
		return users
	}

	// Try parsing as single object
	var user ZenDutyUserRef
	if err := json.Unmarshal(raw, &user); err == nil {
		return []ZenDutyUserRef{user}
	}

	return nil
}

func init() {
	core.RegisterIntegration(ZenDutyWebhook{})
}

type ZenDutyWebhook struct{}

const IntegrationZendutyWebhook = "zenduty_webhook"

func (z ZenDutyWebhook) Name() string {
	return IntegrationZendutyWebhook
}

func (z ZenDutyWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (z ZenDutyWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of ZenDuty Webhook",
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
				Default:          "",
				AutoGenerateFunc: "",
			},
		},
	}
}

func (z ZenDutyWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (z ZenDutyWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

var (
	zenDutyReNamespace   = regexp.MustCompile(`namespace=\"([^\"]+)\"`)
	zenDutyRePod         = regexp.MustCompile(`pod=\"([^\"]+)\"`)
	zenDutyReServiceName = regexp.MustCompile(`service_name=\"([^\"]+)\"`)
)

func (z ZenDutyWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := ZenDutyWebhookPayload{}
	err := common.UnmarshalJson([]byte(webhookPayloadString), &payload)
	if err != nil {
		altPayload := map[string]any{}
		if altErr := common.UnmarshalJson([]byte(webhookPayloadString), &altPayload); altErr != nil {
			return []core.EventIncomingWebhook{}, fmt.Errorf("zendutywebhook: failed to parse payload: %w", err)
		}
		return []core.EventIncomingWebhook{}, errors.New("zendutywebhook: invalid payload format")
	}

	if payload.Payload.EventType == "" {
		return []core.EventIncomingWebhook{}, errors.New("zendutywebhook: invalid payload, event_type not found")
	}

	supportedEvents := map[string]bool{
		"incident.triggered":    true,
		"incident.resolved":     true,
		"incident.acknowledged": true,
		"triggered":             true,
		"resolved":              true,
		"acknowledged":          true,
		"test":                  true,
	}
	if !supportedEvents[payload.Payload.EventType] {
		return nil, core.ErrEventNotSupported
	}

	incident := payload.Payload.Incident
	if incident.UniqueID == "" {
		return []core.EventIncomingWebhook{}, errors.New("zendutywebhook: invalid payload, incident.unique_id not found")
	}

	parsedPayload := core.EventIncomingWebhook{}
	parsedPayload.WebhookId = fmt.Sprintf("zd-%s-%d", incident.UniqueID, time.Now().Unix())
	parsedPayload.EventId = incident.UniqueID
	parsedPayload.EventTitle = incident.Title

	var namespace, pod string
	if match := zenDutyReNamespace.FindStringSubmatch(incident.Title); len(match) == 2 {
		namespace = match[1]
	}
	if match := zenDutyRePod.FindStringSubmatch(incident.Title); len(match) == 2 {
		pod = match[1]
	} else if match := zenDutyReServiceName.FindStringSubmatch(incident.Title); len(match) == 2 {
		pod = match[1]
	}
	parsedPayload.EventSubjectName = pod
	parsedPayload.EventSubjectNamespace = namespace

	parsedPayload.EventDescription = incident.Summary
	parsedPayload.EventType = "incident"

	if incident.CreationDate != "" {
		parsedTime, err := time.Parse(time.RFC3339, incident.CreationDate)
		if err != nil {
			parsedTime, err = time.Parse("2006-01-02T15:04:05Z", incident.CreationDate)
			if err != nil {
				parsedTime = time.Now()
			}
		}
		parsedPayload.EventCreatedAt = parsedTime
	} else {
		parsedPayload.EventCreatedAt = time.Now()
	}

	parsedPayload.EventStatus = mapZenDutyStatusToString(incident.Status)

	parsedPayload.EventPriority = mapZenDutyUrgencyToString(incident.Urgency)

	if incident.HTMLURL != "" {
		parsedPayload.EventUrl = incident.HTMLURL
	} else {
		parsedPayload.EventUrl = fmt.Sprintf("https://www.zenduty.com/incidents/%s", incident.UniqueID)
	}

	parsedPayload.EventTags = incident.Tags
	if parsedPayload.EventTags == nil {
		parsedPayload.EventTags = []string{}
	}

	alert := core.EventIncomingWebhookInvestigation{}
	alert.SourceUrl = parsedPayload.EventUrl
	alert.RuleId = incident.UniqueID
	alert.RuleName = incident.Title
	alert.RuleType = "zenduty_incident"

	switch incident.Status {
	case ZenDutyStatusTriggered:
		alert.Status = event.EventStatusFiring
	case ZenDutyStatusResolved:
		alert.Status = event.EventStatusResolved
	default:
		alert.Status = event.EventStatusFiring
	}

	switch incident.Urgency {
	case ZenDutyUrgencyHigh:
		alert.Severity = event.EventPriortiyHigh
	case ZenDutyUrgencyMedium:
		alert.Severity = event.EventPriortiyMedium
	default:
		alert.Severity = event.EventPriortiyLow
	}

	// Add service info to labels
	alert.Labels = map[string]string{
		"service_id":   incident.Service.UniqueID,
		"service_name": incident.Service.Name,
	}

	if incident.AlertSource != "" {
		alert.Labels["alert_source"] = incident.AlertSource
	}
	if incident.AlertType != "" {
		alert.Labels["alert_type"] = incident.AlertType
	}

	parsedPayload.Investigation = alert

	// Remap account before enrichment so workload lookups target the correct account
	accountMapping := core.ParseAccountMapping(settings, sc.GetLogger())
	accountId = core.ApplyAccountMapping(accountId, parsedPayload.Investigation.Labels, accountMapping)

	// Validate and enrich subject against k8s_workloads inventory
	if parsedPayload.EventSubjectName != "" {
		matchWorkloadAndEnrich(sc, &parsedPayload, accountId)
	}

	// LLM fallback: if no subject found after deterministic parsing, use LLM
	if parsedPayload.EventSubjectName == "" {
		tenantId := sc.GetSecurityContext().GetTenantId()
		if tenant.IsFeatureEnabled(sc, tenantId, tenant.FEATURE_WEBHOOK_LLM_RESOLUTION) {
			resolveZendutySubjectUsingLLM(sc, &parsedPayload, accountId)
		} else {
			parsedPayload.Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}

	// Auto-learn: save confirmed title → service mapping for future LLM prompts
	if parsedPayload.EventSubjectName != "" && parsedPayload.EventTitle != "" {
		LearnSubjectMapping(sc, sc.GetSecurityContext().GetTenantId(), TenantAttrZendutyIncidentsKey, parsedPayload.EventTitle, parsedPayload.EventSubjectName)
	}

	parsedPayload.AccountId = accountId
	return []core.EventIncomingWebhook{parsedPayload}, nil
}

// mapZenDutyStatusToString converts ZenDuty numeric status to Nudgebee standard status.
// Maps: triggered->triggered, acknowledged->firing, resolved->resolved, suppressed->resolved
func mapZenDutyStatusToString(status int) string {
	switch status {
	case ZenDutyStatusTriggered:
		return "triggered"
	case ZenDutyStatusAcknowledged:
		return "firing"
	case ZenDutyStatusResolved:
		return "resolved"
	default:
		return "triggered"
	}
}

// mapZenDutyUrgencyToString converts ZenDuty numeric urgency to string.
func mapZenDutyUrgencyToString(urgency int) string {
	switch urgency {
	case ZenDutyUrgencyLow:
		return "low"
	case ZenDutyUrgencyMedium:
		return "medium"
	case ZenDutyUrgencyHigh:
		return "high"
	default:
		return "medium"
	}
}

// GetZenDutyIncidentDetails fetches full incident details from ZenDuty API.
func GetZenDutyIncidentDetails(apiKey, incidentID string) (*ZenDutyIncident, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	url := fmt.Sprintf("%s/incidents/%s/", ZenDutyDefaultURL, incidentID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch incident: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch incident with status %d: %s", resp.StatusCode, string(body))
	}

	var incident ZenDutyIncident
	if err := json.NewDecoder(resp.Body).Decode(&incident); err != nil {
		return nil, fmt.Errorf("failed to decode incident response: %w", err)
	}

	return &incident, nil
}

// resolveZendutySubjectUsingLLM passes the full alert context to the LLM to extract
// resource identification labels. Fallback when regex cannot identify a resource.
func resolveZendutySubjectUsingLLM(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, accountId string) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: failed to get database manager for LLM matching", "error", err)
		return
	}

	tenantId := sc.GetSecurityContext().GetTenantId()

	var names []string
	err = dbms.Db.Select(&names, `
		SELECT DISTINCT name FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2
		  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
	`, tenantId, accountId)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: failed to query workload names for LLM", "error", err)
		return
	}
	if len(names) == 0 {
		return
	}

	labelsJSON, _ := json.Marshal(parsedPayload.Investigation.Labels)

	historicalMappings, err := GetSubjectMappingsForPrompt(sc, tenantId, TenantAttrZendutyIncidentsKey, 1000)
	if err != nil {
		sc.GetLogger().Warn("zendutywebhook: failed to load historical mappings", "error", err)
	}
	historicalPatterns := FormatSubjectMappingsForPrompt(historicalMappings, 1000)

	prompt := fmt.Sprintf(`@llm You are a resource label extractor for monitoring alerts. Analyze the complete alert data below and extract resource identification labels.

## Alert Data
**Title:** %s
**Description:** %s
**Source URL:** %s
**Existing Labels:** %s

## Historical patterns (title → service)
%s

## Known Running Services/Workloads
%s

## Task
From the alert data above, extract the following labels. Look at ALL the data — title, description, labels, URLs, and historical patterns — to find clues about which resource this alert is about.

Return ONLY a valid JSON object with these fields (use empty string "" if not found):
{
  "subject_name": "<k8s workload/service name from the Running Services list that this alert is about>",
  "namespace": "<k8s namespace>",
  "cluster": "<k8s cluster name>",
  "pod_name": "<specific pod name if mentioned>",
  "service_name": "<application/service name>"
}

RULES:
1. For subject_name: MUST be an exact match from the Running Services list, or empty string
2. Look for service names in alert titles (e.g. "booking-service down" → subject_name="booking-service")
3. Look for service names in pipe-delimited titles (e.g. "Critical | Prod | EKS | payment-service | high latency")
4. Look for k8s resource references in labels (job, pod, deployment names)
5. If the title closely matches a Historical pattern, use the same service mapping
6. If you cannot confidently identify a resource, return empty strings — do NOT guess
7. Return ONLY the JSON object, no explanation, no markdown fences`,
		parsedPayload.EventTitle,
		parsedPayload.EventDescription,
		parsedPayload.Investigation.SourceUrl,
		string(labelsJSON),
		historicalPatterns,
		strings.Join(names, ", "),
	)

	chatRequest := llm.ConversationApiRequest{
		Query:     prompt,
		AccountId: accountId,
		UserId:    sc.GetSecurityContext().GetUserId(),
		Async:     false,
		Source:    "webhook_label_extraction",
	}

	response, err := llm.ChatCompletion(sc, chatRequest)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: LLM label extraction failed", "error", err)
		return
	}
	if response == nil || len(response.Response) == 0 {
		sc.GetLogger().Warn("zendutywebhook: LLM returned empty response")
		return
	}

	var extractedLabels map[string]string
	responseText := response.Response[0]
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	if err := json.Unmarshal([]byte(responseText), &extractedLabels); err != nil {
		sc.GetLogger().Error("zendutywebhook: failed to parse LLM response as JSON",
			"error", err, "response", responseText)
		return
	}

	sc.GetLogger().Info("zendutywebhook: LLM extracted labels", "labels", extractedLabels, "title", parsedPayload.EventTitle)

	if subjectName := extractedLabels["subject_name"]; subjectName != "" {
		parsedPayload.EventSubjectName = subjectName
		parsedPayload.Investigation.Labels["nb_llm_match"] = subjectName
		common.MetricsSubjectResolution(sc.GetContext(), IntegrationZendutyWebhook, "live", "matched", tenantId)
	} else {
		parsedPayload.Investigation.Labels["nb_llm_match"] = "not_found"
		common.MetricsSubjectResolution(sc.GetContext(), IntegrationZendutyWebhook, "live", "not_found", tenantId)
	}

	if ns := extractedLabels["namespace"]; ns != "" && parsedPayload.EventSubjectNamespace == "" {
		parsedPayload.EventSubjectNamespace = ns
		parsedPayload.Investigation.Labels["namespace"] = ns
	}

	for _, key := range []string{"cluster", "pod_name", "service_name"} {
		if val := extractedLabels[key]; val != "" {
			if parsedPayload.Investigation.Labels[key] == "" {
				parsedPayload.Investigation.Labels[key] = val
			}
		}
	}

	if parsedPayload.EventSubjectName != "" {
		matchWorkloadAndEnrich(sc, parsedPayload, accountId)
	}
}

// EnrichWithZenDutyIncident enriches the event with full incident details from ZenDuty API.
func EnrichWithZenDutyIncident(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, apiKey string) {
	incident, err := GetZenDutyIncidentDetails(apiKey, parsedPayload.EventId)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: failed to enrich incident", "error", err, "incident_id", parsedPayload.EventId)
		return
	}

	// Update payload with enriched data
	if incident.Summary != "" && parsedPayload.EventDescription == "" {
		parsedPayload.EventDescription = incident.Summary
	}

	// Add additional tags from enrichment
	if len(incident.Tags) > 0 {
		parsedPayload.EventTags = append(parsedPayload.EventTags, incident.Tags...)
	}

	// Update investigation labels with enriched data
	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = make(map[string]string)
	}

	assignees := parseAssignedTo(incident.AssignedTo)
	if len(assignees) > 0 {
		parsedPayload.Investigation.Labels["assigned_to"] = assignees[0].Email
	}
	for i, assignee := range assignees {
		parsedPayload.Investigation.Labels[fmt.Sprintf("assignee_%d_email", i)] = assignee.Email
	}
}
