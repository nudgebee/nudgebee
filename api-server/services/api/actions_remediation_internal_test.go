package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The remediation loop's actions are NudgeBee's own: a call that names a user, or a super-admin context, is
// refused before anything is read.
func TestRemediationInternalActionsAreSystemOnly(t *testing.T) {
	const account = "7b0f3c4e-1d2a-4b5c-9e8f-0a1b2c3d4e5f"
	for _, tc := range []struct {
		name    string
		caller  string
		allowed bool
	}{
		{"the tenant's system identity", `{"TenantId":"tenant-1","AccountIds":["` + account + `"],"Roles":["account_admin","tenant_admin"]}`, true},
		{"a person, even a tenant admin", `{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["` + account + `"],"Roles":["account_admin","tenant_admin"]}`, false},
		{"a super admin", `{"IsServerInternal":true}`, false},
		{"no tenant", `{"AccountIds":["` + account + `"],"Roles":["account_admin"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(tc.caller), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
			for _, handler := range []func(*ActionRequest, *gin.Context, *security.RequestContext){
				handleEventsApplyRemediationVerdict, handleEventsUpdateRefireLabel,
			} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				// No event id: an allowed caller gets past the identity check to the input check (400).
				handler(&ActionRequest{Input: map[string]any{"account_id": account}}, c, ctx)
				if tc.allowed {
					assert.Equal(t, http.StatusBadRequest, rec.Code)
				} else {
					assert.Equal(t, http.StatusForbidden, rec.Code)
				}
			}
		})
	}
}

// llm-server calls the loop's actions by name (api/remediation_verdict.go); the triage switch must keep a case
// for each, and the app gateway must not route them.
func TestRemediationInternalActionsMatchTheirCaller(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	src, err := os.ReadFile(filepath.Join(root, "llm/llm-server/api/remediation_verdict.go"))
	if err != nil {
		t.Skipf("llm-server source not available: %v", err)
	}
	names := regexp.MustCompile(`action(?:ApplyRemediationVerdict|UpdateRefireLabel)\s*=\s*"([a-z_]+)"`).FindAllSubmatch(src, -1)
	require.Len(t, names, 2, "the caller's action constants were renamed")
	handled := handledTriageActions(t)
	gateway, err := os.ReadFile(filepath.Join(root, "app/src/lib/actions.yaml"))
	require.NoError(t, err)
	for _, m := range names {
		assert.Contains(t, handled, string(m[1]))
		assert.NotContains(t, string(gateway), "name: "+string(m[1]), "a service-only action must not be routed by the app gateway")
	}
}

// A likely-fixed verdict is resolved by a person with write access only: NudgeBee's own identity, and a read-only
// person, are refused before anything is read.
func TestRemediationResolveIsAPersonsWithWriteAccess(t *testing.T) {
	const account = "7b0f3c4e-1d2a-4b5c-9e8f-0a1b2c3d4e5f"
	for _, tc := range []struct {
		name   string
		caller string
		code   int
	}{
		{"a person with write access", `{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["` + account + `"],"Roles":["account_admin","tenant_admin"]}`, http.StatusBadRequest},
		{"a read-only person", `{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["` + account + `"],"Roles":["tenant_admin_readonly"]}`, http.StatusForbidden},
		{"the tenant's system identity", `{"TenantId":"tenant-1","AccountIds":["` + account + `"],"Roles":["account_admin","tenant_admin"]}`, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(tc.caller), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			// An allowed caller gets past the checks to the missing run id (400).
			handleEventsApplyRemediationResolve(&ActionRequest{Input: map[string]any{"account_id": account, "event_id": "ev"}}, c, ctx)
			assert.Equal(t, tc.code, rec.Code)
		})
	}
	assert.Contains(t, handledTriageActions(t), "events_apply_remediation_resolve")
}
