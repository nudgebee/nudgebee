package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	kgGrantTenant   = "kg-grant-tenant"
	kgGrantAccountA = "aaaaaaaa-1111-1111-1111-111111111111"
	kgGrantAccountB = "bbbbbbbb-2222-2222-2222-222222222222"
	kgGrantForeign  = "cccccccc-3333-3333-3333-333333333333" // another tenant's account
)

// kgGrantCaller is a security context in kgGrantTenant (accounts A and B).
func kgGrantCaller(t *testing.T, userID string, fields map[string]any) *security.SecurityContext {
	t.Helper()
	body := map[string]any{"TenantId": kgGrantTenant, "UserId": userID,
		"AccountIds": []string{kgGrantAccountA, kgGrantAccountB}, "Roles": []string{}}
	for k, v := range fields {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return &sc
}

// The manual node and dependency writes used to accept only a built-in role, so
// a person whose access comes from a custom role was refused although the
// gateway admitted them. manualAccountAuthorizer now takes the action's grant
// key too: tenant-wide for a row outside any account, tenant-wide or bound to
// the account for an account's row. The context is built the way
// buildContextFromPayload builds it for the action.
func TestManualWritesHonourTheActionsGrant(t *testing.T) {
	actions := []struct {
		name   string
		key    string
		access security.SecurityAccessType
	}{
		{"kg_create_manual_node", "kg:Write", security.SecurityAccessTypeCreate},
		{"kg_update_manual_node", "kg:Write", security.SecurityAccessTypeUpdate},
		{"kg_delete_manual_node", "kg:Write", security.SecurityAccessTypeDelete},
		{"kg_apply_manual_node_merge", "kg:Execute", security.SecurityAccessTypeUpdate},
		{"kg_create_manual_declarations", "kg:Write", security.SecurityAccessTypeCreate},
		{"kg_create_manual_dependency", "kg:Write", security.SecurityAccessTypeCreate},
		{"kg_update_manual_dependency", "kg:Write", security.SecurityAccessTypeUpdate},
		{"kg_delete_manual_dependency", "kg:Write", security.SecurityAccessTypeDelete},
		{"kg_resolve_manual_dependency", "kg:Execute", security.SecurityAccessTypeUpdate},
		{"kg_reresolve_manual_dependency", "kg:Execute", security.SecurityAccessTypeUpdate},
		{"kg_reresolve_manual_dependencies", "kg:Execute", security.SecurityAccessTypeUpdate},
	}
	otherClass := map[string]string{"kg:Write": "kg:Execute", "kg:Execute": "kg:Write"}

	for _, a := range actions {
		t.Run(a.name, func(t *testing.T) {
			require.Equal(t, a.key, security.ActionKey(a.name, ""), "the gateway's classification of the action")
			may := func(sc *security.SecurityContext, account string) bool {
				ctx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)
				ctx.SetActionPermission(security.ActionKey(a.name, ""))
				return manualAccountAuthorizer(ctx, a.access)(account)
			}

			tenantWide := kgGrantCaller(t, "u-tenant-wide", map[string]any{"CustomPermissions": map[string]bool{a.key: true}})
			assert.True(t, may(tenantWide, kgGrantAccountA), "tenant-wide grant, an account's row")
			assert.True(t, may(tenantWide, ""), "tenant-wide grant, a row outside any account")
			assert.False(t, may(tenantWide, kgGrantForeign), "a grant never reaches another tenant's account")

			bound := kgGrantCaller(t, "u-bound", map[string]any{"ScopedCustomPermissions": map[string]map[string]bool{kgGrantAccountA: {a.key: true}}})
			assert.True(t, may(bound, kgGrantAccountA), "grant bound to the row's account")
			assert.False(t, may(bound, kgGrantAccountB), "grant bound to another account")
			assert.False(t, may(bound, ""), "an account-bound grant does not cover a tenant-level row")

			for _, other := range []string{otherClass[a.key], "events:Write", "kg:Read"} {
				sc := kgGrantCaller(t, "u-other", map[string]any{"CustomPermissions": map[string]bool{other: true}})
				assert.False(t, may(sc, kgGrantAccountA), "grant %s does not authorize %s", other, a.key)
				assert.False(t, may(sc, ""), "grant %s does not authorize %s", other, a.key)
			}

			admin := kgGrantCaller(t, "u-admin", map[string]any{"Roles": []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
				"ScopedEntityIds": map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {kgGrantAccountA}}})
			assert.True(t, may(admin, kgGrantAccountA), "built-in account admin, own account")
			assert.False(t, may(admin, kgGrantAccountB), "built-in account admin, another account")
			assert.False(t, may(admin, ""), "built-in account admin, a tenant-level row")

			tenantAdmin := kgGrantCaller(t, "u-tadmin", map[string]any{"Roles": []string{security.AUTH_TENANT_ADMIN_ROLE}})
			assert.True(t, may(tenantAdmin, kgGrantAccountB), "built-in tenant admin, any account")
			assert.True(t, may(tenantAdmin, ""), "built-in tenant admin, a tenant-level row")
		})
	}
}

// kgGrantRequest runs a handler as userID, whose context is served from the
// security-context cache the way NewSecurityContext serves it.
func kgGrantRequest(t *testing.T, sc *security.SecurityContext, action string, input map[string]any,
	handler func(*ActionRequest, *gin.Context, *trace.Tracer, *metric.Meter, *slog.Logger)) int {
	t.Helper()
	raw, err := json.Marshal(sc)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet("security_context_v2", kgGrantTenant+":"+sc.GetUserId(), raw))
	t.Cleanup(func() { _ = common.CacheDelete("security_context_v2", kgGrantTenant+":"+sc.GetUserId()) })

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/rpc/kg", nil)
	c.Request.Header.Set("x-tenant-id", kgGrantTenant)
	c.Request.Header.Set("x-user-id", sc.GetUserId())
	handler(&ActionRequest{Action: ActionRequestAction{Name: action}, Input: input}, c, nil, nil, slog.Default())
	return rec.Code
}

