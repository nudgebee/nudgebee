package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Workspace routes skip the service-token middleware and workspace pods can
// reach them, so extractWorkspacePayloadAndContext is their only gate.

const testServiceToken = "svc-token-for-tests"

func withServiceToken(t *testing.T, token string) {
	t.Helper()
	prevToken, prevHeader, prevJwt := config.Config.LlmServerToken, config.Config.LlmServerTokenHeader, config.Config.LlmServerJwtSecret
	t.Cleanup(func() {
		config.Config.LlmServerToken, config.Config.LlmServerTokenHeader, config.Config.LlmServerJwtSecret = prevToken, prevHeader, prevJwt
	})
	config.Config.LlmServerToken = token
	config.Config.LlmServerTokenHeader = "X-ACTION-TOKEN"
	config.Config.LlmServerJwtSecret = "test-secret"
}

func workspaceRequest(t *testing.T, body any, headers map[string]string) *gin.Context {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workspace/files", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

// RPC action bodies that claim the server's admin role or another tenant,
// sent from a workspace with no token.
func unauthenticatedActionBodies() map[string]map[string]any {
	return map[string]map[string]any{
		"session_variables role admin": {
			"action":            map[string]any{"name": "ai_list_workspace_files"},
			"input":             map[string]any{"account_id": "other-acct"},
			"session_variables": map[string]any{"role": "admin", "allowed_roles": []any{"super_admin"}},
		},
		"payload tenant_id": {
			"action": map[string]any{"name": "ai_get_workspace_file"},
			"input":  map[string]any{"request": map[string]any{"account_id": "other-acct", "tenant_id": "other-tenant", "path": "x"}},
		},
	}
}

func TestWorkspaceActionWithoutServiceTokenIsRefused(t *testing.T) {
	withServiceToken(t, testServiceToken)
	for name, body := range unauthenticatedActionBodies() {
		t.Run(name, func(t *testing.T) {
			c := workspaceRequest(t, body, map[string]string{"x-tenant-id": "other-tenant"})
			_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
			require.ErrorContains(t, err, "missing X-ACTION-TOKEN")
			assert.Nil(t, ctx)
		})
	}
}

func TestWorkspaceActionWithWrongServiceTokenIsRefused(t *testing.T) {
	withServiceToken(t, testServiceToken)
	body := unauthenticatedActionBodies()["session_variables role admin"]
	c := workspaceRequest(t, body, map[string]string{"X-ACTION-TOKEN": "wrong"})
	_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
	require.ErrorContains(t, err, "invalid X-ACTION-TOKEN")
	assert.Nil(t, ctx)
}

// With no LLM_SERVER_TOKEN configured an action can't be authenticated, so
// every action is refused, even one sending an empty header.
func TestWorkspaceActionRefusedWhenServiceTokenUnset(t *testing.T) {
	withServiceToken(t, "")
	for name, body := range unauthenticatedActionBodies() {
		t.Run(name, func(t *testing.T) {
			c := workspaceRequest(t, body, nil)
			_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
			require.ErrorContains(t, err, "disabled until LLM_SERVER_TOKEN is configured")
			assert.Nil(t, ctx)
		})
	}
	c := workspaceRequest(t, map[string]any{"account_id": "acc"}, map[string]string{"X-ACTION-TOKEN": "anything"})
	_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
	require.ErrorContains(t, err, "invalid X-ACTION-TOKEN")
	assert.Nil(t, ctx)
}

// The app gateway's call: the service token plus the caller's identity. The
// identity decides the context (a tenant user's call resolves through the DB
// and is covered by BuildContextFromPayload's own callers); the token alone no
// longer makes an action run as the server.
func TestWorkspaceActionWithServiceTokenUsesCallerIdentity(t *testing.T) {
	withServiceToken(t, testServiceToken)

	t.Run("gateway admin session", func(t *testing.T) {
		body := map[string]any{
			"action":            map[string]any{"name": "ai_list_workspace_files"},
			"input":             map[string]any{"request": map[string]any{"path": "/"}},
			"session_variables": map[string]any{"role": "admin"},
		}
		c := workspaceRequest(t, body, map[string]string{"X-ACTION-TOKEN": testServiceToken})
		payload, ctx, action, err := extractWorkspacePayloadAndContext(c, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "ai_list_workspace_files", action)
		assert.Equal(t, "/", payload["path"])
		assert.True(t, ctx.GetSecurityContext().IsSuperAdmin())
	})

	t.Run("no identity", func(t *testing.T) {
		body := map[string]any{
			"action": map[string]any{"name": "ai_list_workspace_files"},
			"input":  map[string]any{"request": map[string]any{"account_id": "acc", "path": "/"}},
		}
		c := workspaceRequest(t, body, map[string]string{"X-ACTION-TOKEN": testServiceToken})
		_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
		require.ErrorContains(t, err, "unable to identify tenant")
		assert.Nil(t, ctx)
	})
}

func TestWorkspaceServiceTokenWithoutActionRunsAsServer(t *testing.T) {
	withServiceToken(t, testServiceToken)
	c := workspaceRequest(t, map[string]any{"account_id": "acc", "tool": "kubectl"}, map[string]string{"X-ACTION-TOKEN": testServiceToken})
	_, ctx, action, err := extractWorkspacePayloadAndContext(c, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, action)
	assert.True(t, ctx.GetSecurityContext().IsSuperAdmin())
}

// The shim's call: no service token, an execution JWT for the user.
func TestWorkspaceExecutionTokenStillAuthorizes(t *testing.T) {
	withServiceToken(t, testServiceToken)
	security.SetTenantIdCacheForTest("jwt-acct", "jwt-tenant")
	stubTenantAccounts(t, "jwt-acct")
	token, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("jwt-tenant", "jwt-user", []string{"jwt-acct"}), "jwt-acct", "", time.Minute)
	require.NoError(t, err)

	c := workspaceRequest(t, map[string]any{"account_id": "jwt-acct", "tool": "kubectl"}, map[string]string{"X-Workspace-Token": token})
	_, ctx, _, err := extractWorkspacePayloadAndContext(c, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "jwt-tenant", ctx.GetSecurityContext().GetTenantId())
	assert.Equal(t, "jwt-user", ctx.GetSecurityContext().GetUserId())
	assert.False(t, ctx.GetSecurityContext().IsSuperAdmin())

	// The same JWT does not unlock the action branch.
	body := unauthenticatedActionBodies()["session_variables role admin"]
	c = workspaceRequest(t, body, map[string]string{"X-Workspace-Token": token})
	_, _, _, err = extractWorkspacePayloadAndContext(c, nil, nil)
	require.ErrorContains(t, err, "missing X-ACTION-TOKEN")
}
