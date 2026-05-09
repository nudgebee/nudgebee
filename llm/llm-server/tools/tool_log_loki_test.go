package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLokiExecuteTool_CleanupQuery(t *testing.T) {
	tool := LokiExecuteTool{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain query unchanged",
			input:    `{app="my-app"}`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip single backticks",
			input:    "`{app=\"my-app\"}`",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip markdown code block with language tag",
			input:    "```logql\n{app=\"my-app\"}\n```",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip markdown code block without language tag",
			input:    "```\n{app=\"my-app\"}\n```",
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip double quote wrapping",
			input:    `"{app=\"my-app\"}"`,
			expected: `{app=\"my-app\"}`,
		},
		{
			name:     "strip trailing pipe",
			input:    `{app="my-app"} |`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip trailing pipe tilde",
			input:    `{app="my-app"} |~`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip trailing pipe equals",
			input:    `{app="my-app"} |=`,
			expected: `{app="my-app"}`,
		},
		{
			name:     "strip leading and trailing whitespace",
			input:    `   {app="my-app"}   `,
			expected: `{app="my-app"}`,
		},
		{
			name:     "preserve valid line filter",
			input:    `{app="my-app"} |~ "(?i)error"`,
			expected: `{app="my-app"} |~ "(?i)error"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.cleanupQuery(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLokiExecuteTool_FindInvalidLabels(t *testing.T) {
	tool := LokiExecuteTool{}

	tests := []struct {
		name        string
		query       string
		validLabels []string
		expected    []string
	}{
		{
			name:        "all labels valid",
			query:       `{app="my-app", namespace="prod"}`,
			validLabels: []string{"app", "namespace", "pod", "container"},
			expected:    nil,
		},
		{
			name:        "one invalid label",
			query:       `{service="my-app", namespace="prod"}`,
			validLabels: []string{"app", "namespace", "pod", "container"},
			expected:    []string{"service"},
		},
		{
			name:        "multiple invalid labels",
			query:       `{service="my-app", deployment="web"}`,
			validLabels: []string{"app", "namespace", "pod"},
			expected:    []string{"service", "deployment"},
		},
		{
			name:        "regex match operator",
			query:       `{app=~"my-app.*", namespace="prod"}`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "negation operator",
			query:       `{app!="my-app"}`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "no stream selector",
			query:       `|= "error"`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
		{
			name:        "empty valid labels treats all as invalid",
			query:       `{app="my-app"}`,
			validLabels: []string{},
			expected:    []string{"app"},
		},
		{
			name:        "query with line filter after selector",
			query:       `{app="my-app"} |~ "(?i)error"`,
			validLabels: []string{"app", "namespace"},
			expected:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.findInvalidLabels(tt.query, tt.validLabels)
			assert.Equal(t, tt.expected, result)
		})
	}
}
