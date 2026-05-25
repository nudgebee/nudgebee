package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// tenant_attrs keys per source
const (
	TenantAttrPagerDutyIncidentsKey = "PAGERDUTY_INCIDENT_TITLE_SERVICE_MAPPING"
	TenantAttrZendutyIncidentsKey   = "ZENDUTY_INCIDENT_TITLE_SERVICE_MAPPING"
)

// Request/response types for sync actions.

type WebhookSubjectMappingsSyncRequest struct {
	Source    string `json:"source" mapstructure:"source" validate:"required"`
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Days      int    `json:"days,omitempty" mapstructure:"days"`
}

type WebhookSubjectMappingsSyncResponse struct {
	Status      string `json:"status"`
	SyncedCount int    `json:"synced_count"`
}

// SyncWebhookSubjectMappings dispatches to the correct source-specific sync function.
func SyncWebhookSubjectMappings(sc *security.RequestContext, request WebhookSubjectMappingsSyncRequest) (*WebhookSubjectMappingsSyncResponse, error) {
	switch strings.ToLower(request.Source) {
	case "datadog":
		return SyncWebhookSubjectMappingsFromDatadog(sc, request)
	case "pagerduty":
		return SyncWebhookSubjectMappingsFromPagerDuty(sc, request)
	case "zenduty":
		return SyncWebhookSubjectMappingsFromZenduty(sc, request)
	default:
		return nil, fmt.Errorf("unsupported source: %s (supported: datadog, pagerduty, zenduty)", request.Source)
	}
}

// --- Generic merge helper ---

