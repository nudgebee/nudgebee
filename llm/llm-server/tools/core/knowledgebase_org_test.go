package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

const (
	orgTestTenant  = "11111111-1111-1111-1111-111111111111"
	orgTestOther   = "99999999-9999-9999-9999-999999999999"
	orgTestAccount = "aaaaaaaa-0000-0000-0000-000000000001"
)

var ownerColumns = []string{"account_id", "tenant_id"}

func tenantAdmin(tenant string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(tenant, "", []string{orgTestAccount})
}

// accountAdmin is an admin of orgTestAccount only.
func accountAdmin(t *testing.T) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": orgTestTenant, "AccountIds": []string{orgTestAccount}, "Roles": []string{"account_admin"},
		"ScopedEntityIds": map[string][]string{"account_admin": {orgTestAccount}},
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func expectOwner(mock sqlmock.Sqlmock, kbId, accountId, tenantId string) {
	mock.ExpectQuery(`SELECT COALESCE\(account_id::text, ''\) AS account_id, tenant_id::text AS tenant_id FROM llm_knowledgebases WHERE id = \$1`).
		WithArgs(kbId).WillReturnRows(sqlmock.NewRows(ownerColumns).AddRow(accountId, tenantId))
}

func TestOwnerPredicateKeepsTheAccountClause(t *testing.T) {
	pred, arg := kbOwner{AccountId: orgTestAccount, TenantId: orgTestTenant}.predicate(2)
	assert.Equal(t, "account_id = $2", pred)
	assert.Equal(t, orgTestAccount, arg)

	pred, arg = kbOwner{TenantId: orgTestTenant}.predicate(3)
	assert.Equal(t, "account_id IS NULL AND tenant_id = $3", pred)
	assert.Equal(t, orgTestTenant, arg)
}

func TestResolveKBForWrite(t *testing.T) {
	cases := []struct {
		name      string
		sc        func(*testing.T) *security.RequestContext
		accountId string
		rowAcct   string
		rowTenant string
		wantOrg   bool
		wantErr   string
	}{
		{"tenant admin changes an organisation KB without an account", func(*testing.T) *security.RequestContext { return tenantAdmin(orgTestTenant) }, "", "", orgTestTenant, true, ""},
		{"tenant admin of another tenant may not", func(*testing.T) *security.RequestContext { return tenantAdmin(orgTestOther) }, "", "", orgTestTenant, false, errKBOrgAdminOnly},
		{"account admin may not change an organisation KB", accountAdmin, orgTestAccount, "", orgTestTenant, false, errKBOrgAdminOnly},
		{"account admin changes their own account KB", accountAdmin, orgTestAccount, orgTestAccount, orgTestTenant, false, ""},
		{"an account KB of another account is not found", accountAdmin, orgTestAccount, "aaaaaaaa-0000-0000-0000-000000000002", orgTestTenant, false, errKBNotFound},
		{"an account KB needs its account", func(*testing.T) *security.RequestContext { return tenantAdmin(orgTestTenant) }, "", orgTestAccount, orgTestTenant, false, errKBAccountIDRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := registerMockMetastore(t)
			common.ResetDatabaseManager(common.Metastore)
			expectOwner(mock, "kb-1", c.rowAcct, c.rowTenant)
			dbms, err := common.GetDatabaseManager(common.Metastore)
			require.NoError(t, err)

			owner, err := resolveKBForWrite(c.sc(t), dbms, c.accountId, "kb-1", security.SecurityAccessTypeUpdate)

			if c.wantErr != "" {
				require.EqualError(t, err, c.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantOrg, owner.isOrg())
			assert.Equal(t, c.rowTenant, owner.TenantId)
		})
	}
}

