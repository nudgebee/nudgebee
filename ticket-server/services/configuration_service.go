package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	pagerduty "github.com/PagerDuty/go-pagerduty"
	"github.com/andygrunwald/go-jira"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v67/github"
	servicenowsdkgo "github.com/michaeldcanady/servicenow-sdk-go"
	"github.com/michaeldcanady/servicenow-sdk-go/credentials"
	tableapi "github.com/michaeldcanady/servicenow-sdk-go/table-api"
	gitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/database"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/tools"
)

// ValidateAndGetMetadata is the backward-compatible wrapper that accepts *gin.Context.
func ValidateAndGetMetadata(ctx *gin.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	return ValidateAndGetMetadataWithContext(ctx, configuration)
}

// ValidateAndGetMetadataWithContext validates credentials and fetches full metadata (projects, priorities, users).
// Accepts context.Context so it can be called from both HTTP handlers and background goroutines.
func ValidateAndGetMetadataWithContext(ctx context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	validationFunctions := map[string]func(context.Context, models.TicketConfigurations) ([]map[string]interface{}, error){
		"jira":       validateJiraConfigurationAndReturnMetadata,
		"github":     validateGithubConfigurationAndReturnMetadata,
		"gitlab":     validateGitLabConfigurationAndReturnMetadata,
		"servicenow": validateServiceNowConfigurationAndReturnMetadata,
		"pagerduty":  validatePagerDutyConfigurationAndReturnMetadata,
		"zenduty":    validateZenDutyConfigurationAndReturnMetadata,
	}

	if validateFunc, exists := validationFunctions[configuration.Tool]; exists {
		return validateFunc(ctx, configuration)
	}
	return nil, fmt.Errorf("invalid tool: %s (supported tools: jira, github, gitlab, servicenow, pagerduty, zenduty)", configuration.Tool)
}

// LoadExistingPassword returns the decrypted password for an existing integration if one is
// found by id or by (tenant, name, tool). Returns ("", false, nil) when no match exists.
// Real database errors are propagated so the caller can distinguish "not found" from
// "lookup failed" — never returned as a missing password.
// On edit flows the frontend may omit the password (leaving the stored secret untouched);
// callers use this to rehydrate the plaintext so validation can succeed.
func LoadExistingPassword(id, tenant, name, tool string) (string, bool, error) {
	if tenant == "" {
		return "", false, nil
	}
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return "", false, err
	}

	// Use Select (slice) rather than Get to avoid sql.ErrNoRows on empty results;
	// a zero-length slice is "not found" and a non-nil error is always a real DB failure.
	var ids []string
	if id != "" {
		if err := dbManager.Select(&ids, `SELECT id FROM integrations WHERE id = $1 AND tenant_id = $2 LIMIT 1`, id, tenant); err != nil {
			return "", false, fmt.Errorf("lookup integration by id: %w", err)
		}
	}
	if len(ids) == 0 && name != "" && tool != "" {
		if err := dbManager.Select(&ids, `
			SELECT id FROM integrations
			WHERE tenant_id = $1 AND name = $2 AND type = $3
			LIMIT 1
		`, tenant, name, tool); err != nil {
			return "", false, fmt.Errorf("lookup integration by tenant/name: %w", err)
		}
	}
	if len(ids) == 0 {
		return "", false, nil
	}

	var encrypted []string
	if err := dbManager.Select(&encrypted, `
		SELECT value FROM integration_config_values
		WHERE integration_id = $1 AND name = 'password'
		LIMIT 1
	`, ids[0]); err != nil {
		return "", false, fmt.Errorf("lookup stored password: %w", err)
	}
	if len(encrypted) == 0 || encrypted[0] == "" {
		return "", false, nil
	}

	plaintext, err := common.Decrypt(encrypted[0])
	if err != nil {
		return "", false, fmt.Errorf("decrypt stored password: %w", err)
	}
	return plaintext, true, nil
}

// QuickValidateCredentials performs a lightweight auth-only check for each tool.
// It verifies credentials are valid without fetching all repos/projects/users.
func QuickValidateCredentials(ctx context.Context, configuration models.TicketConfigurations) error {
	switch configuration.Tool {
	case "github":
		return quickValidateGithub(ctx, configuration)
	case "jira":
		return quickValidateJira(configuration)
	case "gitlab":
		return quickValidateGitLab(configuration)
	case "servicenow":
		return quickValidateServiceNow(ctx, configuration)
	case "pagerduty":
		return quickValidatePagerDuty(ctx, configuration)
	case "zenduty":
		return quickValidateZenDuty(ctx, configuration)
	default:
		return fmt.Errorf("invalid tool: %s (supported tools: jira, github, gitlab, servicenow, pagerduty, zenduty)", configuration.Tool)
	}
}

