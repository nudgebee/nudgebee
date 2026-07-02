package core

import (
	"encoding/json"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
)

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

const (
	// webhookSubjectAgentMention pins the dedicated single-shot classifier agent in
	// llm-server, bypassing the LLM router.
	webhookSubjectAgentMention = "@webhook_subject_name_extractor"
	// webhookSubjectAgentSource tags the request origin for observability/budgeting.
	webhookSubjectAgentSource = "webhook_subject_name_extractor"
	// webhookSubjectAgentNotFound is the sentinel the agent returns when it declines.
	webhookSubjectAgentNotFound = "Not Found"
)

// ResolveSubjectNameViaAgent asks the webhook_subject_name_extractor agent to map
// an alert to a single running-service name. The agent owns the running-services
// inventory and the historical title→service patterns (fetched and cached
// server-side), so this sends only the alert itself — no inventory is embedded in
// the request, which is what keeps webhook token cost bounded.
//
// It returns the matched service name, or "" when the agent declines ("Not Found"),
// and records the nb_llm_match label (and a default service label) on labels.
func ResolveSubjectNameViaAgent(sc *security.RequestContext, accountId, title, description, sourceURL string, labels map[string]string) string {
	if labels == nil {
		labels = map[string]string{}
	}

	payload, _ := json.Marshal(map[string]any{
		"title":       title,
		"description": description,
		"source_url":  sourceURL,
		"labels":      labels,
	})

	chatRequest := llm.ConversationApiRequest{
		Query:     webhookSubjectAgentMention + "\n" + string(payload),
		AccountId: accountId,
		UserId:    sc.GetSecurityContext().GetUserId(),
		Async:     false,
		Source:    webhookSubjectAgentSource,
	}

	response, err := llm.ChatCompletion(sc, chatRequest)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: webhook_subject_name_extractor call failed", "error", err)
		return ""
	}
	if response == nil || len(response.Response) == 0 {
		sc.GetLogger().Warn("subject_resolution: webhook_subject_name_extractor returned empty response")
		return ""
	}

	name := strings.TrimSpace(response.Response[0])
	if name == "" || strings.EqualFold(name, webhookSubjectAgentNotFound) {
		labels["nb_llm_match"] = "not_found"
		return ""
	}
	labels["nb_llm_match"] = name
	if labels["service"] == "" {
		labels["service"] = name
	}
	return name
}

// ResolveSubjectViaLLM resolves an event's subject via the
// webhook_subject_name_extractor agent and validates the result against the
// k8s_workloads inventory. The caller should only invoke this when
// EventSubjectName is empty.
func ResolveSubjectViaLLM(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) {
	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}

	name := ResolveSubjectNameViaAgent(sc, accountId, e.EventTitle, e.EventDescription, e.Investigation.SourceUrl, e.Investigation.Labels)
	if name == "" {
		return
	}

	e.EventSubjectName = name
	MatchWorkloadAndEnrich(sc, e, accountId)
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
		// An integration-specific resolver (datadog/pagerduty/zenduty) already ran
		// the extractor agent when it set nb_llm_match. Don't fire a second, redundant
		// call here — the agent would just reproduce the prior not_found.
		if _, alreadyTried := events[i].Investigation.Labels["nb_llm_match"]; alreadyTried {
			continue
		}
		if llmEnabled {
			ResolveSubjectViaLLM(sc, &events[i], accountId)
		} else {
			events[i].Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}
}