func mergeAndSaveMappings(sc *security.RequestContext, tenantId, attrKey string, fetched []incidentMapping) (*WebhookSubjectMappingsSyncResponse, error) {
	if len(fetched) == 0 {
		return &WebhookSubjectMappingsSyncResponse{Status: "success", SyncedCount: 0}, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Use transaction with row-level lock to prevent lost updates from concurrent writes
	tx, err := dbms.Db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingJSON string
	// FOR UPDATE locks the row until commit, preventing concurrent read-modify-write races
	_ = tx.Get(&existingJSON,
		`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = $2 FOR UPDATE LIMIT 1`,
		tenantId, attrKey)

	var existing []HistoricalIncident
	if existingJSON != "" {
		if err := common.UnmarshalJson([]byte(existingJSON), &existing); err != nil {
			return nil, fmt.Errorf("failed to unmarshal existing mappings (corrupt data?): %w", err)
		}
	}

	existingMap := make(map[string]int, len(existing))
	for i, inc := range existing {
		existingMap[strings.ToLower(inc.Title)] = i
	}

	newCount := 0
	for _, f := range fetched {
		key := strings.ToLower(f.Title)
		if idx, ok := existingMap[key]; ok {
			existing[idx].Service = &f.Service
		} else {
			svc := f.Service
			existing = append(existing, HistoricalIncident{Title: f.Title, Service: &svc})
			existingMap[key] = len(existing) - 1
			newCount++
		}
	}

	mergedJSON, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged mappings: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO tenant_attrs (tenant_id, name, value) VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, name) DO UPDATE SET value = $3, updated_at = now()`,
		tenantId, attrKey, string(mergedJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to save merged mappings to tenant_attrs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	invalidateSubjectMappingsCache(tenantId, attrKey)

	return &WebhookSubjectMappingsSyncResponse{
		Status:      "success",
		SyncedCount: newCount,
	}, nil
}

type incidentMapping struct {
	Title   string
	Service string
}

// ===================== Datadog Sync =====================

// SyncWebhookSubjectMappingsFromDatadog fetches resolved incidents from Datadog,
// resolves each incident's account via ApplyAccountMapping, fetches workloads for
// that account, and uses CallChatCompletionAPI to match title → service.
func SyncWebhookSubjectMappingsFromDatadog(sc *security.RequestContext, request WebhookSubjectMappingsSyncRequest) (*WebhookSubjectMappingsSyncResponse, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()

	apiKey, appKey, site, err := GetDatadogConfigs(sc, request.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog configs: %w", err)
	}

	days := request.Days
	if days <= 0 {
		days = 90
	}

	// Get webhook integration settings for account mapping
	webhookSettings, err := getWebhookIntegrationSettings(sc, request.AccountId, IntegrationDatadogWebhook)
	if err != nil {
		sc.GetLogger().Warn("datadog sync: failed to get webhook settings for account mapping, using default account", "error", err)
	}
	accountMapping := core.ParseAccountMapping(webhookSettings, sc.GetLogger())

	// Fetch resolved incidents with all fields (including env)
	incidents, err := fetchDatadogResolvedIncidentsWithFields(sc, apiKey, appKey, site, days)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch datadog incidents: %w", err)
	}

	sc.GetLogger().Info("datadog sync: fetched resolved incidents", "count", len(incidents))

	if len(incidents) == 0 {
		return &WebhookSubjectMappingsSyncResponse{Status: "success", SyncedCount: 0}, nil
	}

	allMappings := resolveIncidentsWithLLM(sc, incidents, request.AccountId, IntegrationDatadogWebhook, accountMapping)

	return mergeAndSaveMappings(sc, tenantId, TenantAttrHistoricalIncidentsKey, allMappings)
}

// incidentWithFields holds a fetched incident with extracted labels.
// Service is set if deterministic parsing found a match, empty otherwise.
type incidentWithFields struct {
	Title   string
	Service string            // deterministic match, empty if not found
	Labels  map[string]string // all extracted labels (env, namespace, etc.)
}

// resolveIncidentsWithLLM takes fetched incidents (some with Service already set, some without),
// applies account mapping, fetches workloads, and calls LLM for unresolved incidents.
// Returns flat incidentMappings ready to save.
func resolveIncidentsWithLLM(
	sc *security.RequestContext,
	incidents []incidentWithFields,
	accountId string,
	integrationType string,
	accountMapping *core.AccountMapping,
) []incidentMapping {
	tenantId := sc.GetSecurityContext().GetTenantId()
	llmEnabled := tenant.IsFeatureEnabled(sc, tenantId, tenant.FEATURE_WEBHOOK_LLM_RESOLUTION)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("sync: failed to get database manager", "error", err)
		// Return only deterministic matches
		var mappings []incidentMapping
		for _, inc := range incidents {
			if inc.Service != "" {
				mappings = append(mappings, incidentMapping{Title: inc.Title, Service: inc.Service})
			}
		}
		return mappings
	}

	workloadCache := make(map[string][]string)
	var allMappings []incidentMapping

	for _, inc := range incidents {
		if inc.Service != "" {
			allMappings = append(allMappings, incidentMapping{Title: inc.Title, Service: inc.Service})
			continue
		}

		if !llmEnabled {
			continue
		}

		resolvedAccountId := core.ApplyAccountMapping(accountId, inc.Labels, accountMapping)

		candidateAccountIds, lookupErr := core.GetLinkedCloudAccountIds(sc, resolvedAccountId, integrationType)
		if lookupErr != nil {
			sc.GetLogger().Warn("sync: failed to expand linked accounts", "error", lookupErr)
		}
		if len(candidateAccountIds) == 0 {
			candidateAccountIds = []string{resolvedAccountId}
		}

		cacheKey := strings.Join(candidateAccountIds, ",")

		names, ok := workloadCache[cacheKey]
		if !ok {
			err = dbms.Db.Select(&names, `
				SELECT DISTINCT name
				FROM k8s_workloads
				WHERE tenant_id = $1
				  AND cloud_account_id = ANY($2)
				  AND is_active = true
				  AND kind NOT IN ('Job', 'CronJob')
			`, tenantId, pq.Array(candidateAccountIds))
			if err != nil {
				sc.GetLogger().Error("sync: failed to fetch workload names", "error", err)
				continue
			}
			workloadCache[cacheKey] = names
		}

		if len(names) == 0 {
			continue
		}

		serviceName := CallChatCompletionAPI(sc, resolvedAccountId, inc.Title, strings.Join(names, ","))
		result := "not_found"
		if serviceName != "" {
			allMappings = append(allMappings, incidentMapping{Title: inc.Title, Service: serviceName})
			result = "matched"
		}
		common.MetricsSubjectResolution(sc.GetContext(), integrationType, "sync", result, tenantId)

		// Throttle LLM calls to avoid rate limiting (429)
		time.Sleep(5 * time.Second)
	}

	return allMappings
}

// fetchDatadogResolvedIncidentsWithFields fetches resolved incidents from Datadog
// and extracts all field values as labels.
func fetchDatadogResolvedIncidentsWithFields(sc *security.RequestContext, apiKey, appKey, site string, days int) ([]incidentWithFields, error) {
	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	var allIncidents []incidentWithFields
	pageSize := 100
	// Datadog Incidents API v2 uses cursor-based pagination via next URL
	nextURL := fmt.Sprintf("https://%s/api/v2/incidents?filter[status]=resolved&page[size]=%d",
		site, pageSize)
	totalFetched := 0

	for nextURL != "" {
		resp, err := common.HttpGet(nextURL, common.HttpWithHeaders(headers), common.HttpWithTimeout(60*time.Second))
		if err != nil {
			return nil, fmt.Errorf("failed to fetch datadog incidents: %w", err)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		var result map[string]any
		if err := common.UnmarshalJson(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal incidents response: %w", err)
		}

		data, ok := result["data"].([]any)
		if !ok || len(data) == 0 {
			break
		}

		for _, item := range data {
			incident, ok := item.(map[string]any)
			if !ok {
				continue
			}
			attrs, ok := incident["attributes"].(map[string]any)
			if !ok {
				continue
			}

			title, _ := attrs["title"].(string)
			if title == "" {
				continue
			}

			// Extract all fields as labels (same logic as ProcessEventWebook lines 1191-1237)
			labels := make(map[string]string)
			serviceName := ""

			if fields, ok := attrs["fields"].(map[string]any); ok {
				for fieldName, fieldData := range fields {
					fieldMap, ok := fieldData.(map[string]any)
					if !ok {
						continue
					}
					value := fieldMap["value"]
					if value == nil {
						continue
					}
					normalizedName := strings.ToLower(strings.ReplaceAll(fieldName, " ", "_"))

					switch v := value.(type) {
					case string:
						labels[normalizedName] = v
						if normalizedName == "services" {
							serviceName = v
						}
					case []any:
						parts := make([]string, 0, len(v))
						for _, s := range v {
							if str, ok := s.(string); ok {
								parts = append(parts, str)
							}
						}
						if len(parts) > 0 {
							labels[normalizedName] = parts[0]
							if normalizedName == "services" {
								serviceName = strings.Join(parts, ", ")
							}
						}
					}
				}
			}

			allIncidents = append(allIncidents, incidentWithFields{
				Title:   title,
				Service: serviceName,
				Labels:  labels,
			})
		}

		totalFetched += len(data)

		// Get next page URL from response
		nextURL = ""
		if links, ok := result["links"].(map[string]any); ok {
			if next, ok := links["next"].(string); ok && next != "" {
				nextURL = next
			}
		}

		// Safety limit
		if totalFetched > 5000 {
			break
		}
	}

	return allIncidents, nil
}

// getWebhookIntegrationSettings fetches the integration config values for a webhook integration.
func getWebhookIntegrationSettings(sc *security.RequestContext, accountId, integrationType string) ([]core.IntegrationConfigValue, error) {
	integrations, err := core.ListIntegrationConfigs(sc, accountId, integrationType)
	if err != nil {
		return nil, err
	}
	if len(integrations) == 0 {
		return nil, fmt.Errorf("no %s integration found for account %s", integrationType, accountId)
	}
	return integrations[0].Configs, nil
}

// ===================== PagerDuty Sync =====================

func SyncWebhookSubjectMappingsFromPagerDuty(sc *security.RequestContext, request WebhookSubjectMappingsSyncRequest) (*WebhookSubjectMappingsSyncResponse, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()

	apiToken, err := getPagerDutyAPIToken(sc, tenantId)
	if err != nil {
		return nil, fmt.Errorf("failed to get pagerduty config: %w", err)
	}

	days := request.Days
	if days <= 0 {
		days = 90
	}
	if days > 180 {
		days = 180
	}

	webhookSettings, err := getWebhookIntegrationSettings(sc, request.AccountId, "pagerduty_webhook")
	if err != nil {
		sc.GetLogger().Warn("pagerduty sync: failed to get webhook settings for account mapping", "error", err)
	}
	accountMapping := core.ParseAccountMapping(webhookSettings, sc.GetLogger())

	// Run async — return immediately, sync in background
	bgSc := detachedContext(sc)
	go func() {
		sc := bgSc // use detached context in goroutine
		incidents, err := fetchPagerDutyResolvedIncidents(sc, apiToken, days)
		if err != nil {
			sc.GetLogger().Error("pagerduty sync: failed to fetch incidents", "error", err)
			return
		}

		allMappings := resolveIncidentsWithLLM(sc, incidents, request.AccountId, "pagerduty_webhook", accountMapping)

		resp, err := mergeAndSaveMappings(sc, tenantId, TenantAttrPagerDutyIncidentsKey, allMappings)
		if err != nil {
			sc.GetLogger().Error("pagerduty sync: failed to save mappings", "error", err)
			return
		}
		sc.GetLogger().Info("pagerduty sync: completed", "synced_count", resp.SyncedCount)
	}()

	return &WebhookSubjectMappingsSyncResponse{Status: "started"}, nil
}

// getPagerDutyAPIToken extracts the API token from the PagerDuty integration config.
func getPagerDutyAPIToken(sc *security.RequestContext, tenantId string) (string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	var encryptedPassword string
	err = dbms.Db.Get(&encryptedPassword, `
		SELECT COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') as password
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.status = 'enabled' AND i.type = 'pagerduty' AND i.tenant_id = $1
		GROUP BY i.id
		ORDER BY i.updated_at DESC
		LIMIT 1
	`, tenantId)
	if err != nil {
		return "", fmt.Errorf("no enabled pagerduty integration found: %w", err)
	}
	if encryptedPassword == "" {
		return "", fmt.Errorf("pagerduty API token is empty")
	}

	password, err := common.Decrypt(encryptedPassword)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt pagerduty token: %w", err)
	}
	return password, nil
}

// fetchPagerDutyResolvedIncidents lists resolved incidents, then fetches each one
// individually to extract the deployment/service label from body.details.
// Uses 10 concurrent workers to avoid blocking.
func fetchPagerDutyResolvedIncidents(sc *security.RequestContext, apiToken string, days int) ([]incidentWithFields, error) {
	since := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	until := time.Now().UTC().Format(time.RFC3339)

	pdHeaders := map[string]string{
		"Authorization": "Token token=" + apiToken,
		"Accept":        "application/vnd.pagerduty+json;version=2",
		"Content-Type":  "application/json",
	}

	// Step 1: List resolved incident IDs and titles
	type incidentRef struct {
		ID    string
		Title string
	}
	var incidents []incidentRef
	limit := 100
	offset := 0

	for {
		reqURL := fmt.Sprintf(
			"https://api.pagerduty.com/incidents?statuses[]=resolved&since=%s&until=%s&limit=%d&offset=%d",
			since, until, limit, offset)

		resp, err := common.HttpGet(reqURL, common.HttpWithHeaders(pdHeaders), common.HttpWithTimeout(60*time.Second))
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pagerduty incidents: %w", err)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("pagerduty API returned status %d: %s", resp.StatusCode, string(bodyBytes))
		}

		var result struct {
			Incidents []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"incidents"`
			More bool `json:"more"`
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pagerduty response: %w", err)
		}

		for _, inc := range result.Incidents {
			if inc.ID != "" && inc.Title != "" {
				incidents = append(incidents, incidentRef{ID: inc.ID, Title: inc.Title})
			}
		}

		if !result.More {
			break
		}
		offset += limit
		if offset > 5000 {
			break
		}
	}

	sc.GetLogger().Info("pagerduty sync: listed resolved incidents", "count", len(incidents))

	// Step 2: Fetch incident details concurrently (10 workers)
	subjectLabelKeys := []string{"deployment", "statefulset", "daemonset", "app_id", "service_name", "pod"}

	concurrency := 10
	sem := make(chan struct{}, concurrency)
	resultsCh := make(chan incidentWithFields, len(incidents))
	var wg sync.WaitGroup

	for _, ref := range incidents {
		wg.Add(1)
		go func(r incidentRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			incident, err := GetPagerDutyIncident(apiToken, r.ID)
			if err != nil {
				sc.GetLogger().Warn("pagerduty sync: failed to fetch incident detail, skipping",
					"incident_id", r.ID, "error", err)
				return
			}

			bodyDetails := incident.Body.GetBodyDetails()
			if bodyDetails == nil {
				return
			}

			firingText := findFiringText(incident, bodyDetails)
			if firingText == "" || !strings.Contains(firingText, "Labels:") {
				return
			}

			firingLabels := parseFiringLabels(firingText)

			subjectName := ""
			for _, key := range subjectLabelKeys {
				if val, ok := firingLabels[key]; ok && val != "" {
					subjectName = val
					break
				}
			}

			resultsCh <- incidentWithFields{
				Title:   r.Title,
				Service: subjectName, // empty if not found — LLM will try
				Labels:  firingLabels,
			}
		}(ref)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var allIncidents []incidentWithFields
	for r := range resultsCh {
		allIncidents = append(allIncidents, r)
	}

	return allIncidents, nil
}

