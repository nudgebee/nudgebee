package flow_sources

import (
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

const testTenant = "11111111-1111-1111-1111-111111111111"

func TestBuildTenantScopedNode(t *testing.T) {
	n := &ManualNode{TenantID: testTenant, NodeType: "Database", SpecificType: "PostgreSQL", Name: "billing-db", Namespace: "finance", Region: "on-prem-dc1"}
	node, err := buildTenantScopedNode(n)
	if err != nil {
		t.Fatalf("buildTenantScopedNode: %v", err)
	}
	wantKey := "external:" + testTenant + "::Database:finance:billing-db"
	if node.UniqueKey != wantKey {
		t.Errorf("unique key = %s, want %s", node.UniqueKey, wantKey)
	}
	if node.CloudAccountID != testTenant {
		t.Errorf("cloud_account_id = %s, want the tenant (partition key)", node.CloudAccountID)
	}
	if node.ID != core.NodeIDFor(wantKey, testTenant, testTenant) {
		t.Error("id is not NodeIDFor-derived")
	}
	// Region is descriptive: it must not move the node.
	n.Region = "elsewhere"
	again, _ := buildTenantScopedNode(n)
	if again.ID != node.ID {
		t.Error("changing the region changed the node id")
	}

	for name, bad := range map[string]*ManualNode{
		"k8s kind":       {TenantID: testTenant, NodeType: "Pod", Name: "x"},
		"no name":        {TenantID: testTenant, NodeType: "Database"},
		"colon in name":  {TenantID: testTenant, NodeType: "Database", Name: "a:b"},
		"colon in group": {TenantID: testTenant, NodeType: "Database", Name: "a", Namespace: "x:y"},
	} {
		if _, err := buildTenantScopedNode(bad); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTenantScopedManualNodeKinds_MatchManualTypes(t *testing.T) {
	kinds := TenantScopedManualNodeKinds()
	if len(kinds) != len(core.ManualNodeTypes) {
		t.Fatalf("got %d kinds, want one per declarable type (%d)", len(kinds), len(core.ManualNodeTypes))
	}
	for _, k := range kinds {
		if len(k.KeyFields) == 0 || k.KeyFields[0].Name != core.ManualNodeFieldName || !k.KeyFields[0].Required {
			t.Errorf("%s: name must be the first, required key field", k.SpecificType)
		}
	}
}

func TestValidateManualNodeDetails(t *testing.T) {
	tenantScoped := func(props map[string]interface{}, labels map[string]string) *ManualNode {
		return &ManualNode{TenantID: testTenant, NodeType: "Database", SpecificType: "Database", Properties: props, Labels: labels}
	}
	if err := validateManualNodeDetails(tenantScoped(map[string]interface{}{"endpoint": "db.internal:5432", "engine": "postgres"}, map[string]string{"team": "x"})); err != nil {
		t.Errorf("allowed details refused: %v", err)
	}
	if err := validateManualNodeDetails(tenantScoped(map[string]interface{}{"replicas": "3"}, nil)); err == nil || !strings.Contains(err.Error(), "replicas") {
		t.Errorf("unknown property must be refused by name, got %v", err)
	}
	if err := validateManualNodeDetails(tenantScoped(map[string]interface{}{"engine": map[string]interface{}{"x": 1}}, nil)); err == nil {
		t.Error("non-scalar property value must be refused")
	}
	if err := validateManualNodeDetails(tenantScoped(nil, map[string]string{" ": "x"})); err == nil {
		t.Error("empty label key must be refused")
	}

	// In a cloud account the type's own schema decides.
	core.RegisterSpecificTypeSchema(core.SpecificTypeSchema{
		SpecificType: "TestManualCloudType", NodeType: core.NodeTypeDatabase,
		Properties: []core.PropertyDef{{Name: "engine_version"}},
	})
	cloud := &ManualNode{TenantID: testTenant, CloudAccountID: "acct", SpecificType: "TestManualCloudType"}
	cloud.Properties = map[string]interface{}{"engine_version": "16"}
	if err := validateManualNodeDetails(cloud); err != nil {
		t.Errorf("schema property refused: %v", err)
	}
	cloud.Properties = map[string]interface{}{"endpoint": "x"}
	if err := validateManualNodeDetails(cloud); err == nil {
		t.Error("a property outside the type's schema must be refused")
	}
}

// A declared value must have the shape the collector stores the property in,
// and a field only the collector can know is refused with that reason.
func TestValidateManualNodeDetails_Shapes(t *testing.T) {
	core.RegisterSpecificTypeSchema(core.SpecificTypeSchema{
		SpecificType: "TestManualShapedType", NodeType: core.NodeTypeWorkload,
		Properties: []core.PropertyDef{
			{Name: "annotations", Shape: core.PropertyShapeMap},
			{Name: "container_images", Shape: core.PropertyShapeList},
			{Name: "replicas"},
			{Name: "created_at"},
		},
	})
	node := func(props map[string]interface{}) *ManualNode {
		return &ManualNode{TenantID: testTenant, CloudAccountID: "acct", SpecificType: "TestManualShapedType", Properties: props}
	}
	ok := map[string]interface{}{
		"annotations":      map[string]interface{}{"team": "payments"},
		"container_images": []interface{}{"ledger:1.4"},
		"replicas":         float64(3),
	}
	if err := validateManualNodeDetails(node(ok)); err != nil {
		t.Errorf("well-shaped details refused: %v", err)
	}
	for name, props := range map[string]map[string]interface{}{
		"map as text":       {"annotations": "team=payments"},
		"map of non-text":   {"annotations": map[string]interface{}{"team": 1}},
		"list as text":      {"container_images": "ledger:1.4"},
		"list of non-text":  {"container_images": []interface{}{1}},
		"blank list item":   {"container_images": []interface{}{" "}},
		"scalar as a list":  {"replicas": []interface{}{"3"}},
		"collector-only":    {"created_at": "2026-01-01"},
		"unknown is listed": {"nope": "x"},
	} {
		if err := validateManualNodeDetails(node(props)); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
	err := validateManualNodeDetails(node(map[string]interface{}{"created_at": "2026-01-01"}))
	if err == nil || !strings.Contains(err.Error(), "read from the resource") {
		t.Errorf("a collector-only field must say why it is refused, got %v", err)
	}
}
