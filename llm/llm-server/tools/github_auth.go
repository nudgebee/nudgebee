package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"os"
	"strings"
)

// extractGithubConfigFields pulls auth_type, url, and password from a github
// integration's ToolConfig values. Password is already decrypted by
// ListToolConfigs for ToolConfigSourceTicket integrations.
func extractGithubConfigFields(values []core.ToolConfigValue) (authType, apiUrl, password string) {
	for _, v := range values {
		switch v.Name {
		case "auth_type":
			authType = v.Value
		case "url":
			apiUrl = v.Value
		case "password":
			password = v.Value
		}
	}
	return
}

// resolveGithubToken returns the token to inject as GITHUB_TOKEN. For
// auth_type=="application", password holds the installation_id and is
// exchanged for a short-lived installation token.
func resolveGithubToken(ctx context.Context, authType, apiUrl, password string) (string, error) {
	if authType != "application" {
		return password, nil
	}
	// Local-dev escape hatch: see the matching one in tool_github.go — a local
	// llm-server has no GitHub App credentials provisioned, so this exchange
	// always fails there even though the tenant's DB config is correct.
	// GITHUB_TOKEN matches the existing local-testing fallback in
	// agent_code2.go's resolveGitToken.
	if localToken := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); localToken != "" {
		return localToken, nil
	}
	var installationID int64
	if _, err := fmt.Sscanf(password, "%d", &installationID); err != nil {
		return "", errors.New("github: invalid installation_id in password field: not a number")
	}
	token, err := utils.GetGithubAppInstallationToken(ctx, apiUrl, installationID)
	if err != nil {
		return "", fmt.Errorf("github: failed to get installation token: %w", err)
	}
	return token, nil
}

// BuildGithubAuth builds the env needed to authenticate `gh` CLI commands in a
// workspace. Returns a CloudAuthResult (the generic auth-carrier type, despite
// the "Cloud" name) so the shell tool can merge it alongside cloud auth.
func BuildGithubAuth(ctx context.Context, cfg core.ToolConfig) (*CloudAuthResult, error) {
	authType, apiUrl, password := extractGithubConfigFields(cfg.Values)
	token, err := resolveGithubToken(ctx, authType, apiUrl, password)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("github: empty token after resolution")
	}
	return &CloudAuthResult{
		Env: map[string]string{"GITHUB_TOKEN": token},
	}, nil
}

// ResolveTenantGithubToken returns the API URL and a token from the tenant's
// first enabled GitHub integration, or empty strings when it has none (public
// repos are then read anonymously). Used to sync skill sources.
func ResolveTenantGithubToken(ctx context.Context, tenantId string) (string, string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", "", err
	}
	var row struct {
		URL      *string `db:"url"`
		Password *string `db:"password"`
		AuthType *string `db:"auth_type"`
	}
	err = dbms.Db.Get(&row, `
		SELECT
			MAX(CASE WHEN icv.name = 'url' THEN icv.value END) AS url,
			MAX(CASE WHEN icv.name = 'password' THEN icv.value END) AS password,
			MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) AS auth_type
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.tenant_id = $1 AND i.status = 'enabled' AND i.type = 'github'
		GROUP BY i.id, i.created_at
		ORDER BY i.created_at
		LIMIT 1`, tenantId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("github: read integration: %w", err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	password := deref(row.Password)
	if password != "" {
		if password, err = common.Decrypt(password); err != nil {
			return "", "", fmt.Errorf("github: decrypt token: %w", err)
		}
	}
	token, err := resolveGithubToken(ctx, deref(row.AuthType), deref(row.URL), password)
	if err != nil {
		return "", "", err
	}
	return githubAPIBase(deref(row.URL)), token, nil
}

// githubAPIBase maps an integration's URL to the REST API base: github.com
// (or no URL) is api.github.com; a GitHub Enterprise host gets /api/v3, the
// same rule the installation-token exchange uses.
func githubAPIBase(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	if u == "" || strings.Contains(u, "api.github.com") || strings.HasSuffix(u, "://github.com") {
		return "https://api.github.com"
	}
	if !strings.HasSuffix(u, "/api/v3") {
		u += "/api/v3"
	}
	return u
}

// GithubConfigFields returns a github integration's auth type, API URL and
// password (a token, or the installation id for a GitHub App).
func GithubConfigFields(cfg core.ToolConfig) (authType, apiUrl, password string) {
	return extractGithubConfigFields(cfg.Values)
}

// ResolveGithubToken returns the token for a github integration, minting a
// GitHub App installation token when authType is "application".
func ResolveGithubToken(ctx context.Context, authType, apiUrl, password string) (string, error) {
	return resolveGithubToken(ctx, authType, apiUrl, password)
}
