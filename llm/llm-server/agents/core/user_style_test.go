package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderUserStyleBlock(t *testing.T) {
	assert.Equal(t, "", renderUserStyleBlock("  \n"), "no style profile → no block")

	out := renderUserStyleBlock("<user_style>\nverbosity: terse\n</user_style>")
	assert.True(t, strings.HasPrefix(out, "**ANSWER STYLE (user-declared):**"), "block opens with the instruction")
	assert.Contains(t, out, "overrides the default formatting and verbosity rules")
	assert.Contains(t, out, "</user_style>\nConcretely:\n- Keep the prose under about 120 words",
		"a known value becomes a concrete rule after the profile")

	plain := renderUserStyleBlock("<user_style>\n\nsome free text\n</user_style>")
	assert.NotContains(t, plain, "Concretely:", "no rules when no known value is set")
}

// The style block sits in the human message right before <question>, outside
// <user_memory>, and only for the top-level agent (sub-agents don't write the
// final answer).
func TestReAct4Human_UserStyleSitsBeforeQuestionOutsideMemory(t *testing.T) {
	tmpl := newReact4HumanPromptTemplate()
	format := func(topLevel bool) string {
		out, err := tmpl.Format(map[string]any{
			"today": "now", "kb_prestep_content": "", "rag_examples": "", "skill_lists_menu": "",
			"global_preferences_block": "", "user_context_block": "", "execution_targets_block": "",
			"is_top_level": topLevel, "orchestrator_mode": true, "conversation_context": "", "history": "",
			"evidence_index":        "",
			"memory_context_block":  "<user_memory>\nMEMORY-MARKER\n</user_memory>",
			"channel_context_block": "",
			"user_style_block":      renderUserStyleBlock("<user_style>\nverbosity: terse\n</user_style>"),
			"notebook":              "",
			"input":                 "why is checkout slow?",
		})
		assert.NoError(t, err)
		return out
	}

	top := format(true)
	style := strings.Index(top, "**ANSWER STYLE")
	assert.Greater(t, style, strings.Index(top, "</user_memory>"), "style comes after the memory block, not inside it")
	assert.Less(t, style, strings.Index(top, "<question>"), "style comes right before the question")

	assert.NotContains(t, format(false), "verbosity: terse")
}

func TestUserStyleRules(t *testing.T) {
	rules := userStyleRules("<user_style>\n  tone  :  direct  \nVerbosity: Terse\nexplain_reasoning: none\nformat_code_first: true\nunknown: thing\n</user_style>")
	assert.Len(t, rules, 4)
	assert.Contains(t, rules[0], "No preamble")
	assert.Contains(t, rules[1], "120 words")
	assert.Contains(t, rules[2], "result only")
	assert.Contains(t, rules[3], "command or code")
	assert.Empty(t, userStyleRules("verbosity: balanced"), "balanced is the default; no rule")
}
