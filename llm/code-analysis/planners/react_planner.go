package planners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/tools/core"

	"github.com/tmc/langchaingo/llms"
)

const (
	// defaultMaxConsecutiveFailures is the number of consecutive failures of the same tool
	// before the circuit breaker forces the agent to try a different approach.
	// Set to 5 to accommodate build verification retries (install deps, scope adjustments).
	defaultMaxConsecutiveFailures = 5
)

var (
	// Regex for git blame output (from our previous refactor)
	blameRegex = regexp.MustCompile(`^?([a-f0-9]{8,})\s+\(?([a-zA-Z0-9_\-\s]+?)\s+(20\d{2}-\d{2}-\d{2})`)
)

type Message struct {
	Role    string `json:"role"`              // "user", "assistant", "tool"
	Content string `json:"content"`           // Actual content
	ToolID  string `json:"tool_id,omitempty"` // For tool messages
}

type ReActPlanner struct {
	llmClient              *llm.Client
	genaiSession           *llm.GenAISession      // Per-Plan genai recording state. Reset on each Plan() call so concurrent analyses on the same *llm.Client don't share thought_signature recordings.
	tools                  map[string]core.NBTool // Use map for faster lookup
	toolDefs               []llm.ToolDefinition   // Tool definitions for native function calling
	maxIterations          int
	secureContext          map[string]any
	tempWorkspace          string // Temporary workspace directory for this planning session
	currentSteps           []Step // Track steps for validation
	logger                 *common.Logger
	lastSubmitAnalysisData any                // Store structured data from submit_analysis tool
	repositoryContext      *RepositoryContext // Repository and troubleshooting context

	// Structured memory approach (User-defined, parallel to LLM message list)
	messages    []Message         // Clean conversation + tool outputs
	toolOutputs map[string]string // tool_id -> raw output for reference

	// Exploration tracking
	consecutiveExplorationCount int // Track consecutive exploration commands
	explorationLimit            int // Maximum consecutive exploration commands

	// Pattern detection for repetitive loops
	responseHistory   []string // Track recent responses to detect loops
	patternWindow     int      // How many responses to check for patterns
	analysisLoopCount int      // Count of detected analysis loops
	maxAnalysisLoops  int      // Maximum analysis loops before forcing submit

	// Retry tracking for submit_analysis
	submitRetryCount int // Count of submit_analysis retry attempts
	maxSubmitRetries int // Maximum submit_analysis retries

	// Circuit breaker for repeated tool failures
	consecutiveToolFailures map[string]int // tool_name -> consecutive failure count
	maxConsecutiveFailures  int            // Maximum consecutive failures before forcing different approach

	// Tool invocation tracking
	toolTracker *common.ToolInvocationTracker // Track all tool invocations

	// Observation limits
	maxObservationLines int // Maximum lines per tool observation (0 = unlimited)
	maxContextTokens    int // Approximate max context tokens before compaction

	// Deduplication tracking
	executedCallHashes map[string]int // hash -> execution count
	mu                 sync.Mutex     // Guards executedCallHashes and other shared state
}

// RepositoryContext provides comprehensive repository and troubleshooting information
type RepositoryContext struct {
	URL           string   `json:"url"`                      // Git repository URL
	Branch        string   `json:"branch"`                   // Current branch
	DefaultBranch string   `json:"default_branch"`           // Default branch (main/master)
	LocalPath     string   `json:"local_path"`               // Local repository path
	GitHubRepo    string   `json:"git_repo,omitempty"`       // Extracted owner/repo for GitHub operations
	GitLabProject string   `json:"gitlab_project,omitempty"` // Extracted group/project for GitLab operations (supports nested groups)
	GitProvider   string   `json:"git_provider"`             // Git provider: "github", "gitlab", or empty for auto-detect
	WorkloadInfo  string   `json:"workload_info,omitempty"`  // Context about the workload being analyzed
	AnalysisType  string   `json:"analysis_type,omitempty"`  // Type of analysis (error_investigation, etc.)
	IsMonorepo    bool     `json:"is_monorepo,omitempty"`    // Whether this is a monorepo with multiple modules
	KnownModules  []string `json:"known_modules,omitempty"`  // List of known modules/services in the repo
}

// isRepositoryActuallyCloned checks if the repository is actually cloned and ready for file operations
func (rc *RepositoryContext) isRepositoryActuallyCloned() bool {
	if rc.LocalPath == "" {
		return false
	}

	// Check if the directory exists and contains a .git directory
	gitDir := filepath.Join(rc.LocalPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false
	}

	return true
}

// GetRepositoryGuidance returns context-aware guidance for agents
func (rc *RepositoryContext) GetRepositoryGuidance() string {
	if rc == nil {
		return "## REPOSITORY CONTEXT\nNo repository context available - performing log-only analysis\n"
	}

	guidance := "## REPOSITORY CONTEXT\n"
	if rc.URL != "" {
		guidance += fmt.Sprintf("- Repository: %s\n", rc.URL)
	}
	if rc.GitHubRepo != "" {
		guidance += fmt.Sprintf("- GitHub: %s\n", rc.GitHubRepo)
	}
	if rc.Branch != "" {
		guidance += fmt.Sprintf("- Branch: %s\n", rc.Branch)
	}
	if rc.LocalPath != "" {
		guidance += fmt.Sprintf("- Local Path: %s\n", rc.LocalPath)
	}

	// Add intelligent scope guidance based on actual repository status
	guidance += rc.generateIntelligentWorkingScope()

	return guidance
}

// generateIntelligentWorkingScope creates dynamic guidance based on repository state and task requirements
func (rc *RepositoryContext) generateIntelligentWorkingScope() string {
	scope := "\n## INTELLIGENT WORKING APPROACH\n"

	if rc.isRepositoryActuallyCloned() {
		scope += "**Repository Status:** ✅ Ready for analysis\n"
		scope += "**Available Operations:** Direct file access, git history, code search\n"
		scope += "**Recommended Approach:** Analyze code structure to understand the problem domain\n"
	} else if rc.URL != "" {
		scope += "**Repository Status:** 🔄 Requires cloning for file access\n"
		scope += "**Critical First Step:** Use `repo_clone` tool to establish workspace\n"
		scope += "**Analysis Strategy:** Clone first, then investigate file structure and code\n"
		scope += "**Tool Sequence:** repo_clone → file_find → file_view → analysis\n"
	} else {
		scope += "**Repository Status:** ❌ No repository access available\n"
		scope += "**Analysis Mode:** Log-only analysis with provided data\n"
		scope += "**Strategy:** Extract insights from error messages and stack traces\n"
	}

	scope += "\n**Intelligence Guidelines:**\n"
	scope += "- Analyze the problem context to determine what information you need\n"
	scope += "- Choose tools based on the specific investigation requirements\n"
	scope += "- If you need file access but don't have repository cloned, clone first\n"
	scope += "- Focus on understanding the root cause, not just surface symptoms\n"

	return scope
}

// DetectMonorepoStructure analyzes the repository to detect if it's a monorepo
// by scanning for multiple build system files in different subdirectories.
func (rc *RepositoryContext) DetectMonorepoStructure() {
	if rc == nil {
		return
	}

	// Only scan if the repository is actually cloned
	if !rc.isRepositoryActuallyCloned() {
		return
	}

	buildFiles := []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml", "build.gradle", "Makefile"}
	moduleMap := make(map[string]bool)

	entries, err := os.ReadDir(rc.LocalPath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" ||
			name == "build" || name == "dist" || name == "__pycache__" || name == "deploy" {
			continue
		}
		for _, bf := range buildFiles {
			buildPath := filepath.Join(rc.LocalPath, name, bf)
			if _, statErr := os.Stat(buildPath); statErr == nil {
				moduleMap[name] = true
				break
			}
		}
	}

	if len(moduleMap) >= 2 {
		rc.IsMonorepo = true
		rc.KnownModules = make([]string, 0, len(moduleMap))
		for mod := range moduleMap {
			rc.KnownModules = append(rc.KnownModules, mod)
		}
	}
}

type Step struct {
	Number      int            `json:"step_number"`
	Thought     string         `json:"thought"`
	Action      string         `json:"action"`
	ActionInput map[string]any `json:"action_input"`
	Observation string         `json:"observation"`
	Status      string         `json:"status"` // "parsing", "executing", "completed", "failed"
	Error       string         `json:"error,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"` // ID from LLM for request/response correlation
}

type PlannerResult struct {
	Steps       []Step `json:"steps"`
	FinalAnswer string `json:"final_answer"`
	Status      string `json:"status"` // "completed", "failed", "max_iterations"
	Iterations  int    `json:"iterations"`
	Error       string `json:"error,omitempty"`
}

// convertNBToolsToToolDefs converts our internal tool definitions to llm.ToolDefinition
// for native function calling. This is computed once at construction time.
func convertNBToolsToToolDefs(tools map[string]core.NBTool) []llm.ToolDefinition {
	result := make([]llm.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		schema := tool.InputSchema()
		result = append(result, llm.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters: map[string]any{
				"type":       schema.Type,
				"properties": schema.Properties,
				"required":   schema.Required,
			},
		})
	}
	return result
}

