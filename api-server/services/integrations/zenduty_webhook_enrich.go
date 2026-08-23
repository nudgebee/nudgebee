package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

// Zenduty's outgoing webhook (vmalert/Prometheus/...) often omits the
// Alertmanager labels block, leaving just a one-line summary + title. The
// underlying alert object on Zenduty's REST API exposes two extra fields the
// webhook doesn't:
//
//   - entity_id              : clean alertname (e.g. "KubeDeploymentReplicasMismatch")
//   - integration_object.name: upstream source name (e.g. "vmalert", "Grafana")
//
// We fetch /api/incidents/{id}/alerts/ and merge those into alert.Labels.
// Best-effort: any API failure falls through silently — the webhook still
// processes with what it has.
//
// The raw Alertmanager body (commonLabels/annotations/generatorURL) is NOT on
// any documented endpoint — /incidents/{id}/alerts/ has no payload field and
// /incidents/{id}/alerts/{alert_id}/ is a 404. It is reachable through an
// undocumented v2 endpoint that returns a presigned S3 URL:
//
//	GET /api/v2/incidents/get_alert_payload/alert_payload/
//	      {account}/{team}/{service}/{integration}/{Y}/{M}/{D}/{alert_id}.json/
//	  -> {"url": "https://zenduty-alerts.s3.amazonaws.com/... (1h expiry)"}
//
// It authorizes with the same API token (a browser session is NOT required).
// Every path component is derivable: team/service/integration come from the
// alerts response above, the date from the alert's creation_date (UTC, and
// NOT zero-padded), and the account from /api/account/teams/.
//
// Being undocumented, this can break without notice — every step fails soft and
// the webhook still processes with whatever it already had.

const zendutyEnrichCacheNamespace = "zenduty_alert_enrichment"
const zendutyEnrichCacheTTL = 30 * time.Minute
const zendutyEnrichAPITimeout = 10 * time.Second

// zendutyAPIBaseURL is the base URL used by enrichment fetches. Package-level
// var (not const) so tests can swap it for an httptest.Server URL.
var zendutyAPIBaseURL = ZenDutyDefaultURL

// zendutyPayloadBaseURL hosts the undocumented alert-payload endpoint. Separate
// var because it sits under /api/v2 rather than the documented /api root, and so
// tests can point it at an httptest.Server.
var zendutyPayloadBaseURL = "https://www.zenduty.com/api/v2"

func init() {
	common.CacheCreateNamespace(zendutyEnrichCacheNamespace,
		common.CacheNamespaceWithExpiration(zendutyEnrichCacheTTL),
		common.CacheNamespaceWithMaxEntries(5000),
	)
}

// zendutyEnrichResult is what we extract from the alerts API and cache.
type zendutyEnrichResult struct {
	EntityID            string `json:"entity_id,omitempty"`
	IntegrationName     string `json:"integration_name,omitempty"`
	IntegrationUniqueID string `json:"integration_id,omitempty"`
	// PayloadLabels is the raw Alertmanager label set recovered from the
	// alert-payload endpoint, already flattened into the same key vocabulary
	// parseFiringLabels produces for PagerDuty.
	PayloadLabels map[string]string `json:"payload_labels,omitempty"`
}

// zendutyAlert is the subset of an alert object we consume from
// /incidents/{id}/alerts/. The list serializer is the full object — there is no
// per-alert detail view.
type zendutyAlert struct {
	UniqueID          string `json:"unique_id"`
	EntityID          string `json:"entity_id"`
	Summary           string `json:"summary"`
	CreationDate      string `json:"creation_date"`
	IntegrationObject struct {
		Name     string `json:"name"`
		UniqueID string `json:"unique_id"`
		Team     string `json:"team"`
		Service  string `json:"service"`
	} `json:"integration_object"`
}

