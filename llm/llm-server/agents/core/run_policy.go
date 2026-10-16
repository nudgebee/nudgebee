package core

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// A governed run is a conversation an extension owns. Its identity, a
// RunProfile, is written on the conversation once, when it is created, and
// only from a RunClaim on that request: request headers the app gateway never
// forwards, honoured only from a caller with the service token. Every path
// that runs an agent in the conversation (turn, resume, follow-up skip,
// queued message, recovery, sub-agent) reads the profile from the row, so the
// run is governed by exactly one policy: that of profile.Extension. Nothing a
// request carries can select, change or widen a profile.

// RunProfile is the identity of a governed run.
type RunProfile struct {
	// Extension owns the run; its registered AgentRunPolicy governs it.
	Extension string `json:"extension"`
	// Ref is the owner's reference for the run (opaque to core).
	Ref string `json:"ref,omitempty"`
	// Agent is the agent the run is pinned to.
	Agent string `json:"agent"`
}

// Value stores a RunProfile as JSON.
func (p RunProfile) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// nullRunProfile scans a nullable run_profile column.
type nullRunProfile struct{ p *RunProfile }

func (n *nullRunProfile) Scan(src any) error {
	n.p = nil
	var raw []byte
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("run_profile: unexpected type %T", src)
	}
	if len(raw) == 0 {
		return nil
	}
	var p RunProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("run_profile: %w", err)
	}
	n.p = &p
	return nil
}

func (n nullRunProfile) profile() *RunProfile { return n.p }

// RunClaim is a request to start a governed run, read from the request's
// X-NB-Run-* headers. The owning extension turns it into the run's profile
// (AgentRunPolicy.PrepareRun) or refuses it.
type RunClaim struct {
	Extension string
	Ref       string
	Agent     string
}

// Request headers that carry a RunClaim. The app gateway forwards a fixed
// header set that never includes them (a test in app/ checks it), and a claim
// is honoured only on a request that passed the service-token check.
const (
	RunClaimHeaderExtension = "X-NB-Run-Extension"
	RunClaimHeaderRef       = "X-NB-Run-Ref"
	RunClaimHeaderAgent     = "X-NB-Run-Agent"
)

// AgentRunPolicy is an extension's rules for the runs it owns. Core consults
// only the policy of the run's owner (RunProfileOf(ctx).Extension); every
// other registered policy is inert for that run. With no profile the run is
// ordinary and NoRunPolicy answers.
type AgentRunPolicy interface {
	// PrepareRun turns a claim for a new run into its profile, or refuses it.
	// Called once, before the conversation is written.
	PrepareRun(ctx *security.RequestContext, claim RunClaim, accountId string) (RunProfile, error)
	// StartAgent is called where an agent starts running in the run
	// (executeAgent), on every path. It returns the context and request to run
	// with, or an error that refuses the run.
	StartAgent(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest) (*security.RequestContext, NBAgentRequest, error)
	// ConversationTurn is called where a turn enters the conversation, before
	// anything is written for it. An error refuses the turn.
	ConversationTurn(ctx *security.RequestContext, agent NBAgent, conversation Conversation, userId string) (*security.RequestContext, error)
	// MayActOnConversation reports whether userId may act on the conversation
	// outside a turn, such as dismissing one of its follow-ups.
	MayActOnConversation(conversation Conversation, userId string) bool
	// OwnToolsOnly reports whether the run dispatches only to its agents' own
	// tools: no client tool from the request, no registry or custom-agent
	// fallback for other names.
	OwnToolsOnly(ctx *security.RequestContext) bool
	// CheckTool decides a tool call (see ToolVerdict).
	CheckTool(ctx *security.RequestContext, call ToolCall) ToolVerdict
	// AgentTools is a custom agent's tool set in the run, given the tools on
	// its list.
	AgentTools(ctx *security.RequestContext, agent NBAgent, configured []toolcore.NBTool) []toolcore.NBTool
	// AnswerInstructions is an instruction to add to agent's system prompt and
	// fallback summary in the run, or "".
	AnswerInstructions(ctx *security.RequestContext, agent NBAgent) string
	// MaxTurns caps agent's iterations in the run, or 0.
	MaxTurns(ctx *security.RequestContext, agent NBAgent) int
	// AddTokens counts an LLM call's tokens against the run's budget.
	AddTokens(ctx *security.RequestContext, tokens int)
	// BudgetSpent reports whether the run has spent its token budget.
	BudgetSpent(ctx *security.RequestContext) (used, limit int64, spent bool)
	// FinishCutShort rewrites the answer of agent's run that stopped on a cap
	// (budgetStopped: the token budget, else its turns).
	FinishCutShort(ctx *security.RequestContext, agent NBAgent, answer string, used, limit int64, budgetStopped bool) string
}