func TestOrganisationKBIsIndexedForItsTenant(t *testing.T) {
	old := config.Config.RAGServerUrl
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"status":"success","document_count":1}`))
	}))
	defer server.Close()
	config.Config.RAGServerUrl = server.URL

	_, err := createKBVectorCollection(kbOwner{TenantId: orgTestTenant}, "kb-1", "steps", "text", "", "tester", "user_create", nil)

	require.NoError(t, err)
	assert.Equal(t, orgTestTenant, payload["tenant_id"])
	assert.NotContains(t, payload, "account_id")
}

func TestWideningUpdatesTheRowThenRescopesTheCollection(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	oldURL, oldInvalidator := config.Config.RAGServerUrl, OrgKnowledgeCacheInvalidator
	var scopeBody map[string]any
	var invalidatedTenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/kb/scope", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &scopeBody))
		_, _ = w.Write([]byte(`{"status":"success","updated":true}`))
	}))
	defer server.Close()
	config.Config.RAGServerUrl = server.URL
	OrgKnowledgeCacheInvalidator = func(tenantId string) { invalidatedTenant = tenantId }
	t.Cleanup(func() { config.Config.RAGServerUrl, OrgKnowledgeCacheInvalidator = oldURL, oldInvalidator })

	mock.ExpectQuery(`SELECT name, COALESCE\(account_id::text, ''\) AS account_id, tenant_id::text AS tenant_id`).
		WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "account_id", "tenant_id", "visibility", "kb_type"}).
			AddRow("DB failover", orgTestAccount, orgTestTenant, "account", "manual"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM llm_knowledgebases WHERE account_id IS NULL AND tenant_id = \$1 AND LOWER\(BTRIM\(name\)\) = LOWER\(BTRIM\(\$2\)\)`).
		WithArgs(orgTestTenant, "DB failover", "kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE llm_knowledgebases\s+SET account_id = NULLIF\(\$1, ''\)::uuid, visibility = \$2`).
		WithArgs("", "organisation", sqlmock.AnyArg(), "kb-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM llm_kb_account_overrides WHERE kb_id = \$1 AND enabled`).
		WithArgs("kb-1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectQuery(`FROM llm_knowledgebases kb WHERE kb.id = \$1`).WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "visibility", "name"}).AddRow("kb-1", "", "organisation", "DB failover"))

	kb, err := SetKnowledgebaseVisibility(tenantAdmin(orgTestTenant), "kb-1", KBVisibilityOrganisation, nil, "")

	require.NoError(t, err)
	assert.Equal(t, "organisation", kb.Visibility)
	assert.Equal(t, orgTestTenant, scopeBody["tenant_id"])
	assert.NotContains(t, scopeBody, "account_id")
	assert.Equal(t, orgTestTenant, invalidatedTenant)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVisibilityChangesAreTenantAdminOnly(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	mock.ExpectQuery(`SELECT name, COALESCE\(account_id::text, ''\) AS account_id`).WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "account_id", "tenant_id", "visibility", "kb_type"}).
			AddRow("DB failover", orgTestAccount, orgTestTenant, "account", "manual"))

	_, err := SetKnowledgebaseVisibility(accountAdmin(t), "kb-1", KBVisibilityOrganisation, nil, "")

	require.EqualError(t, err, errKBOrgAdminOnly)
}

func TestSelectedAccountsMustBelongToTheOrganisation(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)

	_, err = orgKBAccounts(dbms, orgTestTenant, KBVisibilitySelectedAccounts, nil)
	require.Error(t, err, "selected_accounts with no accounts")

	mock.ExpectQuery(`SELECT id::text FROM cloud_accounts WHERE tenant = \$1`).
		WithArgs(orgTestTenant, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(orgTestAccount))
	_, err = orgKBAccounts(dbms, orgTestTenant, KBVisibilitySelectedAccounts, []string{orgTestAccount, "aaaaaaaa-0000-0000-0000-00000000dead"})
	require.Error(t, err, "an account outside the organisation")

	ids, err := orgKBAccounts(dbms, orgTestTenant, KBVisibilityOrganisation, []string{orgTestAccount})
	require.NoError(t, err)
	assert.Empty(t, ids, "organisation visibility ignores the list")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountAdminSwitchesAnOrganisationKBOffForTheirAccount(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	oldInvalidator := OrgKnowledgeCacheInvalidator
	OrgKnowledgeCacheInvalidator = func(string) {}
	t.Cleanup(func() { OrgKnowledgeCacheInvalidator = oldInvalidator })

	mock.ExpectQuery(`FROM llm_knowledgebases kb JOIN cloud_accounts ca ON ca.id = \$2 AND ca.tenant = kb.tenant_id\s+WHERE kb.id = \$1 AND kb.account_id IS NULL`).
		WithArgs("kb-1", orgTestAccount).
		WillReturnRows(sqlmock.NewRows([]string{"name", "tenant_id", "visibility"}).AddRow("DB failover", orgTestTenant, "organisation"))
	mock.ExpectExec(`INSERT INTO llm_kb_account_overrides`).
		WithArgs("kb-1", orgTestAccount, orgTestTenant, false, "").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, SetKBAccountOverride(accountAdmin(t), orgTestAccount, "kb-1", false))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOnlyATenantAdminAddsAnAccountToASelectedAccountsKB(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	mock.ExpectQuery(`FROM llm_knowledgebases kb JOIN cloud_accounts ca`).
		WithArgs("kb-1", orgTestAccount).
		WillReturnRows(sqlmock.NewRows([]string{"name", "tenant_id", "visibility"}).AddRow("Payments", orgTestTenant, "selected_accounts"))

	err := SetKBAccountOverride(accountAdmin(t), orgTestAccount, "kb-1", true)

	require.EqualError(t, err, errKBOrgAdminOnly)
}

