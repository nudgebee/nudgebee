package api

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// callKG runs one kg_* action through the real dispatcher.
func callKG(t *testing.T, action, tenantID string, request map[string]any) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/rpc/knowledge-graph", nil)
	input := map[string]any{}
	if request != nil {
		input["request"] = request
	}
	handleKnowledgeGraphAction(&ActionRequest{
		Action:           ActionRequestAction{Name: action},
		Input:            input,
		SessionVariables: map[string]any{"tenant_id": tenantID},
	}, c, nil, nil, slog.Default())
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestManualNodeActions_MalformedRequestIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"kg_create_manual_node", "kg_update_manual_node", "kg_delete_manual_node", "kg_apply_manual_node_merge", "kg_import_manual_nodes"} {
		t.Run(action, func(t *testing.T) {
			if code, _ := callKG(t, action, uuid.NewString(), nil); code != 400 {
				t.Fatalf("missing request: got %d, want 400", code)
			}
		})
	}
	for _, tc := range []struct {
		action  string
		request map[string]any
	}{
		{"kg_update_manual_node", map[string]any{"node": map[string]any{}}},
		{"kg_delete_manual_node", map[string]any{}},
		{"kg_apply_manual_node_merge", map[string]any{}},
		{"kg_import_manual_nodes", map[string]any{"csv": ""}},
	} {
		if code, _ := callKG(t, tc.action, uuid.NewString(), tc.request); code != 400 {
			t.Errorf("%s with %v: got %d, want 400", tc.action, tc.request, code)
		}
	}
}

func TestManualNodeActions_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testenv.RequireMetastore(t)
	tenantID, userID, accountID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, display_name) VALUES ($1, $2, 'manual node api test')`, []any{userID, "mn-api-" + userID}},
		{`INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $3)`, []any{tenantID, "mn-api-" + tenantID, userID}},
		{`INSERT INTO cloud_accounts (id, account_name, created_by, updated_by, tenant, account_type, cloud_provider, account_number)
			VALUES ($1, 'manual node api account', $2, $2, $3, 'aws', 'AWS', '210987654321')`, []any{accountID, userID, tenantID}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// The schema offers the AWS account (with its declarable types) and the
	// outside-any-account place.
	code, body := callKG(t, "kg_get_manual_node_schema", tenantID, map[string]any{})
	if code != 200 {
		t.Fatalf("schema: %d %v", code, body)
	}
	places, _ := body["data"].([]any)
	if len(places) != 2 {
		t.Fatalf("schema places = %d, want the AWS account + outside: %v", len(places), body)
	}

	// Declare in the account.
	code, body = callKG(t, "kg_create_manual_node", tenantID, map[string]any{
		"cloud_account_id": accountID, "specific_type": "SQSQueue", "name": "orders", "region": "us-east-1",
		"properties": map[string]any{"queue_type": "fifo"},
	})
	if code != 200 {
		t.Fatalf("create: %d %v", code, body)
	}
	created := body["data"].(map[string]any)
	id, nodeID := created["id"].(float64), created["kg_node_id"].(string)

	// Twice is a 409, not a second node.
	if code, body = callKG(t, "kg_create_manual_node", tenantID, map[string]any{
		"cloud_account_id": accountID, "specific_type": "SQSQueue", "name": "orders", "region": "us-east-1",
	}); code != 409 {
		t.Fatalf("duplicate declaration: %d %v, want 409", code, body)
	}

	// A resource the collector already wrote is refused with that node's id.
	b, _ := core.ManualNodeBuilderFor("AWS")
	collected, err := b.Build(core.ManualNodeInput{TenantID: tenantID, CloudAccountID: accountID, CloudProvider: "AWS",
		SpecificType: "SQSQueue", Name: "payments", Region: "us-east-1"})
	if err != nil {
		t.Fatalf("build collected: %v", err)
	}
	collected.Level = "Tenant"
	ctx := security.NewRequestContextForTenantAdmin(tenantID, nil, nil, nil)
	if err := core.NewService(ctx, ctx.GetLogger(), db).SaveNodes([]*core.DbNode{collected}, 1); err != nil {
		t.Fatalf("save collected: %v", err)
	}
	code, body = callKG(t, "kg_create_manual_node", tenantID, map[string]any{
		"cloud_account_id": accountID, "specific_type": "SQSQueue", "name": "payments", "region": "us-east-1",
	})
	if code != 409 || body["node_id"] != collected.ID || body["code"] != "already_in_graph" {
		t.Fatalf("already-collected resource: %d %v, want 409 with node_id %s", code, body, collected.ID)
	}

	// Edit details; identity is not part of the request.
	if code, body = callKG(t, "kg_update_manual_node", tenantID, map[string]any{
		"id": id, "node": map[string]any{"description": "order events"},
	}); code != 200 || body["data"].(map[string]any)["description"] != "order events" {
		t.Fatalf("update: %d %v", code, body)
	}

	code, body = callKG(t, "kg_list_manual_nodes", tenantID, map[string]any{})
	if code != 200 || body["count"].(float64) != 1 {
		t.Fatalf("list: %d %v", code, body)
	}

	if code, body = callKG(t, "kg_delete_manual_node", tenantID, map[string]any{"id": id}); code != 200 {
		t.Fatalf("delete: %d %v", code, body)
	}
	var active bool
	if err := db.Db.Get(&active, `SELECT is_active FROM knowledge_graph_node WHERE id = $1`, nodeID); err != nil || active {
		t.Errorf("node still active after delete (err %v)", err)
	}
	if code, _ = callKG(t, "kg_delete_manual_node", tenantID, map[string]any{"id": id}); code != 404 {
		t.Errorf("second delete: %d, want 404", code)
	}
}