func NewReActPlanner(llmClient *llm.Client, tools []core.NBTool, maxIterations int) *ReActPlanner {
	toolMap := make(map[string]core.NBTool)
	for _, tool := range tools {
		toolMap[tool.Name()] = tool
	}

	return &ReActPlanner{
		llmClient:                   llmClient,
		tools:                       toolMap,
		toolDefs:                    convertNBToolsToToolDefs(toolMap),
		maxIterations:               maxIterations,
		secureContext:               make(map[string]any),
		messages:                    make([]Message, 0),
		toolOutputs:                 make(map[string]string),
		consecutiveExplorationCount: 0,
		explorationLimit:            3, // Maximum 3 consecutive exploration commands
		responseHistory:             make([]string, 0),
		patternWindow:               5, // Check last 5 responses for patterns
		analysisLoopCount:           0,
		maxAnalysisLoops:            3, // Force submit after 3 detected loops
		submitRetryCount:            0,
		maxSubmitRetries:            2,                    // Allow 2 retries for submit_analysis
		consecutiveToolFailures:     make(map[string]int), // Circuit breaker tracking
		maxConsecutiveFailures:      defaultMaxConsecutiveFailures,
		maxObservationLines:         500,
		maxContextTokens:            200000,
		executedCallHashes:          make(map[string]int),
	}
}

// SetObservationLimits configures observation truncation.
func (p *ReActPlanner) SetObservationLimits(maxLines, maxContextTokens int) {
	if maxLines > 0 {
		p.maxObservationLines = maxLines
	}
	if maxContextTokens > 0 {
		p.maxContextTokens = maxContextTokens
	}
}

// SetMaxContextTokens configures the approximate max context tokens.
func (p *ReActPlanner) SetMaxContextTokens(tokens int) {
	if tokens > 0 {
		p.maxContextTokens = tokens
	}
}

// ResetCallHashes clears the dedup tracker (useful between agent phases).
func (p *ReActPlanner) ResetCallHashes() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executedCallHashes = make(map[string]int)
}

// hashToolCall produces a deterministic hash for a (action, input) pair,
// ignoring fields that vary between retries (e.g. working_directory).
func (p *ReActPlanner) hashToolCall(action string, input map[string]any) string {
	filtered := make(map[string]any, len(input))
	for k, v := range input {
		if k == "working_directory" {
			continue
		}
		filtered[k] = v
	}
	keys := make([]string, 0, len(filtered))
	for k := range filtered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte(action))
	for _, k := range keys {
		h.Write([]byte(k))
		v, _ := json.Marshal(filtered[k])
		h.Write(v)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// parseStepsFromToolCalls parses ALL tool calls from a single LLM response.
func (p *ReActPlanner) parseStepsFromToolCalls(resp *llms.ContentChoice) []Step {
	var steps []Step
	thought := strings.TrimSpace(resp.Content)

	for _, tc := range resp.ToolCalls {
		if tc.FunctionCall == nil {
			continue
		}
		input := make(map[string]any)
		if tc.FunctionCall.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &input); err != nil {
				slog.Error("Failed to parse tool call args", "error", err)
				continue
			}
		}
		steps = append(steps, Step{
			Action:      tc.FunctionCall.Name,
			ActionInput: input,
			Thought:     thought,
			ToolCallID:  tc.ID,
			Status:      "parsing",
		})
		thought = "" // only first step gets the thought
	}
	return steps
}

// stepActions returns a slice of action names from steps.
func stepActions(steps []Step) []string {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Action
	}
	return names
}

// executeSteps runs steps concurrently if ALL are read-only, otherwise serially.
func (p *ReActPlanner) executeSteps(ctx context.Context, steps []Step) {
	allReadOnly := true
	for _, s := range steps {
		tool, ok := p.tools[s.Action]
		if !ok {
			allReadOnly = false
			break
		}
		if ro, ok := tool.(core.ReadOnlyTool); !ok || !ro.IsReadOnly() {
			allReadOnly = false
			break
		}
	}

	if !allReadOnly || len(steps) <= 1 {
		// Serial execution
		for i := range steps {
			p.executeStep(ctx, &steps[i])
		}
		return
	}

	// Concurrent execution with semaphore
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i := range steps {
		wg.Add(1)
		sem <- struct{}{}
		go func(s *Step) {
			defer wg.Done()
			defer func() { <-sem }()
			p.executeStep(ctx, s)
		}(&steps[i])
	}
	wg.Wait()
}

// SetLogger sets the structured logger for the planner
func (p *ReActPlanner) SetLogger(logger *common.Logger) {
	p.logger = logger
}

// SetToolTracker sets the tool invocation tracker for the planner
func (p *ReActPlanner) SetToolTracker(tracker *common.ToolInvocationTracker) {
	p.toolTracker = tracker
}

