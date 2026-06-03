package tools

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strings"
)

/*
Tool Interface implementation
tool-handler functions
functions to connect with ticket server
types for tool input and output
*/

// ---- NBTool implementation ----
func init() {
	core.RegisterNBToolFactory(TicketMasterToolNameV2, func(accountId string) (core.NBTool, error) {
		return TicketMasterV2{}, nil
	})
}

const TicketMasterToolNameV2 = "ticket_master_v2"

// supportedTicketPlatforms is the single source of truth for platform types
// the ticket_master_v2 tool understands. Used by the SQL filter in
// listTicketIntegrations, the platform-inference helper, the human-readable
// Description, and tests. Adding a new platform integration requires updating
// only this list (and the corresponding integration handler).
var supportedTicketPlatforms = []string{"jira", "github", "gitlab", "servicenow", "pagerduty", "zenduty"}

// platformInferenceRegex matches any supported platform as a whole word
// (case-insensitive). Word boundaries prevent false positives from substrings
// like "github copilot" triggering "github" when the query is unrelated.
// Compiled once at package init since the platform list is immutable.
var platformInferenceRegex = func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(` + strings.Join(supportedTicketPlatforms, "|") + `)\b`)
}()

type TicketMasterV2 struct{}

func (m TicketMasterV2) Name() string {
	return TicketMasterToolNameV2
}

func (m TicketMasterV2) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TicketMasterV2) Description() string {
	return `Manages tickets across multiple platforms (Jira, GitHub, GitLab, ServiceNow, PagerDuty, ZenDuty). Supports listing integrations, creating tickets, adding/getting comments, and fetching full ticket details.`
}

func (m TicketMasterV2) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type: core.ToolSchemaTypeString,
				Description: `JSON object with operation_type and parameters. Operations:
				- get_create_meta: Fetches required/optional fields for ticket creation, including the list of valid assignees for the selected project. Optional: project_key. Call this BEFORE create_ticket to discover required fields and valid assignees. Integration is auto-selected.
				- create_ticket: Creates a ticket. Required: title. Optional: description, severity, project_key, ticket_type, assignee, additional_fields (object with custom field keys from get_create_meta, e.g. {"priority": {"name": "High"}, "customfield_10034": {"value": "Backend"}}). Integration is auto-selected from config. The assignee, if provided, MUST be one of the allowed values returned by get_create_meta — never guess a username. If the user requests an assignee that is not in the allowed list, ask them via ask_clarification before calling create_ticket; show the allowed values so they can choose. If no assignee is requested, proceed with creating the ticket unassigned — do NOT prompt for one. The ticket-server validates assignees and will fail the call rather than silently dropping an invalid one.
				- add_comment: Adds comment to ticket. Required: ticket_id, comment_text. Optional: source, project_key.
				- get_comments: Gets comments on ticket. Required: ticket_id. Optional: source.
				- get_ticket: Gets full details of a specific ticket from the external platform. Required: ticket_id. Optional: source.
				- list_tickets: Lists tickets from the external platform with filtering and pagination. project_key is auto-selected from config when available, otherwise provide it. Optional: status, priority, assignee, limit (default 20, max 100), offset (default 0), created_after (ISO 8601), created_before (ISO 8601), sort_by ("created_at" or "updated_at"), sort_order ("asc" or "desc"). Integration is auto-selected from config.`,
			},
		},
		Required: []string{"command"},
	}
}

func (m TicketMasterV2) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		ConfigSource: core.ToolConfigSourceTicketAll,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

// FilterConfigs narrows the full ticket-integration list (ToolConfigSourceTicketAll
// returns every jira/github/gitlab/servicenow/pagerduty/zenduty integration on the
// tenant) down to those matching a platform the user mentioned in their query.
// Runs before any resolution strategy so the narrowed list also drives the
// follow-up prompt when user disambiguation is still needed. Returns the original
// list when no single platform is inferable or when no configs match the platform.
func (m TicketMasterV2) FilterConfigs(ctx core.NbToolContext, configs []core.ToolConfig) []core.ToolConfig {
	platform := inferTicketPlatformFromQuery(ctx.Query)
	if platform == "" {
		return configs
	}
	var matches []core.ToolConfig
	for _, cfg := range configs {
		for _, v := range cfg.Values {
			if v.Name == "type" && strings.EqualFold(v.Value, platform) {
				matches = append(matches, cfg)
				break
			}
		}
	}
	if len(matches) == 0 {
		return configs
	}
	return matches
}

func (m TicketMasterV2) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	var request TicketV2OperationRequest
	if err := common.UnmarshalJson([]byte(input.Command), &request); err != nil {
		slog.Warn("ticket_master_v2: failed to parse command in IdentifyConfig", "error", err)
		return core.ToolConfig{}, nil
	}

	if len(availableConfigs) == 1 {
		return availableConfigs[0], nil
	}

	// get_create_meta needs an integration but the specific one matters when there are multiple.
	// Auto-select only when there's exactly one config; otherwise fall through to
	// normal resolution so the framework can ask the user to choose.
	if request.OperationType == v2OpGetCreateMeta {
		if len(availableConfigs) == 1 {
			return availableConfigs[0], nil
		}
		// fall through to integration_id / platform / query matching below
	}

	// If integration_id is specified, find matching config
	if request.IntegrationID != "" {
		for _, cfg := range availableConfigs {
			for _, v := range cfg.Values {
				if v.Name == "id" && v.Value == request.IntegrationID {
					return cfg, nil
				}
			}
		}
	}

	// If platform is specified, filter configs by platform type.
	// Fall back to inferring the platform from the user's query (e.g.
	// "list jira tickets") when the LLM didn't populate it explicitly —
	// the list_tickets input schema does not expose a platform field, so
	// this is the only signal we have for single-platform routing.
	platform := request.Platform
	if platform == "" {
		platform = request.Source
	}
	if platform == "" {
		platform = inferTicketPlatformFromQuery(ctx.Query)
	}
	if platform != "" {
		var matches []core.ToolConfig
		for _, cfg := range availableConfigs {
			for _, v := range cfg.Values {
				if v.Name == "type" && strings.EqualFold(v.Value, platform) {
					matches = append(matches, cfg)
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
	}

	// If only one integration total, auto-select
	if len(availableConfigs) == 1 {
		return availableConfigs[0], nil
	}

	// Try matching config name from user query using word boundaries
	// to avoid false positives (e.g., config "Dev" matching "development")
	userQuery := strings.ToLower(ctx.Query)
	if userQuery != "" {
		var matched []core.ToolConfig
		for _, cfg := range availableConfigs {
			pattern := `(?i)\b` + regexp.QuoteMeta(cfg.Name) + `\b`
			if re, err := regexp.Compile(pattern); err == nil && re.MatchString(ctx.Query) {
				matched = append(matched, cfg)
			}
		}
		if len(matched) == 1 {
			return matched[0], nil
		}
	}

	return core.ToolConfig{}, nil
}

func (m TicketMasterV2) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	var request TicketV2OperationRequest
	if err := common.UnmarshalJson([]byte(input.Command), &request); err != nil {
		errMsg := fmt.Sprintf("ticket_master_v2: invalid request format: %v", err)
		nbRequestContext.Ctx.GetLogger().Error("ticket_master_v2: invalid request format", slog.String("error", errMsg))
		return core.NBToolResponse{Data: errMsg, Status: core.NBToolResponseStatusError}, fmt.Errorf("%s", errMsg)
	}

	if err := request.Validate(); err != nil {
		errMsg := fmt.Sprintf("ticket_master_v2: %v", err)
		nbRequestContext.Ctx.GetLogger().Error("ticket_master_v2: validation failed", slog.String("error", errMsg))
		return core.NBToolResponse{Data: errMsg, Status: core.NBToolResponseStatusError}, fmt.Errorf("%s", errMsg)
	}

	var response string
	var errResp error

	switch request.OperationType {

	case v2OpGetCreateMeta:
		response, errResp = handleV2GetCreateMeta(nbRequestContext, request)
	case v2OpCreateTicket:
		response, errResp = handleV2CreateTicket(nbRequestContext, request)
	case v2OpAddComment:
		response, errResp = handleV2AddComment(nbRequestContext, request)
	case v2OpGetComments:
		response, errResp = handleV2GetComments(nbRequestContext, request)
	case v2OpGetTicket:
		response, errResp = handleV2GetTicket(nbRequestContext, request)
	case v2OpListTickets:
		response, errResp = handleV2ListTickets(nbRequestContext, request)
	default:
		errResp = fmt.Errorf("unsupported operation_type: %q", request.OperationType)
	}

	if errResp != nil {
		nbRequestContext.Ctx.GetLogger().Error("ticket_master_v2: operation failed",
			"error", errResp, "operation_type", request.OperationType)
		return core.NBToolResponse{
			Data:   errResp.Error(),
			Status: core.NBToolResponseStatusError,
		}, errResp
	}

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// --- Operation handlers ---

func handleV2GetCreateMeta(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID string

	if configID, _ := getIntegrationFromToolConfig(ctx.ToolConfig); configID != "" {
		integrationID = configID
		// platform := configType
	} else if req.IntegrationID != "" {
		integrationID = req.IntegrationID
		// platform = req.Platform
	} else {
		return "", fmt.Errorf("integration_id is required for '%s'", v2OpGetCreateMeta)
	}

	projectKey := req.ProjectKey
	if projectKey == "" {
		resolved, resolveErr := resolveProjectFromConfig(ctx.ToolConfig)
		if resolveErr != nil {
			return "", resolveErr
		}
		projectKey = resolved
	}

	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	result, err := ticketServerGetCreateMeta(ctx.Ctx, tenantID, integrationID, projectKey)
	if err != nil {
		return "", fmt.Errorf("failed to get create meta: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", result.Error)
	}

	issueTypes := result.Data.TicketsGetCreateMeta
	if len(issueTypes) == 0 {
		return "No issue types found for this integration/project.", nil
	}

	var sb strings.Builder
	sb.WriteString("Ticket creation requirements:\n\n")

	// Include the resolved project_key so the agent can use it in create_ticket
	if projectKey != "" {
		fmt.Fprintf(&sb, "**Selected project:** `%s`\n", projectKey)
		sb.WriteString("Use this exact value as `project_key` when calling create_ticket.\n\n")
	}

	for _, issueType := range issueTypes {
		fmt.Fprintf(&sb, "### %s\n", issueType.Name)

		// Sort field keys for deterministic output order
		fieldKeys := make([]string, 0, len(issueType.Fields))
		for k := range issueType.Fields {
			fieldKeys = append(fieldKeys, k)
		}
		sort.Strings(fieldKeys)

		var requiredFields, optionalFields []ticketServerFieldInfo
		for _, k := range fieldKeys {
			field := issueType.Fields[k]
			if field.Required {
				requiredFields = append(requiredFields, field)
			} else {
				optionalFields = append(optionalFields, field)
			}
		}

		if len(requiredFields) > 0 {
			sb.WriteString("**Required fields:**\n")
			for _, f := range requiredFields {
				writeFieldInfo(&sb, f)
			}
		}

		if len(optionalFields) > 0 {
			sb.WriteString("**Optional fields:**\n")
			for _, f := range optionalFields {
				writeFieldInfo(&sb, f)
			}
		}
		sb.WriteString("\n")
	}

	// ticket_type is always required for ticket creation — inform the agent explicitly
	if len(issueTypes) == 1 {
		fmt.Fprintf(&sb, "**ticket_type** (required): Only one issue type available — use `%s`.\n\n", issueTypes[0].Name)
	} else {
		seen := make(map[string]bool)
		var names []string
		for _, it := range issueTypes {
			if !seen[it.Name] {
				seen[it.Name] = true
				names = append(names, it.Name)
			}
		}
		fmt.Fprintf(&sb, "**ticket_type** (required): You MUST specify one of: %s\n\n", strings.Join(names, ", "))
	}

	sb.WriteString("---\n")
	sb.WriteString("**Field mapping for create_ticket:**\n")
	sb.WriteString("- `summary`/`title` → use the `title` parameter\n")
	sb.WriteString("- `description`/`body` → use the `description` parameter\n")
	sb.WriteString("- `issuetype`/`ticket_type` → use the `ticket_type` parameter (use the issue type Name, e.g. \"Task\")\n")
	sb.WriteString("- `priority` → use the `severity` parameter with the priority Name (e.g. \"High\", NOT the ID)\n")
	sb.WriteString("- `assignee` → use the `assignee` parameter\n")
	sb.WriteString("- All other fields (custom fields, labels, etc.) → pass in `additional_fields` using the exact field key from above\n")
	sb.WriteString("- **Format for additional_fields values:**\n")
	sb.WriteString("  - `select` fields: use `{\"id\": \"<id>\"}` with the id from the allowed values list\n")
	sb.WriteString("  - `multicheckboxes`/`multiselect` fields: use `[{\"id\": \"<id>\"}, ...]` array of id objects\n")
	sb.WriteString("  - `datetime` fields: use ISO 8601 format, e.g. `\"2026-03-12T06:52:43.565Z\"`\n")
	sb.WriteString("  - `datepicker` fields: use ISO date format, e.g. `\"2026-03-12\"`\n")
	sb.WriteString("  - `string`/`text` fields: use plain string value\n")
	sb.WriteString("  - `array` fields (e.g. labels): use `[\"value1\", \"value2\"]`\n")

	return sb.String(), nil
}

