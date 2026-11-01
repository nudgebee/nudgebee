package core

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildResumeAgentRequestRestoresClientToolRuntimeFields(t *testing.T) {
	clientTools := []toolcore.NBToolCommand{{
		Name:        "custom_shell_execute",
		Description: "Run a command in the custom agent environment",
	}}
	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"custom_shell_execute"},
	}
	queryConfig := toolcore.NBQueryConfig{
		ClientTools:  clientTools,
		Capabilities: capabilities,
	}

	got := buildResumeAgentRequest(
		NBAgentRequest{Query: "investigate", AgentId: "agent-id"},
		"parent-id",
		"saved-state",
		queryConfig,
	)

	assert.Equal(t, clientTools, got.ClientTools)
	assert.Equal(t, capabilities, got.Capabilities)
	assert.Equal(t, queryConfig, got.QueryConfig)
}

func TestDelegatedTargetPersistsOnAgentNotMessage(t *testing.T) {
	config := toolcore.NBQueryConfig{
		ToolConfigs:     map[string]string{"aws_execute": "origin"},
		DelegatedTarget: &toolcore.ExecutionTarget{Provider: "aws", Name: "prod"},
	}
	messageConfig := messageQueryConfig(config)
	require.Nil(t, messageConfig.DelegatedTarget)
	require.Equal(t, "origin", messageConfig.ToolConfigs["aws_execute"])
	require.Equal(t, "prod", config.DelegatedTarget.Name)

	encoded := `{"delegated_target":{"provider":"aws","name":"prod"}}`
	resumed, err := withResumedAgentTarget(messageConfig, ConversationAgent{QueryConfig: &encoded})
	require.NoError(t, err)
	require.Equal(t, config.DelegatedTarget, resumed.DelegatedTarget)
	resumed, err = withResumedAgentTarget(resumed, ConversationAgent{})
	require.NoError(t, err)
	require.Nil(t, resumed.DelegatedTarget, "another agent must not inherit the child's target")
	empty := ""
	resumed, err = withResumedAgentTarget(config, ConversationAgent{QueryConfig: &empty})
	require.NoError(t, err)
	require.Nil(t, resumed.DelegatedTarget, "an empty saved config must not inherit the previous target")
}

// TestIsResumableAgentStatus locks in the contract for which agent statuses
// V2's idempotency guard treats as eligible for resume vs already-progressed.
//
// Both `waiting` (user-question followup) and `waiting_for_client_tool`
// (shell_execute / custom client tool resume) must qualify. The original
// implementation only accepted `waiting`, which silently swallowed every
// client-tool-result resume — the conversation stayed IN_PROGRESS until
// the client-side task timeout. See conversations on PR #29933 / #29746
// (terminal-bench harness) for the regression observation.
func TestIsResumableAgentStatus(t *testing.T) {
	cases := []struct {
		name   string
		status AgentExecutionStatus
		want   bool
	}{
		{"waiting → resumable (followup question)", AgentExecutionStatusWaiting, true},
		{"waiting_for_client_tool → resumable (client-tool resume)", AgentExecutionStatusWaitingForClientTool, true},
		{"WAITING uppercased → resumable (case-insensitive)", "WAITING", true},
		{"Waiting_For_Client_Tool mixed case → resumable", "Waiting_For_Client_Tool", true},
		{"success → not resumable (already advanced)", AgentExecutionStatusSuccess, false},
		{"fail → not resumable", AgentExecutionStatusFail, false},
		{"in_progress → not resumable (someone else holds the work)", AgentExecutionStatusInProgress, false},
		{"empty → not resumable", AgentExecutionStatus(""), false},
		{"unknown garbage → not resumable", AgentExecutionStatus("flapdoodle"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isResumableAgentStatus(tc.status))
		})
	}
}

// TestIsTrueSibling locks in the level-check the bound-sibling resume loop uses.
//
// Regression: a child agent's tool-confirmation followup gets bound up to its
// parent (both rows share the same followup_message_id). When the followup is
// answered against the CHILD, the loop must NOT treat the parent as a sibling —
// resuming the parent there discards its final answer and clears its state,
// leaving the conversation COMPLETED while the generation message stays
// IN_PROGRESS/empty (observed on a "create pod → decline" decline flow).
func TestIsTrueSibling(t *testing.T) {
	parentID := uuid.New()

	child1 := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID}
	child2 := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID}
	parent := ConversationAgent{ID: parentID, ParentAgentID: uuid.Nil} // the parent itself

	assert.True(t, isTrueSibling(child1, child2),
		"two agents under the same parent are true siblings")
	assert.False(t, isTrueSibling(child1, parent),
		"the parent (bound to a child's followup) must not be treated as a sibling")
	assert.False(t, isTrueSibling(child1, child1),
		"an agent is not its own sibling")

	// Top-level peers (both parentless) are genuine siblings of each other.
	top1 := ConversationAgent{ID: uuid.New(), ParentAgentID: uuid.Nil}
	top2 := ConversationAgent{ID: uuid.New(), ParentAgentID: uuid.Nil}
	assert.True(t, isTrueSibling(top1, top2),
		"two top-level agents share the nil parent and are siblings")
}

