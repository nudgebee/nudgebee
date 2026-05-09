package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type DatadogAlertRuleSource struct{}

func (s *DatadogAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiKey, appKey, site, err := getDatadogConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Datadog configs: %w", err)
	}

	monitorType := "metric alert"
	if config.AlertType == "log" {
		monitorType = "log alert"
	}

	body := map[string]interface{}{
		"name":    config.Name,
		"type":    monitorType,
		"query":   config.Query,
		"message": config.Annotations["description"],
		"tags":    labelsToDatadogTags(config.Labels),
		"options": buildDatadogMonitorOptions(config),
	}

	url := fmt.Sprintf("https://%s/api/v1/monitor", site)
	headers := datadogHeaders(apiKey, appKey)

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Datadog monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Datadog response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("datadog API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Datadog response: %w", err)
	}

	monitorId := fmt.Sprintf("%v", result["id"])
	return &AlertRuleResult{
		ExternalRuleId: monitorId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *DatadogAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiKey, appKey, site, err := getDatadogConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Datadog configs: %w", err)
	}

	monitorType := "metric alert"
	if config.AlertType == "log" {
		monitorType = "log alert"
	}

	body := map[string]interface{}{
		"name":    config.Name,
		"type":    monitorType,
		"query":   config.Query,
		"message": config.Annotations["description"],
		"tags":    labelsToDatadogTags(config.Labels),
		"options": buildDatadogMonitorOptions(config),
	}

	url := fmt.Sprintf("https://%s/api/v1/monitor/%s", site, externalRuleId)
	headers := datadogHeaders(apiKey, appKey)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return nil, fmt.Errorf("failed to update Datadog monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Datadog response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datadog API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *DatadogAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	apiKey, appKey, site, err := getDatadogConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Datadog configs: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/v1/monitor/%s", site, externalRuleId)
	headers := datadogHeaders(apiKey, appKey)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Datadog monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("datadog API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func datadogHeaders(apiKey, appKey string) map[string]string {
	return map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}
}

func labelsToDatadogTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}
	return tags
}

func buildDatadogMonitorOptions(config AlertRuleConfig) map[string]interface{} {
	options := map[string]interface{}{}

	// Extract thresholds from provider_config
	if config.ProviderConfig != nil {
		if thresholds, ok := config.ProviderConfig["thresholds"]; ok {
			options["thresholds"] = thresholds
		}
		if evalDelay, ok := config.ProviderConfig["evaluation_delay"]; ok {
			options["evaluation_delay"] = evalDelay
		}
		if notifyNoData, ok := config.ProviderConfig["notify_no_data"]; ok {
			options["notify_no_data"] = notifyNoData
		}
		if noDataTimeframe, ok := config.ProviderConfig["no_data_timeframe"]; ok {
			options["no_data_timeframe"] = noDataTimeframe
		}
		if includeTags, ok := config.ProviderConfig["include_tags"]; ok {
			options["include_tags"] = includeTags
		}
	}

	return options
}
