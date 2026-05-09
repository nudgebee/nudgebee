package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/common"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/go-github/v67/github"
	"golang.org/x/oauth2"
)

func CreateGithubClient(password string) *github.Client {
	client := github.NewClient(nil).WithAuthToken(password)
	return client
}

func CreateGithubClientWithInstallationToken(ctx context.Context, installationID int64) (*github.Client, error) {
	token, err := GetGithubAppInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)
	return client, nil
}

func GetGithubAppInstallationToken(ctx context.Context, installationID int64) (string, error) {
	appID := common.Config.GithubAppID
	if appID == "" {
		return "", fmt.Errorf("GITHUB_APP_ID environment variable is not set")
	}

	privateKeyPEM := common.Config.GithubPvtKey
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

	// Create installation token
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
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
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.Token, nil
}

func replaceEscapedNewlines(privateKeyPEM string) string {
	return strings.ReplaceAll(privateKeyPEM, `\n`, "\n")
}
