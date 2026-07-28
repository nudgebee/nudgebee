package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFormatRAGDocument_QuestionValueTypes guards the RAG few-shot formatter
// against malformed documents. The mysql (and other DB) agents declare a RAG
// config with QuestionKey "Question"; a retrieved document whose question field
// is null or a non-string used to panic on an unguarded `val.(string)`
// ("interface conversion: interface {} is nil, not string"), aborting the whole
// agent run during system-prompt assembly. The question branch must tolerate
// non-string values the same way the answer/explanation branches do.
func TestFormatRAGDocument_QuestionValueTypes(t *testing.T) {
	ragConfig := NBAgentPromptRag{
		Format:         NBAgentPromptRagFormatJson,
		QuestionKey:    "Question",
		AnswerKey:      "Answer",
		ExplanationKey: "Solution Hint",
	}

	cases := []struct {
		name         string
		rawDocument  string
		wantContains []string
	}{
		{
			name:         "string question",
			rawDocument:  `{"Question": "Show long running queries", "Answer": "SELECT ...", "Solution Hint": "look at processlist"}`,
			wantContains: []string{"Question: Show long running queries", "Answer: SELECT ...", "Explanation: look at processlist"},
		},
		{
			name:         "null question does not panic",
			rawDocument:  `{"Question": null, "Answer": "SELECT ..."}`,
			wantContains: []string{"Answer: SELECT ..."},
		},
		{
			name:         "non-string question is serialized",
			rawDocument:  `{"Question": {"nested": true}, "Answer": "SELECT ..."}`,
			wantContains: []string{"Question: {\"nested\":true}", "Answer: SELECT ..."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out string
			assert.NotPanics(t, func() {
				out = formatRAGDocument(ragConfig, tc.rawDocument)
			})
			for _, want := range tc.wantContains {
				assert.True(t, strings.Contains(out, want), "expected output %q to contain %q", out, want)
			}
		})
	}
}
