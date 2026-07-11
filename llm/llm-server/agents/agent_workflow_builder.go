package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

var uuidRegex = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// outputRefRegex matches {{ Tasks['task-id'].output.fieldname }} patterns for schema-driven validation.
var outputRefRegex = regexp.MustCompile(`Tasks\[['"]([^'"]+)['"]\]\.output\.(\w+)`)

const (
	PlanApprovalOptionApprove = "Approve and Build"
	PlanApprovalOptionChanges = "Request Changes"
	maxPlanAttempts           = 3
)

// turnIntent is the result of classifyTurnIntent — what the user wants this turn.
type turnIntent string

const (
	// turnIntentAnswer: a question/explanation/diagnosis with NO change requested → handleReadOnly.
	turnIntentAnswer turnIntent = "ANSWER"
	// turnIntentEdit: change an EXISTING workflow — fix a failure OR add/modify/remove a feature → handleEditEntry.
	turnIntentEdit turnIntent = "EDIT"
	// turnIntentCreate: build a NEW workflow → handleIntentAndPlan.
	turnIntentCreate turnIntent = "CREATE"
)

// providerAnnotationRegex matches a parenthetical annotation that leaks the cloud
// provider into a clarifying-question option label, e.g.
// "(AWS - assuming this is a K8s cluster)" (#30885). The provider alternation is derived
// from cloudAccountConfigTypes (the single source of truth) so it stays in sync as
// providers are added — no hardcoded list to drift. The benign "(recommended)" marker
// does not start with a provider keyword, so it is preserved.
var providerAnnotationRegex = buildProviderAnnotationRegex()

func buildProviderAnnotationRegex() *regexp.Regexp {
	keys := make([]string, 0, len(cloudAccountConfigTypes))
	for k := range cloudAccountConfigTypes {
		keys = append(keys, regexp.QuoteMeta(k))
	}
	sort.Strings(keys) // deterministic alternation order
	return regexp.MustCompile(`(?i)\s*\((?:` + strings.Join(keys, "|") + `)\b[^)]*\)`)
}

// idAnnotationRegex matches a stray "id=<value>" fragment (parenthesized or not) that
// survived provider-annotation stripping, e.g. a label of the form "prod-aws id=<uuid>".
var idAnnotationRegex = regexp.MustCompile(`(?i)\s*\(?\bid\s*=\s*[^)\s]+\)?`)

// sanitizeAccountOptionLabel strips internal identifiers (provider annotations, id=
// fragments, bare UUIDs) from an LLM-generated clarifying-question option label so the
// user never sees them. The provider annotation (#30885) is the primary target; the
// id=/UUID stripping is a backstop, since the UUID is now withheld from the clarification
// context entirely (#31141) so it should not appear in the first place. It is deliberately
// surgical: a human-readable suffix like "gcp-dev - team-alpha" is left intact because it
// may be the only thing distinguishing two accounts, and "(recommended)" is preserved.
// Returns the trimmed display name; may return "" if the label was nothing but an identifier.
func sanitizeAccountOptionLabel(label string) string {
	out := providerAnnotationRegex.ReplaceAllString(label, "")
	out = idAnnotationRegex.ReplaceAllString(out, "")
	out = uuidRegex.ReplaceAllString(out, "")
	// Trim trailing separators/brackets left behind by the removals
	// (e.g. "prod-aws (" or "prod-aws - ").
	out = strings.TrimRight(out, " -,(")
	return strings.TrimSpace(out)
}

// sanitizeQuestionOptions applies sanitizeAccountOptionLabel to each option, drops any
// that collapse to empty, and dedupes labels that become identical after stripping
// (preserving first-occurrence order). Comparison is case-insensitive.
func sanitizeQuestionOptions(options []string) []string {
	seen := make(map[string]struct{}, len(options))
	out := make([]string, 0, len(options))
	for _, opt := range options {
		clean := sanitizeAccountOptionLabel(opt)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}

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
}

const WorkflowBuilderAgentName = "automation_builder"

