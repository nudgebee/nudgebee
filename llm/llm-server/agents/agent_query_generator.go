package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"sort"
	"strings"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Generates JSON Query based on Natural language`
	toolInput := "Provide natural language question"
	toolOutput := "JSON Query Response"

	core.RegisterNBAgentFactoryAsTool(QueryGeneratorAgentName, func(accountId string) (core.NBAgent, error) {
		return &QueryGeneratorAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

func newQueryGeneratorAgent(accountId string, fields []string, supportedOperators []string, examples []core.NBAgentPromptExample, availableIndices map[string]string) *QueryGeneratorAgent {
	return &QueryGeneratorAgent{
		accountId:          accountId,
		fields:             fields,
		examples:           examples,
		supportedOperators: supportedOperators,
		availableIndices:   availableIndices,
	}
}

const QueryGeneratorAgentName = "query_generator"

type QueryGeneratorAgent struct {
	accountId          string
	fields             []string
	supportedOperators []string
	examples           []core.NBAgentPromptExample
	availableIndices   map[string]string
}

func (l *QueryGeneratorAgent) GetNameAliases() []string {
	return []string{"Query Generator"}
}

func (l *QueryGeneratorAgent) GetName() string {
	return QueryGeneratorAgentName
}

func (l *QueryGeneratorAgent) GetDescription() string {
	return `Returns a valid JSON query based on natural language question.`
}

func (l *QueryGeneratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	supportedOperators := l.supportedOperators
	if len(supportedOperators) == 0 {
		supportedOperators = []string{"_eq", "_neq", "_gt", "_gte", "_lt", "_lte", "_in", "_nin", "_like", "_ilike", "_nlike", "_is_null", "_or", "_and"}
	}

	fieldsProvided := len(l.fields) > 0
	if len(l.fields) == 0 {
		l.fields = []string{"_body", "namespace", "pod"}
	}

	instructions := []string{
		"**GOAL:** Only Generate Query, Cannot Execute Query.",
		"You are an expert in generating JSON queries from natural language.",
		"Your goal is to create a valid JSON query based on the user's question.",
		"Follow this JSON schema:",
		`{"where": {"<field>": {"<operator>": "<value>"}}, "_or": [ ... ], "_and": [ ... ]}, "limit": <number>, "time_range": "<string>", "start_time": "<string>", "index": "<string>"}`,
		"The `where` clause is for filtering. For `_and` or `_or` operators, the value is an array of filter objects.",
		"The `index` field is optional. Use it to target a specific Elasticsearch index or pattern when the user's query implies a particular log source.",
		"Do not use anything other than the provided fields and operators.",
		"Prefer ilike operator for regex matches.",
		"Prefer ilike operator for text matches over eq operator.",
		"AVAILABLE FIELDS AND OPERATORS for query building",
		fmt.Sprintf("  - **Fields**: %s", strings.Join(l.fields, ", ")),
		fmt.Sprintf("  - **Operators**: %s", strings.Join(supportedOperators, ", ")),
	}

	if len(l.availableIndices) > 0 {
		indexList := make([]string, 0, len(l.availableIndices))
		keys := make([]string, 0, len(l.availableIndices))
		for k := range l.availableIndices {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, name := range keys {
			indexList = append(indexList, fmt.Sprintf("%s (%s)", name, l.availableIndices[name]))
		}
		instructions = append(instructions,
			"AVAILABLE ELASTICSEARCH INDICES:",
			fmt.Sprintf("  %s", strings.Join(indexList, ", ")),
			"Pick the most relevant index based on the user's question. If unsure or the request is general, omit the index field to use the account default.",
		)
	}

	constraints := []string{}
	if fieldsProvided {
		// The label-bias guardrail is the load-bearing constraint here: providers
		// like Loki expose labels named `app` / `job` / `container`, but the
		// model's training prior strongly favours OTel/Datadog conventions
		// (`service_name`, `service.name`, `kubernetes.*`). Without an explicit
		// prohibition the model emits those names even when the Fields list
		// does not contain them, the query returns no data, and the parent
		// log agent silently degrades to a kubectl fallback. See #28517.
		constraints = append(constraints,
			"MUST use ONLY the labels/fields listed in the Fields section above. Do not invent labels.",
			"NEVER emit labels that are not in the Fields list. Do not fall back to generic OTel/Datadog conventions (e.g. `service_name`, `service.name`, `kubernetes.*`) unless they are explicitly present in the Fields list. If the equivalent appears in the Fields list under a different name (e.g. `app`, `namespace`, `pod`), use that name verbatim.",
			"When the user's natural-language question uses generic words like 'service X', 'pod X', or 'app X', map them to the matching label from the Fields list. The choice of label name is dictated by the Fields list, not by the user's wording.",
		)
	}
	constraints = append(constraints, []string{
		"Do not answer questions without generating a query.",
		"Ensure the generated JSON is a valid query.",
		"Return only the JSON query object.",
	}...)

	toolUsage := map[string][]string{}

	outputFormat := "A single, valid JSON query object, enclosed in triple backticks."
	rag := core.NBAgentPromptRag{
		Module: "query",
		Format: core.NBAgentPromptRagFormatJson,
	}

	examples := l.examples
	if len(l.examples) == 0 {
		examples = []core.NBAgentPromptExample{
			{
				Question:    "show me recent 504 failures for services abc?",
				Answer:      `{"where":{"http.status_code": {"_eq": 504}, "service_name": {"_eq": "abc"}}}`,
				Explanation: "Available Labels - http.status_code, service_name",
			},
			{
				Question:    "How many apis are taking more than 10seconds for service abc?",
				Answer:      `{"where": {"duration_ns": {"_gt": 10000000000}, "service_name": {"_eq": "abc"}}}`,
				Explanation: "Available Labels - duration_ns, service_name",
			},
			{
				Question:    "Get Recent Api Failures on services-server?",
				Answer:      `{"where": {"service_name": {"_eq": "services-server"}, "http.status_code": {"_gte": 500}}}`,
				Explanation: "Available Labels - service_name, http.status_code",
			},
			{
				Question:    "Show me traces from the last 2 hours for ml-k8s-server",
				Answer:      `{"where": {"service_name": {"_eq": "ml-k8s-server"}}, "time_range": "2h"}`,
				Explanation: "Available Labels - service_name, time_range",
			},
			{
				Question:    "get traces of llm server",
				Answer:      `{"where": {"service_name": {"_eq": "llm-server"}}}`,
				Explanation: "Available Labels - service_name, time_range",
			},
			{
				Question:    "get traces of llm server after 2025-01-01",
				Answer:      `{"where": {"service_name": {"_eq": "llm-server"}}, "start_time": "2025-01-01T00:00:00Z"}`,
				Explanation: "Available Labels - service_name, start_time",
			},
			{
				Question:    "get 10 error logs of services-server",
				Answer:      `{"where": {"service_name": {"_eq": "services-server"}, "body": {"_ilike": "%error%"}}, "limit":10}`,
				Explanation: "Available Labels - service_name, body",
			},
			{
				Question:    "Get me recent logs of app metrics-server in kube-system namespace",
				Answer:      `{"where": {"service_name": {"_eq": "metrics-server"}}, "limit":10}`,
				Explanation: "Available Labels - service_name, body",
			},
			//Get me recent logs of app metrics-server in kube-system namespace
		}
	}

	return core.NBAgentPrompt{
		Role:         "Query generation expert in generating JSON query",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Examples:     examples,
		Rag:          rag,
	}
}

func (l *QueryGeneratorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l *QueryGeneratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}

func (l *QueryGeneratorAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
