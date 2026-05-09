package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

var uuidRegex = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// outputRefRegex matches {{ Tasks['task-id'].output.fieldname }} patterns for schema-driven validation.
var outputRefRegex = regexp.MustCompile(`Tasks\[['"]([^'"]+)['"]\]\.output\.(\w+)`)

const WorkflowBuilderSummarizerToolName = "automation_builder_summarizer"

const (
	PlanApprovalOptionApprove = "Approve and Build"
	PlanApprovalOptionChanges = "Request Changes"
	maxPlanAttempts           = 3

	FixApprovalOptionApply   = "Apply Changes"
	FixApprovalOptionModify  = "Request Modifications"
	FixApprovalOptionDiscard = "Discard"

	// fixNeedsMoreInfoMarker is a sentinel the diagnosis LLM emits at the start of
	// <final_answer> when it cannot find actionable evidence and needs to ask the user.
	// We then surface the rest of the content as a free-text followup question instead
	// of presenting the Apply / Modify / Discard buttons.
	fixNeedsMoreInfoMarker = "[NEEDS_MORE_INFO]"
)

// ClarifyingQuestion represents a single question to ask the user before planning.
type ClarifyingQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

type WorkflowBuilderState struct {
	Stage         string `json:"stage"`
	OriginalQuery string `json:"original_query"`
	Intent        string `json:"intent"`
	Plan          string `json:"plan"`
	Feedback      string `json:"feedback"`
	PlanAttempts  int    `json:"plan_attempts"`
	// Fix mode fields
	Mode               string `json:"mode"`                // "create" or "fix"
	WorkflowId         string `json:"workflow_id"`         // ID of the existing workflow being fixed
	ExecutionId        string `json:"execution_id"`        // Optional: specific failed execution being debugged
	ExistingDefinition string `json:"existing_definition"` // Current workflow JSON
	ExecutionError     string `json:"execution_error"`     // Error context from failed execution
	ProposedChanges    string `json:"proposed_changes"`    // Structured changes JSON from LLM
	ProposedDiff       string `json:"proposed_diff"`       // Human-readable before/after diff
	// Agentic workflow manipulation
	WorkingWorkflow map[string]interface{} `json:"working_workflow,omitempty"` // In-memory workflow being built/modified
	// Clarification stage fields
	ClarifyingQuestions []ClarifyingQuestion `json:"clarifying_questions,omitempty"`
	ClarifyingAnswers   []string             `json:"clarifying_answers,omitempty"`
	ClarifyingIndex     int                  `json:"clarifying_index"`
}

func init() {
	core.RegisterNBAgentFactoryAndTool(WorkflowBuilderAgentName, func(accountId string) (core.NBAgent, error) {
		return newWorkflowBuilderAgent(accountId), nil
	}, "Builds automations for the Automation Server", "Provide a description of the automation you want to build.", "Returns the automation definition in JSON format.")

	// Register the custom workflow summarizer tool
	core.RegisterNBAgentFactoryAndTool(WorkflowBuilderSummarizerToolName, func(accountId string) (core.NBAgent, error) {
		return WorkflowBuilderSummarizerAgent{}, nil
	}, "Extracts the final successful automation JSON from AutomationBuilder's response", "AutomationBuilder full response with all attempts", "Clean JSON automation only")
}

const WorkflowBuilderAgentName = "automation_builder"

type WorkflowBuilderAgent struct {
	accountId       string
	state           WorkflowBuilderState
	cachedTaskTypes string // Cached task type registry JSON for schema-driven validation
}

func newWorkflowBuilderAgent(accountId string) *WorkflowBuilderAgent {
	return &WorkflowBuilderAgent{accountId: accountId}
}

func (a *WorkflowBuilderAgent) MarshalState() ([]byte, error) {
	return json.Marshal(a.state)
}

func (a *WorkflowBuilderAgent) UnmarshalState(data []byte) error {
	return json.Unmarshal(data, &a.state)
}

func (a *WorkflowBuilderAgent) GetName() string {
	return WorkflowBuilderAgentName
}

func (a *WorkflowBuilderAgent) GetNameAliases() []string {
	return []string{"AutomationBuilder", "WorkflowBuilder", "workflow_builder"}
}

func (a *WorkflowBuilderAgent) GetDescription() string {
	return "Specialized agent for building automation definitions. Uses a plan-then-build process: intent extraction, plan generation with user approval, automation building, and validation loop."
}

func (a *WorkflowBuilderAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{} // Not used - Custom planner uses Execute method
}

func (a *WorkflowBuilderAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{} // Not used - Custom planner handles tools directly
}

func (a *WorkflowBuilderAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (a *WorkflowBuilderAgent) PostProcessResponse(ctx *security.RequestContext, request core.NBAgentRequest, resp core.NBAgentResponse) core.NBAgentResponse {
	if len(resp.Response) > 0 {
		content := resp.Response[0]
		// Clean up markdown code blocks only if the response contains JSON (workflow definition)
		// Text responses (investigation, debugging) are passed through as-is
		if strings.Contains(content, "```") && strings.Contains(content, "{") {
			lines := strings.Split(content, "\n")
			var jsonLines []string
			inCodeBlock := false
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "```") {
					inCodeBlock = !inCodeBlock
					continue
				}
				if inCodeBlock || (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) {
					jsonLines = append(jsonLines, line)
				}
			}
			if len(jsonLines) > 0 {
				content = strings.Join(jsonLines, "\n")
			} else {
				// Fallback: try to just strip the markers if logic above failed or simple wrapping
				content = strings.TrimPrefix(content, "```json")
				content = strings.TrimPrefix(content, "```")
				content = strings.TrimSuffix(content, "```")
			}
			resp.Response = []string{content}
		}
	}
	return resp
}

// getWorkflowSchema returns the exact workflow schema from runbook-server
func getWorkflowSchema() string {
	return `# AUTOMATION SCHEMA (from runbook-server/internal/model/workflow.go)

## TOP LEVEL STRUCTURE:
{
  "name": "string (required)",
  "definition": { ... },  // AutomationDefinition (required)
  "tags": {},             // map[string]any (optional)
  "status": "ACTIVE"      // AutomationStatus (optional): "ACTIVE", "INACTIVE", "PAUSED", "DRAFT"
}

## DEFINITION STRUCTURE (inside "definition"):
{
  "version": "v1",                    // string (optional, usually "v1")
  "inputs": [...],                    // []Input (optional)
  "triggers": [...],                  // []Trigger (required, min=1)
  "tasks": [...],                     // []Task (required, min=1)
  "hooks": {...},                     // *Hooks (optional)
  "output": {},                       // map[string]any (optional)
  "set_execution_tags": [],           // []string (optional)
  "retry_policy": {...},              // *AutomationRetryPolicy (optional)
  "timeout": "30m"                    // string duration (optional, e.g., "30m", "1h")
}

## TASK STRUCTURE:
{
  "id": "string (required)",          // Validated as taskid
  "type": "string (required)",        // Task type from available types
  "params": {},                       // map[string]any (optional)
  "tasks": [],                        // []Task (optional, for nested tasks)
  "set_vars": {},                     // map[string]any (optional)
  "set_state": {},                    // map[string]any (optional)
  "depends_on": [],                   // []string (optional)
  "if": "string (optional)",          // Jinja2 condition
  "matrix": {},                       // map[string]any (optional)
  "failure_policy": {...},            // *FailurePolicy (optional)
  "timeout": "5m",                    // string duration (optional)
  "hooks": {...}                      // *Hooks (optional)
}

## TRIGGER TYPES:
- "manual" - No params required (params must be empty or omitted)
- "schedule" - Requires params: {"cron": "0 * * * *", "overlap_policy": "Skip|BufferOne|BufferAll|CancelOther|TerminateOther|AllowAll"}
- "webhook" - Requires params: {"integration_name": "string", "filter": "jinja2 (optional)", "secret": "string (optional)"}
- "event" - Requires params: {"event_type": "string", "filter": "jinja2 (optional)"}

## INPUT STRUCTURE:
{
  "id": "string (required)",
  "description": "string (optional)",
  "type": "string (optional)",        // e.g., "string", "json", "number", "boolean"
  "default": any (optional),
  "required": bool (optional)
}

## FAILURE POLICY:
{
  "retry": {
    "initial_interval": "1s",
    "backoff_coefficient": 2.0,
    "maximum_interval": "1m",
    "maximum_attempts": 3,
    "non_retryable_error_types": []
  },
  "action": "continue|fail"            // "continue" or "fail" (default)
}

## HOOKS:
{
  "success": [{"type": "string", "params": {}}],
  "failure": [{"type": "string", "params": {}}],
  "always": [{"type": "string", "params": {}}]
}

## TASK STATUS VALUES (for {{ Tasks['id'].status }}):
- COMPLETED
- FAILED
- SKIPPED
- STARTED
- SCHEDULED
- TIMED_OUT
- CANCELED

## VALIDATION RULES:
1. "name" is required at top level
2. "definition" is required at top level
3. "definition.triggers" is required and must have at least 1 trigger
4. "definition.tasks" is required and must have at least 1 task
5. Each task must have "id" and "type"
6. "depends_on" task IDs must exist in automation
7. Jinja2 templates in "if", "params", "set_state", "output" are validated
8. Duration fields ("timeout") must be valid durations (e.g., "30s", "5m", "1h")
9. Manual trigger must NOT have params (or empty params)
10. Schedule trigger MUST have "cron" param
11. Webhook trigger MUST have "integration_name" param
12. Event trigger MUST have "event_type" param
`
}

func (a *WorkflowBuilderAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	switch a.state.Stage {
	case "clarification":
		return a.handleClarificationResponse(ctx, request)
	case "plan_approval":
		return a.handlePlanApproval(ctx, request)
	case "feedback":
		return a.handleFeedback(ctx, request)
	case "fix_approval":
		return a.handleFixApproval(ctx, request)
	case "fix_feedback":
		return a.handleFixFeedback(ctx, request)
	default:
		return a.handleEntry(ctx, request)
	}
}

// handleEntry detects whether this is a create or fix request and routes accordingly.
// Workflow ID can come from QueryConfig["workflow_id"] (direct API call) or be extracted
// from the query text (when WorkflowAgent delegates via agent-as-tool).
func (a *WorkflowBuilderAgent) handleEntry(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	// Check QueryConfig first (direct invocation with explicit workflow_id)
	if request.QueryConfig.WorkflowId != "" {
		return a.handleFixEntry(ctx, request, request.QueryConfig.WorkflowId)
	}

	// Fallback: extract workflow ID from query text (agent-as-tool delegation from WorkflowAgent)
	if workflowId := extractWorkflowIdFromQuery(request.Query); workflowId != "" {
		ctx.GetLogger().Info("workflow_builder: detected workflow ID in query text", "workflow_id", workflowId)
		return a.handleFixEntry(ctx, request, workflowId)
	}

	return a.handleIntentAndPlan(ctx, request)
}

// extractWorkflowIdFromQuery looks for a UUID in the query text that likely represents a workflow ID.
// The WorkflowAgent is instructed to include the workflow ID when delegating fix requests.
func extractWorkflowIdFromQuery(query string) string {
	lowerQuery := strings.ToLower(query)
	// Only extract if the query looks like a fix/debug request (not a new workflow creation)
	fixKeywords := []string{"fix", "debug", "error", "failed", "failing", "broken", "issue", "wrong", "update", "modify", "change", "repair"}
	isFix := false
	for _, kw := range fixKeywords {
		if strings.Contains(lowerQuery, kw) {
			isFix = true
			break
		}
	}
	if !isFix {
		return ""
	}

	match := uuidRegex.FindString(query)
	return match
}

// handleIntentAndPlan extracts intent, optionally asks clarifying questions, generates a plan, and asks for user approval.
func (a *WorkflowBuilderAgent) handleIntentAndPlan(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	intent, err := a.extractIntent(ctx, request)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("intent extraction failed: %w", err)
	}

	// Generate clarifying questions based on intent + environment context
	questions, err := a.generateClarifyingQuestions(ctx, request, intent)
	if err != nil {
		// Non-fatal: skip clarification and proceed to plan
		ctx.GetLogger().Warn("workflow_builder: clarification generation failed, skipping", "error", err)
		questions = nil
	}

	if len(questions) > 0 {
		// Save state and ask first question
		a.state = WorkflowBuilderState{
			Stage:               "clarification",
			OriginalQuery:       request.Query,
			Intent:              intent,
			ClarifyingQuestions: questions,
			ClarifyingAnswers:   make([]string, len(questions)),
			ClarifyingIndex:     0,
		}
		return a.buildClarificationResponse(request, questions[0])
	}

	// No clarification needed — proceed directly to plan generation
	return a.generatePlanAndAskApproval(ctx, request, intent, "")
}

// generatePlanAndAskApproval generates a plan and returns a WAITING response for user approval.
func (a *WorkflowBuilderAgent) generatePlanAndAskApproval(ctx *security.RequestContext, request core.NBAgentRequest, intent string, clarificationContext string) (core.NBAgentResponse, error) {
	plan, err := a.generatePlan(ctx, request, intent, clarificationContext)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("plan generation failed: %w", err)
	}

	// Save state for resume after user responds
	a.state.Stage = "plan_approval"
	a.state.OriginalQuery = request.Query
	a.state.Intent = intent
	a.state.Plan = plan
	a.state.PlanAttempts = 1

	agentId := uuid.Nil
	if request.AgentId != "" {
		agentId = uuid.MustParse(request.AgentId)
	}

	planQuestion := fmt.Sprintf("Here's my plan for building your automation:\n\n%s\n\nWould you like to approve this plan or request changes?", plan)
	resp := core.NBAgentResponse{
		Status: core.ConversationStatusWaiting,
		FollowupRequest: core.FollowupRequest{
			Question:        planQuestion,
			FollowupType:    core.FollowupTypeSingleSelect,
			FollowupOptions: []string{PlanApprovalOptionApprove, PlanApprovalOptionChanges},
			AgentName:       a.GetName(),
			AgentId:         agentId,
		},
	}
	return resp, nil
}

// handlePlanApproval processes the user's response to the plan approval prompt.
func (a *WorkflowBuilderAgent) handlePlanApproval(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	userChoice := strings.TrimSpace(request.Query)

	if strings.EqualFold(userChoice, PlanApprovalOptionApprove) {
		// Rebuild the request with original query for LLM calls
		originalRequest := request
		originalRequest.Query = a.state.OriginalQuery
		return a.buildAndValidate(ctx, originalRequest, a.state.Intent, a.state.Plan)
	}

	if strings.EqualFold(userChoice, PlanApprovalOptionChanges) || userChoice == "" {
		// User selected "Request Changes" without providing details — ask for specifics
		a.state.Stage = "feedback"

		agentId := uuid.Nil
		if request.AgentId != "" {
			agentId = uuid.MustParse(request.AgentId)
		}

		resp := core.NBAgentResponse{
			Status: core.ConversationStatusWaiting,
			FollowupRequest: core.FollowupRequest{
				Question:     "What changes would you like me to make to the plan?",
				FollowupType: core.FollowupTypeText,
				AgentName:    a.GetName(),
				AgentId:      agentId,
			},
		}
		return resp, nil
	}

	// Free-text feedback — user typed changes directly, use them immediately
	a.state.Stage = "feedback"
	return a.handleFeedback(ctx, request)
}

