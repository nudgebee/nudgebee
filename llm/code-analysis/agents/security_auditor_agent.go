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

type SecurityAuditorAgent struct {
	llmClient    *llm.Client
	planner      *planners.ReActPlanner
	logger       *common.Logger
	tools        []core.NBTool
	promptLoader *common.PromptLoader
}

func NewSecurityAuditorAgent(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string) *SecurityAuditorAgent {
	// Create a default tracker for standalone usage
	tracker := common.NewToolInvocationTracker("security_auditor_agent")
	return NewSecurityAuditorAgentWithTracker(cfg, llmClient, gitClient, logger, workspaceDir, tracker)
}

// NewSecurityAuditorAgentWithTracker creates a SecurityAuditorAgent with a shared tracker (used by orchestrator)
func NewSecurityAuditorAgentWithTracker(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string, tracker *common.ToolInvocationTracker) *SecurityAuditorAgent {

	// Initialize raw tools for the Security Auditor agent
	// Security auditor needs file tools, git tools, and repo_clone for analyzing codebases
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
	rawTools = append(rawTools, tools.NewGrepTool(workspaceDir))
	rawTools = append(rawTools, tools.NewRipgrepTool(workspaceDir))
	rawTools = append(rawTools, tools.NewGitTool(workspaceDir))
	rawTools = append(rawTools, ghTool)
	rawTools = append(rawTools, tools.NewSubmitAnalysisTool())
	rawTools = append(rawTools, glabTool)

	// Add repo_clone tool - Security auditor needs to clone repo when URL provided
	if gitClient != nil {
		rawTools = append(rawTools, tools.NewRepoCloneTool(workspaceDir, gitClient))
	}

	// Use the provided shared tracker and wrap tools for comprehensive tracking
	trackedTools := make([]core.NBTool, len(rawTools))
	for i, tool := range rawTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, tracker, logger)
	}

	// Initialize ReAct planner with tracked tools
	planner := planners.NewReActPlanner(llmClient, trackedTools, cfg.Agent.ReActMaxIterations)
	planner.SetLogger(logger)

	return &SecurityAuditorAgent{
		llmClient:    llmClient,
		planner:      planner,
		logger:       logger,
		tools:        trackedTools,
		promptLoader: common.NewPromptLoader(),
	}
}

func (a *SecurityAuditorAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
	if a.planner != nil {
		a.planner.SetLogger(logger)
	}
}

func (a *SecurityAuditorAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext) (string, error) {
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
	systemPrompt, err := a.buildSecurityAuditorPrompt(sessionCtx)
	if err != nil {
		return "", fmt.Errorf("failed to build security auditor prompt: %w", err)
	}

	result, err := a.planner.Plan(ctx, enhancedQuery, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("security auditor planner execution failed: %w", err)
	}

	return result.FinalAnswer, nil
}

func (a *SecurityAuditorAgent) buildEnhancedQuery(sessionCtx *session.SessionContext) string {
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

func (a *SecurityAuditorAgent) buildSecurityAuditorPrompt(sessionCtx *session.SessionContext) (string, error) {
	contextInfo := ""
	if sessionCtx.RepoContext != nil {
		contextInfo = sessionCtx.RepoContext.GetRepositoryGuidance()
	}

	// Prepare template data (tools are now passed via native function calling, not prompt text)
	templateData := map[string]any{
		"ContextInfo":   contextInfo,
		"OriginalQuery": sessionCtx.OriginalQuery,
		"Skills":        sessionCtx.SkillsContext,
	}

	// Load and execute template
	prompt, err := a.promptLoader.LoadPrompt("security_auditor", templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to load security_auditor prompt template", map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load security_auditor prompt template: %w", err)
	}

	return prompt, nil
}
