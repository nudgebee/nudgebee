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
		"**2. Introspect Unfamiliar Tables (MANDATORY):** For any table you have not already queried in this conversation, first run the schema-introspection query described in the constraints. Skipping this typically costs 3+ terminal `ORA-00942: table or view does not exist` / `ORA-00904: invalid identifier` errors before landing on the right shape.",
		"**3. Formulate Query:** Construct a valid Oracle SQL `SELECT` query using the column names verified in step 2. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, `INSERT`, `DROP`, `ALTER`, `EXECUTE`, or `CALL` statements.",
		"**4. Execute Query:** Use the `oracle_query_execute` tool with the following parameters:",
		"   - `query` (Required): The Oracle SQL query string. Do NOT include a trailing semicolon.",
		"   - `database` (Optional): The target service name / PDB if specified by the user.",
		"   - `instance` (Optional): The target instance/environment if specified by the user.",
		"**5. Interpret & Summarize:** Analyze the returned data. If no rows are returned, explain why.",
	}

	constraints := []string{
		"You MUST use the `oracle_query_execute` tool for all database interactions and MUST NOT answer questions without first querying the database.",
		"Use Oracle SQL syntax. Do NOT use PostgreSQL or MySQL syntax (e.g., use `ROWNUM` or `FETCH FIRST N ROWS ONLY` instead of `LIMIT`).",
		"When a user explicitly asks for 'all' data, do NOT add restrictive `WHERE` clauses unless requested.",
		"**Schema-first for unfamiliar tables (MANDATORY).** Before writing a `SELECT` against any table you have not already queried in this conversation, first introspect its columns via `SELECT column_name FROM USER_TAB_COLUMNS WHERE table_name = '<TABLE_UPPERCASE>'` (Oracle stores unquoted identifiers as uppercase). If you're unsure the table even exists, `SELECT table_name FROM USER_TABLES WHERE table_name LIKE '%<PATTERN>%'` first. **Prefer `USER_TAB_COLUMNS` / `USER_TABLES` over `ALL_TAB_COLUMNS` / `ALL_TABLES`** — the `ALL_` variants also scan every schema the user has *any* grant on (which can be slow on shared instances and produces duplicate rows for tables with the same name across schemas). Fall back to `ALL_` only when the user explicitly names a table in another schema. Both table and column hallucination are common failure modes; one introspection query prevents 3+ terminal `ORA-00942: table or view does not exist` / `ORA-00904: invalid identifier` errors.",
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
			Question: "How many rows are in the ORDERS table for customer ACME?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT column_name FROM USER_TAB_COLUMNS WHERE table_name = 'ORDERS'"}`,
				},
				{
					Tool:  tools.ToolExecuteOracleQuery,
					Input: `{"query": "SELECT COUNT(*) FROM orders WHERE customer_id = 'ACME'"}`,
				},
			},
			Explanation: "Introspect columns first (uppercased `ORDERS` because Oracle stores unquoted identifiers as uppercase). Verifies the join key is `customer_id` before the aggregation — avoids the guess-and-fail cycle that would otherwise cost 2-3 `ORA-00904: invalid identifier` errors.",
		},
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