func writeFieldInfo(sb *strings.Builder, f ticketServerFieldInfo) {
	fmt.Fprintf(sb, "- `%s` (%s, type: %s)", f.Key, f.Name, f.Type)
	if len(f.AllowedValues) > 0 {
		var vals []string
		for _, v := range f.AllowedValues {
			if m, ok := v.(map[string]any); ok {
				if name, exists := m["name"]; exists {
					vals = append(vals, fmt.Sprintf("%v", name))
				} else if value, exists := m["value"]; exists {
					vals = append(vals, fmt.Sprintf("%v", value))
				}
			} else {
				vals = append(vals, fmt.Sprintf("%v", v))
			}
		}
		if len(vals) > 10 {
			fmt.Fprintf(sb, " — allowed: [%s, ... +%d more]", strings.Join(vals[:10], ", "), len(vals)-10)
		} else {
			fmt.Fprintf(sb, " — allowed: [%s]", strings.Join(vals, ", "))
		}
	}
	sb.WriteString("\n")
}

func handleV2CreateTicket(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID, platform string

	integrationID, platform = getIntegrationFromToolConfig(ctx.ToolConfig)
	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	// Resolve project_key from config when not provided
	projectKey := req.ProjectKey
	if projectKey == "" {
		resolved, resolveErr := resolveProjectFromConfig(ctx.ToolConfig)
		if resolveErr != nil {
			return "", resolveErr
		}
		projectKey = resolved
	}
	// Fetch create-meta to validate required fields before creating
	meta, metaErr := ticketServerGetCreateMeta(ctx.Ctx, tenantID, integrationID, projectKey)
	if metaErr != nil {
		ctx.Ctx.GetLogger().Warn(
			"ticket_master_v2: failed to fetch create meta, proceeding without validation",
			slog.String("error", metaErr.Error()),
		)
	} else if meta.Error == "" && len(meta.Data.TicketsGetCreateMeta) > 0 {
		// Auto-select ticket_type when only one issue type is available
		if req.TicketType == "" && len(meta.Data.TicketsGetCreateMeta) == 1 {
			req.TicketType = meta.Data.TicketsGetCreateMeta[0].Name
		}
		// If still no ticket_type and multiple types exist, report as missing
		if req.TicketType == "" && len(meta.Data.TicketsGetCreateMeta) > 1 {
			seen := make(map[string]bool)
			var names []string
			for _, it := range meta.Data.TicketsGetCreateMeta {
				if !seen[it.Name] {
					seen[it.Name] = true
					names = append(names, it.Name)
				}
			}
			return "", fmt.Errorf("ticket_type is required. Available types: %s", strings.Join(names, ", "))
		}
		missingMsg := checkRequiredFields(meta.Data.TicketsGetCreateMeta, req)
		if missingMsg != "" {
			return "", fmt.Errorf("%s", missingMsg)
		}
	}

	// Pass additional_fields through as-is to the ticket-server.
	// The LLM is instructed via get_create_meta to use the exact format
	// the platform expects (e.g., {"id":"10020"} for Jira select fields,
	// plain string for priority ID, ISO string for dates).
	// The ticket-server's platform-specific handler performs any remaining
	// normalization (e.g., Jira service maps priority string to Priority.ID).
	createReq := ticketServerCreateRequest{
		AccountID:        ctx.AccountId,
		Title:            req.Title,
		Description:      req.Description,
		Severity:         req.Severity,
		ProjectKey:       projectKey,
		Platform:         platform,
		IntegrationID:    integrationID,
		Assignee:         req.Assignee,
		TicketType:       req.TicketType,
		Source:           "ui",
		Tenant:           tenantID,
		IsNew:            true,
		ReferenceID:      common.GenerateUUID(),
		AdditionalFields: req.AdditionalFields,
	}

	result, err := ticketServerCreateTicket(ctx.Ctx, tenantID, createReq)
	if err != nil {
		return "", fmt.Errorf("failed to create ticket: %w", err)
	}

	ticket := result.Data.InsertTicketsOne
	if ticket.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", ticket.Error)
	}

	return fmt.Sprintf("Ticket created successfully:\n- **ID**: %s\n- **Platform**: %s\n- **Status**: %s\n- **URL**: %s",
		ticket.TicketID, ticket.Platform, ticket.Status, ticket.URL), nil
}

