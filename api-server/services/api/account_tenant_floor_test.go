package api

import (
	"io"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"

	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	floorTenant         = "d0bee719-4b27-4335-b39f-7e51e1deff6d"
	floorOwnAccount     = "87365cf1-bdf5-51d2-a3c6-87a37fb841ef"
	floorForeignAccount = "0a921890-c3c9-50ed-83ef-eb911d263724"
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

// The floor refuses a request naming another tenant's account before any
// handler runs, wherever in the body the id sits, and lets everything else
// through with the body intact.
func TestAccountTenantFloor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prevLookup := isTenantAccount
	t.Cleanup(func() { isTenantAccount = prevLookup })
	isTenantAccount = func(tenantId, accountId string) (bool, error) {
		return tenantId == floorTenant && accountId == floorOwnAccount, nil
	}

	r := gin.New()
	r.Use(AccountTenantFloor())
	r.POST("/rpc/x", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body))
	})
	call := func(body string, header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/rpc/x", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	tenant := map[string]string{"x-tenant-id": floorTenant}

	for name, tc := range map[string]struct {
		body   string
		header map[string]string
		want   int
	}{
		"own account":                           {`{"input":{"account_id":"` + floorOwnAccount + `"}}`, tenant, 200},
		"foreign account_id":                    {`{"input":{"account_id":"` + floorForeignAccount + `"}}`, tenant, 403},
		"foreign cloud_account_id in objects[]": {`{"input":{"objects":[{"cloud_account_id":"` + floorForeignAccount + `"}]}}`, tenant, 403},
		"foreign id in account_ids list":        {`{"input":{"request":{"account_ids":["` + floorOwnAccount + `","` + floorForeignAccount + `"]}}}`, tenant, 403},
		"tenant from session_variables":         {`{"input":{"account_id":"` + floorForeignAccount + `"},"session_variables":{"tenant_id":"` + floorTenant + `"}}`, nil, 403},
		"upper-case key":                        {`{"input":{"ACCOUNT_ID":"` + floorForeignAccount + `"}}`, tenant, 403},
		"camelCase key":                         {`{"input":{"request":{"cloudAccountId":"` + floorForeignAccount + `"}}}`, tenant, 403},
		"not a uuid (provider account number)":  {`{"input":{"account_id":"123456789012"}}`, tenant, 200},
		"other keys are not account ids":        {`{"input":{"user_id":"` + floorForeignAccount + `"}}`, tenant, 200},
		"no tenant: server-internal caller":     {`{"input":{"account_id":"` + floorForeignAccount + `"}}`, nil, 200},
		"super admin":                           {`{"input":{"account_id":"` + floorForeignAccount + `"},"session_variables":{"allowed_roles":["super_admin"]}}`, tenant, 200},
		"session_variables are not input":       {`{"input":{},"session_variables":{"account_id":"` + floorForeignAccount + `","tenant_id":"` + floorTenant + `"}}`, nil, 200},
		"not json":                              {`account_id=` + floorForeignAccount, tenant, 200},
	} {
		t.Run(name, func(t *testing.T) {
			rec := call(tc.body, tc.header)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want == 200 {
				assert.Equal(t, tc.body, rec.Body.String(), "the handler reads the body unchanged")
			} else {
				assert.Contains(t, rec.Body.String(), "access denied for account")
			}
		})
	}

	t.Run("a malformed tenant id is refused, not a database error", func(t *testing.T) {
		isTenantAccount = prevLookup
		t.Cleanup(func() {
			isTenantAccount = func(tenantId, accountId string) (bool, error) {
				return tenantId == floorTenant && accountId == floorOwnAccount, nil
			}
		})
		assert.Equal(t, 403, call(`{"input":{"account_id":"`+floorOwnAccount+`"}}`, map[string]string{"x-tenant-id": "not-a-tenant"}).Code)
	})
	t.Run("a body over the limit reaches the handler whole", func(t *testing.T) {
		big := `{"input":{"blob":"` + strings.Repeat("a", maxFloorBodyBytes+10) + `"}}`
		rec := call(big, tenant)
		require.Equal(t, 200, rec.Code)
		assert.Equal(t, len(big), rec.Body.Len())
	})
}

// A miss in the tenant's cached list is settled by the account's own row: an
// account onboarded a moment ago is accepted, another tenant's is not.
func TestIsTenantAccountConfirmsAMissByPrimaryKey(t *testing.T) {
	mock := mockMetastore(t)
	common.CacheCreateNamespace(tenantAccountIdsNS)
	_ = common.CacheDelete(tenantAccountIdsNS, floorTenant)

	mock.ExpectQuery(`SELECT id::text FROM cloud_accounts WHERE tenant = \$1::uuid`).WithArgs(floorTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(floorOwnAccount))
	ok, err := isTenantAccount(floorTenant, floorOwnAccount)
	require.NoError(t, err)
	assert.True(t, ok, "listed")

	const fresh = "aaaaaaaa-0000-0000-0000-000000000001"
	mock.ExpectQuery(`SELECT tenant::text FROM cloud_accounts WHERE id = \$1::uuid`).WithArgs(fresh).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(floorTenant))
	ok, err = isTenantAccount(floorTenant, fresh)
	require.NoError(t, err)
	assert.True(t, ok, "onboarded since the list was cached")

	mock.ExpectQuery(`SELECT id::text FROM cloud_accounts WHERE tenant = \$1::uuid`).WithArgs(floorTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(floorOwnAccount).AddRow(fresh))
	mock.ExpectQuery(`SELECT tenant::text FROM cloud_accounts WHERE id = \$1::uuid`).WithArgs(floorForeignAccount).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("99999999-0000-0000-0000-000000000001"))
	// The list is reloaded after a fresh account was accepted, and answers the first time.
	ok, err = isTenantAccount(floorTenant, floorForeignAccount)
	require.NoError(t, err)
	assert.False(t, ok, "another tenant's, from the list")
	// Cached now: the miss is settled by the account's row.
	ok, err = isTenantAccount(floorTenant, floorForeignAccount)
	require.NoError(t, err)
	assert.False(t, ok, "another tenant's, from its row")
	require.NoError(t, mock.ExpectationsWereMet())
}

// An id that belongs to an integration (PagerDuty, Slack, Jira, ...) rather
// than a cloud account passes the floor the same way a cloud account does,
// both from the cached list and from a fresh row.
func TestIsTenantAccountAcceptsAnIntegrationId(t *testing.T) {
	mock := mockMetastore(t)
	common.CacheCreateNamespace(tenantAccountIdsNS)
	_ = common.CacheDelete(tenantAccountIdsNS, floorTenant)

	// Each lookup is a single UNION query; sqlmock sees the one statement and
	// hands back the row the database would have produced after combining
	// both halves, regardless of which subquery sourced it.
	const integrationId = "bbbbbbbb-0000-0000-0000-000000000001"
	mock.ExpectQuery(`SELECT id::text FROM cloud_accounts WHERE tenant = \$1::uuid`).WithArgs(floorTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(integrationId))
	ok, err := isTenantAccount(floorTenant, integrationId)
	require.NoError(t, err)
	assert.True(t, ok, "listed via the integrations union")

	const freshIntegration = "cccccccc-0000-0000-0000-000000000001"
	mock.ExpectQuery(`SELECT tenant::text FROM cloud_accounts WHERE id = \$1::uuid`).WithArgs(freshIntegration).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow(floorTenant))
	ok, err = isTenantAccount(floorTenant, freshIntegration)
	require.NoError(t, err)
	assert.True(t, ok, "integration onboarded since the list was cached")
	require.NoError(t, mock.ExpectationsWereMet())
}