// enrichWithZendutyAPI fills missing fields on alert.Labels from Zenduty's
// REST API. Skipped when no Zenduty integration is configured for the tenant or
// when the API call fails. Cached per (tenant, incidentID, summary) for 30
// minutes — the summary participates because a collated incident holds several
// alerts and the summary is what identifies which one this webhook is about.
//
// incidentSummary is the webhook's incident.summary; it selects the matching
// alert out of a collated incident (see selectZendutyAlert).
func enrichWithZendutyAPI(sc *security.RequestContext, alert *core.EventIncomingWebhookInvestigation, incidentID, incidentSummary string) {
	if incidentID == "" || alert == nil {
		return
	}
	if alert.Labels == nil {
		alert.Labels = map[string]string{}
	}

	// The webhook summary already carried an Alertmanager labels block (the
	// Grafana-routed shape), so parseFiringLabels has given us everything the
	// API would. Nothing to fetch.
	if alert.Labels["alertname"] != "" && alert.Labels["nb_alert_source"] != "" {
		return
	}
	hadSummaryLabels := alert.Labels["alertname"] != ""

	secCtx := sc.GetSecurityContext()
	if secCtx == nil {
		return
	}
	tenantId := secCtx.GetTenantId()
	if tenantId == "" {
		return
	}

	cacheKey := fmt.Sprintf("%s:%s:%d", tenantId, incidentID, hashString(incidentSummary))

	// Cache lookup
	if data, hit := common.CacheGet(zendutyEnrichCacheNamespace, cacheKey); hit {
		var cached zendutyEnrichResult
		if err := json.Unmarshal(data, &cached); err == nil {
			applyZendutyEnrichResult(alert, cached)
			return
		}
	}

	apiKey, err := getZendutyAPIKey(sc, tenantId)
	if err != nil {
		sc.GetLogger().Debug("zendutywebhook: API enrichment skipped (no API key)",
			"incident", incidentID, "error", err)
		return
	}

	alerts, err := fetchZendutyAlertsForIncident(incidentID, apiKey)
	if err != nil {
		sc.GetLogger().Warn("zendutywebhook: alerts API fetch failed, continuing with webhook data",
			"incident", incidentID, "error", err)
		return
	}

	selected, ok := selectZendutyAlert(alerts, incidentSummary)
	if !ok {
		return
	}

	result := zendutyEnrichResult{
		EntityID:            selected.EntityID,
		IntegrationName:     selected.IntegrationObject.Name,
		IntegrationUniqueID: selected.IntegrationObject.UniqueID,
	}

	// Recover the raw Alertmanager labels. Skipped when the webhook summary
	// already carried them, since they'd only be a non-clobber no-op.
	if !hadSummaryLabels {
		labels, payloadErr := fetchZendutyAlertPayloadLabels(sc, selected, tenantId, apiKey)
		if payloadErr != nil {
			sc.GetLogger().Debug("zendutywebhook: alert payload unavailable, falling back to summary parsing",
				"incident", incidentID, "alert", selected.UniqueID, "error", payloadErr)
		} else {
			result.PayloadLabels = labels
		}
	}

	if data, marshalErr := json.Marshal(result); marshalErr == nil {
		if setErr := common.CacheSet(zendutyEnrichCacheNamespace, cacheKey, data); setErr != nil {
			sc.GetLogger().Debug("zendutywebhook: failed to cache enrich result",
				"incident", incidentID, "error", setErr)
		}
	}

	applyZendutyEnrichResult(alert, result)
}

// selectZendutyAlert picks the alert a webhook is about. Zenduty collates
// multiple alerts into one incident (service `collation`), and the alerts API
// returns them newest-first — so the previous Results[0] could name a different
// alert, on a different workload, than the webhook that triggered processing.
// The incident summary is what disambiguates: Zenduty sets it from the alert
// that raised the incident. Falls back to the newest alert when nothing matches.
func selectZendutyAlert(alerts []zendutyAlert, incidentSummary string) (zendutyAlert, bool) {
	if len(alerts) == 0 {
		return zendutyAlert{}, false
	}
	if incidentSummary != "" {
		for _, a := range alerts {
			if a.Summary == incidentSummary {
				return a, true
			}
		}
	}
	return alerts[0], true
}

// hashString is a small non-cryptographic hash used only to keep cache keys
// bounded when the incident summary participates in the key.
func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// applyZendutyEnrichResult merges the API-derived fields into alert.Labels
// using non-clobber semantics — values already present from the webhook
// summary always win.
func applyZendutyEnrichResult(alert *core.EventIncomingWebhookInvestigation, r zendutyEnrichResult) {
	if alert.Labels == nil {
		alert.Labels = map[string]string{}
	}

	if r.EntityID != "" {
		if alert.Labels["alertname"] == "" {
			alert.Labels["alertname"] = r.EntityID
		}
		if alert.Labels["nb_alert_entity_id"] == "" {
			alert.Labels["nb_alert_entity_id"] = r.EntityID
		}
	}

	if r.IntegrationName != "" {
		if alert.Labels["nb_alert_source"] == "" {
			alert.Labels["nb_alert_source"] = normalizeZendutyIntegrationName(r.IntegrationName)
		}
		if alert.Labels["nb_zenduty_integration_name"] == "" {
			alert.Labels["nb_zenduty_integration_name"] = r.IntegrationName
		}
	}

	if r.IntegrationUniqueID != "" && alert.Labels["nb_zenduty_integration_id"] == "" {
		alert.Labels["nb_zenduty_integration_id"] = r.IntegrationUniqueID
	}

	// Raw Alertmanager labels last, still non-clobber: anything already derived
	// from the webhook summary or from entity_id above stays authoritative.
	for k, v := range r.PayloadLabels {
		if v != "" && alert.Labels[k] == "" {
			alert.Labels[k] = v
		}
	}
	if len(r.PayloadLabels) > 0 {
		alert.Labels["nb_zenduty_payload_recovered"] = "true"
	}
}

