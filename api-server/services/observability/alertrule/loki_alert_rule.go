package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

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

// buildLokiAlertRule builds the rule group the relay path sends. The agent action takes
// the rule name separately, so every group it writes can share one static name.
func buildLokiAlertRule(config AlertRuleConfig) string {
	return buildRuleGroup(config, "nudgebee-alerts")
}

// buildRuleGroup builds a Prometheus rule group under the given group name — the
// format the Loki ruler and the Cortex/Mimir ruler both take, so the Prometheus
// direct source uses it as well. On the direct ruler API the group name in the body
// IS the group's identity — POSTing every rule under one name would make each create
// overwrite the last — so the user-mode sources pass the rule's own name here.
func buildRuleGroup(config AlertRuleConfig, groupName string) string {
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
	// The caller's summary is the alert title downstream (the Alertmanager webhook
	// parser reads annotations.summary); fall back to the rule name when none given.
	summary := config.Name
	if config.Annotations != nil {
		description = config.Annotations["description"]
		if s := config.Annotations["summary"]; s != "" {
			summary = s
		}
	}

	// Build Prometheus-compatible rule group YAML
	rule := map[string]interface{}{
		"name": groupName,
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
					"summary":     summary,
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

// LokiSaasAlertRuleSource manages rules through the Loki ruler API over direct HTTP,
// for user-configured (non-agent) Loki integrations.
type LokiSaasAlertRuleSource struct{}

// lokiRulerNamespace is the ruler namespace Nudgebee owns. Matches the namespace the
// agent action writes to, so both variants keep their rules in one place.
const lokiRulerNamespace = "nudgebee"

func (s *LokiSaasAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := getLokiConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	// The group is named after the rule so each rule is its own addressable group.
	if err := s.postRuleGroup(cfg, buildRuleGroup(config, config.Name)); err != nil {
		return nil, fmt.Errorf("failed to create Loki alert rule: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: config.Name,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *LokiSaasAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := getLokiConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	// The ruler API has no separate update verb: re-POSTing a group replaces it, so the
	// group name must stay the one this rule was created under.
	if err := s.postRuleGroup(cfg, buildRuleGroup(config, externalRuleId)); err != nil {
		return nil, fmt.Errorf("failed to update Loki alert rule: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *LokiSaasAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	cfg, err := getLokiConfigs(ctx, accountId)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/loki/api/v1/rules/%s/%s", cfg.RulerBaseURL(), lokiRulerNamespace, neturl.PathEscape(externalRuleId))
	resp, err := lokiRulerRequest(http.MethodDelete, url, "", cfg)
	if err != nil {
		return fmt.Errorf("failed to delete Loki alert rule: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A rule that is already gone is the state the caller asked for.
	if resp.StatusCode == http.StatusNotFound {
		slog.Info("loki alert rule already absent on delete", "rule", externalRuleId, "account", accountId)
		return nil
	}
	if err := checkLokiRulerStatus(resp, "delete"); err != nil {
		return fmt.Errorf("failed to delete Loki alert rule: %w", err)
	}
	return nil
}

// postRuleGroup writes a rule group into the Nudgebee ruler namespace.
func (s *LokiSaasAlertRuleSource) postRuleGroup(cfg *lokiRulerConfig, ruleGroup string) error {
	url := fmt.Sprintf("%s/loki/api/v1/rules/%s", cfg.RulerBaseURL(), lokiRulerNamespace)
	// The body is JSON, which the ruler's YAML parser accepts as a YAML subset; the
	// endpoint only advertises application/yaml.
	resp, err := lokiRulerRequest(http.MethodPost, url, ruleGroup, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return checkLokiRulerStatus(resp, "write")
}

// lokiRulerRequest issues an authenticated request against the Loki ruler API.
func lokiRulerRequest(method, url, body string, cfg *lokiRulerConfig) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create loki ruler request: %w", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/yaml")
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	// Only the selected method's credential is sent — see the same rule in the log transport.
	switch cfg.AuthType {
	case "basic":
		if cfg.Username != "" && cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
	case "bearer_token":
		if cfg.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
		}
	}
	if cfg.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", cfg.TenantID)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// checkLokiRulerStatus turns a ruler response into an operator-facing error.
func checkLokiRulerStatus(resp *http.Response, operation string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	body := strings.TrimSpace(string(bodyBytes))

	// A ruler backed by local rule storage cannot be written to. Loki reports this as a
	// 500 carrying "SetRuleGroup/DeleteRuleGroup unsupported in rule local store"
	// (observed on 3.4.1) — a bare 500 tells an operator nothing, and the fix is a
	// config change, not a retry. Older versions and some proxies answer 405 instead.
	if strings.Contains(body, "unsupported in rule local store") || resp.StatusCode == http.StatusMethodNotAllowed {
		return fmt.Errorf("the Loki ruler is read-only (HTTP %d: %s) — rule %s requires a ruler configured with a writable rule store (e.g. object storage), not local rule storage", resp.StatusCode, body, operation)
	}
	return fmt.Errorf("loki ruler %s: HTTP %d: %s", operation, resp.StatusCode, body)
}
