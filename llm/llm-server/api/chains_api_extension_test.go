package api

import (
	"github.com/stretchr/testify/assert"
	toolcore "nudgebee/llm/tools/core"
	"testing"
)

func TestConversationApiRequestClientTools(t *testing.T) {
	request := ConversationApiRequest{
		Query:     "test query",
		AccountId: "test-account",
		UserId:    "test-user",
		ClientTools: []toolcore.NBToolCommand{
			{
				Name:        "local_shell",
				Description: "Execute local shell commands",
				InputSchema: toolcore.ToolSchema{
					Type: toolcore.ToolSchemaTypeObject,
					Properties: map[string]toolcore.ToolSchemaProperty{
						"command": {
							Type: toolcore.ToolSchemaTypeString,
						},
					},
				},
			},
		},
	}

	assert.Equal(t, "test query", request.Query)
	assert.NotNil(t, request.ClientTools)
	assert.Equal(t, 1, len(request.ClientTools))
	assert.Equal(t, "local_shell", request.ClientTools[0].Name)
}

func TestClientToolResultApiRequestStruct(t *testing.T) {
	// This test will fail to compile initially because ClientToolResultApiRequest doesn't exist yet.
	request := ClientToolResultApiRequest{
		ConversationId: "test-conversation",
		MessageId:      "test-message",
		AgentId:        "test-agent",
		AccountId:      "test-account",
		Results: []ClientToolResultItem{
			{
				ToolId: "test-tool",
				Result: "execution output",
				Status: "SUCCESS",
			},
		},
	}

	assert.Equal(t, "test-conversation", request.ConversationId)
	assert.Equal(t, 1, len(request.Results))
	assert.Equal(t, "SUCCESS", request.Results[0].Status)
}
