package prompts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMemoryConsumptionRules_DoNotNameTheStyleBlock pins that the memory rules
// stay about preferences. They used to tell the model to honour a <soul> block
// that nothing rendered (#41152); the style profile is now its own instruction
// block in the human message (renderUserStyleBlock), not a memory rule.
func TestMemoryConsumptionRules_DoNotNameTheStyleBlock(t *testing.T) {
	rules := GetPromptForTest(PromptMemoryConsumptionRules)
	assert.NotContains(t, rules, "<soul>",
		"memory_consumption_rules names a <soul> block that nothing renders")
	assert.NotContains(t, rules, "user_style",
		"style is an answer instruction with its own block, not a memory rule")
	assert.Contains(t, rules, "`<user_preferences>`")
}