type WorkflowBuilderAgent struct {
	accountId       string
	state           WorkflowBuilderState
	cachedTaskTypes string // Cached task type registry JSON for schema-driven validation

	// Transient per-request context (set at the top of Execute, not persisted in state): the
	// cluster/cloud-account the user is currently viewing, so prompts can default to it instead
	// of asking which account/cluster to target (#30162).
	currentCluster   string
	currentClusterId string
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

func (a *WorkflowBuilderAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

// PostProcessResponse is a pass-through. The builder produces its terminal output
// deterministically: create-mode returns the raw workflow JSON, and every other
// path returns either clean json.MarshalIndent output (toolFinalize) or a
// purpose-built markdown summary (finalizeWithAutoSave). The build path JSON must
// already be valid (checkMissingConfigs/resolveCloudAccountIds parse it), so it is
// never fenced; the read-only path returns prose that may legitimately contain a
// fenced JSON example. The old line-shape reconstruction therefore did nothing on
// the JSON path and could mangle a prose answer — pure risk. Final-JSON extraction
// must never pass through any further transformation here (#31499).
func (a *WorkflowBuilderAgent) PostProcessResponse(ctx *security.RequestContext, request core.NBAgentRequest, resp core.NBAgentResponse) core.NBAgentResponse {
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
  "status": "ACTIVE"      // AutomationStatus (optional): "ACTIVE", "INACTIVE", "PAUSED" (no DRAFT). New automations default to PAUSED.
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
- "manual" - No params required (params must be empty or omitted). User-supplied inputs available as {{ Inputs.<key> }} in tasks.
- "schedule" - Requires params: {"cron": "0 * * * *" (5-field UTC), "overlap_policy": "Skip|BufferOne|BufferAll|AllowAll|CancelOther|TerminateOther" (optional, default "Skip"), "catchup_window": Go time.ParseDuration string using units ns|us|ms|s|m|h ONLY — day/week units ("7d", "1w") are NOT supported; use hours instead ("168h" = 7 days). Compound durations are allowed ("1h30m", "90m15s"). Examples: "60s", "10m", "1h", "1h30m", "168h"; default "60s"}. Auto-injected: {{ Inputs.workflow_scheduled_time }}, {{ Inputs.workflow_execution_time }}.
- "webhook" - Requires params: {"integration_name": "string (a workflow_webhook integration name)", "secret": "string (optional)", "filter": "jinja2 (optional, must render to literal \"true\" or \"1\")"}. Filter sees {{ webhook_payload }} at root. Tasks read request body via {{ Inputs.webhook_payload }}.
- "event" - Requires AT LEAST ONE of: event_type OR filter (both is fine; rejecting both empty). Params: {"event_type": "string or [string,...]", "filter": "jinja2"}. Filter sees {{ event.<field> }} at root — fields: event_type, source, cluster, subject_namespace, subject_name, priority (HIGH|MEDIUM|LOW|INFO|DEBUG), status, labels. Tasks read the event via {{ Inputs.event.<field> }}.
- "optimization" - All params optional (empty = match every recommendation). Params: {"categories": ["PodRightSizing"|"RightSizing"|"K8sInstanceRecommendation"|"K8sSpotRecommendation"|"Configuration"|"Security"|"K8sMissingAttribute"], "rule_names": ["vertical_rightsize"|"horizontal_rightsize"|"pvc_rightsize"|"continuous_rightsize"|"replica_right_sizing"|"Spot instance recommendation"|"Abandoned resource"], "clusters": ["string",...], "filter": "jinja2 (optional)"}. Filter and tasks see the recommendation event — fields: category, rule_name, cluster, resource_id, estimated_savings, severity, recommendation_id. Tasks read it via {{ Inputs.event.<field> }}.

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
7. Jinja2 templates in "if", "params", "set_state", "output" are parsed and validated. Templates are Jinja2 ONLY — JMESPath/JSONPath constructs ([*], [?...], .., @) are NOT supported and fail validation with: invalid expression ... near "*". Map list fields in an upstream scripting.run_script task, not in the template
8. Duration fields ("timeout") must be valid durations (e.g., "30s", "5m", "1h")
9. Manual trigger must NOT have params (or empty params)
10. Schedule trigger MUST have "cron" param. Optional "overlap_policy" must be one of Skip|BufferOne|BufferAll|AllowAll|CancelOther|TerminateOther. Optional "catchup_window" MUST use Go time.ParseDuration units (ns|us|ms|s|m|h) — day units like "7d" are NOT supported (use "168h" for 7 days); compound durations like "1h30m" ARE allowed
11. Webhook trigger MUST have "integration_name" param
12. Event trigger MUST have AT LEAST ONE of "event_type" OR "filter". "event_type" may be a string or array of strings
13. Optimization trigger: all params are optional (categories[], rule_names[], clusters[], filter). Empty params means "match every recommendation". Array params must contain strings only
`
}

func (a *WorkflowBuilderAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	// Capture the current viewing context for this turn so the env block can default to it (#30162).
	a.currentCluster = strings.TrimSpace(request.QueryConfig.CurrentCluster)
	a.currentClusterId = strings.TrimSpace(request.QueryConfig.CurrentClusterId)

	switch a.state.Stage {
	case "clarification":
		return a.handleClarificationResponse(ctx, request)
	case "plan_approval":
		return a.handlePlanApproval(ctx, request)
	case "feedback":
		return a.handleFeedback(ctx, request)
	default:
		// Includes the retired "fix_approval"/"fix_feedback" stages: any conversation
		// parked in those (pre-redesign) falls through here and is re-classified fresh.
		return a.handleEntry(ctx, request)
	}
}

// handleEntry classifies the turn's intent and routes it. The builder is reached via the forced
// /workflow-generate path, which always carries the canvas automation's workflow_id; routing on the
// mere presence of that id used to collapse every actionable turn into the failure-diagnosis flow
// (so "add a feature" got "No failed runs found…"). Instead we classify intent:
//   - ANSWER → handleReadOnly (explain/diagnose/status, never mutates)
//   - EDIT   → handleEditEntry (fix a failure OR add/modify/remove a feature; the agent decides)
//   - CREATE → handleIntentAndPlan (build a new workflow)
//
// Explicit orchestrator directives still win: the WorkflowAgent delegates as an agent-as-tool and
// transmits its decision structurally via a JSON command ({"mode":"create"|"fix","workflow_id":...,
// "query":...}). A "create"/"fix" directive routes deterministically without re-classifying; only the
// canvas / no-directive path consults the intent classifier. request.Query is replaced with the inner
// instruction text so every downstream LLM call sees clean text instead of the JSON envelope. This
// runs only at the non-mid-stage entry; mid-stage turns are handled by their cases in Execute.
func (a *WorkflowBuilderAgent) handleEntry(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	directiveMode, directiveWorkflowId, commandText := parseDelegationCommand(request.Query)
	request.Query = commandText

	// Explicit orchestrator directives win — no inference, no classification.
	if directiveMode == "create" {
		return a.handleIntentAndPlan(ctx, request)
	}
	if directiveMode == "fix" && directiveWorkflowId != "" {
		ctx.GetLogger().Info("workflow_builder: routing to edit via delegation directive", "workflow_id", directiveWorkflowId)
		return a.handleEditEntry(ctx, request, directiveWorkflowId)
	}

	// Canvas / direct API path (QueryConfig.WorkflowId) or a legacy delegation that embeds the id in
	// text (account UUIDs excluded so a create payload isn't mistaken for a fix). Classify the turn.
	workflowId := request.QueryConfig.WorkflowId
	if workflowId == "" {
		workflowId = extractWorkflowIdFromQuery(request.Query, a.knownAccountIds(ctx))
	}
	hasWorkflow := workflowId != ""

	switch a.classifyTurnIntent(ctx, request, hasWorkflow) {
	case turnIntentAnswer:
		return a.handleReadOnly(ctx, request)
	case turnIntentEdit:
		return a.handleEditEntry(ctx, request, workflowId)
	default: // turnIntentCreate
		return a.handleIntentAndPlan(ctx, request)
	}
}

// classifyTurnIntent classifies the current turn into ANSWER / EDIT / CREATE with a single
// lightweight LLM call (retrieval tier, same pattern as generateClarifyingQuestions). It replaces
// the old binary read-only check plus the "workflow_id ⇒ fix" hardcoding, so feature changes to an
// existing workflow ("also fetch comments and summarise") are recognised as edits rather than being
// funnelled into failure diagnosis.
//
// Conservative defaults: on classifier error or an unparseable answer it returns EDIT when a
// workflow is loaded (the safe non-create choice for the canvas) and CREATE otherwise. It never
// returns EDIT when no workflow exists — a stray "edit" with nothing to edit becomes CREATE.
func (a *WorkflowBuilderAgent) classifyTurnIntent(ctx *security.RequestContext, request core.NBAgentRequest, hasWorkflow bool) turnIntent {
	fallback := turnIntentCreate
	if hasWorkflow {
		fallback = turnIntentEdit
	}

	systemPrompt := fmt.Sprintf(`You classify a user's message in an automation-builder chat into exactly one of three intents. There %s currently an automation open in the editor.

ANSWER — the user wants information, an explanation, or a diagnosis with NO change requested. Examples:
- "explain this workflow", "what does this automation do?", "summarize it"
- "why did it fail?", "why is this failing?", "what's the reason for this error?"
- "what can you do?", "help", greetings
- any question seeking understanding, diagnosis, or status WITHOUT asking to change anything

EDIT — the user wants to change the EXISTING automation. This covers BOTH debugging a failure AND
adding/modifying/removing functionality. Examples:
- "fix it", "fix the error in step 2", "apply the fix", "it's failing, repair it"
- "change the timeout to 5m", "add a Slack alert", "remove the email task", "rename task X"
- "can we update to also fetch their comments and summarise the story?"
- polite / question-form requests that still ask for a change:
  "can you replace the Slack step with print?", "could you add a retry?", "can we swap X for Y?"

CREATE — the user wants to build a NEW automation from scratch. Examples:
- "create a workflow to fetch top 5 hackernews", "build an automation that ..."

Rules:
- Classify by the underlying request, NOT politeness or grammar. A change asked as a question
  ("can you ...?", "could you ...?", "would you ...?") is still EDIT (or CREATE for a brand-new one).
- Verbs that signal a change to the open automation — add, remove, delete, replace, swap, rename,
  change, update, set, fix, apply, repair — mean EDIT, even when phrased as a question.
- "create"/"build"/"make a new" a fresh automation is CREATE. If something is already open and the
  user asks to change it, that is EDIT, not CREATE.
- If the user only asks to understand or diagnose (even an error or failure) and requests NO change,
  it is ANSWER.
- If genuinely ambiguous between ANSWER and a change, prefer the change (EDIT if an automation is
  open, otherwise CREATE).

Respond with ONLY one word: ANSWER, EDIT, or CREATE.`, map[bool]string{true: "IS", false: "is NOT"}[hasWorkflow])

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	// Lite/fast model — this is a one-word classification.
	classifyCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyModelTier, core.ModelTierRetrieval),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := core.GenerateAndTrackLLMContent(classifyCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false, llms.WithTemperature(0.0))
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: turn intent classification failed, using fallback", "error", err, "fallback", fallback)
		return fallback
	}
	if completion == nil || len(completion.Choices) == 0 {
		return fallback
	}
	return parseTurnIntent(completion.Choices[0].Content, hasWorkflow)
}

// parseTurnIntent interprets the classifier's raw answer into a turnIntent. It checks for EDIT/CREATE
// before ANSWER so a verbose reply naming a change wins over an incidental "answer" mention, and it
// downgrades EDIT to CREATE when no workflow is loaded (nothing to edit). On no match it returns the
// hasWorkflow-aware fallback (EDIT when a workflow is open, else CREATE) — never silently ANSWER.
func parseTurnIntent(answer string, hasWorkflow bool) turnIntent {
	up := strings.ToUpper(strings.TrimSpace(answer))
	switch {
	case strings.Contains(up, string(turnIntentEdit)):
		if !hasWorkflow {
			return turnIntentCreate
		}
		return turnIntentEdit
	case strings.Contains(up, string(turnIntentCreate)):
		return turnIntentCreate
	case strings.Contains(up, string(turnIntentAnswer)):
		return turnIntentAnswer
	default:
		if hasWorkflow {
			return turnIntentEdit
		}
		return turnIntentCreate
	}
}

// handleReadOnly answers a question about the automation (explain / summarize / why-did-it-fail /
// capability) WITHOUT building or modifying anything. It reuses the agentic tool loop with a
// narration prompt and read-only tools, returning prose. It never persists: the loop's finalize
// only returns JSON in-memory and this handler issues no save (edits persist via handleEditEntry,
// new builds via handlePlanApproval → buildAndValidate).
func (a *WorkflowBuilderAgent) handleReadOnly(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	workflowId := request.QueryConfig.WorkflowId
	if workflowId == "" {
		workflowId = extractWorkflowIdFromQuery(request.Query, a.knownAccountIds(ctx))
	}

	var definitionContext string
	if workflowId != "" {
		a.state.WorkflowId = workflowId
		if wfResp, err := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows/%s", workflowId), nil, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId()); err == nil {
			definitionContext = string(wfResp)
			var wf map[string]interface{}
			if json.Unmarshal(wfResp, &wf) == nil {
				// Load so list_tasks / get_task in the read-only loop can inspect the definition.
				a.state.WorkingWorkflow = wf
			}
		} else {
			ctx.GetLogger().Warn("workflow_builder: read-only could not fetch workflow, answering from question only", "workflow_id", workflowId, "error", err)
		}
	}

	answer, err := a.runToolLoop(ctx, request, buildReadOnlyPrompt(request.Query, definitionContext), fmt.Sprintf("Answer the user's question: %s", request.Query))
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("read-only answer failed: %w", err)
	}
	return core.NBAgentResponse{Response: []string{answer}, IsTerminal: true}, nil
}

// buildReadOnlyPrompt builds the narration system prompt for read-only turns. It grants the agentic
// loop's read tools only and forbids any modification — the loop must emit a prose <final_answer>,
// never a workflow definition or a finalize call (so nothing can be auto-applied to the canvas).
func buildReadOnlyPrompt(userQuery, definitionContext string) string {
	var defSection string
	if strings.TrimSpace(definitionContext) != "" {
		defSection = fmt.Sprintf(`
CURRENT AUTOMATION DEFINITION:
%s
`, definitionContext)
	}

	return fmt.Sprintf(`You are a Nudgebee automation assistant answering a QUESTION about an automation, or running a non-destructive dry-run when asked. You must NOT build, modify, fix, or apply any change to the saved definition.

USER QUESTION:
%s
%s
AVAILABLE TOOLS (use ONLY these; never init_workflow / add_task / modify_task / delete_task / validate / finalize):
- list_tasks — list the automation's tasks
- get_task — inspect a specific task
- list_executions — list recent runs (use status="FAILED" for failure questions)
- get_execution — read a run's task-level errors and outputs
- dry_run — execute the automation against the engine WITHOUT persisting external side effects, returning per-task status and errors. This is how you verify it works or reproduce a failure.

INSTRUCTIONS:
- Dry-run / test requests ("dry run this", "test it", "does this work", "run it safely"): call dry_run, then report the overall result and, for any failing task, its id + error. Do NOT claim the automation "has no dry-run mode" — the engine dry-runs it for you. Dry-run does not modify the saved automation.
- Failure questions ("why did it fail", "what is this error"): gather evidence first — list_executions(status="FAILED"), then get_execution on the most recent failed run; or dry_run to reproduce. Quote the actual error, name the failing task, and explain the cause. Do NOT propose or apply a fix unless the user explicitly asks to fix it.
- Explain/summarize questions: describe the trigger, tasks, and flow from the definition above.
- Capability/greeting questions: briefly say you can build, modify, explain, diagnose, and dry-run automations.
- TONE: be a proactive collaborator, not a dead-end. After answering, close by conversationally offering the single most useful next step for where the user is (e.g. dry-run to verify it works, fix a failing task, publish a version) phrased as a brief question — keep the conversation moving. Suggest only what actually fits the situation; never invent steps.
- When you have the answer, emit a <final_answer> whose <content> is your prose answer in Markdown. Do NOT output a workflow JSON definition. Do NOT call finalize.`, userQuery, defSection)
}

// parseDelegationCommand interprets a builder delegation command. The WorkflowAgent is instructed to
// delegate as a JSON object — {"mode":"create"|"fix","workflow_id":"<id>","query":"<instructions>"} —
// so create-vs-fix routing is read from explicit fields instead of inferred from prose. Returns the
// lower-cased mode, the workflow id (fix only), and the instruction text to use for downstream LLM
// calls. A plain-string command (or JSON without these fields) yields empty mode/id and the original
// text unchanged, so legacy/unstructured delegations fall through to the heuristic backstop.
func parseDelegationCommand(query string) (mode, workflowId, text string) {
	text = query
	var d struct {
		Mode       string `json:"mode"`
		WorkflowId string `json:"workflow_id"`
		Query      string `json:"query"`
	}
	if err := common.UnmarshalJson([]byte(query), &d); err == nil {
		if strings.TrimSpace(d.Query) != "" {
			text = d.Query
		}
		mode = strings.ToLower(strings.TrimSpace(d.Mode))
		workflowId = strings.TrimSpace(d.WorkflowId)
	}
	return mode, workflowId, text
}

// extractWorkflowIdFromQuery looks for a UUID in the query text that likely represents a workflow ID.
// The WorkflowAgent is instructed to include the workflow ID when delegating fix requests.
//
// excludeIds holds account UUIDs (the k8s account plus every configured cloud account) that
// legitimately appear in *create* delegations — e.g. `params.account_id` on k8s.cli / cloud.*.cli
// tasks. Those must never be treated as a workflow ID: doing so misroutes a create request into fix
// mode, which then issues GET workflows/{account_id} → 404 ("workflow not found"). The first non-
// excluded UUID is returned, so a genuine fix delegation ("Fix automation <wf-id> in account <acct-id>")
// still resolves to the workflow ID even when an account UUID precedes it in the text.
func extractWorkflowIdFromQuery(query string, excludeIds map[string]bool) string {
	// Intent (edit vs create vs answer) is decided by classifyTurnIntent, so this no longer gates on
	// keywords — it returns the first non-excluded UUID (account UUIDs that legitimately appear in
	// create payloads are skipped so they're never mistaken for a workflow id).
	for _, match := range uuidRegex.FindAllString(query, -1) {
		if !excludeIds[strings.ToLower(match)] {
			return match
		}
	}
	return ""
}

// knownAccountIds returns the set of account UUIDs (lower-cased) that legitimately appear in build
// payloads: this k8s account plus every configured cloud-account (aws/gcp/azure/k8s). These are
// excluded from workflow-ID extraction so a create delegation embedding an account_id is not
// mistaken for a fix request. Degrades gracefully — if configs can't be listed, only the primary
// account ID is excluded (still covers the most common misroute).
func (a *WorkflowBuilderAgent) knownAccountIds(ctx *security.RequestContext) map[string]bool {
	ids := map[string]bool{}
	// Fail closed: an empty account id would make ListAllToolConfigs unscoped, and a nil ctx has no
	// logger. Either way there is nothing to exclude — return the empty set.
	if a.accountId == "" || ctx == nil {
		return ids
	}
	ids[strings.ToLower(a.accountId)] = true
	configs, err := toolcore.ListAllToolConfigs(ctx, a.accountId)
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: knownAccountIds: ListAllToolConfigs failed, excluding only the primary account id", "error", err)
		return ids
	}
	for _, cfg := range configs {
		if !cloudAccountConfigTypes[strings.ToLower(cfg.Schema.ConfigType)] {
			continue
		}
		if id := cloudAccountId(cfg); id != "" {
			ids[strings.ToLower(id)] = true
		}
	}
	return ids
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
// getClarificationSystemPrompt builds the LLM prompt used by generateClarifyingQuestions.
// Extracted into a top-level function so the prompt body is testable from unit
// tests (matches the pattern of getWorkflowSchema / getBuildSystemPrompt). The
// rules here decide which questions the LLM is allowed to ask before the user
// approves a plan — any change to them affects every workflow-builder
// clarification flow.
func getClarificationSystemPrompt(envContext, configsContext, intent string) string {
	return fmt.Sprintf(`You are pre-planning an automation for Nudgebee. Before generating a plan, determine if the user's request has enough detail to build a useful automation.

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
4. For ALL requests (including SPECIFIC): Cross-reference the tasks the automation needs against the ACCOUNT ENVIRONMENT above. If the automation requires an external service (database, notification, cloud CLI, etc.) and there are multiple configured integrations of that type, ask which one to use. If only one exists, infer it. EXCEPTION: if a CURRENT CONTEXT account/cluster is given above, never ask which account, cluster, or workspace to run against — use the CURRENT CONTEXT one.
5. If the trigger type is not explicitly stated, ask — do not silently default to "manual".

QUESTION PRIORITY (ask in this order of importance):
1. Core purpose — what should the automation actually do? (only if truly unclear)
2. Trigger type — manual, scheduled (cron), webhook, event-driven, or optimization (fires on new K8s/cost optimization recommendations)? (if not specified or implied)
3. Integration choices — which specific integrations/configs to use when multiple exist
4. Key action details — targets, thresholds, recipients (only if not inferable from context)

QUESTION STYLE:
- Lead with your recommendation, drawing ONLY from values present in the context above (cloud accounts, integrations, configs). For example with two configured AWS accounts: "I'll run this against prod-aws." options: ["prod-aws (recommended)", "staging-aws"].
- NOT: "Which AWS account?" — that's lazy.
- If an integration is needed but not configured: explain the situation and offer concrete alternatives from what IS configured, or suggest adding a placeholder.
- If the user said "A or B?" — recommend one and explain why.
- Each option must be a concrete, actionable value — not a generic label.
- Option labels are the plain display NAME only. NEVER put a UUID, an "id=..." value, or a provider annotation like "(AWS ...)" or "(k8s, id=...)" in a label — the "id=" and provider shown in the ACCOUNT ENVIRONMENT context are for your internal account_id mapping only and must never be shown to the user. The ONLY parenthetical allowed in a label is the "(recommended)" marker.

DO NOT INVENT RESOURCE VALUES:
- Specific resource names (Slack channels, k8s namespaces, S3 buckets, DB tables, repo names, etc.) MUST come from the context above. Never guess or fabricate them.
- DO NOT ask which Slack channel to send notifications to. The workflow will use {{ Configs.slack_channel }} as a placeholder, and the user will fill in the channel via Configs UI or via workflow Inputs at runtime. Channel selection is NOT a clarification question.
- Same rule for any other resource value that is not enumerated in the context above: do not ask about it here. Defer to {{ Configs.<key> }} or workflow Inputs.

WHEN TO RETURN ZERO QUESTIONS:
- The request describes a clear action (e.g., "check pod health and alert on Slack every hour")
- All needed integrations/accounts can be inferred from context (e.g., only one of a type exists)
- The user already named specific integrations, clusters, trigger type, etc.

OUTPUT FORMAT (JSON only, no other text):
{
  "questions": [
    {
      "question": "I'll run this against prod-aws. Use that account?",
      "options": ["prod-aws (recommended)", "staging-aws"]
    }
  ]
}

Return {"questions": []} ONLY when the request is specific and detailed enough to plan directly.
Maximum 3 questions.`, envContext, configsContext, intent)
}

func (a *WorkflowBuilderAgent) generateClarifyingQuestions(ctx *security.RequestContext, request core.NBAgentRequest, intent string) ([]ClarifyingQuestion, error) {
	// includeAccountIDs=false: clarification options are user-facing, so the id= UUID
	// must never enter this context (#31141). The account_id→UUID mapping happens later
	// in the plan prompt and resolveCloudAccountIds.
	envContext := a.buildEnvironmentContext(ctx, false)
	configsContext := fetchConfigsContext(ctx, a.accountId)

	systemPrompt := getClarificationSystemPrompt(envContext, configsContext, intent)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	// Use lite model for speed
	clarifyCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyModelTier, core.ModelTierRetrieval),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := core.GenerateAndTrackLLMContent(clarifyCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false, llms.WithTemperature(0.0))
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

	// Strip internal identifiers (provider annotations, id=<uuid>, bare UUIDs) that the
	// LLM copies from the ACCOUNT ENVIRONMENT context into user-facing option labels
	// (#30885, #31141). The id is needed by the LLM for account_id mapping but must
	// never be shown to the user.
	for i := range result.Questions {
		result.Questions[i].Options = sanitizeQuestionOptions(result.Questions[i].Options)
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
- TEMPLATES ARE JINJA2 ONLY — NEVER JMESPath/JSONPath: Inside {{ }} only Jinja2 is valid: dotted access, ['key'] subscript, integer index [0], slices [0:3], and | filters. The projection/wildcard forms [*], [?...], .. and @ are JMESPath/JSONPath and are NOT Jinja2 — the validator rejects them with: invalid expression ... near "*". To collect one field across a LIST of objects, do NOT write {{ Tasks['t'].output.result[*].metadata.name }}. Instead add an upstream scripting.run_script (python) task that builds the derived value (e.g. the joined pod names) and reference its scalar output: {{ Tasks['extract-names'].output.data }}. A {{ }} expression must resolve to a single scalar/string, never a projected list.
- Integration IDs: {{ Configs.<type>_integration_id }} (e.g., {{ Configs.slack_integration_id }})
- Integer values: use 5, NOT 5.0
- Only use task types that get_task_schema returns — do NOT invent types
- Task IDs in depends_on must match actual task IDs you've added
- Manual trigger: NO params (or empty object)
- Schedule trigger: MUST have "cron" param. catchup_window (if set) uses Go time.ParseDuration syntax — valid units ns|us|ms|s|m|h ONLY; "7d"/"1w" are NOT supported (use "168h" for 7 days); compound values like "1h30m" ARE valid
- Webhook trigger: MUST have "integration_name". Filter (if any) MUST render to literal "true" or "1" — use {{ <expr> }}, never a raw boolean
- Event trigger: MUST have AT LEAST ONE of "event_type" OR "filter". event_type may be a string or an array
- Optimization trigger: all params optional (categories[], rule_names[], clusters[], filter). Empty = match every recommendation
- TRIGGER PAYLOAD ACCESS IN TASKS:
  - Manual/Schedule: user inputs → {{ Inputs.<key> }}
  - Webhook: request body → {{ Inputs.webhook_payload }}
  - Event: full event → {{ Inputs.event.<field> }} (e.g. {{ Inputs.event.cluster }}, {{ Inputs.event.priority }})
  - Optimization: recommendation → {{ Inputs.event.<field> }} (e.g. {{ Inputs.event.category }}, {{ Inputs.event.cluster }}, {{ Inputs.event.estimated_savings }})
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
- Transitive dependencies count: if X is already reachable through an ancestor in the depends_on chain, the referencing task need not also list X directly — the validator accepts transitive reachability. An explicit direct edge is always valid too.

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
- When fixing one task, do NOT change other tasks unless directly affected.

CLOUD ACCOUNT IDs (account_id parameter):
- For k8s.cli, cloud.aws.cli, cloud.gcp.cli, cloud.azure.cli: the params.account_id MUST be the UUID shown as id=<uuid> in the ACCOUNT ENVIRONMENT block above. NEVER use the display name. The runbook server validates account_id as a UUID and will reject the save with "invalid input syntax for type uuid" otherwise.`, intent, planSection, schema)
}

// getEditSystemPrompt returns the system prompt for the unified agentic edit loop. It covers BOTH
// debugging a failure and changing/extending the automation; the agent decides which from the user's
// request. It generalizes the former fix prompt (evidence-first debugging) and folds in the build
// rules needed when adding/modifying tasks.
func getEditSystemPrompt(errorContext, targetExecutionId, schema string) string {
	var targetSection string
	if targetExecutionId != "" {
		targetSection = fmt.Sprintf(`
TARGET EXECUTION ID: %s
If the user is debugging a failure, call get_execution(execution_id="%s") directly to read this run.
`, targetExecutionId, targetExecutionId)
	}

	var errorSection string
	if strings.TrimSpace(errorContext) != "" {
		errorSection = fmt.Sprintf(`
ERROR CONTEXT (provided by user / UI):
%s
`, errorContext)
	}

	return fmt.Sprintf(`You are a Nudgebee automation editor. You modify an EXISTING automation that is already loaded. The user's request may be either (A) DEBUG a failure or (B) CHANGE/EXTEND the automation. Decide which from the request, then act.
%s%s
AUTOMATION SCHEMA REFERENCE:
%s

FIRST, DECIDE THE INTENT:
- DEBUG signals: "fix", "it's failing", "error", "why broken", an ERROR CONTEXT or TARGET EXECUTION ID above.
- CHANGE signals: "add", "also", "include", "remove", "rename", "change", "update", "instead", "as well", a new capability.
- If the request is purely a question with no change asked, briefly answer in <final_answer> and make no modifications.

IF DEBUGGING (gather evidence FIRST, then fix):
1. If a TARGET EXECUTION ID is given, get_execution on it. Otherwise list_executions(status="FAILED", limit=10) and pick the most recent failed run, then get_execution on it.
2. Read the real error: workflow-level "error", per-task "error"/"status", and the failing task's "rendered_params" + "output". Quote it.
3. list_tasks, then get_task on the failing task and its upstream dependencies.
4. Apply the MINIMAL change that addresses the observed error via modify_task (or add_task/delete_task if required). Do not change unrelated tasks.
5. If there are genuinely no failed runs and no error context, the user likely wants a behavior change — treat the request as a CHANGE instead of dead-ending.

IF CHANGING/EXTENDING:
1. list_tasks to understand the current structure; get_task on tasks you will touch.
2. For each new or changed task: get_task_schema for its type to confirm params and the correct output field names, then add_task / modify_task / delete_task.
3. Preserve unrelated tasks, their IDs, and dependencies.

THEN, ALWAYS:
- Call validate. If it fails, read the error, fix the specific task, and validate again (try a different approach if the same error recurs).
- Once validation passes, call finalize to return the complete updated automation JSON.

RULES:
- Change only what is NECESSARY. Preserve existing task IDs, dependencies, and logic that the request does not touch.
- Jinja2 references: {{ Tasks['task-id'].output.<field> }} — check get_task_schema output_schema for the correct field name.
- TEMPLATES ARE JINJA2 ONLY: a parse error like invalid expression ... near "*" means a JMESPath/JSONPath construct ([*], [?...], .., @) was used inside {{ }}. Jinja2 has NO list projection. Add an upstream scripting.run_script (python) task that produces the derived scalar, then reference {{ Tasks['<that-task>'].output.data }}.
- Integration IDs: {{ Configs.<type>_integration_id }}. Integer values: use 5, NOT 5.0.
- SCRIPTING DATA INJECTION: pass task output to scripts via the "env" parameter — never embed {{ }} inside the script string. Read it via os.environ in the script. Always set "language" explicitly (omitting it defaults to bash).
- scripting.run_script with parser_type "json": stdout MUST be valid JSON — wrap Python in try/except and print JSON on error.
- Template filters use underscores: "to_json" (NOT "tojson"), "from_json" (NOT "fromjson").

OUTPUT FIELDS — DO NOT GUESS:
- ALWAYS call get_task_schema and read "output_schema" for the correct output field name. Different task types use different field names; the wrong field causes silent failures.

TASK DEPENDENCIES (depends_on) — CRITICAL:
- The executor runs tasks in PARALLEL unless constrained by depends_on. If task B references {{ Tasks['A'].output... }}, B MUST have depends_on: ["A"] — otherwise B launches before A completes and gets None. This applies top-level, inside foreach loop bodies, and inside groups. A transitively-reachable dep (already implied by an ancestor chain) need not be duplicated — the validator accepts transitive reachability. "Can't use Getitem on None" usually means a missing depends_on or a skipped upstream ("if" false, or an unselected core.switch branch) — use {{ Tasks['x'].output.data | default('') }} for optional upstreams.

core.foreach — ITEM VARIABLE:
- The "item" param sets the loop variable name (default: "item"). ALWAYS set it explicitly. Variable names are CASE-SENSITIVE: if item="issue", use {{ issue.title }}, NOT {{ Issue.title }}.

CLOUD ACCOUNT IDs (account_id parameter):
- For k8s.cli, cloud.aws.cli, cloud.gcp.cli, cloud.azure.cli: params.account_id MUST be a UUID. If you see a non-UUID value (e.g. a display name), replace it with the matching UUID from the account environment. The runbook server rejects non-UUID account_id values.`, errorSection, targetSection, schema)
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

// cloudAccountConfigTypes lists the ToolConfig.Schema.ConfigType values that
// correspond to cloud accounts whose runtime identifier in workflow tasks
// (params.account_id on k8s.cli / cloud.*.cli) is a UUID stored on the
// ToolConfig record.
var cloudAccountConfigTypes = map[string]bool{
	"aws":   true,
	"gcp":   true,
	"azure": true,
	"k8s":   true,
}

// cloudAccountId returns the canonical UUID for a cloud-account ToolConfig.
// For cloud accounts (k8s / aws / gcp / azure), `ListAllToolConfigs`
// (tools/core/tool_config.go:449, 500) leaves `cfg.Id` empty and stores the
// UUID in `cfg.Values` under the entry named "id". Integrations carry the
// UUID at `cfg.Id` directly. This helper handles both shapes so callers
// always see the UUID the runbook server expects in `params.account_id`.
func cloudAccountId(cfg toolcore.ToolConfig) string {
	if cfg.Id != "" {
		return cfg.Id
	}
	for _, v := range cfg.Values {
		if v.Name == "id" && v.Value != "" {
			return v.Value
		}
	}
	return ""
}

// resolveCloudAccountIds walks the workflow JSON and replaces any task's
// params.account_id that is a known cloud-account display name with the
// corresponding UUID. Values that are already valid UUIDs are left alone.
// Returns an error naming the offending task(s) when account_id is neither a
// UUID nor a known account name, so the user gets an actionable message
// instead of the cryptic "invalid input syntax for type uuid" from Postgres.
func (a *WorkflowBuilderAgent) resolveCloudAccountIds(ctx *security.RequestContext, workflowJSON string) (string, error) {
	allConfigs, err := toolcore.ListAllToolConfigs(ctx, a.accountId)
	if err != nil {
		// Graceful degradation: if we can't list configs, skip the resolution
		// and let the runbook server's save attempt produce the error path.
		ctx.GetLogger().Warn("workflow_builder: resolveCloudAccountIds: ListAllToolConfigs failed, skipping resolution", "error", err)
		return workflowJSON, nil
	}

	nameToId := map[string]string{}
	availableAccounts := []string{}
	for _, cfg := range allConfigs {
		if !cloudAccountConfigTypes[strings.ToLower(cfg.Schema.ConfigType)] {
			continue
		}
		id := cloudAccountId(cfg)
		if cfg.Name == "" || id == "" {
			continue
		}
		nameToId[cfg.Name] = id
		availableAccounts = append(availableAccounts, fmt.Sprintf("%s (id=%s)", cfg.Name, id))
	}

	var wf map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &wf); err != nil {
		return "", fmt.Errorf("resolveCloudAccountIds: parse workflow JSON: %w", err)
	}

	var tasks []interface{}
	if def, ok := wf["definition"].(map[string]interface{}); ok {
		if rawTasks, ok := def["tasks"].([]interface{}); ok {
			tasks = rawTasks
		}
	}

	var unresolved []string
	walkTasksAndResolveAccountIds(tasks, nameToId, &unresolved)

	if len(unresolved) > 0 {
		sort.Strings(availableAccounts)
		msg := fmt.Sprintf("task(s) %s have an unrecognized `account_id`. Use the UUID of a configured cloud account.", strings.Join(unresolved, ", "))
		if len(availableAccounts) > 0 {
			msg += " Available accounts: " + strings.Join(availableAccounts, "; ") + "."
		} else {
			msg += " No cloud accounts are configured on this account."
		}
		return "", fmt.Errorf("%s", msg)
	}

	resolved, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return "", fmt.Errorf("resolveCloudAccountIds: marshal: %w", err)
	}
	return string(resolved), nil
}

