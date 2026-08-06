package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// Each automation an account has opted in to AI invocation becomes a tool in its
// own right, described in the words its author wrote.
//
// Why this rather than leaving workflow_search as the only route: an automation
// then competes for selection the same way every other capability does, so a
// user who describes a problem and never says the word "automation" can still
// reach it. Measured before this existed, four phrasings of a bare symptom all
// went to kubectl and workflow_search was never called once.
//
// The second reason is independent of discovery and arguably worth more: the
// automation declares its inputs as a real schema. workflow_trigger takes a
// free-form `inputs` blob the model assembles by hand, and in testing it
// silently dropped an optional input. Typed parameters are checked by machinery
// that already exists.
//
// This was tried once before and abandoned — ToolExecutorTypeWorkflow, still in
// the codebase as a deprecated executor type. It died of adoption friction
// rather than design: it needed a human to hand-create a tool per runbook, and
// the 16 that exist in prod are all test junk while a customer with 88 real
// automations has none. Deriving them from ai_invocable removes that step.
//
// workflow_search stays. It shares the same eligibility source and is still the
// right tool when an account's automations are not all preloaded.

const (
	// maxAutomationTools bounds how many automations one account contributes.
	// Real accounts sit far below this — the largest eligible count observed in
	// prod is 15, and that is before the opt-in filter — so today this never
	// binds. It exists because an unbounded list is how a tool catalog degrades
	// later without anyone noticing, and because what gets dropped is logged.
	//
	// It MUST stay strictly below runbook-server's maxAISearchLimit (25), which
	// clamps the limit query parameter. The fetch below asks for one past this
	// cap so an overflowing account can be detected rather than silently
	// truncated; if the two constants are equal the server clamps the +1 away,
	// candidates can never exceed the cap, and the detection — along with the
	// warning it exists to emit — is unreachable code. That is exactly what 25
	// here did. Nothing enforces this across the two services, so it is written
	// down in both places and pinned by TestAutomationFetchLimitStaysBelowServerClamp.
	maxAutomationTools = 20

	// automationToolCacheTTL is how long an account's automation tools are held.
	// llm-server cannot observe an edit in runbook-server, so this TTL is the
	// real staleness bound; RegisterToolCacheInvalidator only covers changes
	// made in this process.
	//
	// Staleness is safe rather than merely tolerable: a tool left behind for a
	// since-revoked automation still hits assertAIInvocationAllowed server-side,
	// which refuses it as not_opted_in. The worst case is a confusing refusal,
	// never an unauthorised run.
	automationToolCacheTTL = 5 * time.Minute

	// automationToolFailureTTL is how long a failed load is remembered. Failures
	// are cached far more briefly than successes so a transient error cannot
	// blank an account's automations for the full TTL — but they ARE cached,
	// because this source is consulted on every tool enumeration and an account
	// that is failing produced 14 requests to runbook-server in one conversation.
	automationToolFailureTTL = 30 * time.Second

	// automationToolPrefix namespaces these tools so they cannot collide with a
	// built-in, and so the model can see at a glance what kind of thing it is.
	automationToolPrefix = "automation_"

	// defaultReasonArg is the required "why did you pick this" parameter. See the
	// InputSchema comment. An automation that declares an input of the same name
	// pushes it to fallbackReasonArg rather than losing either field.
	defaultReasonArg  = "reason"
	fallbackReasonArg = "ai_reason"
)

// automationTool is one AI-invocable automation, exposed as a tool.
type automationTool struct {
	accountId string
	id        string
	name      string
	toolName  string
	// description is what the author wrote for the AI, which is what actually
	// drives selection.
	description string
	humanDesc   string
	inputs      []automationInput
	// reasonArg is the parameter name this tool uses for the required reasoning
	// field, resolved per automation so a declared input never loses to it.
	reasonArg string
}

type automationInput struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type automationCandidate struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	LLMDescription string            `json:"llm_description"`
	Inputs         []automationInput `json:"inputs,omitempty"`
}

type automationSearchResponse struct {
	Workflows      []automationCandidate `json:"workflows"`
	TotalInvocable int                   `json:"total_invocable"`
}

// resolveTenantForAccount is indirected through a variable so the request shapes
// this file builds can be tested without a metastore. Production always uses the
// real lookup; only tests replace it.
var resolveTenantForAccount = security.GetTenantIdFromAccountId

func init() {
	core.RegisterAutomationToolSource(listAutomationTools)
	core.RegisterToolCacheInvalidator(func(accountId string) {
		automationToolCacheInstance.delete(accountId)
	})
}

