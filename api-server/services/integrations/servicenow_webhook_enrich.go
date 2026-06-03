package integrations

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// ServiceNowAPICreds holds resolved credentials for calling the ServiceNow Table API.
// EncryptedPassword is the value as stored in integration_config_values; the enricher
// decrypts it just before use.
type ServiceNowAPICreds struct {
	InstanceURL       string
	Username          string
	EncryptedPassword string
}

const (
	servicenowEnrichTimeout = 5 * time.Second
	servicenowTableAPIPath  = "/api/now/table/incident"
)

// resolveServiceNowAPICreds looks up enrichment credentials from the
// "servicenow" ticketing integration in the same tenant. That integration
// stores url + username + encrypted password and is already validated against
// the Table API on save, so it is the single source of truth for SNOW API
// access. Returns ok=false when no enabled ticketing integration exists or
// any of the three fields is empty — in which case enrichment is skipped
// silently without error.
//
// Rationale: the webhook integration deliberately does NOT carry duplicate
// API credentials. Two sets of creds for the same tenant would create config
// drift and an arbitrary "which one wins" rule. Customers who want enrichment
// configure the servicenow ticketing integration once.
func resolveServiceNowAPICreds(sc *security.RequestContext) (ServiceNowAPICreds, bool) {
	tenantID := sc.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return ServiceNowAPICreds{}, false
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("servicenowwebhook: unable to get db manager for cred lookup", "error", err)
		return ServiceNowAPICreds{}, false
	}

	var sisterURL, sisterUser, sisterPass string
	err = dbms.Db.QueryRowx(`
		SELECT
			COALESCE(MAX(CASE WHEN icv.name = 'url' THEN icv.value END), '')      AS url,
			COALESCE(MAX(CASE WHEN icv.name = 'username' THEN icv.value END), '') AS username,
			COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') AS password
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.status = 'enabled' AND i.type = 'servicenow' AND i.tenant_id = $1
		GROUP BY i.id
		ORDER BY i.updated_at DESC
		LIMIT 1
	`, tenantID).Scan(&sisterURL, &sisterUser, &sisterPass)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			sc.GetLogger().Error("servicenowwebhook: failed to look up servicenow integration", "error", err)
		}
		return ServiceNowAPICreds{}, false
	}
	if sisterURL == "" || sisterUser == "" || sisterPass == "" {
		return ServiceNowAPICreds{}, false
	}
	return ServiceNowAPICreds{
		InstanceURL:       sisterURL,
		Username:          sisterUser,
		EncryptedPassword: sisterPass,
	}, true
}

// EnrichWithServiceNowIncident fetches the full incident record by sys_id from
// ServiceNow's Table API and merges the result into parsedPayload's labels,
// subject, and evidence. Failures are logged and swallowed — enrichment is
// best-effort and never blocks event ingestion.
func EnrichWithServiceNowIncident(sc *security.RequestContext, parsedPayload *core.EventIncomingWebhook, creds ServiceNowAPICreds, sysID string) {
	if sysID == "" || creds.InstanceURL == "" || creds.Username == "" || creds.EncryptedPassword == "" {
		return
	}

	password, err := common.Decrypt(creds.EncryptedPassword)
	if err != nil {
		sc.GetLogger().Error("servicenowwebhook: failed to decrypt API password, skipping enrichment", "error", err)
		return
	}

	record, err := getServiceNowIncident(sc, creds.InstanceURL, creds.Username, password, sysID)
	if err != nil {
		sc.GetLogger().Warn("servicenowwebhook: failed to fetch incident details, keeping minimal payload",
			"error", err, "sys_id", sysID)
		return
	}
	if len(record) == 0 {
		sc.GetLogger().Info("servicenowwebhook: incident details empty, keeping minimal payload", "sys_id", sysID)
		return
	}

	if parsedPayload.Investigation.Labels == nil {
		parsedPayload.Investigation.Labels = make(map[string]string)
	}
	mergeServiceNowFieldsIntoLabels(record, parsedPayload.Investigation.Labels)
	applyServiceNowSubjectFromRecord(record, parsedPayload)

	// Replace the existing minimal-payload evidence with the enriched record
	// so downstream consumers (RCA, JSONPath workflows) see the full incident.
	enrichedBytes, err := common.MarshalJson(record)
	if err != nil {
		sc.GetLogger().Error("servicenowwebhook: failed to marshal enriched record", "error", err)
		return
	}
	enrichedEvidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "ServiceNow Incident (enriched)",
			"data": json.RawMessage(enrichedBytes),
		},
		Insight: []event.EventEvidenceInsight{
			{Message: fmt.Sprintf("ServiceNow Incident %s (enriched via Table API)", incidentNumberFrom(record)), Severity: "info"},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "servicenow_incident_enriched",
			"actual_action_name":     "servicenow_incident_enriched",
			"action_title":           "ServiceNow Incident Details (enriched)",
			"conditional_expression": "",
		},
	}
	// Drop the original minimal-payload evidence to avoid confusion. Anything
	// keyed off action_name=servicenow_incident_enriched will pick up the rich
	// blob; older consumers reading by index still get a single evidence entry.
	parsedPayload.Investigation.Evidences = []event.EventEvidence{enrichedEvidence}
}

