package integrations

import (
	"fmt"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

const (
	GitlabConfigUrl      = "url"
	GitlabConfigUsername = "username"
	GitlabConfigPassword = "password"
)

func init() {
	core.RegisterIntegration(Gitlab{})
}

const IntegrationGitlab = "gitlab"

type Gitlab struct{}

func (g Gitlab) Name() string {
	return IntegrationGitlab
}

func (g Gitlab) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (g Gitlab) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{GitlabConfigUsername, GitlabConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			GitlabConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "GitLab URL (default: https://gitlab.com)",
				Default:     "https://gitlab.com",
			},
			GitlabConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "GitLab username",
			},
			GitlabConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "Personal access token",
				IsEncrypted: true,
			},
		},
	}
}

func (g Gitlab) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	url := "https://gitlab.com"
	username := ""
	password := ""

	for _, config := range values {
		switch config.Name {
		case GitlabConfigUrl:
			if config.Value != "" {
				url = config.Value
			}
		case GitlabConfigUsername:
			username = config.Value
		case GitlabConfigPassword:
			password = config.Value
		}
	}

	if username == "" {
		return []error{fmt.Errorf("gitlab username is required")}
	}
	if password == "" {
		return []error{fmt.Errorf("gitlab personal access token is required")}
	}

	client, err := gitlab.NewClient(password, gitlab.WithBaseURL(url))
	if err != nil {
		return []error{fmt.Errorf("failed to create gitlab client: %w", err)}
	}

	user, _, err := client.Users.CurrentUser()
	if err != nil {
		return []error{fmt.Errorf("gitlab authentication failed: %w", err)}
	}

	if user.Username != username {
		return []error{fmt.Errorf("gitlab username mismatch: expected %s, got %s", username, user.Username)}
	}

	return nil
}
