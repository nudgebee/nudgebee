package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Creates tickets, lists tickets, adds comments, gets comments, and fetches ticket details. Platform and integration are auto-selected — just describe the ticket operation directly (e.g., "create ticket titled X", "list open tickets", "add comment to PROJ-123").`
	toolInput := "A question or request about creating, listing, commenting on, or searching tickets."
	toolOutput := "Result of the ticket operation including ticket details, ticket lists, comments, or search results."

	core.RegisterNBAgentFactoryAndTool(TicketsV2AgentName, func(accountId string) (core.NBAgent, error) {
		return TicketMasterV2Agent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const TicketsV2AgentName = "tickets_v2"

type TicketMasterV2Agent struct {
	accountId string
}

func (a TicketMasterV2Agent) GetName() string {
	return TicketsV2AgentName
}

func (a TicketMasterV2Agent) GetNameAliases() []string {
	return []string{"TicketsV2"}
}

func (a TicketMasterV2Agent) GetDescription() string {
	return `Creates and manages tickets across multiple platforms (Jira, GitHub, GitLab, ServiceNow, PagerDuty, ZenDuty).

	Capabilities:
	* Creates tickets on any configured platform.
	* Lists tickets with filtering, sorting, and pagination.
	* Adds comments to existing tickets.
	* Retrieves comments from existing tickets.
	* Fetches full ticket details.

	Usage:
	* Input: Provide a request about ticket operations (e.g., "Create a Jira ticket for the OOM issue", "List open tickets in PROJ", "Add a comment to ticket PROJ-123").
	* Output: Returns ticket details, ticket lists, comments, or operation results.
	`
}

func (a TicketMasterV2Agent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	followupEnabled := core.IsAgentsFollowupEnabled()

	// Review Required Fields instruction varies based on followup support
	reviewFieldsInstruction := "**Review Required Fields**: After get_create_meta, carefully review ALL required fields. For each required field, check if the user has provided a value. If any required fields are missing values that you cannot reasonably infer, ask the user to provide them BEFORE calling create_ticket. Present the missing fields with their allowed values so the user can choose. For datetime/datepicker fields, ask the user for the date. For select fields, show the allowed values."
	if followupEnabled {
		reviewFieldsInstruction = `**Review Required Fields**: After get_create_meta, carefully review ALL required fields. For each required field:
1. If the user already provided a value, use it.
2. If you can infer a sensible default from context (e.g., priority=Medium, today's date for date fields, the user's request as description), set it automatically — do NOT ask.
3. If the field has a fixed set of allowed values and you cannot infer which one, use ask_clarification with 'single_select' or 'multi_select'.
4. If the field is free-form (date, text) and you cannot infer a value, use ask_clarification with 'text'.
5. Ask only ONE field per ask_clarification call so each field gets the correct UI (select vs text). Do NOT bundle multiple fields into one call.
6. Do NOT call create_ticket until all required fields are filled.`
	}

	instructions := []string{
		"**Analyze User Request**: Determine if the user wants to create a ticket, list tickets, add a comment, get comments, or get ticket details.",
		"**Before Creating Tickets**: ALWAYS call ticket_master_v2 with operation_type='get_create_meta' and include project_key if known. This returns required/optional field keys, types, and allowed values. Read the response carefully — it includes a field mapping guide that tells you which fields map to standard parameters (title, description, ticket_type, severity, assignee) and which go in additional_fields. For GitHub and GitLab integrations, if only one project/repository is configured it is auto-selected. If multiple are configured and no project_key is provided, the tool returns an error listing available projects — use ask_clarification with 'single_select' to ask the user to choose one, then retry with the selected project_key. When the response includes a 'Selected project' line, use that exact value as project_key in create_ticket.",
		reviewFieldsInstruction,
		"**Create Tickets**: Use ticket_master_v2 with operation_type='create_ticket'. The integration is auto-selected from config — do NOT pass platform or integration_id. Map fields from get_create_meta as follows: summary/title → 'title' param, description/body → 'description' param, issuetype → 'ticket_type' param, priority → 'severity' param, assignee → 'assignee' param. All other fields (custom fields, labels, etc.) go in 'additional_fields' using the exact field key from get_create_meta. For standard select fields use {\"name\": \"Value\"}, for custom select fields use {\"value\": \"Value\"}.",
		"**Add Comments**: Use ticket_master_v2 with operation_type='add_comment'. Provide ticket_id and comment_text. Integration is auto-selected from config.",
		"**Get Comments**: Use ticket_master_v2 with operation_type='get_comments'. Provide ticket_id. Integration is auto-selected from config.",
		"**Get Ticket**: Use ticket_master_v2 with operation_type='get_ticket'. Provide ticket_id to fetch full details from the external platform. Integration is auto-selected from config.",
		"**List Tickets**: Use ticket_master_v2 with operation_type='list_tickets'. project_key and integration are auto-selected from config — do NOT pass integration_id. You can optionally filter by status, priority, assignee, date range, and control pagination with limit/offset. Results include ticket_id, title, status, severity, assignee, and URL.",
		"**Format Output**: Render ticket details as Markdown with ticket_id, platform, status, severity, and URL when available.",
	}

	if followupEnabled {
		instructions = append(instructions,
			`**Error Recovery with Followup**: When ticket_master_v2 returns an error, analyze the error message:
