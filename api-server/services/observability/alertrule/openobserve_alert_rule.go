package alertrule

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type OpenObserveAlertRuleSource struct{}

func (s *OpenObserveAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	oo, err := getOpenObserveAlertConfig(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}
	body, err := buildOpenObserveAlertPayload(config, oo)
	if err != nil {
		return nil, err
	}

	resp, err := openObserveAlertRequest(http.MethodPost, oo, "", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenObserve alert: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenObserve response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("OpenObserve API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenObserve response: %w", err)
	}
	externalID, _ := result["id"].(string)
	if externalID == "" {
		externalID, _ = result["alert_id"].(string)
	}
	if data, ok := result["data"].(map[string]any); ok && externalID == "" {
		externalID, _ = data["id"].(string)
		if externalID == "" {
			externalID, _ = data["alert_id"].(string)
		}
	}
	if externalID == "" {
		return nil, fmt.Errorf("OpenObserve created the alert without returning an alert ID")
	}

	return &AlertRuleResult{ExternalRuleId: externalID, Name: config.Name, Status: "created"}, nil
}

func (s *OpenObserveAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	oo, err := getOpenObserveAlertConfig(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}
	body, err := buildOpenObserveAlertPayload(config, oo)
	if err != nil {
		return nil, err
	}

	resp, err := openObserveAlertRequest(http.MethodPut, oo, externalRuleId, body)
	if err != nil {
		return nil, fmt.Errorf("failed to update OpenObserve alert: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenObserve response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenObserve API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return &AlertRuleResult{ExternalRuleId: externalRuleId, Name: config.Name, Status: "updated"}, nil
}

func (s *OpenObserveAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	oo, err := getOpenObserveAlertConfig(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	resp, err := openObserveAlertRequest(http.MethodDelete, oo, externalRuleId, nil)
	if err != nil {
		return fmt.Errorf("failed to delete OpenObserve alert: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenObserve API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func openObserveAlertRequest(method string, oo openObserveAlertConfig, alertID string, payload any) (*http.Response, error) {
	if method != http.MethodPost && alertID == "" {
		return nil, fmt.Errorf("OpenObserve alert ID is required for %s", method)
	}
	endpoint := fmt.Sprintf("%s/api/v2/%s/alerts", oo.URL, url.PathEscape(oo.OrgID))
	if alertID != "" {
		endpoint += "/" + url.PathEscape(alertID)
	}
	options := []common.HttpOption{
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(oo.Username+":"+oo.Password)),
			"Content-Type":  "application/json",
		}),
		common.HttpWithTimeout(15 * time.Second),
	}
	if payload != nil {
		options = append(options, common.HttpWithJsonBody(payload))
	}
	switch method {
	case http.MethodPost:
		return common.HttpPost(endpoint, options...)
	case http.MethodPut:
		return common.HttpPut(endpoint, options...)
	case http.MethodDelete:
		return common.HttpDelete(endpoint, options...)
	default:
		return nil, fmt.Errorf("unsupported OpenObserve alert method %q", method)
	}
}

func buildOpenObserveAlertPayload(config AlertRuleConfig, oo openObserveAlertConfig) (map[string]any, error) {
	query := strings.TrimSpace(config.Query)
	if query == "" {
		return nil, fmt.Errorf("OpenObserve alert query is required")
	}
	name := strings.TrimSpace(config.Name)
	if name == "" || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("OpenObserve alert name must be non-empty and contain no control characters")
	}
	if strings.ContainsAny(name, ":#?&%\"'") || strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return nil, fmt.Errorf("OpenObserve alert names cannot contain whitespace, :, #, ?, &, %%, or quotes; use hyphens or underscores instead")
	}
	destinations, err := openObserveAlertDestinations(config.ProviderConfig["destinations"])
	if err != nil {
		return nil, err
	}

	queryType := strings.ToLower(openObserveProviderString(config.ProviderConfig, "query_type"))
	if queryType == "" {
		if config.AlertType == "metric" {
			queryType = "promql"
		} else {
			queryType = "sql"
		}
	}
	if queryType != "sql" && queryType != "promql" {
		return nil, fmt.Errorf("unsupported OpenObserve alert query type %q: use sql or promql", queryType)
	}

	streamType := strings.ToLower(openObserveProviderString(config.ProviderConfig, "stream_type"))
	if streamType == "" {
		if queryType == "promql" {
			streamType = "metrics"
		} else {
			streamType = "logs"
		}
	}
	streamName := openObserveProviderString(config.ProviderConfig, "stream_name")
	if streamName == "" {
		switch streamType {
		case "logs":
			streamName = oo.LogStream
		case "traces":
			streamName = oo.TraceStream
		case "metrics":
			streamName = "default"
		default:
			return nil, fmt.Errorf("unsupported OpenObserve stream type %q", streamType)
		}
	}
	if streamType != "logs" && streamType != "traces" && streamType != "metrics" {
		return nil, fmt.Errorf("unsupported OpenObserve stream type %q", streamType)
	}
	if !openObserveAlertStreamName(streamName) {
		return nil, fmt.Errorf("invalid OpenObserve stream name %q", streamName)
	}

	operator := openObserveProviderString(config.ProviderConfig, "threshold_operator")
	if operator == "" {
		operator = openObserveProviderString(config.ProviderConfig, "operator")
	}
	switch strings.ToUpper(operator) {
	case "":
		operator = ">="
	case "ABOVE":
		operator = ">"
	case "BELOW":
		operator = "<"
	}
	switch operator {
	case "=", "==":
		operator = "="
	case "!=", ">", ">=", "<", "<=":
	default:
		return nil, fmt.Errorf("unsupported OpenObserve alert operator %q", operator)
	}

	threshold := float64(1)
	if queryType == "promql" {
		threshold = 0
	}
	if value, ok := config.ProviderConfig["threshold"]; ok {
		parsed, err := toFloat64(value)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenObserve alert threshold: %w", err)
		}
		threshold = parsed
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return nil, fmt.Errorf("OpenObserve alert threshold must be finite")
	}
	triggerThreshold := 1
	triggerOperator := ">="
	if queryType == "sql" {
		if threshold <= 0 || math.Trunc(threshold) != threshold {
			return nil, fmt.Errorf("OpenObserve SQL alert threshold must be a positive whole number of result rows")
		}
		triggerThreshold = int(threshold)
		triggerOperator = operator
	}
	if value, ok := config.ProviderConfig["trigger_threshold"]; ok {
		parsed, err := toInt(value)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenObserve trigger threshold: %w", err)
		}
		triggerThreshold = parsed
	}

	period := 15
	if duration, err := time.ParseDuration(config.Duration); err == nil && duration.Minutes() > 0 {
		period = int(duration.Minutes())
	}
	if value, ok := config.ProviderConfig["period"]; ok {
		parsed, err := toInt(value)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenObserve alert period: %w", err)
		}
		period = parsed
	}
	frequency, silence := 1, 60
	if value, ok := config.ProviderConfig["frequency"]; ok {
		parsed, err := toInt(value)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenObserve alert frequency: %w", err)
		}
		frequency = parsed
	}
	if value, ok := config.ProviderConfig["silence"]; ok {
		parsed, err := toInt(value)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenObserve alert silence: %w", err)
		}
		silence = parsed
	}
	if period <= 0 || frequency <= 0 || triggerThreshold <= 0 || silence < 0 {
		return nil, fmt.Errorf("OpenObserve alert period, frequency and trigger threshold must be positive; silence cannot be negative")
	}

	description := config.Annotations["description"]
	if description == "" {
		description = config.Annotations["summary"]
	}
	queryCondition := map[string]any{"type": queryType}
	if queryType == "sql" {
		queryCondition["sql"] = query
	} else {
		queryCondition["promql"] = query
		queryCondition["promql_condition"] = map[string]any{
			"column": "value", "ignore_case": false, "operator": operator, "value": threshold,
		}
	}

	payload := map[string]any{
		"name":            name,
		"destinations":    destinations,
		"stream_type":     streamType,
		"stream_name":     streamName,
		"is_real_time":    false,
		"query_condition": queryCondition,
		"trigger_condition": map[string]any{
			"period": period, "operator": triggerOperator, "threshold": triggerThreshold,
			"frequency": frequency, "frequency_type": "minutes", "silence": silence,
		},
		"enabled":     config.Enabled,
		"description": description,
		"tags":        labelsToDatadogTags(config.Labels),
	}
	return payload, nil
}

func openObserveAlertDestinations(value any) ([]string, error) {
	var destinations []string
	switch value := value.(type) {
	case []string:
		destinations = value
	case []any:
		for _, item := range value {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("OpenObserve alert destinations must be a non-empty list of destination names")
			}
			destinations = append(destinations, name)
		}
	}
	if len(destinations) == 0 {
		return nil, fmt.Errorf("OpenObserve alert destinations are required; configure a destination that forwards alerts to Nudgebee")
	}
	for _, name := range destinations {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("OpenObserve alert destination names cannot be empty")
		}
	}
	return destinations, nil
}

func openObserveProviderString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}
