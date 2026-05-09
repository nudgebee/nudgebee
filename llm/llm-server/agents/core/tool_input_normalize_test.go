package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// schemaTool is a minimal NBTool stub that reports a caller-supplied
// InputSchema. Only Name() and InputSchema() are exercised by
// normalizeToolInputForTool / normalizeToolInputByName.
type schemaTool struct {
	MockContextCapturingTool
	schema toolcore.ToolSchema
}

func (s *schemaTool) InputSchema() toolcore.ToolSchema { return s.schema }

func newSchemaTool(name string, props map[string]toolcore.ToolSchemaProperty) *schemaTool {
	t := &schemaTool{schema: toolcore.ToolSchema{Properties: props}}
	t.NameVal = name
	return t
}

func TestNormalizeToolInputForTool(t *testing.T) {
	propsID := map[string]toolcore.ToolSchemaProperty{
		"id":     {Type: "string"},
		"status": {Type: "string"},
	}
	propsCmd := map[string]toolcore.ToolSchemaProperty{
		"command": {Type: "string"},
	}

	tests := []struct {
		name     string
		tool     toolcore.NBTool
		input    string
		expected string
	}{
		{
			name:     "empty input returned as-is",
			tool:     newSchemaTool("t", propsID),
			input:    "",
			expected: "",
		},
		{
			name:     "nil tool returned as-is",
			tool:     nil,
			input:    "id=abc",
			expected: "id=abc",
		},
		{
			name:     "already-JSON input returned unchanged",
			tool:     newSchemaTool("t", propsID),
			input:    `{"id":"abc"}`,
			expected: `{"id":"abc"}`,
		},
		{
			name:     "tool without schema properties returned unchanged",
			tool:     newSchemaTool("t", nil),
			input:    "id=abc",
			expected: "id=abc",
		},
		{
			name:     "XML tags converted to JSON",
			tool:     newSchemaTool("t", propsID),
			input:    "<id>abc</id><status>FAILED</status>",
			expected: `{"id":"abc","status":"FAILED"}`,
		},
		{
			// Pre-existing quirk: the normalizer runs through separators ","
			// and " " in sequence without breaking when the first succeeds, so
			// comma-separated input gets re-split on spaces and "id" is
			// overwritten with the full tail. Documented here to lock current
			// behavior; a fix belongs in a later validation/cleanup PR.
			name:     "key=value comma-separated (documents pre-existing overwrite quirk)",
			tool:     newSchemaTool("t", propsID),
			input:    "id=abc,status=FAILED",
			expected: `{"id":"abc,status=FAILED","status":"FAILED"}`,
		},
		{
			name:     "key=value space-separated converted to JSON",
			tool:     newSchemaTool("t", propsID),
			input:    "id=abc status=FAILED",
			expected: `{"id":"abc","status":"FAILED"}`,
		},
		{
			name:     "plain text wrapped under command schema property",
			tool:     newSchemaTool("t", propsCmd),
			input:    "kubectl get pods -n default",
			expected: `{"command":"kubectl get pods -n default"}`,
		},
		{
			name:     "plain text with no matching property and no command field returned unchanged",
			tool:     newSchemaTool("t", propsID),
			input:    "just some plain text",
			expected: "just some plain text",
		},
		{
			name:     "unknown keys in key=value ignored",
			tool:     newSchemaTool("t", propsID),
			input:    "unknown=foo",
			expected: "unknown=foo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeToolInputForTool(tc.tool, tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestNormalizeToolInputByName(t *testing.T) {
	a := newSchemaTool("alpha", map[string]toolcore.ToolSchemaProperty{"id": {Type: "string"}})
	b := newSchemaTool("beta", map[string]toolcore.ToolSchemaProperty{"command": {Type: "string"}})
	tools := []toolcore.NBTool{a, b}

	t.Run("dispatches to matching tool", func(t *testing.T) {
		got := normalizeToolInputByName(tools, "alpha", "<id>xyz</id>")
		assert.Equal(t, `{"id":"xyz"}`, got)
	})

	t.Run("unknown tool name returns input unchanged", func(t *testing.T) {
		got := normalizeToolInputByName(tools, "does-not-exist", "id=xyz")
		assert.Equal(t, "id=xyz", got)
	})

	t.Run("empty input short-circuits before lookup", func(t *testing.T) {
		got := normalizeToolInputByName(tools, "alpha", "")
		assert.Equal(t, "", got)
	})

	t.Run("command-wrap path via lookup", func(t *testing.T) {
		got := normalizeToolInputByName(tools, "beta", "ls -la")
		assert.Equal(t, `{"command":"ls -la"}`, got)
	})
}
