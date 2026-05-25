package agents

import (
	"context"
	"fmt"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/git"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/models"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

type PerformanceDebuggerAgent struct {
	llmClient    *llm.Client
	planner      *planners.ReActPlanner
	logger       *common.Logger
	tools        []core.NBTool
	promptLoader *common.PromptLoader
}

func NewPerformanceDebuggerAgent(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string) *PerformanceDebuggerAgent {
	// Create a default tracker for standalone usage
	tracker := common.NewToolInvocationTracker("performance_debugger_agent")
	return NewPerformanceDebuggerAgentWithTracker(cfg, llmClient, gitClient, logger, workspaceDir, tracker)
}

// NewPerformanceDebuggerAgentWithTracker creates a PerformanceDebuggerAgent with a shared tracker (used by orchestrator)
func NewPerformanceDebuggerAgentWithTracker(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string, tracker *common.ToolInvocationTracker) *PerformanceDebuggerAgent {

	// Initialize raw tools for the Performance Debugger agent
	cliTool := tools.NewCLITool(workspaceDir)
	cliTool.SetRestrictPROperations(true) // Only orchestrator creates PRs

	var rawTools []core.NBTool
	rawTools = append(rawTools, cliTool)
	rawTools = append(rawTools, tools.NewSubmitAnalysisTool())

	// Use the provided shared tracker and wrap tools for comprehensive tracking
	trackedTools := make([]core.NBTool, len(rawTools))
	for i, tool := range rawTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, tracker, logger)
	}

	logger.Log(common.EventStepStart, "PerformanceDebuggerAgent using shared tool tracker", map[string]any{
		"tool_count": len(trackedTools),
		"tracker_id": "shared_from_orchestrator",
	})

	// Initialize ReAct planner with tracked tools
	planner := planners.NewReActPlanner(llmClient, trackedTools, cfg.Agent.ReActMaxIterations)
	planner.SetLogger(logger)

	return &PerformanceDebuggerAgent{
		llmClient:    llmClient,
		planner:      planner,
		logger:       logger,
		tools:        trackedTools,
		promptLoader: common.NewPromptLoader(),
	}
}

func (a *PerformanceDebuggerAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
	if a.planner != nil {
		a.planner.SetLogger(logger)
	}
}

func (a *PerformanceDebuggerAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext) (string, error) {
	a.planner.SetRepositoryContext(sessionCtx.RepoContext)

	// Set credentials for repository operations
	if sessionCtx.Credentials != nil {
		// Convert ResolvedCredentials to models.Credentials format expected by repo_clone tool
		modelCreds := &models.Credentials{
			Type:     sessionCtx.Credentials.Type,
			Value:    sessionCtx.Credentials.Token,
			Username: sessionCtx.Credentials.Username,
			Password: sessionCtx.Credentials.Password,
		}
		a.planner.SetSecureContext("credentials", modelCreds)
	}

	enhancedQuery := a.buildEnhancedQuery(sessionCtx)
	systemPrompt, err := a.buildPerformanceDebuggerPrompt(sessionCtx)
	if err != nil {
		return "", fmt.Errorf("failed to build performance debugger prompt: %w", err)
	}

	result, err := a.planner.Plan(ctx, enhancedQuery, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("performance debugger planner execution failed: %w", err)
	}

	return result.FinalAnswer, nil
}

func (a *PerformanceDebuggerAgent) buildEnhancedQuery(sessionCtx *session.SessionContext) string {
	var data strings.Builder
	data.WriteString("=== USER QUERY ===\n")
	data.WriteString(sessionCtx.OriginalQuery)
	data.WriteString("\n\n")

	if sessionCtx.InitialLogs != "" {
		data.WriteString("=== RELEVANT LOGS ===\n")
		data.WriteString(sessionCtx.InitialLogs)
		data.WriteString("\n\n")
	}

	return data.String()
}

func (a *PerformanceDebuggerAgent) buildPerformanceDebuggerPrompt(sessionCtx *session.SessionContext) (string, error) {
	contextInfo := ""
	if sessionCtx.RepoContext != nil {
		contextInfo = sessionCtx.RepoContext.GetRepositoryGuidance()
	}

	// Prepare template data (tools are now passed via native function calling, not prompt text)
	templateData := map[string]any{
		"ContextInfo":   contextInfo,
		"OriginalQuery": sessionCtx.OriginalQuery,
	}

	// Load and execute template
	prompt, err := a.promptLoader.LoadPrompt("performance_debugger", templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to load performance_debugger prompt template", map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load performance_debugger prompt template: %w", err)
	}

	return prompt, nil
}