// checkRequiredFields validates that all required fields from create-meta are present in the request.
// Returns an empty string if all required fields are provided, or a descriptive message listing missing fields.
func checkRequiredFields(issueTypes []ticketServerIssueType, req TicketV2OperationRequest) string {
	// Map of known field keys to the values provided in the request
	providedValues := map[string]string{
		"summary":     req.Title,
		"title":       req.Title,
		"description": req.Description,
		"body":        req.Description,
		"issuetype":   req.TicketType,
		"issue_type":  req.TicketType,
		"ticket_type": req.TicketType,
		"priority":    req.Severity,
		"severity":    req.Severity,
		"project":     req.ProjectKey,
		"project_key": req.ProjectKey,
		"assignee":    req.Assignee,
	}

	// Find the matching issue type, or use the first one
	var targetIssueType *ticketServerIssueType
	if req.TicketType != "" {
		for i, it := range issueTypes {
			if strings.EqualFold(it.Name, req.TicketType) {
				targetIssueType = &issueTypes[i]
				break
			}
		}
	}
	if targetIssueType == nil {
		targetIssueType = &issueTypes[0]
	}

	// Sort field keys for deterministic error messages
	fieldKeys := make([]string, 0, len(targetIssueType.Fields))
	for k := range targetIssueType.Fields {
		fieldKeys = append(fieldKeys, k)
	}
	sort.Strings(fieldKeys)

	// Collect missing required fields
	var missingFields []ticketServerFieldInfo
	for _, k := range fieldKeys {
		field := targetIssueType.Fields[k]
		if !field.Required {
			continue
		}
		val, known := providedValues[strings.ToLower(field.Key)]
		if known && val != "" {
			continue
		}
		// Also check by lowercase name
		val, known = providedValues[strings.ToLower(field.Name)]
		if known && val != "" {
			continue
		}
		// Check additional_fields by key
		if req.AdditionalFields != nil {
			if v, ok := req.AdditionalFields[field.Key]; ok && v != nil && fmt.Sprintf("%v", v) != "" {
				continue
			}
		}
		missingFields = append(missingFields, field)
	}

	if len(missingFields) == 0 {
		return ""
	}

	// Build a message telling the agent what's missing and to ask the user
	var sb strings.Builder
	fmt.Fprintf(&sb, "Cannot create ticket: the following required fields are missing for issue type '%s'.\n", targetIssueType.Name)
	sb.WriteString("Ask the user to provide values for these fields before retrying. Show them the allowed values where available:\n\n")
	for _, f := range missingFields {
		writeFieldInfo(&sb, f)
	}

	// Also list available issue types if there are multiple unique ones
	if len(issueTypes) > 1 {
		seen := make(map[string]bool)
		var names []string
		for _, it := range issueTypes {
			if !seen[it.Name] {
				seen[it.Name] = true
				names = append(names, it.Name)
			}
		}
		sb.WriteString("\nAvailable issue types: ")
		sb.WriteString(strings.Join(names, ", "))
		sb.WriteString("\n")
	}

	return sb.String()
}

