package vmpackage

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// security_scan_vm is classified security:Execute and security_update_vm_active
// security:Write. A custom role holding the action's grant on the account gets
// past the access check (and on to the account lookup); another class or
// module's grant is refused before any query; the built-in roles that passed
// before still pass.
func TestVMWritesHonourTheActionGrant(t *testing.T) {
	errPastAuthorization := errors.New("past authorization")
	caller := func(t *testing.T, key string, roles []string, scoped map[string][]string, grants map[string]bool) *security.RequestContext {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{"vm-account"},
			"Roles": roles, "ScopedEntityIds": scoped,
			"ScopedCustomPermissions": map[string]any{"vm-account": grants},
		})
		require.NoError(t, err)
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal(raw, &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		ctx.SetActionPermission(key)
		return ctx
	}
	active := true
	actions := []struct {
		name string
		key  string
		call func(*security.RequestContext) error
	}{
		{"security_scan_vm", "security:Execute", func(ctx *security.RequestContext) error {
			_, err := ScanPackages(ctx, ScanRequest{AccountId: "vm-account", CloudResourceId: "resource-1"})
			return err
		}},
		{"security_update_vm_active", "security:Write", func(ctx *security.RequestContext) error {
			return UpdateVMActive(ctx, VMActiveRequest{AccountId: "vm-account", CloudResourceId: "resource-1", IsActive: &active})
		}},
	}
	for _, a := range actions {
		other := "security:Write"
		if a.key == other {
			other = "security:Execute"
		}
		callers := []struct {
			name    string
			roles   []string
			scoped  map[string][]string
			grants  map[string]bool
			allowed bool
		}{
			{"custom role with the action's grant (" + a.key + ")", []string{}, nil, map[string]bool{a.key: true}, true},
			{"custom role with " + other, []string{}, nil, map[string]bool{other: true}, false},
			{"custom role with security:Read", []string{}, nil, map[string]bool{"security:Read": true}, false},
			{"custom role with another module's grant", []string{}, nil, map[string]bool{"k8s:" + a.key[len("security:"):]: true}, false},
			{"custom role with no grant", []string{}, nil, map[string]bool{}, false},
			{"built-in account_admin", []string{"account_admin"}, map[string][]string{"account_admin": {"vm-account"}}, nil, true},
			{"built-in account_admin_readonly", []string{"account_admin_readonly"}, map[string][]string{"account_admin_readonly": {"vm-account"}}, nil, false},
		}
		for _, c := range callers {
			t.Run(a.name+"/"+c.name, func(t *testing.T) {
				mock := withMockDB(t)
				if c.allowed {
					mock.ExpectQuery("FROM cloud_accounts WHERE id = \\$1").WillReturnError(errPastAuthorization)
				}
				err := a.call(caller(t, a.key, c.roles, c.scoped, c.grants))
				if c.allowed {
					assert.ErrorIs(t, err, errPastAuthorization)
				} else {
					var e common.Error
					require.ErrorAs(t, err, &e)
					assert.Equal(t, 403, e.Code)
				}
				assert.NoError(t, mock.ExpectationsWereMet(), "a refused caller must not reach the database")
			})
		}
	}
}
