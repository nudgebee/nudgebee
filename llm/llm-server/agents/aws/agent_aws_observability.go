package aws

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"
)

// Agent Constants
const AgentAwsObservabilityName = "aws_observability"

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
}

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
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAwsObservability)
	instructions := strings.Split(promptText, "\n")

	constraints := []string{
		// Anti-Hallucination (critical, unique emphasis)
		"CRITICAL: NEVER invent log content, errors, or stack traces - use EXACT QUOTES from CLI output only",
		"Report format: 'Ran: [command] → Result: [actual output] → Analysis: [what it shows]'",
		// Syntax (quick reference)
		"Time formats: Logs = epoch ms (13 digits), CloudTrail = ISO 8601, Logs Insights = epoch sec (10 digits)",
		"Filter pattern OR: \"?ERROR ?EXCEPTION\" NOT \"ERROR OR EXCEPTION\"",
		// Evidence
		"Evidence-based: cite CLI command + raw output for every claim, never speculate",
		// Logs Insights polling cap
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
		{
			Question: "Check for errors but not sure when they occurred",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs filter-log-events --log-group-name /aws/lambda/payment-processor --start-time 1704754800000 --end-time 1704758400000 --filter-pattern \"?ERROR\" --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs filter-log-events --log-group-name /aws/lambda/payment-processor --filter-pattern \"?ERROR\" --max-items 200 --region us-east-1",
				},
			},
			Explanation: "FALLBACK STRATEGY: First try time-based query. If it returns empty {\"events\": []}, use line-based (--max-items) to get recent logs without time constraints. Never give up after first empty result.",
		},
		{
			Question: "Get the most recent application logs to see current state",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs tail /aws/lambda/my-function --since 1h --format short --region us-east-1",
				},
			},
			Explanation: "Use 'aws logs tail' for recent logs with relative time (30m, 1h, 2h) when investigating current issues. Alternative to filter-log-events.",
		},
		{
			Question: "Find who modified the RDS instance in the last 30 minutes",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudtrail lookup-events --lookup-attributes AttributeKey=ResourceType,AttributeValue=AWS::RDS::DBInstance --start-time 2026-01-13T12:30:00Z --end-time 2026-01-13T13:00:00Z --region us-east-1",
				},
			},
			Explanation: "CloudTrail uses ISO 8601 timestamps, NOT epoch milliseconds like CloudWatch Logs. Use --lookup-attributes with AttributeKey=ResourceType/EventName/Username.",
		},
		{
			Question: "Get Lambda error count metric for the last hour",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudwatch get-metric-statistics --namespace AWS/Lambda --metric-name Errors --dimensions Name=FunctionName,Value=my-function --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T13:00:00Z --period 300 --statistics Sum --region us-east-1",
				},
			},
			Explanation: "CloudWatch Metrics uses ISO 8601 format, NOT epoch milliseconds. Use --dimensions Name=<key>,Value=<val> for filtering.",
		},
		{
			Question: "Investigate a CloudWatch alarm that just triggered",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudwatch describe-alarms --alarm-names HighCPUAlarm --region us-east-1",
				},
			},
			Explanation: "Alarm-to-resource mapping: Extract Namespace (e.g., AWS/EC2) and Dimensions (e.g., InstanceId=i-xxx) to identify the affected resource. Report: 'Alarm monitors EC2 instance i-xxx with threshold 80%, current value 95%'. Use StateTransitionedTimestamp to establish when the issue started.",
		},
		{
			Question: "Find what changed before an issue started (temporal correlation)",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudtrail lookup-events --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T12:30:00Z --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudtrail lookup-events --lookup-attributes AttributeKey=EventName,AttributeValue=ModifySecurityGroupRules --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T12:30:00Z --region us-east-1",
				},
			},
			Explanation: "Temporal correlation: Query CloudTrail 15-30 min BEFORE incident. Look for: SecurityGroup changes, IAM modifications, config updates. Report format: 'CloudTrail shows ModifySecurityGroupRules by user@company at 12:15Z, 15 min before errors started'.",
		},
		{
			Question: "Correlate multiple metrics to find root cause",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name DatabaseConnections --dimensions Name=DBInstanceIdentifier,Value=my-db --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T13:00:00Z --period 60 --statistics Average --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name CPUUtilization --dimensions Name=DBInstanceIdentifier,Value=my-db --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T13:00:00Z --period 60 --statistics Average --region us-east-1",
				},
			},
			Explanation: "Metric correlation: Get multiple related metrics in same timeframe. Look for: Which spiked first (cause), which followed (effect). If connections spiked at 12:15, CPU at 12:17 → connections caused high CPU.",
		},
		{
			Question: "Use CloudWatch Logs Insights for complex log analysis",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs start-query --log-group-name /aws/lambda/my-function --start-time 1704758400 --end-time 1704762000 --query-string \"fields @timestamp, @message | filter @message like /ERROR/ | stats count(*) by bin(5m)\" --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs get-query-results --query-id abc123 --region us-east-1",
				},
			},
			Explanation: "Logs Insights uses epoch SECONDS (10 digits), not milliseconds. Use for: aggregations (stats count), grouping (by bin), complex filtering. Poll get-query-results until status='Complete'.",
		},
		{
			Question: "Find slow traces with X-Ray",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws xray get-trace-summaries --start-time 2026-01-13T12:00:00Z --end-time 2026-01-13T13:00:00Z --filter-expression \"responsetime > 5\" --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws xray batch-get-traces --trace-ids 1-abc123-def456 --region us-east-1",
				},
			},
			Explanation: "X-Ray for latency investigation: get-trace-summaries finds slow traces, batch-get-traces shows segment details. Common filters: 'responsetime > 5', 'fault = true', 'http.status >= 500'. Correlate trace timing with metric spikes.",
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

// GetMaxIterations caps the aws_observability ReAct loop, configurable via
// llm_server_agent_observability_max_iterations (default 7).
// Typical workflow: discover log groups -> query logs -> check metrics/alarms ->
// CloudTrail correlation -> X-Ray (optional) -> answer. Default of 7 allows retries.
func (a *AwsObservabilityAgent) GetMaxIterations() int {
	if v := config.Config.LLMServerAgentObservabilityMaxIterations; v > 0 {
		return v
	}
	return 7
}

// GetTimeout enforces a wall-clock deadline on the entire execution, configurable via
// llm_server_agent_aws_observability_timeout_seconds (default 180).
// This prevents runaway Logs Insights polling or large CloudTrail responses
// from pushing p99 latency beyond the target SLA.
func (a *AwsObservabilityAgent) GetTimeout() time.Duration {
	if v := config.Config.LLMServerAgentObservabilityTimeoutSeconds; v > 0 {
		return time.Duration(v) * time.Second
	}
	return 3 * time.Minute
}

func (a *AwsObservabilityAgent) GetPlannerType() core.AgentPlannerType {
	// Use ReAct for iterative reasoning with fact-checking and RAG queries
	return core.AgentPlannerTypeReAct
}