// ===================== Zenduty Sync =====================

func SyncWebhookSubjectMappingsFromZenduty(sc *security.RequestContext, request WebhookSubjectMappingsSyncRequest) (*WebhookSubjectMappingsSyncResponse, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()

	apiKey, err := getZendutyAPIKey(sc, tenantId)
	if err != nil {
		return nil, fmt.Errorf("failed to get zenduty config: %w", err)
	}

	days := request.Days
	if days <= 0 {
		days = 90
	}

	webhookSettings, err := getWebhookIntegrationSettings(sc, request.AccountId, IntegrationZendutyWebhook)
	if err != nil {
		sc.GetLogger().Warn("zenduty sync: failed to get webhook settings for account mapping", "error", err)
	}
	accountMapping := core.ParseAccountMapping(webhookSettings, sc.GetLogger())

	// Run async
	bgSc := detachedContext(sc)
	go func() {
		sc := bgSc // use detached context in goroutine
		incidents, err := fetchZendutyResolvedIncidents(sc, apiKey, days)
		if err != nil {
			sc.GetLogger().Error("zenduty sync: failed to fetch incidents", "error", err)
			return
		}

		allMappings := resolveIncidentsWithLLM(sc, incidents, request.AccountId, IntegrationZendutyWebhook, accountMapping)

		resp, err := mergeAndSaveMappings(sc, tenantId, TenantAttrZendutyIncidentsKey, allMappings)
		if err != nil {
			sc.GetLogger().Error("zenduty sync: failed to save mappings", "error", err)
			return
		}
		sc.GetLogger().Info("zenduty sync: completed", "synced_count", resp.SyncedCount)
	}()

	return &WebhookSubjectMappingsSyncResponse{Status: "started"}, nil
}

