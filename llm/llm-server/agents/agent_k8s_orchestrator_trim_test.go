package agents

import (
	"testing"

	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

func TestTrimmedK8sCoreToolNames_IncludesCoreExcludesSpecialists(t *testing.T) {
	names := trimmedK8sCoreToolNames()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}

	// Core investigation loop + reach-back must be preloaded.
	for _, want := range []string{
		tools.ToolExecuteKubectlCommand,
		LogsAgentName, EventsAgentName, MetricsAgentName, TracesAgentName,
		ResourceSearchAgentName, ServiceDependencyGraph, RecommendationsAgentName,
		DelegateAgentToolName, SearchToolsToolName,
	} {
		assert.True(t, set[want], "core tool %q must be in the trimmed set", want)
	}

	// Specialists must NOT be preloaded — they are reached on-demand.
	for _, notWant := range []string{
		"postgres", "mysql", "mssql", "oracle", "redis", "rabbitmq",
		"helm", "github", "security", "server", "code_analyzer",
		"aws", "aws_observability", "gcp", "azure",
		"visualizer", "websearch", "tickets", "tickets_v2", "automation",
	} {
		assert.False(t, set[notWant], "specialist %q must be excluded from the trimmed set", notWant)
	}
}

func TestTrimOnDemandInstruction_ReferencesSearchTools(t *testing.T) {
	// search_tools is always registered, so the instruction unconditionally routes
	// specialist discovery through it and overrides the heavy prompt's direct-call
	// guidance. (How to delegate is covered by the shared base prompt.)
	instr := trimOnDemandInstruction()
	assert.Contains(t, instr, "search_tools", "the trim instruction must route specialist discovery via search_tools")
	assert.Contains(t, instr, "OVERRIDES", "must override the heavy prompt's direct-call guidance")
}

// Guard: the trimmed set must never accidentally include the shell/other default
// tools as *specialist* entries beyond the intended conditional tail. This pins
// that search_tools is the only discovery entry and delegate_agent the only
// generic delegator preloaded.
func TestTrimmedK8sCoreToolNames_NoDuplicateDelegators(t *testing.T) {
	counts := map[string]int{}
	for _, n := range trimmedK8sCoreToolNames() {
		counts[n]++
	}
	assert.Equal(t, 1, counts[DelegateAgentToolName], "delegate_agent listed exactly once")
	assert.Equal(t, 1, counts[SearchToolsToolName], "search_tools listed exactly once")
	// sanity: kubectl leaf tool, not the kubectl agent, is the k8s entrypoint.
	assert.Equal(t, 1, counts[tools.ToolExecuteKubectlCommand])
	assert.Equal(t, 0, counts[KubectlAgentName], "trim runs kubectl directly, not via the kubectl sub-agent")
}
