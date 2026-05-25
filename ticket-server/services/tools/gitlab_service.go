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
	"time"

	"github.com/gin-gonic/gin"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type GitLabService struct{}

var _ ticket.TicketManager = (*GitLabService)(nil)

func init() {
	ticket.RegisterTicketManager("gitlab", &GitLabService{})
}

func (s *GitLabService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return CreateGitLabIssue(ctx, config, t)
}

func (s *GitLabService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchGitLabIssueCreateMeta(ctx, config, projectKey)
}

func (s *GitLabService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	return AddGitLabComment(ctx, config, t)
}

func (s *GitLabService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	return nil, ticket.ErrNotSupported
}

func (s *GitLabService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	gitlabClient, err := clients.CreateGitLabClient(config.Password, config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	issueIID, err := strconv.ParseInt(ticketID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid issue IID: %w", err)
	}

	// Get the first project
	if len(config.Projects) == 0 {
		return nil, errors.New("no projects configured")
	}
	projectKey := config.Projects[0].Key

	issue, _, err := gitlabClient.Issues.GetIssue(projectKey, issueIID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitLab issue: %w", err)
	}

	var createdAt *time.Time
	if issue.CreatedAt != nil {
		createdAt = issue.CreatedAt
	}

	return &models.Ticket{
		TicketID:    fmt.Sprintf("%d", issue.IID),
		Title:       issue.Title,
		Description: issue.Description,
		Status:      issue.State,
		Platform:    "gitlab",
		URL:         issue.WebURL,
		CreatedAt:   createdAt,
		Raw:         marshalToMap(issue),
	}, nil
}

// CreateGitLabIssue creates a new issue in GitLab.
func CreateGitLabIssue(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	gitlabClient, err := clients.CreateGitLabClient(configuration.Password, configuration.URL)
	if err != nil {
		return ticket, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	// Parse additional fields for labels
	additionalFields, ok := ticket.AdditionalFields.(map[string]interface{})
	if !ok {
		additionalFields = make(map[string]interface{})
	}

	// Build labels list: nudgebee + source + any additional labels
	var labels gitlab.LabelOptions
	labels = append(labels, "nudgebee", ticket.Source)
	if existingLabels, ok := additionalFields["labels"].([]interface{}); ok && len(existingLabels) > 0 {
		for _, label := range existingLabels {
			if labelStr, ok := label.(string); ok {
				labels = append(labels, labelStr)
			}
		}
	}
	additionalFields["labels"] = labels

	// Build issue options
	issueOpts := &gitlab.CreateIssueOptions{
		Title:       gitlab.Ptr(ticket.Title),
		Description: gitlab.Ptr(ticket.Description),
		Labels:      &labels,
	}

	// Set assignee if provided
	if ticket.Assignee != "" {
		// GitLab requires user ID for assignee, but we accept username
		// First try to find the user by username
		users, _, err := gitlabClient.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: gitlab.Ptr(ticket.Assignee),
		})
		if err == nil && len(users) > 0 {
			issueOpts.AssigneeIDs = &[]int64{users[0].ID}
		} else {
			slog.Warn("Could not find GitLab user for assignee", "assignee", ticket.Assignee)
		}
	}

	// Create the issue using the project path (e.g., "group/project")
	createdIssue, _, err := gitlabClient.Issues.CreateIssue(ticket.ProjectKey, issueOpts)
	if err != nil {
		slog.Error("Error creating GitLab issue:", "error", slog.AnyValue(err))
		return ticket, err
	}
	slog.Info("GitLab issue created:", "IID", createdIssue.IID)

	// Update ticket details with GitLab issue information
	ticket.TicketID = fmt.Sprintf("%d", createdIssue.IID)
	ticket.Status = "opened"
	ticket.Severity = "NA"
	ticket.URL = createdIssue.WebURL
	ticket.Platform = "gitlab"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

// FetchGitLabIssueCreateMeta fetches and formats the GitLab issue creation metadata.
func FetchGitLabIssueCreateMeta(ctx *gin.Context, configuration models.TicketConfigurations, projectKey string) (any, error) {
	gitlabClient, err := clients.CreateGitLabClient(configuration.Password, configuration.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	// Fetch all project members (for assignees) with pagination
	var allMembers []*gitlab.ProjectMember
	memberOpts := &gitlab.ListProjectMembersOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
	}
	for {
		members, resp, err := gitlabClient.ProjectMembers.ListAllProjectMembers(projectKey, memberOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch project members: %w", err)
		}
		allMembers = append(allMembers, members...)

		if resp.NextPage == 0 {
			break
		}
		memberOpts.Page = resp.NextPage
	}

	assigneeValues := make([]interface{}, len(allMembers))
	for i, member := range allMembers {
		assigneeValues[i] = map[string]interface{}{
			"id":    member.ID,
			"name":  member.Username,
			"value": member.Username,
		}
	}

	// Fetch all project labels with pagination
	var allLabels []*gitlab.Label
	labelOpts := &gitlab.ListLabelsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
	}
	for {
		labels, resp, err := gitlabClient.Labels.ListLabels(projectKey, labelOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch project labels: %w", err)
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
			"id":    label.ID,
			"name":  label.Name,
			"value": label.Name,
		}
	}

	// Format the data using the same structure as GitHub
	template := Template{
		Name: "GitLab Issue",
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
			},
			"description": {
				AllowedValues: nil,
				Key:           "description",
				Name:          "Description",
				Required:      false,
				Type:          "string",
			},
		},
	}

	// Match the Jira/GitHub shape: {"data": [Template, ...]}. Hasura's
	// `ticket_create_meta_response.data` then exposes the array directly.
	return map[string]interface{}{"data": []Template{template}}, nil
}