// bubbleFakeDao is a minimal IConversationDao that drives the no-saved-state
// branch of bubbleUpIfSiblingsDone. Only the methods that branch reaches are
// implemented; everything else falls through to the embedded nil interface and
// panics if hit (so an unexpected code path is loud, not silent).
type bubbleFakeDao struct {
	IConversationDao

	parentAgent  ConversationAgent
	parentState  string
	waitingCount int

	persistedMsgID     string
	persistedContent   string
	persistedMsgStatus ConversationStatus
	convStatus         ConversationStatus

	getConversationMessageCalls int
	// originalQuestion, when set, makes GetConversationMessage succeed and
	// return it — for the tests that drive grading all the way through.
	originalQuestion string
}

type ancestorFakeDao struct {
	IConversationDao
	agents       map[string]ConversationAgent
	parentStates map[string]struct {
		parentID string
		state    string
	}
}

type resumeTestAgent struct {
	name string
}

func (a *resumeTestAgent) GetName() string { return a.name }
func (a *resumeTestAgent) GetNameAliases() []string {
	return nil
}
func (a *resumeTestAgent) GetDescription() string { return "resume test agent" }
func (a *resumeTestAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (a *resumeTestAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a *resumeTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeOrchestrating }

func (f *ancestorFakeDao) ListConversationAgents(_, agentID string) ([]ConversationAgent, error) {
	agent, ok := f.agents[agentID]
	if !ok {
		return nil, nil
	}
	return []ConversationAgent{agent}, nil
}

func (f *ancestorFakeDao) GetConversationAgentParentAgentIdAndPreviousState(agentID string) (string, string) {
	value := f.parentStates[agentID]
	return value.parentID, value.state
}

func TestResolveRegisteredAncestorSkipsDynamicDelegateAgent(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	const registeredName = "test_resume_registered_ancestor"
	RegisterNBAgentFactory(registeredName, func(string) (NBAgent, error) {
		return &resumeTestAgent{name: registeredName}, nil
	})

	rootID := uuid.New()
	delegateID := uuid.New()
	SetConversationDao(&ancestorFakeDao{agents: map[string]ConversationAgent{
		delegateID.String(): {ID: delegateID, AgentName: "delegate_agent", ParentAgentID: rootID},
		rootID.String():     {ID: rootID, AgentName: registeredName, ParentAgentID: uuid.Nil},
	}})

	agent, dto, walked, err := resolveRegisteredAncestor(
		security.NewRequestContextForSuperAdmin(), delegateID, uuid.NewString(), uuid.New(),
	)
	require.NoError(t, err)
	require.NotNil(t, agent)
	require.NotNil(t, dto)
	assert.Equal(t, registeredName, agent.GetName())
	assert.Equal(t, rootID, dto.ID)
	assert.Equal(t, 1, walked)
}

func TestResolveBubbleUpParentLoadsExecutableAncestorState(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	const registeredName = "test_bubble_up_registered_ancestor"
	RegisterNBAgentFactory(registeredName, func(string) (NBAgent, error) {
		return &resumeTestAgent{name: registeredName}, nil
	})

	rootID := uuid.New()
	delegateID := uuid.New()
	dao := &ancestorFakeDao{
		agents: map[string]ConversationAgent{
			delegateID.String(): {ID: delegateID, AgentName: "delegate_agent", ParentAgentID: rootID},
			rootID.String():     {ID: rootID, AgentName: registeredName, ParentAgentID: uuid.Nil, Query: "original task"},
		},
		parentStates: map[string]struct {
			parentID string
			state    string
		}{
			delegateID.String(): {parentID: rootID.String(), state: "wrong delegate state"},
			rootID.String():     {parentID: uuid.Nil.String(), state: "saved orchestrator state"},
		},
	}
	SetConversationDao(dao)

	agent, dto, parentID, state, walked, err := resolveBubbleUpParent(
		security.NewRequestContextForSuperAdmin(), dao, delegateID.String(), uuid.NewString(), uuid.New(),
	)
	require.NoError(t, err)
	require.NotNil(t, agent)
	require.NotNil(t, dto)
	assert.Equal(t, registeredName, agent.GetName())
	assert.Equal(t, rootID, dto.ID)
	assert.Equal(t, rootID.String(), parentID, "top-level executable ancestor resumes as its own parent")
	assert.Equal(t, "saved orchestrator state", state)
	assert.Equal(t, 1, walked)
}

func (f *bubbleFakeDao) CountWaitingSubAgents(parentAgentId, messageId string) (int, error) {
	return f.waitingCount, nil
}

func (f *bubbleFakeDao) GetConversationAgentParentAgentIdAndPreviousState(agentId string) (string, string) {
	return "", f.parentState
}

func (f *bubbleFakeDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{f.parentAgent}, nil
}

func (f *bubbleFakeDao) UpdateConversationMessage(id, response string, status ConversationStatus) error {
	f.persistedMsgID = id
	f.persistedContent = response
	f.persistedMsgStatus = status
	return nil
}

func (f *bubbleFakeDao) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	f.convStatus = status
	return nil
}

