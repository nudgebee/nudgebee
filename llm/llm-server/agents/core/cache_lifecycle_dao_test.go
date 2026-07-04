package core

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetCacheLifecycleInvalidated_StopsTtlBilling verifies the UPDATE that the
// content_changed cache-replacement path relies on: it must set invalidated_at
// only on rows still alive (invalidated_at IS NULL), guarded so an already-dead
// cache keeps its earlier death time. This is the DB half of the fix that stops
// a superseded cache from billing storage for its full planned TTL.
func TestSetCacheLifecycleInvalidated_StopsTtlBilling(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// The UPDATE must target the row by cache_name AND only touch live rows.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE llm_cache_lifecycle")).
		WithArgs("cachedContents/abc123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = dao.SetCacheLifecycleInvalidated(context.Background(), "cachedContents/abc123")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())

	// Guard rails baked into the query: scope it to one row and never resurrect
	// an already-invalidated cache.
	assert.Contains(t, setCacheLifecycleInvalidatedSQL, "invalidated_at IS NULL",
		"must only invalidate live rows so a re-delete can't move the death time later")
	assert.Contains(t, setCacheLifecycleInvalidatedSQL, "WHERE cache_name = $1",
		"must invalidate exactly the named cache, not a whole slot")
}

// TestSetCacheLifecycleInvalidated_EmptyName rejects an empty cache name before
// touching the DB — a blank name would otherwise match the WHERE on nothing but
// still issue a pointless statement.
func TestSetCacheLifecycleInvalidated_EmptyName(t *testing.T) {
	dao := &ConversationDao{}
	err := dao.SetCacheLifecycleInvalidated(context.Background(), "   ")
	require.Error(t, err)
}

// TestSetCacheLifecycleInvalidated_DBErrorWrapped ensures DB failures are wrapped
// (recordCacheLifecycleInvalidation logs but never propagates, so the wrap is the
// only signal an operator gets).
func TestSetCacheLifecycleInvalidated_DBErrorWrapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE llm_cache_lifecycle")).
		WithArgs("cachedContents/xyz").
		WillReturnError(errors.New("connection reset"))

	err = dao.SetCacheLifecycleInvalidated(context.Background(), "cachedContents/xyz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SetCacheLifecycleInvalidated")
}
