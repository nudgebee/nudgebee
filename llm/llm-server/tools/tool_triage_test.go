package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTriageToolContext builds a tool context whose security context already
// carries a tenant id, so the triage helper resolves tenant from the context
// (header path) without falling back to a DB lookup.
func newTriageToolContext(accountId string) core.NbToolContext {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Build the security context from JSON so the tenant id is set without a DB
	// lookup — the triage helper reads tenant from the context (header path).
	secCtx := &security.SecurityContext{}
	if err := json.Unmarshal([]byte(`{"TenantId":"tenant-123","UserId":"user-1","Roles":["tenant_admin"]}`), secCtx); err != nil {
		panic(err)
	}
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	return core.NbToolContext{
		AccountId: accountId,
		Ctx:       reqCtx,
	}
}

// startTriageStub spins up an httptest server standing in for api-server's
// /rpc/triage endpoint and points config.Config.ServiceEndpoint at it.
// handler receives the decoded action name and input map and returns the body.
func startTriageStub(t *testing.T, handler func(action string, input map[string]any) (int, string)) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rpc/triage", r.URL.Path)
		assert.Equal(t, "tenant-123", r.Header.Get("x-tenant-id"))

		var payload struct {
			Action struct {
				Name string `json:"name"`
			} `json:"action"`
			Input map[string]any `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		status, body := handler(payload.Action.Name, payload.Input)
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

func TestTriageExplanationTool(t *testing.T) {
	t.Run("requires event_id", func(t *testing.T) {
		tool := TriageExplanationTool{}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("forwards event_id and returns body", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"event_id":"e1","is_duplicate":true,"correlation_count":2}`
		})
		defer cleanup()

		tool := TriageExplanationTool{}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Arguments: map[string]any{"event_id": "e1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "event_get_triage", gotAction)
		assert.Equal(t, "e1", gotInput["event_id"])
		assert.Contains(t, resp.Data, `"is_duplicate":true`)
	})

	t.Run("read-only request type", func(t *testing.T) {
		rt, err := TriageExplanationTool{}.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, rt)
	})
}

func TestTriageRulesTool(t *testing.T) {
	var gotAction string
	var gotInput map[string]any
	cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
		gotAction = action
		gotInput = input
		return http.StatusOK, `{"rules":[]}`
	})
	defer cleanup()

	tool := TriageRulesTool{}
	resp, err := tool.Call(newTriageToolContext("acc-9"), core.NBToolCallRequest{
		Arguments: map[string]any{"rule_type": "suppression", "enabled": true},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, "event_get_triage_rules", gotAction)
	assert.Equal(t, "acc-9", gotInput["cloud_account_id"])
	assert.Equal(t, "suppression", gotInput["rule_type"])
	assert.Equal(t, true, gotInput["enabled"])
}

func TestThresholdSuggestionsTool(t *testing.T) {
	var gotAction string
	cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
		gotAction = action
		return http.StatusOK, `{"suggestions":[],"total":0}`
	})
	defer cleanup()

	tool := ThresholdSuggestionsTool{}
	resp, err := tool.Call(newTriageToolContext("acc-2"), core.NBToolCallRequest{
		Arguments: map[string]any{"confidence": "high"},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, "event_list_threshold_suggestions", gotAction)
}

