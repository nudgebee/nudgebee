package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startRPCStub stands in for api-server's /rpc/* action endpoints. It asserts
// the identity headers on every request — x-user-id in particular, because
// omitting it silently escalates the call to tenant-admin on the api-server
// side, which no test would otherwise catch.
func startRPCStub(t *testing.T, wantPath string, handler func(action string, input map[string]any) (int, string)) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, wantPath, r.URL.Path)
		assert.Equal(t, "tenant-123", r.Header.Get("x-tenant-id"))
		assert.Equal(t, "user-1", r.Header.Get("x-user-id"))

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

// newNoUserToolContext mimics an automated/system flow: tenant present, no
// requesting user (EffectiveUserIdForRPC → ""). The write helper must refuse
// it — forwarding an empty x-user-id would escalate to tenant-admin on the
// api-server side.
func newNoUserToolContext(accountId string) core.NbToolContext {
	nbCtx := newTriageToolContext(accountId)
	secCtx := &security.SecurityContext{}
	if err := json.Unmarshal([]byte(`{"TenantId":"tenant-123","Roles":["tenant_admin"]}`), secCtx); err != nil {
		panic(err)
	}
	nbCtx.Ctx = security.NewRequestContext(context.Background(), secCtx, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	return nbCtx
}

func TestRecommendationApplyTool(t *testing.T) {
	t.Run("requires recommendation_id without calling the server", func(t *testing.T) {
		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("refuses to run without a requesting user", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			t.Error("a write without a requesting user must never reach api-server")
			return http.StatusOK, `{}`
		})
		defer cleanup()

		resp, err := RecommendationApplyTool{}.Call(newNoUserToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": "rec-1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "requires a requesting user")
	})

	t.Run("posts the apply envelope with NBLLM resolver", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startRPCStub(t, "/rpc/recommendation", func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"status":"InProgress","pr_action":"created"}`
		})
		defer cleanup()

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{
				"recommendation_id": "rec-1",
				"provider":          "git",
				"data":              map[string]any{"web": map[string]any{"memory": map[string]any{"request": "512Mi"}}},
				"provider_config":   map[string]any{"name": "github-main"},
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "recommendations_apply", gotAction)

		object, ok := gotInput["object"].(map[string]any)
		require.True(t, ok, "apply payload must be wrapped in input.object")
		assert.Equal(t, "acc-7", object["account_id"])
		assert.Equal(t, "rec-1", object["recommendation_id"])
		assert.Equal(t, "git", object["provider"])
		assert.Equal(t, "NBLLM", object["resolver_type"])
		_, hasData := object["data"].(map[string]any)
		assert.True(t, hasData)
		assert.Contains(t, resp.Data, "pr_action")
	})

	t.Run("data defaults to an object, never null", func(t *testing.T) {
		var gotInput map[string]any
		cleanup := startRPCStub(t, "/rpc/recommendation", func(_ string, input map[string]any) (int, string) {
			gotInput = input
			return http.StatusOK, `{"status":"InProgress"}`
		})
		defer cleanup()

		_, err := RecommendationApplyTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": "rec-1"},
		})
		assert.NoError(t, err)
		object := gotInput["object"].(map[string]any)
		data, ok := object["data"].(map[string]any)
		require.True(t, ok, "absent data must serialize as an empty object")
		assert.Empty(t, data)
	})

	t.Run("permission failure surfaces as recoverable error", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			return http.StatusForbidden, `{"message":"not allowed"}`
		})
		defer cleanup()

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": "rec-1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "not permitted")
	})

	t.Run("classifies as write and confirms per action", func(t *testing.T) {
		rt, err := RecommendationApplyTool{}.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeUpdate, rt)

		k1 := RecommendationApplyTool{}.ConfirmationKey(`{"recommendation_id":"rec-1"}`)
		k2 := RecommendationApplyTool{}.ConfirmationKey(`{"recommendation_id":"rec-2"}`)
		assert.True(t, strings.HasPrefix(k1, ToolRecommendationApply+":"))
		assert.NotEqual(t, k1, k2)
		assert.Equal(t, k1, RecommendationApplyTool{}.ConfirmationKey(` {"recommendation_id":"rec-1"} `))
	})
}

func TestRecommendationCliTool(t *testing.T) {
	t.Run("requires commands", func(t *testing.T) {
		resp, err := RecommendationCliTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("posts a flat payload to the cloud endpoint", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startRPCStub(t, "/rpc/cloud", func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"results":[{"command":"aws s3 ls","status":"SUCCESS"}]}`
		})
		defer cleanup()

		resp, err := RecommendationCliTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{
				"commands":          []any{"aws s3 ls"},
				"recommendation_id": "rec-9",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "cloud_execute_command", gotAction)

		// Cloud handlers unmarshal the input directly — an object wrapper would
		// silently produce an empty request.
		_, wrapped := gotInput["object"]
		assert.False(t, wrapped)
		assert.Equal(t, "acc-7", gotInput["account_id"])
		assert.Equal(t, "rec-9", gotInput["recommendation_id"])
		cmds, ok := gotInput["commands"].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{"aws s3 ls"}, cmds)
	})

	t.Run("classifies as write and confirms per action", func(t *testing.T) {
		rt, err := RecommendationCliTool{}.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeUpdate, rt)
		assert.NotEqual(t,
			RecommendationCliTool{}.ConfirmationKey(`{"commands":["a"]}`),
			RecommendationCliTool{}.ConfirmationKey(`{"commands":["b"]}`))
	})
}

func TestRecommendationTicketResolutionTool(t *testing.T) {
	t.Run("requires recommendation_id and ticket_id", func(t *testing.T) {
		resp, err := RecommendationTicketResolutionTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": "rec-1"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("records the ticket linkage with NBLLM resolver", func(t *testing.T) {
		var gotAction string
		var gotInput map[string]any
		cleanup := startRPCStub(t, "/rpc/recommendation", func(action string, input map[string]any) (int, string) {
			gotAction = action
			gotInput = input
			return http.StatusOK, `{"status":"InProgress"}`
		})
		defer cleanup()

		resp, err := RecommendationTicketResolutionTool{}.Call(newTriageToolContext("acc-7"), core.NBToolCallRequest{
			Arguments: map[string]any{
				"recommendation_id": "rec-1",
				"ticket_id":         "OPS-42",
				"ticket_key":        "OPS-42",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, "recommendations_create_ticket_resolution", gotAction)

		object, ok := gotInput["object"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "rec-1", object["recommendation_id"])
		assert.Equal(t, "OPS-42", object["ticket_id"])
		assert.Equal(t, "NBLLM", object["resolver_type"])
	})

	t.Run("classifies as write and confirms per action", func(t *testing.T) {
		rt, err := RecommendationTicketResolutionTool{}.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeCreate, rt)
		assert.NotEqual(t,
			RecommendationTicketResolutionTool{}.ConfirmationKey(`{"ticket_id":"OPS-1"}`),
			RecommendationTicketResolutionTool{}.ConfirmationKey(`{"ticket_id":"OPS-2"}`))
	})
}
