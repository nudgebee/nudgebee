package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v67/github"
)

type GitHubService struct{}

var _ ticket.TicketManager = (*GitHubService)(nil)

func init() {
	ticket.RegisterTicketManager("github", &GitHubService{})
}

func (s *GitHubService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return CreateGithubIssue(ctx, config, t)
}

func (s *GitHubService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchGitHubIssueCreateMeta(ctx, config, projectKey)
}

func (s *GitHubService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	return AddGithubComment(ctx, config, t)
}

func (s *GitHubService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	return FetchGithubComments(ctx, config, ticketID)
}

func (s *GitHubService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	var githubClient *github.Client
	var err error

	if config.AuthType != "application" {
		githubClient = clients.CreateGithubClient(config.Password)
	} else {
		installationID, err := strconv.ParseInt(config.Password, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return nil, err
		}
	}

	// ticketID format: "owner/repo#number" or just "number" if project_key contains owner/repo
	// We need both the repo info and issue number
	issueNumber, err := strconv.Atoi(ticketID)
	if err != nil {
		return nil, fmt.Errorf("invalid issue number: %w", err)
	}

	// Get the first project to determine owner/repo
	if len(config.Projects) == 0 {
		return nil, errors.New("no projects configured")
	}
	parts := strings.Split(config.Projects[0].Key, "/")
	if len(parts) != 2 {
		return nil, errors.New("invalid project key format")
	}
	owner, repo := parts[0], parts[1]

	issue, _, err := githubClient.Issues.Get(ctx, owner, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub issue: %w", err)
	}

	createdAt := issue.GetCreatedAt().Time
	return &models.Ticket{
		TicketID:    fmt.Sprintf("%d", issue.GetNumber()),
		Title:       issue.GetTitle(),
		Description: issue.GetBody(),
		Status:      issue.GetState(),
		Platform:    "github",
		URL:         issue.GetHTMLURL(),
		CreatedAt:   &createdAt,
		Raw:         marshalToMap(issue),
	}, nil
}

func CreateGithubIssue(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	var githubClient *github.Client

	if configuration.AuthType != "application" {
		githubClient = clients.CreateGithubClient(configuration.Password)
	} else {
		installationIDStr := configuration.Password
		installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
		if err != nil {
			return ticket, fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return ticket, err
		}
	}

	return createGithubIssueWithClient(ctx, githubClient, ticket)
}

