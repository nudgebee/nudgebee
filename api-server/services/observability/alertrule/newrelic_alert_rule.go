package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

type NewRelicAlertRuleSource struct{}

func (s *NewRelicAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiKey, nrAccountId, region, err := getNewRelicConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Get or use provided policy ID
	policyId := ""
	if config.ProviderConfig != nil {
		if pid, ok := config.ProviderConfig["policy_id"]; ok {
			policyId = fmt.Sprintf("%v", pid)
		}
	}
	if policyId == "" {
		policyId, err = getOrCreateDefaultNRPolicy(apiKey, nrAccountId, region)
		if err != nil {
			return nil, fmt.Errorf("failed to get/create NR alert policy: %w", err)
		}
	}

	body := buildCreateNRQLConditionRequest(nrAccountId, policyId, config)
	endpoint := getNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create New Relic NRQL condition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read New Relic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("new relic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			AlertsNrqlConditionStaticCreate struct {
				Id   string `json:"id"`
				Name string `json:"name"`
			} `json:"alertsNrqlConditionStaticCreate"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse New Relic response: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("new relic GraphQL error: %s", result.Errors[0].Message)
	}

	return &AlertRuleResult{
		ExternalRuleId: result.Data.AlertsNrqlConditionStaticCreate.Id,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *NewRelicAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	apiKey, nrAccountId, region, err := getNewRelicConfigs(ctx, config.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	body := buildUpdateNRQLConditionRequest(nrAccountId, externalRuleId, config)
	endpoint := getNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return nil, fmt.Errorf("failed to update New Relic NRQL condition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read New Relic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("new relic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *NewRelicAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	apiKey, nrAccountId, region, err := getNewRelicConfigs(ctx, accountId)
	if err != nil {
		return fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	nrAccountIdInt, _ := strconv.Atoi(nrAccountId)
	body := map[string]interface{}{
		"query": `mutation($accountId: Int!, $id: ID!) {
			alertsConditionDelete(accountId: $accountId, id: $id) { id }
		}`,
		"variables": map[string]interface{}{
			"accountId": nrAccountIdInt,
			"id":        externalRuleId,
		},
	}

	endpoint := getNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return fmt.Errorf("failed to delete New Relic condition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("new relic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func getOrCreateDefaultNRPolicy(apiKey, nrAccountId, region string) (string, error) {
	nrAccountIdInt, _ := strconv.Atoi(nrAccountId)

	// First, search for existing "Nudgebee Alerts" policy
	body := map[string]interface{}{
		"query": `query($accountId: Int!) {
			actor {
				account(id: $accountId) {
					alerts {
						policiesSearch(searchCriteria: { name: "Nudgebee Alerts" }) {
							policies { id name }
						}
					}
				}
			}
		}`,
		"variables": map[string]interface{}{
			"accountId": nrAccountIdInt,
		},
	}

	endpoint := getNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)
	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return "", fmt.Errorf("failed to search NR policies: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read NR response: %w", err)
	}

	var searchResult struct {
		Data struct {
			Actor struct {
				Account struct {
					Alerts struct {
						PoliciesSearch struct {
							Policies []struct {
								Id   string `json:"id"`
								Name string `json:"name"`
							} `json:"policies"`
						} `json:"policiesSearch"`
					} `json:"alerts"`
				} `json:"account"`
			} `json:"actor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &searchResult); err != nil {
		return "", fmt.Errorf("failed to parse NR policy search: %w", err)
	}

	policies := searchResult.Data.Actor.Account.Alerts.PoliciesSearch.Policies
	if len(policies) > 0 {
		return policies[0].Id, nil
	}

	// Create new policy
	body = map[string]interface{}{
		"query": `mutation($accountId: Int!, $policy: AlertsPolicyInput!) {
			alertsPolicyCreate(accountId: $accountId, policy: $policy) {
				id name
			}
		}`,
		"variables": map[string]interface{}{
			"accountId": nrAccountIdInt,
			"policy": map[string]interface{}{
				"name":               "Nudgebee Alerts",
				"incidentPreference": "PER_CONDITION",
			},
		},
	}
	resp, err = common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body))
	if err != nil {
		return "", fmt.Errorf("failed to create NR policy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read NR response: %w", err)
	}

	var createResult struct {
		Data struct {
			AlertsPolicyCreate struct {
				Id string `json:"id"`
			} `json:"alertsPolicyCreate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &createResult); err != nil {
		return "", fmt.Errorf("failed to parse NR policy create: %w", err)
	}

	return createResult.Data.AlertsPolicyCreate.Id, nil
}

// nrqlConditionInput builds the GraphQL variables for NRQL condition mutations.
func nrqlConditionInput(config AlertRuleConfig) map[string]interface{} {
	threshold := 0.0
	operator := "ABOVE"
	aggregationWindow := 60

	if config.ProviderConfig != nil {
		if t, ok := config.ProviderConfig["threshold"]; ok {
			if v, err := toFloat64(t); err == nil {
				threshold = v
			}
		}
		if op, ok := config.ProviderConfig["threshold_operator"].(string); ok {
			operator = op
		}
		if w, ok := config.ProviderConfig["aggregation_window"]; ok {
			if v, err := toInt(w); err == nil {
				aggregationWindow = v
			}
		}
	}

	return map[string]interface{}{
		"name":    config.Name,
		"enabled": config.Enabled,
		"nrql": map[string]interface{}{
			"query": config.Query,
		},
		"signal": map[string]interface{}{
			"aggregationWindow": aggregationWindow,
		},
		"terms": []map[string]interface{}{
			{
				"threshold":            threshold,
				"thresholdOccurrences": "ALL",
				"thresholdDuration":    300,
				"operator":             operator,
				"priority":             "CRITICAL",
			},
		},
	}
}

func buildCreateNRQLConditionRequest(nrAccountId, policyId string, config AlertRuleConfig) map[string]interface{} {
	nrAccountIdInt, _ := strconv.Atoi(nrAccountId)
	policyIdInt, _ := strconv.Atoi(policyId)

	return map[string]interface{}{
		"query": `mutation($accountId: Int!, $policyId: ID!, $condition: AlertsNrqlConditionStaticInput!) {
			alertsNrqlConditionStaticCreate(accountId: $accountId, policyId: $policyId, condition: $condition) {
				id name
			}
		}`,
		"variables": map[string]interface{}{
			"accountId": nrAccountIdInt,
			"policyId":  policyIdInt,
			"condition": nrqlConditionInput(config),
		},
	}
}

func buildUpdateNRQLConditionRequest(nrAccountId, conditionId string, config AlertRuleConfig) map[string]interface{} {
	nrAccountIdInt, _ := strconv.Atoi(nrAccountId)

	return map[string]interface{}{
		"query": `mutation($accountId: Int!, $id: ID!, $condition: AlertsNrqlConditionStaticInput!) {
			alertsNrqlConditionStaticUpdate(accountId: $accountId, id: $id, condition: $condition) {
				id name
			}
		}`,
		"variables": map[string]interface{}{
			"accountId": nrAccountIdInt,
			"id":        conditionId,
			"condition": nrqlConditionInput(config),
		},
	}
}

func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func toInt(v interface{}) (int, error) {
	switch val := v.(type) {
	case float64:
		return int(val), nil
	case int:
		return val, nil
	case string:
		return strconv.Atoi(val)
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}
