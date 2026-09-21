package prompts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"text/template"

	"nudgebee/llm/config"
)

// Prompt name constants. The value is the prompt's filename stem and its DB
// prompt_name — one identifier, no lookup table asserting two strings match.
// Generated from the files under prompts/default/v1; keep in sync by regenerating.
const (
	PromptAsyncCompletionRules               = "async_completion_rules"
	PromptCodeAnalysisRules                  = "code_analysis_rules"
	PromptContextContinuity                  = "context_continuity"
	PromptDataProtectionRules                = "data_protection_rules"
	PromptKgUsage                            = "kg_usage"
	PromptMemoryConsumptionRules             = "memory_consumption_rules"
	PromptSecurityRules                      = "security_rules"
	PromptTimeHandlingRules                  = "time_handling_rules"
	PromptVoteSubject                        = "vote_subject"
	PromptUnifiedContextMemory               = "unified_context_memory"
	PromptAwsLean                            = "aws_lean"
	PromptAwsObservability                   = "aws_observability"
	PromptAzureLean                          = "azure_lean"
	PromptFinops                             = "finops"
	PromptGcpLean                            = "gcp_lean"
	PromptK8sLean                            = "k8s_lean"
	PromptK8sNative                          = "k8s_native"
	PromptWebhookSubjectExtractor            = "webhook_subject_extractor"
	PromptAgentRewriteToolInput              = "agent_rewrite_tool_input"
	PromptReact3Base                         = "react_3_base"
	PromptReact3CustomBase                   = "react_3_custom_base"
	PromptReact4Base                         = "react_4_base"
	PromptReact4CustomBase                   = "react_4_custom_base"
	PromptReactCritiquer                     = "react_critiquer"
	PromptConfigAutoSelection                = "config_auto_selection"
	PromptRemediationGenerate                = "remediation_generate"
	PromptRemediationGenerateJson            = "remediation_generate_json"
	PromptAgentResponseSummary               = "agent_response_summary"
	PromptConversationSuggestion             = "conversation_suggestion"
	PromptCostOptimizationAnalysis           = "cost_optimization_analysis"
	PromptEvaluatorQueryResponse             = "evaluator_query_response"
	PromptEvaluatorSystem                    = "evaluator_system"
	PromptEvaluatorToolCalls                 = "evaluator_tool_calls"
	PromptEventDetailedResponseSynthesis     = "event_detailed_response_synthesis"
	PromptEventDigestBriefing                = "event_digest_briefing"
	PromptEventDigestClassSummary            = "event_digest_class_summary"
	PromptEventGeneralSummary                = "event_general_summary"
	PromptEventInvestigation                 = "event_investigation"
	PromptEventSummary                       = "event_summary"
	PromptLogAnalysisCodediffGenerateRequest = "log_analysis_codediff_generate_request"
	PromptLogAnalysisFileExtractor           = "log_analysis_file_extractor"
	PromptMemoryCollectiveConsolidate        = "memory_collective_consolidate"
	PromptMemoryDecisionsSummarise           = "memory_decisions_summarise"
	PromptMemoryExtractor                    = "memory_extractor"
	PromptMemoryPatternsExtract              = "memory_patterns_extract"
	PromptMemorySessionDistill               = "memory_session_distill"
	PromptMemorySessionExtractor             = "memory_session_extractor"
	PromptMemorySoulConsolidate              = "memory_soul_consolidate"
	PromptAgentLlm                           = "agent_llm"
	PromptResponseFormatter                  = "response_formatter"
	PromptResponseFormatterSlack             = "response_formatter_slack"
	PromptScratchpadContextSummarizer        = "scratchpad_context_summarizer"
	PromptScratchpadSummarizer               = "scratchpad_summarizer"
	PromptTitleGeneration                    = "title_generation"
	PromptWatchCompletionSummary             = "watch_completion_summary"
)

