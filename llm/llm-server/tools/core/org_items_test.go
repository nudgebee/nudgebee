package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

const (
	itemTestTenant  = "00000000-0000-0000-0000-000000000001"
	itemTestAccount = "00000000-0000-0000-0000-000000000002"
	itemTestOther   = "00000000-0000-0000-0000-000000000003"
	itemTestItem    = "00000000-0000-0000-0000-000000000004"
)

const (
	adminTestTenant  = "11111111-1111-1111-1111-111111111111"
	adminTestAccount = "aaaaaaaa-0000-0000-0000-000000000001"
)

func itemTenantAdmin(tenant string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(tenant, "", []string{adminTestAccount})
}

// itemAccountAdmin is an admin of adminTestAccount only.
func itemAccountAdmin(t *testing.T) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": adminTestTenant, "AccountIds": []string{adminTestAccount}, "Roles": []string{"account_admin"},
		"ScopedEntityIds": map[string][]string{"account_admin": {adminTestAccount}},
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func newOrgTestTx(t *testing.T) (*sqlx.Tx, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	tx, err := sqlx.NewDb(db, "postgres").Beginx()
	require.NoError(t, err)
	return tx, mock
}

func TestCheckOrgItemName(t *testing.T) {
	clashCols := []string{"visibility", "account_name"}
	tests := []struct {
		name    string
		org     bool
		rows    [][]any
		wantErr string
	}{
		{name: "free name for an organisation item", org: true},
		{name: "free name for an account item", org: false},
		{name: "organisation item clashes with an account item", org: true,
			rows: [][]any{{"account", "prod-aws"}}, wantErr: "an agent named Triage already exists in account prod-aws; rename it first"},
		{name: "account items may repeat a name across accounts", org: false,
			rows: [][]any{{"account", "prod-aws"}}},
		{name: "account item may not take an organisation name", org: false,
			rows: [][]any{{"organisation", ""}}, wantErr: "an organisation agent named Triage already exists"},
		{name: "organisation item may not take an organisation name", org: true,
			rows: [][]any{{"selected_accounts", ""}}, wantErr: "an organisation agent named Triage already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, mock := newOrgTestTx(t)
			mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtext($1))`)).
				WithArgs("llm_agents:" + itemTestTenant + ":triage").WillReturnResult(sqlmock.NewResult(0, 0))
			rows := sqlmock.NewRows(clashCols)
			for _, r := range tt.rows {
				rows.AddRow(r[0], r[1])
			}
			mock.ExpectQuery(`FROM llm_agents t`).WithArgs(itemTestTenant, "triage", "").WillReturnRows(rows)

			err := CheckOrgItemName(tx, OrgAgentKind, itemTestTenant, "Triage", "", tt.org)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOrgItemAccountScopeShape(t *testing.T) {
	agent := OrgAgentKind.AccountScope("lg", 1)
	// Account rows are owned through their installation row, compared as text
	// for agents (llm_agents_installation.agent_id is TEXT).
	assert.Contains(t, agent, "inst.agent_id = lg.id::text AND inst.account_id = $1")
	assert.Contains(t, agent, "lg.tenant_id = (SELECT ca.tenant FROM cloud_accounts ca WHERE ca.id = $1)")
	assert.Contains(t, agent, "llm_agent_account_overrides ovr WHERE ovr.agent_id = lg.id AND ovr.account_id = $1 AND NOT ovr.enabled")

	tool := OrgToolKind.AccountScope("lt", 3)
	assert.Contains(t, tool, "inst.tool_id = lt.id AND inst.account_id = $3")
	assert.Contains(t, tool, "llm_tool_account_overrides ovr WHERE ovr.tool_id = lt.id AND ovr.account_id = $3 AND ovr.enabled")
	assert.NotContains(t, tool, "$1")
}

func TestCheckOrgToolSecrets(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{name: "no env vars", config: map[string]any{"image": "alpine"}},
		{name: "plain env vars", config: map[string]any{"env_vars": map[string]any{"REGION": "us-east-1", "LOG_LEVEL": "debug"}}},
		{name: "token", config: map[string]any{"env_vars": map[string]any{"GITHUB_TOKEN": "x"}}, wantErr: "GITHUB_TOKEN looks like a secret"},
		{name: "lower-case password", config: map[string]any{"env_vars": map[string]any{"db_password": "x"}}, wantErr: "db_password looks like a secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckOrgToolSecrets(tt.config)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestCheckOrgToolName(t *testing.T) {
	nbSystemTools["org_items_test_builtin"] = func(string) (NBTool, error) { return nil, errors.New("unused") }
	t.Cleanup(func() { delete(nbSystemTools, "org_items_test_builtin") })

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	sdb := sqlx.NewDb(db, "postgres")

	assert.ErrorContains(t, CheckOrgToolName(sdb, itemTestTenant, "Org_Items_Test_Builtin"), "built-in tool name")

	mock.ExpectQuery(`FROM integrations WHERE tenant_id = \$1::uuid AND type = 'mcp'`).WithArgs(itemTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("GitHub Prod"))
	assert.ErrorContains(t, CheckOrgToolName(sdb, itemTestTenant, "github_prod_list_issues"), "MCP integration GitHub Prod")

	mock.ExpectQuery(`FROM integrations`).WithArgs(itemTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("GitHub Prod"))
	assert.NoError(t, CheckOrgToolName(sdb, itemTestTenant, "release_notes"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetOrgItemVisibilityWidensAccountItem(t *testing.T) {
	tx, mock := newOrgTestTx(t)
	owner := OrgItemOwner{Id: itemTestItem, Name: "deploy-check", TenantId: itemTestTenant, Visibility: VisibilityAccount, AccountId: itemTestAccount}

	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FROM llm_tools t`).WithArgs(itemTestTenant, "deploy-check", itemTestItem).
		WillReturnRows(sqlmock.NewRows([]string{"visibility", "account_name"}))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_tools_installation WHERE tool_id = $1`)).WithArgs(itemTestItem).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT count\(\*\) FROM cloud_accounts`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_tool_account_overrides WHERE tool_id = $1`)).WithArgs(itemTestItem).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO llm_tool_account_overrides`).WithArgs(itemTestItem, itemTestAccount, itemTestTenant, true, "u1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO llm_tool_account_overrides`).WithArgs(itemTestItem, itemTestOther, itemTestTenant, true, "u1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE llm_tools SET visibility = $1`)).WithArgs(VisibilitySelectedAccounts, itemTestItem).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := SetOrgItemVisibility(tx, OrgToolKind, owner, VisibilitySelectedAccounts, []string{itemTestAccount, itemTestOther, itemTestAccount}, "", "u1")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetOrgItemVisibilityRefusesTakenName(t *testing.T) {
	tx, mock := newOrgTestTx(t)
	owner := OrgItemOwner{Id: itemTestItem, Name: "deploy-check", TenantId: itemTestTenant, Visibility: VisibilityAccount, AccountId: itemTestAccount}
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FROM llm_tools t`).WillReturnRows(sqlmock.NewRows([]string{"visibility", "account_name"}).AddRow("account", "staging"))

	err := SetOrgItemVisibility(tx, OrgToolKind, owner, VisibilityOrganisation, nil, "", "u1")
	assert.ErrorContains(t, err, "already exists in account staging; rename it first")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetOrgItemVisibilitySelectedToOrganisationClearsSelection(t *testing.T) {
	tx, mock := newOrgTestTx(t)
	owner := OrgItemOwner{Id: itemTestItem, Name: "deploy-check", TenantId: itemTestTenant, Visibility: VisibilitySelectedAccounts}
	// Every override row goes: an enabled = false row meant "not selected", not "switched off".
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_tool_account_overrides WHERE tool_id = $1`) + `$`).WithArgs(itemTestItem).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE llm_tools SET visibility = $1`)).WithArgs(VisibilityOrganisation, itemTestItem).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, SetOrgItemVisibility(tx, OrgToolKind, owner, VisibilityOrganisation, nil, "", "u1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetOrgItemVisibilityRefusesNarrowingUsedItem(t *testing.T) {
	tx, mock := newOrgTestTx(t)
	owner := OrgItemOwner{Id: itemTestItem, Name: "deploy-check", TenantId: itemTestTenant, Visibility: VisibilityOrganisation}
	mock.ExpectQuery(`json_array_elements_text`).WithArgs(itemTestTenant, "deploy-check").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("triage"))

	err := SetOrgItemVisibility(tx, OrgToolKind, owner, VisibilitySelectedAccounts, []string{itemTestAccount}, "", "u1")
	assert.ErrorContains(t, err, "tool deploy-check is used by organisation agents triage; remove it from them first")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCanSwitchOrgItem(t *testing.T) {
	orgWide := OrgItemOwner{Id: itemTestItem, TenantId: adminTestTenant, Visibility: VisibilityOrganisation}
	selected := OrgItemOwner{Id: itemTestItem, TenantId: adminTestTenant, Visibility: VisibilitySelectedAccounts}
	otherTenant := OrgItemOwner{Id: itemTestItem, TenantId: itemTestTenant, Visibility: VisibilityOrganisation}

	assert.True(t, CanSwitchOrgItem(itemAccountAdmin(t), OrgToolKind, orgWide, adminTestAccount), "account admin switches an organisation-wide item for their account")
	assert.False(t, CanSwitchOrgItem(itemAccountAdmin(t), OrgToolKind, orgWide, itemTestOther), "but not for another account")
	assert.False(t, CanSwitchOrgItem(itemAccountAdmin(t), OrgToolKind, selected, adminTestAccount), "choosing selected accounts is for tenant admins")
	assert.True(t, CanSwitchOrgItem(itemTenantAdmin(adminTestTenant), OrgToolKind, selected, adminTestAccount))
	assert.False(t, CanSwitchOrgItem(itemTenantAdmin(adminTestTenant), OrgToolKind, otherTenant, adminTestAccount), "never across tenants")
}

