package agents

import (
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFetchLogsDirectRequest_PropagatesKnowledgeForLeafPlanning(t *testing.T) {
	ctx := toolcore.NbToolContext{
		AccountId:             "account",
		OriginalQuery:         "show payment errors",
		OriginalQueryFromUser: true,
		KBPrestepContent:      `<knowledge id="payments-runbook">{namespace="payments", app="payment-service"} |= "error"</knowledge>`,
	}
	request := buildFetchLogsDirectRequest(ctx, toolcore.NBToolCallRequest{Command: "show payment errors"})
	require.Equal(t, ctx.KBPrestepContent, request.KBPrestepContent)
	require.True(t, request.OriginalQueryFromUser, "whether the original request is the user's own travels with it")

	forwarded := buildFetchLogsDirectRequest(toolcore.NbToolContext{}, toolcore.NBToolCallRequest{
		Command:   "show payment errors",
		Arguments: map[string]any{"knowledge_context": "namespace=payments; use the provider query template"},
	})
	require.Equal(t, "namespace=payments; use the provider query template", forwarded.KBPrestepContent)
}

func TestDecodeLogQueryPlan_AcceptsMarkdownFences(t *testing.T) {
	var plan logQueryPlan
	err := decodeLogQueryPlan("Result:\n```json\n{\"query_type\":\"canonical\",\"canonical_query\":{\"where\":{\"app\":{\"_eq\":\"checkout\"}},\"time_range\":\"1h\",\"limit\":1000}}\n```", &plan)
	require.NoError(t, err)
	assert.Equal(t, "canonical", plan.QueryType)
	assert.Equal(t, "1h", plan.CanonicalQuery["time_range"])
}

func TestValidateLogQueryPlan(t *testing.T) {
	provider := services_server.ObservabilityProvider{Provider: "loki"}
	request := core.NBAgentRequest{
		Query:                 "show checkout errors last 30m limit 100",
		OriginalQuery:         "show checkout errors last 30m limit 100",
		OriginalQueryFromUser: true,
	}

	t.Run("valid native defaults execution envelope", func(t *testing.T) {
		plan, err := validateLogQueryPlan(request, provider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `{namespace="payments"} |= "error"`},
		})
		require.NoError(t, err)
		assert.Equal(t, "30m", plan.Native.TimeRange)
		assert.Equal(t, 100, plan.Native.Limit)
	})

	t.Run("original explicit constraints override a rewritten command", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "show checkout errors last 2h limit 500",
			OriginalQuery:         "show checkout errors last 30m limit 100",
			OriginalQueryFromUser: true,
		}, provider, logQueryPlan{
			QueryType: "native",
			Native: nativeLogQueryPlan{
				Query: `{app="checkout"} |= "error"`, StartTime: "2026-09-24T10:00:00Z", Limit: 500,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "30m", plan.Native.TimeRange)
		assert.Empty(t, plan.Native.StartTime)
		assert.Equal(t, 100, plan.Native.Limit)
	})

	t.Run("unanchored relative window discards a stale planner end", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "show checkout errors last 30m",
			OriginalQuery:         "show checkout errors last 30m",
			OriginalQueryFromUser: true,
			KBPrestepContent:      "An old incident ended at 2026-09-20T10:00:00Z",
		}, provider, logQueryPlan{
			QueryType: "native",
			Native: nativeLogQueryPlan{
				Query: `{app="checkout"} |= "error"`, TimeRange: "30m", EndTime: "2026-09-20T10:00:00Z",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "30m", plan.Native.TimeRange)
		assert.Empty(t, plan.Native.EndTime)
	})

	t.Run("explicit historical end is retained", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "show checkout errors last 30m ending at 2026-09-20T10:00:00Z",
			OriginalQuery:         "show checkout errors last 30m ending at 2026-09-20T10:00:00Z",
			OriginalQueryFromUser: true,
		}, provider, logQueryPlan{
			QueryType: "native",
			Native: nativeLogQueryPlan{
				Query: `{app="checkout"} |= "error"`, TimeRange: "30m", EndTime: "2026-09-20T10:00:00Z",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "2026-09-20T10:00:00Z", plan.Native.EndTime)
	})

	t.Run("a historical end the fetch command states is kept", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "show checkout errors last 30m ending at 2026-09-20T10:00:00Z",
			OriginalQuery:         "show checkout errors last 30m",
			OriginalQueryFromUser: true,
		}, provider, logQueryPlan{
			QueryType: "native",
			Native: nativeLogQueryPlan{
				Query: `{app="checkout"} |= "error"`, EndTime: "2026-09-20T10:00:00Z",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "2026-09-20T09:30:00Z", plan.Native.StartTime)
		assert.Equal(t, "2026-09-20T10:00:00Z", plan.Native.EndTime)
		assert.Empty(t, plan.Native.TimeRange)
	})

	t.Run("a historical end only the plan came up with is dropped", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "show checkout errors",
			OriginalQuery:         "show checkout errors last 30m",
			OriginalQueryFromUser: true,
		}, provider, logQueryPlan{
			QueryType: "native",
			Native: nativeLogQueryPlan{
				Query: `{app="checkout"} |= "error"`, TimeRange: "30m", EndTime: "2026-09-20T10:00:00Z",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "30m", plan.Native.TimeRange)
		assert.Empty(t, plan.Native.EndTime)
	})

	t.Run("command constraints fill gaps in original query", func(t *testing.T) {
		plan, err := validateLogQueryPlan(core.NBAgentRequest{
			Query:                 "run the query last 15m limit 25",
			OriginalQuery:         "show checkout errors",
			OriginalQueryFromUser: true,
		}, provider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `{app="checkout"} |= "error"`},
		})
		require.NoError(t, err)
		assert.Equal(t, "15m", plan.Native.TimeRange)
		assert.Equal(t, 25, plan.Native.Limit)
	})

	t.Run("native placeholder is rejected", func(t *testing.T) {
		_, err := validateLogQueryPlan(request, provider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `{namespace="$namespace"} |= "error"`},
		})
		require.ErrorContains(t, err, "empty or incomplete")
	})

	t.Run("provider syntax is left to services-server", func(t *testing.T) {
		plan, err := validateLogQueryPlan(request, provider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `{"query":{"match":{"message":"error"}}}`},
		})
		require.NoError(t, err)
		assert.Equal(t, `{"query":{"match":{"message":"error"}}}`, plan.Native.Query)
	})

	t.Run("unknown provider does not require a local dialect detector", func(t *testing.T) {
		futureProvider := services_server.ObservabilityProvider{Provider: "futurelogs"}
		plan, err := validateLogQueryPlan(request, futureProvider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `stream(service="checkout") | contains("error")`},
		})
		require.NoError(t, err)
		assert.Equal(t, `stream(service="checkout") | contains("error")`, plan.Native.Query)
	})

	t.Run("canonical query cannot be mislabeled native", func(t *testing.T) {
		_, err := validateLogQueryPlan(request, provider, logQueryPlan{
			QueryType: "native",
			Native:    nativeLogQueryPlan{Query: `{"where":{"message":{"_ilike":"%error%"}}}`},
		})
		require.ErrorContains(t, err, "canonical query")
	})

	t.Run("canonical must contain where", func(t *testing.T) {
		_, err := validateLogQueryPlan(request, provider, logQueryPlan{
			QueryType:      "canonical",
			CanonicalQuery: map[string]any{"time_range": "30m", "limit": 100},
		})
		require.ErrorContains(t, err, "invalid canonical query")
	})

	t.Run("unsupported query type is rejected", func(t *testing.T) {
		_, err := validateLogQueryPlan(request, provider, logQueryPlan{QueryType: "sql"})
		require.ErrorContains(t, err, "unsupported query_type")
	})
}

