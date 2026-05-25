package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const ClickhouseAgentName = "clickhouse"

func init() {
	toolDescription := `Debugs ClickHouse issues based on natural language. `
	toolInput := "Provide ClickHouse issues related question in natural language."
	toolOutput := "The tool will return the issue response based on user question."

	core.RegisterNBAgentFactoryAndTool(ClickhouseAgentName, func(accountId string) (core.NBAgent, error) {
		return newClickhouseAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newClickhouseAgent(accountId string) ClickhouseDebugAgent {
	return ClickhouseDebugAgent{
		accountId: accountId,
	}
}

type ClickhouseDebugAgent struct {
	accountId string
}

func (l ClickhouseDebugAgent) GetName() string {
	return ClickhouseAgentName
}

func (l ClickhouseDebugAgent) GetNameAliases() []string {
	return []string{"ClickHouseDB", "ClickHouseSQL"}
}

func (l ClickhouseDebugAgent) GetDescription() string {
	return `Debugs ClickHouse issues based on natural language.`
}

func (l ClickhouseDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Ensure tools.ClickhouseExecuteTool{} will be created in a subsequent step
	supportedTools := []toolcore.NBTool{tools.ClickhouseExecuteTool{}}
	return supportedTools
}

func (l ClickhouseDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**1. Analyze the Request:** Identify the goal (e.g., performance analysis, data retrieval, table schema).",
		"**2. Formulate SQL:** Construct a valid Clickhouse SQL query. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, `INSERT`, `ALTER`, or `DROP` statements.",
		"**3. Execute Query:** Use the `clickhouse_query_execute` tool with the following parameters:",
		"   - `query` (Required): The SQL query string.",
		"   - `database` (Optional): The specific database name if provided by the user.",
		"**4. Interpret & Summarize:** Analyze the returned data. If no rows are found, explain why.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"You MUST use the `clickhouse_query_execute` tool for all database interactions. Do NOT answer without evidence from Clickhouse.",
		"When broad data is requested (e.g., 'all records'), do NOT add restrictive filters unless requested.",
		"Always include a `LIMIT` clause for broad queries to avoid massive outputs (unless 'all' is explicitly requested and necessary).",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteClickhouseQuery: {
			"Executes queries against Clickhouse. Input MUST be a JSON object with a `query` key and optional `database` key. Example: `{\"query\": \"SELECT * FROM logs LIMIT 10;\", \"database\": \"default\"}`.",
			"Output: Data returned by Clickhouse.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteClickhouseQuery] = []string{
			"Executes queries against Clickhouse. Input MUST be a JSON object with a `query` key and optional `database` key. Example: `{\"query\": \"SELECT * FROM logs LIMIT 10;\", \"database\": \"default\"}`.",
			"Output: Data returned by Clickhouse.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the query output.",
		}
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "How many tables are in the current database?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SHOW TABLES;"}`,
				},
			},
			Explanation: "This query lists all tables in the current ClickHouse database.",
		},
		{
			Question: "What are the columns in the 'users' table?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "DESCRIBE TABLE users;"}`,
				},
			},
			Explanation: "This query describes the schema (columns, types) of the 'users' table.",
		},
		{
			Question: "Show me the top 10 users by activity count from the 'user_activity' table.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT userID, count() AS activity_count FROM user_activity GROUP BY userID ORDER BY activity_count DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query retrieves the top 10 users based on their activity count.",
		},
		{
			Question: "What are the current long running queries?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT query_id, query_duration_ms, query, user FROM system.processes WHERE is_cancelled = 0 ORDER BY query_duration_ms DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query lists the top 10 currently running queries by duration from the system.processes table.",
		},
		{
			Question: "Show me the largest tables by disk size in the current database.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT table, formatReadableSize(sum(bytes_on_disk)) AS size FROM system.parts WHERE active AND database = currentDatabase() GROUP BY table ORDER BY sum(bytes_on_disk) DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query lists the top 10 largest tables in the current database based on their uncompressed size on disk.",
		},
		{
			Question: "Are there any tables with a high number of unmerged parts?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT database, table, count() AS parts_count FROM system.parts WHERE active AND database = currentDatabase() GROUP BY database, table HAVING parts_count > 100 ORDER BY parts_count DESC;"}`,
				},
			},
			Explanation: "This query identifies tables that might have too many active parts, which can impact performance. A high number of parts might indicate a need for optimization or checking MergeTree settings.",
		},
		{
			Question: "Which queries are consuming the most memory right now?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT query_id, formatReadableSize(memory_usage) AS mem_usage, query, user FROM system.processes ORDER BY memory_usage DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query shows the top 10 currently running queries by memory consumption.",
		},
		{
			Question: "What is the status of mutations on my tables?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT database, table, mutation_id, command, create_time, is_done FROM system.mutations WHERE is_done = 0 ORDER BY create_time DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query lists the 10 most recent unfinished mutations (ALTER UPDATE/DELETE operations) across all databases and tables.",
		},
		{
			Question: "List all available dictionaries and their current load status.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteClickhouseQuery,
					Input: `{"query": "SELECT name, type, status, origin, last_successful_update_time FROM system.dictionaries;"}`,
				},
			},
			Explanation: "This query provides information about all configured external dictionaries, including their type, current loading status, origin, and last successful update time.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a ClickHouse database expert and troubleshooter",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "markdown, with summary of ClickHouse data",
		Rag: core.NBAgentPromptRag{
			Module:         "clickhouse", // For RAG, if specific ClickHouse docs are available
			Format:         core.NBAgentPromptRagFormatJson,
			QuestionKey:    "Question",
			AnswerKey:      "Diagnostic Query",
			ExplanationKey: "Solution Hint",
		},
	}
}

func (l ClickhouseDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
