package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantCaller builds the context the action gateway builds for a caller: its
// built-in roles, its custom grants, and the action's classified key.
func grantCaller(t *testing.T, key string, roles []string, scopedEntities map[string][]string, tenantGrants map[string]bool, accountGrants map[string]map[string]bool) *security.RequestContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{"account-A"},
		"Roles": roles, "ScopedEntityIds": scopedEntities,
		"CustomPermissions": tenantGrants, "ScopedCustomPermissions": accountGrants,
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	ctx.SetActionPermission(key)
	return ctx
}

// stopAtDatabase points the metastore at a fresh sqlmock for one subtest, so
// a call that gets past its access check fails with errPastAuthorization (when
// the case expects it) and a refused one reaches no query. The package's shared
// metastore (TestMain) is put back afterwards.
var errPastAuthorization = errors.New("past authorization")

func stopAtDatabase(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	previous, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return manager, nil })
	t.Cleanup(func() {
		_ = db.Close()
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return previous, nil })
		// Re-prime the cache: later tests (webhook_recovery_test) read it to
		// decide what to restore.
		_, _ = database.GetDatabaseManager(database.Metastore)
	})
	return mock
}

// integrations_diagnose_connection (integrations:Read) and
// integrations_check_llm_defaults (integrations:Read) are reads: a custom role
// holding integrations:Read gets past the check, another module's grant does
// not, and the built-in roles that passed before still pass.
func TestIntegrationReadsHonourTheReadGrant(t *testing.T) {
	const key = "integrations:Read"
	none := map[string]bool{}
	cases := []struct {
		name string
		ctx  func(t *testing.T) *security.RequestContext
		diag bool // passes DiagnoseIntegrationConnectionForAccount on account-A
		llm  bool // passes CheckLLMChange
	}{
		{"custom role, account-bound integrations:Read", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{}, nil, none, map[string]map[string]bool{"account-A": {key: true}})
		}, true, false}, // an account-bound grant never satisfies a tenant-level read
		{"custom role, tenant-wide integrations:Read", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{}, nil, map[string]bool{key: true}, nil)
		}, true, true},
		{"custom role, tenant-wide integrations:Write", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{}, nil, map[string]bool{"integrations:Write": true}, nil)
		}, true, true},
		{"custom role, another module's grant", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{}, nil, map[string]bool{"k8s:Read": true}, map[string]map[string]bool{"account-A": {"k8s:Read": true}})
		}, false, false},
		{"custom role, grant but no action key", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, "", []string{}, nil, map[string]bool{key: true}, nil)
		}, false, false},
		{"built-in account_admin_readonly", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{"account_admin_readonly"}, map[string][]string{"account_admin_readonly": {"account-A"}}, nil, nil)
		}, true, false},
		{"built-in tenant_admin", func(t *testing.T) *security.RequestContext {
			return grantCaller(t, key, []string{"tenant_admin"}, nil, nil, nil)
		}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/diagnose", func(t *testing.T) {
			mock := stopAtDatabase(t)
			if tc.diag {
				mock.ExpectQuery("FROM integrations").WillReturnError(errPastAuthorization)
			}
			err := DiagnoseIntegrationConnectionForAccount(tc.ctx(t), "integration-1", "account-A")
			if tc.diag {
				assert.ErrorIs(t, err, errPastAuthorization)
			} else {
				assert.EqualError(t, err, "integrations: connection test is not permitted")
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
		t.Run(tc.name+"/check llm defaults", func(t *testing.T) {
			mock := stopAtDatabase(t)
			if tc.llm {
				mock.ExpectQuery("FROM integrations i").WillReturnError(errPastAuthorization)
			}
			_, err := CheckLLMChange(tc.ctx(t), LLMDefaultsChange{Op: "delete", IntegrationId: "integration-1"})
			if tc.llm {
				assert.ErrorIs(t, err, errPastAuthorization)
			} else {
				assert.ErrorIs(t, err, ErrLLMCheckNotPermitted)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
