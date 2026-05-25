package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

func init() {

	toolDescription := `Prometheus Expert. This tool is "smart" and handles its own resource discovery. Retrieves, analyzes, and visualizes Prometheus metrics by translating natural language questions into PromQL queries. Use this agent directly to monitor, query, troubleshoot, and chart metrics such as memory, CPU, network, and more in Kubernetes and cloud environments without needing separate reconnaissance. Returns metric data, summaries, and visualizations (charts/graphs) for automation, dashboards, or troubleshooting.`
	toolInput := "Provide a question in natural language to retrieve, analyze, or visualize Prometheus metrics."
	toolOutput := "Returns metric data, summaries, and visualizations for Prometheus queries."

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(PrometheusAgentName, func(accountId string) (core.NBAgent, error) {
		return PrometheusAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

func newPrometheusAgent(account string) PrometheusAgent {
	return PrometheusAgent{
		accountId: account,
	}
}

type PrometheusAgent struct {
	accountId string
}

const PrometheusAgentName = "prometheus"

func (p PrometheusAgent) GetName() string {
	return PrometheusAgentName
}

func (l PrometheusAgent) GetNameAliases() []string {
	return []string{"Prometheus"}
}

func (p PrometheusAgent) GetDescription() string {
	return `Prometheus Expert. This tool is "smart" and handles its own resource discovery. Retrieves, analyzes, and visualizes Prometheus metrics by translating natural language questions into PromQL queries. Use this agent directly to monitor, query, troubleshoot, and chart metrics such as memory, CPU, network, and more in Kubernetes and cloud environments without needing separate reconnaissance. Returns metric data, summaries, and visualizations (charts/graphs) for automation, dashboards, or troubleshooting.`
}

func (l PrometheusAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Your Primary Goal:** Answer user questions related to prometheus metrics.",
		"**Your Method:** You MUST use following steps to answer users question",
		"1.  **Direct Execution (Fast Path):** If the request is for standard CPU, Memory, Network, or Response Time metrics for Pods, Nodes, Namespaces, or Workloads (Deployments, StatefulSets, etc.), TRY to construct the PromQL query yourself and call `prometheus_execute` directly. Use actual consumption metrics by default.",
		"    **Common PromQL Templates:**",
		"    **cAdvisor Metrics (CPU, Memory) - filter by `pod` and `namespace`:**",
		"    - Pod CPU: `sum(rate(container_cpu_usage_seconds_total{pod=~\"POD_NAME.*\", namespace=\"NS\"}[5m])) by (pod)`",
		"    - Pod Memory: `container_memory_usage_bytes{pod=~\"POD_NAME.*\", namespace=\"NS\"}`",
		"    - Namespace Memory (Usage): `sum(container_memory_usage_bytes{namespace=\"NS\", container!=\"\"})`",
		"    - Node CPU: `100 - (avg by (instance) (irate(node_cpu_seconds_total{mode=\"idle\"}[5m])) * 100)`",
		"    **CPU Utilization % (container usage divided by node allocatable — NOT cores):**",
		"    - Container CPU % per pod: `(sum by(pod,namespace)(rate(container_cpu_usage_seconds_total{namespace=\"NS\"}[5m])) / on(pod,namespace) group_left() kube_pod_container_resource_limits{resource=\"cpu\",namespace=\"NS\"}) * 100`",
		"    - Node CPU % (sum across all pods on node): `sum by(node)(rate(container_cpu_usage_seconds_total[5m])) / on(node) group_left() sum by(node)(kube_node_status_allocatable{resource=\"cpu\"}) * 100`",
		"    **Disk I/O (reads and writes per second per pod):**",
		"    - Disk IOPS per pod: `sum by(pod,namespace)(rate(container_fs_reads_total{namespace=\"NS\"}[5m]) + rate(container_fs_writes_total{namespace=\"NS\"}[5m]))`",
		"    - Disk Read Bytes/s: `sum by(pod,namespace)(rate(container_fs_reads_bytes_total{namespace=\"NS\"}[5m]))`",
		"    - Disk Write Bytes/s: `sum by(pod,namespace)(rate(container_fs_writes_bytes_total{namespace=\"NS\"}[5m]))`",
		"    **eBPF HTTP Metrics (Requests, Latency, Error Rate) - filter by `destination_workload_name` and `destination_workload_namespace`:**",
		"    - IMPORTANT: For `container_http_*` metrics, `pod` and `namespace` labels refer to the OBSERVER agent, NOT the target workload. You MUST use `destination_workload_name` (deployment/service name) and `destination_workload_namespace` instead.",
		"    - Workload Response Time: `sum(rate(container_http_requests_duration_seconds_total_sum{destination_workload_name=\"WORKLOAD_NAME\", destination_workload_namespace=\"NS\"}[5m])) / sum(rate(container_http_requests_total{destination_workload_name=\"WORKLOAD_NAME\", destination_workload_namespace=\"NS\"}[5m]))`",
		"    - Workload Error Rate (5xx): `sum(rate(container_http_requests_total{destination_workload_name=\"WORKLOAD_NAME\", destination_workload_namespace=\"NS\", status=~\"5..\"}[5m])) / sum(rate(container_http_requests_total{destination_workload_name=\"WORKLOAD_NAME\", destination_workload_namespace=\"NS\"}[5m]))`",
		"    - Workload RPS: `sum(rate(container_http_requests_total{destination_workload_name=\"WORKLOAD_NAME\", destination_workload_namespace=\"NS\"}[5m]))`",
		"    - Top 5 Slowest Apps (P99): `topk(5, histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket[5m])) by (le, destination_workload_name, destination_workload_namespace)))`",
		"    **CRITICAL:** If this direct query returns NO DATA, check label schema first: `container_http_*` → use `destination_workload_name`/`destination_workload_namespace`; `container_cpu_*`/`container_memory_*` → use `pod`/`namespace`; CPU % → verify `kube_pod_container_resource_limits` has `resource=\"cpu\"`; Disk I/O → verify `container_fs_reads_total` exists (use `metrics_list` with 'container_fs' if unsure). If still no data after label fix, proceed to Step 2.",
		"2.  **Bypassing Resource Search:** If the user provides a specific resource name (e.g., 'example-server') and namespace, do NOT use `resource_search`. Construct your PromQL regex using that name directly and go to execution. Only search if the name is missing, ambiguous, or if direct execution fails.",
		"2.5 **Custom Metric Discovery:** If the user asks about metrics NOT in the standard templates (e.g., Redis, Kafka, Postgres, JVM, custom app metrics), use `metrics_list` ONCE with a single technology keyword (e.g., 'redis', 'kafka', 'pg', 'jvm'). From the returned metric names, identify the best match using your domain knowledge and construct the PromQL directly. Do NOT use `search_metrics` — it is slow. Only use `promql_query` if `metrics_list` returns no results.",
		"3.  **Complex Query Generation:** ONLY use `promql_query` tool if the request involves custom metrics, complex aggregations, or if you are unsure of the metric name.",
		"4.  **Execute Query:** Use the `prometheus_execute` tool to execute prometheus query and get the results.",
		"5.  **Visualize:** ONLY if the user explicitly asks for a chart, graph, or plot, use the `visualizer` tool. For standard queries, provide a text summary. Do NOT generate any Mermaid/chart syntax yourself.",
		"**Time Handling:**",
		" - If the user request specifies a specific time (e.g., 'at 01:00', 'around 01:00'), you MUST calculate specific `start_time` and `end_time` (e.g. 00:45 to 01:15 for a 30m window) and pass them to `prometheus_execute`.",
		" - Do NOT use `range` for specific past time points; `range` is only for 'last X minutes/hours' relative to NOW.",
		"**Self-Correction:**",
		" - If `prometheus_execute` returns no data, FIRST check if you used the correct label schema (eBPF vs cAdvisor). If wrong, fix and retry.",
		" - If the label schema was correct and still no data, try ONE alternative using `promql_query`. If that also returns no data, report that no data exists for the queried parameters.",
		" - NEVER retry the same exact query. Each retry must change something: labels, metric name, or time range.",
		" - Maximum 2 retries total for any single user question.",
		" - If you receive an error from any tool, analyze the error message, refine your approach, and try again.",
		" - If no data, use `metrics_list` with a technology keyword (e.g., 'redis', 'kafka', 'pg') to discover actual metrics in this environment. Customers may have custom metrics (e.g., `redis_memory_used_bytes`, `confluent_kafka_*`, `pg_*`, `java_*`) that differ from standard K8s templates.",
		"**Example of the Correct Workflow:**",
		"Question: What is the memory usage of the web-server pod?",
		"Thought: The user wants standard memory metrics and provided a name. I can use the Fast Path, bypass resource search, and execute the query directly.",
		"Action: prometheus_execute",
		"Action Input: `container_memory_usage_bytes{pod=~\"web-server.*\"}`",
		"Observation: `[{\"metric\":{\"pod\":\"web-server-123\"}, \"value\":[162... , \"50000000\"]}]`",
		"Thought: I have now successfully fetched the final data. I will summarize this for the user.",
		"Final Answer: The memory usage for the `web-server-123` pod is 50MB.",
	}
	toolUsage := map[string][]string{
		PromqlAgentName: {
			"Generates a PromQL query from a natural language question.",
			"The output of this tool is a query string meant to be used as input for the `prometheus_execute` tool.",
			"Input: A natural language question about metrics.",
			"Output: A PromQL query string.",
		}, tools.ToolQueryPrometheus: {
			"Executes a PromQL query to fetch metric data from Prometheus.",
			"The input for this tool is the query string generated by the `promql_query` tool. Optionally, you can provide a time range.",
			"Input: A valid PromQL query string. Optional: start_time, end_time, or range (e.g. '2d', '1w', '1mo' for months).",
			"Output: The metric data from Prometheus.",
		}, tools.ToolMetricsList: {
			"PRIMARY tool for custom/unknown metric discovery. Searches metrics by technology keyword (substring match).",
			"Input: (required) single technology keyword (e.g., 'redis', 'kafka', 'pg', 'jvm', 'container_fs').",
			"Output: List of matching metric names (capped at 30 families). Use your domain knowledge to pick the right one.",
			"Call ONCE. Do NOT retry with different keywords — if no results, use promql_query.",
		}, tools.ToolMetricsLabelsList: {
			"OPTIONAL: Fetches available labels for a specific metric. Use only when you need label names to construct a filter.",
			"Input: (required) exact metric name.",
			"Output: List of label names for that metric.",
			"If this tool fails, proceed with the metric name and {__CLUSTER__} labels.",
		},
		ResourceSearchAgentName: {
			"Use this tool for fuzzy resource matching and generating search strategies when resources are not found.",
			"Input: search query in natural language",
			"Output: resource suggestions and search strategies",
			"Examples: Can you search pods maching `pod1`",
			"Examples: Get me pods for app `nginx` in namespace `ingress`",
		},
		VisualizationAgentName: {
			"Use this tool to generate visualizations (charts, graphs) for metrics, Without using this tool you can't generate correct charts or graphs.",
			"Input: A natural language request that INCLUDES the FULL metric data. For trend charts (time-series), you MUST pass the arrays of values and timestamps (e.g., 'Series A: [1,2,3], Time: [t1,t2,t3]'). For bar charts, pass the categorical values. Do NOT summarize time-series data into a single number unless explicitly asked.",
			"Output: A Mermaid.js visualization block.",
		},
	}

	constraints := []string{
		"You MUST use `prometheus_execute` tool to execute query and get results.",
		"Prefer actual consumption metrics (e.g. `container_memory_usage_bytes`) over resource requests/limits unless explicitly asked for configurations.",
		"You MAY execute PromQL queries directly using `prometheus_execute` if you are confident in the query syntax and metric names (e.g. standard K8s metrics). Otherwise, use `promql_query` first.",
		"Do NOT use `resource_search` if the resource name and namespace are already provided in the user request.",
		"If `prometheus_execute` returns no data after trying correct label schemas, use `promql_query` ONCE. If still empty, inform the user no data exists.",
		"You MUST use `visualizer` tool to generate visualizations (charts) for metrics, and prefer line/bar charts, without using this tool you can't generate correct charts or graphs.",
		"For specific time queries (e.g. 'around 10am'), ALWAYS use `start_time` and `end_time` calculated from the request. NEVER use `range` for absolute time targets.",
		"The final answer must be a summary or chart of the data from `prometheus_execute` tool, if user requests for chart or graph prefer line/bar charts using `visualizer` tool.",
	}

	return core.NBAgentPrompt{
		Role:         "An SRE expert specializing in Prometheus Querying within Kubernetes environments",
		Instructions: instructions,
		ToolUsage:    toolUsage,
		Constraints:  constraints,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "Get Me the CPU usage of the web-server pod",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `sum(rate(container_cpu_usage_seconds_total{pod=~"web-server.*"}[5m])) by (pod)`,
						Explanation: "Request is for standard CPU metrics. Using Fast Path to execute query directly.",
					},
				},
				Explanation: `The user wants standard CPU usage. I will use the Fast Path to execute the query directly without using 'promql_query'.`,
			},
			{
				Question: "Memory usage of kube-system namespace",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `sum(container_memory_usage_bytes{namespace="kube-system", container!=""})`,
						Explanation: "Request is for standard Namespace memory metrics. Using Fast Path to execute query directly.",
					},
				},
				Explanation: `The user wants namespace memory usage. I will use the Fast Path to execute the query directly.`,
			},
			{
				Question: "List Available Metrics with name containing 'cpu'",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       "cpu",
						Explanation: "List Available Metrics with name containing 'cpu'"},
				},
				Explanation: `The user wants to see available metrics with name containing 'cpu'.`,
			},
			{
				Question: "List Available Labels for metrics 'container_cpu_usage_seconds_total'",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsLabelsList,
						Input:       "container_cpu_usage_seconds_total",
						Explanation: "List Available Labels for metrics 'container_cpu_usage_seconds_total'"},
				},
				Explanation: `The user wants to see available metrics labels with name 'container_cpu_usage_seconds_total'.`,
			},
			{
				Question: "Get me the memory usage of the web-server pod for last 2 days",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `{"command": "container_memory_usage_bytes{pod=~\"web-server.*\"}", "range": "2d"}`,
						Explanation: "Request is for standard Memory metrics. Using Fast Path to execute query directly with 'range' parameter set to '2d'.",
					},
				},
				Explanation: `The user wants memory usage for last 2 days. I will use the Fast Path to execute the query directly.`,
			},
			{
				Question: "Get cpu usage for web-server pod between 2024-01-01T10:00:00Z and 2024-01-01T12:00:00Z",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `{"command": "sum(rate(container_cpu_usage_seconds_total{pod=~\"web-server.*\"}[5m])) by (pod)", "start_time": "2024-01-01T10:00:00Z", "end_time": "2024-01-01T12:00:00Z"}`,
						Explanation: "Request is for standard CPU metrics. Using Fast Path to execute query directly with 'start_time' and 'end_time'.",
					},
				},
				Explanation: `The user wants cpu usage for a specific time range. I will use the Fast Path to execute the query directly.`,
			},
			{
				Question: "What is the error rate of the api-server in namespace default?",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `sum(rate(container_http_requests_total{destination_workload_name="api-server", destination_workload_namespace="default", status=~"5.."}[5m])) / sum(rate(container_http_requests_total{destination_workload_name="api-server", destination_workload_namespace="default"}[5m]))`,
						Explanation: "Request is for HTTP error rate. Using eBPF labels (destination_workload_name, not pod) for container_http_* metrics.",
					},
				},
				Explanation: `HTTP error rate query. For container_http_* metrics, use destination_workload_name and destination_workload_namespace instead of pod and namespace.`,
			},
			{
				Question: "Get memory usage chart for web-server pod",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `{"command": "container_memory_usage_bytes{pod=~\"web-server.*\"}", "start_time": "2024-01-01T10:00:00Z", "end_time": "2024-01-01T12:00:00Z"}`,
						Explanation: "Request is for standard Memory metrics. Using Fast Path to execute query directly.",
					},
					{
						Tool:        VisualizationAgentName,
						Input:       "Generate a time-series line chart for memory usage. Data: Timestamps: [10:00, 10:05, 10:10], web-server-1: [50MB, 55MB, 60MB], web-server-2: [60MB, 58MB, 55MB]",
						Explanation: "I will generate a visualization using the full time-series data I retrieved.",
					},
				},
				Explanation: `The user wants memory usage chart for web-server pod. I will generate visualization (line chart) for memory usage of the web-server pod.`,
			},
			{
				Question: "What is the CPU utilization percentage per pod in namespace nudgebee?",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `(sum by(pod,namespace)(rate(container_cpu_usage_seconds_total{namespace="nudgebee"}[5m])) / on(pod,namespace) group_left() kube_pod_container_resource_limits{resource="cpu",namespace="nudgebee"}) * 100`,
						Explanation: "CPU % utilization — Fast Path template. Divides usage by resource limits, no promql_query needed.",
					},
				},
				Explanation: "CPU utilization % uses the Fast Path template directly. Do not delegate to promql_query for this.",
			},
			{
				Question: "What is the Redis memory usage?",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       "redis",
						Explanation: "Redis not in standard templates. Using metrics_list with keyword 'redis' to discover available metrics.",
					},
					{
						Tool:        tools.ToolQueryPrometheus,
						Input:       `redis_memory_used_bytes`,
						Explanation: "metrics_list returned redis_memory_used_bytes. Execute directly.",
					},
				},
				Explanation: "Custom metric discovery: use metrics_list with a technology keyword, then execute. Do not use search_metrics.",
			},
		},
		OutputFormat: "Markdown. Identify critical insights and patterns. Do NOT include technical data flow descriptions (e.g., 'Prometheus -> Query').",
	}
}

