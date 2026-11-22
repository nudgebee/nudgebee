package api

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestShouldRecoverStageFromConversation(t *testing.T) {
	repo := events.NewEventAnalysisRepository(nil)
	repo.SetAnalysisFreshness(24 * time.Hour)

	tests := []struct {
		name       string
		analysis   *events.EventAnalysis
		regenerate bool
		want       bool
	}{
		{name: "missing row recovers an interrupted database write", want: true},
		{
			name:     "fresh row may recover conversation history",
			analysis: &events.EventAnalysis{UpdatedAt: time.Now().Add(-time.Hour)},
			want:     true,
		},
		{
			name:     "stale row must rerun the stage",
			analysis: &events.EventAnalysis{UpdatedAt: time.Now().Add(-25 * time.Hour), Status: string(events.AnalysisStatusCompleted)},
			want:     false,
		},
		{
			name:     "in-progress row waiting past freshness window recovers completed conversation",
			analysis: &events.EventAnalysis{UpdatedAt: time.Now().Add(-25 * time.Hour), Status: string(events.AnalysisStatusInProgress)},
			want:     true,
		},
		{
			name:       "explicit regeneration never recovers history",
			analysis:   &events.EventAnalysis{UpdatedAt: time.Now().Add(-time.Hour)},
			regenerate: true,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldRecoverStageFromConversation(repo, tt.analysis, tt.regenerate))
		})
	}
}

// Keep the three automatic-analysis stages on the freshness-aware recovery
// path. The remaining direct call is RCA, whose user-triggered regeneration
// semantics are intentionally separate from the automatic freshness window.
// The detailed-response stage no longer appears here: #38925 replaced its LLM
// pass with deterministic assembly, so there is no model answer to recover.
func TestAutomaticStageRecoveryUsesFreshnessGuard(t *testing.T) {
	source, err := os.ReadFile("event_analyzer.go")
	if !assert.NoError(t, err) {
		return
	}

	directCalls := regexp.MustCompile(`(?i)getAgentResponseFromConversation\s*\(\s*ctx\s*,`).FindAll(source, -1)
	require.GreaterOrEqual(t, len(directCalls), 4, "conversation recovery call pattern must still match the expected sites")
	assert.Len(t, directCalls, 4,
		"RCA and the three guarded automatic-stage lookups (summary, investigation, code analysis) must remain the only direct recovery sites")
	guardSites := regexp.MustCompile(`(?i)shouldRecoverStageFromConversation\s*\(`).FindAll(source, -1)
	require.GreaterOrEqual(t, len(guardSites), 4, "freshness guard pattern must still match the helper and automatic stages")
	assert.Len(t, guardSites, 4,
		"the helper definition plus summary, investigation and code-analysis gates must remain present")
}

// Reproduces event a1ffed9c: the debug agent paused on a tool-approval
// followup, the user answered "yes", and stage recovery stored that "yes" as
// the investigation because the followup row is newer than the generation row
// that actually carries the RCA.
func TestLatestAgentGenerationResponseIgnoresFollowupReplies(t *testing.T) {
	agentName := "aws_orchestrator"
	summaryAgent := "events"
	rca := "### 📝 Event Summary\n- **Root Cause:** SSM RunShellScript spawned CPU busy loops"
	messages := []core.ConversationMessage{
		{
			AgentName:   &summaryAgent,
			MessageType: string(core.MessageTypeGeneration),
			Response:    "### 1. Event Details",
			Status:      core.ConversationStatusCompleted,
		},
		{
			AgentName:   &agentName,
			MessageType: string(core.MessageTypeGeneration),
			Response:    rca,
			Status:      core.ConversationStatusCompleted,
		},
		{
			AgentName:   &agentName,
			MessageType: string(core.MessageTypeFollowup),
			Response:    "yes",
			Status:      core.ConversationStatusCompleted,
		},
	}

	got, found := latestAgentGenerationResponse(messages, agentName)
	assert.True(t, found)
	assert.Equal(t, rca, got)
}

func TestLatestAgentGenerationResponseSkipsUnfinishedAndForeignMessages(t *testing.T) {
	agentName := "aws_orchestrator"
	other := "k8s_orchestrator"
	messages := []core.ConversationMessage{
		{AgentName: &other, MessageType: string(core.MessageTypeGeneration), Response: "not mine", Status: core.ConversationStatusCompleted},
		{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Response: "", Status: core.ConversationStatusCompleted},
		{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Response: "still running", Status: core.ConversationStatusInProgress},
		{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Response: "yes", Status: core.ConversationStatusCompleted},
	}

	got, found := latestAgentGenerationResponse(messages, agentName)
	assert.False(t, found)
	assert.Empty(t, got)
}

