package user

import (
	"errors"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suspendTenant = "aaaaaaaa-0000-0000-0000-00000000005a"
	suspendTarget = "cccccccc-0000-0000-0000-00000000005c"
	suspendCaller = "bbbbbbbb-0000-0000-0000-00000000005b"
)

// A users:Write grant holder: user administration, but not a tenant admin.
const usersGrantWire = `{"TenantId":"` + suspendTenant + `","UserId":"` + suspendCaller + `","CustomPermissions":{"users:Write":true}}`
const suspendAdminWire = `{"TenantId":"` + suspendTenant + `","UserId":"` + suspendCaller + `","Roles":["tenant_admin"]}`

const (
	holdsTenantAdminQuery = `SELECT EXISTS`
	tenantStatusUpdate    = `UPDATE users SET status = \$1 WHERE id = \$2 AND id IN`
)

type revocation struct {
	userId    string
	expiresAt time.Time
}

// recordRevocations replaces the denylist write for one test and returns what it
// was asked to revoke. fail makes every write fail.
func recordRevocations(t *testing.T, fail error) *[]revocation {
	t.Helper()
	got := &[]revocation{}
	orig := revokeUserSessions
	revokeUserSessions = func(_ *security.RequestContext, userId string, expiresAt time.Time) error {
		*got = append(*got, revocation{userId, expiresAt})
		return fail
	}
	t.Cleanup(func() { revokeUserSessions = orig })
	return got
}

// The revocation must outlive any session issued before it, not just the next minute.
func requireRevokedLongTerm(t *testing.T, got []revocation, userId string) {
	t.Helper()
	require.Len(t, got, 1)
	assert.Equal(t, userId, got[0].userId)
	assert.True(t, got[0].expiresAt.After(time.Now().Add(300*24*time.Hour)), "expires at %s", got[0].expiresAt)
}

func requireForbidden(t *testing.T, err error) {
	t.Helper()
	var cerr common.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, 403, cerr.Code)
}

func expectHoldsTenantAdmin(mock sqlmock.Sqlmock, holds bool) {
	mock.ExpectQuery(holdsTenantAdminQuery).WithArgs(suspendTenant, suspendTarget, "tenant_admin").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(holds))
}

func TestAUsersGrantCannotSuspendATenantAdmin(t *testing.T) {
	revoked := recordRevocations(t, nil)
	mock := mockMetastore(t)
	expectHoldsTenantAdmin(mock, true)

	_, err := UpdateUserStatus(callerWith(t, usersGrantWire), UserUpdateStatusRequest{UserId: suspendTarget, Status: "suspended"})
	requireForbidden(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "the status is not written")
	assert.Empty(t, *revoked)
}

func TestSuspendingAUserEndsTheirSessions(t *testing.T) {
	t.Run("by a users grant, of someone who is not a tenant admin", func(t *testing.T) {
		revoked := recordRevocations(t, nil)
		mock := mockMetastore(t)
		expectHoldsTenantAdmin(mock, false)
		mock.ExpectExec(tenantStatusUpdate).WithArgs("suspended", suspendTarget, suspendTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))

		_, err := UpdateUserStatus(callerWith(t, usersGrantWire), UserUpdateStatusRequest{UserId: suspendTarget, Status: "suspended"})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
		requireRevokedLongTerm(t, *revoked, suspendTarget)
	})

	t.Run("by a tenant admin", func(t *testing.T) {
		revoked := recordRevocations(t, nil)
		mock := mockMetastore(t)
		mock.ExpectExec(tenantStatusUpdate).WithArgs("suspended", suspendTarget, suspendTenant).
			WillReturnResult(sqlmock.NewResult(0, 1))

		_, err := UpdateUserStatus(callerWith(t, suspendAdminWire), UserUpdateStatusRequest{UserId: suspendTarget, Status: "suspended"})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
		requireRevokedLongTerm(t, *revoked, suspendTarget)
	})
}

func TestActivatingAUserKeepsTheirSessions(t *testing.T) {
	revoked := recordRevocations(t, nil)
	mock := mockMetastore(t)
	mock.ExpectExec(tenantStatusUpdate).WithArgs("active", suspendTarget, suspendTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpdateUserStatus(callerWith(t, usersGrantWire), UserUpdateStatusRequest{UserId: suspendTarget, Status: "active"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, *revoked)
}

// The admin is told when the sessions survived, rather than believing the user
// was cut off.
func TestASuspensionWhoseSessionsSurviveIsReported(t *testing.T) {
	recordRevocations(t, errors.New("connection reset"))
	mock := mockMetastore(t)
	mock.ExpectExec(tenantStatusUpdate).WithArgs("suspended", suspendTarget, suspendTenant).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := UpdateUserStatus(callerWith(t, suspendAdminWire), UserUpdateStatusRequest{UserId: suspendTarget, Status: "suspended"})
	var cerr common.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, 500, cerr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func expectTenantUserLookup(mock sqlmock.Sqlmock, roles ...string) {
	mock.ExpectQuery(`FROM users u\s+JOIN tenant_users`).WithArgs(suspendTenant, "target@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(suspendTarget))
	rows := sqlmock.NewRows([]string{"role"})
	for _, r := range roles {
		rows.AddRow(r)
	}
	mock.ExpectQuery(`SELECT role FROM user_roles`).WithArgs(suspendTenant, suspendTarget).WillReturnRows(rows)
}

func TestTheProfileEditorFollowsTheSameSuspensionRules(t *testing.T) {
	t.Run("a users grant cannot suspend a tenant admin", func(t *testing.T) {
		revoked := recordRevocations(t, nil)
		mock := mockMetastore(t)
		expectTenantUserLookup(mock, "tenant_admin")
		expectHoldsTenantAdmin(mock, true)

		_, err := UpdateUserProfile(callerWith(t, usersGrantWire), UserUpdateProfileRequest{Username: "target@example.com", DisplayName: "T", Status: "suspended"})
		requireForbidden(t, err)
		assert.NoError(t, mock.ExpectationsWereMet(), "the profile is not written")
		assert.Empty(t, *revoked)
	})

	t.Run("a tenant admin's suspension ends the user's sessions", func(t *testing.T) {
		revoked := recordRevocations(t, nil)
		mock := mockMetastore(t)
		expectTenantUserLookup(mock)
		mock.ExpectExec(`UPDATE users SET display_name = \$1, status = \$2 WHERE id = \$3`).
			WithArgs("T", "suspended", suspendTarget).WillReturnResult(sqlmock.NewResult(0, 1))

		_, err := UpdateUserProfile(callerWith(t, suspendAdminWire), UserUpdateProfileRequest{Username: "target@example.com", DisplayName: "T", Status: "suspended"})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
		requireRevokedLongTerm(t, *revoked, suspendTarget)
	})
}