// walkTasksAndResolveAccountIds recurses through a task list (including
// nested `tasks[]` inside core.foreach / core.group / matrix bodies) and
// rewrites params.account_id name → UUID. Tasks whose account_id is a Jinja
// template (e.g. `{{ Configs.foo }}`) or already a UUID are left untouched.
// Tasks with an unresolvable account_id are appended to unresolved.
func walkTasksAndResolveAccountIds(tasks []interface{}, nameToId map[string]string, unresolved *[]string) {
	for _, rawTask := range tasks {
		task, ok := rawTask.(map[string]interface{})
		if !ok {
			continue
		}
		taskId, _ := task["id"].(string)

		if params, ok := task["params"].(map[string]interface{}); ok {
			if rawAccountId, exists := params["account_id"]; exists {
				if accountId, ok := rawAccountId.(string); ok && accountId != "" {
					trimmed := strings.TrimSpace(accountId)
					switch {
					case strings.Contains(trimmed, "{{"):
						// Template expression — let the runtime resolve it.
					case isValidUUID(trimmed):
						// Already a UUID — write back the trimmed value so any
						// surrounding whitespace doesn't reach the Postgres uuid
						// column (which rejects it as invalid syntax).
						params["account_id"] = trimmed
					default:
						if resolvedId, found := nameToId[trimmed]; found {
							params["account_id"] = resolvedId
						} else {
							ref := taskId
							if ref == "" {
								ref = "<unnamed>"
							}
							*unresolved = append(*unresolved, fmt.Sprintf("'%s' (account_id=%q)", ref, trimmed))
						}
					}
				}
			}
		}

		// Recurse into nested tasks (core.foreach, core.group, etc.).
		if nested, ok := task["tasks"].([]interface{}); ok {
			walkTasksAndResolveAccountIds(nested, nameToId, unresolved)
		}
	}
}

