package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/config"
)

// critiqueRecordingModel keeps every request and can answer with a tool call, so tests
// can check what each critique call sent.
type critiqueRecordingModel struct {
	mu      sync.Mutex
	replies []recordedReply
	sent    [][]llms.MessageContent
}

type recordedReply struct {
	content  string
	toolCall bool
}

func (m *critiqueRecordingModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, messages)
	reply := m.replies[len(m.replies)-1]
	if i := len(m.sent) - 1; i < len(m.replies) {
		reply = m.replies[i]
	}
	response := newFakeContentResponse(reply.content, "stop")
	if reply.toolCall {
		response.Choices[0].ToolCalls = []llms.ToolCall{{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "metrics", Arguments: "{}"}}}
	}
	return response, nil
}

func (m *critiqueRecordingModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("critiqueRecordingModel: Call is not used")
}

func (m *critiqueRecordingModel) requests() [][]llms.MessageContent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]llms.MessageContent(nil), m.sent...)
}

func messageText(m llms.MessageContent) string {
	return partsText(m.Parts)
}

var continuationPlannerMessages = []llms.MessageContent{
	llms.TextParts(llms.ChatMessageTypeSystem, "planner system prompt"),
	llms.TextParts(llms.ChatMessageTypeHuman, "why is CPU high"),
}

var continuationSteps = []NBAgentPlannerToolActionStep{
	{Action: NBAgentPlannerToolAction{Tool: "metrics", ToolInput: "cpu for checkout", DisplayID: "E1"}, Observation: "CPU high"},
}

const fencedClaimAudit = "```json\n" + validClaimAudit + "\n```"

func installContinuationFlags(t *testing.T) {
	t.Helper()
	feedback, guidance, continuation := config.Config.ClaimCritiqueFeedbackEnabled, config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled
	t.Cleanup(func() {
		config.Config.ClaimCritiqueFeedbackEnabled, config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled = feedback, guidance, continuation
	})
	config.Config.ClaimCritiqueFeedbackEnabled, config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled = true, true, true
}

func TestCritiqueEvidenceIndex(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "metrics", ToolInput: "cpu\nfor checkout", DisplayID: "E1"}},
		{Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: "notes", DisplayID: "E4"}},
		{Action: NBAgentPlannerToolAction{Tool: "logs", ToolInput: "errors"}},
	}
	index, maxEvidence := critiqueEvidenceIndex(steps)
	require.Equal(t, "E1 = metrics(cpu for checkout)\nE3 = logs(errors)\n", index, "a step without a display id falls back to its position")
	require.Equal(t, 3, maxEvidence, "notebook steps are not citable evidence")
	require.NotContains(t, index, "notes")
}