func (p PrometheusAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{
		tools.PrometheusExecuteTool{},
		tools.SearchMetricsTool{},
		tools.MetricsListTool{Provider: "prometheus"},
		tools.ListMetricsLabelsTool{Provider: "prometheus"},
	}
	if prom, ok := toolcore.GetNBTool(p.accountId, PromqlAgentName); ok {
		tools = append(tools, prom)
	}
	if searchTool, ok := toolcore.GetNBTool(p.accountId, ResourceSearchAgentName); ok {
		tools = append(tools, searchTool)
	}
	if visualizationTool, ok := toolcore.GetNBTool(p.accountId, VisualizationAgentName); ok {
		tools = append(tools, visualizationTool)
	}

	return tools
}

func (l PrometheusAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps the prometheus ReAct loop at 8 steps.
// The default global limit is 10, but the prometheus agent was observed hallucinating
// tool names (generate_promql, prometheus_query_generator) at the iteration limit when
// repeated prometheus_execute calls returned no data. Capping at 8 limits wasted retries
// while still allowing: resource_search → promql_query → execute → retry → execute → answer.
func (l PrometheusAgent) GetMaxIterations() int {
	return 8
}

func (l PrometheusAgent) PostProcessResponse(ctx *security.RequestContext, request core.NBAgentRequest, resp core.NBAgentResponse) core.NBAgentResponse {
	for _, stepRes := range resp.AgentStepResponse {
		if stepRes.Call.FunctionCall.Name == "prometheus_execute" {
			responseStr := stepRes.Response.Content
			responseMap := map[string]any{}
			err := common.UnmarshalJson([]byte(responseStr), &responseMap)
			if err != nil {
				ctx.GetLogger().Error("prometheus: unable unmarshal)", "error", err.Error())
				continue
			}
			resp.Response = []string{responseStr}
		}
	}
	return resp
}

func (l PrometheusAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

func (l PrometheusAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolQueryPrometheus) {
		resultsMap := map[string]any{}
		err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
		if err != nil {
			return toolResponse
		}

		// seriesStat holds the compact stats summary for one time series, used to
		// reconstruct an accurate stats-only footer when the full response is truncated.
		type seriesStat struct {
			query  string
			label  string
			stats  string
			nTotal int // total data points before sub-sampling
		}
		var allStats []seriesStat

		data := ""
		dataResponse := ""
		for k, v := range resultsMap {
			dataArray := []any{}
			err := common.UnmarshalJson([]byte(v.(string)), &dataArray)
			if err == nil && len(dataArray) > 0 {
				for _, dataMapAny := range dataArray {
					if dataMap, ok := dataMapAny.(map[string]any); ok {
						label := ""
						stats := ""
						if datastatMap, ok := dataMap["stats"]; ok {
							datastats, _ := common.MarshalJson(datastatMap)
							stats = string(datastats)
						}
						if datastatMap, ok := dataMap["metric"]; ok {
							datastats, _ := common.MarshalJson(datastatMap)
							label = string(datastats)
						}

						rawValues := []any{}
						if v, ok := dataMap["value"]; ok {
							rawValues, _ = v.([]any)
						} else if v, ok := dataMap["values"]; ok {
							rawValues, _ = v.([]any)
						}
						allStats = append(allStats, seriesStat{query: k, label: label, stats: stats, nTotal: len(rawValues)})

						// Inline raw data points only when the result set is small enough for the LLM to
						// reason over directly. With >5 series the prompt grows significantly, so we
						// switch to a stats-only summary (Peak/Min/Avg) to keep context size bounded.
						// Previously this was 10; reduced to 5 after observing context bloat with
						// wide Prometheus queries returning many label combinations.
						maxInlineDataPoints := config.Config.LlmServerAgentPrometheusMaxInlineDataPoints
						if maxInlineDataPoints <= 0 {
							maxInlineDataPoints = 5
						}
						if len(dataArray) > maxInlineDataPoints {
							dataResponse = fmt.Sprintf("%s\n **Labels** - %s\nResponse Summary - %s\n", dataResponse, label, stats)
						} else {
							values := ""
							rawTimestamps := []any{}
							if v, ok := dataMap["timestamps"]; ok {
								rawTimestamps, _ = v.([]any)
							}

							// Sub-sample if data is too long (e.g., > 100 points)
							maxPoints := 100
							samplingNote := ""

							// Sub-sample if data is too long
							if len(rawValues) > maxPoints {
								step := len(rawValues) / maxPoints
								if step < 1 {
									step = 1
								}
								sampledValues := []any{}
								sampledTimestamps := []any{}
								for i := 0; i < len(rawValues); i += step {
									sampledValues = append(sampledValues, rawValues[i])
									if i < len(rawTimestamps) {
										sampledTimestamps = append(sampledTimestamps, rawTimestamps[i])
									}
								}
								// Ensure the last point is included
								if (len(rawValues)-1)%step != 0 {
									sampledValues = append(sampledValues, rawValues[len(rawValues)-1])
									if len(rawTimestamps) > 0 {
										sampledTimestamps = append(sampledTimestamps, rawTimestamps[len(rawTimestamps)-1])
									}
								}
								samplingNote = fmt.Sprintf("\n**Note:** Data sub-sampled (%d points shown out of %d). Use 'Stats' for accurate Peak/Min/Avg values.", len(sampledValues), len(rawValues))
								rawValues = sampledValues
								rawTimestamps = sampledTimestamps
							}

							datavalue, _ := common.MarshalJson(rawValues)
							values = string(datavalue)

							datatime, _ := common.MarshalJson(rawTimestamps)
							timestamps := string(datatime)

							dataResponse = fmt.Sprintf("%s\n **Labels** - %s\n**Stats** - %s\n**Values** - %s\n**Timestamps** - %s%s", dataResponse, label, stats, values, timestamps, samplingNote)
						}
					}
				}
			}
			if dataResponse == "" {
				dataResponse = "No data found"
			}
			data = fmt.Sprintf("%s \n **Query** - %s \n **Response** - %s", data, k, dataResponse)
			if strings.Contains(dataResponse, "Response Summary") {
				data = data + "\n\n note - data is too big to show all data, so continue with summary response instead of visualization"
			}
		}

		// Hard cap: truncate the full formatted tool response.
		// Even with <=5 series, a wide time range produces many value arrays.
		// When truncated, append a compact stats-only summary for ALL series so the LLM
		// always has Peak/Min/Avg even for series whose raw values were cut off.
		maxToolResponseChars := config.Config.LlmServerAgentPromqlMaxToolRespChars
		if maxToolResponseChars <= 0 {
			maxToolResponseChars = 4000
		}
		if len(data) > maxToolResponseChars {
			statsSummary := "\n\n**Stats Summary (all series — raw values truncated above):**"
			for _, s := range allStats {
				statsSummary += fmt.Sprintf("\n  Query: %s | Labels: %s | Points: %d | Stats: %s", s.query, s.label, s.nTotal, s.stats)
			}
			data = data[:maxToolResponseChars] + fmt.Sprintf(
				"\n\n(response truncated: showing %d of %d chars)%s",
				maxToolResponseChars, len(data), statsSummary,
			)
		}

		return data
	}
	return toolResponse
}