// handleFeedback incorporates user feedback, regenerates the plan, and asks for approval again.
func (a *WorkflowBuilderAgent) handleFeedback(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	feedback := strings.TrimSpace(request.Query)
	a.state.Feedback = feedback
	a.state.PlanAttempts++

	// If we've exceeded max plan attempts, auto-build with current plan
	if a.state.PlanAttempts > maxPlanAttempts {
		ctx.GetLogger().Info("workflow_builder: max plan attempts reached, auto-building", "attempts", a.state.PlanAttempts)
		originalRequest := request
		originalRequest.Query = a.state.OriginalQuery
		return a.buildAndValidate(ctx, originalRequest, a.state.Intent, a.state.Plan)
	}

	// Regenerate plan incorporating feedback
	originalRequest := request
	originalRequest.Query = a.state.OriginalQuery
	plan, err := a.regeneratePlan(ctx, originalRequest, a.state.Intent, a.state.Plan, feedback)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("plan regeneration failed: %w", err)
	}

	a.state.Plan = plan
	a.state.Stage = "plan_approval"

	agentId := uuid.Nil
	if request.AgentId != "" {
		agentId = uuid.MustParse(request.AgentId)
	}

	updatedPlanQuestion := fmt.Sprintf("Here's my updated plan:\n\n%s\n\nWould you like to approve this updated plan or request changes?", plan)
	resp := core.NBAgentResponse{
		Status: core.ConversationStatusWaiting,
		FollowupRequest: core.FollowupRequest{
			Question:        updatedPlanQuestion,
			FollowupType:    core.FollowupTypeSingleSelect,
			FollowupOptions: []string{PlanApprovalOptionApprove, PlanApprovalOptionChanges},
			AgentName:       a.GetName(),
			AgentId:         agentId,
		},
	}
	return resp, nil
}

// generateClarifyingQuestions uses the LLM to identify genuine ambiguities in the user's
// request by cross-referencing against the account's environment (integrations, accounts, configs).
// Returns 0-3 questions. Returns nil if the request is clear enough to plan directly.
func (a *WorkflowBuilderAgent) generateClarifyingQuestions(ctx *security.RequestContext, request core.NBAgentRequest, intent string) ([]ClarifyingQuestion, error) {
	envContext := a.buildEnvironmentContext(ctx)
	configsContext := fetchConfigsContext(ctx, a.accountId)

	systemPrompt := fmt.Sprintf(`You are pre-planning an automation for Nudgebee. Before generating a plan, determine if the user's request has enough detail to build a useful automation.

%s
%s

USER INTENT (extracted):
%s

YOUR JOB:
1. Read the user's request carefully. Assess how much they've specified.
2. Classify the request:
   - VAGUE: The core purpose, trigger type, or key actions are missing or ambiguous (e.g., "create a workflow", "automate something for my cluster", "set up monitoring").
   - SPECIFIC: The user described what the automation should do, even if minor details are missing.
3. For VAGUE requests: Ask about the fundamentals — what should the automation do? What trigger? What key actions? These are critical questions, not optional.
4. For ALL requests (including SPECIFIC): Cross-reference the tasks the automation needs against the ACCOUNT ENVIRONMENT above. If the automation requires an external service (database, notification, cloud CLI, etc.) and there are multiple configured integrations of that type, ask which one to use. If only one exists, infer it.
5. If the trigger type is not explicitly stated, ask — do not silently default to "manual".

QUESTION PRIORITY (ask in this order of importance):
1. Core purpose — what should the automation actually do? (only if truly unclear)
2. Trigger type — manual, scheduled (cron), webhook, or event-driven? (if not specified or implied)
3. Integration choices — which specific integrations/configs to use when multiple exist
4. Key action details — targets, thresholds, recipients (only if not inferable from context)

QUESTION STYLE:
- Lead with your recommendation. Example: "I'll send notifications to Slack (#alerts). Want a different channel?" with options: ["Use #alerts (recommended)", "#engineering", "#ops"]
- NOT: "Which Slack channel should I use?" — that's lazy.
- If an integration is needed but not configured: explain the situation and offer concrete alternatives from what IS configured, or suggest adding a placeholder.
- If the user said "A or B?" — recommend one and explain why.
- Each option must be a concrete, actionable value — not a generic label.

WHEN TO RETURN ZERO QUESTIONS:
- The request describes a clear action (e.g., "check pod health and alert on Slack every hour")
- All needed integrations/accounts can be inferred from context (e.g., only one of a type exists)
- The user already named specific integrations, channels, clusters, trigger type, etc.

OUTPUT FORMAT (JSON only, no other text):
{
  "questions": [
    {
      "question": "I'll send alerts to your Slack workspace (nudgebee-team). Which channel?",
      "options": ["#alerts (recommended)", "#engineering", "#incidents"]
    }
  ]
}

Return {"questions": []} ONLY when the request is specific and detailed enough to plan directly.
Maximum 3 questions.`, envContext, configsContext, intent)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	// Use lite model for speed
	clarifyCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyUseLiteModel, true),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := core.GenerateAndTrackLLMContent(clarifyCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
	if err != nil {
		return nil, fmt.Errorf("clarification LLM call failed: %w", err)
	}

	if len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return nil, nil
	}

	content := strings.TrimSpace(completion.Choices[0].Content)
	// Strip markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Questions []ClarifyingQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to parse clarification response", "error", err, "content", content)
		return nil, nil
	}

	// Cap at 3 questions
	if len(result.Questions) > 3 {
		result.Questions = result.Questions[:3]
	}

	// Ensure every question has "Skip" as last option
	for i := range result.Questions {
		hasSkip := false
		for _, opt := range result.Questions[i].Options {
			if strings.EqualFold(opt, "Skip") {
				hasSkip = true
				break
			}
		}
		if !hasSkip {
			result.Questions[i].Options = append(result.Questions[i].Options, "Skip")
		}
	}

	return result.Questions, nil
}

// handleClarificationResponse processes the user's answer to a clarifying question.
// Stores the answer and either asks the next question or proceeds to plan generation.
func (a *WorkflowBuilderAgent) handleClarificationResponse(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	answer := strings.TrimSpace(request.Query)
	idx := a.state.ClarifyingIndex

	// Store the answer (unless skipped)
	if idx < len(a.state.ClarifyingAnswers) && !strings.EqualFold(answer, "Skip") {
		a.state.ClarifyingAnswers[idx] = answer
	}

	// Move to next question
	a.state.ClarifyingIndex++

	if a.state.ClarifyingIndex < len(a.state.ClarifyingQuestions) {
		// More questions to ask
		nextQ := a.state.ClarifyingQuestions[a.state.ClarifyingIndex]
		return a.buildClarificationResponse(request, nextQ)
	}

	// All questions answered — build clarification context and proceed to plan
	clarificationContext := a.buildClarificationContext()

	originalRequest := request
	originalRequest.Query = a.state.OriginalQuery
	return a.generatePlanAndAskApproval(ctx, originalRequest, a.state.Intent, clarificationContext)
}

// buildClarificationResponse formats a clarifying question as a FollowupRequest with structured option data.
func (a *WorkflowBuilderAgent) buildClarificationResponse(request core.NBAgentRequest, question ClarifyingQuestion) (core.NBAgentResponse, error) {
	agentId := uuid.Nil
	if request.AgentId != "" {
		agentId = uuid.MustParse(request.AgentId)
	}

	// Build structured option data for the frontend
	structuredOptions := make([]map[string]any, 0, len(question.Options))
	for _, opt := range question.Options {
		structuredOptions = append(structuredOptions, map[string]any{
			"label": opt,
		})
	}

	return core.NBAgentResponse{
		Status: core.ConversationStatusWaiting,
		FollowupRequest: core.FollowupRequest{
			Question:        question.Question,
			FollowupType:    core.FollowupTypeSingleSelect,
			FollowupOptions: question.Options,
			AgentName:       a.GetName(),
			AgentId:         agentId,
			FollowupData: map[string]any{
				"type":         "clarification",
				"options":      structuredOptions,
				"allow_custom": true,
				"allow_skip":   true,
			},
		},
	}, nil
}

// buildClarificationContext formats accumulated Q&A answers into a string for the plan prompt.
func (a *WorkflowBuilderAgent) buildClarificationContext() string {
	var parts []string
	for i, q := range a.state.ClarifyingQuestions {
		if i >= len(a.state.ClarifyingAnswers) {
			break
		}
		answer := a.state.ClarifyingAnswers[i]
		if answer == "" {
			continue // skipped
		}
		parts = append(parts, fmt.Sprintf("Q: %s\nA: %s", q.Question, answer))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\nCLARIFICATION ANSWERS (user-provided specifics — use these values in the plan):\n" + strings.Join(parts, "\n\n")
}

// getBuildSystemPrompt returns the system prompt for the agentic build mode.
func getBuildSystemPrompt(intent string, plan string, schema string) string {
	planSection := ""
	if plan != "" {
		planSection = fmt.Sprintf(`
APPROVED PLAN (follow this structure closely):
%s
`, plan)
	}

	return fmt.Sprintf(`You are a Nudgebee automation builder. Build the automation step by step using tools.

INTENT FROM USER REQUEST:
%s
%s
AUTOMATION SCHEMA REFERENCE:
%s

INSTRUCTIONS:
1. Call init_workflow to set up the automation structure (name from plan, triggers, inputs).
2. For each task in the plan:
   a. Call get_task_schema with the task type to understand required/optional parameters.
   b. Call add_task with the correct id, type, params, depends_on, and if condition.
3. After adding ALL tasks, call validate to check for errors.
4. If validation fails:
   a. Read the error message carefully — it identifies the problem and often the specific field.
   b. Call get_task for the affected task to see its current state.
   c. Call get_task_schema for that task type to verify the correct parameter format.
   d. Call modify_task to fix the specific issue.
   e. Call validate again. If the same error recurs, try a different approach entirely.
5. Once validation passes, call finalize to return the completed automation JSON.

CRITICAL RULES:
- Jinja2 references: {{ Tasks['task-id'].output.<field> }} (capital T, bracket notation)
- Integration IDs: {{ Configs.<type>_integration_id }} (e.g., {{ Configs.slack_integration_id }})
- Integer values: use 5, NOT 5.0
- Only use task types that get_task_schema returns — do NOT invent types
- Task IDs in depends_on must match actual task IDs you've added
- Manual trigger: NO params (or empty object)
- Schedule trigger: MUST have "cron" param
- SAFE OUTPUT ACCESS: When referencing output of tasks that may be SKIPPED, use default filters:
  {{ ((Tasks['id'].output.data | default({})).field | default('fallback')) }}
- DATA TRANSFORMATION: ALWAYS use scripting.run_script with language "python" and parser_type "json" instead of data.transform for any non-trivial transformation (slicing, filtering, mapping, object construction). JSONata has many syntax pitfalls. Python is reliable and expressive.
- SCRIPTING DATA INJECTION: ALWAYS pass task output to scripts via the "env" parameter — NEVER embed {{ }} template expressions inside the script string directly. Inline Jinja breaks when data contains quotes or special characters.
  Correct pattern:
    params:
      language: "python"
      parser_type: "json"
      env: { "INPUT_DATA": "{{ Tasks['prev'].output.data | to_json }}" }
      script: |
        import json, os
        try:
            data = json.loads(os.environ['INPUT_DATA'])
            print(json.dumps(data[:3]))
        except Exception as e:
            print(json.dumps({"error": str(e)}))
  Only use data.transform for trivial single-field extraction (e.g., expression: "fieldName").
- scripting.run_script with parser_type "json": stdout MUST be valid JSON. Always wrap Python scripts in try/except, print JSON on error.
- scripting.run_script: ALWAYS set "language" explicitly (e.g., "python"). Omitting it defaults to bash, which will fail for Python scripts.
- OPERATOR PRECEDENCE: Always wrap | default() in parentheses before comparison: {{ (value | default(false)) == true }}

OUTPUT FIELDS — DO NOT GUESS:
- ALWAYS call get_task_schema and read the "output_schema" to find the correct output field name for each task type.
- Different task types use different field names (data, logs, result, results, etc.). Using the wrong field causes silent failures.
- After calling get_task_schema, note the output field names and use ONLY those in your {{ Tasks['id'].output.<field> }} references.

TEMPLATE FILTERS — USE UNDERSCORES:
- Filters use underscores: to_json, from_json, to_yaml, from_yaml.
- NEVER use Jinja2-style names without underscores (tojson, fromjson). They do not exist in this engine.

TASK DEPENDENCIES (depends_on) — CRITICAL:
- The executor runs tasks in PARALLEL unless constrained by depends_on. Tasks without depends_on are all launched simultaneously.
- If task B references {{ Tasks['A'].output.data }}, task B MUST have depends_on: ["A"]. Otherwise B launches before A completes and gets None.
- This applies EVERYWHERE: top-level tasks, tasks inside core.foreach loop bodies, tasks inside core.group.
- EVERY task that uses {{ Tasks['X'].output... }} or {{ Tasks['X'].output... }} in params, if, or env MUST list X in its depends_on.
- Inside core.foreach loop bodies: subtasks reference each other by their original IDs (not prefixed). Add depends_on between subtasks the same way.

core.foreach — ITEM VARIABLE:
- The "item" param sets the loop variable name (default: "item"). ALWAYS set it explicitly.
- Variable names are CASE-SENSITIVE: if item="issue", use {{ issue.title }}, NOT {{ Issue.title }}.

FAILURE RESILIENCE:
- External service tasks (cloud.aws.cli, cloud.gcp.cli, cloud.k8s.cli, integrations.http, network.ssl) should have failure_policy: { action: "continue" } when their failure should not abort the entire workflow.
- For optional tasks that may fail, set failure_policy and have downstream tasks check the upstream status using {{ Tasks['x'].output.data | default('fallback') }}.

CONDITIONAL TASK DEPENDENCIES:
- When task A has an "if" condition and task B depends on A, B MUST handle the case where A was skipped.
- Use the | default() filter: {{ Tasks['A'].output.data | default('') }} to avoid "Can't use Getitem on None" errors.
- Or give B its own "if" condition that checks the same prerequisite as A.

REGRESSION PREVENTION:
- After modifying ANY task, call get_task to verify the change applied correctly.
- Before calling finalize, call list_tasks to verify ALL tasks still have correct output references.
- When fixing one task, do NOT change other tasks unless directly affected.`, intent, planSection, schema)
}

// getFixSystemPrompt returns the system prompt for the agentic fix mode.
func getFixSystemPrompt(errorContext string, schema string) string {
	return fmt.Sprintf(`You are a Nudgebee automation debugger. Fix the automation issue using tools.

ERROR CONTEXT:
%s

AUTOMATION SCHEMA REFERENCE:
%s

INSTRUCTIONS:
1. Call list_tasks to understand the current automation structure.
2. Identify which task(s) are related to the error.
3. Call get_task for the affected task(s) to see their full definition.
4. Call get_task_schema to understand the correct parameters for the task type.
5. Call modify_task to apply the fix.
6. Call validate to verify the fix works.
7. If validation still fails, repeat steps 2-6 for the new error.
8. Once validation passes, call finalize to return the corrected automation JSON.

If you need additional evidence about the failed run while applying the fix, you may
also call list_executions / get_execution to read the actual workflow-level error,
per-task errors, rendered_params, and outputs from a specific execution.

RULES:
- Only change what's NECESSARY — minimize modifications.
- Preserve existing task IDs, dependencies, and logic.
- Jinja2 references: {{ Tasks['task-id'].output.<field> }} — check get_task_schema output_schema for the correct field name.
- Integration IDs: {{ Configs.<type>_integration_id }}
- Integer values: use 5, NOT 5.0
- SCRIPTING DATA INJECTION: If a script task injects data via inline {{ }} in the script string (e.g., triple-quoted Jinja), refactor to use the "env" parameter instead. Inline templates break when data contains quotes or special characters.
  Fix pattern: add env: { "VAR": "{{ Tasks['x'].output.data | to_json }}" }, then read via os.environ['VAR'] in the script.
- scripting.run_script: Always set "language" explicitly. Missing language defaults to bash.
- Template filters use underscores: "to_json" (NOT "tojson"), "from_json" (NOT "fromjson").

OUTPUT FIELDS — DO NOT GUESS:
- ALWAYS call get_task_schema and read "output_schema" for the correct output field name.
- Different task types use different field names. Using the wrong field causes silent failures.

TASK DEPENDENCIES (depends_on) — CRITICAL:
- The executor runs tasks in PARALLEL unless constrained by depends_on.
- If task B references {{ Tasks['A'].output... }}, B MUST have depends_on: ["A"]. Otherwise B launches before A completes and gets None.
- This applies everywhere: top-level, inside foreach loop bodies, inside groups.
- Common error: "Can't use Getitem on None" means a referenced task hasn't completed — check depends_on.

core.foreach — ITEM VARIABLE:
- The "item" param sets the loop variable name (default: "item"). ALWAYS set it explicitly.
- Variable names are CASE-SENSITIVE: if item="issue", use {{ issue.title }}, NOT {{ Issue.title }}.

DEBUGGING METHODOLOGY:
- Read the validation error message carefully — it tells you what's wrong and often which field.
- Call get_task on the affected task to see its current definition.
- Call get_task_schema for that task's type — check BOTH input_schema and output_schema.
- Apply the minimal fix via modify_task, then call validate again.
- If the same error persists after a fix, do NOT repeat the same approach — try a fundamentally different solution.

FAILURE RESILIENCE:
- "Can't use Getitem on None" usually means: (a) missing depends_on, or (b) the upstream task was skipped (if condition was false).
- For skipped-task cascading: use {{ Tasks['x'].output.data | default('') }} when the upstream task has an "if" condition.
- For external service failures: consider adding failure_policy: { action: "continue" } if the task is non-critical.

REGRESSION PREVENTION:
- After modifying a task, call get_task to verify. Also check tasks that reference its output.
- Before calling finalize, call list_tasks to do a full consistency check.
- NEVER modify a task not mentioned in the error unless it directly depends on the broken task.`, errorContext, schema)
}

// buildAndValidate builds the workflow JSON from the approved plan using the agentic tool loop.
func (a *WorkflowBuilderAgent) buildAndValidate(ctx *security.RequestContext, request core.NBAgentRequest, intent string, plan string) (core.NBAgentResponse, error) {
	// Initialize empty working workflow
	a.state.WorkingWorkflow = map[string]interface{}{}

	schema := getWorkflowSchema()
	systemPrompt := getBuildSystemPrompt(intent, plan, schema)

	workflowJSON, err := a.runToolLoop(ctx, request, systemPrompt, fmt.Sprintf("Build the automation now based on the plan. Original request: %s", request.Query))
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("agentic build failed: %w", err)
	}

	return a.checkMissingConfigs(ctx, request, workflowJSON)
}