func handleV2AddComment(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID, source string

	configID, configType := getIntegrationFromToolConfig(ctx.ToolConfig)

	// If the LLM explicitly specified a source that differs from the cached ToolConfig,
	// resolve via source so cross-platform operations work.
	if req.Source != "" && !strings.EqualFold(req.Source, configType) {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			var multiErr *multipleIntegrationsError
			if errors.As(err, &multiErr) {
				return multiErr.Error(), nil
			}
			return "", err
		}
	} else if configID != "" {
		integrationID = configID
		source = configType
	} else {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			var multiErr *multipleIntegrationsError
			if errors.As(err, &multiErr) {
				return multiErr.Error(), nil
			}
			return "", err
		}
	}

	projectKey := req.ProjectKey
	if projectKey == "" {
		resolved, resolveErr := resolveProjectFromConfig(ctx.ToolConfig)
		if resolveErr != nil {
			return "", resolveErr
		}
		projectKey = resolved
	}

	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	result, err := ticketServerAddComment(ctx.Ctx, tenantID, ctx.AccountId, integrationID, source, req.TicketID, req.CommentText, projectKey)
	if err != nil {
		return "", fmt.Errorf("failed to add comment: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", result.Error)
	}

	return fmt.Sprintf("Comment added successfully to ticket %s.", req.TicketID), nil
}

func handleV2GetComments(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID, source string

	configID, configType := getIntegrationFromToolConfig(ctx.ToolConfig)

	// If the LLM explicitly specified a source that differs from the cached ToolConfig,
	// resolve via source so cross-platform lookups work.
	if req.Source != "" && !strings.EqualFold(req.Source, configType) {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			return "", err
		}
	} else if configID != "" {
		integrationID = configID
		source = configType
	} else {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			return "", err
		}
	}

	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	result, err := ticketServerGetComments(ctx.Ctx, tenantID, ctx.AccountId, integrationID, source, req.TicketID)
	if err != nil {
		return "", fmt.Errorf("failed to get comments: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", result.Error)
	}

	if len(result.Comments) == 0 {
		return fmt.Sprintf("No comments found on ticket %s.", req.TicketID), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Comments on ticket %s:\n\n", req.TicketID)
	for _, c := range result.Comments {
		fmt.Fprintf(&sb, "**%s** (%s):\n%s\n\n", c.Author, c.Created, c.Comment)
	}
	return sb.String(), nil
}

func handleV2GetTicket(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID, source string

	configID, configType := getIntegrationFromToolConfig(ctx.ToolConfig)

	// If the LLM explicitly specified a source that differs from the cached ToolConfig,
	// resolve via source so cross-platform lookups work (e.g. GitHub ticket when Jira is cached).
	if req.Source != "" && !strings.EqualFold(req.Source, configType) {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			var multiErr *multipleIntegrationsError
			if errors.As(err, &multiErr) {
				return multiErr.Error(), nil
			}
			return "", err
		}
	} else if configID != "" {
		integrationID = configID
		source = configType
	} else {
		var err error
		integrationID, source, err = resolveIntegrationForComment(ctx.AccountId, req.IntegrationID, req.Source)
		if err != nil {
			var multiErr *multipleIntegrationsError
			if errors.As(err, &multiErr) {
				return multiErr.Error(), nil
			}
			return "", err
		}
	}

	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	result, err := ticketServerGetTicket(ctx.Ctx, tenantID, ctx.AccountId, integrationID, source, req.TicketID)
	if err != nil {
		return "", fmt.Errorf("failed to get ticket: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", result.Error)
	}

	t := result.Data
	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** — %s\n", t.TicketID, t.Title)
	if t.Status != "" {
		fmt.Fprintf(&sb, "- **Status**: %s\n", t.Status)
	}
	if t.Severity != "" {
		fmt.Fprintf(&sb, "- **Severity**: %s\n", t.Severity)
	}
	if t.Assignee != "" {
		fmt.Fprintf(&sb, "- **Assignee**: %s\n", t.Assignee)
	}
	if t.Reporter != "" {
		fmt.Fprintf(&sb, "- **Reporter**: %s\n", t.Reporter)
	}
	if t.Platform != "" {
		fmt.Fprintf(&sb, "- **Platform**: %s\n", t.Platform)
	}
	if t.TicketType != "" {
		fmt.Fprintf(&sb, "- **Type**: %s\n", t.TicketType)
	}
	if t.ProjectKey != "" {
		fmt.Fprintf(&sb, "- **Project**: %s\n", t.ProjectKey)
	}
	if t.URL != "" {
		fmt.Fprintf(&sb, "- **URL**: %s\n", t.URL)
	}
	if t.CreatedAt != "" {
		fmt.Fprintf(&sb, "- **Created**: %s\n", t.CreatedAt)
	}
	if t.Description != "" {
		fmt.Fprintf(&sb, "\n**Description:**\n%s\n", t.Description)
	}

	return sb.String(), nil
}