// ToolCall is a tool call put to AgentRunPolicy.CheckTool.
type ToolCall struct {
	Agent NBAgent
	Tool  toolcore.NBTool
	// Name is the tool as the agent called it.
	Name  string
	Input string
	// RequestType is what the call does; "" when no classifier decided.
	RequestType    toolcore.ToolRequestType
	AccountId      string
	SessionId      string
	ConversationId string
}

// ToolVerdictKind is how a tool call goes ahead.
type ToolVerdictKind int

const (
	// ToolCallAsUsual: core decides as it does on its own (approval prompt,
	// read-only refusal, or run).
	ToolCallAsUsual ToolVerdictKind = iota
	// ToolCallRun: the call runs without an approval prompt, even in a
	// read-only run.
	ToolCallRun
	// ToolCallRefuseReadOnly: the call is refused as a read-only run refuses
	// a write.
	ToolCallRefuseReadOnly
	// ToolCallRefuse: the call is refused, and Observation tells the agent why.
	ToolCallRefuse
)

// ToolVerdict is AgentRunPolicy.CheckTool's answer.
type ToolVerdict struct {
	Kind        ToolVerdictKind
	Observation string
}

// NoRunPolicy governs an ordinary run (no profile): every method answers the
// way core behaves on its own, and it starts no governed run. A test policy
// can embed it and override only what it exercises.
type NoRunPolicy struct{}

func (NoRunPolicy) PrepareRun(_ *security.RequestContext, claim RunClaim, _ string) (RunProfile, error) {
	return RunProfile{}, fmt.Errorf("run claim for %q: no such extension in this build", claim.Extension)
}
func (NoRunPolicy) StartAgent(ctx *security.RequestContext, _ NBAgent, request NBAgentRequest) (*security.RequestContext, NBAgentRequest, error) {
	return ctx, request, nil
}
func (NoRunPolicy) ConversationTurn(ctx *security.RequestContext, _ NBAgent, _ Conversation, _ string) (*security.RequestContext, error) {
	return ctx, nil
}
func (NoRunPolicy) MayActOnConversation(Conversation, string) bool              { return true }
func (NoRunPolicy) OwnToolsOnly(*security.RequestContext) bool                  { return false }
func (NoRunPolicy) CheckTool(*security.RequestContext, ToolCall) ToolVerdict    { return ToolVerdict{} }
func (NoRunPolicy) AnswerInstructions(*security.RequestContext, NBAgent) string { return "" }
func (NoRunPolicy) MaxTurns(*security.RequestContext, NBAgent) int              { return 0 }
func (NoRunPolicy) AddTokens(*security.RequestContext, int)                     {}
func (NoRunPolicy) BudgetSpent(*security.RequestContext) (int64, int64, bool)   { return 0, 0, false }
func (NoRunPolicy) AgentTools(_ *security.RequestContext, _ NBAgent, configured []toolcore.NBTool) []toolcore.NBTool {
	return configured
}
func (NoRunPolicy) FinishCutShort(_ *security.RequestContext, _ NBAgent, answer string, _, _ int64, _ bool) string {
	return answer
}

// unregisteredRunPolicy governs a run whose owner this build does not
// include (a profile written by a build with that extension): every run in it
// is refused rather than run with no rules.
type unregisteredRunPolicy struct {
	NoRunPolicy
	extension string
}

func (p unregisteredRunPolicy) refusal() error {
	return fmt.Errorf("this conversation belongs to the %q extension, which this build does not include", p.extension)
}
func (p unregisteredRunPolicy) StartAgent(ctx *security.RequestContext, _ NBAgent, request NBAgentRequest) (*security.RequestContext, NBAgentRequest, error) {
	return ctx, request, p.refusal()
}
func (p unregisteredRunPolicy) ConversationTurn(ctx *security.RequestContext, _ NBAgent, _ Conversation, _ string) (*security.RequestContext, error) {
	return ctx, p.refusal()
}
func (p unregisteredRunPolicy) MayActOnConversation(Conversation, string) bool { return false }

// runPolicies are the registered policies by extension name. Written only
// from extensions' init (RegisterAgentRunPolicy), read without a lock.
var runPolicies = map[string]AgentRunPolicy{}

// RegisterAgentRunPolicy registers the run policy of the extension named
// extension; nil removes it. Call it only from the extension's init, before
// any request is served.
func RegisterAgentRunPolicy(extension string, policy AgentRunPolicy) {
	extension = strings.TrimSpace(extension)
	if policy == nil {
		delete(runPolicies, extension)
		return
	}
	runPolicies[extension] = policy
}

// policyOf is the policy governing a run with profile: NoRunPolicy for an
// ordinary run, a refusing policy for an owner this build lacks.
func policyOf(profile *RunProfile) AgentRunPolicy {
	if profile == nil || strings.TrimSpace(profile.Extension) == "" {
		return NoRunPolicy{}
	}
	if policy, ok := runPolicies[strings.TrimSpace(profile.Extension)]; ok {
		return policy
	}
	return unregisteredRunPolicy{extension: profile.Extension}
}

