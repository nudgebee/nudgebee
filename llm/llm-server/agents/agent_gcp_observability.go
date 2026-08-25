package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// GcpMetricsAgentName is the metrics-backend agent for GCP accounts that have no
// first-class metrics provider (Prometheus / Datadog / Elasticsearch) configured.
// The metrics dispatcher (newMetricsAgent) routes to it when GetMetricsProvider
// resolves the account's cloud type to "gcp". It answers metric questions using
// Cloud Monitoring via the gcloud CLI, mirroring the dedicated-per-backend pattern
// of datadog_metrics / elastic_search_metrics.
const GcpMetricsAgentName = "gcp_metrics"

func init() {
	core.RegisterNBAgentFactory(GcpMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return newGcpMetricsAgent(accountId), nil
	})
}

func newGcpMetricsAgent(accountId string) core.NBAgent {
	return GcpMetricsAgent{accountId: accountId}
}

type GcpMetricsAgent struct {
	accountId string
}

func (a GcpMetricsAgent) GetName() string { return GcpMetricsAgentName }

func (a GcpMetricsAgent) GetNameAliases() []string { return []string{"GCPMetrics"} }

func (a GcpMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes GCP Cloud Monitoring metrics (CPU, memory, disk, network, request latency/error rate, managed-service metrics) via the gcloud CLI. Used as the metrics backend for GCP accounts without a Prometheus/Datadog/Elasticsearch provider. Handles its own metric-type and resource discovery.`
}

func (a GcpMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's question using GCP Cloud Monitoring metrics. Base every conclusion on CLI output — never invent metric values, resource names, or time ranges.",
		"**Tool name:** Your gcloud tool is `gcloud_execute`. Do NOT emit variants like `gcp_monitoring` or `gcp_metrics_execute` — the planner will fail to resolve them. Use `shell_execute` only for the Cloud Monitoring v3 REST path described below.",
		"**Project is pre-configured:** The GCP project comes from the authenticated account credentials (environment variable). Do NOT run `gcloud config set project`. Pass `--project <project_id>` only when you need to target a non-default project.",
		"**Primary read — `gcloud monitoring time-series list` (note the hyphen in `time-series`):** Use it for standard reads. You MUST scope every query to keep output bounded:",
		"  - `--filter` selects the metric type and resource, e.g. `--filter='metric.type=\"compute.googleapis.com/instance/cpu/utilization\"'`. Add resource labels to narrow further, e.g. ` AND resource.labels.instance_id=\"...\"`.",
		"  - `--interval-start-time` / `--interval-end-time` bound the window (RFC3339). Prefer the `[[Time:-1h]]` / `[[Time:Now]]` macros — NEVER compute timestamps yourself.",
		"  - `--format=json` for parseable output; `--format='table(...)'` (columns in single quotes) for a compact summary.",
		"**Common metric types (discover, don't assume — instrumentation varies):**",
		"  - Compute CPU: `compute.googleapis.com/instance/cpu/utilization`",
		"  - Compute memory (ops agent): `agent.googleapis.com/memory/percent_used`",
		"  - GKE container CPU/memory: `kubernetes.io/container/cpu/core_usage_time`, `kubernetes.io/container/memory/used_bytes`",
		"  - Cloud SQL: `cloudsql.googleapis.com/database/cpu/utilization`, `.../database/memory/utilization`",
		"  - Cloud Run: `run.googleapis.com/container/cpu/utilizations`, `run.googleapis.com/request_latencies`",
		"  If a metric type returns no data, list what actually exists: `gcloud monitoring metric-descriptors list --filter='metric.type=~\"<service>.*\"' --format='value(type)' --limit=50` (use ONE service keyword), then pick the right descriptor.",
		"**Complex reads (aggregations, alignment, MQL) — Cloud Monitoring v3 REST via `shell_execute`:** `gcloud_execute` cannot pass filter/interval/aggregation params to the API. For those, run through `shell_execute`: `curl -s -H \"Authorization: Bearer $(gcloud auth print-access-token)\" 'https://monitoring.googleapis.com/v3/projects/<project_id>/timeSeries?filter=...&interval.startTime=...&interval.endTime=...&aggregation.alignmentPeriod=60s&aggregation.perSeriesAligner=ALIGN_MEAN'`. Run a `gcloud_execute` command first in the conversation so the workspace has an active gcloud session for the token to succeed.",
		"**Large output:** Cloud Monitoring responses can be large. Filter at the API level first (tight `--filter`, short interval, `--limit`). When a response is still big, do NOT pull the full JSON into context — from `shell_execute`, run the read with its raw output redirected to a per-conversation workspace file (`gcloud monitoring time-series list ... --format=json > metrics_gcp.json`; the file persists across turns in this conversation), then `jq` that file for only the points you need and re-read the file instead of re-running the query.",
		"**Self-correction:** On an error, read `Stderr` — it usually names the fix (bad filter syntax, missing flag, wrong metric type). Correct and retry ONCE per distinct cause; never repeat an identical failing command. On a permission error (`PERMISSION_DENIED`, missing `roles/monitoring.viewer`), report the missing role and scope as a finding — NEVER attempt to grant yourself IAM.",
	}

	constraints := []string{
		"Do not ask the user for clarification — resolve using the available tools (metric-descriptor discovery, resource labels).",
		"CRITICAL for stability: always bound queries with a `--filter`, a short `--interval`, and `--limit` to prevent context saturation.",
		"Empty result means 'no data for these parameters', not 'healthy' — state it explicitly and, when a metric type returned nothing, confirm the metric descriptor exists before concluding.",
		"Base the final answer only on values observed in CLI/API output. Prefer actual utilization metrics over quota/limit descriptors unless asked.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteGcpCliCommand: {
			"Use **gcloud_execute** to run `gcloud monitoring time-series list` and `gcloud monitoring metric-descriptors list`.",
			"Always prefer this tool for standard metric reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for the Cloud Monitoring v3 REST path (curl with `gcloud auth print-access-token`) when you need aggregation/alignment params, for `jq` post-processing, and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "What is the CPU utilization of my compute instances in the last hour?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud monitoring time-series list --filter='metric.type=\"compute.googleapis.com/instance/cpu/utilization\"' --interval-start-time=[[Time:-1h]] --interval-end-time=[[Time:Now]] --format=json --limit=50",
					Explanation: "Standard Cloud Monitoring read, scoped by metric type and a 1h window.",
				},
			},
		},
		{
			Question: "Show Cloud SQL CPU utilization for instance my-db over the last 6 hours",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud monitoring time-series list --filter='metric.type=\"cloudsql.googleapis.com/database/cpu/utilization\" AND resource.labels.database_id=~\".*my-db\"' --interval-start-time=[[Time:-6h]] --interval-end-time=[[Time:Now]] --format=json --limit=50",
					Explanation: "Cloud SQL metric type narrowed by resource label; window via time macros.",
				},
			},
		},
		{
			Question: "What metrics are available for Cloud Run?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud monitoring metric-descriptors list --filter='metric.type=~\"run.googleapis.com.*\"' --format='value(type)' --limit=50",
					Explanation: "Descriptor discovery when the exact metric type is unknown — one service keyword.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in GCP Cloud Monitoring metrics",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete metric values with their timestamps. Do NOT describe internal data flow.",
	}
}

