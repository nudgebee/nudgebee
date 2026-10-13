package user

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
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

// beeUsernameArg matches a generated bee username, bee:<canonical name>.<random>:
// not an email address, so no identity provider or mailer can target it.
type beeUsernameArg struct{ name string }

func (m beeUsernameArg) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && regexp.MustCompile(`^bee:`+m.name+`\.[0-9a-f]{8}$`).MatchString(s)
}

func TestCreateBeeUser_CreatesAnActiveBeeInTheTenantWithNoInvite(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO users \(display_name, status, username, kind, created_by, updated_by\)\s+VALUES \(\$1, 'active', \$2, \$3, \$4, \$4\)`).
		WithArgs("Bad Rollout Bee", beeUsernameArg{"bad_rollout"}, models.UserKindBee, "bbbbbbbb-0000-0000-0000-000000000009").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("cccccccc-0000-0000-0000-000000000001"))
	mock.ExpectExec(`INSERT INTO tenant_users`).
		WithArgs("cccccccc-0000-0000-0000-000000000001", beeTenant, "bbbbbbbb-0000-0000-0000-000000000009").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := CreateBeeUser(tenantAdmin(t), BeeCreateRequest{Name: "bad_rollout", DisplayName: "  Bad Rollout Bee "})
	require.NoError(t, err)
	assert.Equal(t, "cccccccc-0000-0000-0000-000000000001", resp.Id)
	assert.Equal(t, "bad_rollout", resp.Name)
	assert.Equal(t, "Bad Rollout Bee", resp.DisplayName)
	assert.True(t, beeUsernameArg{"bad_rollout"}.Match(resp.Username), resp.Username)
	// No user_roles row and no invitation: the only writes are the two above.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateBeeUser_RejectsNamesOutsideTheCanonicalForm(t *testing.T) {
	mock := mockMetastore(t)
	for _, name := range []string{"Bad_rollout", "ab", "bad-rollout", "bad.rollout", "1bad", "bad rollout", strings.Repeat("a", 41)} {
		_, err := CreateBeeUser(tenantAdmin(t), BeeCreateRequest{Name: name, DisplayName: "x"})
		assert.Error(t, err, name)
	}
	_, err := CreateBeeUser(tenantAdmin(t), BeeCreateRequest{Name: "bad_rollout", DisplayName: "   "})
	assert.Error(t, err, "a blank display name")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing is written for a rejected name")
}

func TestCreateBeeUser_RequiresATenantAdmin(t *testing.T) {
	mock := mockMetastore(t)
	for _, wire := range []string{
		// A users:Write grant can invite people, but a bee acts with roles given
		// later, so creating one is as sensitive as assigning a tenant role.
		`{"TenantId":"` + beeTenant + `","UserId":"u1","Roles":[],"CustomPermissions":{"users:Write":true}}`,
		`{"TenantId":"` + beeTenant + `","UserId":"u1","Roles":["tenant_admin_readonly"]}`,
		`{"TenantId":"","UserId":"u1","Roles":["tenant_admin"]}`,
	} {
		_, err := CreateBeeUser(callerWith(t, wire), BeeCreateRequest{Name: "bad_rollout", DisplayName: "Bad Rollout Bee"})
		var ce common.Error
		require.ErrorAs(t, err, &ce, wire)
		assert.Equal(t, 403, ce.Code, wire)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
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

	_, err := CreateUserAuth(tenantAdmin(t), UserCreateAuthRequest{
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