// configRefRegex matches {{ Configs.key_name }} with variable whitespace.
var configRefRegex = regexp.MustCompile(`\{\{\s*Configs\.(\w+)\s*\}\}`)

// extractConfigReferences parses workflow JSON for {{ Configs.xxx }} references and returns deduplicated keys.
func extractConfigReferences(workflowJSON string) []string {
	matches := configRefRegex.FindAllStringSubmatch(workflowJSON, -1)
	seen := map[string]bool{}
	var keys []string
	for _, m := range matches {
		key := m[1]
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

// fetchExistingConfigKeys calls GET /configs on the workflow server and returns a set of existing config keys.
func fetchExistingConfigKeys(ctx *security.RequestContext, accountId string) (map[string]bool, error) {
	resp, err := tools.DoRunbookRequest("GET", "configs", nil, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, err
	}
	var configs []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(resp, &configs); err != nil {
		return nil, fmt.Errorf("failed to parse configs response: %w", err)
	}
	keys := map[string]bool{}
	for _, c := range configs {
		keys[c.Key] = true
	}
	return keys, nil
}

// checkMissingConfigs detects {{ Configs.xxx }} references in the built workflow, finds the ones
// that don't exist on the server, and auto-creates them as empty configs so the workflow can be
// saved without falling over on the workflow-server's "validation failed" rejection (issue #29944).
// The user fills in the values afterwards via the Configs UI; the summary returned to the user lists
// the auto-created keys so they know what to fill in. If the config API is unreachable we degrade
// gracefully and let the workflow-server's save attempt produce the error path.
func (a *WorkflowBuilderAgent) checkMissingConfigs(ctx *security.RequestContext, request core.NBAgentRequest, workflowJSON string) (core.NBAgentResponse, error) {
	refs := extractConfigReferences(workflowJSON)
	if len(refs) == 0 {
		return a.finalizeWithAutoSave(ctx, request, workflowJSON, nil), nil
	}

	existing, err := fetchExistingConfigKeys(ctx, a.accountId)
	if err != nil {
		// Graceful degradation: config API unreachable — return workflow as-is.
		ctx.GetLogger().Warn("workflow_builder: config API unreachable, skipping missing config check", "error", err)
		return a.finalizeWithAutoSave(ctx, request, workflowJSON, nil), nil
	}

	var missing []string
	for _, key := range refs {
		if !existing[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return a.finalizeWithAutoSave(ctx, request, workflowJSON, nil), nil
	}

	created, failed := a.autoCreateEmptyConfigs(ctx, missing)
	if len(failed) > 0 {
		ctx.GetLogger().Warn("workflow_builder: some configs failed to auto-create", "failed", failed)
	}
	return a.finalizeWithAutoSave(ctx, request, workflowJSON, created), nil
}

// autoCreatedConfigPlaceholder is the value stored when we auto-create a config
// for a missing reference. The workflow server rejects empty values, so we use
// a clearly-flagged placeholder that the user can grep for and replace.
const autoCreatedConfigPlaceholder = "<TODO: set value>"

// autoCreateEmptyConfigs POSTs a placeholder-valued config for each missing key.
// The workflow server rejects empty values, so we cannot truly create "empty"
// configs — instead we create one with autoCreatedConfigPlaceholder so the
// workflow saves cleanly and the user is prompted to fill in the real value.
// Returns the keys that were created successfully and the keys that failed.
// Failures are logged here; callers decide whether to propagate.
func (a *WorkflowBuilderAgent) autoCreateEmptyConfigs(ctx *security.RequestContext, keys []string) (created, failed []string) {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()
	for _, key := range keys {
		body := map[string]string{"key": key, "value": autoCreatedConfigPlaceholder, "type": "config"}
		if _, err := tools.DoRunbookRequest("POST", "configs", body, a.accountId, tenantId, userId); err != nil {
			ctx.GetLogger().Error("workflow_builder: auto-create config failed", "key", key, "error", err)
			failed = append(failed, key)
			continue
		}
		created = append(created, key)
	}
	return created, failed
}

// finalizeWithAutoSave returns the terminal response with the workflow JSON and
// attempts to auto-save the workflow via the workflow server API.
// autoCreatedConfigs lists keys we just created as empty placeholders for missing
// {{ Configs.x }} references; the user is told to fill in values for them.
// For WorkflowBuilder source (editor UI): returns raw JSON since the UI consumes it directly.
// For other sources (ask-nudgebee chat): returns a markdown summary with a link to the saved workflow.
func (a *WorkflowBuilderAgent) finalizeWithAutoSave(ctx *security.RequestContext, request core.NBAgentRequest, workflowJSON string, autoCreatedConfigs []string) core.NBAgentResponse {
	// WorkflowBuilder source in create mode: return raw JSON (UI consumes it directly).
	// In fix mode, auto-save so the fix persists server-side — the UI may not re-save.
	if request.ConversationSource == core.ConversationSourceWorkflowBuilder && a.state.Mode != "fix" {
		return core.NBAgentResponse{Response: []string{workflowJSON}, IsTerminal: true}
	}

	workflowId, saveErr := a.autoSaveWorkflow(ctx, workflowJSON, request.SessionId)
	if saveErr != nil {
		ctx.GetLogger().Error("workflow_builder: auto-save failed", "error", saveErr)
	} else {
		ctx.GetLogger().Info("workflow_builder: auto-saved workflow", "workflow_id", workflowId)
	}
	saved := workflowId != ""

	// Build a human-readable summary instead of returning raw JSON
	summary, summaryErr := buildWorkflowSummary(workflowJSON, a.state.Mode, saved)
	if summaryErr != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to build summary, returning raw JSON", "error", summaryErr)
		return core.NBAgentResponse{Response: []string{workflowJSON}, IsTerminal: true}
	}

	resp := core.NBAgentResponse{IsTerminal: true}
	switch {
	case saved:
		link := fmt.Sprintf("/workflow/%s?accountId=%s&session_id=%s#editor", workflowId, a.accountId, request.SessionId)
		summary += fmt.Sprintf("\n\n[Open in Editor](%s)", link)
		resp.References = []toolcore.NBToolResponseReference{{
			Text: "Open in Editor",
			Url:  link,
			Type: "link",
		}}
	default:
		summary += fmt.Sprintf("\n\n*Auto-save failed — %s*", truncateSaveError(saveErr))
	}

	if len(autoCreatedConfigs) > 0 {
		summary += fmt.Sprintf("\n\n*Created %d placeholder config(s) so the automation could save: `%s`. Each was set to `%s` — replace via Configs before running the automation.*",
			len(autoCreatedConfigs), strings.Join(autoCreatedConfigs, "`, `"), autoCreatedConfigPlaceholder)
	}

	resp.Response = []string{summary}
	return resp
}

// truncateSaveError returns a short, user-facing rendition of the save error.
// The full error is already in logs; the UI just needs enough to act on.
// Truncates by runes (not bytes) so multi-byte UTF-8 characters are not split.
func truncateSaveError(err error) string {
	if err == nil {
		return "automation server returned no workflow id."
	}
	msg := err.Error()
	const maxRunes = 240
	runes := []rune(msg)
	if len(runes) > maxRunes {
		msg = string(runes[:maxRunes]) + "…"
	}
	return msg
}

// summaryHeadline produces the first line of the workflow summary, varying the
// verb based on whether the change was persisted (saved) and whether it is a
// new build or a fix. Callers append the per-attempt save status separately.
func summaryHeadline(name, mode string, saved bool) string {
	switch {
	case mode == "fix" && saved:
		return fmt.Sprintf("The automation **`%s`** has been updated.", name)
	case mode == "fix":
		return fmt.Sprintf("The automation **`%s`** changes are ready (not yet saved).", name)
	case saved:
		return fmt.Sprintf("The automation **`%s`** has been built and saved.", name)
	default:
		return fmt.Sprintf("The automation **`%s`** has been built (not yet saved).", name)
	}
}

// buildWorkflowSummary parses the workflow JSON and returns a markdown summary
// with the workflow name, trigger type, and a numbered list of tasks. The headline
// reflects whether the workflow was persisted to the automation server.
func buildWorkflowSummary(workflowJSON string, mode string, saved bool) (string, error) {
	var wf map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &wf); err != nil {
		return "", fmt.Errorf("buildWorkflowSummary: failed to parse workflow JSON: %w", err)
	}

	name, _ := wf["name"].(string)
	if name == "" {
		name = "automation"
	}

	// Extract from definition block
	triggerType := "manual"
	var tasks []map[string]interface{}
	if def, ok := wf["definition"].(map[string]interface{}); ok {
		// Triggers
		if triggers, ok := def["triggers"].([]interface{}); ok && len(triggers) > 0 {
			if t, ok := triggers[0].(map[string]interface{}); ok {
				if tt, ok := t["type"].(string); ok && tt != "" {
					triggerType = tt
				}
			}
		}
		// Tasks
		if rawTasks, ok := def["tasks"].([]interface{}); ok {
			for _, rt := range rawTasks {
				if t, ok := rt.(map[string]interface{}); ok {
					tasks = append(tasks, t)
				}
			}
		}
	} else if triggers, ok := wf["triggers"].([]interface{}); ok && len(triggers) > 0 {
		// Fallback: triggers at top level (some formats)
		if t, ok := triggers[0].(map[string]interface{}); ok {
			if tt, ok := t["type"].(string); ok && tt != "" {
				triggerType = tt
			}
		}
	}

	headline := summaryHeadline(name, mode, saved)

	var sb strings.Builder
	sb.WriteString(headline)
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "**Trigger:** %s\n\n", triggerType)

	if len(tasks) > 0 {
		sb.WriteString("**Tasks:**\n")
		for i, task := range tasks {
			taskId, _ := task["id"].(string)
			taskType, _ := task["type"].(string)
			if taskId == "" {
				taskId = fmt.Sprintf("task-%d", i+1)
			}
			if taskType == "" {
				taskType = "unknown"
			}
			fmt.Fprintf(&sb, "%d. **%s** — `%s`\n", i+1, taskId, taskType)
		}
	}

	return strings.TrimRight(sb.String(), "\n"), nil
}