// GetConversationMessage serves the generation message finalizeCompletedConversation
// reads to recover the original question.
//
// With originalQuestion unset it errors instead, which is what the tests using
// security.NewRequestContextForSuperAdmin() expect: that context carries an
// empty tenant id, so AnswerConfidenceEnabledForTenant short-circuits before
// the read ever happens. Erroring keeps that assumption honest — if a change
// makes those tests reach the read, they fail loudly rather than silently
// grading a placeholder.
func (f *bubbleFakeDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	f.getConversationMessageCalls++
	if f.originalQuestion == "" {
		return ConversationMessage{}, errors.New("bubbleFakeDao: GetConversationMessage not stubbed")
	}
	return ConversationMessage{Message: f.originalQuestion}, nil
}

// originalQueryFakeDao is a minimal IConversationDao stubbing only
// GetConversationMessage, so loadOriginalQuestion can be tested without
// pulling in the rest of bubbleFakeDao's completion-flow stubs.
type originalQueryFakeDao struct {
	IConversationDao
	message ConversationMessage
	err     error

	calledWithID, calledWithAccountID, calledWithConversationID string
}

func (f *originalQueryFakeDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	f.calledWithID, f.calledWithAccountID, f.calledWithConversationID = id, accountId, conversationId
	return f.message, f.err
}

// TestOriginalQuestionForScoring is the regression test for #37534: a turn
// that completes via a followup resume must be graded on the user's ORIGINAL
// question, not the followup answer text (e.g. "yes") that req.Query carries
// at that point. loadOriginalQuestion is what withOriginalQuestion
// uses to recover it — from the generation message row, which a followup
// exchange never overwrites.
func TestOriginalQuestionForScoring(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	msgID := uuid.New().String()
	accountID := uuid.New().String()
	convID := uuid.New().String()
	req := NBAgentRequest{
		MessageId:      msgID,
		AccountId:      accountID,
		ConversationId: convID,
		Query:          "yes", // the followup answer — must NOT be what gets classified
	}

	t.Run("resolves the original question from the generation message", func(t *testing.T) {
		const originalQuestion = "orders-api is down, check what's wrong and fix it"
		fake := &originalQueryFakeDao{message: ConversationMessage{Message: originalQuestion}}
		SetConversationDao(fake)

		got, err := loadOriginalQuestion(req)

		require.NoError(t, err)
		assert.Equal(t, originalQuestion, got)
		assert.Equal(t, msgID, fake.calledWithID)
		assert.Equal(t, accountID, fake.calledWithAccountID)
		assert.Equal(t, convID, fake.calledWithConversationID)

		// This is the actual fix: with the original question in hand instead of
		// the followup answer, the existing gate now classifies the turn as an
		// investigation and qualifies it for grading.
		scoreReq := req
		scoreReq.OriginalQuery = got
		assert.True(t, shouldScoreAnswerConfidence(scoreReq, "final answer"))
		// Without the fix (grading on req.Query="yes" directly), the same gate
		// rejects the turn — pinning the bug this test guards against.
		assert.False(t, shouldScoreAnswerConfidence(req, "final answer"))
	})

	// A DB failure must be distinguishable from "there is no question", so the
	// caller can log it instead of silently never grading again.
	t.Run("DB failure returns an error, not a silent empty string", func(t *testing.T) {
		fake := &originalQueryFakeDao{err: errors.New("boom")}
		SetConversationDao(fake)

		got, err := loadOriginalQuestion(req)
		require.Error(t, err)
		assert.ErrorContains(t, err, "boom", "the underlying cause must survive wrapping")
		assert.Empty(t, got)
	})

	// A blank stored message must yield no question so the caller skips scoring.
	// Whitespace and empty differ in how they'd fail downstream, and only the
	// empty case is actually dangerous — see the sub-assertions below — but the
	// contract is the same for both: no usable question was recovered.
	t.Run("blank stored message yields no question and no error", func(t *testing.T) {
		for _, blank := range []string{"", "   ", "\n\t "} {
			fake := &originalQueryFakeDao{message: ConversationMessage{Message: blank}}
			SetConversationDao(fake)

			got, err := loadOriginalQuestion(req)
			require.NoError(t, err, "a blank message is not a failure, just nothing to grade")
			assert.Empty(t, strings.TrimSpace(got), "blank message %q must not count as a recovered question", blank)
		}
	})

	// Why the empty case specifically needs the guard: shouldScoreAnswerConfidence
	// substitutes req.Query only when OriginalQuery is exactly "". On this resume
	// path req.Query is the followup answer ("yes"), so an investigation-sourced
	// turn would then pass the gate and be graded against "yes" — the very bug
	// this PR fixes. A whitespace-only OriginalQuery is NOT substituted and trips
	// that function's own TrimSpace guard instead, so it already fails closed.
	t.Run("empty (not whitespace) OriginalQuery is what would reintroduce the bug", func(t *testing.T) {
		investigationSourced := req
		investigationSourced.ConversationSource = ConversationSourceInvestigation

		empty := investigationSourced
		empty.OriginalQuery = ""
		assert.True(t, shouldScoreAnswerConfidence(empty, "answer"),
			`"" falls back to req.Query ("yes") and passes on source — this is the hole ok=false closes`)

		whitespace := investigationSourced
		whitespace.OriginalQuery = "   "
		assert.False(t, shouldScoreAnswerConfidence(whitespace, "answer"),
			"whitespace is not substituted, so the gate's own TrimSpace check already rejects it")
	})

	t.Run("empty account id, message id, or conversation id short-circuits without querying", func(t *testing.T) {
		fake := &originalQueryFakeDao{message: ConversationMessage{Message: "should never be reached"}}
		SetConversationDao(fake)

		for _, r := range []NBAgentRequest{
			func() NBAgentRequest { c := req; c.AccountId = ""; return c }(),
			func() NBAgentRequest { c := req; c.MessageId = ""; return c }(),
			func() NBAgentRequest { c := req; c.ConversationId = ""; return c }(),
		} {
			got, err := loadOriginalQuestion(r)
			require.NoError(t, err, "a missing identifier is nothing to look up, not a failure")
			assert.Empty(t, got)
		}

		assert.Empty(t, fake.calledWithID, "must not query the DB with any empty id")
	})
}

