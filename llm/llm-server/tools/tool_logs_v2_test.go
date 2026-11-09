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
		// FetchLogsAgent's executedLogQuery/providerFromLogs read to show the
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

// startLogsStubWindows stands in for api-server's /rpc/logs, answering each
// request through respond with the window it was asked to read, and records
// those windows in order.
func startLogsStubWindows(t *testing.T, windows *[][2]time.Time, respond func(start, end time.Time) string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input struct {
				Request struct {
					StartTime int64 `json:"start_time"`
					EndTime   int64 `json:"end_time"`
				} `json:"request"`
			} `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		start := time.UnixMilli(payload.Input.Request.StartTime).UTC()
		end := time.UnixMilli(payload.Input.Request.EndTime).UTC()
		*windows = append(*windows, [2]time.Time{start, end})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respond(start, end)))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

// logsBody is a services-server reply carrying one line per timestamp.
func logsBody(timestamps ...string) string {
	logs := make([]map[string]string, 0, len(timestamps))
	for _, ts := range timestamps {
		logs = append(logs, map[string]string{"timestamp": ts, "message": "line at " + ts})
	}
	body, _ := json.Marshal(map[string]any{"logs": logs, "query": "{}", "provider": "loki"})
	return string(body)
}

// A window under five minutes is widened to thirty before it is read, and the
// read returns the newest lines up to its limit. On a busy workload that can
// fill the limit from the end of the widened window, so none of the lines come
// from the minutes asked for (issue #40652).
func TestNBLogToolV2_Call_NarrowWindowReadsTheRequestedMinutes(t *testing.T) {
	requestedStart := time.Date(2026, 10, 8, 10, 30, 0, 0, time.UTC)
	requestedEnd := time.Date(2026, 10, 8, 10, 32, 0, 0, time.UTC)
	widenedStart := time.Date(2026, 10, 8, 10, 16, 0, 0, time.UTC)
	widenedEnd := time.Date(2026, 10, 8, 10, 46, 0, 0, time.UTC)
	narrow := `{"where":{"namespace":{"_eq":"loki"}},"start_time":"2026-10-08T10:30:00Z","end_time":"2026-10-08T10:32:00Z","limit":3}`
	tool := &NBLogToolV2{accountId: "acc-1", logProvider: services_server.ObservabilityProvider{Provider: "loki"}}

	coverageOf := func(t *testing.T, data string) *core.ObservabilityLogCoverage {
		t.Helper()
		var doc core.ObservabilityLogResponse
		require.NoError(t, json.Unmarshal([]byte(data), &doc))
		return doc.Coverage
	}

	t.Run("a widened read that stopped after the window is replaced by a read of the window", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(start, _ time.Time) string {
			if start.Equal(widenedStart) {
				return logsBody("2026-10-08T10:45:51Z", "2026-10-08T10:45:55Z", "2026-10-08T10:45:59Z")
			}
			return logsBody("2026-10-08T10:30:10Z", "2026-10-08T10:31:00Z")
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Equal(t, [][2]time.Time{{widenedStart, widenedEnd}, {requestedStart, requestedEnd}}, windows)
		assert.Contains(t, resp.Data, "2026-10-08T10:30:10Z")
		assert.NotContains(t, resp.Data, "10:45:")
		assert.Nil(t, coverageOf(t, resp.Data), "the read of the window alone did not reach its limit")
	})

	t.Run("a re-read that also stops at its limit reports the window asked for", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(start, _ time.Time) string {
			if start.Equal(widenedStart) {
				return logsBody("2026-10-08T10:45:51Z", "2026-10-08T10:45:55Z", "2026-10-08T10:45:59Z")
			}
			return logsBody("2026-10-08T10:31:57Z", "2026-10-08T10:31:58Z", "2026-10-08T10:31:59Z")
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Len(t, windows, 2)
		coverage := coverageOf(t, resp.Data)
		require.NotNil(t, coverage)
		assert.Equal(t, "2026-10-08T10:30:00Z", coverage.RequestedStart)
		assert.Equal(t, "2026-10-08T10:32:00Z", coverage.RequestedEnd)
		assert.Empty(t, coverage.ReadStart, "the read ran over the window asked for")
	})

	t.Run("a widened read that holds the whole window is kept and names both windows", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(_, _ time.Time) string {
			return logsBody("2026-10-08T10:20:00Z", "2026-10-08T10:31:00Z", "2026-10-08T10:45:59Z")
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Equal(t, [][2]time.Time{{widenedStart, widenedEnd}}, windows)
		coverage := coverageOf(t, resp.Data)
		require.NotNil(t, coverage)
		assert.Equal(t, "2026-10-08T10:30:00Z", coverage.RequestedStart)
		assert.Equal(t, "2026-10-08T10:32:00Z", coverage.RequestedEnd)
		assert.Equal(t, "2026-10-08T10:16:00Z", coverage.ReadStart)
		assert.Equal(t, "2026-10-08T10:46:00Z", coverage.ReadEnd)
	})

	t.Run("an empty re-read names the window asked for", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(start, _ time.Time) string {
			if start.Equal(widenedStart) {
				return logsBody("2026-10-08T10:45:51Z", "2026-10-08T10:45:55Z", "2026-10-08T10:45:59Z")
			}
			return logsBody()
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Len(t, windows, 2)
		assert.Contains(t, resp.Data, NoLogsFoundPrefix)
		assert.Contains(t, resp.Data, "time range: 2026-10-08T10:30:00Z to 2026-10-08T10:32:00Z")
	})

	t.Run("a widened read the backend cut short is replaced as well", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(start, _ time.Time) string {
			if start.Equal(widenedStart) {
				return `{"logs":[{"timestamp":"2026-10-08T10:45:59Z","message":"late"}],"query":"{}","provider":"loki","partial":true}`
			}
			return logsBody("2026-10-08T10:31:00Z")
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Equal(t, [][2]time.Time{{widenedStart, widenedEnd}, {requestedStart, requestedEnd}}, windows)
		assert.Contains(t, resp.Data, "2026-10-08T10:31:00Z")
	})

	t.Run("a failed re-read fails the call rather than return lines from other minutes", func(t *testing.T) {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"backend timeout"}`))
				return
			}
			_, _ = w.Write([]byte(logsBody("2026-10-08T10:45:51Z", "2026-10-08T10:45:55Z", "2026-10-08T10:45:59Z")))
		}))
		defer srv.Close()
		prev := config.Config.ServiceEndpoint
		config.Config.ServiceEndpoint = srv.URL
		defer func() { config.Config.ServiceEndpoint = prev }()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.Error(t, err)
		assert.Equal(t, 2, calls)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.NotContains(t, resp.Data, "10:45:")
	})

	t.Run("a widened read under its limit is kept as it is", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(_, _ time.Time) string {
			return logsBody("2026-10-08T10:25:00Z", "2026-10-08T10:45:59Z")
		})
		defer cleanup()

		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Command: narrow})
		require.NoError(t, err)
		require.Equal(t, [][2]time.Time{{widenedStart, widenedEnd}}, windows)
		assert.Nil(t, coverageOf(t, resp.Data))
	})

	t.Run("a window that was not widened is read once", func(t *testing.T) {
		var windows [][2]time.Time
		cleanup := startLogsStubWindows(t, &windows, func(_, _ time.Time) string {
			return logsBody("2026-10-08T10:39:57Z", "2026-10-08T10:39:58Z", "2026-10-08T10:39:59Z")
		})
		defer cleanup()

		_, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Command: `{"where":{"namespace":{"_eq":"loki"}},"start_time":"2026-10-08T10:30:00Z","end_time":"2026-10-08T10:40:00Z","limit":3}`,
		})
		require.NoError(t, err)
		assert.Len(t, windows, 1)
	})
}

func TestCoverageSpans(t *testing.T) {
	start := time.Date(2026, 10, 8, 10, 30, 0, 0, time.UTC)
	end := time.Date(2026, 10, 8, 10, 32, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		oldest, newest string
		want           bool
	}{
		{"lines on both sides of the window", "2026-10-08T10:20:00Z", "2026-10-08T10:45:00Z", true},
		{"oldest line a second before the start", "2026-10-08T10:29:59Z", "2026-10-08T10:32:00Z", true},
		// 10:30:00Z stands for any line up to 10:30:00.999, which may be after a start of 10:30:00.
		{"oldest line in the start's second", "2026-10-08T10:30:00Z", "2026-10-08T10:45:00Z", false},
		{"all lines after the window", "2026-10-08T10:45:51Z", "2026-10-08T10:45:59Z", false},
		{"lines start inside the window", "2026-10-08T10:31:00Z", "2026-10-08T10:45:00Z", false},
		{"lines end inside the window", "2026-10-08T10:20:00Z", "2026-10-08T10:31:00Z", false},
		{"span unknown", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := coverageSpans(&core.ObservabilityLogCoverage{Oldest: tc.oldest, Newest: tc.newest}, start, end)
			assert.Equal(t, tc.want, got)
		})
	}
}