// autoSaveWorkflow persists the workflow JSON to the workflow server. In create
// mode it POSTs a new workflow; in fix mode it PUTs an update to the existing one.
// On create, the originating chat session_id is stamped into the payload so the UI
// can deep-link back to the conversation. The runbook server ignores this field on
// update — the original creation context is immutable.
func (a *WorkflowBuilderAgent) autoSaveWorkflow(ctx *security.RequestContext, workflowJSON string, sessionId string) (string, error) {
	definition, err := tools.EnsureDefinitionObject(workflowJSON)
	if err != nil {
		return "", fmt.Errorf("workflow_builder: invalid workflow JSON for auto-save: %w", err)
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()

	if a.state.Mode == "fix" && a.state.WorkflowId != "" {
		resp, err := tools.DoRunbookRequest("PUT", fmt.Sprintf("workflows/%s", a.state.WorkflowId), definition, a.accountId, tenantId, userId)
		if err != nil {
			return "", fmt.Errorf("workflow_builder: auto-update failed: %w", err)
		}
		return extractWorkflowId(resp, a.state.WorkflowId), nil
	}

	if sessionId != "" {
		if defMap, ok := definition.(map[string]interface{}); ok {
			defMap["created_from_session_id"] = sessionId
		}
	}

	resp, err := tools.DoRunbookRequest("POST", "workflows", definition, a.accountId, tenantId, userId)
	if err != nil {
		return "", fmt.Errorf("workflow_builder: auto-create failed: %w", err)
	}
	return extractWorkflowId(resp, ""), nil
}

// extractWorkflowId tries to extract the workflow ID from the server response.
func extractWorkflowId(resp []byte, fallback string) string {
	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err == nil {
		if id, ok := result["id"].(string); ok && id != "" {
			return id
		}
	}
	return fallback
}

// extractIntent analyzes the user's request and extracts workflow requirements.
func (a *WorkflowBuilderAgent) extractIntent(ctx *security.RequestContext, request core.NBAgentRequest) (string, error) {
	envContext := a.buildEnvironmentContext(ctx)
	taskTypeNames := a.fetchTaskTypeNames(ctx)

	systemPrompt := fmt.Sprintf(`You are an automation intent analyzer for Nudgebee.
%s
%s

TASK: Analyze the user's request and extract automation requirements.
Use ONLY task types from the available list above. Match user intent to the correct task types:
- Log queries → observability.logs (NEVER scripting.run_script)
- Metric queries → observability.metrics (NEVER scripting.run_script)
- AI investigation/code analysis/auto-fixing → llm.investigate
- GitHub operations → scm.github.cli
- Cloud CLI → cloud.<provider>.cli
- K8s operations → k8s.cli
- Database queries → dbms.query
- Ticketing → tickets.create

OUTPUT FORMAT (JSON):
{
  "description": "Brief automation description",
  "task_types_needed": ["task.type1", "task.type2"],
  "needs_inputs": true/false,
  "needs_conditionals": true/false,
  "needs_state": true/false,
  "sequence": "sequential|parallel|mixed"
}

EXAMPLES:

User: "Check pod health in nudgebee and nudgebee-test namespaces"
Output: {
  "description": "Check pod health across multiple namespaces",
  "task_types_needed": ["k8s.cli"],
  "needs_inputs": false,
  "needs_conditionals": false,
  "needs_state": false,
  "sequence": "sequential"
}

User: "Monitor logs and send Slack alerts if errors found"
Output: {
  "description": "Log monitoring with conditional Slack alerts",
  "task_types_needed": ["observability.logs", "notifications.im"],
  "needs_inputs": false,
  "needs_conditionals": true,
  "needs_state": false,
  "sequence": "sequential"
}

Return ONLY the JSON object.`, taskTypeNames, envContext)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(completion.Choices[0].Content), nil
}

// buildEnvironmentContext fetches the account's configured integrations, cloud accounts,
// and observability providers to give the LLM awareness of what is actually available.
// Uses cached APIs (ListAllToolConfigs: 30min, GetAccountConfigSummary: 30min).
func (a *WorkflowBuilderAgent) buildEnvironmentContext(ctx *security.RequestContext) string {
	var parts []string

	// 1. Named integrations + cloud accounts (cached 30 min)
	allConfigs, err := toolcore.ListAllToolConfigs(ctx, a.accountId)
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to list tool configs", "error", err)
	} else {
		var cloudAccounts []string
		integrationsByType := map[string][]string{} // type → [name1, name2]

		for _, cfg := range allConfigs {
			configType := cfg.Schema.ConfigType
			if configType == "" {
				continue
			}
			lowerType := strings.ToLower(configType)
			if lowerType == "aws" || lowerType == "gcp" || lowerType == "azure" || lowerType == "k8s" {
				cloudAccounts = append(cloudAccounts, fmt.Sprintf("%s (%s)", cfg.Name, configType))
			} else {
				integrationsByType[lowerType] = append(integrationsByType[lowerType], cfg.Name)
			}
		}

		if len(cloudAccounts) > 0 {
			parts = append(parts, "Cloud accounts: "+strings.Join(cloudAccounts, ", "))
		}

		if len(integrationsByType) > 0 {
			var integrationParts []string
			for iType, names := range integrationsByType {
				integrationParts = append(integrationParts, iType+": "+strings.Join(names, ", "))
			}
			sort.Strings(integrationParts)
			parts = append(parts, "Integrations: "+strings.Join(integrationParts, " | "))
		}
	}

	// 2. Agent connectivity (needed for tools with ToolConfigSourceAccountAgent)
	summary, summaryErr := toolcore.GetAccountConfigSummary(ctx, a.accountId)
	if summaryErr == nil && summary.HasAgent {
		parts = append(parts, "Nudgebee agent: connected")
	}

	// 3. Default observability providers (log + metrics)
	if logProvider, err := services_server.GetObservabilityProvider(*ctx, a.accountId, "logs"); err == nil && logProvider.Provider != "" {
		parts = append(parts, "Default log provider: "+logProvider.Provider+" (use observability.logs — query syntax depends on provider)")
	}
	if metricProvider, err := services_server.GetObservabilityProvider(*ctx, a.accountId, "metrics"); err == nil && metricProvider.Provider != "" {
		parts = append(parts, "Default metrics provider: "+metricProvider.Provider+" (use observability.metrics)")
	}

	if len(parts) == 0 {
		return ""
	}

	return "\nACCOUNT ENVIRONMENT (configured integrations and cloud accounts on this account):\n" + strings.Join(parts, "\n") +
		"\n\nWhen the user references an integration by name (e.g., 'query dev-pg'), match it to the configured integration above and use {{ Configs.<name>_integration_id }} to reference it." +
		"\nIf a task REQUIRES an integration NOT listed above, warn the user that it is not currently configured."
}

// fetchTaskTypeNames fetches registered task type names from the runbook server.
func (a *WorkflowBuilderAgent) fetchTaskTypeNames(ctx *security.RequestContext) string {
	tasksResp, err := tools.DoRunbookRequest("GET", "tasks", nil, a.accountId,
		ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to fetch task types from runbook server", "error", err)
		return ""
	}
	if len(tasksResp) == 0 {
		return ""
	}
	var tasks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(tasksResp, &tasks); err != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to parse task types response", "error", err)
		return ""
	}
	types := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.Type != "" {
			types = append(types, t.Type)
		}
	}
	if len(types) == 0 {
		return ""
	}
	return "\nAVAILABLE TASK TYPES (use ONLY these — do NOT invent task types):\n" + strings.Join(types, ", ")
}

// getWorkflowPlanningContext returns the common reference knowledge for planning prompts.
func getWorkflowPlanningContext() string {
	return `
TRIGGER TYPES AND THEIR REQUIRED PARAMETERS:
- "manual" → No params allowed. User triggers manually from UI.
- "schedule" → Requires: cron (string, e.g. "0 9 * * MON-FRI"), Optional: overlap_policy ("Skip"|"BufferOne"|"AllowAll"|"CancelOther"|"TerminateOther"), catchup_window (duration). IMPORTANT: Always set overlap_policy: "Skip" for monitoring automations to prevent overlapping runs.
- "webhook" → Requires: integration_name (string). Optional: filter (Jinja2 expression on payload), secret (string)
- "event" → Requires: event_type (string, e.g. "ticket.created"). Optional: filter (Jinja2 expression on event)

COMMON TASK TYPES — WHEN TO USE EACH:

OBSERVABILITY (ALWAYS use these for log/metric queries — NEVER scripting.run_script):
- observability.logs → Query logs from the account's configured log provider (auto-detected at runtime).
  ALWAYS use this for log queries. Query syntax depends on the configured provider.
  Params: account_id (optional), query (string). Output: logs
- observability.log_groups → Group and aggregate log entries. Params: start_time, end_time, namespace, etc. Output: { groups[] }
- observability.metrics → Query metrics from the account's configured metrics provider (auto-detected at runtime).
  ALWAYS use this for metric queries. Params: metric query params. Output: metrics

AI & INVESTIGATION:
- llm.investigate → Invoke Nudgebee's AI agent for investigation, diagnostics, or code analysis.
  Has built-in coding agent — can analyze code, identify root causes, suggest fixes, and raise PRs.
  Use this for ANY AI-powered analysis. Do NOT invent custom AI task types.
  Params: message (string). Output: { data, conversation_id }

NOTIFICATIONS (REQUIRES: a matching notification integration configured on the account):
- notifications.im → Send messages to configured IM provider. Params: provider ("slack"|"teams"), channel (string), message (string). Optional: message_thread_id, template, team_id. Output: { channel, message_id, team, provider }
- notifications.read_thread → Read thread replies/reactions. Params: provider ("slack"), channel_id, thread_ts. Output (directly on output, NOT output.data): { success, messages[], has_responses, has_reactions, reply_count, error, channel_id, thread_ts }. Each message has: ts, text, user, reactions[], is_parent.

KUBERNETES (REQUIRES: K8s cloud account configured):
- k8s.cli → Run kubectl commands (without "kubectl" prefix). Params: command (string, e.g. "get pods -n ns -o json"). Optional: account_id. Output: { data }

CLOUD CLI (REQUIRES: cloud account of matching provider type):
- cloud.aws.cli → Run AWS CLI commands. Params: account_id, command (full AWS CLI string). Output: { data }
- cloud.azure.cli → Run Azure CLI commands. Params: account_id, command. Output: { data }
- cloud.gcp.cli → Run GCP CLI commands. Params: account_id, command. Output: { data }

SOURCE CONTROL (REQUIRES: matching SCM integration configured):
- scm.github.cli → Execute GitHub CLI (gh) commands with auto-authenticated token.
  Params: integration_id (use {{ Configs.github_integration_id }}), command (gh CLI string). GITHUB_TOKEN is auto-set. Output: raw stdout

DATABASES (REQUIRES: matching database integration configured):
- dbms.query → Run SQL queries. Params: integration_id (use {{ Configs.<name>_integration_id }}), dbms_type, command (SQL). Output: query result

TICKETING (REQUIRES: a ticketing integration configured):
- tickets.create → Create tickets. Params: ticket details. Output: created ticket
- tickets.add_comment → Add comment to ticket. Params: ticket ID, comment. Output: confirmation

CI/CD (REQUIRES: matching CI/CD integration configured):
- cicd.argocd → ArgoCD operations. Output: result

DATA PROCESSING:
- data.transform → expression (JSONata or JS string), input (template string), inputType ("json"|"yaml"), Optional: outputType, scriptType ("jsonata"|"javascript"). Output: { data }
  WARNING: Only use for trivial single-field extraction (e.g., expression: "fieldName"). For ANY non-trivial transformation, use scripting.run_script with Python instead. JSONata has many syntax limitations that cause runtime failures.
- data.filter → data (any), expression (string). Output: { filtered_data }

SCRIPTING (last resort — NOT for log/metric queries or operations that have dedicated task types):
- scripting.run_script → Run custom scripts in a container. Use ONLY when no dedicated task type exists.
  Params: script (string), language ("bash"|"python"|"javascript"). Optional: args[], env{}, parser_type ("json"), image, resources{cpu_request,memory_limit,...}. Output: { data }
  Note: Container has Python, Node.js, Bash but NOT gh CLI. For GitHub operations use scm.github.cli or urllib.request in Python.
  Note: With parser_type "json", stdout MUST be valid JSON. Always wrap Python scripts in try/except and print JSON on error.
  PATTERN for passing data into Python: Use triple-quoted template injection:
    script: |
      import json
      DATA = '''{{ Tasks['prev'].output.data | to_json }}'''
      try:
          items = json.loads(DATA)
          result = items[:3]  # slice, filter, transform freely
          print(json.dumps(result))
      except Exception as e:
          print(json.dumps({"error": str(e)}))

HTTP & NETWORKING:
- integrations.http → Make HTTP API calls. Params: url, method. Optional: headers{}, body, timeout, insecure_skip_verify. Output: { status_code, headers, body }
- network.ssl → Check SSL certificate. Params: host (e.g. "example.com:443"). Output: { data } with cert info, expiry dates

FLOW CONTROL:
- core.foreach → Iterate over list. Params: items, tasks[], item (var name), concurrency (int). Output: { results[] }
- core.switch → Branch based on value. Params: value, branches{}. Output: matched branch result
- core.group → Group sub-tasks. Params: tasks[]. Output: { workflowId, runId }
- core.wait → Pause execution. Params: duration (e.g. "5m"). Output: { waited }
- core.approval → Request human approval. Params: message, timeout. Output: approval response
- core.call-workflow → Call another automation. Params: workflow_name, inputs{}. Output: { workflow_id, run_id, output }
- core.print → Print a message. Params: message. Output: { data }

MATRIX (parallel iteration over list):
  Instead of core.foreach, tasks can have a "matrix" field for parallel execution over items:
    - id: comment-on-issues
      type: scm.github.cli
      matrix:
        issue: "{{ Tasks['fetch'].output.data.issue_numbers }}"
      failure_policy:
        action: "continue"
      params:
        command: "gh issue comment {{ Matrix.issue | int }} --repo org/repo -b 'message'"
  Key rules:
  - matrix value MUST be an array
  - Access via {{ Matrix.<var_name> }}
  - Tasks run in PARALLEL (one per item)
  - Use failure_policy: continue so one failure doesn't block others

TEMPLATE VARIABLES (available in "if", "params", "set_state", "output" fields as Jinja2):
- {{ Inputs.input_id }} — automation input values
- {{ Tasks['task-id'].output.data }} — output from a previous task (use bracket notation for hyphenated IDs)
- {{ Tasks['task-id'].status }} — status of a task: COMPLETED, FAILED, SKIPPED, STARTED, TIMED_OUT, CANCELED
- {{ Vars.var_name }} — automation variables set via set_vars
- {{ State['key'] }} — persistent state across automation runs (set via set_state, survives between executions)
- {{ Configs.config_key }} — platform configuration values (e.g., Configs.slack_channel, Configs.aws_account_id)
- {{ Secrets.SECRET_NAME }} — environment secrets (SECRET_ prefix)
- {{ Self.output.field }} — current task's own output (useful in set_state)
- {{ now() }} — current UTC timestamp
- {{ Matrix.key }} — matrix task variables

USEFUL TEMPLATE FILTERS:
- | time_add('-1h') or | time_add('2d') — add/subtract duration from time
- | date_format('2006-01-02') — format time using Go layout
- | to_json / | from_json — JSON conversion
- | to_yaml / | from_yaml — YAML conversion
- | length — get list/string length
- | default('fallback') — default if null/empty
- | int — convert to integer (e.g., {{ Matrix.issue | int }})
- | string — convert to string (e.g., {{ id | string }})
- | first / | last — first/last element of array
- | join(',') — join array with delimiter
- | regex_search(pattern) / | regex_replace(pattern, repl) — regex operations
- | b64encode / | b64decode — base64 encoding
- | human_readable — format bytes (e.g., "1.2 GB")
- ~ operator — string concatenation in templates: 'prefix_' ~ value ~ '_suffix'

STATE PERSISTENCE (set_state):
- Simple: "set_state": { "key": "{{ Self.output.value }}" }
- With TTL: "set_state": { "key": { "value": "{{ Self.output.value }}", "ttl": "24h" } }
- Access: {{ State['key'] }} — returns null if not set or expired
- State persists ACROSS automation runs (useful for thread IDs, counters, last-run timestamps)

FAILURE HANDLING:
- failure_policy.action: "continue" (skip and proceed) or "fail" (stop automation, default)
- failure_policy.retry: { initial_interval, backoff_coefficient, maximum_interval, maximum_attempts }
- Use failure_policy: { action: "continue" } on: matrix tasks, notification tasks, debug/print tasks, and any non-critical task that shouldn't block the automation

CRITICAL TEMPLATE GOTCHAS:
- SAFE OUTPUT ACCESS: When a task is SKIPPED, its output.data is null. Accessing nested properties will fail with "Can't use Getitem on None".
  BAD:  {{ Tasks['x'].output.data.field == true }}
  GOOD: {{ ((Tasks['x'].output.data | default({})).field | default(false)) == true }}
- OPERATOR PRECEDENCE: The | default() filter has lower precedence than comparison operators. Without explicit parentheses, the expression may be parsed incorrectly.
  BAD:  {{ value | default(false) == true }}
  GOOD: {{ (value | default(false)) == true }}
- STRING CONTAINS: The "in" operator does NOT work in "if" conditions. Use a data.transform task with $contains() instead.
  BAD (will fail):  "if": "{{ ':green_circle:' in Tasks['llm'].output.data }}"
  GOOD: Use data.transform with expression: { "is_go": $contains($, ":green_circle:") }, then check Tasks['check'].output.data.is_go == true
- AVOID data.transform FOR COMPLEX LOGIC: JSONata does NOT support Python-style slicing ($[0:3]), has quirky object construction syntax, and many other pitfalls. For ANY non-trivial transformation (slicing, filtering, mapping, object building), use scripting.run_script with language "python" and parser_type "json" instead. Only use data.transform for trivial single-field extraction.
- CRON IS UTC: IST 9 AM = "30 3 * * 1-5" UTC.
- DATE-NAMESPACED STATE KEYS: For automations needing fresh state per day/week, include date in key:
  "if": "{{ State['check_' ~ (now() | date_format('2006-01-02')) ~ '_ts'] == null }}"
`
}

