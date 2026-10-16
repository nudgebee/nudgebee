package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v67/github"
	"go.yaml.in/yaml/v3"
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

// GetAttachment is unimplemented: GitHub comment bodies already embed a full,
// directly-fetchable image URL (markdown ![alt](url)) - no separate attachment
// fetch is needed, the frontend renders the markdown body as-is.
// GetAttachment resolves a github.com/user-attachments/ asset ID (captured by
// extractGithubUserAttachments) to real bytes. The REST API never exposes a
// fetchable URL for these - only GraphQL's bodyHTML field does, by resolving
// the asset into a short-lived signed URL at query time (verified directly:
// fetching that resolved URL immediately returns the real image, 200 OK).
func (s *GitHubService) GetAttachment(ctx *gin.Context, config models.TicketConfigurations, ticketID, attachmentID string) (string, io.ReadCloser, error) {
	if len(config.Projects) == 0 {
		return "", nil, errors.New("no projects configured")
	}
	parts := strings.Split(config.Projects[0].Key, "/")
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid github project key %q: expected format 'owner/repo'", config.Projects[0].Key)
	}
	owner, repo := parts[0], parts[1]

	issueNumber, err := strconv.Atoi(ticketID)
	if err != nil {
		return "", nil, fmt.Errorf("invalid ticket ID %q for GitHub issue number: %w", ticketID, err)
	}

	token, err := githubBearerToken(ctx, config)
	if err != nil {
		return "", nil, err
	}

	resolvedURL, err := resolveGithubUserAttachmentURL(ctx, token, owner, repo, issueNumber, attachmentID)
	if err != nil {
		return "", nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("github get attachment: build request: %w", err)
	}
	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("github get attachment: request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return "", nil, fmt.Errorf("github get attachment: returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return resp.Header.Get("Content-Type"), resp.Body, nil
}

// githubBearerToken returns the raw bearer token for either auth mode, for
// callers (GetAttachment) that need to hit the REST/GraphQL APIs directly
// rather than through a *github.Client.
func githubBearerToken(ctx context.Context, config models.TicketConfigurations) (string, error) {
	if config.AuthType != "application" {
		return config.Password, nil
	}
	installationID, err := strconv.ParseInt(config.Password, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid installation ID: %w", err)
	}
	return clients.GetGithubAppInstallationToken(ctx, installationID)
}