func TestMappedReadsKeepOnlyTheAccountsOwnAndVisibleOrganisationKBs(t *testing.T) {
	// A mapping row outlives a scope change: a KB moved to another account, or
	// an organisation KB since restricted, must drop out of the agent's reads.
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	oldVisible := VisibleOrgKnowledgebaseIDs
	VisibleOrgKnowledgebaseIDs = func(context.Context, string) []string { return []string{"org-kb"} }
	t.Cleanup(func() { VisibleOrgKnowledgebaseIDs = oldVisible })

	mock.ExpectQuery(`WHERE m.account_id = \$1 AND m.agent_id = \$2 AND \(kb.account_id = \$1 OR kb.id = ANY\(\$3::uuid\[\]\)\)`).
		WithArgs(orgTestAccount, "agent-1", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := ListAgentKBs(accountAdmin(t), orgTestAccount, "agent-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountKBNamedLikeAnOrganisationKBClearsTheOrganisationCaches(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var invalidated []string
	oldInvalidator := OrgKnowledgeCacheInvalidator
	OrgKnowledgeCacheInvalidator = func(tenantId string) { invalidated = append(invalidated, tenantId) }
	t.Cleanup(func() { OrgKnowledgeCacheInvalidator = oldInvalidator })

	mock.ExpectQuery(`WHERE kb.account_id IS NULL AND LOWER\(BTRIM\(kb.name\)\) = ANY\(\$2\)`).
		WithArgs(orgTestAccount, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(orgTestTenant))
	invalidateIfOrgKBNameClash(dbms, orgTestAccount, " DB Failover ")
	assert.Equal(t, []string{orgTestTenant}, invalidated)

	mock.ExpectQuery(`WHERE kb.account_id IS NULL`).WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}))
	invalidateIfOrgKBNameClash(dbms, orgTestAccount, "Payments runbook")
	assert.Len(t, invalidated, 1, "no organisation KB has that name")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScopeCannotChangeWhileIndexing(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	mock.ExpectQuery(`SELECT name, COALESCE\(account_id::text, ''\) AS account_id`).WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "account_id", "tenant_id", "visibility", "kb_type", "status"}).
			AddRow("DB failover", orgTestAccount, orgTestTenant, "account", "manual", "processing"))

	_, err := SetKnowledgebaseVisibility(tenantAdmin(orgTestTenant), "kb-1", KBVisibilityOrganisation, nil, "")

	require.ErrorContains(t, err, "still being indexed")
}

func TestSwitchedOffOrganisationKBsStayListable(t *testing.T) {
	off, on := false, true
	name := func(n string) *string { return &n }
	acct := orgTestAccount
	rows := []kbVisibilityRow{
		{Id: "org-off", Name: name("Runbook"), Enabled: true, Visibility: "organisation", OverrideEnabled: &off},
		{Id: "org-visible", Name: name("Payments"), Enabled: true, Visibility: "organisation"},
		{Id: "org-disabled-off", Name: name("Old"), Enabled: false, Visibility: "organisation", OverrideEnabled: &off},
		{Id: "selected-on", Name: name("Billing"), Enabled: true, Visibility: "selected_accounts", OverrideEnabled: &on},
		{Id: "org-shadowed-off", Name: name("DB failover"), Enabled: true, Visibility: "organisation", OverrideEnabled: &off},
		{Id: "own", AccountId: &acct, Name: name("db failover"), Enabled: true, Visibility: "account"},
	}

	assert.Equal(t, []string{"org-off"}, switchedOffOrgKBIDs(rows, orgTestAccount),
		"only an organisation KB hidden by the account's own switch is listed as switched off")
	assert.NotContains(t, visibleKBCollections(rows, orgTestAccount), "kb_org-off",
		"a switched-off KB stays out of every other read")
}

func TestOrganisationKBIsReadWithoutAnAccountByItsTenantAdmin(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	mock.ExpectQuery(`WHERE kb.id = \$1 AND kb.account_id IS NULL AND kb.tenant_id = \$2`).
		WithArgs("kb-1", orgTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "visibility", "name", "data"}).
			AddRow("kb-1", "", "organisation", "DB failover", "steps"))

	kb, err := GetOrganisationKnowledgebase(tenantAdmin(orgTestTenant), "kb-1")
	require.NoError(t, err)
	assert.Equal(t, "steps", kb.Data)
	require.NoError(t, mock.ExpectationsWereMet())

	_, err = GetOrganisationKnowledgebase(accountAdmin(t), "kb-1")
	require.EqualError(t, err, errKBUnauthorized, "an account admin reads organisation KBs through their account")
}

func TestOrganisationKBsCountAsMappedToAllAgents(t *testing.T) {
	assert.Equal(t, "INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id", KBMappingsJoin(1, 3, nil),
		"without organisation KBs the mapping join is unchanged")

	join := KBMappingsJoin(1, 3, []string{"org-kb"})
	assert.Contains(t, join, "FROM llm_kb_agent_mappings WHERE account_id = $1")
	assert.Contains(t, join, "SELECT id, '*', $1::uuid FROM llm_knowledgebases WHERE id = ANY($3::uuid[])",
		"each visible organisation KB is a wildcard mapping row for the account")
}
