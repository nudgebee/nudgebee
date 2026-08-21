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
	"nudgebee/services/security"
	"nudgebee/services/tenant"
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

// ZenDuty Urgency Constants — ZenDuty urgency is binary: 0 = Low, 1 = High
// (https://zenduty.com/docs/workflows/, trigger_data.urgency). There is no
// medium and no value 2.
const (
	ZenDutyUrgencyLow  = 0
	ZenDutyUrgencyHigh = 1
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
	// Only assign when the regex matched — empty assignment would clobber later
	// label-based resolution in resolveSubjectFromLabels.
	if pod != "" {
		parsedPayload.EventSubjectName = pod
	}
	if namespace != "" {
		parsedPayload.EventSubjectNamespace = namespace
	}

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
		alert.Severity = event.EventPriorityHigh
	default:
		alert.Severity = event.EventPriorityLow
	}

	// Parse the Alertmanager-style firing text embedded in incident.Summary.
	// Grafana/Prometheus/SigNoz/Alertmanager-routed alerts deliver real labels
	// (alertname, namespace, pod, severity, etc.) inside this block. Shared with
	// PagerDuty via parseFiringLabels (defined in pagerduty_webhook.go, same package).
	// Note: parseFiringLabels does NOT prefix annotation keys — both Labels and
	// Annotations sections flatten into one map; later section wins on collision.
	summaryLabels := parseFiringLabels(incident.Summary)

	// Build alert.Labels carefully. CRITICAL: do NOT write a "service_name" key
	// from incident.Service.Name — event/service.go:1683 walks subjectKeys
	// (including service_name) as the subject-name fallback when EventSubjectName
	// is empty. The Zenduty "service" is an org grouping, not the alert subject.
	// The "nb_" prefix keeps the grouping available for evidence/audit without
	// participating in subject resolution. Any real service_name from the
	// upstream firing labels still flows through via the merge below.
	alert.Labels = map[string]string{}
	if incident.Service.UniqueID != "" {
		alert.Labels["nb_zenduty_service_id"] = incident.Service.UniqueID
	}
	if incident.Service.Name != "" {
		alert.Labels["nb_zenduty_service_name"] = incident.Service.Name
	}
	// Non-clobber merge so upstream labels never overwrite intentional writes
	// (mirrors mergeWebhookQueryLabels at core/integration_webhook.go:201).
	for k, v := range summaryLabels {
		if _, exists := alert.Labels[k]; !exists {
			alert.Labels[k] = v
		}
	}

	if incident.AlertSource != "" {
		alert.Labels["alert_source"] = incident.AlertSource
	}
	if incident.AlertType != "" {
		alert.Labels["alert_type"] = incident.AlertType
	}

	// Zenduty REST API enrichment. The webhook summary is often a one-line
	// description with no Alertmanager labels block (typical for vmalert →
	// Zenduty). The /api/incidents/{id}/alerts/ endpoint exposes the clean
	// alertname (entity_id) and the upstream source name (integration_object.name)
	// that the webhook does not, and the alert-payload endpoint recovers the raw
	// Alertmanager label set (namespace/pod/deployment/...) that makes subject
	// resolution deterministic. incident.Summary selects the right alert out of a
	// collated incident. Cached per incident; fails soft on any API error.
	enrichWithZendutyAPI(sc, &alert, incident.UniqueID, incident.Summary)

	// Fingerprint: prefer Zenduty's stable IncidentKey (its documented dedup key).
	// When absent — the norm, since the parser does not populate it — leave it
	// empty here and derive a canonical fingerprint from the alert's identity
	// after subject resolution below. Keying on the per-incident UniqueID (the
	// previous fallback) fragments repeat incidents for the same alert into
	// separate occurrence chains; finding_id already carries UniqueID, so rows are
	// unaffected either way.
	if incident.IncidentKey != "" {
		alert.Fingerprint = incident.IncidentKey
	}

	// RuleId / RuleName: derive from the extracted alertname so multiple firings
	// of the same underlying rule share an aggregation_key. Reads from alert.Labels
	// (a superset of summaryLabels post-enrichment) so the API-derived alertname —
	// when present — also drives the rule. Falls back to the per-incident UniqueID
	// only when neither the summary nor the API yielded an alertname/rulename.
	alertname := alert.Labels["alertname"]
	rulename := alert.Labels["rulename"]
	annSummary := alert.Labels["summary"] // annotation_summary — parseFiringLabels does not prefix
	switch {
	case alertname != "":
		alert.RuleId = alertname
		alert.RuleName = alertname
	case rulename != "":
		alert.RuleId = rulename
		alert.RuleName = rulename
	default:
		alert.RuleId = incident.UniqueID
		alert.RuleName = incident.Title
	}

	// Title rewrite: prefer the human-readable annotation summary or rule name
	// over the noisy alertmanager-concatenated incident.Title.
	switch {
	case annSummary != "":
		parsedPayload.EventTitle = annSummary
	case rulename != "":
		parsedPayload.EventTitle = rulename
	case alertname != "":
		parsedPayload.EventTitle = alertname
	}

	// Severity refinement: use the explicit severity label when present
	// (warning/critical/info from Alertmanager → finer-grained than Zenduty urgency).
	if sev := alert.Labels["severity"]; sev != "" {
		alert.Severity = mapSeverityToEventPriority(sev)
	}

	// nb_alert_* normalization for buildAlertRuleEvidence rendering.
	if alert.RuleName != "" && alert.Labels["nb_alert_name"] == "" {
		alert.Labels["nb_alert_name"] = alert.RuleName
	}
	if alert.RuleId != "" && alert.Labels["nb_alert_rule_id"] == "" {
		alert.Labels["nb_alert_rule_id"] = alert.RuleId
	}
	if sev := alert.Labels["severity"]; sev != "" && alert.Labels["nb_alert_severity"] == "" {
		alert.Labels["nb_alert_severity"] = sev
	}
	// Fallback nb_alert_source heuristic when API enrichment didn't set it.
	if alert.Labels["nb_alert_source"] == "" {
		switch {
		case alert.Labels["grafana_folder"] != "" || alert.Labels["datasource_uid"] != "":
			alert.Labels["nb_alert_source"] = "grafana"
		case alert.Labels["alertname"] != "":
			alert.Labels["nb_alert_source"] = "prometheus"
		}
	}

	parsedPayload.Investigation = alert

	// Subject resolution via the prioritized label walk shared with
	// PagerDuty/Datadog/Dynatrace (resolveSubjectFromLabels in pagerduty_webhook.go).
	// Runs only when EventSubjectName is empty — title-regex hit above wins if it found anything.
	resolveSubjectFromLabels(&parsedPayload)

	// Remap account before enrichment so workload lookups target the correct account
	accountMapping := core.ParseAccountMapping(settings, sc.GetLogger())
	accountId = core.ApplyAccountMapping(accountId, parsedPayload.Investigation.Labels, accountMapping)

	// Subject validation against k8s_workloads is NOT done here. Every webhook event
	// goes through core.MatchWorkloadAndEnrich in enrichEventsWithSubjectResolution
	// after account mapping, and that matcher scopes the lookup to the event's own
	// namespace and refuses ambiguous rows when the namespace is unknown.

	// Deterministic last resort: the Zenduty service itself. Only reached once the
	// title regex, the firing-label walk and the API enrichment have all come up
	// empty — the shape produced by every alert source that does not emit an
	// Alertmanager labels block (in-house stacks that post plain
	// "BreachedChecks:"/"Variables:" text carry no k8s labels at all).
	if parsedPayload.EventSubjectName == "" {
		resolveZendutySubjectFromService(sc, &parsedPayload, incident.Service.Name, accountId)
	}

	// Defensive guard: security context can be nil if the webhook arrives without
	// a resolvable tenant (test harness, malformed auth, etc). Skip LLM + learn
	// rather than panic so the parsed payload still flows downstream.
	secCtx := sc.GetSecurityContext()

	// LLM fallback: if no subject found after deterministic parsing, use LLM
	resolvedByLLM := false
	if parsedPayload.EventSubjectName == "" {
		if secCtx == nil {
			parsedPayload.Investigation.Labels["nb_llm_match"] = "disabled"
		} else if tenant.IsFeatureEnabled(sc, secCtx.GetTenantId(), tenant.FEATURE_WEBHOOK_LLM_RESOLUTION) {
			resolveZendutySubjectUsingLLM(sc, &parsedPayload, accountId)
			resolvedByLLM = parsedPayload.EventSubjectName != ""
		} else {
			parsedPayload.Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}

	// If Zenduty gave no stable dedup key (IncidentKey), derive a canonical
	// fingerprint from the alert's identity now that the subject is resolved, so
	// repeat incidents for the same alert collapse into one occurrence chain
	// instead of fragmenting on the per-incident UniqueID. Use the stable alert
	// name (not RuleId, which itself falls back to UniqueID). Require a real
	// alert-type or subject to avoid over-merging distinct alerts; otherwise keep
	// the per-incident id.
	if parsedPayload.Investigation.Fingerprint == "" {
		alertType := alertname
		if alertType == "" {
			alertType = rulename
		}
		if alertType != "" || parsedPayload.EventSubjectName != "" {
			parsedPayload.Investigation.Fingerprint = core.CanonicalFingerprint(
				"zenduty",
				alertType,
				parsedPayload.EventSubjectNamespace,
				parsedPayload.EventSubjectName,
			)
		} else {
			parsedPayload.Investigation.Fingerprint = incident.UniqueID
		}
	}

	// Auto-learn: save confirmed title → service mapping for future LLM prompts.
	// Only LLM-resolved subjects are learned — a deterministic match already came
	// from structured metadata, not the title's wording, so it isn't a genuine
	// title→service pattern and would misleadingly bias future prompts.
	if resolvedByLLM && secCtx != nil && parsedPayload.EventSubjectName != "" && parsedPayload.EventTitle != "" {
		LearnSubjectMapping(sc, secCtx.GetTenantId(), TenantAttrZendutyIncidentsKey, parsedPayload.EventTitle, parsedPayload.EventSubjectName)
	}

	// Alert-rule evidence card (Grafana/Prometheus source URLs, severity, threshold, etc.).
	// Shared with PagerDuty via buildAlertRuleEvidence (pagerduty_webhook.go, same package).
	if ev := buildAlertRuleEvidence(parsedPayload.Investigation.Labels); ev != nil {
		parsedPayload.Investigation.Evidences = append(parsedPayload.Investigation.Evidences, *ev)
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
	if urgency == ZenDutyUrgencyHigh {
		return "high"
	}
	return "low"
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

// resolveZendutySubjectFromService uses incident.service.name as the subject of
// last resort. That field is deliberately kept out of the label walk above (see the
// alert.Labels comment) because a Zenduty "service" is frequently an org grouping —
// "Payments Team" — rather than the object the alert is about, and a wrong subject
// is worse than none: it also suppresses the LLM fallback.
//
// So it is accepted only in the two cases where it cannot produce a wrong answer:
//
//  1. It matches a workload in the account's inventory. Validated, so it names a
//     real subject regardless of how that tenant uses Zenduty services.
//  2. The account has no workload inventory at all AND the name is shaped like a
//     service identifier rather than a human label. The LLM fallback classifies over
//     exactly that inventory: webhook_subject_name_extractor's options are the
//     account's running services plus "Not Found", so on an empty inventory the only
//     answer it can return is "Not Found". These alerts therefore resolve to no
//     subject whatsoever today, and the slug is the only service-identifying field
//     the payload carries. The shape check is what keeps an org grouping ("Database
//     Team", "Payments Team") out: those are title-cased prose, never slugs, so they
//     stay unresolved exactly as they do today.
//
// When the inventory exists but does not contain the name, the subject is left
// empty so the LLM fallback runs exactly as it did before this fallback existed.
func resolveZendutySubjectFromService(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, serviceName, accountId string) {
	if serviceName == "" {
		return
	}
	secCtx := sc.GetSecurityContext()
	if secCtx == nil {
		return
	}
	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = map[string]string{}
	}

	parsedPayload.EventSubjectName = serviceName
	core.MatchWorkloadAndEnrich(sc, parsedPayload, accountId)

	if parsedPayload.Investigation.Labels["nb_matched_workload"] == "true" {
		parsedPayload.Investigation.Labels["nb_subject_resolution"] = "zenduty_service_validated"
		return
	}

	// reServiceNamePattern (pagerduty_webhook.go) matches lowercase hyphenated
	// identifiers — "prod-unicorn-app" passes, "Database Team" does not.
	if !reServiceNamePattern.MatchString(serviceName) ||
		accountHasWorkloadInventory(sc, secCtx.GetTenantId(), accountId) {
		parsedPayload.EventSubjectName = ""
		delete(parsedPayload.Investigation.Labels, "nb_matched_workload")
		return
	}

	parsedPayload.Investigation.Labels["nb_subject_resolution"] = "zenduty_service_unvalidated"
}

// accountHasWorkloadInventory reports whether the account has any active workload
// rows. This distinguishes "the subject is not this workload" from "there is no
// inventory to check against" — the two need opposite fallback decisions. On any
// error it reports true, which preserves the pre-existing LLM-fallback behaviour.
func accountHasWorkloadInventory(sc *security.RequestContext, tenantId, accountId string) bool {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: failed to get database manager for inventory check", "error", err)
		return true
	}

	var exists bool
	err = dbms.Db.Get(&exists, `
		SELECT EXISTS (
			SELECT 1 FROM k8s_workloads
			WHERE tenant_id = $1 AND cloud_account_id = $2
			  AND is_active = true AND kind NOT IN ('Job', 'CronJob')
		)`, tenantId, accountId)
	if err != nil {
		sc.GetLogger().Error("zendutywebhook: workload inventory check failed", "error", err)
		return true
	}
	return exists
}

// resolveZendutySubjectUsingLLM asks the webhook_subject_name_extractor agent to
// map the alert to a running service, then validates against k8s_workloads with the
// ILIKE-capable matcher. The agent owns the running-services and historical context
// server-side, so only the alert is sent here.
func resolveZendutySubjectUsingLLM(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, accountId string) {
	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = map[string]string{}
	}
	tenantId := sc.GetSecurityContext().GetTenantId()

	name := core.ResolveSubjectNameViaAgent(sc, accountId,
		parsedPayload.EventTitle, parsedPayload.EventDescription,
		parsedPayload.Investigation.SourceUrl, parsedPayload.Investigation.Labels)
	if name == "" {
		common.MetricsSubjectResolution(sc.GetContext(), IntegrationZendutyWebhook, "live", "not_found", tenantId)
		return
	}
	common.MetricsSubjectResolution(sc.GetContext(), IntegrationZendutyWebhook, "live", "matched", tenantId)

	parsedPayload.EventSubjectName = name
	core.MatchWorkloadAndEnrich(sc, parsedPayload, accountId)
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
