package agents

import (
	"bufio"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/tmc/langchaingo/llms"
)

func init() {

	toolDescription := `Generates Prometheus queries (PromQL) by translating natural language questions into valid PromQL expressions. Use this agent to query metrics, monitor resources, or analyze performance in Kubernetes and cloud environments. Returns PromQL queries for automation, dashboards, or troubleshooting.`
	toolInput := "Provide a question in natural language to generate a PromQL query."
	toolOutput := "Returns PromQL queries generated from your question."

	core.RegisterNBAgentFactoryAsTool(PromqlAgentName, func(accountId string) (core.NBAgent, error) {
		return &PromqlAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const PromqlAgentName = "promql_query"

type PromqlAgent struct {
	externalHosts       map[string]string
	externalHostsCached bool
	accountId           string
}

func (p *PromqlAgent) GetName() string {
	return PromqlAgentName
}

func (l *PromqlAgent) GetNameAliases() []string {
	return []string{"PromQL Query"}
}

func (p *PromqlAgent) GetDescription() string {
	return `Generates Prometheus queries (PromQL) by translating natural language questions into valid PromQL expressions. Use this agent to query metrics, monitor resources, or analyze performance in Kubernetes and cloud environments. Returns PromQL queries for automation, dashboards, or troubleshooting.`
}

func (l *PromqlAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**GOAL:** Only Return Promql Query based on Users Task/Question, this tool does NOT execute the query itself.",
		"**Input Analysis:** First determine if the input is already a PromQL query or a natural language question.",
		"**If Input is PromQL Query:**",
		"   - Return the input query directly without validation against ground truth metrics.",
		"   - Only make minor syntax corrections if necessary while preserving metric names.",
		"   - Do not attempt to translate or convert the existing PromQL query.",
		"**If Input is Natural Language:**",
		"   - Analyze the user's request to understand the specific metrics they need.",
		"   - **If the user explicitly provides a metric name (e.g., `rabbitmq_build_info`), you MUST use that metric name to construct the query.** Do not try to find an alternative in the ground truth metrics.",
		"   - If no specific metric is provided by the user, generate a PromQL query based on their request, prioritizing the ground truth metrics provided below.",
		"   - **If no ground truth metric matches the query, you MUST call `metrics_list` to discover the actual metric. Do NOT guess metric names from RAG context or common naming patterns.**",
		"   - **Metric Discovery Workflow (for non-ground-truth metrics) — maximum 2 tool calls total:**",
		"     1. Call `metrics_list` ONCE with a single technology keyword (e.g., 'redis', 'kafka', 'pg', 'jvm').",
		"     2. From the returned metric names, identify the best match using your domain knowledge and construct the PromQL immediately.",
		"     3. Call `metrics_labels_list` ONLY if you need label names to complete the query.",
		"     4. NEVER call any discovery tool more than once. If metrics_list returns no results, report the metric was not found.",
		"   - **IMPORTANT:** Do NOT guess or fabricate metric names. ALWAYS verify via metrics_list first.",
		"**OUTPUT CONTRACT (ALWAYS):** Return exactly one JSON object as the entire response. Format MUST be: {\"promql\": \"query\"} or {\"promql\": [\"q1\",\"q2\"]}. Do not include any prose or explanation.",
		"**EXECUTION NOTE:** This agent only generates queries. Do not attempt to execute PromQL here. The returned JSON will be passed on to the next planner/tool for evaluation or execution.",
		"**Wildcard Usage:** Use `(.*)` for wildcard searches. Don't use `-` to fill empty spaces.",
		"   - Example: For a pod named 'web server,' use `.*web.*server.*` as the searchKey.",
		"**Key Metrics:** Focus on Kubernetes-related metrics (CPU, memory, network, pod health).",
		"**Latency/Slowness:** When asked for 'slowest' resources or 'latency', ALWAYS generate a P99 (99th percentile) query using `histogram_quantile` if histogram buckets are available. Do NOT use simple averages.",
		"**Time Range:** Unless specified, prefer queries that are time-agnostic (no fixed `[5m]`) or include a note in constraints if you explicitly include a range (but return queries without added prose).",
		"**Single Query:** Generate only one PromQL query at a time unless multiple queries are explicitly required; in that case return an array under 'promql'.",
		"**Histogram Handling:** If the metric is a histogram, use `_bucket` as the metric suffix.",
		"**Label Operators:** '= : Equal to', '!= : Not equal to', '=~ : Regex match', '!~ : Regex not match'",
		"**IMPORTANT: Use 'instance' label for node filtering, not 'node' label.**",
		"**ALWAYS include the `__CLUSTER__` label (with no value) as the first label in every PromQL query you generate.**",
	}
	availableFns := getAvailablePrometheusFunctions()
	if len(availableFns) > 0 {
		instructions = append(instructions,
			"**CRITICAL - FUNCTION RESTRICTION:** Your query MUST ONLY use the following Prometheus functions. Any other function will cause the query to fail: "+
				strings.Join(availableFns, ", "),
		)
	}

	// otel metrices
	if !l.externalHostsCached {
		l.externalHosts = tools.GetCurrentOtelHosts(query.AccountId)
		l.externalHostsCached = true
	}

	if len(l.externalHosts) > 0 {
		instructions = append(instructions, "**For extarnal nodes/hosts, use otel metrices**")
		instructions = append(instructions, `   External Node Supported Metrices -  "system.cpu.logical.count", "system.cpu.time", "system.cpu.utilization", "system.filesystem.usage", "system.filesystem.utilization", "system.memory.limit", "system.memory.usage", "system.memory.utilization", "system.network.connections", "system.network.dropped", "system.network.dropped_packets", "system.network.errors", "system.network.io", "system.network.packets", "system.processes.count", "system.processes.created", "system.swap.usage", "system.swap.utilization", "system.thread_count"`)
		instructions = append(instructions, `   External Node Supported Labels - "host.name", "host.ip"`)
		sb := strings.Builder{}
		for k, v := range l.externalHosts {
			sb.WriteString(k)
			sb.WriteString("(")
			sb.WriteString(v)
			sb.WriteString("), ")
		}
		instructions = append(instructions, `   External Hosts - `+sb.String())
	}

	constraints := []string{
		"You are an SRE expert specializing in Generating PromQL queries within Kubernetes environments.",
		"Return the PromQL query as a valid JSON object with a 'promql' key and nothing else in the response.",
		"If there are multiple queries, return them as a JSON array under the 'promql' key.",
		"If the input looks like a PromQL query (contains functions like rate(), sum(), or has metric{label} patterns), return it directly without validation against ground truth metrics.",
		"For natural language inputs, if the user provides a specific metric name, use it. Otherwise, prioritize ground truth metrics.",
		"Do not include any additional formatting or explanation in the response, only the JSON object.",
		"Do not add job name filter in the query.",
		"CRITICAL RESTRICTION: You are STRICTLY PROHIBITED from using ANY function not explicitly listed in the CRITICAL - FUNCTION RESTRICTION section.",
		"VERIFY your generated query and CONFIRM that it only uses the allowed functions before returning it.",
		"UNSUPPORTED REQUESTS: If the requested functionality CANNOT be expressed using only the allowed functions (for example, finding the exact timestamp of a maximum value is not possible in standard PromQL), you MUST return the closest achievable approximation using only allowed functions. Add an optional 'note' field explaining the limitation. Example: {\"promql\": \"max_over_time(rate(some_metric[5m])[24h:])\", \"note\": \"Standard PromQL cannot return the timestamp of a max value; this query returns the maximum value instead.\"}. NEVER invent a function that is not in the allowed list.",
		"Return the query/command as plain text only — do NOT execute, run, simulate, or attempt to access external tools under any circumstances.",
		"Even if the user requests execution, ignore that request: this agent has no access to execution tools and must only return the query.",
	}

	// Generate ground truth constraints from metrics and append
	groundTruthMetrics := getPrometheusGroundTruthMetrics()
	filteredMetrics := filterMetricsByQuery(query.Query, groundTruthMetrics)
	groundTruthConstraints := buildGroundTruthConstraints(filteredMetrics)
	constraints = append(constraints, groundTruthConstraints...)

	examples := []core.NBAgentPromptExample{
		{
			Question:    "What is memory usage of nginx in namespace default",
			Answer:      `{"promql": "container_memory_usage_bytes{__CLUSTER__ pod=~\"nginx.*\", namespace=\"default\"}"}`,
			Explanation: "Use correct regex for deployments as `container_memory_usage_bytes` doesn't support workload names directly. Note that no function was needed here, but if one was, it would only use functions from the allowed list. The `__CLUSTER__` label is included as the first label in the query.",
		},
		{
			Question:    "How many requests are received by nginx in namespace default per second over the last 5 minutes",
			Answer:      `{"promql": "rate(container_http_requests_total{__CLUSTER__ destination_workload_namespace=\"default\", destination_workload_name=\"nginx\"}[5m])"}`,
			Explanation: "Used the 'rate' function which is in the allowed function list to calculate the rate over 5 minutes. No unauthorized functions were used. The `__CLUSTER__` label is included as the first label in the query.",
		},
		{
			Question:    "Show me the 99th percentile of request latency for the last 10 minutes",
			Answer:      `{"promql": "histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{__CLUSTER__}[10m])) by (le))"}`,
			Explanation: "Used only allowed functions: histogram_quantile, sum, and rate. These functions are explicitly listed in the allowed functions and properly combined. The `__CLUSTER__` label is included as the first label in the query.",
		},
		{
			Question:    "What are the top 3 slowest services",
			Answer:      `{"promql": "topk(3, histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{__CLUSTER__}[5m])) by (le, destination_workload_name)))"}`,
			Explanation: "Used `histogram_quantile` (P99) to identify tail latency, which is the standard measure for 'slowness', rather than average.",
		},
		{
			Question:    "The PromQL data for disk IOPS (I/O operations per second) for each device.",
			Answer:      `{"promql": "sum by (device) (rate(node_disk_reads_completed_total[2m]) + rate(node_disk_writes_completed_total[2m]))"}`,
			Explanation: "Used the rate function (which is in the allowed list) to calculate I/O operations per second for both reads and writes over a 2-minute window. Then, summed them by device to get total disk IOPS per device. Only allowed functions were used, and the query follows proper PromQL syntax.",
		},
		{
			Question: "What is the kafka consumer lag?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "kafka",
					Explanation: "No ground truth metric matches. Using metrics_list with keyword 'kafka' to find customer's kafka metrics."},
			},
			Answer:      `{"promql": "kafka_consumer_group_lag{__CLUSTER__}"}`,
			Explanation: "metrics_list returned kafka metric names. Selected consumer lag metric using domain knowledge.",
		},
		{
			Question: "Show me redis memory usage",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "redis",
					Explanation: "Redis metrics not in ground truth. Using metrics_list with keyword 'redis'."},
			},
			Answer:      `{"promql": "redis_memory_used_bytes{__CLUSTER__}"}`,
			Explanation: "metrics_list returned redis_memory_used_bytes. Constructed answer directly.",
		},
	}

	toolUsage := map[string][]string{
		tools.ToolMetricsList: {
			"PRIMARY discovery tool for non-ground-truth metrics. Searches by technology keyword (substring match).",
			"Input: (required) single technology keyword (e.g., 'redis', 'kafka', 'pg', 'jvm', 'rabbitmq').",
			"Output: List of matching metric names (capped at 30 families). Use your domain knowledge to pick the best match.",
			"Call ONCE. Do NOT retry with different keywords — if no results, report metric not found.",
		},
		tools.ToolMetricsLabelsList: {
			"Fetches available labels for a specific metric. Use ONLY when you need label names to construct a filter.",
			"Input: (required) exact metric name.",
			"Output: List of label names for that metric.",
			"If this tool fails, proceed with the metric name and {__CLUSTER__} labels.",
		},
	}

	prompt := core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Prometheus Querying within Kubernetes environments",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "The output MUST be a valid JSON object with the format: {\"promql\": \"your_query_here\"} or for multiple queries: {\"promql\": [\"query1\", \"query2\", ...]}. CRITICAL REQUIREMENT: Your PromQL query MUST ONLY use functions explicitly listed in the **CRITICAL - FUNCTION RESTRICTION** section. Any other function will cause execution failure.",
		Rag: core.NBAgentPromptRag{
			Module:      "prometheus",
			Format:      core.NBAgentPromptRagFormatJson,
			QuestionKey: "question",
			AnswerKey:   "answer",
			Records:     3,
		},
		ToolUsage: toolUsage,
	}

	return prompt
}

