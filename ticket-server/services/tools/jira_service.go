package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/cache"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	jira "github.com/andygrunwald/go-jira"
	"github.com/gin-gonic/gin"
	"github.com/trivago/tgo/tcontainer"
)

type JiraService struct{}

var _ ticket.TicketManager = (*JiraService)(nil)

func init() {
	ticket.RegisterTicketManager("jira", &JiraService{})
}

func (s *JiraService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return CreateJiraIssue(config, t)
}

func (s *JiraService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchJiraIssueCreateMeta(config, projectKey)
}

func (s *JiraService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	if t.Comment != "" {
		_, err := AddCustomTicketComment(config, t.TicketID, t.Comment)
		return err
	}
	return AddTicketComment(config, t.TicketID, t.Title, t.Description)
}

func (s *JiraService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	return GetTicketComments(config, ticketID)
}

func (s *JiraService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	jiraClient, err := clients.CreateJiraClient(config.AuthType, config.Username, config.Password, config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jira client: %w", err)
	}

	issue, err := FetchFullIssueDetails(jiraClient, ticketID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Jira issue: %w", err)
	}

	var assignee string
	if issue.Fields.Assignee != nil {
		assignee = issue.Fields.Assignee.DisplayName
	}

	// Jira issues carry a single assignee; mirror it into Assignees so workflow
	// authors can treat the list field uniformly across platforms.
	var assignees []string
	if assignee != "" {
		assignees = []string{assignee}
	}

	var reporter string
	if issue.Fields.Reporter != nil {
		reporter = issue.Fields.Reporter.DisplayName
	}

	var createdAt *time.Time
	if !time.Time(issue.Fields.Created).IsZero() {
		t := time.Time(issue.Fields.Created)
		createdAt = &t
	}

	var updatedAt *time.Time
	if !time.Time(issue.Fields.Updated).IsZero() {
		t := time.Time(issue.Fields.Updated)
		updatedAt = &t
	}

	var priority string
	if issue.Fields.Priority != nil {
		priority = issue.Fields.Priority.Name
	}

	var status string
	if issue.Fields.Status != nil {
		status = issue.Fields.Status.Name
	}

	return &models.Ticket{
		TicketID:    issue.Key,
		Title:       issue.Fields.Summary,
		Description: issue.Fields.Description,
		Status:      status,
		Severity:    priority,
		Assignee:    assignee,
		Assignees:   assignees,
		Reporter:    reporter,
		Labels:      issue.Fields.Labels,
		Platform:    "jira",
		ProjectKey:  issue.Fields.Project.Key,
		URL:         clients.JiraBrowseURL(jiraClient, issue.Key),
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Raw:         marshalToMap(issue),
	}, nil
}

func CreateJiraIssue(configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get Jira client:", "error", slog.AnyValue(err))
		return ticket, err
	}

	additionalFields, ok := ticket.AdditionalFields.(map[string]interface{})
	if !ok {
		additionalFields = make(map[string]interface{})
	}

	fields, err := buildJiraIssueFields(ticket, additionalFields,
		func(assignee string) *jira.User { return resolveJiraAssignee(jiraClient, assignee) },
		func(parentKey string) error { return validateParentIssue(jiraClient, parentKey) })
	if err != nil {
		slog.Error("Error building Jira issue fields:", "error", slog.AnyValue(err))
		return ticket, err
	}

	fields.Priority = resolveJiraPriority(jiraClient, fields.Priority)
	issue := jira.Issue{
		Fields: fields,
	}

	issueResp, _, err := jiraClient.Issue.Create(&issue)
	if err != nil {
		slog.Error("Error creating Jira issue:", "error", slog.AnyValue(err))
		createErr := jiraCreateError(err)
		if options := jiraPriorityOptions(jiraClient, err); options != "" {
			createErr = fmt.Errorf("%w (valid priorities: %s)", createErr, options)
		}
		return ticket, createErr
	}
	slog.Info("Jira issue created:", "Key", issueResp.Key)

	detailedIssue, err := FetchFullIssueDetails(jiraClient, issueResp.Key)
	if err != nil {
		slog.Error("Error fetching Jira issue:", "error", slog.AnyValue(err))
		return ticket, err
	}

	if detailedIssue.Fields.Assignee != nil {
		ticket.Assignee = detailedIssue.Fields.Assignee.DisplayName
	}
	if detailedIssue.Fields.Status != nil {
		ticket.Status = detailedIssue.Fields.Status.Name
	}
	ticket.TicketID = detailedIssue.Key
	if detailedIssue.Fields.Priority != nil {
		ticket.Severity = detailedIssue.Fields.Priority.Name
	}
	ticket.URL = clients.JiraBrowseURL(jiraClient, detailedIssue.Key)
	ticket.Platform = "jira"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

var jiraNumericIDRe = regexp.MustCompile(`^\d+$`)

// jiraPriority sends an all-digit value as a priority ID and anything else as a
// priority name, so create and update read the same input the same way.
func jiraPriority(value string) *jira.Priority {
	value = strings.TrimSpace(value)
	if jiraNumericIDRe.MatchString(value) {
		return &jira.Priority{ID: value}
	}
	return &jira.Priority{Name: value}
}

// resolveJiraPriority swaps a priority name for the instance's ID for it. Jira
// matches a priority name case-sensitively ("Low" is accepted, "LoW" is not), so the
// name is looked up ignoring case in the instance's priority list. Best-effort: with
// no list or no match the name goes through unchanged and Jira gives its own error.
func resolveJiraPriority(client *jira.Client, priority *jira.Priority) *jira.Priority {
	if priority == nil || priority.Name == "" {
		return priority
	}
	if client == nil {
		return priority
	}
	for _, known := range fetchJiraPriorities(client) {
		if known.ID != "" && strings.EqualFold(strings.TrimSpace(known.Name), strings.TrimSpace(priority.Name)) {
			return &jira.Priority{ID: known.ID}
		}
	}
	return priority
}

