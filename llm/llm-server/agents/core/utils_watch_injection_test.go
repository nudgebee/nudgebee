package core

import (
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// FilterAndInjectDefaultTools — watch tool injection
//
// Watch tools (watch_resource / watch_status / watch_cancel / watch_list) are
// injected globally by FilterAndInjectDefaultTools when LlmServerWatchEnabled
// (aka config.Config.WatchEnabled) is true, mirroring the shell_execute path.
// Before this refactor each agent that wanted watch had to wire it in itself,
// which produced two real bugs:
//   1. Agents that lacked the wiring (aws_debug, gcp_debug, helm, etc.) silently
//      had no watch capability even though watches are a generic concern.
//   2. The auth check accepted only what agent.GetSupportedTools() returned, so
//      a globally-injected watch tool reached the LLM but got rejected at
//      dispatch time with "auth: tool not found".
//
// These tests lock in the new behaviour:
//   - When WatchEnabled=true, every agent's tool list gets the four watch tools
//     appended (unless the agent opts out).
//   - When WatchEnabled=false, the tools are stripped even if an agent
//     hand-wired them — guarantees a single source of truth.
//   - DefaultToolsOptOut skips watch the same way it skips shell.
// =============================================================================

// registerMockWatchTools installs no-op factories for the four watch tools so
// FilterAndInjectDefaultTools can resolve them via toolcore.GetNBTool. The
// real factories (tools/tool_watch.go) require Manager state that isn't worth
// standing up in a unit test.
func registerMockWatchTools() {
	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		n := name
		toolcore.RegisterNBToolFactory(n, func(_ string) (toolcore.NBTool, error) {
			return mockTool{name: n}, nil
		})
	}
}

func init() {
	registerMockWatchTools()
}

// hasWatchTool is a test-local helper — we don't want to export a public
// has-this-watch-tool predicate just for tests.
func hasWatchTool(list []toolcore.NBTool, name string) bool {
	for _, t := range list {
		if t.Name() == name {
			return true
		}
	}
	return false
}

func TestFilterAndInjectDefaultTools_InjectsAllWatchToolsWhenEnabled(t *testing.T) {
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	in := []toolcore.NBTool{mockTool{name: "aws_execute"}}
	result := FilterAndInjectDefaultTools("acct", nil, "", in, toolcore.AgentCapabilities{})

	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		assert.Truef(t, hasWatchTool(result, name),
			"watch tool %q must be injected when WatchEnabled=true — this is the contract that lets the auth check accept it later", name)
	}
	// Agent's own tools must survive the injection unchanged.
	assert.True(t, hasWatchTool(result, "aws_execute"),
		"agent's pre-existing tools must remain alongside the injected watch tools")
}

func TestFilterAndInjectDefaultTools_StripsWatchToolsWhenDisabled(t *testing.T) {
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = false
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	// Hand-wire all four watch tools as if an agent had included them. The
	// disable path must strip them regardless — the global flag is the only
	// switch that matters.
	in := []toolcore.NBTool{
		mockTool{name: tools.ToolWatchResource},
		mockTool{name: tools.ToolWatchStatus},
		mockTool{name: tools.ToolWatchCancel},
		mockTool{name: tools.ToolWatchList},
		mockTool{name: "aws_execute"},
	}
	result := FilterAndInjectDefaultTools("acct", nil, "", in, toolcore.AgentCapabilities{})

	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		assert.Falsef(t, hasWatchTool(result, name),
			"watch tool %q must be stripped when WatchEnabled=false — defends against an agent accidentally re-introducing it via its native tool list", name)
	}
	assert.True(t, hasWatchTool(result, "aws_execute"),
		"non-watch tools must not be touched by the strip path")
}

func TestFilterAndInjectDefaultTools_SkipsWatchInjectionForOptOutAgents(t *testing.T) {
	// Same opt-out semantics as shell_execute. The delegate sub-agent
	// (and any future agent whose tool list is intentionally pinned by the
	// caller) must not get watch tools auto-appended.
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	agent := mockOptOutAgent{name: "delegate"}
	in := []toolcore.NBTool{mockTool{name: "aws_execute"}}
	result := FilterAndInjectDefaultTools("acct", agent, "", in, toolcore.AgentCapabilities{})

	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		assert.Falsef(t, hasWatchTool(result, name),
			"watch tool %q must NOT be injected for opt-out agents — same contract as shell_execute", name)
	}
}

func TestFilterAndInjectDefaultTools_DoesNotDuplicateWatchToolsWhenAgentAlreadyHasThem(t *testing.T) {
	// Defends the `already` check inside the injection block. Without it,
	// an agent whose native list happens to include a watch tool (legacy
	// wiring during the refactor) would end up with that tool listed
	// twice — and any downstream code that takes the first match would
	// pick the wrong instance.
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	in := []toolcore.NBTool{
		mockTool{name: tools.ToolWatchResource},
	}
	result := FilterAndInjectDefaultTools("acct", nil, "", in, toolcore.AgentCapabilities{})

	count := 0
	for _, t := range result {
		if t.Name() == tools.ToolWatchResource {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"watch tool present in agent's native list must not be re-appended by the injection — exactly one copy must survive")
}

// =============================================================================
// isWatchToolName — direct tests of the membership helper used by both
// the injection block AND the auth_agent whitelist. A regression here
// (e.g., someone renames a watch tool but forgets to update the slice)
// quietly breaks both consumers, so guard it explicitly.
// =============================================================================

func TestIsWatchToolName_AllFourWatchToolsRecognised(t *testing.T) {
	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		assert.Truef(t, isWatchToolNameViaWatchPackage(name),
			"%q must be recognised as a watch tool — the auth check and the strip path both depend on this", name)
	}
}

func TestIsWatchToolName_NonWatchToolsExcluded(t *testing.T) {
	for _, name := range []string{
		"shell_execute",
		"load_skills",
		"kubectl_execute",
		"watch_something_else", // intentionally close-but-not-equal
		"",
	} {
		assert.Falsef(t, isWatchToolNameViaWatchPackage(name),
			"%q must NOT be recognised as a watch tool — the auth whitelist would otherwise leak permissions to unrelated tools", name)
	}
}

// isWatchToolNameViaWatchPackage exists because isWatchToolName is package-
// private in agents/core (declared in utils.go). The test is in the same
// package so we can just call it; this helper keeps the call-site readable.
func isWatchToolNameViaWatchPackage(name string) bool {
	return isWatchToolName(name)
}
