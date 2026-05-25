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

type ChronosphereAlertRuleSource struct{}

func (s *ChronosphereAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	chronoUrl, bearerToken, err := getChronosphereConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Chronosphere configs: %w", err)
	}

	monitorBody := buildChronosphereMonitor(config)
	url := fmt.Sprintf("%s/api/v1/config/monitors", strings.TrimRight(chronoUrl, "/"))
	headers := chronosphereHeaders(bearerToken)

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(monitorBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create Chronosphere monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Chronosphere response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("chronosphere API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Monitor struct {
			Slug string `json:"slug"`
		} `json:"monitor"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Chronosphere response: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: result.Monitor.Slug,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *ChronosphereAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	chronoUrl, bearerToken, err := getChronosphereConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Chronosphere configs: %w", err)
	}

	monitorBody := buildChronosphereMonitor(config)
	url := fmt.Sprintf("%s/api/v1/config/monitors/%s", strings.TrimRight(chronoUrl, "/"), externalRuleId)
	headers := chronosphereHeaders(bearerToken)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(monitorBody))
	if err != nil {
		return nil, fmt.Errorf("failed to update Chronosphere monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chronosphere API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *ChronosphereAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	chronoUrl, bearerToken, err := getChronosphereConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Chronosphere configs: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/config/monitors/%s", strings.TrimRight(chronoUrl, "/"), externalRuleId)
	headers := chronosphereHeaders(bearerToken)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Chronosphere monitor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chronosphere API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func chronosphereHeaders(bearerToken string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + bearerToken,
	}
}

func buildChronosphereMonitor(config AlertRuleConfig) map[string]interface{} {
	forDuration := "5m"
	if config.Duration != "" {
		forDuration = config.Duration
	}

	severity := "warn"
	switch config.Severity {
	case "critical":
		severity = "critical"
	case "warning":
		severity = "warn"
	case "info":
		severity = "info"
	}

	slug := sanitizeSlug(config.Name)

	monitor := map[string]interface{}{
		"monitor": map[string]interface{}{
			"name": config.Name,
			"slug": slug,
			"series_conditions": map[string]interface{}{
				"conditions": []map[string]interface{}{
					{
						"severity": severity,
						"value":    0,
						"op":       "GREATER_THAN",
					},
				},
			},
			"query": map[string]interface{}{
				"prometheus_expr": config.Query,
			},
			"interval":    forDuration,
			"annotations": config.Annotations,
			"labels":      config.Labels,
		},
	}

	if config.ProviderConfig != nil {
		monitorInner, _ := monitor["monitor"].(map[string]interface{})
		if monitorInner != nil {
			if sc, ok := monitorInner["series_conditions"].(map[string]interface{}); ok {
				if conds, ok := sc["conditions"].([]map[string]interface{}); ok && len(conds) > 0 {
					if t, ok := config.ProviderConfig["threshold"]; ok {
						if v, err := toFloat64(t); err == nil {
							conds[0]["value"] = v
						}
					}
					if op, ok := config.ProviderConfig["condition_operator"].(string); ok && op != "" {
						conds[0]["op"] = op
					}
				}
			}
			if s, ok := config.ProviderConfig["slug"].(string); ok && s != "" {
				monitorInner["slug"] = s
			}
		}
	}

	return monitor
}

func sanitizeSlug(name string) string {
	r := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-")
	slug := strings.ToLower(r.Replace(name))
	// Remove consecutive hyphens
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}
