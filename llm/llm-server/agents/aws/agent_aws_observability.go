package aws

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"
)

// Agent Constants
const (
	AgentAwsObservabilityName = "aws_observability"
	AwsMetricsAgentName       = "aws_metrics"
	AwsLogsAgentName          = "aws_logs"
	AwsTracesAgentName        = "aws_traces"
)

func init() {
	toolDescription := `Expert AWS Observability troubleshooting agent.

Specializes in:
- CloudWatch Logs query, log groups, log streams, filtering
- CloudWatch Metrics analysis, custom metrics, anomaly detection
- CloudWatch Alarms configuration and state
- X-Ray traces and service maps
- CloudTrail API audit logs for root cause analysis
- EventBridge rules and event patterns
- SNS/SQS for alerting pipelines

Uses structured output with evidence citation to prevent hallucinations.
Queries AWS best practices knowledge base for common patterns.
Fact-checks all claims before responding.`

	toolInput := "Natural language query about AWS observability issues (CloudWatch Logs, Metrics, Alarms, X-Ray, CloudTrail)"
	toolOutput := "Structured diagnosis with evidence, confidence level, and remediation recommendations"

	core.RegisterNBAgentFactoryAndTool(AgentAwsObservabilityName, func(accountId string) (core.NBAgent, error) {
		return newAwsObservabilityAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)

	core.RegisterNBAgentFactory(AwsMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return NewAwsMetricsAgent(accountId), nil
	})

	core.RegisterNBAgentFactory(AwsLogsAgentName, func(accountId string) (core.NBAgent, error) {
		return NewAwsLogsAgent(accountId), nil
	})

	core.RegisterNBAgentFactory(AwsTracesAgentName, func(accountId string) (core.NBAgent, error) {
		return NewAwsTracesAgent(accountId), nil
	})
}

// -----------------------------------------------------------------------------
// Legacy Monolithic AWS Observability Agent
// -----------------------------------------------------------------------------

func newAwsObservabilityAgent(accountId string) core.NBAgent {
	return &AwsObservabilityAgent{
		accountId: accountId,
	}
}

type AwsObservabilityAgent struct {
	accountId string
}

func (a *AwsObservabilityAgent) GetName() string {
	return AgentAwsObservabilityName
}

func (a *AwsObservabilityAgent) GetNameAliases() []string {
	return []string{"aws_obs", "cloudwatch_agent", "observability_troubleshoot"}
}

func (a *AwsObservabilityAgent) GetDescription() string {
	return `Expert AWS Observability troubleshooting agent for CloudWatch Logs, Metrics, Alarms, X-Ray traces, and CloudTrail audit logs.`
}

func (a *AwsObservabilityAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{}

	// Add AWS CLI executor tool
	if awsExecuteTool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteAwsCliCommand); ok {
		supportedTools = append(supportedTools, awsExecuteTool)
	}

	if kbAgent, ok := toolcore.GetNBTool(a.accountId, "websearch"); ok {
		supportedTools = append(supportedTools, kbAgent)
	}

	return supportedTools
}