func TestTriageDryRunTool(t *testing.T) {
	t.Run("requires rule_type and action", func(t *testing.T) {
		tool := TriageDryRunTool{}
		resp, err := tool.Call(newTriageToolContext("acc-3"), core.NBToolCallRequest{
			Arguments: map[string]any{"rule_type": "suppression"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("forwards match criteria", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"match_count":3100,"estimated_reduction":97}`
		})
		defer cleanup()

		tool := TriageDryRunTool{}
		resp, err := tool.Call(newTriageToolContext("acc-3"), core.NBToolCallRequest{
			Arguments: map[string]any{
				"rule_type":       "suppression",
				"action":          "suppress",
				"match_alertname": "RabbitmqNoQueueConsumer",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "events_dryrun_triage_rule", gotAction)
		assert.Equal(t, "acc-3", gotInput["cloud_account_id"])
		assert.Equal(t, "RabbitmqNoQueueConsumer", gotInput["match_alertname"])
		assert.Contains(t, resp.Data, "estimated_reduction")
	})
}

func TestTriageToolErrorSurfacing(t *testing.T) {
	cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
		return http.StatusInternalServerError, `{"error":"boom"}`
	})
	defer cleanup()

	tool := TriageRulesTool{}
	resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
	// Errors are surfaced to the LLM as a tool error, not a Go error.
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "Error:")
}

func TestBoundTriageExplanation(t *testing.T) {
	t.Run("caps chain and correlations, preserves totals", func(t *testing.T) {
		chain := make([]map[string]any, 28)
		for i := range chain {
			chain[i] = map[string]any{"occurrence_number": i + 1}
		}
		corr := make([]map[string]any, 28)
		for i := range corr {
			corr[i] = map[string]any{"correlated_event_id": i, "correlation_score": float64(i) / 100.0}
		}
		raw, _ := json.Marshal(map[string]any{
			"event_id":          "e1",
			"duplicate_info":    map[string]any{"total_occurrences": 28, "duplicate_chain": chain},
			"correlated_events": corr,
			"correlation_count": 28,
		})

		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(boundTriageExplanation(string(raw))), &out))

		di := out["duplicate_info"].(map[string]any)
		gotChain := di["duplicate_chain"].([]any)
		assert.Len(t, gotChain, maxDuplicateChainEntries)
		// kept the MOST RECENT occurrences (chain tail)
		assert.Equal(t, float64(28), gotChain[len(gotChain)-1].(map[string]any)["occurrence_number"])
		assert.Equal(t, float64(28), di["total_occurrences"], "total preserved")
		assert.Equal(t, true, di["duplicate_chain_truncated"])

		gotCorr := out["correlated_events"].([]any)
		assert.Len(t, gotCorr, maxCorrelations)
		// sorted by score desc → first is the highest (0.27)
		assert.InDelta(t, 0.27, gotCorr[0].(map[string]any)["correlation_score"], 0.001)
		assert.Equal(t, float64(28), out["correlation_count"], "count preserved")
		assert.Equal(t, true, out["correlated_events_truncated"])
	})

	t.Run("small payload passes through unchanged", func(t *testing.T) {
		raw := `{"event_id":"e1","is_duplicate":false,"correlated_events":[]}`
		assert.JSONEq(t, raw, boundTriageExplanation(raw))
	})

	t.Run("invalid json returned as-is", func(t *testing.T) {
		assert.Equal(t, "not json", boundTriageExplanation("not json"))
	})
}

func TestEventClassificationTool(t *testing.T) {
	t.Run("missing classification (404) returns clean not-classified result", func(t *testing.T) {
		cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
			assert.Equal(t, "event_get_classification", action)
			return http.StatusNotFound, `{"error":"classification not found"}`
		})
		defer cleanup()

		tool := EventClassificationTool{}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Arguments: map[string]any{"event_id": "e1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, `"classified":false`)
	})

	t.Run("returns classification body", func(t *testing.T) {
		cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
			return http.StatusOK, `{"classification":"false_positive","reason_code":"known_noise"}`
		})
		defer cleanup()

		tool := EventClassificationTool{}
		resp, err := tool.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{
			Arguments: map[string]any{"event_id": "e1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, "known_noise")
	})

	t.Run("requires event_id", func(t *testing.T) {
		resp, err := EventClassificationTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})
}

func TestTriageRuleEventsTool(t *testing.T) {
	t.Run("requires rule_id", func(t *testing.T) {
		resp, err := TriageRuleEventsTool{}.Call(newTriageToolContext("acc-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("forwards rule_id and account scope", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startTriageStub(t, func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"events":[],"total":0}`
		})
		defer cleanup()

		resp, err := TriageRuleEventsTool{}.Call(newTriageToolContext("acc-5"), core.NBToolCallRequest{
			Arguments: map[string]any{"rule_id": "rule-9", "limit": float64(10)},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "event_get_triage_rule_events", gotAction)
		assert.Equal(t, "rule-9", gotInput["rule_id"])
		assert.Equal(t, "acc-5", gotInput["account_id"])
	})
}
