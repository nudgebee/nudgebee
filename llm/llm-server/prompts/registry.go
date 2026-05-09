package prompts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"nudgebee/llm/config"
)

const PromptAgentK8sDebug = "agent_k8s_debug"
const PromptAgentK8sDebugReact = "agent_k8s_debug_react"
const PromptResponseFormatter = "executor_response_formatter"
const PromptResponseFormatterSlack = "executor_response_formatter_slack"
const PromptAgentLlm = "agent_llm"
const PromptRewooSolver = "planner_rewoo_solver"
const PromptReactBase = "planner_react_base"
const PromptReact3Base = "planner_react_3_base"

// promptMapping maps prompt name constants to their versioned system entries.
// Format: constantValue -> (fileName, category)
var promptMapping = map[string]struct {
	name     string
	category PromptCategory
}{
	PromptAgentK8sDebug:          {"k8s_debug", CategoryAgents},
	PromptAgentK8sDebugReact:     {"k8s_debug_react", CategoryAgents},
	PromptResponseFormatter:      {"response_formatter", CategoryUtilities},
	PromptResponseFormatterSlack: {"response_formatter_slack", CategoryUtilities},
	PromptAgentLlm:               {"agent_llm", CategoryUtilities},
	PromptRewooSolver:            {"rewoo_solver", CategoryUtilities},
	PromptReactBase:              {"react_base", CategoryUtilities},
	PromptReact3Base:             {"react_3_base", CategoryUtilities},
}

// GetPrompt retrieves a prompt for the given module and account.
// accountID enables account-level experiments and overrides; pass "" for global/default.
func GetPrompt(ctx context.Context, module string, accountID string, args ...any) string {
	mapping, exists := promptMapping[module]
	if !exists {
		slog.Error("prompts: prompt not found in mapping", "module", module)
		return ""
	}

	loader := GetLoader()
	if loader == nil {
		slog.Error("prompts: loader not initialized", "module", module)
		return ""
	}

	req := PromptRequest{
		Name:      mapping.name,
		Category:  mapping.category,
		Provider:  GetProviderFromConfig(),
		AccountID: accountID,
	}

	resp, err := loader.GetPrompt(ctx, req)
	if err != nil {
		slog.Error("prompts: failed to load prompt",
			"module", module,
			"name", mapping.name,
			"account_id", accountID,
			"error", err)
		return ""
	}

	data := resp.Content
	if len(args) > 0 {
		data = fmt.Sprintf(data, args...)
	}
	return data
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

// GetProviderFromConfig returns the LLM provider from config
func GetProviderFromConfig() string {
	provider := strings.ToLower(config.Config.LlmProvider)

	// Map config provider names to prompt system provider names
	switch provider {
	case "bedrock", "aws_bedrock":
		return "bedrock"
	case "azure", "azure_openai":
		return "azure"
	case "openai":
		return "openai"
	case "google", "googleai", "gemini":
		return "googleai"
	case "anthropic":
		return "anthropic"
	case "vertexai", "vertex":
		return "vertexai"
	default:
		return "default"
	}
}
