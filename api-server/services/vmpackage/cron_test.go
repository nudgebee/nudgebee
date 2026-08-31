package vmpackage

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDiscoveryDatasources_FiltersIneligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "target_account_id", "labels"}).
		AddRow("int-1", "tenant-1", "account-1", "aws-account-1", `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`).
		AddRow("int-2", "tenant-1", "account-2", nil, `{"actions":["discovery_ldap"],"allowed_cidrs":["10.0.0.0/24"]}`).  // no discovery_sweep
		AddRow("int-3", "tenant-2", "account-3", nil, `{"actions":["discovery_sweep"],"allowed_cidrs":[]}`).              // empty allowed_cidrs
		AddRow("int-4", "tenant-2", "account-4", nil, `{"actions":["discovery_sweep"],"allowed_cidrs":["10.1.0.0/24"]}`). // sweep-only, no inventory, unassociated
		AddRow("int-5", "tenant-3", "account-5", nil, ``)
	mock.ExpectQuery("SELECT i.id::text AS integration_id").WillReturnRows(rows)

	datasources, err := ListDiscoveryDatasources(mockDBManager)
	require.NoError(t, err)
	require.Len(t, datasources, 2)

	assert.Equal(t, "int-1", datasources[0].IntegrationID)
	assert.Equal(t, []string{"discovery_sweep", "discovery_inventory"}, datasources[0].Labels.Actions)
	assert.Equal(t, []string{"172.31.0.0/28"}, datasources[0].Labels.AllowedCIDRs)
	assert.Equal(t, []int{2}, datasources[0].Labels.PackVersions)
	assert.Equal(t, "aws-account-1", datasources[0].TargetAccountID)

	assert.Equal(t, "int-4", datasources[1].IntegrationID)
	assert.Empty(t, datasources[1].Labels.PackVersions)
	assert.Empty(t, datasources[1].TargetAccountID, "unassociated datasource must resolve to an empty TargetAccountID, not error")
}

func TestListDiscoveryDatasourcesForAccount_FiltersByTargetAndEligibility(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "target_account_id", "labels"}).
		AddRow("int-1", "tenant-1", "account-1", "aws-account-1", `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`)
	mock.ExpectQuery("SELECT i.id::text AS integration_id").
		WithArgs("aws-account-1").
		WillReturnRows(rows)

	datasources, err := ListDiscoveryDatasourcesForAccount(mockDBManager, "aws-account-1")
	require.NoError(t, err)
	require.Len(t, datasources, 1)
	assert.Equal(t, "int-1", datasources[0].IntegrationID)
	assert.Equal(t, "aws-account-1", datasources[0].TargetAccountID)
}

// A self-hosted forager discovery datasource that runs in the queried account
// but carries no explicit discovery_target row must still be returned — this
// is the case that previously 400'd with "no discovery agent is configured to
// scan this account". The row shape here (own account_id == queried account,
// target_account_id NULL) is what the own-fallback arm of the WHERE clause
// matches.
func TestListDiscoveryDatasourcesForAccount_MatchesOwnAccountWithoutTarget(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "target_account_id", "labels"}).
		AddRow("int-1", "tenant-1", "self-hosted-acct", nil, `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`)
	mock.ExpectQuery("SELECT i.id::text AS integration_id").
		WithArgs("self-hosted-acct").
		WillReturnRows(rows)

	datasources, err := ListDiscoveryDatasourcesForAccount(mockDBManager, "self-hosted-acct")
	require.NoError(t, err)
	require.Len(t, datasources, 1)
	assert.Equal(t, "int-1", datasources[0].IntegrationID)
	assert.Equal(t, "self-hosted-acct", datasources[0].AccountID)
	assert.Empty(t, datasources[0].TargetAccountID)
}

