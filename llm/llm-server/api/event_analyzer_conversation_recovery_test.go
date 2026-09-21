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
func TestAutomaticStageRecoveryUsesFreshnessGuard(t *testing.T) {
	source, err := os.ReadFile("event_analyzer.go")
	if !assert.NoError(t, err) {
		return
	}

	directCalls := regexp.MustCompile(`(?i)getAgentResponseFromConversation\s*\(\s*ctx\s*,`).FindAll(source, -1)
	require.GreaterOrEqual(t, len(directCalls), 5, "conversation recovery call pattern must still match the expected sites")
	assert.Len(t, directCalls, 5,
		"RCA and the four guarded automatic-stage lookups (summary, investigation, code analysis, synthesis) must remain the only direct recovery sites")
	guardSites := regexp.MustCompile(`(?i)shouldRecoverStageFromConversation\s*\(`).FindAll(source, -1)
	require.GreaterOrEqual(t, len(guardSites), 5, "freshness guard pattern must still match the helper and automatic stages")
	assert.Len(t, guardSites, 5,
		"the helper definition plus summary, investigation, code-analysis, and synthesis gates must remain present")
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