// isValidUUID returns true if s parses as a UUID. Used to distinguish
// already-correct account_id values from display names.
func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// checkMissingConfigs detects {{ Configs.xxx }} references in the built workflow, finds the ones
// that don't exist on the server, and auto-creates them as empty configs so the workflow can be
// saved without falling over on the workflow-server's "validation failed" rejection (issue #29944).
// The user fills in the values afterwards via the Configs UI; the summary returned to the user lists
// the auto-created keys so they know what to fill in. If the config API is unreachable we degrade
// gracefully and let the workflow-server's save attempt produce the error path.
func (a *WorkflowBuilderAgent) checkMissingConfigs(ctx *security.RequestContext, request core.NBAgentRequest, workflowJSON string) (core.NBAgentResponse, error) {
	// Resolve cloud-account names → UUIDs in task params.account_id before save.
	// The runbook server validates account_id as a UUID; if the LLM emitted a
	// display name (it doesn't always honour the prompt rule), the save will
	// fail with "invalid input syntax for type uuid". This swap closes that gap
	// for both create and fix paths (both funnel through here).
	resolvedJSON, resolveErr := a.resolveCloudAccountIds(ctx, workflowJSON)
	if resolveErr != nil {
		return core.NBAgentResponse{
			Response:   []string{fmt.Sprintf("Cannot save automation: %s", resolveErr.Error())},
			IsTerminal: true,
		}, nil
	}
	workflowJSON = resolvedJSON

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

	// Genuinely missing {{ Configs.x }} references. We deliberately do NOT invent placeholder
	// configs: doing so let automations pass the literal "<TODO: set value>" as an integration id /
	// channel / account id at run time, and left orphan config rows behind on abandoned builds
	// (#31490). Instead mark the automation INACTIVE — the work is preserved server-side but it
	// cannot run until the user supplies real values — and tell them exactly which configs to create.
	// INACTIVE (not PAUSED, the create default) is set explicitly so the fix-mode PUT path also
	// downgrades an already-ACTIVE workflow whose edit introduced an unresolved config.
	inactiveJSON := workflowJSON
	if ij, derr := setWorkflowStatus(workflowJSON, string(workflowStatusInactive)); derr == nil {
		inactiveJSON = ij
	} else {
		ctx.GetLogger().Warn("workflow_builder: could not set INACTIVE status for missing-config save", "error", derr)
	}
	return a.finalizeWithAutoSave(ctx, request, inactiveJSON, missing), nil
}

