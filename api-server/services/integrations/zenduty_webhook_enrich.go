package integrations

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// The actual Alertmanager labels block (namespace/pod/deployment/...) lives
// in an S3-backed payload that's only reachable via an undocumented internal
// Zenduty endpoint. Recovering that is a separate (deferred) effort.

const zendutyEnrichCacheNamespace = "zenduty_alert_enrichment"
const zendutyEnrichCacheTTL = 30 * time.Minute
const zendutyEnrichAPITimeout = 10 * time.Second

// zendutyAPIBaseURL is the base URL used by enrichment fetches. Package-level
// var (not const) so tests can swap it for an httptest.Server URL.
var zendutyAPIBaseURL = ZenDutyDefaultURL

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
}

// enrichWithZendutyAPI fills missing fields on alert.Labels from Zenduty's
// REST API. Skipped when both fields the API helps with are already populated,
// when no Zenduty integration is configured for the tenant, or when the API
// call fails. Cached per (tenant, incidentID) for 30 minutes.
func enrichWithZendutyAPI(sc *security.RequestContext, alert *core.EventIncomingWebhookInvestigation, incidentID string) {
	if incidentID == "" || alert == nil {
		return
	}
	if alert.Labels == nil {
		alert.Labels = map[string]string{}
	}

	// Skip if both fields the API would help with are already populated
	if alert.Labels["alertname"] != "" && alert.Labels["nb_alert_source"] != "" {
		return
	}

	secCtx := sc.GetSecurityContext()
	if secCtx == nil {
		return
	}
	tenantId := secCtx.GetTenantId()
	if tenantId == "" {
		return
	}

	cacheKey := tenantId + ":" + incidentID

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

	result, err := fetchZendutyAlertsForIncident(incidentID, apiKey)
	if err != nil {
		sc.GetLogger().Warn("zendutywebhook: alerts API fetch failed, continuing with webhook data",
			"incident", incidentID, "error", err)
		return
	}

	if data, marshalErr := json.Marshal(result); marshalErr == nil {
		if setErr := common.CacheSet(zendutyEnrichCacheNamespace, cacheKey, data); setErr != nil {
			sc.GetLogger().Debug("zendutywebhook: failed to cache enrich result",
				"incident", incidentID, "error", setErr)
		}
	}

	applyZendutyEnrichResult(alert, result)
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

// fetchZendutyAlertsForIncident calls GET /api/incidents/{id}/alerts/ and
// returns enrichment-relevant fields from the first result.
func fetchZendutyAlertsForIncident(incidentID, apiKey string) (zendutyEnrichResult, error) {
	headers := map[string]string{
		"Authorization": "Token " + apiKey,
		"Content-Type":  "application/json",
	}
	url := fmt.Sprintf("%s/incidents/%s/alerts/", zendutyAPIBaseURL, incidentID)

	resp, err := common.HttpGet(url,
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(zendutyEnrichAPITimeout),
	)
	if err != nil {
		return zendutyEnrichResult{}, fmt.Errorf("zenduty alerts API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return zendutyEnrichResult{}, fmt.Errorf("read alerts response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return zendutyEnrichResult{}, fmt.Errorf("zenduty alerts API returned %d: %s", resp.StatusCode, truncate(string(bodyBytes), 300))
	}

	var payload struct {
		Results []struct {
			EntityID          string `json:"entity_id"`
			IntegrationObject struct {
				Name     string `json:"name"`
				UniqueID string `json:"unique_id"`
			} `json:"integration_object"`
		} `json:"results"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return zendutyEnrichResult{}, fmt.Errorf("unmarshal alerts response: %w", err)
	}
	if len(payload.Results) == 0 {
		return zendutyEnrichResult{}, nil
	}

	first := payload.Results[0]
	return zendutyEnrichResult{
		EntityID:            first.EntityID,
		IntegrationName:     first.IntegrationObject.Name,
		IntegrationUniqueID: first.IntegrationObject.UniqueID,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
