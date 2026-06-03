package integrations

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/runbook/internal/tasks/types"
	"reflect"
	"strings"
	"time"
)

// HttpTask implements the Task interface for making HTTP requests.
type HttpTask struct{}

func (t *HttpTask) GetName() string {
	return "integrations.http"
}

// GetDescription returns a brief description of the task.
func (t *HttpTask) GetDescription() string {
	return "Call any REST API or web endpoint."
}

// GetDisplayName returns a human-readable name for the task.
func (t *HttpTask) GetDisplayName() string {
	return "HTTP Request"
}

// dereferenceValue returns the underlying value of a pointer or interface.
// If it's a pointer, it dereferences it. If nil, returns invalid value.
func dereferenceValue(v any) reflect.Value {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return reflect.Value{}
		}
		val = val.Elem()
	}
	return val
}

func (t *HttpTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing HttpRequestTask", "params", params)

	urlVal, ok := params["url"]
	if !ok {
		return nil, fmt.Errorf("url is a required parameter")
	}
	taskCtx.GetLogger().Info("URL parameter received", "type", fmt.Sprintf("%T", urlVal), "value", urlVal)

	urlStr, ok := urlVal.(string)
	if !ok {
		return nil, fmt.Errorf("url parameter must be a string, got %T", urlVal)
	}

	// Validate URL format
	parsedURL, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL format: %w", err)
	}

	method, ok := params["method"].(string)
	if !ok {
		method = "GET" // Default to GET
	}
	method = strings.ToUpper(method)

	var body io.Reader
	var contentType = "application/json" // Default Content-Type

	if headers, ok := params["headers"].(map[string]any); ok {
		if ct, ok := headers["Content-Type"].(string); ok {
			contentType = ct
		}
	}

	if requestBody, ok := params["body"]; ok {
		switch strings.ToLower(contentType) {
		case "application/json":
			// Check if requestBody is a string or an alias to string (e.g. custom type)
			val := dereferenceValue(requestBody)

			if val.IsValid() && val.Kind() == reflect.String {
				body = strings.NewReader(val.String())
			} else {
				bodyBytes, err := json.Marshal(requestBody)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal JSON body: %w", err)
				}
				body = bytes.NewBuffer(bodyBytes)
			}
		case "application/x-www-form-urlencoded":
			val := dereferenceValue(requestBody)
			if val.IsValid() && val.Kind() == reflect.String {
				body = strings.NewReader(val.String())
			} else if bodyMap, isMap := requestBody.(map[string]any); isMap {
				formData := url.Values{}
				for k, v := range bodyMap {
					formData.Set(k, fmt.Sprintf("%v", v))
				}
				body = strings.NewReader(formData.Encode())
			} else {
				return nil, fmt.Errorf("body for application/x-www-form-urlencoded must be a map or string")
			}
		default:
			// For other content types, assume body is a string
			val := dereferenceValue(requestBody)
			if val.IsValid() && val.Kind() == reflect.String {
				body = strings.NewReader(val.String())
			} else {
				return nil, fmt.Errorf("unsupported Content-Type '%s' or invalid body format", contentType)
			}
		}
	}

	req, err := http.NewRequestWithContext(taskCtx.GetContext(), method, parsedURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers, including Content-Type
	if headers, ok := params["headers"].(map[string]any); ok {
		for key, val := range headers {
			if v, isString := val.(string); isString {
				req.Header.Set(key, v)
			} else {
				// Attempt to convert non-string values to string
				req.Header.Set(key, fmt.Sprintf("%v", val))
			}
		}
	} else if headers, ok := params["headers"].(map[string]string); ok { // Also allow map[string]string
		for key, val := range headers {
			req.Header.Set(key, val)
		}
	}
	// Ensure Content-Type is set if a body is present
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}

	// Apply authentication based on auth_type
	if err := applyAuth(req, params, taskCtx); err != nil {
		return nil, fmt.Errorf("failed to apply authentication: %w", err)
	}

	// Configure http.Client
	httpClient := &http.Client{
		Timeout: 30 * time.Second, // Default timeout
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false}, // Default TLS config
	}

	if timeout, ok := params["timeout"].(string); ok {
		if d, err := time.ParseDuration(timeout); err == nil {
			httpClient.Timeout = d
		} else {
			taskCtx.GetLogger().Warn("Invalid timeout duration, using default", "timeout", timeout, "error", err)
		}
	} else if deadline, ok := taskCtx.GetContext().Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		httpClient.Timeout = remaining
	}

	if insecureSkipVerify, ok := params["insecure_skip_verify"].(bool); ok {
		transport.TLSClientConfig.InsecureSkipVerify = insecureSkipVerify
	}

	if proxyURLStr, ok := params["proxy_url"].(string); ok && proxyURLStr != "" {
		proxyURL, err := url.Parse(proxyURLStr)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	httpClient.Transport = transport

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			taskCtx.GetLogger().Warn("Failed to close response body", "error", err)
		}
	}()

	// Limit response body to 10MB to prevent DoS
	const maxBodySize = 10 * 1024 * 1024 // 10MB
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Prepare response headers for output
	responseHeaders := make(map[string]any)
	for key, values := range resp.Header {
		if len(values) == 1 {
			responseHeaders[key] = values[0]
		} else {
			responseHeaders[key] = values // Keep as array if multiple values
		}
	}

	var parsedBody any
	// Attempt to unmarshal body as JSON, otherwise keep as string
	if err := json.Unmarshal(responseBody, &parsedBody); err != nil {
		parsedBody = string(responseBody)
	}

	// If status code is 4xx or 5xx, return an error
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http request failed with status %d: %v", resp.StatusCode, parsedBody)
	}

	return map[string]any{
		"status_code": float64(resp.StatusCode),
		"headers":     responseHeaders,
		"body":        parsedBody,
	}, nil
}