// --- cache ---

type automationCacheEntry struct {
	tools  []core.NBTool
	expiry time.Time
}

type automationToolCache struct {
	mutex sync.RWMutex
	data  map[string]automationCacheEntry
}

var automationToolCacheInstance = &automationToolCache{
	data: make(map[string]automationCacheEntry),
}

func (c *automationToolCache) get(accountId string) ([]core.NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *automationToolCache) set(accountId string, tools []core.NBTool, ttl time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = automationCacheEntry{tools: tools, expiry: time.Now().Add(ttl)}
}

func (c *automationToolCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

// --- discovery ---

// listAutomationTools returns the account's AI-invocable automations as tools.
//
// The source is the same `workflows/ai-search` endpoint workflow_search uses,
// called with no query so it returns everything eligible. That endpoint already
// applies the feature flag and every rule the run-time gate applies, and reads
// the LIVE version rather than the draft — so nothing here has to re-derive
// eligibility, and an account without the flag simply gets no tools.
func listAutomationTools(accountId string) []core.NBTool {
	if accountId == "" {
		return nil
	}
	if tools, ok := automationToolCacheInstance.get(accountId); ok {
		return tools
	}

	candidates, err := fetchAIInvocableAutomations(accountId)
	if err != nil {
		// Cache the emptiness briefly rather than not at all. Not caching keeps
		// the "a transient error must not blank an account for the full TTL"
		// property, but this source runs on every enumeration, so a failing
		// account re-hits runbook-server continuously — 14 times in one observed
		// conversation. A short window keeps both properties.
		slog.Warn("automation tools: could not load automations for account",
			"account_id", accountId, "error", err, "retry_after", automationToolFailureTTL)
		automationToolCacheInstance.set(accountId, nil, automationToolFailureTTL)
		return nil
	}

	tools := buildAutomationTools(accountId, candidates)
	automationToolCacheInstance.set(accountId, tools, automationToolCacheTTL)
	return tools
}

func fetchAIInvocableAutomations(accountId string) ([]automationCandidate, error) {
	// The tenant has to be resolved from the account rather than passed in.
	// RegisterAccountToolSource hands providers an accountId only, and one of its
	// two call sites (GetNBTool) has no request context to thread — so the tenant
	// simply does not exist at this boundary. Omitting it is not an option:
	// runbook-server refuses any request it cannot attribute to a tenant, so an
	// empty value makes every fetch 401 and the whole feature silently inert.
	//
	// No user id, though: this is tool enumeration, not an action taken on
	// anyone's behalf. The trigger call later carries the real user.
	tenantId, err := resolveTenantForAccount(accountId)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant for account %s: %w", accountId, err)
	}

	// Fetch one past the cap so an account that overflows can be detected and
	// reported, rather than silently truncated by the server's own clamp.
	raw, err := DoRunbookRequest("GET", fmt.Sprintf("workflows/ai-search?limit=%d", maxAutomationTools+1),
		nil, accountId, tenantId, "")
	if err != nil {
		return nil, fmt.Errorf("fetch ai-invocable automations: %w", err)
	}
	var resp automationSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse ai-invocable automations: %w", err)
	}
	return resp.Workflows, nil
}

// buildAutomationTools turns candidates into tools, giving each a stable unique
// name and enforcing the cap.
func buildAutomationTools(accountId string, candidates []automationCandidate) []core.NBTool {
	tools := make([]core.NBTool, 0, len(candidates))
	used := make(map[string]bool, len(candidates))

	for i, c := range candidates {
		if i >= maxAutomationTools {
			// Say what was dropped. A silently truncated list reads as "this
			// account has no more automations", which is a different and wrong
			// statement.
			slog.Warn("automation tools: per-account cap reached, automations not exposed",
				"account_id", accountId, "cap", maxAutomationTools, "dropped", len(candidates)-maxAutomationTools)
			break
		}
		if c.ID == "" || strings.TrimSpace(c.LLMDescription) == "" {
			// The description is what the model selects on; without it the tool
			// is noise. The server should not return these, so this is a guard
			// rather than an expected branch.
			continue
		}
		tools = append(tools, &automationTool{
			accountId:   accountId,
			id:          c.ID,
			name:        c.Name,
			toolName:    automationToolName(c, used),
			description: c.LLMDescription,
			humanDesc:   c.Description,
			inputs:      c.Inputs,
			reasonArg:   reasonArgFor(c.Inputs),
		})
	}
	return tools
}

var nonToolNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// automationToolName derives a stable tool name from the automation's name.
//
// The author's wording is used rather than the id because the name is part of
// what the model selects on — "automation_restart_crashlooping_pods" says what
// an opaque uuid cannot. Collisions (two automations named alike, or names that
// slug identically) take a short id suffix, so a name is never silently shared
// by two automations. Renaming an automation changes its tool name; that churn
// is accepted, and the id travels in the description either way.
func automationToolName(c automationCandidate, used map[string]bool) string {
	slug := nonToolNameChars.ReplaceAllString(strings.ToLower(c.Name), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		slug = "workflow"
	}
	const maxSlug = 48
	if len(slug) > maxSlug {
		slug = strings.Trim(slug[:maxSlug], "_")
	}

	name := automationToolPrefix + slug
	if used[name] {
		suffix := strings.ReplaceAll(c.ID, "-", "")
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		name = fmt.Sprintf("%s_%s", name, suffix)
	}
	used[name] = true
	return name
}

// reasonArgFor picks the parameter name for the required reasoning field.
//
// An automation is free to declare its own input called "reason" — "reason for
// restart", for an audit trail, is a natural thing to ask for. Reserving the
// name unconditionally would silently drop that input, which is precisely the
// failure this PR fixed twice elsewhere. So the reasoning field yields instead,
// and both survive.
func reasonArgFor(inputs []automationInput) string {
	declared := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		declared[in.ID] = true
	}
	if !declared[defaultReasonArg] {
		return defaultReasonArg
	}
	if !declared[fallbackReasonArg] {
		return fallbackReasonArg
	}
	// Both taken. Keep going rather than colliding: the automation's inputs are
	// the user's data and must win.
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", fallbackReasonArg, i)
		if !declared[candidate] {
			return candidate
		}
	}
}

// --- NBTool ---

func (t *automationTool) Name() string { return t.toolName }

func (t *automationTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *automationTool) Description() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Runs the team's %q automation. %s", t.name, t.description)
	if summary := strings.TrimSpace(t.humanDesc); summary != "" {
		fmt.Fprintf(&sb, " (%s)", summary)
	}
	sb.WriteString(" Runs with the user's own permissions after they confirm, and waits for the result.")
	return sb.String()
}

// InferToolRequestType classifies running an automation as a create so the
// executor gates it behind the ask-before-run confirmation and the create RBAC
// check. Absent this, auth_agent defaults an unclassified tool to Read, which
// would skip both — an automation runs real actions against real infrastructure
// and must never be reachable without a human saying yes.
func (t *automationTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeCreate, nil
}

// InputSchema declares the automation's own inputs as typed parameters, plus a
// required `reason`.
//
// The inputs are the point: workflow_trigger takes a free-form blob the model
// assembles by hand, and it silently dropped an optional input in testing.
// Declared here, a missing required input is caught by the same validation that
// catches it for every other tool.
//
// `reason` is required so the model cannot invoke an automation without saying
// what it believes is wrong and why this is the right response. Enforced by
// schema validation rather than by prompt wording, so it is not an instruction
// the model can quietly skip. Note the reason is currently recorded and passed
// along but NOT yet shown in the confirmation prompt — that prompt is generic to
// every write tool and changing it is #35403. Until that lands, this field's
// value is that the reasoning exists and is logged, not that the user sees it.
func (t *automationTool) InputSchema() core.ToolSchema {
	props := map[string]core.ToolSchemaProperty{
		t.reasonArg: {
			Type: core.ToolSchemaTypeString,
			Description: "Why you are running this automation: what you believe the problem is and what that " +
				"is based on. If you have not investigated, say so plainly (e.g. \"the user asked for this\" " +
				"or \"their message described crashlooping pods; I have not looked at the pods\").",
		},
	}
	required := []string{t.reasonArg}

	for _, in := range t.inputs {
		if in.ID == "" {
			continue
		}
		props[in.ID] = core.ToolSchemaProperty{
			Type:        automationInputSchemaType(in.Type),
			Description: in.Description,
			Default:     in.Default,
		}
		// An input with no default is required of the AI whether or not the author
		// ticked "required". Unticked-and-undefaulted is optional only in the sense
		// that the engine will not stop the run: it interpolates the empty value
		// into whatever the automation does, and this is a tool that CHANGES
		// things. A model that omits `service_name` restarts nothing, or worse,
		// everything. A human triggering from the UI is unaffected — this schema is
		// the AI's view alone — and an author who means an input to be genuinely
		// optional says so by giving it a default.
		if in.Required || in.Default == nil {
			required = append(required, in.ID)
		}
	}

	return core.ToolSchema{
		Type:       core.ToolSchemaTypeObject,
		Properties: props,
		Required:   required,
	}
}

