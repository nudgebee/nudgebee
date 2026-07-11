package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

const (
	GitlabConfigUrl      = "url"
	GitlabConfigUsername = "username"
	GitlabConfigPassword = "password"
	GitlabConfigGroup    = "group"
	GitlabConfigProjects = "projects"
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
			GitlabConfigGroup: {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional: a GitLab group (id or full path) to scope user sync to its members (incl. subgroups/inherited). Required for gitlab.com, where listing all users isn't possible; leave empty for self-hosted to list every instance user.",
			},
		},
	}
}

func (g Gitlab) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	url := gitlabDefaultURL
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

	client, err := newGitlabClient(url, password)
	if err != nil {
		return []error{err}
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

const (
	gitlabDefaultURL   = "https://gitlab.com"
	gitlabUserPageSize = 100
	gitlabMaxPages     = 200 // safety cap → up to 20k users
)

// newGitlabClient builds a GitLab API client for the given base URL (defaulting to
// gitlab.com) and personal access token. Shared by ValidateConfig and ListUsers so
// the client construction lives in one place.
func newGitlabClient(baseURL, token string) (*gitlab.Client, error) {
	if baseURL == "" {
		baseURL = gitlabDefaultURL
	}
	client, err := gitlab.NewClient(token, gitlab.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("gitlab: failed to create client: %w", err)
	}
	return client, nil
}

// ListUsers enumerates GitLab users for identity sync. When a group is configured
// it lists that group's members (incl. subgroups/inherited) — the only feasible
// scope on gitlab.com, where listing every user is unbounded; otherwise it lists
// all active instance users (self-hosted). Group members are enriched via the
// user profile for their email (public_email always, private email for admin
// tokens); members with no exposed email stay login-only (manual-map, like
// GitHub). Implements core.UserLister.
func (g Gitlab) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	token := core.ConfigValue(values, GitlabConfigPassword)
	if token == "" {
		return nil, fmt.Errorf("gitlab: personal access token is required")
	}
	client, err := newGitlabClient(core.ConfigValue(values, GitlabConfigUrl), token)
	if err != nil {
		return nil, err
	}

	// Prefer an explicit group; otherwise derive the owning group(s) from the
	// configured projects. Either way, scoping to group members is the only feasible
	// path on gitlab.com (listing every user is unbounded). With neither, fall through
	// to all-users for self-hosted instances.
	if group := core.ConfigValue(values, GitlabConfigGroup); group != "" {
		return listGitlabGroupMembers(ctx, client, []string{group})
	}
	if groups := gitlabGroupsFromProjects(core.ConfigValue(values, GitlabConfigProjects)); len(groups) > 0 {
		return listGitlabGroupMembers(ctx, client, groups)
	}

	var out []core.ExternalUser
	page := 1
	for i := 0; i < gitlabMaxPages && page > 0; i++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		users, resp, err := client.Users.ListUsers(&gitlab.ListUsersOptions{
			ListOptions:        gitlab.ListOptions{PerPage: gitlabUserPageSize, Page: int64(page)},
			Active:             gitlab.Ptr(true),
			WithoutProjectBots: gitlab.Ptr(true),
		}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("gitlab: list users failed: %w", err)
		}
		for _, u := range users {
			if eu, ok := mapGitlabUser(u); ok {
				out = append(out, eu)
			}
		}
		page = int(resp.NextPage)
	}
	return out, nil
}

// mapGitlabUser converts a GitLab user to an ExternalUser, skipping bots/invalid
// rows. Falls back to the public email when the private email isn't exposed; an
// empty email leaves the account login-only. Pure (no I/O) for unit testing.
func mapGitlabUser(u *gitlab.User) (core.ExternalUser, bool) {
	if u == nil || u.ID == 0 || u.Bot {
		return core.ExternalUser{}, false
	}
	email := u.Email
	if email == "" {
		email = u.PublicEmail
	}
	return core.ExternalUser{
		ID:          strconv.FormatInt(u.ID, 10),
		Username:    u.Username,
		Email:       email,
		DisplayName: u.Name,
	}, true
}

// gitlabEmailConcurrency bounds the per-member email lookups so a large group is
// enriched in parallel without hammering the API (gitlab.com permits ~2k req/min).
const gitlabEmailConcurrency = 10