// TestBubbleUpIfSiblingsDone_StatelessParentFinalizesMessage covers the actual
// user-facing fix: when all siblings are done and the parent has no saved state
// (it completed out-of-band and its state was cleared), the parent's saved
// answer must be persisted to the generation message — otherwise the
// conversation goes COMPLETED while the message stays IN_PROGRESS/empty.
//
// Two sub-cases:
//   - parent row has a non-empty Response  ⇒ that response is persisted.
//   - parent row Response is nil           ⇒ fall back to the child's response.
//
// Both must leave the message and conversation COMPLETED, and preserve the
// non-Response fields of childResp (e.g. AgentName).
func TestBubbleUpIfSiblingsDone_StatelessParentFinalizesMessage(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)
	const parentAgentName = "test_stateless_bubble_parent"
	RegisterNBAgentFactory(parentAgentName, func(string) (NBAgent, error) {
		return &resumeTestAgent{name: parentAgentName}, nil
	})

	ctx := security.NewRequestContextForSuperAdmin()
	parentID := uuid.New()
	childID := uuid.New()
	msgID := uuid.New()
	convID := uuid.New()

	childResp := NBAgentResponse{
		Response:  []string{"user has rejected approval (response - no) for this action, stopping"},
		Status:    ConversationStatusCompleted,
		AgentName: "kubectl",
	}
	childAgent := ConversationAgent{ID: childID, ParentAgentID: parentID, MessageID: msgID}
	req := NBAgentRequest{
		ConversationId: convID.String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
	}

	t.Run("parent has saved response → persisted to message", func(t *testing.T) {
		parentAnswer := "The request to scale ml-k8s-server has been cancelled per your instruction."
		fake := &bubbleFakeDao{
			parentState:  "", // stateless → enters the fix branch
			waitingCount: 0,  // all siblings done
			parentAgent:  ConversationAgent{ID: parentID, AgentName: parentAgentName, Response: &parentAnswer},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, childResp, childAgent)
		require.NoError(t, err)

		assert.Equal(t, []string{parentAnswer}, resp.Response, "parent's answer must surface in the response")
		assert.Equal(t, ConversationStatusCompleted, resp.Status)
		assert.Equal(t, "kubectl", resp.AgentName, "non-Response fields preserved from childResp")
		assert.Equal(t, msgID.String(), fake.persistedMsgID)
		assert.Equal(t, parentAnswer, fake.persistedContent, "message must be persisted with the parent answer, not left empty")
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
		assert.Zero(t, fake.getConversationMessageCalls,
			"AnswerConfidenceEnabledForTenant's empty-tenant guard must gate out before the original-question DB read")
	})

	t.Run("parent response nil → falls back to child response", func(t *testing.T) {
		fake := &bubbleFakeDao{
			parentState:  "",
			waitingCount: 0,
			parentAgent:  ConversationAgent{ID: parentID, AgentName: parentAgentName, Response: nil},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, childResp, childAgent)
		require.NoError(t, err)

		assert.Equal(t, childResp.Response, resp.Response, "falls back to child response when parent has none")
		assert.Equal(t, ConversationStatusCompleted, resp.Status, "status is Completed even on fallback")
		assert.Equal(t, childResp.Response[0], fake.persistedContent)
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
		assert.Zero(t, fake.getConversationMessageCalls)
	})
}

