package bedrockclient

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// systemBlockTexts extracts the .Value of every SystemContentBlockMemberText
// in order. Other concrete types are returned as the empty string.
func systemBlockTexts(blocks []types.SystemContentBlock) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if t, ok := b.(*types.SystemContentBlockMemberText); ok {
			out = append(out, t.Value)
		} else {
			out = append(out, "")
		}
	}
	return out
}

func TestBuildConverseMessages_DropsEmptySystemBlocks(t *testing.T) {
	cases := []struct {
		name     string
		messages []Message
		expected []string
	}{
		{
			name: "empty system block is dropped",
			messages: []Message{
				{Role: llms.ChatMessageTypeSystem, Content: "react base"},
				{Role: llms.ChatMessageTypeSystem, Content: ""},
				{Role: llms.ChatMessageTypeSystem, Content: "agent prompt"},
			},
			expected: []string{"react base", "agent prompt"},
		},
		{
			name: "whitespace-only system block is dropped",
			messages: []Message{
				{Role: llms.ChatMessageTypeSystem, Content: "react base"},
				{Role: llms.ChatMessageTypeSystem, Content: "   \n\t  "},
				{Role: llms.ChatMessageTypeSystem, Content: "agent prompt"},
			},
			expected: []string{"react base", "agent prompt"},
		},
		{
			name: "all system blocks empty produces no system content",
			messages: []Message{
				{Role: llms.ChatMessageTypeSystem, Content: ""},
				{Role: llms.ChatMessageTypeSystem, Content: "  "},
			},
			expected: []string{},
		},
		{
			name: "non-empty system blocks are preserved in order",
			messages: []Message{
				{Role: llms.ChatMessageTypeSystem, Content: "first"},
				{Role: llms.ChatMessageTypeSystem, Content: "second"},
				{Role: llms.ChatMessageTypeSystem, Content: "third"},
			},
			expected: []string{"first", "second", "third"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, systemBlocks, err := buildConverseMessages(tc.messages)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, systemBlockTexts(systemBlocks))
		})
	}
}

// Repro for issue #30120: the automation agent assembled a system array where
// system[2] was empty, causing Bedrock Converse to reject the request with a
// 400 ValidationException. After the fix, the empty block must be dropped and
// the remaining blocks must keep their relative order.
func TestBuildConverseMessages_Issue30120Repro(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: llms.ChatMessageTypeSystem, Content: "<react base prompt>"},
		{Role: llms.ChatMessageTypeSystem, Content: "<agent additional prompt>"},
		{Role: llms.ChatMessageTypeSystem, Content: ""}, // the offending empty block
		{Role: llms.ChatMessageTypeHuman, Content: "user query"},
	}

	humanMessages, systemBlocks, err := buildConverseMessages(messages)
	require.NoError(t, err)

	// Two non-empty system blocks; the empty third one is filtered.
	assert.Equal(
		t,
		[]string{"<react base prompt>", "<agent additional prompt>"},
		systemBlockTexts(systemBlocks),
	)

	// Human message survives.
	require.Len(t, humanMessages, 1)
	assert.Equal(t, types.ConversationRoleUser, humanMessages[0].Role)
}

func TestBuildConverseMessages_DropsEmptyHumanAndAIContent(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: llms.ChatMessageTypeSystem, Content: "system"},
		{Role: llms.ChatMessageTypeHuman, Content: ""},
		{Role: llms.ChatMessageTypeAI, Content: ""},
		{Role: llms.ChatMessageTypeHuman, Content: "real human input"},
	}

	bedRockMessages, systemBlocks, err := buildConverseMessages(messages)
	require.NoError(t, err)

	assert.Equal(t, []string{"system"}, systemBlockTexts(systemBlocks))
	require.Len(t, bedRockMessages, 1)
	assert.Equal(t, types.ConversationRoleUser, bedRockMessages[0].Role)
}

// Malformed tool_call_response payloads from the LLM must not panic — they
// originate from external model output and may be missing fields or have
// unexpected types. Comma-ok assertions in buildConverseMessages downgrade
// the failure mode from panic to "fields default to empty string".
func TestBuildConverseMessages_ToolCallResponseMissingFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
	}{
		{name: "all fields present", payload: `{"tool_call_id":"id-1","name":"n","content":"c"}`},
		{name: "missing tool_call_id", payload: `{"name":"n","content":"c"}`},
		{name: "wrong type for name", payload: `{"tool_call_id":"id-1","name":42,"content":"c"}`},
		{name: "empty object", payload: `{}`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			messages := []Message{
				{Role: llms.ChatMessageTypeTool, Type: "tool_call_response", Content: tc.payload},
			}
			require.NotPanics(t, func() {
				_, _, _ = buildConverseMessages(messages)
			})
		})
	}
}

// tool_call payloads with a missing/invalid `function` block must skip the
// entry and log, not panic.
func TestBuildConverseMessages_ToolCallMissingFunctionBlockSkips(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
	}{
		{name: "function block missing", payload: `{"id":"id-1","type":"function"}`},
		{name: "function block wrong type", payload: `{"id":"id-1","type":"function","function":"oops"}`},
		{name: "all fields present", payload: `{"id":"id-1","type":"function","function":{"name":"n","arguments":"{}"}}`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			messages := []Message{
				{Role: llms.ChatMessageTypeTool, Type: "tool_call", Content: tc.payload},
			}
			require.NotPanics(t, func() {
				_, _, _ = buildConverseMessages(messages)
			})
		})
	}
}

func TestBuildConverseMessages_CoalescesConsecutiveHumanMessages(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: llms.ChatMessageTypeHuman, Content: "first"},
		{Role: llms.ChatMessageTypeHuman, Content: "second"},
	}

	bedRockMessages, _, err := buildConverseMessages(messages)
	require.NoError(t, err)
	require.Len(t, bedRockMessages, 1)
	assert.Equal(t, types.ConversationRoleUser, bedRockMessages[0].Role)
	assert.Len(t, bedRockMessages[0].Content, 2)
}
