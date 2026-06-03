package alertrule

import (
	"encoding/json"
	"fmt"

	"nudgebee/services/relay"
	"nudgebee/services/security"
)

type LokiAlertRuleSource struct{}

func (s *LokiAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	ruleYaml := buildLokiAlertRule(config)

	lokiRequest := relay.ActionExecuteBody{
		AccountID:  config.AccountId,
		ActionName: "create_loki_alert_rule",
		ActionParams: map[string]any{
			"namespace": "nudgebee",
			"rule_name": config.Name,
			"rule_yaml": ruleYaml,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Loki alert rule via relay: %w", err)
	}

	ruleId := config.Name
	if data, ok := resp["data"]; ok {
		if dataMap, ok := data.(map[string]interface{}); ok {
			if id, ok := dataMap["rule_id"].(string); ok && id != "" {
				ruleId = id
			}
		}
	}

	return &AlertRuleResult{
		ExternalRuleId: ruleId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *LokiAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	// Loki ruler API uses PUT with the same payload as create
	ruleYaml := buildLokiAlertRule(config)

	lokiRequest := relay.ActionExecuteBody{
		AccountID:  config.AccountId,
		ActionName: "update_loki_alert_rule",
		ActionParams: map[string]any{
			"namespace": "nudgebee",
			"rule_name": externalRuleId,
			"rule_yaml": ruleYaml,
		},
		NoSinks: true,
	}

	_, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update Loki alert rule via relay: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *LokiAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	lokiRequest := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "delete_loki_alert_rule",
		ActionParams: map[string]any{
			"namespace": "nudgebee",
			"rule_name": externalRuleId,
		},
		NoSinks: true,
	}

	_, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return fmt.Errorf("failed to delete Loki alert rule via relay: %w", err)
	}

	return nil
}

func buildLokiAlertRule(config AlertRuleConfig) string {
	forDuration := "5m"
	if config.Duration != "" {
		forDuration = config.Duration
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

	// Build Prometheus-compatible rule group YAML
	rule := map[string]interface{}{
		"name": "nudgebee-alerts",
		"rules": []map[string]interface{}{
			{
				"alert": config.Name,
				"expr":  config.Query,
				"for":   forDuration,
				"labels": map[string]string{
					"severity": severity,
					"source":   "nudgebee",
				},
				"annotations": map[string]string{
					"summary":     config.Name,
					"description": description,
				},
			},
		},
	}

	// Add custom labels
	if config.Labels != nil {
		labels := rule["rules"].([]map[string]interface{})[0]["labels"].(map[string]string)
		for k, v := range config.Labels {
			labels[k] = v
		}
	}

	ruleBytes, _ := json.Marshal(rule)
	return string(ruleBytes)
}