func (p *ReActPlanner) Plan(ctx context.Context, query string, systemPrompt string) (*PlannerResult, error) {
	result := &PlannerResult{
		Steps:  []Step{},
		Status: "in_progress",
	}

	// Fresh genai recording session for this analysis. Per-Plan isolation
	// prevents thought_signature drift when two analyses run concurrently
	// on the same *llm.Client (e.g., an in-flight analyze plus a PR-lifecycle
	// followup on the same workspace pod) — each gets its own recording slice
	// instead of stomping on a shared one.
	p.genaiSession = llm.NewGenAISession()

	// Initialize step tracking and reset submit_analysis data
	p.currentSteps = []Step{}
	p.lastSubmitAnalysisData = nil
	p.toolOutputs = make(map[string]string)
	p.consecutiveExplorationCount = 0 // Reset exploration tracking
	// Reset pattern detection tracking
	p.responseHistory = make([]string, 0)
	p.analysisLoopCount = 0
	// Reset submit_analysis retry tracking
	p.submitRetryCount = 0
	// Initialize the user's parallel message tracker
	p.messages = []Message{
		{Role: "user", Content: query},
	}

	// Create temporary workspace
	if _, hasLocalPath := p.secureContext["repository_path"]; !hasLocalPath {
		if err := p.createTempWorkspace(); err != nil {
			return nil, fmt.Errorf("failed to create temporary workspace: %w", err)
		}
		defer p.cleanupTempWorkspace()
	}

	if p.logger != nil {
		p.logger.Log(common.EventPlanningStart, "ReActPlanner starting", map[string]any{
			"query_length": len(query),
		})
	}

	// Create the initial structured message list for the LLM.
	// Tools are passed via llms.WithTools() in callLLM(), not in the prompt text.
	llmConversation := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextPart(systemPrompt)},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextPart(fmt.Sprintf("Query: %s\n\nAnalyze this using the available tools. Call submit_analysis when you have your findings.", query))},
		},
	}

	for iteration := 0; iteration < p.maxIterations; iteration++ {
		result.Iterations = iteration + 1
		stepNumber := iteration + 1

		// Token-aware compaction: compact when estimated tokens exceed 70% of budget
		// or message count exceeds 40 (fallback for models without token counting).
		if estimateMessageTokens(llmConversation) > p.maxContextTokens*70/100 || len(llmConversation) > 40 {
			llmConversation = p.compactConversationWindow(llmConversation)
		}

		// Step budget awareness: inform the agent when running low on steps
		remainingSteps := p.maxIterations - iteration
		if remainingSteps == 5 && !p.hasCalledSubmitAnalysis() {
			budgetWarning := fmt.Sprintf(
				"[SYSTEM] You have %d steps remaining out of %d total. "+
					"Start wrapping up your investigation. If you have identified the root cause, "+
					"design your implementation_instructions and call submit_analysis soon.",
				remainingSteps, p.maxIterations)
			llmConversation = append(llmConversation, llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextPart(budgetWarning)},
			})
			if p.logger != nil {
				p.logger.Log(common.EventPlanningProgress, "Injected step budget warning", map[string]any{
					"remaining_steps": remainingSteps,
					"iteration":       iteration,
				})
			}
		}

		// Force submit_analysis if approaching max iterations.
		// This is the agent's last chance to produce a final answer — there are no LLM
		// rounds left to retry, so the loop must always terminate here regardless of
		// whether submit_analysis returns success or error.
		if iteration >= p.maxIterations-1 && !p.hasCalledSubmitAnalysis() {
			if p.logger != nil {
				p.logger.Log(common.EventPlanningComplete, "Forcing submit_analysis...", map[string]any{"iteration": iteration + 1})
			}
			forcedStep := p.createForcedSubmitAnalysisStep(ctx, stepNumber, query, systemPrompt)
			result.Steps = append(result.Steps, forcedStep)
			p.currentSteps = append(p.currentSteps, forcedStep)
			p.executeStep(ctx, &forcedStep) // This execution calls injectToolOutputs
			result.FinalAnswer = p.extractFinalAnswer(&forcedStep)
			if forcedStep.Status == "completed" {
				result.Status = "completed"
			} else {
				// Salvage the planner-constructed input (already guarded to have non-empty
				// title/description) so the orchestrator gets structured data instead of
				// an empty FinalAnswer / parse-error envelope.
				p.salvageForcedSubmitInput(&forcedStep)
				result.Status = "max_iterations"
				result.Error = fmt.Sprintf("forced submit_analysis tool errored: %s", forcedStep.Error)
			}
			break
		}

		// Report progress: agent is thinking about next step
		if p.logger != nil && iteration > 0 {
			common.SetProgress(p.logger.GetAnalysisID(), "Analyzing findings and planning next step...")
		}

		// Get LLM response with native function calling
		choice, err := p.callLLM(ctx, llmConversation)
		if err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("LLM call failed: %v", err)
			return result, err
		}

		// Parse ALL tool calls from the LLM response
		steps := p.parseStepsFromToolCalls(choice)

		// Handle text-only response (no tool calls) — nudge model
		if len(steps) == 0 {
			thought := strings.TrimSpace(choice.Content)
			if p.logger != nil {
				p.logger.Log(common.EventPlanningProgress, "Model returned text without tool call, nudging", map[string]any{
					"step":           stepNumber,
					"thought_length": len(thought),
				})
			}
			llmConversation = append(llmConversation, llms.MessageContent{
				Role:  llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.TextContent{Text: thought}},
			})
			llmConversation = append(llmConversation, llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: "Please use one of the available tools to continue your analysis, or call submit_analysis to provide your final answer."}},
			})
			continue
		}

		if p.logger != nil {
			p.logger.Log(common.EventPlanningProgress, "Parsed steps from LLM response", map[string]any{
				"step_count": len(steps),
				"actions":    stepActions(steps),
			})
		}

		// Number the steps, log, and report progress
		for i := range steps {
			steps[i].Number = stepNumber + i
			if p.logger != nil {
				p.logger.StepStart(steps[i].Number, steps[i].Action, steps[i].Thought)
				p.logger.ToolStart(steps[i].Action, steps[i].ActionInput)
				progressText := p.buildToolProgressText(steps[i].Action, steps[i].ActionInput)
				common.SetProgress(p.logger.GetAnalysisID(), progressText)
			}
		}

		// Execute all steps (concurrently if all read-only)
		p.executeSteps(ctx, steps)

		// Post-execution: per-step handling
		loopDone := false
		for i := range steps {
			s := &steps[i]
			result.Steps = append(result.Steps, *s)
			p.currentSteps = append(p.currentSteps, *s)

			if p.logger != nil {
				p.logger.StepComplete(s.Number, s.Action, p.truncate(s.Observation, 200))
			}

			// Circuit breaker per tool
			switch s.Status {
			case "failed", "retriable_failed":
				p.consecutiveToolFailures[s.Action]++
				failCount := p.consecutiveToolFailures[s.Action]
				if failCount >= p.maxConsecutiveFailures {
					circuitMsg := fmt.Sprintf(
						"[SYSTEM] Tool '%s' has failed %d times consecutively. "+
							"Stop using this tool and try a different approach. "+
							"If you have gathered enough information, call submit_analysis with your findings.",
						s.Action, failCount)
					llmConversation = append(llmConversation, llms.MessageContent{
						Role:  llms.ChatMessageTypeHuman,
						Parts: []llms.ContentPart{llms.TextPart(circuitMsg)},
					})
				}
			case "completed":
				p.consecutiveToolFailures[s.Action] = 0
			}

			// Handle submit_analysis completion
			if s.Action == "submit_analysis" {
				if s.Status == "completed" {
					result.FinalAnswer = p.extractFinalAnswer(s)
					result.Status = "completed"
					loopDone = true
					break
				} else if s.Status == "retriable_failed" {
					p.submitRetryCount++
					if p.submitRetryCount > p.maxSubmitRetries {
						result.FinalAnswer = p.extractFinalAnswer(s)
						result.Status = "failed"
						result.Error = fmt.Sprintf("submit_analysis failed after %d retries: %s", p.submitRetryCount, s.Error)
						loopDone = true
						break
					}
				} else {
					result.FinalAnswer = p.extractFinalAnswer(s)
					result.Status = "failed"
					result.Error = fmt.Sprintf("submit_analysis failed: %s", s.Error)
					loopDone = true
					break
				}
			}
		}
		if loopDone {
			break
		}

		// Update conversation with all steps
		llmConversation = p.updateConversationMessagesMulti(llmConversation, steps)

		// Check for consecutive failures
		lastStep := steps[len(steps)-1]
		if lastStep.Status == "failed" {
			if p.logger != nil {
				p.logger.ToolFailure(lastStep.Action, fmt.Errorf("%s", lastStep.Error))
			}
			if p.countConsecutiveFailures() >= 3 {
				result.Status = "max_failures"
				result.Error = "Too many consecutive tool failures"
				break
			}
		}

	}

	// (Rest of the function logic remains the same)
	if result.Status == "in_progress" {
		result.Status = "max_iterations"
		result.Error = "Maximum iterations reached without completion"
	}
	if result.Status != "completed" && p.logger != nil {
		p.logger.Error(common.EventPlanningFailure, "Planning failed", fmt.Errorf("%s", result.Error), map[string]any{
			"status":     result.Status,
			"iterations": result.Iterations,
		})
	} else if p.logger != nil {
		p.logger.Log(common.EventPlanningComplete, "Planning successful", map[string]any{
			"iterations": result.Iterations,
		})
	}
	return result, nil
}

// callLLM sends messages to the LLM with native function calling.
// Uses GenerateContentWithTools which bypasses langchaingo's broken tool conversion
// for GoogleAI and uses the genai SDK directly with proper nested schema support.
// Returns the structured ContentChoice containing both text (thought) and tool calls.
func (p *ReActPlanner) callLLM(ctx context.Context, messages []llms.MessageContent) (*llms.ContentChoice, error) {
	llmCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Use GenerateContentWithTools for proper tool schema handling
	response, err := p.llmClient.GenerateContentWithTools(llmCtx, messages, p.toolDefs, p.genaiSession)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	choice := response.Choices[0]

	// Log response details for observability
	if p.logger != nil {
		p.logger.Log(common.EventPlanningProgress, "LLM response received", map[string]any{
			"text_length":     len(choice.Content),
			"tool_call_count": len(choice.ToolCalls),
			"stop_reason":     choice.StopReason,
		})
	}

	return choice, nil
}

// extractJSONFromContent remains unchanged
func (p *ReActPlanner) extractJSONFromContent(logger *common.Logger, content string) string {
	// Defensive logging - show what we're trying to extract
	if logger != nil {
		// Show first and last 100 characters to diagnose truncation issues
		preview := content
		if len(content) > 200 {
			preview = content[:100] + " ... " + content[len(content)-100:]
		}
		logger.Log(common.EventPlanningProgress, "Attempting JSON extraction", map[string]any{
			"content_length": len(content),
			"preview":        preview,
			"starts_with":    content[:min(50, len(content))],
			"ends_with":      content[max(0, len(content)-50):],
		})
	}

	content = strings.ReplaceAll(content, "```json", "")
	content = strings.ReplaceAll(content, "```", "")
	content = strings.TrimSpace(content)

	// Check if content appears truncated (doesn't end with })
	isTruncated := !strings.HasSuffix(strings.TrimSpace(content), "}")
	if isTruncated && logger != nil {
		logger.Log(common.EventStepFailure, "Content appears truncated - missing closing brace", map[string]any{
			"content_length": len(content),
			"last_50_chars":  content[max(0, len(content)-50):],
		})
	}

	// Primary strategy: Brace counting
	if startIdx := strings.Index(content, "{"); startIdx != -1 {
		braceCount := 0
		inString := false
		escaped := false
		for i := startIdx; i < len(content); i++ {
			char := content[i]
			if char == '"' && !escaped {
				inString = !inString
			}
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if !inString {
				if char == '{' {
					braceCount++
				} else if char == '}' {
					braceCount--
					if braceCount == 0 {
						jsonStr := content[startIdx : i+1]
						// Validate before returning
						if json.Valid([]byte(jsonStr)) {
							var test map[string]any
							if err := json.Unmarshal([]byte(jsonStr), &test); err == nil {
								if logger != nil {
									logger.Log(common.EventPlanningProgress, "Successfully extracted valid JSON via brace counting", map[string]any{
										"json_length": len(jsonStr),
										"has_keys":    len(test),
									})
								}
								return jsonStr
							}
						} else if logger != nil {
							logger.Log(common.EventStepFailure, "Brace counting found JSON but json.Valid() failed", map[string]any{
								"json_preview": jsonStr[:min(200, len(jsonStr))],
							})
						}
						break
					}
				}
			}
		}
	}

	// Fallback regex - try to find JSON without corrupting it
	// Use greedy matching to capture the largest JSON object
	jsonRegex := regexp.MustCompile(`(?s)\{.*\}`)
	matches := jsonRegex.FindAllString(content, -1)

	if logger != nil && len(matches) > 0 {
		logger.Log(common.EventPlanningProgress, "Trying regex fallback extraction", map[string]any{
			"match_count": len(matches),
		})
	}

	// Try largest matches first (reverse order)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		cleanMatch := strings.TrimSpace(match)
		// Validate before attempting unmarshal
		if json.Valid([]byte(cleanMatch)) {
			var test map[string]any
			// DO NOT remove newlines/tabs - they may be part of valid JSON strings
			if err := json.Unmarshal([]byte(cleanMatch), &test); err == nil {
				if logger != nil {
					logger.Log(common.EventPlanningProgress, "Successfully extracted via regex fallback", map[string]any{
						"json_length": len(cleanMatch),
					})
				}
				return cleanMatch
			}
		}
	}

	// No valid JSON found
	if logger != nil {
		logger.Log(common.EventStepFailure, "Failed to extract any valid JSON", map[string]any{
			"content_length": len(content),
			"is_truncated":   isTruncated,
			"has_open_brace": strings.Contains(content, "{"),
		})
	}
	return ""
}

