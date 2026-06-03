package tools

import (
	"context"
	"fmt"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
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
	var installationID int64
	if _, err := fmt.Sscanf(password, "%d", &installationID); err != nil {
		return "", fmt.Errorf("github: invalid installation_id in password field: %w", err)
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