// normalizeZendutyIntegrationName maps Zenduty's integration_object.name strings
// (which mirror upstream monitoring product names) to the canonical
// nb_alert_source vocabulary used by buildAlertRuleEvidence.
func normalizeZendutyIntegrationName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "vmalert", "victoriametrics", "prometheus", "alertmanager":
		return "prometheus"
	case "grafana":
		return "grafana"
	case "signoz", "signoz alert manager":
		return "signoz"
	case "chronosphere":
		return "chronosphere"
	case "aws cloudwatch", "cloudwatch", "aws":
		return "aws"
	case "azure monitor", "azure":
		return "azure"
	case "datadog":
		return "datadog"
	case "newrelic", "new relic":
		return "newrelic"
	case "dynatrace":
		return "dynatrace"
	case "splunk":
		return "splunk"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// zendutyGet performs an authenticated GET and returns the body on HTTP 200.
// Pass an empty apiKey for presigned URLs, which must NOT carry an auth header.
func zendutyGet(url, apiKey, what string) ([]byte, error) {
	options := []common.HttpOption{common.HttpWithTimeout(zendutyEnrichAPITimeout)}
	if apiKey != "" {
		options = append(options, common.HttpWithHeaders(map[string]string{
			"Authorization": "Token " + apiKey,
			"Content-Type":  "application/json",
		}))
	}

	resp, err := common.HttpGet(url, options...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", what, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %d: %s", what, resp.StatusCode, truncate(string(bodyBytes), 300))
	}
	return bodyBytes, nil
}