func createGithubIssueWithClient(ctx *gin.Context, githubClient *github.Client, ticket models.Ticket) (models.Ticket, error) {
	additionalFields, ok := ticket.AdditionalFields.(map[string]interface{})
	if !ok {
		additionalFields = make(map[string]interface{})
	}
	var labels = []string{"nudgebee", ticket.Source}
	// Check if "labels" key exists in AdditionalFields and is of type []interface{}
	if existingLabels, ok := additionalFields["labels"].([]interface{}); ok && len(existingLabels) > 0 {
		for _, label := range existingLabels {
			if labelStr, ok := label.(string); ok {
				labels = append(labels, labelStr)
			}
		}
	}
	additionalFields["labels"] = labels

	parts := strings.Split(ticket.ProjectKey, "/")
	if len(parts) != 2 {
		slog.Error("Invalid github project key:", "error", slog.AnyValue(ticket.ProjectKey))
		return ticket, fmt.Errorf("invalid github project key %q: expected format 'owner/repo'", ticket.ProjectKey)
	}
	owner := parts[0]
	repo := parts[1]

	issueRequest := github.IssueRequest{
		Title:  &ticket.Title,
		Body:   &ticket.Description,
		Labels: &labels,
	}

	// Validate assignee against the repo before creating. GitHub silently
	// drops invalid assignees on Issues.Create, which would make us report
	// success even though the assignment never happened. Skip the field
	// entirely when empty so an empty-string pointer doesn't get serialized.
	assignee := strings.TrimSpace(ticket.Assignee)
	if assignee != "" {
		canAssign, _, checkErr := githubClient.Issues.IsAssignee(ctx, owner, repo, assignee)
		if checkErr != nil {
			return ticket, fmt.Errorf("failed to validate assignee %q for %s/%s: %w", assignee, owner, repo, checkErr)
		}
		if !canAssign {
			valid := listValidAssigneeLogins(ctx, githubClient, owner, repo)
			if len(valid) == 0 {
				return ticket, fmt.Errorf("user %q cannot be assigned to issues in %s/%s", assignee, owner, repo)
			}
			// Cap the message to avoid blowing up LLM context on large-org repos.
			const maxDisplay = 50
			display := strings.Join(valid, ", ")
			if len(valid) > maxDisplay {
				display = strings.Join(valid[:maxDisplay], ", ") + fmt.Sprintf(", ... and %d more", len(valid)-maxDisplay)
			}
			return ticket, fmt.Errorf("user %q cannot be assigned to issues in %s/%s. Valid assignees: %s",
				assignee, owner, repo, display)
		}
		issueRequest.Assignee = &assignee
	}

	// Create the issue
	createdIssue, _, err := githubClient.Issues.Create(ctx, owner, repo, &issueRequest)
	if err != nil {
		slog.Error("Error creating GitHub issue:", "error", slog.AnyValue(err))
		return ticket, err
	}
	slog.Info("GitHub issue created: ", "Number", createdIssue.GetNumber())

	// Update ticket details with GitHub issue information
	ticket.TicketID = fmt.Sprintf("%d", createdIssue.GetNumber())
	ticket.Status = "open"
	ticket.Severity = "NA"
	ticket.URL = createdIssue.GetHTMLURL()
	ticket.Platform = "github"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

// FieldInfo represents the metadata for a field.
//
// Group declares which *basic* (static schema) field a create-meta field backs,
// so the frontend renders it exactly once. Empty Group => a dynamic "Platform
// Field". Non-empty values name the basic field: "severity" (the priority/urgency
// source the static Severity dropdown consumes), "title", "description". The tool
// builder owns this tagging because only it knows the platform's field semantics
// (e.g. which Jira field is the priority source), which is why the frontend no
// longer needs a hardcoded key filter or an urgency->priority string alias.
type FieldInfo struct {
	AllowedValues   []interface{} `json:"allowedValues"`
	AutoCompleteUrl string        `json:"autoCompleteUrl,omitempty"`
	Key             string        `json:"key"`
	Name            string        `json:"name"`
	Required        bool          `json:"required"`
	Type            string        `json:"type"`
	Group           string        `json:"group,omitempty"`
}

// Field group constants — see FieldInfo.Group.
const (
	FieldGroupSeverity    = "severity"
	FieldGroupTitle       = "title"
	FieldGroupDescription = "description"
)

// Template represents a template for creating an issue.
type Template struct {
	Name   string               `json:"name"`
	Fields map[string]FieldInfo `json:"fields"`
}

// FetchGitHubIssueCreateMeta fetches and formats the GitHub issue create metadata.
func FetchGitHubIssueCreateMeta(ctx *gin.Context, configuration models.TicketConfigurations, repoKey string) (any, error) {
	var githubClient *github.Client

	var err error
	if configuration.AuthType != "application" {
		githubClient = clients.CreateGithubClient(configuration.Password)
	} else {
		installationIDStr := configuration.Password
		var installationID int64
		installationID, err = strconv.ParseInt(installationIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return nil, err
		}
	}

	parts := strings.Split(repoKey, "/")
	if len(parts) != 2 {
		slog.Error("Invalid github project key:", "error", slog.AnyValue(repoKey))
		return nil, fmt.Errorf("invalid github project key %q: expected format 'owner/repo'", repoKey)
	}
	owner := parts[0]
	repo := parts[1]

	// Fetch all assignees with pagination
	allAssignees, err := listAllRepoAssignees(ctx, githubClient, owner, repo)
	if err != nil {
		return nil, err
	}

	assigneeValues := make([]interface{}, len(allAssignees))
	for i, assignee := range allAssignees {
		assigneeValues[i] = map[string]interface{}{
			"id":    assignee.GetID(),
			"name":  assignee.GetLogin(),
			"value": assignee.GetLogin(),
		}
	}

	// Fetch all labels with pagination
	var allLabels []*github.Label
	labelOpts := &github.ListOptions{PerPage: 100}
	for {
		labels, resp, err := githubClient.Issues.ListLabels(ctx, owner, repo, labelOpts)
		if err != nil {
			return nil, err
		}
		allLabels = append(allLabels, labels...)

		if resp.NextPage == 0 {
			break
		}
		labelOpts.Page = resp.NextPage
	}

	labelValues := make([]interface{}, len(allLabels))
	for i, label := range allLabels {
		labelValues[i] = map[string]interface{}{
			"id":    label.GetID(),
			"name":  label.GetName(),
			"value": label.GetName(),
		}
	}

	// GitHub has no real issue-type concept, so we ship a single "Issue"
	// template. The frontend's GitHub branch uses "Issue" as the ticket_type
	// value, and matches case-insensitively.
	template := Template{
		Name: "Issue",
		Fields: map[string]FieldInfo{
			"assignee": {
				AllowedValues: assigneeValues,
				Key:           "assignee",
				Name:          "Assignee",
				Required:      false,
				Type:          "select",
			},
			"labels": {
				AllowedValues: labelValues,
				Key:           "labels",
				Name:          "Labels",
				Required:      false,
				Type:          "array",
			},
			"summary": {
				AllowedValues: nil,
				Key:           "summary",
				Name:          "Summary",
				Required:      true,
				Type:          "string",
				Group:         FieldGroupTitle,
			},
			"description": {
				AllowedValues: nil,
				Key:           "description",
				Name:          "Description",
				Required:      false,
				Type:          "string",
				Group:         FieldGroupDescription,
			},
		},
	}

	// Match the Jira shape: {data: [template]}. The RPC action exposes this
	// as `tickets_get_create_meta.data`, which the frontend iterates.
	return map[string]interface{}{"data": []Template{template}}, nil
}

// listAllRepoAssignees returns up to 1000 users that can be assigned to issues
// in the given repo (10 pages × 100). The cap protects against latency and
// rate-limit risk in repos with thousands of collaborators; the error-message
// formatter truncates further before surfacing the list to the LLM.
func listAllRepoAssignees(ctx *gin.Context, githubClient *github.Client, owner, repo string) ([]*github.User, error) {
	const maxPages = 10
	var all []*github.User
	opts := &github.ListOptions{PerPage: 100}
	for i := 0; i < maxPages; i++ {
		page, resp, err := githubClient.Issues.ListAssignees(ctx, owner, repo, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// listValidAssigneeLogins returns the GitHub logins that can be assigned to
// issues in the repo, used to build actionable error messages. Failures are
// swallowed: this only feeds an error message that's already being returned.
func listValidAssigneeLogins(ctx *gin.Context, githubClient *github.Client, owner, repo string) []string {
	users, err := listAllRepoAssignees(ctx, githubClient, owner, repo)
	if err != nil {
		slog.Warn("failed to fetch assignee list for error message", "owner", owner, "repo", repo, "error", err)
		return nil
	}
	logins := make([]string, 0, len(users))
	for _, u := range users {
		if login := u.GetLogin(); login != "" {
			logins = append(logins, login)
		}
	}
	return logins
}

// FetchGithubComments lists comments on a GitHub issue. The ticketID must be the issue
// number; owner/repo is taken from the first configured project, matching the behavior
// of GitHubService.Get.
func FetchGithubComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	if err := utils.ValidateGitHubIssueID(ticketID); err != nil {
		return nil, fmt.Errorf("invalid ticket ID: %w", err)
	}

	var githubClient *github.Client
	var err error

	if config.AuthType != "application" {
		githubClient = clients.CreateGithubClient(config.Password)
	} else {
		installationID, err := strconv.ParseInt(config.Password, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return nil, err
		}
	}

	if len(config.Projects) == 0 {
		return nil, errors.New("no projects configured")
	}
	if err := utils.ValidateProjectKey(config.Projects[0].Key); err != nil {
		return nil, fmt.Errorf("invalid project key: %w", err)
	}
	parts := strings.Split(config.Projects[0].Key, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid github project key %q: expected format 'owner/repo'", config.Projects[0].Key)
	}
	owner, repo := parts[0], parts[1]

	issueNumber, err := strconv.Atoi(ticketID)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket ID %q for GitHub issue number: %w", ticketID, err)
	}

	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var all []models.Comments
	for {
		page, resp, err := githubClient.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list GitHub comments for issue %d in %s/%s: %w", issueNumber, owner, repo, err)
		}
		for _, c := range page {
			var author string
			if c.User != nil {
				author = c.User.GetLogin()
			}
			all = append(all, models.Comments{
				Author:  author,
				Comment: c.GetBody(),
				Created: c.GetCreatedAt().Format(time.RFC3339),
				Updated: c.GetUpdatedAt().Format(time.RFC3339),
			})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return all, nil
}

func AddGithubComment(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) error {
	var githubClient *github.Client
	var err error

	if configuration.AuthType != "application" {
		githubClient = clients.CreateGithubClient(configuration.Password)
	} else {
		installationIDStr := configuration.Password
		installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return err
		}
	}
	parts := strings.Split(ticket.ProjectKey, "/")
	if len(parts) != 2 {
		slog.Error("Invalid github project key format", "project_key", ticket.ProjectKey)
		return fmt.Errorf("invalid github project key format: expected 'owner/repo', got %q", ticket.ProjectKey)
	}
	owner := parts[0]
	repo := parts[1]
	number, err := strconv.Atoi(ticket.TicketID)
	if err != nil {
		slog.Error("invalid ticketID (issue number)", "ticketID", ticket.TicketID, "error", err)
		return fmt.Errorf("invalid ticket ID %q for GitHub issue number: %w", ticket.TicketID, err)
	}

	commentBody := ticket.Comment
	if commentBody == "" {
		if ticket.Title == "" {
			return fmt.Errorf("cannot add an empty comment to a GitHub issue")
		}
		commentBody = fmt.Sprintf("Found *%s* again at *%s* \n\n*Description:*\n%s", ticket.Title, time.Now().Format("02 Jan 2006 15:04:05"), ticket.Description)
	}

	ic := &github.IssueComment{Body: github.String(commentBody)}
	_, _, err = githubClient.Issues.CreateComment(ctx, owner, repo, number, ic)
	if err != nil {
		slog.Error("error creating GitHub issue comment", "error", err)
		return fmt.Errorf("failed to create GitHub comment for issue %d in %s/%s: %w", number, owner, repo, err)
	}

	slog.Debug("comment added to GitHub issue",
		"owner", owner, "repo", repo, "issue", number)
	return nil
}

// List retrieves issues from a GitHub repository with filtering and pagination.
func (s *GitHubService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	var githubClient *github.Client
	var err error

	if config.AuthType != "application" {
		githubClient = clients.CreateGithubClient(config.Password)
	} else {
		installationID, err := strconv.ParseInt(config.Password, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return nil, err
		}
	}

	parts := strings.Split(params.ProjectKey, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid project key %q: expected format 'owner/repo'", params.ProjectKey)
	}
	owner, repo := parts[0], parts[1]

	// Convert offset/limit to page/perPage using local variables to avoid
	// mutating the caller's struct. GitHub caps PerPage at 100 server-side,
	// so we cap here too so the page calculation matches what is actually returned.
	limit := params.Limit
	if limit <= 0 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	page := (offset / limit) + 1

	opts := &github.IssueListByRepoOptions{
		State:     "all",
		Sort:      "created",
		Direction: params.SortOrder,
		ListOptions: github.ListOptions{
			PerPage: limit,
			Page:    page,
		},
	}

	if params.Status != "" {
		opts.State = params.Status
	}
	if params.Assignee != "" {
		opts.Assignee = params.Assignee
	}
	if params.SortBy == "updated_at" {
		opts.Sort = "updated"
	}
	if params.CreatedAfter != "" {
		if t, parseErr := time.Parse(time.RFC3339, params.CreatedAfter); parseErr == nil {
			opts.Since = t
		}
	}

	issues, resp, err := githubClient.Issues.ListByRepo(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list GitHub issues: %w", err)
	}

	tickets := make([]models.Ticket, 0, len(issues))
	for _, issue := range issues {
		// Skip pull requests (GitHub API returns them as issues too)
		if issue.PullRequestLinks != nil {
			continue
		}

		var assignee string
		if issue.Assignee != nil {
			assignee = issue.Assignee.GetLogin()
		}

		createdAt := issue.GetCreatedAt().Time
		tickets = append(tickets, models.Ticket{
			TicketID:  fmt.Sprintf("%d", issue.GetNumber()),
			Title:     issue.GetTitle(),
			Status:    issue.GetState(),
			Platform:  "github",
			URL:       issue.GetHTMLURL(),
			Assignee:  assignee,
			CreatedAt: &createdAt,
		})
	}

	// Estimate total from pagination
	total := -1
	if resp != nil && resp.LastPage > 0 {
		total = resp.LastPage * params.Limit
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

// Update updates fields on a GitHub issue
func (s *GitHubService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateGitHubIssueID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	var githubClient *github.Client
	var err error

	if config.AuthType != "application" {
		githubClient = clients.CreateGithubClient(config.Password)
	} else {
		installationID, err := strconv.ParseInt(config.Password, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return err
		}
	}

	projectKey := updateFields.ProjectKey
	if projectKey == "" && len(config.Projects) > 0 {
		projectKey = config.Projects[0].Key
	}
	if projectKey == "" {
		return fmt.Errorf("project_key is required for GitHub (expected owner/repo format)")
	}
	if err := utils.ValidateProjectKey(projectKey); err != nil {
		return fmt.Errorf("invalid project key: %w", err)
	}
	parts := strings.Split(projectKey, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid project key %q: expected format 'owner/repo'", projectKey)
	}
	owner, repo := parts[0], parts[1]

	issueNumber, err := strconv.Atoi(ticketID)
	if err != nil {
		return fmt.Errorf("invalid issue number: %w", err)
	}

	issueRequest := &github.IssueRequest{}
	hasUpdates := false

	if updateFields.Status != "" {
		// GitHub uses "open" or "closed" states
		state := strings.ToLower(updateFields.Status)
		if state != "open" && state != "closed" {
			return fmt.Errorf("invalid status for GitHub: %s. Use 'open' or 'closed'", updateFields.Status)
		}
		issueRequest.State = &state
		hasUpdates = true
	}

	if updateFields.Assignee != "" {
		issueRequest.Assignee = &updateFields.Assignee
		hasUpdates = true
	}

	if updateFields.Description != "" {
		issueRequest.Body = &updateFields.Description
		hasUpdates = true
	}

	if len(updateFields.Labels) > 0 {
		labels := updateFields.Labels
		issueRequest.Labels = &labels
		hasUpdates = true
	}

	if !hasUpdates {
		return nil
	}

	_, _, err = githubClient.Issues.Edit(ctx, owner, repo, issueNumber, issueRequest)
	if err != nil {
		return fmt.Errorf("failed to update GitHub issue: %w", err)
	}

	slog.Info("GitHub issue updated", "ticketID", ticketID)
	return nil
}

// Transition changes the state of a GitHub issue (open/closed)
func (s *GitHubService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	return s.Update(ctx, config, ticketID, models.UpdateFields{Status: status})
}