// getServiceNowIncident performs a single GET against the Table API for a specific sys_id
// using HTTP Basic auth. It uses sysparm_display_value=all so reference fields (cmdb_ci,
// caller_id, business_service, …) come back as {"value": "...", "display_value": "..."}
// and sysparm_exclude_reference_link=true to drop the noisy "link" subfield.
func getServiceNowIncident(sc *security.RequestContext, instanceURL, username, password, sysID string) (map[string]any, error) {
	base := strings.TrimRight(instanceURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	endpoint := fmt.Sprintf("%s%s/%s", base, servicenowTableAPIPath, url.PathEscape(sysID))

	creds := username + ":" + password
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(creds)),
	}
	queryParams := map[string]string{
		"sysparm_display_value":          "all",
		"sysparm_exclude_reference_link": "true",
	}

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(headers),
		common.HttpWithQueryParams(queryParams),
		common.HttpWithTimeout(servicenowEnrichTimeout),
		common.HttpWithContext(sc.GetContext()),
	)
	if err != nil {
		return nil, fmt.Errorf("snow table api request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		// Drain a bit of body for diagnostics, then bail. 401/403/404 are
		// config errors — caller should not retry.
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("snow table api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(bodySnippet)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("snow table api read body: %w", err)
	}

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("snow table api unmarshal: %w", err)
	}
	return envelope.Result, nil
}

// mergeServiceNowFieldsIntoLabels promotes every non-empty scalar field from the
// SNOW incident record into the labels map. Reference fields returned as
// {"value":..., "display_value":...} are flattened to display_value (preferred
// for human-readable labels) with the raw value preserved under "<key>_value".
// u_payload (if it's a JSON string) is also parsed and its top-level scalar
// keys are merged under a "payload." prefix so the original alarm fields are
// directly searchable.
func mergeServiceNowFieldsIntoLabels(record map[string]any, labels map[string]string) {
	for k, v := range record {
		if k == "" {
			continue
		}
		switch val := v.(type) {
		case string:
			if val != "" {
				labels[k] = val
			}
		case bool:
			labels[k] = fmt.Sprintf("%t", val)
		case float64:
			labels[k] = strconv.FormatFloat(val, 'f', -1, 64)
		case map[string]any:
			// Reference field shape: {"value":"...", "display_value":"...", "link":"..."}
			if dv, ok := val["display_value"].(string); ok && dv != "" {
				labels[k] = dv
			}
			if rv, ok := val["value"].(string); ok && rv != "" {
				labels[k+"_value"] = rv
			}
		}
	}

	// Best-effort: if u_payload is a JSON string, expand its top-level scalars.
	if rawPayload, ok := record["u_payload"]; ok {
		var payloadStr string
		switch p := rawPayload.(type) {
		case string:
			payloadStr = p
		case map[string]any:
			if dv, ok := p["display_value"].(string); ok {
				payloadStr = dv
			} else if rv, ok := p["value"].(string); ok {
				payloadStr = rv
			}
		}
		if payloadStr != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(payloadStr), &parsed); err == nil {
				for pk, pv := range parsed {
					if s, ok := pv.(string); ok && s != "" {
						labels["payload."+pk] = s
					}
				}
			}
		}
	}
}

// applyServiceNowSubjectFromRecord fills EventSubjectName/Kind from the enriched
// record when the webhook payload didn't carry enough context. cmdb_ci.display_value
// is preferred (resolves to the actual host/CI), falling back to business_service
// and u_hostname. We never overwrite an already-populated subject.
func applyServiceNowSubjectFromRecord(record map[string]any, parsedPayload *core.EventIncomingWebhook) {
	if parsedPayload.EventSubjectName != "" {
		return
	}
	candidates := []struct {
		key  string
		kind string
	}{
		{"cmdb_ci", "service"},
		{"business_service", "service"},
		{"u_hostname", "host"},
	}
	for _, c := range candidates {
		v, ok := record[c.key]
		if !ok {
			continue
		}
		var name string
		switch val := v.(type) {
		case string:
			name = val
		case map[string]any:
			if dv, ok := val["display_value"].(string); ok && dv != "" {
				name = dv
			} else if rv, ok := val["value"].(string); ok {
				name = rv
			}
		}
		if name != "" {
			parsedPayload.EventSubjectName = name
			parsedPayload.EventSubjectKind = c.kind
			return
		}
	}
}

// incidentNumberFrom extracts the incident number from the enriched record for
// use in evidence insight messages. Falls back to empty string.
func incidentNumberFrom(record map[string]any) string {
	switch v := record["number"].(type) {
	case string:
		return v
	case map[string]any:
		if dv, ok := v["display_value"].(string); ok {
			return dv
		}
		if rv, ok := v["value"].(string); ok {
			return rv
		}
	}
	return ""
}