// getZendutyAPIKey extracts the API key from the Zenduty integration config.
func getZendutyAPIKey(sc *security.RequestContext, tenantId string) (string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	var encryptedPassword string
	err = dbms.Db.Get(&encryptedPassword, `
		SELECT COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') as password
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.status = 'enabled' AND i.type = 'zenduty' AND i.tenant_id = $1
		GROUP BY i.id
		ORDER BY i.updated_at DESC
		LIMIT 1
	`, tenantId)
	if err != nil {
		return "", fmt.Errorf("no enabled zenduty integration found: %w", err)
	}
	if encryptedPassword == "" {
		return "", fmt.Errorf("zenduty API key is empty")
	}

	apiKey, err := common.Decrypt(encryptedPassword)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt zenduty api key: %w", err)
	}
	return apiKey, nil
}

// fetchZendutyResolvedIncidents calls the Zenduty API to list resolved incidents
// and extracts the workload/service name from the summary field.
func fetchZendutyResolvedIncidents(sc *security.RequestContext, apiKey string, days int) ([]incidentWithFields, error) {
	createdAfter := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)

	zdHeaders := map[string]string{
		"Authorization": "Token " + apiKey,
		"Content-Type":  "application/json",
	}

	var allIncidents []incidentWithFields
	pageNum := 1

	for {
		reqURL := fmt.Sprintf(
			"%s/incidents?status=%d&creation_date__gte=%s&page=%d",
			ZenDutyDefaultURL, ZenDutyStatusResolved, createdAfter, pageNum)
		sc.GetLogger().Info("zenduty sync: fetching incidents", "url", reqURL)

		resp, err := common.HttpGet(reqURL, common.HttpWithHeaders(zdHeaders), common.HttpWithTimeout(60*time.Second))
		if err != nil {
			return nil, fmt.Errorf("failed to fetch zenduty incidents: %w", err)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		sc.GetLogger().Info("zenduty sync: API response", "status", resp.StatusCode, "body_length", len(bodyBytes))

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("zenduty API returned status %d: %s", resp.StatusCode, string(bodyBytes))
		}

		var result struct {
			Results []struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
			} `json:"results"`
			Next *string `json:"next"`
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal zenduty response: %w", err)
		}

		for _, inc := range result.Results {
			if inc.Title == "" {
				continue
			}
			subjectName := extractSubjectFromZendutySummary(inc.Summary)
			// Also extract labels from summary for account mapping
			labels := extractLabelsFromZendutySummary(inc.Summary)

			allIncidents = append(allIncidents, incidentWithFields{
				Title:   inc.Title,
				Service: subjectName, // empty if not found — LLM will try
				Labels:  labels,
			})
		}

		if result.Next == nil {
			break
		}
		pageNum++
		if pageNum > 50 {
			break
		}
	}

	return allIncidents, nil
}

