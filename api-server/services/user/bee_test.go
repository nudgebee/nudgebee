package user

import (
	"context"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const beeTenant = "aaaaaaaa-0000-0000-0000-000000000001"

func callerWith(t *testing.T, wire string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

func tenantAdmin(t *testing.T) *security.RequestContext {
	return callerWith(t, `{"TenantId":"`+beeTenant+`","UserId":"bbbbbbbb-0000-0000-0000-000000000009","Roles":["tenant_admin"]}`)
}

func TestInvitingABeeUsernameIsRefused(t *testing.T) {
	mock := mockMetastore(t)
	// A bee's username is not an email address, so an invitation is refused
	// before anything is written.
	_, err := CreateUser(tenantAdmin(t), UserCreateRequest{Username: "bee:bad_rollout.0a1b2c3d", Firstname: "x"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnboardingAnExistingBeeIsRefused(t *testing.T) {
	mock := mockMetastore(t)
	// The username already exists, so the insert returns nothing and the row is read back.
	mock.ExpectQuery(`INSERT INTO users`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`SELECT id, username, display_name, status, kind FROM users WHERE username = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "display_name", "status", "kind"}).
			AddRow("cccccccc-0000-0000-0000-000000000001", "bee:bad_rollout.0a1b2c3d", "Bad Rollout Bee", "active", models.UserKindBee))

	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	_, _, err = createOrGetUser(dbms, "x", "active", "bee:bad_rollout.0a1b2c3d", nil)
	assert.ErrorIs(t, err, ErrBeeAccount)
	assert.NoError(t, mock.ExpectationsWereMet(), "no tenant row, role or invitation follows")
}

func TestABeeCannotMintAnAPIKey(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`FROM users WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "kind"}).AddRow(storedUser, "active", models.UserKindBee))

	_, err := CreateUserAuthToken(keyOwner(t), UserTokenCreateRequest{Name: "ci"})
	assert.ErrorIs(t, err, ErrBeeAccount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestABeeCannotBeGivenALogin(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`SELECT kind FROM users WHERE id = \$1`).WithArgs("cccccccc-0000-0000-0000-000000000001").
		WillReturnRows(sqlmock.NewRows([]string{"kind"}).AddRow(models.UserKindBee))

	// The server is the only caller that may add a login to another user (#43649).
	_, err := CreateUserAuth(serverInternal(t), UserCreateAuthRequest{
		UserId: "cccccccc-0000-0000-0000-000000000001", Provider: "google", ProviderType: "oauth", AccountId: "1", Name: "google", Status: "active",
	})
	assert.ErrorIs(t, err, ErrBeeAccount)
	assert.NoError(t, mock.ExpectationsWereMet(), "no user_auths row is written")
}

func TestReportRecipientsLeaveBeesOut(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`WHERE u.status = 'active'\s+AND u.kind = 'person'`).WithArgs(beeTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "display_name", "status", "kind"}))

	_, err := GetUserByTenant(tenantAdmin(t), beeTenant)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
