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

type ErrorRCAAgent struct {
	llmClient    *llm.Client
	planner      *planners.ReActPlanner
	logger       *common.Logger
	tools        []core.NBTool
	promptLoader *common.PromptLoader
}

func NewErrorRCAAgent(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string) *ErrorRCAAgent {
	// Create a default tracker for standalone usage
	tracker := common.NewToolInvocationTracker("error_rca_agent")
	return NewErrorRCAAgentWithTracker(cfg, llmClient, gitClient, logger, workspaceDir, tracker)
}

// NewErrorRCAAgentWithTracker creates an ErrorRCAAgent with a shared tracker (used by orchestrator)
func NewErrorRCAAgentWithTracker(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger, workspaceDir string, tracker *common.ToolInvocationTracker) *ErrorRCAAgent {

	// Initialize raw tools for the Error RCA agent
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
	rawTools = append(rawTools, tools.NewRipgrepTool(workspaceDir)) // Ripgrep for code search (regex, fast, replaces grep)
	rawTools = append(rawTools, tools.NewGitTool(workspaceDir))     // Add git tool for blame, log, etc.
	rawTools = append(rawTools, ghTool)                             // Add gh CLI tool for PR information
	rawTools = append(rawTools, tools.NewSubmitAnalysisTool())      // Note: Using the specific submit tool
	rawTools = append(rawTools, glabTool)

	// Add repo_clone tool - RCA needs to clone repo at Step 1 when URL provided
	// Note: Tool should only be used ONCE at start, not re-attempted mid-analysis
	if gitClient != nil {
		rawTools = append(rawTools, tools.NewRepoCloneTool(workspaceDir, gitClient))
	}

	// Use the provided shared tracker and wrap tools for comprehensive tracking
	trackedTools := make([]core.NBTool, len(rawTools))
	for i, tool := range rawTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, tracker, logger)
	}

	logger.Log(common.EventStepStart, "ErrorRCAAgent using shared tool tracker", map[string]any{
		"tool_count": len(trackedTools),
		"tracker_id": "shared_from_orchestrator",
	})

	// Initialize ReAct planner with tracked tools
	planner := planners.NewReActPlanner(llmClient, trackedTools, cfg.Agent.ReActMaxIterations)
	planner.SetLogger(logger)

	return &ErrorRCAAgent{
		llmClient:    llmClient,
		planner:      planner,
		logger:       logger,
		tools:        trackedTools,
		promptLoader: common.NewPromptLoader(),
	}
}

func (a *ErrorRCAAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
	if a.planner != nil {
		a.planner.SetLogger(logger)
	}
}

