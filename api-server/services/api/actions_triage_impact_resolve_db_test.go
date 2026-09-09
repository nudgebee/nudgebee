package api

import (
	"fmt"
	"log/slog"
	"os"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/require"
)

// TestResolveEventSubjectNodeID_DB covers the seed lookup behind the blast
// radius, which decides whether the investigate screen can say anything about
// what an alert affected.
//
// Until this was fixed it searched by name only. A CloudWatch alarm names its
// subject by the provider's identifier (i-0dcee3621b8456783) while the node is
// named from its Name tag (nudgebee-scenario-services-order), so it resolved
// nothing and the panel reported "we don't have a service map for this one" —
// on events that had a knowledge-graph card with the full topology in it.
// Measured on dev: 0 active nodes named by instance id, 3 carrying it as
// properties.resource_id.
//
// DB-gated; see triage.TestCascadePipeline_DB for the local Postgres setup.
// Lives in its own file: actions_triage_impact_seed_test.go holds the pure-logic
// seed tests and needs no database.
func TestResolveEventSubjectNodeID_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}

	const (
		tenantID  = "44444444-4444-4444-4444-444444444444"
		accountID = "55555555-5555-5555-5555-555555555555"
		instID    = "i-0dcee3621b8456783"
		nodeID    = "66666666-6666-6666-6666-666666666666"
		nodeName  = "nudgebee-scenario-services-order"
	)

	mustExec := func(q string, args ...any) {
		_, e := dbms.Db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM knowledge_graph_node WHERE tenant_id = $1`, tenantID)
	})
	_, _ = dbms.Db.Exec(`DELETE FROM knowledge_graph_node WHERE tenant_id = $1`, tenantID)

	mustExec(`INSERT INTO knowledge_graph_node
	          (id, tenant_id, cloud_account_id, node_type, level, is_active, query_attributes, properties, unique_key)
	          VALUES ($1,$2,$3,'ComputeInstance','Tenant',true,
	                  jsonb_build_object('name',$4::text), $5::jsonb, $6)`,
		nodeID, tenantID, accountID, nodeName,
		fmt.Sprintf(`{"name":%q,"resource_id":%q,"arn":"arn:aws:ec2:us-east-1:864186153326:instance/%s"}`,
			nodeName, instID, instID),
		"aws:"+accountID+":us-east-1:ComputeInstance:vpc-1:"+nodeName)

	kg := core.NewService(
		security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil),
		slog.Default(), dbms)

	t.Run("cloud alarm resolves by instance id", func(t *testing.T) {
		// The event as CloudWatch delivers it: subject is the instance id, and
		// the namespace is the provider's service code rather than a k8s namespace.
		got, namespaced, ok := resolveEventSubjectNodeID(
			kg, tenantID, accountID, instID, "AmazonEC2", "compute-instance", "", "")
		require.True(t, ok,
			"the instance did not resolve, so the blast radius reports no service map "+
				"even though the graph holds this node and its dependencies")
		require.Equal(t, nodeID, got)
		require.False(t, namespaced,
			"cloud nodes carry no namespace; claiming one makes the seed key disagree "+
				"with the topology map and the impact tiers come back empty")
	})

	t.Run("name still resolves", func(t *testing.T) {
		// The pre-existing path must keep working — this is an added fallback,
		// not a replacement.
		got, _, ok := resolveEventSubjectNodeID(
			kg, tenantID, accountID, nodeName, "", "compute-instance", "", "")
		require.True(t, ok)
		require.Equal(t, nodeID, got)
	})

	t.Run("unknown subject stays unresolved", func(t *testing.T) {
		// Reporting "unknown coverage" is the honest answer; inventing a seed
		// would produce a blast radius for the wrong resource.
		_, _, ok := resolveEventSubjectNodeID(
			kg, tenantID, accountID, "i-000000000000", "AmazonEC2", "compute-instance", "", "")
		require.False(t, ok)
	})
}