// TestBubbleUpIfSiblingsDone_TerminalChildShortCircuits covers the #31997 fix:
// when an opted-in resumed sub-agent completes with IsTerminal (its answer IS the
// final answer — e.g. automation_builder returning the built workflow JSON after
// "Approve and Build"), the bubble-up must finalize from the child and NOT resume
// the parent. Resuming the parent re-runs an ancestor planner which, for a nested
// builder (k8s_debug → automation → automation_builder), re-delegates a fresh
// build and re-prompts for approval in a loop.
//
// The fake deliberately sets a NON-EMPTY parentState: the pre-fix code would then
// proceed to resume the parent (GetAgentNameFromAgentId / GetNBAgent / executeAgent),
// none of which the fake implements — so it would panic via the embedded nil DAO.
// A clean COMPLETED return therefore PROVES the parent-resume path was not taken.
func TestBubbleUpIfSiblingsDone_TerminalChildShortCircuits(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	parentID := uuid.New()
	childID := uuid.New()
	msgID := uuid.New()
	convID := uuid.New()

	const builtJSON = `{"name":"k8s-pod-inventory","definition":{"triggers":[{"type":"manual"}]}}`
	const terminalAgentName = "test_parent_terminal_agent"
	RegisterNBAgentFactory(terminalAgentName, func(string) (NBAgent, error) {
		return parentTerminalAgent{}, nil
	})
	terminalChild := NBAgentResponse{
		Response:   []string{builtJSON},
		Status:     ConversationStatusCompleted,
		IsTerminal: true,
		AgentName:  terminalAgentName,
	}
	childAgent := ConversationAgent{ID: childID, ParentAgentID: parentID, MessageID: msgID, AgentName: terminalAgentName}
	req := NBAgentRequest{
		ConversationId: convID.String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
	}

	t.Run("terminal child finalizes without resuming the (stateful) parent", func(t *testing.T) {
		fake := &bubbleFakeDao{
			parentState:  "non-empty-state-blob-would-trigger-parent-resume",
			waitingCount: 0, // all siblings done
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug"},
		}
		SetConversationDao(fake)

		// Would panic on the embedded nil DAO if the parent-resume path were taken.
		resp, err := bubbleUpIfSiblingsDone(ctx, req, terminalChild, childAgent)
		require.NoError(t, err)

		assert.Equal(t, []string{builtJSON}, resp.Response, "terminal child's response is the final answer")
		assert.Equal(t, ConversationStatusCompleted, resp.Status)
		assert.Equal(t, terminalAgentName, resp.AgentName, "non-Response fields preserved from childResp")
		assert.Equal(t, msgID.String(), fake.persistedMsgID)
		assert.Equal(t, builtJSON, fake.persistedContent, "generation message persisted with the built workflow")
		assert.Equal(t, ConversationStatusCompleted, fake.persistedMsgStatus)
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
		assert.Zero(t, fake.getConversationMessageCalls)
	})

	t.Run("terminal child does NOT strand a still-waiting sibling", func(t *testing.T) {
		// Placement guard: the IsTerminal short-circuit sits AFTER the waitingCount>0
		// check, so a sibling still awaiting user input keeps the conversation WAITING
		// and is never finalized out from under the user.
		fake := &bubbleFakeDao{
			parentState:  "non-empty-state-blob",
			waitingCount: 1, // a sibling still needs the user
			parentAgent:  ConversationAgent{ID: parentID, AgentName: "k8s_debug"},
		}
		SetConversationDao(fake)

		resp, err := bubbleUpIfSiblingsDone(ctx, req, terminalChild, childAgent)
		require.NoError(t, err)

		assert.Equal(t, ConversationStatusWaiting, fake.convStatus, "conversation stays WAITING for the pending sibling")
		assert.Equal(t, []string{builtJSON}, resp.Response)
		assert.Empty(t, fake.persistedMsgID, "no final message persisted while a sibling is still waiting")
	})
}

