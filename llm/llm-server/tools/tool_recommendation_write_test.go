package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/DATA-DOG/go-sqlmock"
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

const (
	applyRecID     = "3f6b2a1e-5c4d-4e8f-9a0b-1c2d3e4f5a6b"
	applyAccountID = "0b4c7d2e-8f1a-4b3c-9d5e-6f7a8b9c0d1e"
)

// stubSafetyRow serves the next recommendation_view lookup the apply
// precondition makes, with the given stored band (nil = never assessed).
func stubSafetyRow(mock sqlmock.Sqlmock, band any) {
	mock.ExpectQuery("(?i)select").WillReturnRows(sqlmock.NewRows(
		[]string{"rule_name", "status", "safety_band", "safety_reason", "dependent_count", "production_dependents", "estimated_saving"}).
		AddRow("pod_right_sizing", "Open", band, "2 production dependent(s) in the blast radius", 3, 2, 41.0))
}

func TestRecommendationApplyTool(t *testing.T) {
	// One mocked metastore for the whole test: the database manager is cached
	// after its first use, so a per-subtest mock would hand later subtests a
	// closed handle.
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()

	t.Run("requires recommendation_id without calling the server", func(t *testing.T) {
		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{Arguments: map[string]any{}})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	})

	t.Run("refuses to run without a requesting user", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			t.Error("a write without a requesting user must never reach api-server")
			return http.StatusOK, `{}`
		})
		defer cleanup()

		stubSafetyRow(mock, "safe")

		resp, err := RecommendationApplyTool{}.Call(newNoUserToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "safe"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "requires a requesting user")
	})

	t.Run("refuses without a safety band and hands back the stored facts", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			t.Error("an apply without safety context must never reach api-server")
			return http.StatusOK, `{}`
		})
		defer cleanup()
		stubSafetyRow(mock, "risky")

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		// resp.Data is JSON-encoded, so match on the unquoted phrases.
		assert.Contains(t, resp.Data, "call again with safety_band=")
		assert.Contains(t, resp.Data, "risky")
		assert.Contains(t, resp.Data, "production_dependents")
	})

	t.Run("refuses a band that no longer matches the store", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			t.Error("a stale band must never reach api-server")
			return http.StatusOK, `{}`
		})
		defer cleanup()
		stubSafetyRow(mock, "safe")

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "risky"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "does not match the stored verdict")
		assert.Contains(t, resp.Data, "safe")
	})

	t.Run("refuses when the safety lookup itself fails", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			t.Error("an unverified apply must never reach api-server")
			return http.StatusOK, `{}`
		})
		defer cleanup()
		mock.ExpectQuery("(?i)select").WillReturnError(errors.New("connection reset"))

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "safe"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "could not verify")
	})

	t.Run("a never-assessed recommendation reads as unknown", func(t *testing.T) {
		cleanup := startRPCStub(t, "/rpc/recommendation", func(string, map[string]any) (int, string) {
			return http.StatusOK, `{"status":"InProgress"}`
		})
		defer cleanup()
		stubSafetyRow(mock, nil)

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "unknown"},
		})
		assert.NoError(t, err)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
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

		stubSafetyRow(mock, "review")

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{
				"recommendation_id": applyRecID,
				"safety_band":       "review",
				"safety_reason":     "3 dependent(s); none detected as production",
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
		assert.Equal(t, applyAccountID, object["account_id"])
		assert.Equal(t, applyRecID, object["recommendation_id"])
		assert.Equal(t, "git", object["provider"])
		assert.Equal(t, "NBLLM", object["resolver_type"])
		// The safety context gates the call; it is not part of the apply payload.
		_, forwarded := object["safety_band"]
		assert.False(t, forwarded)
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

		stubSafetyRow(mock, "safe")

		_, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "safe"},
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

		stubSafetyRow(mock, "safe")

		resp, err := RecommendationApplyTool{}.Call(newTriageToolContext(applyAccountID), core.NBToolCallRequest{
			Arguments: map[string]any{"recommendation_id": applyRecID, "safety_band": "safe"},
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

// The approval card must describe the actual change in operator terms — a raw
// recommendation id tells the approver nothing.
func TestRecommendationWriteToolConfirmationQuestions(t *testing.T) {
	t.Run("apply shows the summary and channel", func(t *testing.T) {
		q := RecommendationApplyTool{}.ConfirmationQuestion(
			`{"recommendation_id":"rec-1","provider":"git","summary":"Right-size deployment product-catalog (demo): memory request 512Mi → 300Mi"}`)
		assert.Contains(t, q, "Right-size deployment product-catalog (demo): memory request 512Mi → 300Mi")
		assert.Contains(t, q, "a pull request")
		assert.Contains(t, q, "rec-1")
		assert.Contains(t, q, "Do you want to continue?")
	})

	t.Run("apply without summary still names the action, not just raw JSON", func(t *testing.T) {
		q := RecommendationApplyTool{}.ConfirmationQuestion(`{"recommendation_id":"rec-1"}`)
		assert.Contains(t, q, "Apply recommendation rec-1")
		assert.Contains(t, q, "resolution attempt")
	})

	t.Run("apply renders the exact values from data", func(t *testing.T) {
		q := RecommendationApplyTool{}.ConfirmationQuestion(
			`{"recommendation_id":"rec-1","data":{"web":{"cpu":{"request":"0.1"},"memory":{"request":"300Mi","limit":"350Mi"}},"sidecar":{"memory":{"request":"64Mi"}}}}`)
		assert.Contains(t, q, "Values to apply:")
		assert.Contains(t, q, "- web: cpu request → 0.1, memory request → 300Mi, memory limit → 350Mi")
		assert.Contains(t, q, "- sidecar: memory request → 64Mi")
		// Sorted container order keeps the card (and the confirmation key's
		// input) stable across renders.
		assert.Less(t, strings.Index(q, "- sidecar"), strings.Index(q, "- web"))
	})

	t.Run("apply leads with the summary, then the safety line and its safeguard", func(t *testing.T) {
		q := RecommendationApplyTool{}.ConfirmationQuestion(
			`{"recommendation_id":"rec-1","provider":"kubernetes","summary":"Right-size checkout: CPU 500m → 250m (est. $41/mo)","safety_band":"risky","safety_reason":"2 production dependent(s) in the blast radius"}`)
		assert.Contains(t, q, "Safety: Risky — 2 production dependent(s) in the blast radius. Safeguard: use the no-restart (in-place) apply")
		assert.Less(t, strings.Index(q, "est. $41/mo"), strings.Index(q, "Safety:"))
		assert.NotContains(t, strings.ToLower(q), "are you sure")
	})

	t.Run("safety line adapts to the band and the channel", func(t *testing.T) {
		safe := RecommendationApplyTool{}.ConfirmationQuestion(`{"recommendation_id":"rec-1","safety_band":"safe"}`)
		assert.Contains(t, safe, "Safety: Safe — no known dependents.")
		assert.NotContains(t, safe, "Safeguard")

		cloud := RecommendationApplyTool{}.ConfirmationQuestion(`{"recommendation_id":"rec-1","provider":"aws","safety_band":"review","safety_reason":"4 dependent(s); none detected as production."}`)
		assert.Contains(t, cloud, "Safety: Review — 4 dependent(s); none detected as production. Safeguard: apply in a maintenance window")
		assert.NotContains(t, cloud, "no-restart")

		unknown := RecommendationApplyTool{}.ConfirmationQuestion(`{"recommendation_id":"rec-1","safety_band":"unknown"}`)
		assert.Contains(t, unknown, "not in the dependency graph yet")
	})

	t.Run("apply falls back to default rendering on unparseable input", func(t *testing.T) {
		assert.Equal(t, "", RecommendationApplyTool{}.ConfirmationQuestion("not-json"))
	})

	t.Run("cli lists the exact commands and caps long batches", func(t *testing.T) {
		q := RecommendationCliTool{}.ConfirmationQuestion(
			`{"commands":["aws cloudwatch put-metric-alarm --alarm-name a","aws s3 ls"],"recommendation_id":"rec-9"}`)
		assert.Contains(t, q, "Execute 2 cloud CLI commands")
		assert.Contains(t, q, "- aws cloudwatch put-metric-alarm --alarm-name a")
		assert.Contains(t, q, "- aws s3 ls")
		assert.Contains(t, q, "rec-9")

		long := RecommendationCliTool{}.ConfirmationQuestion(
			`{"commands":["c1","c2","c3","c4","c5","c6","c7"]}`)
		assert.Contains(t, long, "Execute 7 cloud CLI commands")
		assert.Contains(t, long, "…and 2 more")
		assert.NotContains(t, long, "- c6")
	})

	t.Run("ticket linkage states the status effect", func(t *testing.T) {
		q := RecommendationTicketResolutionTool{}.ConfirmationQuestion(
			`{"recommendation_id":"rec-1","ticket_id":"OPS-42"}`)
		assert.Contains(t, q, "Record ticket OPS-42")
		assert.Contains(t, q, "recommendation rec-1")
		assert.Contains(t, q, "settles automatically when the ticket closes")
	})
}
