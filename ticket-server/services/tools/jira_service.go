package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
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
	jiraClient, err := clients.CreateJiraClient(config.Username, config.Password, config.URL)
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

	var createdAt *time.Time
	if !time.Time(issue.Fields.Created).IsZero() {
		t := time.Time(issue.Fields.Created)
		createdAt = &t
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
		Platform:    "jira",
		URL:         "https://" + jiraClient.GetBaseURL().Host + "/browse/" + issue.Key,
		CreatedAt:   createdAt,
		Raw:         marshalToMap(issue),
	}, nil
}

func CreateJiraIssue(configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get Jira client:", "error", slog.AnyValue(err))
		return ticket, err
	}

	additionalFields, ok := ticket.AdditionalFields.(map[string]interface{})
	if !ok {
		additionalFields = make(map[string]interface{})
	}

	labels := []string{"nudgebee", ticket.Source}
	if existingLabels, ok := additionalFields["labels"].([]interface{}); ok {
		for _, label := range existingLabels {
			if strLabel, ok := label.(string); ok {
				labels = append(labels, strLabel)
			}
		}
	}
	additionalFields["labels"] = labels

	var assignee *jira.User
	if len(strings.Split(ticket.Assignee, ":")) == 2 {
		assignee = &jira.User{
			AccountID: ticket.Assignee,
		}
	} else if ticket.Assignee != "" {
		accountID := lookupAccountIDByEmail(jiraClient, ticket.Assignee)
		if accountID != "" && len(strings.Split(accountID, ":")) == 2 {
			assignee = &jira.User{
				AccountID: accountID,
			}
		} else {
			assignee = &jira.User{
				EmailAddress: ticket.Assignee,
			}
		}
	}

	fields := &jira.IssueFields{
		Assignee:    assignee,
		Description: ticket.Description,
		Type: jira.IssueType{
			Name: ticket.TicketType,
		},
		Project: jira.Project{
			Key: ticket.ProjectKey,
		},
		Summary: ticket.Title,
	}

	if ticket.Severity != "" {
		// Severity flows in via the basic field as a Jira priority ID (the
		// create-meta option value). Priority is set in exactly one place — it is
		// never carried in additionalFields (it's tagged as a basic field, and the
		// runbook task migrates any legacy additional_fields.priority into severity).
		fields.Priority = &jira.Priority{
			ID: ticket.Severity,
		}
	}

	fields.Unknowns = make(tcontainer.MarshalMap)
	for key, value := range additionalFields {
		switch {
		case ticket.TicketType == "Subtask" && key == "parent":
			parentKey, ok := value.(string)
			if !ok {
				slog.Error("Invalid parent key value:", "value", slog.AnyValue(value))
				return ticket, fmt.Errorf("invalid parent key value: expected string, got %T", value)
			}
			if err := validateParentIssue(jiraClient, parentKey); err != nil {
				slog.Error("Error validating parent issue:", "error", slog.AnyValue(err))
				return ticket, err
			}
			fields.Parent = &jira.Parent{
				Key: parentKey,
			}
		default:
			fields.Unknowns[key] = value
		}
	}

	issue := jira.Issue{
		Fields: fields,
	}

	issueResp, _, err := jiraClient.Issue.Create(&issue)
	if err != nil {
		slog.Error("Error creating Jira issue:", "error", slog.AnyValue(err))
		return ticket, err
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
	ticket.URL = "https://" + jiraClient.GetBaseURL().Host + "/browse/" + detailedIssue.Key
	ticket.Platform = "jira"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

func lookupAccountIDByEmail(jiraClient *jira.Client, email string) string {
	apiEndpoint := fmt.Sprintf("/rest/api/3/user/search?query=%s", url.QueryEscape(email))

	req, err := jiraClient.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return ""
	}

	var users []jira.User
	_, err = jiraClient.Do(req, &users)
	if err != nil {
		return ""
	}

	if len(users) == 0 {
		return ""
	}

	return users[0].AccountID
}

