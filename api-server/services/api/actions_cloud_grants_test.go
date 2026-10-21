package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A user whose only access is a custom role reaches the cloud command handler
// through the grant the gateway admitted the action on; without that grant, or
// with only a Read grant, the handler refuses as before.
func TestCloudExecuteCommandHonorsCustomGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const account = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name       string
		grants     string
		permission string
		forbidden  bool
	}{
		{"cloud:Execute grant", `{"cloud:Execute":true}`, "cloud:Execute", false},
		{"cloud:Read grant cannot run a command", `{"cloud:Read":true}`, "cloud:Read", true},
		{"no grant", `{}`, "cloud:Execute", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockMetastore(t) // past the access check the handler reads the account; nothing is mocked, so it stops there
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+account+`"],"CustomPermissions":`+tc.grants+`}`), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
			ctx.SetActionPermission(tc.permission)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			handleCloudExecuteCommand(&ActionRequest{Input: map[string]any{
				"account_id": account,
				"commands":   []any{"aws ec2 describe-instances"},
			}}, c, ctx)

			assert.Equal(t, tc.forbidden, rec.Code == http.StatusForbidden, "status %d", rec.Code)
		})
	}
}
