package agents

import (
	"database/sql"
	"errors"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const SplunkEnterpriseMetricsAgentName = "splunk_enterprise_metrics"

// splunkEnterpriseProvider is the provider key api-server resolves Splunk Enterprise
// metrics under; the discovery tools send it so they enumerate Splunk's catalog rather
// than the "prometheus" default they otherwise assume.
const splunkEnterpriseProvider = "splunk_enterprise"

func init() {
	toolDescription := `Splunk Enterprise Metrics Expert. Translates natural language questions into Splunk SPL mstats searches and executes them against the Splunk metrics index to retrieve, analyze, and summarize CPU, memory, network, and custom metrics. Use when the configured metrics provider is Splunk Enterprise.`
	toolInput := "Provide a question in natural language to retrieve, analyze, or summarize metrics from Splunk Enterprise."
	toolOutput := "Returns metric data and summaries derived from Splunk mstats results."

	core.RegisterTargetAwareNBAgentFactoryAndTool(SplunkEnterpriseMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return SplunkEnterpriseMetricsAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

// SplunkEnterpriseMetricsAgent answers metric questions for Splunk Enterprise accounts.
//
// It is the Splunk counterpart of ElasticSearchMetricsAgent. Before it existed these
// accounts fell through to PrometheusAgent, whose only execution tool validates PromQL
// locally while the Splunk metric source accepts only `| mstats` SPL — so every value
// query was rejected by one side or the other, and only metric NAMES were reachable.
// AcceptsExecutionTarget: its tools follow a handed target and re-resolve the
// metrics provider and index for it, so the agent can run for another account.
func (a SplunkEnterpriseMetricsAgent) AcceptsExecutionTarget() {}

type SplunkEnterpriseMetricsAgent struct {
	accountId string
}

func (s SplunkEnterpriseMetricsAgent) GetName() string {
	return SplunkEnterpriseMetricsAgentName
}

func (s SplunkEnterpriseMetricsAgent) GetNameAliases() []string {
	return []string{"Splunk Metrics Reader", "Splunk Enterprise Metrics"}
}

func (s SplunkEnterpriseMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes metrics from Splunk Enterprise using SPL mstats searches.`
}

// splunkMetricsIndexLookup resolves the account's configured metrics index. A variable
// so tests can stub the database.
var splunkMetricsIndexLookup = lookupSplunkMetricsIndex

// lookupSplunkMetricsIndex reads `splunk_metric_index` from the account's enabled Splunk
// Enterprise integration.
//
// Read directly because the provider lookup's DefaultIndex does not carry it: api-server
// fills that from the generic `metrics_index` key, and Splunk stores its index under its
// own name. The api-server rejects any metric query that does not name this index as a
// quoted literal, so the model cannot write a working query without it. The ordering
// mirrors api-server's provider resolution: the account's default metrics provider
// first, then user-configured integrations.
func lookupSplunkMetricsIndex(accountId string) (string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("splunk metrics index: database: %w", err)
	}
	var index string
	err = dbms.Db.QueryRowx(`
		SELECT icv.value
		FROM integrations i
		JOIN integrations_cloud_accounts ia ON ia.integration_id = i.id
		JOIN integration_config_values icv ON icv.integration_id = i.id AND icv.name = 'splunk_metric_index'
		WHERE ia.cloud_account_id = $1 AND i.type = 'splunk_enterprise' AND i.status = 'enabled'
		ORDER BY ia.default_metrics_provider DESC NULLS LAST, CASE WHEN i.source = 'user' THEN 0 ELSE 1 END
		LIMIT 1`, accountId).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("splunk metrics index: query: %w", err)
	}
	return strings.TrimSpace(index), nil
}

func (s SplunkEnterpriseMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	accountID := promptAccountID(query, s.accountId)
	index, err := splunkMetricsIndexLookup(accountID)
	if err != nil && ctx != nil {
		ctx.GetLogger().Warn("splunk_enterprise_metrics: could not resolve metrics index", "accountId", accountID, "error", err)
	}
	return s.buildSystemPrompt(index)
}

// buildSystemPrompt renders the prompt for a resolved metrics index. Split from
// GetSystemPrompt so the index interpolation can be asserted without a database.
func (s SplunkEnterpriseMetricsAgent) buildSystemPrompt(index string) core.NBAgentPrompt {
	if index == "" {
		return core.NBAgentPrompt{
			Role: "an SRE expert specializing in Splunk Enterprise metric analysis in Kubernetes environments",
			Instructions: []string{
				"This account's Splunk Enterprise integration has no metrics index configured, so there are no metrics to query.",
				"Tell the user plainly that Splunk Enterprise metrics are not configured for this account (the integration's Splunk Metric Index setting is empty). Do NOT report 'no data' or guess an index.",
			},
			OutputFormat: "One short paragraph stating that Splunk Enterprise metrics are not configured for this account.",
		}
	}

	idx := fmt.Sprintf(`index="%s"`, index)
	// Example inputs are JSON tool calls, so the index's quotes are escaped there.
	idxJSON := fmt.Sprintf(`index=\"%s\"`, index)

	instructions := []string{
		"**Your Primary Goal:** Answer user questions about metrics stored in Splunk Enterprise.",
		fmt.Sprintf("**Your Method:** Write an SPL `| mstats` search and execute it via `%s`. This is SPL, NOT PromQL — PromQL syntax (`rate()`, `{label=\"x\"}`) will be rejected.", tools.ToolSplunkMetricsQuery),
		"**Query Rules (the backend rejects any query that breaks them):**",
		"  1. Start with `| mstats`. Which metrics, dimensions or values exist is answered by the discovery tools below, never by SPL.",
		fmt.Sprintf("  2. Name the metrics index exactly as `%s` in the WHERE clause. No other index can be queried.", idx),
		"  3. Alias the value `AS nb_value` — e.g. `avg(k8s.pod.cpu.usage) AS nb_value`. Values in any other column are dropped. One metric per query.",
		"  4. Pass the time window as the tool's `range` (e.g. `15m`, `1h`) or `start_time`/`end_time`. Do NOT put earliest/latest in the SPL.",
		"  5. Use `span=` for time series (`span=1m` up to 1h, `span=5m` up to 24h, `span=1h` beyond). Omit `span` for a single aggregate value over the whole window.",
		"**Dimensions (OpenTelemetry resource attributes, dots intact — never underscores):**",
		"  `k8s.namespace.name`, `k8s.pod.name`, `k8s.deployment.name`, `k8s.container.name`, `k8s.node.name`, `k8s.cluster.name`, `service.name`.",
		"  Filter with `=` / `!=` and double quotes. For a workload's pods use a prefix wildcard: `k8s.pod.name=\"api-server*\"`, or `k8s.deployment.name=\"api-server\"` when that dimension exists. Group with `BY k8s.pod.name`.",
		"**Common Metric Names (OTel collector; the exact set depends on what this account ships):**",
		"  **CPU:** `k8s.pod.cpu.usage` (cores), `k8s.pod.cpu.utilization`, `container.cpu.usage`, `container.cpu.utilization`, `k8s.node.cpu.usage`",
		"  **Memory:** `k8s.pod.memory.usage` (bytes), `k8s.pod.memory.working_set`, `container.memory.usage`, `container.memory.working_set`, `k8s.node.memory.usage`",
		"  **Network:** `k8s.pod.network.io`, `k8s.pod.network.rx_bytes`, `k8s.pod.network.errors`",
		"**Discovery (metric names and dimensions are environment-specific — discover, don't assume):**",
		fmt.Sprintf("  - For a value question about a named workload, query the common metric names above first. If that mstats query returns empty for a KNOWN workload, do NOT conclude 'no data': call `%s` with the workload (and namespace). It returns the metric families that actually have series for it, grouped by the dimensions that identify it. Pick a family from a group and filter it by THAT group's `namespace_label`/`workload_label` (match_type `prefix` means `<workload_label>=\"<workload>*\"`). Do not mix dimensions across groups.", tools.ToolMetricsSeriesMatch),
		fmt.Sprintf("  - When the question itself is which metrics a named workload has, `%s` is the answer — call it directly.", tools.ToolMetricsSeriesMatch),
		fmt.Sprintf("  - Use `%s` (keyword, e.g. `cpu`, `memory`) and `%s` (a metric's dimensions) only when you do NOT have a concrete workload name. `%s` lists a dimension's values.", tools.ToolMetricsList, tools.ToolMetricsLabelsList, tools.ToolMetricsLabelValues),
		fmt.Sprintf("  - If `%s` answers \"No metric series found\", the workload has no metrics in this index for the window: say so and stop. If it answers \"Inconclusive\", do not conclude that.", tools.ToolMetricsSeriesMatch),
		"**CPU utilization:** `k8s.pod.cpu.usage` is CPU cores in use (1.0 = one full core). Report it as cores (or millicores), and as a percentage only if a limit/utilization metric exists to divide by — never invent a limit.",
		fmt.Sprintf("**Self-Correction (when `%s` returns an error or empty `payload`):**", tools.ToolSplunkMetricsQuery),
		"  1. Read the error text — it names the rule that was broken. Fix exactly that.",
		fmt.Sprintf("  2. Empty result for a named workload: call `%s` (see Discovery) rather than guessing another name. Without a workload name, confirm the metric name with `%s`, then the dimension value with `%s`.", tools.ToolMetricsSeriesMatch, tools.ToolMetricsList, tools.ToolMetricsLabelValues),
		"  3. Widen the time range: `15m` → `1h` → `6h`. Drop filters one at a time.",
		"  NEVER retry the same query unchanged. Maximum 4 retries total.",
		"**Final Answer:** Report the metric values (peak / avg / latest) per pod or series in a concise SRE-style summary, and name the metric(s) you queried.",
	}

	constraints := []string{
		fmt.Sprintf("You MUST execute every value query via `%s` before answering.", tools.ToolSplunkMetricsQuery),
		fmt.Sprintf("Every mstats query MUST contain `%s` and `AS nb_value`.", idx),
		"NEVER write PromQL. NEVER query event indexes (logs/traces) for metric values.",
		fmt.Sprintf("NEVER report 'no data' after only one query with guessed names — for a named workload, `%s` must confirm it first; otherwise verify the metric and dimension values.", tools.ToolMetricsSeriesMatch),
		"Do not ask the user for clarification — make the best assumption and proceed.",
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "CPU usage of the api-server pods in namespace nudgebee over the last 15 minutes",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolSplunkMetricsQuery,
					Input:       fmt.Sprintf(`{"command":"| mstats avg(k8s.pod.cpu.usage) AS nb_value WHERE %s AND k8s.namespace.name=\"nudgebee\" AND k8s.pod.name=\"api-server*\" span=1m BY k8s.pod.name","range":"15m"}`, idxJSON),
					Explanation: "Pod CPU in cores, one series per pod, 1-minute buckets.",
				},
			},
			Explanation: "Standard pod CPU: mstats avg over k8s.pod.cpu.usage, filtered by namespace and pod prefix, grouped by pod.",
		},
		{
			Question: "Which pods in namespace demo use the most memory right now?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolMetricsList,
					Input:       "memory",
					Explanation: "Confirm which memory metric this index carries.",
				},
				{
					Tool:        tools.ToolSplunkMetricsQuery,
					Input:       fmt.Sprintf(`{"command":"| mstats max(k8s.pod.memory.usage) AS nb_value WHERE %s AND k8s.namespace.name=\"demo\" BY k8s.pod.name","range":"15m"}`, idxJSON),
					Explanation: "No span: one peak value per pod over the window.",
				},
			},
			Explanation: "Point-in-time ranking: omit span and group by pod.",
		},
		{
			Question: "What metrics does the payments service emit in namespace prod?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolMetricsSeriesMatch,
					Input:       `{"workload":"payments","namespace":"prod"}`,
					Explanation: "One call answers it: the families grouped by the dimensions that found them.",
				},
			},
			Explanation: "A which-metrics question about a named workload is a series match, not an SPL search.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Splunk Enterprise metric analysis in Kubernetes environments",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage: map[string][]string{
			tools.ToolSplunkMetricsQuery: {
				"Executes an SPL `| mstats` search against the Splunk metrics index.",
				fmt.Sprintf("Input: `command` (SPL containing `%s` and `AS nb_value`) and `range` (e.g. `15m`).", idx),
				"Output: Metric series (labels, timestamps, values) with min/max/avg stats.",
			},
			tools.ToolMetricsList: {
				"Lists metric names in the Splunk metrics index.",
				"Input: optional keyword filter (e.g. `cpu`).",
				"Output: Metric names. Use to confirm a name before querying.",
			},
			tools.ToolMetricsLabelsList: {
				"Lists the dimensions available on a metric.",
				"Input: metric name.",
				"Output: Dimension names.",
			},
			tools.ToolMetricsSeriesMatch: {
				"DETERMINISTIC discovery for a NAMED workload: the metric families that actually have series for it, grouped by the Splunk dimensions that identify it.",
				"Use it when an mstats query returns empty for a workload you can name, or when the question is which metrics that workload has.",
				"Input: workload (required), namespace (recommended).",
				"Output: groups of {namespace_label, workload_label, match_type, families[]}. Filter a family by its group's dimensions; match_type `prefix` => `<workload_label>=\"<workload>*\"`.",
			},
		},
		Examples:     examples,
		OutputFormat: "Markdown SRE-style summary with metric values (peak / avg / latest) per series and the metric names queried. Do not include raw SPL unless asked.",
	}
}

func (s SplunkEnterpriseMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.SplunkMetricsQueryTool{},
		tools.MetricsListTool{Provider: splunkEnterpriseProvider},
		tools.ListMetricsLabelsTool{Provider: splunkEnterpriseProvider},
		tools.ListMetricsLabelValuesTool{Provider: splunkEnterpriseProvider},
		tools.MetricsSeriesMatchTool{Provider: splunkEnterpriseProvider},
	}
}

func (s SplunkEnterpriseMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations allows discover → query → self-correct → query → answer.
func (s SplunkEnterpriseMetricsAgent) GetMaxIterations() int {
	return 10
}

func (s SplunkEnterpriseMetricsAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

// UpdateToolResponseForPlanner reuses the ES agent's compaction: both tools return the
// same summarised {results:[{payload:[{metric,stats,values}]}]} shape.
func (s SplunkEnterpriseMetricsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolSplunkMetricsQuery) {
		toolRequest.Tool = tools.ToolESMetricsQuery
	}
	return ElasticSearchMetricsAgent(s).UpdateToolResponseForPlanner(toolRequest, toolResponse)
}