func handleV2ListTickets(ctx core.NbToolContext, req TicketV2OperationRequest) (string, error) {
	var integrationID string

	if configID, _ := getIntegrationFromToolConfig(ctx.ToolConfig); configID != "" {
		integrationID = configID
	} else if req.IntegrationID != "" {
		integrationID = req.IntegrationID
	} else {
		return "", fmt.Errorf("integration_id is required for '%s'", v2OpListTickets)
	}

	projectKey := req.ProjectKey
	if projectKey == "" {
		resolved, resolveErr := resolveProjectFromConfig(ctx.ToolConfig)
		if resolveErr != nil {
			return "", resolveErr
		}
		projectKey = resolved
	}
	if projectKey == "" {
		return "", fmt.Errorf("project_key is required for '%s' — provide it in the request or select a project from config", v2OpListTickets)
	}

	tenantID, err := resolveTenantID(ctx)
	if err != nil {
		return "", err
	}

	listReq := ticketServerListRequest{
		IntegrationID: integrationID,
		AccountID:     ctx.AccountId,
		Params: ticketServerListParams{
			ProjectKey:    projectKey,
			Status:        req.Status,
			Priority:      req.Priority,
			Assignee:      req.Assignee,
			Limit:         req.Limit,
			Offset:        req.Offset,
			CreatedAfter:  req.CreatedAfter,
			CreatedBefore: req.CreatedBefore,
			SortBy:        req.SortBy,
			SortOrder:     req.SortOrder,
		},
	}

	result, err := ticketServerListTickets(ctx.Ctx, tenantID, listReq)
	if err != nil {
		return "", fmt.Errorf("failed to list tickets: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("ticket-server error: %s", result.Error)
	}

	if len(result.Tickets) == 0 {
		return "No tickets found matching the specified criteria.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d ticket(s) (showing %d–%d):\n\n", result.Total, result.Offset+1, result.Offset+len(result.Tickets))

	for _, t := range result.Tickets {
		fmt.Fprintf(&sb, "- **%s** — %s", t.TicketID, t.Title)
		if t.Status != "" {
			fmt.Fprintf(&sb, " [%s]", t.Status)
		}
		if t.Severity != "" {
			fmt.Fprintf(&sb, " (%s)", t.Severity)
		}
		if t.Assignee != "" {
			fmt.Fprintf(&sb, " → %s", t.Assignee)
		}
		if t.URL != "" {
			fmt.Fprintf(&sb, " | %s", t.URL)
		}
		sb.WriteString("\n")
	}

	if result.Total > result.Offset+len(result.Tickets) {
		fmt.Fprintf(&sb, "\n_Use offset=%d to see the next page._", result.Offset+len(result.Tickets))
	}

	return sb.String(), nil
}

// --- Ticket-server HTTP calls ---

const ticketServerContentType = "application/json"

func ticketServerListTickets(ctx *security.RequestContext, tenantID string, req ticketServerListRequest) (ticketServerListResponse, error) {
	var result ticketServerListResponse

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/list", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
			"x-user-id":    ctx.GetSecurityContext().GetUserId(),
			"x-tenant-id":  tenantID,
		}),
		common.HttpWithJsonBody(req),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: list-tickets request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: list-tickets returned %d: %s", resp.StatusCode, string(body))
	}

	if err := common.UnmarshalJson(body, &result); err != nil {
		return result, fmt.Errorf("ticket-server: unmarshal list-tickets response: %w", err)
	}

	return result, nil
}

func ticketServerCreateTicket(ctx *security.RequestContext, tenantID string, req ticketServerCreateRequest) (ticketServerCreateResponse, error) {
	var result ticketServerCreateResponse

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/create-ticket", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
			"x-user-id":    ctx.GetSecurityContext().GetUserId(),
			"x-tenant-id":  tenantID,
		}),
		common.HttpWithJsonBody(req),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: create-ticket request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: create-ticket returned %d: %s", resp.StatusCode, string(body))
	}

	if err := common.UnmarshalJson(body, &result); err != nil {
		return result, fmt.Errorf("ticket-server: unmarshal create-ticket response: %w", err)
	}

	return result, nil
}

func ticketServerGetTicket(ctx *security.RequestContext, tenantID, accountID, integrationID, source, ticketID string) (ticketServerGetTicketResponse, error) {
	var result ticketServerGetTicketResponse

	reqBody := actionTicketRequest{
		Action: action{Name: "get_ticket"},
		Input: actionTicketInput{
			Object: actionTicketObject{
				TicketID:      ticketID,
				AccountID:     accountID,
				IntegrationID: integrationID,
				Source:        source,
				Tenant:        tenantID,
			},
		},
		SessionVariables: actionSessionVariables{
			Role:         getRole(ctx),
			UserID:       ctx.GetSecurityContext().GetUserId(),
			UserTenantID: tenantID,
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/rpc/get-ticket", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
		}),
		common.HttpWithJsonBody(reqBody),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: get-ticket request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: get-ticket returned %d: %s", resp.StatusCode, string(body))
	}

	if err := common.UnmarshalJson(body, &result); err != nil {
		return result, fmt.Errorf("ticket-server: unmarshal get-ticket response: %w", err)
	}

	return result, nil
}

func ticketServerAddComment(ctx *security.RequestContext, tenantID, accountID, integrationID, source, ticketID, comment, projectKey string) (ticketServerCommentsResponse, error) {
	var result ticketServerCommentsResponse

	reqBody := actionTicketRequest{
		Action: action{Name: "add_comment"},
		Input: actionTicketInput{
			Object: actionTicketObject{
				TicketID:      ticketID,
				AccountID:     accountID,
				IntegrationID: integrationID,
				Source:        source,
				Comment:       comment,
				Tenant:        tenantID,
				ProjectKey:    projectKey,
			},
		},
		SessionVariables: actionSessionVariables{
			Role:         getRole(ctx),
			UserID:       ctx.GetSecurityContext().GetUserId(),
			UserTenantID: tenantID,
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/rpc/add-comment", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
		}),
		common.HttpWithJsonBody(reqBody),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: add-comment request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: add-comment returned %d: %s", resp.StatusCode, string(body))
	}

	if err := common.UnmarshalJson(body, &result); err != nil {
		return result, fmt.Errorf("ticket-server: unmarshal add-comment response: %w", err)
	}

	return result, nil
}

func ticketServerGetComments(ctx *security.RequestContext, tenantID, accountID, integrationID, source, ticketID string) (ticketServerCommentsResponse, error) {
	var result ticketServerCommentsResponse

	reqBody := actionTicketRequest{
		Action: action{Name: "get_comments"},
		Input: actionTicketInput{
			Object: actionTicketObject{
				TicketID:      ticketID,
				AccountID:     accountID,
				IntegrationID: integrationID,
				Source:        source,
			},
		},
		SessionVariables: actionSessionVariables{
			Role:         getRole(ctx),
			UserID:       ctx.GetSecurityContext().GetUserId(),
			UserTenantID: tenantID,
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/rpc/get-comments", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
		}),
		common.HttpWithJsonBody(reqBody),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: get-comments request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: get-comments returned %d: %s", resp.StatusCode, string(body))
	}

	if err := common.UnmarshalJson(body, &result); err != nil {
		return result, fmt.Errorf("ticket-server: unmarshal get-comments response: %w", err)
	}

	return result, nil
}

