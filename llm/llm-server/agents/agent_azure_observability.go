package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// AzureMetricsAgentName is the metrics-backend agent for Azure accounts that have
// no first-class metrics provider (Prometheus / Datadog / Elasticsearch) configured.
// The metrics dispatcher (newMetricsAgent) routes to it when GetMetricsProvider
// resolves the account's cloud type to "azure". It answers metric questions using
// Azure Monitor via the az CLI, mirroring the dedicated-per-backend pattern of
// datadog_metrics / elastic_search_metrics.
const AzureMetricsAgentName = "azure_metrics"

func init() {
	core.RegisterNBAgentFactory(AzureMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return newAzureMetricsAgent(accountId), nil
	})
}

func newAzureMetricsAgent(accountId string) core.NBAgent {
	return AzureMetricsAgent{accountId: accountId}
}

type AzureMetricsAgent struct {
	accountId string
}

func (a AzureMetricsAgent) GetName() string { return AzureMetricsAgentName }

func (a AzureMetricsAgent) GetNameAliases() []string { return []string{"AzureMetrics"} }

func (a AzureMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes Azure Monitor metrics (CPU, memory, disk, network, DTU, request latency/error rate, managed-service metrics) via the az CLI. Used as the metrics backend for Azure accounts without a Prometheus/Datadog/Elasticsearch provider. Handles its own metric-name and resource discovery.`
}

func (a AzureMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's question using Azure Monitor metrics. Base every conclusion on CLI output — never invent metric values, resource IDs, or time ranges.",
		"**Tool name:** Your az tool is `azure_execute`. Do NOT emit variants like `azure_monitor` or `azure_metrics_execute` — the planner will fail to resolve them. Use `shell_execute` only for `jq` post-processing of large responses.",
		"**Primary read — `az monitor metrics list`:** numeric time-series metrics (CPU, memory, DTU, latency). This is NOT `az monitor activity-log list` (control-plane audit events) nor `az monitor metrics alert list` (configured alert rules) — pick the one that matches intent.",
		"  - `--resource` identifies the target. Prefer the short form (`--resource <name> --resource-group <rg> --resource-type <provider/type>`) over a full `/subscriptions/.../providers/...` ID when both work.",
		"  - `--metric` names the metric (e.g. `\"Percentage CPU\"`, `\"Available Memory Bytes\"`). Run `az monitor metrics list-definitions --resource ...` first when unsure of the exact metric name — names are resource-type-specific.",
		"  - `--start-time` / `--end-time` bound the window. Use the `[[Time:-1h]]` / `[[Time:Now]]` macros — NEVER compute timestamps yourself.",
		"  - `--interval` sets aggregation granularity (e.g. `PT1M`, `PT5M`); `--aggregation` selects Average/Maximum/Total.",
		"**Common metrics by resource type (discover, don't assume):**",
		"  - VM (`Microsoft.Compute/virtualMachines`): `\"Percentage CPU\"`, `\"Available Memory Bytes\"`, `\"Network In Total\"`, `\"OS Disk IOPS Consumed Percentage\"`",
		"  - App Service (`Microsoft.Web/sites`): `\"CpuTime\"`, `\"MemoryWorkingSet\"`, `\"Http5xx\"`, `\"AverageResponseTime\"`",
		"  - SQL DB (`Microsoft.Sql/servers/databases`): `\"cpu_percent\"`, `\"dtu_consumption_percent\"`, `\"storage_percent\"`",
		"  - AKS (`Microsoft.ContainerService/managedClusters`): `\"node_cpu_usage_percentage\"`, `\"node_memory_working_set_percentage\"`",
		"  If a metric name returns no data, list what exists: `az monitor metrics list-definitions --resource <id> --query \"[].name.value\" -o tsv`, then pick the right name.",
		"**Large output:** metric responses can be large. Filter at the CLI level first (single `--metric`, tight `--start-time`, coarse `--interval`, `--aggregation`). When still big, do NOT pull the full JSON into context — from `shell_execute`, run the read with its raw output redirected to a per-conversation workspace file (`az monitor metrics list ... --output json > metrics_azure.json`; it persists across turns), then `jq` that file for only the timeseries points you need.",
		"**Self-correction:** On an error, read it carefully — 'invalid argument' means check flag names, 'not found' means verify resource group/subscription scope. Fix and retry ONCE per distinct cause; never repeat an identical failing command. On a permission error (`AuthorizationFailed` / 403), report the missing role (e.g. `Monitoring Reader`) and scope as a finding — NEVER attempt to grant yourself access. Do NOT run `az extension add` — `az monitor metrics list` works without extensions.",
	}

	constraints := []string{
		"Do not ask the user for clarification — resolve using the available tools (metric-definition discovery, resource lookup).",
		"CRITICAL for stability: always bound queries with a single `--metric`, a short window, and a coarse `--interval` to prevent context saturation.",
		"Empty result means 'no data for these parameters', not 'healthy' — state it explicitly and confirm the metric definition exists before concluding.",
		"Base the final answer only on values observed in CLI output. Never invent resource IDs or metric values.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAzureCliCommand: {
			"Use **azure_execute** to run `az monitor metrics list` and `az monitor metrics list-definitions`.",
			"Always prefer this tool for metric reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "What is the CPU percentage of VM my-vm in resource group my-rg over the last hour?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor metrics list --resource my-vm --resource-group my-rg --resource-type Microsoft.Compute/virtualMachines --metric \"Percentage CPU\" --start-time [[Time:-1h]] --end-time [[Time:Now]] --interval PT5M --aggregation Average --output json",
					Explanation: "Standard Azure Monitor read scoped to one metric, resource, and a 1h window.",
				},
			},
		},
		{
			Question: "Show DTU consumption for SQL database mydb on server mysrv in resource group my-rg",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor metrics list --resource mydb --resource-group my-rg --resource-type Microsoft.Sql/servers/databases --namespace Microsoft.Sql/servers/databases --metric \"dtu_consumption_percent\" --start-time [[Time:-6h]] --end-time [[Time:Now]] --interval PT15M --aggregation Average --output json",
					Explanation: "SQL DB metric with the correct resource-type/namespace and a coarse interval over 6h.",
				},
			},
		},
		{
			Question: "What metrics are available for my App Service my-app in resource group my-rg?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor metrics list-definitions --resource my-app --resource-group my-rg --resource-type Microsoft.Web/sites --query \"[].name.value\" -o tsv",
					Explanation: "Metric-definition discovery when the exact metric name is unknown.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Azure Monitor metrics",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete metric values with their timestamps. Do NOT describe internal data flow.",
	}
}

