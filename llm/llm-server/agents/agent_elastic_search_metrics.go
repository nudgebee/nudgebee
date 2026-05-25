package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"strings"
)

const ElasticSearchMetricsAgentName = "elastic_search_metrics"

func init() {
	toolDescription := `Elasticsearch/Opensearch Metrics Expert. Translates natural language questions into Elasticsearch aggregation queries and executes them against metric indices (e.g. metrics-*, metricbeat-*) to retrieve, analyze, and summarize CPU, memory, network, and custom metrics. Use when the configured metrics provider is Elasticsearch or Opensearch.`
	toolInput := "Provide a question in natural language to retrieve, analyze, or summarize metrics from Elasticsearch."
	toolOutput := "Returns metric data and summaries derived from Elasticsearch aggregation responses."

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(ElasticSearchMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return ElasticSearchMetricsAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

type ElasticSearchMetricsAgent struct {
	accountId string
}

func (e ElasticSearchMetricsAgent) GetName() string {
	return ElasticSearchMetricsAgentName
}

func (e ElasticSearchMetricsAgent) GetNameAliases() []string {
	return []string{"Elastic Search Metrics", "Opensearch Metrics"}
}

func (e ElasticSearchMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes metrics from Elasticsearch/Opensearch using aggregation DSL queries.`
}

func (e ElasticSearchMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	indexCfg := utils.GetESAccountMetricsIndexConfig(e.accountId)
	metricPatterns := []string{"metrics-*", "metricbeat-*", "metrix-*"}

	// Add configured indices that aren't already in our common patterns, filtering out known log patterns.
	availableIndices := append([]string{}, metricPatterns...)
	if indexCfg.DefaultIndex != "" && !isLogIndex(indexCfg.DefaultIndex) {
		availableIndices = append(availableIndices, indexCfg.DefaultIndex)
	}
	for _, pattern := range indexCfg.Indices {
		if !isLogIndex(pattern) {
			availableIndices = append(availableIndices, pattern)
		}
	}

	instructions := []string{
		"**Your Primary Goal:** Answer user questions about metrics stored in Elasticsearch / Opensearch.",
		"**Your Method:** Construct an Elasticsearch DSL query (JSON) and execute it via `es_metrics_query`. You MUST provide both `index` (the index pattern) and `query` (the DSL body) fields in the tool input.",
		fmt.Sprintf("**Target Indices:** You MUST only query indices likely to contain metrics: `%v`. NEVER query log indices (e.g., `fluentk8-*`, `logs-*`) as they do not contain the required metric fields.", availableIndices),

		// --- Common OTel Metric Templates (Fast Path) ---
		"**Common OTel Metric Templates (Fast Path):**",
		"  If the request is for standard K8s CPU, Memory, Network, or Filesystem metrics, use these known metric names and field mappings directly without discovery. Call `es_metrics_query` immediately.",
		"  **Query field names (ALWAYS use these exact fields):**",
		"    * Metric name: `name.keyword`",
		"    * Namespace: `attributes.resource.attributes.k8s@namespace@name.keyword`",
		"    * Pod: `attributes.resource.attributes.k8s@pod@name.keyword`",
		"    * Container: `attributes.resource.attributes.k8s@container@name.keyword`",
		"    * Node: `attributes.resource.attributes.k8s@node@name.keyword`",
		"    * Value: `value`",
		"    * Timestamp: `time` (NOT `@timestamp`)",
		"  **CPU Metrics (kubeletstats receiver):**",
		"    - Container CPU usage (cores): `container.cpu.usage` | Container CPU time (s): `container.cpu.time`",
		"    - Pod CPU usage (cores): `k8s.pod.cpu.usage` | Pod CPU time (s): `k8s.pod.cpu.time`",
		"    - Node CPU usage (cores): `k8s.node.cpu.usage` | Node CPU time (s): `k8s.node.cpu.time`",
		"  **Memory Metrics (kubeletstats receiver):**",
		"    - Container: `container.memory.usage`, `container.memory.working_set`, `container.memory.rss`, `container.memory.available`, `container.memory.page_faults`, `container.memory.major_page_faults`",
		"    - Pod: `k8s.pod.memory.usage`, `k8s.pod.memory.working_set`, `k8s.pod.memory.rss`, `k8s.pod.memory.available`, `k8s.pod.memory.page_faults`, `k8s.pod.memory.major_page_faults`",
		"    - Node: `k8s.node.memory.usage`, `k8s.node.memory.working_set`, `k8s.node.memory.rss`, `k8s.node.memory.available`, `k8s.node.memory.page_faults`, `k8s.node.memory.major_page_faults`",
		"  **Network Metrics (kubeletstats receiver):**",
		"    - Pod: `k8s.pod.network.io` (bytes), `k8s.pod.network.errors` — filter by `attributes.metric.attributes.direction.keyword`: `receive` or `transmit`",
		"    - Node: `k8s.node.network.io` (bytes), `k8s.node.network.errors`",
		"  **Filesystem Metrics (kubeletstats receiver):**",
		"    - Container: `container.filesystem.usage`, `container.filesystem.capacity`, `container.filesystem.available`",
		"    - Pod: `k8s.pod.filesystem.usage`, `k8s.pod.filesystem.capacity`, `k8s.pod.filesystem.available`",
		"    - Node: `k8s.node.filesystem.usage`, `k8s.node.filesystem.capacity`, `k8s.node.filesystem.available`",
		"  **Volume Metrics (kubeletstats receiver):**",
		"    - `k8s.volume.available`, `k8s.volume.capacity`, `k8s.volume.inodes`, `k8s.volume.inodes.free`, `k8s.volume.inodes.used`",
		"  **K8s Cluster Metrics (k8scluster receiver — resource requests, limits, workload status):**",
		"    - Container resources: `k8s.container.cpu_request`, `k8s.container.cpu_limit`, `k8s.container.memory_request`, `k8s.container.memory_limit`, `k8s.container.storage_request`, `k8s.container.storage_limit`, `k8s.container.ephemeralstorage_request`, `k8s.container.ephemeralstorage_limit`",
		"    - Container status: `k8s.container.restarts`, `k8s.container.ready`",
		"    - Pod: `k8s.pod.phase` (1=Pending, 2=Running, 3=Succeeded, 4=Failed, 5=Unknown)",
		"    - Deployment: `k8s.deployment.desired`, `k8s.deployment.available`",
		"    - StatefulSet: `k8s.statefulset.desired_pods`, `k8s.statefulset.ready_pods`, `k8s.statefulset.current_pods`, `k8s.statefulset.updated_pods`",
		"    - DaemonSet: `k8s.daemonset.desired_scheduled_nodes`, `k8s.daemonset.current_scheduled_nodes`, `k8s.daemonset.ready_nodes`, `k8s.daemonset.misscheduled_nodes`",
		"    - ReplicaSet: `k8s.replicaset.desired`, `k8s.replicaset.available`",
		"    - Job: `k8s.job.active_pods`, `k8s.job.desired_successful_pods`, `k8s.job.failed_pods`, `k8s.job.successful_pods`, `k8s.job.max_parallel_pods`",
		"    - CronJob: `k8s.cronjob.active_jobs`",
		"    - HPA: `k8s.hpa.current_replicas`, `k8s.hpa.desired_replicas`, `k8s.hpa.min_replicas`, `k8s.hpa.max_replicas`",
		"    - Namespace: `k8s.namespace.phase` (1=Active, 0=Terminating)",
		"    - Resource Quota: `k8s.resource_quota.hard_limit`, `k8s.resource_quota.used`",
		"  **Example Fast Path query (CPU usage for a pod):**",
		"    `{\"index\":\"metrics-*\",\"query\":{\"query\":{\"bool\":{\"filter\":[{\"term\":{\"name.keyword\":\"container.cpu.usage\"}},{\"term\":{\"attributes.resource.attributes.k8s@namespace@name.keyword\":\"default\"}},{\"wildcard\":{\"attributes.resource.attributes.k8s@pod@name.keyword\":\"web-server*\"}},{\"range\":{\"time\":{\"gte\":\"now-1h\"}}}]}}}}`",

		// --- Field Discovery (for non-standard metrics) ---
		"**Field Discovery (for custom/non-standard metrics only):**",
		"  If the metric is NOT in the common templates above, discover it:",
		"  - Step 1: Call `metrics_list` with the target index pattern (e.g. 'metrics-*') to discover available metric names.",
		"  - Step 2: Call `es_metrics_query` with a targeted sample query to discover the actual document field names:",
		"    `{\"index\":\"metrics-*\",\"query\":{\"query\":{\"bool\":{\"filter\":[{\"term\":{\"name.keyword\":\"<metric_name_from_step1>\"}},{\"range\":{\"time\":{\"gte\":\"now-1h\"}}}]}},\"size\":1}}`",
		"  - **IMPORTANT:** The sample document's `metric` labels use DIFFERENT names than the actual ES query fields:",
		"    * `__name__` in the response → use `name.keyword` in queries (NEVER use `__name__` or `__name__.keyword` — they are NOT real ES fields)",
		"    * `resource.attributes.k8s@namespace@name` in the response → use `attributes.resource.attributes.k8s@namespace@name.keyword` in queries",
		"    * `resource.attributes.k8s@pod@name` in the response → use `attributes.resource.attributes.k8s@pod@name.keyword` in queries",
		"    * `metric.attributes.*` in the response → use `attributes.metric.attributes.<field>.keyword` in queries",
		"  - You may also call `metrics_labels_list` but note it may return generic fields from non-metric indices.",
		"**Query Patterns (IMPORTANT — do NOT use ES aggregations):**",
		"  - The metrics API processes document hits into time-series (timestamps + values). ES aggregation results (`aggs`) are NOT returned.",
		"  - Use `bool` / `filter` queries to match metric documents. Do NOT use `size: 0` or `aggs` — they will return empty results.",
		"  - Filter by metric name using `name.keyword` (e.g. `{\"term\":{\"name.keyword\":\"k8s.pod.cpu.usage\"}}`).",
		"  - Filter by namespace, pod, or other dimensions. ALWAYS append `.keyword` to text field names for `term` queries.",
		"  - The backend automatically extracts `value` fields and timestamps from matching documents and returns them as time-series.",
		"  - Example filter query: `{\"query\":{\"bool\":{\"filter\":[{\"term\":{\"name.keyword\":\"container.cpu.usage\"}},{\"term\":{\"attributes.resource.attributes.k8s@namespace@name.keyword\":\"nudgebee\"}},{\"range\":{\"time\":{\"gte\":\"now-1h\"}}}]}}}` — note the `attributes.` prefix on resource/metric attribute fields.",
		"**Time Range:** Always include a `range` filter on `time` (NOT `@timestamp`). Default to `now-1h` if the user does not specify a time range.",
		"**Self-Correction (CRITICAL — follow this order exactly when `es_metrics_query` returns empty `payload`):**",
		"  1. **FIRST: Widen the time range.** Change `now-1h` to `now-6h`, then `now-24h`.",
		"  2. SECOND: Remove filters one by one to isolate which filter returns no matches (keep only metric name + time range first).",
		"  3. THIRD: Check that you appended `.keyword` to text fields and used `time` (not `@timestamp`) for the range.",
		"  4. FOURTH: Try a different index pattern.",
		"  - NEVER retry the same query unchanged. Maximum 4 retries total.",
		"**Final Answer:** Summarize the metric values (peak / avg / latest) in a concise SRE-style report. Do NOT return the raw DSL query as the answer.",
	}

	constraints := []string{
		"For standard K8s metrics (CPU, Memory, Network, Filesystem), use the Fast Path templates directly. For custom metrics, you MUST discover field names (via sample document or `metrics_labels_list`) BEFORE writing any query.",
		"You MUST execute every query via `es_metrics_query` before answering.",
		"ALWAYS append `.keyword` to text field names when using them in `term` filters (e.g. `attributes.resource.attributes.k8s@namespace@name.keyword`).",
		"ALWAYS use `time` (NOT `@timestamp`) for the timestamp range filter. Using `@timestamp` will return 0 results.",
		"NEVER use `__name__` or `__name__.keyword` in queries — these are NOT real ES fields. The correct field for metric name is `name.keyword`.",
		"NEVER query log-specific indices (e.g. `fluentk8-*`, `logs-*`) for metric data.",
		"NEVER return the generated JSON DSL query as the final answer — users want metric values, not queries.",
		"NEVER use `size: 0` or `aggs` in queries — the backend does not return aggregation results. Use filter queries instead.",
		"Always include a timestamp `range` filter on the correct timestamp field.",
		"Do not ask the user for clarification — make the best assumption and proceed.",
	}

	toolUsage := map[string][]string{
		tools.ToolESMetricsQuery: {
			"Executes an Elasticsearch DSL query against the metrics API.",
			"Input: A JSON object with `index` (e.g., \"metrics-*\") and `query` (the full DSL body with query filters — NO aggs or size:0).",
			"Output: Metric time-series results with timestamps and values extracted from document hits.",
		},
		tools.ToolMetricsList: {
			"Lists available metric names in an Elasticsearch index.",
			"Input: (required) index pattern (e.g., 'metrics-*', 'metricbeat-*').",
			"Output: List of distinct metric names found in that index. Use to discover what metrics are available before querying.",
		},
		tools.ToolMetricsLabelsList: {
			"Fetches available fields (labels) for a specific index or metric pattern.",
			"Input: (required) index pattern (e.g., 'metrics-*', 'metricbeat-*').",
			"Output: List of field names and their data types. Use this to discover the correct field names before querying.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Elasticsearch / Opensearch metric analysis in Kubernetes environments",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "What is the average CPU usage of all pods in namespace default over the last 30 minutes?",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       `metrics-*`,
						Explanation: "Discover available metric names (e.g. container.cpu.usage, k8s.pod.cpu.usage).",
					},
					{
						Tool: tools.ToolESMetricsQuery,
						Input: `{"index":"metrics-*","query":{"query":{"bool":{"filter":[` +
							`{"term":{"name.keyword":"container.cpu.usage"}},` +
							`{"range":{"time":{"gte":"now-30m"}}}` +
							`]}},"size":1}}`,
						Explanation: "Fetch a sample document to discover actual field names. Note: response shows `__name__` but the query field is `name.keyword`.",
					},
					{
						Tool: tools.ToolESMetricsQuery,
						Input: `{"index":"metrics-*","query":{"query":{"bool":{"filter":[` +
							`{"term":{"name.keyword":"container.cpu.usage"}},` +
							`{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"default"}},` +
							`{"range":{"time":{"gte":"now-30m"}}}` +
							`]}}}}`,
						Explanation: "Query with `attributes.resource.attributes.` prefix on field names from sample doc. Use `name.keyword` (NOT `__name__`), `time` (NOT `@timestamp`).",
					},
				},
				Explanation: "Discover metrics via metrics_list, fetch sample doc to learn field structure, then query with correct `attributes.` prefixed field names.",
			},
			{
				Question: "Memory usage trend per pod in namespace nudgebee for the last 1 hour",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       `metrics-*`,
						Explanation: "Discover available metric names (look for container.memory.usage, container.memory.working_set, etc.).",
					},
					{
						Tool: tools.ToolESMetricsQuery,
						Input: `{"index":"metrics-*","query":{"query":{"bool":{"filter":[` +
							`{"term":{"name.keyword":"container.memory.working_set"}},` +
							`{"range":{"time":{"gte":"now-1h"}}}` +
							`]}},"size":1}}`,
						Explanation: "Fetch sample document to discover field names. Remember: response `__name__` → query `name.keyword`, response `resource.attributes.X` → query `attributes.resource.attributes.X.keyword`.",
					},
					{
						Tool: tools.ToolESMetricsQuery,
						Input: `{"index":"metrics-*","query":{"query":{"bool":{"filter":[` +
							`{"term":{"name.keyword":"container.memory.working_set"}},` +
							`{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"nudgebee"}},` +
							`{"range":{"time":{"gte":"now-1h"}}}` +
							`]}}}}`,
						Explanation: "Filter by namespace using `attributes.resource.attributes.` prefix. Use `name.keyword` for metric name, `time` for timestamp.",
					},
				},
				Explanation: "Always prefix resource/metric attribute fields with `attributes.` when querying. Response labels omit this prefix but queries require it.",
			},
		},
		OutputFormat: "Markdown SRE-style summary highlighting key metric values (peak / avg / latest) and any anomalies. Do not include raw DSL.",
		Rag: core.NBAgentPromptRag{
			Module: "elasticsearch_metrics",
		},
	}
}