func quickValidateGithub(ctx context.Context, configuration models.TicketConfigurations) error {
	if configuration.AuthType != "application" {
		client := clients.CreateGithubClient(configuration.Password)
		user, _, err := client.Users.Get(ctx, configuration.Username)
		if err != nil || user.Login == nil || configuration.Username != *user.Login {
			return fmt.Errorf("github auth failed for user %s: %w", configuration.Username, err)
		}
		return nil
	}

	installationID, err := strconv.ParseInt(configuration.Password, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid github app installation ID: %w", err)
	}
	client, err := clients.CreateGithubClientWithInstallationToken(ctx, installationID)
	if err != nil {
		return fmt.Errorf("github app auth failed: %w", err)
	}
	// Verify by listing 1 repo
	_, _, err = client.Repositories.ListByOrg(ctx, configuration.Username, &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return fmt.Errorf("github app failed to list repos for org %s: %w", configuration.Username, err)
	}
	return nil
}

func quickValidateJira(configuration models.TicketConfigurations) error {
	if configuration.URL == "" || configuration.Username == "" || configuration.Password == "" {
		return fmt.Errorf("jira url, username and api token are all required")
	}
	client, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		return fmt.Errorf("jira client creation failed: %w", err)
	}
	// `/rest/api/2/project` returns 200 with public projects on instances that
	// permit anonymous reads, so it can't distinguish "valid creds + 0 projects"
	// from "no creds at all". `/myself` is the canonical "who am I" endpoint —
	// it requires authenticated identity and returns 401 otherwise.
	req, err := client.NewRequest("GET", "rest/api/2/myself", nil)
	if err != nil {
		return fmt.Errorf("jira request creation failed: %w", err)
	}
	var me struct {
		AccountID    string `json:"accountId"`
		EmailAddress string `json:"emailAddress"`
		DisplayName  string `json:"displayName"`
		Name         string `json:"name"`
		Active       bool   `json:"active"`
	}
	if _, err = client.Do(req, &me); err != nil {
		return fmt.Errorf("jira auth failed: %w", err)
	}
	// Defense-in-depth: a 200 with an empty body would otherwise pass. Require
	// some user identity (accountId on Cloud, name on Server/DC).
	if me.AccountID == "" && me.Name == "" {
		return fmt.Errorf("jira auth failed: empty user response (credentials likely invalid)")
	}
	// On Jira Cloud the API token is bound to a specific email — accepting a
	// stranger's token under your own username is what the user is hitting. If
	// the email is visible (privacy setting permitting), require it to match.
	// When the email is hidden we can't enforce this, but the /myself success
	// already proves the token owner authenticated; the username is only used
	// to scope ticket attribution in our DB rows after that.
	if me.EmailAddress != "" && !strings.EqualFold(me.EmailAddress, configuration.Username) {
		return fmt.Errorf("jira authenticated user '%s' does not match configured username '%s'", me.EmailAddress, configuration.Username)
	}
	return nil
}

func quickValidateGitLab(configuration models.TicketConfigurations) error {
	gitlabClient, err := clients.CreateGitLabClient(configuration.Password, configuration.URL)
	if err != nil {
		return fmt.Errorf("gitlab client creation failed: %w", err)
	}
	_, _, err = gitlabClient.Projects.ListProjects(&gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 1},
		Membership:  gitlab.Ptr(true),
	})
	if err != nil {
		return fmt.Errorf("gitlab auth failed (URL: %s): %w", configuration.URL, err)
	}
	return nil
}

func quickValidateServiceNow(ctx context.Context, configuration models.TicketConfigurations) error {
	cred := credentials.NewBasicProvider(configuration.Username, configuration.Password)
	baseURL := "https://" + strings.TrimPrefix(configuration.URL, "https://")
	client, err := servicenowsdkgo.NewServiceNowServiceClient(
		servicenowsdkgo.WithAuthenticationProvider(cred),
		servicenowsdkgo.WithURL(baseURL),
	)
	if err != nil {
		return fmt.Errorf("servicenow client creation failed: %w", err)
	}
	tableBuilder := tableapi.NewDefaultTableRequestBuilder2Internal(map[string]string{
		"baseurl": baseURL,
		"table":   "incident",
	}, client.GetRequestAdapter())
	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{Limit: 1},
	}
	if _, err := tableBuilder.Get(ctx, getConfig); err != nil {
		// The servicenow-sdk-go library has no error factory for non-2xx
		// statuses, so its raw error reads "no error factory is registered
		// for this code: 401". Surface a user-actionable message instead.
		return interpretServiceNowError(err)
	}
	return nil
}

