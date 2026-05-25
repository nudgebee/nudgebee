package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/common"
	"time"
)

// BaseIncidentClient provides common HTTP operations for incident management APIs.
// This can be embedded by specific incident management clients (PagerDuty, ZenDuty, OpsGenie, etc.).
type BaseIncidentClient struct {
	BaseURL     string
	AuthHeader  string // Header name for auth (e.g., "Authorization")
	AuthPrefix  string // Auth value prefix (e.g., "Token " for ZenDuty, "Token token=" for PagerDuty)
	AuthValue   string // The actual token/key value
	HTTPClient  *http.Client
	DefaultCtx  context.Context
	ContentType string
}

// NewBaseIncidentClient creates a new base incident client with default settings.
func NewBaseIncidentClient(baseURL, authHeader, authPrefix, authValue string) *BaseIncidentClient {
	return &BaseIncidentClient{
		BaseURL:     baseURL,
		AuthHeader:  authHeader,
		AuthPrefix:  authPrefix,
		AuthValue:   authValue,
		HTTPClient:  common.HttpClient(),
		DefaultCtx:  context.Background(),
		ContentType: "application/json",
	}
}

// WithContext sets a custom context for requests.
func (c *BaseIncidentClient) WithContext(ctx context.Context) *BaseIncidentClient {
	c.DefaultCtx = ctx
	return c
}

// WithTimeout sets a timeout for HTTP requests.
func (c *BaseIncidentClient) WithTimeout(timeout time.Duration) *BaseIncidentClient {
	c.HTTPClient.Timeout = timeout
	return c
}

// buildAuthHeader returns the complete auth header value.
func (c *BaseIncidentClient) buildAuthHeader() string {
	return c.AuthPrefix + c.AuthValue
}

// DoRequest performs an HTTP request with the configured auth and returns the response.
func (c *BaseIncidentClient) DoRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	url := c.BaseURL + endpoint

	var options []common.HttpOption
	options = append(options, common.HttpWithHeaders(map[string]string{
		c.AuthHeader:   c.buildAuthHeader(),
		"Content-Type": c.ContentType,
	}))

	if ctx != nil {
		options = append(options, common.HttpWithContext(ctx))
	} else if c.DefaultCtx != nil {
		options = append(options, common.HttpWithContext(c.DefaultCtx))
	}

	if body != nil {
		options = append(options, common.HttpWithJsonBody(body))
	}

	var resp *http.Response
	var err error

	switch method {
	case http.MethodGet:
		resp, err = common.HttpGet(url, options...)
	case http.MethodPost:
		resp, err = common.HttpPost(url, options...)
	case http.MethodPut:
		resp, err = common.HttpPut(url, options...)
	case http.MethodDelete:
		resp, err = common.HttpDelete(url, options...)
	default:
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}

	return resp, err
}

// Get performs a GET request and returns the response body as bytes.
func (c *BaseIncidentClient) Get(ctx context.Context, endpoint string) ([]byte, error) {
	resp, err := c.DoRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body", "error", cerr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// Post performs a POST request with JSON body and returns the response body as bytes.
func (c *BaseIncidentClient) Post(ctx context.Context, endpoint string, body interface{}) ([]byte, error) {
	resp, err := c.DoRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("POST request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body", "error", cerr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Put performs a PUT request with JSON body and returns the response body as bytes.
func (c *BaseIncidentClient) Put(ctx context.Context, endpoint string, body interface{}) ([]byte, error) {
	resp, err := c.DoRequest(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("PUT request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body", "error", cerr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Delete performs a DELETE request and returns the response body as bytes.
func (c *BaseIncidentClient) Delete(ctx context.Context, endpoint string) ([]byte, error) {
	resp, err := c.DoRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("DELETE request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body", "error", cerr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// GetJSON performs a GET request and unmarshals the response into the provided result.
func (c *BaseIncidentClient) GetJSON(ctx context.Context, endpoint string, result interface{}) error {
	body, err := c.Get(ctx, endpoint)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// PostJSON performs a POST request and unmarshals the response into the provided result.
func (c *BaseIncidentClient) PostJSON(ctx context.Context, endpoint string, body interface{}, result interface{}) error {
	respBody, err := c.Post(ctx, endpoint, body)
	if err != nil {
		return err
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// PutJSON performs a PUT request and unmarshals the response into the provided result.
func (c *BaseIncidentClient) PutJSON(ctx context.Context, endpoint string, body interface{}, result interface{}) error {
	respBody, err := c.Put(ctx, endpoint, body)
	if err != nil {
		return err
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}
