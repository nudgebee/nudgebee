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

const (
	accessTestTenant         = "44444444-4444-4444-4444-444444444444"
	accessTestOwnAccount     = "11111111-1111-1111-1111-111111111111"
	accessTestForeignAccount = "33333333-3333-3333-3333-333333333333"
)

func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return manager, nil })
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	return mock
}

// These actions act on the account id the caller sends. Each must refuse an
// account of another tenant before it queues, runs or reads anything.
func TestActionsRefuseAnotherTenantsAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		action      string
		handler     func(*ActionRequest, *gin.Context)
		input       map[string]any
		confirmMiss bool // IsTenantAccount re-checks a cache miss against the database
	}{
		{
			name:    "anomaly_execute",
			action:  "anomaly_execute",
			handler: func(a *ActionRequest, c *gin.Context) { handleAnomaly(a, c, nil, nil, slog.Default()) },
			input:   map[string]any{"account_id": accessTestForeignAccount},
		},
		{
			name:        "applications_execute_profile",
			action:      "applications_execute_profile",
			handler:     func(a *ActionRequest, c *gin.Context) { handleApplicationEvent(a, c, nil, nil, slog.Default()) },
			input:       map[string]any{"account_id": accessTestForeignAccount, "namespace": "ns", "pod_name": "pod"},
			confirmMiss: true,
		},
		{
			name:        "applications_get_profile_status",
			action:      "applications_get_profile_status",
			handler:     func(a *ActionRequest, c *gin.Context) { handleApplicationEvent(a, c, nil, nil, slog.Default()) },
			input:       map[string]any{"account_id": accessTestForeignAccount, "profile_id": "task-1"},
			confirmMiss: true,
		},
		{
			// It used to run the insight rules on any account of any tenant.
			name:        "insights_aggregate",
			action:      "insights_aggregate",
			handler:     func(a *ActionRequest, c *gin.Context) { handleInsights(a, c, nil, nil, slog.Default()) },
			input:       map[string]any{"request": map[string]any{"account_id": accessTestForeignAccount}},
			confirmMiss: true,
		},
		{
			name:    "cloud_list_metrics",
			action:  "cloud_list_metrics",
			handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
			input:   map[string]any{"request": map[string]any{"account_id": accessTestForeignAccount, "query": map[string]any{"namespace": "AWS/EC2", "metric_name": "CPUUtilization"}}},
		},
		{
			name:    "database_performance_insights",
			action:  "database_performance_insights",
			handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
			input:   map[string]any{"request": map[string]any{"account_id": accessTestForeignAccount, "database_identifier": "db-1", "region": "us-east-1"}},
		},
		{
			name:    "accounts_sync",
			action:  "accounts_sync",
			handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
			input:   map[string]any{"account_id": accessTestForeignAccount},
		},
		{
			name:    "cloud_sync_service",
			action:  "cloud_sync_service",
			handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
			input:   map[string]any{"account_id": accessTestForeignAccount, "service_name": "AmazonEC2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := mockMetastore(t)
			// The tenant-admin context lists the tenant's accounts.
			mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
				WithArgs(accessTestTenant).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accessTestOwnAccount))
			if tc.confirmMiss {
				mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM cloud_accounts").
					WithArgs(accessTestForeignAccount, accessTestTenant).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
			c.Request.Header.Set("x-tenant-id", accessTestTenant)

			tc.handler(&ActionRequest{Action: ActionRequestAction{Name: tc.action}, Input: tc.input}, c)

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "handler queried beyond the access check")
		})
	}
}

// The profile actions are open to read-only and tenant_usage roles, so they
// check only that the account is the caller's tenant's: an own-tenant account
// must get past the check (the handler then fails later, on the unmocked task
// lookup, not with 403).
func TestProfileActionAllowsOwnTenantAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock := mockMetastore(t)
	mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
		WithArgs(accessTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accessTestOwnAccount))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
	c.Request.Header.Set("x-tenant-id", accessTestTenant)

	handleApplicationEvent(&ActionRequest{
		Action: ActionRequestAction{Name: "applications_get_profile_status"},
		Input:  map[string]any{"account_id": accessTestOwnAccount, "profile_id": "task-1"},
	}, c, nil, nil, slog.Default())

	assert.NotEqual(t, http.StatusForbidden, rec.Code)
}

// upgrade_execute_command runs kubectl/aws against the account it is given, so
// the caller needs write access to that account. An unsupported command type
// stands in for a real command: past the access check it fails with 400 and
// runs nothing.
func TestUpgradeExecuteCommandRequiresAccountWriteAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		context string
		account string
		want    int
	}{
		{"another tenant's account", `{"TenantId":"` + accessTestTenant + `","UserId":"user-1","AccountIds":["` + accessTestOwnAccount + `"],"Roles":["tenant_admin"]}`, accessTestForeignAccount, http.StatusForbidden},
		{"read-only admin of the account", `{"TenantId":"` + accessTestTenant + `","UserId":"user-1","AccountIds":["` + accessTestOwnAccount + `"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["` + accessTestOwnAccount + `"]}}`, accessTestOwnAccount, http.StatusForbidden},
		{"admin of the account", `{"TenantId":"` + accessTestTenant + `","UserId":"user-1","AccountIds":["` + accessTestOwnAccount + `"],"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + accessTestOwnAccount + `"]}}`, accessTestOwnAccount, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := mockMetastore(t)
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(tc.context), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			handleExecuteCommandAction(map[string]any{
				"account_id":   tc.account,
				"plan_id":      "plan-1",
				"step_id":      "step-1",
				"task_id":      "task-1",
				"command":      "get pods",
				"command_type": "unsupported",
			}, ctx, c)

			assert.Equal(t, tc.want, rec.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "no command recorded or run")
		})
	}
}
