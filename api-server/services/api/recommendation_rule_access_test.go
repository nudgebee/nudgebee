package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each of these acts on the account id in the request: an export reads its
// recommendations, a retry re-applies a fix to it, a rule sync calls its
// observability provider with its credentials, and the action list probes its
// Prometheus. Each must refuse another tenant's account before any of that.
func TestRecommendationAndRuleActionsRefuseAnotherTenantsAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		action      string
		input       map[string]any
		run         func(*ActionRequest, *gin.Context)
		contexts    int  // how many times the handler builds a security context
		confirmMiss bool // IsTenantAccount re-checks a cache miss against the database
	}{
		{
			name:   "recommendation_export",
			action: "recommendation_export",
			input:  map[string]any{"account_id": accessTestForeignAccount, "format": "csv", "category": "Configuration"},
			run: func(a *ActionRequest, c *gin.Context) {
				body, err := json.Marshal(a)
				require.NoError(t, err)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/export/recommendations", bytes.NewReader(body))
				c.Request.Header.Set("x-tenant-id", accessTestTenant)
				handleExportRecommendations(c, nil, nil, slog.Default())
			},
		},
		{
			name:   "recommendation_resolution_retry",
			action: "recommendation_resolution_retry",
			input:  map[string]any{"account_id": accessTestForeignAccount, "resolution_id": "res-1"},
			run:    func(a *ActionRequest, c *gin.Context) { handleRecommendationAction(a, c, nil, nil, slog.Default()) },
		},
		{
			name:   "eventrules_sync_provider_rules",
			action: "eventrules_sync_provider_rules",
			input:  map[string]any{"account_id": accessTestForeignAccount, "sources": []any{"datadog"}},
			run:    func(a *ActionRequest, c *gin.Context) { handleEventRuleAction(a, c, nil, nil, slog.Default()) },
		},
		{
			name:        "alertmanager_list_actions",
			action:      "alertmanager_list_actions",
			input:       map[string]any{"cloud_account_id": accessTestForeignAccount, "source": "prometheus", "query": "up"},
			run:         func(a *ActionRequest, c *gin.Context) { handleEventRuleAction(a, c, nil, nil, slog.Default()) },
			contexts:    2, // once for the audit event, once again inside the case
			confirmMiss: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := mockMetastore(t)
			// The tenant-admin context lists the tenant's accounts.
			for i := 0; i < max(tc.contexts, 1); i++ {
				mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
					WithArgs(accessTestTenant).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(accessTestOwnAccount))
			}
			if tc.confirmMiss {
				mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM cloud_accounts").
					WithArgs(accessTestForeignAccount, accessTestTenant).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
			c.Request.Header.Set("x-tenant-id", accessTestTenant)

			tc.run(&ActionRequest{Action: ActionRequestAction{Name: tc.action}, Input: tc.input}, c)

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.NoError(t, mock.ExpectationsWereMet(), "handler queried beyond the access check")
		})
	}
}