// fetchConfigsContext fetches available configs from the runbook server and formats them for the planning prompt.
func fetchConfigsContext(ctx *security.RequestContext, accountId string) string {
	configsResp, err := tools.DoRunbookRequest("GET", "configs", nil, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil || len(configsResp) == 0 {
		return ""
	}
	return fmt.Sprintf(`
AVAILABLE CONFIGS ON THIS ACCOUNT (use {{ Configs.<key> }} to reference these in automation definitions):
%s
`, string(configsResp))
}

// generatePlan creates a human-readable workflow plan for user approval.
// clarificationContext contains user's answers to clarifying questions (empty if none were asked).
func (a *WorkflowBuilderAgent) generatePlan(ctx *security.RequestContext, request core.NBAgentRequest, intent string, clarificationContext string) (string, error) {
	tasksResp, err := tools.DoRunbookRequest("GET", "tasks", nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	var taskTypesInfo string
	if err == nil {
		taskTypesInfo = string(tasksResp)
	} else {
		taskTypesInfo = "Task types not available"
	}

	configsInfo := fetchConfigsContext(ctx, a.accountId)
	planningContext := getWorkflowPlanningContext()
	envContext := a.buildEnvironmentContext(ctx)

	systemPrompt := fmt.Sprintf(`You are a world-class automation planner for Nudgebee. Your job is to create a precise, actionable plan that a user can review BEFORE we build the actual automation JSON. A great plan means the automation builds correctly on the first attempt.

INTENT FROM USER REQUEST:
%s
%s
REGISTERED TASK TYPES ON THIS ACCOUNT:
%s
%s
%s
%s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PLAN FORMAT — Fill in every section. Be specific, not vague.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

**1. Automation Name:** (kebab-case, 3-50 chars, e.g. "pod-health-monitor")

**2. Trigger:**
   - Type: manual | schedule | webhook | event
   - If schedule: exact cron expression + overlap_policy
   - If webhook: integration_name + optional filter
   - If event: event_type + optional filter
   - If manual: just "manual" (no params)

**3. Inputs:** (skip if none)
   - For each: id, type (string/number/boolean/json), default value, whether required
   - Example: namespace (string, default: "nudgebee", required: false)

**4. Tasks:** (numbered, in execution order)
   For EACH task, specify:
   - Task ID (kebab-case)
   - Task type (MUST be from the registered types above)
   - Key parameters with ACTUAL values (not placeholders)
   - depends_on (which prior task IDs, if any)
   - Condition ("if" expression, if any)
   - What this task outputs (for downstream reference)

**5. Data Flow:**
   - How outputs flow between tasks (e.g., "Task 2 uses {{ Tasks['task-1'].output.data }}")
   - Any data transformations needed

**6. Conditionals & Branching:** (skip if none)
   - Exact conditions using Jinja2 syntax

**7. State Management:** (skip if not needed)
   - What state keys to set/read
   - TTL for state entries
   - Why state is needed (e.g., "store Slack thread ID for daily grouping")

**8. Error Handling:** (skip if using defaults)
   - Which tasks need retry or continue-on-failure

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

**Configuration values needed from you:**
Review what the user has and hasn't specified. List EVERY value the automation requires that isn't provided:
- Notification channel (Slack channel name or "will use {{ Configs.slack_channel }}")
- Cron schedule expression
- Namespace(s) / cluster targets
- Threshold values for conditions
- Integration names (webhook, cloud accounts)
- Specific kubectl commands or API endpoints
- Script content details
- Any hardcoded values vs Configs references

For each, show:
  - What's needed
  - Suggested default (if applicable)
  - "Using: {{ Configs.xxx }}" if it should come from platform config

If the user provided everything, write: "None — all configuration values are specified."

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

RULES:
- Only use task types from the REGISTERED TASK TYPES list above
- Be specific: "k8s.cli with command: get pods -n nudgebee -o json" not "k8s task to get pods"
- For notifications.im always specify: provider, channel, message content approach
- For schedule triggers always include cron + overlap_policy
- Use {{ Configs.xxx }} for environment-specific values the user shouldn't hardcode
- For integration_id and account_id fields: use {{ Configs.<type>_integration_id }} (e.g., {{ Configs.jira_integration_id }}). These are account-level stored values that resolve at runtime. NEVER use fake UUIDs.
- Do NOT generate JSON — this is a plan description only

PREREQUISITES:
- If a task requires an integration NOT listed in ACCOUNT ENVIRONMENT, warn: "⚠️ Requires <integration type> — not currently configured on this account."
- Match the customer's actual stack — if their log provider is loki, write LogQL queries. If datadog, use Datadog query syntax. Etc.
- When the user references an integration by name, match it to the configured integrations and use the correct {{ Configs.<name>_integration_id }}.

APPROVAL GATES (proactive — suggest even if the user didn't ask):
- Suggest a core.approval task BEFORE any destructive or high-impact operation:
  Deleting resources, stopping/terminating instances or pods, scaling down to zero, modifying IAM/security groups/access policies, draining/cordoning nodes, database migrations, deploying to production, modifying DNS or load balancer configs.
- If any step targets a "prod" or "production" environment, suggest approval.

ERROR HANDLING (suggest based on task risk):
- Non-critical tasks (notifications, debug prints): failure_policy: { action: "continue" }
- Tasks in matrix/foreach loops: failure_policy: { action: "continue" } so one failure doesn't block others
- Transient-failure tasks (HTTP calls, cloud API): suggest retry with backoff
- Critical tasks (data modification, deployment): no continue-on-failure

NOTIFICATIONS:
- For production-targeting automations: suggest notifying on failure (e.g., Slack alert)
- For long-running or scheduled automations: suggest notifications on completion
- Use the customer's configured notification channel from ACCOUNT ENVIRONMENT if available

SAFETY:
- If the automation targets "all instances", "all pods", "all clusters" without filters, flag this and suggest adding scope constraints
- If environment is ambiguous (user didn't specify prod vs staging), ask in the "Configuration values needed" section

NOTE: In all user-facing text, refer to these as "automations" (not "workflows"). The product uses the term "Automation".`, intent, clarificationContext, taskTypesInfo, configsInfo, envContext, planningContext)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(completion.Choices[0].Content), nil
}

// regeneratePlan creates an updated plan incorporating user feedback.
func (a *WorkflowBuilderAgent) regeneratePlan(ctx *security.RequestContext, request core.NBAgentRequest, intent string, previousPlan string, feedback string) (string, error) {
	tasksResp, err := tools.DoRunbookRequest("GET", "tasks", nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	var taskTypesInfo string
	if err == nil {
		taskTypesInfo = string(tasksResp)
	} else {
		taskTypesInfo = "Task types not available"
	}

	configsInfo := fetchConfigsContext(ctx, a.accountId)
	planningContext := getWorkflowPlanningContext()
	envContext := a.buildEnvironmentContext(ctx)

	systemPrompt := fmt.Sprintf(`You are a world-class automation planner for Nudgebee. The user has reviewed your previous plan and requested changes.

INTENT FROM USER REQUEST:
%s

PREVIOUS PLAN:
%s

USER FEEDBACK:
%s

REGISTERED TASK TYPES ON THIS ACCOUNT:
%s
%s
%s
%s

Create an UPDATED plan that addresses the user's feedback. Use the SAME format as before:

**1. Automation Name**
**2. Trigger** (with exact params — cron expression, integration name, event type)
**3. Inputs** (with id, type, default, required)
**4. Tasks** (with task ID, type, key parameters with actual values, depends_on, conditions)
**5. Data Flow** (how outputs connect between tasks)
**6. Conditionals & Branching**
**7. State Management**
**8. Error Handling**

**Configuration values needed from you:**
- If the user provided values in their feedback (e.g., channel name, namespace, cron), use them and remove from this list
- List any remaining unspecified values
- If all values are now provided, write: "None — all configuration values are specified."

RULES:
- Only use task types from the REGISTERED TASK TYPES list
- Incorporate ALL user feedback — don't ignore any requests
- Be specific with actual parameter values, not vague descriptions
- Do NOT generate JSON`, intent, previousPlan, feedback, taskTypesInfo, configsInfo, envContext, planningContext)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Original request: %s\n\nPlease update the plan based on my feedback: %s", request.Query, feedback)),
	}

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(completion.Choices[0].Content), nil
}

// coerceWorkflowTypes walks the workflow JSON map and converts float64 values
// that are whole numbers to int. Go's json.Unmarshal into map[string]interface{}
// always produces float64 for JSON numbers, but the validation API expects
// integer types for fields like concurrency, max_retries, etc.
func coerceWorkflowTypes(data map[string]interface{}) {
	for key, val := range data {
		switch v := val.(type) {
		case float64:
			// Convert whole-number floats to int (e.g., 5.0 → 5)
			if v == float64(int(v)) {
				data[key] = int(v)
			}
		case map[string]interface{}:
			coerceWorkflowTypes(v)
		case []interface{}:
			for i, item := range v {
				switch itemVal := item.(type) {
				case float64:
					if itemVal == float64(int(itemVal)) {
						v[i] = int(itemVal)
					}
				case map[string]interface{}:
					coerceWorkflowTypes(itemVal)
				}
			}
		}
	}
}

// ==================== AGENTIC TOOL-BASED BUILD/FIX ====================

// workflowToolDef describes a tool available in the agentic tool loop.
type workflowToolDef struct {
	Name        string
	Description string
	Params      string // Human-readable parameter description
}

// getWorkflowToolDescriptions returns the tool descriptions formatted for the system prompt.
func getWorkflowToolDescriptions() string {
	tools := []workflowToolDef{
		{
			Name:        "init_workflow",
			Description: "Initialize a new automation with name, triggers, and optional inputs. Call this first when creating a new automation.",
			Params:      `{"name": "string", "triggers": [{"type": "manual"}], "inputs": [{"id": "ns", "type": "string", "default": "nudgebee"}]}`,
		},
		{
			Name:        "add_task",
			Description: "Add a task to the automation. Provide the full task object with id, type, params, depends_on, and optional if condition.",
			Params:      `{"id": "task-id", "type": "task.type", "params": {...}, "depends_on": ["other-task"], "if": "{{ condition }}"}`,
		},
		{
			Name:        "get_task",
			Description: "Read a specific task's full definition by its ID. Use this to inspect a task before modifying it.",
			Params:      `{"task_id": "task-id"}`,
		},
		{
			Name:        "modify_task",
			Description: "Replace a task's definition. Provide the task_id and the complete updated task object.",
			Params:      `{"task_id": "task-id", "id": "task-id", "type": "task.type", "params": {...}}`,
		},
		{
			Name:        "delete_task",
			Description: "Remove a task from the automation by its ID.",
			Params:      `{"task_id": "task-id"}`,
		},
		{
			Name:        "list_tasks",
			Description: "List all tasks in the automation showing their id, type, depends_on, and if conditions. Use this to understand automation structure.",
			Params:      `{}`,
		},
		{
			Name:        "get_task_schema",
			Description: "Get the parameter schema for a specific task type. Call this BEFORE adding a task to understand its required and optional parameters.",
			Params:      `{"task_type": "notifications.im"}`,
		},
		{
			Name:        "validate",
			Description: "Validate the current automation against the server's validation API. Returns 'OK' or error details. Call this after adding all tasks.",
			Params:      `{}`,
		},
		{
			Name:        "finalize",
			Description: "Finalize and return the complete automation JSON. Call this only after validate returns OK.",
			Params:      `{}`,
		},
		{
			Name:        "list_executions",
			Description: "Fix mode only. List recent runs of the current automation. Use status='FAILED' to find failed runs first. Returns a list of {id, status, start_time, trigger_type}.",
			Params:      `{"status": "FAILED", "limit": "10"}`,
		},
		{
			Name:        "get_execution",
			Description: "Fix mode only. Read the full event log for a specific run, including the workflow-level error, per-task errors, rendered_params, and outputs. Call this BEFORE diagnosing a failure to ground your reasoning in real evidence.",
			Params:      `{"execution_id": "execution-uuid"}`,
		},
	}

	var sb strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&sb, "- **%s**: %s\n  Params: %s\n\n", t.Name, t.Description, t.Params)
	}
	return sb.String()
}

