package core

import (
	"errors"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetImageSupportCache clears the package-level cache singleton so each
// test starts from a cold, stale state regardless of test execution order.
func resetImageSupportCache(t *testing.T) {
	t.Helper()
	imageSupport.mu.Lock()
	imageSupport.values = nil
	imageSupport.loadedAt = time.Time{}
	imageSupport.mu.Unlock()
}

func TestGetImageSupportCatalog_OmitsNullRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("anthropic", "claude-2.1", false).
			AddRow("openai", "gpt-4o", true))

	catalog, err := dao.GetImageSupportCatalog()
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{
		"anthropic:claude-2.1": false,
		"openai:gpt-4o":        true,
	}, catalog)
}

func TestIsVisionCapableModel_DBVerdictFalseIsRespected(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	SetConversationDao(dao)

	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("bedrock", "some-custom-model", false))

	assert.False(t, IsVisionCapableModel("bedrock", "some-custom-model"))
}

func TestIsVisionCapableModel_DBVerdictTrueIsRespected(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	SetConversationDao(dao)

	// "some-custom-model" isn't caught by any regex pattern. Under
	// default-deny it would otherwise be treated as non-vision-capable — an
	// explicit true row is the only way to make it vision-capable.
	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("bedrock", "some-custom-model", true))

	assert.True(t, IsVisionCapableModel("bedrock", "some-custom-model"))
}

func TestIsVisionCapableModel_RegexDenyWinsOverDBTrue(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	SetConversationDao(dao)

	// Even if the DB explicitly says true, the regex deny-list still wins —
	// it runs first and returns before the DB lookup happens.
	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("openai", "gpt-3.5-turbo", true))

	assert.False(t, IsVisionCapableModel("openai", "gpt-3.5-turbo"))
}

func TestIsVisionCapableModel_UnknownDefaultsToNotVisionCapable(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	SetConversationDao(dao)

	// Catalog loads successfully but has no row for this model — still
	// unknown. Default-deny: no positive DB record means not vision-capable.
	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}))

	assert.False(t, IsVisionCapableModel("openai", "some-unlisted-model"))
}

func TestImageSupportCache_QueryErrorKeepsPreviousSnapshotAndDoesNotBackOff(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	SetConversationDao(dao)

	// First load succeeds and primes the cache.
	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("openai", "gpt-4o", true))
	supported, known := imageSupport.lookup("openai", "gpt-4o")
	require.True(t, known)
	require.True(t, supported)

	// Force staleness; the second refresh attempt fails.
	imageSupport.mu.Lock()
	imageSupport.loadedAt = time.Time{}
	imageSupport.mu.Unlock()
	mock.ExpectQuery("supports_image_input").WillReturnError(errors.New("connection reset"))

	supported, known = imageSupport.lookup("openai", "gpt-4o")
	assert.True(t, known, "previous snapshot must survive a failed refresh")
	assert.True(t, supported)

	// loadedAt must NOT be stamped on failure — an outage (or DAO not yet
	// established) must not be cached as a 5-minute-long "known empty"
	// catalog. The next lookup should retry immediately rather than back off.
	imageSupport.mu.RLock()
	loadedAt := imageSupport.loadedAt
	imageSupport.mu.RUnlock()
	assert.True(t, loadedAt.IsZero(), "a failed refresh must not update loadedAt")
}

func TestImageSupportCache_NilDAODoesNotDisableCacheForTTLWindow(t *testing.T) {
	original := PeekConversationDao()
	defer SetConversationDao(original)
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	SetConversationDao(nil)

	// With no DAO established (e.g. at process startup), a lookup must not
	// cache "unknown" for the full TTL — it must retry as soon as the DAO
	// becomes available, not 5 minutes later.
	_, known := imageSupport.lookup("openai", "gpt-4o")
	assert.False(t, known)

	imageSupport.mu.RLock()
	loadedAt := imageSupport.loadedAt
	imageSupport.mu.RUnlock()
	assert.True(t, loadedAt.IsZero(), "a nil DAO must not stamp loadedAt")
}