// AddGitLabComment adds a note (comment) to an existing GitLab issue.
func AddGitLabComment(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) error {
	gitlabClient, err := clients.CreateGitLabClient(configuration.Password, configuration.URL)
	if err != nil {
		return fmt.Errorf("failed to create GitLab client: %w", err)
	}

	issueIID, err := strconv.ParseInt(ticket.TicketID, 10, 64)
	if err != nil {
		slog.Error("invalid ticketID (issue IID)", "ticketID", ticket.TicketID, "error", err)
		return fmt.Errorf("invalid ticket ID %q for GitLab issue IID: %w", ticket.TicketID, err)
	}

	commentBody := ticket.Comment
	if commentBody == "" {
		if ticket.Title == "" {
			return fmt.Errorf("cannot add an empty comment to GitLab issue %s in project %s: both comment and title are empty", ticket.TicketID, ticket.ProjectKey)
		}
		commentBody = fmt.Sprintf("Found *%s* again at *%s* \n\n*Description:*\n%s", ticket.Title, time.Now().Format("02 Jan 2006 15:04:05"), ticket.Description)
	}

	noteOpts := &gitlab.CreateIssueNoteOptions{
		Body: gitlab.Ptr(commentBody),
	}

	_, _, err = gitlabClient.Notes.CreateIssueNote(ticket.ProjectKey, issueIID, noteOpts)
	if err != nil {
		slog.Error("error creating GitLab issue note", "error", err)
		return fmt.Errorf("failed to create GitLab note for issue %d in %s: %w", issueIID, ticket.ProjectKey, err)
	}

	slog.Debug("note added to GitLab issue", "project", ticket.ProjectKey, "issue", issueIID)
	return nil
}

