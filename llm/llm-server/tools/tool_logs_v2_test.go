package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoLogsFoundMessage(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	where := core.QueryWhereClause{Binary: core.BinaryWhereClause{"namespace": {core.Eq: "prodd"}}}

	t.Run("suggestion present is returned verbatim", func(t *testing.T) {
		suggestion := `no logs matched: value "prodd" for label "namespace" not found; closest valid value(s): [prod]`
		got := noLogsFoundMessage(suggestion, "loki", where, start, end, 100)
		assert.Equal(t, suggestion, got)
	})

	t.Run("empty suggestion falls back to generic guidance", func(t *testing.T) {
		got := noLogsFoundMessage("", "loki", where, start, end, 100)
		assert.Contains(t, got, NoLogsFoundPrefix)
		assert.Contains(t, got, "loki")
		assert.Contains(t, got, "broader filters")
	})
}

// startLogsStub spins up an httptest server standing in for api-server's
// /rpc/logs endpoint and points config.Config.ServiceEndpoint at it.
func startLogsStub(t *testing.T, status int, body string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rpc/logs", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

// TestNBLogToolV2_Call_EmptyResult exercises the full Call() path against a fake
// services-server response, confirming the ValidateRequest empty-result diagnosis
// (a 200 carrying logs:[] + suggestion) turns into a Success tool response whose
// Data is the actionable suggestion, not the generic "no results" message.
func TestNBLogToolV2_Call_EmptyResult(t *testing.T) {
	logProvider := services_server.ObservabilityProvider{Provider: "loki"}
	command := `{"where":{"namespace":{"_eq":"prodd"}}}`

	t.Run("prefers services-server suggestion over the generic message", func(t *testing.T) {
		cleanup := startLogsStub(t, http.StatusOK,
			`{"logs":[],"query":"{namespace=~\".+\"}","provider":"loki","suggestion":"no logs matched: value \"prodd\" for label \"namespace\" not found; closest valid value(s): [prod]"}`)
		defer cleanup()

		tool := &NBLogToolV2{accountId: "acc-1", logProvider: logProvider}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: command})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, "closest valid value(s): [prod]")
		assert.NotContains(t, resp.Data, NoLogsFoundPrefix)

		// The real backend query/provider services-server executed must still be
		// extractable from Data even on a zero-row result — this is what
		// FetchLogsAgentV2's executedLogQuery/providerFromLogs read to show the
		// real query instead of falling back to the LLM's canonical where-clause.
		var doc struct {
			Metadata struct {
				Query    string `json:"query"`
				Provider string `json:"provider"`
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Data), &doc))
		assert.Equal(t, `{namespace=~".+"}`, doc.Metadata.Query)
		assert.Equal(t, "loki", doc.Metadata.Provider)
	})

	t.Run("falls back to the generic message when services-server has no suggestion", func(t *testing.T) {
		cleanup := startLogsStub(t, http.StatusOK, `{"logs":[],"query":"{namespace=~\".+\"}","provider":"loki"}`)
		defer cleanup()

		tool := &NBLogToolV2{accountId: "acc-1", logProvider: logProvider}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: command})
		require.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, NoLogsFoundPrefix)

		var doc struct {
			Metadata struct {
				Query    string `json:"query"`
				Provider string `json:"provider"`
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Data), &doc))
		assert.Equal(t, `{namespace=~".+"}`, doc.Metadata.Query)
		assert.Equal(t, "loki", doc.Metadata.Provider)
	})
}

