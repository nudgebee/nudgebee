package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Searches jira issues, updates tickets, and adds comments.`
	toolInput := "A question or request about jira issue."
	toolOutput := "Jira issues list with summaries and links to the issues or result of the operation."

	core.RegisterNBAgentFactoryAndTool(TicketsAgentName, func(accountId string) (core.NBAgent, error) {
		return TicketMaster{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const TicketsAgentName = "tickets"

type TicketMaster struct {
	accountId string
}

func (l TicketMaster) GetName() string {
	return TicketsAgentName
}

func (l TicketMaster) GetNameAliases() []string {
	return []string{"Tickets"}
}

func (l TicketMaster) GetDescription() string {
	return `Retrieves and manages Jira issues. Use this agent to search for issues, update ticket fields, or add comments using natural language requests.

	Capabilities:
	* Searches Jira issues using natural language queries.
	* Updates ticket fields such as priority, status, or labels.
	* Adds comments to tickets.

	Usage:
	* Input: Provide a question or request about Jira issues (e.g., "Show me open bugs in project X", "Update ticket ABC-123 to high priority", "Add a comment to ticket XYZ-456").
	* Output: Returns a list of issues with summaries and links, or the result of the update/comment operation.
	`
}

func (l TicketMaster) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request**: Carefully analyze the user's request to understand if they want to search for issues, update a ticket, or add a comment.",
		"**Construct and use Jira Query Language**: For search operations, generate a valid JQL query to retrieve the issues. Always use Jira Query Language (JQL) to interact with Jira issues.",
		"**Format Output**: If the tool returns a list of issues, render each as a Markdown block with key, priority, status, assignee, labels and url. Do not summarize or analyze the issues.",
		"**Perform Operations**: For update or comment operations, use the ticket_master tool with the appropriate parameters.",
	}

	constraints := []string{
		"The JQL must be syntactically correct and executable by Jira.",
		"Always include relevant filters like `project`, `status`, `issuetype`, `assignee`, etc., based on the user's query.",
		"Prefer explicit field names (`project`, `status`, `priority`, `created`, etc.) over ambiguous wording.",
		"Do not make up values. If the user doesn't mention something (like project name), leave it out.",
		"Use ticket_master tool with operation_type = 'search' for searching issues.",
		"Use ticket_master tool with operation_type = 'update' to modify ticket fields. Required parameters: ticket_id, field_name, new_value.",
		"Use ticket_master tool with operation_type = 'comment' to add comments. Required parameters: ticket_id, comment_text.",
		"For array fields (like labels), use append: true to add values without overwriting existing ones.",
		"Do not filter output from tool it is already filtered.",
		"NEVER use SQL-style keywords like LIMIT in JQL queries - use ORDER BY with Jira's native sorting instead.",
		"NEVER use unsupported JQL operators or functions - stick to standard Jira operators (=, !=, ~, !~, >, <, >=, <=, IN, NOT IN).",
		"For pagination or limiting results, let Jira's API handle it - do not add any limit clauses to JQL.",
	}

	toolUsage := map[string][]string{
		"ticket_master": {
			"Use this tool to perform any ticket operation. Specify operation_type as 'search', 'update', or 'comment'.",
			"For search: Provide JQL query in the query parameter.",
			"For update: Provide ticket_id, field_name, and new_value. Use append: true to append to array fields instead of overwriting.",
			"For comment: Provide ticket_id and comment_text.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "What are the latest open bugs in Nudgebee?",
			Answer:      "ticket_master: {\"operation_type\": \"search\", \"query\": \"project = Nudgebee AND issuetype = Bug AND status = Open ORDER BY created DESC\"}",
			Explanation: "Using ticket_master tool to search for open bugs in Nudgebee project.",
		},
		{
			Question:    "Update ticket ABC-123 to high priority",
			Answer:      "ticket_master: {\"operation_type\": \"update\", \"ticket_id\": \"ABC-123\", \"field_name\": \"priority\", \"new_value\": \"High\"}",
			Explanation: "Using ticket_master tool to update the priority of ticket ABC-123 to High.",
		},
		{
			Question:    "Add labels 'bug' and 'high-priority' to ticket XYZ-123",
			Answer:      "ticket_master: {\"operation_type\": \"update\", \"ticket_id\": \"XYZ-123\", \"field_name\": \"labels\", \"new_value\": \"bug,high-priority\", \"append\": true}",
			Explanation: "Using ticket_master tool to append new labels to the existing labels of ticket XYZ-123.",
		},
		{
			Question:    "Add a comment to ticket XYZ-456: 'Fixed the issue by updating the configuration'",
			Answer:      "ticket_master: {\"operation_type\": \"comment\", \"ticket_id\": \"XYZ-456\", \"comment_text\": \"Fixed the issue by updating the configuration\"}",
			Explanation: "Using ticket_master tool to add a comment to ticket XYZ-456.",
		},
	}

	promptTemplate := core.NBAgentPrompt{
		Role:         "An intelligent ticketing Assistant",
		Instructions: instructions,
		Constraints:  constraints,
		OutputFormat: "Markdown",
		ToolUsage:    toolUsage,
		Examples:     examples,
	}

	return promptTemplate
}

func (l TicketMaster) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{
		tools.TicketMaster{},
	}
	return tools
}

func (l TicketMaster) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
