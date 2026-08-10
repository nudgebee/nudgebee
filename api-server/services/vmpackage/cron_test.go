package vmpackage

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDiscoveryDatasources_FiltersIneligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"integration_id", "tenant_id", "account_id", "labels"}).
		AddRow("int-1", "tenant-1", "account-1", `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`).
		AddRow("int-2", "tenant-1", "account-2", `{"actions":["discovery_ldap"],"allowed_cidrs":["10.0.0.0/24"]}`).  // no discovery_sweep
		AddRow("int-3", "tenant-2", "account-3", `{"actions":["discovery_sweep"],"allowed_cidrs":[]}`).              // empty allowed_cidrs
		AddRow("int-4", "tenant-2", "account-4", `{"actions":["discovery_sweep"],"allowed_cidrs":["10.1.0.0/24"]}`). // sweep-only, no inventory
		AddRow("int-5", "tenant-3", "account-5", ``)
	mock.ExpectQuery("SELECT i.id::text AS integration_id").WillReturnRows(rows)

	datasources, err := ListDiscoveryDatasources(mockDBManager)
	require.NoError(t, err)
	require.Len(t, datasources, 2)

	assert.Equal(t, "int-1", datasources[0].IntegrationID)
	assert.Equal(t, []string{"discovery_sweep", "discovery_inventory"}, datasources[0].Labels.Actions)
	assert.Equal(t, []string{"172.31.0.0/28"}, datasources[0].Labels.AllowedCIDRs)
	assert.Equal(t, []int{2}, datasources[0].Labels.PackVersions)

	assert.Equal(t, "int-4", datasources[1].IntegrationID)
	assert.Empty(t, datasources[1].Labels.PackVersions)
}

func TestGetDiscoveryDatasourceByID_Found(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "labels"}).
		AddRow("tenant-1", `{"actions":["discovery_sweep","discovery_inventory"],"allowed_cidrs":["172.31.0.0/28"],"pack_versions":[2]}`)
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	ds, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-1", "account-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "int-1", ds.IntegrationID)
	assert.Equal(t, "tenant-1", ds.TenantID)
	assert.Equal(t, "account-1", ds.AccountID)
	assert.Equal(t, []string{"172.31.0.0/28"}, ds.Labels.AllowedCIDRs)
}

func TestGetDiscoveryDatasourceByID_NotFound(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "labels"})
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-missing", "account-1").
		WillReturnRows(rows)

	_, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-missing", "account-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestGetDiscoveryDatasourceByID_NoLongerEligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "labels"}).
		AddRow("tenant-1", `{"actions":["discovery_ldap"],"allowed_cidrs":["172.31.0.0/28"]}`)
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	_, ok, err := GetDiscoveryDatasourceByID(mockDBManager, "int-1", "account-1")
	require.NoError(t, err)
	assert.False(t, ok)
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
