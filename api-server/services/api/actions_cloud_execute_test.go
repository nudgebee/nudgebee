package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A command run on one account must not settle a recommendation of another:
// the handler updates the linked recommendation by id, so it refuses one the
// account does not own before recording or running anything.
func TestCloudExecuteCommandRefusesAnotherAccountsRecommendation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		account        = "11111111-1111-1111-1111-111111111111"
		recommendation = "77777777-7777-7777-7777-777777777777" // belongs to another account
	)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return manager, nil })
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	mock.ExpectQuery("SELECT account_access FROM cloud_accounts").
		WillReturnRows(sqlmock.NewRows([]string{"account_access"}).AddRow("readwrite"))
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM recommendation WHERE id = \\$1 AND cloud_account_id = \\$2 AND tenant_id = \\$3\\)").
		WithArgs(recommendation, account, "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+account+`"],"Roles":["tenant_admin"]}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handleCloudExecuteCommand(&ActionRequest{Input: map[string]any{
		"account_id":        account,
		"commands":          []any{"aws ec2 delete-volume --volume-id vol-1"},
		"recommendation_id": recommendation,
	}}, c, ctx)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet(), "no resolution recorded and no recommendation updated")
}