func validateParentIssue(client *jira.Client, parentKey string) error {
	issue, _, err := client.Issue.Get(parentKey, nil)
	if err != nil {
		return fmt.Errorf("error fetching parent issue details: %v", err)
	}
	if issue == nil || issue.Fields == nil {
		return fmt.Errorf("parent issue %s returned nil or empty fields", parentKey)
	}
	if issue.Fields.Type.Name != "Task" {
		return fmt.Errorf("parent issue with key %s is not a Task", parentKey)
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

func AddTicketComment(configuration models.TicketConfigurations, ticketId, title, description string) error {
	jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
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
	jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get jira client for configuration: "+configuration.ID, "error", slog.AnyValue(err))
		return nil, err
	}

	createMetaInfo, _, err := jiraClient.Issue.GetCreateMeta(projectKey)
	if err != nil {
		slog.Error("Unable to get jira create meta for project key: "+projectKey, "error", slog.AnyValue(err))
		return nil, err
	}

	// Live reference data so Severity and Assignee don't depend on per-issue-type
	// createmeta quirks (Jira commonly omits priority allowedValues, and the
	// assignee autocomplete returns nothing under GDPR-strict / no-permission).
	// Both are best-effort: failures degrade to whatever createmeta provided.
	priorities := fetchJiraPriorities(jiraClient)
	users := fetchJiraAssignableUsers(jiraClient, projectKey)

	return sanitizeJiraMeta(createMetaInfo, priorities, users), nil
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
					type_ := getFieldType(fieldMap["schema"])
					if type_ == "" {
						continue
					}
					if fieldMap["key"] == "parent" {
						type_ = "string"
					}
					fi := FieldInfo{
						Name:     fmt.Sprintf("%v", fieldMap["name"]),
						Key:      fmt.Sprintf("%v", fieldMap["key"]),
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

func getFieldType(schema interface{}) string {
	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return ""
	}

	inputTypes := []string{"string", "array"}
	if customValue, ok := schemaMap["custom"].(string); ok {
		parts := strings.Split(customValue, ":")
		if len(parts) < 2 {
			return ""
		}
		return parts[1]
	}

	// Safe comma-ok assertion — bare assertion panics on absent/non-string keys.
	typeVal, ok := schemaMap["type"].(string)
	if !ok || typeVal == "" {
		return ""
	}
	if contains(inputTypes, typeVal) {
		return typeVal
	}
	return "select"
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
	jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to get Jira client for configuration: "+configuration.ID, "error", slog.AnyValue(err))
		return nil, err
	}

	// Validate that the auto-complete URL belongs to the configured Jira instance
	parsedInputURL, err := url.Parse(request.Input.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid auto-complete URL: %v", err)
	}
	parsedConfigURL, err := url.Parse(configuration.URL)
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
		fieldValues, err = usersToFieldValues(ctx, jiraClient, resp, fieldValues)
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

func usersToFieldValues(ctx *gin.Context, client *jira.Client, resp *jira.Response, fieldValues []models.FieldValue) ([]models.FieldValue, error) {
	var users []jira.User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, err
	}

	for _, user := range users {
		fieldValue := models.FieldValue{
			ID:   user.AccountID,
			Name: user.DisplayName,
			Value: func() string {
				if user.EmailAddress != "" {
					return user.EmailAddress
				}
				email, err := getEmailForUser(ctx, client, user.DisplayName)
				if err != nil {
					slog.Debug("Error fetching email address for user " + user.DisplayName + ": " + err.Error())
					return ""
				}
				return email
			}(),
		}

		fieldValues = append(fieldValues, fieldValue)
	}
	return fieldValues, nil
}

func getEmailForUser(ctx *gin.Context, client *jira.Client, displayName string) (string, error) {
	users, _, err := client.User.FindWithContext(ctx, displayName)
	if err != nil {
		return "", err
	}

	// Check if user is found
	if len(users) == 0 {
		return "", fmt.Errorf("user with display name %s not found", displayName)
	}

	// Return the email address of the first user found
	return users[0].EmailAddress, nil
}

func GetTicketComments(config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	jc, err := clients.CreateJiraClient(config.Username, config.Password, config.URL)
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
		startAt += pageSize
	}

	return all, nil
}