// toolInitWorkflow initializes the working workflow with name, triggers, and inputs.
func (a *WorkflowBuilderAgent) toolInitWorkflow(args map[string]interface{}) string {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: 'name' is required"
	}

	definition := map[string]interface{}{
		"version":  "v1",
		"triggers": []interface{}{},
		"tasks":    []interface{}{},
	}

	if triggers, ok := args["triggers"].([]interface{}); ok && len(triggers) > 0 {
		definition["triggers"] = triggers
	} else {
		definition["triggers"] = []interface{}{map[string]interface{}{"type": "manual"}}
	}

	if inputs, ok := args["inputs"].([]interface{}); ok && len(inputs) > 0 {
		definition["inputs"] = inputs
	}

	a.state.WorkingWorkflow = map[string]interface{}{
		"name":       name,
		"definition": definition,
	}

	triggerCount := 0
	if triggers, ok := definition["triggers"].([]interface{}); ok {
		triggerCount = len(triggers)
	}
	return fmt.Sprintf("Automation '%s' initialized with %d trigger(s).", name, triggerCount)
}

// toolAddTask adds a task to the working workflow.
func (a *WorkflowBuilderAgent) toolAddTask(args map[string]interface{}) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized. Call init_workflow first."
	}

	taskId, _ := args["id"].(string)
	if taskId == "" {
		return "Error: task 'id' is required"
	}
	taskType, _ := args["type"].(string)
	if taskType == "" {
		return "Error: task 'type' is required"
	}

	definition, ok := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if !ok {
		return "Error: automation definition is invalid"
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok {
		tasks = []interface{}{}
	}

	// Check for duplicate task ID
	for _, t := range tasks {
		if task, ok := t.(map[string]interface{}); ok {
			if id, ok := task["id"].(string); ok && id == taskId {
				return fmt.Sprintf("Error: task '%s' already exists. Use modify_task to update it.", taskId)
			}
		}
	}

	// Build the task object from args (keep all fields provided)
	task := map[string]interface{}{}
	for k, v := range args {
		task[k] = v
	}

	tasks = append(tasks, task)
	definition["tasks"] = tasks
	a.state.WorkingWorkflow["definition"] = definition

	result := fmt.Sprintf("Task '%s' (type: %s) added successfully. Automation now has %d task(s).", taskId, taskType, len(tasks))

	// Emit smart warnings for common mistakes
	result += a.validateTaskHints(taskType, args, a.cachedTaskTypes)

	return result
}

// toolGetTask returns a specific task's full definition.
func (a *WorkflowBuilderAgent) toolGetTask(args map[string]interface{}) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	taskId, _ := args["task_id"].(string)
	if taskId == "" {
		return "Error: 'task_id' is required"
	}

	definition, ok := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if !ok {
		return "Error: automation definition is invalid"
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok {
		return "Error: no tasks in automation"
	}

	for _, t := range tasks {
		if task, ok := t.(map[string]interface{}); ok {
			if id, ok := task["id"].(string); ok && id == taskId {
				taskJSON, err := json.MarshalIndent(task, "", "  ")
				if err != nil {
					return fmt.Sprintf("Error marshaling task: %v", err)
				}
				return string(taskJSON)
			}
		}
	}

	return fmt.Sprintf("Error: task '%s' not found", taskId)
}

// toolModifyTask replaces a task's definition in the working workflow.
func (a *WorkflowBuilderAgent) toolModifyTask(args map[string]interface{}) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	taskId, _ := args["task_id"].(string)
	if taskId == "" {
		return "Error: 'task_id' is required"
	}

	definition, ok := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if !ok {
		return "Error: automation definition is invalid"
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok {
		return "Error: no tasks in automation"
	}

	// Build the new task object (exclude task_id meta-field)
	newTask := map[string]interface{}{}
	for k, v := range args {
		if k != "task_id" {
			newTask[k] = v
		}
	}
	// Ensure the id field is set
	if _, hasId := newTask["id"]; !hasId {
		newTask["id"] = taskId
	}

	found := false
	for i, t := range tasks {
		if task, ok := t.(map[string]interface{}); ok {
			if id, ok := task["id"].(string); ok && id == taskId {
				tasks[i] = newTask
				found = true
				break
			}
		}
	}

	if !found {
		return fmt.Sprintf("Error: task '%s' not found", taskId)
	}

	definition["tasks"] = tasks
	a.state.WorkingWorkflow["definition"] = definition

	taskType, _ := newTask["type"].(string)
	result := fmt.Sprintf("Task '%s' updated successfully.", taskId)
	result += a.validateTaskHints(taskType, newTask, a.cachedTaskTypes)

	return result
}

// knownTemplateFilters is the set of valid Gonja template filter names.
// Populated once from the runbook server's template docs endpoint.
// Falls back to a minimal static set if the endpoint is unavailable.
var knownTemplateFilters = map[string]string{
	// Common wrong names → correct names (used for suggestions)
	"tojson":   "to_json",
	"fromjson": "from_json",
	"toyaml":   "to_yaml",
	"fromyaml": "from_yaml",
}

// validateTaskHints performs schema-driven validation on a task definition and returns warnings.
// It uses the cached task type registry and the current workflow state to cross-reference
// output field names, filter names, and common structural issues.
func (a *WorkflowBuilderAgent) validateTaskHints(taskType string, args map[string]interface{}, cachedTaskTypes string) string {
	var warnings strings.Builder

	// Serialize the full task for generic checks
	argsJSON, _ := json.Marshal(args)
	argsStr := string(argsJSON)

	// 1. Schema-driven: validate output field references against the task registry
	// Extract all {{ Tasks['xxx'].output.YYY }} references from the task definition
	outputRefs := extractOutputReferences(argsStr)
	if len(outputRefs) > 0 && cachedTaskTypes != "" {
		taskSchemas := parseTaskSchemas(cachedTaskTypes)
		// Also build a map of task-id → task-type from the current workflow
		taskTypeMap := a.getTaskTypeMap()

		for _, ref := range outputRefs {
			// ref = {taskId: "parse-issues", field: "json"}
			referencedType, typeKnown := taskTypeMap[ref.taskID]
			if !typeKnown {
				continue // task not yet added, can't validate
			}
			schema, schemaKnown := taskSchemas[referencedType]
			if !schemaKnown {
				continue
			}
			// Check if the referenced output field exists in the schema
			if _, fieldValid := schema.outputFields[ref.field]; !fieldValid && len(schema.outputFields) > 0 {
				validFields := make([]string, 0, len(schema.outputFields))
				for f := range schema.outputFields {
					validFields = append(validFields, f)
				}
				sort.Strings(validFields)
				fmt.Fprintf(&warnings,
					"\n⚠️ WARNING: References Tasks['%s'].output.%s but task type '%s' does not have output field '%s'. Valid output fields: %s. Call get_task_schema for '%s' to verify.",
					ref.taskID, ref.field, referencedType, ref.field, strings.Join(validFields, ", "), referencedType)
			}
		}
	}

	// 2. Filter name validation: detect common Jinja2 filter names that don't exist in Gonja
	for wrongName, correctName := range knownTemplateFilters {
		// Match "| wrongName" or "|wrongName" (with optional spaces)
		if strings.Contains(argsStr, "| "+wrongName) || strings.Contains(argsStr, "|"+wrongName) {
			fmt.Fprintf(&warnings,
				"\n⚠️ WARNING: Uses filter '%s' which does not exist. The correct filter name is '%s' (with underscore).",
				wrongName, correctName)
		}
	}

	// 3. Structural checks that apply to specific task types based on their input_schema semantics
	params, _ := args["params"].(map[string]interface{})
	if params != nil {
		// scripting.run_script: check parser_type and language
		if taskType == "scripting.run_script" {
			script, _ := params["script"].(string)
			parserType, _ := params["parser_type"].(string)
			language, _ := params["language"].(string)

			if parserType != "json" && containsAny(script, "json.dumps", "JSON.stringify", "json.Marshal") {
				warnings.WriteString("\n⚠️ WARNING: Script outputs JSON but parser_type is not 'json'. Without it, output.data is a raw string. Downstream tasks expecting structured data will fail.")
			}
			if language == "" && containsAny(script, "import ", "def ", "print(") {
				warnings.WriteString("\n⚠️ WARNING: Script looks like Python but no 'language' parameter is set (defaults to 'bash'). Set language: 'python'.")
			}
			if strings.Contains(script, "{{") && strings.Contains(script, "}}") {
				env, _ := params["env"].(map[string]interface{})
				if len(env) == 0 {
					warnings.WriteString("\n⚠️ WARNING: Script contains {{ }} template expressions without 'env' parameter. Use env to pass data safely instead of inline templates.")
				}
			}
		}

		// core.foreach: check item param consistency
		if taskType == "core.foreach" {
			itemParam, _ := params["item"].(string)
			if itemParam == "" {
				itemParam = "item" // default
			}
			// Scan subtask templates for mismatched item variable names
			if subtasks, ok := params["tasks"].([]interface{}); ok {
				subtasksJSON, _ := json.Marshal(subtasks)
				subtasksStr := string(subtasksJSON)
				// Generate case variants the LLM might confuse
				titleCase := strings.ToUpper(itemParam[:1]) + itemParam[1:] // "item" → "Item", "issue" → "Issue"
				possibleWrongNames := []string{
					titleCase,
					strings.ToUpper(itemParam), // "ITEM", "ISSUE"
				}
				for _, wrongName := range possibleWrongNames {
					if wrongName != itemParam && strings.Contains(subtasksStr, "{{ "+wrongName+".") {
						fmt.Fprintf(&warnings,
							"\n⚠️ WARNING: Subtask templates use '{{ %s.* }}' but the item parameter is '%s'. Variable names are case-sensitive. Change references to '{{ %s.* }}' or set item: \"%s\".",
							wrongName, itemParam, itemParam, wrongName)
						break
					}
				}
			}
		}
	}

	return warnings.String()
}

// outputRef represents a parsed {{ Tasks['taskId'].output.field }} reference.
type outputRef struct {
	taskID string
	field  string
}

// extractOutputReferences parses all {{ Tasks['xxx'].output.YYY }} patterns from a string.
func extractOutputReferences(s string) []outputRef {
	matches := outputRefRegex.FindAllStringSubmatch(s, -1)
	seen := map[string]bool{}
	var refs []outputRef
	for _, m := range matches {
		key := m[1] + "." + m[2]
		if !seen[key] {
			seen[key] = true
			refs = append(refs, outputRef{taskID: m[1], field: m[2]})
		}
	}
	return refs
}

// taskSchemaInfo holds parsed schema information for a task type.
type taskSchemaInfo struct {
	outputFields map[string]bool // set of valid output field names
}

// parseTaskSchemas parses the cached task types JSON into a map of type → schema info.
// The JSON format is {"tasks": [{"name": "type.name", "output_schema": {...}, ...}]}
// or a bare array of task objects.
func parseTaskSchemas(cachedTaskTypes string) map[string]taskSchemaInfo {
	result := map[string]taskSchemaInfo{}

	// Try wrapped format first: {"tasks": [...]}
	var wrapped struct {
		Tasks []map[string]interface{} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(cachedTaskTypes), &wrapped); err == nil && len(wrapped.Tasks) > 0 {
		for _, t := range wrapped.Tasks {
			addTaskSchema(result, t)
		}
		return result
	}

	// Fall back to bare array
	var allTypes []map[string]interface{}
	if err := json.Unmarshal([]byte(cachedTaskTypes), &allTypes); err == nil {
		for _, t := range allTypes {
			addTaskSchema(result, t)
		}
	}
	return result
}

func addTaskSchema(result map[string]taskSchemaInfo, t map[string]interface{}) {
	typeName, _ := t["name"].(string)
	if typeName == "" {
		return
	}
	info := taskSchemaInfo{outputFields: map[string]bool{}}
	if outputSchema, ok := t["output_schema"].(map[string]interface{}); ok {
		for fieldName := range outputSchema {
			info.outputFields[fieldName] = true
		}
	}
	result[typeName] = info
}

// getTaskTypeMap builds a map of task-id → task-type from the current working workflow.
func (a *WorkflowBuilderAgent) getTaskTypeMap() map[string]string {
	result := map[string]string{}
	if a.state.WorkingWorkflow == nil {
		return result
	}
	definition, _ := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if definition == nil {
		return result
	}
	tasks, _ := definition["tasks"].([]interface{})
	for _, t := range tasks {
		if task, ok := t.(map[string]interface{}); ok {
			id, _ := task["id"].(string)
			taskType, _ := task["type"].(string)
			if id != "" && taskType != "" {
				result[id] = taskType
			}
		}
	}
	return result
}

// containsAny returns true if s contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// toolDeleteTask removes a task from the working workflow.
func (a *WorkflowBuilderAgent) toolDeleteTask(args map[string]interface{}) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	taskId, _ := args["task_id"].(string)
	if taskId == "" {
		return "Error: 'task_id' is required"
	}

	definition, ok := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if !ok {
		return "Error: automation definition is invalid"
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok {
		return "Error: no tasks in automation"
	}

	newTasks := make([]interface{}, 0, len(tasks))
	found := false
	for _, t := range tasks {
		if task, ok := t.(map[string]interface{}); ok {
			if id, ok := task["id"].(string); ok && id == taskId {
				found = true
				continue
			}
		}
		newTasks = append(newTasks, t)
	}

	if !found {
		return fmt.Sprintf("Error: task '%s' not found", taskId)
	}

	definition["tasks"] = newTasks
	a.state.WorkingWorkflow["definition"] = definition

	return fmt.Sprintf("Task '%s' deleted. Automation now has %d task(s).", taskId, len(newTasks))
}

