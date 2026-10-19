package vmservicemap

// DB-gated test for ResolveStore: the account's vm_agent Prometheus
// datasource, and which one wins when there are several. Skips cleanly when
// no Postgres is reachable.

import (
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"

	"github.com/lib/pq"
)

const (
	rsTenant      = "6e2f0000-0000-4000-8000-000000000001"
	rsOtherTenant = "6e2f0000-0000-4000-8000-000000000002"
	rsVMAccount   = "6e2f0000-0000-4000-8000-0000000000a1"
	rsUser        = "6e2f0000-0000-4000-8000-0000000000ff"
)

func cleanupStoreFixture(dbm *database.DatabaseManager, integrationIDs []string) {
	_, _ = dbm.Exec(`DELETE FROM integration_config_values WHERE integration_id = ANY($1::uuid[])`, pq.Array(integrationIDs))
	_, _ = dbm.Exec(`DELETE FROM integrations_cloud_accounts WHERE integration_id = ANY($1::uuid[])`, pq.Array(integrationIDs))
	_, _ = dbm.Exec(`DELETE FROM integrations WHERE id = ANY($1::uuid[])`, pq.Array(integrationIDs))
	_, _ = dbm.Exec(`DELETE FROM cloud_accounts WHERE id = $1::uuid`, rsVMAccount)
}

type storeFixtureIntegration struct {
	id              string
	accountID       string
	tenantID        string
	datasourceKey   string
	agentType       string
	connectionMode  string // "" omits the config row entirely
	status          string
	isDefaultMetric bool
}

func seedStoreFixture(t *testing.T, dbm *database.DatabaseManager, integrations []storeFixtureIntegration) {
	t.Helper()
	tx, err := dbm.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
		t.Skipf("cannot seed without foreign keys: %v", err)
	}

	statements := []string{
		`INSERT INTO cloud_accounts (id, tenant, account_name, cloud_provider, account_type, status, created_by, updated_by)
		 VALUES ('` + rsVMAccount + `', '` + rsTenant + `', 'fixture-vm', 'SelfHosted', 'vm', 'active', '` + rsUser + `', '` + rsUser + `')`,
	}
	for _, in := range integrations {
		statements = append(statements,
			`INSERT INTO integrations (id, tenant_id, type, source, name, status)
			 VALUES ('`+in.id+`', '`+in.tenantID+`', 'prometheus', 'agent', 'fixture-`+in.id+`', '`+in.status+`')`,
			`INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role, default_metrics_provider)
			 VALUES ('`+in.id+`', '`+in.accountID+`', '`+in.tenantID+`', 'own', `+boolLiteral(in.isDefaultMetric)+`)`,
		)
		if in.connectionMode != "" {
			statements = append(statements,
				`INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by)
				 VALUES (gen_random_uuid(), '`+in.id+`', 'connection_mode', '`+in.connectionMode+`', false, NOW(), NOW(), '`+rsUser+`', '`+rsUser+`')`,
			)
		}
		if in.datasourceKey != "" {
			statements = append(statements,
				`INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by)
				 VALUES (gen_random_uuid(), '`+in.id+`', 'datasource_key', '`+in.datasourceKey+`', false, NOW(), NOW(), '`+rsUser+`', '`+rsUser+`')`,
			)
		}
		if in.agentType != "" {
			statements = append(statements,
				`INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by)
				 VALUES (gen_random_uuid(), '`+in.id+`', 'agent_type', '`+in.agentType+`', false, NOW(), NOW(), '`+rsUser+`', '`+rsUser+`')`,
			)
		}
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func boolLiteral(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestResolveStore_NoIntegrationConfigured(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001001"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		// Right account, but connection_mode is not vm_agent — must not match.
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds", connectionMode: "direct", status: "enabled"},
	})

	_, err := ResolveStore(rsVMAccount, rsTenant)
	if err == nil {
		t.Fatal("expected ErrNoStore, got nil")
	}
}

func TestResolveStore_SingleIntegrationResolves(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001002"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds-only", agentType: "proxy", connectionMode: "vm_agent", status: "enabled"},
	})

	store, err := ResolveStore(rsVMAccount, rsTenant)
	if err != nil {
		t.Fatal(err)
	}
	if store.DatasourceKey != "ds-only" || store.AgentType != "proxy" {
		t.Errorf("store = %+v, want ds-only/proxy", store)
	}
}

// A user-created integration has no datasource_key config row (the relay only
// writes one on its own mirror row), so the store falls back to the integration
// id, as the metric-query loader does. Regression: this used to be ErrNoStore.
func TestResolveStore_NoDatasourceKeyFallsBackToIntegrationID(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001008"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, connectionMode: "vm_agent", status: "enabled"},
	})

	store, err := ResolveStore(rsVMAccount, rsTenant)
	if err != nil {
		t.Fatal(err)
	}
	if store.DatasourceKey != ids[0] {
		t.Errorf("DatasourceKey = %q, want the integration id %q", store.DatasourceKey, ids[0])
	}
}

// TestResolveStore_TwoIntegrationsDefaultWins is the regression test for the
// bug Codex review found: the default-metrics-provider flag lives on
// integrations_cloud_accounts (the account-link row), set by the integration
// save path (integrations/core/integration_config.go) — not in
// integration_config_values, which ResolveStore used to (wrongly) query. That
// join could never match, so this exact scenario (several vm_agent
// integrations, one marked default) always fell through to the "none
// settled it" error, even though a default was in fact set.
func TestResolveStore_TwoIntegrationsDefaultWins(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001003", "6e2f0000-0000-4000-8000-000000001004"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds-a", agentType: "proxy", connectionMode: "vm_agent", status: "enabled", isDefaultMetric: false},
		{id: ids[1], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds-b", agentType: "proxy", connectionMode: "vm_agent", status: "enabled", isDefaultMetric: true},
	})

	store, err := ResolveStore(rsVMAccount, rsTenant)
	if err != nil {
		t.Fatal(err)
	}
	if store.DatasourceKey != "ds-b" {
		t.Errorf("DatasourceKey = %q, want ds-b (the one marked default)", store.DatasourceKey)
	}
}

func TestResolveStore_ScopedToTenant(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001007"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds", connectionMode: "vm_agent", status: "enabled"},
	})

	// Right account, wrong tenant — the integration belongs to rsTenant.
	if _, err := ResolveStore(rsVMAccount, rsOtherTenant); err == nil {
		t.Fatal("expected ErrNoStore for the wrong tenant, got nil")
	}
}

func TestResolveStore_TwoIntegrationsNoDefaultIsAmbiguous(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ids := []string{"6e2f0000-0000-4000-8000-000000001005", "6e2f0000-0000-4000-8000-000000001006"}
	cleanupStoreFixture(dbm, ids)
	t.Cleanup(func() { cleanupStoreFixture(dbm, ids) })
	seedStoreFixture(t, dbm, []storeFixtureIntegration{
		{id: ids[0], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds-a", connectionMode: "vm_agent", status: "enabled"},
		{id: ids[1], accountID: rsVMAccount, tenantID: rsTenant, datasourceKey: "ds-b", connectionMode: "vm_agent", status: "enabled"},
	})

	_, err := ResolveStore(rsVMAccount, rsTenant)
	if err == nil {
		t.Fatal("expected an ambiguous-default error, got nil")
	}
}
