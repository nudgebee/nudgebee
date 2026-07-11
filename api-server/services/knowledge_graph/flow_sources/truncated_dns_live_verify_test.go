package flow_sources

import (
	"database/sql"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"

	_ "github.com/lib/pq"
)

// TestVerifyTruncatedAWSDNS_AgainstLiveDB is an offline verification harness, NOT
// a CI unit test. It loads the *real* active ExternalService + cloud-resource
// nodes for a tenant straight from Postgres, builds the same endpoint index the
// CentralizedExternalServiceEnricher builds, and runs the actual matching
// strategies (Strategy 0 DirectEndpointMatch + Strategy 0.5 TruncatedAWSDNS)
// over every ExternalService name. It reports how many orphan external services
// the truncated-DNS fix now collapses onto a real cloud resource.
//
// It is skipped unless KG_VERIFY_DB_URL is set, so `make test` / CI never touch
// a database:
//
//	KG_VERIFY_DB_URL='postgresql://postgres:...@localhost:5432/nudgebee?sslmode=disable' \
//	KG_VERIFY_TENANT='890cad87-c452-4aa7-b84a-742cee0454a1' \
//	go test ./knowledge_graph/flow_sources/ -run TestVerifyTruncatedAWSDNS_AgainstLiveDB -v
func TestVerifyTruncatedAWSDNS_AgainstLiveDB(t *testing.T) {
	dbURL := os.Getenv("KG_VERIFY_DB_URL")
	if dbURL == "" {
		t.Skip("KG_VERIFY_DB_URL not set — skipping live-DB verification harness")
	}
	tenantID := os.Getenv("KG_VERIFY_TENANT")
	if tenantID == "" {
		tenantID = "890cad87-c452-4aa7-b84a-742cee0454a1"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Load every active node the enricher cares about: the ExternalServices to
	// resolve, plus the cloud-resource types buildCloudEndpointIndex indexes.
	const q = `
		SELECT id, node_type, COALESCE(cloud_account_id::text,''), tenant_id, COALESCE(properties::text,'{}')
		FROM public.knowledge_graph_node
		WHERE is_active AND tenant_id = $1
		  AND node_type IN (
		    'ExternalService','Storage','Database','Cache','LoadBalancer','CDN',
		    'APIGateway','MessageQueue','ComputeInstance','Topic',
		    'ContainerRegistry','ManagedCluster','ServerlessFunction'
		  )`
	rows, err := db.Query(q, tenantID)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var allNodes []*core.DbNode
	var externalServices []*core.DbNode
	for rows.Next() {
		var id, nodeType, accountID, tid, propsJSON string
		if err := rows.Scan(&id, &nodeType, &accountID, &tid, &propsJSON); err != nil {
			t.Fatalf("scan: %v", err)
		}
		props := map[string]interface{}{}
		_ = json.Unmarshal([]byte(propsJSON), &props)
		n := &core.DbNode{
			ID:             id,
			NodeType:       core.NodeType(nodeType),
			Properties:     props,
			CloudAccountID: accountID,
			TenantID:       tid,
		}
		allNodes = append(allNodes, n)
		if n.NodeType == core.NodeTypeExternalService {
			externalServices = append(externalServices, n)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	idx := buildCloudEndpointIndex(nil, tenantID, allNodes, silentLogger())
	t.Logf("loaded %d nodes (%d external services); endpoint index has %d entries",
		len(allNodes), len(externalServices), len(idx))

	direct := NewDirectEndpointMatchStrategy()
	truncated := NewTruncatedAWSDNSMatchStrategy()
	ctx := &MatchingContext{EndpointIndex: idx}

	var exactHits, truncatedHits, stillOrphan int
	var truncatedResolutions []string
	for _, es := range externalServices {
		name, _ := es.Properties["name"].(string)
		if name == "" {
			continue
		}
		if r := direct.Match(name, ctx); r.Matched {
			exactHits++
			continue
		}
		if r := truncated.Match(name, ctx); r.Matched {
			truncatedHits++
			target, _ := r.Node.Properties["dns_name"].(string)
			if target == "" {
				target, _ = r.Node.Properties["name"].(string)
			}
			truncatedResolutions = append(truncatedResolutions, name+"  ->  "+target+"  ["+string(r.Node.NodeType)+"]")
			continue
		}
		stillOrphan++
	}

	sort.Strings(truncatedResolutions)
	t.Logf("================ TRUNCATED-DNS FIX VERIFICATION ================")
	t.Logf("external services total            : %d", len(externalServices))
	t.Logf("resolved by exact DirectEndpoint   : %d", exactHits)
	t.Logf("resolved by NEW TruncatedAWSDNS    : %d", truncatedHits)
	t.Logf("still unmatched by index strategies: %d", stillOrphan)
	t.Logf("---------------- truncated resolutions ----------------")
	for _, line := range truncatedResolutions {
		t.Logf("  %s", line)
	}
	t.Logf("===============================================================")

	if truncatedHits == 0 {
		t.Errorf("expected the truncated-DNS strategy to resolve at least one orphan external service, got 0")
	}
}
