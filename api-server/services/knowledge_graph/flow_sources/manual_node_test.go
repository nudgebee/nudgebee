package flow_sources

import (
	"bytes"
	"encoding/csv"
	"os"
	"reflect"
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

func TestCSVRowToManualNode_Shapes(t *testing.T) {
	core.RegisterSpecificTypeSchema(core.SpecificTypeSchema{
		SpecificType: "TestManualCSVShapedType", NodeType: core.NodeTypeWorkload,
		Properties: []core.PropertyDef{
			{Name: "annotations", Shape: core.PropertyShapeMap},
			{Name: "container_images", Shape: core.PropertyShapeList},
		},
	})
	header := []string{"cloud_account", "specific_type", "name", "annotations", "container_images"}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	req, err := csvRowToManualNode([]string{"acct", "TestManualCSVShapedType", "ledger", "team=payments; tier = gold", "ledger:1.4; sidecar:2 ;"}, header, idx)
	if err != nil {
		t.Fatalf("csvRowToManualNode: %v", err)
	}
	if got, want := req.Properties["annotations"], map[string]interface{}{"team": "payments", "tier": "gold"}; !reflect.DeepEqual(got, want) {
		t.Errorf("annotations = %v, want %v", got, want)
	}
	if got, want := req.Properties["container_images"], []interface{}{"ledger:1.4", "sidecar:2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("container_images = %v, want %v", got, want)
	}
	if _, err := csvRowToManualNode([]string{"acct", "TestManualCSVShapedType", "ledger", "team", ""}, header, idx); err == nil || !strings.Contains(err.Error(), "annotations") {
		t.Errorf("a malformed map cell must be refused by column name, got %v", err)
	}
}

func TestParseCSVLabels(t *testing.T) {
	got, err := parseCSVLabels("team=payments; env = prod ;")
	if err != nil || got["team"] != "payments" || got["env"] != "prod" || len(got) != 2 {
		t.Errorf("parseCSVLabels = %v, %v", got, err)
	}
	if _, err := parseCSVLabels("team"); err == nil {
		t.Error("a pair without = must be refused")
	}
}

func TestCSVRowToManualNode(t *testing.T) {
	header := []string{"cloud_account", "specific_type", "name", "region", "network", "labels", "engine_version"}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	req, err := csvRowToManualNode([]string{"123456789012", "RDSInstance", "orders-db", "us-east-1", "vpc-0abc", "team=payments", "16"}, header, idx)
	if err != nil {
		t.Fatalf("csvRowToManualNode: %v", err)
	}
	if req.CloudAccount != "123456789012" || req.SpecificType != "RDSInstance" || req.Name != "orders-db" ||
		req.Region != "us-east-1" || req.Network != "vpc-0abc" || req.Labels["team"] != "payments" {
		t.Errorf("columns mis-mapped: %+v", req)
	}
	if req.Properties["engine_version"] != "16" || len(req.Properties) != 1 {
		t.Errorf("extra columns must become properties: %v", req.Properties)
	}
}

// The sample CSV shipped for QA must stay parseable by the importer, with one
// row per documented outcome (see test/testdata/sample_manual_nodes.README.md).
func TestSampleManualNodesCSVParses(t *testing.T) {
	raw, err := os.ReadFile("../test/testdata/sample_manual_nodes.csv")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("sample is not valid CSV: %v", err)
	}
	header, rows := records[0], records[1:]
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	if _, ok := idx["name"]; !ok {
		t.Fatal("sample header has no name column")
	}
	if len(rows) != 10 {
		t.Errorf("sample has %d rows; the README documents 10", len(rows))
	}
	for i, rec := range rows {
		req, err := csvRowToManualNode(rec, header, idx)
		if err != nil {
			t.Errorf("row %d does not parse: %v", i, err)
		}
		if req.Name == "" {
			t.Errorf("row %d has no name", i)
		}
	}
}
