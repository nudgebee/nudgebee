package core

import (
	"testing"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestWriteConfirmationRequired: write tools (create/update/delete) prompt, except
// watch_resource which is exempt; watch_cancel is intentionally NOT exempt.
func TestWriteConfirmationRequired(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }

	cases := []struct {
		name        string
		requestType *toolcore.ToolRequestType
		toolName    string
		want        bool
	}{
		{"nil request type", nil, "kubectl_execute", false},
		{"empty request type", rt(""), "kubectl_execute", false},
		{"read is never confirmed", rt(toolcore.ToolRequestTypeRead), "kubectl_execute", false},
		{"create write tool confirms", rt(toolcore.ToolRequestTypeCreate), "kubectl_execute", true},
		{"update write tool confirms", rt(toolcore.ToolRequestTypeUpdate), "aws_execute", true},
		{"delete write tool confirms", rt(toolcore.ToolRequestTypeDelete), "shell_execute", true},
		{"watch_resource create is exempt", rt(toolcore.ToolRequestTypeCreate), tools.ToolWatchResource, false},
		{"watch_resource exempt case-insensitive", rt(toolcore.ToolRequestTypeCreate), "Watch_Resource", false},
		{"watch_cancel update still confirms", rt(toolcore.ToolRequestTypeUpdate), tools.ToolWatchCancel, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, writeConfirmationRequired(tc.requestType, tc.toolName))
		})
	}
}
