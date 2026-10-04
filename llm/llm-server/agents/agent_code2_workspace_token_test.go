package agents

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The analysis's model calls must run as the user who started it (or the
// system user for background runs), for at least the whole polling window.
func TestSetAnalysisWorkspaceToken(t *testing.T) {
	origSecret := config.Config.LlmServerJwtSecret
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("ws-acct", "t1")
	orig := security.WorkspaceTenantAccounts
	security.WorkspaceTenantAccounts = func(string) ([]string, error) { return []string{"ws-acct"}, nil }
	t.Cleanup(func() {
		security.WorkspaceTenantAccounts = orig
		config.Config.LlmServerJwtSecret = origSecret
	})

	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"t1","UserId":"u1","AccountIds":["ws-acct"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["ws-acct"]}}`), &sc))
	userCtx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

	cases := map[string]struct {
		ctx       *security.RequestContext
		wantUser  string
		wantRoles []string
	}{
		"user run":       {ctx: userCtx, wantUser: "u1", wantRoles: []string{"account_admin_readonly"}},
		"background run": {ctx: security.NewRequestContextForSuperAdmin(), wantUser: security.GetSystemUserId(), wantRoles: []string{security.AUTH_TENANT_ADMIN_ROLE}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			analyzeRequest := map[string]any{}
			require.NoError(t, setAnalysisWorkspaceToken(tc.ctx, "ws-acct", analyzeRequest))

			token, ok := analyzeRequest["workspace_token"].(string)
			require.True(t, ok)
			claims := &workspace.WorkspaceTokenClaims{}
			_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(config.Config.LlmServerJwtSecret), nil })
			require.NoError(t, err)
			assert.Equal(t, jwt.ClaimStrings{workspace.AudienceWorkspaceExecute}, claims.Audience)
			assert.Equal(t, "ws-acct", claims.AccountId)
			require.NotNil(t, claims.Grants)
			assert.Equal(t, tc.wantUser, claims.Grants.UserId)
			assert.Equal(t, tc.wantRoles, claims.Grants.Roles)
			assert.True(t, claims.ExpiresAt.After(time.Now().Add(codeAnalysisMaxPollDuration)), "token must outlive the polling window")
		})
	}
}
