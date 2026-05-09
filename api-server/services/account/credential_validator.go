package account

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/config"
	"strings"
	"time"
)

// validateAzureCredentialsInternal delegates to collector-server
func validateAzureCredentialsInternal(ctx context.Context, tenantID, clientID, clientSecret, subscriptionID string) ValidateCloudCredentialsResponse {
	request := map[string]interface{}{
		"cloud_provider":  "Azure",
		"tenant_id":       tenantID,
		"client_id":       clientID,
		"client_secret":   clientSecret,
		"subscription_id": subscriptionID,
	}

	return callCollectorValidationEndpoint(ctx, request)
}

// validateGCPCredentialsInternal delegates to collector-server
func validateGCPCredentialsInternal(ctx context.Context, credentialsJSON, projectID, billingProjectID, billingDatasetID, billingTableID string) ValidateCloudCredentialsResponse {
	request := map[string]interface{}{
		"cloud_provider":   "GCP",
		"credentials_json": credentialsJSON,
		"project_id":       projectID,
	}

	// Include billing fields only when provided
	if billingProjectID != "" {
		request["billing_project_id"] = billingProjectID
	}
	if billingDatasetID != "" {
		request["billing_dataset_id"] = billingDatasetID
	}
	if billingTableID != "" {
		request["billing_table_id"] = billingTableID
	}

	return callCollectorValidationEndpoint(ctx, request)
}

// validateCFCredentialsInternal validates Cloud Foundry credentials by:
// 1. Calling GET /v3/info to verify API reachability
// 2. Making an authenticated call to GET /v3/organizations to verify credentials work
func validateCFCredentialsInternal(ctx context.Context, cfAPIURL, accessSecret, accessKey string, data map[string]any) error {
	skipSSL, _ := data["skip_ssl"].(bool)
	authType, _ := data["auth_type"].(string)

	transport := &http.Transport{}
	if skipSSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

	// Step 1: Verify CF API is reachable via /v3/info (unauthenticated)
	infoURL := cfAPIURL + "/v3/info"
	req, err := http.NewRequest("GET", infoURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to CF API at %s: %w", cfAPIURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CF API at %s returned status %d", cfAPIURL, resp.StatusCode)
	}

	// Step 2: Verify credentials by making an authenticated API call
	token := accessSecret
	if authType == "uaa" {
		// For UAA: discover UAA URL, then fetch a token
		uaaURL, err := discoverCFUAAURL(client, cfAPIURL, resp)
		if err != nil {
			return fmt.Errorf("UAA URL not found — %w", err)
		}

		// Fetch token from UAA using client_credentials grant
		token, err = fetchUAATokenForValidation(client, uaaURL, accessKey, accessSecret)
		if err != nil {
			return fmt.Errorf("failed to authenticate with UAA: %w", err)
		}
	}

	// Call an authenticated endpoint to verify the token/credentials actually work
	orgsURL := cfAPIURL + "/v3/organizations?per_page=1"
	authReq, err := http.NewRequest("GET", orgsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create auth validation request: %w", err)
	}
	authReq.Header.Set("Authorization", "Bearer "+token)
	authReq.Header.Set("Accept", "application/json")

	authResp, err := client.Do(authReq)
	if err != nil {
		return fmt.Errorf("failed to make authenticated request to CF API: %w", err)
	}
	defer func() { _ = authResp.Body.Close() }()

	if authResp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("CF credentials are invalid — received 401 Unauthorized from %s", cfAPIURL)
	}
	if authResp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("CF credentials lack permissions — received 403 Forbidden from %s", cfAPIURL)
	}
	if authResp.StatusCode < 200 || authResp.StatusCode >= 300 {
		return fmt.Errorf("CF API authenticated call returned status %d", authResp.StatusCode)
	}

	return nil
}

// discoverCFUAAURL discovers the UAA URL from the CF API.
// It first checks links.uaa.href from the already-fetched /v3/info response,
// then falls back to token_endpoint from /v2/info (common in PCF/TAS).
func discoverCFUAAURL(client *http.Client, cfAPIURL string, v3InfoResp *http.Response) (string, error) {
	// Try /v3/info response first (already fetched)
	body, err := io.ReadAll(v3InfoResp.Body)
	if err == nil {
		var info struct {
			Links struct {
				UAA struct {
					Href string `json:"href"`
				} `json:"uaa"`
			} `json:"links"`
		}
		if json.Unmarshal(body, &info) == nil && info.Links.UAA.Href != "" {
			return info.Links.UAA.Href, nil
		}
	}

	// Fall back to /v2/info (PCF/TAS exposes token_endpoint there)
	v2InfoURL := strings.TrimRight(cfAPIURL, "/") + "/v2/info"
	v2Resp, err := client.Get(v2InfoURL)
	if err != nil {
		return "", fmt.Errorf("CF API /v3/info has no UAA link and /v2/info is unreachable: %w", err)
	}
	defer func() { _ = v2Resp.Body.Close() }()

	v2Body, err := io.ReadAll(v2Resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read /v2/info response: %w", err)
	}

	var v2Info struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(v2Body, &v2Info); err != nil {
		return "", fmt.Errorf("failed to parse /v2/info response: %w", err)
	}
	if v2Info.TokenEndpoint == "" {
		return "", fmt.Errorf("UAA URL not found in /v3/info (links.uaa) or /v2/info (token_endpoint)")
	}

	return v2Info.TokenEndpoint, nil
}