// toolListTasks returns a compact summary of all tasks in the workflow.
func (a *WorkflowBuilderAgent) toolListTasks(args map[string]interface{}) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	definition, ok := a.state.WorkingWorkflow["definition"].(map[string]interface{})
	if !ok {
		return "Error: automation definition is invalid"
	}

	tasks, ok := definition["tasks"].([]interface{})
	if !ok || len(tasks) == 0 {
		return "No tasks in automation."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Automation: %v\n", a.state.WorkingWorkflow["name"])
	fmt.Fprintf(&sb, "Tasks (%d):\n", len(tasks))

	for _, t := range tasks {
		task, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := task["id"].(string)
		taskType, _ := task["type"].(string)
		line := fmt.Sprintf("  - %s [%s]", id, taskType)

		if deps, ok := task["depends_on"].([]interface{}); ok && len(deps) > 0 {
			depStrs := make([]string, 0, len(deps))
			for _, d := range deps {
				if ds, ok := d.(string); ok {
					depStrs = append(depStrs, ds)
				}
			}
			line += fmt.Sprintf(" depends_on:[%s]", strings.Join(depStrs, ", "))
		}
		if ifCond, ok := task["if"].(string); ok && ifCond != "" {
			line += fmt.Sprintf(" if: %s", ifCond)
		}
		sb.WriteString(line + "\n")
	}

	return sb.String()
}

// toolGetTaskSchema returns the schema for a specific task type from the cached task types.
func (a *WorkflowBuilderAgent) toolGetTaskSchema(args map[string]interface{}, cachedTaskTypes string) string {
	taskType, _ := args["task_type"].(string)
	if taskType == "" {
		return "Error: 'task_type' is required"
	}

	if cachedTaskTypes == "" {
		return fmt.Sprintf("Task types not available. Use task type '%s' with your best knowledge of its parameters.", taskType)
	}

	// findInList searches a slice of task objects for the matching task type.
	// Checks both "name" and "type" keys since the API uses "name" but legacy code checked "type".
	findInList := func(list []map[string]interface{}) string {
		for _, t := range list {
			name, _ := t["name"].(string)
			if name == "" {
				name, _ = t["type"].(string) // fallback to "type" key
			}
			if name == taskType {
				result, _ := json.MarshalIndent(t, "", "  ")
				return string(result)
			}
		}
		return ""
	}

	// Try wrapped format first: {"tasks": [...]}
	var wrapped struct {
		Tasks []map[string]interface{} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(cachedTaskTypes), &wrapped); err == nil && len(wrapped.Tasks) > 0 {
		if result := findInList(wrapped.Tasks); result != "" {
			return result
		}
		return fmt.Sprintf("Task type '%s' not found in available types. Check the type name and try again.", taskType)
	}

	// Try bare array
	var allTypes []map[string]interface{}
	if err := json.Unmarshal([]byte(cachedTaskTypes), &allTypes); err == nil {
		if result := findInList(allTypes); result != "" {
			return result
		}
	}

	return fmt.Sprintf("Task type '%s' not found in available types. Check the type name and try again.", taskType)
}

// toolValidate validates the current working workflow via the runbook-server API.
func (a *WorkflowBuilderAgent) toolValidate(ctx *security.RequestContext) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	// Apply type coercion before validation
	coerceWorkflowTypes(a.state.WorkingWorkflow)

	_, err := tools.DoRunbookRequest("POST", "workflows/validate", a.state.WorkingWorkflow, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		errMsg := common.SanitizeErrorMessage(err.Error())

		// Structural errors (missing depends_on, unknown task types, bad params) are always hard failures.
		// These indicate real bugs in the workflow definition that must be fixed.
		structuralErrorPatterns := []string{
			"depends_on",
			"references Tasks[",
			"unknown type",
			"duplicate task ID",
			"circular dependency",
			"parameters validation failed",
			"non-existent task",
		}
		for _, pattern := range structuralErrorPatterns {
			if strings.Contains(errMsg, pattern) {
				return fmt.Sprintf("Validation FAILED: %s\n\nThis is a structural error that must be fixed. Use modify_task to correct the issue, then call validate again.", errMsg)
			}
		}

		// Template rendering errors for runtime values (Configs, Inputs, Tasks outputs)
		// are expected during static validation — these resolve at execution time.
		if strings.Contains(errMsg, "unable to execute template") {
			return "Validation OK (with template warnings). Some template expressions cannot be evaluated during validation because they reference runtime values (task outputs, configs, inputs). They will resolve correctly at execution time. The automation structure is valid. Proceed to finalize."
		}
		return fmt.Sprintf("Validation FAILED: %s", errMsg)
	}

	return "Validation OK. The automation is valid."
}

// toolFinalize returns the complete workflow JSON.
func (a *WorkflowBuilderAgent) toolFinalize() string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}

	// Apply type coercion before finalizing
	coerceWorkflowTypes(a.state.WorkingWorkflow)

	result, err := json.MarshalIndent(a.state.WorkingWorkflow, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling automation: %v", err)
	}
	return string(result)
}

// toolListExecutions lists recent runs of the current workflow. Fix mode only —
// requires a.state.WorkflowId to be set by handleFixEntry.
func (a *WorkflowBuilderAgent) toolListExecutions(ctx *security.RequestContext, args map[string]interface{}) string {
	if a.state.WorkflowId == "" {
		return "Error: no workflow id in context. This tool is only usable when debugging an existing automation."
	}

	queryParams := []string{}
	if val, ok := args["status"].(string); ok && val != "" {
		queryParams = append(queryParams, fmt.Sprintf("status=%s", val))
	}
	limit := "10"
	if val, ok := args["limit"]; ok {
		limit = fmt.Sprintf("%v", val)
	}
	queryParams = append(queryParams, fmt.Sprintf("limit=%s", limit))

	path := fmt.Sprintf("workflows/%s/runs?%s", a.state.WorkflowId, strings.Join(queryParams, "&"))
	resp, err := tools.DoRunbookRequest("GET", path, nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return fmt.Sprintf("Error listing executions: %v", err)
	}
	return string(resp)
}

// toolGetExecution returns the detailed event log for a specific run. Fix mode only.
// Large string fields are truncated to keep the LLM context manageable.
func (a *WorkflowBuilderAgent) toolGetExecution(ctx *security.RequestContext, args map[string]interface{}) string {
	if a.state.WorkflowId == "" {
		return "Error: no workflow id in context. This tool is only usable when debugging an existing automation."
	}
	executionId, _ := args["execution_id"].(string)
	if executionId == "" {
		return "Error: 'execution_id' is required"
	}

	path := fmt.Sprintf("workflows/%s/runs/%s", a.state.WorkflowId, executionId)
	resp, err := tools.DoRunbookRequest("GET", path, nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return fmt.Sprintf("Error fetching execution %s: %v", executionId, err)
	}
	return compactExecutionJSON(resp, 4096)
}

// compactExecutionJSON truncates oversized string fields in an execution detail blob
// (e.g. multi-MB stdout) to keep the LLM context manageable. Returns raw bytes if
// the response isn't valid JSON.
func compactExecutionJSON(raw []byte, maxStringBytes int) string {
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(truncateStringsInValue(data, maxStringBytes), "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func truncateStringsInValue(v any, maxBytes int) any {
	switch val := v.(type) {
	case string:
		if len(val) > maxBytes {
			return core.TruncateMiddle(val, maxBytes/2, maxBytes/2)
		}
		return val
	case map[string]any:
		for k, vv := range val {
			val[k] = truncateStringsInValue(vv, maxBytes)
		}
		return val
	case []any:
		for i, vv := range val {
			val[i] = truncateStringsInValue(vv, maxBytes)
		}
		return val
	default:
		return val
	}
}

// executeWorkflowTool dispatches a tool call to the appropriate handler.
func (a *WorkflowBuilderAgent) executeWorkflowTool(ctx *security.RequestContext, toolName string, toolInput string, cachedTaskTypes string) string {
	// Parse tool input as JSON
	var args map[string]interface{}
	if toolInput != "" {
		if err := json.Unmarshal([]byte(toolInput), &args); err != nil {
			// Try to handle non-JSON inputs
			args = map[string]interface{}{"input": toolInput}
		}
	}
	if args == nil {
		args = map[string]interface{}{}
	}

	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "init_workflow":
		return a.toolInitWorkflow(args)
	case "add_task":
		return a.toolAddTask(args)
	case "get_task":
		return a.toolGetTask(args)
	case "modify_task":
		return a.toolModifyTask(args)
	case "delete_task":
		return a.toolDeleteTask(args)
	case "list_tasks":
		return a.toolListTasks(args)
	case "get_task_schema":
		return a.toolGetTaskSchema(args, cachedTaskTypes)
	case "validate":
		return a.toolValidate(ctx)
	case "finalize":
		return a.toolFinalize()
	case "list_executions":
		return a.toolListExecutions(ctx, args)
	case "get_execution":
		return a.toolGetExecution(ctx, args)
	default:
		return fmt.Sprintf("Unknown tool: '%s'. Available tools: init_workflow, add_task, get_task, modify_task, delete_task, list_tasks, get_task_schema, validate, finalize, list_executions, get_execution", toolName)
	}
}

// runToolLoop runs an agentic tool-calling loop where the LLM reasons step-by-step
// and calls workflow manipulation tools. This follows the same XML format as the ReAct planner.
func (a *WorkflowBuilderAgent) runToolLoop(ctx *security.RequestContext, request core.NBAgentRequest, systemPrompt string, userMessage string) (string, error) {
	// Fetch task types once for all get_task_schema calls and schema-driven validation
	tasksResp, _ := tools.DoRunbookRequest("GET", "tasks", nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	cachedTaskTypes := string(tasksResp)
	a.cachedTaskTypes = cachedTaskTypes

	toolDescriptions := getWorkflowToolDescriptions()

	fullSystemPrompt := fmt.Sprintf(`%s

AVAILABLE TOOLS:
%s

RESPONSE FORMAT:
When you need to use a tool, respond with:
<thought_action>
<thought>Your reasoning about what to do next</thought>
<action>
    <tool_name>tool_name_here</tool_name>
    <tool_input>{"param": "value"}</tool_input>
</action>
</thought_action>

When you have the final workflow JSON ready, respond with:
<final_answer>
<content>The complete workflow JSON here</content>
</final_answer>

RULES:
- Call ONE tool at a time, then wait for the observation.
- Do NOT generate <observation> tags — I will provide them.
- Do NOT output both <thought_action> and <final_answer> in the same response.
- Call validate before finalize.
- Call finalize to return the completed workflow — put the JSON inside <content>.`, systemPrompt, toolDescriptions)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, fullSystemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, userMessage),
	}

	const maxIterations = 20
	for i := 0; i < maxIterations; i++ {
		// Use </thought_action> and </final_answer> as stop words so the LLM stops
		// after producing exactly one tool call or one final answer. The previous stop
		// word "<observation>" was never generated by the LLM (it's injected by code),
		// causing the model to repeat the same tool call hundreds of times until hitting
		// the max output token limit.
		// Cap max tokens at 8192 — a single tool call or final answer never needs more.
		result, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messages, false, llms.WithTemperature(0.0), llms.WithStopWords([]string{"</thought_action>", "</final_answer>"}), llms.WithMaxTokens(8192))
		if err != nil {
			return "", fmt.Errorf("tool loop LLM call failed (iteration %d): %w", i+1, err)
		}

		if len(result.Choices) == 0 || result.Choices[0].Content == "" {
			ctx.GetLogger().Warn("workflow_builder: empty LLM response in tool loop", "iteration", i+1)
			// Retry with a nudge — use non-empty AI text to avoid Google AI CountTokens
			// crash on empty model parts.
			messages = append(messages,
				llms.TextParts(llms.ChatMessageTypeAI, "I need to continue processing."),
				llms.TextParts(llms.ChatMessageTypeHuman, "Please continue. Use a tool or provide your final answer."),
			)
			continue
		}

		content := result.Choices[0].Content

		// Re-append closing tags consumed by stop words so XML parsing works.
		if strings.Contains(content, "<thought_action>") && !strings.Contains(content, "</thought_action>") {
			content += "</thought_action>"
		}
		if strings.Contains(content, "<final_answer>") && !strings.Contains(content, "</final_answer>") {
			content += "</final_answer>"
		}

		// Check for final answer
		finalContent := common.XmlExtractTagContent(content, "content")
		if finalContent == "" {
			finalContent = common.XmlExtractTagContent(content, "final_answer")
		}
		if finalContent != "" && strings.Contains(content, "<final_answer>") {
			// Clean up markdown code blocks if present
			finalContent = strings.TrimPrefix(finalContent, "```json")
			finalContent = strings.TrimPrefix(finalContent, "```")
			finalContent = strings.TrimSuffix(finalContent, "```")
			finalContent = strings.TrimSpace(finalContent)
			return finalContent, nil
		}

		// Parse tool call
		toolName := common.XmlExtractTagContent(content, "tool_name")
		toolInput := common.XmlExtractTagContent(content, "tool_input")

		if toolName == "" {
			// No tool call and no final answer — check if content looks like raw JSON (workflow)
			trimmed := strings.TrimSpace(content)
			if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\"definition\"") {
				return trimmed, nil
			}

			// Nudge the LLM to use a tool or finish
			messages = append(messages,
				llms.TextParts(llms.ChatMessageTypeAI, content),
				llms.TextParts(llms.ChatMessageTypeHuman, "Please use a tool (init_workflow, add_task, get_task_schema, validate, finalize, etc.) or provide your <final_answer>."),
			)
			continue
		}

		// Execute the tool
		ctx.GetLogger().Info("workflow_builder: tool call", "tool", toolName, "iteration", i+1)
		observation := a.executeWorkflowTool(ctx, toolName, toolInput, cachedTaskTypes)

		// Trim content to just the tool call XML before appending to message history.
		// This prevents context bloat from any extra content the LLM may have generated.
		trimmedContent := content
		if idx := strings.Index(content, "</thought_action>"); idx >= 0 {
			trimmedContent = content[:idx+len("</thought_action>")]
		}

		// Append assistant response + observation to message history
		messages = append(messages,
			llms.TextParts(llms.ChatMessageTypeAI, trimmedContent),
			llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("<observation>%s</observation>", observation)),
		)
	}

	// If we exhausted iterations, try to finalize whatever we have
	if a.state.WorkingWorkflow != nil {
		finalJSON := a.toolFinalize()
		if strings.HasPrefix(strings.TrimSpace(finalJSON), "{") {
			return finalJSON, nil
		}
	}

	return "", fmt.Errorf("agentic tool loop exceeded %d iterations without completing", maxIterations)
}

// ==================== FIX MODE (AGENTIC) ====================