// buildJiraIssueFields assembles the field set sent to Jira's create API.
// Keys that back a typed jira.IssueFields slot must never reach Unknowns:
// go-jira hoists Unknowns over the typed struct at marshal time, so a raw
// duplicate silently replaces the typed slot with the wrong wire shape.
func buildJiraIssueFields(ticket models.Ticket, additionalFields map[string]interface{},
	resolveAssignee func(string) *jira.User, validateParent func(string) error) (*jira.IssueFields, error) {
	fields := &jira.IssueFields{
		Description: ticket.Description,
		Type: jira.IssueType{
			Name: ticket.TicketType,
		},
		Project: jira.Project{
			Key: ticket.ProjectKey,
		},
		Summary: ticket.Title,
	}

	fields.Labels = []string{"nudgebee", ticket.Source}
	switch existing := additionalFields["labels"].(type) {
	case []interface{}:
		for _, label := range existing {
			if strLabel, ok := label.(string); ok {
				fields.Labels = append(fields.Labels, strLabel)
			}
		}
	case []string:
		fields.Labels = append(fields.Labels, existing...)
	}

	if ticket.Severity != "" {
		// Severity is the create-meta option value (a priority ID) from the UI, but a
		// priority name from workflows and the assistant; Jira accepts either.
		fields.Priority = jiraPriority(ticket.Severity)
	}

	assignee := ticket.Assignee
	if assignee == "" {
		// Workflow payloads can carry assignee inside additional_fields instead.
		if s, ok := jiraOptionIDString(additionalFields["assignee"]); ok {
			assignee = s
		} else if ref, ok := additionalFields["assignee"].(map[string]interface{}); ok {
			if s, ok := jiraScalarString(ref["accountId"]); ok {
				assignee = s
			}
		}
	}
	fields.Assignee = resolveAssignee(assignee)

	fields.Unknowns = make(tcontainer.MarshalMap)
	for key, value := range additionalFields {
		switch key {
		case "labels", "assignee":
			// consumed into their typed slots above
		case "parent":
			parentKey, ok := jiraOptionIDString(value)
			if !ok {
				slog.Warn("jira create: skipping empty parent value", "value", slog.AnyValue(value))
				continue
			}
			if err := validateParent(parentKey); err != nil {
				return nil, err
			}
			fields.Parent = &jira.Parent{
				Key: parentKey,
			}
		case "priority":
			// Severity wins; a raw duplicate here would hoist over the typed slot.
			if fields.Priority != nil {
				continue
			}
			if id, ok := jiraOptionIDString(value); ok {
				fields.Priority = &jira.Priority{
					ID: id,
				}
			}
		default:
			fields.Unknowns[key] = value
		}
	}

	return fields, nil
}

// resolveJiraAssignee maps an assignee input to the user reference the issue
// APIs accept. Cloud identifies users by accountId and searches with `query`;
// Server/Data Center identifies them by name and searches with `username`.
// Returns nil for an empty input.
func resolveJiraAssignee(jiraClient *jira.Client, assignee string) *jira.User {
	if assignee == "" {
		return nil
	}
	if len(strings.Split(assignee, ":")) == 2 {
		return &jira.User{AccountID: assignee}
	}
	cloud, err := clients.IsJiraCloud(jiraClient)
	if err != nil {
		slog.Warn("Jira: could not determine deployment type for assignee lookup", "error", slog.AnyValue(err))
		return &jira.User{EmailAddress: assignee}
	}
	if cloud {
		if u := lookupJiraUser(jiraClient, "query", assignee); u != nil && u.AccountID != "" {
			return &jira.User{AccountID: u.AccountID}
		}
		return &jira.User{EmailAddress: assignee}
	}
	if u := lookupJiraUser(jiraClient, "username", assignee); u != nil && u.Name != "" {
		return &jira.User{Name: u.Name}
	}
	return &jira.User{Name: assignee}
}

// jiraUserRef reduces a resolved user to the single identifier the update
// API needs, so the wire body carries no empty struct fields.
func jiraUserRef(u *jira.User) map[string]string {
	switch {
	case u == nil:
		return nil
	case u.AccountID != "":
		return map[string]string{"accountId": u.AccountID}
	case u.Name != "":
		return map[string]string{"name": u.Name}
	default:
		return map[string]string{"emailAddress": u.EmailAddress}
	}
}

// lookupJiraUser searches users by the given query parameter and returns the
// result whose email or name equals the input, else the first result.
func lookupJiraUser(jiraClient *jira.Client, param, value string) *jira.User {
	endpoint := fmt.Sprintf("rest/api/2/user/search?%s=%s", param, url.QueryEscape(value))
	req, err := jiraClient.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil
	}
	var users []jira.User
	if _, err := jiraClient.Do(req, &users); err != nil {
		slog.Warn("Jira: user search failed", "error", slog.AnyValue(err))
		return nil
	}
	for i := range users {
		if strings.EqualFold(users[i].EmailAddress, value) || strings.EqualFold(users[i].Name, value) {
			return &users[i]
		}
	}
	if len(users) > 0 {
		return &users[0]
	}
	return nil
}

// jiraCreateError surfaces Jira's per-field validation detail, which the
// default jira.Error string flattens to a generic "Status code: 400".
func jiraCreateError(err error) error {
	var jerr *jira.Error
	if !errors.As(err, &jerr) {
		return err
	}
	parts := append([]string{}, jerr.ErrorMessages...)
	fieldErrors := make([]string, 0, len(jerr.Errors))
	for field, msg := range jerr.Errors {
		fieldErrors = append(fieldErrors, field+": "+msg)
	}
	sort.Strings(fieldErrors)
	parts = append(parts, fieldErrors...)
	if len(parts) == 0 {
		return err
	}
	return fmt.Errorf("jira rejected the create request: %s", strings.Join(parts, "; "))
}

