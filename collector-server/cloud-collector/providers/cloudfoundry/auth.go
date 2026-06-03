package cloudfoundry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cfAuthProvider manages UAA OAuth2 authentication for CF API requests.
type cfAuthProvider struct {
	clientID     string // UAA client ID
	clientSecret string // UAA client secret
	uaaURL       string // UAA endpoint URL

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// newUAAAuth creates an auth provider for UAA OAuth2 client_credentials flow.
func newUAAAuth(clientID, clientSecret, uaaURL string) *cfAuthProvider {
	return &cfAuthProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		uaaURL:       uaaURL,
	}
}

// getToken returns a valid bearer token, refreshing from UAA if needed.
func (a *cfAuthProvider) getToken(httpClient *http.Client) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Return cached token if still valid (with 60s buffer)
	if a.cachedToken != "" && time.Now().Add(60*time.Second).Before(a.tokenExpiry) {
		return a.cachedToken, nil
	}

	// Request new token from UAA
	token, err := a.fetchUAAToken(httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to fetch UAA token: %w", err)
	}

	return token, nil
}

// fetchUAAToken performs the OAuth2 client_credentials grant against UAA.
// It sends credentials both in the form body (URL-encoded) and via Basic auth header
// for maximum compatibility across PCF/TAS UAA versions.
func (a *cfAuthProvider) fetchUAAToken(httpClient *http.Client) (string, error) {
	tokenURL := strings.TrimRight(a.uaaURL, "/") + "/oauth/token"

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create UAA token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Also set Basic auth header (some UAA configs prefer this over form body)
	req.SetBasicAuth(a.clientID, a.clientSecret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("UAA token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read UAA response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("UAA token request returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp uaaTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse UAA token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("UAA returned empty access token")
	}

	a.cachedToken = tokenResp.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return a.cachedToken, nil
}

// invalidateToken clears the cached token, forcing a refresh on next request.
func (a *cfAuthProvider) invalidateToken() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedToken = ""
	a.tokenExpiry = time.Time{}
}

// discoverUAAURL discovers the UAA URL from the CF API.
// It tries /v3/info first (newer CF), then falls back to /v2/info
// (which exposes UAA as "token_endpoint") for older deployments.
func discoverUAAURL(httpClient *http.Client, cfAPIURL string) (string, error) {
	baseURL := strings.TrimRight(cfAPIURL, "/")

	// Try /v3/info first
	uaaURL, v3Err := discoverUAAFromV3Info(httpClient, baseURL)
	if v3Err == nil && uaaURL != "" {
		return uaaURL, nil
	}

	// Fall back to /v2/info (many CF deployments only expose UAA here)
	uaaURL, v2Err := discoverUAAFromV2Info(httpClient, baseURL)
	if v2Err == nil && uaaURL != "" {
		return uaaURL, nil
	}

	return "", fmt.Errorf("UAA URL not found: /v3/info: %v, /v2/info: %v", v3Err, v2Err)
}

func discoverUAAFromV3Info(httpClient *http.Client, baseURL string) (string, error) {
	resp, err := httpClient.Get(baseURL + "/v3/info")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/v3/info returned %d", resp.StatusCode)
	}

	var info cfInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}

	return info.Links.UAA.Href, nil
}

func discoverUAAFromV2Info(httpClient *http.Client, baseURL string) (string, error) {
	resp, err := httpClient.Get(baseURL + "/v2/info")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/v2/info returned %d", resp.StatusCode)
	}

	var v2Info struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(body, &v2Info); err != nil {
		return "", err
	}

	return v2Info.TokenEndpoint, nil
}
