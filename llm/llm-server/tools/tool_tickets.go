package tools

import (
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/tmc/langchaingo/schema"
)

func init() {
	core.RegisterNBToolFactory(TicketMasterToolName, func(accountId string) (core.NBTool, error) {
		return TicketMaster{}, nil
	})
}

const TicketMasterToolName = "ticket_master"

type TicketMaster struct{}

func (m TicketMaster) Name() string {
	return TicketMasterToolName
}

func (m TicketMaster) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TicketMaster) Description() string {
	return `Executes a JQL query to search Jira issues.`
}

func (m TicketMaster) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Search string",
			},
		},
		Required: []string{"command"},
	}
}

type TicketOperationRequest struct {
	OperationType string `json:"operation_type"`
	Query         string `json:"query"`
	TicketID      string `json:"ticket_id"`
	FieldName     string `json:"field_name"`
	NewValue      string `json:"new_value"`
	CommentText   string `json:"comment_text"`
	Append        bool   `json:"append"`
}

func (r *TicketOperationRequest) Validate() error {
	switch r.OperationType {
	case OperationSearch:
		if r.Query == "" {
			return fmt.Errorf("query is required for operation '%s'", OperationSearch)
		}
	case OperationUpdate:
		if r.TicketID == "" || r.FieldName == "" || r.NewValue == "" {
			return fmt.Errorf("ticket_id, field_name, and new_value are required for '%s' operation", OperationUpdate)
		}
	case OperationComment:
		if r.TicketID == "" || r.CommentText == "" {
			return fmt.Errorf("ticket_id and comment_text are required for '%s' operation", OperationComment)
		}
	default:
		return fmt.Errorf("invalid operation type: %q", r.OperationType)
	}
	return nil
}

const (
	OperationSearch  = "search"
	OperationUpdate  = "update"
	OperationComment = "comment"
)