// repairTruncatedJSON attempts to fix JSON that was truncated by closing open braces/brackets.
func (p *ReActPlanner) repairTruncatedJSON(content string) string {
	content = strings.ReplaceAll(content, "```json", "")
	content = strings.ReplaceAll(content, "```", "")
	content = strings.TrimSpace(content)

	startIdx := strings.Index(content, "{")
	if startIdx == -1 {
		return ""
	}
	content = content[startIdx:]

	// Truncate at last complete key-value pair (find last complete string value or closing bracket)
	// Then close open structures
	inString := false
	escaped := false
	var stack []byte // track { and [

	lastGoodEnd := -1
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == ch {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				// Fully balanced — use extractJSONFromContent instead
				return ""
			}
		case ',':
			// A comma outside strings means prior value was complete
			lastGoodEnd = i
		}
	}

	if len(stack) == 0 || lastGoodEnd < 1 {
		return ""
	}

	// Truncate at last comma (remove trailing incomplete value) and close open structures
	repaired := content[:lastGoodEnd]

	// Trim trailing commas or partial content
	repaired = strings.TrimRight(repaired, " \t\n\r,")

	// Close all open structures
	for i := len(stack) - 1; i >= 0; i-- {
		repaired += string(stack[i])
	}

	if json.Valid([]byte(repaired)) {
		var test map[string]any
		if err := json.Unmarshal([]byte(repaired), &test); err == nil {
			if p.logger != nil {
				p.logger.Log(common.EventPlanningProgress, "Repaired truncated JSON", map[string]any{
					"original_length": len(content),
					"repaired_length": len(repaired),
					"keys":            len(test),
					"open_structures": len(stack),
				})
			}
			return repaired
		}
	}
	return ""
}

// isExplorationCommand checks if a command is exploratory (ls, find without specific purpose)
func (p *ReActPlanner) isExplorationCommand(action string, actionInput map[string]any) bool {
	if action != "cli" {
		return false
	}

	if command, ok := actionInput["command"].(string); ok {
		command = strings.TrimSpace(strings.ToLower(command))
		// Check for basic directory listing commands
		return strings.HasPrefix(command, "ls ") || command == "ls" ||
			strings.HasPrefix(command, "ls -") ||
			(strings.HasPrefix(command, "find") && !strings.Contains(command, "-name") && !strings.Contains(command, "-type f"))
	}
	return false
}

// executeStep is now updated to store raw tool output
func (p *ReActPlanner) executeStep(ctx context.Context, step *Step) {
	if step.Status != "parsing" {
		return
	}

	// Check exploration limits before execution
	if p.isExplorationCommand(step.Action, step.ActionInput) {
		p.mu.Lock()
		p.consecutiveExplorationCount++
		explCount := p.consecutiveExplorationCount
		p.mu.Unlock()
		if explCount > p.explorationLimit {
			step.Status = "failed"
			step.Error = fmt.Sprintf("Too many consecutive exploration commands (%d). Instead of ls, try: 1) 'grep -r \"keyword\" .' to find relevant files, 2) 'find . -name \"*pattern*\" -type f' to locate specific files, or 3) directly analyze files you've already found.", p.consecutiveExplorationCount)
			step.Observation = "Exploration limit exceeded. Use targeted search commands like grep or find instead of ls."
			if p.logger != nil {
				p.logger.Log(common.EventStepFailure, "Exploration limit exceeded", map[string]any{
					"consecutive_count": p.consecutiveExplorationCount,
					"limit":             p.explorationLimit,
				})
			}
			return
		}
	} else {
		p.mu.Lock()
		p.consecutiveExplorationCount = 0
		p.mu.Unlock()
	}

	// Prevent duplicate repo_clone calls
	if step.Action == "repo_clone" {
		if workingDir, hasDir := p.secureContext["working_directory"]; hasDir && workingDir != "" {
			step.Status = "failed"
			step.Error = fmt.Sprintf("BLOCKED: Repository already cloned to '%v'. You can now use file_view, rg, and other tools directly. NEVER call repo_clone more than once!", workingDir)
			step.Observation = "Repository already exists in workspace. Use file operations on existing clone."
			if p.logger != nil {
				p.logger.Log(common.EventStepFailure, "Blocked duplicate repo_clone attempt", map[string]any{
					"working_directory": workingDir,
				})
			}
			return
		}
	}

	tool, exists := p.tools[step.Action]
	if !exists {
		// Collect available tool names for helpful error message
		availableTools := make([]string, 0, len(p.tools))
		for toolName := range p.tools {
			availableTools = append(availableTools, toolName)
		}

		// Provide context-specific error message
		var errorMsg string
		if step.Action == "replace" || step.Action == "write" || step.Action == "edit" {
			errorMsg = fmt.Sprintf("Tool '%s' is not available in this agent's toolset.\n\n"+
				"❌ You CANNOT use file modification tools - you are an ANALYSIS agent.\n"+
				"✅ Your role: Analyze issues and create implementation_instructions\n"+
				"✅ CodeFixerAgent will execute your instructions later\n\n"+
				"Available tools for analysis: %v\n\n"+
				"CORRECT approach:\n"+
				"Use submit_analysis with implementation_instructions field containing detailed fix steps.\n"+
				"Example:\n"+
				"Action: submit_analysis\n"+
				"Action Input: {\n"+
				"  \"title\": \"Issue description\",\n"+
				"  \"implementation_instructions\": [\n"+
				"    {\"step\": 1, \"action\": \"replace\", \"file_path\": \"...\", \"line_number\": 123, \"old_string\": \"...\", \"new_string\": \"...\", \"purpose\": \"...\"}\n"+
				"  ]\n"+
				"}",
				step.Action, availableTools)
		} else {
			errorMsg = fmt.Sprintf("Unknown tool '%s'. Available tools: %v", step.Action, availableTools)
		}

		step.Status = "failed"
		step.Error = errorMsg

		if p.logger != nil {
			p.logger.Log(common.EventStepFailure, "Tool validation failed", map[string]any{
				"requested_tool":  step.Action,
				"available_tools": availableTools,
				"is_modify_tool":  step.Action == "replace" || step.Action == "write" || step.Action == "edit",
			})
		}
		return
	}

	// Dedup check: warn on 2nd identical call, block on 3rd+
	hash := p.hashToolCall(step.Action, step.ActionInput)
	p.mu.Lock()
	p.executedCallHashes[hash]++
	cnt := p.executedCallHashes[hash]
	p.mu.Unlock()
	if cnt >= 3 {
		step.Status = "failed"
		step.Error = fmt.Sprintf("BLOCKED: identical %s call executed %d times — try a different approach", step.Action, cnt)
		step.Observation = step.Error
		return
	}

	secureInput := p.injectSecureContext(step.Action, step.ActionInput)

	// For submit_analysis, inject all previous tool outputs
	if step.Action == "submit_analysis" {
		secureInput = p.injectToolOutputs(secureInput)
	}

	// Track tool invocation if tracker is available
	var invocationID string
	if p.toolTracker != nil {
		invocationID = p.toolTracker.StartInvocation(step.Action, secureInput)
		if p.logger != nil {
			p.logger.Log(common.EventToolStart, "Tool execution started", map[string]any{
				"tool_name":     step.Action,
				"invocation_id": invocationID,
			})
		}
	}

	result := tool.Execute(ctx, secureInput)

	// Complete tool invocation tracking
	if p.toolTracker != nil && invocationID != "" {
		var err error
		if result.Status == "error" {
			err = fmt.Errorf("%s", result.Error)
		}
		p.toolTracker.CompleteInvocation(invocationID, result.Data, result.Status, err)
		if p.logger != nil {
			p.logger.Log(common.EventToolComplete, "Tool execution completed", map[string]any{
				"tool_name":     step.Action,
				"invocation_id": invocationID,
				"status":        result.Status,
			})
		}
	}

	// DEBUG: Log submit_analysis tool execution result
	if step.Action == "submit_analysis" {
		if p.logger != nil {
			logFields := map[string]any{
				"result_status":             result.Status,
				"result_data_nil":           result.Data == nil,
				"result_observation_length": len(result.Observation),
				"result_result_length":      len(result.Result),
			}
			if result.Status == "error" {
				// Surface the underlying validation/parse error so operators can
				// diagnose forced-submit failures without re-running the agent.
				logFields["result_error"] = result.Error
				logFields["result_observation"] = result.Observation
			}
			p.logger.Log(common.EventStepComplete, "SUBMIT_ANALYSIS TOOL EXECUTED", logFields)
		}
	}

	if result.Status == "error" {
		// Check if this is a submit_analysis tool with missing required fields
		if step.Action == "submit_analysis" && p.isRetriableSubmitError(result.Error) {
			// Set as retriable failure instead of hard failure
			step.Status = "retriable_failed"
			step.Error = result.Error
			step.Observation = fmt.Sprintf("Tool execution failed with retriable error: %s", result.Error)
			if p.logger != nil {
				p.logger.Log(common.EventStepFailure, "Submit_analysis failed with missing required field - will retry", map[string]any{
					"error":       result.Error,
					"step_number": step.Number,
				})
			}
		} else {
			step.Status = "failed"
			step.Error = result.Error
			step.Observation = fmt.Sprintf("Tool execution failed: %s", result.Error)
		}
	} else {
		step.Status = "completed"
		step.Observation = p.formatObservation(tool, result)

		// Append dedup warning if this was 2nd identical call
		if cnt == 2 {
			step.Observation += "\n\nWARNING: This is the 2nd identical call. A 3rd will be blocked. Try a different approach."
		}

		// REMOVED: Loop counter reset logic
		// The loop detector is working correctly - we should trust it
		// Resetting the counter was allowing true loops to continue indefinitely
		// Let the loop detector enforce the 3-loop limit naturally

		// Update working directory when repo_clone succeeds
		if step.Action == "repo_clone" && result.Data != nil {
			if data, ok := result.Data.(map[string]any); ok {
				if localPath, ok := data["local_path"].(string); ok && localPath != "" {
					p.SetSecureContext("working_directory", localPath)
					if p.logger != nil {
						p.logger.Log(common.EventStepComplete, "Updated working directory from repo_clone", map[string]any{
							"working_directory": localPath,
						})
					}
				}
			}
		}

		// NEW: Store raw tool output in structured memory map
		// Generate a unique-ish ID for this tool call
		toolCallID := fmt.Sprintf("tool_call_%s_step_%d", step.Action, step.Number)
		p.addToolOutput(toolCallID, step.Observation) // Store the simple observation string

		// Debug: Always log when processing submit_analysis action
		if p.logger != nil {
			p.logger.Log(common.EventStepComplete, "Processing step action", map[string]any{
				"action":             step.Action,
				"action_trimmed":     strings.TrimSpace(step.Action),
				"is_submit_analysis": step.Action == "submit_analysis",
			})
		}

		if step.Action == "submit_analysis" {
			if p.logger != nil {
				p.logger.Log(common.EventStepComplete, "MATCHED submit_analysis action", map[string]any{
					"result_data_nil": result.Data == nil,
					"result_status":   result.Status,
				})
			}
			if result.Data != nil {
				// CRITICAL: Inject repository LocalPath as working_directory into the result data
				// This ensures the orchestrator can find the cloned repo path even if tool tracker fails
				if dataMap, ok := result.Data.(map[string]any); ok {
					if p.repositoryContext != nil && p.repositoryContext.LocalPath != "" {
						// Only add if not already present
						if _, exists := dataMap["working_directory"]; !exists {
							dataMap["working_directory"] = p.repositoryContext.LocalPath
							if p.logger != nil {
								p.logger.Log(common.EventStepComplete, "Injected working_directory into submit_analysis data", map[string]any{
									"working_directory": p.repositoryContext.LocalPath,
								})
							}
						}
					}
					p.lastSubmitAnalysisData = dataMap
				} else {
					p.lastSubmitAnalysisData = result.Data
				}

				if p.logger != nil {
					p.logger.Log(common.EventStepComplete, "STORED submit_analysis data", map[string]any{
						"data_size":       len(fmt.Sprintf("%v", p.lastSubmitAnalysisData)),
						"step_number":     step.Number,
						"planner_pointer": fmt.Sprintf("%p", p),
					})
				}

				// Validate data can be marshaled (logging removed to reduce noise)
				if _, err := json.MarshalIndent(p.lastSubmitAnalysisData, "", "  "); err != nil {
					if p.logger != nil {
						p.logger.Log(common.EventStepFailure, "SUBMIT_ANALYSIS data marshal failed", map[string]any{
							"error": err.Error(),
						})
					}
				}
			} else {
				if p.logger != nil {
					p.logger.Log(common.EventStepFailure, "submit_analysis result.Data is nil", map[string]any{
						"result_status":      result.Status,
						"result_result":      result.Result,
						"result_observation": result.Observation,
					})
				}
			}
		}
		p.updateSecureContext(step.Action, result)
	}
}

