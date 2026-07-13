package core

import (
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// FilterAndInjectDefaultTools watch injection: tools go in only for WatchCapable
// agents; WatchEnabled=false strips them; skipInjection wins over capability.

// mockWatchCapableAgent is a plain NBAgent that additionally declares WatchCapable,
// standing in for an action agent (remediation, github, kubectl, ...).
type mockWatchCapableAgent struct{ name string }

func (a mockWatchCapableAgent) GetName() string          { return a.name }
func (a mockWatchCapableAgent) GetNameAliases() []string { return nil }
func (a mockWatchCapableAgent) GetDescription() string   { return "mock watch-capable agent" }
func (a mockWatchCapableAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (a mockWatchCapableAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a mockWatchCapableAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }
func (a mockWatchCapableAgent) IsWatchCapable() bool             { return true }

// mockWatchCapableOptOutAgent is watch-capable AND opts out of default injection —
// verifies skipInjection wins over the capability declaration.
type mockWatchCapableOptOutAgent struct{ name string }

func (a mockWatchCapableOptOutAgent) GetName() string          { return a.name }
func (a mockWatchCapableOptOutAgent) GetNameAliases() []string { return nil }
func (a mockWatchCapableOptOutAgent) GetDescription() string {
	return "mock watch-capable opt-out agent"
}
func (a mockWatchCapableOptOutAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (a mockWatchCapableOptOutAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a mockWatchCapableOptOutAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }
func (a mockWatchCapableOptOutAgent) IsWatchCapable() bool             { return true }
func (a mockWatchCapableOptOutAgent) OptOutDefaultTools() bool         { return true }

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

func TestFilterAndInjectDefaultTools_InjectsAllWatchToolsForWatchCapableAgent(t *testing.T) {
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	in := []toolcore.NBTool{mockTool{name: "aws_execute"}}
	result := FilterAndInjectDefaultTools("acct", mockWatchCapableAgent{name: "remediation"}, "", in, toolcore.AgentCapabilities{})

	for _, name := range []string{
		tools.ToolWatchResource,
		tools.ToolWatchStatus,
		tools.ToolWatchCancel,
		tools.ToolWatchList,
	} {
		assert.Truef(t, hasWatchTool(result, name),
			"watch tool %q must be injected for a watch-capable agent — this is the contract that lets the auth check accept it later", name)
	}
	// Agent's own tools must survive the injection unchanged.
	assert.True(t, hasWatchTool(result, "aws_execute"),
		"agent's pre-existing tools must remain alongside the injected watch tools")
}

func TestFilterAndInjectDefaultTools_SkipsWatchInjectionForReadOnlyAgents(t *testing.T) {
	// Core opt-in guarantee: an agent that does NOT declare WatchCapable (and nil)
	// gets no watch tools even with the flag on.
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	for _, agent := range []NBAgent{nil, mockPlainAgent{name: "logs"}} {
		in := []toolcore.NBTool{mockTool{name: "fetch_logs"}}
		result := FilterAndInjectDefaultTools("acct", agent, "", in, toolcore.AgentCapabilities{})

		for _, name := range []string{
			tools.ToolWatchResource,
			tools.ToolWatchStatus,
			tools.ToolWatchCancel,
			tools.ToolWatchList,
		} {
			assert.Falsef(t, hasWatchTool(result, name),
				"watch tool %q must NOT be injected for a read-only agent — it would only register spurious watches", name)
		}
		assert.True(t, hasWatchTool(result, "fetch_logs"),
			"the read-only agent's own tools must be untouched")
	}
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
	// skipInjection (DefaultToolsOptOut) wins even for a watch-capable agent whose
	// tool list is intentionally pinned (e.g. the delegate sub-agent).
	prev := config.Config.WatchEnabled
	config.Config.WatchEnabled = true
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	agent := mockWatchCapableOptOutAgent{name: "delegate"}
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
	result := FilterAndInjectDefaultTools("acct", mockWatchCapableAgent{name: "kubectl"}, "", in, toolcore.AgentCapabilities{})

	count := 0
	for _, t := range result {
		if t.Name() == tools.ToolWatchResource {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"watch tool present in agent's native list must not be re-appended by the injection — exactly one copy must survive")
}

// agentWatchCapable / asyncCompletionRules — the opt-in gate driving BOTH the
// tool injection and the async-completion prompt from one signal.

func TestAgentWatchCapable(t *testing.T) {
	assert.False(t, agentWatchCapable(nil), "nil agent must default to read-only (not watch-capable)")
	assert.False(t, agentWatchCapable(mockPlainAgent{name: "logs"}),
		"an agent that does not implement WatchCapable must default to read-only")
	assert.True(t, agentWatchCapable(mockWatchCapableAgent{name: "remediation"}),
		"an agent that declares WatchCapable=true must be watch-capable")
}

func TestAsyncCompletionRules_GatedByCapabilityAndFlag(t *testing.T) {
	prev := config.Config.WatchEnabled
	t.Cleanup(func() { config.Config.WatchEnabled = prev })

	config.Config.WatchEnabled = true
	assert.NotEmpty(t, asyncCompletionRules(mockWatchCapableAgent{name: "github"}),
		"watch-capable agent must get the async-completion prompt when the flag is on")
	assert.Empty(t, asyncCompletionRules(mockPlainAgent{name: "logs"}),
		"read-only agent must never get the async-completion prompt — this is what stopped the spurious registration")
	assert.Empty(t, asyncCompletionRules(nil), "nil agent must not get the async-completion prompt")

	config.Config.WatchEnabled = false
	assert.Empty(t, asyncCompletionRules(mockWatchCapableAgent{name: "github"}),
		"no async-completion prompt when the feature flag is off, even for a watch-capable agent")
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
