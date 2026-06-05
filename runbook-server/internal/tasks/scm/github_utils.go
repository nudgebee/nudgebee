package scm

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/safehttp"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v66/github"
	"golang.org/x/oauth2"
)

// CreateGithubClient creates a GitHub client based on the authentication type.
// It supports both "token" authentication and "application" (GitHub App) authentication.
// For GitHub App authentication, the password field contains the installation_id.
// apiUrl parameter supports GitHub Enterprise installations (e.g., "https://github.mycompany.com/api/v3")
func CreateGithubClient(ctx context.Context, apiUrl, authType, username, password string) (*github.Client, error) {
	// SSRF defense: apiUrl is workflow-supplied (for GitHub Enterprise). Empty
	// is fine — go-github defaults to api.github.com. Non-empty must resolve to
	// a public IP. The direct HTTP call in GetGithubAppInstallationToken below
	// also wires a safe transport for DNS-rebinding defense; calls made via the
	// go-github library still inherit the validation here (URL-shape only).
	if apiUrl != "" {
		if err := safehttp.ValidateURL(apiUrl); err != nil {
			return nil, fmt.Errorf("github apiUrl failed safety validation: %w", err)
		}
	}

	var client *github.Client
	var err error

	switch authType {
	case "token":
		// Token-based authentication
		client = github.NewClient(nil).WithAuthToken(password)
	case "application":
		// GitHub App authentication - password contains installation_id
		installationID := int64(0)
		if _, err := fmt.Sscanf(password, "%d", &installationID); err != nil {
			return nil, fmt.Errorf("invalid installation_id in password field: %w", err)
		}
		if installationID == 0 {
			return nil, fmt.Errorf("installation_id is required for GitHub App authentication")
		}
		client, err = CreateGithubClientWithInstallationToken(ctx, apiUrl, installationID)
		if err != nil {
			return nil, err
		}
	default:
		// Basic authentication (legacy)
		tp := github.BasicAuthTransport{
			Username: username,
			Password: password,
		}
		client = github.NewClient(tp.Client())
	}

	// Configure for GitHub Enterprise if apiUrl is provided and not github.com
	if client != nil && apiUrl != "" && !strings.Contains(apiUrl, "api.github.com") {
		client, err = configureEnterpriseURLs(client, apiUrl)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

// CreateGithubClientWithInstallationToken creates a GitHub client using GitHub App installation token.
// apiUrl parameter supports GitHub Enterprise installations.
func CreateGithubClientWithInstallationToken(ctx context.Context, apiUrl string, installationID int64) (*github.Client, error) {
	// SSRF defense — also enforced in GetGithubAppInstallationToken below; this
	// is a second gate for callers that bypass CreateGithubClient.
	if apiUrl != "" {
		if err := safehttp.ValidateURL(apiUrl); err != nil {
			return nil, fmt.Errorf("github apiUrl failed safety validation: %w", err)
		}
	}

	token, err := GetGithubAppInstallationToken(ctx, apiUrl, installationID)
	if err != nil {
		return nil, err
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	// Configure for GitHub Enterprise if apiUrl is provided and not github.com
	if apiUrl != "" && !strings.Contains(apiUrl, "api.github.com") {
		client, err = configureEnterpriseURLs(client, apiUrl)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

// GetGithubAppInstallationToken retrieves an installation access token for a GitHub App.
// apiUrl parameter supports GitHub Enterprise installations.
func GetGithubAppInstallationToken(ctx context.Context, apiUrl string, installationID int64) (string, error) {
	appID := os.Getenv("GITHUB_APP_ID")
	if appID == "" {
		return "", fmt.Errorf("GITHUB_APP_ID environment variable is not set")
	}

	privateKeyPEM := os.Getenv("GITHUB_PRIVATE_KEY")
	if privateKeyPEM == "" {
		if base64Key := os.Getenv("GITHUB_PRIVATE_KEY_BASE64"); base64Key != "" {
			if decodedData, err := base64.StdEncoding.DecodeString(base64Key); err == nil {
				privateKeyPEM = string(decodedData)
			}
		}
		if privateKeyPEM == "" {
			return "", fmt.Errorf("GITHUB_PRIVATE_KEY or GITHUB_PRIVATE_KEY_BASE64 environment variable is not set")
		}
	}

	privateKeyPEM = replaceEscapedNewlines(privateKeyPEM)
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// JWT for GitHub App
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedJWT, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	// Build installation token URL (supports GitHub Enterprise).
	// SSRF defense: validate the constructed URL so even callers that skipped
	// the entry-point checks are gated, and DNS-rebinding is closed via the
	// transport DialContext below.
	installationTokenURL := buildInstallationTokenURL(apiUrl, installationID)
	if err := safehttp.ValidateURL(installationTokenURL); err != nil {
		return "", fmt.Errorf("github installation token url failed safety validation: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", installationTokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+signedJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: safehttp.NewSafeDialContext(30 * time.Second),
		},
		CheckRedirect: safehttp.SafeCheckRedirect,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request to GitHub API: %w", err)
	}
	defer func(Body io.ReadCloser) {
		if closeErr := Body.Close(); closeErr != nil {
			slog.Error("failed to close response body", "error", closeErr)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create installation token, status: %s, body: %s", resp.Status, string(body))
	}

	var result struct {
		Token string `json:"token"`
	}
	body, err := io.ReadAll(resp.Body) // Read the body first
	if err != nil {
		return "", fmt.Errorf("failed to read token response body: %w", err)
	}
	if err := common.UnmarshalJson(body, &result); err != nil { // Use common.UnmarshalJson
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.Token, nil
}

// replaceEscapedNewlines replaces escaped newline characters with actual newlines.
func replaceEscapedNewlines(privateKeyPEM string) string {
	return strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
}

// buildInstallationTokenURL constructs the installation token URL for GitHub or GitHub Enterprise.
func buildInstallationTokenURL(apiUrl string, installationID int64) string {
	// Default to GitHub.com
	if apiUrl == "" || strings.Contains(apiUrl, "api.github.com") {
		return fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	}

	// For GitHub Enterprise, ensure we have the correct API endpoint
	baseURL := strings.TrimSuffix(apiUrl, "/")

	// If the URL doesn't end with /api/v3, append it
	if !strings.HasSuffix(baseURL, "/api/v3") {
		baseURL = baseURL + "/api/v3"
	}

	return fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)
}

// configureEnterpriseURLs configures the GitHub client for Enterprise installations.
func configureEnterpriseURLs(client *github.Client, apiUrl string) (*github.Client, error) {
	baseURL := strings.TrimSuffix(apiUrl, "/")

	// Ensure it ends with /api/v3 for GitHub Enterprise
	if !strings.HasSuffix(baseURL, "/api/v3") {
		baseURL = baseURL + "/api/v3"
	}

	// Add trailing slash for go-github compatibility
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	// Upload URL is typically the same for GitHub Enterprise
	uploadURL := baseURL

	var err error
	client, err = client.WithEnterpriseURLs(baseURL, uploadURL)
	if err != nil {
		return nil, fmt.Errorf("failed to configure GitHub Enterprise URLs: %w", err)
	}

	return client, nil
}