// jiraPriorityOptions lists the instance's priority names, but only when Jira just
// rejected the priority, so the common path makes no extra call.
func jiraPriorityOptions(client *jira.Client, err error) string {
	var jerr *jira.Error
	if !errors.As(err, &jerr) {
		return ""
	}
	if _, rejected := jerr.Errors["priority"]; !rejected {
		return ""
	}
	names := make([]string, 0)
	for _, p := range fetchJiraPriorities(client) {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

func validateParentIssue(client *jira.Client, parentKey string) error {
	issue, _, err := client.Issue.Get(parentKey, nil)
	if err != nil {
		return fmt.Errorf("error fetching parent issue details: %v", err)
	}
	if issue == nil || issue.Fields == nil {
		return fmt.Errorf("parent issue %s returned nil or empty fields", parentKey)
	}
	// Jira accepts any standard issue type as a parent (Task, Story, Bug, Epic);
	// only a sub-task can never parent another issue.
	if issue.Fields.Type.Subtask {
		return fmt.Errorf("parent issue %s is a sub-task and cannot be a parent", parentKey)
	}
	return nil
}

// FetchFullIssueDetails Function to fetch full details of a Jira issue
func FetchFullIssueDetails(jiraClient *jira.Client, issueKey string) (*jira.Issue, error) {
	jiraIssue, _, err := jiraClient.Issue.Get(issueKey, nil)
	if err != nil {
		slog.Debug("Error fetching Jira issue details:", "error", slog.AnyValue(err))
		return nil, err
	}

	return jiraIssue, nil
}

const (
	// jiraSyncBatchSize bounds how many ticket keys go into one "key in (...)"
	// JQL search, keeping each sync's search requests well under Jira's
	// practical JQL/URL length limits.
	jiraSyncBatchSize = 100
	// jiraSyncMaxRetries bounds 429 (rate-limit) retries per batch.
	jiraSyncMaxRetries = 3
	// jiraSyncMaxRetryWait caps how long a Retry-After header is honored so a
	// large value can't stall the sync job.
	jiraSyncMaxRetryWait = 30 * time.Second
)

// FetchIssuesByKeys fetches many Jira issues via one or more "key in (...)"
// JQL searches (jiraSyncBatchSize keys per search) instead of one GET per
// key — the per-GET pattern is exactly what Jira Cloud rate-limits under
// load. Used by the ticket-status sync job. A 429 is retried up to
// jiraSyncMaxRetries times honoring Retry-After instead of being treated as
// a generic fetch failure. A batch that still fails after retries does not
// abort the rest: every batch is attempted, and the issues from the ones
// that succeeded are returned alongside the joined errors from the ones that
// didn't, so one bad batch (e.g. a malformed key tripping the JQL parser)
// can't stop sync from reaching tickets in later, unrelated batches.
func FetchIssuesByKeys(ctx context.Context, jiraClient *jira.Client, keys []string) ([]jira.Issue, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	cloud, err := clients.IsJiraCloud(jiraClient)
	if err != nil {
		return nil, fmt.Errorf("failed to detect Jira deployment: %w", err)
	}

	var all []jira.Issue
	var errs []error
	for start := 0; start < len(keys); start += jiraSyncBatchSize {
		batch := keys[start:min(start+jiraSyncBatchSize, len(keys))]
		issues, err := fetchJiraIssueBatch(ctx, jiraClient, cloud, batch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		all = append(all, issues...)
	}
	return all, errors.Join(errs...)
}

// fetchJiraIssueBatch runs a single "key in (...)" JQL search for batch,
// retrying on 429 per the jiraSync* bounds above.
func fetchJiraIssueBatch(ctx context.Context, jiraClient *jira.Client, cloud bool, batch []string) ([]jira.Issue, error) {
	quoted := make([]string, len(batch))
	for i, k := range batch {
		quoted[i] = strconv.Quote(k)
	}
	jql := fmt.Sprintf("key in (%s)", strings.Join(quoted, ","))

	for attempt := 0; ; attempt++ {
		var issues []jira.Issue
		var resp *jira.Response
		var err error
		if cloud {
			issues, resp, err = searchJiraIssuesJQLCloud(ctx, jiraClient, jql, len(batch))
		} else {
			issues, resp, err = jiraClient.Issue.SearchWithContext(ctx, jql, &jira.SearchOptions{MaxResults: len(batch)})
		}
		if err == nil {
			return issues, nil
		}
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests && attempt < jiraSyncMaxRetries {
			wait := jiraRetryWait(resp.Header.Get("Retry-After"), attempt)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}
		return nil, fmt.Errorf("jira batch search failed: %w", err)
	}
}

// searchJiraIssuesJQLCloud runs jql against Jira Cloud's rest/api/2/search/jql
// via a POST body (unlike the SDK's SearchV2JQLWithContext, which puts jql in
// the query string — a problem for a "key in (...)" list long enough to
// approach URL length limits). Mirrors the request shape walkJiraCloud already
// uses for paginated listing, without that function's cursor caching, which a
// one-shot batch-by-keys fetch has no use for.
func searchJiraIssuesJQLCloud(ctx context.Context, jiraClient *jira.Client, jql string, maxResults int) ([]jira.Issue, *jira.Response, error) {
	body := map[string]interface{}{
		"jql":        jql,
		"fields":     []string{"summary", "status", "priority", "assignee"},
		"maxResults": maxResults,
	}
	req, err := jiraClient.NewRequestWithContext(ctx, "POST", "rest/api/2/search/jql", body)
	if err != nil {
		return nil, nil, err
	}
	var result jiraSearchPage
	resp, err := jiraClient.Do(req, &result)
	return result.Issues, resp, err
}

// jiraRetryWait resolves the wait before a 429 retry: the Retry-After header
// (seconds) when present and sane, otherwise exponential backoff (1s, 2s,
// 4s…). Capped at jiraSyncMaxRetryWait.
func jiraRetryWait(header string, attempt int) time.Duration {
	wait := time.Duration(1<<attempt) * time.Second
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		}
	}
	if wait > jiraSyncMaxRetryWait {
		wait = jiraSyncMaxRetryWait
	}
	return wait
}

func AddTicketComment(configuration models.TicketConfigurations, ticketId, title, description string) error {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Failed to create Jira client", "error", err, "configurationID", configuration.ID)
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	commentBody := fmt.Sprintf("Found *%s* again at *%s* \n\n*Description:*\n%s", title, time.Now().Format("02 Jan 2006 15:04:05"), description)
	c := &jira.Comment{
		Body: commentBody,
	}

	comment, _, err := jiraClient.Issue.AddComment(ticketId, c)
	if err != nil {
		slog.Debug("Error fetching Jira issue details: %v", "error", slog.AnyValue(err))
		return err
	}
	slog.Info("ticket comment added for %s, details - %s", ticketId, comment.Body)
	return nil
}

// FetchJiraIssueCreateMeta Function to fetch create meta of a Jira issue
func FetchJiraIssueCreateMeta(configuration models.TicketConfigurations, projectKey string) (any, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get jira client for configuration: "+configuration.ID, "error", slog.AnyValue(err))
		return nil, err
	}

	createMetaInfo, _, err := jiraClient.Issue.GetCreateMeta(projectKey)
	if err != nil {
		slog.Warn("Jira: createmeta expand endpoint unavailable, using per-issue-type endpoints", "project", projectKey, "error", slog.AnyValue(err))
		createMetaInfo, err = fetchJiraCreateMetaByIssueType(jiraClient, projectKey)
		if err != nil {
			slog.Error("Unable to get jira create meta for project key: "+projectKey, "error", slog.AnyValue(err))
			return nil, err
		}
	}

	// Live reference data so Severity and Assignee don't depend on per-issue-type
	// createmeta quirks (Jira commonly omits priority allowedValues, and the
	// assignee autocomplete returns nothing under GDPR-strict / no-permission).
	// Both are best-effort: failures degrade to whatever createmeta provided.
	priorities := fetchJiraPriorities(jiraClient)
	users := fetchJiraAssignableUsers(jiraClient, projectKey)

	return sanitizeJiraMeta(createMetaInfo, priorities, users), nil
}

