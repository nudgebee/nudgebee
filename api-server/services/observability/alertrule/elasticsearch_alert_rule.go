package alertrule

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type ElasticsearchAlertRuleSource struct{}

func (s *ElasticsearchAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	esCfg, err := getElasticsearchConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Elasticsearch configs: %w", err)
	}

	watchId := sanitizeWatchId(config.Name)
	watchBody := buildElasticsearchWatch(config)

	url := fmt.Sprintf("%s/_watcher/watch/%s", esCfg.Url, watchId)
	headers := esHeaders(esCfg)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(watchBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create Elasticsearch watcher: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Elasticsearch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("elasticsearch API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Elasticsearch response: %w", err)
	}

	resultId := watchId
	if id, ok := result["_id"].(string); ok {
		resultId = id
	}

	return &AlertRuleResult{
		ExternalRuleId: resultId,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *ElasticsearchAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	esCfg, err := getElasticsearchConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Elasticsearch configs: %w", err)
	}

	watchBody := buildElasticsearchWatch(config)
	url := fmt.Sprintf("%s/_watcher/watch/%s", esCfg.Url, externalRuleId)
	headers := esHeaders(esCfg)

	resp, err := common.HttpPut(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(watchBody))
	if err != nil {
		return nil, fmt.Errorf("failed to update Elasticsearch watcher: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elasticsearch API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *ElasticsearchAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	esCfg, err := getElasticsearchConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get Elasticsearch configs: %w", err)
	}

	url := fmt.Sprintf("%s/_watcher/watch/%s", esCfg.Url, externalRuleId)
	headers := esHeaders(esCfg)

	resp, err := common.HttpDelete(url, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to delete Elasticsearch watcher: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elasticsearch API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func esHeaders(cfg *elasticsearchConfig) map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if cfg.AuthType == "basic" && cfg.Username != "" && cfg.Password != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		headers["Authorization"] = "Basic " + credentials
	}
	return headers
}

func sanitizeWatchId(name string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_")
	return strings.ToLower(r.Replace(name))
}

func buildElasticsearchWatch(config AlertRuleConfig) map[string]interface{} {
	scheduleInterval := "5m"
	if config.ProviderConfig != nil {
		if si, ok := config.ProviderConfig["schedule_interval"].(string); ok && si != "" {
			scheduleInterval = si
		}
	}

	index := "*"
	if config.ProviderConfig != nil {
		if idx, ok := config.ProviderConfig["index"].(string); ok && idx != "" {
			index = idx
		}
	}

	threshold := 0.0
	if config.ProviderConfig != nil {
		if t, ok := config.ProviderConfig["threshold"]; ok {
			if v, err := toFloat64(t); err == nil {
				threshold = v
			}
		}
	}

	description := ""
	if config.Annotations != nil {
		description = config.Annotations["description"]
	}

	watch := map[string]interface{}{
		"trigger": map[string]interface{}{
			"schedule": map[string]interface{}{
				"interval": scheduleInterval,
			},
		},
		"input": map[string]interface{}{
			"search": map[string]interface{}{
				"request": map[string]interface{}{
					"indices": []string{index},
					"body": map[string]interface{}{
						"query": map[string]interface{}{
							"query_string": map[string]interface{}{
								"query": config.Query,
							},
						},
					},
				},
			},
		},
		"condition": map[string]interface{}{
			"compare": map[string]interface{}{
				"ctx.payload.hits.total": map[string]interface{}{
					"gt": threshold,
				},
			},
		},
		"actions": map[string]interface{}{
			"log_alert": map[string]interface{}{
				"logging": map[string]interface{}{
					"text": fmt.Sprintf("Nudgebee Alert: %s - %s", config.Name, description),
				},
			},
		},
		"metadata": map[string]interface{}{
			"name":   config.Name,
			"source": "nudgebee",
		},
	}

	if !config.Enabled {
		watch["status"] = map[string]interface{}{
			"state": map[string]interface{}{
				"active": false,
			},
		}
	}

	return watch
}
