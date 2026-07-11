package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	"nudgebee/llm-gateway/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockValidator(t *testing.T) (*userAuthsValidator, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "postgres")
	mgr := &common.DatabaseManager{Db: sqlxDB}
	return &userAuthsValidator{db: func() (*common.DatabaseManager, error) { return mgr, nil }}, mock
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestUserAuthsValidator_ValidToken(t *testing.T) {
	v, mock := mockValidator(t)
	raw := "abcdef0123456789" // stand-in raw PAT
	mock.ExpectQuery("FROM user_auths").
		WithArgs(sha256hex(raw)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user", "name"}).
			AddRow("tenant-1", "user-1", "my-cli-token"))

	id, ok := v.Validate(raw)
	require.True(t, ok)
	assert.Equal(t, "tenant-1", id.TenantID)
	assert.Equal(t, "user-1", id.UserID)
	assert.Equal(t, "my-cli-token", id.TokenID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserAuthsValidator_UnknownToken(t *testing.T) {
	v, mock := mockValidator(t)
	mock.ExpectQuery("FROM user_auths").WillReturnError(sql.ErrNoRows)
	_, ok := v.Validate("nope")
	assert.False(t, ok)
}

func TestUserAuthsValidator_EmptyToken(t *testing.T) {
	v, _ := mockValidator(t)
	_, ok := v.Validate("")
	assert.False(t, ok, "empty token never hits the DB")
}

func TestUserAuthsValidator_DBError_FailsClosed(t *testing.T) {
	v := &userAuthsValidator{db: func() (*common.DatabaseManager, error) {
		return nil, assertErr{}
	}}
	_, ok := v.Validate("x")
	assert.False(t, ok, "metastore unavailable → reject (fail closed)")
}

func TestUserAuthsValidator_RequiresToken(t *testing.T) {
	assert.True(t, (&userAuthsValidator{}).RequiresToken())
}

type assertErr struct{}

func (assertErr) Error() string { return "db down" }