// workflowStatusInactive mirrors the runbook model.WorkflowStatusInactive value. The valid workflow
// statuses are ACTIVE, INACTIVE and PAUSED (there is no DRAFT — the builder prompt schema is wrong);
// brand-new workflows default to PAUSED at the DAO.
const workflowStatusInactive = "INACTIVE"

// setWorkflowStatus returns the workflow JSON with its top-level status set to the given value.
// The runbook server honours a non-empty top-level status on save.
func setWorkflowStatus(workflowJSON, status string) (string, error) {
	var wf map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &wf); err != nil {
		return "", fmt.Errorf("setWorkflowStatus: %w", err)
	}
	wf["status"] = status
	out, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return "", fmt.Errorf("setWorkflowStatus: %w", err)
	}
	return string(out), nil
}

// finalizeWithAutoSave returns the terminal response with the workflow JSON and
// attempts to auto-save the workflow via the workflow server API.
// missingConfigs lists {{ Configs.x }} keys that have no value yet; when non-empty the workflow has
// been marked INACTIVE by the caller and the user is told which configs to create before
// activating.
// For WorkflowBuilder source (editor UI): returns raw JSON since the UI consumes it directly.
// For other sources (ask-nudgebee chat): returns a markdown summary with a link to the saved workflow.
func (a *WorkflowBuilderAgent) finalizeWithAutoSave(ctx *security.RequestContext, request core.NBAgentRequest, workflowJSON string, missingConfigs []string) core.NBAgentResponse {
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

	// When the assistant is invoked from inside the editor (the embedded "Automation
	// Context" chat), the user is already on the automation's editor page, so an
	// "Open in Editor" link points back to where they are. Only surface the link for
	// off-editor surfaces (e.g. ask-nudgebee chat). The editor canvas refreshes in place.
	inEditor := request.ConversationSource == core.ConversationSourceWorkflowBuilder

	resp := core.NBAgentResponse{IsTerminal: true}
	switch {
	case saved && inEditor:
		// Saved from the editor — no link, the canvas refreshes in place.
	case saved:
		link := a.buildWorkflowEditorLink(request, workflowId)
		if link == "" {
			// Cannot construct a usable link without accountId — the editor
			// page requires `?accountId=<uuid>` to load the right tenant
			// context. Emit a degraded summary instead of a broken URL.
			ctx.GetLogger().Warn("workflow_builder: cannot build editor link, accountId unavailable",
				"workflow_id", workflowId,
				"agent_account_id_empty", a.accountId == "",
				"request_account_id_empty", request.AccountId == "")
			summary += "\n\n*Saved, but the Open-in-Editor link could not be generated (accountId unavailable). Open the automation from the Automations list.*"
		} else {
			summary += fmt.Sprintf("\n\n[Open in Editor](%s)", link)
			resp.References = []toolcore.NBToolResponseReference{{
				Text: "Open in Editor",
				Url:  link,
				Type: "link",
			}}
		}
	default:
		summary += fmt.Sprintf("\n\n*Auto-save failed — %s*", truncateSaveError(saveErr))
	}

	if len(missingConfigs) > 0 {
		summary += fmt.Sprintf("\n\n*Saved as **INACTIVE** because %d config value(s) are not set yet: `%s`. Create them under Configs (use a `secret`-typed config for API keys/tokens), then set the automation to **ACTIVE** to run it.*",
			len(missingConfigs), strings.Join(missingConfigs, "`, `"))
	}

	resp.Response = []string{summary}
	return resp
}

