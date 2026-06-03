package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const OracleAgentName = "oracle"

func init() {
	toolDescription := `Diagnoses and troubleshoots Oracle Database issues by translating natural language questions into Oracle SQL queries. This tool is "smart" and handles its own database/instance discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
	toolInput := "Provide a question in natural language to investigate, query, or troubleshoot Oracle Database."
	toolOutput := "Returns query results and summaries for Oracle Database operations."

	core.RegisterNBAgentFactoryAndTool(OracleAgentName, func(accountId string) (core.NBAgent, error) {
		return newOracleAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newOracleAgent(accountId string) OracleDebugAgent {
	return OracleDebugAgent{
		accountId: accountId,
	}
}

type OracleDebugAgent struct {
	accountId string
}

func (l OracleDebugAgent) GetName() string {
	return OracleAgentName
}

func (l OracleDebugAgent) GetNameAliases() []string {
	return []string{"Oracle", "OracleDb", "OracleDatabase"}
}

func (l OracleDebugAgent) GetDescription() string {
	return `Diagnoses and troubleshoots Oracle Database issues by translating natural language questions into Oracle SQL queries. This tool is "smart" and handles its own database/instance discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
}

func (l OracleDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.OracleExecuteTool{}}
}

func (l OracleDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**1. Analyze the Request:** Determine the goal (e.g., performance tuning, lock analysis, session investigation, schema exploration).",
		"**2. Formulate Query:** Construct a valid Oracle SQL `SELECT` query. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, `INSERT`, `DROP`, `ALTER`, `EXECUTE`, or `CALL` statements.",
		"**3. Execute Query:** Use the `oracle_query_execute` tool with the following parameters:",
		"   - `query` (Required): The Oracle SQL query string. Do NOT include a trailing semicolon.",
		"   - `database` (Optional): The target service name / PDB if specified by the user.",
		"   - `instance` (Optional): The target instance/environment if specified by the user.",
		"**4. Interpret & Summarize:** Analyze the returned data. If no rows are returned, explain why.",
	}

	constraints := []string{
		"You MUST use the `oracle_query_execute` tool for all database interactions and MUST NOT answer questions without first querying the database.",
		"Use Oracle SQL syntax. Do NOT use PostgreSQL or MySQL syntax (e.g., use `ROWNUM` or `FETCH FIRST N ROWS ONLY` instead of `LIMIT`).",
		"When a user explicitly asks for 'all' data, do NOT add restrictive `WHERE` clauses unless requested.",
		"For schema discovery, query `ALL_TABLES`, `ALL_COLUMNS`, `ALL_INDEXES` or `USER_TABLES`, `USER_COLUMNS`.",
		"For performance diagnostics, use Oracle dynamic views: `V$SESSION`, `V$SQL`, `V$LOCKED_OBJECT`, `GV$SESSION`, `V$ACTIVE_SESSION_HISTORY`.",
		"Do NOT include a trailing semicolon in the query — it is added automatically.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteOracleQuery: {
			"Executes `SELECT` Oracle SQL queries. Input MUST be a JSON object with a `query` key (SQL string, no trailing semicolon) and optional `database` and `instance` keys. Example: `{\"query\": \"SELECT * FROM all_tables WHERE rownum <= 10\", \"database\": \"ORCLPDB1\", \"instance\": \"prod\"}`.",
			"Output: The data returned by the Oracle SQL query.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Show me all active sessions",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT sid, serial#, username, status, machine, program, sql_id FROM v$session WHERE type = 'USER' AND status = 'ACTIVE'"}`,
				},
			},
			Explanation: "This query lists all active user sessions from Oracle's V$SESSION view.",
		},
		{
			Question: "What are the long running queries?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT s.sid, s.serial#, s.username, s.status, q.sql_text, ROUND(s.last_call_et/60, 2) AS elapsed_minutes FROM v$session s JOIN v$sql q ON s.sql_id = q.sql_id WHERE s.type = 'USER' AND s.status = 'ACTIVE' AND s.last_call_et > 60 ORDER BY s.last_call_et DESC"}`,
				},
			},
			Explanation: "This query joins V$SESSION and V$SQL to find active sessions running for more than 60 seconds.",
		},
		{
			Question: "Show me current locked objects",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT lo.session_id, lo.oracle_username, lo.os_user_name, do.object_name, do.object_type, lo.locked_mode FROM v$locked_object lo JOIN dba_objects do ON lo.object_id = do.object_id"}`,
				},
			},
			Explanation: "This query identifies locked database objects by joining V$LOCKED_OBJECT with DBA_OBJECTS.",
		},
		{
			Question: "List all tables in the schema",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT table_name, num_rows, last_analyzed FROM user_tables ORDER BY table_name"}`,
				},
			},
			Explanation: "This query lists all tables owned by the current user along with row counts and last analysis date.",
		},
		{
			Question: "What are the top 10 most expensive SQL statements?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT sql_id, ROUND(elapsed_time/executions/1000000, 2) AS avg_elapsed_sec, executions, buffer_gets, disk_reads, SUBSTR(sql_text, 1, 100) AS sql_text FROM v$sql WHERE executions > 0 ORDER BY elapsed_time/executions DESC FETCH FIRST 10 ROWS ONLY"}`,
				},
			},
			Explanation: "This query identifies the top 10 SQL statements by average elapsed time from V$SQL.",
		},
		{
			Question: "Show active sessions in prod env",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT sid, serial#, username, status, machine, program FROM v$session WHERE type = 'USER' AND status = 'ACTIVE'", "instance": "prod"}`,
				},
			},
			Explanation: "Passing 'instance' selects the correct Oracle config for the prod environment.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an Oracle Database expert and troubleshooter",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown, with summary of oracle data",
	}
}

func (l OracleDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
