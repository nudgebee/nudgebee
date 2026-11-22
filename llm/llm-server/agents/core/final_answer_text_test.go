package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitFinalAnswer(t *testing.T) {
	summary := "### 📝 Event Summary\n- **Title:** Pod api-1 OOMKilled\n\n### 🔍 Root Cause\nMemory limit 128Mi is below the 300MB cache."
	tests := []struct {
		name, in, answer, notes string
	}{
		{
			name:   "plain answer passes through",
			in:     "The deployment has 3 ready replicas.",
			answer: "The deployment has 3 ready replicas.",
		},
		{
			name:   "leading thought block without a wrapper",
			in:     "<thought> The investigation is complete. We identified the pod. </thought>\n\n" + summary,
			answer: summary,
			notes:  "The investigation is complete. We identified the pod.",
		},
		{
			name:   "closed final_answer wrapper",
			in:     "<final_answer><thought>done</thought><content>" + summary + "</content></final_answer>",
			answer: summary,
			notes:  "done",
		},
		{
			name:   "wrapper left unclosed",
			in:     "<final_answer>\n<thought>The review notes the answer was truncated.</thought>\n<content>\n" + summary,
			answer: summary,
			notes:  "The review notes the answer was truncated.",
		},
		{
			name:   "stray closing tag before the thought block",
			in:     "<final_answer>see </thought> below<thought>real notes</thought><content>" + summary,
			answer: summary,
			notes:  "real notes",
		},
		{
			name:   "wrapper without a content tag",
			in:     "<final_answer><thought>notes</thought>" + summary + "</final_answer>",
			answer: summary,
			notes:  "notes",
		},
		{
			name:   "plain notes before the event summary",
			in:     "Look at build-images first. That discriminates it definitively!\n\n" + summary,
			answer: summary,
			notes:  "Look at build-images first. That discriminates it definitively!",
		},
		{
			name:   "answer that starts with the event summary is unchanged",
			in:     summary,
			answer: summary,
		},
		{
			name:   "thought tag inside the answer body is kept",
			in:     "Use the <thought> tag in prompts to separate reasoning.",
			answer: "Use the <thought> tag in prompts to separate reasoning.",
		},
		{
			name:   "heading quoted mid-sentence is kept",
			in:     "Alert emails start with `### 📝 Event Summary` and then list causes.",
			answer: "Alert emails start with `### 📝 Event Summary` and then list causes.",
		},
		{
			name:   "a real section before the heading is kept",
			in:     "## Overview\nfoo\n" + summary,
			answer: "## Overview\nfoo\n" + summary,
		},
		{
			name:   "thought-only output keeps the text",
			in:     "<thought>only notes</thought>",
			answer: "<thought>only notes</thought>",
		},
		{
			name:   "unclosed leading thought is left alone",
			in:     "<thought> still working",
			answer: "<thought> still working",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer, notes := splitFinalAnswer(tt.in)
			assert.Equal(t, tt.answer, answer)
			assert.Equal(t, tt.notes, notes)
		})
	}
}
