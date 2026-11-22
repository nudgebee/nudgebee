package ownership

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ownership writes used to admit only a tenant or super admin, so a custom
// role granted ownership:Write / ownership:Execute was refused by the handler
// after the gateway let it through. They now take the action's tenant-wide grant.
func TestOwnershipWritesHonourTheActionsGrant(t *testing.T) {
	db, _, err := sqlmock.New() // no expectations: a call let through fails on its first query
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	calls := map[string]func(ctx *security.RequestContext) error{
		"ownership_assign": func(ctx *security.RequestContext) error {
			_, err := AssignOwner(ctx, AssignOwnerRequest{ResourceType: ResourceTypeWorkload, ResourceKey: "w1", OwnerType: OwnerTypeUser, OwnerId: "u2"})
			return err
		},
		"ownership_delete": func(ctx *security.RequestContext) error {
			_, err := RemoveOwner(ctx, DeleteOwnerRequest{ResourceType: ResourceTypeWorkload, ResourceKey: "w1"})
			return err
		},
		"ownership_cleanup": func(ctx *security.RequestContext) error {
			_, err := CleanupOrphans(ctx, struct{}{})
			return err
		},
		"ownership_upsert_rule": func(ctx *security.RequestContext) error {
			_, err := UpsertRule(ctx, UpsertRuleRequest{Name: "r", MatchScope: MatchScopeNamespace, MatchValue: "ns", OwnerType: OwnerTypeUser, OwnerId: "u2"})
			return err
		},
		"ownership_delete_rule": func(ctx *security.RequestContext) error {
			_, err := DeleteRule(ctx, DeleteRuleRequest{Id: "r1"})
			return err
		},
	}

	for action, call := range calls {
		t.Run(action, func(t *testing.T) {
			key := security.ActionKey(action, "")
			require.NotEmpty(t, key)
			own := map[string]bool{key: true}
			assert.False(t, denied(call(caller(t, nil, own, key))), "custom role holding the action's grant")
			assert.True(t, denied(call(caller(t, nil, nil, key))), "custom role without a grant")
			assert.True(t, denied(call(caller(t, nil, map[string]bool{"tickets:Write": true}, key))), "another module's grant")
			assert.True(t, denied(call(caller(t, nil, map[string]bool{"ownership:Read": true}, key))), "read grant")
			assert.True(t, denied(call(caller(t, nil, own, ""))), "no key: built-in roles only")
			assert.True(t, denied(call(caller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, key))), "tenant read admin")
			assert.False(t, denied(call(caller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, ""))), "tenant admin")
			assert.False(t, denied(call(caller(t, []string{security.AUTH_SUPER_ADMIN_FULL_ROLE}, nil, ""))), "super admin")
		})
	}
}

// NoActiveResource goes to the callers who may assign an owner, whichever read
// action is being served.
func TestResourceStateGoesToWhoeverMayAssign(t *testing.T) {
	for _, key := range []string{security.ActionKey("ownership_get", ""), security.ActionKey("ownership_resolve", "")} {
		assert.True(t, canSeeResourceState(caller(t, nil, map[string]bool{"ownership:Write": true}, key)), key+": write grant")
		assert.True(t, canSeeResourceState(caller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, key)), key+": tenant admin")
		assert.True(t, canSeeResourceState(caller(t, []string{security.AUTH_SUPER_ADMIN_FULL_ROLE}, nil, key)), key+": super admin")
		assert.False(t, canSeeResourceState(caller(t, nil, map[string]bool{"ownership:Read": true}, key)), key+": read grant")
		assert.False(t, canSeeResourceState(caller(t, nil, map[string]bool{"ownership:Execute": true}, key)), key+": execute grant")
		assert.False(t, canSeeResourceState(caller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, key)), key+": tenant read admin")
	}
}

func caller(t *testing.T, roles []string, grants map[string]bool, key string) *security.RequestContext {
	t.Helper()
	if roles == nil {
		roles = []string{}
	}
	raw, err := json.Marshal(map[string]any{
		"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{"acct-1"}, "Roles": roles,
		"CustomPermissions": grants,
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	ctx.SetActionPermission(key)
	return ctx
}

func denied(err error) bool {
	var e common.Error
	return errors.As(err, &e) && e.Code == 403
}
