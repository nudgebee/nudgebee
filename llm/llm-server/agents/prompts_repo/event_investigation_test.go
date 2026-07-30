package prompts_repo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// eventInvestigationArgCount is the number of positional %s placeholders in
// event_investigation.txt. generateEventAnalysisPrompt must pass exactly this
// many args, in template order: id, definition, title, description, labels,
// time, window start, window end, source, summary.
const eventInvestigationArgCount = 10

// TestEventInvestigationPrompt_RendersCleanly locks the positional-arg
// contract: GetPrompt uses fmt.Sprintf, so an arg-count mismatch silently
// injects %!s(MISSING) / %!(EXTRA ...) artifacts into every automated
// investigation prompt.
func TestEventInvestigationPrompt_RendersCleanly(t *testing.T) {
	assert.Equal(t, eventInvestigationArgCount, strings.Count(eventInvestigation, "%s"),
		"placeholder count changed — update generateEventAnalysisPrompt's GetPrompt args and this test together")

	args := make([]any, eventInvestigationArgCount)
	for i := range args {
		args[i] = "arg"
	}
	rendered := GetPrompt(PromptEventInvestigation, args...)
	assert.NotEmpty(t, rendered)
	assert.NotContains(t, rendered, "%!", "prompt must render without Sprintf artifacts")
	assert.Contains(t, rendered, "Incident Window", "prompt must surface the incident window")
	assert.Contains(t, rendered, "Evidence Discipline", "prompt must carry the evidence-discipline rules")
}