func (a GcpMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.GcpCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a GcpMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GcpLogsAgentName is the logs-backend agent for GCP accounts that have no
// first-class log provider (Loki / Elasticsearch / Datadog) configured. The log
// dispatcher (getLogAgent) routes to it when GetLogProvider resolves the
// account's cloud type to "gcp". It reads Cloud Logging via the gcloud CLI and,
// like the AWS observability agent, discovers the log resource (resource.type /
// log name) in its ReAct loop before querying — mirroring the dedicated
// per-backend pattern of the metrics/traces dispatchers.
const GcpLogsAgentName = "gcp_logs"

func init() {
	core.RegisterNBAgentFactory(GcpLogsAgentName, func(accountId string) (core.NBAgent, error) {
		return newGcpLogsAgent(accountId), nil
	})
}

func newGcpLogsAgent(accountId string) core.NBAgent {
	return GcpLogsAgent{accountId: accountId}
}

type GcpLogsAgent struct {
	accountId string
}

func (a GcpLogsAgent) GetName() string { return GcpLogsAgentName }

func (a GcpLogsAgent) GetNameAliases() []string { return []string{"GCPLogs"} }

func (a GcpLogsAgent) GetDescription() string {
	return `Retrieves and analyzes GCP Cloud Logging entries (GKE container logs, Compute/serverless logs, Cloud SQL, load balancer, audit logs) via the gcloud CLI. Used as the logs backend for GCP accounts without a Loki/Elasticsearch/Datadog provider. Discovers the log resource type and labels before querying, and cites concrete log lines.`
}

func (a GcpLogsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's log question using GCP Cloud Logging. Cite concrete log lines and timestamps — never invent log content, resource names, or errors.",
		"**Tool name:** Your gcloud tool is `gcloud_execute`. Do NOT emit variants like `gcp_logging` or `gcp_logs_execute`. Use `shell_execute` only for `jq`/`grep` post-processing of large results.",
		"**Project is pre-configured** from the authenticated account credentials. Do NOT run `gcloud config set project`; pass `--project <project_id>` only to target a non-default project.",
		"**Primary read — `gcloud logging read '<filter>'`:** You MUST bound every read to keep output manageable:",
		"  - `--freshness=1h` (or `2h`/`24h`) for relative windows; or a `timestamp>=\"<RFC3339>\"` clause in the filter for absolute ranges. Prefer the `[[Time:-1h]]` / `[[Time:Now]]` macros over computing timestamps.",
		"  - `--limit=200` for routine viewing, up to `--limit=1000` for investigation. `--format=json` for parseable output.",
		"  - `--order=desc` (newest first) by default.",
		"**Filter construction — scope by resource.type + resource.labels (this is the GCP analog of an AWS log group / Azure workspace; discover it, don't assume):**",
		"  - GKE container: `resource.type=\"k8s_container\" resource.labels.namespace_name=\"NS\" resource.labels.pod_name=\"POD\"` (or `resource.labels.container_name=\"C\"`).",
		"**Workload vs pod (resolve the name yourself — this replaces the k8s resource_search step):** a cloud-only account has no in-cluster resource index, so you MUST resolve names via the log filter itself. If the user gives a workload/Deployment name rather than an exact pod (i.e. it lacks the `-<6-10 hex>-<5 alnum>` pod hash suffix), do NOT filter `resource.labels.pod_name=\"<workload>\"` — that matches zero entries because GKE pod names always carry the hash suffix. Instead match every pod of the workload with a regex, `resource.labels.pod_name=~\"<workload>-.*\"`, or filter by the workload label `labels.\"k8s-pod/app\"=\"<workload>\"`. Use an exact `pod_name=\"...\"` only when you already have the hashed pod name.",
		"  - Compute VM: `resource.type=\"gce_instance\" resource.labels.instance_id=\"ID\"`.",
		"  - Cloud Run: `resource.type=\"cloud_run_revision\" resource.labels.service_name=\"SVC\"`.",
		"  - Cloud SQL: `resource.type=\"cloudsql_database\" resource.labels.database_id=\"PROJECT:INSTANCE\"`.",
		"  - Errors only: add `severity>=ERROR`. Free-text: add a bare `\"<term>\"` or `textPayload:\"<term>\"` / `jsonPayload.message=~\"<regex>\"` clause.",
		"**Discovery when the resource is unknown:** run `gcloud logging read` with just `resource.type=\"...\"` and `--limit=5` to confirm entries exist, or `gcloud logging logs list --format='value(name)' --limit=50` to see which logs are populated. Discover ONCE, then build the scoped query — do not loop discovery.",
		"**Large output:** filter tightly at the API first (specific resource labels, short freshness, `severity`, `--limit`). When a result is still large, do NOT pull the full JSON into context — from `shell_execute`, run the read with its raw output redirected to a per-conversation workspace file (`gcloud logging read '...' --format=json > logs_gcp.json`; it persists across turns), then `jq`/`grep` that file for only the lines you need and re-read the file instead of re-running the query.",
		"**Investigation intent** (why/root-cause/failing/crash): pull a broad chronological window first (`--freshness=24h --limit=1000`, no error filter) so the trigger surfaces before the symptom, then narrow. **Routine intent** (recent/tail): a single small scoped read (`--freshness=1h --limit=200`).",
		"**Self-correction:** read `Stderr` on error — it names the fix (bad filter syntax, wrong label, missing resource.type). Correct and retry ONCE per distinct cause. On a permission error (`PERMISSION_DENIED`, missing `roles/logging.viewer`), report the missing role/scope as a finding — NEVER attempt to grant yourself IAM.",
	}

	constraints := []string{
		"Do not ask the user for clarification — resolve the resource via discovery reads.",
		"CRITICAL for stability: always bound reads with `resource.type`, a `--freshness`/timestamp window, and `--limit`.",
		"Empty result means 'no matching entries', not 'healthy' — state it explicitly and confirm the resource.type/log exists before concluding.",
		"MUST cite concrete log lines, timestamps, or error signatures. Preserve literal identifiers (host:port, resource names, error codes) verbatim.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteGcpCliCommand: {
			"Use **gcloud_execute** to run `gcloud logging read` and `gcloud logging logs list`.",
			"Always prefer this tool for log reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq`/`grep` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Show recent logs for pod checkout-7f8b9c-x2k in namespace prod",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud logging read 'resource.type=\"k8s_container\" resource.labels.namespace_name=\"prod\" resource.labels.pod_name=\"checkout-7f8b9c-x2k\"' --freshness=1h --limit=200 --order=desc --format=json",
					Explanation: "Routine read scoped to the exact GKE pod; small limit, 1h window.",
				},
			},
		},
		{
			Question: "Why is the payments Cloud Run service failing in the last few hours?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud logging read 'resource.type=\"cloud_run_revision\" resource.labels.service_name=\"payments\"' --freshness=24h --limit=1000 --order=desc --format=json",
					Explanation: "Investigation: broad chronological window, NO error filter, so the trigger surfaces alongside the symptom.",
				},
			},
		},
		{
			Question: "What log resource types are emitting for this project?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud logging logs list --format='value(name)' --limit=50",
					Explanation: "Discovery when the resource is unknown — enumerate populated logs once, then build a scoped read.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in GCP Cloud Logging investigation",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite exact log lines with timestamps. For investigations, include a short symptom → cause chain. Do NOT describe internal data flow.",
	}
}