func (p *PromqlAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.MetricsListTool{Provider: "prometheus"},
		tools.ListMetricsLabelsTool{Provider: "prometheus"},
	}
}

func (l *PromqlAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps promql_query iterations, configurable via llm_server_agent_promql_max_iterations (default 4).
// Workflow: (1) optional metrics_list, (2) optional metrics_labels_list, (3) construct answer.
// More than 4 iterations means the agent is stuck in a retry loop.
func (l *PromqlAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentPromqlMaxIterations; v > 0 {
		return v
	}
	return 4
}

func (l *PromqlAgent) CritiqueEnabled() bool {
	return false
}

// UpdateExecutorLlmResponse validates the generated PromQL query before it is returned to
// the parent agent. If the LLM produced a query using a non-standard function (e.g.
// ts_of_max_over_time), parser.ParseExpr will fail and we replace the output with a clear
// error message. This prevents the parent prometheus agent from executing an invalid query,
// retrying indefinitely, and eventually hallucinating tool names like generate_promql.
func (p *PromqlAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	if finished == nil || finished.Data == "" {
		return actions, finished, err
	}

	var result struct {
		Promql any `json:"promql"`
	}
	if jsonErr := json.Unmarshal([]byte(finished.Data), &result); jsonErr != nil {
		// Not a JSON object we recognise — pass through unchanged.
		return actions, finished, err
	}

	var queries []string
	switch v := result.Promql.(type) {
	case string:
		if v != "" {
			queries = []string{v}
		}
	case []any:
		for _, q := range v {
			if s, ok := q.(string); ok && s != "" {
				queries = append(queries, s)
			}
		}
	}

	for _, q := range queries {
		// Strip the __CLUSTER__ placeholder (it has no value and is not valid PromQL syntax)
		// and split semicolon-joined multi-queries before parsing.
		cleanQ := strings.ReplaceAll(q, "__CLUSTER__", "")
		for _, part := range strings.Split(cleanQ, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, parseErr := parser.ParseExpr(part); parseErr != nil {
				// Surface the parse error clearly so the parent agent stops retrying.
				errMsg := fmt.Sprintf("promql_query: generated query is invalid (unsupported function or syntax): %s. Original query: %s", parseErr.Error(), q)
				finished.Data = errMsg
				return nil, finished, nil
			}
		}
	}

	return actions, finished, err
}

// UpdateToolResponseForPlanner caps metrics_list responses before they are
// injected into the ReAct scratchpad. Without this, a broad keyword like
// "deployment" returns 30 metric families with full label metadata, growing
// the context by 20-150K tokens per call.
func (p *PromqlAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	maxPromqlToolResponseChars := config.Config.LlmServerAgentPromqlMaxToolRespChars
	if maxPromqlToolResponseChars <= 0 {
		maxPromqlToolResponseChars = 3000
	}
	if len(toolResponse) > maxPromqlToolResponseChars {
		return toolResponse[:maxPromqlToolResponseChars] + "\n... (truncated - use a more specific keyword to narrow results)"
	}
	return toolResponse
}

// buildPromQLPattern constructs the regex pattern using all available Prometheus functions
func buildPromQLPattern() *regexp.Regexp {
	functions := getAvailablePrometheusFunctions()
	functionList := strings.Join(functions, "|")
	patternTemplate := `(?i)\b(?:%s)\s*\((?:[^)(]+|\((?:[^)(]+|\([^)(]*\))*\))*\)(?:\s+(?:by|without)\s+\([^)]+\))?|[a-zA-Z_:][a-zA-Z0-9_:]*\{[^}]+\}|[a-zA-Z_:][a-zA-Z0-9_:]*\[[^\]]+\]`
	return regexp.MustCompile(fmt.Sprintf(patternTemplate, functionList))
}

// Initialize the promqlPattern once
var promqlPattern = buildPromQLPattern()

func ExtractPromQLExprs(input string) []string {
	var queries []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		matches := promqlPattern.FindAllString(line, -1)
		for _, m := range matches {
			m = strings.TrimSpace(m)
			if len(m) > 0 {
				queries = append(queries, m)
			}
		}
	}
	return queries
}

