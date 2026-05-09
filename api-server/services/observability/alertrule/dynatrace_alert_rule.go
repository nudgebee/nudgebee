package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type DynatraceAlertRuleSource struct{}

func (s *DynatraceAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiToken, baseURL, err := getDynatraceConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	payload := buildDynatraceMetricEventPayload(config)
	url := strings.TrimRight(baseURL, "/") + "/api/v2/settings/objects"

	headers := dynatraceHeaders(apiToken)

	// Settings API expects an array of objects
	body := []map[string]interface{}{payload}
	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Dynatrace metric event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Dynatrace response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("dynatrace API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var results []struct {
		ObjectId string `json:"objectId"`
		Code     int    `json:"code"`
	}
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, fmt.Errorf("failed to parse Dynatrace response: %w", err)
	}
	if len(results) == 0 || results[0].ObjectId == "" {
		return nil, fmt.Errorf("dynatrace returned empty object ID")
	}

	return &AlertRuleResult{
		ExternalRuleId: results[0].ObjectId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *DynatraceAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiToken, baseURL, err := getDynatraceConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	payload := buildDynatraceMetricEventPayload(config)
	url := strings.TrimRight(baseURL, "/") + "/api/v2/settings/objects/" + externalRuleId

	headers := dynatraceHeaders(apiToken)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to update Dynatrace metric event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dynatrace API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *DynatraceAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	apiToken, baseURL, err := getDynatraceConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/v2/settings/objects/" + externalRuleId
	headers := dynatraceHeaders(apiToken)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Dynatrace metric event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dynatrace API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func dynatraceHeaders(apiToken string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}
}

func buildDynatraceMetricEventPayload(config AlertRuleConfig) map[string]interface{} {
	threshold := 0.0
	alertCondition := "ABOVE"
	samples := 5
	violatingSamples := 3
	dealertingSamples := 5

	if config.ProviderConfig != nil {
		if t, ok := config.ProviderConfig["threshold"]; ok {
			if v, err := toFloat64(t); err == nil {
				threshold = v
			}
		}
		if ac, ok := config.ProviderConfig["alert_condition"].(string); ok {
			alertCondition = ac
		}
		if s, ok := config.ProviderConfig["samples"]; ok {
			if v, err := toInt(s); err == nil {
				samples = v
			}
		}
		if vs, ok := config.ProviderConfig["violating_samples"]; ok {
			if v, err := toInt(vs); err == nil {
				violatingSamples = v
			}
		}
		if ds, ok := config.ProviderConfig["dealerting_samples"]; ok {
			if v, err := toInt(ds); err == nil {
				dealertingSamples = v
			}
		}
	}

	metricSelector := config.Query
	if config.ProviderConfig != nil {
		if ms, ok := config.ProviderConfig["metric_selector"].(string); ok && ms != "" {
			metricSelector = ms
		}
	}

	description := ""
	if config.Annotations != nil {
		description = config.Annotations["description"]
	}

	severity := "CUSTOM_ALERT"
	switch config.Severity {
	case "critical":
		severity = "CUSTOM_ALERT"
	case "warning":
		severity = "CUSTOM_ALERT"
	}

	value := map[string]interface{}{
		"summary": config.Name,
		"queryDefinition": map[string]interface{}{
			"type":           "METRIC_SELECTOR",
			"metricSelector": metricSelector,
		},
		"modelProperties": map[string]interface{}{
			"type":              "STATIC_THRESHOLD",
			"threshold":         threshold,
			"alertCondition":    alertCondition,
			"samples":           samples,
			"violatingSamples":  violatingSamples,
			"dealertingSamples": dealertingSamples,
		},
		"eventTemplate": map[string]interface{}{
			"title":       config.Name,
			"description": description,
		},
		"enabled":                 config.Enabled,
		"alertingScope":           []interface{}{},
		"eventEntityDimensionKey": "",
	}

	if severity != "" {
		value["eventTemplate"].(map[string]interface{})["eventType"] = severity
	}

	return map[string]interface{}{
		"schemaId":      "builtin:anomaly-detection.metric-events",
		"schemaVersion": "2.0.3",
		"scope":         "environment",
		"value":         value,
	}
}
