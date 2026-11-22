package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"
	"nudgebee/services/vmpackage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The VM scan actions honour the grant the gateway classifies them as:
// security_scan_vm_account is security:Execute, and the two lists the scan pages
// read are security:Read. A custom role with the action's grant on the account
// gets past the check; another class or module's grant is refused before any
// query; the built-in roles that passed before still pass.
func TestVMScanActionsHonourTheActionGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const account = accessTestOwnAccount
	errPastAuthorization := errors.New("past authorization")

	caller := func(t *testing.T, key string, roles []string, grants map[string]bool) *security.RequestContext {
		t.Helper()
		scoped := map[string][]string{}
		for _, r := range roles {
			scoped[r] = []string{account}
		}
		raw, err := json.Marshal(map[string]any{
			"TenantId": accessTestTenant, "UserId": "user-1", "AccountIds": []string{account},
			"Roles": roles, "ScopedEntityIds": scoped,
			"ScopedCustomPermissions": map[string]any{account: grants},
		})
		require.NoError(t, err)
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal(raw, &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		ctx.SetActionPermission(key)
		return ctx
	}
	// respond runs a gin-style handler and reports whether it refused (403).
	respond := func(h func(*gin.Context, *security.RequestContext, string)) func(*security.RequestContext) bool {
		return func(ctx *security.RequestContext) bool {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			h(c, ctx, account)
			return rec.Code == http.StatusForbidden
		}
	}

	actions := []struct {
		name     string
		key      string
		firstSQL string
		refused  func(*security.RequestContext) bool
	}{
		{"security_scan_vm_account", "security:Execute", "FROM cloud_accounts WHERE id = \\$1", func(ctx *security.RequestContext) bool {
			_, err := scanVmAccount(ctx, vmpackage.ScanAccountRequest{AccountId: account})
			var e common.Error
			return errors.As(err, &e) && e.Code == http.StatusForbidden
		}},
		{"security_list_vm_scan_statuses", "security:Read", "FROM vm_scan_status", respond(respondVMScanStatuses)},
		{"security_list_vm_scan_datasources", "security:Read", "FROM integrations i", respond(respondVMScanDatasources)},
	}
	for _, a := range actions {
		write := a.key != "security:Read"
		callers := []struct {
			name    string
			key     string
			roles   []string
			grants  map[string]bool
			allowed bool
		}{
			{"custom role with the action's grant (" + a.key + ")", a.key, []string{}, map[string]bool{a.key: true}, true},
			{"custom role with security:Write", a.key, []string{}, map[string]bool{"security:Write": true}, !write}, // Write implies Read, never Execute
			{"custom role with security:Read", a.key, []string{}, map[string]bool{"security:Read": true}, !write},
			{"custom role with another module's grant", a.key, []string{}, map[string]bool{"k8s:Read": true, "k8s:Execute": true}, false},
			{"custom role with the grant but no action key", "", []string{}, map[string]bool{a.key: true}, false},
			{"built-in account_admin", a.key, []string{"account_admin"}, nil, true},
			{"built-in account_admin_readonly", a.key, []string{"account_admin_readonly"}, nil, !write},
		}
		for _, c := range callers {
			t.Run(a.name+"/"+c.name, func(t *testing.T) {
				mock := mockMetastore(t)
				if c.allowed {
					mock.ExpectQuery(a.firstSQL).WillReturnError(errPastAuthorization)
				}
				assert.Equal(t, !c.allowed, a.refused(caller(t, c.key, c.roles, c.grants)))
				assert.NoError(t, mock.ExpectationsWereMet(), "a refused caller must not reach the database; an allowed one must")
			})
		}
	}
}
