package core

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"sync"
)

// HistoricalIncident represents a past incident title mapped to a service.
// Stored in tenant_attrs under TenantAttrHistoricalIncidentsKey so the LLM
// prompt can ground its service-name guesses in real prior incidents.
type HistoricalIncident struct {
	Title   string  `json:"title"`
	Service *string `json:"service"`
}

// TenantAttrHistoricalIncidentsKey is the tenant_attrs row name that stores
// the historical title→service mapping JSON.
const TenantAttrHistoricalIncidentsKey = "DATADOG_INCIDENT_TITLE_SERVICE_MAPPING"

// historicalIncidentsByTenant caches the parsed mapping per tenant. The key is
// the tenantId; absence means "not loaded yet"; an empty slice means "loaded,
// but the tenant has no patterns configured" (prevents repeated DB lookups).
var historicalIncidentsByTenant sync.Map

// historicalForTenant returns the cached patterns for the calling tenant,
// loading from tenant_attrs (with proper tenant_id filtering) on the first
// call per tenant. A miss in tenant_attrs is cached as an empty slice so we
// don't hammer the DB on every webhook.
func historicalForTenant(sc *security.RequestContext) []HistoricalIncident {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if cached, ok := historicalIncidentsByTenant.Load(tenantId); ok {
		return cached.([]HistoricalIncident)
	}

	attrs, err := tenant.GetTenantAttributesByName(sc, TenantAttrHistoricalIncidentsKey)
	if err != nil || len(attrs) == 0 {
		historicalIncidentsByTenant.Store(tenantId, []HistoricalIncident{})
		return nil
	}

	var incidents []HistoricalIncident
	if err := common.UnmarshalJson([]byte(attrs[0].Value), &incidents); err != nil {
		sc.GetLogger().Warn("subject_resolution: failed to unmarshal historical incidents", "error", err)
		historicalIncidentsByTenant.Store(tenantId, []HistoricalIncident{})
		return nil
	}

	historicalIncidentsByTenant.Store(tenantId, incidents)
	sc.GetLogger().Info("subject_resolution: loaded historical incidents", "tenant_id", tenantId, "count", len(incidents))
	return incidents
}

// historicalIncidentsForPrompt formats up to `limit` historical incidents
// for inclusion in the LLM prompt.
func historicalIncidentsForPrompt(incidents []HistoricalIncident, limit int) string {
	if limit <= 0 {
		limit = 50
	}
	if len(incidents) == 0 {
		return "(No historical data available)"
	}

	var sb strings.Builder
	count := 0
	for _, incident := range incidents {
		if incident.Service == nil || *incident.Service == "" {
			continue
		}
		fmt.Fprintf(&sb, "  - %q → %q\n", incident.Title, *incident.Service)
		count++
		if count >= limit {
			break
		}
	}
	if sb.Len() == 0 {
		return "(No historical data available)"
	}
	return sb.String()
}

