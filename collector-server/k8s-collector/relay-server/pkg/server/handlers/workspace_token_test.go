package handlers

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceTokenRequiresExecuteAudience(t *testing.T) {
	const secret = "test-secret"
	sign := func(audience ...string) string {
		claims := workspaceTokenClaims{AccountId: "acct", TenantId: "tenant", RegisteredClaims: jwt.RegisteredClaims{
			Audience:  audience,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		}}
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		require.NoError(t, err)
		return s
	}

	claims, err := parseWorkspaceToken(sign(workspaceExecuteAudience), secret)
	require.NoError(t, err)
	require.Equal(t, "acct", claims.AccountId)

	_, err = parseWorkspaceToken(sign("workspace-pod"), secret)
	require.Error(t, err, "the pod's own token must not execute tools")
	_, err = parseWorkspaceToken(sign(), secret)
	require.Error(t, err, "tokens issued before audiences existed are pod tokens")
}
