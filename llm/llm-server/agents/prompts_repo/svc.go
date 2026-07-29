package prompts_repo

import (
	_ "embed"
	"fmt"
	"strings"

	"nudgebee/llm/config"
)

//go:embed title_generation_agent.txt
var titleGenerationAgent string

//go:embed executor_response_formatter.txt
var executorResponseFormatter string

//go:embed event_analyzer.txt
var eventAnalyzerBase string

//go:embed log_analysis_file_extractor.txt
var logAnalysisFileExtractor string

//go:embed log_analysis_codediff_generate_request.txt
var logAnalysisCodeDiffGenerateRequest string

//go:embed planner_react_followup_base.txt
var plannerReActBaseFollowup string

//go:embed planner_react_3_base.txt
var plannerReactBase3 string

//go:embed suggestions_base.txt
var suggestionBase string

//go:embed planner_react_critiquer.txt
var plannerReactCritiquer string

//go:embed agent_response_summary.txt
var agentResponseSummary string

//go:embed planner_agent_rewrite_tool_input.txt
var plannerAgentRewriteToolInput string

//go:embed evaluator_system.txt
var evaluatorSystem string

//go:embed evaluator_query_response.txt
var evaluatorQueryResponse string

//go:embed evaluator_tool_calls.txt
var evaluatorToolCalls string

//go:embed event_summary.txt
var eventSummary string

//go:embed event_general_summary.txt
var eventGeneralSummary string

//go:embed event_investigation.txt
var eventInvestigation string

//go:embed memory_extractor.txt
var memoryExtractor string

//go:embed agent_k8s_debug_react.txt
var agentK8sDebugReact string

//go:embed agent_k8s_lean.txt
var agentK8sLean string

//go:embed agent_aws_lean.txt
var agentAwsLean string

//go:embed agent_gcp_lean.txt
var agentGcpLean string

//go:embed agent_azure_lean.txt
var agentAzureLean string

//go:embed summarization_security.txt
var summarizationSecurity string

//go:embed unified_context_memory.txt
var unifiedContextMemory string

//go:embed executor_response_formatter_slack.txt
var executorResponseFormatterSlack string

//go:embed tool_config_auto_selection.txt
var toolConfigAutoSelection string

//go:embed agent_aws_debug_react.txt
var agentAwsDebugReact string

//go:embed agent_gcp_debug_react.txt
var agentGcpDebugReact string

//go:embed agent_azure_debug_react.txt
var agentAzureDebugReact string

//go:embed agent_aws.txt
var agentAws string

//go:embed agent_aws_observability.txt
var agentAwsObservability string

//go:embed context_continuity.txt
var contextContinuity string

//go:embed event_detailed_response_synthesis.txt
var eventDetailedResponseSynthesis string

//go:embed shared_time_handling_rules.txt
var sharedTimeHandlingRules string

//go:embed shared_data_protection_rules.txt
var sharedDataProtectionRules string

//go:embed shared_code_analysis_rules.txt
var sharedCodeAnalysisRules string

//go:embed shared_security_rules.txt
var sharedSecurityRules string

//go:embed shared_async_completion_rules.txt
var sharedAsyncCompletionRules string

//go:embed watch_completion_summary.txt
var watchCompletionSummary string

// toolRemediationGenerate is the system prompt for the remediation_generate tool.
// It instructs the LLM to produce a structured remediation plan (root cause, impact,
// proposed actions, verification steps) without executing anything — the user must approve first.
//
//go:embed tool_remediation_generate.txt
var toolRemediationGenerate string

//go:embed tool_remediation_generate_json.txt
var toolRemediationGenerateJson string

// agentFinops is the system prompt for the FinOps cost optimization supervisor agent.
// It orchestrates spend analysis, recommendation discovery, and resource investigation
// by delegating to specialized sub-tools and agents.
//
//go:embed agent_finops.txt
var agentFinops string

// agentWebhookSubjectExtractor is the system prompt for the webhook subject-name
// extractor: a single-shot classifier that maps an incoming alert to the running
// workload/service it concerns.
//
//go:embed agent_webhook_subject_extractor.txt
var agentWebhookSubjectExtractor string

// scratchpadSummarizer is used by the scratchpad compression system to generate
// concise LLM-backed summaries of older tool observations, preserving analytical
// value (error patterns, metrics, causal relationships) that byte truncation destroys.
//
//go:embed scratchpad_summarizer.txt
var scratchpadSummarizer string