func (a *ErrorRCAAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext) (string, error) {
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
	systemPrompt, err := a.buildAuditorPrompt(sessionCtx)
	if err != nil {
		return "", fmt.Errorf("failed to build auditor prompt: %w", err)
	}

	// Execute the ReAct planner
	result, err := a.planner.Plan(ctx, enhancedQuery, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("error rca planner execution failed: %w", err)
	}

	// Record investigation process in scratchpad for context sharing
	investigationNotes := fmt.Sprintf("Investigation Query: %s\n\nSteps Taken: %d\nStatus: %s\n\nFinal Analysis: %s",
		enhancedQuery, len(result.Steps), result.Status, result.FinalAnswer)
	sessionCtx.AddToScratchpad("ErrorRCAAgent", investigationNotes)

	// CRITICAL: Return structured data from submit_analysis, not just the FinalAnswer text
	if structuredData := a.planner.GetSubmitAnalysisData(); structuredData != nil {
		if data, ok := structuredData.(map[string]any); ok {
			// CRITICAL: Always enforce requires_fix based on implementation_instructions
			// Override LLM's requires_fix if it conflicts with implementation_instructions presence
			if instructions, hasInstructions := data["implementation_instructions"].([]any); hasInstructions && len(instructions) > 0 {
				// Has implementation_instructions → MUST set requires_fix=true (override any false value)
				originalValue := data["requires_fix"]
				data["requires_fix"] = true
				if a.logger != nil {
					a.logger.Log(common.EventStepComplete, "Enforced requires_fix=true due to implementation_instructions", map[string]any{
						"instruction_count": len(instructions),
						"original_value":    originalValue,
						"overridden":        originalValue == false,
					})
				}
			} else {
				// No valid implementation_instructions → ALWAYS override requires_fix to false
				originalValue := data["requires_fix"]
				data["requires_fix"] = false
				if a.logger != nil {
					a.logger.Log(common.EventStepComplete, "Overrode requires_fix=false - no valid implementation_instructions", map[string]any{
						"original_value": originalValue,
						"reason":         "implementation_instructions nil or empty - CodeFixer would run blind",
					})
				}
			}
			// CRITICAL: Include working directory in the return data for orchestrator
			if workingDir := a.planner.GetSecureContext("working_directory"); workingDir != nil {
				if dir, ok := workingDir.(string); ok && dir != "" {
					data["working_directory"] = dir
				}
			}

			if jsonData, err := json.MarshalIndent(data, "", "  "); err == nil {
				a.logger.Log(common.EventStepComplete, "RCA returning structured data from submit_analysis", map[string]any{
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

func (a *ErrorRCAAgent) buildEnhancedQuery(sessionCtx *session.SessionContext) string {
	var data strings.Builder
	data.WriteString("=== USER QUERY ===\n")
	data.WriteString(sessionCtx.OriginalQuery)
	data.WriteString("\n\n")

	// Include repository information if available
	if sessionCtx.RepoContext != nil && sessionCtx.RepoContext.URL != "" {
		data.WriteString("=== REPOSITORY INFORMATION ===\n")
		fmt.Fprintf(&data, "Repository URL: %s\n", sessionCtx.RepoContext.URL)
		if sessionCtx.RepoContext.Branch != "" {
			fmt.Fprintf(&data, "Branch: %s\n", sessionCtx.RepoContext.Branch)
		}
		data.WriteString("IMPORTANT: Use the repo_clone tool first to clone this repository before analyzing files.\n")
		data.WriteString("\n\n")
	}

	if sessionCtx.InitialLogs != "" {
		data.WriteString("=== ERROR LOGS (COMPLETE) ===\n")
		data.WriteString(sessionCtx.InitialLogs)
		data.WriteString("\n\n")
	}

	return data.String()
}

func (a *ErrorRCAAgent) buildAuditorPrompt(sessionCtx *session.SessionContext) (string, error) {
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
		"Skills":        sessionCtx.SkillsContext,
	}

	// Choose appropriate prompt template based on context complexity
	templateName := a.choosePromptTemplate(sessionCtx)

	// Load and execute template
	promptTemplate, err := a.promptLoader.LoadPrompt(templateName, templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, fmt.Sprintf("Failed to load %s prompt template", templateName), map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load %s prompt template: %w", templateName, err)
	}

	return promptTemplate, nil
}

// choosePromptTemplate selects the appropriate prompt template based on analysis complexity
func (a *ErrorRCAAgent) choosePromptTemplate(sessionCtx *session.SessionContext) string {
	// Use complex template if any of these conditions are true:
	// 1. Repository context suggests multiple files/services are involved
	// 2. Error logs are extensive (>2000 characters indicating complex traces)
	// 3. Query suggests system-wide investigation ("across services", "multiple components", etc.)

	complexityIndicators := 0

	// Check repository context complexity
	if sessionCtx.RepoContext != nil {
		guidance := sessionCtx.RepoContext.GetRepositoryGuidance()
		if strings.Contains(strings.ToLower(guidance), "multiple") ||
			strings.Contains(strings.ToLower(guidance), "services") ||
			strings.Contains(strings.ToLower(guidance), "microservice") {
			complexityIndicators++
		}
	}

	// Check error log length and complexity
	if len(sessionCtx.InitialLogs) > 2000 {
		complexityIndicators++
	}

	// Check for stack traces (indicating deeper investigation needed)
	if strings.Contains(sessionCtx.InitialLogs, "Traceback") ||
		strings.Contains(sessionCtx.InitialLogs, "stack trace") ||
		strings.Contains(sessionCtx.InitialLogs, "at ") || // Java/JavaScript stack traces
		strings.Contains(sessionCtx.InitialLogs, "ValidationError") || // Pydantic validation errors
		strings.Contains(sessionCtx.InitialLogs, "pydantic") { // Any Pydantic-related errors
		complexityIndicators++
	}

	// Check query complexity keywords
	queryLower := strings.ToLower(sessionCtx.OriginalQuery)
	complexKeywords := []string{
		"across", "multiple", "system", "integration", "dependency",
		"microservice", "distributed", "performance", "memory",
		"deadlock", "race condition", "concurrency",
	}

	for _, keyword := range complexKeywords {
		if strings.Contains(queryLower, keyword) {
			complexityIndicators++
			break // Only count once for query complexity
		}
	}

	// Always use the main error_rca template to ensure consistent field names
	a.logger.Log(common.EventStepStart, "Using error_rca template", map[string]any{
		"complexity_indicators": complexityIndicators,
		"reason":                "Using unified template for consistent submit_analysis field names",
	})
	return "error_rca"
}
