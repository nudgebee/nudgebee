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

// NewRelicWebhookPayload represents the minimal webhook payload needed to extract issue ID
type NewRelicWebhookPayload struct {
	ID               string   `json:"id"`
	IssueId          string   `json:"issueId"`
	IssueCreatedAt   int64    `json:"createdAt"` // epoch milliseconds
	ImpactedEntities []string `json:"impactedEntities"`
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

	// Step 1: Parse minimal webhook payload to extract issue ID
	var payload NewRelicWebhookPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("newrelic_webhook: failed to unmarshal payload: %w", err)
	}

	// Determine issue ID
	issueId := payload.ID
	if issueId == "" {
		issueId = payload.IssueId
	}
	if issueId == "" {
		return nil, fmt.Errorf("newrelic_webhook: missing issue ID in payload")
	}

	// Step 2: Fetch NewRelic config for this account
	apiKey, nrAccountId, region, err := GetNewRelicConfigs(sc, accountId)
	if err != nil {
		return nil, fmt.Errorf("newrelic_webhook: failed to get NewRelic config: %w", err)
	}

	// Step 3: Fetch complete issue details from NewRelic NerdGraph API
	var issueCreatedAt *int64
	if payload.IssueCreatedAt > 0 {
		issueCreatedAt = &payload.IssueCreatedAt
	}
	issue, err := getNewRelicIssueDetails(apiKey, nrAccountId, region, issueId, issueCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("newrelic_webhook: failed to fetch issue details: %w", err)
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
	if subjectName != "" {
		if dbms, err := database.GetDatabaseManager(database.Metastore); err == nil {
			tenantId := sc.GetSecurityContext().GetTenantId()

			type workloadResult struct {
				Name            string `db:"name"`
				Namespace       string `db:"namespace"`
				Kind            string `db:"kind"`
				CloudResourceId string `db:"cloud_resource_id"`
			}

			var workload workloadResult
			found := false

			queries := []string{
				`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND name = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
				`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND labels->>'app.kubernetes.io/name' = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
				`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND labels->>'app' = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
				`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND name ILIKE '%' || $3 || '%' AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
			}

			for _, q := range queries {
				err := dbms.Db.Get(&workload, q, tenantId, accountId, subjectName)
				if err == nil {
					found = true
					break
				}
				if !errors.Is(err, sql.ErrNoRows) {
					sc.GetLogger().Warn("newrelic_webhook: database error during workload lookup", "error", err)
					break
				}
			}

			if found {
				subjectName = workload.Name
				subjectKind = strings.ToLower(workload.Kind)
				cloudResourceId = workload.CloudResourceId
				if workload.Namespace != "" {
					labels["namespace"] = workload.Namespace
				}
				labels["kind"] = workload.Kind
				labels["cloud_resource_id"] = workload.CloudResourceId
				labels["nb_matched_workload"] = "true"
				sc.GetLogger().Info("newrelic_webhook: matched workload", "subject_name", subjectName, "kind", workload.Kind, "namespace", workload.Namespace)
			} else {
				sc.GetLogger().Info("newrelic_webhook: no workload match found", "subject_name", subjectName, "account_id", accountId)
			}
		} else {
			sc.GetLogger().Warn("newrelic_webhook: failed to get database manager for workload matching", "error", err)
		}
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

	// Clean title by removing priority prefixes
	cleanedTitle := cleanAlertTitle(getString(issue.Title))

	// Determine New Relic region prefix for UI URL
	uiRegionPrefix := ""
	if region == "eu" {
		uiRegionPrefix = "eu."
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    cleanedTitle,
		RuleId:      ruleId,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   fmt.Sprintf("https://one.%snewrelic.com/redirect/apm-issues/%s", uiRegionPrefix, issueId),
		Labels:      labels,
		Evidences:   evidences,
	}

	// Step 13: Create the event
	webhookEvent := core.EventIncomingWebhook{
		WebhookId:             issueId,
		EventType:             "new_relic_alert",
		EventId:               issueId,
		EventUrl:              fmt.Sprintf("https://one.%snewrelic.com/redirect/apm-issues/%s", uiRegionPrefix, issueId),
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
