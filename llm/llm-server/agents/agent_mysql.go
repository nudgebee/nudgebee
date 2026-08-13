package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const MySQLAgentName = "mysql"

func init() {
	// This describes the 'mysql' agent when it is used as a tool by another agent.
	toolDescription := `Diagnoses and troubleshoots MySQL issues by translating natural language questions into SQL queries. This tool is "smart" and handles its own database discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
	toolInput := "Provide a question in natural language to investigate, query, or troubleshoot MySQL."
	toolOutput := "Returns query results and summaries for MySQL operations."

	core.RegisterNBAgentFactoryAndTool(MySQLAgentName, func(accountId string) (core.NBAgent, error) {
		return MySQLDebugAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

type MySQLDebugAgent struct {
	accountId string
}

func (l MySQLDebugAgent) GetName() string {
	return MySQLAgentName
}

func (l MySQLDebugAgent) GetNameAliases() []string {
	return []string{"MySql"}
}

func (l MySQLDebugAgent) GetDescription() string {
	return `Diagnoses and troubleshoots MySQL issues by translating natural language questions into SQL queries. This tool is "smart" and handles its own database discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
}

func (l MySQLDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.MySQLExecuteTool{}}
	return tools
}

func (l MySQLDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**1. Analyze the Request:** Identify the goal (e.g., table structure, query optimization, data retrieval).",
		"**2. Introspect Unfamiliar Tables (MANDATORY):** For any table you have not already queried in this conversation, first run the schema-introspection query described in the constraints. Skipping this typically costs 3+ terminal `Unknown column` / `Table doesn't exist` errors before landing on the right shape.",
		"**3. Formulate SQL:** Construct a valid MySQL `SELECT` query using the column names verified in step 2. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, or `INSERT` statements.",
		"**4. Execute Query:** Use the `mysql_query_execute` tool with the following parameters:",
		"   - `query` (Required): The SQL query string.",
		"   - `database` (Optional): The specific database name if provided by the user.",
		"**5. Interpret & Summarize:** Analyze the returned results. If no data is found, explain why.",
	}

	constraints := []string{
		"You MUST use the `mysql_query_execute` tool for all database interactions. Do NOT provide answers without evidence from the database.",
		"When broad data is requested (e.g., 'all users'), do NOT add restrictive filters unless requested.",
		"**Schema-first for unfamiliar tables (MANDATORY).** Before writing a `SELECT` against any table you have not already queried in this conversation, first introspect its columns via `SELECT column_name FROM information_schema.columns WHERE table_name = '<table>' AND table_schema = DATABASE()` (and, if you're unsure the table even exists, `SELECT table_name FROM information_schema.tables WHERE table_name LIKE '%<pattern>%' AND table_schema = DATABASE()` first). **Filter by `table_schema = DATABASE()`** — without it MySQL's `information_schema` returns rows across every database on the server, so when two databases have a table with the same name you'll get duplicate/misleading columns. Both table and column hallucination are common failure modes; one introspection query prevents 3+ terminal `Unknown column` / `Table doesn't exist` errors.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteMySQLQuery: {
			"Executes `SELECT` queries against MySQL. Input MUST be a JSON object with a `query` key and optional `database` key. Example: `{\"query\": \"SELECT * FROM orders LIMIT 10;\", \"database\": \"sales\"}`.",
			"Output: Data returned by the SQL query.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteMySQLQuery] = []string{
			"Executes `SELECT` queries against MySQL. Input MUST be a JSON object with a `query` key and optional `database` key. Example: `{\"query\": \"SELECT * FROM orders LIMIT 10;\", \"database\": \"sales\"}`.",
			"Output: Data returned by the SQL query.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the query output.",
		}
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "Find recent orders for customer 'acme-corp'",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SELECT column_name FROM information_schema.columns WHERE table_name = 'orders' AND table_schema = DATABASE();"}`,
				},
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SELECT order_id, customer_id, total, created_at FROM orders WHERE customer_id = 'acme-corp' ORDER BY created_at DESC LIMIT 20;"}`,
				},
			},
			Explanation: "Introspect the columns of `orders` first — the LLM might otherwise assume a `customer` or `name` column that doesn't exist. The introspection cost is one cheap query; the guess-and-fail alternative costs 2-3 terminal `Unknown column` errors before landing on the right shape.",
		},
		{
			Question: "Show me long running transactions?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SELECT id, user, host, db, command, time, state, info FROM information_schema.processlist WHERE command != 'Sleep' AND time > 60;"}`,
				},
			},
			Explanation: "This query helps identify transactions that have been running for more than 60 seconds, potentially indicating a performance bottleneck.",
		},
		{
			Question: "What is the status of my database connection?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SHOW STATUS;"}`,
				},
			},
			Explanation: "This query provides information about the server's operation and performance.",
		},
		{
			Question: "Show me active connections ?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SHOW PROCESSLIST;"}`,
				},
			},
			Explanation: "This query provides information about active connections.",
		},
		{
			Question: "show me the current locks",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteMySQLQuery,
					Input: `{"query": "SHOW ENGINE INNODB STATUS;"}`,
				},
			},
			Explanation: "This query will show currently held locks and other innodb internal status.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a MySQL database expert and troubleshooter",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module:         "mysql",
			Format:         core.NBAgentPromptRagFormatJson,
			QuestionKey:    "Question",
			AnswerKey:      "Answer",
			ExplanationKey: "Solution Hint",
		},
	}
}

func (l MySQLDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l MySQLDebugAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}
