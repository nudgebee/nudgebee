package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unwrapped field conditions are now moved under _binary before decoding (see
// query.NormalizeQueryRequestWhere). What cannot be read as a clause — here the same field
// both inside and outside _binary — is refused by both observability handlers with a 400
// naming it, instead of reaching the decoder.
const (
	whereTestTenant  = "tenant-1"
	whereTestAccount = "11111111-1111-1111-1111-111111111111"
)

// newWhereTestContext returns a request context for whereTestTenant whose metastore lists
// whereTestAccount as the tenant's only account.
func newWhereTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
		WithArgs(whereTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(whereTestAccount))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/rpc/x", nil)
	c.Request.Header.Set("x-tenant-id", whereTestTenant)
	return c, rec
}

func TestObservabilityHandlersRefuseUnreadableWhere(t *testing.T) {
	request := map[string]any{
		"account_id": whereTestAccount,
		"query_request": map[string]any{"where": map[string]any{
			"_binary":   map[string]any{"namespace": map[string]any{"_eq": "prod"}},
			"namespace": map[string]any{"_eq": "dev"},
		}},
	}

	for name, handle := range map[string]func(*ActionRequest, *gin.Context){
		"logs_list": func(a *ActionRequest, c *gin.Context) { handleLogsAction(a, c, nil, nil, slog.Default()) },
		"traces_list": func(a *ActionRequest, c *gin.Context) {
			handleTracesAction(a, c, nil, nil, slog.Default())
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, rec := newWhereTestContext(t)

			handle(&ActionRequest{Action: ActionRequestAction{Name: name}, Input: map[string]any{"request": request}}, c)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), `field \"namespace\" is given both inside and outside query_request.where._binary`)
		})
	}
}

// Every traces action type-asserts Input["request"]; a missing one is a 400, not a panic.
func TestTracesHandlerRefusesMissingRequest(t *testing.T) {
	c, rec := newWhereTestContext(t)

	handleTracesAction(&ActionRequest{Action: ActionRequestAction{Name: "traces_list"}, Input: map[string]any{}}, c, nil, nil, slog.Default())

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing or invalid request input")
}
