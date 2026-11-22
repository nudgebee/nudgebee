package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// TestScanImageHonoursCustomRoleGrants: a caller whose only right on the
// account is a custom-role grant of the action (an automation account, or a person with
// a custom role) may start a scan; the same caller without that grant, or with
// it on another account, may not. Built-in roles keep working.
func TestScanImageHonoursCustomRoleGrants(t *testing.T) {
	const own, other = "acc-own", "acc-other"
	// No query is expected: past the check, the handler's first query fails
	// here, which tells an admitted call from a refused one without a database.
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return manager, nil })
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	ctxFor := func(t *testing.T, scJSON, permission string) *security.RequestContext {
		t.Helper()
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal([]byte(scJSON), &sc))
		rc := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		rc.SetActionPermission(permission)
		return rc
	}
	scoped := func(account, grant string) string {
		return `{"TenantId":"t1","UserId":"u1","AccountIds":["` + own + `","` + other + `"],
			"ScopedCustomPermissions":{"` + account + `":{"` + grant + `":true}}}`
	}
	accountAdmin := `{"TenantId":"t1","UserId":"u1","AccountIds":["` + own + `"],"Roles":["account_admin"],
		"ScopedEntityIds":{"account_admin":["` + own + `"]}}`

	cases := []struct {
		name, sc, permission string
		allowed              bool
	}{
		{"custom-role grant on the account", scoped(own, "security:Execute"), "security:Execute", true},
		{"custom-role grant, call carries no grant key", scoped(own, "security:Execute"), "", false},
		{"custom-role grant on another account", scoped(other, "security:Execute"), "security:Execute", false},
		{"custom-role read grant only", scoped(own, "security:Read"), "security:Execute", false},
		{"built-in account admin", accountAdmin, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ScanImage(ctxFor(t, tc.sc, tc.permission), RecommendationScanImageRequest{AccountId: own, Namespace: "default", Workload: "web"})
			var apiErr common.Error
			refused := errors.As(err, &apiErr) && apiErr.Code == common.ErrorUnauthorized("").Code
			if tc.allowed {
				require.Error(t, err, "the handler's first query is not mocked")
				assert.False(t, refused, "refused: %v", err)
			} else {
				assert.True(t, refused, "want unauthorized, got %v", err)
			}
		})
	}
}
