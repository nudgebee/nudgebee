package tools

import (
	"errors"
	"testing"

	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMongoToolContext builds a minimal NbToolContext wired to a vm_agent
// MongoDB proxy integration, without touching the network.
func newMongoToolContext() core.NbToolContext {
	sc := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"})
	return core.NbToolContext{
		AccountId: "acct-1",
		Ctx:       sc,
		ToolConfig: core.ToolConfig{
			Id:   "ds-mongo-1",
			Name: "mongo-prod",
			Values: []core.ToolConfigValue{
				{Name: "connection_mode", Value: "vm_agent"},
				{Name: "datasource_key", Value: "ds-mongo-1"},
				{Name: "host", Value: "mongo.internal"},
			},
		},
	}
}

// withFakeRelay swaps the relay seam for the duration of fn.
func withFakeRelay(t *testing.T, fake func(relay.ActionExecuteBody) (map[string]any, error), fn func()) {
	t.Helper()
	orig := mongoRelayExecute
	mongoRelayExecute = fake
	defer func() { mongoRelayExecute = orig }()
	fn()
}

func TestMongoDBTool_SendsExpectedCommand(t *testing.T) {
	cases := []struct {
		toolName   string
		wantAction string
	}{
		{ToolMongoServerStatus, "mongo_server_status"},
		{ToolMongoReplSetStatus, "mongo_repl_status"},
		{ToolMongoCurrentOp, "mongo_current_ops"},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			var captured relay.ActionExecuteBody
			fake := func(body relay.ActionExecuteBody) (map[string]any, error) {
				captured = body
				return map[string]any{"data": `{"ok":1}`}, nil
			}

			tool := MongoDBTool{toolName: tc.toolName}
			withFakeRelay(t, fake, func() {
				resp, err := tool.Call(newMongoToolContext(), core.NBToolCallRequest{})
				require.NoError(t, err)
				assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
			})

			// (a) each diagnostic maps to its dedicated forager action, routed via
			// the proxy agent and scoped to the datasource.
			assert.Equal(t, tc.wantAction, captured.ActionName)
			assert.Equal(t, "proxy", captured.AgentType, "vm_agent integration must route to the proxy agent")
			assert.Equal(t, "ds-mongo-1", captured.ActionParams["datasource_id"])

			// (b) these forager actions hardcode their own command — the tool must
			// NOT send a command document or timeout_ms in params.
			_, hasCommand := captured.ActionParams["command"]
			assert.False(t, hasCommand, "status actions must not send a command document")
			_, hasTimeout := captured.ActionParams["timeout_ms"]
			assert.False(t, hasTimeout, "status actions must not send timeout_ms")
		})
	}
}

func TestMongoDBTool_PassesThroughJSON(t *testing.T) {
	doc := `{"connections":{"current":42,"available":51158},"ok":1}`
	fake := func(relay.ActionExecuteBody) (map[string]any, error) {
		return map[string]any{"data": doc}, nil
	}

	tool := MongoDBTool{toolName: ToolMongoServerStatus}
	withFakeRelay(t, fake, func() {
		resp, err := tool.Call(newMongoToolContext(), core.NBToolCallRequest{})
		require.NoError(t, err)
		// (b) response is the raw JSON, passed through as Text (not a table).
		assert.Equal(t, core.NBToolResponseTypeText, resp.Type)
		assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.JSONEq(t, doc, resp.Data)
	})
}

func TestMongoDBTool_SurfacesRelayError(t *testing.T) {
	fake := func(relay.ActionExecuteBody) (map[string]any, error) {
		return nil, errors.New("dial tcp mongo.internal:27017: connection refused")
	}

	tool := MongoDBTool{toolName: ToolMongoReplSetStatus}
	withFakeRelay(t, fake, func() {
		resp, err := tool.Call(newMongoToolContext(), core.NBToolCallRequest{})
		// (c) a clear, propagated error.
		require.Error(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "connection refused")
	})
}

func TestMongoDBTool_SurfacesForagerError(t *testing.T) {
	fake := func(relay.ActionExecuteBody) (map[string]any, error) {
		// forager reports the error inside the data document.
		return map[string]any{"data": `{"error":"Authentication failed"}`}, nil
	}

	tool := MongoDBTool{toolName: ToolMongoCurrentOp}
	withFakeRelay(t, fake, func() {
		resp, err := tool.Call(newMongoToolContext(), core.NBToolCallRequest{})
		require.Error(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, resp.Data, "Authentication failed")
	})
}

func TestMongoDBTool_NoConfig(t *testing.T) {
	tool := MongoDBTool{toolName: ToolMongoServerStatus}
	ctx := core.NbToolContext{
		AccountId: "acct-1",
		Ctx:       security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"}),
	}
	_, err := tool.Call(ctx, core.NBToolCallRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "please configure")
}

func TestMongoDBTool_RequiresAccountId(t *testing.T) {
	// A configured integration with no tenant scope (empty AccountId) must
	// fail fast before any proxy execution, rather than running unscoped.
	ctx := newMongoToolContext()
	ctx.AccountId = ""

	tool := MongoDBTool{toolName: ToolMongoServerStatus}
	called := false
	withFakeRelay(t, func(relay.ActionExecuteBody) (map[string]any, error) {
		called = true
		return map[string]any{"data": `{"ok":1}`}, nil
	}, func() {
		resp, err := tool.Call(ctx, core.NBToolCallRequest{})
		require.Error(t, err)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, err.Error(), "accountId is required")
	})
	assert.False(t, called, "relay must not be invoked when tenant scope is missing")
}