// List retrieves issues from a GitLab project with filtering and pagination.
func (s *GitLabService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	gitlabClient, err := clients.CreateGitLabClient(config.Password, config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	// Convert offset/limit to page/perPage
	page := (params.Offset / params.Limit) + 1

	opts := &gitlab.ListProjectIssuesOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: int64(params.Limit),
			Page:    int64(page),
		},
		OrderBy: gitlab.Ptr("created_at"),
		Sort:    gitlab.Ptr("desc"),
	}

	if params.Status != "" {
		opts.State = gitlab.Ptr(params.Status)
	}
	if params.Assignee != "" {
		opts.AssigneeUsername = gitlab.Ptr(params.Assignee)
	}
	if params.SortBy == "updated_at" {
		opts.OrderBy = gitlab.Ptr("updated_at")
	}
	if params.SortOrder == "asc" {
		opts.Sort = gitlab.Ptr("asc")
	}
	if params.CreatedAfter != "" {
		if t, parseErr := time.Parse(time.RFC3339, params.CreatedAfter); parseErr == nil {
			opts.CreatedAfter = &t
		}
	}
	if params.CreatedBefore != "" {
		if t, parseErr := time.Parse(time.RFC3339, params.CreatedBefore); parseErr == nil {
			opts.CreatedBefore = &t
		}
	}

	issues, resp, err := gitlabClient.Issues.ListProjectIssues(params.ProjectKey, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list GitLab issues: %w", err)
	}

	tickets := make([]models.Ticket, 0, len(issues))
	for _, issue := range issues {
		var assignee string
		if len(issue.Assignees) > 0 {
			assignee = issue.Assignees[0].Username
		}

		var createdAt *time.Time
		if issue.CreatedAt != nil {
			createdAt = issue.CreatedAt
		}

		tickets = append(tickets, models.Ticket{
			TicketID:  fmt.Sprintf("%d", issue.IID),
			Title:     issue.Title,
			Status:    issue.State,
			Platform:  "gitlab",
			URL:       issue.WebURL,
			Assignee:  assignee,
			CreatedAt: createdAt,
		})
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   int(resp.TotalItems),
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

// Update updates fields on a GitLab issue
func (s *GitLabService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateGitLabIssueID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	gitlabClient, err := clients.CreateGitLabClient(config.Password, config.URL)
	if err != nil {
		return fmt.Errorf("failed to create GitLab client: %w", err)
	}

	issueIID, err := strconv.ParseInt(ticketID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid issue IID: %w", err)
	}

	projectKey := updateFields.ProjectKey
	if projectKey == "" && len(config.Projects) > 0 {
		projectKey = config.Projects[0].Key
	}
	if projectKey == "" {
		return fmt.Errorf("project_key is required for GitLab")
	}
	if err := utils.ValidateProjectKey(projectKey); err != nil {
		return fmt.Errorf("invalid project key: %w", err)
	}

	updateOpts := &gitlab.UpdateIssueOptions{}
	hasUpdates := false

	if updateFields.Status != "" {
		// GitLab uses "close" or "reopen" state events
		stateEvent := ""
		switch updateFields.Status {
		case "closed", "close":
			stateEvent = "close"
		case "opened", "reopen", "open":
			stateEvent = "reopen"
		default:
			return fmt.Errorf("invalid status for GitLab: %s. Use 'close' or 'reopen'", updateFields.Status)
		}
		updateOpts.StateEvent = gitlab.Ptr(stateEvent)
		hasUpdates = true
	}

	if updateFields.Assignee != "" {
		// Lookup user by username
		users, _, err := gitlabClient.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: gitlab.Ptr(updateFields.Assignee),
		})
		if err == nil && len(users) > 0 {
			updateOpts.AssigneeIDs = &[]int64{users[0].ID}
			hasUpdates = true
		}
	}

	if updateFields.Description != "" {
		updateOpts.Description = gitlab.Ptr(updateFields.Description)
		hasUpdates = true
	}

	if len(updateFields.Labels) > 0 {
		lbls := gitlab.LabelOptions(updateFields.Labels)
		updateOpts.Labels = &lbls
		hasUpdates = true
	}

	if !hasUpdates {
		return nil
	}

	_, _, err = gitlabClient.Issues.UpdateIssue(projectKey, issueIID, updateOpts)
	if err != nil {
		return fmt.Errorf("failed to update GitLab issue: %w", err)
	}

	slog.Info("GitLab issue updated", "ticketID", ticketID)
	return nil
}

// Transition changes the state of a GitLab issue (open/closed)
func (s *GitLabService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	return s.Update(ctx, config, ticketID, models.UpdateFields{Status: status})
}
