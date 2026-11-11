package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	if client != nil && !isPublicGithub(apiUrl) {
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
	if !isPublicGithub(apiUrl) {
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
		return "", fmt.Errorf("GITHUB_PRIVATE_KEY environment variable is not set")
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

	installationTokenURL, err := buildInstallationTokenURL(apiUrl, installationID)
	if err != nil {
		return "", err
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
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request to GitHub API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create installation token, status: %s, body: %s", resp.Status, string(body))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.Token, nil
}

// replaceEscapedNewlines replaces escaped newline characters with actual newlines.
func replaceEscapedNewlines(privateKeyPEM string) string {
	return strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
}

// ErrGithubAppOnEnterprise is returned when an integration on a host other than
// github.com asks for a token from Nudgebee's GitHub App.
var ErrGithubAppOnEnterprise = errors.New("the Nudgebee GitHub App works only with github.com; for GitHub Enterprise Server use a personal access token")

// buildInstallationTokenURL returns where Nudgebee's GitHub App mints an
// installation token. The request carries a JWT signed with the platform-wide
// GITHUB_PRIVATE_KEY, which can mint tokens for every installation of the App,
// so it only ever goes to github.com. The App is registered on github.com and
// cannot be installed on a GitHub Enterprise Server, so no other host is valid.
func buildInstallationTokenURL(apiUrl string, installationID int64) (string, error) {
	if !isPublicGithub(apiUrl) {
		return "", ErrGithubAppOnEnterprise
	}
	return fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID), nil
}

// isPublicGithub reports whether an integration URL names github.com: empty,
// github.com, www.github.com or api.github.com, with or without scheme or port.
func isPublicGithub(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "www.github.com", "api.github.com":
		return true
	}
	return false
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