1. If the error mentions missing or invalid fields (e.g., project_key, ticket_type, required fields), extract the missing field name and any available options from the error message.
2. Use ask_clarification to ask the user for the missing value — use 'single_select' with options if the error lists available values, or 'text' for free-form fields.
3. After the user responds, retry the failed operation with the provided value.
4. Do NOT just report the error as text — ALWAYS attempt recovery via ask_clarification when the error indicates missing user input.`)
	}

	constraints := []string{
		"Use ticket_master_v2 tool for all ticket operations.",
		"NEVER call create_ticket without first reviewing ALL required fields from get_create_meta. If the user has not provided values for required fields, ask them — do not guess or skip required fields.",
		"Do not make up integration IDs or ticket IDs.",
		"The integration/platform is ALREADY auto-selected by the framework before your tools run. NEVER ask the user which integration or platform to use (jira, github, gitlab, etc.). Just call ticket_master_v2 directly — the correct integration is injected automatically.",
		"This agent does NOT support JQL queries or direct field updates.",
	}

	if followupEnabled {
		constraints[1] = "NEVER call create_ticket without first reviewing ALL required fields from get_create_meta. For fields you cannot infer, use ask_clarification to ask the user. For fields where a sensible default exists (e.g., priority=Medium, dates=today), set the default automatically without asking."
		constraints = append(constraints,
			"When you need any information from the user (missing fields, unclear request, or recovering from a tool error about missing input), ALWAYS use the ask_clarification tool instead of just generating text. This pauses execution and waits for the user to respond. NEVER use ask_clarification to ask about integrations or platforms — those are auto-selected.",
			"Ask only ONE field per ask_clarification call. Each field type needs its own UI element (single_select, multi_select, or text). Never combine multiple fields into one ask_clarification call.",
		)
	}

	toolUsage := map[string][]string{
		"ticket_master_v2": {
			"Use this tool for all ticket operations. Specify operation_type as one of: get_create_meta, create_ticket, add_comment, get_comments, get_ticket, list_tickets.",
			"For get_create_meta: Fetches required/optional fields for ticket creation. Include project_key when known (especially for Jira). For GitHub/GitLab, project_key must be in 'owner/repo' format — if not provided, the tool auto-selects when one project is configured or lists available projects. ALWAYS call this before create_ticket — the response includes a field mapping guide.",
			"For create_ticket: Required: title. Optional: description, severity, project_key, ticket_type, assignee, additional_fields. Integration is auto-selected from config. Follow the field mapping from get_create_meta: standard fields go as direct parameters, all other fields go in additional_fields with exact field keys.",
			"For add_comment: Required: ticket_id, comment_text. Integration is auto-selected from config.",
			"For get_comments: Required: ticket_id. Integration is auto-selected from config.",
			"For get_ticket: Required: ticket_id. Optional: source.",
			`For list_tickets: project_key is auto-selected from config when available. Optional: status, priority, assignee, limit (default 20, max 100), offset (default 0), created_after (ISO 8601), created_before (ISO 8601), sort_by ("created_at" or "updated_at"), sort_order ("asc" or "desc"). Integration is auto-selected from config.`,
		},
	}

	if followupEnabled {
		toolUsage["ask_clarification"] = []string{
			"Use this tool to ask the user a follow-up question when you need additional information to complete their request.",
			"Use when: (1) a required field is missing and you cannot infer a sensible default, (2) the request is ambiguous, or (3) ticket_master_v2 returned an error about missing/invalid input that the user can resolve. Do NOT use for integration/platform selection — that is handled automatically by the framework.",
			"IMPORTANT: Ask only ONE field per call. Choose the correct followup_type for that field: 'single_select' with options for fields with a fixed list of allowed values (e.g., ticket_type, priority, team, project_key), 'multi_select' with options for multi-checkbox/multi-value fields, 'text' for free-form fields (dates, descriptions).",
			"When recovering from a tool error: parse the error message for available options (e.g., 'Available types: Bug, Task' or 'Available projects: X, Y') and pass them as options in a 'single_select' followup.",
			"This tool pauses execution and waits for the user's response before continuing. Do NOT re-ask a question the user has already answered.",
		}
	}

	var createTicketExample core.NBAgentPromptExample
	if followupEnabled {
		createTicketExample = core.NBAgentPromptExample{
			Question: "Create a ticket for the OOM issue in production",
			Answer: `Step 1: Call get_create_meta to discover required and optional fields:
