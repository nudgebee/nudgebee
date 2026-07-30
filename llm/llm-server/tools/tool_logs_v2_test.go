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