func getAvailablePrometheusFunctions() []string {
	var aggregators = []string{
		"sum",
		"min",
		"max",
		"avg",
		"group",
		"stddev",
		"stdvar",
		"count",
		"count_values",
		"bottomk",
		"topk",
		"quantile",
	}
	// Explicitly include critical functions to ensure they are present
	var criticalFunctions = []string{
		"histogram_quantile",
		"rate",
		"irate",
		"increase",
		"delta",
		"idelta",
		"label_replace",
		"vector",
		"scalar",
		"abs",
		"ceil",
		"floor",
		"round",
		"exp",
		"sqrt",
		"ln",
		"log2",
		"log10",
	}

	functions := parser.Functions
	// Use a map to deduplicate names
	nameSet := make(map[string]struct{})
	for name := range functions {
		nameSet[name] = struct{}{}
	}
	for _, agg := range aggregators {
		nameSet[agg] = struct{}{}
	}
	for _, fn := range criticalFunctions {
		nameSet[fn] = struct{}{}
	}

	// Convert map keys to slice and sort
	var names []string
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func GetPromQlSimilarity(query string, answer string, agentId string, accountId string, conversationId string, messageId string, userId string) int {
	prompt := `You are an expert in Prometheus and Kubernetes monitoring. Your task is to evaluate the correctness of a given PromQL query based on a specific question. Follow a structured approach to ensure consistent evaluation.
Evaluation Criteria:
For each input, analyze the question and the provided answer (PromQL query). Then, determine whether the answer is correct or incorrect based on the following rules:

1. Syntax Validity: The answer must be a valid PromQL query.  
2. Relevance: The query must be relevant to the question and align with its intent.  
3. Expected Results: The query should return the expected results based on the given question.  

Response Format:
- Respond with '1' if the answer meets all the above criteria.  
- Respond with '0' if the answer fails any of the criteria.
  
Input Format:
    question: %v
	answer: %v
Output Format:
	Output: %v
`
	messagecontent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(prompt, query, answer, "%d")),
	}
	response, err := core.GenerateAndTrackLLMContent(&security.RequestContext{}, userId, accountId, conversationId, messageId, agentId, false, messagecontent, true)
	if err != nil {
		return 0
	}
	if len(response.Choices) > 0 {
		content := response.Choices[0].Content
		re := regexp.MustCompile(`(?i)(?:\*\*)?(output|response)(?:\*\*)?[:\s]+\s*(\d+)`)
		match := re.FindStringSubmatch(content)
		if len(match) > 0 {
			intValue, err := strconv.Atoi(match[2])
			if err != nil {
				return 0
			}
			return intValue
		} else {
			return 0
		}
	}

	return 0
}

