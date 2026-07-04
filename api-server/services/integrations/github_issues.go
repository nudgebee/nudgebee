package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/go-github/v61/github"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	GithubConfigUrl           = "url"
	GithubConfigUsername      = "username"
	GithubConfigPassword      = "password"
	GithubConfigAuthType      = "auth_type"
	GithubConfigProjects      = "projects"
	GithubConfigUsers         = "users"
	GithubConfigLastConnected = "last_connected"
)

func init() {
	core.RegisterIntegration(GithubIssues{})
}

const IntegrationGithubIssues = "github"

type GithubIssues struct{}

func (g GithubIssues) Name() string {
	return IntegrationGithubIssues
}

func (g GithubIssues) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (g GithubIssues) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{GithubConfigUsername, GithubConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			GithubConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "GitHub URL (default: https://github.com)",
				Default:     "https://github.com",
			},
			GithubConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "GitHub username or organization name",
			},
			GithubConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "Personal access token or GitHub App installation ID",
				IsEncrypted: true,
			},
			GithubConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (token or application)",
				Default:     "token",
			},
			GithubConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of GitHub repositories",
			},
			GithubConfigUsers: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of repository collaborators",
			},
			GithubConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
			},
		},
	}
}

func (g GithubIssues) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	username := ""
	password := ""
	authType := "token"

	// Extract config values
	for _, config := range values {
		switch config.Name {
		case GithubConfigUsername:
			username = config.Value
		case GithubConfigPassword:
			password = config.Value
		case GithubConfigAuthType:
			authType = config.Value
		}
	}

	// Validate required fields
	if username == "" {
		return []error{fmt.Errorf("github username is required")}
	}
	if password == "" {
		return []error{fmt.Errorf("github token/installation id is required")}
	}

	var client *github.Client
	if authType == "application" {
		installationID, err := strconv.ParseInt(password, 10, 64)
		if err != nil {
			return []error{fmt.Errorf("invalid GitHub installation ID: %w", err)}
		}

		if installationID <= 0 {
			return []error{fmt.Errorf("installation ID must be positive")}
		}

		return nil
	} else {
		// Token authentication
		client = github.NewClient(nil).WithAuthToken(password)
	}

	user, _, err := client.Users.Get(context.Background(), username)
	if err != nil {
		return []error{fmt.Errorf("github authentication failed: %w", err)}
	}

	if user.Login == nil || username != *user.Login {
		return []error{fmt.Errorf("github username mismatch")}
	}

	return nil
}

// githubRepoUsers mirrors one entry of the GitHub integration's "users" config:
// a repository and the collaborator logins on it, as populated by the metadata sync.
type githubRepoUsers struct {
	Repository string   `json:"repository"`
	Users      []string `json:"users"`
}

// ListUsers enumerates GitHub collaborator logins from the integration's
// already-synced "users" config (one entry per repo). GitHub exposes no email, so
// these are login-only (Email empty). Reading the config — rather than calling the
// GitHub API — sidesteps PAT-vs-App auth, the org-vs-personal-account distinction,
// and the /orgs/{user}/members 404 that personal-account integrations otherwise hit.
// Implements core.UserLister.
func (g GithubIssues) ListUsers(_ context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	raw := core.ConfigValue(values, GithubConfigUsers)
	if raw == "" {
		return nil, nil
	}

	var repos []githubRepoUsers
	if err := json.Unmarshal([]byte(raw), &repos); err != nil {
		return nil, fmt.Errorf("github: parse users config: %w", err)
	}

	seen := make(map[string]bool)
	var out []core.ExternalUser
	for _, r := range repos {
		for _, login := range r.Users {
			if login == "" || seen[login] {
				continue
			}
			seen[login] = true
			out = append(out, core.ExternalUser{ID: login, Username: login, DisplayName: login})
		}
	}
	return out, nil
}
