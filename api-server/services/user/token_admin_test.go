package user

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenSuffix(t *testing.T) {
	assert.Equal(t, "a1b2", tokenSuffix("sk-nb-gw-0000a1b2"))
	assert.Equal(t, "", tokenSuffix("abc"))
	assert.Equal(t, "", tokenSuffix(""))
}

func TestKeyAuditInput_TargetsTheKey(t *testing.T) {
	key := keyScope{KeyID: storedKey, UserID: storedUser, TenantID: storedTenant}
	in := keyAuditInput("admin-1", audit.EventTypeUserLoginDelete, audit.EventActionDelete, key, auditViaAdmin,
		map[string]any{"name": "ci"})
	assert.Equal(t, storedKey, in.TargetID)
	assert.Equal(t, "user_auths", in.TableName)
	assert.Nil(t, in.NewData)
	assert.Equal(t, map[string]any{"name": "ci", "key_id": storedKey, "owner_id": storedUser,
		"actor_id": "admin-1", "via": auditViaAdmin}, in.OldData)

	in = keyAuditInput(storedUser, audit.EventTypeUserLoginCreate, audit.EventActionCreate, key, auditViaOwner, nil)
	assert.Nil(t, in.OldData)
	assert.Equal(t, storedUser, in.NewData["owner_id"])

	// Caller data never overrides who the key belongs to or who acted.
	in = keyAuditInput(adminUser, audit.EventTypeUserUpdate, audit.EventActionUpdate, key, auditViaOwner,
		map[string]any{"owner_id": "forged", "key_id": "forged", "actor_id": "forged", "via": "forged", "kind": KindGateway})
	assert.Equal(t, map[string]any{"kind": KindGateway, "key_id": storedKey, "owner_id": storedUser,
		"actor_id": adminUser, "via": auditViaOwner}, in.NewData)
}

func TestClampPage(t *testing.T) {
	for _, c := range []struct{ limit, offset, wantLimit, wantOffset int }{
		{0, 0, 50, 0}, {-5, -1, 50, 0}, {10, 20, 10, 20}, {10000, 0, 100, 0},
	} {
		l, o := clampPage(c.limit, c.offset)
		assert.Equal(t, c.wantLimit, l, "limit %d", c.limit)
		assert.Equal(t, c.wantOffset, o, "offset %d", c.offset)
	}
}

var tokenRowColumns = []string{"id", "name", "provider", "kind", "status", "created_at", "accessed_at",
	"expires_at", "scopes", "token_suffix", "owner_id", "owner_name"}

// The admin list reads every key in the tenant, then fills limits, policy, spend and last use
// for exactly the keys on the page; a key with no gateway traffic keeps an empty last model.
func TestListTokens_FillsEveryKeyOnThePage(t *testing.T) {
	mock := mockMetastore(t)
	const other = "22222222-2222-2222-2222-222222222222"
	mock.ExpectQuery(`FROM user_auths ua\s+LEFT JOIN users u[\s\S]*WHERE ua.tenant_id = \$1[\s\S]*LIMIT NULLIF\(\$4, 0\) OFFSET \$5`).
		WithArgs(storedTenant, "", "", 50, 0).
		WillReturnRows(sqlmock.NewRows(tokenRowColumns).
			AddRow(storedKey, "ci", "token", KindGateway, "active", "2026-10-01T00:00:00Z", nil, nil, nil, "a1b2", storedUser, "Asha").
			AddRow(other, "cli", "token", KindPlatform, "active", "2026-09-01T00:00:00Z", nil, nil, nil, "", storedUser, "Asha"))
	keys := pq.Array([]string{storedKey, other})
	mock.ExpectQuery(`FROM llm_gateway_rate_limits[\s\S]*api_key_id = ANY\(\$2\)`).WithArgs(storedTenant, keys).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "metric", "period", "value", "model"}).
			AddRow(storedKey, "cost", "month", 20.0, ""))
	mock.ExpectQuery(`FROM user_auths ua\s+JOIN llm_gateway_access_policies p[\s\S]*ua.id = ANY\(\$1::uuid\[\]\)`).WithArgs(keys).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "dimension", "value"}).AddRow(storedKey, "model", "gpt-4o-mini"))
	mock.ExpectQuery(`FROM llm_gateway_usage[\s\S]*GROUP BY api_key_id`).WithArgs(storedTenant, keys).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "requests", "tokens", "cost_usd"}).AddRow(storedKey, 3, 900, 0.42))
	mock.ExpectQuery(`FROM unnest\(\$2::text\[\]\)[\s\S]*JOIN LATERAL[\s\S]*ORDER BY created_at DESC LIMIT 1`).WithArgs(storedTenant, keys).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "provider", "model"}).AddRow(storedKey, "openai", "gpt-4o-mini"))

	db := mustMetastore(t)
	got, err := listTokens(db, tokenFilter{TenantID: storedTenant, Limit: 50})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Asha", got[0].OwnerName)
	assert.Equal(t, "a1b2", got[0].TokenSuffix)
	assert.Equal(t, "gpt-4o-mini", got[0].LastModel)
	assert.Equal(t, "openai", got[0].LastProvider)
	assert.Equal(t, []TokenLimit{{Metric: "cost", Period: "month", Value: 20, Model: ""}}, got[0].Limits)
	assert.Equal(t, []string{"gpt-4o-mini"}, got[0].GatewayPolicy.Models)
	assert.Equal(t, int64(3), got[0].Usage.Requests)
	assert.Equal(t, "", got[1].LastModel)
	assert.Nil(t, got[1].Usage)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An empty page runs one query: nothing to fill.