// fetchZendutyAlertsForIncident calls GET /api/incidents/{id}/alerts/ and returns
// every alert on the incident. A collated incident holds more than one, so the
// caller selects (see selectZendutyAlert) rather than assuming the first.
func fetchZendutyAlertsForIncident(incidentID, apiKey string) ([]zendutyAlert, error) {
	url := fmt.Sprintf("%s/incidents/%s/alerts/", zendutyAPIBaseURL, incidentID)
	bodyBytes, err := zendutyGet(url, apiKey, "zenduty alerts API")
	if err != nil {
		return nil, err
	}

	var payload struct {
		Results []zendutyAlert `json:"results"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal alerts response: %w", err)
	}
	return payload.Results, nil
}

// fetchZendutyAccountID resolves the account that owns a team, via
// GET /api/account/teams/. Cached per (tenant, team) — the mapping never changes.
func fetchZendutyAccountID(sc *security.RequestContext, tenantId, teamID, apiKey string) (string, error) {
	if teamID == "" {
		return "", errors.New("no team id on alert integration")
	}

	cacheKey := "account:" + tenantId + ":" + teamID
	if data, hit := common.CacheGet(zendutyEnrichCacheNamespace, cacheKey); hit && len(data) > 0 {
		return string(data), nil
	}

	bodyBytes, err := zendutyGet(zendutyAPIBaseURL+"/account/teams/", apiKey, "zenduty teams API")
	if err != nil {
		return "", err
	}

	var teams []struct {
		UniqueID string `json:"unique_id"`
		Account  string `json:"account"`
	}
	if err := json.Unmarshal(bodyBytes, &teams); err != nil {
		return "", fmt.Errorf("unmarshal teams response: %w", err)
	}

	for _, t := range teams {
		if t.UniqueID == teamID && t.Account != "" {
			if setErr := common.CacheSet(zendutyEnrichCacheNamespace, cacheKey, []byte(t.Account)); setErr != nil {
				sc.GetLogger().Debug("zendutywebhook: failed to cache account id", "team", teamID, "error", setErr)
			}
			return t.Account, nil
		}
	}
	return "", fmt.Errorf("team %s not found in account teams", teamID)
}

// fetchZendutyAlertPayloadLabels recovers the raw Alertmanager body for an alert
// and flattens it into labels. Two hops: the v2 endpoint returns a presigned S3
// URL, which is then fetched without auth.
func fetchZendutyAlertPayloadLabels(sc *security.RequestContext, a zendutyAlert, tenantId, apiKey string) (map[string]string, error) {
	if a.UniqueID == "" {
		return nil, errors.New("alert has no unique_id")
	}
	account, err := fetchZendutyAccountID(sc, tenantId, a.IntegrationObject.Team, apiKey)
	if err != nil {
		return nil, err
	}

	url, err := zendutyAlertPayloadURL(a, account)
	if err != nil {
		return nil, err
	}

	presignBytes, err := zendutyGet(url, apiKey, "zenduty alert payload API")
	if err != nil {
		return nil, err
	}

	var presigned struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(presignBytes, &presigned); err != nil {
		return nil, fmt.Errorf("unmarshal presigned response: %w", err)
	}
	if presigned.URL == "" {
		return nil, errors.New("alert payload response carried no url")
	}

	// Presigned — an Authorization header would conflict with the signature.
	payloadBytes, err := zendutyGet(presigned.URL, "", "zenduty alert payload object")
	if err != nil {
		return nil, err
	}

	var stored struct {
		Payload alertmanagerPayload `json:"payload"`
	}
	if err := json.Unmarshal(payloadBytes, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal alert payload: %w", err)
	}

	labels := alertmanagerPayloadToLabels(stored.Payload)
	if len(labels) == 0 {
		return nil, errors.New("alert payload carried no labels")
	}
	return labels, nil
}

// zendutyAlertPayloadURL builds the undocumented alert-payload path. The date
// segments come from the alert's own creation_date in UTC and are NOT
// zero-padded — "2026/7/29", not "2026/07/29". A padded path 404s.
func zendutyAlertPayloadURL(a zendutyAlert, account string) (string, error) {
	created, err := time.Parse(time.RFC3339, a.CreationDate)
	if err != nil {
		return "", fmt.Errorf("parse alert creation_date %q: %w", a.CreationDate, err)
	}
	created = created.UTC()

	if account == "" || a.IntegrationObject.Team == "" ||
		a.IntegrationObject.Service == "" || a.IntegrationObject.UniqueID == "" {
		return "", errors.New("alert is missing account/team/service/integration ids")
	}

	return fmt.Sprintf("%s/incidents/get_alert_payload/alert_payload/%s/%s/%s/%s/%d/%d/%d/%s.json/",
		zendutyPayloadBaseURL, account, a.IntegrationObject.Team, a.IntegrationObject.Service,
		a.IntegrationObject.UniqueID, created.Year(), int(created.Month()), created.Day(), a.UniqueID), nil
}

// alertmanagerPayload is the Alertmanager v4 webhook body Zenduty archives.
type alertmanagerPayload struct {
	Alerts []struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		GeneratorURL string            `json:"generatorURL"`
		Fingerprint  string            `json:"fingerprint"`
	} `json:"alerts"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
}

// alertmanagerPayloadToLabels flattens an Alertmanager body into the SAME key
// vocabulary parseFiringLabels produces for PagerDuty, so both sources feed
// resolveSubjectFromLabels identically and nothing downstream needs to care
// which integration an alert arrived through.
//
// Annotations are flattened unprefixed, matching parseFiringLabels exactly.
// That means an annotation and a label of the same name collide — a known wart
// on the PagerDuty path, deliberately mirrored here rather than fixed on one
// side only. Fixing it belongs in a change that moves both.
func alertmanagerPayloadToLabels(p alertmanagerPayload) map[string]string {
	labels := map[string]string{}

	// commonLabels covers the whole group; fall back to the first alert's own
	// labels when Alertmanager grouped nothing in common.
	src := p.CommonLabels
	if len(src) == 0 && len(p.Alerts) > 0 {
		src = p.Alerts[0].Labels
	}
	for k, v := range src {
		if v != "" {
			labels[k] = v
		}
	}

	ann := p.CommonAnnotations
	if len(ann) == 0 && len(p.Alerts) > 0 {
		ann = p.Alerts[0].Annotations
	}
	for k, v := range ann {
		if v != "" {
			labels[k] = v
		}
	}

	if len(p.Alerts) > 0 {
		first := p.Alerts[0]
		// PagerDuty derives source_url from the "Source:" line of the rendered
		// firing text; generatorURL is the same thing pre-rendering.
		if first.GeneratorURL != "" {
			labels["source_url"] = first.GeneratorURL
		}
		// Alertmanager's own fingerprint — a stable per-series dedup key. The
		// PagerDuty path has no equivalent.
		if first.Fingerprint != "" {
			labels["nb_alert_fingerprint"] = first.Fingerprint
		}
		labels["nb_alert_firing_count"] = strconv.Itoa(len(p.Alerts))
	}

	return labels
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