// fetchUAATokenForValidation performs a client_credentials grant against UAA to validate credentials.
func fetchUAATokenForValidation(client *http.Client, uaaURL, clientID, clientSecret string) (string, error) {
	tokenURL := strings.TrimRight(uaaURL, "/") + "/oauth/token"

	form := fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s",
		url.QueryEscape(clientID), url.QueryEscape(clientSecret))

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(form))
	if err != nil {
		return "", fmt.Errorf("failed to create UAA token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("UAA token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read UAA response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("UAA returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse UAA token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("UAA returned empty access token")
	}

	return tokenResp.AccessToken, nil
}

// collectorHTTPClient is a shared HTTP client for calls to collector-server,
// enabling connection reuse across requests.
var collectorHTTPClient = &http.Client{Timeout: 60 * time.Second}

// listAzureSubscriptionsInternal calls collector-server to discover accessible Azure subscriptions
func listAzureSubscriptionsInternal(ctx context.Context, tenantID, clientID, clientSecret string) ([]AzureSubscriptionInfo, error) {
	body, err := json.Marshal(map[string]string{
		"tenant_id":     tenantID,
		"client_id":     clientID,
		"client_secret": clientSecret,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	collectorURL := fmt.Sprintf("%s/v1/cloud/azure_list_subscriptions", config.Config.CloudCollectorServerUrl)
	req, err := http.NewRequestWithContext(ctx, "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)

	resp, err := collectorHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data struct {
			Subscriptions []AzureSubscriptionInfo `json:"subscriptions"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return nil, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}

	return apiResponse.Data.Subscriptions, nil
}

// listGCPProjectsInternal calls collector-server to discover accessible GCP projects
func listGCPProjectsInternal(ctx context.Context, credentialsJSON string) ([]GcpProjectInfo, error) {
	body, err := json.Marshal(map[string]string{
		"credentials_json": credentialsJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	collectorURL := fmt.Sprintf("%s/v1/cloud/gcp_list_projects", config.Config.CloudCollectorServerUrl)
	req, err := http.NewRequestWithContext(ctx, "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)

	resp, err := collectorHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data struct {
			Projects []GcpProjectInfo `json:"projects"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return nil, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}

	return apiResponse.Data.Projects, nil
}

// callCollectorValidationEndpoint makes HTTP request to collector-server
func callCollectorValidationEndpoint(ctx context.Context, request map[string]interface{}) ValidateCloudCredentialsResponse {
	// Build request body
	body, err := json.Marshal(request)
	if err != nil {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to marshal request: %v", err),
		}
	}

	// Create HTTP request
	collectorURL := fmt.Sprintf("%s/v1/cloud/validate_credentials", config.Config.CloudCollectorServerUrl)
	req, err := http.NewRequestWithContext(ctx, "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to create request: %v", err),
		}
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)

	// Make request using shared client
	resp, err := collectorHTTPClient.Do(req)
	if err != nil {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to call collector-server: %v", err),
		}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to read response: %v", err),
		}
	}

	// Handle non-200 responses
	if resp.StatusCode != http.StatusOK {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("collector-server returned %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	// Parse response
	var apiResponse struct {
		Data struct {
			Success            bool               `json:"success"`
			Provider           string             `json:"provider"`
			MissingPermissions []string           `json:"missingPermissions"`
			PermissionDetails  []PermissionStatus `json:"permissionDetails"`
			ErrorMessage       string             `json:"errorMessage"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to parse response: %v", err),
		}
	}

	// Check for API errors
	if len(apiResponse.Errors) > 0 {
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: apiResponse.Errors[0].Message,
		}
	}

	// Return validation result
	return ValidateCloudCredentialsResponse{
		Success:            apiResponse.Data.Success,
		Provider:           apiResponse.Data.Provider,
		MissingPermissions: apiResponse.Data.MissingPermissions,
		PermissionDetails:  apiResponse.Data.PermissionDetails,
		ErrorMessage:       apiResponse.Data.ErrorMessage,
	}
}