func (m TicketMaster) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	var request TicketOperationRequest
	if err := common.UnmarshalJson([]byte(input.Command), &request); err != nil {
		errMsg := fmt.Sprintf("ticketMaster: invalid request format: %v", err)
		nbRequestContext.Ctx.GetLogger().Error(
			"ticketMaster: invalid request format",
			slog.String("error", errMsg),
			slog.String("request", input.Command),
		)
		return core.NBToolResponse{
			Data:   errMsg,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("%s", errMsg)
	}

	if err := request.Validate(); err != nil {
		errMsg := fmt.Sprintf("ticketMaster: invalid request: %v", err)
		nbRequestContext.Ctx.GetLogger().Error("ticketMaster: invalid request", slog.String("error", errMsg))
		return core.NBToolResponse{
			Data:   errMsg,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("%s", errMsg)
	}

	config, err := getJiraIntegrationConfig(nbRequestContext.AccountId)
	if err != nil {
		errMsg := fmt.Sprintf("ticketMaster: failed to get Jira config: %v", err)
		nbRequestContext.Ctx.GetLogger().Error("ticketMaster: failed to get Jira config", "error", errMsg)
		return core.NBToolResponse{
			Data:   errMsg,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("%s", errMsg)
	}

	client, err := newJiraClient(config)
	if err != nil {
		errMsg := fmt.Sprintf("ticketMaster: failed to create Jira client: %v", err)
		nbRequestContext.Ctx.GetLogger().Error("ticketMaster: failed to create Jira client", "error", errMsg)
		return core.NBToolResponse{
			Data:   errMsg,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("%s", errMsg)
	}

	var (
		response string
		errResp  error
	)

	switch request.OperationType {

	case OperationSearch:
		if request.Query == "" {
			errResp = fmt.Errorf("query is required for 'search' operation")
			break
		}

		issues, err := searchIssuesV3(client, request.Query)
		if err != nil {
			errResp = fmt.Errorf("ticketMaster: search failed: %v", err)
			break
		}

		var matchingDocs []schema.Document
		for _, issue := range issues {
			pageContent := fmt.Sprintf(
				"[%s](%s/browse/%s)\nStatus: %s\nPriority: %s\nLabels: %s\nAssignee: %s",
				issue.Fields.Summary,
				sanitizeURL(config["url"]),
				issue.Key,
				issue.Fields.Status.Name,
				nullableField(issue.Fields.Priority),
				strings.Join(issue.Fields.Labels, ", "),
				nullableField(issue.Fields.Assignee),
			)
			matchingDocs = append(matchingDocs, schema.Document{
				PageContent: pageContent,
				Metadata:    map[string]any{},
				Score:       0.0,
			})
		}

		if len(matchingDocs) == 0 {
			response = "no search results found"
			break
		}

		result, err := common.MarshalJson(matchingDocs)
		if err != nil {
			errResp = fmt.Errorf("ticketMaster: failed to marshal results: %v", err)
			break
		}
		response = string(result)

	case OperationUpdate:
		response, errResp = handleUpdateOperation(client, config, request)

	case OperationComment:
		if request.TicketID == "" || request.CommentText == "" {
			errResp = fmt.Errorf("ticket_id and comment_text are required for 'comment' operation")
			break
		}

		if _, _, err := client.Issue.Get(request.TicketID, nil); err != nil {
			errResp = fmt.Errorf("ticketMaster: failed to fetch issue %q", request.TicketID)
			break
		}

		comment := jira.Comment{Body: request.CommentText}
		if _, _, err := client.Issue.AddComment(request.TicketID, &comment); err != nil {
			errResp = fmt.Errorf("ticketMaster: failed to add comment: %v", err)
			break
		}
		response = fmt.Sprintf(
			"Successfully added comment for ticket [%s](%s/browse/%s)",
			request.TicketID,
			sanitizeURL(config["url"]),
			request.TicketID,
		)

	default:
		errResp = fmt.Errorf("invalid operation_type: %q", request.OperationType)
	}

	if errResp != nil {
		nbRequestContext.Ctx.GetLogger().Error("ticketMaster: operation failed", "error", errResp, "operation_type", request.OperationType)
		return core.NBToolResponse{
			Data:   errResp.Error(),
			Status: core.NBToolResponseStatusError,
		}, errResp
	}

	resp := core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}

	if request.OperationType == OperationSearch {
		resp.References = []core.NBToolResponseReference{
			core.GetNudgebeeUIReference(nbRequestContext, "tickets", "Ticket Details", map[string]string{
				"accountId": nbRequestContext.AccountId,
			}, ""),
		}
	}

	return resp, nil
}

func handleUpdateOperation(client *jira.Client, config map[string]string, request TicketOperationRequest) (string, error) {
	if request.TicketID == "" || request.FieldName == "" || request.NewValue == "" {
		return "", fmt.Errorf("ticket_id, field_name, and new_value are required for 'update' operation")
	}

	issue, _, err := client.Issue.Get(request.TicketID, nil)
	if err != nil {
		return "", fmt.Errorf("ticketMaster: failed to fetch issue %q", request.TicketID)
	}

	editMeta, _, err := client.Issue.GetEditMeta(issue)
	if err != nil {
		return "", fmt.Errorf("ticketMaster: failed to fetch editable fields: %v", err)
	}

	rawField, ok := editMeta.Fields[request.FieldName]
	if !ok {
		var validFields []string
		for fieldID := range editMeta.Fields {
			validFields = append(validFields, fieldID)
		}
		return "", fmt.Errorf("'%s' is not a valid editable field.\nEditable fields are:\n- %s", request.FieldName, strings.Join(validFields, "\n- "))
	}

	fieldMap, ok := rawField.(map[string]any)
	if !ok {
		return "", fmt.Errorf("ticketMaster: unexpected field schema format for field '%s'", request.FieldName)
	}

	fieldSchema, ok := fieldMap["schema"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("ticketMaster: field '%s' schema metadata is not in the expected map format", request.FieldName)
	}

	schemaType, ok := fieldSchema["type"].(string)
	if !ok {
		return "", fmt.Errorf("ticketMaster: field '%s' schema 'type' is missing or not a string", request.FieldName)
	}

	var updateValue any

	switch schemaType {
	case "user":
		assignableUsers, err := GetAssignableUsers(client, request.TicketID)
		if err != nil {
			return "", fmt.Errorf("ticketMaster: failed to get assignable users: %v", err)
		}

		searchTerm := strings.ToLower(request.NewValue)
		var matches []jira.User
		for _, user := range assignableUsers {
			if strings.Contains(strings.ToLower(user.EmailAddress), searchTerm) ||
				strings.Contains(strings.ToLower(user.DisplayName), searchTerm) {
				matches = append(matches, user)
			}
		}

		switch len(matches) {
		case 0:
			return "", fmt.Errorf("ticketMaster: no users found matching '%s'", request.NewValue)
		case 1:
			updateValue = &matches[0]
		default:
			var clarification strings.Builder
			fmt.Fprintf(&clarification, "Multiple users found matching '%s'. Please specify which one you want to assign:", request.NewValue)
			for i, user := range matches {
				fmt.Fprintf(&clarification, "\n%d. %s (%s)", i+1, user.DisplayName, user.EmailAddress)
			}
			return "", fmt.Errorf("ticketMaster: %s", clarification.String())
		}

	case "array":
		newValues := strings.Split(request.NewValue, ",")
		if request.Append {
			if request.FieldName == "labels" {
				updateValue = append(issue.Fields.Labels, newValues...)
			} else {
				existing, ok := issue.Fields.Unknowns[request.FieldName].([]any)
				if !ok {
					existing = []any{}
				}
				for _, val := range newValues {
					existing = append(existing, val)
				}
				updateValue = existing
			}
		} else {
			updateValue = newValues
		}

	case "option", "priority":
		if request.FieldName == "priority" {
			priorities, _, err := client.Priority.GetList()
			if err != nil {
				return "", fmt.Errorf("ticketMaster: failed to fetch priorities: %v", err)
			}
			var found *jira.Priority
			for _, p := range priorities {
				if p.Name == request.NewValue {
					found = &p
					break
				}
			}
			if found == nil {
				return "", fmt.Errorf("ticketMaster: priority '%s' not found in available priorities", request.NewValue)
			}
			updateValue = found
		} else {
			updateValue = map[string]any{"name": request.NewValue}
		}

	case "string", "number", "date", "datetime":
		updateValue = request.NewValue

	default:
		updateValue = request.NewValue
	}

	updatePayload := map[string]any{
		request.FieldName: updateValue,
	}

	updatedIssue := &jira.Issue{
		Key:    request.TicketID,
		Fields: &jira.IssueFields{},
	}

	raw, err := common.MarshalJson(updatePayload)
	if err != nil {
		return "", fmt.Errorf("ticketMaster: failed to marshal update fields: %v", err)
	}

	if err := common.UnmarshalJson(raw, &updatedIssue.Fields.Unknowns); err != nil {
		return "", fmt.Errorf("ticketMaster: failed to unmarshal to unknown fields: %v", err)
	}

	if _, _, err := client.Issue.Update(updatedIssue); err != nil {
		return "", fmt.Errorf("ticketMaster: failed to update issue: %v", err)
	}

	return fmt.Sprintf(
		"Successfully updated field **%s** to **%s** for ticket [%s](%s/browse/%s)",
		request.FieldName,
		request.NewValue,
		request.TicketID,
		sanitizeURL(config["url"]),
		request.TicketID,
	), nil
}

func getJiraIntegrationConfig(accountId string) (map[string]string, error) {
	const (
		query = `SELECT
				MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
				MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
				MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
				MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			JOIN cloud_accounts ca ON i.tenant_id = ca.tenant
			WHERE i.status = 'enabled'
			  AND i.type = 'jira'
			  AND ca.id = $1
			GROUP BY i.id
			LIMIT 1`
	)

	if err := sqlValidateReadOnly(query, ""); err != nil {
		return nil, fmt.Errorf("jira config: read-only validation failed: %w", err)
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("jira config: db manager error: %w", err)
	}

	row := dbManager.Db.QueryRowx(query, accountId)

	var url, username, password *string
	var authType *string
	if err := row.Scan(&url, &username, &password, &authType); err != nil {
		return nil, fmt.Errorf("jira config: error scanning row: %w", err)
	}

	// Validate required fields
	if url == nil || username == nil || password == nil {
		return nil, fmt.Errorf("jira config: missing required configuration values")
	}

	decryptedPassword, err := common.Decrypt(*password)
	if err != nil {
		slog.Error("error decrypting password for jira configuration", "url", *url, "error", err)
		return nil, fmt.Errorf("jira config: error decrypting password: %w", err)
	}

	return map[string]string{
		"url":      sanitizeURL(*url),
		"username": *username,
		"token":    decryptedPassword,
	}, nil
}

func newJiraClient(config map[string]string) (*jira.Client, error) {
	tp := jira.BasicAuthTransport{
		Username: config["username"],
		Password: config["token"],
	}

	return jira.NewClient(tp.Client(), config["url"])
}

func sanitizeURL(rawURL string) string {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		slog.Error("failed to parse URL", "url", rawURL, "error", err)
		return rawURL
	}
	return u.String()
}

func nullableField(field any) string {
	if field == nil {
		return ""
	}
	switch v := field.(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
		return ""
	default:
		return fmt.Sprintf("%v", field)
	}
}

func searchIssuesV3(client *jira.Client, jql string) ([]jira.Issue, error) {
	apiEndpoint := "rest/api/3/search/jql"

	reqBody := map[string]any{
		"jql":    jql,
		"fields": []string{"*all", "-description", "-comment", "-worklog"},
	}

	req, err := client.NewRequest("POST", apiEndpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	var result struct {
		Issues []jira.Issue `json:"issues"`
	}
	if _, err := client.Do(req, &result); err != nil {
		return nil, fmt.Errorf("executing request and decoding response: %w", err)
	}

	return result.Issues, nil
}

func GetAssignableUsers(client *jira.Client, issueId string) ([]jira.User, error) {
	apiEndpoint := fmt.Sprintf("rest/api/2/user/assignable/search?issueKey=%s", url.QueryEscape(issueId))

	req, err := client.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return nil, err
	}

	var users []jira.User
	_, err = client.Do(req, &users)
	if err != nil {
		return nil, err
	}

	return users, nil
}