// MatchWorkloadAndEnrich validates EventSubjectName against the k8s_workloads
// inventory for the given account and enriches the event with namespace, kind,
// cloud_resource_id, and matching labels.
//
// Strategies tried in order per candidate: exact name, app.kubernetes.io/name
// label, app label, tags.datadoghq.com/service label. All four require an
// exact match — there is intentionally no substring/ILIKE fallback because it
// risks overwriting a correct subject with an unrelated workload whose name
// happens to contain the candidate as a substring.
//
// When labels["nb_workload_candidates"] is set (comma-separated list, e.g. the
// dynatrace impacted-entity list), each entry is also tried after EventSubjectName
// fails — first match wins. The label is consumed before persistence.
//
// When a webhook opts out via labels["nb_skip_workload_match"]="true" (e.g.
// solarwinds title-as-subject events that would false-positive on ILIKE), the
// function returns without querying the workloads table. The opt-out label is
// consumed on its way out so it doesn't leak into the persisted event.
func MatchWorkloadAndEnrich(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) {
	if e.EventSubjectName == "" && e.Investigation.Labels[workloadCandidatesLabel] == "" {
		return
	}
	if e.Investigation.Labels[skipWorkloadMatchLabel] == "true" {
		delete(e.Investigation.Labels, skipWorkloadMatchLabel)
		delete(e.Investigation.Labels, workloadCandidatesLabel)
		return
	}

	candidates := []string{e.EventSubjectName}
	if extra := e.Investigation.Labels[workloadCandidatesLabel]; extra != "" {
		seen := map[string]bool{e.EventSubjectName: true}
		for _, c := range strings.Split(extra, ",") {
			c = strings.TrimSpace(c)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			candidates = append(candidates, c)
		}
	}
	delete(e.Investigation.Labels, workloadCandidatesLabel)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to get database manager", "error", err)
		return
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
		// Datadog convention — workloads tagged with tags.datadoghq.com/service.
		`SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND labels->>'tags.datadoghq.com/service' = $3 AND is_active = true AND kind NOT IN ('Job','CronJob') LIMIT 1`,
	}

	matched := false
	matchedCandidate := ""
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		for _, q := range queries {
			if err := dbms.Db.Get(&workload, q, tenantId, accountId, candidate); err == nil {
				matched = true
				matchedCandidate = candidate
				break
			}
		}
		if matched {
			break
		}
	}

	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}

	if !matched {
		sc.GetLogger().Info("subject_resolution: no workload match", "candidates", candidates, "account_id", accountId)
		e.Investigation.Labels["nb_matched_workload"] = "false"
		return
	}
	_ = matchedCandidate // available for future debug logging

	// Capture the pre-update name so we can keep EventSubjectOwner consistent
	// with EventSubjectName when the webhook had set them in lockstep
	// (datadog/dynatrace/newrelic pattern). Webhooks that intentionally use
	// EventSubjectOwner for a different purpose (e.g. servicenow's AssignedTo
	// user) won't satisfy this equality check, so they're left alone.
	priorSubjectName := e.EventSubjectName

	e.EventSubjectName = workload.Name
	if e.EventSubjectNamespace == "" {
		e.EventSubjectNamespace = workload.Namespace
	}
	kindLower := strings.ToLower(workload.Kind)
	e.EventSubjectKind = kindLower
	e.CloudResourceId = workload.CloudResourceId

	if e.EventSubjectOwner == "" || e.EventSubjectOwner == priorSubjectName {
		e.EventSubjectOwner = workload.Name
	}
	if e.EventSubjectOwnerKind == "" {
		e.EventSubjectOwnerKind = kindLower
	}

	e.Investigation.Labels["pod"] = workload.Name
	if workload.Namespace != "" {
		e.Investigation.Labels["namespace"] = workload.Namespace
	}
	e.Investigation.Labels["kind"] = workload.Kind
	e.Investigation.Labels["cloud_resource_id"] = workload.CloudResourceId
	e.Investigation.Labels["nb_matched_workload"] = "true"
}

// skipWorkloadMatchLabel marks an event whose EventSubjectName is display-only
// (e.g. an alert title) and should not be matched against the k8s_workloads
// inventory. The label is consumed by MatchWorkloadAndEnrich so it never
// reaches the persisted event payload.
const skipWorkloadMatchLabel = "nb_skip_workload_match"

// workloadCandidatesLabel carries a comma-separated list of additional workload
// names a webhook would like MatchWorkloadAndEnrich to try after EventSubjectName
// (e.g. dynatrace's impacted-entity list). The label is consumed during matching.
const workloadCandidatesLabel = "nb_workload_candidates"