// jiraPage is the paged envelope the per-issue-type createmeta endpoints return.
type jiraPage[T any] struct {
	Values []T  `json:"values"`
	IsLast bool `json:"isLast"`
}

// fetchJiraCreateMetaByIssueType assembles create-meta from the
// createmeta/{project}/issuetypes and createmeta/{project}/issuetypes/{id}
// endpoints, which replaced the single expand endpoint (Data Center 9 removed
// it). Those return fields as a list carrying fieldId; they are re-keyed into
// the map shape the expand endpoint returned so one sanitizer serves both.
func fetchJiraCreateMetaByIssueType(jiraClient *jira.Client, projectKey string) (*jira.CreateMetaInfo, error) {
	base := "rest/api/2/issue/createmeta/" + url.PathEscape(projectKey) + "/issuetypes"
	var issueTypes []*jira.MetaIssueType
	if err := fetchJiraPages(jiraClient, base, &issueTypes); err != nil {
		return nil, fmt.Errorf("listing issue types: %w", err)
	}
	for _, issueType := range issueTypes {
		var fields []map[string]interface{}
		if err := fetchJiraPages(jiraClient, base+"/"+url.PathEscape(issueType.Id), &fields); err != nil {
			return nil, fmt.Errorf("listing fields for issue type %s: %w", issueType.Name, err)
		}
		issueType.Fields = tcontainer.MarshalMap{}
		for _, field := range fields {
			id, _ := field["fieldId"].(string)
			if id == "" {
				continue
			}
			if _, ok := field["key"]; !ok {
				field["key"] = id
			}
			issueType.Fields[id] = field
		}
	}
	return &jira.CreateMetaInfo{Projects: []*jira.MetaProject{{Key: projectKey, IssueTypes: issueTypes}}}, nil
}

// fetchJiraPages follows startAt/isLast paging and appends every value to out.
// It fails rather than returns a partial list if paging never terminates.
func fetchJiraPages[T any](jiraClient *jira.Client, endpoint string, out *[]T) error {
	const (
		pageSize = 200
		maxPages = 50
	)
	startAt := 0
	for range maxPages {
		req, err := jiraClient.NewRequest("GET", fmt.Sprintf("%s?startAt=%d&maxResults=%d", endpoint, startAt, pageSize), nil)
		if err != nil {
			return err
		}
		var page jiraPage[T]
		if _, err := jiraClient.Do(req, &page); err != nil {
			return err
		}
		*out = append(*out, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			return nil
		}
		startAt += len(page.Values)
	}
	return fmt.Errorf("%s: paging did not finish within %d pages", endpoint, maxPages)
}

// fetchJiraPriorities returns the instance-level priority scheme (one cheap call),
// used to seed/synthesize the Severity field. Returns nil on error (caller degrades).
func fetchJiraPriorities(jiraClient *jira.Client) []jira.Priority {
	priorities, _, err := jiraClient.Priority.GetList()
	if err != nil {
		slog.Warn("Jira: failed to fetch priorities for severity seed", "error", slog.AnyValue(err))
		return nil
	}
	return priorities
}

// fetchJiraAssignableUsers returns the first page of users assignable to the project,
// used to seed the Assignee dropdown so it isn't blank on open. Live typeahead still
// refines via the field's autoCompleteUrl. Returns nil on error / no permission.
func fetchJiraAssignableUsers(jiraClient *jira.Client, projectKey string) []jira.User {
	if projectKey == "" {
		return nil // assignable-search requires a project; avoid a guaranteed 400
	}
	endpoint := fmt.Sprintf("rest/api/2/user/assignable/search?project=%s&maxResults=50", url.QueryEscape(projectKey))
	req, err := jiraClient.NewRequest("GET", endpoint, nil)
	if err != nil {
		slog.Warn("Jira: failed to build assignable-users request", "error", slog.AnyValue(err))
		return nil
	}
	var users []jira.User
	if _, err := jiraClient.Do(req, &users); err != nil {
		slog.Warn("Jira: failed to fetch assignable users for assignee seed", "error", slog.AnyValue(err))
		return nil
	}
	return users
}

// jiraPrioritiesToAllowedValues maps the priority scheme to create-meta options.
// value is the priority ID — what CreateJiraIssue sets via jira.Priority{ID}.
func jiraPrioritiesToAllowedValues(priorities []jira.Priority) []interface{} {
	vals := make([]interface{}, 0, len(priorities))
	for _, p := range priorities {
		if p.ID == "" {
			continue
		}
		vals = append(vals, map[string]interface{}{"id": p.ID, "name": p.Name, "value": p.ID})
	}
	return vals
}

// jiraUsersToAllowedValues maps users to assignee options. value is the accountId
// (Cloud) or name/key (Server/DC) — the identifier CreateJiraIssue assigns by.
func jiraUsersToAllowedValues(users []jira.User) []interface{} {
	vals := make([]interface{}, 0, len(users))
	for _, u := range users {
		id := u.AccountID
		if id == "" {
			id = u.Name
		}
		if id == "" {
			continue
		}
		display := u.DisplayName
		if display == "" {
			display = u.EmailAddress
		}
		vals = append(vals, map[string]interface{}{"id": id, "name": display, "value": id})
	}
	return vals
}