// promptCategories records which category directory each prompt lives in.
// Resolution is {model}/{version}/{category}/{name}.yaml, so this is the only
// thing the caller cannot derive from the name alone.
var promptCategories = map[string]PromptCategory{
	PromptAsyncCompletionRules:               CategoryFragments,
	PromptCodeAnalysisRules:                  CategoryFragments,
	PromptContextContinuity:                  CategoryFragments,
	PromptDataProtectionRules:                CategoryFragments,
	PromptKgUsage:                            CategoryFragments,
	PromptMemoryConsumptionRules:             CategoryFragments,
	PromptSecurityRules:                      CategoryFragments,
	PromptTimeHandlingRules:                  CategoryFragments,
	PromptVoteSubject:                        CategoryUtilities,
	PromptUnifiedContextMemory:               CategoryFragments,
	PromptAwsLean:                            CategoryAgents,
	PromptAwsObservability:                   CategoryAgents,
	PromptAzureLean:                          CategoryAgents,
	PromptFinops:                             CategoryAgents,
	PromptGcpLean:                            CategoryAgents,
	PromptK8sLean:                            CategoryAgents,
	PromptK8sNative:                          CategoryAgents,
	PromptWebhookSubjectExtractor:            CategoryAgents,
	PromptAgentRewriteToolInput:              CategoryPlanners,
	PromptReact3Base:                         CategoryPlanners,
	PromptReact3CustomBase:                   CategoryPlanners,
	PromptReact4Base:                         CategoryPlanners,
	PromptReact4CustomBase:                   CategoryPlanners,
	PromptReactCritiquer:                     CategoryPlanners,
	PromptConfigAutoSelection:                CategoryTools,
	PromptRemediationGenerate:                CategoryTools,
	PromptRemediationGenerateJson:            CategoryTools,
	PromptAgentResponseSummary:               CategoryUtilities,
	PromptConversationSuggestion:             CategoryUtilities,
	PromptCostOptimizationAnalysis:           CategoryUtilities,
	PromptEvaluatorQueryResponse:             CategoryUtilities,
	PromptEvaluatorSystem:                    CategoryUtilities,
	PromptEvaluatorToolCalls:                 CategoryUtilities,
	PromptEventDetailedResponseSynthesis:     CategoryUtilities,
	PromptEventDigestBriefing:                CategoryUtilities,
	PromptEventDigestClassSummary:            CategoryUtilities,
	PromptEventGeneralSummary:                CategoryUtilities,
	PromptEventInvestigation:                 CategoryUtilities,
	PromptEventSummary:                       CategoryUtilities,
	PromptLogAnalysisCodediffGenerateRequest: CategoryUtilities,
	PromptLogAnalysisFileExtractor:           CategoryUtilities,
	PromptMemoryCollectiveConsolidate:        CategoryUtilities,
	PromptMemoryDecisionsSummarise:           CategoryUtilities,
	PromptMemoryExtractor:                    CategoryUtilities,
	PromptMemoryPatternsExtract:              CategoryUtilities,
	PromptMemorySessionDistill:               CategoryUtilities,
	PromptMemorySessionExtractor:             CategoryUtilities,
	PromptMemorySoulConsolidate:              CategoryUtilities,
	PromptAgentLlm:                           CategoryUtilities,
	PromptResponseFormatter:                  CategoryUtilities,
	PromptResponseFormatterSlack:             CategoryUtilities,
	PromptScratchpadContextSummarizer:        CategoryUtilities,
	PromptScratchpadSummarizer:               CategoryUtilities,
	PromptTitleGeneration:                    CategoryUtilities,
	PromptWatchCompletionSummary:             CategoryUtilities,
}

// resolve returns the category for a registered prompt.
func resolve(module string) (PromptCategory, error) {
	category, ok := promptCategories[module]
	if !ok {
		return "", fmt.Errorf("prompts: module %q is not registered", module)
	}
	return category, nil
}

// GetPromptStrict retrieves a prompt and returns an error when it cannot be resolved,
// instead of an empty string that invites a silent fallback to an older copy.
//
// Prompt files are embedded in the binary and every registered prompt is verified to
// resolve at startup (see MustResolveAll), so an error here means a genuine defect.
// The previous versioning rollout kept "" + fall back to the legacy tree, which is why
// it served stale prompts for months without anyone noticing.
func GetPromptStrict(ctx context.Context, module string, accountID string, args ...any) (string, error) {
	category, err := resolve(module)
	if err != nil {
		return "", err
	}

	loader := GetLoader()
	if loader == nil {
		return "", fmt.Errorf("prompts: loader not initialized for module %q", module)
	}

	resp, err := loader.GetPrompt(ctx, PromptRequest{
		Name:      module,
		Category:  category,
		Model:     modelForRequest(ctx),
		AccountID: accountID,
	})
	if err != nil {
		return "", fmt.Errorf("prompts: loading %s/%s: %w", category, module, err)
	}

	data := resp.Content
	if len(args) > 0 {
		data = fmt.Sprintf(data, args...)
	}
	return data, nil
}