// interpretServiceNowError translates the SDK's terse status-code error into
// something a user can act on. Falls back to the raw error when the status
// code isn't one we recognize.
func interpretServiceNowError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401"):
		return fmt.Errorf("servicenow authentication failed: invalid username or password")
	case strings.Contains(msg, "403"):
		return fmt.Errorf("servicenow authentication failed: user lacks permission to read the incident table")
	case strings.Contains(msg, "404"):
		return fmt.Errorf("servicenow connection failed: instance URL not found (check the URL field)")
	default:
		return fmt.Errorf("servicenow auth failed: %w", err)
	}
}

func quickValidatePagerDuty(ctx context.Context, configuration models.TicketConfigurations) error {
	if configuration.Password == "" {
		return fmt.Errorf("pagerduty api key is required")
	}
	// Pass the user-entered URL into the client so a typo in the form fails
	// validation here instead of being masked by the SDK's hardcoded endpoint.
	client := clients.CreatePagerdutyClientWithURL(configuration.Password, configuration.URL)
	if _, err := client.ListServicesWithContext(ctx, pagerduty.ListServiceOptions{Limit: 1}); err != nil {
		return fmt.Errorf("pagerduty auth failed: %w", err)
	}
	// Username is required: it's used as the "From" email on every
	// ManageIncidents / CreateIncident call. Without this check, a bad email
	// only fails later at incident-creation time. We confirm it resolves to a
	// real user via PagerDuty's `?query=` filter and an exact (case-insensitive)
	// email match, since `query` is a fuzzy substring search.
	if configuration.Username == "" {
		return fmt.Errorf("pagerduty username (email) is required")
	}
	users, err := client.ListUsersWithContext(ctx, pagerduty.ListUsersOptions{Query: configuration.Username, Limit: 25})
	if err != nil {
		return fmt.Errorf("pagerduty user lookup failed: %w", err)
	}
	for _, u := range users.Users {
		if strings.EqualFold(u.Email, configuration.Username) {
			return nil
		}
	}
	return fmt.Errorf("pagerduty user '%s' not found in this account", configuration.Username)
}

func quickValidateZenDuty(ctx context.Context, configuration models.TicketConfigurations) error {
	if configuration.Password == "" {
		return fmt.Errorf("zenduty api token is required")
	}
	client := clients.CreateZenDutyClient(configuration.Password)
	if _, err := client.ListTeams(ctx); err != nil {
		return fmt.Errorf("zenduty auth failed: %w", err)
	}
	// Email is stored on the integration row and used as the actor for downstream
	// ticket attribution. Token-based auth doesn't bind to an email, so without
	// this check a typo email + any valid token would be accepted silently.
	if configuration.Username != "" {
		users, err := client.ListUsers(ctx)
		if err != nil {
			return fmt.Errorf("zenduty user lookup failed: %w", err)
		}
		for _, u := range users {
			if strings.EqualFold(u.User.Email, configuration.Username) {
				return nil
			}
		}
		return fmt.Errorf("zenduty user '%s' not found in this account", configuration.Username)
	}
	return nil
}

// PopulateMetadataAsync fetches full metadata in the background and updates the DB.
// It runs detached from the HTTP request context with its own timeout.
func PopulateMetadataAsync(integrationID string, configuration models.TicketConfigurations) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in background metadata sync", "integration_id", integrationID, "tool", configuration.Tool, "panic", r)
				_ = upsertConfigValue(integrationID, "metadata_sync_status", fmt.Sprintf("failed: panic: %v", r), false, configuration.CreatedBy)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		slog.Info("Starting background metadata sync", "integration_id", integrationID, "tool", configuration.Tool)

		// Set status to syncing
		if err := upsertConfigValue(integrationID, "metadata_sync_status", "syncing", false, configuration.CreatedBy); err != nil {
			slog.Error("Failed to set metadata_sync_status to syncing", "integration_id", integrationID, "error", err)
			return
		}

		metadata, err := ValidateAndGetMetadataWithContext(ctx, configuration)
		if err != nil {
			slog.Error("Background metadata sync failed", "integration_id", integrationID, "tool", configuration.Tool, "error", err)
			_ = upsertConfigValue(integrationID, "metadata_sync_status", fmt.Sprintf("failed: %v", err), false, configuration.CreatedBy)
			return
		}

		if err := updateMetadataConfigValues(integrationID, metadata, configuration.CreatedBy); err != nil {
			slog.Error("Failed to persist background metadata", "integration_id", integrationID, "error", err)
			_ = upsertConfigValue(integrationID, "metadata_sync_status", fmt.Sprintf("failed: %v", err), false, configuration.CreatedBy)
			return
		}

		_ = upsertConfigValue(integrationID, "metadata_sync_status", "synced", false, configuration.CreatedBy)
		slog.Info("Background metadata sync completed", "integration_id", integrationID, "tool", configuration.Tool)
	}()
}