// listGitlabGroupMembers enumerates the members (including subgroup/inherited
// members) of one or more groups — the bounded, gitlab.com-friendly alternative to
// listing every user — deduped across groups by user id. The group-members API
// omits email, so emailless members are enriched from their user profile with
// bounded concurrency: a large group would otherwise become N sequential ~500ms
// round-trips that blow the per-integration timeout.
func listGitlabGroupMembers(ctx context.Context, client *gitlab.Client, groups []string) ([]core.ExternalUser, error) {
	seen := map[string]bool{}
	var out []core.ExternalUser
	for _, group := range groups {
		page := 1
		for i := 0; i < gitlabMaxPages && page > 0; i++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			members, resp, err := client.Groups.ListAllGroupMembers(group, &gitlab.ListGroupMembersOptions{
				ListOptions: gitlab.ListOptions{PerPage: gitlabUserPageSize, Page: int64(page)},
			}, gitlab.WithContext(ctx))
			if err != nil {
				return nil, fmt.Errorf("gitlab: list group members failed for %q: %w", group, err)
			}
			// Map + dedup on this single goroutine (seen/out aren't concurrency-safe);
			// defer emailless members to a bounded concurrent enrichment pass.
			var pendingMembers []*gitlab.GroupMember
			var pendingUsers []core.ExternalUser
			for _, m := range members {
				eu, ok := mapGitlabMember(m)
				if !ok || seen[eu.ID] {
					continue
				}
				seen[eu.ID] = true
				if eu.Email != "" {
					out = append(out, eu)
					continue
				}
				pendingMembers = append(pendingMembers, m)
				pendingUsers = append(pendingUsers, eu)
			}
			out = append(out, enrichGitlabMemberEmails(ctx, client, pendingMembers, pendingUsers)...)
			page = int(resp.NextPage)
		}
	}
	return out, nil
}

// enrichGitlabMemberEmails fills each member's email via GET /users/:id, running the
// lookups with bounded concurrency. Each goroutine writes only its own slice slot
// (disjoint indices), so no further synchronization is needed; a failed or cancelled
// lookup leaves that account login-only.
func enrichGitlabMemberEmails(ctx context.Context, client *gitlab.Client, members []*gitlab.GroupMember, users []core.ExternalUser) []core.ExternalUser {
	sem := make(chan struct{}, gitlabEmailConcurrency)
	var wg sync.WaitGroup
	for i := range members {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			users[i].Email = resolveGitlabMemberEmail(ctx, client, members[i], "")
		}(i)
	}
	wg.Wait()
	return users
}

// resolveGitlabMemberEmail returns current if it's already set; otherwise it
// enriches from the user profile. GitLab's group-members API omits email/
// public_email (blank regardless of token permissions), but GET /users/:id
// returns public_email always and the private email for admin tokens. An empty
// result leaves the account login-only. One extra call per emailless member,
// bounded by the per-integration sync timeout.
func resolveGitlabMemberEmail(ctx context.Context, client *gitlab.Client, m *gitlab.GroupMember, current string) string {
	if current != "" {
		return current
	}
	full, _, err := client.Users.GetUser(m.ID, gitlab.GetUsersOptions{}, gitlab.WithContext(ctx))
	if err != nil {
		return ""
	}
	if mapped, ok := mapGitlabUser(full); ok {
		return mapped.Email
	}
	return ""
}

// gitlabGroupsFromProjects extracts the distinct owning groups from the integration's
// configured projects (JSON [{"key":"group/sub/project"}]) — the group is the project
// path minus its last segment. Projects directly under a user namespace (no group
// prefix) are skipped. Lets gitlab.com user-sync reuse the groups the customer already
// configured for ticketing, without a separate group field.
func gitlabGroupsFromProjects(projectsJSON string) []string {
	projectsJSON = strings.TrimSpace(projectsJSON)
	if projectsJSON == "" || projectsJSON == "null" {
		return nil
	}
	var projects []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(projectsJSON), &projects); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var groups []string
	for _, p := range projects {
		key := strings.Trim(strings.TrimSpace(p.Key), "/")
		idx := strings.LastIndex(key, "/")
		if idx <= 0 {
			continue
		}
		if group := key[:idx]; !seen[group] {
			seen[group] = true
			groups = append(groups, group)
		}
	}
	return groups
}

// mapGitlabMember converts a GitLab group member to an ExternalUser, skipping bots
// (state "blocked_pending_approval"/bot accounts) and invalid rows. Email falls back
// to public_email; empty leaves the account login-only. Pure (no I/O) for testing.
func mapGitlabMember(m *gitlab.GroupMember) (core.ExternalUser, bool) {
	if m == nil || m.ID == 0 {
		return core.ExternalUser{}, false
	}
	email := m.Email
	if email == "" {
		email = m.PublicEmail
	}
	return core.ExternalUser{
		ID:          strconv.FormatInt(m.ID, 10),
		Username:    m.Username,
		Email:       email,
		DisplayName: m.Name,
	}, true
}
