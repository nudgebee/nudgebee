package workspace

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseExecutionToken(t *testing.T, token string) *WorkspaceTokenClaims {
	t.Helper()
	claims := &WorkspaceTokenClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(config.Config.LlmServerJwtSecret), nil })
	require.NoError(t, err)
	return claims
}

func executionTokenTestSetup(t *testing.T) {
	t.Helper()
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("ws-acct", "t1")
	orig := security.WorkspaceTenantAccounts
	security.WorkspaceTenantAccounts = func(string) ([]string, error) { return []string{"ws-acct", "other-cluster"}, nil }
	t.Cleanup(func() { security.WorkspaceTenantAccounts = orig })
}

func userContext(t *testing.T, raw string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(raw), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

func TestExecutionTokenCarriesCallerGrants(t *testing.T) {
	executionTokenTestSetup(t)
	ctx := userContext(t, `{"TenantId":"t1","UserId":"u1","AccountIds":["ws-acct","other-cluster"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["ws-acct"]}}`)

	token, err := ExecutionToken(ctx, "ws-acct", "", time.Minute)
	require.NoError(t, err)
	claims := parseExecutionToken(t, token)
	assert.Equal(t, jwt.ClaimStrings{AudienceWorkspaceExecute}, claims.Audience)
	assert.Equal(t, "ws-acct", claims.AccountId)
	assert.Equal(t, "t1", claims.TenantId)
	require.NotNil(t, claims.Grants)
	assert.Equal(t, "u1", claims.Grants.UserId)
	assert.Equal(t, []string{"account_admin_readonly"}, claims.Grants.Roles)
	assert.WithinDuration(t, time.Now().Add(time.Minute), claims.ExpiresAt.Time, 5*time.Second)

	_, err = ExecutionToken(ctx, "ws-acct", "other-cluster", time.Minute)
	require.ErrorContains(t, err, "access denied", "a target the user cannot read is refused")
}

func TestExecutionTokenForSystemFlowNeverCarriesSuperAdmin(t *testing.T) {
	executionTokenTestSetup(t)
	token, err := ExecutionToken(security.NewRequestContextForSuperAdmin(), "ws-acct", "other-cluster", time.Minute)
	require.NoError(t, err)
	claims := parseExecutionToken(t, token)
	assert.Equal(t, "t1", claims.TenantId, "tenant comes from the workspace account, not the context")
	assert.Equal(t, "other-cluster", claims.TargetAccountId)
	assert.Equal(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, claims.Grants.Roles)
	assert.Equal(t, security.GetSystemUserId(), claims.Grants.UserId)
}

func TestCommandEnvMintsPerAttemptWithoutMutatingCaller(t *testing.T) {
	executionTokenTestSetup(t)
	w := &workspaceManager{httpClient: &http.Client{Timeout: 60 * time.Second}}
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{"ws-acct", "other-cluster"})
	caller := map[string]string{
		ENV_NB_TOOL_CONFIG_NAME:  "other",
		ENV_NB_TARGET_ACCOUNT_ID: "other-cluster",
		ENV_NB_WORKSPACE_TOKEN:   "caller-supplied",
	}

	env, err := w.commandEnv(ctx, "ws-acct", caller)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		ENV_NB_TOOL_CONFIG_NAME:  "other",
		ENV_NB_TARGET_ACCOUNT_ID: "other-cluster",
		ENV_NB_WORKSPACE_TOKEN:   "caller-supplied",
	}, caller, "the caller's map is not changed, so a retry starts clean")
	_, leaked := env[ENV_NB_TARGET_ACCOUNT_ID]
	assert.False(t, leaked, "the target request never reaches the pod")
	assert.Equal(t, "ws-acct", env["nb_account_id"])

	claims := parseExecutionToken(t, env[ENV_NB_WORKSPACE_TOKEN])
	assert.Equal(t, "other-cluster", claims.TargetAccountId, "caller-supplied tokens are replaced")
	assert.WithinDuration(t, time.Now().Add(90*time.Second), claims.ExpiresAt.Time, 5*time.Second)
}
