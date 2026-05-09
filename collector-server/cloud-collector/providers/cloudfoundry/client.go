package cloudfoundry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// cfClient wraps the CF API v3 HTTP client with auth, pagination, and retry.
type cfClient struct {
	baseURL     string
	httpClient  *http.Client
	auth        *cfAuthProvider
	logCacheURL string // Log Cache base URL (empty = not available)
}

// newCFClient creates a new CF API client from an Account's stored credentials.
func newCFClient(ctx providers.CloudProviderContext, account providers.Account) (*cfClient, error) {
	// Parse account data
	var data cfAccountData
	if account.Data != nil && *account.Data != "" {
		if err := json.Unmarshal([]byte(*account.Data), &data); err != nil {
			return nil, fmt.Errorf("failed to parse account data: %w", err)
		}
	}

	if data.CFAPIURL == "" {
		return nil, fmt.Errorf("cf_api_url is required in account data")
	}

	// Build HTTP client
	transport := &http.Transport{}
	if data.SkipSSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-configured for self-signed certs (Korifi dev)
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// UAA OAuth2: access_key = client_id, access_secret = client_secret (encrypted)
	clientID := ""
	if account.AccessKey != nil {
		clientID = *account.AccessKey
	}
	clientSecret := ""
	if account.AccessSecret != nil && *account.AccessSecret != "" {
		decrypted, err := common.Decrypt(*account.AccessSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt access secret: %w", err)
		}
		clientSecret = decrypted
	}

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("UAA auth requires access_key (client_id) and access_secret (client_secret)")
	}

	// Discover UAA URL from CF API
	uaaURL, err := discoverUAAURL(httpClient, data.CFAPIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover UAA URL: %w", err)
	}

	auth := newUAAAuth(clientID, clientSecret, uaaURL)

	// Discover Log Cache URL (non-blocking: failure just means no log cache)
	logCacheURL := discoverLogCacheURL(httpClient, data)

	return &cfClient{
		baseURL:     strings.TrimRight(data.CFAPIURL, "/"),
		httpClient:  httpClient,
		auth:        auth,
		logCacheURL: logCacheURL,
	}, nil
}

// get performs an authenticated GET request to the CF API.
func (c *cfClient) get(path string) ([]byte, error) {
	return c.doRequestWithContext(context.Background(), "GET", path, nil)
}

// getWithContext performs an authenticated GET request with a context for timeout/cancellation.
func (c *cfClient) getWithContext(ctx context.Context, path string) ([]byte, error) {
	return c.doRequestWithContext(ctx, "GET", path, nil)
}

// post performs an authenticated POST request to the CF API.
func (c *cfClient) post(path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}
	return c.doRequest("POST", path, bodyReader)
}

// doRequest performs an authenticated HTTP request with retry on 401.
func (c *cfClient) doRequest(method, path string, body io.Reader) ([]byte, error) {
	return c.doRequestWithContext(context.Background(), method, path, body)
}

// doRequestWithContext performs an authenticated HTTP request with context and retry on 401.
func (c *cfClient) doRequestWithContext(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	fullURL := c.baseURL + path

	resp, err := c.executeRequest(ctx, method, fullURL, body)
	if err != nil {
		return nil, err
	}

	// On 401, invalidate token and retry once (handles expired UAA tokens)
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		c.auth.invalidateToken()

		if seeker, ok := body.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, fmt.Errorf("failed to rewind request body for retry: %w", err)
			}
		} else if body != nil {
			// Body is not seekable, so we cannot retry.
			return nil, fmt.Errorf("CF API %s %s returned 401 (Unauthorized): token may have expired or lack required scopes; cannot retry non-seekable POST/PUT requests automatically", method, path)
		}
		resp, err = c.executeRequest(ctx, method, fullURL, body)
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CF API %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (c *cfClient) executeRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := c.auth.getToken(c.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// ValidateEventAccess checks whether the current token can read audit events.
// Returns nil if accessible, or an error describing the permission issue.
// Note: This is available for account setup validation; it is NOT called during
// periodic event collection to avoid silently blocking event fetching.
func (c *cfClient) ValidateEventAccess() error {
	_, err := c.get("/v3/audit_events?per_page=1")
	if err != nil {
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "Forbidden") {
			return fmt.Errorf("audit events not accessible: token lacks 'cloud_controller.admin_read_only' or 'audit_events.read' scope. Events will not be collected until this is resolved")
		}
		return fmt.Errorf("audit events endpoint check failed: %w", err)
	}
	return nil
}

// getFromLogCache performs an authenticated GET request to the Log Cache API.
func (c *cfClient) getFromLogCache(ctx context.Context, path string) ([]byte, error) {
	if c.logCacheURL == "" {
		return nil, fmt.Errorf("log cache URL not configured")
	}

	fullURL := c.logCacheURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create log cache request: %w", err)
	}

	token, err := c.auth.getToken(c.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token for log cache: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("log cache request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read log cache response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("log cache %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return body, nil
}

// discoverLogCacheURL resolves the Log Cache endpoint using:
// 1. Explicit cfAccountData.LogCacheURL (if set)
// 2. /v3/info response links.log_cache.href
// 3. Derived from CF API URL: api.X → log-cache.X
func discoverLogCacheURL(httpClient *http.Client, data cfAccountData) string {
	// 1. Explicit configuration
	if data.LogCacheURL != "" {
		return strings.TrimRight(data.LogCacheURL, "/")
	}

	baseURL := strings.TrimRight(data.CFAPIURL, "/")

	// 2. Try /v3/info links.log_cache.href
	resp, err := httpClient.Get(baseURL + "/v3/info")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				var info cfInfo
				if err := json.Unmarshal(body, &info); err == nil && info.Links.LogCache.Href != "" {
					return strings.TrimRight(info.Links.LogCache.Href, "/")
				}
			}
		}
	}

	// 3. Derive from CF API URL pattern: api.X → log-cache.X
	parsed, err := url.Parse(baseURL)
	if err == nil && strings.HasPrefix(parsed.Hostname(), "api.") {
		derived := strings.Replace(parsed.Hostname(), "api.", "log-cache.", 1)
		logCacheURL := fmt.Sprintf("%s://%s", parsed.Scheme, derived)
		return logCacheURL
	}

	return ""
}

// getPaginated fetches all pages from a paginated CF API endpoint.
// decoder is called for each page to extract resources.
func getPaginated[T any](c *cfClient, path string) ([]T, error) {
	var allResources []T
	currentPath := path

	for currentPath != "" {
		body, err := c.get(currentPath)
		if err != nil {
			return allResources, err
		}

		var page struct {
			Pagination cfPagination `json:"pagination"`
			Resources  []T          `json:"resources"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return allResources, fmt.Errorf("failed to parse paginated response: %w", err)
		}

		allResources = append(allResources, page.Resources...)

		if page.Pagination.Next != nil && page.Pagination.Next.Href != "" {
			// Extract path from full URL
			nextURL := page.Pagination.Next.Href
			if strings.HasPrefix(nextURL, c.baseURL) {
				currentPath = strings.TrimPrefix(nextURL, c.baseURL)
			} else {
				currentPath = nextURL
			}
		} else {
			currentPath = ""
		}
	}

	return allResources, nil
}
