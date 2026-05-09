package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type GrafanaAlertRuleSource struct{}

func (s *GrafanaAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	grafanaUrl, apiToken, err := getGrafanaConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana configs: %w", err)
	}

	ruleBody := buildGrafanaAlertRule(config)
	url := fmt.Sprintf("%s/api/v1/provisioning/alert-rules", strings.TrimRight(grafanaUrl, "/"))
	headers := grafanaHeaders(apiToken)

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(ruleBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Grafana response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("grafana API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Grafana response: %w", err)
	}

	ruleUID := fmt.Sprintf("%v", result["uid"])
	return &AlertRuleResult{
		ExternalRuleId: ruleUID,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *GrafanaAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	grafanaUrl, apiToken, err := getGrafanaConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana configs: %w", err)
	}

	ruleBody := buildGrafanaAlertRule(config)
	url := fmt.Sprintf("%s/api/v1/provisioning/alert-rules/%s", strings.TrimRight(grafanaUrl, "/"), externalRuleId)
	headers := grafanaHeaders(apiToken)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(ruleBody))
	if err != nil {
		return nil, fmt.Errorf("failed to update Grafana alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("grafana API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *GrafanaAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	grafanaUrl, apiToken, err := getGrafanaConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Grafana configs: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/provisioning/alert-rules/%s", strings.TrimRight(grafanaUrl, "/"), externalRuleId)
	headers := grafanaHeaders(apiToken)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Grafana alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grafana API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func grafanaHeaders(apiToken string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiToken,
	}
}

func buildGrafanaAlertRule(config AlertRuleConfig) map[string]interface{} {
	folderUID := "nudgebee-alerts"
	ruleGroup := "nudgebee"
	datasourceUID := ""

	if config.ProviderConfig != nil {
		if f, ok := config.ProviderConfig["folder_uid"].(string); ok && f != "" {
			folderUID = f
		}
		if rg, ok := config.ProviderConfig["rule_group"].(string); ok && rg != "" {
			ruleGroup = rg
		}
		if ds, ok := config.ProviderConfig["datasource_uid"].(string); ok && ds != "" {
			datasourceUID = ds
		}
	}

	threshold := 0.0
	conditionOp := "gt"
	if config.ProviderConfig != nil {
		if t, ok := config.ProviderConfig["threshold"]; ok {
			if v, err := toFloat64(t); err == nil {
				threshold = v
			}
		}
		if op, ok := config.ProviderConfig["condition_operator"].(string); ok && op != "" {
			conditionOp = op
		}
	}

	forDuration := "5m"
	if config.Duration != "" {
		forDuration = config.Duration
	}

	description := ""
	if config.Annotations != nil {
		description = config.Annotations["description"]
	}

	// Build data queries
	dataQuery := map[string]interface{}{
		"refId":         "A",
		"datasourceUid": datasourceUID,
		"model": map[string]interface{}{
			"expr":  config.Query,
			"refId": "A",
		},
		"relativeTimeRange": map[string]interface{}{
			"from": 600,
			"to":   0,
		},
	}

	conditionQuery := map[string]interface{}{
		"refId":         "B",
		"datasourceUid": "__expr__",
		"model": map[string]interface{}{
			"type":  "threshold",
			"refId": "B",
			"conditions": []map[string]interface{}{
				{
					"type": "query",
					"evaluator": map[string]interface{}{
						"type":   conditionOp,
						"params": []float64{threshold},
					},
				},
			},
			"expression": "A",
		},
		"relativeTimeRange": map[string]interface{}{
			"from": 600,
			"to":   0,
		},
	}

	rule := map[string]interface{}{
		"title":        config.Name,
		"ruleGroup":    ruleGroup,
		"folderUID":    folderUID,
		"condition":    "B",
		"data":         []interface{}{dataQuery, conditionQuery},
		"for":          forDuration,
		"noDataState":  "NoData",
		"execErrState": "Error",
		"labels":       config.Labels,
		"annotations": map[string]string{
			"description": description,
			"summary":     config.Name,
		},
		"isPaused": !config.Enabled,
		"updated":  time.Now().UTC().Format(time.RFC3339),
	}

	return rule
}