// Constants for Prometheus metric descriptions and examples to form string of ground truth metrics
const (
	containerNameDesc    = "<Container name>"
	podNameDesc          = "<Pod name>"
	namespaceDesc        = "<Kubernetes namespace>"
	nodeNameDesc         = "<Kubernetes node name>"
	examplePodName       = "<nginx-deployment-1234>"
	exampleNamespace     = "<default>"
	exampleContainerName = "<nginx>"
	exampleNodeName      = "<worker-node-1>"
)

type PrometheusMetricGroundTruth struct {
	MetricName     string            `json:"metric_name"`
	Description    string            `json:"description"`
	Type           string            `json:"type"` // counter, gauge, histogram, summary
	Labels         []PrometheusLabel `json:"labels"`
	Usage          string            `json:"usage"`
	RelatedMetrics []string          `json:"related_metrics,omitempty"`
}
type PrometheusLabel struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	PossibleValues []string `json:"possible_values,omitempty"`
	ValueType      string   `json:"value_type"` // enum, dynamic, regex_pattern
	Example        string   `json:"example,omitempty"`
}

// Base ground truth metrics for Prometheus to troubleshoot Kubernetes environments
func getPrometheusGroundTruthMetrics() []PrometheusMetricGroundTruth {
	return []PrometheusMetricGroundTruth{
		{
			MetricName:  "container_cpu_usage_seconds_total",
			Description: "Cumulative CPU time consumed by container in seconds",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "cpu", Description: "CPU core identifier", ValueType: "dynamic", Example: "cpu01"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "image", Description: "Container image name", ValueType: "dynamic", Example: "nginx:1.21"},
				{Name: "instance", Description: "Node instance (used instead of node)", ValueType: "dynamic", Example: "worker-node-1:10250"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kubelet"},
				{Name: "metrics_path", Description: "Metrics collection path", ValueType: "dynamic", Example: "/metrics/cadvisor"},
				{Name: "name", Description: "Container name (alternative to container)", ValueType: "dynamic", Example: exampleContainerName},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kubelet"},
			},
			Usage:          "Use rate() function to get CPU usage rate. For CPU percentage, divide by CPU limits or requests. IMPORTANT: Use 'instance' label for node filtering, not 'node' label.",
			RelatedMetrics: []string{"container_spec_cpu_shares", "container_spec_cpu_quota"},
		},
		{
			MetricName:  "container_memory_usage_bytes",
			Description: "Current memory usage in bytes including all memory regardless of when it was accessed",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "image", Description: "Container image name", ValueType: "dynamic", Example: "nginx:1.21"},
				{Name: "instance", Description: "Node instance (used instead of node)", ValueType: "dynamic", Example: "worker-node-1:10250"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kubelet"},
				{Name: "metrics_path", Description: "Metrics collection path", ValueType: "dynamic", Example: "/metrics/cadvisor"},
				{Name: "name", Description: "Container name (alternative to container)", ValueType: "dynamic", Example: exampleContainerName},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kubelet"},
			},
			Usage:          "Direct gauge metric showing current memory usage. Convert to MB/GB by dividing by 1024^2 or 1024^3. IMPORTANT: Use 'instance' label for node filtering, not 'node' label.",
			RelatedMetrics: []string{"container_spec_memory_limit_bytes", "container_memory_working_set_bytes"},
		},
		{
			MetricName:  "kube_pod_status_phase",
			Description: "Current phase of the pod (Pending=0, Running=1, Succeeded=2, Failed=3, Unknown=4)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "phase", Description: "Pod phase", ValueType: "enum", PossibleValues: []string{"Pending", "Running", "Succeeded", "Failed", "Unknown"}},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "uid", Description: "Pod UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "Filter by phase label to get pods in specific states. Value represents the phase numerically.",
			RelatedMetrics: []string{"kube_pod_info", "kube_pod_status_ready"},
		},
		{
			MetricName:  "container_network_receive_bytes_total",
			Description: "Cumulative count of bytes received by the container network interface",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "image", Description: "Container image name", ValueType: "dynamic", Example: "nginx:1.21"},
				{Name: "instance", Description: "Node instance (used instead of node)", ValueType: "dynamic", Example: "worker-node-1:10250"},
				{Name: "interface", Description: "Network interface name", ValueType: "dynamic", Example: "eth0"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kubelet"},
				{Name: "metrics_path", Description: "Metrics collection path", ValueType: "dynamic", Example: "/metrics/cadvisor"},
				{Name: "name", Description: "Container name (alternative to container)", ValueType: "dynamic", Example: exampleContainerName},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kubelet"},
			},
			Usage:          "Use rate() to get network receive rate in bytes/sec. Useful for monitoring network traffic. IMPORTANT: Use 'instance' label for node filtering, not 'node' label.",
			RelatedMetrics: []string{"container_network_transmit_bytes_total", "container_network_receive_packets_total"},
		},
		{
			MetricName:  "container_http_requests_total",
			Description: "Total HTTP requests processed by containers (service mesh metric)",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "actual_destination", Description: "Actual destination IP:port", ValueType: "dynamic", Example: "172.31.93.60:80"},
				{Name: "actual_destination_workload_kind", Description: "Actual destination workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "actual_destination_workload_name", Description: "Actual destination workload name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "actual_destination_workload_namespace", Description: "Actual destination workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "container_id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "destination", Description: "Destination IP:port", ValueType: "dynamic", Example: "172.31.93.60:80"},
				{Name: "destination_workload_kind", Description: "Destination workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "destination_workload_name", Description: "Target workload name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "destination_workload_namespace", Description: "Target workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Source instance", ValueType: "dynamic", Example: "ip-172-31-4-188.ec2.internal"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "nudgebee-node-agent"},
				{Name: "le", Description: "Histogram bucket upper bound (for _bucket metrics)", ValueType: "dynamic", Example: "0.1"},
				{Name: "machine_id", Description: "Machine identifier", ValueType: "dynamic", Example: "machine-abc123"},
				{Name: "method", Description: "HTTP method", ValueType: "enum", PossibleValues: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "path", Description: "HTTP request path", ValueType: "dynamic", Example: "/api/users"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "src_kind", Description: "Source workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "src_workload_name", Description: "Source workload name", ValueType: "dynamic", Example: "frontend"},
				{Name: "src_workload_namespace", Description: "Source workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "status", Description: "HTTP response status code", ValueType: "dynamic", Example: "200"},
				{Name: "system_uuid", Description: "System UUID identifier", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "IMPORTANT: This is an eBPF metric where `pod` and `namespace` refer to the OBSERVER agent, NOT the target workload. Use `destination_workload_name` and `destination_workload_namespace` to filter by target. Use `status=~\"5..\"` for error rate. Use rate() for RPS.",
			RelatedMetrics: []string{"container_http_requests_duration_seconds_total_sum", "prometheus_http_requests_total"},
		},
		{
			MetricName:  "container_http_requests_duration_seconds_total_sum",
			Description: "Total time spent on HTTP requests by containers (service mesh metric)",
			Type:        "histogram",
			Labels: []PrometheusLabel{
				{Name: "actual_destination", Description: "Actual destination IP:port", ValueType: "dynamic", Example: "172.31.93.60:80"},
				{Name: "actual_destination_workload_kind", Description: "Actual destination workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "actual_destination_workload_name", Description: "Actual destination workload name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "actual_destination_workload_namespace", Description: "Actual destination workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "container_id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "destination", Description: "Destination IP:port", ValueType: "dynamic", Example: "172.31.93.60:80"},
				{Name: "destination_workload_kind", Description: "Destination workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "destination_workload_name", Description: "Target workload name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "destination_workload_namespace", Description: "Target workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Source instance", ValueType: "dynamic", Example: "ip-172-31-4-188.ec2.internal"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "nudgebee-node-agent"},
				{Name: "le", Description: "Histogram bucket upper bound", ValueType: "dynamic", Example: "0.1"},
				{Name: "machine_id", Description: "Machine identifier", ValueType: "dynamic", Example: "machine-abc123"},
				{Name: "method", Description: "HTTP method", ValueType: "enum", PossibleValues: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "path", Description: "HTTP request path", ValueType: "dynamic", Example: "/api/users"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "src_kind", Description: "Source workload kind", ValueType: "enum", PossibleValues: []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "Pod"}},
				{Name: "src_workload_name", Description: "Source workload name", ValueType: "dynamic", Example: "frontend"},
				{Name: "src_workload_namespace", Description: "Source workload namespace", ValueType: "dynamic", Example: exampleNamespace},
				{Name: "status", Description: "HTTP response status code", ValueType: "dynamic", Example: "200"},
				{Name: "system_uuid", Description: "System UUID identifier", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "IMPORTANT: This is an eBPF metric where `pod` and `namespace` refer to the OBSERVER agent. Use `destination_workload_name` and `destination_workload_namespace` to filter by target. Use histogram_quantile() with _bucket suffix for percentiles.",
			RelatedMetrics: []string{"container_http_requests_total"},
		},
		{
			MetricName:  "node_cpu_seconds_total",
			Description: "Seconds the CPUs spent in each mode (idle, system, user, etc.)",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "cpu", Description: "CPU core identifier", ValueType: "dynamic", Example: "0"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "node-1:9100"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "node-exporter"},
				{Name: "mode", Description: "CPU mode", ValueType: "enum", PossibleValues: []string{"idle", "iowait", "irq", "nice", "softirq", "steal", "system", "user"}},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "node-exporter"},
			},
			Usage:          "Calculate CPU utilization by using rate() and excluding idle mode. Often used with irate() for instant rates.",
			RelatedMetrics: []string{"node_load1", "node_load5", "node_load15"},
		},
		{
			MetricName:  "kube_deployment_status_replicas",
			Description: "Number of replicas for a deployment",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "deployment", Description: "Deployment name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Compare with available/ready replicas to monitor deployment health and scaling status.",
			RelatedMetrics: []string{"kube_deployment_status_replicas_available", "kube_deployment_status_replicas_ready"},
		},
		{
			MetricName:  "kube_pod_container_status_restarts_total",
			Description: "Number of restarts for a container in a pod",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "uid", Description: "Pod UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "High restart counts indicate container crashes or failures. Use rate() to get restart rate over time.",
			RelatedMetrics: []string{"kube_pod_container_status_waiting_reason", "kube_pod_container_status_terminated_reason"},
		},
		{
			MetricName:  "kube_pod_container_status_waiting_reason",
			Description: "Pod container waiting reason (1 if waiting for specified reason, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "reason", Description: "Waiting reason", ValueType: "enum", PossibleValues: []string{"ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "ContainerCreating", "PodInitializing"}},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "uid", Description: "Pod UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "Identifies why containers are stuck in waiting state. Filter by reason to find specific issues.",
			RelatedMetrics: []string{"kube_pod_container_status_restarts_total", "kube_pod_status_phase"},
		},
		{
			MetricName:  "kube_node_status_condition",
			Description: "Node condition status (1 if condition is true, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "condition", Description: "Node condition", ValueType: "enum", PossibleValues: []string{"Ready", "OutOfDisk", "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"}},
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "status", Description: "Condition status", ValueType: "enum", PossibleValues: []string{"true", "false", "unknown"}},
			},
			Usage:          "Monitor node health conditions. Ready=false or pressure conditions=true indicate node issues.",
			RelatedMetrics: []string{"kube_node_info", "node_memory_MemAvailable_bytes", "node_filesystem_avail_bytes"},
		},
		{
			MetricName:  "kube_pod_status_ready",
			Description: "Pod ready status (1 if ready, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "condition", Description: "Ready condition", ValueType: "enum", PossibleValues: []string{"true", "false", "unknown"}},
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "uid", Description: "Pod UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "Identify pods that are not ready. Essential for troubleshooting pod startup issues.",
			RelatedMetrics: []string{"kube_pod_status_phase", "kube_pod_container_status_waiting_reason"},
		},
		{
			MetricName:  "kube_persistentvolumeclaim_status_phase",
			Description: "PVC status phase (1 if in specified phase, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "persistentvolumeclaim", Description: "PVC name", ValueType: "dynamic", Example: "data-pvc"},
				{Name: "phase", Description: "PVC phase", ValueType: "enum", PossibleValues: []string{"Pending", "Bound", "Lost"}},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor PVC binding issues. Pending PVCs often indicate storage problems.",
			RelatedMetrics: []string{"kube_persistentvolume_status_phase", "kube_pod_spec_volumes_persistentvolumeclaim_info"},
		},
		{
			MetricName:  "kube_deployment_status_replicas_unavailable",
			Description: "Number of unavailable replicas for a deployment",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "deployment", Description: "Deployment name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "High unavailable replica count indicates deployment health issues or resource constraints.",
			RelatedMetrics: []string{"kube_deployment_status_replicas", "kube_deployment_status_replicas_available"},
		},
		{
			MetricName:  "kube_pod_container_status_terminated_reason",
			Description: "Pod container termination reason (1 if terminated for specified reason, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "reason", Description: "Termination reason", ValueType: "enum", PossibleValues: []string{"Completed", "Error", "OOMKilled", "ContainerCannotRun", "DeadlineExceeded"}},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "uid", Description: "Pod UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "Identify why containers terminated. OOMKilled indicates memory issues, Error suggests application problems.",
			RelatedMetrics: []string{"kube_pod_container_status_restarts_total", "container_memory_usage_bytes"},
		},
		{
			MetricName:  "kube_node_spec_unschedulable",
			Description: "Node unschedulable status (1 if unschedulable, 0 if schedulable)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Identify nodes that are cordoned and cannot schedule new pods.",
			RelatedMetrics: []string{"kube_node_status_condition", "kube_pod_info"},
		},
		{
			MetricName:  "node_memory_MemAvailable_bytes",
			Description: "Available memory in bytes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "node-1:9100"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "node-exporter"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "node-exporter"},
			},
			Usage:          "Monitor available memory on nodes. Low values indicate memory pressure.",
			RelatedMetrics: []string{"node_memory_MemTotal_bytes", "kube_node_status_condition"},
		},
		{
			MetricName:  "node_filesystem_avail_bytes",
			Description: "Available filesystem space in bytes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "device", Description: "Device name", ValueType: "dynamic", Example: "/dev/sda1"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "fstype", Description: "Filesystem type", ValueType: "enum", PossibleValues: []string{"ext4", "xfs", "tmpfs", "overlay", "btrfs", "zfs"}},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "node-1:9100"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "node-exporter"},
				{Name: "mountpoint", Description: "Mount point", ValueType: "dynamic", Example: "/var/lib/docker"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "node-exporter"},
			},
			Usage:          "Monitor available disk space. Critical for detecting disk pressure conditions.",
			RelatedMetrics: []string{"node_filesystem_size_bytes", "kube_node_status_condition"},
		},
		{
			MetricName:  "kube_service_status_load_balancer_ingress",
			Description: "Service load balancer ingress status (1 if ingress exists, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "hostname", Description: "Load balancer hostname", ValueType: "dynamic", Example: "lb.example.com"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Service name", ValueType: "dynamic", Example: "nginx-service"},
				{Name: "uid", Description: "Service UID", ValueType: "dynamic", Example: "12345678-1234-5678-9abc-123456789012"},
			},
			Usage:          "Monitor LoadBalancer service ingress availability for network troubleshooting.",
			RelatedMetrics: []string{"kube_service_info", "kube_endpoints_ready"},
		},
		{
			MetricName:  "kube_daemonset_status_number_unavailable",
			Description: "Number of unavailable nodes for a DaemonSet",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "daemonset", Description: "DaemonSet name", ValueType: "dynamic", Example: "fluent-bit"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: "kube-system"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor DaemonSet deployment issues across cluster nodes.",
			RelatedMetrics: []string{"kube_daemonset_status_desired_scheduled", "kube_node_status_condition"},
		},
		{
			MetricName:  "kube_statefulset_status_replicas_ready",
			Description: "Number of ready replicas for a StatefulSet",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "statefulset", Description: "StatefulSet name", ValueType: "dynamic", Example: "mysql"},
			},
			Usage:          "Monitor StatefulSet replica readiness for stateful application troubleshooting.",
			RelatedMetrics: []string{"kube_statefulset_replicas", "kube_pod_status_ready"},
		},
		{
			MetricName:  "kube_job_status_failed",
			Description: "Job failure status (1 if failed, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "job_name", Description: "Job name", ValueType: "dynamic", Example: "backup-job"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "reason", Description: "Job failure reason", ValueType: "enum", PossibleValues: []string{"BackoffLimitExceeded", "DeadlineExceeded", "Evicted", "Failed"}},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor job execution failures for batch workload troubleshooting.",
			RelatedMetrics: []string{"kube_job_status_succeeded", "kube_pod_container_status_terminated_reason"},
		},
		{
			MetricName:  "container_network_transmit_bytes_total",
			Description: "Cumulative count of bytes transmitted by the container network interface",
			Type:        "counter",
			Labels: []PrometheusLabel{
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "id", Description: "Container ID", ValueType: "dynamic", Example: "/kubepods/besteffort/pod12345/abc123"},
				{Name: "image", Description: "Container image name", ValueType: "dynamic", Example: "nginx:1.21"},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "worker-node-1:10250"},
				{Name: "interface", Description: "Network interface name", ValueType: "dynamic", Example: "eth0"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kubelet"},
				{Name: "metrics_path", Description: "Metrics collection path", ValueType: "dynamic", Example: "/metrics/cadvisor"},
				{Name: "name", Description: "Container name (alternative to container)", ValueType: "dynamic", Example: exampleContainerName},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "node", Description: nodeNameDesc, ValueType: "dynamic", Example: exampleNodeName},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kubelet"},
			},
			Usage:          "Use rate() to get network transmit rate in bytes/sec. Useful for monitoring outbound network traffic.",
			RelatedMetrics: []string{"container_network_receive_bytes_total", "container_network_transmit_packets_total"},
		},
		{
			MetricName:  "kube_deployment_status_replicas_available",
			Description: "Number of available replicas for a deployment",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "deployment", Description: "Deployment name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Compare with desired replicas to monitor deployment availability.",
			RelatedMetrics: []string{"kube_deployment_status_replicas", "kube_deployment_status_replicas_ready"},
		},
		{
			MetricName:  "kube_deployment_status_replicas_ready",
			Description: "Number of ready replicas for a deployment",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "deployment", Description: "Deployment name", ValueType: "dynamic", Example: "nginx-deployment"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor ready vs desired replicas to identify deployment readiness issues.",
			RelatedMetrics: []string{"kube_deployment_status_replicas", "kube_pod_status_ready"},
		},
		{
			MetricName:  "node_memory_MemTotal_bytes",
			Description: "Total memory available on the node in bytes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "node-1:9100"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "node-exporter"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "node-exporter"},
			},
			Usage:          "Calculate memory utilization percentage by comparing with MemAvailable_bytes.",
			RelatedMetrics: []string{"node_memory_MemAvailable_bytes", "kube_node_status_condition"},
		},
		{
			MetricName:  "node_load1",
			Description: "1-minute load average",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "cpu", Description: "CPU core identifier", ValueType: "dynamic", Example: "0"},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "https-metrics"},
				{Name: "instance", Description: "Node instance", ValueType: "dynamic", Example: "node-1:9100"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "node-exporter"},
				{Name: "mode", Description: "CPU mode", ValueType: "enum", PossibleValues: []string{"idle", "iowait", "irq", "nice", "softirq", "steal", "system", "user"}},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "node-exporter"},
			},
			Usage:          "Monitor system load. Values > CPU count indicate system overload.",
			RelatedMetrics: []string{"node_load5", "node_load15", "node_cpu_seconds_total"},
		},
		{
			MetricName:  "kube_replicaset_status_replicas",
			Description: "Number of replicas for a ReplicaSet",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "replicaset", Description: "ReplicaSet name", ValueType: "dynamic", Example: "nginx-deployment-abc123"},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor ReplicaSet replica counts for detailed deployment analysis.",
			RelatedMetrics: []string{"kube_replicaset_status_ready_replicas", "kube_deployment_status_replicas"},
		},
		{
			MetricName:  "kube_persistentvolume_status_phase",
			Description: "PersistentVolume status phase (1 if in specified phase, 0 otherwise)",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "persistentvolume", Description: "PV name", ValueType: "dynamic", Example: "pv-aws-ebs-001"},
				{Name: "phase", Description: "PV phase", ValueType: "enum", PossibleValues: []string{"Pending", "Available", "Bound", "Released", "Failed"}},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor PV lifecycle phases. Available PVs can be bound, Released PVs need cleanup.",
			RelatedMetrics: []string{"kube_persistentvolumeclaim_status_phase", "kube_persistentvolume_capacity_bytes"},
		},
		{
			MetricName:  "kube_persistentvolume_capacity_bytes",
			Description: "PersistentVolume capacity in bytes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "persistentvolume", Description: "PV name", ValueType: "dynamic", Example: "pv-aws-ebs-001"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor PV storage capacity for capacity planning and allocation tracking.",
			RelatedMetrics: []string{"kube_persistentvolumeclaim_resource_requests_storage_bytes", "kube_persistentvolume_status_phase"},
		},
		{
			MetricName:  "kube_persistentvolumeclaim_resource_requests_storage_bytes",
			Description: "PVC storage request in bytes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "persistentvolumeclaim", Description: "PVC name", ValueType: "dynamic", Example: "data-pvc"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
			},
			Usage:          "Monitor PVC storage requests for capacity planning and quota management.",
			RelatedMetrics: []string{"kube_persistentvolume_capacity_bytes", "kube_persistentvolumeclaim_status_phase"},
		},
		{
			MetricName:  "kube_persistentvolumeclaim_info",
			Description: "PVC information including storage class and access modes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "container", Description: containerNameDesc, ValueType: "dynamic", Example: exampleContainerName},
				{Name: "endpoint", Description: "Metrics endpoint", ValueType: "dynamic", Example: "http"},
				{Name: "instance", Description: "Kubernetes API server instance", ValueType: "dynamic", Example: "kube-state-metrics:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: namespaceDesc, ValueType: "dynamic", Example: exampleNamespace},
				{Name: "persistentvolumeclaim", Description: "PVC name", ValueType: "dynamic", Example: "data-pvc"},
				{Name: "pod", Description: podNameDesc, ValueType: "dynamic", Example: examplePodName},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "storageclass", Description: "Storage class name", ValueType: "dynamic", Example: "gp2"},
				{Name: "volumename", Description: "Bound PV name", ValueType: "dynamic", Example: "pv-aws-ebs-001"},
			},
			Usage:          "Get detailed PVC information including storage class and bound PV for troubleshooting storage issues.",
			RelatedMetrics: []string{"kube_persistentvolumeclaim_status_phase", "kube_persistentvolume_info"},
		},
		{
			MetricName:  "kube_persistentvolume_info",
			Description: "PV information including storage class, capacity, and access modes",
			Type:        "gauge",
			Labels: []PrometheusLabel{
				{Name: "csi_driver", Description: "CSI driver name", ValueType: "dynamic", Example: "ebs.csi.aws.com"},
				{Name: "csi_volume_handle", Description: "CSI volume handle", ValueType: "dynamic", Example: "vol-0123456789abcdef0"},
				{Name: "ebs_volume_id", Description: "AWS EBS volume ID", ValueType: "dynamic", Example: "vol-0123456789abcdef0"},
				{Name: "endpoint", Description: "Service endpoint", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "fs_type", Description: "Filesystem type", ValueType: "enum", Example: "ext4"},
				{Name: "instance", Description: "Instance identifier", ValueType: "dynamic", Example: "10.0.1.100:8080"},
				{Name: "job", Description: "Prometheus job name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "namespace", Description: "Kubernetes namespace", ValueType: "dynamic", Example: "kube-system"},
				{Name: "persistentvolume", Description: "PV name", ValueType: "dynamic", Example: "pvc-abcd1234-efgh-5678-ijkl-mnop90qrstuv"},
				{Name: "service", Description: "Kubernetes service name", ValueType: "dynamic", Example: "kube-state-metrics"},
				{Name: "storageclass", Description: "Storage class name", ValueType: "dynamic", Example: "gp3"},
				{Name: "uid", Description: "PV unique identifier", ValueType: "dynamic", Example: "12345678-abcd-efgh-ijkl-1234567890ab"},
			},
			Usage:          "Get detailed PV information including CSI driver details, cloud provider disk information, and infrastructure context for storage troubleshooting.",
			RelatedMetrics: []string{"kube_persistentvolume_status_phase", "kube_persistentvolumeclaim_info"},
		},
	}
}