func (a AzureMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.AzureCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a AzureMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// AzureLogsAgentName is the logs-backend agent for Azure accounts that have no
// first-class log provider (Loki / Elasticsearch / Datadog) configured. The log
// dispatcher (getLogAgent) routes to it when GetLogProvider resolves the
// account's cloud type to "azure". It reads logs via the az CLI and, like the
// AWS observability agent, discovers the log source (Log Analytics workspace /
// App Service / activity log) in its ReAct loop before querying — Azure has no
// single universal log-read command, so the discover-then-query loop is
// essential here.
const AzureLogsAgentName = "azure_logs"

func init() {
	core.RegisterNBAgentFactory(AzureLogsAgentName, func(accountId string) (core.NBAgent, error) {
		return newAzureLogsAgent(accountId), nil
	})
}

func newAzureLogsAgent(accountId string) core.NBAgent {
	return AzureLogsAgent{accountId: accountId}
}

type AzureLogsAgent struct {
	accountId string
}

func (a AzureLogsAgent) GetName() string { return AzureLogsAgentName }

func (a AzureLogsAgent) GetNameAliases() []string { return []string{"AzureLogs"} }

func (a AzureLogsAgent) GetDescription() string {
	return `Retrieves and analyzes Azure logs (Log Analytics / AKS container logs, App Service logs, VM diagnostics, Activity Log) via the az CLI. Used as the logs backend for Azure accounts without a Loki/Elasticsearch/Datadog provider. Discovers the log source (Log Analytics workspace or resource) before querying, and cites concrete log lines.`
}

func (a AzureLogsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's log question using Azure logs. Cite concrete log lines and timestamps — never invent log content, resource IDs, or errors.",
		"**Tool name:** Your az tool is `azure_execute`. Do NOT emit variants like `azure_monitor` or `azure_logs_execute`. Use `shell_execute` only for `jq`/`grep` post-processing of large results.",
		"**Azure has no single universal log-read command — pick the source that matches the resource, discovering it first (this is the Azure analog of an AWS log group / GCP resource.type):**",
		"  1. **Log Analytics (primary for AKS container logs and any resource with diagnostic settings routed to a workspace):** first DISCOVER the workspace — `az monitor log-analytics workspace list --query \"[].{name:name,rg:resourceGroup,cid:customerId}\" -o json`. Then query with KQL: `az monitor log-analytics query --workspace <customerId> --analytics-query \"<KQL>\" --output json`. Example KQL for AKS: `ContainerLogV2 | where PodName == \"POD\" | where TimeGenerated > ago(1h) | project TimeGenerated, LogMessage | take 200`.",
		"  1a. **Workload vs pod (resolve the name yourself — this replaces the k8s resource_search step):** if the user gives a workload/Deployment name rather than an exact pod, do NOT use `PodName == \"<workload>\"` (zero rows — AKS pod names carry a replicaset+pod hash suffix). Match every pod of the workload with `ContainerLogV2 | where PodName startswith \"<workload>-\"` (add `| where PodNamespace == \"<ns>\"` when known). Use `PodName == \"...\"` only when you already have the exact hashed pod name.",
		"  2. **App Service / Function App:** `az webapp log show` for config, and for recent entries prefer Log Analytics (AppServiceConsoleLogs / AppServiceHTTPLogs tables) if diagnostics are routed there. `az webapp log tail` is streaming (not suitable for a one-shot read).",
		"  3. **VM:** OS-level via `az vm run-command invoke` (detect OS first) or boot diagnostics; diagnostics-extension logs land in the Log Analytics workspace.",
		"  4. **Control-plane audit (who changed what) — no workspace needed:** `az monitor activity-log list --resource-group <rg> --start-time [[Time:-24h]] --end-time [[Time:Now]] -o json`.",
		"**KQL discipline:** always bound with `| where TimeGenerated > ago(<window>)` and `| take <N>` (200 routine, up to 1000 investigation). Filter to the specific resource (PodName / Resource / _ResourceId) before projecting columns.",
		"**Time Macros:** NEVER compute dates. Use `[[Time:Now]]`, `[[Time:-1h]]`, `[[Time:-24h]]` in `az` flags; use KQL `ago(1h)` inside `--analytics-query`.",
		"**Large output:** filter at the query level first (specific resource, short window, `take`, projected columns). When still large, do NOT pull the full JSON into context — from `shell_execute`, run the read with its raw output redirected to a per-conversation workspace file (`az monitor log-analytics query ... --output json > logs_azure.json`; it persists across turns), then `jq`/`grep` that file for only the lines you need and re-read the file instead of re-running the query.",
		"**Investigation intent** (why/root-cause/failing/crash): pull a broad chronological window first (`ago(24h)`, `take 1000`, no error filter) so the trigger surfaces before the symptom, then narrow. **Routine intent** (recent/tail): a single small scoped query (`ago(1h)`, `take 200`).",
		"**Self-correction:** read the error — 'workspace not found' means discover it first; 'no such table' means the resource's diagnostics aren't routed to that workspace (try activity-log or another table). Retry ONCE per distinct cause. On `AuthorizationFailed`/403, report the missing role (e.g. `Log Analytics Reader`, `Monitoring Reader`) and scope — NEVER attempt to grant yourself access. Do NOT run `az extension add`.",
	}

	constraints := []string{
		"Do not ask the user for clarification — discover the workspace/resource via az list commands.",
		"CRITICAL for stability: always bound KQL with `where TimeGenerated > ago(...)` and `take N`, and scope to the specific resource.",
		"Empty result means 'no matching entries', not 'healthy' — confirm the workspace/table exists and diagnostics are configured before concluding.",
		"MUST cite concrete log lines, timestamps, or error signatures. Preserve literal identifiers verbatim.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAzureCliCommand: {
			"Use **azure_execute** to run `az monitor log-analytics workspace list`, `az monitor log-analytics query`, and `az monitor activity-log list`.",
			"Always prefer this tool for log reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq`/`grep` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Show recent logs for pod checkout in AKS",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor log-analytics workspace list --query \"[].{name:name,rg:resourceGroup,cid:customerId}\" -o json",
					Explanation: "Discover the Log Analytics workspace first — its customerId is required to query.",
				},
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor log-analytics query --workspace <customerId> --analytics-query 'ContainerLogV2 | where PodName has \"checkout\" | where TimeGenerated > ago(1h) | project TimeGenerated, LogMessage | take 200' --output json",
					Explanation: "Scoped KQL read against the discovered workspace, bounded window and take.",
				},
			},
		},
		{
			Question: "Who modified resources in resource group my-rg in the last 24 hours?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor activity-log list --resource-group my-rg --start-time [[Time:-24h]] --end-time [[Time:Now]] --query \"[].{time:eventTimestamp,caller:caller,op:operationName.value,status:status.value}\" -o json",
					Explanation: "Control-plane audit needs no workspace — Activity Log answers 'who changed what'.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Azure log investigation (Log Analytics / KQL)",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite exact log lines with timestamps. For investigations, include a short symptom → cause chain. Do NOT describe internal data flow.",
	}
}

