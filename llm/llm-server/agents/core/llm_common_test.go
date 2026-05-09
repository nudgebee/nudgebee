package core

import (
	"context"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestHuggingFaceLLM(t *testing.T) {
	model, err := GetLlmModelWithProvider("huggingface", "", false, "", "")
	if err != nil {
		t.Skip("Skipping HuggingFace test: ", err)
	}
	assert.NotNil(t, model)

	respone, err := model.GenerateContent(context.Background(), []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "generate prometheus query for top 5 pods with max memory")})
	assert.Nil(t, err)
	assert.NotNil(t, respone)
	assert.True(t, len(respone.Choices) > 0)
	assert.True(t, len(respone.Choices[0].Content) > 0)
}

func TestSanatizeMarkdownCodeBlock(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard markdown code block with language",
			input:    "```go\npackage main\n\nfunc main() {}\n```",
			expected: "package main\n\nfunc main() {}",
		},
		{
			name:     "Markdown code block without language",
			input:    "```\nSome text\n```",
			expected: "Some text",
		},
		{
			name:     "String with only triple backticks",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: "{\"key\": \"value\"}",
		},
		{
			name:     "String with single backticks",
			input:    "`inline code`",
			expected: "inline code",
		},
		{
			name:     "String with leading/trailing whitespace and markdown",
			input:    "  ```python\nprint('hello')\n```  ",
			expected: "print('hello')",
		},
		{
			name:     "String with no markdown",
			input:    "Just a regular string.",
			expected: "Just a regular string.",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "String with mixed backticks",
			input:    "```text\nThis is `inline` within a block\n```",
			expected: "This is `inline` within a block",
		},
		{
			name:     "String starting with single backtick",
			input:    "`start`",
			expected: "start",
		},
		{
			name:     "String ending with single backtick",
			input:    "end`",
			expected: "end`",
		},
		{
			name:     "within markdown quotes",
			input:    "```markdown\nend\n```\n\n",
			expected: "end",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := SanatizeMarkdownCodeBlock(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