func TestListTokens_EmptyPageStopsAfterTheKeys(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`FROM user_auths ua`).WillReturnRows(sqlmock.NewRows(tokenRowColumns))
	got, err := listTokens(mustMetastore(t), tokenFilter{TenantID: storedTenant, OwnerID: strings.ToUpper(storedUser)})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// mustMetastore returns the db registered by mockMetastore.
func mustMetastore(t *testing.T) *sqlx.DB {
	t.Helper()
	m, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	return m.Db
}

const adminUser = "cccccccc-0000-0000-0000-000000000009"

// signedInAs is a caller in storedTenant holding roles.
func signedInAs(t *testing.T, userID string, roles ...string) *security.RequestContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"TenantId": strings.ToUpper(storedTenant), "UserId": userID, "Roles": roles})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson(raw, &sc))
	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

func TestTenantTokenRoles(t *testing.T) {
	for _, c := range []struct {
		roles        []string
		list, revoke bool
	}{
		{[]string{"tenant_admin"}, true, true},
		{[]string{"tenant_admin_readonly"}, true, false},
		{[]string{"super_admin"}, true, true},
		{[]string{"super_admin_readonly"}, true, false},
		{[]string{"account_admin"}, false, false},
		{nil, false, false},
	} {
		sc := signedInAs(t, adminUser, c.roles...).GetSecurityContext()
		assert.Equal(t, c.list, canListTenantTokens(sc), "list %v", c.roles)
		assert.Equal(t, c.revoke, canRevokeTenantTokens(sc), "revoke %v", c.roles)
	}
}

func TestListTenantTokens_RefusesNonAdmins(t *testing.T) {
	mock := mockMetastore(t)
	_, err := ListTenantTokens(signedInAs(t, adminUser, "account_admin"), TenantTokenListRequest{})
	var ce common.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 403, ce.Code)
	assert.NoError(t, mock.ExpectationsWereMet()) // no query ran
}

func TestListTenantTokens_RejectsAMalformedUserFilter(t *testing.T) {
	mockMetastore(t)
	_, err := ListTenantTokens(signedInAs(t, adminUser, "tenant_admin"), TenantTokenListRequest{UserID: "not-a-uuid"})
	var ce common.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 400, ce.Code)
}

func TestListTenantTokens_PagesTheCallersTenant(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(`SELECT count\(\*\) FROM user_auths ua`).WithArgs(strings.ToUpper(storedTenant), "", KindGateway).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`FROM user_auths ua\s+LEFT JOIN users u`).WithArgs(strings.ToUpper(storedTenant), "", KindGateway, 100, 0).
		WillReturnRows(sqlmock.NewRows(tokenRowColumns))
	resp, err := ListTenantTokens(signedInAs(t, adminUser, "tenant_admin_readonly"),
		TenantTokenListRequest{Kind: KindGateway, Limit: 10000, Offset: -3})
	require.NoError(t, err)
	assert.Equal(t, 0, resp.Total)
	assert.Empty(t, resp.Tokens)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteTenantToken_RefusesReadOnlyAdmins(t *testing.T) {
	mock := mockMetastore(t)
	_, err := DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin_readonly"), TenantTokenDeleteRequest{Id: storedKey})
	var ce common.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 403, ce.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A malformed id and a key outside the tenant both read as "not found", with nothing touched.
func TestDeleteTenantToken_NotFoundDisclosesNothing(t *testing.T) {
	mock := mockMetastore(t)
	_, err := DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin"), TenantTokenDeleteRequest{Id: "x'; drop"})
	var ce common.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 404, ce.Code)
	assert.Equal(t, "token not found", ce.Message)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text, "user"::text, tenant_id::text, name, kind FROM user_auths[\s\S]*tenant_id = \$2[\s\S]*FOR UPDATE`).
		WithArgs(storedKey, strings.ToUpper(storedTenant)).WillReturnRows(sqlmock.NewRows([]string{"id", "user", "tenant_id", "name", "kind"}))
	mock.ExpectRollback()
	_, err = DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin"), TenantTokenDeleteRequest{Id: storedKey})
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 404, ce.Code)
	assert.Equal(t, "token not found", ce.Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Revoking another user's key cleans up that user's rows, keyed by the stored ids even when
// the request carries the id in upper case; nothing is written under the admin.
func TestDeleteTenantToken_CleansUpUnderTheOwner(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text, "user"::text, tenant_id::text, name, kind FROM user_auths[\s\S]*FOR UPDATE`).
		WithArgs(strings.ToUpper(storedKey), strings.ToUpper(storedTenant)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user", "tenant_id", "name", "kind"}).AddRow(storedKey, storedUser, storedTenant, "ci", KindGateway))
	mock.ExpectExec(`DELETE FROM user_auths WHERE id = \$1`).WithArgs(storedKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM llm_gateway_rate_limits WHERE api_key_id = \$1$`).
		WithArgs(storedKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(deleteKeyPolicy).WithArgs(storedKey, storedUser).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin"), TenantTokenDeleteRequest{Id: strings.ToUpper(storedKey)})
	require.NoError(t, err)
	assert.Equal(t, TenantTokenDeleteResponse{Id: storedKey, Name: "ci", OwnerId: storedUser, Status: "deleted"}, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}
