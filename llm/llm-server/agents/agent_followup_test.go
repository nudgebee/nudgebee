package agents

import (
	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFollowupAgent_Call_MultiSelectValidation(t *testing.T) {
	agent := FollowupAgent{}

	tests := []struct {
		name           string
		input          toolcore.NBToolCallRequest
		expectedStatus toolcore.NBToolResponseStatus
		expectError    bool
	}{
		{
			name: "multi_select without options returns error",
			input: toolcore.NBToolCallRequest{
				Command: "Select the labels for this ticket",
				Arguments: map[string]any{
					"followup_type": "multi_select",
				},
			},
			expectedStatus: toolcore.NBToolResponseStatusError,
		},
		{
			name: "multi_select with options succeeds",
			input: toolcore.NBToolCallRequest{
				Command: "Select the labels for this ticket",
				Arguments: map[string]any{
					"followup_type": "multi_select",
					"options":       []any{"bug", "feature", "urgent"},
				},
			},
			expectedStatus: toolcore.NBToolResponseStatusWaiting,
		},
		{
			name: "single_select without options returns error",
			input: toolcore.NBToolCallRequest{
				Command: "Select the priority",
				Arguments: map[string]any{
					"followup_type": "single_select",
				},
			},
			expectedStatus: toolcore.NBToolResponseStatusError,
		},
		{
			name: "single_select with options succeeds",
			input: toolcore.NBToolCallRequest{
				Command: "Select the priority",
				Arguments: map[string]any{
					"followup_type": "single_select",
					"options":       []any{"High", "Medium", "Low"},
				},
			},
			expectedStatus: toolcore.NBToolResponseStatusWaiting,
		},
		{
			name: "text type succeeds without options",
			input: toolcore.NBToolCallRequest{
				Command: "Please describe the issue",
				Arguments: map[string]any{
					"followup_type": "text",
				},
			},
			expectedStatus: toolcore.NBToolResponseStatusWaiting,
		},
		{
			name: "empty command returns error",
			input: toolcore.NBToolCallRequest{
				Command:   "",
				Arguments: map[string]any{},
			},
			expectedStatus: toolcore.NBToolResponseStatusError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := toolcore.NbToolContext{ToolCallId: "test-tool-id"}
			resp, err := agent.Call(ctx, tc.input)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedStatus, resp.Status)

			if tc.expectedStatus == toolcore.NBToolResponseStatusWaiting {
				details := resp.AdditionalDetails["followup_request"]
				assert.NotNil(t, details)
				followup, ok := details.(core.FollowupRequest)
				assert.True(t, ok)
				assert.Equal(t, tc.input.Command, followup.Question)
			}
		})
	}
}

func TestFollowupAgent_Call_MultiSelectFollowupType(t *testing.T) {
	agent := FollowupAgent{}
	ctx := toolcore.NbToolContext{ToolCallId: "test-tool-id"}

	resp, err := agent.Call(ctx, toolcore.NBToolCallRequest{
		Command: "Select labels",
		Arguments: map[string]any{
			"followup_type": "multi_select",
			"options":       []any{"bug", "feature", "urgent"},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, toolcore.NBToolResponseStatusWaiting, resp.Status)

	followup := resp.AdditionalDetails["followup_request"].(core.FollowupRequest)
	assert.Equal(t, core.FollowupTypeMultiSelect, followup.FollowupType)
	assert.Equal(t, []string{"bug", "feature", "urgent"}, followup.FollowupOptions)
}

func TestFollowupAgent_Call_TextWithOptionsAutoCorrects(t *testing.T) {
	agent := FollowupAgent{}
	ctx := toolcore.NbToolContext{ToolCallId: "test-tool-id"}

	resp, err := agent.Call(ctx, toolcore.NBToolCallRequest{
		Command: "Select priority",
		Arguments: map[string]any{
			"followup_type": "text",
			"options":       []any{"High", "Low"},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, toolcore.NBToolResponseStatusWaiting, resp.Status)

	followup := resp.AdditionalDetails["followup_request"].(core.FollowupRequest)
	// text + options should auto-correct to single_select
	assert.Equal(t, core.FollowupTypeSingleSelect, followup.FollowupType)
}

func TestExtractOptionsFromText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "options with bracket pattern",
			text:     "Please select the priority. Options: [High, Medium, Low]",
			expected: []string{"High", "Medium", "Low"},
		},
		{
			name:     "no options pattern",
			text:     "What is the issue description?",
			expected: nil,
		},
		{
			name:     "choose from pattern",
			text:     "Choose from: [bug, feature, improvement]",
			expected: []string{"bug", "feature", "improvement"},
		},
		{
			name:     "single option is not extracted",
			text:     "Options: [single]",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractOptionsFromText(tc.text)
			assert.Equal(t, tc.expected, result)
		})
	}
}
