package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

func init() {

	toolDescription := `loki query based on natural language question.`
	toolInput := "Provide loki question in natural language."
	toolOutput := "The tool will return the loki query retrieved from your question."

	core.RegisterNBAgentFactoryAsTool(LogqlAgentName, func(accountId string) (core.NBAgent, error) {
		return &LogqlAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const LogqlAgentName = "logql_query_generator"

type LogqlAgent struct {
	accountId string
	labels    []string
}

func (p LogqlAgent) GetName() string {
	return LogqlAgentName
}

func (l LogqlAgent) GetNameAliases() []string {
	return []string{"LogQl Query Generator"}
}

func (p LogqlAgent) GetDescription() string {
	return `Generate Loki query based on natural language question.`
}

func (l *LogqlAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate LogQL Query:** Construct a valid LogQL query based on the user's request.",
		"**Label Filtering:**",
		"   - Start the query with curly braces `{}`.",
		"   - Inside the braces, use key-value pairs (e.g., `{label=\"value\"}`).",
		"   - Use `=` for exact matches (e.g., `{app=\"my-app\"}`).",
		"   - Use `=~` for broader matches (e.g., `{app=~\"my-app.*\"}`).",
		"   - If the user asks for an application, use the `app` label.",
		"   - If the user mentions a namespace, use the `namespace` label.",
		"**Line Filtering:**",
		"   - Use `|=` for log line matching.",
		"   - Use `|~` for regular expression matching (case-insensitive).",
		"**Query Style:**",
		"   - Keep the query as short and to the point as possible.",
		"   - Do not include any additional information.",
		"**Error Handling:** Return a JSON in case of an error with the key 'error'.",
	}
	constraints := []string{
		"Always return a valid LogQL query",
		"Do not add any formatting or additional information in the response. Return only LogQL query",
		"Do not wrap the query in backticks (```).",
		"If no query can be build, return json with the following format: {\"error\":\"error message\"}",
	}

	// get supported labels
	if len(l.labels) == 0 {
		labels, err := tools.GetLokiLabels(l.accountId)
		if err == nil {
			l.labels = labels
		}
	}

	if len(l.labels) > 0 {
		constraints = append(constraints, fmt.Sprintf("ONLY use these valid labels: %s. Using any other label will cause the query to fail.", strings.Join(l.labels, ", ")))
	}

	toolUsage := map[string][]string{}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me logs for the application 'my-app'.",
			Answer:      "{app=\"my-app\"}",
			Explanation: "This LogQL query uses the `app` label to filter logs for the application 'my-app'.",
		},
		{
			Question:    "Get logs for 'my-app' in the 'prod' namespace.",
			Answer:      "{app=\"my-app\", namespace=\"prod\"}",
			Explanation: "This LogQL query filters logs for the 'my-app' application within the 'prod' namespace.",
		},
		{
			Question:    "Show logs for 'my-app' mentioning errors or exceptions.",
			Answer:      "{app=\"my-app\"} |~ \"(?i)error|exception\"",
			Explanation: "This LogQL query filters logs for the 'my-app' application and matches lines containing 'error' or 'exception' (case-insensitive).",
		},
		{
			Question:    "Get me recent logs from services-server containing hasura",
			Answer:      "{app=\"services-server\"} |= \"hasura\"",
			Explanation: "This LogQL query filters logs for the 'services-server' application and matches lines containing 'hasura'.",
		},
		{
			Question:    "get me logs containing error for services-server in prod namespace",
			Answer:      "{app=\"services-server\", namespace=\"prod\"} |~ \"(?i)error\"",
			Explanation: "This LogQL query filters logs for the 'services-server' application within the 'prod' namespace and matches lines containing 'error'.",
		},
		{
			Question:    "Get all logs from namespace test containing exception",
			Answer:      "{namespace=\"test\"} |~ \"(?i)exception\"",
			Explanation: "This LogQL query filters logs for all apps from test namespace and matches lines containing 'exception'.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Loki and LogQL",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "loki",
		},
	}
}

func (p LogqlAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l LogqlAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}