func sanitizeJiraMeta(meta *jira.CreateMetaInfo, priorities []jira.Priority, users []jira.User) map[string]interface{} {
	var templates []Template

	// priority is a mustField so it's retained even when Jira marks it optional and
	// ships no allowedValues; the seed below then fills it from the instance scheme.
	mustFields := []string{"assignee", "description", "issuetype", "labels", "priority"}
	ignoreFields := []string{"reporter", "project", "issuetype"}

	priorityValues := jiraPrioritiesToAllowedValues(priorities)
	userValues := jiraUsersToAllowedValues(users)

	for _, project := range meta.Projects {
		for _, issueType := range project.IssueTypes {
			fields := make(map[string]FieldInfo)
			for fieldName, field := range issueType.Fields {
				fieldMap, ok := field.(map[string]interface{})
				if !ok {
					continue
				}
				// Jira occasionally omits `required` on certain custom field types,
				// in which case the assertion would panic and 500 the whole call.
				required, _ := fieldMap["required"].(bool)
				allowedValues, _ := fieldMap["allowedValues"].([]interface{})
				hasAllowedValues := len(allowedValues) > 0
				// Include fields that are required, must-have for our UX, or carry
				// concrete allowedValues. The last clause covers select fields like
				// `priority` that Jira marks optional but still ships options for —
				// without it the Severity dropdown renders empty.
				if (required || contains(mustFields, fieldName) || hasAllowedValues) && !contains(ignoreFields, fieldName) {
					fieldKey, _ := fieldMap["key"].(string)
					if fieldKey == "" {
						fieldKey = fieldName
					}
					type_, supported := normalizeJiraFieldType(fieldKey, fieldMap["schema"])
					if !supported {
						// Drop rather than emit a type no renderer understands.
						if required {
							slog.Error("Jira create-meta: dropping required field with unsupported type — this issue type cannot be created from the UI",
								"field", fieldName, "issueType", issueType.Name, "schema", slog.AnyValue(fieldMap["schema"]))
						} else {
							slog.Warn("Jira create-meta: dropping field with unsupported type",
								"field", fieldName, "issueType", issueType.Name)
						}
						continue
					}
					fi := FieldInfo{
						Name:     fmt.Sprintf("%v", fieldMap["name"]),
						Key:      fieldKey,
						Required: required,
						Type:     type_,
						Group:    jiraFieldGroup(fieldName),
					}
					if autoComplete, ok := fieldMap["autoCompleteUrl"].(string); ok {
						fi.AutoCompleteUrl = autoComplete
					}
					if hasAllowedValues {
						fi.AllowedValues = allowedValues
					}
					fields[fieldName] = fi
				}
			}

			// Severity resilience: fill priority from the instance scheme when
			// createmeta omitted its allowedValues, or synthesize it when the field
			// was dropped entirely. Assignee seed: fill the first page of users while
			// keeping the autoCompleteUrl so live typeahead still refines.
			applyJiraSeveritySeed(fields, priorityValues)
			applyJiraAssigneeSeed(fields, userValues)

			templates = append(templates, Template{
				Name:   issueType.Name,
				Fields: fields,
			})
		}
	}
	data := make(map[string]interface{})
	data["data"] = templates
	return data
}

// jiraFieldGroup tags the create-meta fields that back a basic (static) UI field so
// the frontend renders each concept once. priority -> the Severity source.
func jiraFieldGroup(fieldName string) string {
	switch fieldName {
	case "priority":
		return FieldGroupSeverity
	case "summary":
		return FieldGroupTitle
	case "description":
		return FieldGroupDescription
	}
	return ""
}

// applyJiraSeveritySeed guarantees a usable priority (Severity) field: live
// allowedValues win; otherwise fill from the instance scheme; if the field was
// dropped entirely, synthesize it. No-op when the scheme couldn't be fetched.
func applyJiraSeveritySeed(fields map[string]FieldInfo, priorityValues []interface{}) {
	if len(priorityValues) == 0 {
		return
	}
	if fi, ok := fields["priority"]; ok {
		if len(fi.AllowedValues) == 0 {
			fi.AllowedValues = priorityValues
		}
		fi.Group = FieldGroupSeverity
		fields["priority"] = fi
		return
	}
	fields["priority"] = FieldInfo{
		AllowedValues: priorityValues,
		Key:           "priority",
		Name:          "Priority",
		Required:      false,
		Type:          "select",
		Group:         FieldGroupSeverity,
	}
}

// applyJiraAssigneeSeed fills the assignee dropdown with the first page of users
// when createmeta returned none, while preserving autoCompleteUrl for live search.
// Empty user list (e.g. no Browse Users permission) leaves the field untouched.
func applyJiraAssigneeSeed(fields map[string]FieldInfo, userValues []interface{}) {
	fi, ok := fields["assignee"]
	if !ok {
		return
	}
	if len(fi.AllowedValues) == 0 && len(userValues) > 0 {
		fi.AllowedValues = userValues
	}
	fields["assignee"] = fi
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func QueryIssueFieldDetails(ctx *gin.Context, configuration models.TicketConfigurations, request models.FieldValuesRequest) (any, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get Jira client for configuration: "+configuration.ID, "error", slog.AnyValue(err))
		return nil, err
	}

	// Validate that the auto-complete URL belongs to the configured Jira instance
	parsedInputURL, err := url.Parse(request.Input.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid auto-complete URL: %v", err)
	}
	parsedConfigURL, err := url.Parse(clients.NormalizeJiraURL(configuration.URL))
	if err != nil {
		return nil, fmt.Errorf("invalid Jira configuration URL: %v", err)
	}
	if !strings.EqualFold(parsedInputURL.Host, parsedConfigURL.Host) {
		return nil, fmt.Errorf("auto-complete URL host %q does not match configured Jira host %q", parsedInputURL.Host, parsedConfigURL.Host)
	}

	fullURL := request.Input.URL + request.Input.SearchTerm

	// Fetch details from Jira
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := jiraClient.Do(req, nil)
	// Register close before checking err: the library can return a non-nil
	// response alongside an error (e.g. non-2xx), and we must drain/close it.
	if resp != nil && resp.Body != nil {
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				slog.Error("Failed to close response body:", "error", slog.AnyValue(cerr))
			}
		}()
	}
	if err != nil {
		slog.Error("Unable to perform request to Jira: "+configuration.ID, "error", slog.AnyValue(err))
		return nil, err
	}

	var fieldValues []models.FieldValue
	switch request.Input.KEY {
	case "assignee":
		fieldValues, err = usersToFieldValues(jiraClient, resp, fieldValues)
	case "labels":
		fieldValues, err = labelsToFieldValues(resp, fieldValues)
	default:
		return nil, fmt.Errorf("unsupported field key: %q (supported keys: assignee, labels)", request.Input.KEY)
	}

	if err != nil {
		slog.Error("Unable to get field values for configuration: "+configuration.ID+" and URL: "+fullURL, "error", slog.AnyValue(err))
		return nil, err
	}

	return map[string]interface{}{"data": fieldValues}, nil
}

