package agents

import (
	"context"
	"fmt"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/llm"

	"github.com/tmc/langchaingo/llms"
)

type RouterAgent struct {
	llmClient    *llm.Client
	logger       *common.Logger
	promptLoader *common.PromptLoader
}

func NewRouterAgent(llmClient *llm.Client, logger *common.Logger) *RouterAgent {
	return &RouterAgent{
		llmClient:    llmClient,
		logger:       logger,
		promptLoader: common.NewPromptLoader(),
	}
}

func (a *RouterAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext) (string, error) {
	prompt, err := a.buildRouterPrompt(sessionCtx.OriginalQuery)
	if err != nil {
		return "", fmt.Errorf("failed to build router prompt: %w", err)
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	resp, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("router LLM call failed: %w", err)
	}

	// Validate and clean the response
	agentName := a.validateAgentName(resp.Choices[0].Content)
	return agentName, nil
}

func (a *RouterAgent) buildRouterPrompt(query string) (string, error) {
	// Prepare template data
	templateData := map[string]any{
		"Query": query,
	}

	// Load and execute template
	prompt, err := a.promptLoader.LoadPrompt("router_agent", templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to load router_agent prompt template", map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load router_agent prompt template: %w", err)
	}

	return prompt, nil
}

// validateAgentName ensures the router returns a valid agent name
func (a *RouterAgent) validateAgentName(response string) string {
	// Clean up the response
	cleaned := strings.TrimSpace(strings.ToLower(response))

	// Remove quotes if present
	cleaned = strings.Trim(cleaned, "\"'`")

	// Valid agent names (security queries routed to error_rca)
	validAgents := []string{"code_agent", "error_rca", "performance_debugger"}

	// Check if the response contains one of the valid agent names
	for _, agent := range validAgents {
		if strings.Contains(cleaned, agent) {
			return agent
		}
	}

	// Default fallback based on keywords in the response
	response_lower := strings.ToLower(response)

	// Look for error-related keywords
	errorKeywords := []string{"error", "bug", "crash", "exception", "fail", "segmentation", "fault", "traceback"}
	for _, keyword := range errorKeywords {
		if strings.Contains(response_lower, keyword) {
			return "error_rca"
		}
	}

	// Look for performance-related keywords
	perfKeywords := []string{"performance", "slow", "memory", "cpu", "optimization", "latency", "timeout"}
	for _, keyword := range perfKeywords {
		if strings.Contains(response_lower, keyword) {
			return "performance_debugger"
		}
	}

	// Look for security-related keywords - route to error_rca which has all tools including repo_clone
	secKeywords := []string{"security", "vulnerability", "cve", "auth", "permission", "injection", "xss"}
	for _, keyword := range secKeywords {
		if strings.Contains(response_lower, keyword) {
			return "error_rca"
		}
	}

	// Final fallback - default to code_agent for general queries
	return "code_agent"
}