// ResolveSubjectViaLLM asks the LLM to extract resource-identification labels
// from the full alert context (title, description, URL, labels) and validates
// the result against the k8s_workloads inventory.
//
// The caller should only invoke this when EventSubjectName is empty — this
// function does not short-circuit to preserve the same prompt-gen cost/benefit
// tradeoff the caller chose.
func ResolveSubjectViaLLM(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to get database manager for LLM matching", "error", err)
		return
	}

	tenantId := sc.GetSecurityContext().GetTenantId()

	// Pull both workload names and the datadog service tag values so the LLM
	// can match either convention. Some envs use bare workload names; others
	// (datadog deployments) tag workloads with tags.datadoghq.com/service.
	var names []string
	err = dbms.Db.Select(&names, `
		SELECT DISTINCT name FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2
		  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
		  AND name IS NOT NULL
		UNION
		SELECT DISTINCT labels->>'tags.datadoghq.com/service' FROM k8s_workloads
		WHERE tenant_id = $1 AND cloud_account_id = $2
		  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
		  AND labels->>'tags.datadoghq.com/service' IS NOT NULL
	`, tenantId, accountId)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to query workload names for LLM", "error", err)
		return
	}
	if len(names) == 0 {
		return
	}

	historicalPatterns := historicalIncidentsForPrompt(historicalForTenant(sc), 1000)

	labelsJSON, _ := json.Marshal(e.Investigation.Labels)

	prompt := fmt.Sprintf(`@llm You are a resource label extractor for monitoring alerts. Analyze the complete alert data below and extract resource identification labels.

## Alert Data
**Title:** %s
**Description:** %s
**Source URL:** %s
**Existing Labels:** %s

## Known Running Services/Workloads
%s

## Historical Title → Service Patterns
%s

## Task
From the alert data above, extract the following labels. Look at ALL the data — title, description, labels, URLs — to find clues about which resource this alert is about.

Return ONLY a valid JSON object with these fields (use empty string "" if not found):
{
  "subject_name": "<k8s workload/service name from the Running Services list that this alert is about>",
  "namespace": "<k8s namespace>",
  "cluster": "<k8s cluster name>",
  "pod_name": "<specific pod name if mentioned>",
  "service_name": "<application/service name>",
  "aws_service_name": "<AWS service like EC2, RDS, ELB if this is an AWS alert>",
  "aws_resource_id": "<AWS resource identifier like instance-id, ARN>"
}

RULES:
1. For subject_name: MUST be an exact match from the Running Services list, or empty string
2. Look for service names in alert titles (e.g. "booking-service down" → subject_name="booking-service")
3. Look for service names in pipe-delimited titles (e.g. "Critical | Prod | EKS | payment-service | high latency")
4. Look for service names embedded in URLs (e.g. service.name=courier-worker in query params)
5. Look for k8s resource references in labels (job, pod, deployment names)
6. For AWS alerts: extract the resource type and identifier from dimensions/labels
7. Consult the Historical Title → Service Patterns when rules 1-5 don't yield a match
8. If you cannot confidently identify a resource, return empty strings — do NOT guess
9. Return ONLY the JSON object, no explanation, no markdown fences`,
		e.EventTitle,
		e.EventDescription,
		e.Investigation.SourceUrl,
		string(labelsJSON),
		strings.Join(names, ", "),
		historicalPatterns,
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
		sc.GetLogger().Error("subject_resolution: LLM label extraction failed", "error", err)
		return
	}
	if response == nil || len(response.Response) == 0 {
		sc.GetLogger().Warn("subject_resolution: LLM returned empty response")
		return
	}

	responseText := response.Response[0]
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	var extracted map[string]string
	if err := json.Unmarshal([]byte(responseText), &extracted); err != nil {
		sc.GetLogger().Error("subject_resolution: failed to parse LLM response as JSON", "error", err, "response", responseText)
		return
	}

	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}

	if subj := extracted["subject_name"]; subj != "" {
		// LLM may return multiple comma-separated services. Keep the full
		// response on labels["service"] for downstream auto-actions, but use
		// only the first part as the subject + workload-lookup candidate.
		lookup := subj
		if parts := strings.SplitN(subj, ",", 2); len(parts) > 1 {
			lookup = strings.TrimSpace(parts[0])
		}
		e.EventSubjectName = lookup
		e.Investigation.Labels["nb_llm_match"] = subj
		if e.Investigation.Labels["service"] == "" {
			e.Investigation.Labels["service"] = subj
		}
	} else {
		e.Investigation.Labels["nb_llm_match"] = "not_found"
	}

	if ns := extracted["namespace"]; ns != "" && e.EventSubjectNamespace == "" {
		e.EventSubjectNamespace = ns
		e.Investigation.Labels["namespace"] = ns
	}

	for _, key := range []string{"cluster", "pod_name", "service_name", "aws_service_name", "aws_resource_id"} {
		if val := extracted[key]; val != "" {
			if e.Investigation.Labels[key] == "" {
				e.Investigation.Labels[key] = val
			}
		}
	}

	if e.EventSubjectName != "" {
		MatchWorkloadAndEnrich(sc, e, accountId)
	}
}

// enrichEventsWithSubjectResolution runs on every webhook event after account
// mapping. When a subject is already identified, it validates against the
// workloads inventory. When one isn't, it falls back to the LLM extractor if
// the feature flag is enabled.
func enrichEventsWithSubjectResolution(sc *security.RequestContext, events []EventIncomingWebhook) {
	if len(events) == 0 {
		return
	}
	tenantId := sc.GetSecurityContext().GetTenantId()
	llmEnabled := tenant.IsFeatureEnabled(sc, tenantId, tenant.FEATURE_WEBHOOK_LLM_RESOLUTION)

	for i := range events {
		accountId := events[i].AccountId
		if accountId == "" {
			continue
		}
		if events[i].Investigation.Labels == nil {
			events[i].Investigation.Labels = map[string]string{}
		}
		if events[i].EventSubjectName != "" {
			MatchWorkloadAndEnrich(sc, &events[i], accountId)
			continue
		}
		if llmEnabled {
			ResolveSubjectViaLLM(sc, &events[i], accountId)
		} else {
			events[i].Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}
}