func TestNativeQueryExecutionArgs(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	provider := services_server.ObservabilityProvider{DefaultIndex: "logs-default-*"}
	start, end, limit, index, err := nativeQueryExecutionArgs(nativeLogQueryPlan{
		TimeRange: "30m", Limit: 100, Index: "payments-*",
	}, provider, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-30*time.Minute), start)
	assert.Equal(t, now, end)
	assert.Equal(t, 100, limit)
	assert.Equal(t, "payments-*", index)

	t.Run("relative window is anchored to historical end", func(t *testing.T) {
		start, end, _, _, err := nativeQueryExecutionArgs(nativeLogQueryPlan{
			TimeRange: "30m", EndTime: "2026-09-20T10:00:00Z",
		}, provider, now)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC), start)
		assert.Equal(t, time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC), end)
	})

	t.Run("absolute bounds preserve fractional seconds", func(t *testing.T) {
		start, end, _, _, err := nativeQueryExecutionArgs(nativeLogQueryPlan{
			StartTime: "2026-09-20T09:00:00.123456789Z",
			EndTime:   "2026-09-20T10:00:00.987654321Z",
		}, provider, now)
		require.NoError(t, err)
		assert.Equal(t, "2026-09-20T09:00:00.123456789Z", start.Format(time.RFC3339Nano))
		assert.Equal(t, "2026-09-20T10:00:00.987654321Z", end.Format(time.RFC3339Nano))
	})

	for _, tc := range []struct {
		name string
		plan nativeLogQueryPlan
	}{
		{"invalid start", nativeLogQueryPlan{StartTime: "yesterday"}},
		{"invalid end", nativeLogQueryPlan{EndTime: "yesterday", TimeRange: "1h"}},
		{"invalid window", nativeLogQueryPlan{TimeRange: "soon"}},
		{"conflicting bounds", nativeLogQueryPlan{StartTime: "2026-09-20T09:00:00Z", TimeRange: "1h"}},
		{"reversed bounds", nativeLogQueryPlan{StartTime: "2026-09-20T11:00:00Z", EndTime: "2026-09-20T10:00:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, _, err := nativeQueryExecutionArgs(tc.plan, provider, now)
			require.Error(t, err)
		})
	}
}