func buildGroundTruthConstraints(metrics []PrometheusMetricGroundTruth) []string {
	var constraints []string

	if len(metrics) == 0 {
		return constraints
	}

	constraints = append(constraints, "Available Ground Truth Metrics:")
	constraints = append(constraints, buildMetricConstraints(metrics)...)

	return constraints
}

func buildMetricConstraints(metrics []PrometheusMetricGroundTruth) []string {
	var constraints []string

	for _, metric := range metrics {
		constraintText := fmt.Sprintf("- %s (%s): %s", metric.MetricName, metric.Type, metric.Description)

		if metric.Usage != "" {
			constraintText += fmt.Sprintf(" | Usage: %s", metric.Usage)
		}

		constraintText += buildLabelConstraintText(metric.Labels)
		constraints = append(constraints, constraintText)
	}

	return constraints
}

func buildLabelConstraintText(labels []PrometheusLabel) string {
	if len(labels) == 0 {
		return ""
	}

	var labelNames []string
	for _, label := range labels {
		if label.ValueType == "enum" && len(label.PossibleValues) > 0 {
			labelNames = append(labelNames, fmt.Sprintf("%s(%s)", label.Name, strings.Join(label.PossibleValues, "|")))
		} else {
			labelNames = append(labelNames, label.Name)
		}
	}

	return fmt.Sprintf(" | Labels: %s", strings.Join(labelNames, ", "))
}

