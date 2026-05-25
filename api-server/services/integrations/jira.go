package integrations

import (
	"fmt"
	"time"

	"github.com/andygrunwald/go-jira"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	JiraConfigUrl           = "url"
	JiraConfigUsername      = "username"
	JiraConfigPassword      = "password"
	JiraConfigAuthType      = "auth_type"
	JiraConfigProjects      = "projects"
	JiraConfigPriorities    = "priorities"
	JiraConfigLastConnected = "last_connected"
)

func init() {
	core.RegisterIntegration(Jira{})
}

const IntegrationJira = "jira"

type Jira struct{}

func (j Jira) Name() string {
	return IntegrationJira
}

func (j Jira) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (j Jira) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{JiraConfigUrl, JiraConfigUsername, JiraConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			JiraConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "Jira instance URL (e.g., company.atlassian.net)",
			},
			JiraConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "Jira username or email",
			},
			JiraConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "API token or password",
				IsEncrypted: true,
			},
			JiraConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (token or application)",
				Default:     "token",
			},
			JiraConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of Jira projects",
			},
			JiraConfigPriorities: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of Jira priorities",
			},
			JiraConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
			},
		},
	}
}

func (j Jira) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	url := ""
	username := ""
	password := ""

	// Extract config values
	for _, config := range values {
		switch config.Name {
		case JiraConfigUrl:
			url = config.Value
		case JiraConfigUsername:
			username = config.Value
		case JiraConfigPassword:
			password = config.Value
		}
	}

	// Validate required fields
	if url == "" {
		return []error{fmt.Errorf("jira url is required")}
	}
	if username == "" {
		return []error{fmt.Errorf("jira username is required")}
	}
	if password == "" {
		return []error{fmt.Errorf("jira password/token is required")}
	}

	// Test connection by creating client and fetching projects
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}
	client := tp.Client()
	client.Timeout = 15 * time.Second

	jiraClient, err := jira.NewClient(client, "https://"+url)
	if err != nil {
		return []error{fmt.Errorf("failed to create jira client: %w", err)}
	}

	// Try to fetch projects to validate credentials
	apiEndpoint := "rest/api/2/project?startAt=0&maxResults=1"
	req, err := jiraClient.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return []error{fmt.Errorf("failed to create jira request: %w", err)}
	}

	var projects []jira.Project
	_, err = jiraClient.Do(req, &projects)
	if err != nil {
		return []error{fmt.Errorf("jira authentication failed: %w", err)}
	}

	return nil
}
