package core

import (
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingTool records the context a tool call was made with.
type capturingTool struct {
	got toolcore.NbToolContext
}

func (c *capturingTool) Name() string                     { return "capturing_tool" }
func (c *capturingTool) Description() string              { return "records its context" }
func (c *capturingTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (c *capturingTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (c *capturingTool) Call(ctx toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	c.got = ctx
	return toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusSuccess}, nil
}

// A tool reads the original request to learn what the user asked for. Whether
// that text is the user's own is decided once, where the original request is
// recorded, and has to arrive with it: a sub-agent's request carries no
// conversation source to decide it from.
func TestOriginalQueryFromUser_ReachesToolCalls(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"account"})
	for _, fromUser := range []bool{true, false} {
		tool := &capturingTool{}
		_, err := callNbTool(ctx, NBAgentRequest{
			AccountId: "account", UserId: "user", ConversationId: "conversation", MessageId: "message", AgentId: "agent",
			Query:                 "all logs for app api last 24h, limit 5000",
			OriginalQuery:         "what happened in the last 30m",
			OriginalQueryFromUser: fromUser,
			// A sub-agent's request: the conversation source is not carried down.
		}, "logs", tool, `{"command":"all logs for app api last 24h, limit 5000"}`, nil, "", "tool-call-1", "")
		require.NoError(t, err)
		assert.Equal(t, "what happened in the last 30m", tool.got.OriginalQuery)
		assert.Equal(t, fromUser, tool.got.OriginalQueryFromUser)
	}
}

// Only a chat a person is in counts as the user's own text. An event
// investigation, an automation or a notification starts from a generated
// prompt, whatever it happens to say.
func TestSeedOriginalQuery(t *testing.T) {
	for source, fromUser := range map[ConversationSource]bool{
		ConversationSourceUserInvestigation:    true,
		ConversationSourceUserInvestigationCLI: true,
		ConversationSourceInvestigation:        false,
		ConversationSourceAutomation:           false,
		ConversationSourceInstantNotification:  false,
		"":                                     false,
	} {
		request := NBAgentRequest{Query: "investigate the alert. Do NOT fetch ranges like \"last 2h\"", ConversationSource: source}
		assert.True(t, seedOriginalQuery(&request), "top level: %q", source)
		assert.Equal(t, request.Query, request.OriginalQuery)
		assert.Equal(t, fromUser, request.OriginalQueryFromUser, "source %q", source)
	}

	underParent := NBAgentRequest{Query: "get logs for api", OriginalQuery: "why is api failing", OriginalQueryFromUser: true}
	assert.False(t, seedOriginalQuery(&underParent))
	assert.Equal(t, "why is api failing", underParent.OriginalQuery)
	assert.True(t, underParent.OriginalQueryFromUser, "a sub-agent keeps what its parent recorded")
}
