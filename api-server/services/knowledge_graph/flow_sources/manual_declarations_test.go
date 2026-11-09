package flow_sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestParseManualDeclarations(t *testing.T) {
	doc, err := ParseManualDeclarations([]byte(`{
	  "nodes": [
	    {"ref": "db", "node_type": "Database", "name": "billing-db", "labels": {"team": "finance"}, "properties": {"engine": "postgres"}},
	    {"cloud_account": "123456789012", "specific_type": "KubernetesDeployment", "name": "ledger-worker", "namespace": "payments",
	     "properties": {"replicas": 3, "paused": true, "annotations": {"owner": "ledger"}, "container_names": ["a", "b"]}}
	  ],
	  "dependencies": [
	    {"source": {"node_type": "Workload", "name": "accounting", "namespace": "demo", "cluster": "k8s-east"}, "dest": "db"},
	    {"source": " ledger-worker ", "dest": {"node_type": "MessageQueue", "name": "orders", "account_id": "123456789012"},
	     "relationship_type": "PUBLISHES_TO", "notes": "n"}
	  ]}`))
	if err != nil {
		t.Fatalf("ParseManualDeclarations: %v", err)
	}
	if got := doc.Nodes[0]; got.Ref != "db" || got.NodeType != "Database" || got.Labels["team"] != "finance" || got.Properties["engine"] != "postgres" {
		t.Errorf("nodes[0] = %+v", got)
	}
	// A ref defaults to the name, and JSON values keep their types.
	n := doc.Nodes[1]
	if n.Ref != "ledger-worker" || n.CloudAccount != "123456789012" || n.Namespace != "payments" {
		t.Errorf("nodes[1] = %+v", n)
	}
	if n.Properties["replicas"] != float64(3) || n.Properties["paused"] != true {
		t.Errorf("scalars lost their type: %#v", n.Properties)
	}
	if _, ok := n.Properties["annotations"].(map[string]interface{}); !ok {
		t.Errorf("annotations is %T, want a map", n.Properties["annotations"])
	}
	if l, ok := n.Properties["container_names"].([]interface{}); !ok || len(l) != 2 {
		t.Errorf("container_names is %#v, want a list of two", n.Properties["container_names"])
	}

	d := doc.Dependencies[0]
	if d.Source.Endpoint == nil || *d.Source.Endpoint != (ManualEndpoint{NodeType: "Workload", Name: "accounting", Namespace: "demo", Cluster: "k8s-east"}) {
		t.Errorf("dependencies[0].source = %+v", d.Source)
	}
	if d.Dest.Ref != "db" || d.Dest.Endpoint != nil || d.RelationshipType != "CALLS" {
		t.Errorf("dependencies[0] dest/relationship = %+v / %q", d.Dest, d.RelationshipType)
	}
	d = doc.Dependencies[1]
	if d.Source.Ref != "ledger-worker" || d.Dest.Endpoint == nil || d.Dest.Endpoint.AccountID != "123456789012" ||
		d.RelationshipType != "PUBLISHES_TO" || d.Notes != "n" {
		t.Errorf("dependencies[1] = %+v", d)
	}

	// A side is written back the way it was declared.
	out, err := json.Marshal(doc.Dependencies[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s := string(out); !strings.Contains(s, `"dest":"db"`) || !strings.Contains(s, `"source":{"node_type":"Workload","name":"accounting"`) {
		t.Errorf("round trip: %s", s)
	}
}

func TestParseManualDeclarations_RefusesTheDocument(t *testing.T) {
	endpoint := `{"node_type": "Database", "name": "a"}`
	for name, tc := range map[string]struct{ doc, want string }{
		"not json":               {`{`, "invalid document"},
		"unknown top field":      {`{"nodes": [], "edges": []}`, `unknown field "edges"`},
		"unknown node field":     {`{"nodes": [{"name": "a", "typo": 1}]}`, `unknown field "typo"`},
		"unknown endpoint field": {`{"dependencies": [{"source": {"node_type": "Database", "nmae": "a"}, "dest": ` + endpoint + `}]}`, `unknown field "nmae"`},
		"trailing data":          {`{"nodes": [{"node_type": "Database", "name": "a"}]} {}`, "unexpected data"},
		"stray closing brace":    {`{"nodes": [{"node_type": "Database", "name": "a"}]} }`, "unexpected data"},
		"stray closing bracket":  {`{"nodes": [{"node_type": "Database", "name": "a"}]} ]`, "unexpected data"},
		"empty":                  {`{}`, "declares nothing"},
		"empty arrays":           {`{"nodes": [], "dependencies": []}`, "declares nothing"},
		"duplicate ref":          {`{"nodes": [{"ref": "x", "name": "a"}, {"ref": "x", "name": "b"}]}`, `nodes[1]: ref "x" is already used by nodes[0]`},
		"duplicate default ref":  {`{"nodes": [{"name": "a"}, {"ref": "a", "name": "b"}]}`, `nodes[1]: ref "a" is already used by nodes[0]`},
		"unknown ref":            {`{"dependencies": [{"source": ` + endpoint + `, "dest": "ghost"}]}`, `dependencies[0].dest: no node with ref "ghost" in this document`},
		"side of another type":   {`{"dependencies": [{"source": 5, "dest": ` + endpoint + `}]}`, "must be a ref string or an endpoint object"},
		"null side":              {`{"dependencies": [{"source": null, "dest": ` + endpoint + `}]}`, "must be a ref string or an endpoint object"},
		"empty ref":              {`{"dependencies": [{"source": " ", "dest": ` + endpoint + `}]}`, "a ref may not be empty"},
		"missing side":           {`{"dependencies": [{"dest": ` + endpoint + `}]}`, "dependencies[0].source is required"},
	} {
		if _, err := ParseManualDeclarations([]byte(tc.doc)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}

	big := append([]byte(`{"nodes": [{"name": "a", "description": "`), bytes.Repeat([]byte("x"), MaxManualDeclarationsBytes)...)
	big = append(big, []byte(`"}]}`)...)
	if _, err := ParseManualDeclarations(big); err == nil || !strings.Contains(err.Error(), "1024 KB limit") {
		t.Errorf("oversized document: got %v", err)
	}

	var many strings.Builder
	many.WriteString(`{"nodes": [`)
	for i := 0; i <= MaxManualDeclarationsItems; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"node_type": "Database", "name": "db-%d"}`, i)
	}
	many.WriteString(`]}`)
	if _, err := ParseManualDeclarations([]byte(many.String())); err == nil || !strings.Contains(err.Error(), "at most 500 per import") {
		t.Errorf("document over the item cap: got %v", err)
	}
}

// A node with no name has no ref; it is left for the per-item "name is
// required" rejection instead of failing the whole document.
func TestParseManualDeclarations_NamelessNodeIsNotADocumentError(t *testing.T) {
	doc, err := ParseManualDeclarations([]byte(`{"nodes": [{"node_type": "Database"}, {"node_type": "Cache"}]}`))
	if err != nil {
		t.Fatalf("got %v, want the nodes parsed for per-item rejection", err)
	}
	if doc.Nodes[0].Ref != "" || doc.Nodes[1].Ref != "" {
		t.Errorf("refs = %q, %q", doc.Nodes[0].Ref, doc.Nodes[1].Ref)
	}
}

// The sample a user starts from must parse as shipped (placeholders and all),
// with every ref it uses declared.
func TestSampleManualDeclarationsParses(t *testing.T) {
	raw, err := os.ReadFile("../test/testdata/sample_manual_declarations.json")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	doc, err := ParseManualDeclarations(raw)
	if err != nil {
		t.Fatalf("sample does not parse: %v", err)
	}
	if len(doc.Nodes) != 8 || len(doc.Dependencies) != 6 {
		t.Fatalf("sample has %d nodes and %d dependencies; update the README's outcome table with it", len(doc.Nodes), len(doc.Dependencies))
	}
	var refSides, objectSides int
	for _, d := range doc.Dependencies {
		for _, side := range []ManualDeclarationEndpoint{d.Source, d.Dest} {
			if side.Endpoint != nil {
				objectSides++
			} else {
				refSides++
			}
		}
	}
	if refSides == 0 || objectSides == 0 {
		t.Errorf("the sample should show both kinds of side: %d refs, %d objects", refSides, objectSides)
	}
	if replicas := doc.Nodes[4].Properties["replicas"]; replicas != float64(3) {
		t.Errorf("replicas = %#v, want the number 3", replicas)
	}
}