// upsertConfigValue upserts a single config value for an integration.
func upsertConfigValue(integrationID, name, value string, isEncrypted bool, createdBy *string) error {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}
	_, err = dbManager.Exec(`
		INSERT INTO integration_config_values (
			id, integration_id, name, value, is_encrypted,
			created_at, updated_at, created_by, updated_by
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW(), NOW(), $5, $5)
		ON CONFLICT (integration_id, name)
		DO UPDATE SET value = EXCLUDED.value, updated_at = NOW(), updated_by = EXCLUDED.updated_by
	`, integrationID, name, value, isEncrypted, createdBy)
	if err != nil {
		return fmt.Errorf("failed to upsert config value %s: %w", name, err)
	}
	return nil
}

// updateMetadataConfigValues persists projects, priorities, users, and last_connected from metadata.
func updateMetadataConfigValues(integrationID string, metadata []map[string]interface{}, createdBy *string) error {
	var projects []models.Project
	var priorities []models.Priority
	var users interface{}

	for _, entry := range metadata {
		if v, ok := entry["projects"].([]models.Project); ok {
			projects = v
		}
		if v, ok := entry["priorities"].([]models.Priority); ok {
			priorities = v
		}
		if v, ok := entry["users"]; ok {
			users = v
		}
	}

	projectsJSON, err := json.Marshal(projects)
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}
	prioritiesJSON, err := json.Marshal(priorities)
	if err != nil {
		return fmt.Errorf("failed to marshal priorities: %w", err)
	}
	usersJSON, err := json.Marshal(users)
	if err != nil {
		return fmt.Errorf("failed to marshal users: %w", err)
	}
	lastConnected := time.Now().Format(time.RFC3339)

	configEntries := map[string]string{
		"projects":       string(projectsJSON),
		"priorities":     string(prioritiesJSON),
		"users":          string(usersJSON),
		"last_connected": lastConnected,
	}

	for name, value := range configEntries {
		if err := upsertConfigValue(integrationID, name, value, false, createdBy); err != nil {
			return err
		}
	}
	return nil
}

func validateJiraConfigurationAndReturnMetadata(_ context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	client, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Warn("Jira client creation failed", "error", err)
		return nil, err
	}

	// Fetch all projects with pagination
	projects := make([]models.Project, 0)
	startAt := 0
	maxResults := 50
	for {
		apiEndpoint := fmt.Sprintf("rest/api/2/project?startAt=%d&maxResults=%d", startAt, maxResults)
		req, err := client.NewRequest("GET", apiEndpoint, nil)
		if err != nil {
			slog.Warn("Failed to create Jira request for projects", "error", err)
			return nil, err
		}

		var projectPage []jira.Project
		_, err = client.Do(req, &projectPage)
		if err != nil {
			slog.Warn("Failed to fetch Jira projects", "error", err)
			return nil, err
		}

		if len(projectPage) == 0 {
			break
		}

		for _, p := range projectPage {
			projects = append(projects, models.Project{Name: p.Name, Key: p.Key})
		}

		if len(projectPage) < maxResults {
			break
		}

		startAt += len(projectPage)
	}

	priorities := make([]models.Priority, 0)
	priorityList, _, err := client.Priority.GetList()
	if err != nil {
		slog.Warn("Failed to fetch Jira priorities", "error", err)
		return nil, err
	}
	for _, p := range priorityList {
		priorities = append(priorities, models.Priority{Name: p.Name, ID: p.ID})
	}

	return []map[string]interface{}{
		{"projects": projects},
		{"priorities": priorities},
	}, nil
}