// startLogsStubCapture is startLogsStub plus a capture of the decoded
// `input.request` object the tool posted, so a test can assert the wire shape
// api-server actually receives.
func startLogsStubCapture(t *testing.T, body string, captured *map[string]any) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rpc/logs", r.URL.Path)
		var payload struct {
			Input struct {
				Request map[string]any `json:"request"`
			} `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		*captured = payload.Input.Request
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

// TestNBLogToolV2_Call_SendsIndexInRequestBag pins the fix for the Elasticsearch
// index being dropped between llm-server and services-server. The top-level
// `index` field has no counterpart on services-server's FetchLogRequest, so it is
// discarded on decode; the index is only read out of the free-form `request` bag.
// Before the fix an ES query ran against the account's default index (SaaS) or
// failed outright with "index is required" (agent/relay), whichever index the
// agent picked.
func TestNBLogToolV2_Call_SendsIndexInRequestBag(t *testing.T) {
	const okBody = `{"logs":[],"query":"{}","provider":"ES"}`

	t.Run("explicit index from the query travels in request.index", func(t *testing.T) {
		var got map[string]any
		cleanup := startLogsStubCapture(t, okBody, &got)
		defer cleanup()

		tool := &NBLogToolV2{
			accountId:   "acc-1",
			logProvider: services_server.ObservabilityProvider{Provider: "ES", DefaultIndex: "logs-default-*"},
		}
		_, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Command: `{"where":{"namespace":{"_eq":"prod"}},"index":"app-logs-*"}`,
		})
		require.NoError(t, err)

		bag, ok := got["request"].(map[string]any)
		require.True(t, ok, "request bag missing from payload: %v", got)
		assert.Equal(t, "app-logs-*", bag["index"], "explicit index must override the account default")
		// Kept for callers that do read the typed field; the bag is what services-server reads.
		assert.Equal(t, "app-logs-*", got["index"])
	})

	t.Run("falls back to the provider default index", func(t *testing.T) {
		var got map[string]any
		cleanup := startLogsStubCapture(t, okBody, &got)
		defer cleanup()

		tool := &NBLogToolV2{
			accountId:   "acc-1",
			logProvider: services_server.ObservabilityProvider{Provider: "ES", DefaultIndex: "logs-default-*"},
		}
		_, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Command: `{"where":{"namespace":{"_eq":"prod"}}}`,
		})
		require.NoError(t, err)

		bag, ok := got["request"].(map[string]any)
		require.True(t, ok, "request bag missing from payload: %v", got)
		assert.Equal(t, "logs-default-*", bag["index"])
	})

	t.Run("no index resolved leaves the bag untouched", func(t *testing.T) {
		var got map[string]any
		cleanup := startLogsStubCapture(t, `{"logs":[],"query":"{}","provider":"loki"}`, &got)
		defer cleanup()

		tool := &NBLogToolV2{
			accountId:   "acc-1",
			logProvider: services_server.ObservabilityProvider{Provider: "loki"},
		}
		_, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Command: `{"where":{"namespace":{"_eq":"prod"}}}`,
		})
		require.NoError(t, err)

		if bag, ok := got["request"].(map[string]any); ok {
			assert.NotContains(t, bag, "index", "non-ES providers must not get a synthetic index")
		}
	})
}

// TestSetRequestIndex covers the helper's merge semantics directly: it must not
// clobber provider parameters that already ride in the request bag, and an empty
// index must leave the request untouched rather than writing an empty key.
func TestSetRequestIndex(t *testing.T) {
	t.Run("creates the bag when absent", func(t *testing.T) {
		req := services_server.LogQueryRequest{}
		setRequestIndex(&req, "logs-*")
		assert.Equal(t, map[string]any{"index": "logs-*"}, req.Request)
	})

	t.Run("merges into an existing bag", func(t *testing.T) {
		req := services_server.LogQueryRequest{Request: map[string]any{"query_type": "dsl"}}
		setRequestIndex(&req, "logs-*")
		assert.Equal(t, map[string]any{"query_type": "dsl", "index": "logs-*"}, req.Request)
	})

	t.Run("does not write through to the caller's map", func(t *testing.T) {
		// parseFetchLogConfigs aliases the bag straight from configs["request"],
		// so an in-place write would leak the index back into a configs map the
		// caller may reuse — or race a concurrent reader of it.
		caller := map[string]any{"query_type": "dsl"}
		req := services_server.LogQueryRequest{Request: caller}
		setRequestIndex(&req, "logs-*")
		assert.Equal(t, map[string]any{"query_type": "dsl"}, caller, "caller's map must be untouched")
		assert.Equal(t, "logs-*", req.Request["index"])
	})

	t.Run("empty index is a no-op", func(t *testing.T) {
		req := services_server.LogQueryRequest{}
		setRequestIndex(&req, "")
		assert.Nil(t, req.Request)
	})
}
