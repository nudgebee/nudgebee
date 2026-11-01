package api

import (
	"encoding/json"
	"errors"
	"testing"

	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// agentRunDao embeds IConversationDao (nil) and answers only the agent-run
// lookup, recording what it was asked for.
type agentRunDao struct {
	core.IConversationDao
	err   error
	calls int
	asked [3]string
}

func (d *agentRunDao) GetAgentRunAttribution(agentId, accountId, agentName string) (string, string, string, error) {
	d.calls++
	d.asked = [3]string{agentId, accountId, agentName}
	if d.err != nil {
		return "", "", "", d.err
	}
	return "conv-1", "msg-1", "user-1", nil
}

func TestWorkspaceUsageLink(t *testing.T) {
	const runID = "7b0f6a52-1d0e-4a57-9d0c-0a6f1d2b3c4d"

	tests := []struct {
		name       string
		req        workspaceLLMRequest
		daoErr     error
		wantLinked bool
		wantLookup bool
	}{
		{
			name:       "run found in the token's account is linked",
			req:        workspaceLLMRequest{AccountID: "acct-1", AgentRunID: runID, ModelTier: "retrieval", TaskType: "reflection"},
			wantLinked: true,
			wantLookup: true,
		},
		{
			name: "no run id is unlinked without a lookup",
			req:  workspaceLLMRequest{AccountID: "acct-1"},
		},
		{
			name: "malformed run id is unlinked without a lookup",
			req:  workspaceLLMRequest{AccountID: "acct-1", AgentRunID: "not-a-uuid"},
		},
		{
			// Another account's run, or a run of another agent, matches no row.
			name:       "run that does not resolve is unlinked",
			req:        workspaceLLMRequest{AccountID: "acct-1", AgentRunID: runID},
			daoErr:     errors.New("sql: no rows in result set"),
			wantLookup: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dao := &agentRunDao{err: tt.daoErr}
			previous := core.GetConversationDao()
			core.SetConversationDao(dao)
			t.Cleanup(func() { core.SetConversationDao(previous) })

			link, userID, linked := workspaceUsageLink(security.NewRequestContextForTenantAdmin("tenant-1"), tt.req)

			assert.Equal(t, tt.wantLinked, linked)
			if tt.wantLookup {
				require.Equal(t, 1, dao.calls)
				assert.Equal(t, [3]string{runID, "acct-1", agents.AgentCodeAnalyzer}, dao.asked)
			} else {
				assert.Zero(t, dao.calls)
			}
			if !tt.wantLinked {
				assert.Equal(t, core.UsageLink{}, link)
				assert.Empty(t, userID)
				return
			}
			assert.Equal(t, core.UsageLink{ConversationID: "conv-1", MessageID: "msg-1", AgentID: runID, ModelTier: "retrieval", TaskType: "reflection"}, link)
			assert.Equal(t, "user-1", userID)
		})
	}
}

// The reply stays the generation result a workspace on an older image decodes,
// with the usage receipt as one extra key.
func TestWorkspaceLLMResponseShape(t *testing.T) {
	generated := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "ok", StopReason: "stop"}}}

	linked, err := json.Marshal(workspaceLLMResponse{ContentResponse: generated, UsageLinked: true})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(linked, &decoded))
	assert.Equal(t, true, decoded["usage_linked"])
	var plain llms.ContentResponse
	require.NoError(t, json.Unmarshal(linked, &plain))
	require.Len(t, plain.Choices, 1)
	assert.Equal(t, "ok", plain.Choices[0].Content)

	unlinked, err := json.Marshal(workspaceLLMResponse{ContentResponse: generated})
	require.NoError(t, err)
	bare, err := json.Marshal(generated)
	require.NoError(t, err)
	assert.JSONEq(t, string(bare), string(unlinked))
}

// workspaceWirePart is a message part as a workspace encodes it: langchaingo's
// own JSON for the tool call and the tool result.
type workspaceWirePart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ToolCall     *llms.ToolCall         `json:"tool_call,omitempty"`
	ToolResponse *llms.ToolCallResponse `json:"tool_response,omitempty"`
}

type workspaceWireMessage struct {
	Role  string              `json:"role"`
	Parts []workspaceWirePart `json:"parts"`
}

func decodeWorkspaceWire(t *testing.T, wire []workspaceWireMessage) []workspaceLLMMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"account_id": "acct-1", "messages": wire})
	require.NoError(t, err)
	var req workspaceLLMRequest
	require.NoError(t, json.Unmarshal(body, &req))
	return req.Messages
}

// A step's history reaches the provider client intact: each tool call keeps its
// id, name and arguments, and each tool result is its own tool message.
func TestWorkspaceLLMMessagesKeepToolCallsAndSplitResults(t *testing.T) {
	viewCall := llms.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"main.go"}`}}
	searchCall := llms.ToolCall{ID: "call-2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: `{"pattern":"listen"}`}}
	viewResult := llms.ToolCallResponse{ToolCallID: "call-1", Name: "file_view", Content: "package main"}
	searchResult := llms.ToolCallResponse{ToolCallID: "call-2", Name: "rg", Content: "main.go:12"}
	lastResult := llms.ToolCallResponse{ToolCallID: "call-3", Name: "file_find", Content: "main.go"}

	messages, err := workspaceLLMMessages(decodeWorkspaceWire(t, []workspaceWireMessage{
		{Role: "human", Parts: []workspaceWirePart{{Type: "text", Text: "where does it listen?"}}},
		{Role: "ai", Parts: []workspaceWirePart{
			{Type: "text", Text: "reading both"},
			{Type: "tool_call", ToolCall: &viewCall},
			{Type: "tool_call", ToolCall: &searchCall},
		}},
		{Role: "tool", Parts: []workspaceWirePart{
			{Type: "tool_response", ToolResponse: &viewResult},
			{Type: "tool_response", ToolResponse: &searchResult},
		}},
		{Role: "tool", Parts: []workspaceWirePart{{Type: "tool_response", ToolResponse: &lastResult}}},
	}))

	require.NoError(t, err)
	assert.Equal(t, []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("where does it listen?")}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextPart("reading both"), viewCall, searchCall}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{viewResult}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{searchResult}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{lastResult}},
	}, messages)
}

func TestWorkspaceLLMMessagesRejectMalformedParts(t *testing.T) {
	tests := []struct {
		name    string
		part    workspaceWirePart
		wantErr string
	}{
		{name: "tool call without a function", part: workspaceWirePart{Type: "tool_call", ToolCall: &llms.ToolCall{ID: "call-1", Type: "function"}}, wantErr: "tool_call part is missing its function"},
		{name: "tool call part without a tool call", part: workspaceWirePart{Type: "tool_call"}, wantErr: "tool_call part is missing its function"},
		{name: "tool response part without a response", part: workspaceWirePart{Type: "tool_response"}, wantErr: "tool_response part is missing tool_response"},
		{name: "unknown part type", part: workspaceWirePart{Type: "image"}, wantErr: `unsupported message part type "image"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workspaceLLMMessages(decodeWorkspaceWire(t, []workspaceWireMessage{{Role: "ai", Parts: []workspaceWirePart{tt.part}}}))

			assert.EqualError(t, err, tt.wantErr)
		})
	}
}
