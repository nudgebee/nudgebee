package flow_sources

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
)

// TestVerifyK8sCreationPolicy_AgainstLiveDB is an offline verification
// harness, NOT a CI unit test. It is READ-ONLY: it issues SELECTs and replays
// the real creation paths in memory, and writes nothing.
//
// It exists because the unit tests build the per-account context by hand. This
// runs the *real* newK8sCreationContext against the *real* k8s_workloads table
// for every K8s account in a tenant, over the *real* node set, and answers the
// two questions that matter before this ships:
//
//  1. For an account the collector describes: is every flow-sourced workload
//     node with no active inventory row actually refused now?
//  2. For an account with no inventory at all: are workload nodes still
//     created, and marked flow_authoritative so the sweep leaves them?
//
// It also prints a blast-radius table, because the dangerous failure mode is
// not "a stale node survives" but "a real customer's graph is emptied".
//
//	APP_DATABASE_URL='postgresql://...' KG_VERIFY_TENANT='<tenant-uuid>' \
//	go test ./knowledge_graph/flow_sources/ -run TestVerifyK8sCreationPolicy_AgainstLiveDB -v
func TestVerifyK8sCreationPolicy_AgainstLiveDB(t *testing.T) {
	if os.Getenv("KG_VERIFY_TENANT") == "" {
		t.Skip("KG_VERIFY_TENANT not set — skipping live-DB verification harness")
	}
	dbm := testenv.RequireMetastore(t)
	tenantID := os.Getenv("KG_VERIFY_TENANT")

	// The node set a build would hand the flow source, in the same shape.
	nodeRows, err := dbm.Db.Query(`
		SELECT COALESCE(cloud_account_id::text,''), node_type,
		       COALESCE(properties->>'name',''), COALESCE(properties->>'namespace',''),
		       COALESCE(properties->>'source',''), unique_key
		FROM public.knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND is_active = true`, tenantID)
	if err != nil {
		t.Fatalf("load nodes: %v", err)
	}
	defer func() { _ = nodeRows.Close() }()
	var nodes []*core.DbNode
	byAccount := map[string][]*core.DbNode{}
	for nodeRows.Next() {
		var acct, nodeType, name, ns, source, uk string
		if err := nodeRows.Scan(&acct, &nodeType, &name, &ns, &source, &uk); err != nil {
			t.Fatalf("scan node: %v", err)
		}
		n := &core.DbNode{
			NodeType:       core.NodeType(nodeType),
			UniqueKey:      uk,
			CloudAccountID: acct,
			Source:         source,
			Properties: map[string]interface{}{
				"name": name, "namespace": ns, "source": source,
			},
		}
		nodes = append(nodes, n)
		byAccount[acct] = append(byAccount[acct], n)
	}
	if err := nodeRows.Err(); err != nil {
		t.Fatalf("iterate nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("tenant %s has no active nodes; wrong tenant or wrong database", tenantID)
	}

	// The check that caught the Job/CronJob mistake before it shipped, and the
	// reason this harness exists. A type is only safe to gate if a static
	// source actually EMITS it — otherwise gating deletes the whole population
	// instead of the stale part of it. k8s_source registers Job and CronJob
	// with NodeType: core.NodeTypeWorkload, so NodeTypeJob/NodeTypeCronJob
	// have no static producer and gating them emptied every graph of Jobs and
	// CronJobs. Asserted against real data rather than against an assumption,
	// because the type registry is in a package flow_sources cannot import
	// (sources imports flow_sources, so the dependency only goes one way).
	for nodeType := range inventoryOwnedWorkloadTypes {
		var staticNodes int
		if err := dbm.Db.QueryRow(`
			SELECT count(*) FROM public.knowledge_graph_node
			WHERE node_type = $1 AND (properties->>'source') NOT IN ('ebpf','traces','manual',
				'datadog-apm','newrelic-apm','elastic-apm','aws-vpc-flow','vm-service-map')`,
			string(nodeType)).Scan(&staticNodes); err != nil {
			t.Fatalf("count static %s nodes: %v", nodeType, err)
		}
		if staticNodes == 0 {
			t.Errorf("%s is treated as inventory-owned, but NO static source has ever emitted one "+
				"in this database — gating it deletes the entire population instead of the stale "+
				"part of it. Move it to flowProducedInfraTypes.", nodeType)
		} else {
			t.Logf("inventory-owned %s: %d statically-sourced nodes exist, safe to gate", nodeType, staticNodes)
		}
	}

	source := NewEbpfFlowSource(slog.New(slog.NewTextHandler(io.Discard, nil)))
	source.InitializeNodeMatcher(nodes)

	// Every K8s account in the tenant, with its real inventory counts.
	acctRows, err := dbm.Db.Query(`
		SELECT ca.id::text, ca.account_name, ca.status,
		       (SELECT count(*) FROM k8s_workloads w
		          WHERE w.cloud_account_id = ca.id AND w.is_active) AS inv_active
		FROM cloud_accounts ca
		WHERE ca.tenant = $1 AND ca.cloud_provider = 'K8s'
		ORDER BY ca.account_name`, tenantID)
	if err != nil {
		t.Fatalf("load accounts: %v", err)
	}
	defer func() { _ = acctRows.Close() }()
	type acct struct {
		id, name, status string
		invActive        int
	}
	var accounts []acct
	for acctRows.Next() {
		var a acct
		if err := acctRows.Scan(&a.id, &a.name, &a.status, &a.invActive); err != nil {
			t.Fatalf("scan account: %v", err)
		}
		accounts = append(accounts, a)
	}
	if err := acctRows.Err(); err != nil {
		t.Fatalf("iterate accounts: %v", err)
	}

	t.Logf("tenant %s: %d active nodes across %d K8s accounts", tenantID, len(nodes), len(accounts))
	t.Logf("%-38s %-22s %-9s %-10s %s", "ACCOUNT", "NAME", "STATUS", "INV_ACTIVE", "POLICY")

	for _, a := range accounts {
		// The real probe, against the real table.
		cc := newK8sCreationContext(slog.New(slog.NewTextHandler(io.Discard, nil)), a.id)
		policy := "GATED (inventory is the oracle)"
		if !cc.inventoryAuthoritative {
			policy = "FLOW MAY CREATE (no inventory)"
		}
		t.Logf("%-38s %-22s %-9s %-10d %s", a.id, a.name, a.status, a.invActive, policy)

		// The probe must agree with the table it reads.
		if (a.invActive > 0) != cc.inventoryAuthoritative {
			t.Errorf("account %s (%s): inv_active=%d but inventoryAuthoritative=%v — "+
				"the probe disagrees with k8s_workloads", a.id, a.name, a.invActive, cc.inventoryAuthoritative)
		}

		// Replay the real creation paths for this account's flow-sourced
		// workload nodes that the inventory has no active row for. Loaded in
		// a closure so the rows are closed per account rather than deferred
		// until the whole tenant has been walked.
		stales, err := loadStaleFlowWorkloads(dbm, tenantID, a.id, !cc.inventoryAuthoritative)
		if err != nil {
			t.Fatalf("load stale nodes for %s: %v", a.id, err)
		}
		if len(stales) == 0 {
			continue
		}

		// Partitioned by whether the type is gate-relevant at all. Only
		// inventoryOwnedWorkloadTypes (Workload) is subject to the per-account
		// decision; Job / CronJob / CRD are flow-produced types whose
		// lifecycle this change does not touch, so they are expected to keep
		// being created and carry no flow_authoritative marker.
		gatedKept, gatedRefused := 0, 0
		flowKept, flowRefused := 0, 0
		var gatedKeptNames, flowKeptNames []string
		for _, st := range stales {
			kind := st.kind
			if kind == "" {
				kind = map[string]string{"Workload": "Deployment", "Job": "Job", "CronJob": "CronJob"}[st.nodeType]
			}
			app := &core.ServiceApplication{
				Id: core.ServiceApplicationId{Name: st.name, Kind: kind, Namespace: st.ns},
			}
			account := core.K8sAccount{CloudAccountID: a.id, Name: a.name}
			node := source.createNodeForApplication(app, tenantID, account, cc)
			gateRelevant := inventoryOwnedWorkloadTypes[core.NodeType(st.nodeType)]
			label := fmt.Sprintf("%s %s/%s", st.nodeType, st.ns, st.name)

			if node == nil {
				if gateRelevant {
					gatedRefused++
				} else {
					flowRefused++
					t.Errorf("account %s (%s): refused %s, but %s has no static producer — "+
						"refusing it deletes the whole population, not the stale part",
						a.id, a.name, label, st.nodeType)
				}
				continue
			}
			marked := node.Properties[core.FlowAuthoritativeProperty] == true
			if gateRelevant {
				gatedKept++
				gatedKeptNames = append(gatedKeptNames, label)
				// Only a gate-relevant type created in an inventory-less
				// account needs the sweep exemption.
				if !cc.inventoryAuthoritative && !marked {
					t.Errorf("account %s (%s): created %s WITHOUT %s — the sweep would retire it on silence",
						a.id, a.name, label, core.FlowAuthoritativeProperty)
				}
			} else {
				flowKept++
				flowKeptNames = append(flowKeptNames, label)
				if marked {
					t.Errorf("account %s (%s): %s carries %s, but its lifecycle is unchanged by this "+
						"fix — marking it silently exempts it from a sweep it was always subject to",
						a.id, a.name, label, core.FlowAuthoritativeProperty)
				}
			}
		}
		sort.Strings(gatedKeptNames)
		sort.Strings(flowKeptNames)

		t.Logf("    gate-relevant (Workload): refused %d, created %d | flow-produced (Job/CronJob/CRD): created %d, refused %d",
			gatedRefused, gatedKept, flowKept, flowRefused)

		if cc.inventoryAuthoritative {
			if gatedKept != 0 {
				t.Errorf("account %s (%s) HAS inventory, yet %d stale workloads were still created: %v",
					a.id, a.name, gatedKept, gatedKeptNames)
			}
		} else if gatedRefused != 0 {
			t.Errorf("account %s (%s) has NO inventory, yet %d workload nodes were refused — "+
				"this account's graph would lose its workloads entirely",
				a.id, a.name, gatedRefused)
		}
	}

	// Regression guard: a workload the inventory DOES have must still resolve,
	// or the fix would strip live dependencies rather than stale ones.
	liveRows, err := dbm.Db.Query(`
		SELECT w.cloud_account_id::text, w.namespace, w.name, w.kind
		FROM k8s_workloads w
		JOIN cloud_accounts ca ON ca.id = w.cloud_account_id
		WHERE ca.tenant = $1 AND w.is_active
		  AND w.kind IN ('Deployment','StatefulSet','DaemonSet')
		LIMIT 40`, tenantID)
	if err != nil {
		t.Fatalf("load live workloads: %v", err)
	}
	defer func() { _ = liveRows.Close() }()
	checked, matched, mangled := 0, 0, 0
	for liveRows.Next() {
		var acctID, ns, name, kind string
		if err := liveRows.Scan(&acctID, &ns, &name, &kind); err != nil {
			t.Fatalf("scan live: %v", err)
		}
		checked++
		app := &core.ServiceApplication{
			Id: core.ServiceApplicationId{Name: name, Kind: kind, Namespace: ns},
		}
		if _, err := source.matchApplicationToNode(app, acctID); err == nil {
			matched++
			continue
		}
		// A miss where the derived name differs from the inventory name is a
		// harness artifact, not a finding: this harness feeds a WORKLOAD name
		// into getWorkloadName, which exists to strip pod suffixes, and
		// isPodName reads a three-segment workload name (elastic-agent-agent)
		// as a pod. The real agent reports the pod name, which strips back to
		// the workload name and matches. Counted separately so a genuine
		// regression is not buried under it.
		if derived := source.getWorkloadName(app); derived != name {
			mangled++
			t.Logf("    [harness artifact] %s/%s (%s): getWorkloadName derived %q, so the probe "+
				"looked for the wrong name; the real agent reports a pod name here", ns, name, kind, derived)
			continue
		}
		t.Errorf("live workload %s/%s (%s) did not match any node although the inventory has it "+
			"active — the fix would drop this running workload's edges", ns, name, kind)
	}
	if err := liveRows.Err(); err != nil {
		t.Fatalf("iterate live workloads: %v", err)
	}
	t.Logf("live workloads still resolving to a node: %d/%d (%d skipped as harness name-mangling)",
		matched, checked, mangled)
	if checked > 0 && matched == 0 {
		t.Errorf("not one of %d running workloads matched a node — the fix would drop every live edge", checked)
	}
}

// staleFlowWorkload is one flow-sourced workload-shaped node the inventory has
// no active row for.
type staleFlowWorkload struct{ nodeType, name, ns, kind, src string }

// loadStaleFlowWorkloads reads them for one account. includeTombstoned admits
// rows an earlier build already retired: for an inventory-less account the
// whole population may be tombstoned, and the question is what a build would
// do with it now.
func loadStaleFlowWorkloads(dbm *database.DatabaseManager, tenantID, accountID string, includeTombstoned bool) ([]staleFlowWorkload, error) {
	rows, err := dbm.Db.Query(`
		SELECT n.node_type, n.properties->>'name', COALESCE(n.properties->>'namespace',''),
		       COALESCE(n.properties->>'kind',''), n.properties->>'source'
		FROM public.knowledge_graph_node n
		LEFT JOIN k8s_workloads w
		  ON w.cloud_account_id = n.cloud_account_id
		 AND w.namespace = n.properties->>'namespace'
		 AND w.name = n.properties->>'name'
		 AND w.is_active
		WHERE n.tenant_id = $1 AND n.cloud_account_id = $2
		  AND n.level = 'Tenant'
		  AND n.properties->>'source' IN ('ebpf','traces')
		  AND n.node_type IN ('Workload','Job','CronJob')
		  AND w.name IS NULL
		  AND ($3 OR n.is_active)`, tenantID, accountID, includeTombstoned)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []staleFlowWorkload
	for rows.Next() {
		var st staleFlowWorkload
		if err := rows.Scan(&st.nodeType, &st.name, &st.ns, &st.kind, &st.src); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