func labelsToFieldValues(resp *jira.Response, fieldValues []models.FieldValue) ([]models.FieldValue, error) {
	var suggestionsResponse models.SuggestionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&suggestionsResponse); err != nil {
		return nil, err
	}

	for _, suggestion := range suggestionsResponse.Suggestions {
		if suggestion.Label != "" {
			fieldValues = append(fieldValues, models.FieldValue{
				ID:   suggestion.Label,
				Name: suggestion.Label,
			})
		}
	}

	return fieldValues, nil
}

// usersToFieldValues maps a user picker's autocomplete results to field
// values. ID is the identifier the deployment assigns by (accountId on Cloud,
// name on Server/Data Center); Value is the email, looked up when the picker
// omits it.
func usersToFieldValues(client *jira.Client, resp *jira.Response, fieldValues []models.FieldValue) ([]models.FieldValue, error) {
	var users []jira.User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, err
	}

	searchParam := ""
	for _, user := range users {
		id := user.AccountID
		if id == "" {
			id = user.Name
		}
		email := user.EmailAddress
		if email == "" {
			if searchParam == "" {
				searchParam = jiraUserSearchParam(client)
			}
			if u := lookupJiraUser(client, searchParam, user.DisplayName); u != nil {
				email = u.EmailAddress
			}
		}
		fieldValues = append(fieldValues, models.FieldValue{
			ID:    id,
			Name:  user.DisplayName,
			Value: email,
		})
	}
	return fieldValues, nil
}

// jiraUserSearchParam is the user/search query parameter the deployment
// accepts: Cloud takes `query`, Server/Data Center takes `username`. Cloud is
// assumed when the deployment cannot be determined, matching the older
// behaviour of this lookup.
func jiraUserSearchParam(client *jira.Client) string {
	cloud, err := clients.IsJiraCloud(client)
	if err != nil {
		slog.Warn("Jira: could not determine deployment type for user lookup", "error", slog.AnyValue(err))
		return "query"
	}
	if cloud {
		return "query"
	}
	return "username"
}

func GetTicketComments(config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	jc, err := clients.CreateJiraClient(config.AuthType, config.Username, config.Password, config.URL)
	if err != nil {
		return nil, err
	}

	return fetchCommentsFromJira(ticketID, jc)
}

func fetchCommentsFromJira(ticketID string, jc *jira.Client) ([]models.Comments, error) {
	const (
		pageSize = 100
		// Hard ceiling prevents an infinite loop when the API reports a
		// positive Total but consistently returns an empty Comments slice
		// (e.g. permission boundary mid-result or items deleted between pages).
		maxPages = 500
	)
	startAt := 0
	var all []models.Comments

	for page := 0; page < maxPages; page++ {
		endpoint := fmt.Sprintf(
			"/rest/api/2/issue/%s/comment?startAt=%d&maxResults=%d&orderBy=created",
			url.PathEscape(ticketID), startAt, pageSize,
		)

		var result struct {
			StartAt    int            `json:"startAt"`
			MaxResults int            `json:"maxResults"`
			Total      int            `json:"total"`
			Comments   []jira.Comment `json:"comments"`
		}

		req, err := jc.NewRequest("GET", endpoint, nil)
		if err != nil {
			slog.Error("Failed to create new Jira request", "endpoint", endpoint, "error", err)
			return nil, fmt.Errorf("failed to create new Jira request for %s: %w", endpoint, err)
		}
		if _, err = jc.Do(req, &result); err != nil {
			return nil, err
		}

		for _, c := range result.Comments {
			all = append(all, models.Comments{
				Author:  c.Author.DisplayName,
				Comment: c.Body,
				Created: c.Created,
				Updated: c.Updated,
			})
		}

		// Stop when we have everything, the page was empty, or Total is zero.
		if result.Total == 0 || len(all) >= result.Total || len(result.Comments) == 0 {
			break
		}
		startAt += len(result.Comments)
	}

	return all, nil
}

func AddCustomTicketComment(configuration models.TicketConfigurations, ticketId, comment string) ([]models.Comments, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Failed to create Jira client", "error", err, "configurationID", configuration.ID)
		return []models.Comments{}, fmt.Errorf("failed to create Jira client: %w", err)
	}

	c := &jira.Comment{
		Body: comment,
	}

	_, _, err = jiraClient.Issue.AddComment(ticketId, c)
	if err != nil {
		slog.Debug("Error fetching Jira issue details: %v", "error", slog.AnyValue(err))
		return []models.Comments{}, err
	}

	return fetchCommentsFromJira(ticketId, jiraClient)
}

// jqlDateLayout is the JQL date format ("yyyy-MM-dd").
const jqlDateLayout = "2006-01-02"

// jqlDateTimeLayout is the JQL datetime format ("yyyy-MM-dd HH:mm").
const jqlDateTimeLayout = "2006-01-02 15:04"

// validateJQLDate ensures a date filter value is a valid date before it is
// interpolated into the JQL, so a malformed value returns a clear, field-specific
// error instead of a cryptic Jira search failure. ListParams dates are ISO 8601
// datetimes, but a plain YYYY-MM-DD is also accepted. The returned string is
// normalized to a JQL-compatible form ("yyyy-MM-dd" or "yyyy-MM-dd HH:mm").
func validateJQLDate(field, value string) (string, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Format(jqlDateTimeLayout), nil
	}
	if t, err := time.Parse(jqlDateLayout, value); err == nil {
		return t.Format(jqlDateLayout), nil
	}
	return "", fmt.Errorf("invalid %s: %q must be an ISO 8601 datetime or YYYY-MM-DD date", field, value)
}