func (a *AwsObservabilityAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Load prompt from prompts repository
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAwsObservability, query.AccountId)
	if promptErr != nil {
		ctx.GetLogger().Error("aws observability: system prompt failed to load", "error", promptErr)
	}
	instructions := strings.Split(promptText, "\n")

	constraints := []string{
		"CRITICAL: NEVER invent log content, errors, or stack traces - use EXACT QUOTES from CLI output only",
		"Report format: 'Ran: [command] → Result: [actual output] → Analysis: [what it shows]'",
		"Time formats: Logs = epoch ms (13 digits), CloudTrail = ISO 8601, Logs Insights = epoch sec (10 digits)",
		"Filter pattern OR: \"?ERROR ?EXCEPTION\" NOT \"ERROR OR EXCEPTION\"",
		"Evidence-based: cite CLI command + raw output for every claim, never speculate",
		"Logs Insights: poll get-query-results max 3 times. If status != 'Complete' after 3 polls, report partial results and move on.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAwsCliCommand: {
			"Use aws_execute to run AWS CLI commands (logs, cloudwatch, cloudtrail, xray)",
			"Primary tool for CloudWatch Logs (filter-log-events, tail) and CloudTrail (lookup-events)",
		},
		"websearch": {
			"Query web/docs/skills for CloudWatch patterns and troubleshooting guides",
			"Example queries: 'CloudWatch Logs filter syntax', 'CloudWatch alarm best practices', 'X-Ray sampling'",
			"Use websearch content to inform diagnosis but always verify against actual AWS state",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Find and query application logs for errors (log group unknown)",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs describe-log-groups --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs describe-log-groups --log-group-name-prefix /demo/ --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs filter-log-events --log-group-name /demo/backend-app --filter-pattern \"?ERROR ?Exception\" --max-items 100 --region us-east-1",
				},
			},
			Explanation: "MANDATORY: Discover log groups FIRST before querying. Step 1: List all log groups. Step 2: Narrow by prefix (e.g., /demo/, /aws/lambda/). Step 3: ONLY after confirming log group exists, query with filter-log-events. NEVER skip discovery.",
		},
		{
			Question: "Get CloudWatch logs from /aws/ec2/Frontend-App for the last hour with errors",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs describe-log-groups --log-group-name-prefix /aws/ec2/Frontend-App --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs filter-log-events --log-group-name /aws/ec2/Frontend-App --start-time 1704754800000 --end-time 1704758400000 --filter-pattern \"?ERROR\" --region us-east-1",
				},
			},
			Explanation: "Even when log group name is provided, verify it exists first with describe-log-groups. Then use epoch milliseconds (13 digits) for time range. Use ?ERROR for OR pattern, NOT 'ERROR OR EXCEPTION' (wrong syntax).",
		},
	}

	return core.NBAgentPrompt{
		Role:         "expert AWS Observability troubleshooting specialist",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (a *AwsObservabilityAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentObservabilityMaxIterations; v > 0 {
		return v
	}
	return 7
}

func (a *AwsObservabilityAgent) GetTimeout() time.Duration {
	if v := config.Config.LLMServerAgentObservabilityTimeoutSeconds; v > 0 {
		return time.Duration(v) * time.Second
	}
	return 3 * time.Minute
}

func (a *AwsObservabilityAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// -----------------------------------------------------------------------------
// Dedicated AWS Metrics Sub-Agent
// -----------------------------------------------------------------------------

func NewAwsMetricsAgent(accountId string) core.NBAgent {
	return AwsMetricsAgent{accountId: accountId}
}

type AwsMetricsAgent struct {
	accountId string
}

func (a AwsMetricsAgent) GetName() string { return AwsMetricsAgentName }

func (a AwsMetricsAgent) GetNameAliases() []string { return []string{"AWSMetrics"} }

func (a AwsMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes AWS CloudWatch metrics (CPU, memory, disk, network, request latency/error rate, managed-service metrics, alarms) via the aws CLI. Used as the metrics backend for AWS accounts without a Prometheus/Datadog/Elasticsearch provider. Handles its own metric-name and resource discovery.`
}

func (a AwsMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's question using AWS CloudWatch metrics and alarms. Base every conclusion on CLI output — never invent metric values, dimensions, or time ranges.",
		"**Tool name:** Your aws tool is `aws_execute`. Use `shell_execute` only for `jq` post-processing of large responses.",
		"**Primary read — `aws cloudwatch get-metric-statistics`:** numeric time-series metrics (CPU, memory, connections, latency).",
		"  - `--namespace` specifies the AWS service (e.g. `AWS/EC2`, `AWS/RDS`, `AWS/Lambda`, `AWS/ApplicationELB`).",
		"  - `--metric-name` names the metric (e.g. `CPUUtilization`, `DatabaseConnections`, `Errors`). Run `aws cloudwatch list-metrics --namespace ...` first when unsure of the exact metric name.",
		"  - `--dimensions` filters by resource (e.g. `Name=InstanceId,Value=i-12345` or `Name=DBInstanceIdentifier,Value=my-db`).",
		"  - `--start-time` / `--end-time` bound the window in ISO 8601 format. Prefer `[[Time:-1h]]` / `[[Time:Now]]` macros — NEVER compute timestamps yourself.",
		"  - `--period` sets granularity in seconds (e.g. 60, 300); `--statistics` selects `Average`, `Maximum`, `Sum`, or `SampleCount`.",
		"**CloudWatch Alarms — `aws cloudwatch describe-alarms`:** check alarm states and thresholds when investigating active alerts.",
		"**Common metrics by namespace (discover, don't assume):**",
		"  - EC2 (`AWS/EC2`): `CPUUtilization`, `NetworkIn`, `NetworkOut`, `DiskReadOps`",
		"  - Lambda (`AWS/Lambda`): `Errors`, `Duration`, `Invocations`, `Throttles`",
		"  - RDS (`AWS/RDS`): `CPUUtilization`, `DatabaseConnections`, `FreeStorageSpace`, `ReadLatency`",
		"  - Application ELB (`AWS/ApplicationELB`): `RequestCount`, `TargetResponseTime`, `HTTPCode_Target_5XX_Count`",
		"  If a metric returns no data, list what exists: `aws cloudwatch list-metrics --namespace <namespace> --query \"Metrics[].MetricName\" --output text`.",
		"**Large output:** metric responses can be large. Filter at the CLI level first (single `--metric-name`, tight time window, appropriate `--period`). When still large, do NOT pull full JSON into context — from `shell_execute`, run the read with raw output redirected to a per-conversation workspace file (`aws cloudwatch get-metric-statistics ... > metrics_aws.json`; it persists across turns), then `jq` that file for required datapoints.",
		"**Self-correction:** Read `Stderr` on error — check argument syntax, dimension names, or namespace. Correct and retry ONCE per distinct cause; never repeat an identical failing command. On permission error (`AccessDenied`), report the missing IAM permission as a finding.",
	}

	constraints := []string{
		"Do not ask the user for clarification — resolve using available tools (list-metrics, describe-alarms).",
		"CRITICAL for stability: always bound queries with `--namespace`, `--metric-name`, short time window, and suitable `--period`.",
		"Empty result means 'no data for these parameters', not 'healthy' — state it explicitly and confirm metric exists before concluding.",
		"Base final answer strictly on values observed in CLI output.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAwsCliCommand: {
			"Use **aws_execute** to run `aws cloudwatch get-metric-statistics`, `aws cloudwatch list-metrics`, and `aws cloudwatch describe-alarms`.",
			"Always prefer this tool for metric reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Get Lambda error count metric for the last hour",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws cloudwatch get-metric-statistics --namespace AWS/Lambda --metric-name Errors --dimensions Name=FunctionName,Value=my-function --start-time [[Time:-1h]] --end-time [[Time:Now]] --period 300 --statistics Sum --region us-east-1",
					Explanation: "CloudWatch Metrics query using ISO 8601 macros and dimensions.",
				},
			},
		},
		{
			Question: "Investigate a CloudWatch alarm that just triggered",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws cloudwatch describe-alarms --alarm-names HighCPUAlarm --region us-east-1",
					Explanation: "Check alarm configuration, state, and metric dimensions.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in AWS CloudWatch metrics",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete metric values with their timestamps. Do NOT describe internal data flow.",
	}
}

