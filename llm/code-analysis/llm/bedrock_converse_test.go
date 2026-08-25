package llm

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func textMsg(role llms.ChatMessageType, s string) llms.MessageContent {
	return llms.MessageContent{Role: role, Parts: []llms.ContentPart{llms.TextContent{Text: s}}}
}

func TestBuildConverseMessages_SystemBlocksAreSeparated(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "you are a code agent"),
		textMsg(llms.ChatMessageTypeHuman, "find the bug"),
	}

	converse, system, err := buildConverseMessages(msgs)
	require.NoError(t, err)

	require.Len(t, system, 1, "system content travels in its own field, not as a message")
	require.Len(t, converse, 1)
	assert.Equal(t, types.ConversationRoleUser, converse[0].Role)
}

// Converse rejects zero-length blocks with a ValidationException.
func TestBuildConverseMessages_DropsEmptyText(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "   "),
		textMsg(llms.ChatMessageTypeHuman, ""),
		textMsg(llms.ChatMessageTypeHuman, "real content"),
	}

	converse, system, err := buildConverseMessages(msgs)
	require.NoError(t, err)

	assert.Empty(t, system)
	require.Len(t, converse, 1)
	assert.Len(t, converse[0].Content, 1)
}

// Converse rejects consecutive messages with the same role.
func TestBuildConverseMessages_CoalescesSameRoleRuns(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "first"),
		textMsg(llms.ChatMessageTypeHuman, "second"),
		textMsg(llms.ChatMessageTypeAI, "reply"),
	}

	converse, _, err := buildConverseMessages(msgs)
	require.NoError(t, err)

	require.Len(t, converse, 2, "the two human turns must coalesce")
	assert.Len(t, converse[0].Content, 2)
	assert.Equal(t, types.ConversationRoleAssistant, converse[1].Role)
}