// policyFor is the policy governing the run ctx belongs to.
func policyFor(ctx *security.RequestContext) AgentRunPolicy {
	return policyOf(RunProfileOf(ctx))
}

type runProfileKey struct{}

// WithRunProfile marks ctx as part of the governed run profile (nil: an
// ordinary run). Agents started from ctx (sub-agents, resumed agents)
// inherit it.
func WithRunProfile(ctx *security.RequestContext, profile *RunProfile) *security.RequestContext {
	if ctx == nil || ctx.GetContext() == nil {
		return ctx
	}
	current := RunProfileOf(ctx)
	if current == profile || (current != nil && profile != nil && *current == *profile) {
		return ctx
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), runProfileKey{}, profile))
}

// RunProfileOf is the profile of the governed run ctx belongs to, or nil.
func RunProfileOf(ctx *security.RequestContext) *RunProfile {
	if ctx == nil || ctx.GetContext() == nil {
		return nil
	}
	profile, _ := ctx.GetContext().Value(runProfileKey{}).(*RunProfile)
	return profile
}

// PrepareRun turns claim into the profile of a new governed run, through the
// claimed extension's policy. A claim naming an extension this build lacks is
// refused.
func PrepareRun(ctx *security.RequestContext, claim RunClaim, accountId string) (RunProfile, error) {
	claim.Extension = strings.TrimSpace(claim.Extension)
	policy, ok := runPolicies[claim.Extension]
	if !ok || claim.Extension == "" {
		return NoRunPolicy{}.PrepareRun(ctx, claim, accountId)
	}
	profile, err := policy.PrepareRun(ctx, claim, accountId)
	if err != nil {
		return RunProfile{}, err
	}
	if profile.Extension != claim.Extension || strings.TrimSpace(profile.Agent) == "" {
		return RunProfile{}, fmt.Errorf("run claim for %q: the extension returned an invalid profile", claim.Extension)
	}
	return profile, nil
}

// MayActOnConversation reports whether userId may act on conversation outside
// a turn (see AgentRunPolicy.MayActOnConversation), by the policy of the
// conversation's owner.
func MayActOnConversation(conversation Conversation, userId string) bool {
	return policyOf(conversation.RunProfile).MayActOnConversation(conversation, userId)
}

// requiredExtension is the extension a custom agent's stored record says it
// runs under (config requires_extension), or "".
func requiredExtension(agent NBAgent) string {
	definition, _, ok := CustomAgentDefinition(agent)
	if !ok {
		return ""
	}
	extension, _ := definition.Config["requires_extension"].(string)
	return strings.TrimSpace(extension)
}

// CheckAgentInRun refuses an agent whose record names an extension in a run
// that extension does not own: such an agent runs only in its own
// extension's governed runs, never as an ordinary agent or in another
// extension's run. Any other agent may run anywhere, under the run's policy.
func CheckAgentInRun(agent NBAgent, profile *RunProfile) error {
	extension := requiredExtension(agent)
	if extension == "" {
		return nil
	}
	if _, ok := runPolicies[extension]; !ok {
		return fmt.Errorf("agent %s needs the %q extension, which this build does not include", agent.GetName(), extension)
	}
	if profile == nil || strings.TrimSpace(profile.Extension) != extension {
		return fmt.Errorf("agent %s runs only in runs of the %q extension; it cannot be called here", agent.GetName(), extension)
	}
	return nil
}

// ToolRefusedError is a tool call the run policy refused before it was
// classified; the planner turns it into a failed step with Observation.
type ToolRefusedError struct {
	Tool        string
	Observation string
}

func (e *ToolRefusedError) Error() string {
	return fmt.Sprintf("tool %s refused by the run policy", e.Tool)
}

// toolCallOf is the ToolCall for action on tool in request.
func toolCallOf(agent NBAgent, request NBAgentRequest, tool toolcore.NBTool, action NBAgentPlannerToolAction, requestType toolcore.ToolRequestType) ToolCall {
	return ToolCall{
		Agent:          agent,
		Tool:           tool,
		Name:           action.Tool,
		Input:          action.ToolInput,
		RequestType:    requestType,
		AccountId:      request.AccountId,
		SessionId:      request.SessionId,
		ConversationId: request.ConversationId,
	}
}

// CustomAgentDefinition returns the stored definition and account of a
// database-backed custom agent, and whether agent is one.
func CustomAgentDefinition(agent NBAgent) (AgentDto, string, bool) {
	custom, ok := agent.(*nbCustomAgent)
	if !ok || custom == nil {
		return AgentDto{}, "", false
	}
	return custom.agent, custom.accountId, true
}

// NewCustomAgentWithTools is a custom agent whose tool list is already
// resolved to resolved, so GetSupportedTools looks nothing up.
func NewCustomAgentWithTools(definition AgentDto, accountId string, resolved []toolcore.NBTool) NBAgent {
	return &nbCustomAgent{agent: definition, accountId: accountId, tools: resolved}
}
