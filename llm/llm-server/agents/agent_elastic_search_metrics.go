package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"sort"
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

// esMetricSchema identifies the ingestion pipeline / field schema for a metric index.
type esMetricSchema string

const (
	schemaOTel       esMetricSchema = "otel"
	schemaMetricbeat esMetricSchema = "metricbeat"
	schemaElasticK8s esMetricSchema = "elastic_k8s"
	schemaCloudWatch esMetricSchema = "cloudwatch"
)

// classifyIndex returns the metric schema for a given index pattern based on naming conventions.
func classifyIndex(pattern string) esMetricSchema {
	lower := strings.ToLower(pattern)
	if strings.Contains(lower, "cloudwatch") {
		return schemaCloudWatch
	}
	if strings.Contains(lower, "kubernetes") {
		return schemaElasticK8s
	}
	if strings.Contains(lower, "metricbeat") || strings.Contains(lower, "metricsbeat") {
		return schemaMetricbeat
	}
	return schemaOTel
}

// detectMetricSchemas inspects the account's configured index patterns and returns
// the set of active schemas plus a deduplicated list of queryable index patterns.
// Falls back to the standard OTel trio when no index is explicitly configured.
func detectMetricSchemas(indexCfg utils.ESIndexConfig) (schemas map[esMetricSchema]bool, availableIndices []string) {
	schemas = make(map[esMetricSchema]bool)
	seen := make(map[string]bool)

	add := func(pattern string) {
		if pattern == "" || isLogIndex(pattern) || seen[pattern] {
			return
		}
		seen[pattern] = true
		schemas[classifyIndex(pattern)] = true
		availableIndices = append(availableIndices, pattern)
	}

	add(indexCfg.DefaultIndex)
	for _, p := range indexCfg.Indices {
		add(p)
	}

	// When OTel schema is active, ensure standard OTel index variants are listed.
	if schemas[schemaOTel] {
		for _, p := range []string{"metrics-*", "metrix-*"} {
			if !seen[p] {
				seen[p] = true
				availableIndices = append(availableIndices, p)
			}
		}
	}

	// Default fallback: no config found → assume OTel.
	if len(schemas) == 0 {
		schemas[schemaOTel] = true
		availableIndices = []string{"metrics-*", "metrix-*"}
	}

	// Sort availableIndices for deterministic LLM prompt generation & caching.
	sort.Strings(availableIndices)

	return schemas, availableIndices
}