func validateGithubConfigurationAndReturnMetadata(ctx context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	var client *github.Client
	var err error

	if configuration.AuthType != "application" {
		client = clients.CreateGithubClient(configuration.Password)
	} else {
		installationIDStr := configuration.Password
		installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
		if err != nil {
			return nil, err
		}
		client, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return nil, err
		}
	}

	var repositories []*github.Repository
	if configuration.AuthType != "application" {
		var user *github.User
		user, _, err = client.Users.Get(ctx, configuration.Username)
		if err != nil || user.Login == nil || configuration.Username != *user.Login {
			return nil, fmt.Errorf("github auth failed for user %s: %w", configuration.Username, err)
		}

		opts := &github.RepositoryListByAuthenticatedUserOptions{
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			repos, resp, err := client.Repositories.ListByAuthenticatedUser(ctx, opts)
			if err != nil {
				slog.Error("Failed to fetch GitHub repositories", "error", err)
				return nil, err
			}
			repositories = append(repositories, repos...)

			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	} else {
		opts := &github.RepositoryListByOrgOptions{
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			repos, resp, err := client.Repositories.ListByOrg(ctx, configuration.Username, opts)
			if err != nil {
				slog.Error("Failed to fetch GitHub repositories", "error", err)
				return nil, err
			}
			repositories = append(repositories, repos...)

			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}

	var projects []models.Project
	var users []map[string]interface{}

	for _, repo := range repositories {
		if repo.HasProjects != nil && *repo.HasProjects {
			var repoUsers []string

			// Try to fetch collaborators, but don't fail if permissions are insufficient
			collabOpts := &github.ListCollaboratorsOptions{
				ListOptions: github.ListOptions{PerPage: 100},
			}
			for {
				collabs, resp, err := client.Repositories.ListCollaborators(ctx, *repo.Owner.Login, *repo.Name, collabOpts)
				if err != nil {
					slog.Warn("Failed to fetch GitHub collaborators (may lack permission)", "repo", *repo.Name, "error", err)
					break
				}
				for _, u := range collabs {
					if u.Login != nil {
						repoUsers = append(repoUsers, *u.Login)
					}
				}

				if resp.NextPage == 0 {
					break
				}
				collabOpts.Page = resp.NextPage
			}

			users = append(users, map[string]interface{}{
				"repository": *repo.FullName,
				"users":      repoUsers,
			})
			projects = append(projects, models.Project{Name: *repo.Name, Key: *repo.FullName})
		}
	}

	return []map[string]interface{}{
		{"projects": projects},
		{"users": users},
	}, nil
}

func validateServiceNowConfigurationAndReturnMetadata(ctx context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	cred := credentials.NewBasicProvider(configuration.Username, configuration.Password)
	baseURL := "https://" + strings.TrimPrefix(configuration.URL, "https://")
	client, err := servicenowsdkgo.NewServiceNowServiceClient(
		servicenowsdkgo.WithAuthenticationProvider(cred),
		servicenowsdkgo.WithURL(baseURL),
	)
	if err != nil {
		slog.Error("ServiceNow client creation failed", "error", err)
		return nil, err
	}

	tableBuilder := tableapi.NewDefaultTableRequestBuilder2Internal(map[string]string{
		"baseurl": baseURL,
		"table":   "incident",
	}, client.GetRequestAdapter())

	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{Limit: 1},
	}
	if _, err := tableBuilder.Get(ctx, getConfig); err != nil {
		slog.Error("Failed to query ServiceNow table", "error", err)
		return nil, err
	}

	return []map[string]interface{}{
		{"projects": []models.Project{{Name: "Incident", Key: "incident"}}},
		{"priorities": []models.Priority{}},
	}, nil
}

func validatePagerDutyConfigurationAndReturnMetadata(ctx context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	client := clients.CreatePagerdutyClientWithURL(configuration.Password, configuration.URL)

	services, err := client.ListServicesWithContext(ctx, pagerduty.ListServiceOptions{})
	if err != nil {
		slog.Error("Failed to fetch PagerDuty services", "error", err)
		return nil, err
	}

	var projects []models.Project
	for _, s := range services.Services {
		projects = append(projects, models.Project{Name: s.Name, Key: s.ID})
	}

	return []map[string]interface{}{
		{"projects": projects},
		{"priorities": []models.Priority{}},
	}, nil
}

func validateZenDutyConfigurationAndReturnMetadata(ctx context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	client := clients.CreateZenDutyClient(configuration.Password)

	services, teams, err := client.ListAllServicesWithTeams(ctx)
	if err != nil {
		slog.Error("Failed to fetch ZenDuty services", "error", err)
		return nil, err
	}

	// Build projects list with team names for better identification
	var projects []models.Project
	for _, s := range services {
		// Format: "Service Name (Team Name)" for better UX
		displayName := fmt.Sprintf("%s (%s)", s.Name, s.TeamName)
		projects = append(projects, models.Project{Name: displayName, Key: s.UniqueID})
	}

	// ZenDuty urgency levels as priorities
	priorities := []models.Priority{
		{ID: "0", Name: "Low"},
		{ID: "1", Name: "Medium"},
		{ID: "2", Name: "High"},
	}

	// Store teams as users for reference
	var users []map[string]string
	for _, t := range teams {
		users = append(users, map[string]string{
			"id":   t.UniqueID,
			"name": t.Name,
		})
	}

	return []map[string]interface{}{
		{"projects": projects},
		{"priorities": priorities},
		{"users": users},
	}, nil
}