// formatObservation: INTELLIGENT DATA PRESERVATION - Never truncate critical data
func (p *ReActPlanner) formatObservation(tool core.NBTool, result core.NBToolResponse) string {
	obs := result.Observation
	if obs == "" {
		data, _ := json.Marshal(result.Data)
		obs = fmt.Sprintf("Status: %s\nData: %s", result.Status, string(data))
	}

	// Try tool-specific summarization first
	if s, ok := tool.(core.Summarizer); ok && p.maxObservationLines > 0 {
		maxChars := p.maxObservationLines * 120
		if len(obs) > maxChars {
			obs = s.Summarize(obs, maxChars)
		}
	}

	// Apply line-based truncation
	if p.maxObservationLines > 0 {
		obs = lineBasedTruncate(obs, p.maxObservationLines)
	}

	// Apply proportional truncation as final safety net
	obs = proportionalTruncate(obs, 30000)

	return obs
}

// extractFinalAnswer extracts the final answer from a submit_analysis step
func (p *ReActPlanner) extractFinalAnswer(step *Step) string {
	// DEBUG: Log all extractFinalAnswer attempts for submit_analysis
	if step.Action == "submit_analysis" {
		if p.logger != nil {
			p.logger.Log(common.EventStepComplete, "EXTRACT_FINAL_ANSWER CALLED", map[string]any{
				"step_action":                step.Action,
				"step_number":                step.Number,
				"lastSubmitAnalysisData_nil": p.lastSubmitAnalysisData == nil,
				"step_observation_length":    len(step.Observation),
				"planner_pointer":            fmt.Sprintf("%p", p),
			})
		}
	}

	// For submit_analysis actions, return the stored analysis data
	if step.Action == "submit_analysis" {
		// First, try to get the data from the stored lastSubmitAnalysisData
		if p.lastSubmitAnalysisData != nil {
			if p.logger != nil {
				p.logger.Log(common.EventStepComplete, "USING lastSubmitAnalysisData", map[string]any{
					"data_type": fmt.Sprintf("%T", p.lastSubmitAnalysisData),
				})
			}
			if data, err := json.MarshalIndent(p.lastSubmitAnalysisData, "", "  "); err == nil {
				if p.logger != nil {
					p.logger.Log(common.EventStepComplete, "RETURNING JSON DATA", map[string]any{
						"data_length": len(string(data)),
					})
				}
				return string(data)
			} else {
				if p.logger != nil {
					p.logger.Log(common.EventStepFailure, "JSON MARSHAL FAILED", map[string]any{
						"error": err.Error(),
					})
				}
			}
		} else {
			if p.logger != nil {
				p.logger.Log(common.EventStepFailure, "lastSubmitAnalysisData IS NIL", map[string]any{})
			}
		}

		// Fallback: try to parse the observation as JSON to extract the result
		if step.Observation != "" {
			// Try to parse the observation as JSON to extract the result
			var response map[string]any
			if err := json.Unmarshal([]byte(step.Observation), &response); err == nil {
				if result, ok := response["result"].(map[string]any); ok {
					// Return the structured result as JSON
					if data, err := json.MarshalIndent(result, "", "  "); err == nil {
						return string(data)
					}
				}
			}
			// If parsing fails, return the observation as-is
			return step.Observation
		}

		// Last fallback: return the action input as JSON if observation is empty
		if len(step.ActionInput) > 0 {
			data, _ := json.MarshalIndent(step.ActionInput, "", "  ")
			return string(data)
		}
	}

	// For other actions, return observation or action input
	if step.Observation != "" {
		return step.Observation
	}

	if len(step.ActionInput) > 0 {
		data, _ := json.MarshalIndent(step.ActionInput, "", "  ")
		return string(data)
	}

	return ""
}

// addToolOutput completes your stubbed function
func (p *ReActPlanner) addToolOutput(toolID string, output string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolOutputs[toolID] = output
	// Also add a corresponding message to the parallel tracker
	p.messages = append(p.messages, Message{
		Role:    "tool",
		Content: output, // Content can be the observation
		ToolID:  toolID,
	})
}

