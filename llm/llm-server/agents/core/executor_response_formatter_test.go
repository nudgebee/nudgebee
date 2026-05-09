package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSlackReqest(t *testing.T) {
	testCases := []struct {
		name      string
		sessionID string
		expected  bool
	}{
		{
			name:      "Valid Slack Channel ID with timestamp",
			sessionID: "C064YLRJAHG-1757766276.809109",
			expected:  true,
		},
		{
			name:      "Valid Slack Channel ID without timestamp",
			sessionID: "C064YLRJAHG",
			expected:  true,
		},
		{
			name:      "Valid Slack User ID",
			sessionID: "U024BE7LH",
			expected:  true,
		},
		{
			name:      "Valid ID with minimum length",
			sessionID: "W12345678",
			expected:  true,
		},
		{
			name:      "Empty Session ID",
			sessionID: "",
			expected:  false,
		},
		{
			name:      "Non-Slack ID",
			sessionID: "not-a-slack-id",
			expected:  false,
		},
		{
			name:      "ID with wrong prefix",
			sessionID: "X024BE7LH",
			expected:  false,
		},
		{
			name:      "ID too short",
			sessionID: "C1234567",
			expected:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := NBAgentRequest{SessionId: tc.sessionID}
			assert.Equal(t, tc.expected, isSlackRequest(request))
		})
	}
}

func TestConvertMarkdownToSlackMarkdown(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Headings",
			input:    "# Title\n## Subtitle",
			expected: "*Title*\n*Subtitle*",
		},
		{
			name:     "Links",
			input:    "Here is a [link](https://example.com) to a website.",
			expected: "Here is a <https://example.com|link> to a website.",
		},
		{
			name:     "Strikethrough",
			input:    "This is ~~wrong~~ right.",
			expected: "This is ~wrong~ right.",
		},
		{
			name:     "Bold",
			input:    "This is **bold** and so is __this__.",
			expected: "This is *bold* and so is *this*.",
		},
		{
			name:     "Italic",
			input:    "This is *italic* and so is _this_.",
			expected: "This is _italic_ and so is _this_.",
		},
		{
			name:     "Bold and Italic",
			input:    "***This is both.***",
			expected: "_*This is both.*_",
		},
		{
			name:     "Inline code",
			input:    "Use the `my_function()` function.",
			expected: "Use the `my_function()` function.",
		},
		{
			name:     "Fenced code block",
			input:    "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
			expected: "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
		},
		{
			name:     "Markdown inside code block",
			input:    "```\n# This is not a heading\n**This is not bold**\n```",
			expected: "```\n# This is not a heading\n**This is not bold**\n```",
		},
		{
			name:     "Complex mixed content",
			input:    "# Report\nHere is the data you requested:\n\n- **Metric A**: 123\n- *Metric B*: 456\n\nSee `generate_report()` for details. [Dashboard](https://dashboard.link).",
			expected: "*Report*\nHere is the data you requested:\n\n- *Metric A*: 123\n- _Metric B_: 456\n\nSee `generate_report()` for details. <https://dashboard.link|Dashboard>.",
		},
		{
			name:     "Simple Bold Asterisk",
			input:    "**bold**",
			expected: "*bold*",
		},
		{
			name:     "Markdown in inline code",
			input:    "this is `**not bold**`",
			expected: "this is `**not bold**`",
		},
		{
			name:     "Markdown in fenced code",
			input:    "```\n**not bold**\n```",
			expected: "```\n**not bold**\n```",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := convertMarkdownToSlackMarkdown(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
