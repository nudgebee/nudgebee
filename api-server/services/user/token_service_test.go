package user

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key as user_auths stores it. The caller's session carries the same ids in upper case,
// which Postgres matches, so every policy row below must take the stored form instead: the
// gateway matches rows on id::text and "user"::text.
const (
	storedKey    = "11111111-1111-1111-1111-111111111111"
	storedUser   = "bbbbbbbb-0000-0000-0000-000000000001"
	storedTenant = "aaaaaaaa-0000-0000-0000-000000000001"
)

// mockMetastore points the service at a sqlmock for one test. Expectations run in order.
func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { _ = db.Close() })
	return mock
}

// keyOwner is the key's owner, signed in with upper-case ids.
func keyOwner(t *testing.T) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(`{"TenantId":"`+strings.ToUpper(storedTenant)+
		`","UserId":"`+strings.ToUpper(storedUser)+`"}`), &sc))
	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

// fourChars matches the stored suffix: the token is random, so only its shape is known.
type fourChars struct{}

func (fourChars) Match(v driver.Value) bool { s, ok := v.(string); return ok && len(s) == 4 }

func storedKeyRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user", "tenant_id"}).AddRow(storedKey, storedUser, storedTenant)
}

const (
	deleteKeyPolicy = `DELETE FROM llm_gateway_access_policies WHERE api_key_id = \$1 AND user_id = \$2`
	insertKeyPolicy = `INSERT INTO llm_gateway_access_policies`
)

// A restricted key is created restricted: its policy rows are written in the transaction that
// inserts it, under the scope read back from the new row.
func TestCreateUserAuthToken_StoresThePolicyUnderTheStoredKey(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`FROM users WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(storedUser, "active"))
	mock.ExpectQuery(`SELECT id FROM user_auths WHERE name = \$1`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	anyArg := sqlmock.AnyArg()
	mock.ExpectQuery(`INSERT INTO user_auths [\s\S]*token_suffix\)[\s\S]* RETURNING id::text, "user"::text, tenant_id::text`).
		WithArgs(anyArg, anyArg, anyArg, anyArg, anyArg, anyArg, anyArg, anyArg, fourChars{}).
		WillReturnRows(storedKeyRow())
	mock.ExpectExec(deleteKeyPolicy).WithArgs(storedKey, storedUser).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insertKeyPolicy).WithArgs(storedTenant, storedUser, storedKey, "model", "gpt-4o-mini").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := CreateUserAuthToken(keyOwner(t), UserTokenCreateRequest{
		Name: "ci", Kind: KindGateway, GatewayPolicy: &GatewayPolicy{Models: []string{"gpt-4o-mini"}},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token) // shown once, to its creator
	assert.Equal(t, []string{"gpt-4o-mini"}, resp.GatewayPolicy.Models)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An edit locks the key's row before it touches the policy, so a concurrent edit waits for it
// rather than leaving the union of both lists.
func TestUpdateUserAuthToken_LocksTheKeyBeforeReplacingItsPolicy(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`SELECT kind, scopes, name FROM user_auths`).WillReturnRows(sqlmock.NewRows([]string{"kind", "scopes", "name"}).AddRow(KindGateway, nil, "ci"))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text, "user"::text, tenant_id::text FROM user_auths[\s\S]*FOR UPDATE`).
		WithArgs(strings.ToUpper(storedKey), strings.ToUpper(storedTenant), strings.ToUpper(storedUser)).
		WillReturnRows(storedKeyRow())
	mock.ExpectExec(deleteKeyPolicy).WithArgs(storedKey, storedUser).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(insertKeyPolicy).WithArgs(storedTenant, storedUser, storedKey, "endpoint", "chat").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := UpdateUserAuthToken(keyOwner(t), UserTokenUpdateRequest{
		Id: strings.ToUpper(storedKey), GatewayPolicy: &GatewayPolicy{Endpoints: []string{"chat"}},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An unscoped all key cannot take a gateway policy by edit: its platform side could mint an
// unrestricted key. Scoped, it can.
func TestUpdateUserAuthToken_AllKeyPolicyNeedsScopes(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`SELECT kind, scopes, name FROM user_auths`).WillReturnRows(sqlmock.NewRows([]string{"kind", "scopes", "name"}).AddRow(KindAll, nil, "ci"))

	_, err := UpdateUserAuthToken(keyOwner(t), UserTokenUpdateRequest{
		Id: storedKey, GatewayPolicy: &GatewayPolicy{Models: []string{"gpt-4o-mini"}},
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())

	mock = mockMetastore(t)
	mock.ExpectQuery(`SELECT kind, scopes, name FROM user_auths`).WillReturnRows(sqlmock.NewRows([]string{"kind", "scopes", "name"}).AddRow(KindAll, "{tickets:Read}", "ci"))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text, "user"::text, tenant_id::text FROM user_auths[\s\S]*FOR UPDATE`).WillReturnRows(storedKeyRow())
	mock.ExpectExec(deleteKeyPolicy).WithArgs(storedKey, storedUser).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insertKeyPolicy).WithArgs(storedTenant, storedUser, storedKey, "model", "gpt-4o-mini").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err = UpdateUserAuthToken(keyOwner(t), UserTokenUpdateRequest{
		Id: storedKey, GatewayPolicy: &GatewayPolicy{Models: []string{"gpt-4o-mini"}},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A revoked key leaves no policy rows behind.
func TestDeleteUserAuthToken_RemovesTheKeysPolicy(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`DELETE FROM user_auths [\s\S]* RETURNING id::text, "user"::text, tenant_id::text, name, kind`).
		WithArgs(storedKey, strings.ToUpper(storedTenant), strings.ToUpper(storedUser)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user", "tenant_id", "name", "kind"}).
			AddRow(storedKey, storedUser, storedTenant, "ci", KindGateway))
	mock.ExpectExec(`DELETE FROM llm_gateway_rate_limits WHERE api_key_id = \$1$`).WithArgs(storedKey).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(deleteKeyPolicy).WithArgs(storedKey, storedUser).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	_, err := DeleteUserAuthToken(keyOwner(t), UserTokenDeleteRequest{Id: storedKey})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