func (e ElasticSearchMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.ESMetricsQueryTool{},
		tools.MetricsListTool{Provider: "ES"},
		tools.ListMetricsLabelsTool{Provider: "ES"},
	}
}

func (e ElasticSearchMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps the ReAct loop. Mirrors the Prometheus agent rationale:
// allow query → execute → one self-corrected retry → execute → answer.
func (e ElasticSearchMetricsAgent) GetMaxIterations() int {
	return 10
}

func (e ElasticSearchMetricsAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

// UpdateToolResponseForPlanner truncates and summarizes large ES metric responses
// to prevent context window bloat in the ReAct loop.
func (e ElasticSearchMetricsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if !strings.EqualFold(toolRequest.Tool, tools.ToolESMetricsQuery) {
		// Truncate metrics_list / metrics_labels_list if too large.
		const maxDiscoveryChars = 4000
		if len(toolResponse) > maxDiscoveryChars {
			cutoff := strings.LastIndex(toolResponse[:maxDiscoveryChars], "\n")
			if cutoff <= 0 {
				cutoff = maxDiscoveryChars
			}
			return toolResponse[:cutoff] + "\n... (truncated — use a more specific pattern to narrow results)"
		}
		return toolResponse
	}

	// For es_metrics_query: parse the JSON response and build a compact summary.
	var response map[string]any
	if err := common.UnmarshalJson([]byte(toolResponse), &response); err != nil {
		return truncateString(toolResponse, 4000)
	}

	results, ok := response["results"].([]any)
	if !ok || len(results) == 0 {
		return toolResponse
	}

	var sb strings.Builder
	for _, resultAny := range results {
		result, ok := resultAny.(map[string]any)
		if !ok {
			continue
		}

		query, _ := result["query"].(string)
		payload, ok := result["payload"].([]any)
		if !ok || len(payload) == 0 {
			fmt.Fprintf(&sb, "Query: %s\nResult: No data found (empty payload)\n\n", query)
			continue
		}

		fmt.Fprintf(&sb, "Query: %s\nSeries count: %d\n", query, len(payload))

		// Show up to 5 series with stats, sub-sample values if too many points.
		maxSeries := 5
		if len(payload) > maxSeries {
			fmt.Fprintf(&sb, "(showing first %d of %d series)\n", maxSeries, len(payload))
			payload = payload[:maxSeries]
		}

		for i, seriesAny := range payload {
			series, ok := seriesAny.(map[string]any)
			if !ok {
				continue
			}

			// Extract metric labels.
			metricLabels := ""
			if m, ok := series["metric"]; ok {
				labelBytes, _ := common.MarshalJson(m)
				metricLabels = string(labelBytes)
			}

			// Extract timestamps and values.
			timestamps, _ := series["timestamps"].([]any)
			values, _ := series["values"].([]any)
			numPoints, _ := series["num_points"].(float64)
			nPoints := int(numPoints)
			if nPoints == 0 {
				nPoints = len(values)
			}

			fmt.Fprintf(&sb, "\n--- Series %d ---\n", i+1)
			fmt.Fprintf(&sb, "Labels: %s\n", metricLabels)
			fmt.Fprintf(&sb, "Points: %d\n", nPoints)

			// Use pre-computed stats from summarizeESMetricsResponse.
			if stats, ok := series["stats"].(map[string]any); ok {
				fmt.Fprintf(&sb, "Stats: min=%v, max=%v, avg=%v\n", stats["min"], stats["max"], stats["avg"])
			}

			// Include sub-sampled values if small enough.
			if len(values) > 0 && len(values) <= 20 {
				valBytes, _ := common.MarshalJson(values)
				tsBytes, _ := common.MarshalJson(timestamps)
				fmt.Fprintf(&sb, "Values: %s\nTimestamps: %s\n", string(valBytes), string(tsBytes))
			} else if len(values) > 20 {
				fmt.Fprintf(&sb, "(raw values omitted — %d points, use stats above)\n", len(values))
			}
		}
		sb.WriteString("\n")
	}

	result := sb.String()
	return truncateString(result, 6000)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (truncated: %d of %d chars)", maxLen, len(s))
}

func isLogIndex(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, "log") || strings.Contains(lower, "fluent") || strings.Contains(lower, "signoz")
}