// buildWorkflowEditorLink returns the canonical "Open in Editor" URL the chat
// surfaces after building or fixing an automation. The frontend route is
// `/workflow/[workflowId]` and `WorkflowBuilderNotebook` reads `accountId` from
// `router.query`, so the link MUST include a non-empty `accountId`.
//
// The URL is absolute (prefixed with config.BaseUrl) because Slack mrkdwn only
// renders `<URL|label>` as a clickable hyperlink when URL has an http(s)
// scheme; a path-only URL appears as raw text. Runbook-server uses the same
// convention for its workflow links.
//
// Returns "" if accountId cannot be determined — callers MUST fall back to a
// no-link summary rather than emit a URL with an empty `accountId=` value,
// which would land the user on the editor page with no tenant context.
func (a *WorkflowBuilderAgent) buildWorkflowEditorLink(request core.NBAgentRequest, workflowId string) string {
	accountId := a.accountId
	if accountId == "" {
		accountId = request.AccountId
	}
	if accountId == "" || workflowId == "" {
		return ""
	}
	q := url.Values{}
	q.Set("accountId", accountId)
	if request.SessionId != "" {
		q.Set("session_id", request.SessionId)
	}
	base := strings.TrimRight(config.Config.BaseUrl, "/")
	return fmt.Sprintf("%s/workflow/%s?%s#editor", base, url.PathEscape(workflowId), q.Encode())
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
		// The builder is invoked statelessly per agent-as-tool call, so a re-delegation of the same
		// build (e.g. the WorkflowAgent retrying) re-POSTs and collides on the per-tenant/account name
		// uniqueness constraint. If THIS conversation already created that workflow
		// (created_from_session_id matches), converge by updating it instead of failing — an
		// idempotent save. We never overwrite a workflow we can't prove this session created, so a
		// name owned by a different/human-authored workflow is left untouched (data-loss guard).
		if isNameConflictError(err) && sessionId != "" {
			if existingId := a.findOwnWorkflowByName(ctx, workflowDefinitionName(definition), sessionId); existingId != "" {
				putResp, putErr := tools.DoRunbookRequest("PUT", fmt.Sprintf("workflows/%s", existingId), definition, a.accountId, tenantId, userId)
				if putErr == nil {
					ctx.GetLogger().Info("workflow_builder: create collided with this session's prior save, converged via update", "workflow_id", existingId)
					return extractWorkflowId(putResp, existingId), nil
				}
				ctx.GetLogger().Warn("workflow_builder: idempotent update after create-conflict failed", "workflow_id", existingId, "error", putErr)
			}
			return "", fmt.Errorf("workflow_builder: an automation named %q already exists for this account; rename it or open the existing one to edit", workflowDefinitionName(definition))
		}
		return "", fmt.Errorf("workflow_builder: auto-create failed: %w", err)
	}
	return extractWorkflowId(resp, ""), nil
}

// isNameConflictError reports whether a DoRunbookRequest error is the runbook server's
// per-tenant/account name-uniqueness rejection (HTTP 400/409 with an "already exists" body).
func isNameConflictError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// workflowDefinitionName extracts the top-level "name" from an EnsureDefinitionObject payload
// ({"name": ..., "definition": {...}}). Returns "" when absent.
func workflowDefinitionName(definition interface{}) string {
	if defMap, ok := definition.(map[string]interface{}); ok {
		if name, ok := defMap["name"].(string); ok {
			return name
		}
	}
	return ""
}