func (e ElasticSearchMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	var indexCfg utils.ESIndexConfig
	if ctx != nil {
		if provider, err := services_server.GetObservabilityProvider(*ctx, e.accountId, "metrics"); err == nil && provider.DefaultIndex != "" {
			indexCfg.DefaultIndex = provider.DefaultIndex
		}
	}
	if indexCfg.DefaultIndex == "" || indexCfg.DefaultIndex == "*" {
		indexCfg = utils.GetESAccountIndexConfig(e.accountId, "metrics")
	}
	schemas, availableIndices := detectMetricSchemas(indexCfg)

	// --- Base instructions (always included) ---
	instructions := []string{
		"**Your Primary Goal:** Answer user questions about metrics stored in Elasticsearch / Opensearch.",
		"**Your Method:** Construct an Elasticsearch DSL query (JSON) and execute it via `es_metrics_query`. You MUST provide both `index` (the index pattern) and `query` (the DSL body) fields in the tool input.",
		fmt.Sprintf("**Target Indices:** You MUST only query these metric indices: '%s'. NEVER query log indices (e.g. 'fluentk8-*', 'logs-*', 'logs-kubernetes.*', 'logs-apm.*', 'logs-aws.cloudtrail-*').", strings.Join(availableIndices, ", ")),
	}

	// --- Schema routing table (only rows for detected schemas) ---
	instructions = append(instructions,
		"**Schema Routing (CRITICAL — pick fields by index pattern):**",
		"  Different ingestion pipelines store metrics with completely different field schemas. ALWAYS identify the schema before writing the query:",
		"  | Index pattern                           | Schema                       | Timestamp | Metric-name field |",
		"  |-----------------------------------------|------------------------------|-----------|-------------------|",
	)
	if schemas[schemaOTel] {
		instructions = append(instructions,
			"  | `metrics-*`, `metrix-*`                 | OTel collector               | `time`    | `name.keyword`    |",
		)
	}
	if schemas[schemaMetricbeat] {
		instructions = append(instructions,
			"  | `metricbeat-*`                          | Metricbeat (ECS)             | `@timestamp` | (embedded field) |",
		)
	}
	if schemas[schemaElasticK8s] {
		instructions = append(instructions,
			"  | `metrics-kubernetes.pod-*`              | Elastic Agent K8s — pod      | `@timestamp` | (embedded path) |",
			"  | `metrics-kubernetes.container-*`        | Elastic Agent K8s — container| `@timestamp` | (embedded path) |",
			"  | `metrics-kubernetes.node-*`             | Elastic Agent K8s — node     | `@timestamp` | (embedded path) |",
			"  | `metrics-kubernetes.volume-*`           | Elastic Agent K8s — volume   | `@timestamp` | (embedded path) |",
			"  | `metrics-kubernetes.event-*`            | Elastic Agent K8s — events   | `@timestamp` | (embedded path) |",
			"  | `metrics-kubernetes.state_<resource>-*` | Elastic Agent kube-state    | `@timestamp` | (embedded path) |",
		)
	}
	if schemas[schemaCloudWatch] {
		instructions = append(instructions,
			"  | `metrics-cloudwatch_metrics-*`          | Elastic Agent AWS CloudWatch | `@timestamp` | `aws.cloudwatch.metric_name.keyword` (value under `aws.<svc>.metrics.<metric>.<stat>`) |",
		)
	}
	if schemas[schemaElasticK8s] || schemas[schemaCloudWatch] {
		instructions = append(instructions,
			"  Elastic Agent indices do NOT have a `name`/`name.keyword` field — the metric is identified by the index itself, and the value lives at a fully-qualified path (e.g. `kubernetes.container.cpu.usage.nanocores`). Filter on that path's existence.",
		)
	}

	// --- OTel fast path (only if account uses OTel indices) ---
	if schemas[schemaOTel] {
		instructions = append(instructions,
			"**OTel Schema Fast Path (for `metrics-*` / `metrix-*` indices):**",
			"  For standard K8s CPU, Memory, Network, or Filesystem metrics, call `es_metrics_query` directly — no discovery needed.",
			"  **Query fields:**",
			"    * Metric name: `name.keyword` | Timestamp: `time` (NOT `@timestamp`) | Value: `value`",
			"    * Namespace: `attributes.resource.attributes.k8s@namespace@name.keyword`",
			"    * Pod: `attributes.resource.attributes.k8s@pod@name.keyword`",
			"    * Container: `attributes.resource.attributes.k8s@container@name.keyword`",
			"    * Node: `attributes.resource.attributes.k8s@node@name.keyword`",
			"  **CPU:** `container.cpu.usage`, `k8s.pod.cpu.usage`, `k8s.node.cpu.usage`",
			"  **Memory:** `container.memory.usage`, `container.memory.working_set`, `container.memory.rss`, `k8s.pod.memory.usage`, `k8s.node.memory.usage`",
			"  **Network:** `k8s.pod.network.io`, `k8s.pod.network.errors` (filter direction: `receive`/`transmit` via `attributes.metric.attributes.direction.keyword`)",
			"  **Filesystem:** `container.filesystem.usage`, `container.filesystem.capacity`, `k8s.node.filesystem.usage`",
			"  **Volume:** `k8s.volume.available`, `k8s.volume.capacity`, `k8s.volume.inodes`, `k8s.volume.inodes.free`, `k8s.volume.inodes.used`",
			"  **K8s Cluster (k8scluster receiver):** `k8s.container.cpu_request`, `k8s.container.cpu_limit`, `k8s.container.memory_request`, `k8s.container.restarts`, `k8s.pod.phase`, `k8s.deployment.desired`, `k8s.deployment.available`, `k8s.daemonset.desired_scheduled_nodes`, `k8s.daemonset.ready_nodes`, `k8s.replicaset.desired`, `k8s.hpa.current_replicas`, `k8s.hpa.desired_replicas`",
			"  **Example (pod CPU):**",
			`    {"index":"metrics-*","query":{"query":{"bool":{"filter":[{"term":{"name.keyword":"container.cpu.usage"}},{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"default"}},{"range":{"time":{"gte":"now-1h"}}}]}}}}`,
		)
	}

	// --- Elastic Agent K8s fast path (only if account uses elastic_k8s indices) ---
	if schemas[schemaElasticK8s] {
		instructions = append(instructions,
			"**Elastic Agent K8s Fast Path (for `metrics-kubernetes.*` indices):**",
			"  Filter by `exists` on the value path plus dimension filters. Use `@timestamp`. Dimension fields are keyword-mapped — do NOT append `.keyword`.",
			"  **Dimensions:** `kubernetes.namespace`, `kubernetes.pod.name`, `kubernetes.container.name`, `kubernetes.node.name`, `kubernetes.deployment.name`, `kubernetes.labels.<name>`",
			"  **Container (`metrics-kubernetes.container-*`):** CPU: `kubernetes.container.cpu.usage.nanocores`, `kubernetes.container.cpu.limit.nanocores` | Memory: `kubernetes.container.memory.usage.bytes`, `kubernetes.container.memory.workingset.bytes`, `kubernetes.container.memory.rss.bytes`, `kubernetes.container.memory.limit.bytes` | FS: `kubernetes.container.fs.usage.bytes`, `kubernetes.container.fs.capacity.bytes` | Status: `kubernetes.container.status.phase`, `kubernetes.container.status.ready`, `kubernetes.container.status.restarts`",
			"  **Pod (`metrics-kubernetes.pod-*`):** CPU: `kubernetes.pod.cpu.usage.nanocores` | Memory: `kubernetes.pod.memory.usage.bytes`, `kubernetes.pod.memory.workingset.bytes` | Network: `kubernetes.pod.network.rx.bytes`, `kubernetes.pod.network.tx.bytes`, `kubernetes.pod.network.rx.errors`, `kubernetes.pod.network.tx.errors`",
			"  **Node (`metrics-kubernetes.node-*`):** CPU: `kubernetes.node.cpu.usage.nanocores` | Memory: `kubernetes.node.memory.usage.bytes`, `kubernetes.node.memory.available.bytes` | FS: `kubernetes.node.fs.usage.bytes`, `kubernetes.node.fs.capacity.bytes` | Network: `kubernetes.node.network.rx.bytes`, `kubernetes.node.network.tx.bytes`",
			"  **Volume (`metrics-kubernetes.volume-*`):** `kubernetes.volume.available.bytes`, `kubernetes.volume.capacity.bytes`, `kubernetes.volume.used.bytes`, `kubernetes.volume.inodes.count`, `kubernetes.volume.inodes.free`",
			"  **Events (`metrics-kubernetes.event-*`):** `kubernetes.event.count`, `kubernetes.event.type` (Normal/Warning), `kubernetes.event.reason`, `kubernetes.event.involved_object.kind`, `kubernetes.event.involved_object.name`",
			"  **State metrics (`metrics-kubernetes.state_<resource>-*`):**",
			"    - state_pod: `kubernetes.pod.status.phase`, `kubernetes.pod.status.ready`, `kubernetes.pod.status.scheduled`",
			"    - state_container: `kubernetes.container.status.phase`, `kubernetes.container.status.ready`, `kubernetes.container.status.restarts`, `kubernetes.container.status.waiting.reason`",
			"    - state_deployment: `kubernetes.deployment.replicas.desired`, `kubernetes.deployment.replicas.available`, `kubernetes.deployment.replicas.unavailable`, `kubernetes.deployment.paused`",
			"    - state_replicaset: `kubernetes.replicaset.replicas.desired`, `kubernetes.replicaset.replicas.ready`, `kubernetes.replicaset.replicas.available`",
			"    - state_daemonset: `kubernetes.daemonset.replicas.desired`, `kubernetes.daemonset.replicas.ready`, `kubernetes.daemonset.replicas.misscheduled`",
			"    - state_job: `kubernetes.job.pods.active`, `kubernetes.job.pods.succeeded`, `kubernetes.job.pods.failed`",
			"    - state_node: `kubernetes.node.status.ready` (1=True, 0=False), `kubernetes.node.cpu.allocatable.cores`, `kubernetes.node.memory.allocatable.bytes`",
			"    - state_persistentvolume: `kubernetes.persistentvolume.capacity.bytes`, `kubernetes.persistentvolume.phase`",
			"    - state_persistentvolumeclaim: `kubernetes.persistentvolumeclaim.capacity.bytes`, `kubernetes.persistentvolumeclaim.phase`, `kubernetes.persistentvolumeclaim.request_storage.bytes`",
			"  **Example (container CPU):**",
			`    {"index":"metrics-kubernetes.container-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.container.cpu.usage.nanocores"}},{"term":{"kubernetes.namespace":"default"}},{"range":{"@timestamp":{"gte":"now-1h"}}}]}}}}`,
			"  **Example (deployment replicas):**",
			`    {"index":"metrics-kubernetes.state_deployment-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.deployment.replicas.available"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"@timestamp":{"gte":"now-30m"}}}]}}}}`,
		)
	}

	// --- CloudWatch fast path (only if account uses cloudwatch indices) ---
	if schemas[schemaCloudWatch] {
		instructions = append(instructions,
			"**AWS CloudWatch Fast Path (for `metrics-cloudwatch_metrics-*`):**",
			"  Filter by `exists` on the metric value path plus dimension filters. Use `@timestamp`. Do NOT append `.keyword` to dimension fields.",
			"  **Dimensions:** `cloud.region`, `cloud.account.id`, `aws.dimensions.InstanceId`, `aws.dimensions.DBInstanceIdentifier`, `aws.dimensions.FunctionName`, `aws.dimensions.QueueName`, `aws.dimensions.BucketName`",
			"  **Metric value paths (suffix: `.avg`, `.max`, `.min`, `.sum`, `.count`):**",
			"    - EC2: `aws.ec2.metrics.CPUUtilization.avg`, `aws.ec2.metrics.NetworkIn.sum`, `aws.ec2.metrics.NetworkOut.sum`, `aws.ec2.metrics.StatusCheckFailed.max`",
			"    - RDS: `aws.rds.metrics.CPUUtilization.avg`, `aws.rds.metrics.FreeableMemory.avg`, `aws.rds.metrics.FreeStorageSpace.avg`, `aws.rds.metrics.DatabaseConnections.avg`, `aws.rds.metrics.ReadLatency.avg`",
			"    - Lambda: `aws.lambda.metrics.Invocations.sum`, `aws.lambda.metrics.Errors.sum`, `aws.lambda.metrics.Duration.avg`, `aws.lambda.metrics.Throttles.sum`",
			"    - SQS: `aws.sqs.metrics.NumberOfMessagesSent.sum`, `aws.sqs.metrics.ApproximateNumberOfMessagesVisible.avg`, `aws.sqs.metrics.ApproximateAgeOfOldestMessage.max`",
			"    - S3: `aws.s3.metrics.BucketSizeBytes.avg`, `aws.s3.metrics.NumberOfObjects.avg`, `aws.s3.metrics.AllRequests.sum`",
			"    - ALB: `aws.applicationelb.metrics.RequestCount.sum`, `aws.applicationelb.metrics.HTTPCode_Target_5XX_Count.sum`, `aws.applicationelb.metrics.TargetResponseTime.avg`",
			"  **Example (EC2 CPU):**",
			`    {"index":"metrics-cloudwatch_metrics-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"aws.ec2.metrics.CPUUtilization.avg"}},{"term":{"aws.dimensions.InstanceId":"i-0123456789abcdef0"}},{"range":{"@timestamp":{"gte":"now-1h"}}}]}}}}`,
		)
	}

	// --- Field discovery and query patterns (always included, schema-aware) ---
	discoveryHints := []string{
		"  - **OTel:** sample doc `__name__` → query `name.keyword`. `resource.attributes.k8s@namespace@name` → `attributes.resource.attributes.k8s@namespace@name.keyword`. Append `.keyword` to all text fields.",
	}
	if schemas[schemaElasticK8s] {
		discoveryHints = append(discoveryHints,
			"  - **Elastic Agent K8s:** no `name` field — filter by `exists` on the value path. Dimension fields (e.g. `kubernetes.namespace`) are keyword-mapped; do NOT append `.keyword`.",
		)
	}
	if schemas[schemaCloudWatch] {
		discoveryHints = append(discoveryHints,
			"  - **CloudWatch:** metric value at `aws.<service>.metrics.<MetricName>.<stat>`. Use `aws.cloudwatch.metric_name.keyword` to filter by name when multiple metrics are present.",
		)
	}

	instructions = append(instructions,
		"**Field Discovery (for custom/non-standard metrics only):**",
		"  If the metric is NOT in the templates above:",
		"  - Step 1: Call `metrics_list` with the index pattern to discover metric names.",
		"  - Step 2: Fetch a `size:1` sample document to inspect the field structure.",
	)
	instructions = append(instructions, discoveryHints...)
	instructions = append(instructions,
		"**Query Patterns:** Use `bool`/`filter` queries only. Do NOT use `size:0` or `aggs` — the backend extracts time-series from document hits, not aggregation results.",
	)

	// Timestamp rule — tailored to the account's schemas.
	tsRule := buildTimestampRule(schemas)
	instructions = append(instructions,
		"**Time Range:** "+tsRule+" Default to `now-1h` if the user does not specify.",
		"**Self-Correction (when `es_metrics_query` returns empty `payload`):**",
		"  1. Widen the time range: `now-1h` → `now-6h` → `now-24h`.",
		"  2. Remove filters one by one — keep only the metric filter + time range first.",
		"  3. Verify you used the correct timestamp field for this index's schema.",
		"  4. Verify field naming conventions (`.keyword` suffix / `exists` filter) match the schema.",
		"  5. Try a different (more specific) index pattern.",
		"  NEVER retry the same query unchanged. Maximum 4 retries total.",
		"**Final Answer:** Summarize metric values (peak / avg / latest) in a concise SRE-style report. Do NOT return the raw DSL.",
	)

	// --- Constraints (schema-aware) ---
	constraints := []string{
		"You MUST execute every query via `es_metrics_query` before answering.",
		"For custom/non-standard metrics, you MUST discover field names via a `size:1` sample document or `metrics_labels_list` BEFORE writing the final query.",
		tsRule,
		"NEVER use `size:0` or `aggs` in queries — use filter queries only.",
		"NEVER return the generated DSL as the final answer — users want metric values.",
		"NEVER query log-specific indices for metric data.",
		"Do not ask the user for clarification — make the best assumption and proceed.",
	}
	if schemas[schemaOTel] {
		constraints = append(constraints,
			"OTel schema (`metrics-*`): ALWAYS append `.keyword` to text fields in `term` filters. NEVER use `__name__` — use `name.keyword` instead.",
		)
	}
	if schemas[schemaElasticK8s] || schemas[schemaCloudWatch] {
		constraints = append(constraints,
			"Elastic Agent / CloudWatch schemas: filter by `exists` on the value path; do NOT append `.keyword` to dimension fields — they are already keyword-mapped.",
		)
	}

	// --- Examples (only for detected schemas) ---
	var examples []core.NBAgentPromptExample

	if schemas[schemaOTel] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "Average CPU usage of all pods in namespace default over the last 30 minutes",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       `metrics-*`,
						Explanation: "Discover available metric names (e.g. container.cpu.usage, k8s.pod.cpu.usage).",
					},
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       `{"index":"metrics-*","query":{"query":{"bool":{"filter":[{"term":{"name.keyword":"container.cpu.usage"}},{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"default"}},{"range":{"time":{"gte":"now-30m"}}}]}}}}`,
						Explanation: "Filter by name.keyword and namespace with attributes.resource.attributes. prefix. Use `time` (NOT `@timestamp`) for the OTel schema.",
					},
				},
				Explanation: "OTel schema: use name.keyword for metric name, attributes.resource.attributes.* prefix for K8s dimensions, time for timestamp.",
			},
		)
	}

	if schemas[schemaElasticK8s] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "Container CPU usage in namespace production over the last hour",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       `{"index":"metrics-kubernetes.container-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.container.cpu.usage.nanocores"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"@timestamp":{"gte":"now-1h"}}}]}}}}`,
						Explanation: "Elastic Agent K8s: filter by exists on the value path, @timestamp for time range, no .keyword on dimension fields.",
					},
				},
				Explanation: "Elastic Agent K8s schema: metric = exists filter on value path; @timestamp; dimensions are already keyword-mapped.",
			},
			core.NBAgentPromptExample{
				Question: "Deployment replica counts in namespace production",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       `{"index":"metrics-kubernetes.state_deployment-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.deployment.replicas.available"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"@timestamp":{"gte":"now-15m"}}}]}}}}`,
						Explanation: "State metrics live in dedicated state_<resource>-* indices. Filter by exists on the value path.",
					},
				},
				Explanation: "State metrics: one index per K8s resource type; filter by exists on the replica/status value path.",
			},
		)
	}

	if schemas[schemaCloudWatch] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "EC2 CPU utilization for instance i-0123abcd over the last 30 minutes",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       `{"index":"metrics-cloudwatch_metrics-*","query":{"query":{"bool":{"filter":[{"exists":{"field":"aws.ec2.metrics.CPUUtilization.avg"}},{"term":{"aws.dimensions.InstanceId":"i-0123abcd"}},{"range":{"@timestamp":{"gte":"now-30m"}}}]}}}}`,
						Explanation: "CloudWatch: value at aws.<svc>.metrics.<MetricName>.<stat>; dimension under aws.dimensions.*; @timestamp.",
					},
				},
				Explanation: "CloudWatch schema: metric paths include statistic suffix (avg/max/min/sum/count); dimensions under aws.dimensions.*.",
			},
		)
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Elasticsearch / Opensearch metric analysis in Kubernetes environments",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage: map[string][]string{
			tools.ToolESMetricsQuery: {
				"Executes an Elasticsearch DSL query against the metrics API.",
				"Input: JSON object with `index` (index pattern) and `query` (DSL body with filter queries — NO aggs or size:0).",
				"Output: Metric time-series results (timestamps and values) extracted from document hits.",
			},
			tools.ToolMetricsList: {
				"Lists available metric names in an Elasticsearch index.",
				"Input: index pattern (e.g. 'metrics-*').",
				"Output: Distinct metric names. Use to discover available metrics before querying.",
			},
			tools.ToolMetricsLabelsList: {
				"Fetches available field names and types for an index pattern.",
				"Input: index pattern.",
				"Output: Field names and data types. Use to discover correct field names before querying.",
			},
		},
		Examples:     examples,
		OutputFormat: "Markdown SRE-style summary highlighting key metric values (peak / avg / latest) and any anomalies. Do not include raw DSL.",
		Rag: core.NBAgentPromptRag{
			Module: "elasticsearch_metrics",
		},
	}
}

