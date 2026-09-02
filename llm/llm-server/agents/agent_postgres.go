package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const PostgresAgentName = "postgres"

func init() {
	// This describes the 'postgres' agent when it is used as a tool by another agent (e.g., k8s_debug).
	toolDescription := `Diagnoses and troubleshoots PostgreSQL issues by translating natural language questions into SQL queries. This tool is "smart" and handles its own database/instance discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
	toolInput := "Provide a question in natural language to investigate, query, or troubleshoot PostgreSQL."
	toolOutput := "Returns query results and summaries for PostgreSQL operations."

	core.RegisterNBAgentFactoryAndTool(PostgresAgentName, func(accountId string) (core.NBAgent, error) {
		return newPostgresAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newPostgresAgent(accountId string) PostgresDebugAgent {
	return PostgresDebugAgent{
		accountId: accountId,
	}
}

type PostgresDebugAgent struct {
	accountId string
}

func (l PostgresDebugAgent) GetName() string {
	return PostgresAgentName
}

func (l PostgresDebugAgent) GetNameAliases() []string {
	return []string{"PostgresSql", "Postgres"}
}

func (l PostgresDebugAgent) GetDescription() string {
	return `Diagnoses and troubleshoots PostgreSQL issues by translating natural language questions into SQL queries. This tool is "smart" and handles its own database/instance discovery. Use this agent directly to investigate performance, query data, or analyze database health without needing separate reconnaissance. Returns query results and summaries for automation, monitoring, or troubleshooting.`
}

func (l PostgresDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {

	tools := []toolcore.NBTool{tools.PostgresExecuteTool{}}
	return tools
}

func (l PostgresDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**1. Analyze the Request:** Determine the goal (e.g., performance tuning, lock analysis, general investigation).",
		"**2. Introspect Unfamiliar Tables (MANDATORY):** For any table you have not already queried in this conversation, first run the schema-introspection query described in the constraints. Skipping this typically costs 3+ terminal `column X does not exist` / `relation Y does not exist` errors before landing on the right shape.",
		"**3. Formulate Query:** Construct a valid PostgreSQL `SELECT` query using the column names verified in step 2. **CRITICAL:** You are strictly forbidden from using `CREATE`, `UPDATE`, `DELETE`, or `INSERT` statements.",
		"**4. Execute Query:** Use the `postgres_query_execute` tool with the following parameters:",
		"   - `query` (Required): The SQL query string.",
		"   - `database` (Optional): The target database name if specified by the user.",
		"   - `instance` (Optional): The target instance/environment if specified by the user.",
		"**5. Interpret & Summarize:** Analyze the returned data. If no rows are returned, explain why.",
	}

	constraints := []string{
		"You MUST use the `postgres_query_execute` tool for all database interactions and MUST NOT answer questions without first querying the database using this tool.",
		"When a user explicitly asks for 'all' data or uses similar broad terms, do NOT add restrictive `WHERE` clauses or filters unless specifically requested.",
		"**Schema-first for unfamiliar tables (MANDATORY).** Before writing a `SELECT` against any table you have not already queried in this conversation, first introspect its columns via `SELECT column_name FROM information_schema.columns WHERE table_name = '<table>' AND table_schema = 'public'` (and, if you're unsure the table even exists, `SELECT table_name FROM information_schema.tables WHERE table_name ILIKE '%<pattern>%' AND table_schema = 'public'` first). **Filter by `table_schema` (default `'public'`, replace with the active schema when appropriate)** — without it `information_schema` returns rows across every schema and you'll get duplicate/misleading columns when the same table name lives in multiple schemas. Both table hallucination AND column hallucination (guessing a plausible column that doesn't exist in a real table) are common failure modes; one introspection query prevents 3+ terminal `column X does not exist` / `relation Y does not exist` errors.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecutePostgresQuery: {
			"Executes `SELECT` SQL queries against PostgreSQL. Input MUST be a JSON object with a `query` key (SQL query string) and optional `database` and `instance` keys. Include `database` or `instance` if explicitly mentioned by the user. Example: `{\"query\": \"SELECT * FROM users;\", \"database\": \"mydb\", \"instance\": \"dev\"}`.",
			"Output: The data returned by the SQL query.",
		},
	}

	toolUsage[tools.ToolExecutePostgresQuery] = []string{
		"Executes `SELECT` SQL queries against PostgreSQL. Input MUST be a JSON object with a `query` key (SQL query string) and optional `database` and `instance` keys. Include `database` or `instance` if explicitly mentioned by the user. Example: `{\"query\": \"SELECT * FROM users;\", \"database\": \"mydb\", \"instance\": \"dev\"}`.",
		"Output: The data returned by the SQL query.",
		"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the query output.",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "Find the checkout workload in the demo namespace",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT table_name FROM information_schema.tables WHERE table_name ILIKE '%workload%' AND table_schema = 'public';"}`,
				},
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT column_name FROM information_schema.columns WHERE table_name = 'k8s_workloads' AND table_schema = 'public';"}`,
				},
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT workload_name, namespace, workload_type FROM k8s_workloads WHERE workload_name = 'checkout' AND namespace = 'demo';"}`,
				},
			},
			Explanation: "Two introspection queries first — list matching tables, then list columns — then the real query using verified names. Avoids the guess-and-fail cycle that would otherwise take 3+ terminal errors (`relation X does not exist`, `column Y does not exist`) before landing on the right shape. The introspection queries are cheap; the guess-and-fail loop is not.",
		},
		{
			Question: "Can you optimize query performance of - select * from users?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "explain analyze select * from users;"}`,
				},
			},
			Explanation: "First run explain analyze to understand query plan and then decide next steps",
		},
		{
			Question: "Can you get me all connections?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT * FROM pg_stat_activity;"}`,
				},
			},
			Explanation: "This query retrieves all active connections to the PostgreSQL database. note that this may return a large number of rows depending on the number of active connections. We are not adding any filters as user just asked for all connections. if user wanted specific database, state or user we could have added WHERE clause accordingly.",
		},
		{
			Question: "What are the currently long running queries?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT pid, age(clock_timestamp(), query_start), usename, query FROM pg_stat_activity WHERE query != '<IDLE>' AND query NOT ILIKE '%pg_stat_activity%' ORDER BY query_start desc;"}`,
				},
			},
			Explanation: "This query lists active queries and their durations. Filtering out '<IDLE>' and queries related to `pg_stat_activity` provides a cleaner view of long-running user queries.",
		},
		{
			Question: "What are the top 10 longest-running queries?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT query, (total_time/calls) AS avg_time FROM pg_stat_statements ORDER BY avg_time DESC LIMIT 10;"}`,
				},
			},
			Explanation: "This query shows the top 10 most time-consuming queries by average execution time. Requires the 'pg_stat_statements' extension.",
		},
		{
			Question: "show me active locks in DB abc in dev environment?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT pid, mode, granted, query FROM pg_locks pl LEFT JOIN pg_stat_activity pa ON pl.pid = pa.pid WHERE NOT granted;", "database": "abc", "instance": "dev"}`,
				},
			},
			Explanation: "This query identifies blocked queries waiting for locks. As the user specified the database 'abc' and 'dev' environment, these are passed in the 'database' and 'instance' fields respectively.",
		},
		{
			Question: "What are the top 10 longest-running queries in my dev env?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecutePostgresQuery,
					Input: `{"query": "SELECT query, (total_time/calls) AS avg_time FROM pg_stat_statements ORDER BY avg_time DESC LIMIT 10;", "instance": "dev"}`,
				},
			},
			Explanation: "This query shows the top 10 most time-consuming queries by average execution time. Requires the 'pg_stat_statements' extension, Also specifying instance=dev as its mentioned in query",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a PostgreSQL database expert and troubleshooter",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown, with summary of postgres data",
	}
}

func (l PostgresDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l PostgresDebugAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}