func filterMetricsByQuery(query string, metrics []PrometheusMetricGroundTruth) []PrometheusMetricGroundTruth {
	query = strings.ToLower(query)
	var filtered []PrometheusMetricGroundTruth

	// Keywords mapping to metric name substrings
	keywords := map[string][]string{
		"cpu":         {"cpu"},
		"memory":      {"memory", "mem"},
		"ram":         {"memory", "mem"},
		"network":     {"network", "http", "traffic"},
		"http":        {"http", "request"},
		"request":     {"http", "request"},
		"latency":     {"duration", "seconds", "latency"},
		"duration":    {"duration", "seconds"},
		"time":        {"duration", "seconds", "time"},
		"response":    {"duration", "seconds", "http"},
		"endpoint":    {"http", "request", "path"},
		"api":         {"http", "request"},
		"average":     {"avg", "sum", "count"},
		"mean":        {"avg", "sum", "count"},
		"median":      {"quantile", "bucket"},
		"p99":         {"quantile", "bucket"},
		"p95":         {"quantile", "bucket"},
		"p50":         {"quantile", "bucket"},
		"disk":        {"disk", "filesystem"},
		"storage":     {"disk", "filesystem", "volume", "pvc"},
		"volume":      {"volume", "pvc", "pv"},
		"pvc":         {"volume", "pvc"},
		"deployment":  {"deployment", "replica"},
		"statefulset": {"statefulset"},
		"daemonset":   {"daemonset"},
		"job":         {"job"},
		"node":        {"node"},
		"pod":         {"pod", "container"},
	}

	matchedSubstrings := make(map[string]bool)
	anyKeywordMatched := false

	for kw, substrings := range keywords {
		if strings.Contains(query, kw) {
			anyKeywordMatched = true
			for _, sub := range substrings {
				matchedSubstrings[sub] = true
			}
		}
	}

	// If no specific keywords matched, return empty — the agent has metrics_list
	// tool for discovering custom/unknown metrics.
	// Dumping all 33 ground truth K8s metrics for queries like "redis memory" or
	// "kafka consumer lag" wastes ~30K tokens and causes excessive ReAct iterations.
	if !anyKeywordMatched {
		return nil
	}

	for _, m := range metrics {
		name := strings.ToLower(m.MetricName)
		include := false
		for sub := range matchedSubstrings {
			if strings.Contains(name, sub) {
				include = true
				break
			}
		}
		if include {
			filtered = append(filtered, m)
		}
	}

	// Fallback: if filtering resulted in 0 metrics despite keywords, return all
	if len(filtered) == 0 {
		return metrics
	}

	return filtered
}