// updateConversationMessages adds the AI response (thought + tool call) and tool result
// to the conversation using native function calling message types.
// AI messages contain TextContent (thought) + ToolCall parts.
// Tool results use ToolCallResponse parts (ChatMessageTypeTool).
// No "nudge" messages — the tool result is sufficient for the model to continue.
// updateConversationMessagesMulti builds a single AI message with all ToolCalls
// and a single Tool message with all ToolCallResponses, preserving IDs.
func (p *ReActPlanner) updateConversationMessagesMulti(
	messages []llms.MessageContent,
	steps []Step,
) []llms.MessageContent {
	// Build AI message with thought + all tool calls
	aiParts := []llms.ContentPart{}
	if len(steps) > 0 && steps[0].Thought != "" {
		aiParts = append(aiParts, llms.TextContent{Text: steps[0].Thought})
	}
	for _, s := range steps {
		if s.Action == "" {
			continue
		}
		argsJSON, _ := json.Marshal(s.ActionInput)
		aiParts = append(aiParts, llms.ToolCall{
			ID: s.ToolCallID,
			FunctionCall: &llms.FunctionCall{
				Name:      s.Action,
				Arguments: string(argsJSON),
			},
		})
	}
	if len(aiParts) > 0 {
		messages = append(messages, llms.MessageContent{
			Role:  llms.ChatMessageTypeAI,
			Parts: aiParts,
		})
	}

	// Build Tool message with all responses
	toolParts := []llms.ContentPart{}
	for _, s := range steps {
		if s.Action == "" {
			continue
		}
		// Skip tool response for successful submit_analysis
		if s.Action == "submit_analysis" && s.Status != "retriable_failed" {
			continue
		}
		var toolOutput string
		switch s.Status {
		case "completed":
			toolOutput = s.Observation
		case "failed":
			toolOutput = fmt.Sprintf("Error: %s\n%s", s.Error, p.generateHelpfulGuidance(s))
		case "retriable_failed":
			toolOutput = fmt.Sprintf("Error: %s\n%s", s.Error, p.generateRetryGuidance(s))
		default:
			continue
		}
		if len(toolOutput) > 15000 {
			toolOutput = toolOutput[:15000] + "\n[truncated]"
		}
		toolParts = append(toolParts, llms.ToolCallResponse{
			ToolCallID: s.ToolCallID,
			Name:       s.Action,
			Content:    toolOutput,
		})
	}
	if len(toolParts) > 0 {
		messages = append(messages, llms.MessageContent{
			Role:  llms.ChatMessageTypeTool,
			Parts: toolParts,
		})
	}

	return messages
}

// generateHelpfulGuidance remains unchanged
func (p *ReActPlanner) generateHelpfulGuidance(step Step) string {
	// ... (this function logic is correct and remains unchanged) ...
	if strings.Contains(step.Error, "JSON") {
		return `\nIMPORTANT: You must follow the exact format. Check your JSON syntax.`
	}
	if strings.Contains(step.Error, "Unknown tool") {
		availableTools := make([]string, 0, len(p.tools))
		for name := range p.tools {
			availableTools = append(availableTools, name)
		}
		return fmt.Sprintf(`\nERROR: You used an invalid tool name. Available tools: %v`, availableTools)
	}
	return "\nAn error occurred. Please analyze the error and try again."
}

// compactConversationWindow applies a sliding window to prevent unbounded conversation growth.
// It keeps: system prompt (index 0) + initial user query (index 1) + last 12 messages (~6 tool call rounds).
// Older messages (between index 2 and len-12) are compacted:
//   - AI messages: keep text (thought) + tool call name only (drop arguments JSON)
//   - Tool messages: truncate ToolCallResponse content to 1000 chars
//   - Human nudge messages: drop entirely (these are only used for text-only responses)
func (p *ReActPlanner) compactConversationWindow(messages []llms.MessageContent) []llms.MessageContent {
	if len(messages) <= 16 {
		return messages // Nothing to compact
	}

	// Keep first 2 (system + initial query) and last 12 messages (~6 tool call rounds)
	recentWindowSize := 12
	if recentWindowSize > len(messages)-2 {
		recentWindowSize = len(messages) - 2
	}

	compacted := make([]llms.MessageContent, 0, 2+recentWindowSize+10)

	// Always keep system prompt and initial query
	compacted = append(compacted, messages[0], messages[1])

	// Compact middle messages (index 2 to len-recentWindowSize)
	middleEnd := len(messages) - recentWindowSize

	// Ensure the boundary doesn't split an AI+ToolCall / ToolResponse pair.
	// If the first message in the recent section is a tool response, pull it
	// (and its preceding AI message) into the recent section to keep them adjacent.
	for middleEnd > 2 && middleEnd < len(messages) && messages[middleEnd].Role == llms.ChatMessageTypeTool {
		middleEnd--
	}

	for i := 2; i < middleEnd; i++ {
		msg := messages[i]
		switch msg.Role {
		case llms.ChatMessageTypeAI:
			// Keep text (thought) + tool call name, drop arguments to save space
			compactParts := []llms.ContentPart{}
			for _, part := range msg.Parts {
				switch p := part.(type) {
				case llms.TextContent:
					// Truncate thought text for compacted messages
					text := p.Text
					if len(text) > 200 {
						text = text[:200] + "..."
					}
					if text != "" {
						compactParts = append(compactParts, llms.TextContent{Text: text})
					}
				case llms.ToolCall:
					// Keep tool call name + compact arguments summary
					if p.FunctionCall != nil {
						compactParts = append(compactParts, llms.ToolCall{
							ID: p.ID,
							FunctionCall: &llms.FunctionCall{
								Name:      p.FunctionCall.Name,
								Arguments: compactJSONArgs(p.FunctionCall.Arguments, 200),
							},
						})
					}
				}
			}
			if len(compactParts) > 0 {
				compacted = append(compacted, llms.MessageContent{
					Role:  llms.ChatMessageTypeAI,
					Parts: compactParts,
				})
			}
		case llms.ChatMessageTypeTool:
			// Truncate tool response content to 1000 chars
			compactParts := []llms.ContentPart{}
			for _, part := range msg.Parts {
				switch p := part.(type) {
				case llms.ToolCallResponse:
					content := p.Content
					if len(content) > 1000 {
						content = content[:1000] + "... [truncated by sliding window]"
					}
					compactParts = append(compactParts, llms.ToolCallResponse{
						ToolCallID: p.ToolCallID,
						Name:       p.Name,
						Content:    content,
					})
				case llms.TextContent:
					// Legacy text parts — truncate
					text := p.Text
					if len(text) > 1000 {
						text = text[:1000] + "... [truncated by sliding window]"
					}
					compactParts = append(compactParts, llms.TextContent{Text: text})
				}
			}
			if len(compactParts) > 0 {
				compacted = append(compacted, llms.MessageContent{
					Role:  llms.ChatMessageTypeTool,
					Parts: compactParts,
				})
			}
		case llms.ChatMessageTypeHuman:
			// Drop short nudge messages from compacted section
			if len(msg.Parts) > 0 {
				if textPart, ok := msg.Parts[0].(llms.TextContent); ok {
					if len(textPart.Text) > 100 {
						// Likely a correction prompt, keep it truncated
						truncated := textPart.Text
						if len(truncated) > 300 {
							truncated = truncated[:300] + "..."
						}
						compacted = append(compacted, llms.MessageContent{
							Role:  llms.ChatMessageTypeHuman,
							Parts: []llms.ContentPart{llms.TextContent{Text: truncated}},
						})
					}
				}
			}
		default:
			compacted = append(compacted, msg)
		}
	}

	// Add separator to indicate compaction happened, but only if the last
	// compacted message isn't an AI message with a pending tool call (which
	// would break the function_call → function_response adjacency for Gemini).
	lastIsToolCall := false
	if len(compacted) > 0 {
		lastMsg := compacted[len(compacted)-1]
		if lastMsg.Role == llms.ChatMessageTypeAI {
			for _, part := range lastMsg.Parts {
				if _, ok := part.(llms.ToolCall); ok {
					lastIsToolCall = true
					break
				}
			}
		}
	}
	if !lastIsToolCall {
		compacted = append(compacted, llms.MessageContent{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "[Earlier conversation steps were summarized to save context. Recent steps follow in full detail.]"}},
		})
	}

	// Append recent messages unchanged
	compacted = append(compacted, messages[middleEnd:]...)

	if p.logger != nil {
		p.logger.Log(common.EventPlanningProgress, "Compacted conversation window", map[string]any{
			"original_messages":  len(messages),
			"compacted_messages": len(compacted),
		})
	}

	return compacted
}

// injectSecureContext remains unchanged
func (p *ReActPlanner) injectSecureContext(action string, input map[string]any) map[string]any {
	// ... (this function logic is correct and remains unchanged) ...
	secureInput := make(map[string]any, len(input))
	for k, v := range input {
		secureInput[k] = v
	}
	switch action {
	case "cli":
		// Inject working directory for CLI commands
		if workingDir, ok := p.secureContext["working_directory"]; ok {
			secureInput["working_directory"] = workingDir
		}
		if repoPath, ok := p.secureContext["repository_path"]; ok {
			if secureInput["working_dir"] == nil {
				secureInput["working_dir"] = repoPath
			}
		}
		if credentials, ok := p.secureContext["credentials"]; ok {
			if credsInterface, ok := credentials.(map[string]any); ok {
				if token, ok := credsInterface["value"].(string); ok && token != "" {
					secureInput["github_token"] = token
				}
			} else {
				// Handle struct types (e.g., *models.Credentials) by marshaling to map
				credData, err := json.Marshal(credentials)
				if err != nil {
					if p.logger != nil {
						p.logger.Log(common.EventPlanningProgress, "Failed to marshal credentials for cli token injection", map[string]any{"error": err.Error()})
					}
				} else {
					var credMap map[string]any
					if err := json.Unmarshal(credData, &credMap); err != nil {
						if p.logger != nil {
							p.logger.Log(common.EventPlanningProgress, "Failed to unmarshal credentials for cli token injection", map[string]any{"error": err.Error()})
						}
					} else {
						if token, ok := credMap["value"].(string); ok && token != "" {
							secureInput["github_token"] = token
						} else if token, ok := credMap["token"].(string); ok && token != "" {
							secureInput["github_token"] = token
						}
					}
				}
			}
		}
	case "semantic_analysis":
		if repoPath, ok := p.secureContext["repository_path"]; ok {
			secureInput["workspace_dir"] = repoPath
		}
	case "file_find", "file_view", "grep", "rg", "ripgrep", "replace", "git", "gh":
		// Inject working directory for all file/repository tools
		if workingDir, ok := p.secureContext["working_directory"]; ok {
			secureInput["working_directory"] = workingDir
		}
	case "repo_clone":
		// Inject credentials for repository cloning
		if credentials, ok := p.secureContext["credentials"]; ok {
			// Convert credentials.ResolvedCredentials to models.Credentials format
			// ResolvedCredentials has "Token" field, but models.Credentials expects "Value" field
			if credMap, ok := credentials.(map[string]any); ok {
				// Already in map format (from LLM), pass as-is
				secureInput["credentials"] = credMap
			} else {
				// Convert from struct format to map with correct field names
				credData, err := json.Marshal(credentials)
				if err == nil {
					var resolvedCred map[string]any
					if err := json.Unmarshal(credData, &resolvedCred); err == nil {
						// Convert "token" field to "value" field for models.Credentials compatibility
						if token, hasToken := resolvedCred["token"]; hasToken && token != "" {
							resolvedCred["value"] = token
							delete(resolvedCred, "token") // Remove the "token" field
						}
						secureInput["credentials"] = resolvedCred
					}
				}
			}
		}
	}
	return secureInput
}

