package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	kgcore "nudgebee/services/knowledge_graph/core"

	"github.com/stretchr/testify/require"
)

// TestCascadePipeline_DB runs a real dependency cascade through the real triage
// pipeline against a real schema, and asserts what an operator is supposed to
// get out of it.
//
// Everything that broke this week broke *between* components, where no unit test
// looks: the evidence lookup matched on a name the event does not carry, the
// property allowlist dropped the identifier that lookup needed, and the blast
// radius seed re-resolved the subject by name a third time. Each component's own
// tests passed throughout. The only thing that would have caught them is running
// an event end to end and checking the answer.
//
// Topology and events are copied from the dev account (the
// nudgebee-scenario-services cascade), so a failure here means the product is
// wrong, not that a fixture drifted.
//
// Local setup — full schema in about 40 seconds:
//
//	docker run -d --name nb-test-pg -e POSTGRES_PASSWORD=postgres \
//	  -e POSTGRES_DB=appdb -p 55432:5432 postgres:15
//	cd api-server/migrations && atlas migrate apply -c file://atlas.hcl --env default \
//	  --url 'postgres://postgres:postgres@localhost:55432/appdb?sslmode=disable' --tx-mode file
//	cd api-server/services && APP_DATABASE_URL='postgres://postgres:postgres@localhost:55432/appdb?sslmode=disable' \
//	  REQUIRE_DB_TESTS=true go test ./triage/ -run '_DB$' -v
func TestCascadePipeline_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	db := dbms.Db
	ctx := context.Background()

	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		accountID = "22222222-2222-2222-2222-222222222222"

		orderID     = "i-0dcee3621b8456783"
		paymentID   = "i-0b079820a95b1517a"
		inventoryID = "i-00e845d10772a9058"
		databaseID  = "i-0150dbd583caa0e69"
	)

	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}

	// Fixtures: a user (tenant.created_by references it), a tenant and a cloud
	// account. The severity/source/status lookup tables the events FKs point at
	// are seeded by the migrations themselves.
	const userID = "33333333-3333-3333-3333-333333333333"
	mustExec(`INSERT INTO users (id, username, display_name, status, created_at, updated_at)
	          VALUES ($1,'e2e@test.local','e2e','active',now(),now()) ON CONFLICT (id) DO NOTHING`, userID)
	mustExec(`INSERT INTO tenant (id, name, created_at, updated_at, created_by, updated_by)
	          VALUES ($1,'e2e',now(),now(),$2,$2) ON CONFLICT (id) DO NOTHING`, tenantID, userID)
	mustExec(`INSERT INTO cloud_accounts
	          (id, account_name, created_at, created_by, updated_at, updated_by, tenant, status, account_type, etl_attempt, account_env)
	          VALUES ($1,'e2e-aws',now(),$3,now(),$3,$2,'active','aws',0,'non_prod')
	          ON CONFLICT (id) DO NOTHING`, accountID, tenantID, userID)

	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM event_correlations WHERE tenant_id = $1`,
			`DELETE FROM event_duplicates WHERE tenant_id = $1`,
			`DELETE FROM events WHERE tenant = $1`,
			`DELETE FROM knowledge_graph_edge WHERE tenant_id = $1`,
			`DELETE FROM knowledge_graph_node WHERE tenant_id = $1`,
		} {
			_, _ = db.Exec(q, tenantID)
		}
	})
	// Start clean so a previous run's chains cannot decide this one's outcome —
	// exactly the trap that made the dev cascade ungroupable for 24 hours.
	for _, q := range []string{
		`DELETE FROM event_correlations WHERE tenant_id = $1`,
		`DELETE FROM event_duplicates WHERE tenant_id = $1`,
		`DELETE FROM events WHERE tenant = $1`,
	} {
		mustExec(q, tenantID)
	}

	// --- the graph, as the collector built it on dev -------------------------
	type svc struct{ node, name, inst string }
	services := []svc{
		{"25771ecf-d1f5-55ba-b376-28c550755252", "nudgebee-scenario-services-order", orderID},
		{"0ff0c3a8-d5cd-5316-b638-9bbdf8835d39", "nudgebee-scenario-services-payment", paymentID},
		{"fcf379a8-c0aa-5a08-a533-774bb4a13a0e", "nudgebee-scenario-services-inventory", inventoryID},
		{"ce0ad7f1-8449-5965-a616-665c803e7bee", "nudgebee-scenario-services-database", databaseID},
	}
	kgNodes := make([]kgcore.KgNode, 0, len(services))
	for _, s := range services {
		// The node is named from the Name tag; the instance id lives in
		// properties. That gap is the whole reason this test exists.
		props := fmt.Sprintf(`{"name":%q,"region":"us-east-1","status":"Active","resource_id":%q,"arn":%q}`,
			s.name, s.inst, "arn:aws:ec2:us-east-1:864186153326:instance/"+s.inst)
		mustExec(`INSERT INTO knowledge_graph_node
		          (id, tenant_id, cloud_account_id, node_type, level, is_active, query_attributes, properties, unique_key)
		          VALUES ($1,$2,$3,'ComputeInstance','Tenant',true,
		                  jsonb_build_object('name',$4::text), $5::jsonb, $6)`,
			s.node, tenantID, accountID, s.name, props,
			"aws:"+accountID+":us-east-1:ComputeInstance:vpc-1:"+s.name)

		var p map[string]any
		require.NoError(t, json.Unmarshal([]byte(props), &p))
		kgNodes = append(kgNodes, kgcore.KgNode{
			ID: s.node, NodeType: "ComputeInstance", Properties: p,
			UniqueKey: "aws:" + accountID + ":us-east-1:ComputeInstance:vpc-1:" + s.name,
		})
	}

	// payment→order, inventory→order, and all three→database: the CALLS edges
	// VPC flow logs produced.
	type edge struct{ from, to string }
	edges := []edge{
		{services[1].node, services[0].node},
		{services[2].node, services[0].node},
		{services[0].node, services[3].node},
		{services[1].node, services[3].node},
		{services[2].node, services[3].node},
	}
	edgeCards := make([]any, 0, len(edges))
	for i, e := range edges {
		mustExec(`INSERT INTO knowledge_graph_edge
		          (id, tenant_id, cloud_account_id, source_node_id, destination_node_id,
		           relationship_type, level, is_active, properties, contributing_sources)
		          VALUES ($1,$2,$3,$4,$5,'CALLS','Tenant',true,'{}'::jsonb,
		                  '[{"source":"aws-vpc-flow"}]'::jsonb)`,
			fmt.Sprintf("00000000-0000-0000-0000-0000000000%02d", i+1),
			tenantID, accountID, e.from, e.to)
		edgeCards = append(edgeCards, map[string]any{
			"source_node_id": e.from, "dest_node_id": e.to, "relationship_type": "CALLS",
			"properties": map[string]any{"contributing_sources": []any{"aws-vpc-flow"}},
		})
	}

	// The evidence card, built by the real writer projection — not a hand-rolled
	// copy of what it emits, so a change to the allowlist breaks this test.
	projected, err := json.Marshal(kgcore.ToEvidenceNodes(kgNodes))
	require.NoError(t, err)
	var nodeCards []any
	require.NoError(t, json.Unmarshal(projected, &nodeCards))
	evidenceJSON, err := json.Marshal([]any{map[string]any{
		"type": "knowledge_graph", "nodes": nodeCards, "edges": edgeCards,
		"additional_info": map[string]any{"action_name": "knowledge_graph_service_map"},
	}})
	require.NoError(t, err)

	// --- the cascade, in the order dev received it ---------------------------
	base := time.Now().UTC().Add(-10 * time.Minute)
	type alarm struct {
		id, inst, name string
		at             time.Time
	}
	cascade := []alarm{
		{"aaaa0001-0000-0000-0000-000000000001", inventoryID, "inventory-down", base},
		{"aaaa0002-0000-0000-0000-000000000002", paymentID, "payment-cpu", base.Add(9 * time.Second)},
		{"aaaa0003-0000-0000-0000-000000000003", inventoryID, "inventory-cpu", base.Add(12 * time.Second)},
		{"aaaa0004-0000-0000-0000-000000000004", orderID, "order-cpu", base.Add(60 * time.Second)},
		{"aaaa0005-0000-0000-0000-000000000005", paymentID, "payment-down", base.Add(97 * time.Second)},
		{"aaaa0006-0000-0000-0000-000000000006", orderID, "order-down", base.Add(114 * time.Second)},
	}

	for _, a := range cascade {
		fp := "arn:aws:cloudwatch:us-east-1:864186153326:alarm:nudgebee-scenario-services-" + a.name
		// service_key in the synthetic-ARN form CloudWatch alarm events carry.
		svcKey := "arn:aws:ec2:us-east-1:864186153326:compute-instance:" + a.inst
		mustExec(`INSERT INTO events
		          (id, created_at, updated_at, finding_id, title, aggregation_key, finding_type,
		           priority, subject_name, subject_type, subject_namespace, cluster, evidences,
		           tenant, cloud_account_id, fingerprint, service_key, starts_at, source, status)
		          VALUES ($1,now(),now(),$2,$3,$4,'issue','HIGH',$5,'compute-instance','AmazonEC2',
		                  'dev-aws',$6::jsonb,$7,$8,$9,$10,$11,'AWS_CloudWatch_Alarm','FIRING')`,
			a.id, a.id, "CloudWatch Alarm: "+a.name, "nudgebee-scenario-services-"+a.name,
			a.inst, string(evidenceJSON), tenantID, accountID, fp, svcKey, a.at)

		var ev models.Event
		require.NoError(t, db.Get(&ev, `SELECT * FROM events WHERE id = $1`, a.id))
		require.NoError(t, ProcessEvent(ctx, db, &ev), "ProcessEvent(%s)", a.name)
	}

	// --- what an operator must get ------------------------------------------
	t.Run("cross-instance dependency hops", func(t *testing.T) {
		type row struct {
			Src      string `db:"src"`
			Dst      string `db:"dst"`
			Type     string `db:"correlation_type"`
			Distance int    `db:"dependency_distance"`
		}
		var rows []row
		require.NoError(t, db.Select(&rows, `
			SELECT e1.subject_name AS src, e2.subject_name AS dst,
			       c.correlation_type, c.dependency_distance
			FROM event_correlations c
			JOIN events e1 ON e1.id = c.event_id
			JOIN events e2 ON e2.id = c.related_event_id
			WHERE c.tenant_id = $1 AND c.dependency_distance > 0
			  AND e1.subject_name <> e2.subject_name`, tenantID))

		require.NotEmpty(t, rows,
			"no cross-instance dependency hop. The graph holds payment CALLS order and "+
				"both events resolve to those instances, so this is the failure that made "+
				"AWS correlation look broken for two days — an event that cannot be matched "+
				"to its node scores every pair at distance 0.")
		// payment and inventory both call order and the database but not each
		// other, so 2 is the correct answer for that pair — asserting 1 everywhere
		// would be asserting a wrong topology.
		direct := 0
		for _, r := range rows {
			require.LessOrEqual(t, r.Distance, 2,
				"%s -> %s at distance %d: nothing in this topology is more than two hops apart",
				r.Src, r.Dst, r.Distance)
			if r.Distance == 1 {
				direct++
			}
		}
		require.Positive(t, direct,
			"every pair came back further than one hop, so no direct CALLS edge was "+
				"used — the graph is being traversed but the events are matching the "+
				"wrong nodes")
		t.Logf("%d cross-instance hops (%d direct)", len(rows), direct)
	})

	t.Run("same-subject alarms group under one leader", func(t *testing.T) {
		// order-cpu and order-down are one machine with one problem, 54s apart.
		// Showing them as two unrelated incidents is the alert-fatigue case
		// incident grouping exists to prevent.
		var n int
		require.NoError(t, db.Get(&n, `
			SELECT count(*) FROM event_correlations c
			JOIN events e1 ON e1.id = c.event_id
			JOIN events e2 ON e2.id = c.related_event_id
			WHERE c.tenant_id = $1 AND c.correlation_type = 'same_incident'
			  AND e1.subject_name = e2.subject_name`, tenantID))
		require.Positive(t, n,
			"two alarms on one instance, a minute apart, were not grouped")
	})
}