// findOwnWorkflowByName resolves the ID of a workflow with the exact given name that was created
// from sessionId — i.e. one this conversation owns. Returns "" if name/session is empty, the lookup
// fails, no exact-name match exists, or no match proves same-session ownership (the conservative
// data-loss guard: we only converge onto a workflow we can show this session created).
func (a *WorkflowBuilderAgent) findOwnWorkflowByName(ctx *security.RequestContext, name, sessionId string) string {
	if name == "" || sessionId == "" || ctx == nil || ctx.GetSecurityContext() == nil {
		return ""
	}
	// Fail closed in a multi-tenant store: a blank tenant or account would issue an unscoped lookup.
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" || a.accountId == "" {
		return ""
	}
	resp, err := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows?name=%s", url.QueryEscape(name)), nil, a.accountId, tenantId, ctx.GetSecurityContext().GetUserId())
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: findOwnWorkflowByName: list failed", "name", name, "error", err)
		return ""
	}
	var parsed struct {
		Workflows []map[string]interface{} `json:"workflows"`
	}
	if jsonErr := json.Unmarshal(resp, &parsed); jsonErr != nil {
		ctx.GetLogger().Warn("workflow_builder: findOwnWorkflowByName: parse failed", "error", jsonErr)
		return ""
	}
	for _, wf := range parsed.Workflows {
		// The server `name` filter is a partial match — require an exact name match.
		if wfName, _ := wf["name"].(string); wfName != name {
			continue
		}
		// Ownership: created_from_session_id may live at the top level or inside the definition,
		// depending on how the server persists it. Only converge when it equals this session.
		if workflowSessionId(wf) != sessionId {
			continue
		}
		if id, ok := wf["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// workflowSessionId reads created_from_session_id from a workflow object, tolerating it being
// stored either at the top level or nested under "definition".
func workflowSessionId(wf map[string]interface{}) string {
	if sid, ok := wf["created_from_session_id"].(string); ok && sid != "" {
		return sid
	}
	if def, ok := wf["definition"].(map[string]interface{}); ok {
		if sid, ok := def["created_from_session_id"].(string); ok && sid != "" {
			return sid
		}
	}
	return ""
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
	envContext := a.buildEnvironmentContext(ctx, true)
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

User: "Check pod health in production and staging namespaces"
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

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false, llms.WithTemperature(0.0))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(completion.Choices[0].Content), nil
}

// buildEnvironmentContext fetches the account's configured integrations, cloud accounts,
// and observability providers to give the LLM awareness of what is actually available.
// Uses cached APIs (ListAllToolConfigs: 30min, GetAccountConfigSummary: 30min).
//
// includeAccountIDs controls whether each cloud account is rendered with its internal
// UUID ("name (aws, id=<uuid>)") or by display name only ("name (aws)"). Plan/build
// prompts need the UUID because they emit workflow JSON whose `account_id` must be a
// UUID. The clarification prompt must NOT receive it: its output is user-facing option
// labels, and the LLM was copying the id straight into them (#31141). Withholding the
// id there fixes that at the source — there is simply nothing to leak.
func (a *WorkflowBuilderAgent) buildEnvironmentContext(ctx *security.RequestContext, includeAccountIDs bool) string {
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
			if cloudAccountConfigTypes[lowerType] {
				if includeAccountIDs {
					cloudAccounts = append(cloudAccounts, fmt.Sprintf("%s (%s, id=%s)", cfg.Name, configType, cloudAccountId(cfg)))
				} else {
					cloudAccounts = append(cloudAccounts, fmt.Sprintf("%s (%s)", cfg.Name, configType))
				}
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

	currentContext := a.currentContextSection()
	if len(parts) == 0 {
		return currentContext
	}

	accountIDRule := ""
	if includeAccountIDs {
		// Only meaningful when the id= UUID is actually rendered above.
		accountIDRule = "\nWhen a task requires `account_id` (k8s.cli, cloud.aws.cli, cloud.gcp.cli, cloud.azure.cli, etc.), use the UUID `id=` value shown for that cloud account above — NEVER the display name. The runbook server validates `account_id` as a UUID and rejects names." +
			"\nThe `id=` UUID shown above is INTERNAL — use it only to populate `account_id`. Never display it to the user in a question or option label; refer to a cloud account by its display name only."
	}

	return "\nACCOUNT ENVIRONMENT (configured integrations and cloud accounts on this account):\n" + strings.Join(parts, "\n") +
		"\n\nWhen the user references an integration by name (e.g., 'query dev-pg'), match it to the configured integration above and use {{ Configs.<name>_integration_id }} to reference it." +
		accountIDRule +
		"\nIf a task REQUIRES an integration NOT listed above, warn the user that it is not currently configured." +
		currentContext
}

// currentContextSection returns a prompt block naming the cluster/cloud-account the user is
// currently viewing, instructing prompts to default to it instead of asking which account/cluster
// to target (#30162). Returns "" when no current context was supplied (behavior then unchanged).
func (a *WorkflowBuilderAgent) currentContextSection() string {
	if a.currentCluster == "" && a.currentClusterId == "" {
		return ""
	}

	label := a.currentCluster
	if label == "" {
		label = a.currentClusterId
	}
	idSuffix := ""
	if a.currentClusterId != "" {
		idSuffix = fmt.Sprintf(" (account_id=%s)", a.currentClusterId)
	}

	return fmt.Sprintf(
		"\n\nCURRENT CONTEXT: The user is already working inside the cluster/account \"%s\"%s. "+
			"Do NOT ask the user which account, cluster, or workspace to use. "+
			"For a task that needs an `account_id` (k8s.cli, cloud.*.cli), default to THIS account/cluster when it fits the task's provider; "+
			"otherwise pick the appropriate configured account from the ACCOUNT ENVIRONMENT above without asking. "+
			"Only ask if no suitable account is configured.",
		label, idSuffix)
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
- "manual" → No params allowed. User runs the automation from the UI on demand. User-supplied inputs are read in tasks via {{ Inputs.<key> }}.
- "schedule" → Requires: cron (5-field UTC string, e.g. "0 9 * * MON-FRI"). Optional: overlap_policy ("Skip"|"BufferOne"|"BufferAll"|"AllowAll"|"CancelOther"|"TerminateOther"; default "Skip"), catchup_window (Go time.ParseDuration syntax; valid units ns|us|ms|s|m|h ONLY; day/week units like "7d" are NOT supported — use hours: "168h" = 7 days; compound durations like "1h30m" ARE valid; default "60s"). IMPORTANT: Always set overlap_policy: "Skip" for monitoring automations to prevent overlapping runs.
- "webhook" → Requires: integration_name (string — must reference a workflow_webhook integration configured on the account). Optional: secret (string), filter (Jinja2 expression on payload, must render to literal "true" or "1"). Filter context: {{ webhook_payload }} at root. Tasks read the request body via {{ Inputs.webhook_payload }}.
- "event" → Requires AT LEAST ONE of: event_type (string or [string,...]) OR filter (Jinja2). Filter context: {{ event.<field> }} at root — known fields: event_type, source, cluster, subject_namespace, subject_name, priority (HIGH|MEDIUM|LOW|INFO|DEBUG), status, labels. Tasks read the event via {{ Inputs.event.<field> }}.
- "optimization" → Fires on new K8s/cost optimization recommendations. All params optional (empty = match every recommendation). Optional: categories ([string,...] from: PodRightSizing, RightSizing, K8sInstanceRecommendation, K8sSpotRecommendation, Configuration, Security, K8sMissingAttribute), rule_names ([string,...] from: vertical_rightsize, horizontal_rightsize, pvc_rightsize, continuous_rightsize, replica_right_sizing, "Spot instance recommendation", "Abandoned resource"), clusters ([string,...]), filter (Jinja2). Tasks read the recommendation via {{ Inputs.event.<field> }} — known fields: category, rule_name, cluster, resource_id, estimated_savings, severity, recommendation_id.

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
- core.switch → Branch based on value. Params: value, branches{}. Output: matched branch result. Tasks in UNSELECTED branches (and tasks that depend only on them) are stamped SKIPPED at run time — see SWITCH FAN-IN below before joining branch outputs.
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
- SWITCH FAN-IN (SKIPPED propagation): Unselected core.switch branches are stamped SKIPPED, and so is any task that depends ONLY on skipped tasks. A fan-in/join task still RUNS as long as at least one of its depends_on tasks was selected — but it is itself SKIPPED if EVERY dep is skipped, OR if it depends on any plain (non-switch) skipped task. So: (1) read unselected-branch outputs with default() — {{ Tasks['branch-a'].output.data | default('') }}; (2) for a task that must run after the switch regardless of which branch fired, depend on the switch task (or on every branch), not on a single branch that may be skipped; (3) do NOT add an optional/skippable non-switch task to a join's depends_on unless you intend the join to skip when that task skips.
- OPERATOR PRECEDENCE: The | default() filter has lower precedence than comparison operators. Without explicit parentheses, the expression may be parsed incorrectly.
  BAD:  {{ value | default(false) == true }}
  GOOD: {{ (value | default(false)) == true }}
- STRING CONTAINS: The "in" operator does NOT work in "if" conditions. Use a data.transform task with $contains() instead.
  BAD (will fail):  "if": "{{ ':green_circle:' in Tasks['llm'].output.data }}"
  GOOD: Use data.transform with expression: { "is_go": $contains($, ":green_circle:") }, then check Tasks['check'].output.data.is_go == true
- AVOID data.transform FOR COMPLEX LOGIC: JSONata does NOT support Python-style slicing ($[0:3]), has quirky object construction syntax, and many other pitfalls. For ANY non-trivial transformation (slicing, filtering, mapping, object building), use scripting.run_script with language "python" and parser_type "json" instead. Only use data.transform for trivial single-field extraction.
- WHOLE-VALUE TEMPLATES FOR NON-STRING PARAMS: You may set the ENTIRE value of an object/array/number/boolean param to a single {{ ... }} template (e.g. MCP task args/headers, integrations.http body/headers, a map- or list-typed param). It passes save-time validation and resolves to the correct shape at run time. Only WHOLE-value templates work — you cannot template just part of a map (build the derived map in an upstream scripting.run_script task and reference its scalar output instead).
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
	envContext := a.buildEnvironmentContext(ctx, true)

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
   - Type: manual | schedule | webhook | event | optimization
   - If manual: just "manual" (no params)
   - If schedule: exact cron expression (5-field UTC) + overlap_policy + (if needed) catchup_window (Go duration syntax, units ns|us|ms|s|m|h ONLY — "7d" NOT supported, use "168h"; compound "1h30m" OK)
   - If webhook: integration_name + optional filter (must render "true"/"1") + optional secret
   - If event: event_type (string or array) AND/OR filter (at least one required); call out which event fields ({{ event.priority }}, {{ event.cluster }}, etc.) the filter uses
   - If optimization: any of categories[]/rule_names[]/clusters[]/filter (all optional; empty = match every recommendation). Use enum values verbatim from the planning context.

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
- For schedule triggers always include cron + overlap_policy. catchup_window (if specified) MUST use Go time.ParseDuration units (ns|us|ms|s|m|h); "7d" is NOT supported (use "168h"); compound durations like "1h30m" ARE allowed
- For webhook/event/optimization filter expressions: must render to literal "true" or "1" — wrap in {{ ... }}, do not emit a raw Python/JS boolean
- For event triggers without an event_type, the filter is REQUIRED (engine rejects events lacking both)
- For optimization triggers: use the category/rule_name enum values exactly as listed in the planning context; never invent new ones
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

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false, llms.WithTemperature(0.0))
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
	envContext := a.buildEnvironmentContext(ctx, true)

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
**2. Trigger** (with exact params per type — schedule: cron + overlap_policy (+ catchup_window if relevant); webhook: integration_name (+ filter/secret); event: event_type or filter; optimization: categories/rule_names/clusters/filter as applicable)
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

	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, false, llms.WithTemperature(0.0))
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
			Description: "Initialize a new automation with name, triggers, and optional inputs. Call this first when creating a new automation. Valid trigger types: manual, schedule, webhook, event, optimization (see TRIGGER TYPES and getWorkflowPlanningContext for required params per type).",
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
			// Widened: runtime/engine errors that are unambiguously real and must never soft-pass.
			"template execution panic",
			"workflow stalled",
			"matrix",
			"failed to process",
		}
		for _, pattern := range structuralErrorPatterns {
			if strings.Contains(errMsg, pattern) {
				return fmt.Sprintf("Validation FAILED: %s\n\nThis is a structural error that must be fixed. Use modify_task to correct the issue, then call validate again.", errMsg)
			}
		}

		// Template rendering errors are fail-closed. Only defer the narrow case where the
		// expression genuinely references a runtime value (task output / config / input) that
		// cannot be evaluated statically. A template error that carries a syntax/filter marker
		// (bad filter, unsupported JMESPath projection, malformed expression) is a REAL bug and
		// must fail even though it shares the "unable to execute template" prefix — otherwise a
		// typo'd template soft-passes and only breaks at execution time.
		// NOTE: fully disambiguating ambiguous template errors needs an execution-grounded
		// dry-run (#31496); until then we err toward failing closed.
		if strings.Contains(errMsg, "unable to execute template") {
			if isRealTemplateSyntaxError(errMsg) || !referencesRuntimeValue(errMsg) {
				return fmt.Sprintf("Validation FAILED: %s\n\nThis template expression could not be evaluated and does not clearly reference a runtime value, so it is treated as a real error. Fix the template (check filters, expression syntax, and that any Tasks/Configs/Inputs reference is correct), then call validate again.", errMsg)
			}
			return "Validation OK (with template warnings). Some template expressions cannot be evaluated during validation because they reference runtime values (task outputs, configs, inputs). They will resolve correctly at execution time. The automation structure is valid. Proceed to finalize."
		}
		return fmt.Sprintf("Validation FAILED: %s", errMsg)
	}

	return "Validation OK. The automation is valid."
}

// dryRunBuilderTimeout bounds the synchronous dry-run call from the builder loop. The
// runbook dry-run handler can block up to its own ceiling; most automations finish in
// seconds. A longer one is reported as still-running rather than hanging the loop.
const dryRunBuilderTimeout = 3 * time.Minute

