package prompts

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAgentResponseSummaryPrompt_HasNoFormatVerbs locks the invariant that the
// fix in generateAsyncAgentSummary depends on: the summary template is a bare
// instruction consumed without Sprintf args. If a printf verb (%s, %d, ...) is
// ever added here, a caller passing args would silently dump the raw payload
// into the prompt as a "%!(EXTRA ...)" overflow — exactly the bug that inflated
// summary inputs past 200k tokens.
func TestAgentResponseSummaryPrompt_HasNoFormatVerbs(t *testing.T) {
	p := GetPromptForTest(PromptAgentResponseSummary)
	assert.NotEmpty(t, p)
	assert.NotContains(t, p, "%!", "GetPrompt must not have applied Sprintf args to the summary prompt")

	printfVerb := regexp.MustCompile(`%[+\-# 0-9.]*[sdvqxXeEfgGtbcoUp]`)
	assert.False(t, printfVerb.MatchString(GetPromptForTest(PromptAgentResponseSummary)),
		"summary template must stay free of printf verbs so callers cannot reintroduce the EXTRA-args overflow")
}