// The regression this file exists for: Bedrock rejects a turn holding both tool
// results and ordinary content — "Conversation blocks and tool result blocks
// cannot be provided in the same turn" — and roles must still alternate, so the
// trailing text has to be folded into the tool result rather than appended
// beside it or split into a second user turn.
func TestBuildConverseMessages_TextAfterToolResultStaysOutOfTheTurn(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "investigate"),
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "call-1", Type: "function",
				FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"main.go"}`}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{ToolCallID: "call-1", Name: "file_view", Content: "package main"},
		}},
		textMsg(llms.ChatMessageTypeHuman, "now answer"),
	}

	converse, _, err := buildConverseMessages(msgs)
	require.NoError(t, err)

	for i, m := range converse {
		var toolResults, other int
		for _, c := range m.Content {
			if _, ok := c.(*types.ContentBlockMemberToolResult); ok {
				toolResults++
			} else {
				other++
			}
		}
		assert.False(t, toolResults > 0 && other > 0,
			"turn %d mixes %d tool-result and %d ordinary blocks", i, toolResults, other)
	}

	// Roles must alternate.
	for i := 1; i < len(converse); i++ {
		assert.NotEqual(t, converse[i-1].Role, converse[i].Role, "consecutive same-role turns at %d", i)
	}
}

// A tool call must survive as a ToolUse block, or the model never learns what it
// already asked for.
func TestBuildConverseMessages_ToolCallBecomesToolUse(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "go"),
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "call-9", Type: "function",
				FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: `{"pattern":"func main"}`}},
		}},
	}

	converse, _, err := buildConverseMessages(msgs)
	require.NoError(t, err)
	require.Len(t, converse, 2)

	use, ok := converse[1].Content[0].(*types.ContentBlockMemberToolUse)
	require.True(t, ok, "expected a ToolUse block, got %T", converse[1].Content[0])
	assert.Equal(t, "rg", *use.Value.Name)
	assert.Equal(t, "call-9", *use.Value.ToolUseId)
}

func TestBuildConverseMessages_MalformedToolArgumentsError(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "x", FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: "{not json"}},
		}},
	}

	_, _, err := buildConverseMessages(msgs)
	assert.Error(t, err, "unparseable tool arguments must not be sent silently")
}

// The whole point of moving off langchaingo: tools have to reach the model.
func TestBuildConverseToolConfig(t *testing.T) {
	t.Run("maps tools to specs", func(t *testing.T) {
		cfg := buildConverseToolConfig([]llms.Tool{{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "file_view",
				Description: "read a file",
				Parameters:  map[string]any{"type": "object"},
			},
		}})
		require.NotNil(t, cfg)
		require.Len(t, cfg.Tools, 1)
		spec, ok := cfg.Tools[0].(*types.ToolMemberToolSpec)
		require.True(t, ok)
		assert.Equal(t, "file_view", *spec.Value.Name)
	})

	t.Run("nil when there is nothing usable", func(t *testing.T) {
		assert.Nil(t, buildConverseToolConfig(nil), "Converse rejects an empty ToolConfig")
		assert.Nil(t, buildConverseToolConfig([]llms.Tool{{Function: nil}}))
		assert.Nil(t, buildConverseToolConfig([]llms.Tool{{Function: &llms.FunctionDefinition{Name: ""}}}))
	})

	t.Run("a tool with no parameters still gets a schema", func(t *testing.T) {
		cfg := buildConverseToolConfig([]llms.Tool{{
			Function: &llms.FunctionDefinition{Name: "noargs", Description: "d"},
		}})
		require.NotNil(t, cfg)
		require.Len(t, cfg.Tools, 1)
	})
}

// The mirror of the turn-purity case: ordinary content already in the turn, and
// a tool result arriving after it. Folding only the last block would still leave
// an earlier text block sitting beside a tool result.
func TestBuildConverseMessages_ToolResultAfterTextStaysOutOfTheTurn(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "first note"),
		textMsg(llms.ChatMessageTypeHuman, "second note"),
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{ToolCallID: "call-1", Name: "rg", Content: "match"},
		}},
	}

	converse, _, err := buildConverseMessages(msgs)
	require.NoError(t, err)

	for i, m := range converse {
		var toolResults, other int
		for _, c := range m.Content {
			if _, ok := c.(*types.ContentBlockMemberToolResult); ok {
				toolResults++
			} else {
				other++
			}
		}
		assert.False(t, toolResults > 0 && other > 0,
			"turn %d mixes %d tool-result and %d ordinary blocks", i, toolResults, other)
	}
	for i := 1; i < len(converse); i++ {
		assert.NotEqual(t, converse[i-1].Role, converse[i].Role, "consecutive same-role turns at %d", i)
	}
}

// Converse rejects zero-length text, so a tool that produced no output must not
// take the whole request down.
func TestBuildConverseMessages_EmptyToolResultGetsPlaceholder(t *testing.T) {
	for _, empty := range []string{"", "   ", "\n\t"} {
		msgs := []llms.MessageContent{
			textMsg(llms.ChatMessageTypeHuman, "go"),
			{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
				llms.ToolCall{ID: "c1", FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: "{}"}},
			}},
			{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
				llms.ToolCallResponse{ToolCallID: "c1", Name: "rg", Content: empty},
			}},
		}

		converse, _, err := buildConverseMessages(msgs)
		require.NoError(t, err)

		last := converse[len(converse)-1]
		tr, ok := last.Content[0].(*types.ContentBlockMemberToolResult)
		require.True(t, ok)
		text, ok := tr.Value.Content[0].(*types.ToolResultContentBlockMemberText)
		require.True(t, ok)
		assert.NotEmpty(t, text.Value, "empty tool output must not produce a zero-length block")
	}
}

// Defensive paths in response parsing. Input is an interface, so a tool invoked
// with no arguments arrives nil and would panic on MarshalSmithyDocument.
func TestParseConverseOutput_Defensive(t *testing.T) {
	t.Run("nil response is an error, not a panic", func(t *testing.T) {
		_, err := parseConverseOutput(nil)
		assert.Error(t, err)
	})

	t.Run("argument-less tool call renders as an empty object", func(t *testing.T) {
		resp := &bedrockruntime.ConverseOutput{
			Output: &types.ConverseOutputMemberMessage{
				Value: types.Message{
					Role: types.ConversationRoleAssistant,
					Content: []types.ContentBlock{
						&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
							Name:      aws.String("noargs"),
							ToolUseId: aws.String("c1"),
							Input:     nil,
						}},
					},
				},
			},
		}

		out, err := parseConverseOutput(resp)
		require.NoError(t, err)
		require.Len(t, out.Choices, 1)
		require.Len(t, out.Choices[0].ToolCalls, 1)
		assert.Equal(t, "{}", out.Choices[0].ToolCalls[0].FunctionCall.Arguments)
	})
}

// A request with nothing but system content cannot be sent; fail with something
// that names the problem instead of an opaque 400.
func TestGenerateContent_NoSendableMessages(t *testing.T) {
	b := newBedrockConverseLLM(nil, "test-model")
	_, err := b.GenerateContent(context.Background(), []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "only a system prompt"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no non-system message")
}

// Converse requires the exchange to open with a user turn.
func TestBuildConverseMessages_DropsLeadingAssistantTurn(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeAI, "unsolicited opener"),
		textMsg(llms.ChatMessageTypeHuman, "question"),
	}

	converse, _, err := buildConverseMessages(msgs)
	require.NoError(t, err)
	require.NotEmpty(t, converse)
	assert.Equal(t, types.ConversationRoleUser, converse[0].Role)
}
