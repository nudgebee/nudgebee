package tools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSearchRequest serves a canned response and records the query string the
// tool built, so argument handling can be asserted without a real server.
func captureSearchRequest(t *testing.T) *url.Values {
	t.Helper()
	captured := &url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*captured = q
		_, _ = w.Write([]byte(`{"workflows":[],"total_invocable":0}`))
	}))
	t.Cleanup(srv.Close)

	original := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = original })
	return captured
}

func TestWorkflowSearchToolIsAReadSoItNeedsNoConfirmation(t *testing.T) {
	// Searching has no effects; only the follow-up trigger does. Classifying it
	// as a write would put a confirmation prompt in front of every lookup.
	requestType, err := WorkflowSearchTool{}.InferToolRequestType(nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, requestType)
}

func TestWorkflowSearchToolSchemaMakesBothArgsOptional(t *testing.T) {
	// "What can you run for me?" must work without a query.
	schema := WorkflowSearchTool{}.InputSchema()
	assert.Empty(t, schema.Required)
	assert.Contains(t, schema.Properties, "query")
	assert.Contains(t, schema.Properties, "limit")
}

func TestWorkflowSearchToolBuildsQueryString(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      map[string]any
		wantQuery string
		wantLimit string
	}{
		{"query and numeric limit", map[string]any{"query": "pods crashlooping", "limit": float64(3)}, "pods crashlooping", "3"},
		{"limit as a string", map[string]any{"query": "restart", "limit": "7"}, "restart", "7"},
		{"query only", map[string]any{"query": "restart"}, "restart", ""},
		{"no args lists everything", map[string]any{}, "", ""},
		{"blank query is dropped", map[string]any{"query": "   "}, "", ""},
		{"zero limit falls back to the server default", map[string]any{"query": "x", "limit": float64(0)}, "x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := captureSearchRequest(t)
			ctx := core.NbToolContext{AccountId: "acct", Ctx: security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})}

			_, err := WorkflowSearchTool{}.Call(ctx, core.NBToolCallRequest{Arguments: tc.args})
			require.NoError(t, err)
			assert.Equal(t, tc.wantQuery, captured.Get("query"))
			assert.Equal(t, tc.wantLimit, captured.Get("limit"))
		})
	}
}

func TestWorkflowSearchToolWrapsTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	original := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = original })

	ctx := core.NbToolContext{AccountId: "acct", Ctx: security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})}
	_, err := WorkflowSearchTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"query": "x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to search automations")
}
