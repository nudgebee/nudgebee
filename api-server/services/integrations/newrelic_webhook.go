package integrations

import (
	"database/sql"
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(NewRelicWebhook{})
}

const IntegrationNewRelicWebhook = "newrelic_webhook"

type NewRelicWebhook struct{}

func (m NewRelicWebhook) Name() string {
	return IntegrationNewRelicWebhook
}

func (m NewRelicWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m NewRelicWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of New Relic Webhook",
				Default:     "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:    core.ToolSchemaTypeString,
				Default: "",
			},
		},
	}
}

func (m NewRelicWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m NewRelicWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// NewRelicWebhookPayload is the modern AI Workflow / Issue webhook payload
// (camelCase). Includes both the bare minimum for issue lookup and the rich
// fields used as a fallback when the NerdGraph aiIssues fetch is unavailable
// or fails.
type NewRelicWebhookPayload struct {
	ID                  string                 `json:"id"`
	IssueId             string                 `json:"issueId"`
	IssueUrl            string                 `json:"issueUrl"`
	IssuePageUrl        string                 `json:"issuePageUrl"`
	Title               string                 `json:"title"`
	IssueTitle          string                 `json:"issueTitle"`
	Priority            string                 `json:"priority"`
	State               string                 `json:"state"`
	Trigger             string                 `json:"trigger"`
	TriggerEvent        string                 `json:"triggerEvent"`
	IsAcknowledged      bool                   `json:"isAcknowledged"`
	IssueCreatedAt      int64                  `json:"createdAt"` // epoch ms
	UpdatedAt           int64                  `json:"updatedAt"`
	ActivatedAt         int64                  `json:"activatedAt"`
	ClosedAt            int64                  `json:"closedAt"`
	Sources             []string               `json:"sources"`
	AlertPolicyNames    []string               `json:"alertPolicyNames"`
	AlertConditionNames []string               `json:"alertConditionNames"`
	ImpactedEntities    []string               `json:"impactedEntities"`
	IncidentIds         []string               `json:"incidentIds"`
	TotalIncidents      int64                  `json:"totalIncidents"`
	WorkflowName        string                 `json:"workflowName"`
	Accumulations       map[string]interface{} `json:"accumulations"`
}

// NewRelicLegacyIncidentPayload is the older "Alerts → Notification channels →
// Webhook" payload (snake_case). Deprecated by NewRelic in 2023 but still
// produced by migration-frozen channels and customers using legacy alert
// policies. Detected at the dispatcher level by a non-zero `incident_id` —
// modern issue payloads never carry that field.
type NewRelicLegacyIncidentPayload struct {
	AccountId              int64                  `json:"account_id"`
	AccountName            string                 `json:"account_name"`
	ConditionId            int64                  `json:"condition_id"`
	ConditionName          string                 `json:"condition_name"`
	CurrentState           string                 `json:"current_state"`
	Details                string                 `json:"details"`
	EventType              string                 `json:"event_type"`
	IncidentAcknowledgeUrl string                 `json:"incident_acknowledge_url"`
	IncidentId             int64                  `json:"incident_id"`
	IncidentUrl            string                 `json:"incident_url"`
	Owner                  string                 `json:"owner"`
	PolicyName             string                 `json:"policy_name"`
	PolicyUrl              string                 `json:"policy_url"`
	RunbookUrl             string                 `json:"runbook_url"`
	Severity               string                 `json:"severity"`
	Targets                []NewRelicLegacyTarget `json:"targets"`
	Timestamp              int64                  `json:"timestamp"` // epoch ms
	Duration               int64                  `json:"duration"`  // seconds
	Version                string                 `json:"version"`
}

// NewRelicLegacyTarget is one entry in the legacy payload's `targets[]` array.
type NewRelicLegacyTarget struct {
	Id      string            `json:"id"`
	Name    string            `json:"name"`
	Link    string            `json:"link"`
	Labels  map[string]string `json:"labels"`
	Product string            `json:"product"`
	Type    string            `json:"type"`
}

// extractIssueIdFromUrl pulls the trailing UUID out of a NewRelic issueUrl.
// AI Workflow notifications carry the queryable issue ID only in this URL —
// the top-level `id` field is the notification batch UUID.
// Example: https://radar-api.service.newrelic.com/accounts/1/issues/<uuid>?notifier=SLACK
func extractIssueIdFromUrl(issueUrl string) string {
	if issueUrl == "" {
		return ""
	}
	if i := strings.Index(issueUrl, "?"); i >= 0 {
		issueUrl = issueUrl[:i]
	}
	if i := strings.LastIndex(issueUrl, "/"); i >= 0 && i+1 < len(issueUrl) {
		return issueUrl[i+1:]
	}
	return ""
}

// buildIssueFromPayload synthesizes a NewRelicNrAiIssue from webhook fields,
// used when GetNewRelicConfigs or getNewRelicIssueDetails fails. The downstream
// label/fingerprint/evidence code tolerates the empty fields we can't fill
// (entityGuids, conditionFamilyId, totalIncidents).
func buildIssueFromPayload(p *NewRelicWebhookPayload, issueId string) *NewRelicNrAiIssue {
	return &NewRelicNrAiIssue{
		IssueId:       issueId,
		Title:         p.Title,
		State:         p.State,
		Priority:      p.Priority,
		CreatedAt:     p.IssueCreatedAt,
		ActivatedAt:   p.IssueCreatedAt,
		UpdatedAt:     p.UpdatedAt,
		Sources:       p.Sources,
		ConditionName: p.AlertConditionNames,
		PolicyName:    p.AlertPolicyNames,
		EntityNames:   p.ImpactedEntities,
	}
}

// NewRelicImpactedEntity represents an entity affected by the alert
type NewRelicImpactedEntity struct {
	Guid       string            `json:"guid"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	EntityType string            `json:"entityType"`
	Domain     string            `json:"domain"`
	Tags       map[string]string `json:"tags"`
}

// escapeNRQLString escapes special characters for safe use in NRQL query strings
// Prevents NRQL injection attacks
func escapeNRQLString(s string) string {
	// Escape single quotes by doubling them (NRQL standard)
	s = strings.ReplaceAll(s, "'", "''")
	// Remove or escape potentially dangerous characters
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// cleanAlertTitle removes priority prefixes from alert titles
func cleanAlertTitle(title string) string {
	// Remove common priority prefixes
	prefixes := []string{"[CRITICAL]", "[HIGH]", "[MEDIUM]", "[LOW]", "[INFO]", "[WARNING]"}
	cleaned := title
	for _, prefix := range prefixes {
		cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, prefix))
	}
	return cleaned
}

func (m NewRelicWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	// TODO: Add webhook token validation when HTTP request context is available
	// The token should be validated against settings before processing the webhook
	// Helper functions to safely extract values from interface{} fields
	// formatVal converts a JSON-decoded value to string.
	// JSON numbers are decoded as float64; large integers rendered via %v
	// produce scientific notation (e.g. 5.8870873e+07), so we detect whole
	// float64 values and format them as integers instead.
	formatVal := func(val interface{}) string {
		if f, ok := val.(float64); ok {
			if f == float64(int64(f)) {
				return strconv.FormatInt(int64(f), 10)
			}
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return fmt.Sprintf("%v", val)
	}

	getString := func(val interface{}) string {
		if val == nil {
			return ""
		}
		// Handle array - take first element
		if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
			return formatVal(arr[0])
		}
		return formatVal(val)
	}

	getStringArray := func(val interface{}) []string {
		if val == nil {
			return []string{}
		}
		// Already a string array
		if arr, ok := val.([]string); ok {
			return arr
		}
		// Array of interface{}
		if arr, ok := val.([]interface{}); ok {
			result := make([]string, len(arr))
			for i, v := range arr {
				result[i] = formatVal(v)
			}
			return result
		}
		// Single value - wrap in array
		return []string{formatVal(val)}
	}

	getInt64 := func(val interface{}) int64 {
		if val == nil {
			return 0
		}
		// Handle array - take first element
		if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
			val = arr[0]
		}
		// Handle float64 (JSON numbers)
		if f, ok := val.(float64); ok {
			return int64(f)
		}
		// Handle int64
		if i, ok := val.(int64); ok {
			return i
		}
		// Try parsing string
		if s, ok := val.(string); ok {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i
			}
		}
		return 0
	}

	// Step 0: Dispatch on payload shape. The legacy "Notification channel →
	// Webhook" format uses snake_case `incident_id` (an integer) — modern
	// AI Workflow issue payloads never carry that field, so a non-zero
	// `incident_id` is an unambiguous tell.
	var legacy NewRelicLegacyIncidentPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &legacy); err == nil && legacy.IncidentId != 0 {
		return m.processLegacyIncident(sc, accountId, &legacy)
	}

	// Step 1: Parse modern webhook payload to extract issue ID
	var payload NewRelicWebhookPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("newrelic_webhook: failed to unmarshal payload: %w", err)
	}

	// Determine issue ID. Prefer extraction from issueUrl since AI Workflow
	// notifications put the queryable issue UUID there, while the top-level
	// `id` is the notification batch UUID and won't match in NerdGraph.
	issueId := extractIssueIdFromUrl(payload.IssueUrl)
	if issueId == "" {
		issueId = payload.IssueId
	}
	if issueId == "" {
		issueId = payload.ID
	}
	if issueId == "" {
		return nil, fmt.Errorf("newrelic_webhook: missing issue ID in payload")
	}

	// Step 2: Try to fetch NewRelic API config. If absent, we degrade to
	// webhook-only data instead of dropping the event.
	apiKey, nrAccountId, region, configErr := GetNewRelicConfigs(sc, accountId)

	// Step 3: Fetch complete issue details from NewRelic NerdGraph API.
	// On any failure (no config, transient API error, issue not found in
	// the queried window), fall back to building the issue from the webhook
	// payload itself — which already carries title, priority, state, sources,
	// policy/condition names, and impacted entities.
	var issueCreatedAt *int64
	if payload.IssueCreatedAt > 0 {
		issueCreatedAt = &payload.IssueCreatedAt
	}
	var issue *NewRelicNrAiIssue
	if configErr != nil {
		sc.GetLogger().Warn("newrelic_webhook: NewRelic API integration not configured, using webhook payload only", "error", configErr, "account_id", accountId)
	} else {
		fetched, err := getNewRelicIssueDetails(apiKey, nrAccountId, region, issueId, issueCreatedAt)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: NerdGraph issue fetch failed, falling back to webhook payload", "error", err, "issue_id", issueId)
		} else {
			issue = fetched
		}
	}
	if issue == nil {
		issue = buildIssueFromPayload(&payload, issueId)
	}

	// Step 4: Map issue state to our event status
	status := mapNewRelicStateToStatus(getString(issue.State))

	// Step 5: Map issue priority to our event priority
	priority := mapNewRelicPriority(getString(issue.Priority))

	// Step 6: Build labels from issue data
	labels := make(map[string]string)

	// Add issue metadata
	conditionNames := getStringArray(issue.ConditionName)
	if len(conditionNames) > 0 {
		labels["condition_name"] = strings.Join(conditionNames, ",")
	}

	if issue.ConditionFamilyId != nil {
		labels["condition_family_id"] = getString(issue.ConditionFamilyId)
	}

	policyNames := getStringArray(issue.PolicyName)
	if len(policyNames) > 0 {
		labels["policy_name"] = strings.Join(policyNames, ",")
	}

	// Add entity information — fall back to webhook impactedEntities if NerdGraph returns none
	entityNames := getStringArray(issue.EntityNames)
	if len(entityNames) == 0 && len(payload.ImpactedEntities) > 0 {
		// Deduplicate impactedEntities from webhook payload
		seen := make(map[string]struct{})
		for _, e := range payload.ImpactedEntities {
			if e != "" {
				if _, exists := seen[e]; !exists {
					seen[e] = struct{}{}
					entityNames = append(entityNames, e)
				}
			}
		}
		sc.GetLogger().Info("newrelic_webhook: using impactedEntities from payload as fallback for entity names", "entities", entityNames)
	}
	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
		labels["service"] = entityNames[0]
	}

	entityGuids := getStringArray(issue.EntityGuids)
	if len(entityGuids) > 0 {
		labels["entity_guids"] = strings.Join(entityGuids, ",")
	}

	// Add sources
	sources := getStringArray(issue.Sources)
	if len(sources) > 0 {
		labels["sources"] = strings.Join(sources, ",")
	}

	// Add total incidents
	totalIncidents := getInt64(issue.TotalIncidents)
	if totalIncidents > 0 {
		labels["total_incidents"] = fmt.Sprintf("%d", totalIncidents)
	}

	// Carry workflow name from the webhook payload — it's not in the
	// NerdGraph aiIssues response, so we always source it from the payload.
	if payload.WorkflowName != "" {
		labels["workflow_name"] = payload.WorkflowName
	}

	// Pull select accumulations.* values from modern Workflow payloads.
	// These are arrays (one entry per aggregated incident); we take [0].
	if payload.Accumulations != nil {
		if v := firstString(accumulationsStringList(payload.Accumulations, "runbookUrl")); v != "" {
			labels["runbook_url"] = v
		}
		if v := firstString(accumulationsStringList(payload.Accumulations, "deepLinkUrl")); v != "" {
			labels["deep_link_url"] = v
		}
		if v := firstString(accumulationsStringList(payload.Accumulations, "conditionProduct")); v != "" {
			labels["condition_product"] = v
		}
		if v := firstString(accumulationsStringList(payload.Accumulations, "nrqlQuery")); v != "" {
			// Truncate verbose NRQL strings to keep label sizes bounded.
			if len(v) > 512 {
				v = v[:512] + "…"
			}
			labels["nrql_query"] = v
		}
		// Accumulations.tag is an arbitrary map[string][]string of entity tags
		// that the customer attached in NewRelic. Surface them as tag_<name>.
		if rawTag, ok := payload.Accumulations["tag"].(map[string]interface{}); ok {
			for tagName, tagVal := range rawTag {
				vals := accumulationsStringList(map[string]interface{}{tagName: tagVal}, tagName)
				if v := firstString(vals); v != "" {
					labels["tag_"+tagName] = v
				}
			}
		}
	}

	if len(payload.IncidentIds) > 0 {
		labels["incident_ids"] = strings.Join(payload.IncidentIds, ",")
	}

	// Step 7: Determine timestamps
	var createdAt time.Time
	var endsAt time.Time

	activatedAt := getInt64(issue.ActivatedAt)
	createdAtVal := getInt64(issue.CreatedAt)
	closedAt := getInt64(issue.ClosedAt)

	if activatedAt > 0 {
		createdAt = time.UnixMilli(activatedAt)
	} else if createdAtVal > 0 {
		createdAt = time.UnixMilli(createdAtVal)
	} else {
		createdAt = time.Now()
	}

	if closedAt > 0 {
		endsAt = time.UnixMilli(closedAt)
	}

	// Step 8: Build event description
	conditionName := ""
	if len(conditionNames) > 0 {
		conditionName = conditionNames[0]
	}
	policyName := ""
	if len(policyNames) > 0 {
		policyName = policyNames[0]
	}
	description := fmt.Sprintf("Alert condition '%s' from policy '%s'", conditionName, policyName)

	// Step 9: Collect evidences
	evidences := []event.EventEvidence{}

	// Add issue data as evidence
	issueEvidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "New Relic Issue Details",
			"data": issue,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("New Relic Issue ID: %s", issueId),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "newrelic_issue_details",
			"actual_action_name":     "newrelic_issue_details",
			"action_title":           "New Relic Issue Details",
			"conditional_expression": "",
		},
	}
	evidences = append(evidences, issueEvidence)

	// Fetch related logs, traces, and entity details.
	// Use a wider window (-2h to +6h) to capture pre-alert context and
	// post-restart logs (services that were down may only produce logs after recovery).
	fromTs := createdAt.Add(-2 * time.Hour).UnixMilli()
	toTs := createdAt.Add(6 * time.Hour).UnixMilli()
	evidences = append(evidences, fetchNewRelicObservabilityEvidences(sc, apiKey, nrAccountId, region, entityNames, entityGuids, fromTs, toTs)...)

	// Step 10: Determine subject information from entity data
	subjectName := ""
	subjectKind := ""
	cloudResourceId := ""

	if len(entityNames) > 0 {
		subjectName = entityNames[0]
	}

	// Look up cloud_resource_id (UUID) from k8s_workloads by subject name.
	// NewRelic entity GUIDs are base64 strings, not UUIDs, so they cannot
	// be used directly. Mirror the PagerDuty workload-matching strategy.
	if match := matchK8sWorkload(sc, accountId, subjectName, labels); match.Found {
		subjectName = match.Name
		subjectKind = strings.ToLower(match.Kind)
		cloudResourceId = match.CloudResourceId
	}

	// Step 11: Build fingerprint
	fingerprint := issueId
	condFamilyId := getString(issue.ConditionFamilyId)
	if condFamilyId != "" {
		fingerprint = fmt.Sprintf("%s-%s", condFamilyId, issueId)
	}

	// Step 12: Build investigation details
	ruleId := condFamilyId
	if ruleId == "" && len(conditionNames) > 0 {
		ruleId = conditionNames[0]
	}

	// Clean title by removing priority prefixes. Title can come from
	// `title` (legacy default) or `issueTitle` (Workflow custom payload).
	titleSrc := getString(issue.Title)
	if titleSrc == "" {
		titleSrc = payload.IssueTitle
	}
	cleanedTitle := cleanAlertTitle(titleSrc)

	// Prefer NewRelic's own issuePageUrl when present — it routes to the
	// correct region/account and uses the canonical NewRelic redirector.
	// Fall back to constructing one when only the region is known.
	uiRegionPrefix := ""
	if region == "eu" {
		uiRegionPrefix = "eu."
	}
	sourceUrl := payload.IssuePageUrl
	if sourceUrl == "" {
		sourceUrl = fmt.Sprintf("https://one.%snewrelic.com/redirect/apm-issues/%s", uiRegionPrefix, issueId)
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    cleanedTitle,
		RuleId:      ruleId,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   sourceUrl,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Step 13: Create the event
	webhookEvent := core.EventIncomingWebhook{
		WebhookId:             issueId,
		EventType:             "new_relic_alert",
		EventId:               issueId,
		EventUrl:              sourceUrl,
		EventStatus:           status,
		EventPriority:         getString(issue.Priority),
		EventCreatedAt:        createdAt,
		EventEndsAt:           endsAt,
		EventTitle:            cleanedTitle,
		EventDescription:      description,
		EventTags:             sources,
		Investigation:         investigation,
		EventSubjectName:      subjectName,
		EventSubjectKind:      subjectKind,
		AccountId:             accountId,
		EventSubjectOwner:     subjectName,
		EventSubjectOwnerKind: subjectKind,
		CloudResourceId:       cloudResourceId,
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// fetchNewRelicObservabilityEvidences fetches logs, traces, metrics, and entity details
// for the given entity names/GUIDs within the specified time window.
// Errors are logged as warnings and do not stop processing.
func fetchNewRelicObservabilityEvidences(sc *security.RequestContext, apiKey, nrAccountId, region string, entityNames, entityGuids []string, fromTs, toTs int64) []event.EventEvidence {
	var evidences []event.EventEvidence

	if len(entityNames) > 0 {
		sanitizedEntityName := escapeNRQLString(entityNames[0])

		logQuery := fmt.Sprintf("service.name = '%s' OR entity.name = '%s'", sanitizedEntityName, sanitizedEntityName)
		_, logEvidence, err := getNewRelicLogs(sc, apiKey, nrAccountId, region, logQuery, fromTs, toTs)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: failed to fetch logs", "error", err)
		} else if logEvidence.Type != "" {
			evidences = append(evidences, logEvidence)
		}

		traceQuery := fmt.Sprintf("service.name = '%s' OR entity.name = '%s'", sanitizedEntityName, sanitizedEntityName)
		_, traceEvidence, err := getNewRelicTraces(sc, apiKey, nrAccountId, region, traceQuery, fromTs, toTs)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: failed to fetch traces", "error", err)
		} else if traceEvidence.Type != "" {
			evidences = append(evidences, traceEvidence)
		}

		_, metricEvidence, err := getNewRelicMetrics(sc, apiKey, nrAccountId, region, entityNames[0], fromTs, toTs)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: failed to fetch metrics", "error", err)
		} else if metricEvidence.Type != "" {
			evidences = append(evidences, metricEvidence)
		}
	} else if len(entityGuids) > 0 {
		guidList := make([]string, len(entityGuids))
		for i, g := range entityGuids {
			guidList[i] = fmt.Sprintf("'%s'", escapeNRQLString(g))
		}
		guidFilter := strings.Join(guidList, ", ")

		logQuery := fmt.Sprintf("nr.entity.guids IN (%s)", guidFilter)
		_, logEvidence, err := getNewRelicLogs(sc, apiKey, nrAccountId, region, logQuery, fromTs, toTs)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: failed to fetch logs by entity guid", "error", err)
		} else if logEvidence.Type != "" {
			evidences = append(evidences, logEvidence)
		}

		traceQuery := fmt.Sprintf("nr.entity.guids IN (%s)", guidFilter)
		_, traceEvidence, err := getNewRelicTraces(sc, apiKey, nrAccountId, region, traceQuery, fromTs, toTs)
		if err != nil {
			sc.GetLogger().Warn("newrelic_webhook: failed to fetch traces by entity guid", "error", err)
		} else if traceEvidence.Type != "" {
			evidences = append(evidences, traceEvidence)
		}
	}

	for i, entityGuid := range entityGuids {
		entityDetails, err := getNewRelicEntityDetails(apiKey, nrAccountId, region, entityGuid)
		if err != nil || entityDetails == nil {
			continue
		}
		entityName := "New Relic Entity"
		if i < len(entityNames) {
			entityName = entityNames[i]
		}
		evidences = append(evidences, event.EventEvidence{
			Type: "json",
			Data: map[string]any{
				"name": fmt.Sprintf("Entity: %s", entityName),
				"data": entityDetails,
			},
			Insight: []event.EventEvidenceInsight{
				{
					Message:  fmt.Sprintf("Entity: %s (GUID: %s)", entityName, entityGuid),
					Severity: "info",
				},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "newrelic_entity_details",
				"actual_action_name":     "newrelic_entity_details",
				"action_title":           fmt.Sprintf("Entity: %s", entityName),
				"conditional_expression": "",
			},
		})
	}

	return evidences
}

// mapNewRelicStateToStatus maps New Relic issue state to our event status
func mapNewRelicStateToStatus(state string) string {
	switch strings.ToUpper(state) {
	case "CREATED", "ACTIVATED", "OPEN":
		return string(event.EventStatusFiring)
	case "ACKNOWLEDGED":
		return "acknowledged"
	case "CLOSED", "RESOLVED":
		return string(event.EventStatusResolved)
	default:
		return strings.ToLower(state)
	}
}

// mapNewRelicPriority maps New Relic priority to our event priority
func mapNewRelicPriority(priority string) event.EventPriortiy {
	switch strings.ToUpper(priority) {
	case "CRITICAL":
		return event.EventPriortiyHigh
	case "HIGH":
		return event.EventPriortiyHigh
	case "MEDIUM":
		return event.EventPriortiyMedium
	case "LOW":
		return event.EventPriortiyLow
	case "INFO":
		return event.EventPriortiyInfo
	default:
		return event.EventPriortiyLow
	}
}

// mapLegacyStateToStatus maps the legacy incident webhook's `current_state`
// (lower-case open/acknowledged/closed) to our event status.
func mapLegacyStateToStatus(state string) string {
	switch strings.ToLower(state) {
	case "open":
		return string(event.EventStatusFiring)
	case "acknowledged":
		return "acknowledged"
	case "closed":
		return string(event.EventStatusResolved)
	default:
		return strings.ToLower(state)
	}
}

// mapLegacySeverityToPriority maps the legacy `severity` enum
// (CRITICAL/WARNING/INFO) to our event priority. NewRelic's pre-migration
// shim emits WARNING for HIGH-priority modern issues, so WARNING→High
// would over-promote; mapping WARNING→Medium preserves the original spread.
func mapLegacySeverityToPriority(severity string) event.EventPriortiy {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return event.EventPriortiyHigh
	case "WARNING":
		return event.EventPriortiyMedium
	case "INFO":
		return event.EventPriortiyInfo
	default:
		return event.EventPriortiyLow
	}
}

// workloadMatch carries the result of looking a NewRelic entity name up
// against k8s_workloads.
type workloadMatch struct {
	Name            string
	Namespace       string
	Kind            string
	CloudResourceId string
	Found           bool
}

// matchK8sWorkload runs the same progressive 4-query lookup used by the
// PagerDuty webhook to resolve a NewRelic entity name to a k8s_workloads
// row. On match, populates kind/namespace/cloud_resource_id/nb_matched_workload
// labels in `labels` (mutates the map). Returns Found=false if no row matches.
func matchK8sWorkload(sc *security.RequestContext, accountId, subjectName string, labels map[string]string) workloadMatch {
	result := workloadMatch{}
	if subjectName == "" {
		return result
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Warn("newrelic_webhook: failed to get database manager for workload matching", "error", err)
		return result
	}
	tenantId := sc.GetSecurityContext().GetTenantId()

	type workloadResult struct {
		Name            string `db:"name"`
		Namespace       string `db:"namespace"`
		Kind            string `db:"kind"`
		CloudResourceId string `db:"cloud_resource_id"`
	}

	var workload workloadResult
	queries := []string{
		`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND name = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
		`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND labels->>'app.kubernetes.io/name' = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
		`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND labels->>'app' = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
		`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND name ILIKE '%' || $3 || '%' AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
	}

	for _, q := range queries {
		err := dbms.Db.Get(&workload, q, tenantId, accountId, subjectName)
		if err == nil {
			result.Name = workload.Name
			result.Namespace = workload.Namespace
			result.Kind = workload.Kind
			result.CloudResourceId = workload.CloudResourceId
			result.Found = true
			if workload.Namespace != "" {
				labels["namespace"] = workload.Namespace
			}
			labels["kind"] = workload.Kind
			labels["cloud_resource_id"] = workload.CloudResourceId
			labels["nb_matched_workload"] = "true"
			sc.GetLogger().Info("newrelic_webhook: matched workload", "subject_name", subjectName, "kind", workload.Kind, "namespace", workload.Namespace)
			return result
		}
		if !errors.Is(err, sql.ErrNoRows) {
			sc.GetLogger().Warn("newrelic_webhook: database error during workload lookup", "error", err)
			return result
		}
	}
	sc.GetLogger().Info("newrelic_webhook: no workload match found", "subject_name", subjectName, "account_id", accountId)
	return result
}

// firstString returns the first element of a string slice or "" if empty.
// Used to read accumulations.* arrays where templates conventionally take [0].
func firstString(arr []string) string {
	if len(arr) > 0 {
		return arr[0]
	}
	return ""
}

// accumulationsStringList interprets one accumulations key as an array of
// strings. Modern NewRelic Workflows put scalar values inside single-element
// arrays (one entry per aggregated incident), so this helper handles both
// `[]interface{}` and a bare scalar.
func accumulationsStringList(acc map[string]interface{}, key string) []string {
	if acc == nil {
		return nil
	}
	val, ok := acc[key]
	if !ok || val == nil {
		return nil
	}
	if arr, ok := val.([]interface{}); ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			if v == nil {
				continue
			}
			out = append(out, fmt.Sprintf("%v", v))
		}
		return out
	}
	return []string{fmt.Sprintf("%v", val)}
}

// processLegacyIncident builds an EventIncomingWebhook from the legacy
// snake_case "Notification channel → Webhook" payload. The legacy format
// has no concept of issueId/aiIssues, so we never call NerdGraph for issue
// lookup; instead we synthesize the event entirely from the payload and
// optionally enrich with logs/traces if a NewRelic API integration is
// configured for the same account.
func (m NewRelicWebhook) processLegacyIncident(sc *security.RequestContext, accountId string, p *NewRelicLegacyIncidentPayload) ([]core.EventIncomingWebhook, error) {
	incidentId := strconv.FormatInt(p.IncidentId, 10)
	status := mapLegacyStateToStatus(p.CurrentState)
	priority := mapLegacySeverityToPriority(p.Severity)

	labels := make(map[string]string)
	if p.PolicyName != "" {
		labels["policy_name"] = p.PolicyName
	}
	if p.ConditionName != "" {
		labels["condition_name"] = p.ConditionName
	}
	if p.ConditionId != 0 {
		labels["condition_family_id"] = strconv.FormatInt(p.ConditionId, 10)
	}
	if p.AccountName != "" {
		labels["nr_account_name"] = p.AccountName
	}
	if p.AccountId != 0 {
		labels["nr_account_id"] = strconv.FormatInt(p.AccountId, 10)
	}
	if p.RunbookUrl != "" {
		labels["runbook_url"] = p.RunbookUrl
	}
	if p.PolicyUrl != "" {
		labels["policy_url"] = p.PolicyUrl
	}
	if p.Owner != "" {
		labels["owner"] = p.Owner
	}
	if p.Duration > 0 {
		labels["duration_seconds"] = strconv.FormatInt(p.Duration, 10)
	}
	if p.EventType != "" {
		labels["event_type"] = p.EventType
	}

	entityNames := make([]string, 0, len(p.Targets))
	for _, t := range p.Targets {
		if t.Name != "" {
			entityNames = append(entityNames, t.Name)
		}
	}
	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
		labels["service"] = entityNames[0]
	}
	if len(p.Targets) > 0 {
		first := p.Targets[0]
		for k, v := range first.Labels {
			labels["target_"+k] = v
		}
		if first.Product != "" {
			labels["product"] = first.Product
		}
		if first.Type != "" {
			labels["target_type"] = first.Type
		}
	}

	createdAt := time.Now()
	if p.Timestamp > 0 {
		createdAt = time.UnixMilli(p.Timestamp)
	}

	title := cleanAlertTitle(p.ConditionName)
	if title == "" {
		title = cleanAlertTitle(p.PolicyName)
	}
	if title == "" {
		title = fmt.Sprintf("Incident %s", incidentId)
	}

	description := p.Details
	if description == "" {
		description = fmt.Sprintf("Alert condition '%s' from policy '%s'", p.ConditionName, p.PolicyName)
	}

	subjectName := ""
	subjectKind := ""
	cloudResourceId := ""
	if len(entityNames) > 0 {
		subjectName = entityNames[0]
		match := matchK8sWorkload(sc, accountId, subjectName, labels)
		if match.Found {
			subjectName = match.Name
			subjectKind = strings.ToLower(match.Kind)
			cloudResourceId = match.CloudResourceId
		}
	}

	evidences := []event.EventEvidence{
		{
			Type: "json",
			Data: map[string]any{
				"name": "New Relic Incident Details",
				"data": p,
			},
			Insight: []event.EventEvidenceInsight{
				{
					Message:  fmt.Sprintf("New Relic Incident ID: %s", incidentId),
					Severity: "info",
				},
			},
			AdditionalInfo: map[string]any{
				"action_name":            "newrelic_incident_details",
				"actual_action_name":     "newrelic_incident_details",
				"action_title":           "New Relic Incident Details",
				"conditional_expression": "",
			},
		},
	}

	// Optional enrichment via the NewRelic API integration on the same account.
	// Errors degrade silently — the event is still emitted from the webhook.
	if apiKey, nrAccountId, region, configErr := GetNewRelicConfigs(sc, accountId); configErr == nil && len(entityNames) > 0 {
		fromTs := createdAt.Add(-2 * time.Hour).UnixMilli()
		toTs := createdAt.Add(6 * time.Hour).UnixMilli()
		evidences = append(evidences, fetchNewRelicObservabilityEvidences(sc, apiKey, nrAccountId, region, entityNames, nil, fromTs, toTs)...)
	}

	sourceUrl := p.IncidentUrl
	fingerprint := incidentId
	if p.ConditionId != 0 {
		fingerprint = fmt.Sprintf("%d-%s", p.ConditionId, incidentId)
	}

	ruleId := strconv.FormatInt(p.ConditionId, 10)
	if p.ConditionId == 0 {
		ruleId = p.ConditionName
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      ruleId,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   sourceUrl,
		Labels:      labels,
		Evidences:   evidences,
	}

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:             incidentId,
		EventType:             "new_relic_alert",
		EventId:               incidentId,
		EventUrl:              sourceUrl,
		EventStatus:           status,
		EventPriority:         p.Severity,
		EventCreatedAt:        createdAt,
		EventTitle:            title,
		EventDescription:      description,
		EventTags:             []string{"newrelic"},
		Investigation:         investigation,
		EventSubjectName:      subjectName,
		EventSubjectKind:      subjectKind,
		AccountId:             accountId,
		EventSubjectOwner:     subjectName,
		EventSubjectOwnerKind: subjectKind,
		CloudResourceId:       cloudResourceId,
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}
