package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestAllRegisteredTools_RenderValidNativeToolSchema audits every tool in the
// built-in registry against the strictest provider schema converter (Google AI).
//
// This guard is the actual fix for the class of bug it was written for. Under
// ReAct3 a tool's InputSchema was rendered into the prompt as XML *text*, so a
// malformed schema was invisible — nothing validated it. Under provider-native
// tool calling the same schema becomes an API object the provider validates, and
// because a planner advertises an agent's ENTIRE toolset in one request, ONE bad
// tool returns 400 for every request that agent makes — not just calls to that
// tool. Two such bugs (a named `ToolSchemaType` failing a `.(string)` assertion,
// and an array property with no `items`) each took down 100% of requests and were
// only found by a live e2e run.
//
// Running the conversion offline turns "discover one live 400 at a time" into a
// millisecond build-time check over the whole registry.
//
// It lives in package `agents` because that is where every tool's init() has run
// (package tools is imported transitively); agents/core alone has an empty
// registry.
func TestAllRegisteredTools_RenderValidNativeToolSchema(t *testing.T) {
	names := toolcore.ListRegisteredSystemToolNames()
	assert.NotEmpty(t, names, "tool registry is empty — imports/init() did not run")

	var failures []string
	audited := 0
	for _, name := range names {
		tool, ok := toolcore.GetNBTool("", name)
		if !ok || tool == nil {
			// Some factories legitimately need account scope to construct; they
			// cannot be audited here and are not a schema defect.
			continue
		}
		audited++

		if err := core.ValidateNativeToolSchemas([]toolcore.NBTool{tool}); err != nil {
			failures = append(failures, name+": "+err.Error())
		}
	}

	// Guard against a vacuous pass: if tool construction started failing, every
	// tool would be skipped and this audit would silently assert nothing.
	t.Logf("audited %d of %d registered tools", audited, len(names))
	assert.Greater(t, audited, 50,
		"audited too few tools — construction is failing and the audit is not actually running")

	assert.Empty(t, failures,
		"these tools do not render a provider-valid native tool schema. Each one breaks "+
			"EVERY native tool-calling request for any agent that carries it. Common causes: "+
			"a property Type that is not a plain string, or an array property missing Items.")
}