func (a AwsMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	var supportedTools []toolcore.NBTool
	if awsExecuteTool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteAwsCliCommand); ok {
		supportedTools = append(supportedTools, awsExecuteTool)
	}
	if shellTool, ok := toolcore.GetNBTool(a.accountId, toolcore.ToolExecuteShellCommand); ok {
		supportedTools = append(supportedTools, shellTool)
	}
	return supportedTools
}

func (a AwsMetricsAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentObservabilityMaxIterations; v > 0 {
		return v
	}
	return 7
}

func (a AwsMetricsAgent) GetTimeout() time.Duration {
	if v := config.Config.LLMServerAgentObservabilityTimeoutSeconds; v > 0 {
		return time.Duration(v) * time.Second
	}
	return 3 * time.Minute
}

func (a AwsMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// -----------------------------------------------------------------------------
// Dedicated AWS Logs Sub-Agent
// -----------------------------------------------------------------------------

func NewAwsLogsAgent(accountId string) core.NBAgent {
	return AwsLogsAgent{accountId: accountId}
}

type AwsLogsAgent struct {
	accountId string
}

func (a AwsLogsAgent) GetName() string { return AwsLogsAgentName }

func (a AwsLogsAgent) GetNameAliases() []string { return []string{"AWSLogs"} }

func (a AwsLogsAgent) GetDescription() string {
	return `Retrieves and analyzes AWS CloudWatch logs (application logs, Lambda logs, ECS/EKS logs, CloudTrail audit logs) via the aws CLI. Used as the logs backend for AWS accounts without a Loki/Elasticsearch/Datadog provider. Discovers log groups and streams before querying, and cites concrete log lines.`
}

func (a AwsLogsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's log question using CloudWatch Logs and CloudTrail. Cite concrete log lines and timestamps — never invent log content, log groups, or errors.",
		"**Tool name:** Your aws tool is `aws_execute`. Use `shell_execute` only for `jq`/`grep` post-processing of large results.",
		"**Primary reads — CloudWatch Logs & CloudTrail:**",
		"  1. **Log group discovery:** ALWAYS discover log groups first — `aws logs describe-log-groups --log-group-name-prefix <prefix>` (e.g. `/aws/lambda/`, `/aws/ecs/`, `/demo/`). Never guess log group names.",
		"  2. **Filter log events — `aws logs filter-log-events`:** filter lines by pattern and time range.",
		"     - `--start-time` / `--end-time` in epoch milliseconds (13 digits). Use macros `[[Time:-1h]]` / `[[Time:Now]]` where supported.",
		"     - `--filter-pattern` uses CloudWatch filter syntax (e.g. `\"?ERROR ?EXCEPTION\"` for OR pattern, NOT `\"ERROR OR EXCEPTION\"`).",
		"     - `--max-items 200` for routine reads, up to `--max-items 1000` for investigations.",
		"  3. **Tail recent logs — `aws logs tail <log-group-name>`:** `aws logs tail /aws/lambda/my-function --since 1h --format short` for fast relative-time reads.",
		"  4. **Logs Insights — `aws logs start-query` + `get-query-results`:** use epoch seconds (10 digits) for time ranges. Poll `get-query-results` max 3 times. If status != 'Complete' after 3 polls, report partial results.",
		"  5. **CloudTrail audit logs (who changed what):** `aws cloudtrail lookup-events --lookup-attributes AttributeKey=ResourceType,AttributeValue=... --start-time [[Time:-24h]] --end-time [[Time:Now]]` (CloudTrail uses ISO 8601 timestamps).",
		"**Large output:** filter at CLI level first (`--log-group-name`, `--filter-pattern`, `--max-items`, time window). When still large, do NOT pull full JSON into context — from `shell_execute`, run read with raw output redirected to workspace file (`aws logs filter-log-events ... > logs_aws.json`), then `jq`/`grep` that file.",
		"**Self-correction:** Read `Stderr` on error (e.g. ResourceNotFoundException means log group does not exist). Correct and retry ONCE per distinct cause. On permission error (`AccessDeniedException`), report missing IAM permission as a finding.",
	}

	constraints := []string{
		"Do not ask user for clarification — discover log groups via `aws logs describe-log-groups`.",
		"CRITICAL for stability: bound reads with log group name, time window, and `--max-items` / limit.",
		"Empty result means 'no matching entries', not 'healthy' — state explicitly and confirm log group exists.",
		"MUST cite concrete log lines, timestamps, or error signatures. Preserve literal identifiers verbatim.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAwsCliCommand: {
			"Use **aws_execute** to run `aws logs describe-log-groups`, `aws logs filter-log-events`, `aws logs tail`, `aws logs start-query`, and `aws cloudtrail lookup-events`.",
			"Always prefer this tool for log reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq`/`grep` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Find and query application logs for errors (log group unknown)",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws logs describe-log-groups --log-group-name-prefix /demo/ --region us-east-1",
					Explanation: "Discover log group first by prefix.",
				},
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws logs filter-log-events --log-group-name /demo/backend-app --filter-pattern \"?ERROR ?Exception\" --max-items 100 --region us-east-1",
					Explanation: "Query discovered log group with filter pattern.",
				},
			},
		},
		{
			Question: "Find who modified RDS instance in the last 30 minutes",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws cloudtrail lookup-events --lookup-attributes AttributeKey=ResourceType,AttributeValue=AWS::RDS::DBInstance --start-time [[Time:-30m]] --end-time [[Time:Now]] --region us-east-1",
					Explanation: "CloudTrail audit log lookup using ISO 8601 macros.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in AWS CloudWatch Logs and CloudTrail investigation",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite exact log lines with timestamps. For investigations, include a short symptom → cause chain. Do NOT describe internal data flow.",
	}
}

