package flow_sources

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestVerifyResolverAccountScoping_AgainstLiveDB is an offline verification
// harness, NOT a CI unit test. It replays the production resolver lookups over
// the *real* node set for a tenant and proves no lookup can cross a cloud
// account boundary.
//
// It exists because the unit tests use synthetic two-node fixtures; this runs
// the same code over every same-named workload that actually collides across
// clusters in a live graph, in the real load order (created_at DESC, which is
// what made the older cluster win the last-write-wins race).
//
// Skipped unless KG_VERIFY_DB_URL is set, so `make test` / CI never touch a DB:
//
//	KG_VERIFY_DB_URL='postgresql://postgres:...@localhost:5432/nudgebee?sslmode=disable' \
//	KG_VERIFY_TENANT='<tenant-uuid>' \
//	go test ./knowledge_graph/flow_sources/ -run TestVerifyResolverAccountScoping_AgainstLiveDB -v
func TestVerifyResolverAccountScoping_AgainstLiveDB(t *testing.T) {
	dbURL := os.Getenv("KG_VERIFY_DB_URL")
	if dbURL == "" {
		t.Skip("KG_VERIFY_DB_URL not set — skipping live-DB verification harness")
	}
	tenantID := os.Getenv("KG_VERIFY_TENANT")
	if tenantID == "" {
		t.Fatal("KG_VERIFY_TENANT is required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Mirrors core.GetNodesByTenant exactly, including ORDER BY created_at DESC.
	// The ordering is load-bearing: it is what decided which node won the
	// cluster-less key when the index was account-blind.
	const q = `
		SELECT id, node_type, COALESCE(cloud_account_id::text,''), tenant_id, COALESCE(properties::text,'{}')
		FROM public.knowledge_graph_node
		WHERE tenant_id = $1 AND level = 'Tenant' AND is_active = true
		  AND (NOT jsonb_exists(properties, 'inferred') OR properties->>'inferred' = 'false')
		ORDER BY created_at DESC`
	rows, err := db.Query(q, tenantID)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var allNodes []*core.DbNode
	for rows.Next() {
		var id, nodeType, accountID, tid, propsJSON string
		if err := rows.Scan(&id, &nodeType, &accountID, &tid, &propsJSON); err != nil {
			t.Fatalf("scan: %v", err)
		}
		props := map[string]interface{}{}
		_ = json.Unmarshal([]byte(propsJSON), &props)
		allNodes = append(allNodes, &core.DbNode{
			ID: id, NodeType: core.NodeType(nodeType), Properties: props,
			CloudAccountID: accountID, TenantID: tid,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	accounts := map[string]bool{}
	for _, n := range allNodes {
		if n.CloudAccountID != "" {
			accounts[n.CloudAccountID] = true
		}
	}
	t.Logf("loaded %d active tenant-level nodes across %d cloud accounts", len(allNodes), len(accounts))

	// ---- Workload identity collisions (the bug that produced the bad edge) ----

	type ident struct{ namespace, kind, name string }
	byIdent := map[ident]map[string]bool{} // identity -> set of accounts
	for _, n := range allNodes {
		if n.NodeType != core.NodeTypeWorkload && n.NodeType != core.NodeTypePod {
			continue
		}
		k := ident{stringProp(n, "namespace"), stringProp(n, "kind"), stringProp(n, "name")}
		if k.namespace == "" || k.kind == "" || k.name == "" || n.CloudAccountID == "" {
			continue
		}
		if byIdent[k] == nil {
			byIdent[k] = map[string]bool{}
		}
		byIdent[k][n.CloudAccountID] = true
	}

	var collisions []ident
	for k, accts := range byIdent {
		if len(accts) > 1 {
			collisions = append(collisions, k)
		}
	}
	sort.Slice(collisions, func(i, j int) bool {
		return fmt.Sprint(collisions[i]) < fmt.Sprint(collisions[j])
	})
	t.Logf("workload identities colliding across cloud accounts: %d", len(collisions))

	// Build one index per account, exactly as NewPodIPResolver does.
	idxByAccount := map[string]workloadIndex{}
	for acct := range accounts {
		idxByAccount[acct] = indexWorkloadsByOwner(allNodes, acct)
	}

	leaks := 0
	for _, k := range collisions {
		for acct := range byIdent[k] {
			// cluster="" is the production call from addInventoryPodName.
			got, ok := idxByAccount[acct].lookup("", k.namespace, k.kind, k.name, "")
			if !ok {
				t.Errorf("account %s: %s/%s/%s exists but did not resolve", acct, k.namespace, k.kind, k.name)
				continue
			}
			if got.CloudAccountID != acct {
				leaks++
				t.Errorf("LEAK account %s: %s/%s/%s resolved to node %s in account %s",
					acct, k.namespace, k.kind, k.name, got.ID, got.CloudAccountID)
			}
		}
	}
	if len(collisions) > 0 {
		for _, k := range collisions[:min(5, len(collisions))] {
			t.Logf("  colliding identity: %s/%s/%s across %d accounts", k.namespace, k.kind, k.name, len(byIdent[k]))
		}
	}

	// ---- ClusterIP collisions across accounts ----

	ipAccounts := map[string]map[string]bool{}
	for _, n := range allNodes {
		if n.NodeType != core.NodeTypeK8sService {
			continue
		}
		ip := stringProp(n, "cluster_ip")
		if ip == "" || ip == "None" || ip == "0.0.0.0" || n.CloudAccountID == "" {
			continue
		}
		if ipAccounts[ip] == nil {
			ipAccounts[ip] = map[string]bool{}
		}
		ipAccounts[ip][n.CloudAccountID] = true
	}
	svcResolver := NewK8sServiceIPResolver(allNodes)
	sharedIPs, ipLeaks := 0, 0
	for ip, accts := range ipAccounts {
		if len(accts) < 2 {
			continue
		}
		sharedIPs++
		for acct := range accts {
			if got, ok := svcResolver.Resolve(acct, "", ip); ok && got.CloudAccountID != acct {
				ipLeaks++
				t.Errorf("LEAK ClusterIP %s for account %s resolved to node %s in account %s",
					ip, acct, got.ID, got.CloudAccountID)
			}
		}
	}
	t.Logf("ClusterIPs shared across cloud accounts: %d", sharedIPs)

	if leaks == 0 && ipLeaks == 0 {
		t.Logf("PASS: no resolver returned a node from another cloud account")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
