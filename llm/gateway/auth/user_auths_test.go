package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sync"
	"testing"
	"time"

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
	var recordedHash string
	v.recordAccess = func(db *common.DatabaseManager, hash string) {
		recordedHash = hash
	}
	raw := "abcdef0123456789" // stand-in raw PAT
	mock.ExpectQuery("FROM user_auths").
		WithArgs(sha256hex(raw)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user", "name", "api_key_id"}).
			AddRow("tenant-1", "user-1", "my-cli-token", "token-key-uuid"))

	id, ok := v.Validate(raw)
	require.True(t, ok)
	assert.Equal(t, "tenant-1", id.TenantID)
	assert.Equal(t, "user-1", id.UserID)
	assert.Equal(t, "my-cli-token", id.TokenID)
	assert.Equal(t, "token-key-uuid", id.APIKeyID)
	assert.Equal(t, sha256hex(raw), recordedHash)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The key's policy is read in the same lookup that authenticates it, so a policy edit
// binds on the next request. The query is pinned: each array to its own dimension, and the
// rows to the key and its owner alone, since a tenant predicate would drop the policy once
// the owner switches tenant. A dimension with no rows (a NULL aggregate) stays unrestricted;
// TestUserAuthsValidator_KeyPolicy_DB runs the same lookup on Postgres.
func TestUserAuthsValidator_ReadsKeyPolicy(t *testing.T) {
	v, mock := mockValidator(t)
	v.recordAccess = func(*common.DatabaseManager, string) {}
	raw := "restricted-key"
	mock.ExpectQuery(`array_agg\(p\.value\) FILTER \(WHERE p\.dimension = 'provider'\) AS providers,\s+` +
		`array_agg\(p\.value\) FILTER \(WHERE p\.dimension = 'model'\) AS models,\s+` +
		`array_agg\(p\.value\) FILTER \(WHERE p\.dimension = 'endpoint'\) AS endpoints\s+` +
		`FROM llm_gateway_access_policies p\s+` +
		`WHERE p\.api_key_id = ua\.id::text AND p\.user_id = ua\."user"::text\s+\) pol ON true`).
		WithArgs(sha256hex(raw)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user", "name", "api_key_id", "providers", "models", "endpoints"}).
			AddRow("tenant-1", "user-1", "ci", "key-1", nil, "{gpt-4o-mini,anthropic/claude-haiku-4-5}", "{chat}"))

	id, ok := v.Validate(raw)
	require.True(t, ok)
	assert.Nil(t, id.Policy.Providers)
	assert.Equal(t, []string{"gpt-4o-mini", "anthropic/claude-haiku-4-5"}, id.Policy.Models)
	assert.Equal(t, []string{"chat"}, id.Policy.Endpoints)
	assert.True(t, id.Policy.Restricted())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserAuthsValidator_RecordTokenAccessQuery(t *testing.T) {
	v, mock := mockValidator(t)
	raw := "token-for-access-check"
	hash := sha256hex(raw)

	mock.ExpectExec("UPDATE user_auths").
		WithArgs(hash).
		WillReturnResult(sqlmock.NewResult(1, 1))

	db, err := v.db()
	require.NoError(t, err)

	updateQuery := `
		UPDATE user_auths
		SET accessed_at = now()
		WHERE ` + tokenHashColumn + ` = $1
		  AND (accessed_at IS NULL OR accessed_at < now() - interval '1 minute')`
	_, err = db.Db.Exec(updateQuery, hash)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserAuthsValidator_RecordTokenAccess_InMemoryThrottling(t *testing.T) {
	v, mock := mockValidator(t)
	var wg sync.WaitGroup
	v.wg = &wg
	raw := "throttled-token"
	hash := sha256hex(raw)

	// First access executes update query
	mock.ExpectExec("UPDATE user_auths").
		WithArgs(hash).
		WillReturnResult(sqlmock.NewResult(1, 1))

	db, err := v.db()
	require.NoError(t, err)

	v.recordTokenAccess(db, hash)

	// Second access within 1 minute should be throttled in-memory (no DB query expected)
	v.recordTokenAccess(db, hash)

	wg.Wait()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserAuthsValidator_RecordTokenAccess_Sweeping(t *testing.T) {
	v, mock := mockValidator(t)
	var wg sync.WaitGroup
	v.wg = &wg
	v.lastUpdated = make(map[string]time.Time)
	now := time.Now()
	// Populate 1005 entries older than 1 minute
	for i := 0; i < 1005; i++ {
		v.lastUpdated[string(rune(i))] = now.Add(-2 * time.Minute)
	}
	v.lastSweep = now.Add(-6 * time.Minute)

	db, err := v.db()
	require.NoError(t, err)

	// Expect the update query for the new token we are about to record
	newHash := sha256hex("new-token")
	mock.ExpectExec("UPDATE user_auths").
		WithArgs(newHash).
		WillReturnResult(sqlmock.NewResult(1, 1))

	v.recordTokenAccess(db, newHash)

	wg.Wait()

	v.mu.Lock()
	remaining := len(v.lastUpdated)
	v.mu.Unlock()

	assert.Equal(t, 1, remaining)
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

func TestUserAuthsValidator_ReturnsAPIKeyID(t *testing.T) {
	v, mock := mockValidator(t)
	v.recordAccess = func(db *common.DatabaseManager, hash string) {}
	raw := "sk-nb-gw-abcdef0123456789"
	mock.ExpectQuery("FROM user_auths").
		WithArgs(sha256hex(raw)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user", "name", "api_key_id"}).
			AddRow("tenant-1", "user-1", "claude-code", "key-uuid-1"))

	id, ok := v.Validate(raw)
	require.True(t, ok)
	assert.Equal(t, "key-uuid-1", id.APIKeyID)
	assert.Equal(t, "claude-code", id.TokenID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserAuthsValidator_QueryFiltersGatewayKinds(t *testing.T) {
	v, mock := mockValidator(t)
	v.recordAccess = func(db *common.DatabaseManager, hash string) {}
	raw := "sk-nb-platform-only"
	// A platform key never matches: the WHERE clause admits only the two kinds that reach
	// the gateway, so the row set is empty. An all-access ('all') key is admitted by the same
	// clause — this asserts the clause itself, which is what keeps the two lists in step.
	mock.ExpectQuery(`kind IN \('gateway', 'all'\)`).
		WithArgs(sha256hex(raw)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user", "name", "api_key_id"}))

	_, ok := v.Validate(raw)
	assert.False(t, ok)
	assert.NoError(t, mock.ExpectationsWereMet())
}

type assertErr struct{}

func (assertErr) Error() string { return "db down" }