// scratchpadContextSummarizer summarizes a block of older scratchpad context
// (multiple thoughts/actions/observations) into a compact summary, used when
// the full scratchpad exceeds the token budget.
//
//go:embed scratchpad_context_summarizer.txt
var scratchpadContextSummarizer string

// agentKgUsage is the KG-tool usage guidance appended to the system prompt of
// the KG-only service_dependency_graph V2 agent. Lives in the agent system
// prompt (cached prefix), so the embed cost is paid once per CacheScopeAccount
// window (12h TTL), not per ReAct iteration.
//
//go:embed agent_kg_usage.txt
var agentKgUsage string

//go:embed cost_optimization_analysis.txt
var costOptimizationAnalysis string

const PromptExecutor_response_formatter = "executor_response_formatter"
const PromptEventAnalyzerBase = "event_analyzer_base"
const PromptTitleGeneration = "title-generation-agent"
const PromptLogAnalysisFileExtractor = "log_analysis_file_extractor"
const PromptLogAnalysisCodeDiffGenerateRequest = "log_analysis_codediff_generate_request"
const PromptConversationSuggestion = "conversation_suggestion"
const PromptPlannerReActBaseFollowup = "planner_react_base_followup"
const PromptPlannerReactBase3 = "planner_react_base_3"
const PromptPlannerReactCritiquer = "planner_react_critiquer"

const PromptAgentResponseSummary = "agent_response_summary"
const PromptPlannerAgentRewriteToolInput = "planner_agent_rewrite_tool_input"
const PromptEvaluatorSystem = "evaluator_system"
const PromptEvaluatorQueryResponse = "evaluator_query_response"
const PromptEvaluatorToolCalls = "evaluator_tool_calls"
const PromptEventSummary = "event_summary"
const PromptEventGeneralSummary = "event_general_summary"
const PromptEventInvestigation = "event_investigation"
const PromptMemoryExtractor = "memory_extractor"
const PromptAgentK8sDebugReact = "agent_k8s_debug_react"
const PromptAgentK8sLean = "agent_k8s_lean"
const PromptAgentAwsLean = "agent_aws_lean"
const PromptAgentGcpLean = "agent_gcp_lean"
const PromptAgentAzureLean = "agent_azure_lean"
const PromptSummarizationSecurity = "summarization_security"
const PromptUnifiedContextMemory = "unified_context_memory"
const PromptExecutorResponseFormatterSlack = "executor_response_formatter_slack"
const PromptToolConfigAutoSelection = "tool_config_auto_selection"
const PromptAgentAwsDebugReact = "agent_aws_debug_react"
const PromptAgentGcpDebugReact = "agent_gcp_debug_react"
const PromptAgentAzureDebugReact = "agent_azure_debug_react"
const PromptAgentAws = "agent_aws"
const PromptAgentAwsObservability = "agent_aws_observability"
const PromptContextContinuity = "context_continuity"
const PromptEventDetailedResponseSynthesis = "event_detailed_response_synthesis"
const PromptSharedTimeHandlingRules = "shared_time_handling_rules"
const PromptSharedDataProtectionRules = "shared_data_protection_rules"
const PromptSharedCodeAnalysisRules = "shared_code_analysis_rules"
const PromptSharedSecurityRules = "shared_security_rules"
const PromptSharedAsyncCompletionRules = "shared_async_completion_rules"
const PromptWatchCompletionSummary = "watch_completion_summary"

// PromptToolRemediationGenerate is the system prompt embedded in tool_remediation_generate.txt.
// Loaded by RemediationGenerateTool.Call() to avoid hardcoding prompts in Go source.
const PromptToolRemediationGenerate = "tool_remediation_generate"

// PromptToolRemediationGenerateJson is the system prompt embedded in tool_remediation_generate_json.txt.
// It instructs the model to emit a strict JSON remediation plan (root cause + execute/verify/rollback
// commands per step) for the structured remediation panel, as opposed to the prose plan the
// agent-facing PromptToolRemediationGenerate produces.
const PromptToolRemediationGenerateJson = "tool_remediation_generate_json"

// PromptScratchpadContextSummarizer summarizes a block of older scratchpad context for budget compression.
const PromptScratchpadContextSummarizer = "scratchpad_context_summarizer"

// PromptAgentFinops is the system prompt for the FinOps cost optimization supervisor agent.
const PromptAgentFinops = "agent_finops"

// PromptAgentWebhookSubjectExtractor is the system prompt for the webhook
// subject-name extractor agent.
const PromptAgentWebhookSubjectExtractor = "agent_webhook_subject_extractor"