// toolDryRun executes the current automation against the runbook engine WITHOUT
// persisting external side effects, returning per-task status/errors. This is how the
// editor's assistant verifies an automation works and diagnoses a failure with real
// execution evidence (the engine supports dry-run even though there is no "dry-run
// mode" baked into the definition). The dry-run endpoint takes the inner definition.
func (a *WorkflowBuilderAgent) toolDryRun(ctx *security.RequestContext) string {
	if a.state.WorkingWorkflow == nil {
		return "Error: automation not initialized."
	}
	coerceWorkflowTypes(a.state.WorkingWorkflow)

	// WorkingWorkflow is the full workflow object ({name, definition, ...}); the
	// dry-run endpoint expects {definition: <inner>}. Fall back to sending the whole
	// thing if there's no nested definition key.
	var definition any = a.state.WorkingWorkflow
	if inner, ok := a.state.WorkingWorkflow["definition"]; ok {
		definition = inner
	}
	body := map[string]any{"definition": definition}
	if name, ok := a.state.WorkingWorkflow["name"].(string); ok && name != "" {
		body["name"] = name
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), dryRunBuilderTimeout)
	defer cancel()

	resp, err := tools.DoRunbookRequestWithContext(reqCtx, "POST", "workflows/dry-run", body, a.accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return fmt.Sprintf("Dry-run is still running after %s — this automation is long-running. Trigger it as a real run and inspect the execution instead.", dryRunBuilderTimeout)
		}
		return fmt.Sprintf("Dry-run FAILED to start: %s", common.SanitizeErrorMessage(err.Error()))
	}
	return "Dry-run complete. Result (per-task status and errors):\n" + tools.SummarizeDryRunResponse(resp)
}

// isRealTemplateSyntaxError reports whether an "unable to execute template" error carries a marker
// of a genuine syntax/filter mistake (bad filter, unsupported JMESPath/JSONPath projection,
// malformed expression) rather than a value that is merely unknown until execution time. Such
// errors must fail validation — soft-passing them ships a template that only breaks at run time.
func isRealTemplateSyntaxError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	markers := []string{
		"invalid expression",
		"unexpected",
		"unable to parse",
		"syntax error",
		"no filter named",
		"unknown filter",
		"filter not found",
		"invalid filter",
		"tojson", // common mistake: `tojson` instead of the supported `to_json`
		"[*]",    // JMESPath/JSONPath projections are not valid Jinja2 and fail validation
		"[?",
		"near \"",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// referencesRuntimeValue reports whether a template error clearly involves a value only known at
// execution time (task output / config / input). Those legitimately cannot render during static
// validation, so they are safe to defer to execution.
func referencesRuntimeValue(errMsg string) bool {
	markers := []string{"Tasks[", "Tasks.", "Configs.", "Inputs.", ".output", "runtime"}
	for _, m := range markers {
		if strings.Contains(errMsg, m) {
			return true
		}
	}
	return false
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
// requires a.state.WorkflowId to be set by handleEditEntry.
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
	case "dry_run":
		return a.toolDryRun(ctx)
	case "finalize":
		return a.toolFinalize()
	case "list_executions":
		return a.toolListExecutions(ctx, args)
	case "get_execution":
		return a.toolGetExecution(ctx, args)
	default:
		return fmt.Sprintf("Unknown tool: '%s'. Available tools: init_workflow, add_task, get_task, modify_task, delete_task, list_tasks, get_task_schema, validate, dry_run, finalize, list_executions, get_execution", toolName)
	}
}

// persistBuildToolCall records a single build-tool invocation to llm_conversation_tool_calls so a
// build is debuggable from the database. The bespoke runToolLoop calls the build tools as plain
// methods (not framework NBTools), so it bypasses the planner's automatic tool-call persistence and
// previously left zero rows across every build (#31492). Best-effort: a persistence failure or an
// unavailable DAO must never break the build itself.
func (a *WorkflowBuilderAgent) persistBuildToolCall(ctx *security.RequestContext, request core.NBAgentRequest, toolName, toolInput, thought, observation string) {
	// Skip when any identifier the row requires is missing: conversation_id, message_id and
	// agent_id are uuid columns that reject empty strings, and a row without them is unattributable
	// anyway. Persistence is best-effort observability and must never block the build.
	if request.ConversationId == "" || request.MessageId == "" || request.AgentId == "" || request.AccountId == "" {
		return
	}
	dao := core.GetConversationDao()
	if dao == nil {
		return
	}
	status := toolcore.NBToolResponseStatusSuccess
	if strings.HasPrefix(observation, "Error") || strings.Contains(observation, "Validation FAILED") || strings.HasPrefix(observation, "Unknown tool") {
		status = toolcore.NBToolResponseStatusError
	}
	// Fresh tool_id per call so each iteration is its own row (the upsert key includes tool_id).
	if err := dao.SaveConversationToolCall(
		request.ConversationId, request.AccountId, request.UserId, request.MessageId, request.AgentId,
		uuid.New().String(), toolName, toolInput, thought, "", observation,
		status, toolcore.NBToolTypeTool, nil, nil, nil,
	); err != nil {
		ctx.GetLogger().Warn("workflow_builder: failed to persist build tool call", "tool", toolName, "error", err)
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

	// Anti-thrash tracking: weaker models can loop on read-only inspection tools (e.g. repeated
	// get_task_schema) and burn every iteration without ever committing a mutation or finishing.
	// Track exact-duplicate calls and the length of the current read-only streak so we can nudge
	// the model decisively toward acting (mutate → validate → finalize) or finishing.
	toolCallSeen := map[string]int{}
	readOnlyToolStreak := 0

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
		thought := common.XmlExtractTagContent(content, "thought")
		observation := a.executeWorkflowTool(ctx, toolName, toolInput, cachedTaskTypes)
		a.persistBuildToolCall(ctx, request, toolName, toolInput, thought, observation)

		// Anti-thrash: detect an exact-duplicate call or a long run of read-only inspection with no
		// progress, and append a decisive nudge so weak models stop exploring and either apply the
		// change (mutate → validate → finalize) or emit their <final_answer>.
		sig := toolName + "|" + strings.TrimSpace(toolInput)
		toolCallSeen[sig]++
		switch toolName {
		case "get_task_schema", "get_task", "list_tasks", "list_executions", "get_execution":
			readOnlyToolStreak++
		default:
			readOnlyToolStreak = 0
		}
		nudge := ""
		switch {
		case toolCallSeen[sig] >= 2:
			nudge = "\nNOTE: you already ran this exact tool call — the result is unchanged. Do NOT repeat it. Take the next concrete step now: apply the requested change (add_task / modify_task / delete_task), then validate, then finalize. If no change is needed, emit your <final_answer>."
		case readOnlyToolStreak >= 4:
			nudge = "\nNOTE: you have gathered enough information. Stop inspecting and act now: apply the requested change (add_task / modify_task / delete_task), then validate, then finalize. If no change is needed, emit your <final_answer>."
		}

		// Trim content to just the tool call XML before appending to message history.
		// This prevents context bloat from any extra content the LLM may have generated.
		trimmedContent := content
		if idx := strings.Index(content, "</thought_action>"); idx >= 0 {
			trimmedContent = content[:idx+len("</thought_action>")]
		}

		// Append assistant response + observation to message history
		messages = append(messages,
			llms.TextParts(llms.ChatMessageTypeAI, trimmedContent),
			llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("<observation>%s</observation>%s", observation, nudge)),
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

// ==================== EDIT MODE (AGENTIC) ====================

// handleEditEntry handles any change to an EXISTING automation — whether debugging a failure or
// adding/modifying/removing functionality. It fetches the current definition, loads it into the
// working workflow, and runs ONE agentic tool loop. The agent itself decides, from the user's
// request, whether to gather execution evidence (debug) or design a feature change (enhance) — there
// is no hardcoded "find the failed run" gate, so an enhancement request is never dead-ended with
// "No failed runs found". The edited definition is funnelled through the same checkMissingConfigs →
// finalizeWithAutoSave path as a build, so it auto-saves and the canvas refreshes; the user reviews
// the result on the canvas and Publishes.
func (a *WorkflowBuilderAgent) handleEditEntry(ctx *security.RequestContext, request core.NBAgentRequest, workflowId string) (core.NBAgentResponse, error) {
	// Fail fast at the boundary before any tenant-scoped fetch/write: a malformed workflow id would
	// only 404 later, and an empty tenant id would issue an unscoped runbook request. Reject both up
	// front (fail-closed) so a caller without proper tenant context can't reach edit logic.
	if _, err := uuid.Parse(workflowId); err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("handleEditEntry: invalid workflow id %q: %w", workflowId, err)
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return core.NBAgentResponse{}, fmt.Errorf("handleEditEntry: empty tenant id; refusing tenant-scoped workflow fetch")
	}

	// Fetch existing workflow definition
	workflowResp, err := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows/%s", workflowId), nil, a.accountId, tenantId, ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("failed to fetch workflow %s: %w", workflowId, err)
	}

	// Load into workingWorkflow so the loop's read/mutate tools operate on the current definition.
	var workflow map[string]interface{}
	if err := json.Unmarshal(workflowResp, &workflow); err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("failed to parse workflow JSON: %w", err)
	}
	a.state.WorkingWorkflow = workflow
	a.state.WorkflowId = workflowId
	// Mode "fix" keeps autoSaveWorkflow on the PUT-update path (vs POST-create) and makes
	// finalizeWithAutoSave persist the edit server-side so the canvas can refresh.
	a.state.Mode = "fix"
	a.state.OriginalQuery = request.Query

	// errorContext (from the UI) and a targeted execution id let the agent jump straight to the
	// relevant run when the user is debugging a specific failure; both are optional.
	errorContext := request.QueryContext
	targetExecutionId := request.QueryConfig.ExecutionId
	a.state.ExecutionId = targetExecutionId

	schema := getWorkflowSchema()
	systemPrompt := getEditSystemPrompt(errorContext, targetExecutionId, schema)
	userMessage := fmt.Sprintf("Apply the user's request to the existing automation: %s", request.Query)

	workflowJSON, err := a.runToolLoop(ctx, request, systemPrompt, userMessage)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("agentic edit failed: %w", err)
	}

	// Same missing-config detection + cloud-account-id resolution + auto-save as the build path.
	return a.checkMissingConfigs(ctx, request, workflowJSON)
}
