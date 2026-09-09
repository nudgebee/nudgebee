package core

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/stretchr/testify/require"
)

// TestFindNodesByProviderIdentifier_DB covers the lookup that lets a cloud event
// find the node it is about.
//
// The mismatch it exists for: an event names its subject by the provider's
// identifier (i-0dcee3621b8456783), while the node is named from its Name tag
// (nudgebee-scenario-services-order). Measured on dev, 0 active nodes are named
// by instance id and 3 carry it as properties.resource_id — so every name-based
// lookup misses, and every caller degrades silently: no evidence, no
// correlation, "we don't have a service map for this one".
//
// Uses a throwaway table mirroring the columns the SQL touches, so it needs no
// graph fixtures or foreign keys (the convention pr_lifecycle_*_test.go set).
// DB-gated: skips when no database is reachable.
func TestFindNodesByProviderIdentifier_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		// REQUIRE_DB_TESTS is set only by the job that provisions a database, so a
		// missing one there is a failure rather than a silent skip.
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const table = "zz_kg_provider_identifier_test"
	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	mustExec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		id text PRIMARY KEY,
		tenant_id text NOT NULL,
		cloud_account_id text,
		node_type text NOT NULL,
		level text NOT NULL,
		is_active boolean NOT NULL DEFAULT true,
		properties jsonb NOT NULL DEFAULT '{}'::jsonb
	)`, table))
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
	})

	const (
		tenant  = "t-1"
		account = "a-1"
		instID  = "i-0dcee3621b8456783"
		arn     = "arn:aws:ec2:us-east-1:864186153326:instance/" + instID
	)
	insert := func(id, acct, nodeType, level string, active bool, props string) {
		mustExec(fmt.Sprintf(
			`INSERT INTO %s (id, tenant_id, cloud_account_id, node_type, level, is_active, properties)
			 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`, table),
			id, tenant, acct, nodeType, level, active, props)
	}

	// The node the event is about: named by its tag, identified by instance id.
	insert("node-order", account, string(NodeTypeComputeInstance), "Tenant", true,
		fmt.Sprintf(`{"name":"nudgebee-scenario-services-order","resource_id":%q,"arn":%q}`, instID, arn))
	// Same instance id, but retired — a rename forks node identity because
	// unique_key embeds the display name, so stale rows for one machine are
	// normal. Matching one would seed the blast radius on a dead node.
	insert("node-order-old", account, string(NodeTypeComputeInstance), "Tenant", false,
		fmt.Sprintf(`{"name":"nb-demo-web","resource_id":%q}`, instID))
	// Another tenant's resource that happens to share the id.
	mustExec(fmt.Sprintf(
		`INSERT INTO %s (id, tenant_id, cloud_account_id, node_type, level, is_active, properties)
		 VALUES ('node-other-tenant','t-2',$1,$2,'Tenant',true,$3::jsonb)`, table),
		account, string(NodeTypeComputeInstance),
		fmt.Sprintf(`{"name":"someone-elses","resource_id":%q}`, instID))

	t.Run("resolves by instance id", func(t *testing.T) {
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, account, instID)
		require.NoError(t, err)
		require.Equal(t, []string{"node-order"}, got,
			"the active node carrying this instance id must resolve; inactive rows and "+
				"other tenants must not")
	})

	t.Run("resolves by arn", func(t *testing.T) {
		// A CloudWatch alarm carries an ARN in service_key, so both forms have to work.
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, account, arn)
		require.NoError(t, err)
		require.Equal(t, []string{"node-order"}, got)
	})

	t.Run("empty identifier returns nothing rather than every node", func(t *testing.T) {
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, account, "")
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("unknown identifier resolves nothing", func(t *testing.T) {
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, account, "i-000000000000")
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("ambiguous identifier is reported, not guessed", func(t *testing.T) {
		// Two live nodes claiming one identifier: we cannot say which resource the
		// event is about. The caller requires exactly one, so this must return both
		// and let it decline rather than seeding on an arbitrary pick.
		insert("node-dupe", account, string(NodeTypeComputeInstance), "Tenant", true,
			fmt.Sprintf(`{"name":"duplicate-claim","resource_id":%q}`, instID))
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, account, instID)
		require.NoError(t, err)
		require.Len(t, got, 2, "both claimants must be returned so the caller can refuse to guess")
		mustExec(fmt.Sprintf(`DELETE FROM %s WHERE id = 'node-dupe'`, table))
	})

	t.Run("account scoping", func(t *testing.T) {
		got, err := findNodesByProviderIdentifierInTable(dbms, table, tenant, "other-account", instID)
		require.NoError(t, err)
		require.Empty(t, got, "a node in a different cloud account must not resolve")

		// No account given means tenant-wide, which is what the evidence path uses.
		got, err = findNodesByProviderIdentifierInTable(dbms, table, tenant, "", instID)
		require.NoError(t, err)
		require.Equal(t, []string{"node-order"}, got)
	})
}
