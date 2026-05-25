package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type SplunkAlertRuleSource struct{}

func (s *SplunkAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	splunkCfg, err := getSplunkO11yConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk configs: %w", err)
	}

	detector := buildSplunkDetector(config)
	url := fmt.Sprintf("https://api.%s.signalfx.com/v2/detector", splunkCfg.Realm)
	headers := splunkHeaders(splunkCfg.AccessToken)

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(detector))
	if err != nil {
		return nil, fmt.Errorf("failed to create Splunk detector: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Splunk response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("splunk API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Splunk response: %w", err)
	}

	detectorId := fmt.Sprintf("%v", result["id"])
	return &AlertRuleResult{
		ExternalRuleId: detectorId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *SplunkAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	splunkCfg, err := getSplunkO11yConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk configs: %w", err)
	}

	detector := buildSplunkDetector(config)
	url := fmt.Sprintf("https://api.%s.signalfx.com/v2/detector/%s", splunkCfg.Realm, externalRuleId)
	headers := splunkHeaders(splunkCfg.AccessToken)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(detector))
	if err != nil {
		return nil, fmt.Errorf("failed to update Splunk detector: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("splunk API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *SplunkAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	splunkCfg, err := getSplunkO11yConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Splunk configs: %w", err)
	}

	url := fmt.Sprintf("https://api.%s.signalfx.com/v2/detector/%s", splunkCfg.Realm, externalRuleId)
	headers := splunkHeaders(splunkCfg.AccessToken)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Splunk detector: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("splunk API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func splunkHeaders(accessToken string) map[string]string {
	return map[string]string{
		"Content-Type": "application/json",
		"X-SF-TOKEN":   accessToken,
	}
}

func buildSplunkDetector(config AlertRuleConfig) map[string]interface{} {
	programText := config.Query
	if config.ProviderConfig != nil {
		if pt, ok := config.ProviderConfig["program_text"].(string); ok && pt != "" {
			programText = pt
		}
	}

	detectLabel := "Nudgebee Alert"
	if config.ProviderConfig != nil {
		if dl, ok := config.ProviderConfig["detect_label"].(string); ok && dl != "" {
			detectLabel = dl
		}
	}

	severity := "Critical"
	switch config.Severity {
	case "critical":
		severity = "Critical"
	case "warning":
		severity = "Warning"
	case "info":
		severity = "Info"
	}
	if config.ProviderConfig != nil {
		if s, ok := config.ProviderConfig["severity"].(string); ok && s != "" {
			severity = s
		}
	}

	description := ""
	if config.Annotations != nil {
		description = config.Annotations["description"]
	}

	rules := []map[string]interface{}{
		{
			"detectLabel":   detectLabel,
			"severity":      severity,
			"disabled":      !config.Enabled,
			"description":   description,
			"notifications": []interface{}{},
		},
	}

	detector := map[string]interface{}{
		"name":        config.Name,
		"description": description,
		"programText": programText,
		"rules":       rules,
		"tags":        labelsToSplunkTags(config.Labels),
	}

	return detector
}

func labelsToSplunkTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}
	return tags
}