// GetPrompt returns a registered prompt's content.
//
// Safe to use in expression position (map literals, struct fields) because
// MustResolveAll fails startup if any registered prompt is missing — a lookup cannot
// fail at runtime for a name that is in promptCategories. It logs and returns "" only
// in the impossible case, rather than panicking inside a live request.
func GetPrompt(ctx context.Context, module string, accountID string, args ...any) string {
	data, err := GetPromptStrict(ctx, module, accountID, args...)
	if err != nil {
		slog.Error("prompts: registered prompt failed to resolve at runtime", "module", module, "error", err)
		return ""
	}
	return data
}

// MustResolveAll verifies every registered prompt resolves against the embedded FS.
// Called during startup so a missing or malformed prompt file aborts the process
// instead of degrading silently on a live request. This is what makes the plain
// string-returning GetPrompt safe.
func MustResolveAll() error {
	loader := GetLoader()
	if loader == nil {
		return fmt.Errorf("prompts: loader not initialized")
	}

	modules := make([]string, 0, len(promptCategories))
	for module := range promptCategories {
		modules = append(modules, module)
	}
	sort.Strings(modules)

	var missing []string
	for _, module := range modules {
		// Verified against the default model at v1: that is the final fallback of
		// every resolution path, so if it exists no model/version can resolve to nothing.
		if _, _, err := loader.loadPromptFile(module, promptCategories[module], "default", "v1"); err != nil {
			missing = append(missing, fmt.Sprintf("%s/%s: %v", promptCategories[module], module, err))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("prompts: %d registered prompt(s) failed to resolve:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	slog.Info("prompts: all registered prompts resolved", "count", len(modules))
	return nil
}

// RenderPrompt loads a prompt and renders it with Go template data.
// Use this for prompts that have {{ .key }} style template variables.
func RenderPrompt(ctx context.Context, module string, accountID string, data map[string]interface{}) string {
	promptText := GetPrompt(ctx, module, accountID)
	if promptText == "" {
		return ""
	}

	tmpl, err := template.New("prompt").Parse(promptText)
	if err != nil {
		slog.Warn("prompts: failed to parse template", "module", module, "error", err)
		return promptText
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		slog.Warn("prompts: failed to render template", "module", module, "error", err)
		return promptText
	}

	return buf.String()
}

// requestModelKey carries the LLM model resolved for the current request
// (conversation override → pinned source → tier config → env, reconciled by
// agents/core.ResolveLLMConfig). Set via WithRequestModel at the start of an
// agent execution so prompt resolution matches the model that will actually
// serve the call, instead of the deployment-wide LLM_MODEL env var.
type requestModelKey struct{}

// WithRequestModel returns a context carrying the model prompt resolution
// should use for this request. The value is normalized (lowercased/trimmed) at
// read time. A nil ctx is tolerated and treated as context.Background().
func WithRequestModel(ctx context.Context, model string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestModelKey{}, model)
}

// modelForRequest resolves the model for a prompt load: the per-request value
// when one was attached, otherwise the deployment-wide config default.
// Background jobs and startup validation carry no request model and keep
// today's env-based behavior.
func modelForRequest(ctx context.Context) string {
	if ctx != nil {
		if m, ok := ctx.Value(requestModelKey{}).(string); ok && m != "" {
			return NormalizeModelName(m)
		}
	}
	return GetModelFromConfig()
}

// GetModelFromConfig returns the deployment-wide default model from config.
func GetModelFromConfig() string {
	return NormalizeModelName(config.Config.LlmModel)
}

// NormalizeModelName lowercases/trims a model identifier for use as a
// prompt-tree lookup key. Unlike the provider scheme this replaces, models
// aren't a fixed enum — any non-empty string is a valid exact-match key as-is;
// only whitespace/casing is normalized so "Qwen3-235B-Vertex" and
// "qwen3-235b-vertex" resolve to the same override. Empty normalizes to
// "default", matching the resolution chain's global fallback tier.
func NormalizeModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return "default"
	}
	return model
}