// List retrieves tickets from Jira using JQL search.
func (s *JiraService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	jiraClient, err := clients.CreateJiraClient(config.AuthType, config.Username, config.Password, config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jira client: %w", err)
	}

	// Build JQL query
	var jqlParts []string
	jqlParts = append(jqlParts, fmt.Sprintf("project = %q", params.ProjectKey))

	if params.Status != "" {
		jqlParts = append(jqlParts, fmt.Sprintf("status = %q", params.Status))
	}
	if params.Priority != "" {
		jqlParts = append(jqlParts, fmt.Sprintf("priority = %q", params.Priority))
	}
	if params.Assignee != "" {
		jqlParts = append(jqlParts, fmt.Sprintf("assignee = %q", params.Assignee))
	}
	if params.CreatedAfter != "" {
		formatted, err := validateJQLDate("created_after", params.CreatedAfter)
		if err != nil {
			return nil, err
		}
		jqlParts = append(jqlParts, fmt.Sprintf("created >= %q", formatted))
	}
	if params.CreatedBefore != "" {
		formatted, err := validateJQLDate("created_before", params.CreatedBefore)
		if err != nil {
			return nil, err
		}
		jqlParts = append(jqlParts, fmt.Sprintf("created <= %q", formatted))
	}

	jql := strings.Join(jqlParts, " AND ")
	countJQL := jql

	// Add ORDER BY
	orderField := "created"
	if params.SortBy == "updated_at" {
		orderField = "updated"
	}
	orderDir := "DESC"
	if params.SortOrder == "asc" {
		orderDir = "ASC"
	}
	jql += fmt.Sprintf(" ORDER BY %s %s", orderField, orderDir)

	cloud, err := clients.IsJiraCloud(jiraClient)
	if err != nil {
		return nil, fmt.Errorf("failed to detect Jira deployment: %w", err)
	}

	var (
		issues  []jira.Issue
		total   int
		hasMore bool
	)
	if cloud {
		var seen int
		issues, hasMore, seen, err = searchJiraCloudPage(listContext(ctx), jiraClient, config.ID, jql, params.Offset, params.Limit)
		if err != nil {
			return nil, fmt.Errorf("failed to search Jira issues: %w", err)
		}
		if !hasMore {
			total = seen
		} else {
			// The cursor API reports no total; use Jira's approximate count while
			// more pages remain, never below what has already been served.
			if n := jiraApproximateCount(listContext(ctx), jiraClient, config.ID, countJQL); n > 0 {
				total = max(n, params.Offset+len(issues)+1)
			}
		}
	} else {
		var resp *jira.Response
		issues, resp, err = jiraClient.Issue.Search(jql, &jira.SearchOptions{StartAt: params.Offset, MaxResults: params.Limit})
		if err != nil {
			return nil, fmt.Errorf("failed to search Jira issues: %w", err)
		}
		if resp != nil {
			total = resp.Total
		}
		hasMore = total > params.Offset+len(issues)
	}

	tickets := make([]models.Ticket, 0, len(issues))
	for _, issue := range issues {
		var assignee string
		if issue.Fields.Assignee != nil {
			assignee = issue.Fields.Assignee.DisplayName
		}

		var priority string
		if issue.Fields.Priority != nil {
			priority = issue.Fields.Priority.Name
		}

		var createdAt *time.Time
		if !time.Time(issue.Fields.Created).IsZero() {
			t := time.Time(issue.Fields.Created)
			createdAt = &t
		}

		var status string
		if issue.Fields.Status != nil {
			status = issue.Fields.Status.Name
		}

		tickets = append(tickets, models.Ticket{
			TicketID:  issue.Key,
			Title:     issue.Fields.Summary,
			Status:    status,
			Severity:  priority,
			Assignee:  assignee,
			Platform:  "jira",
			URL:       clients.JiraBrowseURL(jiraClient, issue.Key),
			CreatedAt: createdAt,
		})
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
		HasMore: hasMore,
	}, nil
}

