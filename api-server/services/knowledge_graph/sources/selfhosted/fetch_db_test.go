package selfhosted

// DB-gated test for hostRowsQuery: which scanned hosts a VM account owns.
// Skips cleanly when no Postgres is reachable.

import (
	"context"
	"sort"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
)

const (
	dbTenant      = "5e1f0000-0000-4000-8000-000000000001"
	dbOtherTenant = "5e1f0000-0000-4000-8000-000000000002"
	dbVMAccount   = "5e1f0000-0000-4000-8000-0000000000a1"
	dbAWSAccount  = "5e1f0000-0000-4000-8000-0000000000a2" // active discovery target
	dbOffAccount  = "5e1f0000-0000-4000-8000-0000000000a3" // disabled discovery target
	dbUser        = "5e1f0000-0000-4000-8000-0000000000ff"

	dbDatasource    = "5e1f0000-0000-4000-8000-0000000000d1"
	dbOffDatasource = "5e1f0000-0000-4000-8000-0000000000d2"

	rowFresh       = "5e1f0000-0000-4000-8000-000000000101"
	rowStale       = "5e1f0000-0000-4000-8000-000000000102"
	rowRetired     = "5e1f0000-0000-4000-8000-000000000103"
	rowNeverScan   = "5e1f0000-0000-4000-8000-000000000104"
	rowInTarget    = "5e1f0000-0000-4000-8000-000000000105"
	rowInOff       = "5e1f0000-0000-4000-8000-000000000106"
	rowOtherTenant = "5e1f0000-0000-4000-8000-000000000107"
)

func cleanupHostFixture(dbm *database.DatabaseManager) {
	accounts := []interface{}{dbVMAccount, dbAWSAccount, dbOffAccount}
	_, _ = dbm.Exec(`DELETE FROM vm_package WHERE cloud_account_id IN ($1::uuid, $2::uuid, $3::uuid)`, accounts...)
	_, _ = dbm.Exec(`DELETE FROM vm_scan_status WHERE cloud_account_id IN ($1::uuid, $2::uuid, $3::uuid)`, accounts...)
	_, _ = dbm.Exec(`DELETE FROM cloud_resourses WHERE account IN ($1::uuid, $2::uuid, $3::uuid)`, accounts...)
	_, _ = dbm.Exec(`DELETE FROM integrations_cloud_accounts WHERE integration_id IN ($1::uuid, $2::uuid)`, dbDatasource, dbOffDatasource)
	_, _ = dbm.Exec(`DELETE FROM integrations WHERE id IN ($1::uuid, $2::uuid)`, dbDatasource, dbOffDatasource)
	_, _ = dbm.Exec(`DELETE FROM cloud_accounts WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`, accounts...)
}