// An approval can complete a followup row while its owning generation is still
// pending (or has failed). Falling back to a previous generation would persist
// obsolete findings as the result of the current event investigation (#37865).
func TestLatestAgentGenerationResponseDoesNotReusePreviousGeneration(t *testing.T) {
	agentName := "aws_orchestrator"
	tests := []struct {
		name     string
		status   core.ConversationStatus
		response string
	}{
		{"waiting for approval", core.ConversationStatusWaiting, "Please approve the tool"},
		{"waiting for client tool", core.ConversationStatusWaitingForClientTool, ""},
		{"resumed on another worker", core.ConversationStatusInProgress, ""},
		{"failed generation", core.ConversationStatusFailed, "failed to execute"},
		{"completed without findings", core.ConversationStatusCompleted, ""},
		{"completed with whitespace", core.ConversationStatusCompleted, " \n\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []core.ConversationMessage{
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: "obsolete investigation"},
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: tt.status, Response: tt.response},
				{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Status: core.ConversationStatusCompleted, Response: "yes"},
			}
			got, found := latestAgentGenerationResponse(messages, agentName)
			assert.False(t, found, "the newest generation must be usable before its stage can be recovered")
			assert.Empty(t, got)
		})
	}
}

func TestLatestAgentGenerationResponsePreservesCompletedFindingsAfterApproval(t *testing.T) {
	agentName := "aws_orchestrator"
	otherAgent := "events"
	for _, approval := range []string{"yes", "no", "test-pg", ""} {
		t.Run("followup="+approval, func(t *testing.T) {
			want := "  Root cause: database retry loop.\n"
			messages := []core.ConversationMessage{
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: "obsolete investigation"},
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: want},
				{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Status: core.ConversationStatusCompleted, Response: approval},
				{AgentName: &otherAgent, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusWaiting, Response: "unrelated stage"},
			}
			got, found := latestAgentGenerationResponse(messages, agentName)
			require.True(t, found)
			assert.Equal(t, want, got, "preserve the generation text verbatim; approval wording is irrelevant")
		})
	}
}

// Reproduces dev events 11271ebc and f2581730: a new firing's stage row was
// IN_PROGRESS, so recovery ran against the fingerprint conversation and saved
// the previous firing's answer as the new event's analysis, with no agent run.
func TestMessagesForEventIgnoresEarlierFirings(t *testing.T) {
	summaryAgent, debugAgent, synth := "events", "k8s_orchestrator", "event_detailed_response"
	previous, current := "ab70097a-f890-47de-875b-f57663939b8b", "11271ebc-6867-41fc-afdf-33b9434134f9"
	gen := func(agent, message, response string) core.ConversationMessage {
		return core.ConversationMessage{AgentName: &agent, MessageType: string(core.MessageTypeGeneration),
			Message: message, Response: response, Status: core.ConversationStatusCompleted}
	}
	earlier := []core.ConversationMessage{
		gen(summaryAgent, "Get the details of Event with id - "+previous, "old summary"),
		gen(debugAgent, "Identify RootCause of the event with id "+previous, "old RCA"),
		gen(synth, "synthesize detailed response", "old report"),
	}

	t.Run("new firing with no run yet recovers nothing", func(t *testing.T) {
		for _, agent := range []string{summaryAgent, debugAgent, synth} {
			got, found := latestAgentGenerationResponse(messagesForEvent(earlier, current), agent)
			assert.False(t, found, agent)
			assert.Empty(t, got, agent)
		}
	})

	t.Run("interrupted run of this firing recovers its own answers", func(t *testing.T) {
		messages := append(append([]core.ConversationMessage{}, earlier...),
			gen(summaryAgent, "Get the details of Event with id - "+current, "new summary"),
			gen(debugAgent, "## Runbook\n...\nIdentify RootCause of the event with id "+current, "new RCA"),
		)
		scoped := messagesForEvent(messages, current)

		got, found := latestAgentGenerationResponse(scoped, debugAgent)
		assert.True(t, found)
		assert.Equal(t, "new RCA", got)

		// Synthesis has not run for this firing; the older report must not stand in.
		_, found = latestAgentGenerationResponse(scoped, synth)
		assert.False(t, found)
	})

	t.Run("empty event id keeps the whole conversation", func(t *testing.T) {
		assert.Len(t, messagesForEvent(earlier, ""), len(earlier))
	})
}

// Every automatic stage must scope recovery to the event and to its run (the
// run is zero with the remediation loop off); only RCA, whose attempts own their
// session, may pass an empty event id and no run.
func TestAutomaticStageRecoveryIsScopedToEvent(t *testing.T) {
	source, err := os.ReadFile("event_analyzer.go")
	require.NoError(t, err)

	calls := regexp.MustCompile(`getAgentResponseFromConversation\(ctx, [^\n]*\)`).FindAll(source, -1)
	require.Len(t, calls, 4)
	unscoped := 0
	for _, c := range calls {
		if regexp.MustCompile(`, "", events\.EventRun\{\}\)$`).Match(c) {
			unscoped++
			assert.Contains(t, string(c), "rcaAgent.GetName()", "only the RCA lookup may skip event scoping")
		} else {
			assert.Contains(t, string(c), "request.EventId, request.run)", "automatic stages must pass the event id and the run")
		}
	}
	assert.Equal(t, 1, unscoped)
}
