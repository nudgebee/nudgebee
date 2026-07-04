package integrations

import (
	"context"
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
	jiraClient, err := newJiraClient(url, username, password, 15*time.Second)
	if err != nil {
		return []error{err}
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

const (
	jiraUserPageSize = 50
	jiraMaxPages     = 200 // safety cap → up to 10k users
)

// newJiraClient builds a basic-auth Jira client for the given instance with the
// supplied request timeout. Shared by ValidateConfig and ListUsers so the client
// construction lives in one place.
func newJiraClient(url, username, password string, timeout time.Duration) (*jira.Client, error) {
	tp := jira.BasicAuthTransport{Username: username, Password: password}
	httpClient := tp.Client()
	httpClient.Timeout = timeout
	client, err := jira.NewClient(httpClient, "https://"+url)
	if err != nil {
		return nil, fmt.Errorf("jira: failed to create client: %w", err)
	}
	return client, nil
}

// ListUsers enumerates Jira users for identity sync via the bulk users/search
// endpoint. Jira Cloud frequently omits emailAddress (GDPR), in which case the
// account is login-only (manual-map, like GitHub); Server/DC returns the email.
// Implements core.UserLister.
func (j Jira) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	url := core.ConfigValue(values, JiraConfigUrl)
	username := core.ConfigValue(values, JiraConfigUsername)
	password := core.ConfigValue(values, JiraConfigPassword)
	if url == "" || username == "" || password == "" {
		return nil, fmt.Errorf("jira: url, username and password/token are required")
	}

	jiraClient, err := newJiraClient(url, username, password, 20*time.Second)
	if err != nil {
		return nil, err
	}

	var out []core.ExternalUser
	for page := 0; page < jiraMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		endpoint := fmt.Sprintf("rest/api/2/users/search?startAt=%d&maxResults=%d", page*jiraUserPageSize, jiraUserPageSize)
		req, err := jiraClient.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("jira: build request: %w", err)
		}
		var users []jira.User
		if _, err := jiraClient.Do(req, &users); err != nil {
			return nil, fmt.Errorf("jira: list users failed: %w", err)
		}
		if len(users) == 0 {
			break
		}
		for _, u := range users {
			if eu, ok := mapJiraUser(u); ok {
				out = append(out, eu)
			}
		}
		if len(users) < jiraUserPageSize {
			break
		}
	}
	return out, nil
}

// mapJiraUser converts a Jira user to an ExternalUser, skipping app/bot accounts
// and deactivated users (the bulk users/search endpoint returns inactive users
// too) — consistent with the active-only filter the other integrations apply.
// ID prefers accountId (Cloud), falling back to name/key (Server). Email is often
// empty on Cloud → login-only. Pure (no I/O) for unit testing.
func mapJiraUser(u jira.User) (core.ExternalUser, bool) {
	if u.AccountType == "app" || !u.Active {
		return core.ExternalUser{}, false
	}
	id := u.AccountID
	if id == "" {
		id = u.Name
	}
	if id == "" {
		id = u.Key
	}
	if id == "" {
		return core.ExternalUser{}, false
	}
	username := u.Name
	if username == "" {
		username = u.AccountID
	}
	return core.ExternalUser{
		ID:          id,
		Username:    username,
		Email:       u.EmailAddress,
		DisplayName: u.DisplayName,
	}, true
}
