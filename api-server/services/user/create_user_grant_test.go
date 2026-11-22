package user

import (
	"context"
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

// users_create decides who may do what before its first write. A users:Write
// holder may create a user and add them to the tenant; asking for a tenant role
// is refused before any row is written; a tenant admin is unchanged.
//
// An admitted call is driven as far as the tenant_users insert — past the point
// that used to refuse a grant holder after the global users row was already
// written — and stopped there with a sentinel error, so no background audit or
// invitation work is started.
func TestCreateUserDecidesAuthorizationBeforeWriting(t *testing.T) {
	const tenantId, newUserId = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	stop := errors.New("stop after the tenant membership insert")

	cases := []struct {
		name     string
		wire     string
		role     string
		admitted bool
	}{
		{"users:Write creates a user with no tenant role", `{"Roles":[],"CustomPermissions":{"users:Write":true}}`, "", true},
		{"users:Write asking for tenant_admin", `{"Roles":[],"CustomPermissions":{"users:Write":true}}`, security.AUTH_TENANT_ADMIN_ROLE, false},
		{"users:Write asking for tenant_admin_readonly", `{"Roles":[],"CustomPermissions":{"users:Write":true}}`, security.AUTH_TENANT_READ_ADMIN_ROLE, false},
		{"users:Read is not enough", `{"Roles":[],"CustomPermissions":{"users:Read":true}}`, "", false},
		{"another module's grant", `{"Roles":[],"CustomPermissions":{"ownership:Write":true}}`, "", false},
		{"tenant_admin_readonly alone", `{"Roles":["tenant_admin_readonly"]}`, "", false},
		{"tenant admin with no role", `{"Roles":["tenant_admin"]}`, "", true},
		{"tenant admin assigning tenant_admin", `{"Roles":["tenant_admin"]}`, security.AUTH_TENANT_ADMIN_ROLE, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
				return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
			})
			t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

			if tc.admitted {
				// The username already exists, so no users row (and no audit) is written.
				mock.ExpectQuery(`INSERT INTO users`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
				mock.ExpectQuery(`SELECT id, username, display_name, status, kind FROM users`).
					WillReturnRows(sqlmock.NewRows([]string{"id", "username", "display_name", "status", "kind"}).
						AddRow(newUserId, "new@example.com", "New Person", "inactive", "human"))
				mock.ExpectExec(`INSERT INTO tenant_users`).WillReturnError(stop)
			}

			wire := tc.wire[:len(tc.wire)-1] + `,"TenantId":"` + tenantId + `","UserId":"33333333-3333-3333-3333-333333333333"}`
			ctx := security.NewRequestContext(context.Background(), contextFor(t, wire), slog.Default(), nil, nil)
			ctx.SetActionPermission(security.ActionKey("users_create", ""))

			_, err = CreateUser(ctx, UserCreateRequest{Username: "new@example.com", Firstname: "New", Lastname: "Person", Role: tc.role})
			if tc.admitted {
				assert.ErrorIs(t, err, stop, "admitted: reaches the tenant membership insert")
			} else {
				var e common.Error
				require.True(t, errors.As(err, &e), "refused with an authorization error, got %v", err)
				assert.Equal(t, 403, e.Code)
			}
			assert.NoError(t, mock.ExpectationsWereMet(), "refused callers write nothing; admitted callers reach the insert")
		})
	}
}