// seedHostFixture inserts rows with foreign-key triggers off: they reference
// tenants and users the test does not create. Needs a role allowed to set
// session_replication_role; skips otherwise.
func seedHostFixture(t *testing.T, dbm *database.DatabaseManager) {
	t.Helper()
	tx, err := dbm.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
		t.Skipf("cannot seed without foreign keys: %v", err)
	}

	account := func(id, provider, accountType, status string) string {
		return `INSERT INTO cloud_accounts (id, tenant, account_name, cloud_provider, account_type, status, created_by, updated_by)
		        VALUES ('` + id + `', '` + dbTenant + `', 'fixture-` + id[len(id)-2:] + `', '` + provider + `', '` + accountType + `', '` + status + `', '` + dbUser + `', '` + dbUser + `')`
	}
	host := func(id, tenant, accountID, provider, resourceID string, active bool) string {
		isActive := "true"
		if !active {
			isActive = "false"
		}
		return `INSERT INTO cloud_resourses (id, account, tenant, cloud_provider, region, resourse_id, name, type, is_active, status, meta)
		        VALUES ('` + id + `', '` + accountID + `', '` + tenant + `', '` + provider + `', 'us-east-1', '` + resourceID + `', '` + resourceID + `',
		                'compute-instance', ` + isActive + `, 'Active', '{"PrivateIpAddress": "10.0.0.1"}')`
	}
	scan := func(resourceID, accountID, age string) string {
		return `INSERT INTO vm_scan_status (cloud_resource_id, tenant_id, cloud_account_id, status, package_count, finding_count, scanned_at)
		        VALUES ('` + resourceID + `', '` + dbTenant + `', '` + accountID + `', 'completed', 10, 2, NOW() - INTERVAL '` + age + `')`
	}
	datasource := func(id, target string) []string {
		return []string{
			`INSERT INTO integrations (id, tenant_id, type, source, name, status)
			 VALUES ('` + id + `', '` + dbTenant + `', 'discovery', 'agent', 'fixture-` + id[len(id)-2:] + `', 'enabled')`,
			`INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role)
			 VALUES ('` + id + `', '` + dbVMAccount + `', '` + dbTenant + `', 'own')`,
			`INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role)
			 VALUES ('` + id + `', '` + target + `', '` + dbTenant + `', 'discovery_target')`,
		}
	}

	statements := []string{
		account(dbVMAccount, "SelfHosted", "vm", "active"),
		account(dbAWSAccount, "AWS", "cloud", "active"),
		account(dbOffAccount, "AWS", "cloud", "disabled"),

		host(rowFresh, dbTenant, dbVMAccount, "AWS", "i-fresh", true),
		host(rowStale, dbTenant, dbVMAccount, "AWS", "i-stale", true),
		host(rowRetired, dbTenant, dbVMAccount, "SelfHosted", "vm-10.0.0.9", false),
		host(rowNeverScan, dbTenant, dbVMAccount, "SelfHosted", "vm-10.0.0.8", true),
		host(rowInTarget, dbTenant, dbAWSAccount, "AWS", "i-in-target", true),
		host(rowInOff, dbTenant, dbOffAccount, "AWS", "i-in-off-account", true),
		host(rowOtherTenant, dbOtherTenant, dbVMAccount, "AWS", "i-other-tenant", true),

		scan(rowFresh, dbVMAccount, "1 hour"),
		scan(rowStale, dbVMAccount, "10 days"),
		scan(rowRetired, dbVMAccount, "1 hour"),
		scan(rowInTarget, dbAWSAccount, "2 hours"),
		scan(rowInOff, dbOffAccount, "1 hour"),
		scan(rowOtherTenant, dbVMAccount, "1 hour"),

		`INSERT INTO vm_package (tenant_id, cloud_account_id, cloud_resource_id, os_family, os_version, pkg_type, name, version)
		 VALUES ('` + dbTenant + `', '` + dbVMAccount + `', '` + rowFresh + `', 'ubuntu', '22.04', 'deb', 'openssl', '3.0.2')`,
	}
	statements = append(statements, datasource(dbDatasource, dbAWSAccount)...)
	statements = append(statements, datasource(dbOffDatasource, dbOffAccount)...)

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestFetchHostRows_ScopeAndFreshness(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	cleanupHostFixture(dbm)
	t.Cleanup(func() { cleanupHostFixture(dbm) })
	seedHostFixture(t, dbm)

	rows, err := fetchHostRows(context.Background(), dbTenant, dbVMAccount, 7)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(rows))
	byID := map[string]HostRow{}
	for _, r := range rows {
		got = append(got, r.ResourceID)
		byID[r.ResourceID] = r
	}
	sort.Strings(got)

	// Included: a fresh host filed in the VM account, and a fresh host filed in
	// the active discovery-target account. Excluded: a stale scan, a retired
	// placeholder, a host never scanned, a host in a disabled target account,
	// and a row belonging to another tenant.
	want := []string{"i-fresh", "i-in-target"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("hosts = %v, want %v", got, want)
	}

	fresh := byID["i-fresh"]
	if fresh.OSFamily != "ubuntu" || fresh.OSVersion != "22.04" {
		t.Errorf("os = %q/%q, want ubuntu/22.04", fresh.OSFamily, fresh.OSVersion)
	}
	if !fresh.PackageCount.Valid || fresh.PackageCount.Int64 != 10 || !fresh.FindingCount.Valid || fresh.FindingCount.Int64 != 2 {
		t.Errorf("counts = %+v/%+v", fresh.PackageCount, fresh.FindingCount)
	}
	if fresh.PrivateIP != "10.0.0.1" || fresh.Region != "us-east-1" || fresh.NBResourceID != rowFresh {
		t.Errorf("row = %+v", fresh)
	}
	// Newest scan first, so ConvertHostRows keeps the newest row per key.
	if rows[0].ResourceID != "i-fresh" {
		t.Errorf("rows not ordered newest scan first: %v", got)
	}
}