// The two panic buttons withdraw every account's rows, so they take tenant-wide
// access: a built-in tenant role or a tenant-wide grant for the action's key
// (kg:Write). A refused caller gets 403 before the delete opens a transaction.
func TestManualDeleteAllHonoursTheActionsGrant(t *testing.T) {
	handlers := map[string]func(*ActionRequest, *gin.Context, *trace.Tracer, *metric.Meter, *slog.Logger){
		"kg_delete_all_manual_nodes":        handleKgDeleteAllManualNodes,
		"kg_delete_all_manual_dependencies": handleKgDeleteAllManualDependencies,
	}
	callers := []struct {
		name    string
		fields  map[string]any
		allowed bool
	}{
		{"custom role, tenant-wide kg:Write", map[string]any{"CustomPermissions": map[string]bool{"kg:Write": true}}, true},
		{"built-in tenant admin", map[string]any{"Roles": []string{security.AUTH_TENANT_ADMIN_ROLE}}, true},
		{"custom role, kg:Write bound to one account", map[string]any{"ScopedCustomPermissions": map[string]map[string]bool{kgGrantAccountA: {"kg:Write": true}}}, false},
		{"custom role, another module's grant", map[string]any{"CustomPermissions": map[string]bool{"events:Write": true}}, false},
		{"custom role, kg:Read", map[string]any{"CustomPermissions": map[string]bool{"kg:Read": true}}, false},
		{"no role, no grant", nil, false},
		{"built-in account admin", map[string]any{"Roles": []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			"ScopedEntityIds": map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {kgGrantAccountA}}}, false},
	}
	for action, handler := range handlers {
		require.Equal(t, "kg:Write", security.ActionKey(action, ""), action)
		for _, caller := range callers {
			t.Run(action+"/"+caller.name, func(t *testing.T) {
				mock := mockMetastore(t)
				if caller.allowed {
					// Past the check the delete opens its transaction; failing it
					// keeps the test from needing the rest of the statements.
					mock.ExpectBegin().WillReturnError(errors.New("reached the delete"))
				}
				code := kgGrantRequest(t, kgGrantCaller(t, "u-"+action, caller.fields), action, nil, handler)
				if caller.allowed {
					assert.Equal(t, http.StatusInternalServerError, code, "past the check, into the delete")
				} else {
					assert.Equal(t, http.StatusForbidden, code)
				}
				assert.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

// The single-node writes load the declaration and check its account. A grant
// bound to that account lets the call through to the write; a grant bound to
// another account, or another module's grant, is refused with nothing written.
func TestManualNodeWritesHonourTheActionsGrant(t *testing.T) {
	handlers := map[string]func(*ActionRequest, *gin.Context, *trace.Tracer, *metric.Meter, *slog.Logger){
		"kg_update_manual_node":      handleKgUpdateManualNode,
		"kg_delete_manual_node":      handleKgDeleteManualNode,
		"kg_apply_manual_node_merge": handleKgApplyManualNodeMerge,
	}
	declaration := func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery("FROM kg_manual_nodes").WithArgs(kgGrantTenant, int64(7)).WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "cloud_account_id", "cloud_provider", "node_type", "specific_type", "name",
			"region", "namespace", "network_node_id", "description", "properties", "labels",
			"kg_node_id", "unique_key", "base_properties", "status", "duplicate_node_id", "declared_by_user_id",
			"created_at", "updated_at",
		}).AddRow(int64(7), kgGrantTenant, kgGrantAccountA, "AWS", "MessageQueue", "SQSQueue", "orders",
			"", "", "", "", nil, nil, "dddddddd-0000-0000-0000-000000000000", "k", nil, "active", "", "", time.Now(), time.Now()))
	}
	for action, handler := range handlers {
		key := security.ActionKey(action, "")
		callers := []struct {
			name    string
			fields  map[string]any
			allowed bool
		}{
			{"grant bound to the declaration's account", map[string]any{"ScopedCustomPermissions": map[string]map[string]bool{kgGrantAccountA: {key: true}}}, true},
			{"tenant-wide grant", map[string]any{"CustomPermissions": map[string]bool{key: true}}, true},
			{"built-in account admin of the account", map[string]any{"Roles": []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
				"ScopedEntityIds": map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {kgGrantAccountA}}}, true},
			{"grant bound to another account", map[string]any{"ScopedCustomPermissions": map[string]map[string]bool{kgGrantAccountB: {key: true}}}, false},
			{"another module's grant", map[string]any{"CustomPermissions": map[string]bool{"events:Write": true}}, false},
		}
		for _, caller := range callers {
			t.Run(action+"/"+caller.name, func(t *testing.T) {
				mock := mockMetastore(t)
				declaration(mock)
				// Nothing past the load is mocked: an allowed call fails in the
				// write itself, a refused one never gets there.
				code := kgGrantRequest(t, kgGrantCaller(t, "u-"+action, caller.fields), action,
					map[string]any{"request": map[string]any{"id": 7}}, handler)
				if caller.allowed {
					assert.NotEqual(t, http.StatusForbidden, code, "past the check, into the write")
				} else {
					assert.Equal(t, http.StatusForbidden, code)
				}
				assert.NoError(t, mock.ExpectationsWereMet(), "the declaration was loaded")
			})
		}
	}
}