// TestResumeFollowupLocked_CrossAccountAgentRejected is a security
// regression test for the V2 resume path — the same class of guard proven
// for HandleFollowupDismiss/HandleFollowupResponse in
// followup_cancel_test.go. Checked before the unregistered-agent-impl
// ancestor walk, so a cross-account agentId can't reach that logic (which
// would panic on this fake's embedded nil DAO if it were reached).
func TestResumeFollowupLocked_CrossAccountAgentRejected(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	agentAccountID := uuid.New()
	SetConversationDao(&crossAccountFakeDao{
		agent: ConversationAgent{
			ID:        uuid.New(),
			AccountID: agentAccountID,
			Status:    AgentExecutionStatusWaiting,
		},
	})

	ctx := security.NewRequestContextForSuperAdmin()
	agentID := uuid.New().String()
	resp, err := resumeFollowupLocked(ctx, NBAgentRequest{
		AgentId:        agentID,
		AccountId:      uuid.New().String(), // deliberately different from agentAccountID
		Query:          "some answer",
		ConversationId: uuid.New().String(),
	})
	assert.Equal(t, NBAgentResponse{}, resp)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "not found")
	}
}

// A turn that completed with no usable answer is not gradeable, so it must
// short-circuit before either DB read — the tenant-flag lookup and the
// original-question lookup are both wasted work for it.
//
// Swaps package-level seams; must not run in parallel.
func TestFinalizeCompletedConversation_BlankAnswerTouchesNoDatabase(t *testing.T) {
	origDao := GetConversationDao()
	defer SetConversationDao(origDao)

	origGrader, origFlag := scoreAnswerConfidenceFn, answerConfidenceFeatureCheck
	defer func() { scoreAnswerConfidenceFn, answerConfidenceFeatureCheck = origGrader, origFlag }()

	flagReads := 0
	answerConfidenceFeatureCheck = func(string, string) (bool, error) { flagReads++; return true, nil }
	scoreAnswerConfidenceFn = func(_ *security.RequestContext, _, _, _, messageId, _, _ string) error {
		t.Fatalf("an ungradeable turn reached the grader (message %s)", messageId)
		return nil
	}

	msgID := uuid.New()
	req := NBAgentRequest{
		ConversationId: uuid.New().String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
		Query:          "yes",
	}
	ctx := security.NewRequestContextForTenantAccountAdmin(
		"44444444-4444-4444-4444-444444444444", "user-under-test", []string{req.AccountId})

	for _, blank := range [][]string{nil, {""}, {"   \n"}} {
		fake := &bubbleFakeDao{originalQuestion: "why is the checkout deployment unhealthy"}
		SetConversationDao(fake)

		finalizeCompletedConversation(ctx, req, NBAgentResponse{
			Response:  blank,
			Status:    ConversationStatusCompleted,
			AgentName: "k8s_orchestrator",
		})

		// The turn is still finalized — only grading is skipped.
		assert.Equal(t, msgID.String(), fake.persistedMsgID, "the message must still be persisted")
		assert.Equal(t, ConversationStatusCompleted, fake.convStatus)
		assert.Zero(t, fake.getConversationMessageCalls,
			"no original-question lookup for a turn with answer %q", blank)
	}
	assert.Zero(t, flagReads, "no tenant-flag lookup either — the answer check is free and comes first")
}