func TestStripJSONFence(t *testing.T) {
	require.Equal(t, `{"a":1}`, stripJSONFence("```json\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripJSONFence("```\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripJSONFence(` {"a":1} `))
}

func TestNewCritiqueConversationSkipsCompressedRuns(t *testing.T) {
	ctx := llmOverrideContext()
	require.Nil(t, newCritiqueConversation(ctx, "", continuationPlannerMessages, "answer", nil, continuationSteps, true))
	require.Nil(t, newCritiqueConversation(ctx, "", continuationPlannerMessages, " ", nil, continuationSteps, false))
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "answer", nil, continuationSteps, false)
	require.NotNil(t, conv)
	require.Len(t, conv.base, len(continuationPlannerMessages)+1)
	require.Equal(t, llms.ChatMessageTypeAI, conv.base[len(conv.base)-1].Role)
}

// All three critique calls continue the planner's request: each starts with the
// planner's messages and its answer, so the provider cache covers that prefix.
func TestCritiqueContinuationReusesPlannerRequest(t *testing.T) {
	critiqueCallsInOrder(t)
	installContinuationFlags(t)
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &critiqueRecordingModel{replies: []recordedReply{
		{content: "<decision>accept</decision><feedback></feedback>"},
		{content: fencedClaimAudit},
		{content: validClaimGuidance},
	}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	request := NBAgentRequest{Query: "why is CPU high"}
	p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2}
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "the answer", nil, continuationSteps, false)
	require.NotNil(t, conv)

	decision, feedback := p.runCritique(request.Query, "metrics", "the answer", continuationSteps, conv)

	require.Equal(t, "refine", decision)
	require.True(t, strings.HasPrefix(feedback, claimGuidanceHeader), feedback)
	requests := model.requests()
	require.Len(t, requests, 3, "answer critique, audit and guidance; no standalone fallback")
	for i, sent := range requests {
		require.Greater(t, len(sent), len(conv.base), "call %d", i)
		for j, m := range conv.base {
			require.Equal(t, m.Role, sent[j].Role, "call %d message %d", i, j)
			require.Equal(t, messageText(m), messageText(sent[j]), "call %d message %d", i, j)
		}
		instruction := messageText(sent[len(conv.base)])
		require.Contains(t, instruction, "STOP INVESTIGATING", "call %d", i)
		require.Contains(t, instruction, "E1 = metrics(cpu for checkout)", "call %d", i)
	}
	// Guidance continues the audit: its instruction and reply come before the guidance turn.
	guidance := requests[2]
	require.Len(t, guidance, len(conv.base)+3)
	require.Equal(t, messageText(requests[1][len(conv.base)]), messageText(guidance[len(conv.base)]))
	require.Equal(t, llms.ChatMessageTypeAI, guidance[len(conv.base)+1].Role)
	require.Equal(t, fencedClaimAudit, messageText(guidance[len(conv.base)+1]), "the audit's reply exactly as returned, so the prefix matches")
	require.Contains(t, messageText(guidance[len(conv.base)+2]), "flagged_claims")

	require.NotEmpty(t, dao.rows)
	final := dao.rows[len(dao.rows)-1]
	require.Equal(t, claimModeContinuation, final.Detail.Mode)
	require.Equal(t, claimModeContinuation, final.Detail.Guidance.Mode)
}

// A continuation that calls a tool or returns an invalid audit falls back to the
// standalone critique, so the verdict never depends on the continuation working.
func TestCritiqueContinuationFallsBack(t *testing.T) {
	critiqueCallsInOrder(t)
	installContinuationFlags(t)
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &critiqueRecordingModel{replies: []recordedReply{
		{content: "<decision>refine</decision><feedback>from a tool-calling reply</feedback>", toolCall: true}, // continuation answer critique calls a tool
		{content: "<decision>accept</decision><feedback></feedback>"},                                          // standalone answer critique
		{content: `{"claims":[`},      // continuation audit is invalid
		{content: validClaimAudit},    // standalone audit
		{content: validClaimGuidance}, // standalone guidance
	}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	request := NBAgentRequest{Query: "why is CPU high"}
	p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2}
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "the answer", nil, continuationSteps, false)

	decision, feedback := p.runCritique(request.Query, "metrics", "the answer", continuationSteps, conv)

	require.Equal(t, "refine", decision)
	require.True(t, strings.HasPrefix(feedback, claimGuidanceHeader), feedback)
	requests := model.requests()
	require.Len(t, requests, 5)
	// The standalone audit sends the packet, not the planner's conversation.
	require.Equal(t, llms.ChatMessageTypeSystem, requests[3][0].Role)
	require.Contains(t, messageText(requests[3][1]), `"observations"`)
	final := dao.rows[len(dao.rows)-1]
	require.Equal(t, claimModeFallback, final.Detail.Mode)
	require.NotEqual(t, dao.rows[0].TokenUsageID, final.TokenUsageID, "the fallback audit is a new attempt with its own usage id")
	require.Contains(t, final.Detail.ContinuationError, "invalid_output")
	require.Empty(t, final.Detail.Guidance.Mode, "guidance follows the audit onto the standalone path")
}

// With the flag off nothing changes: no conversation is built and both
// critiques run standalone.
func TestCritiqueContinuationOffKeepsStandalone(t *testing.T) {
	critiqueCallsInOrder(t)
	installContinuationFlags(t)
	config.Config.CritiqueContinuationEnabled = false
	installClaimCaptureDAO(t, &claimCaptureDAO{})
	model := &critiqueRecordingModel{replies: []recordedReply{
		{content: "<decision>accept</decision><feedback></feedback>"},
		{content: strings.Replace(validClaimAudit, `"inferred"`, `"observed"`, 1)},
	}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	request := NBAgentRequest{Query: "why is CPU high"}
	p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2}

	decision, _ := p.runCritique(request.Query, "metrics", "the answer", continuationSteps, nil)

	require.Equal(t, "accept", decision)
	requests := model.requests()
	require.Len(t, requests, 2)
	for _, sent := range requests {
		require.NotContains(t, messageText(sent[len(sent)-1]), "STOP INVESTIGATING")
	}
}

func TestIsRequestEcho(t *testing.T) {
	cases := []struct {
		tool, input string
		want        bool
	}{
		{"get_event_by_id", `{"event_id":"e1"}`, true},
		{"get_event_evidence", `{"event_id":"e1","evidence_type":"alert_labels"}`, true},
		{"shell_execute", `{"command":"cat event_labels_overflow_e1.txt"}`, true},
		// Collector-gathered evidence is an observation of the system, not an echo.
		{"get_event_evidence", `{"event_id":"e1","evidence_type":"pod_metrics"}`, false},
		{"shell_execute", `{"command":"jq . event_e1_evidence.json"}`, false},
		{"get_event_incident_assembly", `{"event_id":"e1"}`, false},
		{"metrics", "cpu for checkout", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, isRequestEcho(c.tool, c.input), "%s(%s)", c.tool, c.input)
	}
	index, _ := critiqueEvidenceIndex([]NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "get_event_by_id", ToolInput: `{"event_id":"e1"}`, DisplayID: "E1"}},
		{Action: NBAgentPlannerToolAction{Tool: "metrics", ToolInput: "cpu", DisplayID: "E2"}},
	})
	require.Equal(t, "E1 = get_event_by_id({\"event_id\":\"e1\"})  [request_echo]\nE2 = metrics(cpu)\n", index)
}

// An evidence packet over the standalone limit no longer skips the audit: the
// continuation sends the planner's own request, not the packet (#40236).
func TestOversizedPacketStillRunsContinuationAudit(t *testing.T) {
	installContinuationFlags(t)
	pinClaimAuditOnly(t)
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &critiqueRecordingModel{replies: []recordedReply{{content: fencedClaimAudit}}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "the answer", nil, continuationSteps, false)
	require.NotNil(t, conv)

	decision, _ := runClaimCritiqueShadow(ctx, NBAgentRequest{}, "react4", "test_agent", "accept", "",
		strings.Repeat("x", claimCritiqueInputLimit), "the answer", "", continuationSteps, false, conv)

	require.Equal(t, "refine", decision)
	require.Len(t, model.requests(), 1)
	final := dao.rows[len(dao.rows)-1]
	require.Equal(t, "ok", final.Status)
	require.Equal(t, claimModeContinuation, final.Detail.Mode)
	require.Empty(t, final.Detail.InputPacket)
	require.Empty(t, final.Detail.ErrorStage)
	require.Equal(t, "the answer", final.Answer, "the reviewed answer is kept so the row stays countable")
	require.Equal(t, strings.Repeat("x", claimCritiqueInputLimit), final.Input, "the request is kept too")
}

// Without a packet there is no standalone fallback: a failed continuation is the outcome.
func TestOversizedPacketContinuationFailureHasNoFallback(t *testing.T) {
	installContinuationFlags(t)
	pinClaimAuditOnly(t)
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &critiqueRecordingModel{replies: []recordedReply{{content: fencedClaimAudit, toolCall: true}}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "the answer", nil, continuationSteps, false)

	decision, _ := runClaimCritiqueShadow(ctx, NBAgentRequest{}, "react4", "test_agent", "accept", "",
		strings.Repeat("x", claimCritiqueInputLimit), "the answer", "", continuationSteps, false, conv)

	require.Equal(t, "accept", decision, "no verdict keeps the incoming decision")
	require.Len(t, model.requests(), 1, "no standalone fallback without a packet")
	final := dao.rows[len(dao.rows)-1]
	require.Equal(t, "tool_call", final.Status)
	require.Equal(t, claimModeContinuation, final.Detail.Mode)
	require.NotEmpty(t, final.Detail.ContinuationError)
	require.True(t, final.Completed)
}

// critiqueCallsInOrder runs the answer critiquer before the claim audit, so a
// test that scripts model replies in call order gets them in the order written.
func critiqueCallsInOrder(t *testing.T) {
	t.Helper()
	previous := startAnswerCritique
	startAnswerCritique = func(critique func()) { critique() }
	t.Cleanup(func() { startAnswerCritique = previous })
}

// overlapModel answers the answer critiquer and the claim audit by content and
// records how many calls were in flight at once. Each call waits for the other
// to start (or 5 s), so calls made one after the other never overlap.
type overlapModel struct {
	mu              sync.Mutex
	inFlight        int
	maxInFlight     int
	both            chan struct{}
	once            sync.Once
	panicOnCritique bool
}

func (m *overlapModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.mu.Lock()
	m.inFlight++
	m.maxInFlight = max(m.maxInFlight, m.inFlight)
	if m.inFlight == 2 {
		m.once.Do(func() { close(m.both) })
	}
	m.mu.Unlock()
	select {
	case <-m.both:
	case <-time.After(5 * time.Second):
	}
	m.mu.Lock()
	m.inFlight--
	m.mu.Unlock()
	if strings.Contains(messageText(messages[len(messages)-1]), "<decision>") {
		if m.panicOnCritique {
			panic("critique model exploded")
		}
		return newFakeContentResponse("<decision>accept</decision><feedback></feedback>", "stop"), nil
	}
	return newFakeContentResponse(validClaimAudit, "stop"), nil
}

func (m *overlapModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("overlapModel: Call is not used")
}

func (m *overlapModel) peak() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxInFlight
}

// The answer critiquer and the claim audit judge the same answer independently,
// so ReAct4 runs them at the same time; only combining the verdicts waits for both.
func TestReAct4AnswerCritiqueRunsAlongsideClaimAudit(t *testing.T) {
	installContinuationFlags(t)
	config.Config.ClaimCritiqueActionableFeedbackEnabled = false // two calls: critique and audit
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &overlapModel{both: make(chan struct{})}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	request := NBAgentRequest{Query: "why is CPU high"}
	p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2}
	conv := newCritiqueConversation(ctx, "", continuationPlannerMessages, "the answer", nil, continuationSteps, false)
	require.NotNil(t, conv)

	decision, feedback := p.runCritique(request.Query, "metrics", "the answer", continuationSteps, conv)

	require.Equal(t, 2, model.peak(), "the critique and the audit overlap")
	require.Equal(t, "refine", decision, "an accepted answer with a refuted claim is sent back, as before")
	require.NotEmpty(t, feedback)
	require.GreaterOrEqual(t, len(dao.rows), 2)
	require.Equal(t, claimBaselinePending, dao.rows[0].BaselineDecision, "stored before the critique has answered")
	require.Equal(t, "accept", dao.rows[len(dao.rows)-1].BaselineDecision)
}

// A panic in the answer critiquer is the same as a failed critique: the answer is
// accepted, and the claim audit still completes its row.
func TestReAct4AnswerCritiquePanicAcceptsAnswer(t *testing.T) {
	installContinuationFlags(t)
	config.Config.ClaimCritiqueActionableFeedbackEnabled = false
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	model := &overlapModel{both: make(chan struct{}), panicOnCritique: true}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	request := NBAgentRequest{Query: "why is CPU high"}
	p := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}, maxRefinementAttempts: 2}

	decision, _ := p.runCritique(request.Query, "metrics", "the answer", continuationSteps, nil)

	require.Empty(t, decision)
	final := dao.rows[len(dao.rows)-1]
	require.True(t, final.Completed)
	require.Equal(t, "unknown", final.BaselineDecision)
}

// The audit never reads the critiquer's verdict, so it asks for it only after it
// has run; the row written before then says the verdict is pending.
func TestClaimCritiqueAsksForBaselineAfterAudit(t *testing.T) {
	pinClaimAuditOnly(t)
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{turns: []fakeTurn{{content: validClaimAudit}}}
	withFakeLLMModel(t, fake)
	callsWhenAsked := -1
	baseline := func() (string, string) {
		callsWhenAsked = fake.callCount()
		return "refine", "check the node"
	}

	decision, feedback := runClaimCritique(llmOverrideContext(), NBAgentRequest{}, "react4", "test_agent", baseline,
		"why", "answer", "", nil, false, nil)

	require.Equal(t, 1, callsWhenAsked, "asked after the audit call")
	require.Equal(t, "refine", decision)
	require.True(t, strings.HasPrefix(feedback, "check the node"), feedback)
	require.Equal(t, claimBaselinePending, dao.rows[0].BaselineDecision)
	final := dao.rows[len(dao.rows)-1]
	require.Equal(t, "refine", final.BaselineDecision)
	require.Equal(t, "check the node", final.BaselineFeedback)
}
