package runtime

import (
	"nudgebee/llm/common"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetImageSupportCatalog_OmitsNullRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectQuery("supports_image_input").WillReturnRows(
		sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("anthropic", "claude-2.1", false).
			AddRow("openai", "gpt-4o", true))

	catalog, err := dao.GetImageSupportCatalog("")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{
		"anthropic:claude-2.1": false,
		"openai:gpt-4o":        true,
	}, catalog)
}

// A tenant's catalog is its own rows only; the built-in query is unchanged.
func TestGetImageSupportCatalog_TenantReadsOnlyThatTenantsRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectQuery(`tenant_id = \$1`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"provider_name", "model_name", "supports_image_input"}).
			AddRow("custom", "Qwen/Qwen3.6-35B-A3B-FP8", true))

	catalog, err := dao.GetImageSupportCatalog("tenant-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"custom:Qwen/Qwen3.6-35B-A3B-FP8": true}, catalog)
	require.NoError(t, mock.ExpectationsWereMet())
}