func ticketServerGetCreateMeta(ctx *security.RequestContext, tenantID, integrationID, projectKey string) (ticketServerCreateMetaResponse, error) {
	var result ticketServerCreateMetaResponse

	reqBody := ticketServerCreateMetaRequest{
		Action: action{Name: "create_meta"},
		Input: ticketServerCreateMetaInput{
			IntegrationID: integrationID,
			ProjectKey:    projectKey,
		},
		SessionVariables: actionSessionVariables{
			Role:         getRole(ctx),
			UserID:       ctx.GetSecurityContext().GetUserId(),
			UserTenantID: tenantID,
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/tickets/create-meta", config.Config.TicketServerUrl),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": ticketServerContentType,
			"Accept":       ticketServerContentType,
		}),
		common.HttpWithJsonBody(reqBody),
	)
	if err != nil {
		return result, fmt.Errorf("ticket-server: create-meta request failed: %w", err)
	}
	defer closeRespBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("ticket-server: read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return result, fmt.Errorf("ticket-server: create-meta returned %d: %s", resp.StatusCode, string(body))
	}

	// Try the standard format first: {"data": {"tickets_get_create_meta": [...]}}  (GitHub/GitLab)
	// Jira returns a different shape: {"data": [...]}  (direct array under "data")
	// which causes an unmarshal error on the standard struct, so we fall back.
	stdErr := common.UnmarshalJson(body, &result)
	if stdErr == nil && len(result.Data.TicketsGetCreateMeta) > 0 {
		return result, nil
	}

	// Fall back to Jira format
	var jiraResult ticketServerCreateMetaResponseJira
	if err := common.UnmarshalJson(body, &jiraResult); err == nil && len(jiraResult.Data) > 0 {
		result.Data.TicketsGetCreateMeta = jiraResult.Data
		result.Error = jiraResult.Error
		return result, nil
	}

	// If standard parse had an error and Jira also didn't work, check for error-only response
	if stdErr != nil {
		return result, fmt.Errorf("ticket-server: unmarshal create-meta response: %w", stdErr)
	}

	return result, nil
}

// --- DB query for integrations ---

func listTicketIntegrations(accountId string) ([]ticketIntegration, error) {
	// Build the IN-list from supportedTicketPlatforms so adding a new platform
	// doesn't require touching this query. Values are package-controlled
	// (no user input), so direct interpolation is safe and avoids the
	// awkwardness of dynamic positional placeholders.
	quoted := make([]string, len(supportedTicketPlatforms))
	for i, p := range supportedTicketPlatforms {
		quoted[i] = "'" + p + "'"
	}
	query := fmt.Sprintf(`
		SELECT i.id, i.type, i.name
		FROM integrations i
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.status = 'enabled'
		  AND i.type IN (%s)
		ORDER BY i.name
	`, strings.Join(quoted, ", "))

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("listTicketIntegrations: db manager error: %w", err)
	}

	rows, err := dbManager.Db.Query(query, accountId)
	if err != nil {
		return nil, fmt.Errorf("listTicketIntegrations: query error: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("listTicketIntegrations: failed to close rows", "error", err)
		}
	}()

	var integrations []ticketIntegration
	for rows.Next() {
		var intg ticketIntegration
		if err := rows.Scan(&intg.ID, &intg.Type, &intg.Name); err != nil {
			return nil, fmt.Errorf("listTicketIntegrations: scan error: %w", err)
		}
		integrations = append(integrations, intg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listTicketIntegrations: rows iteration error: %w", err)
	}

	return integrations, nil
}

// --- Integration resolution ---

// inferTicketPlatformFromQuery scans the user's query (case-insensitive, whole
// word) for a single supported ticket-platform keyword. Returns the matched
// platform type (lowercase) or "" when none — or when multiple distinct
// platforms are mentioned — so the caller can fall through to other strategies.
// Word boundaries prevent unrelated tokens like "github copilot" or "JiraProd"
// from triggering a match when the query is not actually about that platform.
func inferTicketPlatformFromQuery(query string) string {
	if query == "" {
		return ""
	}
	matches := platformInferenceRegex.FindAllString(query, -1)
	if len(matches) == 0 {
		return ""
	}
	first := strings.ToLower(matches[0])
	for _, m := range matches[1:] {
		if !strings.EqualFold(m, first) {
			return ""
		}
	}
	return first
}

func resolveIntegration(accountId, integrationID, platform string) (string, string, error) {
	if integrationID != "" {
		if platform == "" {
			// Look up platform from integration
			integrations, err := listTicketIntegrations(accountId)
			if err != nil {
				return integrationID, "", nil // proceed without platform
			}
			for _, intg := range integrations {
				if intg.ID == integrationID {
					return integrationID, intg.Type, nil
				}
			}
		}
		return integrationID, platform, nil
	}

	// Resolve by platform
	integrations, err := listTicketIntegrations(accountId)
	if err != nil {
		return "", "", fmt.Errorf("failed to list integrations: %w", err)
	}

	var matches []ticketIntegration
	for _, intg := range integrations {
		if strings.EqualFold(intg.Type, platform) {
			matches = append(matches, intg)
		}
	}

	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("no %s integration found for this account", platform)
	case 1:
		return matches[0].ID, matches[0].Type, nil
	default:
		return "", "", &multipleIntegrationsError{platform: platform, matches: matches}
	}
}

func resolveIntegrationForComment(accountId, integrationID, source string) (string, string, error) {
	if integrationID != "" {
		return integrationID, source, nil
	}
	if source != "" {
		// Resolve integration_id from source/platform
		resolvedID, resolvedPlatform, err := resolveIntegration(accountId, "", source)
		if err != nil {
			var multiErr *multipleIntegrationsError
			if errors.As(err, &multiErr) {
				return "", "", multiErr
			}
			return "", source, nil // proceed with just source, ticket-server can resolve by tenant+source
		}
		return resolvedID, resolvedPlatform, nil
	}
	return "", "", fmt.Errorf("either integration_id or source is required to identify the ticket platform")
}

func resolveTenantID(ctx core.NbToolContext) (string, error) {
	tenantID := ctx.Ctx.GetSecurityContext().GetTenantId()
	if tenantID != "" {
		return tenantID, nil
	}
	return security.GetTenantIdFromAccountId(ctx.AccountId)
}

// --- ToolConfig helpers ---

