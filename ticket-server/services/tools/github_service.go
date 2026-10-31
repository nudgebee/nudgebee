package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

func (s *GitHubService) GetTransitionMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchGitHubProjectTransitionMeta(ctx, config, projectKey)
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
	projectKey := config.Projects[0].Key
	parts := strings.Split(projectKey, "/")
	if len(parts) != 2 {
		return nil, errors.New("invalid project key format")
	}
	owner, repo := parts[0], parts[1]

	return getGithubIssueWithClient(ctx, githubClient, owner, repo, issueNumber, projectKey)
}

// getGithubIssueWithClient fetches a single issue and maps it to the normalized
// Ticket, promoting metadata (assignees, labels, reporter, milestone,
// updated_at) to top-level fields. Split out from Get so tests can drive the
// mapping against an httptest-backed client, mirroring createGithubIssueWithClient.
func getGithubIssueWithClient(ctx *gin.Context, githubClient *github.Client, owner, repo string, issueNumber int, projectKey string) (*models.Ticket, error) {
	// Same request as Issues.Get, but the body is kept: the SDK's Issue struct
	// drops keys it does not model, issue_field_values among them.
	req, err := githubClient.NewRequest(http.MethodGet, fmt.Sprintf("repos/%v/%v/issues/%d", owner, repo, issueNumber), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub issue: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.squirrel-girl-preview")
	var body json.RawMessage
	if _, err := githubClient.Do(ctx, req, &body); err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil &&
			(ghErr.Response.StatusCode == http.StatusNotFound || ghErr.Response.StatusCode == http.StatusGone) {
			return nil, fmt.Errorf("github issue %s/%s#%d not found: %w", owner, repo, issueNumber, ticket.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get GitHub issue: %w", err)
	}
	issue := new(github.Issue)
	if err := json.Unmarshal(body, issue); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub issue: %w", err)
	}
	var full map[string]any
	if err := json.Unmarshal(body, &full); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub issue: %w", err)
	}

	createdAt := issue.GetCreatedAt().Time

	var updatedAt *time.Time
	if t := issue.GetUpdatedAt().Time; !t.IsZero() {
		updatedAt = &t
	}

	assignees := make([]string, 0, len(issue.Assignees))
	for _, a := range issue.Assignees {
		if login := a.GetLogin(); login != "" {
			assignees = append(assignees, login)
		}
	}

	labels := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		if name := l.GetName(); name != "" {
			labels = append(labels, name)
		}
	}

	return &models.Ticket{
		TicketID:    fmt.Sprintf("%d", issue.GetNumber()),
		Title:       issue.GetTitle(),
		Description: issue.GetBody(),
		Status:      issue.GetState(),
		Assignee:    issue.GetAssignee().GetLogin(),
		Assignees:   assignees,
		Reporter:    issue.GetUser().GetLogin(),
		Labels:      labels,
		Milestone:   issue.GetMilestone().GetTitle(),
		Platform:    "github",
		ProjectKey:  projectKey,
		URL:         issue.GetHTMLURL(),
		CreatedAt:   &createdAt,
		UpdatedAt:   updatedAt,
		Raw:         rawWithUnmodeled(issue, full),
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

type githubGraphQLError struct {
	Message string `json:"message"`
}

type githubProjectStatusOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type githubPageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type githubProjectStatusField struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type githubProject struct {
	ID     string                       `json:"id"`
	Title  string                       `json:"title"`
	Number int                          `json:"number"`
}

type githubProjectItem struct {
	ID      string         `json:"id"`
	Project githubProject `json:"project"`
}

type githubProjectsPageQuery struct {
	Errors []githubGraphQLError `json:"errors"`
	Data struct {
		Repository struct {
			ProjectsV2 struct {
				Nodes    []githubProject `json:"nodes"`
				PageInfo githubPageInfo `json:"pageInfo"`
			} `json:"projectsV2"`
		} `json:"repository"`
	} `json:"data"`
}

type githubProjectFieldsQuery struct {
	Errors []githubGraphQLError `json:"errors"`
	Data struct {
		Node struct {
			Fields struct {
				Nodes    []githubProjectStatusField `json:"nodes"`
				PageInfo githubPageInfo `json:"pageInfo"`
			} `json:"fields"`
		} `json:"node"`
	} `json:"data"`
}

type githubProjectOptionsQuery struct {
	Errors []githubGraphQLError `json:"errors"`
	Data struct {
		Node struct {
			Options []githubProjectStatusOption `json:"options"`
		} `json:"node"`
	} `json:"data"`
}

type githubIssueProjectItemsPageQuery struct {
	Errors []githubGraphQLError `json:"errors"`
	Data struct {
		Repository struct {
			Issue struct {
				ProjectItems struct {
					Nodes    []githubProjectItem `json:"nodes"`
					PageInfo githubPageInfo `json:"pageInfo"`
				} `json:"projectItems"`
			} `json:"issue"`
		} `json:"repository"`
	} `json:"data"`
}

type githubProjectStatusMutation struct {
	Errors []githubGraphQLError `json:"errors"`
	Data   struct {
		UpdateProjectV2ItemFieldValue struct {
			ProjectV2Item struct {
				ID string `json:"id"`
			} `json:"projectV2Item"`
		} `json:"updateProjectV2ItemFieldValue"`
	} `json:"data"`
}

// FetchGitHubIssueCreateMeta fetches and formats the GitHub issue create metadata.
func FetchGitHubIssueCreateMeta(ctx *gin.Context, configuration models.TicketConfigurations, repoKey string) (any, error) {
	githubClient, err := githubClientForConfiguration(ctx, configuration)
	if err != nil {
		return nil, err
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

const githubProjectsPageQuery = `query($owner: String!, $repo: String!, $cursor: String) {
	repository(owner: $owner, name: $repo) {
		projectsV2(first: 100, after: $cursor) {
			nodes { id title number }
			pageInfo { hasNextPage endCursor }
		}
	}
}`

const githubProjectFieldsQuery = `query($id: ID!, $cursor: String) {
	node(id: $id) {
		... on ProjectV2 {
			fields(first: 100, after: $cursor) {
				nodes {
					... on ProjectV2SingleSelectField { id name }
				}
				pageInfo { hasNextPage endCursor }
			}
		}
	}
}`

const githubProjectOptionsQuery = `query($id: ID!) {
	node(id: $id) {
		... on ProjectV2SingleSelectField {
			options { id name }
		}
	}
}`

const githubIssueProjectItemsPageQuery = `query($owner: String!, $repo: String!, $number: Int!, $cursor: String) {
	repository(owner: $owner, name: $repo) {
		issue(number: $number) {
			projectItems(first: 100, after: $cursor) {
				nodes { id project { id title number } }
				pageInfo { hasNextPage endCursor }
			}
		}
	}
}`

func githubGraphQLRequest(ctx *gin.Context, githubClient *github.Client, query string, variables map[string]interface{}, response interface{}) error {
	req, err := githubClient.NewRequest(http.MethodPost, "graphql", map[string]interface{}{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return fmt.Errorf("failed to create GitHub GraphQL request: %w", err)
	}
	if _, err := githubClient.Do(ctx, req, response); err != nil {
		var githubErr *github.ErrorResponse
		if errors.As(err, &githubErr) && githubErr.Response != nil &&
			(githubErr.Response.StatusCode == http.StatusUnauthorized ||
				(githubErr.Response.StatusCode == http.StatusForbidden) &&
					!strings.Contains(strings.ToLower(githubErr.Message), "rate limit")) {
			return &githubProjectsPermissionError{err: fmt.Errorf("GitHub Projects lookup is unauthorized: %w", err)}
		}
		return fmt.Errorf("GitHub GraphQL request failed: %w", err)
	}
	return nil
}

type githubProjectsPermissionError struct{ err error }

func (e *githubProjectsPermissionError) Error() string { return e.err.Error() }
func (e *githubProjectsPermissionError) Unwrap() error { return e.err }

const maxGitHubGraphQLPages = 100

func pageCursor(pageInfo githubPageInfo, context string, page int) (string, bool, error) {
	if !pageInfo.HasNextPage {
		return "", false, nil
	}
	if page >= maxGitHubGraphQLPages || pageInfo.EndCursor == nil || *pageInfo.EndCursor == "" {
		return "", false, fmt.Errorf("GitHub Projects %s exceeded the pagination limit of %d pages", context, maxGitHubGraphQLPages)
	}
	return *pageInfo.EndCursor, true, nil
}

func isProjectsPermissionMessage(message string) bool {
	message = strings.ToLower(message)
	if strings.Contains(message, "rate limit") {
		return false
	}
	return strings.Contains(message, "resource not accessible") ||
		strings.Contains(message, "insufficient permission") ||
		strings.Contains(message, "does not have access") ||
		strings.Contains(message, "forbidden")
}

func githubClientForConfiguration(ctx *gin.Context, configuration models.TicketConfigurations) (*github.Client, error) {
	if configuration.AuthType != "application" {
		return clients.CreateGithubClient(configuration.Password), nil
	}
	installationID, err := strconv.ParseInt(configuration.Password, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid installation ID: %w", err)
	}
	return clients.CreateGithubClientWithInstallationToken(ctx, installationID)
}

func githubGraphQLErrors(errors []githubGraphQLError) error {
	if len(errors) == 0 {
		return nil
	}
	err := fmt.Errorf("GitHub Projects status lookup failed: %s", errors[0].Message)
	allPermissionErrors := true
	for _, graphqlErr := range errors {
		if !isProjectsPermissionMessage(graphqlErr.Message) {
			allPermissionErrors = false
			break
		}
	}
	if allPermissionErrors {
		return &githubProjectsPermissionError{err: err}
	}
	return err
}

func fetchProjectFields(ctx *gin.Context, client *github.Client, projectID string) ([]githubProjectStatusField, error) {
	var fields []githubProjectStatusField
	var cursor *string
	for page := 1; ; page++ {
		var response githubProjectFieldsQuery
		if err := githubGraphQLRequest(ctx, client, githubProjectFieldsQuery, map[string]interface{}{"id": projectID, "cursor": cursor}, &response); err != nil {
			return nil, err
		}
		if err := githubGraphQLErrors(response.Errors); err != nil {
			return nil, err
		}
		fields = append(fields, response.Data.Node.Fields.Nodes...)
		next, more, err := pageCursor(response.Data.Node.Fields.PageInfo, "fields", page)
		if err != nil {
			return nil, err
		}
		if !more {
			return fields, nil
		}
		cursor = &next
	}
}

func fetchFieldOptions(ctx *gin.Context, client *github.Client, fieldID string) ([]githubProjectStatusOption, error) {
	var response githubProjectOptionsQuery
	if err := githubGraphQLRequest(ctx, client, githubProjectOptionsQuery, map[string]interface{}{"id": fieldID}, &response); err != nil {
		return nil, err
	}
	if err := githubGraphQLErrors(response.Errors); err != nil {
		return nil, err
	}
	return response.Data.Node.Options, nil
}

func fetchProjects(ctx *gin.Context, client *github.Client, owner, repo string) ([]githubProject, error) {
	var projects []githubProject
	var cursor *string
	for page := 1; ; page++ {
		var response githubProjectsPageQuery
		if err := githubGraphQLRequest(ctx, client, githubProjectsPageQuery, map[string]interface{}{"owner": owner, "repo": repo, "cursor": cursor}, &response); err != nil {
			return nil, err
		}
		if err := githubGraphQLErrors(response.Errors); err != nil {
			return nil, err
		}
		projects = append(projects, response.Data.Repository.ProjectsV2.Nodes...)
		next, more, err := pageCursor(response.Data.Repository.ProjectsV2.PageInfo, "projects", page)
		if err != nil {
			return nil, err
		}
		if !more {
			return projects, nil
		}
		cursor = &next
	}
}

func projectStatusValues(ctx *gin.Context, client *github.Client, owner, repo string) ([]interface{}, error) {
	projects, err := fetchProjects(ctx, client, owner, repo)
	if err != nil {
		return nil, err
	}
	type transitionOption struct {
		id, name, value string
	}
	options := []transitionOption{
		{id: "open", name: "open", value: "open"},
		{id: "closed", name: "closed", value: "closed"},
	}
	for _, project := range projects {
		fields, err := fetchProjectFields(ctx, client, project.ID)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			if !strings.EqualFold(field.Name, "Status") {
				continue
			}
			options, err := fetchFieldOptions(ctx, client, field.ID)
			if err != nil {
				return nil, err
			}
			for _, option := range options {
				if option.ID == "" || option.Name == "" {
					continue
				}
				value := fmt.Sprintf("github-project:%s:%s:%s", project.ID, field.ID, option.ID)
				options = append(options, transitionOption{
					id: option.ID, name: fmt.Sprintf("Project #%d %s: %s", project.Number, project.Title, option.Name), value: value,
				})
			}
		}
	}
	labelCounts := make(map[string]int, len(options))
	for _, option := range options {
		labelCounts[option.name]++
	}
	values := make([]interface{}, 0, len(options))
	for _, option := range options {
		name := option.name
		if labelCounts[name] > 1 {
			idSuffix := option.id
			if len(idSuffix) > 8 {
				idSuffix = idSuffix[len(idSuffix)-8:]
			}
			name = fmt.Sprintf("%s (%s)", name, idSuffix)
		}
		values = append(values, map[string]interface{}{"id": option.id, "name": name, "value": option.value})
	}
	return values, nil
}

func FetchGitHubProjectTransitionMeta(ctx *gin.Context, configuration models.TicketConfigurations, repoKey string) (interface{}, error) {
	client, err := githubClientForConfiguration(ctx, configuration)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(repoKey, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid github project key %q: expected format 'owner/repo'", repoKey)
	}
	values, err := projectStatusValues(ctx, client, parts[0], parts[1])
	if err != nil {
		var permissionErr *githubProjectsPermissionError
		if errors.As(err, &permissionErr) {
			return map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{"id": "open", "name": "open", "value": "open"},
					map[string]interface{}{"id": "closed", "name": "closed", "value": "closed"},
				},
				"warning": "GitHub Projects status choices are unavailable because this token is not authorized to read Projects; open/closed remain available.",
			}, nil
		}
		return nil, err
	}
	return map[string]interface{}{"data": values}, nil
}

func fetchIssueProjectItems(ctx *gin.Context, client *github.Client, owner, repo string, issueNumber int) ([]githubProjectItem, error) {
	var items []githubProjectItem
	var cursor *string
	for page := 1; ; page++ {
		var response githubIssueProjectItemsPageQuery
		if err := githubGraphQLRequest(ctx, client, githubIssueProjectItemsPageQuery, map[string]interface{}{"owner": owner, "repo": repo, "number": issueNumber, "cursor": cursor}, &response); err != nil {
			return nil, err
		}
		if err := githubGraphQLErrors(response.Errors); err != nil {
			return nil, err
		}
		items = append(items, response.Data.Repository.Issue.ProjectItems.Nodes...)
		next, more, err := pageCursor(response.Data.Repository.Issue.ProjectItems.PageInfo, "issue project items", page)
		if err != nil {
			return nil, err
		}
		if !more {
			return items, nil
		}
		cursor = &next
	}
}

func (s *GitHubService) updateProjectStatus(ctx *gin.Context, githubClient *github.Client, owner, repo string, issueNumber int, status string) error {
	parts := strings.Split(status, ":")
	if len(parts) != 4 || parts[0] != "github-project" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return fmt.Errorf("invalid GitHub Project status selection; choose a current Project status option")
	}
	items, err := fetchIssueProjectItems(ctx, githubClient, owner, repo, issueNumber)
	if err != nil {
		return err
	}
	var match *struct{ itemID, projectID, fieldID, optionID string }
	for _, item := range items {
		if item.Project.ID != parts[1] {
			continue
		}
		fields, err := fetchProjectFields(ctx, githubClient, item.Project.ID)
		if err != nil {
			return err
		}
		for _, field := range fields {
			if field.ID != parts[2] || !strings.EqualFold(field.Name, "Status") {
				continue
			}
			options, err := fetchFieldOptions(ctx, githubClient, field.ID)
			if err != nil {
				return err
			}
			for _, option := range options {
				if option.ID == parts[3] {
					if match != nil {
						return fmt.Errorf("GitHub issue %s/%s#%d has an ambiguous Project status selection; refusing transition", owner, repo, issueNumber)
					}
					match = &struct{ itemID, projectID, fieldID, optionID string }{item.ID, item.Project.ID, field.ID, option.ID}
				}
			}
		}
	}
	if match == nil {
		return fmt.Errorf("GitHub issue %s/%s#%d is not linked to the selected Project status (it may be missing, stale, or unauthorized)", owner, repo, issueNumber)
	}

	var mutation githubProjectStatusMutation
	if err := githubGraphQLRequest(ctx, githubClient, `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
		updateProjectV2ItemFieldValue(input: {
			projectId: $projectId
			itemId: $itemId
			fieldId: $fieldId
			value: { singleSelectOptionId: $optionId }
		}) {
			projectV2Item { id }
		}
	}`, map[string]interface{}{
		"projectId": match.projectID,
		"itemId":    match.itemID,
		"fieldId":    match.fieldID,
		"optionId":   match.optionID,
	}, &mutation); err != nil {
		return err
	}
	if len(mutation.Errors) > 0 {
		return fmt.Errorf("GitHub Project status update failed: %s", mutation.Errors[0].Message)
	}
	return nil
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

	// Normalize offset/limit in-place. params is passed by value, so mutating
	// its fields is safe and keeps later logic (total estimate and ListResult)
	// consistent with the page size actually used. GitHub caps PerPage at 100
	// server-side, so we cap here too so the page calculation matches.
	params.Limit = normalizeLimit(params.Limit)
	params.Offset = normalizeOffset(params.Offset)
	limit := params.Limit
	offset := params.Offset
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

	isRESTState := strings.EqualFold(updateFields.Status, "open") || strings.EqualFold(updateFields.Status, "closed")
	if updateFields.Status != "" && !isRESTState &&
		(len(updateFields.AssigneeList()) > 0 || updateFields.Description != "" || len(updateFields.Labels) > 0) {
		return fmt.Errorf("GitHub Project status transitions cannot be combined with REST issue field updates")
	}

	issueRequest := &github.IssueRequest{}
	hasUpdates := false

	if updateFields.Status != "" {
		// GitHub uses "open" or "closed" states
		state := strings.ToLower(updateFields.Status)
		if state == "open" || state == "closed" {
			issueRequest.State = &state
			hasUpdates = true
		} else {
			if err := s.updateProjectStatus(ctx, githubClient, owner, repo, issueNumber, updateFields.Status); err != nil {
				return err
			}
		}
	}

	if assignees := updateFields.AssigneeList(); len(assignees) > 0 {
		// GitHub natively supports multiple assignees. Setting the Assignees
		// array replaces the current set (the requested "set to selected" semantics).
		issueRequest.Assignees = &assignees
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

// Transition changes a GitHub issue state or its linked Project v2 Status.
func (s *GitHubService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	return s.Update(ctx, config, ticketID, models.UpdateFields{Status: status})
}