// automationInputSchemaType maps a workflow input type onto a schema type.
// Unknown types become strings rather than erroring: an automation with an
// exotic input is still worth exposing, and the engine does its own coercion.
func automationInputSchemaType(t string) core.ToolSchemaType {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "int", "integer", "number", "float":
		return core.ToolSchemaTypeNumber
	case "bool", "boolean":
		return core.ToolSchemaTypeBoolean
	case "array", "list":
		return core.ToolSchemaTypeArray
	case "json", "object", "map":
		return core.ToolSchemaTypeObject
	default:
		return core.ToolSchemaTypeString
	}
}

func (t *automationTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	reason, _ := input.Arguments[t.reasonArg].(string)
	if strings.TrimSpace(reason) == "" {
		return core.NBToolResponse{}, fmt.Errorf(
			"%s is required: state what you believe the problem is and what that is based on before running an automation", t.reasonArg)
	}

	tenantId := ctx.Ctx.GetSecurityContext().GetTenantId()
	userId := ctx.Ctx.GetSecurityContext().GetUserId()
	// Same refusal workflow_trigger makes: runbook-server reads a missing user
	// as a system call and grants tenant-account-admin, so an unidentified run
	// would execute with more authority than whoever asked for it.
	if userId == "" || userId == security.GetSystemUserId() {
		return core.NBToolResponse{}, fmt.Errorf("cannot run an automation without an identified user")
	}

	inputs := map[string]any{}
	declared := map[string]struct{}{t.reasonArg: {}}
	accepted := make([]string, 0, len(t.inputs))
	for _, in := range t.inputs {
		if in.ID == "" {
			continue
		}
		declared[in.ID] = struct{}{}
		accepted = append(accepted, in.ID)
		if v, ok := input.Arguments[in.ID]; ok && v != nil {
			inputs[in.ID] = v
		}
	}

	// Reject arguments matching no declared input. The loop above copies declared
	// keys only, so a misspelled one is otherwise dropped in silence and the
	// automation runs with fewer inputs than the model believed it passed. That
	// surfaces as an opaque engine error from an unresolved template ("child
	// workflow execution error"), which names neither the parameter nor the
	// automation — so neither the model nor the user can see what went wrong.
	// Named here, the model corrects itself on the retry, exactly as it already
	// does for the required `reason`.
	var unknown []string
	for k := range input.Arguments {
		if _, ok := declared[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		sort.Strings(accepted)
		accepts := "this automation takes no inputs"
		if len(accepted) > 0 {
			accepts = "this automation accepts: " + strings.Join(accepted, ", ")
		}
		return core.NBToolResponse{}, fmt.Errorf(
			"%s: unknown parameter(s) %s — %s (plus the required %s). Re-issue the call using those exact names",
			t.toolName, strings.Join(unknown, ", "), accepts, t.reasonArg)
	}

	ctx.Ctx.GetLogger().Info("automation tool: running automation",
		"workflow_id", t.id, "tool", t.toolName, "account_id", t.accountId,
		"reason", reason, "input_count", len(inputs))

	// The trigger endpoint binds the request body *directly* as the inputs map —
	// it is not wrapped in an "inputs" key. Wrapping it means every declared
	// input arrives unset: a required one fails the run outright, and an
	// automation with only optional inputs runs silently on its defaults, which
	// is worse. workflow_trigger sends it flat for the same reason.
	raw, err := DoRunbookRequestWithContext(ctx.GoContext(), "POST",
		fmt.Sprintf("workflows/%s/trigger", t.id), inputs, t.accountId, tenantId, userId)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to run automation %q: %w", t.name, err)
	}

	var triggered struct {
		WorkflowID  string `json:"workflow_id"`
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(raw, &triggered); err != nil || triggered.ExecutionID == "" {
		// The run was started; only reading back its identifiers failed. Say so
		// rather than implying the automation did not run.
		return core.NBToolResponse{Data: string(raw), Type: core.NBToolResponseTypeJson}, nil
	}
	if triggered.WorkflowID == "" {
		triggered.WorkflowID = t.id
	}

	outcome := waitForExecution(ctx.GoContext(), triggered.WorkflowID, triggered.ExecutionID,
		t.accountId, tenantId, userId)
	out, err := json.Marshal(outcome)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("marshal automation outcome: %w", err)
	}
	return core.NBToolResponse{Data: string(out), Type: core.NBToolResponseTypeJson}, nil
}