func validateGitLabConfigurationAndReturnMetadata(_ context.Context, configuration models.TicketConfigurations) ([]map[string]interface{}, error) {
	gitlabClient, err := clients.CreateGitLabClient(configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("GitLab client creation failed", "error", err)
		return nil, err
	}

	// List accessible projects with pagination
	var projects []models.Project
	opts := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
		Membership:  gitlab.Ptr(true), // Only projects user has access to
	}

	for {
		projectPage, resp, err := gitlabClient.Projects.ListProjects(opts)
		if err != nil {
			slog.Error("Failed to fetch GitLab projects", "error", err)
			return nil, fmt.Errorf("GitLab failed to list projects (URL: %s): %w", configuration.URL, err)
		}

		for _, p := range projectPage {
			projects = append(projects, models.Project{
				Name: p.NameWithNamespace,
				Key:  p.PathWithNamespace, // Use path for identification (e.g., "group/project")
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return []map[string]interface{}{
		{"projects": projects},
		{"priorities": []models.Priority{}}, // GitLab doesn't have Jira-style priorities
	}, nil
}

func SaveTicketConfiguration(config models.TicketConfigurations, metadata []map[string]interface{}) (models.TicketIntegrationCreateResponse, error) {
	var response models.TicketIntegrationCreateResponse

	// Reserved config keys that cannot be overwritten via config_values
	// These are system-managed and may contain sensitive/encrypted data
	reservedConfigKeys := map[string]bool{
		"url":                  true,
		"username":             true,
		"password":             true,
		"auth_type":            true,
		"projects":             true,
		"priorities":           true,
		"users":                true,
		"last_connected":       true,
		"metadata_sync_status": true,
	}

	var (
		projects   []models.Project
		priorities []models.Priority
		users      interface{}
	)

	// Parse metadata
	for _, entry := range metadata {
		if v, ok := entry["projects"].([]models.Project); ok {
			projects = v
		} else if entry["projects"] != nil {
			return response, fmt.Errorf("invalid type for 'projects': expected []Project, got %T", entry["projects"])
		}
		if v, ok := entry["priorities"].([]models.Priority); ok {
			priorities = v
		} else if entry["priorities"] != nil {
			return response, fmt.Errorf("invalid type for 'priorities': expected []Priority, got %T", entry["priorities"])
		}
		if v, ok := entry["users"]; ok {
			users = v
		}
	}

	// Encrypt password for storage
	encryptedPassword := config.Password
	if config.Password != "" {
		encrypted, err := common.Encrypt(config.Password)
		if err != nil {
			return response, fmt.Errorf("encrypt password: %w", err)
		}
		encryptedPassword = encrypted
	}

	// Get database manager
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return response, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Prepare config values
	projectsJson, err := json.Marshal(projects)
	if err != nil {
		return response, fmt.Errorf("failed to marshal projects: %w", err)
	}
	prioritiesJson, err := json.Marshal(priorities)
	if err != nil {
		return response, fmt.Errorf("failed to marshal priorities: %w", err)
	}
	usersJson, err := json.Marshal(users)
	if err != nil {
		return response, fmt.Errorf("failed to marshal users: %w", err)
	}
	now := time.Now()

	var integrationID string
	var foundExisting bool

	// Check if integration exists by ID and belongs to current tenant
	if config.ID != "" && config.Tenant != "" {
		var count int
		err := dbManager.Get(&count, `SELECT COUNT(*) FROM integrations WHERE id = $1 AND tenant_id = $2`, config.ID, config.Tenant)
		if err == nil && count > 0 {
			integrationID = config.ID
			foundExisting = true
		}
	}

	// If not found by ID, check by tenant and name
	if !foundExisting && config.Tenant != "" && config.Name != "" {
		err := dbManager.Get(&integrationID, `
			SELECT id FROM integrations
			WHERE tenant_id = $1 AND name = $2 AND type = $3
			LIMIT 1
		`, config.Tenant, config.Name, config.Tool)
		if err == nil {
			foundExisting = true
			slog.Info("Found existing configuration by tenant and name, will update",
				"tenant", config.Tenant, "name", config.Name, "id", integrationID)
		}
	}

	// Set default URL for GitHub if not provided
	if config.Tool == "github" && config.URL == "" {
		config.URL = "https://api.github.com"
	}

	// If existing configuration found, update it
	if foundExisting {
		// Update integration metadata (include tenant_id check for defense in depth)
		_, err := dbManager.Exec(`
			UPDATE integrations
			SET name = $1, status = $2, updated_at = NOW(), updated_by = $3
			WHERE id = $4 AND tenant_id = $5
		`, config.Name, "enabled", config.CreatedBy, integrationID, config.Tenant)
		if err != nil {
			return response, fmt.Errorf("failed to update integration: %w", err)
		}

		// Upsert config values
		configValues := map[string]struct {
			value       string
			isEncrypted bool
		}{
			"url":            {config.URL, false},
			"username":       {config.Username, false},
			"password":       {encryptedPassword, true},
			"auth_type":      {config.AuthType, false},
			"projects":       {string(projectsJson), false},
			"priorities":     {string(prioritiesJson), false},
			"users":          {string(usersJson), false},
			"last_connected": {now.Format(time.RFC3339), false},
		}

		for name, cv := range configValues {
			if cv.value == "" && name != "password" {
				continue
			}
			_, err := dbManager.Exec(`
				INSERT INTO integration_config_values (
					id, integration_id, name, value, is_encrypted,
					created_at, updated_at, created_by, updated_by
				)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW(), NOW(), $5, $5)
				ON CONFLICT (integration_id, name)
				DO UPDATE SET value = EXCLUDED.value, updated_at = NOW(), updated_by = EXCLUDED.updated_by
			`, integrationID, name, cv.value, cv.isEncrypted, config.CreatedBy)
			if err != nil {
				return response, fmt.Errorf("failed to upsert config value %s: %w", name, err)
			}
		}

		// Save additional config values from request (skip reserved keys to prevent security bypass)
		for _, cv := range config.ConfigValues {
			if cv.Name == "" || cv.Value == "" {
				continue
			}
			// Prevent overwriting reserved/sensitive config keys
			if reservedConfigKeys[cv.Name] {
				slog.Warn("Attempted to set reserved config key via config_values, skipping",
					"key", cv.Name, "integration_id", integrationID)
				continue
			}
			_, err := dbManager.Exec(`
				INSERT INTO integration_config_values (
					id, integration_id, name, value, is_encrypted,
					created_at, updated_at, created_by, updated_by
				)
				VALUES (gen_random_uuid(), $1, $2, $3, false, NOW(), NOW(), $4, $4)
				ON CONFLICT (integration_id, name)
				DO UPDATE SET value = EXCLUDED.value, is_encrypted = EXCLUDED.is_encrypted, updated_at = NOW(), updated_by = EXCLUDED.updated_by
			`, integrationID, cv.Name, cv.Value, config.CreatedBy)
			if err != nil {
				return response, fmt.Errorf("failed to save additional config value %s: %w", cv.Name, err)
			}
		}

		response.ID = integrationID
		return response, nil
	}

	// No existing configuration found, insert new one
	if config.AuthType == "" {
		config.AuthType = "token"
	}

	// Insert integration
	err = dbManager.Get(&integrationID, `
		INSERT INTO integrations (
			id, tenant_id, type, source, name, status,
			created_at, updated_at, created_by, updated_by, labels
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NOW(), NOW(), $6, $6, '{}'::json)
		RETURNING id
	`, config.Tenant, config.Tool, "user", config.Name, "enabled", config.CreatedBy)
	if err != nil {
		slog.Error("Failed to insert integration", "error", err)
		return response, err
	}

	// Insert config values
	configValues := map[string]struct {
		value       string
		isEncrypted bool
	}{
		"url":            {config.URL, false},
		"username":       {config.Username, false},
		"password":       {encryptedPassword, true},
		"auth_type":      {config.AuthType, false},
		"projects":       {string(projectsJson), false},
		"priorities":     {string(prioritiesJson), false},
		"users":          {string(usersJson), false},
		"last_connected": {now.Format(time.RFC3339), false},
	}

	for name, cv := range configValues {
		if cv.value == "" && name != "password" {
			continue
		}
		_, err := dbManager.Exec(`
			INSERT INTO integration_config_values (
				id, integration_id, name, value, is_encrypted,
				created_at, updated_at, created_by, updated_by
			)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW(), NOW(), $5, $5)
		`, integrationID, name, cv.value, cv.isEncrypted, config.CreatedBy)
		if err != nil {
			return response, fmt.Errorf("failed to insert config value %s: %w", name, err)
		}
	}

	// Save additional config values from request (skip reserved keys to prevent security bypass)
	for _, cv := range config.ConfigValues {
		if cv.Name == "" || cv.Value == "" {
			continue
		}
		// Prevent overwriting reserved/sensitive config keys
		if reservedConfigKeys[cv.Name] {
			slog.Warn("Attempted to set reserved config key via config_values, skipping",
				"key", cv.Name, "integration_id", integrationID)
			continue
		}
		_, err := dbManager.Exec(`
			INSERT INTO integration_config_values (
				id, integration_id, name, value, is_encrypted,
				created_at, updated_at, created_by, updated_by
			)
			VALUES (gen_random_uuid(), $1, $2, $3, false, NOW(), NOW(), $4, $4)
		`, integrationID, cv.Name, cv.Value, config.CreatedBy)
		if err != nil {
			return response, fmt.Errorf("failed to save additional config value %s: %w", cv.Name, err)
		}
	}

	response.ID = integrationID
	return response, nil
}

// TestConnection validates an existing ticketing integration by re-running the platform-specific
// connection check. It fetches stored credentials from the DB and attempts to authenticate
// against the external platform API. On success, it also persists the refreshed metadata.
func TestConnection(ctx *gin.Context, integrationID, tenantID string) models.TestConnectionResponse {
	config, err := fetchToolConfigurationForTenant(integrationID, tenantID)
	if err != nil {
		slog.Error("Test connection: failed to fetch configuration", "integration_id", integrationID, "error", err)
		return models.TestConnectionResponse{
			Success: false,
			Tool:    "",
			Error:   fmt.Sprintf("Failed to fetch integration configuration: %v", err),
		}
	}

	if config.Status != "enabled" {
		return models.TestConnectionResponse{
			Success: false,
			Tool:    config.Tool,
			Error:   "Integration is disabled",
		}
	}

	metadata, err := ValidateAndGetMetadata(ctx, config)
	if err != nil {
		slog.Warn("Test connection: validation failed", "integration_id", integrationID, "tool", config.Tool, "error", err)
		return models.TestConnectionResponse{
			Success: false,
			Tool:    config.Tool,
			Error:   fmt.Sprintf("Connection failed: %v", err),
		}
	}

	// Persist refreshed metadata to DB
	if err := updateMetadataConfigValues(integrationID, metadata, config.CreatedBy); err != nil {
		slog.Error("Test connection: failed to persist refreshed metadata", "integration_id", integrationID, "error", err)
	}

	// Count projects from metadata
	projectsCount := 0
	for _, entry := range metadata {
		if projects, ok := entry["projects"].([]models.Project); ok {
			projectsCount = len(projects)
		}
	}

	return models.TestConnectionResponse{
		Success:       true,
		Message:       fmt.Sprintf("Successfully connected to %s", config.Tool),
		Tool:          config.Tool,
		ProjectsCount: projectsCount,
	}
}

func FetchActiveIssueTemplates(ctx *gin.Context, request models.IssueTemplateMetaRequest) (interface{}, error) {
	tenantID := request.SessionVariables.UserTenantID
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id missing from request")
	}
	config, err := fetchToolConfigurationForTenant(request.Input.IntegrationId, tenantID)
	if err != nil {
		slog.Error("Unable to fetch configuration", "id", request.Input.IntegrationId, "tenant", tenantID, "error", err)
		return nil, err
	}

	switch config.Tool {
	case "jira":
		return tools.FetchJiraIssueCreateMeta(config, request.Input.ProjectKey)
	case "github":
		return tools.FetchGitHubIssueCreateMeta(ctx, config, request.Input.ProjectKey)
	case "gitlab":
		return tools.FetchGitLabIssueCreateMeta(ctx, config, request.Input.ProjectKey)
	case "pagerduty":
		return tools.FetchPagerDutyIncidentCreateMeta(ctx, config, request.Input.ProjectKey)
	case "zenduty":
		return tools.FetchZenDutyIncidentCreateMeta(ctx, config, request.Input.ProjectKey)
	default:
		return nil, fmt.Errorf("issue templates not supported for tool %s (integration %s)", config.Tool, config.ID)
	}
}

func QueryIssueFieldDetails(ctx *gin.Context, request models.FieldValuesRequest) (interface{}, error) {
	tenantID := request.SessionVariables.UserTenantID
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id missing from request")
	}
	config, err := fetchToolConfigurationForTenant(request.Input.IntegrationId, tenantID)
	if err != nil {
		slog.Error("Unable to fetch configuration", "id", request.Input.IntegrationId, "tenant", tenantID, "error", err)
		return nil, err
	}

	switch config.Tool {
	case "jira":
		return tools.QueryIssueFieldDetails(ctx, config, request)
	default:
		return nil, fmt.Errorf("field details query not supported for tool %s (integration %s)", config.Tool, config.ID)
	}
}