// TestFinalizeCompletedConversation_ScoresResumedTurn is the end-to-end
// regression test for #37534: an investigation that paused on a tool
// confirmation and was resumed must still reach the confidence grader, and
// must be graded on the user's original question rather than the answer they
// gave the confirmation prompt.
//
// Before the fix this path emitted nothing at all — resume_v2 finalizes the
// turn itself and never called the grader, so 0 of 91 followup-resumed turns
// on dev carried a score against a 16.3% baseline.
//
// Swaps package-level seams; must not run in parallel.
func TestFinalizeCompletedConversation_ScoresResumedTurn(t *testing.T) {
	const originalQuestion = "why is the checkout deployment unhealthy, and restart it"

	origDao := GetConversationDao()
	defer SetConversationDao(origDao)

	origGrader, origFlag := scoreAnswerConfidenceFn, answerConfidenceFeatureCheck
	defer func() { scoreAnswerConfidenceFn, answerConfidenceFeatureCheck = origGrader, origFlag }()
	answerConfidenceFeatureCheck = func(string, string) (bool, error) { return true, nil }

	type graded struct{ messageID, question, answer string }
	calls := make(chan graded, 4)
	scoreAnswerConfidenceFn = func(_ *security.RequestContext, _, _, _, messageId, question, answer string) error {
		calls <- graded{messageID: messageId, question: question, answer: answer}
		return nil
	}

	fake := &bubbleFakeDao{originalQuestion: originalQuestion}
	SetConversationDao(fake)

	msgID := uuid.New()
	childAgent := ConversationAgent{ID: uuid.New(), ParentAgentID: uuid.Nil, MessageID: msgID}
	// Shaped like a real resume: Query is the confirmation answer, and
	// OriginalQuery is empty because it never survives the resume round-trip.
	req := NBAgentRequest{
		ConversationId: uuid.New().String(),
		MessageId:      msgID.String(),
		AccountId:      uuid.New().String(),
		Query:          "yes",
	}
	childResp := NBAgentResponse{
		Response:  []string{"**Investigation Summary:** the pod was OOMKilled."},
		Status:    ConversationStatusCompleted,
		AgentName: "k8s_orchestrator",
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(
		"33333333-3333-3333-3333-333333333333", "user-under-test", []string{req.AccountId})

	resp, err := bubbleUpIfSiblingsDone(ctx, req, childResp, childAgent)
	require.NoError(t, err)
	assert.Equal(t, ConversationStatusCompleted, resp.Status)

	// The turn is still finalized exactly as before the grader existed.
	assert.Equal(t, msgID.String(), fake.persistedMsgID)
	assert.Equal(t, ConversationStatusCompleted, fake.convStatus)

	select {
	case got := <-calls:
		assert.Equal(t, originalQuestion, got.question,
			"a resumed turn must be graded on the original question")
		assert.NotEqual(t, "yes", got.question,
			`grading "yes" is the bug this test exists to catch`)
		assert.Equal(t, msgID.String(), got.messageID)
		assert.Equal(t, childResp.Response[0], got.answer)
	case <-time.After(10 * time.Second):
		t.Fatal("resumed turn never reached the confidence grader")
	}

	select {
	case extra := <-calls:
		t.Fatalf("resumed turn was graded more than once (second call on %s)", extra.messageID)
	case <-time.After(750 * time.Millisecond):
	}
}

// resumeOverriddenFields are the only fields resume may change. Everything else
// on the request must survive: it arrives fully populated from the normal
// conversation path, and rebuilding it field by field is what silently dropped
// account context, memory, KB content and skills from every resumed turn.
var resumeOverriddenFields = map[string]bool{
	"ParentAgentId":  true,
	"TopLevelResume": true,
	"PreviousState":  true,
	"QueryConfig":    true,
	"ClientTools":    true,
	"Capabilities":   true,
}

func TestBuildResumeAgentRequestCarriesEveryFieldForward(t *testing.T) {
	req := NBAgentRequest{
		Query:               "why is it still restarting?",
		OriginalQuery:       "why does checkout keep restarting?",
		AccountId:           "acct-1",
		UserId:              "user-1",
		AccountContext:      "=== APPLICATION OVERVIEW ===\nservices: primary backend",
		AccountPrompt:       "prefer kubectl over helm",
		MemoryContext:       "<memory>user prefers concise answers</memory>",
		KBPrestepContent:    "runbook: restart loop triage",
		SkillListsMenu:      "skill: k8s-triage",
		ConversationContext: "earlier: checkout deploy at 14:02",
		EnableCritique:      true,
	}

	got := buildResumeAgentRequest(req, "parent-1", "state", toolcore.NBQueryConfig{})

	rv, gv := reflect.ValueOf(req), reflect.ValueOf(got)
	for i := 0; i < rv.NumField(); i++ {
		// An unexported field would panic on Interface(); skip rather than
		// crash, so adding one later fails this test honestly or not at all.
		if !rv.Field(i).CanInterface() {
			continue
		}
		name := rv.Type().Field(i).Name
		if resumeOverriddenFields[name] {
			continue
		}
		assert.Equal(t, rv.Field(i).Interface(), gv.Field(i).Interface(),
			"resume dropped or altered %s — it must carry forward from the first turn", name)
	}

	assert.Equal(t, "parent-1", got.ParentAgentId)
	assert.Equal(t, "state", got.PreviousState)
}

// TestBuildResumeAgentRequestKeepsAccountContextFromDB checks the same contract
// against a real account's stored context, so the rendered <account_context>
// block — which the provider cache slot is keyed on content-wise — is identical
// on the first and resumed turn. Needs a database; skips without TEST_ACCOUNT.
func TestBuildResumeAgentRequestKeepsAccountContextFromDB(t *testing.T) {
	account, tenant := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_TENANT")
	if account == "" || tenant == "" {
		t.Skip("set TEST_ACCOUNT and TEST_TENANT to run against a database")
	}

	ctx := security.NewRequestContextForTenantAdmin(tenant)
	firstTurn := toolcore.LoadActiveGlobalContext(ctx, account)
	require.NotEmpty(t, firstTurn, "account has no active global context — pick one that does")

	resumed := buildResumeAgentRequest(
		NBAgentRequest{Query: "and why is it still failing?", AccountId: account, AccountContext: firstTurn},
		"parent-1", "saved-state", toolcore.NBQueryConfig{},
	)

	assert.Equal(t, firstTurn, resumed.AccountContext)
	assert.Equal(t, renderAccountContextBlock(firstTurn), renderAccountContextBlock(resumed.AccountContext),
		"rendered account context differs across resume — the two prefixes would evict each other")
}

// TestWithOriginalQuestion_ResumedInvestigationKeepsItsShape is the regression
// test for #40085: after an approval, the resumed request carries the answer
// ("yes") as Query and no OriginalQuery, so isTopLevelPlainRetrievalTurn read
// "yes" and the rest of the investigation ran on the lean prompt (and a new
// prompt cache). withOriginalQuestion restores the question from the
// generation message row.
func TestWithOriginalQuestion_ResumedInvestigationKeepsItsShape(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)
	ctx := security.NewRequestContextForSuperAdmin()

	const question = "why is vmsingle restarting? fix it"
	require.True(t, IsInvestigationRequestTask(question), "fixture must be an investigation question")
	req := NBAgentRequest{
		AgentId:            "orch-1",
		MessageId:          uuid.New().String(),
		AccountId:          uuid.New().String(),
		ConversationId:     uuid.New().String(),
		Query:              "yes",
		ConversationSource: ConversationSourceUserInvestigation,
	}
	require.True(t, isTopLevelPlainRetrievalTurn(req), `without the fix "yes" classifies as a plain query`)

	fake := &originalQueryFakeDao{message: ConversationMessage{Message: "  " + question + "\n"}}
	SetConversationDao(fake)

	got := withOriginalQuestion(ctx, req)
	assert.Equal(t, question, got.OriginalQuery)
	assert.True(t, got.OriginalQueryFromUser)
	assert.Equal(t, "yes", got.Query, "Query stays the answer; refine needs it")
	assert.False(t, isTopLevelPlainRetrievalTurn(got), "the resumed turn must stay an investigation")
	assert.Empty(t, promptVariantForRequest(got), "full prompt, not lean")

	t.Run("routed agent mention is stripped like the first run", func(t *testing.T) {
		SetConversationDao(&originalQueryFakeDao{message: ConversationMessage{Message: "@k8s_orchestrator " + question}})
		assert.Equal(t, question, withOriginalQuestion(ctx, req).OriginalQuery)
	})

	t.Run("already set is left alone without a lookup", func(t *testing.T) {
		fake := &originalQueryFakeDao{err: errors.New("must not be called")}
		SetConversationDao(fake)
		set := req
		set.OriginalQuery = "list pods"
		assert.Equal(t, set, withOriginalQuestion(ctx, set))
		assert.Empty(t, fake.calledWithID)
	})

	t.Run("lookup failure leaves the request unchanged", func(t *testing.T) {
		SetConversationDao(&originalQueryFakeDao{err: errors.New("db down")})
		assert.Equal(t, req, withOriginalQuestion(ctx, req))
	})
}

