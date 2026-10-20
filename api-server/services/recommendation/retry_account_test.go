package recommendation

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retry is authorized for query.AccountId but looks the resolution up by id
// alone, so it must refuse a resolution whose recommendation is on another
// account before anything is applied.
func TestRetryRecommendationResolutionRefusesAnotherAccountsResolution(t *testing.T) {
	const (
		tenant         = "tenant-1"
		ownAccount     = "11111111-1111-1111-1111-111111111111"
		foreignAccount = "33333333-3333-3333-3333-333333333333"
	)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return manager, nil })
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	mock.ExpectQuery("FROM recommendation_resolution WHERE id = \\$1").
		WithArgs("res-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "recommendation_id", "status"}).AddRow("res-1", "rec-1", "Failed"))
	mock.ExpectQuery("FROM recommendation r").
		WithArgs("rec-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "cloud_account_id"}).AddRow("rec-1", tenant, foreignAccount))

	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"`+tenant+`","UserId":"user-1","AccountIds":["`+ownAccount+`"],"Roles":["tenant_admin"]}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

	_, err = RetryRecommendationResolution(ctx, RetryRecommendationResolutionRequest{AccountId: ownAccount, ResolutionId: "res-1"})

	assert.EqualError(t, err, "resolution not found: res-1")
	assert.NoError(t, mock.ExpectationsWereMet(), "no account loaded and nothing applied")
}