// Pins the WHERE clause: an explicit discovery_target must win over the 'own'
// account, so a datasource with own=A / target=B is scannable only from B, not
// A. Matching it from A would mis-scan (A untouched, B's findings rewritten by
// resolveTargets against ds.TargetAccountID) and let a caller with access to A
// only trigger a scan on B. sqlmock does not execute the SQL, so this asserts
// the guarded predicate is present in the query text rather than its runtime
// effect — full behavioural coverage would need a real-Postgres fixture.
func TestListDiscoveryDatasourcesForAccount_ExplicitTargetWinsOverOwn(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery(`target\.cloud_account_id IS NULL AND own\.cloud_account_id = \$1`).
		WithArgs("host-acct").
		WillReturnRows(sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "target_account_id", "labels"}))

	datasources, err := ListDiscoveryDatasourcesForAccount(mockDBManager, "host-acct")
	require.NoError(t, err)
	assert.Empty(t, datasources)
}

func TestListDiscoveryDatasourcesForAccount_NoneEligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "target_account_id", "labels"})
	mock.ExpectQuery("SELECT i.id::text AS integration_id").
		WithArgs("aws-account-2").
		WillReturnRows(rows)

	datasources, err := ListDiscoveryDatasourcesForAccount(mockDBManager, "aws-account-2")
	require.NoError(t, err)
	assert.Empty(t, datasources)
}

func TestGetDiscoveryDatasourceByID_Found(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "target_account_id", "labels"}).
		AddRow("tenant-1", "aws-account-1", `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`)
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	ds, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-1", "account-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "int-1", ds.IntegrationID)
	assert.Equal(t, "tenant-1", ds.TenantID)
	assert.Equal(t, "account-1", ds.AccountID)
	assert.Equal(t, "aws-account-1", ds.TargetAccountID)
	assert.Equal(t, []string{"172.31.0.0/28"}, ds.Labels.AllowedCIDRs)
}

func TestGetDiscoveryDatasourceByID_NotFound(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "target_account_id", "labels"})
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-missing", "account-1").
		WillReturnRows(rows)

	_, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-missing", "account-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestGetDiscoveryDatasourceByID_NoLongerEligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "target_account_id", "labels"}).
		AddRow("tenant-1", nil, `{"actions":["discovery_ldap"],"allowed_cidrs":["172.31.0.0/28"]}`)
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	_, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-1", "account-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestGetDiscoveryDatasourceByID_Unassociated(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "target_account_id", "labels"}).
		AddRow("tenant-1", nil, `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`)
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	ds, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-1", "account-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Empty(t, ds.TargetAccountID)
}

func TestResolveDiscoveryDatasourceKey_Found(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"value"}).AddRow("forager-ds-key-1")
	mock.ExpectQuery("SELECT value FROM integration_config_values").
		WithArgs("int-1").
		WillReturnRows(rows)

	key, err := resolveDiscoveryDatasourceKey(mockDBManager, "int-1")
	require.NoError(t, err)
	assert.Equal(t, "forager-ds-key-1", key)
}

func TestFindOrCreateVMResource_Existing(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"id"}).AddRow("resource-1")
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("vm-172.31.0.11", "account-1", "tenant-1").
		WillReturnRows(rows)

	id, err := findOrCreateVMResource(mockDBManager, "tenant-1", "account-1", "172.31.0.11")
	require.NoError(t, err)
	assert.Equal(t, "resource-1", id)
}

func TestFindOrCreateVMResource_CreatesWhenMissing(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("vm-172.31.0.12", "account-1", "tenant-1").
		WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectExec("INSERT INTO public.cloud_resourses").
		WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"id"}).AddRow("resource-new")
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("vm-172.31.0.12", "account-1", "tenant-1").
		WillReturnRows(rows)

	id, err := findOrCreateVMResource(mockDBManager, "tenant-1", "account-1", "172.31.0.12")
	require.NoError(t, err)
	assert.Equal(t, "resource-new", id)
}

func TestMaxInt(t *testing.T) {
	assert.Equal(t, 3, maxInt([]int{1, 3, 2}))
	assert.Equal(t, 0, maxInt(nil))
}