// applyAuth sets the appropriate Authorization header on the request
// based on the auth_type parameter.
func applyAuth(req *http.Request, params map[string]any, taskCtx types.TaskContext) error {
	authType, _ := params["auth_type"].(string)
	switch authType {
	case "api_key":
		apiKey, _ := params["api_key"].(string)
		headerName, _ := params["api_header_name"].(string)
		if headerName == "" {
			headerName = "Authorization"
		}
		if apiKey != "" {
			req.Header.Set(headerName, apiKey)
		}

	case "bearer":
		token, _ := params["bearer_token"].(string)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

	case "basic":
		username, _ := params["basic_auth_username"].(string)
		password, _ := params["basic_auth_password"].(string)
		if username != "" {
			encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			req.Header.Set("Authorization", "Basic "+encoded)
		}

	case "oauth2":
		tokenURL, _ := params["oauth_token_url"].(string)
		clientID, _ := params["oauth_client_id"].(string)
		clientSecret, _ := params["oauth_client_secret"].(string)
		scope, _ := params["oauth_scope"].(string)
		audience, _ := params["oauth_audience"].(string)

		if tokenURL == "" || clientID == "" || clientSecret == "" {
			return fmt.Errorf("OAuth 2.0 requires oauth_token_url, oauth_client_id, and oauth_client_secret")
		}

		token, err := FetchOAuthToken(tokenURL, clientID, clientSecret, scope, audience)
		if err != nil {
			return fmt.Errorf("failed to fetch OAuth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		taskCtx.GetLogger().Info("Applied OAuth 2.0 authentication")

	case "":
		// No auth
	default:
		taskCtx.GetLogger().Warn("Unknown auth_type, skipping authentication", "auth_type", authType)
	}
	return nil
}

func (t *HttpTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"url": {
				Type:        types.PropertyTypeString,
				Description: "The URL to make the HTTP request to.",
				Required:    true,
				Order:       1,
			},
			"method": {
				Type:        types.PropertyTypeString,
				Description: "The HTTP method (e.g., GET, POST). Defaults to GET.",
				Required:    false,
				Default:     "GET",
				Options:     []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
				Order:       2,
			},
			"headers": {
				Type:        types.PropertyTypeObject,
				Description: "A map of request headers.",
				Required:    false,
				Order:       3,
			},
			"body": {
				Type:        types.PropertyTypeString,
				Description: "The request body",
				Required:    false,
				Order:       4,
			},
			"auth_type": {
				Type:        types.PropertyTypeString,
				Description: "Authentication type: 'bearer', 'basic', 'api_key', 'oauth2', or empty for none.",
				Required:    false,
				Options:     []string{"", "bearer", "basic", "api_key", "oauth2"},
				Order:       5,
			},
			"bearer_token": {
				Type:        types.PropertyTypeString,
				Description: "Bearer token for Authorization header.",
				Required:    false,
				Order:       6,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"bearer"}},
			},
			"basic_auth_username": {
				Type:        types.PropertyTypeString,
				Description: "Username for Basic auth.",
				Required:    false,
				Order:       7,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"basic"}},
			},
			"basic_auth_password": {
				Type:        types.PropertyTypeString,
				Description: "Password for Basic auth.",
				Required:    false,
				IsEncrypted: true,
				Order:       8,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"basic"}},
			},
			"api_key": {
				Type:        types.PropertyTypeString,
				Description: "API key value.",
				Required:    false,
				IsEncrypted: true,
				Order:       9,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"api_key"}},
			},
			"api_header_name": {
				Type:        types.PropertyTypeString,
				Description: "Header name for API key. Defaults to 'Authorization'.",
				Required:    false,
				Default:     "Authorization",
				Order:       10,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"api_key"}},
			},
			"oauth_token_url": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 token endpoint URL.",
				Required:    false,
				Order:       11,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_client_id": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 client ID.",
				Required:    false,
				Order:       12,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_client_secret": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 client secret.",
				Required:    false,
				IsEncrypted: true,
				Order:       13,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_scope": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 scope, space-separated.",
				Required:    false,
				Order:       14,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_audience": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 audience / resource identifier. Required by some providers like Auth0.",
				Required:    false,
				Order:       15,
				DependsOn:   []string{"auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"timeout": {
				Type:        types.PropertyTypeString,
				Description: "Timeout for the request (e.g., '10s', '1m'). Defaults to 30s.",
				Required:    false,
				Default:     "30s",
				Order:       16,
			},
			"insecure_skip_verify": {
				Type:        types.PropertyTypeBoolean,
				Description: "Whether to skip TLS certificate verification. Defaults to false.",
				Required:    false,
				Default:     false,
				Order:       17,
			},
			"proxy_url": {
				Type:        types.PropertyTypeString,
				Description: "URL of a proxy to use for the request.",
				Required:    false,
				Order:       18,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *HttpTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status_code": {
				Type:        "number",
				Description: "The HTTP status code of the response.",
				Required:    true,
			},
			"headers": {
				Type:        "map[string]any",
				Description: "A map of response headers.",
				Required:    true,
			},
			"body": {
				Type:        "any",
				Description: "The parsed JSON response body (if applicable) or raw string body.",
				Required:    true,
			},
		},
	}
}
