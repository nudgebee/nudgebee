package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// A LogQL provider's Code-mode editor runs only LogQL, so a filter
// services-server cannot preview as LogQL is an error there, not JSON in the
// editor; other providers still get the canonical JSON.
func TestPreviewOrCanonical(t *testing.T) {
	const jsonQuery = `{"where":{"_or":[{"namespace":{"_eq":"a"}},{"pod":{"_eq":"b"}}]}}`

	q, err := previewOrCanonical("otel_clickhouse", `{namespace="a"}`, jsonQuery)
	assert.NoError(t, err)
	assert.Equal(t, `{namespace="a"}`, q)

	_, err = previewOrCanonical("otel_clickhouse", "", jsonQuery)
	assert.ErrorContains(t, err, "cannot be written as LogQL")
	assert.ErrorContains(t, err, jsonQuery, "the error shows the filter that was generated")

	q, err = previewOrCanonical("pinot", "", jsonQuery)
	assert.NoError(t, err)
	assert.Equal(t, jsonQuery, q)
}

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
	assert.JSONEq(t, `{"query":"{app=\"checkout\"}","provider":"loki"}`, string(data), "index must be omitted for a backend with no index concept")
}

// TestLogQueryResult_MarshalWithIndex covers the ES shape: the caller needs the
// index back by name so the logs tab can show which index the query in the bar
// will run against.
func TestLogQueryResult_MarshalWithIndex(t *testing.T) {
	result := logQueryResult{
		Query:    `{"query":{"bool":{}}}`,
		Provider: "ES",
		Index:    "logs-kubernetes-*",
	}
	data, err := common.MarshalJson(result)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"query":"{\"query\":{\"bool\":{}}}","provider":"ES","index":"logs-kubernetes-*"}`, string(data))
}

// TestLogQueryResult_MarshalWithWhere: the where-clause is echoed in the same
// `_and`/`_binary` shape logs_get_query accepts as query_request.where, so the
// logs tab can re-render the text with a new Limit without regenerating it.
func TestLogQueryResult_MarshalWithWhere(t *testing.T) {
	where := toolcore.QueryWhereClause{And: []toolcore.QueryWhereClause{
		{Binary: toolcore.BinaryWhereClause{"namespace": {toolcore.Eq: "nudgebee"}}},
	}}
	result := logQueryResult{Query: `k8s.namespace.name:="nudgebee"`, Provider: "cubeapm", Where: &where}
	data, err := common.MarshalJson(result)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"query":"k8s.namespace.name:=\"nudgebee\"","provider":"cubeapm","where":{"_and":[{"_binary":{"namespace":{"_eq":"nudgebee"}}}]}}`, string(data))
}

// TestLogQueryAgent_EffectiveIndex covers the index precedence the ES "Select an
// Index" dropdown depends on. The caller's selection is a hard pin — a generated
// query must never be silently resolved against a different index than the one
// the dropdown shows — and a backend with no index concept must never inherit an
// index from the request.
func TestLogQueryAgent_EffectiveIndex(t *testing.T) {
	cases := []struct {
		name           string
		provider       services_server.ObservabilityProvider
		requestedIndex string
		generatedIndex string
		want           string
	}{
		{
			name:           "requested index wins over the generated one",
			provider:       services_server.ObservabilityProvider{Provider: "ES", DefaultIndex: "logs-default-*"},
			requestedIndex: "logs-nginx-*",
			generatedIndex: "logs-app-*",
			want:           "logs-nginx-*",
		},
		{
			name:           "generated index used when none was requested",
			provider:       services_server.ObservabilityProvider{Provider: "ES", DefaultIndex: "logs-default-*"},
			generatedIndex: "logs-app-*",
			want:           "logs-app-*",
		},
		{
			name:     "falls back to the account default when neither is set",
			provider: services_server.ObservabilityProvider{Provider: "elasticsearch", DefaultIndex: "logs-default-*"},
			want:     "logs-default-*",
		},
		{
			name:           "non-ES provider ignores a requested index",
			provider:       services_server.ObservabilityProvider{Provider: "loki"},
			requestedIndex: "logs-nginx-*",
			want:           "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &LogQueryAgent{accountId: "acct", provider: tc.provider, requestedIndex: tc.requestedIndex}
			assert.Equal(t, tc.want, a.effectiveIndex(tc.generatedIndex))
		})
	}
}

// TestLogQueryAgent_EffectiveLimit: the generated query carries the smaller of
// the logs tab's "Limit" dropdown and the generator's limit, so the generator's
// 1000/5000 default never shows a limit the run won't apply, while a count the
// user stated in the question survives a larger dropdown default.
func TestLogQueryAgent_EffectiveLimit(t *testing.T) {
	cases := []struct {
		name           string
		requestedLimit int
		generatedLimit int
		want           int
	}{
		{name: "dropdown caps the generator's routine default", requestedLimit: 50, generatedLimit: 1000, want: 50},
		{name: "dropdown caps an investigation default", requestedLimit: 200, generatedLimit: 5000, want: 200},
		{name: "count stated in the question survives a larger dropdown", requestedLimit: 100, generatedLimit: 20, want: 20},
		{name: "equal limits", requestedLimit: 100, generatedLimit: 100, want: 100},
		{name: "dropdown used when the generator gave none", requestedLimit: 50, generatedLimit: 0, want: 50},
		{name: "generated limit kept when no dropdown value was sent", generatedLimit: 1000, want: 1000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &LogQueryAgent{accountId: "acct", requestedLimit: tc.requestedLimit}
			assert.Equal(t, tc.want, a.effectiveLimit(tc.generatedLimit))
		})
	}
}
