package agents

import (
	"context"
	"encoding/json"
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

type CodeAgent struct {
	llmClient    *llm.Client
	planner      *planners.ReActPlanner
	logger       *common.Logger
	tools        []core.NBTool
	promptLoader *common.PromptLoader
}

func NewCodeAgent(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string) *CodeAgent {
	// Create a default tracker for standalone usage
	tracker := common.NewToolInvocationTracker("code_agent")
	return NewCodeAgentWithTracker(cfg, llmClient, gitClient, logger, workspaceDir, tracker)
}

// NewCodeAgentWithTracker creates a CodeAgent with a shared tracker (used by orchestrator)
func NewCodeAgentWithTracker(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string, tracker *common.ToolInvocationTracker) *CodeAgent {

	// Initialize raw tools for the Code agent
	// Restrict PR/MR creation — only the orchestrator creates PRs with proper formatting
	cliTool := tools.NewCLITool(workspaceDir)
	cliTool.SetRestrictPROperations(true)
	ghTool := tools.NewGHTool(workspaceDir)
	ghTool.SetRestrictPROperations(true)
	glabTool := tools.NewGLabTool(workspaceDir)
	glabTool.SetRestrictPROperations(true)

	var rawTools []core.NBTool
	rawTools = append(rawTools, cliTool)
	rawTools = append(rawTools, tools.NewFileFindTool(workspaceDir))
	rawTools = append(rawTools, tools.NewFileViewTool(workspaceDir))
	rawTools = append(rawTools, tools.NewRipgrepTool(workspaceDir))
	rawTools = append(rawTools, tools.NewSubmitAnalysisTool())

	// Add repo_clone tool so agent can decide when to clone repositories
	if gitClient != nil {
		rawTools = append(rawTools, tools.NewRepoCloneTool(workspaceDir, gitClient))
	}

	// Add git and gh tools for intelligent repository operations
	rawTools = append(rawTools, tools.NewGitTool(workspaceDir))
	rawTools = append(rawTools, ghTool)
	rawTools = append(rawTools, glabTool)

	// Use the provided shared tracker and wrap tools for comprehensive tracking
	trackedTools := make([]core.NBTool, len(rawTools))
	for i, tool := range rawTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, tracker, logger)
	}

	logger.Log(common.EventStepStart, "CodeAgent using shared tool tracker", map[string]any{
		"tool_count": len(trackedTools),
		"tracker_id": "shared_from_orchestrator",
	})

	// Initialize ReAct planner with tracked tools
	planner := planners.NewReActPlanner(llmClient, trackedTools, cfg.Agent.ReActMaxIterations)
	planner.SetLogger(logger)

	return &CodeAgent{
		llmClient:    llmClient,
		planner:      planner,
		logger:       logger,
		tools:        trackedTools,
		promptLoader: common.NewPromptLoader(),
	}
}

func (a *CodeAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
	if a.planner != nil {
		a.planner.SetLogger(logger)
	}
}

// SetToolTracker sets the tool invocation tracker for the code agent's planner
func (a *CodeAgent) SetToolTracker(tracker *common.ToolInvocationTracker) {
	if a.planner != nil {
		a.planner.SetToolTracker(tracker)
	}
}

func (a *CodeAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext) (string, error) {
	// Set repository context in the planner
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

	// Build the enhanced query and the specific prompt for this agent
	enhancedQuery := a.buildEnhancedQuery(sessionCtx)
	systemPrompt, err := a.buildCodeAgentPrompt(sessionCtx)
	if err != nil {
		return "", fmt.Errorf("failed to build code agent prompt: %w", err)
	}

	// Execute the ReAct planner
	result, err := a.planner.Plan(ctx, enhancedQuery, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("code agent planner execution failed: %w", err)
	}

	// Record investigation process in scratchpad for context sharing
	investigationNotes := fmt.Sprintf("Investigation Query: %s\n\nSteps Taken: %d\nStatus: %s\n\nFinal Analysis: %s",
		enhancedQuery, len(result.Steps), result.Status, result.FinalAnswer)
	sessionCtx.AddToScratchpad("CodeAgent", investigationNotes)

	// Return structured data from submit_analysis, not just the FinalAnswer text
	if structuredData := a.planner.GetSubmitAnalysisData(); structuredData != nil {
		if data, ok := structuredData.(map[string]any); ok {
			if jsonData, err := json.MarshalIndent(data, "", "  "); err == nil {
				a.logger.Log(common.EventStepComplete, "Code agent returning structured data from submit_analysis", map[string]any{
					"data_type": fmt.Sprintf("%T", data),
					"data_size": len(jsonData),
				})
				return string(jsonData), nil
			} else {
				a.logger.Log(common.EventStepFailure, "Failed to marshal structured data, falling back to FinalAnswer", map[string]any{
					"error": err.Error(),
				})
			}
		}
	} else {
		a.logger.Log(common.EventStepFailure, "No structured data from submit_analysis, using FinalAnswer", nil)
	}

	return result.FinalAnswer, nil
}

func (a *CodeAgent) buildEnhancedQuery(sessionCtx *session.SessionContext) string {
	var data strings.Builder
	data.WriteString("=== USER QUERY ===\n")
	data.WriteString(sessionCtx.OriginalQuery)
	data.WriteString("\n\n")

	if sessionCtx.InitialLogs != "" {
		data.WriteString("=== LOGS (if provided) ===\n")
		data.WriteString(sessionCtx.InitialLogs)
		data.WriteString("\n\n")
	}

	return data.String()
}

func (a *CodeAgent) buildCodeAgentPrompt(sessionCtx *session.SessionContext) (string, error) {
	contextInfo := ""
	if sessionCtx.RepoContext != nil {
		contextInfo = sessionCtx.RepoContext.GetRepositoryGuidance()
	}

	// Prepare template data (tools are now passed via native function calling, not prompt text)
	mode := sessionCtx.Mode
	if mode == "" {
		mode = "explore"
	}
	templateData := map[string]any{
		"ContextInfo":   contextInfo,
		"OriginalQuery": sessionCtx.OriginalQuery,
		"Mode":          mode,
		"IsExploreMode": mode == "explore",
		"IsFixMode":     mode == "fix",
	}

	// Load and execute template
	promptTemplate, err := a.promptLoader.LoadPrompt("code_agent", templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to load code_agent prompt template", map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load code_agent prompt template: %w", err)
	}

	return promptTemplate, nil
}
