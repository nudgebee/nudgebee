package core

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
)

func onDemandTestTools() []NBTool {
	return []NBTool{
		NewMCPToolForTest("qa", "Use for browser tests of the Nudgebee UI.", "app_map", "Map the app's pages."),
		NewMCPToolForTest("qa", "Use for browser tests of the Nudgebee UI.", "screenshot", "Take a screenshot."),
		NewMCPToolForTest("pprof-debug", "Profiles Go services.", "cpu", "CPU profile."),
		NewMCPToolForTest("deepwiki", "", "ask_question", "Ask about a repo."),
		NewMCPUnavailableToolForTest("test-mcp"),
	}
}

// The integrations line sits in the cached system prompt, so the same
// integrations must give the same bytes whatever order discovery returned
// them in (the map-order lesson of #39761).
func TestMCPIntegrationsPromptBlock_ByteStable(t *testing.T) {
	want := MCPIntegrationsPromptBlock(onDemandTestTools())
	assert.Equal(t, "MCP integrations connected to this account: `deepwiki`, `pprof-debug`, `qa` "+
		"(unreachable right now: `test-mcp` — tell the user these cannot be used instead of trying their tools). "+
		"Their tools are not in your tool list. Find them with search_tools, by integration name or by what you need, "+
		"and run them through delegate_agent.", want)

	r := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		tools := onDemandTestTools()
		r.Shuffle(len(tools), func(a, b int) { tools[a], tools[b] = tools[b], tools[a] })
		assert.Equal(t, want, MCPIntegrationsPromptBlock(tools))
	}
}

func TestMCPIntegrationsPromptBlock_NoneAndOnlyUnreachable(t *testing.T) {
	assert.Empty(t, MCPIntegrationsPromptBlock(nil))
	block := MCPIntegrationsPromptBlock([]NBTool{NewMCPUnavailableToolForTest("test-mcp")})
	assert.Contains(t, block, "connected to this account: none reachable (unreachable right now: `test-mcp`")
}

func TestMCPToolParts(t *testing.T) {
	tools := onDemandTestTools()
	integration, instructions, desc, ok := MCPToolParts(tools[0])
	assert.True(t, ok)
	assert.Equal(t, "qa", integration)
	assert.Equal(t, "Use for browser tests of the Nudgebee UI.", instructions)
	assert.Equal(t, "Map the app's pages.", desc)
	_, _, _, ok = MCPToolParts(tools[4])
	assert.False(t, ok, "an unreachable placeholder is not an MCP tool")
}