func (a AzureLogsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.AzureCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a AzureLogsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// AzureTracesAgentName is the traces-backend agent for Azure accounts that have
// no first-class trace provider (ClickHouse / Jaeger / Datadog) configured. The
// trace dispatcher (newTracesAgent) routes to it when GetTraceProvider resolves
// the account's cloud type to "azure". Azure distributed tracing lives in
// Application Insights, so it discovers the App Insights component first, then
// queries dependencies/requests via KQL — partial by nature (only answers when
// App Insights is configured for the workload).
const AzureTracesAgentName = "azure_traces"

func init() {
	core.RegisterNBAgentFactory(AzureTracesAgentName, func(accountId string) (core.NBAgent, error) {
		return newAzureTracesAgent(accountId), nil
	})
}

func newAzureTracesAgent(accountId string) core.NBAgent {
	return AzureTracesAgent{accountId: accountId}
}

type AzureTracesAgent struct {
	accountId string
}

func (a AzureTracesAgent) GetName() string { return AzureTracesAgentName }

func (a AzureTracesAgent) GetNameAliases() []string { return []string{"AzureTraces"} }

func (a AzureTracesAgent) GetDescription() string {
	return `Retrieves and analyzes Azure distributed traces via Application Insights (request/dependency latency, failures, end-to-end transactions) using the az CLI. Used as the traces backend for Azure accounts without a ClickHouse/Jaeger/Datadog provider. Only answers when Application Insights is configured for the workload.`
}

func (a AzureTracesAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's trace/latency question using Azure Application Insights. Cite concrete operation names, durations, and failure counts — never invent them.",
		"**Tool name:** Your az tool is `azure_execute`. Use `shell_execute` only for `jq`/`grep` post-processing.",
		"**Azure distributed tracing lives in Application Insights — discover the component first, then query its tables with KQL:**",
		"  1. **Discover:** `az monitor app-insights component list --query \"[].{name:name,rg:resourceGroup,appId:appId}\" -o json` (add `--resource-group <rg>` to scope). The `appId` identifies the component for queries.",
		"  2. **Query dependencies/requests via KQL:** `az monitor app-insights query --app <appId> --analytics-query \"<KQL>\" --output json`. Key tables: `requests` (inbound calls: name, duration, resultCode, success), `dependencies` (outbound calls to DB/HTTP: target, duration, success), `exceptions`.",
		"  - Slow requests: `requests | where timestamp > ago(1h) | where duration > 500 | project timestamp, name, duration, resultCode | order by duration desc | take 20`.",
		"  - Failing dependencies: `dependencies | where timestamp > ago(1h) | where success == false | summarize count() by target, name | order by count_ desc | take 20`.",
		"  - End-to-end for one operation: filter both tables by `operation_Id`.",
		"**If Application Insights is NOT configured** (component list returns empty, or the app has no instrumentation): say so explicitly — distributed traces are unavailable for this workload. Fall back to latency signals in Azure Monitor metrics (`AverageResponseTime`, `Http5xx`) via the metrics path, and note that App Insights instrumentation would be needed for span-level traces. Do NOT invent traces.",
		"**Time Macros:** NEVER compute dates. Use `ago(1h)`/`ago(24h)` inside `--analytics-query`; use `[[Time:-1h]]`/`[[Time:Now]]` for any `az` flags.",
		"**Large output:** bound KQL with `where timestamp > ago(<window>)`, `take <N>`, and a duration/success filter; project only needed columns. When still large, redirect the raw `shell_execute` read to a per-conversation workspace file (`az monitor app-insights query ... --output json > traces_azure.json`; it persists across turns) and `jq` that file for only the rows you need — do NOT reason over the full JSON in context.",
		"**Self-correction:** 'app not found' → discover the component first. 'no such table' → the app has no App Insights data. Retry ONCE per distinct cause. On `AuthorizationFailed`/403, report the missing role (e.g. `Monitoring Reader`) and scope — NEVER attempt to grant yourself access. Do NOT run `az extension add` (application-insights ships with recent az).",
	}

	constraints := []string{
		"Do not ask the user for clarification — discover the App Insights component via az list/show.",
		"CRITICAL for stability: always bound KQL with `where timestamp > ago(...)`, `take N`, and a duration/success filter.",
		"Empty result means 'no matching traces' or 'App Insights not configured' — state which explicitly; never conclude 'healthy' from an empty trace query.",
		"MUST cite concrete operation names, durations, and failure counts. Preserve literal identifiers verbatim.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAzureCliCommand: {
			"Use **azure_execute** to run `az monitor app-insights component list` and `az monitor app-insights query`.",
			"Always prefer this tool for trace reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq`/`grep` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Find slow requests in the last hour",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor app-insights component list --query \"[].{name:name,rg:resourceGroup,appId:appId}\" -o json",
					Explanation: "Discover the Application Insights component first — its appId is required to query.",
				},
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor app-insights query --app <appId> --analytics-query 'requests | where timestamp > ago(1h) | where duration > 500 | project timestamp, name, duration, resultCode | order by duration desc | take 20' --output json",
					Explanation: "Scoped KQL over the requests table, bounded window and take, ordered by duration.",
				},
			},
		},
		{
			Question: "Which dependencies are failing?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAzureCliCommand,
					Input:       "az monitor app-insights query --app <appId> --analytics-query 'dependencies | where timestamp > ago(1h) | where success == false | summarize count() by target, name | order by count_ desc | take 20' --output json",
					Explanation: "Aggregate failing outbound dependencies by target over a bounded window.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Azure Application Insights distributed tracing",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete operation names, durations, and failure counts. Do NOT describe internal data flow.",
	}
}

func (a AzureTracesAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.AzureCliTool{},
		tools.ShellTool{AccountId: a.accountId},
	}
}

func (a AzureTracesAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