// A resumed top-level agent arrives with OriginalQuery already restored, so
// seedOriginalQuery reports "not top level"; executeAgent must still compose
// memory and discover knowledge for it, on the question rather than the
// answer (#40085).
func TestIsResumedTopLevelInvocation(t *testing.T) {
	for name, tc := range map[string]struct {
		req  NBAgentRequest
		want bool
	}{
		"resumed top level":                 {NBAgentRequest{AgentId: "a", ParentAgentId: "a", TopLevelResume: true, PreviousState: "s", OriginalQuery: "q", Query: "yes"}, true},
		"resumed top level, no saved state": {NBAgentRequest{AgentId: "a", ParentAgentId: "a", TopLevelResume: true, OriginalQuery: "q", Query: "yes"}, true},
		"resumed sub-agent":                 {NBAgentRequest{AgentId: "c", ParentAgentId: "a", PreviousState: "s", OriginalQuery: "q", Query: "yes"}, false},
		"tool request with self parent":     {NBAgentRequest{AgentId: "a", ParentAgentId: "a", OriginalQuery: "q", Query: "logs"}, false},
		"question not recovered":            {NBAgentRequest{AgentId: "a", ParentAgentId: "a", TopLevelResume: true, Query: "yes"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, isResumedTopLevelInvocation(tc.req))
		})
	}

	// The resume marks only the agent that is its own parent.
	assert.True(t, buildResumeAgentRequest(NBAgentRequest{AgentId: "a"}, "a", "", toolcore.NBQueryConfig{}).TopLevelResume)
	assert.False(t, buildResumeAgentRequest(NBAgentRequest{AgentId: "c"}, "a", "s", toolcore.NBQueryConfig{}).TopLevelResume)
}