// resolveProjectFromConfig resolves the project_key from the tool's selected integration config.
// If only one project is configured, it auto-selects it. If multiple projects exist,
// it returns an error listing available projects so the agent can ask the user via ask_clarification.
// Returns empty string with nil error if no projects are configured (e.g., Jira where project_key is passed directly).
func resolveProjectFromConfig(toolConfig core.ToolConfig) (string, error) {
	var projectsJSON string
	for _, v := range toolConfig.Values {
		if v.Name == "projects" && v.Value != "" {
			projectsJSON = v.Value
			break
		}
	}
	if projectsJSON == "" {
		return "", nil
	}

	var projects []map[string]any
	if err := common.UnmarshalJson([]byte(projectsJSON), &projects); err != nil {
		return "", nil
	}

	if len(projects) == 0 {
		return "", nil
	}

	// Single project — auto-select
	if len(projects) == 1 {
		if key, ok := projects[0]["key"].(string); ok && key != "" {
			return key, nil
		}
		if name, ok := projects[0]["name"].(string); ok && name != "" {
			return name, nil
		}
		return "", nil
	}

	// Multiple projects — return error listing available options
	options := make([]string, 0, len(projects))
	for _, p := range projects {
		if key, ok := p["key"].(string); ok && key != "" {
			options = append(options, key)
		} else if name, ok := p["name"].(string); ok && name != "" {
			options = append(options, name)
		}
	}
	if len(options) == 0 {
		return "", nil
	}

	return "", fmt.Errorf("multiple projects/repositories are configured. Please specify project_key. Available projects: %s", strings.Join(options, ", "))
}

func getIntegrationFromToolConfig(toolConfig core.ToolConfig) (integrationID, integrationType string) {
	for _, v := range toolConfig.Values {
		switch v.Name {
		case "id":
			integrationID = v.Value
		case "type":
			integrationType = v.Value
		}
	}
	return
}

// --- Operation constants ---

const (
	v2OpGetCreateMeta = "get_create_meta"
	v2OpCreateTicket  = "create_ticket"
	v2OpAddComment    = "add_comment"
	v2OpGetComments   = "get_comments"
	v2OpGetTicket     = "get_ticket"
	v2OpListTickets   = "list_tickets"
)

// --- Tool input request ---

type TicketV2OperationRequest struct {
	OperationType string `json:"operation_type"`

	// create_ticket fields
	Platform         string         `json:"platform,omitempty"`
	IntegrationID    string         `json:"integration_id,omitempty"`
	Title            string         `json:"title,omitempty"`
	Description      string         `json:"description,omitempty"`
	Severity         string         `json:"severity,omitempty"`
	ProjectKey       string         `json:"project_key,omitempty"`
	TicketType       string         `json:"ticket_type,omitempty"`
	Assignee         string         `json:"assignee,omitempty"`
	AdditionalFields map[string]any `json:"additional_fields,omitempty"`

	// comment / get_ticket fields
	TicketID    string `json:"ticket_id,omitempty"`
	Source      string `json:"source,omitempty"`
	CommentText string `json:"comment_text,omitempty"`

	// list_tickets fields
	Status        string `json:"status,omitempty"`
	Priority      string `json:"priority,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	SortBy        string `json:"sort_by,omitempty"`
	SortOrder     string `json:"sort_order,omitempty"`
}

func (r *TicketV2OperationRequest) Validate() error {
	switch r.OperationType {

	case v2OpGetCreateMeta:
		// integration_id can come from ToolConfig, so not strictly required here
		return nil
	case v2OpCreateTicket:
		if r.Title == "" {
			return fmt.Errorf("title is required for '%s'", v2OpCreateTicket)
		}
		// integration_id/platform can come from ToolConfig, so not strictly required here
	case v2OpAddComment:
		if r.TicketID == "" || r.CommentText == "" {
			return fmt.Errorf("ticket_id and comment_text are required for '%s'", v2OpAddComment)
		}
	case v2OpGetComments:
		if r.TicketID == "" {
			return fmt.Errorf("ticket_id is required for '%s'", v2OpGetComments)
		}
	case v2OpGetTicket:
		if r.TicketID == "" {
			return fmt.Errorf("ticket_id is required for '%s'", v2OpGetTicket)
		}
	case v2OpListTickets:
		// project_key can come from QueryConfig/ToolConfig, so not strictly required here
	default:
		return fmt.Errorf("invalid operation_type: %q. Valid types: %s",
			r.OperationType,
			strings.Join([]string{v2OpGetCreateMeta, v2OpCreateTicket, v2OpAddComment, v2OpGetComments, v2OpGetTicket, v2OpListTickets}, ", "))
	}
	return nil
}

// --- Ticket-server request/response types ---

// --- List tickets request/response types ---

type ticketServerListRequest struct {
	IntegrationID string                 `json:"integration_id"`
	AccountID     string                 `json:"account_id"`
	Params        ticketServerListParams `json:"params"`
}

type ticketServerListParams struct {
	ProjectKey    string `json:"project_key"`
	Status        string `json:"status,omitempty"`
	Priority      string `json:"priority,omitempty"`
	Assignee      string `json:"assignee,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	SortBy        string `json:"sort_by,omitempty"`
	SortOrder     string `json:"sort_order,omitempty"`
}

type ticketServerListResponse struct {
	Tickets []ticketServerListItem `json:"tickets"`
	Total   int                    `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset"`
	Error   string                 `json:"error,omitempty"`
}

type ticketServerListItem struct {
	TicketID  string `json:"ticket_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Severity  string `json:"severity"`
	Assignee  string `json:"assignee"`
	Platform  string `json:"platform"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

type ticketServerCreateRequest struct {
	AccountID        string `json:"account_id"`
	Title            string `json:"title"`
	Description      string `json:"description,omitempty"`
	Severity         string `json:"severity,omitempty"`
	ProjectKey       string `json:"project_key,omitempty"`
	Platform         string `json:"platform,omitempty"`
	IntegrationID    string `json:"integration_id"`
	Assignee         string `json:"assignee,omitempty"`
	TicketType       string `json:"ticket_type,omitempty"`
	Source           string `json:"source,omitempty"`
	ReferenceID      string `json:"reference_id,omitempty"`
	Tenant           string `json:"tenant,omitempty"`
	AdditionalFields any    `json:"additional_fields,omitempty"`
	IsNew            bool   `json:"is_new"`
}

type ticketServerCreateResponse struct {
	Data struct {
		InsertTicketsOne struct {
			ID          string `json:"id"`
			Severity    string `json:"severity"`
			Platform    string `json:"platform"`
			ReferenceID string `json:"reference_id"`
			TicketID    string `json:"ticket_id"`
			URL         string `json:"url"`
			Status      string `json:"status"`
			Error       string `json:"error"`
			Action      string `json:"action"`
			Message     string `json:"message"`
		} `json:"insert_tickets_one"`
	} `json:"data"`
}

type ticketServerGetTicketResponse struct {
	Data  ticketServerTicketDetail `json:"data"`
	Error string                   `json:"error,omitempty"`
}

type ticketServerTicketDetail struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	TicketID    string `json:"ticket_id"`
	TicketType  string `json:"ticket_type"`
	ProjectKey  string `json:"project_key"`
	Assignee    string `json:"assignee"`
	Reporter    string `json:"reporter"`
	Platform    string `json:"platform"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at"`
	Tags        string `json:"tags"`
}

