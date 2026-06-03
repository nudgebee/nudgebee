package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const MSSQLAgentName = "mssql"

func init() {
	toolDescription := `Diagnoses and troubleshoots Microsoft SQL Server issues by translating natural language questions into T-SQL queries. This tool is "smart" and handles its own database discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
	toolInput := "Provide a question in natural language to investigate, query, or troubleshoot MSSQL."
	toolOutput := "Returns query results and summaries for MSSQL operations."

	core.RegisterNBAgentFactoryAndTool(MSSQLAgentName, func(accountId string) (core.NBAgent, error) {
		return MSSQLDebugAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

type MSSQLDebugAgent struct {
	accountId string
}

func (l MSSQLDebugAgent) GetName() string {
	return MSSQLAgentName
}

func (l MSSQLDebugAgent) GetNameAliases() []string {
	return []string{"MsSql", "SqlServer"}
}

func (l MSSQLDebugAgent) GetDescription() string {
	return `Diagnoses and troubleshoots Microsoft SQL Server issues by translating natural language questions into T-SQL queries. This tool is "smart" and handles its own database discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
}

func (l MSSQLDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.MSSQLExecuteTool{}}
}

func (l MSSQLDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**1. Analyze the Request:** Identify the goal (e.g., table structure, query optimization, data retrieval, performance diagnostics).",
		"**2. Formulate T-SQL:** Construct a valid T-SQL `SELECT` query. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, `INSERT`, `DROP`, `ALTER`, or `EXEC` statements.",
		"**3. Execute Query:** Use the `mssql_query_execute` tool with the following parameters:",
		"   - `query` (Required): The T-SQL query string.",
		"   - `database` (Optional): The specific database name if provided by the user.",
		"**4. Interpret & Summarize:** Analyze the returned results. If no data is found, explain why.",
	}

	constraints := []string{
		"You MUST use the `mssql_query_execute` tool for all database interactions. Do NOT provide answers without evidence from the database.",
		"When broad data is requested (e.g., 'all users'), do NOT add restrictive filters unless requested.",
		"If schema information is needed, query `INFORMATION_SCHEMA.TABLES` and `INFORMATION_SCHEMA.COLUMNS` first.",
		"Use T-SQL syntax (not MySQL or PostgreSQL syntax). For example, use `TOP N` instead of `LIMIT N`.",
		"For performance diagnostics, use Dynamic Management Views (DMVs) such as `sys.dm_exec_requests`, `sys.dm_exec_sessions`, `sys.dm_exec_query_stats`.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteMSSQLQuery: {
			"Executes `SELECT` queries against Microsoft SQL Server. Input MUST be a JSON object with a `query` key and optional `database` key. Example: `{\"query\": \"SELECT TOP 10 * FROM orders\", \"database\": \"sales\"}`.",
			"Output: Data returned by the T-SQL query.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Show me long running queries?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMSSQLQuery,
					Input: `{"query": "SELECT r.session_id, r.start_time, r.status, r.command, r.wait_type, r.total_elapsed_time/1000 AS elapsed_seconds, t.text AS query_text FROM sys.dm_exec_requests r CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t WHERE r.total_elapsed_time > 60000 ORDER BY r.total_elapsed_time DESC"}`,
				},
			},
			Explanation: "This query identifies requests running longer than 60 seconds using the dm_exec_requests DMV.",
		},
		{
			Question: "What is the status of my database?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMSSQLQuery,
					Input: `{"query": "SELECT name, state_desc, recovery_model_desc, compatibility_level FROM sys.databases"}`,
				},
			},
			Explanation: "This query provides information about all databases and their states.",
		},
		{
			Question: "Show me active connections?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMSSQLQuery,
					Input: `{"query": "SELECT session_id, login_name, host_name, program_name, status, cpu_time, memory_usage, last_request_start_time FROM sys.dm_exec_sessions WHERE is_user_process = 1"}`,
				},
			},
			Explanation: "This query provides information about active user sessions.",
		},
		{
			Question: "Show me current blocking",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMSSQLQuery,
					Input: `{"query": "SELECT r.session_id, r.blocking_session_id, r.wait_type, r.wait_time, t.text AS query_text FROM sys.dm_exec_requests r CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t WHERE r.blocking_session_id > 0"}`,
				},
			},
			Explanation: "This query shows currently blocked sessions and the sessions blocking them.",
		},
		{
			Question: "Query the employees table in testdb",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMSSQLQuery,
					Input: `{"query": "SELECT TOP 100 * FROM employees", "database": "testdb"}`,
				},
			},
			Explanation: "Use the 'database' parameter to target a specific database instead of USE statements.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a Microsoft SQL Server database expert and troubleshooter",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module:         "mssql",
			Format:         core.NBAgentPromptRagFormatJson,
			QuestionKey:    "Question",
			AnswerKey:      "Answer",
			ExplanationKey: "Solution Hint",
		},
	}
}

func (l MSSQLDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
