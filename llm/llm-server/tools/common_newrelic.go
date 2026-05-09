package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

// getNewRelicConfigs retrieves New Relic configuration from tool context
// Returns apiKey, accountId, region, and error
func getNewRelicConfigs(ctx core.NbToolContext) (string, string, string, error) {
	var err error
	apiKey := ""
	accountId := ""
	region := "us" // Default to US region

	for _, cfgValue := range ctx.ToolConfig.Values {
		if strings.EqualFold(cfgValue.Name, "region") && cfgValue.Value != "" {
			region = cfgValue.Value
		} else if strings.EqualFold(cfgValue.Name, "api_key") && cfgValue.Value != "" {
			apiKey = cfgValue.Value
			if cfgValue.IsEncrypted {
				apiKey, err = common.Decrypt(cfgValue.Value)
				if err != nil {
					ctx.Ctx.GetLogger().Error("newrelic: unable to decrypt api_key", "error", err)
					return "", "", "", err
				}
			}
		} else if strings.EqualFold(cfgValue.Name, "nr_account_id") && cfgValue.Value != "" {
			accountId = cfgValue.Value
		}
	}

	if apiKey == "" || accountId == "" {
		return "", "", "", errors.New("newrelic: api_key and nr_account_id are required. Please check tool configuration")
	}

	// Validate region
	if region != "us" && region != "eu" {
		ctx.Ctx.GetLogger().Warn("newrelic: invalid region, defaulting to 'us'", "region", region)
		region = "us"
	}

	return apiKey, accountId, region, nil
}

// getNewRelicConfigSchema returns the configuration schema for New Relic tools
func getNewRelicConfigSchema() core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"api_key", "nr_account_id", "region"},
		ConfigType:   "newrelic",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"api_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic User API Key",
				IsEncrypted: true,
			},
			"nr_account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic Account ID",
			},
			"region": {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic Region (us or eu)",
				Enum:        []any{"us", "eu"},
			},
		},
	}
}

// getNewRelicGraphQLEndpoint returns the GraphQL endpoint based on region
func getNewRelicGraphQLEndpoint(region string) string {
	if region == "eu" {
		return "https://api.eu.newrelic.com/graphql"
	}
	return "https://api.newrelic.com/graphql"
}

// doNewRelicGraphQLRequest executes a GraphQL query against New Relic's NerdGraph API
func doNewRelicGraphQLRequest(apiKey, region, query string, variables map[string]any) (map[string]any, error) {
	endpoint := getNewRelicGraphQLEndpoint(region)

	// Build request body
	requestBody := map[string]any{
		"query": query,
	}
	if len(variables) > 0 {
		requestBody["variables"] = variables
	}

	bodyBytes, err := common.MarshalJson(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Execute request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body, bodyReadErr := io.ReadAll(resp.Body)
	if bodyReadErr != nil {
		return nil, fmt.Errorf("failed to read newrelic response body: %w", bodyReadErr)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("newrelic api error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result map[string]any
	if unmarshalErr := common.UnmarshalJson(body, &result); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshal newrelic response JSON: %w. Response body: %s", unmarshalErr, string(body))
	}

	// Check for GraphQL errors
	if errors, ok := result["errors"].([]any); ok && len(errors) > 0 {
		if firstError, ok := errors[0].(map[string]any); ok {
			if message, ok := firstError["message"].(string); ok {
				return nil, fmt.Errorf("newrelic graphql error: %s", message)
			}
		}
		return nil, fmt.Errorf("newrelic graphql error: %v", errors)
	}

	return result, nil
}

// escapeGraphQLString escapes special characters for safe use in GraphQL query strings
// Prevents GraphQL injection attacks
func escapeGraphQLString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\") // Backslash first (must be first!)
	s = strings.ReplaceAll(s, "'", "\\'")   // Single quote
	s = strings.ReplaceAll(s, "\"", "\\\"") // Double quote
	s = strings.ReplaceAll(s, "\n", "\\n")  // Newline
	s = strings.ReplaceAll(s, "\r", "\\r")  // Carriage return
	s = strings.ReplaceAll(s, "\t", "\\t")  // Tab
	s = strings.ReplaceAll(s, "{", "")      // Remove braces (GraphQL injection)
	s = strings.ReplaceAll(s, "}", "")
	return s
}

// buildEntitySearchQuery constructs a NerdGraph entity search query string
// Supports simple filters like "name:api-server" or "tags.environment:production"
func buildEntitySearchQuery(domain, entityType, filters string) string {
	queryParts := []string{}

	if domain != "" {
		queryParts = append(queryParts, fmt.Sprintf("domain='%s'", domain))
	}

	if entityType != "" {
		queryParts = append(queryParts, fmt.Sprintf("type='%s'", entityType))
	}

	// Parse simple filter format: "name:value" or "tags.key:value"
	if filters != "" {
		filterParts := strings.Split(filters, " ")
		for _, part := range filterParts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			// Split by first colon
			colonIndex := strings.Index(part, ":")
			if colonIndex > 0 {
				key := strings.TrimSpace(part[:colonIndex])
				value := strings.TrimSpace(part[colonIndex+1:])

				// Skip empty keys or values
				if key == "" || value == "" {
					continue
				}

				// Escape the value to prevent injection
				escapedValue := escapeGraphQLString(value)

				if key == "name" {
					// Use LIKE for name searches
					queryParts = append(queryParts, fmt.Sprintf("name LIKE '%%%s%%'", escapedValue))
				} else if strings.HasPrefix(key, "tags.") {
					// Tag filter - validate tag key contains only safe characters
					tagKey := strings.TrimPrefix(key, "tags.")
					if isValidFieldName(tagKey) {
						queryParts = append(queryParts, fmt.Sprintf("tags.%s='%s'", tagKey, escapedValue))
					}
				} else {
					// Generic field filter - validate field name
					if isValidFieldName(key) {
						queryParts = append(queryParts, fmt.Sprintf("%s='%s'", key, escapedValue))
					}
				}
			}
		}
	}

	return strings.Join(queryParts, " AND ")
}

// isValidFieldName checks if a field name contains only safe characters
// Prevents injection via field names
func isValidFieldName(name string) bool {
	if name == "" {
		return false
	}
	// Allow alphanumeric, underscore, dot, and hyphen
	for _, char := range name {
		isValid := (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '.' || char == '-'
		if !isValid {
			return false
		}
	}
	return true
}