// --- Create-meta request/response types ---

type ticketServerCreateMetaRequest struct {
	Action           action                      `json:"action"`
	Input            ticketServerCreateMetaInput `json:"input"`
	SessionVariables actionSessionVariables      `json:"session_variables"`
}

type ticketServerCreateMetaInput struct {
	IntegrationID string `json:"integration_id"`
	ProjectKey    string `json:"project_key"`
}

type ticketServerCreateMetaResponse struct {
	Data struct {
		TicketsGetCreateMeta []ticketServerIssueType `json:"tickets_get_create_meta"`
	} `json:"data"`
	Error string `json:"error,omitempty"`
}

type ticketServerCreateMetaResponseJira struct {
	Data  []ticketServerIssueType `json:"data"`
	Error string                  `json:"error,omitempty"`
}

type ticketServerIssueType struct {
	Name   string                           `json:"name"`
	Fields map[string]ticketServerFieldInfo `json:"fields"`
}

type ticketServerFieldInfo struct {
	AllowedValues []any  `json:"allowedValues,omitempty"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Required      bool   `json:"required"`
	Type          string `json:"type"`
}

type actionTicketRequest struct {
	Action           action                 `json:"action"`
	Input            actionTicketInput      `json:"input"`
	SessionVariables actionSessionVariables `json:"session_variables"`
}

type action struct {
	Name string `json:"name"`
}

type actionTicketInput struct {
	Object actionTicketObject `json:"object"`
}

type actionTicketObject struct {
	TicketID      string `json:"ticket_id"`
	AccountID     string `json:"account_id"`
	IntegrationID string `json:"integration_id,omitempty"`
	Source        string `json:"source,omitempty"`
	Comment       string `json:"comment,omitempty"`
	Tenant        string `json:"tenant,omitempty"`
	ProjectKey    string `json:"project_key,omitempty"`
}

type actionSessionVariables struct {
	Role         string `json:"role"`
	UserID       string `json:"user_id"`
	UserTenantID string `json:"tenant_id"`
}

type ticketServerCommentsResponse struct {
	TicketID string                    `json:"ticket_id"`
	Error    string                    `json:"error,omitempty"`
	Comments []ticketServerCommentItem `json:"comments"`
}

type ticketServerCommentItem struct {
	Author  string `json:"author"`
	Comment string `json:"comment"`
	Created string `json:"created_at"`
	Updated string `json:"updated_at"`
}

type ticketIntegration struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// --- Integration resolution types ---

type multipleIntegrationsError struct {
	platform string
	matches  []ticketIntegration
}

func (e *multipleIntegrationsError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Multiple %s integrations found. Please ask the user to specify which one to use:\n\n", e.platform)
	for _, m := range e.matches {
		fmt.Fprintf(&sb, "- **%s** (integration_id: `%s`)\n", m.Name, m.ID)
	}
	return sb.String()
}

// --- Helpers ---

// buildFieldTypeMap extracts a field key → type mapping from create-meta response.
func buildFieldTypeMap(meta ticketServerCreateMetaResponse) map[string]string {
	if meta.Error != "" || len(meta.Data.TicketsGetCreateMeta) == 0 {
		return nil
	}
	fieldTypes := make(map[string]string)
	for _, issueType := range meta.Data.TicketsGetCreateMeta {
		for _, field := range issueType.Fields {
			fieldTypes[field.Key] = field.Type
		}
	}
	return fieldTypes
}

// formatAdditionalFields normalizes additional_fields values based on field type metadata.
// Select/multiselect fields need object format for Jira ({"value":"X"} or {"id":"X"}),
// while scalar fields (priority, datetime, datepicker) need plain string values.
func formatAdditionalFields(fields map[string]any, fieldTypes map[string]string) map[string]any {
	if fields == nil {
		return nil
	}
	formatted := make(map[string]any, len(fields))
	for k, v := range fields {
		fieldType := fieldTypes[k]
		switch fieldType {
		case "select":
			formatted[k] = toSelectValue(v)
		case "multicheckboxes", "multiselect":
			formatted[k] = toMultiSelectValue(v)
		default:
			// priority, datetime, datepicker, string, number, etc. → flatten to plain value
			formatted[k] = toScalarValue(v)
		}
	}
	return formatted
}

// toSelectValue ensures the value is in a structured format for select fields.
// If already structured (has "id", "value", or "name" key), preserves as-is.
// Jira standard fields use {"name":"X"}, custom select fields use {"value":"X"}.
// Plain strings are wrapped as {"value":"X"}.
func toSelectValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		if _, ok := val["id"]; ok {
			return val
		}
		if _, ok := val["value"]; ok {
			return val
		}
		if _, ok := val["name"]; ok {
			return val
		}
		return v
	case string:
		return map[string]any{"value": val}
	default:
		return v
	}
}

// toMultiSelectValue ensures the value is in [{"value":"X"}] format for Jira multicheckbox/multiselect fields.
func toMultiSelectValue(v any) any {
	switch val := v.(type) {
	case []any:
		result := make([]any, 0, len(val))
		for _, item := range val {
			result = append(result, toSelectValue(item))
		}
		return result
	default:
		return []any{toSelectValue(v)}
	}
}

// toScalarValue flattens object values to plain strings for fields the ticket-server handles directly.
func toScalarValue(v any) any {
	if m, ok := v.(map[string]any); ok {
		if name, exists := m["name"]; exists {
			return name
		}
		if val, exists := m["value"]; exists {
			return val
		}
	}
	return v
}

// getRole returns the most appropriate RPC role from the user's security context
// instead of hardcoding "admin", so the ticket server can enforce its own authorization.
func getRole(ctx *security.RequestContext) string {
	sc := ctx.GetSecurityContext()
	if sc.IsSuperAdmin() {
		return "admin"
	}
	if sc.IsTenantAdmin() {
		return "tenant_admin"
	}
	roles := sc.GetRoles()
	if len(roles) > 0 {
		return roles[0]
	}
	return "user"
}

func closeRespBody(body io.ReadCloser) {
	if body != nil {
		if err := body.Close(); err != nil {
			slog.Info("ticket_master_v2: failed to close response body", "error", err)
		}
	}
}