type githubBodyHTMLResponse struct {
	Data struct {
		Repository struct {
			Issue struct {
				Comments struct {
					Nodes []struct {
						BodyHTML string `json:"bodyHTML"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issue"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// resolveGithubUserAttachmentURL re-renders the issue's comments via GraphQL
// (bodyHTML resolves GitHub's internal asset shorthand into a fresh signed
// URL) and returns the one matching assetID. Re-fetches all comments rather
// than a single targeted one because the REST comment ID isn't threaded
// through CommentAttachment.ID - the asset UUID alone is enough to find it
// and is globally unique, so this never matches the wrong image.
func resolveGithubUserAttachmentURL(ctx context.Context, token, owner, repo string, issueNumber int, assetID string) (string, error) {
	const query = `query($owner: String!, $name: String!, $number: Int!) {
		repository(owner: $owner, name: $name) {
			issue(number: $number) {
				comments(last: 100) { nodes { bodyHTML } }
			}
		}
	}`
	payload, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"owner": owner, "name": repo, "number": issueNumber},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("github graphql request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github graphql returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed githubBodyHTMLResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("github graphql: unmarshal: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return "", fmt.Errorf("github graphql error: %s", parsed.Errors[0].Message)
	}

	for _, node := range parsed.Data.Repository.Issue.Comments.Nodes {
		for _, srcMatch := range githubImgSrcRe.FindAllStringSubmatch(node.BodyHTML, -1) {
			if strings.Contains(srcMatch[1], assetID) {
				// bodyHTML is HTML, so its src attribute is HTML-escaped - a
				// signed URL's multiple query params ("&...") come through as
				// "&amp;..." and must be unescaped before use as a real URL.
				return html.UnescapeString(srcMatch[1]), nil
			}
		}
	}
	return "", fmt.Errorf("attachment %s not found on issue %d", assetID, issueNumber)
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

	// Any additional field beyond the reserved ones (assignee/labels, handled
	// separately) came from an issue-form template's dynamic fields - fold the
	// answers into the body as GitHub's own UI would, since the REST Issues API
	// has no field slot for them. fieldOrder (sent by the frontend from the
	// matching GetCreateMeta Template) restores the form's declared layout,
	// since additionalFields is otherwise just an unordered map.
	fieldOrder := extractGithubFieldOrder(additionalFields)
	fieldLabels := extractGithubFieldLabels(additionalFields)

	body := ticket.Description
	if descriptionHasOrderedPosition(fieldOrder) {
		// The form itself has a "Description"/"Summary" question (parsed in
		// FetchGitHubIssueCreateMeta) and said where it belongs relative to
		// its other questions - e.g. a Feature Request form's Summary is its
		// 3rd field, not its 1st. Render it as one ordered section among the
		// rest, in its real position, instead of always pinning it first.
		if strings.TrimSpace(ticket.Description) != "" {
			additionalFields["description"] = ticket.Description
		}
		body = formatGithubIssueFormAnswers(additionalFields, fieldOrder, fieldLabels)
	} else if extra := formatGithubIssueFormAnswers(additionalFields, fieldOrder, fieldLabels); extra != "" {
		// No form-declared position for it (a plain ticket, or a form with no
		// Description/Summary question at all) - keep it as the fixed
		// leading section it has always been, headed only when there are
		// other sections for it to sit alongside.
		if strings.TrimSpace(body) != "" {
			body = "### Description\n\n" + body + "\n\n---\n\n" + extra
		} else {
			body = extra
		}
	}

	issueRequest := github.IssueRequest{
		Title:  &ticket.Title,
		Body:   &body,
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
	// Persisted to NudgeBee's own DB as-is (CreateIssue inserts ticket.Description
	// directly) - the composed body, not the original free text, so the local
	// record matches what the issue actually contains (nothing reads this
	// ticket back from GitHub to redisplay it; tickets_list reads the DB row).
	ticket.Description = body
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
//
// Order lists dynamic (non-static) field keys in the source form's declared
// order. Only GitHub issue-form templates populate it - Fields is a map, so
// without it the body sections rebuilt from submitted answers would come out
// in random order instead of matching the form's layout. Other providers
// leave it empty; a consumer with no Order falls back to map order.
type Template struct {
	Name   string               `json:"name"`
	Fields map[string]FieldInfo `json:"fields"`
	Order  []string             `json:"order,omitempty"`
}

type githubGraphQLError struct {
	Message    string `json:"message"`
	Type       string `json:"type"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
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
	ID      string                      `json:"id"`
	Name    string                      `json:"name"`
	Options []githubProjectStatusOption `json:"options"`
}

type githubProject struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Number int    `json:"number"`
}

type githubProjectItem struct {
	ID         string        `json:"id"`
	IsArchived bool          `json:"isArchived"`
	Project    githubProject `json:"project"`
}

type githubProjectsPageResponse struct {
	Errors []githubGraphQLError `json:"errors"`
	Data   struct {
		Repository struct {
			ProjectsV2 struct {
				Nodes    []githubProject `json:"nodes"`
				PageInfo githubPageInfo  `json:"pageInfo"`
			} `json:"projectsV2"`
		} `json:"repository"`
	} `json:"data"`
}

type githubProjectFieldsResponse struct {
	Errors []githubGraphQLError `json:"errors"`
	Data   struct {
		Node struct {
			Fields struct {
				Nodes    []githubProjectStatusField `json:"nodes"`
				PageInfo githubPageInfo             `json:"pageInfo"`
			} `json:"fields"`
		} `json:"node"`
	} `json:"data"`
}

type githubIssueProjectItemsPageResponse struct {
	Errors []githubGraphQLError `json:"errors"`
	Data   struct {
		Repository struct {
			Issue struct {
				ProjectItems struct {
					Nodes    []githubProjectItem `json:"nodes"`
					PageInfo githubPageInfo      `json:"pageInfo"`
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

	return fetchGitHubIssueCreateMetaWithClient(ctx, githubClient, repoKey)
}

// fetchGitHubIssueCreateMetaWithClient is split out from FetchGitHubIssueCreateMeta
// so tests can drive it against an httptest-backed client, mirroring
// createGithubIssueWithClient / getGithubIssueWithClient.
func fetchGitHubIssueCreateMetaWithClient(ctx *gin.Context, githubClient *github.Client, repoKey string) (any, error) {
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

	// "id" must be the login, not GetID()'s numeric GitHub user ID: the
	// frontend's generic option-value fallback (no explicit "key") stores
	// "id" verbatim as the selected value, and createGithubIssueWithClient
	// validates/assigns by login (IsAssignee, issueRequest.Assignee) - every
	// other provider in this file already puts the real submission value in
	// "id" for the same reason (e.g. Jira/Freshdesk use their wire id, never
	// an unrelated internal numeric one).
	assigneeValues := make([]interface{}, len(allAssignees))
	for i, assignee := range allAssignees {
		assigneeValues[i] = map[string]interface{}{
			"id":    assignee.GetLogin(),
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

	// Same reasoning as assigneeValues above: "id" must be the label name,
	// not GetID()'s numeric label ID - issueRequest.Labels (and the "labels"
	// merge in createGithubIssueWithClient) takes names, never numeric ids.
	labelValues := make([]interface{}, len(allLabels))
	for i, label := range allLabels {
		labelValues[i] = map[string]interface{}{
			"id":    label.GetName(),
			"name":  label.GetName(),
			"value": label.GetName(),
		}
	}

	// assignee/labels/summary/description are always offered, regardless of
	// which (if any) issue-form template is selected - they're repo-level
	// (assignee, labels) or have no per-form equivalent (summary/description
	// bind to the static Subject/Description inputs every ticket has).
	staticFields := func(dynamic map[string]FieldInfo) map[string]FieldInfo {
		fields := map[string]FieldInfo{
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
		}
		for key, field := range dynamic {
			fields[key] = field
		}
		return fields
	}

	// GitHub has no real issue-type concept, so the plain "Issue" template is
	// always offered, alongside any real form templates - a repo that also
	// defines forms shouldn't lose the option to file a plain issue. The
	// leading icon matches the convention most repos' own template names
	// already follow (e.g. "🐛 Bug Report"), so this fallback doesn't stand
	// out as the one option with no icon.
	templates := []Template{{Name: "📝 Issue", Fields: staticFields(nil)}}

	// Repos that define issue-form templates (.github/ISSUE_TEMPLATE/*.yml) get
	// one additional Template per form, carrying that form's own custom
	// fields. Parsing is best-effort per file - a malformed form is skipped
	// rather than failing create-meta for the whole repo.
	formTemplates, err := fetchGithubIssueFormTemplates(ctx, githubClient, owner, repo)
	if err != nil {
		slog.Warn("github: failed to list issue form templates, falling back to plain issue template",
			"owner", owner, "repo", repo, "error", err)
	}
	for _, t := range formTemplates {
		if strings.EqualFold(strings.TrimSpace(t.Name), "Issue") {
			// A form literally named "Issue" is redundant with the
			// always-present static template above (same purpose: a plain
			// issue, no custom fields) - skip it rather than offering two.
			slog.Warn("github: issue form named \"Issue\" collides with the static template, skipping it",
				"owner", owner, "repo", repo)
			continue
		}
		t.Fields = staticFields(t.Fields)
		templates = append(templates, t)
	}

	// Match the Jira shape: {data: [template]}. The RPC action exposes this
	// as `tickets_get_create_meta.data`, which the frontend iterates.
	return map[string]interface{}{"data": templates}, nil
}

// githubIssueFormReservedKeys are the field keys already used by the static
// assignee/labels/summary/description slots above. A form field whose id or
// label would collide must be renamed - TicketFormComponent filters the
// literal keys "summary" and "description" out of its dynamic-field render
// (they're shown via the static Subject/Description inputs instead), so a
// form field landing on one of those keys would silently never render.
var githubIssueFormReservedKeys = map[string]bool{
	"assignee":    true,
	"labels":      true,
	"summary":     true,
	"description": true,
}

// githubIssueFormFile is the subset of GitHub's issue-forms YAML schema
// (docs.github.com/en/communities/.../syntax-for-issue-forms) this parser
// understands: the form name and its body items.
type githubIssueFormFile struct {
	Name string                `yaml:"name"`
	Body []githubIssueFormItem `yaml:"body"`
}

type githubIssueFormItem struct {
	Type       string `yaml:"type"`
	ID         string `yaml:"id"`
	Attributes struct {
		Label    string        `yaml:"label"`
		Multiple bool          `yaml:"multiple"`
		Options  []interface{} `yaml:"options"`
	} `yaml:"attributes"`
	Validations struct {
		Required bool `yaml:"required"`
	} `yaml:"validations"`
}

// fetchGithubIssueFormTemplates fetches and parses every issue-form YAML file
// in .github/ISSUE_TEMPLATE/, skipping config.yml (the chooser config, not a
// form) and any file this parser can't read as a form. Returns (nil, nil) -
// not an error - when the repo has no such directory.
func fetchGithubIssueFormTemplates(ctx *gin.Context, githubClient *github.Client, owner, repo string) ([]Template, error) {
	_, entries, _, err := githubClient.Repositories.GetContents(ctx, owner, repo, ".github/ISSUE_TEMPLATE", nil)
	if err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}

	var templates []Template
	for _, entry := range entries {
		lower := strings.ToLower(entry.GetName())
		if entry.GetType() != "file" || (!strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml")) {
			continue
		}
		if lower == "config.yml" || lower == "config.yaml" {
			continue
		}

		fileContent, _, _, err := githubClient.Repositories.GetContents(ctx, owner, repo, entry.GetPath(), nil)
		if err != nil {
			slog.Warn("github: failed to fetch issue form file, skipping", "owner", owner, "repo", repo, "path", entry.GetPath(), "error", err)
			continue
		}
		raw, err := fileContent.GetContent()
		if err != nil {
			slog.Warn("github: failed to decode issue form file, skipping", "owner", owner, "repo", repo, "path", entry.GetPath(), "error", err)
			continue
		}

		template, ok := parseGithubIssueFormYAML(entry.GetName(), raw)
		if !ok {
			continue
		}
		templates = append(templates, template)
	}
	return templates, nil
}

// parseGithubIssueFormYAML parses one issue-form file into a Template. Returns
// ok=false for a file with no usable input fields - a plain markdown-only
// template, a legacy non-forms template, or YAML this parser couldn't read -
// rather than erroring; the caller falls back to the plain static template.
func parseGithubIssueFormYAML(fileName, raw string) (Template, bool) {
	var form githubIssueFormFile
	if err := yaml.Unmarshal([]byte(raw), &form); err != nil {
		slog.Warn("github: failed to parse issue form YAML, skipping", "file", fileName, "error", err)
		return Template{}, false
	}

	name := strings.TrimSpace(form.Name)
	if name == "" {
		name = strings.TrimSuffix(strings.TrimSuffix(fileName, ".yml"), ".yaml")
	}

	// Bind one body item, if any, directly onto the static Description slot
	// (literal key "description") instead of rendering it as its own
	// duplicate-looking field: the item naturally keyed "description" wins
	// when the form has one, else the one keyed "summary" (a common label
	// for the same kind of free-text "what is this about" question, e.g. a
	// Spike Request's Summary) is used instead - GitHub's real issue title
	// always comes from the separate top-level title: key, never a body
	// item, so a "Summary" question is never actually a title duplicate.
	// When the form has neither, the generic optional Description box is
	// left untouched. When it has both (two distinct questions), only
	// "description" is bound here - "summary" still renders as its own
	// field below, so the user sees both rather than losing one.
	contentKey := ""
	for _, candidate := range []string{"description", "summary"} {
		for _, item := range form.Body {
			if githubFieldKeyFor(item) == candidate {
				contentKey = candidate
				break
			}
		}
		if contentKey != "" {
			break
		}
	}

	fields := map[string]FieldInfo{}
	var order []string
	used := map[string]bool{}
	boundContent := false
	for _, item := range form.Body {
		field, ok := githubFormFieldFromItem(item)
		if !ok {
			continue
		}
		naturalKey := githubFieldKeyFor(item)
		if !boundContent && contentKey != "" && naturalKey == contentKey {
			field.Key = "description"
			field.Group = FieldGroupDescription
			fields["description"] = field
			used["description"] = true
			boundContent = true
			// Recorded at its real position in the form, not skipped: a
			// "Summary"/"Description" question is rarely the form's first
			// field (see e.g. a Feature Request form: Function, Who asked
			// for this, Summary, What this unblocks), so createGithubIssueWithClient
			// must be able to place it where the form actually put it,
			// not always first.
			order = append(order, "description")
			continue
		}
		// Any other body field is never dropped on a reserved-key collision
		// (assignee/labels/summary, or a second item that also happens to be
		// keyed "description"/"summary" beyond the one bound above) - renamed
		// instead, so it stays visible under a disambiguated key rather than
		// silently losing required content the plain Issues API has no way
		// to flag as missing.
		key := uniqueGithubFieldKey(naturalKey, used)
		field.Key = key
		fields[key] = field
		order = append(order, key)
	}

	if len(fields) == 0 {
		return Template{}, false
	}
	return Template{Name: name, Fields: fields, Order: order}, true
}

// githubFieldKeyFor derives a stable field key: the form's own id when given
// (GitHub requires ids unique within a form already), else a slug of the label.
func githubFieldKeyFor(item githubIssueFormItem) string {
	if id := strings.TrimSpace(item.ID); id != "" {
		return githubSlugify(id)
	}
	return githubSlugify(item.Attributes.Label)
}

var githubSlugNonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

func githubSlugify(s string) string {
	slug := strings.Trim(githubSlugNonAlnumRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_"), "_")
	if slug == "" {
		return "field"
	}
	return slug
}

// uniqueGithubFieldKey renames a key that collides with a reserved static
// field or an earlier field in the same form, so it can't silently overwrite
// or (for "summary"/"description") get filtered out by the frontend.
func uniqueGithubFieldKey(key string, used map[string]bool) string {
	candidate := key
	for n := 2; githubIssueFormReservedKeys[candidate] || used[candidate]; n++ {
		candidate = fmt.Sprintf("%s_%d", key, n)
	}
	used[candidate] = true
	return candidate
}

// githubFormFieldFromItem maps one issue-forms body item to a FieldInfo.
// "markdown" items are display-only text with no answer, so they're skipped,
// as is any item type this parser doesn't model yet.
func githubFormFieldFromItem(item githubIssueFormItem) (FieldInfo, bool) {
	label := strings.TrimSpace(item.Attributes.Label)
	if label == "" {
		label = strings.TrimSpace(item.ID)
	}

	switch item.Type {
	case "input":
		return FieldInfo{Name: label, Required: item.Validations.Required, Type: "string"}, true
	case "textarea":
		return FieldInfo{Name: label, Required: item.Validations.Required, Type: "text"}, true
	case "dropdown":
		fieldType := "select"
		if item.Attributes.Multiple {
			fieldType = "array"
		}
		return FieldInfo{Name: label, Required: item.Validations.Required, Type: fieldType, AllowedValues: githubFormOptionValues(item.Attributes.Options)}, true
	case "checkboxes":
		values, anyRequired := githubFormCheckboxValues(item.Attributes.Options)
		return FieldInfo{Name: label, Required: anyRequired, Type: "multiselect", AllowedValues: values}, true
	default:
		return FieldInfo{}, false
	}
}

// githubFormOptionValues converts dropdown options (plain scalars per
// GitHub's schema) into the {id,name,value} shape every other FieldInfo's
// AllowedValues already uses.
func githubFormOptionValues(options []interface{}) []interface{} {
	values := make([]interface{}, 0, len(options))
	for _, opt := range options {
		label := fmt.Sprint(opt)
		if label == "" {
			continue
		}
		values = append(values, map[string]interface{}{"id": label, "name": label, "value": label})
	}
	return values
}

// githubFormCheckboxValues converts checkboxes options - each either a plain
// string or a {label, required} map - into AllowedValues, and reports whether
// any option is itself required (checkboxes has no field-level `required`;
// GitHub puts it per-option instead).
func githubFormCheckboxValues(options []interface{}) ([]interface{}, bool) {
	values := make([]interface{}, 0, len(options))
	anyRequired := false
	for _, opt := range options {
		var label string
		if o, ok := opt.(map[string]interface{}); ok {
			label = fmt.Sprint(o["label"])
			if req, _ := o["required"].(bool); req {
				anyRequired = true
			}
		} else {
			label = fmt.Sprint(opt)
		}
		if label == "" {
			continue
		}
		values = append(values, map[string]interface{}{"id": label, "name": label, "value": label})
	}
	return values, anyRequired
}

// githubFieldOrderKey is the additionalFields key the frontend uses to carry
// a GetCreateMeta Template's field Order back on ticket creation - see
// extractGithubFieldOrder. additionalFields otherwise flows through
// untouched to other providers' encoders (jira/freshdesk), so this is
// deliberately namespaced to avoid ever colliding with a real field id.
const githubFieldOrderKey = "_field_order"

// githubFieldLabelsKey carries each dynamic field's real FieldInfo.Name
// (e.g. "Reproduction steps") back on ticket creation - see
// extractGithubFieldLabels. Without it, the body heading can only be
// reconstructed from the field's key/id (e.g. "reprod"), which commonly
// differs from its label - GitHub form authors frequently give a field a
// short id and a fuller display label.
const githubFieldLabelsKey = "_field_labels"

// extractGithubFieldOrder pulls the client-supplied field order out of
// additionalFields and removes it, so it is never rendered as an answer of
// its own. additionalFields is a plain map with no inherent order, and the
// create request carries nothing else that would let the body sections
// match the form's declared layout - see Template.Order and
// TicketFormComponent's fieldOrder prop, which is this same value's
// round trip from GetCreateMeta through the UI and back.
func extractGithubFieldOrder(additionalFields map[string]interface{}) []string {
	raw, ok := additionalFields[githubFieldOrderKey]
	if !ok {
		return nil
	}
	delete(additionalFields, githubFieldOrderKey)
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	order := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			order = append(order, s)
		}
	}
	return order
}

// extractGithubFieldLabels pulls the client-supplied key->label map out of
// additionalFields and removes it, so it is never rendered as an answer of
// its own (mirrors extractGithubFieldOrder).
func extractGithubFieldLabels(additionalFields map[string]interface{}) map[string]string {
	raw, ok := additionalFields[githubFieldLabelsKey]
	if !ok {
		return nil
	}
	delete(additionalFields, githubFieldLabelsKey)
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	labels := make(map[string]string, len(rawMap))
	for key, v := range rawMap {
		if s, ok := v.(string); ok && s != "" {
			labels[key] = s
		}
	}
	return labels
}

// descriptionHasOrderedPosition reports whether fieldOrder places
// "description" at a specific spot - meaning the parsed form had its own
// Description/Summary question (see parseGithubIssueFormYAML's content-field
// binding) rather than relying on the generic, position-less static box.
func descriptionHasOrderedPosition(fieldOrder []string) bool {
	for _, key := range fieldOrder {
		if key == "description" {
			return true
		}
	}
	return false
}

// formatGithubIssueFormAnswers renders every additionalFields entry beyond
// "labels" (handled separately in createGithubIssueWithClient) as a markdown
// section, approximating what GitHub's own UI does when it flattens a
// submitted issue form into the plain Issues API's body field. order (the
// matching GetCreateMeta Template's field order - see extractGithubFieldOrder)
// is honored first so sections match the form's declared layout; any
// additionalFields key order doesn't cover (stale cached order, or a key
// order never knew about) falls back to alphabetical. labels (see
// extractGithubFieldLabels) gives each section its real form label (e.g.
// "Reproduction steps") when known; a key labels doesn't cover falls back
// to a heading reconstructed from the key itself (e.g. "reprod" -> "Reprod").
func formatGithubIssueFormAnswers(additionalFields map[string]interface{}, order []string, labels map[string]string) string {
	seen := map[string]bool{"labels": true}
	keys := make([]string, 0, len(additionalFields))
	for _, key := range order {
		if seen[key] {
			continue
		}
		if _, ok := additionalFields[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(additionalFields))
	for key := range additionalFields {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	var sections []string
	for _, key := range keys {
		value := formatGithubFieldValue(additionalFields[key])
		if value == "" {
			continue
		}
		heading := labels[key]
		if heading == "" {
			heading = githubFieldHeading(key)
		}
		sections = append(sections, "### "+heading+"\n\n"+value)
	}
	return strings.Join(sections, "\n\n")
}

func formatGithubFieldValue(v interface{}) string {
	if v == nil {
		return ""
	}
	if list, ok := v.([]interface{}); ok {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			if s := githubFieldValueScalar(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return githubFieldValueScalar(v)
}

// githubFieldValueScalar renders one answer value as text. A multiselect
// (checkboxes) field's selections arrive as {"id": ...} objects - the same
// wire shape TicketFormComponent uses for every provider's multiselect type
// (see jiraOptionIDString for the Jira equivalent) - so a plain fmt.Sprint
// would render "map[id:...]" instead of the value.
func githubFieldValueScalar(v interface{}) string {
	if m, ok := v.(map[string]interface{}); ok {
		for _, key := range []string{"id", "value", "key"} {
			if s := strings.TrimSpace(fmt.Sprint(m[key])); s != "" && m[key] != nil {
				return s
			}
		}
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// githubFieldHeading turns a field key (e.g. "what_happened") into a heading
// (e.g. "What Happened"). additionalFields carries only the key, not the
// form's original label, so this is an approximation.
func githubFieldHeading(key string) string {
	words := strings.FieldsFunc(key, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

const githubProjectsQuery = `query($owner: String!, $repo: String!, $cursor: String) {
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
					... on ProjectV2SingleSelectField {
						id
						name
						options { id name }
					}
				}
				pageInfo { hasNextPage endCursor }
			}
		}
	}
}`

const githubIssueProjectItemsQuery = `query($owner: String!, $repo: String!, $number: Int!, $cursor: String) {
	repository(owner: $owner, name: $repo) {
		issue(number: $number) {
			projectItems(first: 100, after: $cursor, includeArchived: false) {
				nodes { id isArchived project { id title number } }
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
		var rateLimitErr *github.RateLimitError
		var abuseErr *github.AbuseRateLimitError
		if errors.As(err, &rateLimitErr) || errors.As(err, &abuseErr) {
			return fmt.Errorf("GitHub GraphQL request failed: %w", err)
		}
		var githubErr *github.ErrorResponse
		if errors.As(err, &githubErr) && githubErr.Response != nil &&
			(githubErr.Response.StatusCode == http.StatusUnauthorized ||
				githubErr.Response.StatusCode == http.StatusForbidden ||
				githubErr.Response.StatusCode == http.StatusNotFound) {
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
		code := graphqlErr.Type
		if code == "" {
			code = graphqlErr.Extensions.Code
		}
		switch strings.ToUpper(code) {
		case "INSUFFICIENT_SCOPES", "FORBIDDEN", "NOT_FOUND":
		default:
			allPermissionErrors = false
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
		var response githubProjectFieldsResponse
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

func fetchProjects(ctx *gin.Context, client *github.Client, owner, repo string) ([]githubProject, error) {
	var projects []githubProject
	var cursor *string
	for page := 1; ; page++ {
		var response githubProjectsPageResponse
		if err := githubGraphQLRequest(ctx, client, githubProjectsQuery, map[string]interface{}{"owner": owner, "repo": repo, "cursor": cursor}, &response); err != nil {
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
	statusOptions := []transitionOption{
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
			for _, option := range field.Options {
				if option.ID == "" || option.Name == "" {
					continue
				}
				value := fmt.Sprintf("github-project:%s:%s:%s", project.ID, field.ID, option.ID)
				statusOptions = append(statusOptions, transitionOption{
					id: value, name: fmt.Sprintf("Project #%d %s: %s", project.Number, project.Title, option.Name), value: value,
				})
			}
		}
	}
	labelCounts := make(map[string]int, len(statusOptions))
	for _, option := range statusOptions {
		labelCounts[option.name]++
	}
	values := make([]interface{}, 0, len(statusOptions))
	for _, option := range statusOptions {
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
	return fetchGitHubProjectTransitionMetaWithClient(ctx, client, repoKey)
}

func fetchGitHubProjectTransitionMetaWithClient(ctx *gin.Context, client *github.Client, repoKey string) (interface{}, error) {
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
		var response githubIssueProjectItemsPageResponse
		if err := githubGraphQLRequest(ctx, client, githubIssueProjectItemsQuery, map[string]interface{}{"owner": owner, "repo": repo, "number": issueNumber, "cursor": cursor}, &response); err != nil {
			return nil, err
		}
		if err := githubGraphQLErrors(response.Errors); err != nil {
			return nil, err
		}
		for _, item := range response.Data.Repository.Issue.ProjectItems.Nodes {
			if !item.IsArchived {
				items = append(items, item)
			}
		}
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

type githubProjectStatusSelection struct {
	itemID, projectID, fieldID, optionID, optionName string
}

func (s *GitHubService) updateProjectStatus(ctx *gin.Context, githubClient *github.Client, owner, repo string, issueNumber int, status string) (string, error) {
	status = strings.TrimSpace(status)
	encodedParts := strings.Split(status, ":")
	encoded := len(encodedParts) == 4 && encodedParts[0] == "github-project" &&
		encodedParts[1] != "" && encodedParts[2] != "" && encodedParts[3] != ""
	if strings.HasPrefix(status, "github-project:") && !encoded {
		return "", fmt.Errorf("invalid GitHub Project status selection; choose a current Project status option")
	}
	items, err := fetchIssueProjectItems(ctx, githubClient, owner, repo, issueNumber)
	if err != nil {
		return "", err
	}
	var match *githubProjectStatusSelection
	for _, item := range items {
		if encoded && item.Project.ID != encodedParts[1] {
			continue
		}
		fields, err := fetchProjectFields(ctx, githubClient, item.Project.ID)
		if err != nil {
			return "", err
		}
		for _, field := range fields {
			if !strings.EqualFold(field.Name, "Status") ||
				(encoded && field.ID != encodedParts[2]) {
				continue
			}
			for _, option := range field.Options {
				if (encoded && option.ID == encodedParts[3]) ||
					(!encoded && strings.EqualFold(option.Name, status)) {
					if match != nil {
						return "", fmt.Errorf("GitHub issue %s/%s#%d has an ambiguous Project status selection; refusing transition", owner, repo, issueNumber)
					}
					match = &githubProjectStatusSelection{
						itemID: item.ID, projectID: item.Project.ID, fieldID: field.ID,
						optionID: option.ID, optionName: option.Name,
					}
				}
			}
		}
	}
	if match == nil {
		return "", fmt.Errorf("GitHub issue %s/%s#%d is not linked to Project status %q (it may be missing, stale, or unauthorized)", owner, repo, issueNumber, status)
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
		"fieldId":   match.fieldID,
		"optionId":  match.optionID,
	}, &mutation); err != nil {
		return "", err
	}
	if len(mutation.Errors) > 0 {
		return "", fmt.Errorf("GitHub Project status update failed: %s", mutation.Errors[0].Message)
	}
	return match.optionName, nil
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
var (
	githubImgTagRe      = regexp.MustCompile(`<img\b[^>]*>`)
	githubImgSrcRe      = regexp.MustCompile(`src="([^"]+)"`)
	githubImgAltRe      = regexp.MustCompile(`alt="([^"]*)"`)
	githubMarkdownImgRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
)

// githubUnembeddableImageHosts: private-user-images URLs are JWT-signed with a
// 5-minute TTL, so a URL pasted verbatim by a user is effectively always
// expired by the time Nudgebee reads the comment. (user-attachments/ used to
// be in this list too, but GetAttachment now resolves those via GraphQL
// bodyHTML - see extractGithubUserAttachments.)
var githubUnembeddableImageHosts = []string{
	"private-user-images.githubusercontent.com",
}

var githubUserAttachmentIDRe = regexp.MustCompile(`user-attachments/assets/([0-9a-fA-F-]+)`)

// extractGithubUserAttachments pulls github.com/user-attachments/ image
// references (the <img> or markdown ![]() form) out of the comment body into
// real CommentAttachments, stripping them from the returned text so they
// aren't also left behind as a dead inline image. GetAttachment resolves the
// stored asset ID back to real bytes via GraphQL at render time.

func extractGithubUserAttachments(body string) (string, []models.CommentAttachment) {
	var attachments []models.CommentAttachment
	extract := func(src, alt string) string {
		m := githubUserAttachmentIDRe.FindStringSubmatch(src)
		if m == nil {
			return ""
		}
		label := alt
		if label == "" {
			label = "attached image"
		}
		attachments = append(attachments, models.CommentAttachment{ID: m[1], Name: label, ContentType: "image/*"})
		return attachmentMarker(m[1])
	}

	body = githubImgTagRe.ReplaceAllStringFunc(body, func(tag string) string {
		srcMatch := githubImgSrcRe.FindStringSubmatch(tag)
		if len(srcMatch) < 2 {
			return tag
		}
		alt := ""
		if altMatch := githubImgAltRe.FindStringSubmatch(tag); len(altMatch) >= 2 {
			alt = altMatch[1]
		}
		if marker := extract(srcMatch[1], alt); marker != "" {
			return marker
		}
		return tag
	})

	body = githubMarkdownImgRe.ReplaceAllStringFunc(body, func(tag string) string {
		m := githubMarkdownImgRe.FindStringSubmatch(tag)
		if marker := extract(m[2], m[1]); marker != "" {
			return marker
		}
		return tag
	})

	return body, attachments
}

func isUnembeddableGithubImageURL(url string) bool {
	for _, host := range githubUnembeddableImageHosts {
		if strings.Contains(url, host) {
			return true
		}
	}
	return false
}

// rewriteUnembeddableGithubImages turns an unembeddable image - as a raw <img>
// tag or markdown ![]() - into a plain link. Other hosts pass through untouched.
func rewriteUnembeddableGithubImages(body string) string {
	body = githubImgTagRe.ReplaceAllStringFunc(body, func(tag string) string {
		srcMatch := githubImgSrcRe.FindStringSubmatch(tag)
		if len(srcMatch) < 2 || !isUnembeddableGithubImageURL(srcMatch[1]) {
			return tag
		}
		label := "attached image"
		if altMatch := githubImgAltRe.FindStringSubmatch(tag); len(altMatch) >= 2 && altMatch[1] != "" {
			label = altMatch[1]
		}
		return fmt.Sprintf("[\U0001F4CE %s (view on GitHub)](%s)", label, srcMatch[1])
	})

	body = githubMarkdownImgRe.ReplaceAllStringFunc(body, func(tag string) string {
		m := githubMarkdownImgRe.FindStringSubmatch(tag)
		alt, url := m[1], m[2]
		if !isUnembeddableGithubImageURL(url) {
			return tag
		}
		label := "attached image"
		if alt != "" {
			label = alt
		}
		return fmt.Sprintf("[\U0001F4CE %s (view on GitHub)](%s)", label, url)
	})

	return body
}

// Unlike <img>, <video> is a container element - GitHub emits
// <video src="...">...</video>, so the closing tag must be matched too or
// it's left behind as stray text after the opening tag is replaced.
var githubVideoTagRe = regexp.MustCompile(`<video\b[^>]*>[^<]*</video>`)

// rewriteGithubVideoTags converts a <video> tag (GitHub's markup for a
// dragged/pasted video, on either unembeddable CDN) into a plain link.
// Unlike images, video isn't resolved into a real inline player - that would
// need range-request-aware proxying for seeking/buffering, a much bigger lift
// than a one-shot image fetch - so every video becomes a link. Clicking it
// still works even though embedding doesn't: a clicked link is a top-level
// navigation, which (unlike an auto-fetched <video src>) carries the viewer's
// own GitHub session cookie past SameSite=Lax.
func rewriteGithubVideoTags(body string) string {
	return githubVideoTagRe.ReplaceAllStringFunc(body, func(tag string) string {
		srcMatch := githubImgSrcRe.FindStringSubmatch(tag)
		if len(srcMatch) < 2 {
			return tag
		}
		src := srcMatch[1]
		if !githubUserAttachmentIDRe.MatchString(src) && !strings.Contains(src, "private-user-images.githubusercontent.com") {
			return tag
		}
		return fmt.Sprintf("[\U0001F3AC attached video (view on GitHub)](%s)", src)
	})
}

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
			cleanedBody, attachments := extractGithubUserAttachments(c.GetBody())
			cleanedBody = rewriteUnembeddableGithubImages(cleanedBody)
			cleanedBody = rewriteGithubVideoTags(cleanedBody)
			all = append(all, models.Comments{
				Author:      author,
				Comment:     cleanedBody,
				Created:     c.GetCreatedAt().Format(time.RFC3339),
				Updated:     c.GetUpdatedAt().Format(time.RFC3339),
				Attachments: attachments,
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
	_, err := s.UpdateWithStatus(ctx, config, ticketID, updateFields)
	return err
}

// UpdateWithStatus updates a GitHub issue and returns the status that should
// be persisted locally. Project selectors are resolved to their option names.
func (s *GitHubService) UpdateWithStatus(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) (string, error) {
	if err := utils.ValidateGitHubIssueID(ticketID); err != nil {
		return "", fmt.Errorf("invalid ticket ID: %w", err)
	}

	var githubClient *github.Client
	var err error

	if config.AuthType != "application" {
		githubClient = clients.CreateGithubClient(config.Password)
	} else {
		installationID, err := strconv.ParseInt(config.Password, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid installation ID: %w", err)
		}
		githubClient, err = clients.CreateGithubClientWithInstallationToken(ctx, installationID)
		if err != nil {
			return "", err
		}
	}

	projectKey := updateFields.ProjectKey
	if projectKey == "" && len(config.Projects) > 0 {
		projectKey = config.Projects[0].Key
	}
	if projectKey == "" {
		return "", fmt.Errorf("project_key is required for GitHub (expected owner/repo format)")
	}
	if err := utils.ValidateProjectKey(projectKey); err != nil {
		return "", fmt.Errorf("invalid project key: %w", err)
	}
	parts := strings.Split(projectKey, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid project key %q: expected format 'owner/repo'", projectKey)
	}
	owner, repo := parts[0], parts[1]

	issueNumber, err := strconv.Atoi(ticketID)
	if err != nil {
		return "", fmt.Errorf("invalid issue number: %w", err)
	}

	isRESTState := strings.EqualFold(updateFields.Status, "open") || strings.EqualFold(updateFields.Status, "closed")
	if updateFields.Status != "" && !isRESTState &&
		(len(updateFields.AssigneeList()) > 0 || updateFields.Description != "" || len(updateFields.Labels) > 0) {
		return "", fmt.Errorf("GitHub Project status transitions cannot be combined with REST issue field updates")
	}

	issueRequest := &github.IssueRequest{}
	hasUpdates := false
	persistedStatus := ""

	if updateFields.Status != "" {
		// GitHub uses "open" or "closed" states
		state := strings.ToLower(updateFields.Status)
		if state == "open" || state == "closed" {
			issueRequest.State = &state
			hasUpdates = true
			persistedStatus = state
		} else {
			var err error
			persistedStatus, err = s.updateProjectStatus(ctx, githubClient, owner, repo, issueNumber, updateFields.Status)
			if err != nil {
				return "", err
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
		return persistedStatus, nil
	}

	_, _, err = githubClient.Issues.Edit(ctx, owner, repo, issueNumber, issueRequest)
	if err != nil {
		return "", fmt.Errorf("failed to update GitHub issue: %w", err)
	}

	slog.Info("GitHub issue updated", "ticketID", ticketID)
	return persistedStatus, nil
}

// Transition changes a GitHub issue state or its linked Project v2 Status.
func (s *GitHubService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	_, err := s.TransitionWithStatus(ctx, config, ticketID, status)
	return err
}

func (s *GitHubService) TransitionWithStatus(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) (string, error) {
	return s.UpdateWithStatus(ctx, config, ticketID, models.UpdateFields{Status: status})
}
