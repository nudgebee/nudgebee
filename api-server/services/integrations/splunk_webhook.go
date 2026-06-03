package integrations

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(SplunkWebhook{})
}

const IntegrationSplunkWebhook = "splunk_webhook"

type SplunkWebhook struct{}

func (m SplunkWebhook) Name() string {
	return IntegrationSplunkWebhook
}

func (m SplunkWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m SplunkWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Splunk Webhook",
				Default:     "",
				Priority:    100,
			},
			"account_id": {
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

func (m SplunkWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m SplunkWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// SplunkWebhookResult represents a single result/event from a Splunk alert
type SplunkWebhookResult struct {
	Time       string `json:"_time"`
	Raw        string `json:"_raw"`
	Host       string `json:"host"`
	Source     string `json:"source"`
	Sourcetype string `json:"sourcetype"`
	Index      string `json:"index"`
	Severity   string `json:"severity"`
	Urgency    string `json:"urgency"`
	Priority   string `json:"priority"`
	// Splunk ES notable event fields
	EventID  string `json:"event_id"`
	RuleName string `json:"rule_name"`
	Owner    string `json:"owner"`
}

// SplunkWebhookPayload represents the webhook payload sent by Splunk alert actions
type SplunkWebhookPayload struct {
	SID         string                `json:"sid"`
	SearchName  string                `json:"search_name"`
	App         string                `json:"app"`
	Owner       string                `json:"owner"`
	ResultsLink string                `json:"results_link"`
	TriggerTime interface{}           `json:"trigger_time"` // may be string or number
	Result      SplunkWebhookResult   `json:"result"`
	Results     []SplunkWebhookResult `json:"results"`
}

func (m SplunkWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload SplunkWebhookPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("splunk_webhook: failed to unmarshal payload: %w", err)
	}

	// Validate required fields
	if payload.SID == "" && payload.SearchName == "" {
		return nil, fmt.Errorf("splunk_webhook: missing required fields: sid or search_name")
	}

	// Use SID as primary identifier, fall back to search_name
	alertID := payload.SID
	if alertID == "" {
		alertID = payload.SearchName
	}

	// Parse trigger time (may be epoch string or number)
	triggerTime := parseSplunkTriggerTime(payload.TriggerTime)
	if triggerTime.IsZero() {
		triggerTime = time.Now()
	}

	// Determine the primary result to extract entity/severity info
	primaryResult := payload.Result
	if len(payload.Results) > 0 {
		primaryResult = payload.Results[0]
	}

	// Map severity from result fields
	priority := mapSplunkSeverity(primaryResult.Severity, primaryResult.Urgency, primaryResult.Priority)

	// Determine if this is a Splunk ES Notable Event (has event_id field)
	isNotableEvent := primaryResult.EventID != ""

	// Extract entity information
	host := primaryResult.Host
	source := primaryResult.Source

	// Build labels
	labels := make(map[string]string)
	labels["search_name"] = payload.SearchName
	labels["app"] = payload.App
	if payload.Owner != "" {
		labels["owner"] = payload.Owner
	}
	if host != "" {
		labels["host"] = host
	}
	if source != "" {
		labels["source"] = source
	}
	if primaryResult.Index != "" {
		labels["index"] = primaryResult.Index
	}
	if primaryResult.Sourcetype != "" {
		labels["sourcetype"] = primaryResult.Sourcetype
	}
	if isNotableEvent {
		labels["notable_event_id"] = primaryResult.EventID
		labels["event_type"] = "splunk_es_notable"
	}

	// Build event description
	description := fmt.Sprintf("Splunk alert '%s' triggered", payload.SearchName)
	if isNotableEvent && primaryResult.RuleName != "" {
		description = fmt.Sprintf("Splunk ES notable event: %s", primaryResult.RuleName)
	}

	// Collect evidences
	evidences := []event.EventEvidence{}

	// Add raw alert payload as evidence
	alertEvidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "Splunk Alert Details",
			"data": payload,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Splunk Alert SID: %s, Search: %s", payload.SID, payload.SearchName),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "splunk_alert_details",
			"actual_action_name":     "splunk_alert_details",
			"action_title":           "Splunk Alert Details",
			"conditional_expression": "",
		},
	}
	evidences = append(evidences, alertEvidence)

	// Fetch related logs from Splunk Observability Cloud Log Observer if integration is configured
	if host != "" || source != "" {
		cfg, err := GetSplunkO11yConfigs(sc, accountId)
		if err != nil {
			sc.GetLogger().Warn("splunk_webhook: failed to get Splunk O11y config for log enrichment", "error", err)
		} else {
			fromTs := triggerTime.Add(-30 * time.Minute).UnixMilli()
			toTs := triggerTime.Add(30 * time.Minute).UnixMilli()

			// Build Log Observer Lucene filter for related logs
			var filterParts []string
			if host != "" {
				filterParts = append(filterParts, fmt.Sprintf("host.name:%s", EscapeO11yFieldValue(host)))
			}
			if source != "" {
				filterParts = append(filterParts, fmt.Sprintf("service.name:%s", EscapeO11yFieldValue(source)))
			}

			logFilter := ""
			if len(filterParts) > 0 {
				logFilter = "(" + strings.Join(filterParts, " OR ") + ")"
			}

			_, logEvidence, logErr := getSplunkO11yLogs(cfg, logFilter, fromTs, toTs)
			if logErr != nil {
				sc.GetLogger().Warn("splunk_webhook: failed to fetch related logs from Splunk O11y", "error", logErr)
			} else if logEvidence.Type != "" {
				evidences = append(evidences, logEvidence)
			}
		}
	}

	// Build fingerprint from SID or search_name + trigger_time
	fingerprint := alertID
	if payload.SearchName != "" && payload.SID != "" {
		fingerprint = fmt.Sprintf("%s-%s", payload.SearchName, payload.SID)
	}

	// Determine source URL
	sourceURL := payload.ResultsLink
	if sourceURL == "" && payload.SID != "" {
		sourceURL = fmt.Sprintf("splunk://search?sid=%s", payload.SID)
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    payload.SearchName,
		RuleId:      payload.SID,
		Fingerprint: fingerprint,
		Status:      event.EventStatusFiring,
		Severity:    priority,
		SourceUrl:   sourceURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:             alertID,
		EventType:             "splunk_alert",
		EventId:               alertID,
		EventUrl:              sourceURL,
		EventStatus:           string(event.EventStatusFiring),
		EventPriority:         string(priority),
		EventCreatedAt:        triggerTime,
		EventTitle:            payload.SearchName,
		EventDescription:      description,
		Investigation:         investigation,
		EventSubjectName:      host,
		EventSubjectKind:      "host",
		AccountId:             accountId,
		EventSubjectOwner:     host,
		EventSubjectOwnerKind: "host",
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// parseSplunkTriggerTime parses the trigger_time field which can be epoch string or number
func parseSplunkTriggerTime(val interface{}) time.Time {
	if val == nil {
		return time.Time{}
	}
	switch v := val.(type) {
	case float64:
		return time.Unix(int64(v), 0)
	case int64:
		return time.Unix(v, 0)
	case string:
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(epoch, 0)
		}
		// Try RFC3339
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// mapSplunkSeverity maps Splunk severity/urgency/priority fields to EventPriority
func mapSplunkSeverity(severity, urgency, priority string) event.EventPriortiy {
	// Prefer severity, fall back to urgency, then priority
	val := severity
	if val == "" {
		val = urgency
	}
	if val == "" {
		val = priority
	}

	switch strings.ToLower(val) {
	case "critical", "fatal":
		return event.EventPriortiyHigh
	case "high", "error":
		return event.EventPriortiyHigh
	case "medium", "warning", "warn":
		return event.EventPriortiyMedium
	case "low", "notice":
		return event.EventPriortiyLow
	case "info", "informational", "debug":
		return event.EventPriortiyInfo
	default:
		return event.EventPriortiyLow
	}
}
