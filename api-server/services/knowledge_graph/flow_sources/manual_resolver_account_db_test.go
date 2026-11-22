package flow_sources

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// The account qualifier on a manual dependency endpoint names the connected
// account the node lives in. The form and CSV send the provider's account
// number; it used to be compared with query_attributes.aws_account_number,
// which only identity nodes carry, so any endpoint with an account was
// unmatched. Seeds two AWS accounts and a GCP project, each with a node of
// the same name and type, and resolves against each qualifier.
func TestManualEndpointResolver_AccountQualifier(t *testing.T) {
	db := testenv.RequireMetastore(t)
	tenantID, userID := uuid.NewString(), uuid.NewString()
	awsA, awsB, gcp := uuid.NewString(), uuid.NewString(), uuid.NewString()

	seed := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, display_name) VALUES ($1, $2, 'resolver test')`, []any{userID, "rt-" + userID}},
		{`INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $3)`, []any{tenantID, "rt-" + tenantID, userID}},
	}
	for _, a := range []struct{ id, provider, number string }{
		{awsA, "AWS", "111111111111"}, {awsB, "AWS", "222222222222"}, {gcp, "GCP", "demo-project"},
	} {
		seed = append(seed, struct {
			sql  string
			args []any
		}{`INSERT INTO cloud_accounts (id, account_name, created_by, updated_by, tenant, account_type, cloud_provider, account_number)
			VALUES ($1, $2, $3, $3, $4, 'test', $5, $6)`, []any{a.id, "resolver test " + a.number, userID, tenantID, a.provider, a.number}})
	}
	for _, s := range seed {
		if _, err := db.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	nodeIn := func(account, provider string) *core.DbNode {
		n := core.NewNode(core.NodeTypeDatabase,
			core.BuildUniqueKey(provider, account, "us-east-1", core.NodeTypeDatabase, "", "orders-db"),
			map[string]interface{}{"name": "orders-db", "region": "us-east-1"}, tenantID, account, provider)
		n.Level = "Tenant"
		return n
	}
	nodes := map[string]*core.DbNode{awsA: nodeIn(awsA, "aws"), awsB: nodeIn(awsB, "aws"), gcp: nodeIn(gcp, "gcp")}
	ctx := security.NewRequestContextForTenantAdmin(tenantID, nil, nil, nil)
	if err := core.NewService(ctx, ctx.GetLogger(), db).SaveNodes([]*core.DbNode{nodes[awsA], nodes[awsB], nodes[gcp]}, 1); err != nil {
		t.Fatalf("save nodes: %v", err)
	}

	resolver := NewManualEndpointResolver(db, nil)
	resolve := func(account string) *EndpointResolveResult {
		t.Helper()
		r, err := resolver.Resolve(context.Background(), tenantID, ManualEndpoint{NodeType: "Database", Name: "orders-db", AccountID: account})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", account, err)
		}
		return r
	}

	for _, tc := range []struct {
		name, account, wantNode string
	}{
		{"AWS account number", "111111111111", awsA},
		{"the other AWS account", "222222222222", awsB},
		{"GCP project id", "demo-project", gcp},
		{"connected account id", awsB, awsB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := resolve(tc.account)
			if r.Status != EndpointStatusResolved || r.NodeID != nodes[tc.wantNode].ID {
				t.Fatalf("got %s → %s, want resolved → %s", r.Status, r.NodeID, nodes[tc.wantNode].ID)
			}
		})
	}

	if r := resolve("999999999999"); r.Status != EndpointStatusUnmatched {
		t.Errorf("an account the tenant has not connected must stay unmatched, got %s", r.Status)
	}
	if r := resolve(""); r.Status != EndpointStatusAmbiguous || r.MatchCount != 3 {
		t.Errorf("without an account all three are candidates, got %s (%d)", r.Status, r.MatchCount)
	}
}