func (a AwsLogsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	var supportedTools []toolcore.NBTool
	if awsExecuteTool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteAwsCliCommand); ok {
		supportedTools = append(supportedTools, awsExecuteTool)
	}
	if shellTool, ok := toolcore.GetNBTool(a.accountId, toolcore.ToolExecuteShellCommand); ok {
		supportedTools = append(supportedTools, shellTool)
	}
	return supportedTools
}

func (a AwsLogsAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentObservabilityMaxIterations; v > 0 {
		return v
	}
	return 7
}

func (a AwsLogsAgent) GetTimeout() time.Duration {
	if v := config.Config.LLMServerAgentObservabilityTimeoutSeconds; v > 0 {
		return time.Duration(v) * time.Second
	}
	return 3 * time.Minute
}

func (a AwsLogsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// -----------------------------------------------------------------------------
// Dedicated AWS Traces Sub-Agent
// -----------------------------------------------------------------------------

func NewAwsTracesAgent(accountId string) core.NBAgent {
	return AwsTracesAgent{accountId: accountId}
}

type AwsTracesAgent struct {
	accountId string
}

func (a AwsTracesAgent) GetName() string { return AwsTracesAgentName }

func (a AwsTracesAgent) GetNameAliases() []string { return []string{"AWSTraces"} }

func (a AwsTracesAgent) GetDescription() string {
	return `Retrieves and analyzes AWS X-Ray distributed traces (latency, slow traces, service maps, segment details) via the aws CLI, and correlates trace-tagged logs via CloudWatch Logs. Used as the traces backend for AWS accounts without a ClickHouse/Jaeger/Datadog provider.`
}

func (a AwsTracesAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Goal:** Answer the user's trace/latency question using AWS X-Ray. Cite concrete trace IDs, segment names, and latencies — never invent them.",
		"**Tool name:** Your aws tool is `aws_execute`. Use `shell_execute` only for `jq` post-processing of large results.",
		"**Primary reads — AWS X-Ray CLI commands:**",
		"  1. **Find trace summaries — `aws xray get-trace-summaries`:** list traces in a window with filter expressions.",
		"     - `--start-time` / `--end-time` in ISO 8601 format. Use macros `[[Time:-1h]]` / `[[Time:Now]]`.",
		"     - `--filter-expression` for targeting issues (e.g. `\"responsetime > 5\"`, `\"fault = true\"`, `\"http.status >= 500\"`).",
		"  2. **Fetch trace details — `aws xray batch-get-traces`:** inspect full segment tree for specific trace IDs (`--trace-ids <id>`).",
		"  3. **Service graph — `aws xray get-service-graph`:** retrieve service topology and error rates over time window.",
		"**Log Correlation via CloudWatch Logs:** when trace IDs are present in application logs, use `aws logs filter-log-events` to pull correlated log lines.",
		"**Large output:** bound reads with time window and filter expressions. When JSON is still large, redirect raw `aws_execute` output to a workspace file (`aws xray batch-get-traces ... > traces_aws.json`), then `jq` that file.",
		"**Self-correction:** Read `Stderr` on error. Correct and retry ONCE per distinct cause. On permission error (`AccessDeniedException`), report missing IAM role/permissions as a finding.",
	}

	constraints := []string{
		"Do not ask user for clarification — resolve via X-Ray API and log correlation.",
		"CRITICAL for stability: bound every read with time window and filter expression.",
		"Empty result means 'no matching traces', state explicitly.",
		"MUST cite concrete trace IDs, segment names, and latencies verbatim.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAwsCliCommand: {
			"Use **aws_execute** to run `aws xray get-trace-summaries`, `aws xray batch-get-traces`, `aws xray get-service-graph`, and `aws logs filter-log-events`.",
			"Always prefer this tool for trace reads.",
		},
		toolcore.ToolExecuteShellCommand: {
			"Use **shell_execute** for `jq` post-processing and for large-output offload to a workspace file (see **Large output**).",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Find slow traces with X-Ray",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws xray get-trace-summaries --start-time [[Time:-1h]] --end-time [[Time:Now]] --filter-expression \"responsetime > 5\" --region us-east-1",
					Explanation: "List slow trace summaries using X-Ray filter expression.",
				},
				{
					Tool:        tools.ToolExecuteAwsCliCommand,
					Input:       "aws xray batch-get-traces --trace-ids 1-abc123-def456 --region us-east-1",
					Explanation: "Get full trace segments for specific trace ID.",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in AWS X-Ray distributed tracing",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		OutputFormat: "Markdown. Lead with the answer and cite concrete trace IDs, segment names, and latencies. Do NOT describe internal data flow.",
	}
}

func (a AwsTracesAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	var supportedTools []toolcore.NBTool
	if awsExecuteTool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteAwsCliCommand); ok {
		supportedTools = append(supportedTools, awsExecuteTool)
	}
	if shellTool, ok := toolcore.GetNBTool(a.accountId, toolcore.ToolExecuteShellCommand); ok {
		supportedTools = append(supportedTools, shellTool)
	}
	return supportedTools
}

func (a AwsTracesAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentObservabilityMaxIterations; v > 0 {
		return v
	}
	return 7
}

func (a AwsTracesAgent) GetTimeout() time.Duration {
	if v := config.Config.LLMServerAgentObservabilityTimeoutSeconds; v > 0 {
		return time.Duration(v) * time.Second
	}
	return 3 * time.Minute
}

func (a AwsTracesAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