// injectToolOutputs remains unchanged (it correctly uses the user's p.toolOutputs map)
func (p *ReActPlanner) injectToolOutputs(input map[string]any) map[string]any {
	// ... (this function logic is correct and remains unchanged) ...
	result := make(map[string]any)
	for k, v := range input {
		result[k] = v
	}
	result["_tool_outputs"] = p.toolOutputs
	return result
}

// updateSecureContext remains unchanged
func (p *ReActPlanner) updateSecureContext(action string, result core.NBToolResponse) {
	// No secure context updates needed
}

// SetRepositoryContext sets the repository context for enhanced troubleshooting
func (p *ReActPlanner) SetRepositoryContext(ctx *RepositoryContext) {
	p.repositoryContext = ctx

	// Set the local path in secure context so tools can access the correct repository directory
	if ctx != nil && ctx.LocalPath != "" {
		p.SetSecureContext("repository_path", ctx.LocalPath)
	}
}

// SetTools updates the tools map without losing existing context
func (p *ReActPlanner) SetTools(tools []core.NBTool) {
	p.tools = make(map[string]core.NBTool)
	for _, tool := range tools {
		p.tools[tool.Name()] = tool
	}
}

// truncate remains unchanged
func (p *ReActPlanner) truncate(s string, maxLen int) string {
	// ... (this function logic is correct and remains unchanged) ...
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// hasCalledSubmitAnalysis remains unchanged
func (p *ReActPlanner) hasCalledSubmitAnalysis() bool {
	// ... (this function logic is correct and remains unchanged) ...
	for _, step := range p.currentSteps {
		if step.Action == "submit_analysis" {
			return true
		}
	}
	return false
}

// createForcedSubmitAnalysisStep remains unchanged (using our previous regex fix)
func (p *ReActPlanner) createForcedSubmitAnalysisStep(ctx context.Context, stepNumber int, query, systemPrompt string) Step {
	// ... (this function logic is correct and remains unchanged) ...
	var filePath, errorMsg, origCode, fixedCode, commitHash, author, commitDate string
	var lineNumber int
	var prList []any

	for _, step := range p.currentSteps {
		// Extract file_path from tool inputs (more reliable than regex on observations)
		if filePath == "" {
			switch step.Action {
			case "file_view", "rg", "replace":
				if fp, ok := step.ActionInput["file_path"].(string); ok && fp != "" {
					filePath = fp
				}
			case "file_find":
				// file_find returns matched files in observations; skip for filePath extraction
			}
		}

		if step.Status == "completed" && step.Observation != "" {
			obs := step.Observation
			if errorMsg == "" && (strings.Contains(strings.ToLower(obs), "error") || strings.Contains(strings.ToLower(obs), "exception")) {
				lines := strings.Split(obs, "\n")
				for _, line := range lines {
					if strings.Contains(strings.ToLower(line), "error") || strings.Contains(strings.ToLower(line), "exception") {
						if len(line) < 200 {
							errorMsg = strings.TrimSpace(line)
							break
						}
					}
				}
			}
			if commitHash == "" {
				if matches := blameRegex.FindStringSubmatch(obs); len(matches) >= 4 {
					commitHash = matches[1]
					author = strings.TrimSpace(matches[2])
					commitDate = matches[3]
				}
			}
			if len(prList) == 0 && strings.Contains(obs, "\"number\"") && strings.Contains(obs, "\"title\"") {
				jsonStr := p.extractJSONFromContent(p.logger, obs)
				if jsonStr != "" {
					var parsedPRs []any
					if err := json.Unmarshal([]byte(jsonStr), &parsedPRs); err == nil {
						prList = parsedPRs
					}
				}
			}
		}
	}
	// Use LLM to intelligently summarize the investigation findings
	analysisResult, err := p.generateLLMSummary(ctx, p.currentSteps, query, systemPrompt)
	if err != nil {
		// Fallback to basic summary if LLM fails
		title := "Code Analysis - Investigation Summary (Partial)"
		description := "The investigation reached the maximum iteration limit, and the LLM failed to generate a structured summary. "
		if filePath != "" {
			description += fmt.Sprintf("The investigation focused on %s. ", filePath)
		}
		if errorMsg != "" {
			description += fmt.Sprintf("The identified error was: %s. ", errorMsg)
		}
		description += "Please review the investigation steps in the logs for more details."

		analysisResult = map[string]any{
			"title":            title,
			"description":      description,
			"requires_fix":     false, // False when forced submit - agent couldn't find root cause to provide implementation_instructions
			"confidence_score": "Low",
		}
	}

	// Ensure title and description are never empty to prevent submit_analysis validation failures
	titleStr, _ := analysisResult["title"].(string)
	descriptionStr, _ := analysisResult["description"].(string)
	if titleStr == "" {
		analysisResult["title"] = "Code Analysis - Unable to Complete Investigation"
	}
	if descriptionStr == "" {
		analysisResult["description"] = fmt.Sprintf("Investigation reached max iterations without resolution. Query: %s", query)
	}

	return Step{
		Number:  stepNumber,
		Thought: "I need to submit analysis with the information gathered so far as I'm approaching the maximum iteration limit",
		Action:  "submit_analysis",
		ActionInput: map[string]any{
			"title":         analysisResult["title"],
			"description":   analysisResult["description"],
			"file_path":     filePath,
			"line_number":   lineNumber,
			"error_message": errorMsg,
			"original_code": origCode,
			"fixed_code":    fixedCode,
			"git_diff":      "",
			"commits": []map[string]any{{
				"hash": commitHash, "author": author, "date": commitDate, "message": "", "changes": "",
			}},
			"pr_list":                     prList,
			"requires_fix":                analysisResult["requires_fix"],
			"confidence_score":            analysisResult["confidence_score"],
			"root_cause_analysis":         analysisResult["root_cause_analysis"],
			"implementation_instructions": analysisResult["implementation_instructions"],
		},
		Status: "parsing",
	}
}

// Interface methods remain unchanged
func (p *ReActPlanner) GetTools() []core.NBTool {
	// ... (this function logic is correct and remains unchanged) ...
	tools := make([]core.NBTool, 0, len(p.tools))
	for _, tool := range p.tools {
		tools = append(tools, tool)
	}
	return tools
}
func (p *ReActPlanner) SetSecureContext(key string, value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.secureContext[key] = value
}

func (p *ReActPlanner) GetSecureContext(key string) any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.secureContext[key]
}

// createTempWorkspace remains unchanged
func (p *ReActPlanner) createTempWorkspace() error {
	// ... (this function logic is correct and remains unchanged) ...
	timestamp := time.Now().Format("20060102-150405-000")
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("code-analysis-%s", timestamp))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp workspace: %w", err)
	}
	p.tempWorkspace = tempDir
	return nil
}

// cleanupTempWorkspace remains unchanged
func (p *ReActPlanner) cleanupTempWorkspace() {
	// ... (this function logic is correct and remains unchanged) ...
	if p.tempWorkspace == "" {
		return
	}
	if err := os.RemoveAll(p.tempWorkspace); err != nil {
		slog.Warn("Failed to cleanup temporary workspace", "path", p.tempWorkspace, "error", err)
	} else {
		slog.Info("Cleaned up temporary workspace", "path", p.tempWorkspace)
	}
	p.tempWorkspace = ""
}

// countConsecutiveFailures counts only hard failures, not retriable ones
func (p *ReActPlanner) countConsecutiveFailures() int {
	if len(p.currentSteps) == 0 {
		return 0
	}
	count := 0
	for i := len(p.currentSteps) - 1; i >= 0; i-- {
		// Only count hard failures, not retriable ones
		if p.currentSteps[i].Status == "failed" {
			count++
		} else if p.currentSteps[i].Status == "retriable_failed" {
			// Retriable failures don't count towards consecutive failure limit
			// but we stop counting here since this breaks the "consecutive" chain
			break
		} else {
			break
		}
	}
	return count
}