// PromptScratchpadSummarizer generates concise summaries of tool observations for scratchpad compression.
const PromptScratchpadSummarizer = "scratchpad_summarizer"

// PromptAgentKgUsage is the KG-tool usage guidance for the KG-only V2 service_dependency_graph agent.
const PromptAgentKgUsage = "agent_kg_usage"
const PromptCostOptimization = "cost_optimization_analysis"

func GetPrompt(module string, args ...any) string {
	data := ""
	switch module {
	case PromptPlannerReActBaseFollowup:
		data = plannerReActBaseFollowup

	case PromptExecutor_response_formatter:
		data = executorResponseFormatter
	case PromptEventAnalyzerBase:
		data = eventAnalyzerBase
	case PromptTitleGeneration:
		data = titleGenerationAgent
	case PromptLogAnalysisFileExtractor:
		data = logAnalysisFileExtractor
	case PromptLogAnalysisCodeDiffGenerateRequest:
		data = logAnalysisCodeDiffGenerateRequest
	case PromptPlannerReactBase3:
		data = plannerReactBase3
	case PromptPlannerReactCritiquer:
		data = plannerReactCritiquer
	case PromptConversationSuggestion:
		data = suggestionBase
	case PromptAgentResponseSummary:
		data = agentResponseSummary
	case PromptPlannerAgentRewriteToolInput:
		data = plannerAgentRewriteToolInput
	case PromptEvaluatorSystem:
		data = evaluatorSystem
	case PromptEvaluatorQueryResponse:
		data = evaluatorQueryResponse
	case PromptEvaluatorToolCalls:
		data = evaluatorToolCalls
	case PromptEventSummary:
		data = eventSummary
	case PromptEventGeneralSummary:
		data = eventGeneralSummary
	case PromptEventInvestigation:
		data = eventInvestigation
	case PromptMemoryExtractor:
		data = memoryExtractor
	case PromptAgentK8sDebugReact:
		data = agentK8sDebugReact
	case PromptAgentK8sLean:
		data = agentK8sLean
	case PromptAgentAwsLean:
		data = agentAwsLean
	case PromptAgentGcpLean:
		data = agentGcpLean
	case PromptAgentAzureLean:
		data = agentAzureLean
	case PromptSummarizationSecurity:
		data = summarizationSecurity
	case PromptUnifiedContextMemory:
		data = unifiedContextMemory
	case PromptExecutorResponseFormatterSlack:
		data = executorResponseFormatterSlack
	case PromptToolConfigAutoSelection:
		data = toolConfigAutoSelection
	case PromptAgentAwsDebugReact:
		data = agentAwsDebugReact
	case PromptAgentGcpDebugReact:
		data = agentGcpDebugReact
	case PromptAgentAzureDebugReact:
		data = agentAzureDebugReact
	case PromptAgentAws:
		data = agentAws
	case PromptAgentAwsObservability:
		data = agentAwsObservability
	case PromptContextContinuity:
		data = contextContinuity
	case PromptEventDetailedResponseSynthesis:
		data = eventDetailedResponseSynthesis
	case PromptSharedTimeHandlingRules:
		data = sharedTimeHandlingRules
	case PromptSharedDataProtectionRules:
		data = sharedDataProtectionRules
	case PromptSharedCodeAnalysisRules:
		data = sharedCodeAnalysisRules
	case PromptSharedSecurityRules:
		data = sharedSecurityRules
	case PromptSharedAsyncCompletionRules:
		data = sharedAsyncCompletionRules
	case PromptWatchCompletionSummary:
		data = watchCompletionSummary
	case PromptToolRemediationGenerate:
		data = toolRemediationGenerate
	case PromptToolRemediationGenerateJson:
		data = toolRemediationGenerateJson
	case PromptScratchpadSummarizer:
		data = scratchpadSummarizer
	case PromptScratchpadContextSummarizer:
		data = scratchpadContextSummarizer
	case PromptAgentFinops:
		data = agentFinops
	case PromptAgentWebhookSubjectExtractor:
		data = agentWebhookSubjectExtractor
	case PromptAgentKgUsage:
		data = agentKgUsage
	case PromptCostOptimization:
		data = costOptimizationAnalysis
	}
	if len(args) > 0 {
		data = fmt.Sprintf(data, args...)
	}

	// Replace identity placeholders with configured values
	data = strings.ReplaceAll(data, "{{@assistant_name}}", config.Config.AIAssistantName)
	data = strings.ReplaceAll(data, "{{@assistant_company}}", config.Config.AIAssistantCompany)

	return data
}