// handleFixEntry fetches the existing workflow, loads it into workingWorkflow, and uses the
// agentic tool loop to diagnose and fix the issue.
func (a *WorkflowBuilderAgent) handleFixEntry(ctx *security.RequestContext, request core.NBAgentRequest, workflowId string) (core.NBAgentResponse, error) {
	// Fetch existing workflow definition
	workflowResp, err := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows/%s", workflowId), nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("failed to fetch workflow %s: %w", workflowId, err)
	}

	// Load into workingWorkflow
	var workflow map[string]interface{}
	if err := json.Unmarshal(workflowResp, &workflow); err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("failed to parse workflow JSON: %w", err)
	}
	a.state.WorkingWorkflow = workflow
	a.state.WorkflowId = workflowId
	a.state.Mode = "fix"
	a.state.OriginalQuery = request.Query

	// Error context can come from QueryContext (string) or via a specific execution_id
	// the agent will fetch itself in the diagnosis loop.
	errorContext := request.QueryContext
	targetExecutionId := request.QueryConfig.ExecutionId
	a.state.ExecutionId = targetExecutionId

	// If followup is not enabled, apply fix directly via tool loop
	if !core.IsAgentsFollowupEnabled() {
		schema := getWorkflowSchema()
		systemPrompt := getFixSystemPrompt(errorContext, schema)
		workflowJSON, err := a.runToolLoop(ctx, request, systemPrompt, fmt.Sprintf("Fix this automation. User request: %s", request.Query))
		if err != nil {
			return core.NBAgentResponse{}, fmt.Errorf("agentic fix failed: %w", err)
		}
		return core.NBAgentResponse{Response: []string{workflowJSON}, IsTerminal: true}, nil
	}

	// With followup enabled: do an evidence-grounded diagnosis first, then ask for approval
	diagnosisPrompt := buildFixDiagnosisPrompt(request.Query, errorContext, targetExecutionId)

	diagnosisResult, err := a.runToolLoop(ctx, request, diagnosisPrompt, "Diagnose the automation issue.")
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("diagnosis failed: %w", err)
	}

	agentId := uuid.Nil
	if request.AgentId != "" {
		agentId = uuid.MustParse(request.AgentId)
	}

	// If the agent couldn't find actionable evidence, surface as a free-text followup
	// instead of presenting Apply / Modify / Discard against a guess.
	if question, isAsk := parseDiagnosisForFollowup(diagnosisResult); isAsk {
		a.state.Stage = "fix_feedback"
		a.state.ExistingDefinition = string(workflowResp)
		a.state.ExecutionError = errorContext
		a.state.ProposedDiff = ""
		return core.NBAgentResponse{
			Status: core.ConversationStatusWaiting,
			FollowupRequest: core.FollowupRequest{
				Question:     question,
				FollowupType: core.FollowupTypeText,
				AgentName:    a.GetName(),
				AgentId:      agentId,
			},
		}, nil
	}

	// Store state for resume
	a.state.Stage = "fix_approval"
	a.state.ExistingDefinition = string(workflowResp)
	a.state.ExecutionError = errorContext
	a.state.ProposedDiff = diagnosisResult

	responseText := fmt.Sprintf("**Diagnosis:**\n\n%s\n\nWould you like to apply these changes?", diagnosisResult)

	resp := core.NBAgentResponse{
		Status: core.ConversationStatusWaiting,
		FollowupRequest: core.FollowupRequest{
			Question:        responseText,
			FollowupType:    core.FollowupTypeSingleSelect,
			FollowupOptions: []string{FixApprovalOptionApply, FixApprovalOptionModify, FixApprovalOptionDiscard},
			AgentName:       a.GetName(),
			AgentId:         agentId,
		},
	}
	return resp, nil
}

// parseDiagnosisForFollowup recognises the [NEEDS_MORE_INFO] sentinel the LLM emits
// when it cannot find actionable evidence and needs to ask the user. Returns the
// remaining question text and true if the marker is present.
func parseDiagnosisForFollowup(result string) (string, bool) {
	trimmed := strings.TrimSpace(result)
	if !strings.HasPrefix(trimmed, fixNeedsMoreInfoMarker) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, fixNeedsMoreInfoMarker)), true
}

// buildFixDiagnosisPrompt constructs the diagnosis system prompt for a fresh fix
// request. It REQUIRES the LLM to fetch real execution evidence before theorizing,
// and instructs it to emit [NEEDS_MORE_INFO] when no actionable evidence is found.
func buildFixDiagnosisPrompt(userQuery, errorContext, targetExecutionId string) string {
	var targetSection string
	if targetExecutionId != "" {
		targetSection = fmt.Sprintf(`
TARGET EXECUTION ID: %s
The user has pointed at a specific run. Skip list_executions and call:
    get_execution(execution_id="%s")
`, targetExecutionId, targetExecutionId)
	}

	var errorSection string
	if strings.TrimSpace(errorContext) != "" {
		errorSection = fmt.Sprintf(`
ERROR CONTEXT (provided by user / UI):
%s
`, errorContext)
	}

	return fmt.Sprintf(`You are a Nudgebee automation debugger. Diagnose the user's failure with EVIDENCE, not speculation.

USER REQUEST:
%s
%s%s
INSTRUCTIONS (evidence-first — do these in order):

1. FIND THE FAILED RUN.
   - If a TARGET EXECUTION ID is given above, call get_execution on it directly.
   - Otherwise, call list_executions(status="FAILED", limit=10). Pick the most recent failed run.

2. READ THE ACTUAL ERROR.
   - Call get_execution(execution_id) on the chosen run.
   - Examine: workflow-level "error", per-task "error" / "status", and the failing task's "rendered_params" + "output".

3. INSPECT THE RELEVANT TASKS.
   - Call list_tasks to see the automation structure.
   - Call get_task on the failing task and any upstream dependencies it consumes.

4. DIAGNOSE WITH CITATIONS.
   - Quote the actual error string in your diagnosis.
   - Identify the failing task by id.
   - Propose a minimal change that addresses the error you observed.
   - DO NOT speculate about issues unrelated to the error you read.

5. IF YOU CANNOT FIND ACTIONABLE EVIDENCE
   (no failed runs found, error is opaque/generic, or multiple failures with different errors)
   emit a <final_answer> whose <content> begins on the first line with the literal marker:
       %s
   followed by a specific question for the user. Examples:
   - "%s\nNo failed runs found in the last 10 runs. What behavior are you trying to debug?"
   - "%s\nFound 3 failed runs with different errors. Which one should I focus on? IDs: <id1>, <id2>, <id3>"

6. WHEN YOU HAVE EVIDENCE, emit a <final_answer> with your diagnosis (no marker), describing:
   - What the problem is (with the quoted error)
   - Which task(s) need to change
   - What specific changes you propose

Do NOT apply changes — just diagnose. Do NOT theorize without evidence.`,
		userQuery, errorSection, targetSection,
		fixNeedsMoreInfoMarker, fixNeedsMoreInfoMarker, fixNeedsMoreInfoMarker)
}

// handleFixApproval processes the user's response to the fix approval prompt.
// Uses the agentic tool loop to apply approved fixes.
func (a *WorkflowBuilderAgent) handleFixApproval(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	userChoice := strings.TrimSpace(request.Query)

	switch {
	case strings.EqualFold(userChoice, FixApprovalOptionApply):
		// Reload workflow into workingWorkflow from stored state
		var workflow map[string]interface{}
		if err := json.Unmarshal([]byte(a.state.ExistingDefinition), &workflow); err != nil {
			return core.NBAgentResponse{}, fmt.Errorf("failed to parse stored workflow: %w", err)
		}
		a.state.WorkingWorkflow = workflow

		// Run the full fix tool loop with the diagnosis as context
		schema := getWorkflowSchema()
		systemPrompt := getFixSystemPrompt(a.state.ExecutionError, schema)
		userMessage := fmt.Sprintf("Apply the following approved changes to the automation:\n\n%s\n\nOriginal request: %s", a.state.ProposedDiff, a.state.OriginalQuery)

		workflowJSON, err := a.runToolLoop(ctx, request, systemPrompt, userMessage)
		if err != nil {
			return core.NBAgentResponse{}, fmt.Errorf("agentic fix failed: %w", err)
		}

		// Run the same missing-config detection as the create path: an agentic fix
		// may introduce new {{ Configs.x }} references that don't exist on the
		// server, which would otherwise trip the workflow-server's save validation.
		return a.checkMissingConfigs(ctx, request, workflowJSON)

	case strings.EqualFold(userChoice, FixApprovalOptionDiscard):
		return core.NBAgentResponse{
			Response: []string{"Changes discarded. No modifications were made to the automation."},
			Status:   core.ConversationStatusCompleted,
		}, nil

	default:
		// Request modifications — ask for feedback
		a.state.Stage = "fix_feedback"

		agentId := uuid.Nil
		if request.AgentId != "" {
			agentId = uuid.MustParse(request.AgentId)
		}

		resp := core.NBAgentResponse{
			Status: core.ConversationStatusWaiting,
			FollowupRequest: core.FollowupRequest{
				Question:     "What modifications would you like me to make to the proposed changes?",
				FollowupType: core.FollowupTypeText,
				AgentName:    a.GetName(),
				AgentId:      agentId,
			},
		}
		return resp, nil
	}
}

// handleFixFeedback incorporates user feedback and re-diagnoses using the tool loop.
func (a *WorkflowBuilderAgent) handleFixFeedback(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	feedback := strings.TrimSpace(request.Query)

	// Reload workflow into workingWorkflow
	var workflow map[string]interface{}
	if err := json.Unmarshal([]byte(a.state.ExistingDefinition), &workflow); err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("failed to parse stored workflow: %w", err)
	}
	a.state.WorkingWorkflow = workflow

	// Re-diagnose with user feedback via the same evidence-first prompt, threading the
	// previous diagnosis (if any) and the user's clarification/feedback into the prompt.
	diagnosisPrompt := buildFixDiagnosisFeedbackPrompt(a.state.OriginalQuery, a.state.ExecutionError, a.state.ExecutionId, a.state.ProposedDiff, feedback)

	diagnosisResult, err := a.runToolLoop(ctx, request, diagnosisPrompt, fmt.Sprintf("Re-diagnose incorporating this feedback: %s", feedback))
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("re-diagnosis failed: %w", err)
	}

	agentId := uuid.Nil
	if request.AgentId != "" {
		agentId = uuid.MustParse(request.AgentId)
	}

	// The LLM may still need more info after a clarification round (e.g. user's clarification
	// pointed at a run we couldn't find). Keep the followup loop open in that case.
	if question, isAsk := parseDiagnosisForFollowup(diagnosisResult); isAsk {
		a.state.ProposedDiff = ""
		a.state.Stage = "fix_feedback"
		return core.NBAgentResponse{
			Status: core.ConversationStatusWaiting,
			FollowupRequest: core.FollowupRequest{
				Question:     question,
				FollowupType: core.FollowupTypeText,
				AgentName:    a.GetName(),
				AgentId:      agentId,
			},
		}, nil
	}

	// Update state
	a.state.ProposedDiff = diagnosisResult
	a.state.Stage = "fix_approval"

	responseText := fmt.Sprintf("**Updated Diagnosis:**\n\n%s\n\nWould you like to apply these changes?", diagnosisResult)

	resp := core.NBAgentResponse{
		Status: core.ConversationStatusWaiting,
		FollowupRequest: core.FollowupRequest{
			Question:        responseText,
			FollowupType:    core.FollowupTypeSingleSelect,
			FollowupOptions: []string{FixApprovalOptionApply, FixApprovalOptionModify, FixApprovalOptionDiscard},
			AgentName:       a.GetName(),
			AgentId:         agentId,
		},
	}
	return resp, nil
}

// buildFixDiagnosisFeedbackPrompt builds the diagnosis prompt for a re-run after the
// user provides feedback or a clarification. It reuses the evidence-first contract
// from buildFixDiagnosisPrompt, threading in the previous diagnosis (if any) and the
// new user input.
func buildFixDiagnosisFeedbackPrompt(originalQuery, errorContext, targetExecutionId, previousDiagnosis, feedback string) string {
	var prevSection string
	if strings.TrimSpace(previousDiagnosis) != "" {
		prevSection = fmt.Sprintf(`
PREVIOUS DIAGNOSIS:
%s
`, previousDiagnosis)
	}
	augmentedQuery := fmt.Sprintf(`%s

USER FEEDBACK / CLARIFICATION:
%s%s`, originalQuery, feedback, prevSection)

	return buildFixDiagnosisPrompt(augmentedQuery, errorContext, targetExecutionId)
}

// WorkflowBuilderSummarizerAgent - Extracts final JSON from multi-attempt responses
type WorkflowBuilderSummarizerAgent struct{}

func (p WorkflowBuilderSummarizerAgent) GetName() string {
	return WorkflowBuilderSummarizerToolName
}

func (a WorkflowBuilderSummarizerAgent) GetNameAliases() []string {
	return []string{"AutomationSummarizer", "WorkflowSummarizer", "workflow_builder_summarizer"}
}

func (p WorkflowBuilderSummarizerAgent) GetDescription() string {
	return "Specialized summarizer for AutomationBuilder that extracts only the final successful Nudgebee automation JSON"
}

func (l WorkflowBuilderSummarizerAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (p WorkflowBuilderSummarizerAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l WorkflowBuilderSummarizerAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l WorkflowBuilderSummarizerAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	queryContext := request.QueryContext

	var args struct {
		Query   string `json:"query"`
		Context string `json:"context"`
	}
	if err := common.UnmarshalJson([]byte(request.Query), &args); err != nil {
		args.Query = request.Query
	}
	command := strings.TrimSpace(args.Query)
	if args.Context != "" {
		queryContext = args.Context + "\n\n" + queryContext
	}

	systemPrompt := `You are the WorkflowBuilder output extractor for Nudgebee Runbook Server.

CONTEXT: The WorkflowBuilder agent generates Nudgebee workflows and validates them.

YOUR TASK: Extract ONLY the FINAL SUCCESSFUL NUDGEBEE workflow JSON.

HOW TO IDENTIFY THE CORRECT NUDGEBEE WORKFLOW:
1. Look for JSON code blocks (` + "```json ... ```" + `) in the response
2. It MUST be Nudgebee format with:
   - Top level: { "name": ..., "definition": { ... } }
   - Inside definition: "version", "triggers", "tasks"
   - Each task has: "id", "type", "params"
3. It must be the LAST valid Nudgebee workflow in the response

OUTPUT RULES:
1. Return ONLY the final Nudgebee workflow JSON in a markdown code block
2. Use proper JSON formatting with 2-space indentation
3. The output MUST start with ` + "```json" + ` and end with ` + "```" + `

EXAMPLE CORRECT OUTPUT:
` + "```json\n{\n  \"name\": \"my-workflow\",\n  \"definition\": {\n    \"version\": \"v1\",\n    \"triggers\": [{\"type\": \"manual\"}],\n    \"tasks\": [...]\n  }\n}\n```"

	// Merge queryContext and command into a single human message to avoid consecutive
	// human messages, which cause Google AI CountTokens API to reject the request.
	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, queryContext+"\n\n"+command),
	}

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false)
	if err != nil {
		ctx.GetLogger().Error("workflow_builder_summarizer: unable to generate content", "error", err)
		return core.NBAgentResponse{Response: nil}, err
	}

	content := strings.TrimSpace(completion.Choices[0].Content)
	if len(content) == 0 {
		return core.NBAgentResponse{Response: []string{request.Query}}, nil
	}

	return core.NBAgentResponse{Response: []string{content}}, nil
}
