package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// These tests cover core's side of governed runs: a run's owner comes only
// from the profile stored on its conversation, exactly one policy (the
// owner's) governs it, and with no profile every seam behaves as core does on
// its own. The one real policy is tested in its own package.

const (
	policyTestAccount = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	testExt           = "test_ext"
)

// fakeRunPolicy is test_ext's policy. Core consults it only for runs whose
// profile names test_ext, so its rules apply unconditionally: read-only,
// own tools only, writes refused, and (unless allowUnclassified) nothing
// unclassified. ownAutomation names a tool that runs without approval.
type fakeRunPolicy struct {
	NoRunPolicy
	allowUnclassified bool
	ownAutomation     string
	spent             bool
	tokens            int
	mayAct            bool
	prepared          []RunClaim
	prepareErr        error
	prepareExtension  string
}

// governedRun is ctx as part of a test_ext run pinned to the agent "governed".
func governedRun(ctx *security.RequestContext) *security.RequestContext {
	return WithRunProfile(WithReadOnlyRun(ctx), &RunProfile{Extension: testExt, Ref: "run-1", Agent: "governed"})
}

func (f *fakeRunPolicy) PrepareRun(_ *security.RequestContext, claim RunClaim, _ string) (RunProfile, error) {
	f.prepared = append(f.prepared, claim)
	if f.prepareErr != nil {
		return RunProfile{}, f.prepareErr
	}
	extension := claim.Extension
	if f.prepareExtension != "" {
		extension = f.prepareExtension
	}
	return RunProfile{Extension: extension, Ref: claim.Ref, Agent: claim.Agent}, nil
}
func (f *fakeRunPolicy) StartAgent(ctx *security.RequestContext, _ NBAgent, request NBAgentRequest) (*security.RequestContext, NBAgentRequest, error) {
	request.ClientTools = nil
	request.QueryConfig.ToolConfirmations = nil
	return ctx, request, nil
}
func (f *fakeRunPolicy) MayActOnConversation(Conversation, string) bool { return f.mayAct }
func (f *fakeRunPolicy) OwnToolsOnly(*security.RequestContext) bool     { return true }
func (f *fakeRunPolicy) CheckTool(_ *security.RequestContext, call ToolCall) ToolVerdict {
	if f.ownAutomation != "" && call.Tool.Name() == f.ownAutomation {
		return ToolVerdict{Kind: ToolCallRun}
	}
	switch call.RequestType {
	case "":
		if f.allowUnclassified {
			return ToolVerdict{}
		}
		return ToolVerdict{Kind: ToolCallRefuse, Observation: "Not run: " + call.Name + " is unclassified in a governed run."}
	case toolcore.ToolRequestTypeRead:
		return ToolVerdict{}
	default:
		return ToolVerdict{Kind: ToolCallRefuseReadOnly}
	}
}
func (f *fakeRunPolicy) AddTokens(_ *security.RequestContext, tokens int) { f.tokens += tokens }
func (f *fakeRunPolicy) BudgetSpent(*security.RequestContext) (int64, int64, bool) {
	return 12, 10, f.spent
}
func (f *fakeRunPolicy) AnswerInstructions(_ *security.RequestContext, agent NBAgent) string {
	if agent.GetName() == "governed" {
		return "END-WITH-POLICY-JSON"
	}
	return ""
}
func (f *fakeRunPolicy) FinishAnswer(_ *security.RequestContext, _ NBAgent, answer string, used, limit int64, cutShort, budgetStopped bool) string {
	if !cutShort {
		return fmt.Sprintf("%s [final %d/%d]", answer, used, limit)
	}
	return fmt.Sprintf("%s [cut budget=%v %d/%d]", answer, budgetStopped, used, limit)
}

// withFakeRunPolicy registers policy for the test under extension.
func withFakeRunPolicy(t *testing.T, policy AgentRunPolicy, extension ...string) {
	t.Helper()
	name := testExt
	if len(extension) > 0 {
		name = extension[0]
	}
	RegisterAgentRunPolicy(name, policy)
	t.Cleanup(func() { RegisterAgentRunPolicy(name, nil) })
}

// governedAgent is an agent named "governed", the agent test_ext runs pin.
type governedAgent struct{ MockAgent }

func (*governedAgent) GetName() string { return "governed" }

func policyActionRequest() NBAgentRequest {
	return NBAgentRequest{AccountId: policyTestAccount, UserId: "dddddddd-dddd-dddd-dddd-dddddddddddd", ConversationId: "conv-1", MessageId: "msg-1"}
}

// registeredOnlyTool is a tool reachable only through the tool registry.
const registeredOnlyTool = "policy_test_registry_only_tool"