// extractSubjectFromZendutySummary tries to extract a workload/service name from
// the Zenduty incident summary field.
// extractLabelsFromZendutySummary extracts all key-value pairs from the summary
// for use in account mapping (env, namespace, cluster, etc.).
func extractLabelsFromZendutySummary(summary string) map[string]string {
	labels := make(map[string]string)
	if summary == "" {
		return labels
	}

	// Extract markdown fields: **Key**: value
	mdKeys := []string{"Subject Name", "Subject Namespace", "Priority", "Aggregation Key", "Subject Type"}
	for _, key := range mdKeys {
		if val := extractMarkdownField(summary, key); val != "" {
			normalizedKey := strings.ToLower(strings.ReplaceAll(key, " ", "_"))
			labels[normalizedKey] = val
		}
	}

	// Extract Alertmanager-style labels: - key = value
	lines := strings.Split(summary, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			labelLine := strings.TrimPrefix(line, "- ")
			parts := strings.SplitN(labelLine, " = ", 2)
			if len(parts) == 2 {
				labels[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	return labels
}

func extractSubjectFromZendutySummary(summary string) string {
	if summary == "" {
		return ""
	}

	subjectKeys := []string{"Subject Name", "deployment", "statefulset", "daemonset", "app_id", "service_name", "pod"}

	for _, key := range subjectKeys {
		if val := extractMarkdownField(summary, key); val != "" {
			return val
		}
		if val := extractLabelValue(summary, key); val != "" {
			return val
		}
	}

	return ""
}

func extractMarkdownField(text, key string) string {
	patterns := []string{
		"**" + key + "**: ",
		"**" + key + "** : ",
		"**" + key + "**:",
	}
	for _, pattern := range patterns {
		idx := strings.Index(text, pattern)
		if idx == -1 {
			continue
		}
		start := idx + len(pattern)
		end := strings.IndexAny(text[start:], "\n\r")
		if end == -1 {
			end = len(text[start:])
		}
		val := strings.TrimSpace(text[start : start+end])
		if val != "" {
			return val
		}
	}
	return ""
}

func extractLabelValue(text, key string) string {
	patterns := []string{
		"- " + key + " = ",
		key + " = ",
		key + "=",
	}
	lowerText := strings.ToLower(text)

	for _, pattern := range patterns {
		lowerPattern := strings.ToLower(pattern)
		idx := strings.Index(lowerText, lowerPattern)
		if idx == -1 {
			continue
		}
		start := idx + len(pattern)
		end := strings.IndexAny(text[start:], "\n\r,;")
		if end == -1 {
			end = len(text[start:])
		}
		val := strings.TrimSpace(text[start : start+end])
		if val != "" {
			return val
		}
	}
	return ""
}

// detachedContext creates a new RequestContext with context.Background() to avoid
// cancellation when the parent HTTP request context ends. Used by background goroutines.
func detachedContext(sc *security.RequestContext) *security.RequestContext {
	return security.NewRequestContext(
		context.Background(),
		sc.GetSecurityContext(),
		sc.GetLogger(),
		sc.GetTracer(),
		sc.GetMeter(),
	)
}

// --- Auto-learn: save confirmed title → service mapping back to tenant_attrs ---

func LearnSubjectMapping(sc *security.RequestContext, tenantId, attrKey, title, serviceName string) {
	if title == "" || serviceName == "" {
		return
	}
	bgSc := detachedContext(sc)

	go func() {
		sc := bgSc // use detached context in goroutine
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			sc.GetLogger().Error("webhook: failed to get database manager for auto-learn", "error", err)
			return
		}

		tx, err := dbms.Db.Beginx()
		if err != nil {
			sc.GetLogger().Error("webhook: failed to begin transaction for auto-learn", "error", err)
			return
		}
		defer func() { _ = tx.Rollback() }()

		var existingJSON string
		_ = tx.Get(&existingJSON,
			`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = $2 FOR UPDATE LIMIT 1`,
			tenantId, attrKey)

		var existing []HistoricalIncident
		if existingJSON != "" {
			if err := common.UnmarshalJson([]byte(existingJSON), &existing); err != nil {
				sc.GetLogger().Error("webhook: failed to unmarshal existing mappings for auto-learn", "error", err)
				return
			}
		}

		titleLower := strings.ToLower(title)
		for i, inc := range existing {
			if strings.ToLower(inc.Title) == titleLower {
				if inc.Service != nil && *inc.Service == serviceName {
					return
				}
				existing[i].Service = &serviceName
				goto save
			}
		}
		existing = append(existing, HistoricalIncident{Title: title, Service: &serviceName})

	save:
		mergedJSON, err := json.Marshal(existing)
		if err != nil {
			sc.GetLogger().Error("webhook: failed to marshal auto-learned mapping", "error", err)
			return
		}

		_, err = tx.Exec(
			`INSERT INTO tenant_attrs (tenant_id, name, value) VALUES ($1, $2, $3)
			 ON CONFLICT (tenant_id, name) DO UPDATE SET value = $3, updated_at = now()`,
			tenantId, attrKey, string(mergedJSON))
		if err != nil {
			sc.GetLogger().Error("webhook: failed to save auto-learned mapping", "error", err)
			return
		}

		if err := tx.Commit(); err != nil {
			sc.GetLogger().Error("webhook: failed to commit auto-learn transaction", "error", err)
			return
		}

		invalidateSubjectMappingsCache(tenantId, attrKey)
		sc.GetLogger().Info("webhook: auto-learned subject mapping", "title", title, "service", serviceName, "source", attrKey)
	}()
}

// --- Tenant-scoped TTL cache for historical incident mappings ---

type subjectMappingsCache struct {
	mu      sync.RWMutex
	entries map[string]*subjectMappingsCacheEntry
}

type subjectMappingsCacheEntry struct {
	mappings []HistoricalIncident
	expiry   time.Time
}

var mappingsCache = &subjectMappingsCache{
	entries: make(map[string]*subjectMappingsCacheEntry),
}

const mappingsCacheTTL = 30 * time.Minute

func invalidateSubjectMappingsCache(tenantId, attrKey string) {
	mappingsCache.mu.Lock()
	defer mappingsCache.mu.Unlock()
	delete(mappingsCache.entries, tenantId+":"+attrKey)
}

// GetSubjectMappingsForPrompt returns historical incident mappings for a tenant
// from the tenant_attrs JSON blob for the given source key.
func GetSubjectMappingsForPrompt(sc *security.RequestContext, tenantId, attrKey string, limit int) ([]HistoricalIncident, error) {
	if limit <= 0 {
		limit = 1000
	}

	cacheKey := tenantId + ":" + attrKey

	mappingsCache.mu.RLock()
	entry, ok := mappingsCache.entries[cacheKey]
	mappingsCache.mu.RUnlock()

	if ok && time.Now().Before(entry.expiry) {
		if len(entry.mappings) > limit {
			return entry.mappings[:limit], nil
		}
		return entry.mappings, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	var jsonValue string
	err = dbms.Db.Get(&jsonValue,
		`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = $2 LIMIT 1`,
		tenantId, attrKey)
	if err != nil {
		sc.GetLogger().Warn("webhook: no historical incidents in tenant_attrs", "key", attrKey, "error", err)
		return nil, nil
	}

	var incidents []HistoricalIncident
	if err := common.UnmarshalJson([]byte(jsonValue), &incidents); err != nil {
		return nil, fmt.Errorf("failed to unmarshal historical incidents: %w", err)
	}

	mappingsCache.mu.Lock()
	mappingsCache.entries[cacheKey] = &subjectMappingsCacheEntry{
		mappings: incidents,
		expiry:   time.Now().Add(mappingsCacheTTL),
	}
	mappingsCache.mu.Unlock()

	if len(incidents) > limit {
		return incidents[:limit], nil
	}
	return incidents, nil
}

// FormatSubjectMappingsForPrompt formats mappings as a string for LLM prompts.
func FormatSubjectMappingsForPrompt(mappings []HistoricalIncident, limit int) string {
	if limit <= 0 {
		limit = 50
	}
	if len(mappings) == 0 {
		return "(No historical data available)"
	}

	var sb strings.Builder
	count := 0
	for _, m := range mappings {
		if m.Service == nil || *m.Service == "" {
			continue
		}
		fmt.Fprintf(&sb, "  - \"%s\" → \"%s\"\n", m.Title, *m.Service)
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