// listContext returns the request context, or a background one when the
// handler runs without a request (unit tests).
func listContext(ctx *gin.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// jiraSearchPage is the cursor-paged envelope Jira Cloud's search/jql returns.
type jiraSearchPage struct {
	Issues        []jira.Issue `json:"issues"`
	NextPageToken string       `json:"nextPageToken"`
}

// searchJiraCloudPage serves an offset/limit window from Jira Cloud's
// cursor-paged search (rest/api/2/search/jql), which replaced the offset-paged
// rest/api/2/search. The cursor for each position it reaches is cached, so the
// next page resumes there instead of re-walking from the first page; a cold or
// expired cursor falls back to walking from the start. hasMore reports whether
// anything follows the window and seen is how many rows precede the cursor,
// which is the exact total once the cursor ends. Cloud may return fewer rows per
// page than requested, so the page size is only a hint.
func searchJiraCloudPage(ctx context.Context, jiraClient *jira.Client, integrationID, jql string, offset, limit int) ([]jira.Issue, bool, int, error) {
	if token, ok := cachedJiraCursor(ctx, integrationID, jql, offset); ok {
		issues, hasMore, seen, err := walkJiraCloud(ctx, jiraClient, integrationID, jql, offset, limit, offset, token)
		if err == nil {
			return issues, hasMore, seen, nil
		}
		slog.Warn("Jira cursor resume failed; re-walking from the first page", "offset", offset, "error", err)
	}
	return walkJiraCloud(ctx, jiraClient, integrationID, jql, offset, limit, 0, "")
}

// walkJiraCloud pages from position base (whose cursor is token; empty for the
// first page) until the offset/limit window is filled or the cursor ends.
func walkJiraCloud(ctx context.Context, jiraClient *jira.Client, integrationID, jql string, offset, limit, base int, token string) ([]jira.Issue, bool, int, error) {
	const maxPages = 20
	want := offset + limit
	var collected []jira.Issue
	pos := base
	for page := 0; page < maxPages; page++ {
		body := map[string]interface{}{
			"jql":        jql,
			"fields":     []string{"summary", "status", "priority", "assignee", "created"},
			"maxResults": want - pos,
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		req, err := jiraClient.NewRequestWithContext(ctx, "POST", "rest/api/2/search/jql", body)
		if err != nil {
			return nil, false, 0, err
		}
		var result jiraSearchPage
		if _, err := jiraClient.Do(req, &result); err != nil {
			return nil, false, 0, err
		}
		collected = append(collected, result.Issues...)
		pos = base + len(collected)
		token = result.NextPageToken
		if token != "" {
			storeJiraCursor(ctx, integrationID, jql, pos, token)
		}
		if pos >= want || token == "" || len(result.Issues) == 0 {
			break
		}
	}
	hasMore := pos > want || token != ""
	rel := offset - base
	if rel >= len(collected) {
		if token != "" {
			return nil, false, 0, fmt.Errorf("offset %d is beyond the %d issues reachable through cursor paging", offset, pos)
		}
		return nil, hasMore, pos, nil
	}
	return collected[rel:min(rel+limit, len(collected))], hasMore, pos, nil
}

// jiraApproximateCount returns Jira Cloud's approximate number of issues matching
// jql (which must not carry an ORDER BY), or 0 when it cannot be determined. The
// count is cached briefly per (integration, jql) so paging does not repeat the call.
func jiraApproximateCount(ctx context.Context, jiraClient *jira.Client, integrationID, jql string) int {
	key := jiraCursorKey(integrationID, jql, -1)
	if integrationID != "" {
		if raw, ok := cache.Get(ctx, jiraCursorNamespace, key); ok {
			if n, err := strconv.Atoi(string(raw)); err == nil {
				return n
			}
		}
	}
	req, err := jiraClient.NewRequestWithContext(ctx, "POST", "rest/api/2/search/approximate-count", map[string]interface{}{"jql": jql})
	if err != nil {
		return 0
	}
	var result struct {
		Count int `json:"count"`
	}
	if _, err := jiraClient.Do(req, &result); err != nil {
		slog.Warn("Jira approximate count unavailable; reporting total as unknown", "error", err)
		return 0
	}
	if integrationID != "" {
		if err := cache.Set(ctx, jiraCursorNamespace, key, []byte(strconv.Itoa(result.Count)), jiraCursorTTL, integrationID); err != nil {
			slog.Debug("Jira count cache write failed", "error", err)
		}
	}
	return result.Count
}

// jiraCursorNamespace caches Jira Cloud nextPageTokens by (integration, jql,
// position) so deep pages do not re-walk the result set.
const (
	jiraCursorNamespace = "jira_search_cursor"
	jiraCursorTTL       = 10 * time.Minute
)

func init() {
	cache.CreateNamespace(jiraCursorNamespace)
}

func jiraCursorKey(integrationID, jql string, pos int) string {
	sum := sha256.Sum256([]byte(jql))
	return fmt.Sprintf("%s:%x:%d", integrationID, sum[:8], pos)
}

func cachedJiraCursor(ctx context.Context, integrationID, jql string, pos int) (string, bool) {
	if integrationID == "" || pos <= 0 {
		return "", false
	}
	token, ok := cache.Get(ctx, jiraCursorNamespace, jiraCursorKey(integrationID, jql, pos))
	return string(token), ok && len(token) > 0
}

func storeJiraCursor(ctx context.Context, integrationID, jql string, pos int, token string) {
	if integrationID == "" {
		return
	}
	if err := cache.Set(ctx, jiraCursorNamespace, jiraCursorKey(integrationID, jql, pos), []byte(token), jiraCursorTTL, integrationID); err != nil {
		slog.Debug("Jira cursor cache write failed", "error", err)
	}
}

// Update updates fields on a Jira issue. Status changes go through the
// transitions endpoint; all other fields go through PUT /rest/api/2/issue/{id},
// which Cloud and Data Center both serve and which takes the description as
// plain text, the same as create. When both are set, fields are applied first
// so transition validators that inspect field values see the new values.
func (s *JiraService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateJiraTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	jiraClient, err := clients.CreateJiraClient(config.AuthType, config.Username, config.Password, config.URL)
	if err != nil {
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	update := make(map[string]interface{})

	if updateFields.Severity != "" {
		update["priority"] = resolveJiraPriority(jiraClient, jiraPriority(updateFields.Severity))
	}

	if assignees := updateFields.AssigneeList(); len(assignees) > 0 {
		if len(assignees) > 1 {
			return fmt.Errorf("only a single assignee is supported for Jira; %d were provided", len(assignees))
		}
		update["assignee"] = jiraUserRef(resolveJiraAssignee(jiraClient, assignees[0]))
	}

	if updateFields.Description != "" {
		update["description"] = updateFields.Description
	}

	if len(updateFields.Labels) > 0 {
		update["labels"] = updateFields.Labels
	}

	if len(update) > 0 {
		issueUpdate := map[string]interface{}{"fields": update}

		req, err := jiraClient.NewRequest("PUT", "/rest/api/2/issue/"+ticketID, issueUpdate)
		if err != nil {
			return fmt.Errorf("failed to create update request: %w", err)
		}

		if _, err := jiraClient.Do(req, nil); err != nil {
			return fmt.Errorf("failed to update Jira issue: %w", err)
		}

		slog.Info("Jira issue fields updated", "ticketID", ticketID)
	}

	if updateFields.Status != "" {
		if err := s.Transition(ctx, config, ticketID, updateFields.Status); err != nil {
			return err
		}
	}

	return nil
}

// Transition changes the status of a Jira issue
func (s *JiraService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	if err := utils.ValidateJiraTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	jiraClient, err := clients.CreateJiraClient(config.AuthType, config.Username, config.Password, config.URL)
	if err != nil {
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	return transitionJiraIssue(jiraClient, ticketID, status)
}

// transitionJiraIssue moves the issue to status. Jira never offers a transition to
// the status an issue is already in, so that case is a successful no-op and a
// re-run of a workflow does not fail on an already-settled ticket.
func transitionJiraIssue(jiraClient *jira.Client, ticketID string, status string) error {
	// Get available transitions
	transitions, _, err := jiraClient.Issue.GetTransitions(ticketID)
	if err != nil {
		return fmt.Errorf("failed to get transitions: %w", err)
	}

	// Find matching transition
	var transitionID string
	statusLower := strings.ToLower(strings.TrimSpace(status))
	for _, t := range transitions {
		if strings.ToLower(t.Name) == statusLower || strings.ToLower(t.To.Name) == statusLower {
			transitionID = t.ID
			break
		}
	}

	if transitionID == "" {
		if issue, _, getErr := jiraClient.Issue.Get(ticketID, &jira.GetQueryOptions{Fields: "status"}); getErr == nil &&
			issue.Fields != nil && issue.Fields.Status != nil &&
			strings.ToLower(strings.TrimSpace(issue.Fields.Status.Name)) == statusLower {
			slog.Info("Jira issue already in target status, nothing to do", "ticketID", ticketID, "status", status)
			return nil
		}
		availableStatuses := make([]string, len(transitions))
		for i, t := range transitions {
			availableStatuses[i] = t.To.Name
		}
		return fmt.Errorf("transition to '%s' not available. Available: %v", status, availableStatuses)
	}

	// Perform transition
	_, err = jiraClient.Issue.DoTransition(ticketID, transitionID)
	if err != nil {
		return fmt.Errorf("failed to transition issue: %w", err)
	}

	slog.Info("Jira issue transitioned", "ticketID", ticketID, "status", status)
	return nil
}