func init() {
	toolcore.RegisterNBToolFactory(registeredOnlyTool, func(string) (toolcore.NBTool, error) {
		return &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: registeredOnlyTool}}, nil
	})
}

// fakePolicyContext is what the run's policy gives agent when it starts.
func fakePolicyContext(t *testing.T, ctx *security.RequestContext, agent NBAgent) *security.RequestContext {
	t.Helper()
	ctx, _, err := policyFor(ctx).StartAgent(ctx, agent, policyActionRequest())
	require.NoError(t, err)
	return ctx
}

func requireNoPolicy(t *testing.T) {
	t.Helper()
	require.Empty(t, runPolicies, "core tests run without a registered policy")
}

// customAgentWith is a stored custom agent with config.
func customAgentWith(config map[string]any) NBAgent {
	return &nbCustomAgent{agent: AgentDto{Name: "c", SystemPrompt: "You help.", Config: config}}
}

// With no profile every seam answers as core does on its own, even with a
// policy registered: an ordinary run is never governed.
func TestOrdinaryRunIsNeverGoverned(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{spent: true})
	ctx := security.NewRequestContextForSuperAdmin()
	request := policyActionRequest()
	request.SessionId = "governed-" + uuid.NewString()
	request.ClientTools = []toolcore.NBToolCommand{{Name: "client_tool"}}
	request.QueryConfig.ToolConfirmations = map[string]string{"write_tool": "ok"}

	require.Nil(t, RunProfileOf(ctx))
	policy := policyFor(ctx)
	assert.Equal(t, NoRunPolicy{}, policy)
	gotCtx, gotRequest, err := policy.StartAgent(ctx, &governedAgent{}, request)
	require.NoError(t, err)
	assert.Same(t, ctx, gotCtx)
	assert.Equal(t, request, gotRequest, "a session id or agent name selects nothing")
	assert.False(t, IsReadOnlyRun(gotCtx))
	assert.True(t, MayActOnConversation(Conversation{ID: uuid.New(), UserID: uuid.New(), SessionID: request.SessionId}, uuid.NewString()))
	custom := customAgentWith(map[string]any{"max_turns": 3})
	configured := []toolcore.NBTool{&MockContextCapturingTool{NameVal: "t"}}
	assert.Equal(t, configured, policy.AgentTools(ctx, custom, configured))
	assert.Equal(t, "answer", policy.FinishAnswer(ctx, custom, "answer", 1, 1, true, true))
	assert.Equal(t, "answer", policy.FinishAnswer(ctx, custom, "answer", 1, 1, false, false))
	assert.Empty(t, policy.AnswerInstructions(ctx, custom))
	assert.Zero(t, policy.MaxTurns(ctx, custom))
	_, _, spent := policy.BudgetSpent(ctx)
	assert.False(t, spent)
	assert.Equal(t, ToolVerdict{}, policy.CheckTool(ctx, ToolCall{Agent: custom, Tool: configured[0], RequestType: toolcore.ToolRequestTypeCreate}))
}