// generateLLMSummary uses LLM to intelligently analyze investigation findings
func (p *ReActPlanner) generateLLMSummary(ctx context.Context, steps []Step, originalQuery, systemPrompt string) (map[string]any, error) {
	// Build concise summary — keep under ~8K chars to avoid LLM output truncation
	var investigationSummary strings.Builder
	investigationSummary.WriteString("## Investigation Summary\n")
	fmt.Fprintf(&investigationSummary, "Original Query: %s\n\n", originalQuery)

	investigationSummary.WriteString("## Investigation Steps:\n")
	for _, step := range steps {
		if step.Thought != "" {
			thought := step.Thought
			if len(thought) > 300 {
				thought = thought[:300] + "..."
			}
			fmt.Fprintf(&investigationSummary, "Step %d Thought: %s\n", step.Number, thought)
		}
		if step.Status == "completed" && step.Observation != "" {
			obs := step.Observation
			if len(obs) > 500 {
				obs = obs[:500] + "..."
			}
			fmt.Fprintf(&investigationSummary, "Step %d (%s): %s\n", step.Number, step.Action, obs)
		} else if step.Status == "failed" && step.Error != "" {
			errMsg := step.Error
			if len(errMsg) > 300 {
				errMsg = errMsg[:300] + "..."
			}
			fmt.Fprintf(&investigationSummary, "Step %d (%s) FAILED: %s\n", step.Number, step.Action, errMsg)
		}
	}

	prompt := fmt.Sprintf(`Based on the investigation findings below, create a structured JSON analysis summary.

%s

Respond with ONLY this JSON (no markdown, no explanation):
{
  "title": "Clear, specific title describing what was found",
  "description": "Detailed explanation of the issue, root cause, and recommended solution",
  "requires_fix": true or false,
  "confidence_score": "High/Medium/Low",
  "root_cause_analysis": "Brief explanation of the underlying cause",
  "implementation_instructions": [
    {"step": 1, "file_path": "<path>", "action": "replace", "purpose": "<what needs to change>"}
  ]
}

Set requires_fix=true only if you can identify a specific file_path and purpose for the fix.
Set implementation_instructions to [] if no fix can be determined.
Keep the response concise.`, investigationSummary.String())

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextPart(systemPrompt)}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart(prompt)}},
	}

	response, err := p.llmClient.GenerateContent(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to generate LLM summary: %w", err)
	}

	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	// Extract JSON from response
	responseText := response.Choices[0].Content
	jsonStr := p.extractJSONFromContent(p.logger, responseText)
	if jsonStr == "" {
		// Try repairing truncated JSON by closing open braces/brackets
		jsonStr = p.repairTruncatedJSON(responseText)
		if jsonStr == "" {
			return nil, fmt.Errorf("no valid JSON found in LLM response")
		}
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM summary JSON: %w", err)
	}

	return result, nil
}

// GetSubmitAnalysisData returns the structured data from the last submit_analysis call
func (p *ReActPlanner) GetSubmitAnalysisData() any {
	return p.lastSubmitAnalysisData
}

// salvageForcedSubmitInput preserves the planner-constructed input of a forced
// submit_analysis step as structured data when the tool itself errored. This runs
// only on the forced-submit path where there are no retries left; the constructed
// input already has guarded title/description and is the best-effort answer we can
// hand to the orchestrator. No-op if structured data was already captured by a
// previous successful submit_analysis call in the same run.
func (p *ReActPlanner) salvageForcedSubmitInput(step *Step) {
	if p.lastSubmitAnalysisData != nil || step == nil || step.ActionInput == nil {
		return
	}
	salvage := make(map[string]any, len(step.ActionInput))
	for k, v := range step.ActionInput {
		if k == "_tool_outputs" {
			continue // injected at execute time, not part of the analysis payload
		}
		salvage[k] = v
	}
	p.lastSubmitAnalysisData = salvage
	if p.logger != nil {
		p.logger.Log(common.EventStepComplete, "Salvaged forced submit_analysis input as structured data", map[string]any{
			"step_number": step.Number,
			"tool_error":  step.Error,
		})
	}
}

// isRetriableSubmitError checks if a submit_analysis error is retriable (missing required fields)
func (p *ReActPlanner) isRetriableSubmitError(errorMsg string) bool {
	retriablePatterns := []string{
		"title is required",
		"description is required",
		"Missing analysis title",
		"Missing analysis description",
		"Invalid input parameters",
	}

	errorLower := strings.ToLower(errorMsg)
	for _, pattern := range retriablePatterns {
		if strings.Contains(errorLower, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// generateRetryGuidance provides specific guidance for fixing submit_analysis errors
func (p *ReActPlanner) generateRetryGuidance(step Step) string {
	errorLower := strings.ToLower(step.Error)

	if strings.Contains(errorLower, "title is required") || strings.Contains(errorLower, "missing analysis title") {
		return `
🔄 RETRY INSTRUCTION: The submit_analysis action failed because the "title" field is missing.

Please retry the submit_analysis action with the SAME action_input you just provided, but add a "title" field.

Example format:
Action: submit_analysis
Action Input: {
  "title": "Brief descriptive title of the issue found",
  "description": "Your existing description...",
  ... (all your other fields remain the same)
}

Your analysis was good - just add the missing title field and resubmit.`
	}

	if strings.Contains(errorLower, "description is required") || strings.Contains(errorLower, "missing analysis description") {
		return `
🔄 RETRY INSTRUCTION: The submit_analysis action failed because the "description" field is missing.

Please retry the submit_analysis action with a "description" field that explains your analysis findings.

Example format:
Action: submit_analysis
Action Input: {
  "title": "Your title...",
  "description": "Detailed explanation of the issue, root cause, and recommended fix",
  ... (all your other fields)
}`
	}

	if strings.Contains(errorLower, "invalid input parameters") {
		return `
🔄 RETRY INSTRUCTION: The submit_analysis action failed due to invalid input parameters.

Please ensure your action input includes the required fields:
- "title": Brief descriptive title 
- "description": Detailed analysis explanation

And verify all field names match the expected format exactly.`
	}

	return `
🔄 RETRY INSTRUCTION: The submit_analysis action failed but can be retried.

Please check the error message and ensure all required fields are included in your action input.
Required fields: "title" and "description"`
}

// buildToolProgressText generates contextual progress text based on the tool being executed.
func (p *ReActPlanner) buildToolProgressText(action string, actionInput map[string]any) string {
	switch action {
	case "repo_clone":
		if repoURL, ok := actionInput["repo_url"].(string); ok {
			// Extract org/repo from URL
			parts := strings.Split(strings.TrimSuffix(repoURL, ".git"), "/")
			if len(parts) >= 2 {
				return fmt.Sprintf("Cloning repository %s/%s...", parts[len(parts)-2], parts[len(parts)-1])
			}
			return fmt.Sprintf("Cloning repository %s...", repoURL)
		}
		return "Cloning repository..."
	case "file_find":
		if pattern, ok := actionInput["pattern"].(string); ok {
			return fmt.Sprintf("Searching for files matching '%s'...", p.truncate(pattern, 60))
		}
		if query, ok := actionInput["query"].(string); ok {
			return fmt.Sprintf("Searching for files: %s", p.truncate(query, 60))
		}
		return "Searching for relevant files..."
	case "file_view", "file_read":
		if filePath, ok := actionInput["file_path"].(string); ok {
			return fmt.Sprintf("Reading %s...", p.truncate(filePath, 80))
		}
		return "Reading source file..."
	case "grep", "rg", "ripgrep":
		if pattern, ok := actionInput["pattern"].(string); ok {
			return fmt.Sprintf("Searching code for '%s'...", p.truncate(pattern, 60))
		}
		return "Searching codebase..."
	case "git":
		if subcommand, ok := actionInput["subcommand"].(string); ok {
			switch subcommand {
			case "blame":
				if filePath, ok := actionInput["file_path"].(string); ok {
					return fmt.Sprintf("Checking git blame for %s...", p.truncate(filePath, 60))
				}
				return "Checking git blame..."
			case "log":
				return "Reviewing git history..."
			case "diff":
				return "Reviewing code changes..."
			}
			return fmt.Sprintf("Running git %s...", subcommand)
		}
		return "Running git command..."
	case "gh":
		if subcommand, ok := actionInput["subcommand"].(string); ok {
			return fmt.Sprintf("Running gh %s...", p.truncate(subcommand, 40))
		}
		return "Querying GitHub..."
	case "replace":
		if filePath, ok := actionInput["file_path"].(string); ok {
			return fmt.Sprintf("Editing %s...", p.truncate(filePath, 80))
		}
		return "Applying code changes..."
	case "cli":
		if cmd, ok := actionInput["command"].(string); ok {
			return fmt.Sprintf("Running: %s", p.truncate(cmd, 60))
		}
		return "Running CLI command..."
	case "semantic_analysis":
		return "Running semantic code analysis..."
	case "submit_analysis":
		return "Submitting analysis results..."
	case "shell":
		if cmd, ok := actionInput["command"].(string); ok {
			return fmt.Sprintf("Running: %s", p.truncate(cmd, 60))
		}
		return "Running command..."
	default:
		return fmt.Sprintf("Running %s...", action)
	}
}