func TestCheckNotUsedByOrgAgents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	sdb := sqlx.NewDb(db, "postgres")

	mock.ExpectQuery(`json_array_elements_text`).WithArgs(itemTestTenant, "log-check").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("triage").AddRow("deploy"))
	assert.ErrorContains(t, CheckNotUsedByOrgAgents(sdb, OrgToolKind, itemTestTenant, "log-check"),
		"tool log-check is used by organisation agents triage, deploy; remove it from them first")

	mock.ExpectQuery(`json_array_elements_text`).WithArgs(itemTestTenant, "unused").WillReturnRows(sqlmock.NewRows([]string{"name"}))
	assert.NoError(t, CheckNotUsedByOrgAgents(sdb, OrgToolKind, itemTestTenant, "unused"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestForEachTenantAccount(t *testing.T) {
	orig := TenantAccountIDs
	t.Cleanup(func() { TenantAccountIDs = orig })
	TenantAccountIDs = func(string) []string { return []string{"a", "b", "c"} }

	var mu sync.Mutex
	var batches [][]string
	rest := make(chan struct{})
	// The background batch waits until the synchronous one is checked, so a
	// fast goroutine can't append before the first assertion.
	release := make(chan struct{})
	ForEachTenantAccount(itemTestTenant, []string{"b", "", "b"}, func(ids []string) {
		if len(ids) != 1 || ids[0] != "b" {
			<-release
		}
		mu.Lock()
		batches = append(batches, ids)
		n := len(batches)
		mu.Unlock()
		if n == 2 {
			close(rest)
		}
	})
	mu.Lock()
	assert.Equal(t, [][]string{{"b"}}, batches, "the request's accounts are done, as one batch, before it returns")
	mu.Unlock()
	close(release)

	select {
	case <-rest:
	case <-time.After(5 * time.Second):
		t.Fatal("the other accounts were not invalidated")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]string{{"b"}, {"a", "c"}}, batches, "the rest come as one batch")
}