// Each run is governed by exactly one policy: its profile's owner. A profile
// naming an extension this build lacks refuses the run.
func TestOnePolicyPerRun(t *testing.T) {
	a, b := &fakeRunPolicy{mayAct: true}, &fakeRunPolicy{}
	withFakeRunPolicy(t, a, "ext_a")
	withFakeRunPolicy(t, b, "ext_b")
	base := security.NewRequestContextForSuperAdmin()
	assert.Same(t, a, policyFor(WithRunProfile(base, &RunProfile{Extension: "ext_a", Agent: "x"})))
	assert.Same(t, b, policyFor(WithRunProfile(base, &RunProfile{Extension: "ext_b", Agent: "x"})))
	assert.Equal(t, NoRunPolicy{}, policyFor(base))

	missing := WithRunProfile(base, &RunProfile{Extension: "ext_gone", Agent: "x"})
	_, _, err := policyFor(missing).StartAgent(missing, &MockAgent{}, policyActionRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"ext_gone" extension, which this build does not include`)
	_, err = policyFor(missing).ConversationTurn(missing, &MockAgent{}, Conversation{}, "u")
	require.Error(t, err)
	assert.False(t, MayActOnConversation(Conversation{RunProfile: &RunProfile{Extension: "ext_gone"}}, "u"))
	assert.True(t, MayActOnConversation(Conversation{RunProfile: &RunProfile{Extension: "ext_a"}}, "u"), "the row's owner decides")
	assert.False(t, MayActOnConversation(Conversation{RunProfile: &RunProfile{Extension: "ext_b"}}, "u"))
}

// An agent whose record names an extension runs only in that extension's
// runs; any other agent runs anywhere.
func TestCheckAgentInRun(t *testing.T) {
	requireNoPolicy(t)
	bound := customAgentWith(map[string]any{"requires_extension": testExt})
	err := CheckAgentInRun(bound, &RunProfile{Extension: testExt})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `needs the "test_ext" extension, which this build does not include`)

	withFakeRunPolicy(t, &fakeRunPolicy{})
	require.NoError(t, CheckAgentInRun(bound, &RunProfile{Extension: testExt, Agent: "c"}))
	for name, profile := range map[string]*RunProfile{"ordinary run": nil, "another extension's run": {Extension: "other_ext"}} {
		err := CheckAgentInRun(bound, profile)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `runs only in runs of the "test_ext" extension`, name)
	}
	for _, agent := range []NBAgent{customAgentWith(map[string]any{"max_turns": 3}), customAgentWith(map[string]any{"requires_extension": "  "}), &MockAgent{}} {
		require.NoError(t, CheckAgentInRun(agent, nil))
		require.NoError(t, CheckAgentInRun(agent, &RunProfile{Extension: testExt}), "an unbound agent runs under the run's policy")
	}
}

// A claim becomes a profile only through the claimed extension, and only with
// the profile that extension returns for itself.
func TestPrepareRun(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := PrepareRun(ctx, RunClaim{Extension: testExt, Ref: "r", Agent: "a"}, policyTestAccount)
	require.Error(t, err, "no such extension")

	fake := &fakeRunPolicy{}
	withFakeRunPolicy(t, fake)
	profile, err := PrepareRun(ctx, RunClaim{Extension: " test_ext ", Ref: "r", Agent: "a"}, policyTestAccount)
	require.NoError(t, err)
	assert.Equal(t, RunProfile{Extension: testExt, Ref: "r", Agent: "a"}, profile)

	fake.prepareExtension = "other_ext"
	_, err = PrepareRun(ctx, RunClaim{Extension: testExt, Ref: "r", Agent: "a"}, policyTestAccount)
	require.Error(t, err, "an extension cannot hand a run to another")
	fake.prepareExtension = ""
	_, err = PrepareRun(ctx, RunClaim{Extension: testExt, Ref: "r"}, policyTestAccount)
	require.Error(t, err, "a profile must pin an agent")
	fake.prepareErr = errors.New("not a run")
	_, err = PrepareRun(ctx, RunClaim{Extension: testExt, Ref: "r", Agent: "a"}, policyTestAccount)
	require.ErrorContains(t, err, "not a run")
}

// The profile is stored as JSON, NULL for an ordinary conversation, and is
// written by the INSERT only: no later save of the row can set, change or
// clear it.
func TestRunProfileStoredOnceAsJSON(t *testing.T) {
	value, err := RunProfile{Extension: testExt, Ref: "r", Agent: "a"}.Value()
	require.NoError(t, err)
	var scanned nullRunProfile
	require.NoError(t, scanned.Scan(value))
	assert.Equal(t, &RunProfile{Extension: testExt, Ref: "r", Agent: "a"}, scanned.profile())
	require.NoError(t, scanned.Scan(nil))
	assert.Nil(t, scanned.profile())

	for _, query := range []string{saveConversationQueryWithUser, saveConversationQueryNullUser} {
		insert, update, ok := strings.Cut(query, "DO UPDATE SET")
		require.True(t, ok)
		assert.Contains(t, insert, "run_profile")
		assert.NotContains(t, update, "run_profile")
	}
}

// Without the runtime an unclassified tool runs as a read and the registry
// fallback resolves names, exactly as before.
func TestNoRunPolicyDispatchAsBefore(t *testing.T) {
	prevDao := GetConversationDao()
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { SetConversationDao(prevDao) })

	plain := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "plain_tool"}}
	executor := newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{plain}}, policyActionRequest(), 3)
	executor.toolCallbackHandler = nil
	step, _, err := executor.doAction(getNameToTool([]toolcore.NBTool{plain}), NBAgentPlannerToolAction{ToolID: "t1", Tool: "plain_tool", ToolInput: "{}"}, "")
	require.NoError(t, err)
	assert.Equal(t, ToolStatusSuccess, step.Status)
	assert.Equal(t, 1, plain.calls)

	_, _, err = executor.doAction(getNameToTool([]toolcore.NBTool{plain}), NBAgentPlannerToolAction{ToolID: "t2", Tool: registeredOnlyTool, ToolInput: "{}"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth: tool not found", "the registry fallback resolved the tool; auth judged it")
}

// In a governed run an agent dispatches only to its own tools: no registry or
// custom-agent fallback, no client tool from the request, and allowed_tools
// cannot add one.
func TestPolicyRunDispatchesOnlyOwnTools(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{allowUnclassified: true})
	prevDao := GetConversationDao()
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { SetConversationDao(prevDao) })

	own := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "own_tool"}}
	request := policyActionRequest()
	request.ClientTools = []toolcore.NBToolCommand{{Name: "client_tool"}}
	request.Capabilities = toolcore.AgentCapabilities{AllowedTools: []string{registeredOnlyTool, "client_tool", "own_tool"}}
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{own}}
	for _, name := range []string{registeredOnlyTool, "client_tool"} {
		executor := newPlannerExecutor(governedRun(security.NewRequestContextForSuperAdmin()), &mockNBAgentPlanner{}, agent, request, 3)
		step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{own}), NBAgentPlannerToolAction{ToolID: "t-" + name, Tool: name, ToolInput: "{}"}, "")
		require.NoError(t, err, name)
		assert.Nil(t, finish)
		assert.Contains(t, step.Observation, "Tool not found", name)
	}
}

// mcpLikeTool is an MCP integration tool without a classifier.
type mcpLikeTool struct{ runCountingTool }

func (*mcpLikeTool) GetImplType() string { return toolcore.ToolImplTypeMCP }

// In a governed run a tool no classifier decided on is refused unless the policy
// allows it; outside one it runs as a read.
func TestPolicyRunUnclassifiedTool(t *testing.T) {
	run := func(t *testing.T, ctx *security.RequestContext, tool toolcore.NBTool) NBAgentPlannerToolActionStep {
		t.Helper()
		executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, policyActionRequest(), 3)
		executor.toolCallbackHandler = nil
		step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), NBAgentPlannerToolAction{ToolID: "t1", Tool: tool.Name(), ToolInput: "{}"}, "")
		require.NoError(t, err)
		assert.Nil(t, finish)
		return step
	}

	fake := &fakeRunPolicy{}
	withFakeRunPolicy(t, fake)
	plain := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "plain_tool"}}
	step := run(t, governedRun(security.NewRequestContextForSuperAdmin()), plain)
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "plain_tool is unclassified in a governed run")
	assert.Equal(t, 0, plain.calls)

	mcp := &mcpLikeTool{runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "mcp_tool"}}}
	step = run(t, governedRun(security.NewRequestContextForSuperAdmin()), mcp)
	assert.Contains(t, step.Observation, "mcp_tool is unclassified in a governed run")
	assert.Equal(t, 0, mcp.calls)

	fake.allowUnclassified = true
	step = run(t, governedRun(security.NewRequestContextForSuperAdmin()), mcp)
	assert.Equal(t, ToolStatusSuccess, step.Status, step.Observation)
	assert.Equal(t, 1, mcp.calls, "allowed by the runtime")

	fake.allowUnclassified = false
	step = run(t, security.NewRequestContextForSuperAdmin(), plain)
	assert.Equal(t, ToolStatusSuccess, step.Status, "outside a governed run it runs as a read")
}

// An approval the request brings (config.tool_confirmations) must not let a
// write run in a governed run. The same approval is honoured in an ordinary
// read-only run, which is how a browser user could have run a write in a governed run.
func TestPolicyRunIgnoresApprovalsFromTheRequest(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{})
	run := func(t *testing.T, ctx *security.RequestContext) (*MockClassifiedTool, NBAgentPlannerToolActionStep) {
		t.Helper()
		write := &MockClassifiedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"}, RequestType: toolcore.ToolRequestTypeCreate}
		nameToTool := getNameToTool([]toolcore.NBTool{write})
		action := NBAgentPlannerToolAction{ToolID: "t1", Tool: "write_tool", ToolInput: `{"command":"redis-cli set k 1"}`}
		executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{write}}, policyActionRequest(), 3)
		executor.toolCallbackHandler = nil
		executor.agentRequest.QueryConfig.ToolConfirmations = map[string]string{executor.confirmationKeyOf(nameToTool, action): "ok"}
		step, finish, err := executor.doAction(nameToTool, action, "ran")
		require.NoError(t, err)
		assert.Nil(t, finish)
		return write, step
	}
	user := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "dddddddd-dddd-dddd-dddd-dddddddddddd", []string{policyTestAccount})

	write, _ := run(t, WithReadOnlyRun(user))
	assert.Equal(t, "ran", write.CapturedCtx, "control: a read-only run honours a recorded approval")

	write, step := run(t, governedRun(user))
	assert.Empty(t, write.CapturedCtx, "the write never ran in the governed run")
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "read-only investigation")
}

// exemptWriteTool is a write that skips the confirmation card, as an
// automation the policy lets run does.
type exemptWriteTool struct{ MockClassifiedTool }

func (*exemptWriteTool) SkipWriteConfirmation(string) bool { return true }

// A tool the policy lets run goes ahead in a read-only governed run without
// a prompt; the same write the policy does not let run is refused.
func TestPolicyRunOwnAutomationRuns(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{ownAutomation: "restart_pod"})
	for name, wantRun := range map[string]bool{"restart_pod": true, "other_write": false} {
		automation := &exemptWriteTool{MockClassifiedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: name}, RequestType: toolcore.ToolRequestTypeCreate}}
		executor := newPlannerExecutor(governedRun(security.NewRequestContextForSuperAdmin()), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{automation}}, policyActionRequest(), 3)
		executor.toolCallbackHandler = nil
		step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{automation}), NBAgentPlannerToolAction{ToolID: "t1", Tool: name, ToolInput: "{}"}, "ran")
		require.NoError(t, err)
		assert.Nil(t, finish, "no approval prompt")
		if wantRun {
			assert.Equal(t, "ran", automation.CapturedCtx, step.Observation)
		} else {
			assert.Empty(t, automation.CapturedCtx)
			assert.Contains(t, step.Observation, "read-only investigation")
		}
	}
}

// An agent a governed agent calls inherits the run's context, and with it the limits.
func TestPolicyRunRulesReachSubAgents(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{})
	prevDao := GetConversationDao()
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { SetConversationDao(prevDao) })

	governedCtx := fakePolicyContext(t, governedRun(security.NewRequestContextForSuperAdmin()), &governedAgent{})
	plain := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "plain_tool"}}
	sub := &MockAgent{SupportedTools: []toolcore.NBTool{plain}}
	subCtx := fakePolicyContext(t, governedCtx, sub)
	require.Equal(t, RunProfileOf(governedCtx), RunProfileOf(subCtx), "a sub-agent inherits the run")
	require.True(t, policyFor(subCtx).OwnToolsOnly(subCtx))
	for _, tool := range []string{registeredOnlyTool, "plain_tool"} {
		executor := newPlannerExecutor(subCtx, &mockNBAgentPlanner{}, sub, policyActionRequest(), 3)
		executor.toolCallbackHandler = nil
		step, _, err := executor.doAction(getNameToTool([]toolcore.NBTool{plain}), NBAgentPlannerToolAction{ToolID: "t-" + tool, Tool: tool, ToolInput: "{}"}, "")
		require.NoError(t, err, tool)
		assert.Equal(t, ToolStatusFailure, step.Status, tool)
	}
	assert.Equal(t, 0, plain.calls)
}

// Tokens are handed to the runtime from each LLM response, in the call's own
// goroutine.
func TestRunPolicyCountsTokensFromEachLLMResponse(t *testing.T) {
	fake := &fakeRunPolicy{}
	withFakeRunPolicy(t, fake)
	drainTokenUsageWrites(t)
	withFakeLLMModel(t, &fakeLLMModel{response: "ok"})
	_, err := llmruntime.GenerateAndTrackLLMContent(governedRun(llmOverrideContext()), "user-1", "acct-1", "conv-1", "msg-1", "", false, nil, false)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.tokens, "1 input + 1 output token")
}

// usageScriptedModel answers each call with the next scripted response.
type usageScriptedModel struct {
	fakeLLMModel
	responses []*llms.ContentResponse
}

func (m *usageScriptedModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.responses[min(m.calls, len(m.responses)-1)]
	m.calls++
	return r, nil
}

// The budget counts what each call processed afresh, continuation chunks
// included: cache reads do not count; output and thinking (summed across
// chunks) do.
func TestRunPolicyBudgetCountsUncachedTokensAcrossContinuations(t *testing.T) {
	fake := &fakeRunPolicy{}
	withFakeRunPolicy(t, fake)
	drainTokenUsageWrites(t)
	chunk := func(content, stop string, prompt, cached, completion, thinking int) *llms.ContentResponse {
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: content, StopReason: stop, GenerationInfo: map[string]any{
			"PromptTokens": prompt, "PromptCachedTokens": cached, "CompletionTokens": completion, "ThinkingTokens": thinking,
		}}}}
	}
	model := &usageScriptedModel{responses: []*llms.ContentResponse{
		chunk("first half", "length", 20000, 15000, 400, 100),
		chunk(" second half", "stop", 21000, 16000, 300, 50),
	}}
	withFakeLLMModel(t, model)
	_, err := llmruntime.GenerateAndTrackLLMContent(governedRun(llmOverrideContext()), "user-1", "acct-1", "conv-1", "msg-1", "", false, nil, false)
	require.NoError(t, err)
	require.Equal(t, 2, model.calls, "the cut-off response was continued")
	assert.Equal(t, (5000+400+100)+(5000+300+50), fake.tokens)
}

// drainTokenUsageWrites sends the test's background usage writes through a
// pool that cleanup waits for, so none reads the conversation store after an
// earlier cleanup has put the real one back. The returned func waits for them
// sooner, for a test that swaps the store again before it ends.
func drainTokenUsageWrites(t *testing.T) func() {
	t.Helper()
	prev := llmruntime.GetMetricsWorkerPool()
	pool := common.NewWorkerPool("policy-test-metrics", 1, 16)
	llmruntime.SetMetricsWorkerPool(pool)
	var once sync.Once
	drain := func() {
		once.Do(func() {
			pool.Stop()
			llmruntime.SetMetricsWorkerPool(prev)
		})
	}
	t.Cleanup(drain)
	return drain
}

// loopPlanner proposes a read on every turn, or finishes on its first.
type loopPlanner struct {
	mockNBAgentPlanner
	tools        []toolcore.NBTool
	finishFirst  bool
	finishStatus ConversationStatus // of the first-turn finish; completed when empty
	// after the first turn: "repeat" proposes the first turn's call again,
	// "fail" calls a tool that does not exist, "empty" proposes nothing,
	// "vary" changes the input but gets the same result — the shapes the loop
	// brakes stop on.
	after string
	calls int
}

func (p *loopPlanner) GetTools() []toolcore.NBTool { return p.tools }
func (p *loopPlanner) Plan(_ context.Context, _ []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	p.calls++
	if p.finishFirst {
		status := p.finishStatus
		if status == "" {
			status = ConversationStatusCompleted
		}
		return nil, &NBAgentPlannerFinishAction{Data: "done", Status: status}, nil
	}
	id, tool, input := fmt.Sprintf("t%d", p.calls), "own_tool", "{}"
	if p.calls > 1 {
		switch p.after {
		case "repeat":
			id = "t1"
		case "fail":
			tool = "missing_tool"
		case "empty":
			return nil, nil, nil
		case "vary":
			input = fmt.Sprintf(`{"n":%d}`, p.calls)
		}
	}
	return []NBAgentPlannerToolAction{{ToolID: id, Tool: tool, ToolInput: input}}, nil, nil
}

// runLoop runs the planner loop for agent from startIteration and returns
// the answer and the fake LLM, to inspect the summary prompt.
func runLoop(t *testing.T, governed bool, agent NBAgent, planner *loopPlanner, maxIterations, startIteration int) (string, *fakeLLMModel) {
	t.Helper()
	prevDao := GetConversationDao()
	SetConversationDao(readOnlyLoopDao{})
	t.Cleanup(func() { SetConversationDao(prevDao) })
	drain := drainTokenUsageWrites(t)
	llm := &fakeLLMModel{response: "Partial findings."}
	withFakeLLMModel(t, llm)

	ctx := llmOverrideContext()
	if governed {
		ctx = governedRun(ctx)
	}
	executor := newPlannerExecutor(ctx, planner, agent, policyActionRequest(), maxIterations)
	executor.toolCallbackHandler = nil
	executor.currentIteration = startIteration
	out, err := executor.Call(context.Background(), map[string]any{"input": "why did api-1 restart"})
	drain() // before the next runLoop in the same test swaps the store
	require.NoError(t, err)
	answer, _ := out["output"].(string)
	return answer, llm
}

func loopFixture(finishFirst bool) (*MockAgent, *loopPlanner) {
	own := &MockClassifiedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "own_tool", ReturnOutput: "pod restarted"}, RequestType: toolcore.ToolRequestTypeRead}
	return &MockAgent{SupportedTools: []toolcore.NBTool{own}}, &loopPlanner{tools: []toolcore.NBTool{own}, finishFirst: finishFirst}
}

// A spent budget stops the loop before the next turn; the answer goes
// through FinishAnswer as cut short, and the summary carries the runtime's answer
// format for the agent.
func TestPolicyRunBudgetStopsTheLoop(t *testing.T) {
	fake := &fakeRunPolicy{spent: true}
	withFakeRunPolicy(t, fake)
	_, planner := loopFixture(false)
	answer, llm := runLoop(t, true, &governedAgent{MockAgent: MockAgent{SupportedTools: planner.tools}}, planner, 10, 0)
	assert.Equal(t, 0, planner.calls, "no turn once the budget is spent")
	assert.Equal(t, "Partial findings. [cut budget=true 12/10]", answer)
	sent := llm.lastSent()
	require.NotEmpty(t, sent)
	assert.Contains(t, sent[len(sent)-1].Parts[0].(llms.TextContent).Text, "END-WITH-POLICY-JSON")
}

// Running out of turns — or resuming already past the cap — goes through
// FinishAnswer as cut short too; a normal finish goes through it as final,
// with the run's budget at that point.
func TestPolicyRunTurnCapAndNormalFinish(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{})
	agent, planner := loopFixture(false)
	answer, _ := runLoop(t, true, agent, planner, 2, 0)
	assert.Equal(t, 2, planner.calls)
	assert.Equal(t, "Partial findings. [cut budget=false 12/10]", answer)

	agent, planner = loopFixture(false)
	answer, _ = runLoop(t, true, agent, planner, 3, 3)
	assert.Equal(t, 0, planner.calls)
	assert.True(t, strings.HasSuffix(answer, "[cut budget=false 12/10]"), "resumed past the cap")

	agent, planner = loopFixture(true)
	answer, _ = runLoop(t, true, agent, planner, 10, 0)
	assert.Equal(t, "done [final 12/10]", answer)

	// A run paused on a question has not answered: the question is left as it is.
	for _, waiting := range []ConversationStatus{ConversationStatusWaiting, ConversationStatusWaitingForClientTool} {
		agent, planner = loopFixture(true)
		planner.finishStatus = waiting
		answer, _ = runLoop(t, true, agent, planner, 10, 0)
		assert.Equal(t, "done", answer, waiting)
	}
}

// An ordinary run's answer is byte for byte what the agent wrote, on a
// normal finish and on one its turns cut short, even with a policy
// registered: every final answer passes through NoRunPolicy.FinishAnswer.
func TestNoRunPolicyAnswerUnchanged(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{})
	agent, planner := loopFixture(true)
	answer, _ := runLoop(t, false, agent, planner, 10, 0)
	assert.Equal(t, "done", answer)

	agent, planner = loopFixture(false)
	answer, _ = runLoop(t, false, agent, planner, 2, 0)
	assert.Equal(t, "Partial findings.", answer)

	for _, a := range []string{"", "plain", "x\n```json\n{\"verdict\":\"leak\"}\n```\n", "  spaced\r\n"} {
		assert.Equal(t, a, NoRunPolicy{}.FinishAnswer(nil, nil, a, 5, 10, false, false))
		assert.Equal(t, a, NoRunPolicy{}.FinishAnswer(nil, nil, a, 15, 10, true, true))
	}
}

// An ordinary run that ends on its turn cap answers as before.
func TestNoRunPolicyTurnCapAsBefore(t *testing.T) {
	agent, planner := loopFixture(false)
	withFakeRunPolicy(t, &fakeRunPolicy{})
	answer, llm := runLoop(t, false, agent, planner, 2, 0)
	assert.Equal(t, "Partial findings.", answer, "an ordinary run is not governed even with a policy registered")
	sent := llm.lastSent()
	assert.NotContains(t, fmt.Sprint(sent[len(sent)-1].Parts), "END-WITH-POLICY-JSON")
}

// refuseBeforeWriteDao finds no conversation and counts writes.
type refuseBeforeWriteDao struct {
	IConversationDao
	saves *int
}

func (d refuseBeforeWriteDao) GetConversationBySession(string, string) (Conversation, error) {
	return Conversation{}, nil
}
func (d refuseBeforeWriteDao) GetConversation(string) (Conversation, error) {
	return Conversation{}, ErrConversationNotFound
}
func (d refuseBeforeWriteDao) SaveConversation(string, string, string, string, string, string, string, ConversationStatus, ConversationSource, string, string, *llmruntime.ConversationTierOverrides, string, *RunProfile) (uuid.UUID, error) {
	*d.saves++
	return uuid.New(), nil
}

// An agent bound to an extension is refused before its conversation is
// written, on every entry point that creates one (here the shared
// HandleConversationSessionRequest that chat, auto chat, queued dispatch and
// event analysis use), unless the request carries that extension's profile.
func TestBoundAgentRefusedBeforeTheConversationIsWritten(t *testing.T) {
	withFakeRunPolicy(t, &fakeRunPolicy{})
	saves := 0
	prevDao := GetConversationDao()
	SetConversationDao(refuseBeforeWriteDao{saves: &saves})
	t.Cleanup(func() { SetConversationDao(prevDao) })
	bound := &nbCustomAgent{agent: AgentDto{Name: "governed", SystemPrompt: "You help.", Config: map[string]any{"requires_extension": testExt}}, accountId: policyTestAccount}
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "dddddddd-dddd-dddd-dddd-dddddddddddd", []string{policyTestAccount})

	_, err := HandleConversationSessionRequest(ctx, bound, "dddddddd-dddd-dddd-dddd-dddddddddddd", policyTestAccount, "session-1", "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `runs only in runs of the "test_ext" extension`)
	assert.Zero(t, saves, "no conversation row is written for a refused agent")
}

// A run's owner judges the caller the security context identifies; only a
// context that identifies nobody falls back to the user the request names.
func TestActingUser(t *testing.T) {
	caller, named := uuid.NewString(), uuid.NewString()
	signedIn := security.NewRequestContextForTenantAccountAdmin(uuid.NewString(), caller, []string{uuid.NewString()})
	assert.Equal(t, caller, ActingUser(signedIn, named), "a caller acting as another user stays the caller")
	assert.Equal(t, caller, ActingUser(signedIn, ""))
	assert.Equal(t, named, ActingUser(security.NewRequestContextForSuperAdmin(), named), "an internal context names nobody")
	system := security.NewRequestContextForTenantAccountAdmin(uuid.NewString(), security.GetSystemUserId(), []string{uuid.NewString()})
	assert.Equal(t, named, ActingUser(system, named), "nor does the system user")
	assert.Equal(t, named, ActingUser(nil, named))
}

// A governed run resolves its agent's tool list fresh: it neither reads nor
// fills the cross-request cache, which an ordinary run keeps using.
func TestGovernedRunSkipsTheAgentToolCache(t *testing.T) {
	agent := &nbCustomAgent{agent: AgentDto{Name: "cache_probe", Tools: []string{registeredOnlyTool}}, accountId: policyTestAccount}
	customAgentToolsCacheInst.delete(policyTestAccount, agent.agent.Name)
	t.Cleanup(func() { customAgentToolsCacheInst.delete(policyTestAccount, agent.agent.Name) })

	require.Len(t, agent.configuredTools(governedRun(security.NewRequestContextForSuperAdmin())), 1)
	_, cached := customAgentToolsCacheInst.get(policyTestAccount, agent.agent.Name)
	assert.False(t, cached, "a governed run does not fill the cache")

	customAgentToolsCacheInst.set(policyTestAccount, agent.agent.Name, nil)
	assert.Len(t, agent.configuredTools(governedRun(security.NewRequestContextForSuperAdmin())), 1, "nor read it")
	assert.Empty(t, agent.configuredTools(security.NewRequestContextForSuperAdmin()), "an ordinary run uses the cache as before")
}

// A loop brake ends the run before its turns are up; the summary is told it
// stopped early and why, while a run that used its turns gets no such note (#40199).
func TestLoopBrakeStopReasonReachesSummary(t *testing.T) {
	summaryPrompt := func(llm *fakeLLMModel) string {
		sent := llm.lastSent()
		require.NotEmpty(t, sent)
		return sent[len(sent)-1].Parts[0].(llms.TextContent).Text
	}
	origBrake := config.Config.LlmServerNoProgressBrakeEnabled
	t.Cleanup(func() { config.Config.LlmServerNoProgressBrakeEnabled = origBrake })
	for _, tc := range []struct {
		after  string
		brake  bool
		calls  int
		reason string
	}{
		{after: "repeat", calls: 3, reason: stopReasonDuplicateActions},
		{after: "fail", brake: true, calls: 3, reason: stopReasonNoUsableActions},
		{after: "empty", calls: 2, reason: stopReasonPlanFailed},
		{after: "vary", brake: true, calls: 3, reason: stopReasonNoProgress},
	} {
		t.Run(tc.after, func(t *testing.T) {
			config.Config.LlmServerNoProgressBrakeEnabled = tc.brake
			agent, planner := loopFixture(false)
			planner.after = tc.after
			_, llm := runLoop(t, false, agent, planner, 10, 0)
			assert.Equal(t, tc.calls, planner.calls, "the run stops well before its 10 turns")
			assert.Contains(t, summaryPrompt(llm), "stopped early, before it finished, because "+tc.reason)
		})
	}

	config.Config.LlmServerNoProgressBrakeEnabled = origBrake
	agent, planner := loopFixture(false)
	_, llm := runLoop(t, false, agent, planner, 2, 0)
	assert.NotContains(t, summaryPrompt(llm), "stopped early")
}