// buildTimestampRule returns a concise timestamp rule tailored to the active schemas.
func buildTimestampRule(schemas map[esMetricSchema]bool) string {
	otel := schemas[schemaOTel]
	other := schemas[schemaElasticK8s] || schemas[schemaCloudWatch] || schemas[schemaMetricbeat]

	switch {
	case otel && other:
		return "Always include a `range` filter on the timestamp field: `time` for OTel (`metrics-*`/`metrix-*`), `@timestamp` for all other indices. Using the wrong field returns 0 results."
	case other:
		return "Always include a `range` filter on `@timestamp`. Do NOT use `time` — it is not a valid timestamp field for these indices."
	default: // otel only
		return "Always include a `range` filter on `time` (NOT `@timestamp`). Using `@timestamp` on OTel indices returns 0 results."
	}
}

func (e ElasticSearchMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	defaultIndex := ""
	if ctx != nil {
		if providerInfo, err := services_server.GetObservabilityProvider(*ctx, e.accountId, "metrics"); err == nil && providerInfo.DefaultIndex != "" {
			defaultIndex = providerInfo.DefaultIndex
		}
	}
	if defaultIndex == "" {
		defaultIndex = utils.GetESAccountIndexConfig(e.accountId, "metrics").DefaultIndex
	}
	return []toolcore.NBTool{
		tools.ESMetricsQueryTool{},
		tools.MetricsListTool{Provider: "ES", DefaultIndex: defaultIndex},
		tools.ListMetricsLabelsTool{Provider: "ES", DefaultIndex: defaultIndex},
		tools.ListMetricsLabelValuesTool{Provider: "ES", DefaultIndex: defaultIndex},
	}
}

func (e ElasticSearchMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps the ReAct loop to allow query → execute → self-correct → execute → answer.
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

			metricLabels := ""
			if m, ok := series["metric"]; ok {
				labelBytes, _ := common.MarshalJson(m)
				metricLabels = string(labelBytes)
			}

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

			if stats, ok := series["stats"].(map[string]any); ok {
				fmt.Fprintf(&sb, "Stats: min=%v, max=%v, avg=%v\n", stats["min"], stats["max"], stats["avg"])
			}

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

	return truncateString(sb.String(), 6000)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (truncated: %d of %d chars)", maxLen, len(s))
}

// isLogIndex returns true for index patterns known to contain logs rather than metrics.
// All Elastic Agent log streams (`logs-*`) contain the substring "log", as do
// OTel/Fluent/SigNoz log index names, so a single Contains check is sufficient.
func isLogIndex(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, "log") || strings.Contains(lower, "fluent") || strings.Contains(lower, "signoz")
}
