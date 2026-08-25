package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
)

// TestLogQueryAgent_GetPlannerType covers the NBCustomAgent wiring: Execute is
// only invoked by the executor when GetPlannerType reports Custom.
func TestLogQueryAgent_GetPlannerType(t *testing.T) {
	a := &LogQueryAgent{accountId: "acct"}
	assert.Equal(t, core.AgentPlannerTypeCustom, a.GetPlannerType())
}

// TestLogQueryAgent_Execute_UnsupportedProviders covers the two providers
// Execute rejects outright before ever calling the canonical query-generation
// LLM step: empty/k8s-only (no query language to generate against) and
// datadog (stays on its own bespoke facet-syntax chain, not the canonical path).
func TestLogQueryAgent_Execute_UnsupportedProviders(t *testing.T) {
	cases := []struct {
		name     string
		provider services_server.ObservabilityProvider
	}{
		{"empty provider", services_server.ObservabilityProvider{}},
		{"datadog", services_server.ObservabilityProvider{Provider: "datadog"}},
		{"datadog mixed case", services_server.ObservabilityProvider{Provider: "DataDog"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &LogQueryAgent{accountId: "acct", provider: tc.provider}
			ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"acct"})

			resp, err := a.Execute(ctx, core.NBAgentRequest{AccountId: "acct", Query: "errors in checkout last 1h"})

			assert.NoError(t, err)
			assert.Equal(t, core.ConversationStatusFailed, resp.Status)
			assert.Equal(t, LogQueryAgentName, resp.AgentName)
			assert.Len(t, resp.Response, 1)
		})
	}
}

// TestLogQueryAgent_Execute_RequestedProviderNotConfigured covers the override
// path: when the user's "Log Provider:" dropdown selection didn't resolve to a
// configured backend (l.provider ends up empty even though a specific provider
// was requested), Execute must report that the REQUESTED provider isn't
// configured — not the generic "no log backend configured" message, and it
// must NOT silently fall back to generating against a different provider.
func TestLogQueryAgent_Execute_RequestedProviderNotConfigured(t *testing.T) {
	a := &LogQueryAgent{accountId: "acct", provider: services_server.ObservabilityProvider{}, requestedProvider: "loki"}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"acct"})

	resp, err := a.Execute(ctx, core.NBAgentRequest{AccountId: "acct", Query: "errors in checkout last 1h"})

	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusFailed, resp.Status)
	assert.Len(t, resp.Response, 1)
	assert.Contains(t, resp.Response[0], `"loki"`, "error should name the specific requested provider, not just say no provider is configured")
}

// TestLogQueryResult_Marshal locks in that the response envelope is a typed
// struct (not a map[string]string), so json.Marshal preserves field-declaration
// order regardless of key name — the old LokiAgent response relied on naming
// its field "logql_query" specifically so it would sort alphabetically before
// "logs" when marshaled from a map. Callers of this envelope read fields by
// name, not by key order, but this guards the contract's shape (query/provider
// only — no logs field; a preview-only query resolution fetches no log data).
func TestLogQueryResult_Marshal(t *testing.T) {
	result := logQueryResult{
		Query:    `{app="checkout"}`,
		Provider: "loki",
	}
	data, err := common.MarshalJson(result)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"query":"{app=\"checkout\"}","provider":"loki"}`, string(data))
}
