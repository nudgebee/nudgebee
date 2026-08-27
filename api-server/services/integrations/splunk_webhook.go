package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
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

	// Fingerprint from the alert's stable identity (saved-search name + subject),
	// NOT the SID: the SID is the search-execution id, unique per firing, so
	// keying on it fragments every run of the same saved search into its own
	// occurrence chain. Keep the per-run id only when there is no search name.
	// When host/source are absent the key is just the saved-search name — a
	// deliberate choice (the saved search IS the alert identity, and still
	// strictly better than the per-run SID); flagged for the #34660 harness to
	// confirm it does not over-merge alerts about different hosts.
	fingerprint := alertID
	if payload.SearchName != "" {
		fingerprint = core.CanonicalFingerprint("splunk", payload.SearchName, host, source)
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

	// Upsert an event rule so the alert appears in rule management, mirroring the
	// other webhook integrations. No resolved-status guard here (unlike
	// openobserve_webhook): Splunk's webhook alert action has no resolve callback
	// at all, so Status above is unconditionally Firing and a guard would be dead
	// code.
	createSplunkEventRule(sc, accountId, payload.SearchName,
		webhookEvent.EventTitle, webhookEvent.EventDescription,
		eventRuleSeverity(priority),
		splunkEventRuleAlertType(webhookPayloadString))

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
func mapSplunkSeverity(severity, urgency, priority string) event.EventPriority {
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
		return event.EventPriorityHigh
	case "high", "error":
		return event.EventPriorityHigh
	case "medium", "warning", "warn":
		return event.EventPriorityMedium
	case "low", "notice":
		return event.EventPriorityLow
	case "info", "informational", "debug":
		return event.EventPriorityInfo
	default:
		return event.EventPriorityLow
	}
}

// splunkWebhookMetricResultFields are the fields a Splunk metrics search (`| mstats`)
// puts on a result row. Their presence is the only signal of metric-ness available
// here: Splunk's webhook body carries the saved search's NAME, its SID and its first
// result row, but never the SPL itself — so the `mstats` that would identify a metric
// search is invisible to this handler.
var splunkWebhookMetricResultFields = []string{"metric_name", "_value"}

// splunkEventRuleAlertType decides what goes in event_rules.alert_type, which is
// FK-constrained to event_rule_alert_type ('metric' or 'log').
//
// It defaults to "log", and that direction is deliberate rather than arbitrary.
// CreateEventRule treats an empty AlertType as 'metric', and a log alert mislabelled
// 'metric' is exactly the bug that made every OpenObserve investigation fail: the
// metric evidence path fed the rule's expression to a PromQL parser and threw a stack
// trace on each run. The reverse mistake degrades quietly — a metric alert labelled
// 'log' runs a log query that returns nothing — so when the payload is ambiguous, and
// for Splunk saved searches it usually is, "log" is the direction that fails softly.
// Splunk Enterprise saved-search alerts are overwhelmingly event searches in any case.
func splunkEventRuleAlertType(payloadString string) string {
	var probe struct {
		Result  map[string]any   `json:"result"`
		Results []map[string]any `json:"results"`
	}
	// A payload that no longer parses cannot be classified; the caller already
	// decoded it once, so this only fails on a shape change, and "log" remains the
	// safe answer.
	if err := json.Unmarshal([]byte(payloadString), &probe); err != nil {
		return "log"
	}

	rows := probe.Results
	if probe.Result != nil {
		rows = append(rows, probe.Result)
	}
	for _, row := range rows {
		for _, field := range splunkWebhookMetricResultFields {
			if _, ok := row[field]; ok {
				return "metric"
			}
		}
	}
	return "log"
}

// createSplunkEventRule fire-and-forgets the event-rule upsert on a detached context.
// The inbound request context is cancelled as soon as the webhook handler responds, so
// reusing it would abort the insert mid-flight.
//
// Expr is left empty: unlike OpenObserve, whose payload carries alert_operator and
// alert_threshold, Splunk sends no machine-readable trigger condition — only the
// saved-search name and its first result row. Inventing an expression from the result
// would put a condition in rule management that the alert does not actually use.
func createSplunkEventRule(sc *security.RequestContext, accountId, alertName, title, description, severityLabel, alertType string) {
	// A rule needs a stable name to upsert against; without the saved-search name
	// the only identifier left is the per-firing SID, which would create a new rule
	// on every run.
	if alertName == "" {
		return
	}

	// Read everything off the request context up front: the handler has already
	// responded by the time this goroutine runs, so nothing derived from sc should be
	// resolved inside it. Registering recover() first also means a panic anywhere in
	// the body — including context setup — is logged rather than taking the process down.
	secCtx := sc.GetSecurityContext()
	logger := sc.GetLogger()
	tracer := sc.GetTracer()
	meter := sc.GetMeter()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("splunk_webhook: panic in CreateEventRule goroutine", "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		detachedSc := security.NewRequestContext(ctx, secCtx, logger, tracer, meter)
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
			Expr: "",
			Labels: struct {
				Severity string `json:"severity"`
			}{Severity: severityLabel},
			Alert:         alertName,
			Duration:      "0",
			AccountID:     accountId,
			Source:        IntegrationSplunkWebhook,
			Category:      "alert",
			Severity:      severityLabel,
			AlertType:     alertType,
			Enabled:       true,
			TriggerParams: []map[string]any{},
			ActionParams:  []map[string]any{},
		}
		if _, err := eventrule.CreateEventRule(detachedSc, eventReq); err != nil {
			detachedSc.GetLogger().Error("splunk_webhook: CreateEventRule failed", "error", err, "alert", alertName)
		}
	}()
}
