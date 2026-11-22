package account

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

// cloud_account_attrs_upsert writes settings of the accounts it names: it used
// to check only that they were the caller's tenant's, so a read-only admin of an
// account could change them. It now needs write access to each.
func TestUpsertAccountAttrsRequiresAccountWriteAccess(t *testing.T) {
	db, mock, err := sqlmock.New() // no expectations: a refusal must come before any query
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	const acct = "11111111-1111-1111-1111-111111111111"
	as := func(role string) *security.RequestContext {
		raw := `{"TenantId":"tenant-1","UserId":"u1","AccountIds":["` + acct + `"],"Roles":["` + role + `"],"ScopedEntityIds":{"` + role + `":["` + acct + `"]}}`
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal([]byte(raw), &sc))
		return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	}
	req := AccountAttrUpsertRequest{Objects: []AccountAttr{{CloudAccountId: acct, Name: "owner_team", Value: "payments"}}}
	refused := func(err error) bool {
		var e common.Error
		return errors.As(err, &e) && e.Code == 403
	}

	_, err = UpsertAccountAttrs(as("account_admin_readonly"), req)
	assert.True(t, refused(err), "read-only admin: %v", err)
	assert.NoError(t, mock.ExpectationsWereMet())

	_, err = UpsertAccountAttrs(as("account_admin"), req)
	assert.False(t, refused(err), "account admin passes the check: %v", err)
}
