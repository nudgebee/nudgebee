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

type SigNozAlertRuleSource struct{}

func (s *SigNozAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	signozUrl, jwtToken, err := getSignozConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get SigNoz auth: %w", err)
	}

	ruleBody := buildSignozAlertRule(config)
	url := fmt.Sprintf("%s/api/v1/rules", strings.TrimRight(signozUrl, "/"))
	headers := signozHeaders(jwtToken)

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(ruleBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create SigNoz alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SigNoz response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("signoz API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			Id interface{} `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz response: %w", err)
	}

	ruleId := fmt.Sprintf("%v", result.Data.Id)
	return &AlertRuleResult{
		ExternalRuleId: ruleId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *SigNozAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	signozUrl, jwtToken, err := getSignozConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get SigNoz auth: %w", err)
	}

	ruleBody := buildSignozAlertRule(config)
	ruleBody["id"] = externalRuleId
	url := fmt.Sprintf("%s/api/v1/rules/%s", strings.TrimRight(signozUrl, "/"), externalRuleId)
	headers := signozHeaders(jwtToken)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(ruleBody))
	if err != nil {
		return nil, fmt.Errorf("failed to update SigNoz alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("signoz API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *SigNozAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	signozUrl, jwtToken, err := getSignozConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get SigNoz auth: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/rules/%s", strings.TrimRight(signozUrl, "/"), externalRuleId)
	headers := signozHeaders(jwtToken)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete SigNoz alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signoz API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func signozHeaders(jwtToken string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + jwtToken,
	}
}

func buildSignozAlertRule(config AlertRuleConfig) map[string]interface{} {
	threshold := 0.0
	thresholdOp := ">"
	evalWindow := "5m0s"
	frequency := "1m0s"

	if config.ProviderConfig != nil {
		if t, ok := config.ProviderConfig["threshold"]; ok {
			if v, err := toFloat64(t); err == nil {
				threshold = v
			}
		}
		if op, ok := config.ProviderConfig["threshold_operator"].(string); ok && op != "" {
			thresholdOp = op
		}
		if ew, ok := config.ProviderConfig["eval_window"].(string); ok && ew != "" {
			evalWindow = ew
		}
		if f, ok := config.ProviderConfig["frequency"].(string); ok && f != "" {
			frequency = f
		}
	}

	severity := "warning"
	switch config.Severity {
	case "critical":
		severity = "critical"
	case "warning":
		severity = "warning"
	case "info":
		severity = "info"
	}

	description := ""
	if config.Annotations != nil {
		description = config.Annotations["description"]
	}

	alertType := "METRIC_BASED_ALERT"
	if config.AlertType == "log" {
		alertType = "LOGS_BASED_ALERT"
	}

	ruleType := "threshold_rule"
	if config.ProviderConfig != nil {
		if rt, ok := config.ProviderConfig["rule_type"].(string); ok && rt != "" {
			ruleType = rt
		}
	}

	rule := map[string]interface{}{
		"alert":     config.Name,
		"alertType": alertType,
		"ruleType":  ruleType,
		"condition": map[string]interface{}{
			"compositeQuery": map[string]interface{}{
				"queryType":      "builder",
				"builderQueries": map[string]interface{}{},
			},
			"op":        thresholdOp,
			"target":    threshold,
			"matchType": "1",
		},
		"evalWindow": evalWindow,
		"frequency":  frequency,
		"severity":   severity,
		"labels":     config.Labels,
		"annotations": map[string]string{
			"description": description,
		},
		"state":    stateFromEnabled(config.Enabled),
		"disabled": !config.Enabled,
	}

	if config.Query != "" {
		rule["condition"].(map[string]interface{})["compositeQuery"].(map[string]interface{})["promql"] = config.Query
		rule["condition"].(map[string]interface{})["compositeQuery"].(map[string]interface{})["queryType"] = "promql"
	}

	return rule
}

func stateFromEnabled(enabled bool) string {
	if enabled {
		return "firing"
	}
	return "disabled"
}
