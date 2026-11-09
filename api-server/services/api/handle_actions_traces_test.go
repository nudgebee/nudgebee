package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/observability"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bare span array can carry neither the empty-result diagnosis nor the executed query and
// provider, so either flag has to select the envelope. Gating on ValidateRequest alone made
// IncludeExecutedQuery silently useless over HTTP whenever validation was disabled — an empty
// trace result would go back to being unattributable (#37406).
func TestWantsTracesEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		request  observability.TracesV3Request
		expected bool
	}{
		{
			name:     "neither flag keeps the bare span array",
			request:  observability.TracesV3Request{},
			expected: false,
		},
		{
			name:     "validation alone needs the envelope for the suggestion",
			request:  observability.TracesV3Request{ValidateRequest: true},
			expected: true,
		},
		{
			name:     "the executed query alone needs the envelope",
			request:  observability.TracesV3Request{IncludeExecutedQuery: true},
			expected: true,
		},
		{
			name:     "both flags together",
			request:  observability.TracesV3Request{ValidateRequest: true, IncludeExecutedQuery: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, wantsTracesEnvelope(tt.request))
		})
	}
}

// traces_grouping_v3 and traces_get_query took account_id straight from the request and
// resolved its trace provider, credentials included, without asking whether the caller
// may read that account. Every other traces action checked; these two answered for any
// account id, including another tenant's.
func TestTracesActionsRequireAccountAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		tenant         = "tenant-1"
		ownAccount     = "11111111-1111-1111-1111-111111111111"
		foreignAccount = "33333333-3333-3333-3333-333333333333"
	)

	for _, action := range []string{"traces_grouping_v3", "traces_get_query"} {
		t.Run(action, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
			database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
				return manager, nil
			})
			t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

			// The tenant-admin context lists the tenant's accounts; nothing else may run.
			mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
				WithArgs(tenant).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(ownAccount))

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/rpc/traces", nil)
			c.Request.Header.Set("x-tenant-id", tenant)

			handleTracesAction(&ActionRequest{
				Action: ActionRequestAction{Name: action},
				Input:  map[string]any{"request": map[string]any{"account_id": foreignAccount}},
			}, c, nil, nil, slog.Default())

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "handler queried beyond the access check")
		})
	}
}