func (a GcpLogsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.GcpCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a GcpLogsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GcpTracesAgentName is the traces-backend agent for GCP accounts that have no
// first-class trace provider (ClickHouse / Jaeger / Datadog) configured. The
// trace dispatcher (newTracesAgent) routes to it when GetTraceProvider resolves
// the account's cloud type to "gcp". Cloud Trace has no GA gcloud command, so it
// reads the v1 REST API through shell_execute with a gcloud access token — the
// same path the gcp orchestrator prompt documents.
const GcpTracesAgentName = "gcp_traces"

func init() {
	core.RegisterNBAgentFactory(GcpTracesAgentName, func(accountId string) (core.NBAgent, error) {
		return newGcpTracesAgent(accountId), nil
	})
}

func newGcpTracesAgent(accountId string) core.NBAgent {
	return GcpTracesAgent{accountId: accountId}
}

type GcpTracesAgent struct {
	accountId string
}

func (a GcpTracesAgent) GetName() string { return GcpTracesAgentName }

func (a GcpTracesAgent) GetNameAliases() []string { return []string{"GCPTraces"} }

func (a GcpTracesAgent) GetDescription() string {
	return `Retrieves and analyzes GCP Cloud Trace distributed traces (latency, slow spans, service dependencies) via the Cloud Trace v1 REST API, and correlates trace-tagged logs via Cloud Logging. Used as the traces backend for GCP accounts without a ClickHouse/Jaeger/Datadog provider.`
}

func (a GcpTracesAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's trace/latency question using GCP Cloud Trace. Cite concrete trace IDs, span names, and latencies — never invent them.",
		"**Cloud Trace has no GA gcloud command — read the v1 REST API via `shell_execute` with a gcloud access token:**",
		"  - List: `curl -s -H \"Authorization: Bearer $(gcloud auth print-access-token)\" 'https://cloudtrace.googleapis.com/v1/projects/<project_id>/traces?pageSize=20&startTime=[[Time:-1h]]&endTime=[[Time:Now]]&filter=...'`. Useful filters: `latency:500ms` (min latency), `+span:NAME`, `+root:URL`.",
		"  - Describe one trace: append `/TRACE_ID` to the list URL to get its full span tree.",
		"  - Requires `roles/cloudtrace.user` and the `cloud-platform` (or `trace.readonly`) OAuth scope. `ACCESS_TOKEN_SCOPE_INSUFFICIENT` means the scope is missing, NOT that traces don't exist. Run a `gcloud_execute` command first so the workspace has an active gcloud session for the token to succeed.",
		"**Tool name:** `gcloud_execute` runs gcloud subcommands only (use it for the auth session and for `gcloud logging read`); the Cloud Trace REST calls go through `shell_execute` (curl). Do NOT try to run curl via gcloud_execute — it rejects it.",
		"**Correlation path via Cloud Logging (when you have a trace id, or to find slow requests):** `gcloud logging read 'trace=\"projects/<project_id>/traces/TRACE_ID\"' --limit=50 --format=json` pulls the logs for a known trace; `gcloud logging read 'httpRequest.latency>\"0.5s\"' --freshness=1h --limit=50 --format=json` finds slow HTTP requests. This surfaces only traces whose services emit the `trace` field into logs — empty results mean 'no trace-correlated logs', not 'no traces' (check the Trace API).",
		"**Large output:** bound every read with a short time window, `pageSize`/`--limit`, and a latency/span filter. When the JSON is still large, redirect the raw `shell_execute` read to a per-conversation workspace file (`curl ... > traces_gcp.json`; it persists across turns) and `jq` that file for only the spans you need — do NOT reason over the full JSON in context.",
		"**Self-correction:** on an empty or unauthorized result, do NOT conclude the system is healthy — fall back to latency/error patterns in logs (`severity>=ERROR`, `httpRequest.latency`) narrowed by resource labels. Retry ONCE per distinct cause. On a permission error, report the missing role/scope as a finding — NEVER attempt to grant yourself IAM.",
	}

	constraints := []string{
		"Do not ask the user for clarification — resolve via the Trace API and log correlation.",
		"CRITICAL for stability: bound every read with a time window, a page/limit cap, and a latency/span filter.",
		"Empty result means 'no matching traces', not 'healthy' — state it explicitly and check the log-correlation path before concluding.",
		"MUST cite concrete trace IDs, span names, and latencies. Preserve literal identifiers verbatim.",
	}

	toolUsage := map[string][]string{
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for the Cloud Trace v1 REST API (curl with `gcloud auth print-access-token`), for `jq` post-processing, and for large-output offload to a workspace file (see **Large output**).",
		},
		tools.ToolExecuteGcpCliCommand: {
			"Use **gcloud_execute** to establish the gcloud session (any gcloud command) and to run `gcloud logging read` for trace-correlated logs.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Find slow traces (over 500ms) in the last hour",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud config get-value project",
					Explanation: "Establish the gcloud session (so the access token works) and confirm the project id.",
				},
				{
					Tool:        toolcore.ToolExecuteShellCommand,
					Input:       "curl -s -H \"Authorization: Bearer $(gcloud auth print-access-token)\" 'https://cloudtrace.googleapis.com/v1/projects/<project_id>/traces?pageSize=20&startTime=[[Time:-1h]]&endTime=[[Time:Now]]&filter=latency:500ms'",
					Explanation: "List slow traces via the Cloud Trace v1 REST API with a latency filter and bounded window.",
				},
			},
		},
		{
			Question: "Show the logs correlated with trace abc123",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGcpCliCommand,
					Input:       "gcloud logging read 'trace=\"projects/<project_id>/traces/abc123\"' --limit=50 --order=asc --format=json",
					Explanation: "Pull the trace-correlated logs for a known trace id via Cloud Logging.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in GCP Cloud Trace and latency investigation",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete trace IDs, span names, and latencies. Do NOT describe internal data flow.",
	}
}

func (a GcpTracesAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.GcpCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a GcpTracesAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
