package agents

import (
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

// TestTracesDefaultAgentV2_EmbedsV1Identity guards the embedding contract: v2 keeps the
// v1 ReAct planner + "Traces" alias (inherited from TracesDefaultAgent) while exposing
// its own name.
func TestTracesDefaultAgentV2_EmbedsV1Identity(t *testing.T) {
	a := newTracesDefaultAgentV2("acct", services_server.ObservabilityProvider{})
	assert.Equal(t, TracesDefaultV2AgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())
	assert.Equal(t, []string{"Traces"}, a.GetNameAliases())
}

// TestResolveTraceQueryOperators: backend ops win when present, default set used when
// empty, _or/_and always unioned, deduped with order preserved.
func TestResolveTraceQueryOperators(t *testing.T) {
	t.Run("empty falls back to default plus combinators", func(t *testing.T) {
		got := resolveTraceQueryOperators(nil)
		for _, op := range defaultTraceQueryOperators {
			assert.Contains(t, got, op)
		}
		assert.Contains(t, got, "_or")
		assert.Contains(t, got, "_and")
	})
	t.Run("backend operators used verbatim plus combinators", func(t *testing.T) {
		got := resolveTraceQueryOperators([]string{"_eq", "_neq", "_regex"})
		assert.Equal(t, []string{"_eq", "_neq", "_regex", "_or", "_and"}, got)
	})
	t.Run("dedupe keeps first occurrence", func(t *testing.T) {
		got := resolveTraceQueryOperators([]string{"_eq", "_eq", "_or"})
		assert.Equal(t, []string{"_eq", "_or", "_and"}, got)
	})
}

// TestBuildCanonicalTraceQueryPrompt_CapabilityDriven: a populated LabelMappings makes
// the prompt advertise canonical→backend fields; an empty map falls back to the shared
// generic schema (with the source/destination + duration rules). Both use the v2 tool.
func TestBuildCanonicalTraceQueryPrompt_CapabilityDriven(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("populated capabilities advertise canonical mapping", func(t *testing.T) {
		a := newTracesDefaultAgentV2("acct", services_server.ObservabilityProvider{
			Provider: "chronosphere",
			Capabilities: services_server.ProviderCapabilities{
				SupportedOperators: []string{"_eq", "_neq"},
				LabelMappings:      map[string]string{"http.status_code": "http.status_code", "k8s_cluster": "k8s_cluster"},
			},
		})
		p := a.GetSystemPrompt(ctx, core.NBAgentRequest{})
		joined := strings.Join(p.Instructions, "\n")
		assert.Contains(t, joined, "http.status_code → http.status_code")
		assert.Contains(t, joined, "k8s_cluster → k8s_cluster")
		assert.Contains(t, joined, "_eq, _neq, _or, _and")
		// Uses the v2 tool, not the v1 default tool.
		_, hasV2 := p.ToolUsage[tools.ToolTracesExecuteV2]
		assert.True(t, hasV2)
		assert.NotContains(t, joined, "traces_execute_default")
	})

	t.Run("empty capabilities fall back to generic schema", func(t *testing.T) {
		a := newTracesDefaultAgentV2("acct", services_server.ObservabilityProvider{Provider: "jaeger"})
		p := a.GetSystemPrompt(ctx, core.NBAgentRequest{})
		joined := strings.Join(p.Instructions, "\n")
		assert.Contains(t, joined, "duration_ns")
		assert.Contains(t, joined, "destination_workload_name")
		assert.Contains(t, joined, "Source vs Destination")
	})
}

// TestCanonicalTraceQueryExamples_Placeholders: few-shots are structure-only — they use
// <PLACEHOLDER> field tokens, route through the v2 tool, and never hardcode a specific
// provider-native field name.
func TestCanonicalTraceQueryExamples_Placeholders(t *testing.T) {
	examples := canonicalTraceQueryExamples()
	assert.NotEmpty(t, examples)
	for _, ex := range examples {
		for _, step := range ex.AnswerSteps {
			assert.Equal(t, tools.ToolTracesExecuteV2, step.Tool)
			assert.Contains(t, step.Input, "<", "example field names must be placeholders")
			// No anchoring on concrete provider field names.
			assert.NotContains(t, step.Input, "http.status_code")
			assert.NotContains(t, step.Input, "kube_namespace")
		}
	}
}

// TestFilterDiscoveredTraceAttributes covers the live-labels selection logic (the
// trace counterpart of the log agent's get_labels fields): only dotted attribute keys
// are surfaced (curated snake_case fields are already documented), anything already
// advertised via the mapping is dropped, and the sorted list is capped.
func TestFilterDiscoveredTraceAttributes(t *testing.T) {
	t.Run("keeps dotted keys, drops snake_case and advertised", func(t *testing.T) {
		labels := []string{"service_name", "status_code", "http.method", "db.system", "app.order.id"}
		advertised := map[string]string{"http.method": "spanattributes['http.method']"}
		got, capped := filterDiscoveredTraceAttributes(labels, advertised)
		assert.False(t, capped)
		// snake_case (service_name/status_code) excluded; http.method advertised → excluded.
		assert.Equal(t, []string{"app.order.id", "db.system"}, got)
	})

	t.Run("sorts and caps at the max", func(t *testing.T) {
		labels := make([]string, 0, maxDiscoveredTraceAttrs+10)
		for i := maxDiscoveredTraceAttrs + 9; i >= 0; i-- { // reverse order to prove sorting
			labels = append(labels, fmt.Sprintf("attr.%03d", i))
		}
		got, capped := filterDiscoveredTraceAttributes(labels, nil)
		assert.True(t, capped)
		assert.Len(t, got, maxDiscoveredTraceAttrs)
		assert.Equal(t, "attr.000", got[0]) // sorted ascending
	})

	t.Run("empty input", func(t *testing.T) {
		got, capped := filterDiscoveredTraceAttributes(nil, nil)
		assert.Empty(t, got)
		assert.False(t, capped)
	})
}
