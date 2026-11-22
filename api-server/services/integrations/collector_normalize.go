package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"time"
)

// collectorNormalizedEvent is the event row the collector's polling path would store
// for an alert a cloud provider pushed by webhook, as returned by its normalize_*
// endpoints (normalize_gcp_monitoring_webhook, normalize_azure_monitor_webhook).
type collectorNormalizedEvent struct {
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	AggregationKey   string            `json:"aggregation_key"`
	SubjectName      string            `json:"subject_name"`
	SubjectType      string            `json:"subject_type"`
	SubjectNamespace string            `json:"subject_namespace"`
	ServiceKey       string            `json:"service_key"`
	Fingerprint      string            `json:"fingerprint"`
	FindingId        string            `json:"finding_id"`
	CloudResourceId  string            `json:"cloud_resource_id"`
	Priority         string            `json:"priority"`
	Status           string            `json:"status"`
	Labels           map[string]string `json:"labels"`
}

// normalizeByCollector posts a webhook delivery to one of the collector's
// normalize_* endpoints and returns the event row polling would store for it.
func normalizeByCollector(sc *security.RequestContext, endpoint, accountId, webhookPayload string) (collectorNormalizedEvent, error) {
	// The tenant-admin context is nil when its account lookup failed.
	if sc.GetSecurityContext() == nil {
		return collectorNormalizedEvent{}, fmt.Errorf("no tenant context")
	}
	collectorURL := fmt.Sprintf("%s/v1/cloud/%s?account_id=%s",
		config.Config.CloudCollectorServerUrl, endpoint, url.QueryEscape(accountId))
	httpReq, err := http.NewRequestWithContext(sc.GetContext(), http.MethodPost, collectorURL, bytes.NewReader([]byte(webhookPayload)))
	if err != nil {
		return collectorNormalizedEvent{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)
	httpReq.Header.Set("x-tenant-id", sc.GetSecurityContext().GetTenantId())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return collectorNormalizedEvent{}, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return collectorNormalizedEvent{}, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return collectorNormalizedEvent{}, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data   collectorNormalizedEvent `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return collectorNormalizedEvent{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return collectorNormalizedEvent{}, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}
	if apiResponse.Data.FindingId == "" {
		return collectorNormalizedEvent{}, fmt.Errorf("collector-server returned no finding_id")
	}
	return apiResponse.Data, nil
}