ticket_master_v2: {"operation_type": "get_create_meta"}
→ Observe: get_create_meta returns a list of required/optional fields with their types and allowed values. Each field has a key, display name, type, and (for select/multi-select) a list of allowed values.

Step 2: Review each required field from get_create_meta:
- For each field, decide: (a) user already provided it, (b) can infer a sensible default, or (c) must ask the user.
- Fields with type "string" that match title/summary → use from user's request.
- Fields with type "select" where context makes the answer obvious (e.g., priority=High for a production OOM) → set as default.
- Fields with type "datetime" or "datepicker" → default to current date/time.
- Fields with type "select" where you cannot infer → ask with followup_type="single_select" and pass the allowed values as options.
- Fields with type "multicheckboxes" or multi-value → ask with followup_type="multi_select" and pass the allowed values as options.
- Fields with type "string"/"text" where you cannot infer → ask with followup_type="text".

Step 3: For each field that must be asked, call ask_clarification ONE field at a time:
ask_clarification: {"command": "<clear question about this specific field>", "followup_type": "<single_select|multi_select|text>", "options": ["<allowed values if select type>"]}
→ Wait for user response, then continue to the next missing field.

Step 4: Once all required fields are filled (from user input + defaults + user selections), call create_ticket with everything.`,
			Explanation: "Always call get_create_meta first. Use the returned field types to decide defaults vs asking. Ask ONE field per ask_clarification call with the matching followup_type. Set sensible defaults for fields where the answer is obvious from context. Only call create_ticket once all required fields are filled.",
		}
	} else {
		createTicketExample = core.NBAgentPromptExample{
			Question: "Create a ticket for the OOM issue in production",
			Answer: `Step 1: Call get_create_meta to discover required and optional fields:
ticket_master_v2: {"operation_type": "get_create_meta"}
→ Observe: get_create_meta returns a list of required/optional fields with their types and allowed values.

Step 2: Review each required field. For fields the user provided or you can infer from context, set them directly. For fields you cannot infer, ask the user.

Step 3: Once all required fields are filled, call create_ticket with everything.`,
			Explanation: "Always call get_create_meta first. Review ALL required fields. Ask the user for any required field values you cannot infer. Only call create_ticket once you have values for every required field. Integration is auto-selected from config — do not pass platform or integration_id.",
		}
	}

	examples := []core.NBAgentPromptExample{
		createTicketExample,
		{
			Question:    "Add a comment to ticket PROJ-123: 'Fixed by scaling up memory limits'",
			Answer:      `ticket_master_v2: {"operation_type": "add_comment", "ticket_id": "PROJ-123", "comment_text": "Fixed by scaling up memory limits"}`,
			Explanation: "Using ticket_master_v2 to add a comment to an existing ticket.",
		},
		{
			Question:    "Get comments on ticket PROJ-456",
			Answer:      `ticket_master_v2: {"operation_type": "get_comments", "ticket_id": "PROJ-456"}`,
			Explanation: "Using ticket_master_v2 to retrieve comments from a ticket.",
		},
		{
			Question:    "Show me all open high-priority tickets",
			Answer:      `ticket_master_v2: {"operation_type": "list_tickets", "status": "open", "priority": "High"}`,
			Explanation: "Using ticket_master_v2 to list tickets with status and priority filters. project_key and integration are auto-selected from config.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "An intelligent multi-platform ticketing assistant",
		Instructions: instructions,
		Constraints:  constraints,
		OutputFormat: "Markdown",
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (a TicketMasterV2Agent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{
		tools.TicketMasterV2{},
	}

	if core.IsAgentsFollowupEnabled() {
		supportedTools = append(supportedTools, FollowupAgent{})
	}
	return supportedTools
}

func (a TicketMasterV2Agent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
