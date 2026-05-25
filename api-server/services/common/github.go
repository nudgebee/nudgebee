package common

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/config"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// GetGithubAppInstallationToken generates an installation access token for a GitHub App
// installationID can be either int64, int, or string (will be converted)
func GetGithubAppInstallationToken(ctx context.Context, installationID any) (string, error) {
	// Convert installationID to int64
	var instID int64
	var err error
	switch v := installationID.(type) {
	case int64:
		instID = v
	case string:
		instID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid installation ID: %w", err)
		}
	case int:
		instID = int64(v)
	default:
		return "", fmt.Errorf("installation ID must be int64, int, or string, got %T", installationID)
	}

	appID := config.Config.GithubAppId
	if appID == "" {
		return "", fmt.Errorf("GITHUB_APP_ID is not configured")
	}

	privateKeyPEM := config.Config.GithubPrivateKey
	if privateKeyPEM == "" {
		return "", fmt.Errorf("GITHUB_PRIVATE_KEY is not configured")
	}

	// Replace escaped newlines (\\n) with actual newlines
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Create JWT for GitHub App authentication
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // Issued 60 seconds in the past to account for clock drift
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedJWT, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	// Request installation access token from GitHub
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", instID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
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
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create installation token, status: %s, body: %s", resp.Status, string(body))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := UnmarshalJson(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.Token, nil
}