func AddCustomTicketComment(configuration models.TicketConfigurations, ticketId, comment string) ([]models.Comments, error) {
	jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
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

// List retrieves tickets from Jira using JQL search.
func (s *JiraService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	jiraClient, err := clients.CreateJiraClient(config.Username, config.Password, config.URL)
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
		jqlParts = append(jqlParts, fmt.Sprintf("created >= %q", params.CreatedAfter))
	}
	if params.CreatedBefore != "" {
		jqlParts = append(jqlParts, fmt.Sprintf("created <= %q", params.CreatedBefore))
	}

	jql := strings.Join(jqlParts, " AND ")

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

	searchOpts := &jira.SearchOptions{
		StartAt:    params.Offset,
		MaxResults: params.Limit,
	}

	issues, resp, err := jiraClient.Issue.Search(jql, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to search Jira issues: %w", err)
	}

	tickets := make([]models.Ticket, 0, len(issues))
	baseURL := "https://" + jiraClient.GetBaseURL().Host + "/browse/"
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
			URL:       baseURL + issue.Key,
			CreatedAt: createdAt,
		})
	}

	total := 0
	if resp != nil {
		total = resp.Total
	}
	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

// buildADFDocument converts plain text into an Atlassian Document Format
// document suitable for the Jira Cloud v3 description field. ADF text nodes
// cannot contain newline characters, so each line becomes its own paragraph
// node. CRLF and lone CR are normalized to LF; blank lines become empty
// paragraphs to preserve spacing.
func buildADFDocument(text string) map[string]any {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	content := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		paragraph := map[string]any{"type": "paragraph"}
		if line != "" {
			paragraph["content"] = []map[string]any{
				{"type": "text", "text": line},
			}
		} else {
			paragraph["content"] = []map[string]any{}
		}
		content = append(content, paragraph)
	}

	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}

// Update updates fields on a Jira issue. Status changes go through the
// transitions endpoint; all other fields go through PUT /rest/api/3/issue/{id}.
// When both are set, fields are applied first so transition validators that
// inspect field values see the new values.
func (s *JiraService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateJiraTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	jiraClient, err := clients.CreateJiraClient(config.Username, config.Password, config.URL)
	if err != nil {
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	update := make(map[string]interface{})

	if updateFields.Severity != "" {
		update["priority"] = map[string]string{"name": updateFields.Severity}
	}

	if updateFields.Assignee != "" {
		// Check if it's an account ID or email
		if len(strings.Split(updateFields.Assignee, ":")) == 2 {
			update["assignee"] = map[string]string{"accountId": updateFields.Assignee}
		} else {
			// Try to look up by email
			accountID := lookupAccountIDByEmail(jiraClient, updateFields.Assignee)
			if accountID != "" {
				update["assignee"] = map[string]string{"accountId": accountID}
			} else {
				update["assignee"] = map[string]string{"emailAddress": updateFields.Assignee}
			}
		}
	}

	if updateFields.Description != "" {
		update["description"] = buildADFDocument(updateFields.Description)
	}

	if len(updateFields.Labels) > 0 {
		update["labels"] = updateFields.Labels
	}

	if len(update) > 0 {
		issueUpdate := map[string]interface{}{"fields": update}

		req, err := jiraClient.NewRequest("PUT", "/rest/api/3/issue/"+ticketID, issueUpdate)
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

	jiraClient, err := clients.CreateJiraClient(config.Username, config.Password, config.URL)
	if err != nil {
		return fmt.Errorf("failed to create Jira client: %w", err)
	}

	// Get available transitions
	transitions, _, err := jiraClient.Issue.GetTransitions(ticketID)
	if err != nil {
		return fmt.Errorf("failed to get transitions: %w", err)
	}

	// Find matching transition
	var transitionID string
	statusLower := strings.ToLower(status)
	for _, t := range transitions {
		if strings.ToLower(t.Name) == statusLower || strings.ToLower(t.To.Name) == statusLower {
			transitionID = t.ID
			break
		}
	}

	if transitionID == "" {
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
