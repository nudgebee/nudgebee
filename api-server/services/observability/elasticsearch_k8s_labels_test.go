package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/query"
)

// The pod/workload Logs tab filters on the canonical labels namespace / pod /
// app. Elasticsearch has no Log Label Mapping to translate them, so those
// filters were sent as terms on fields literally named "namespace" / "pod" /
// "app" and matched nothing on any real shipper. Each must now expand to a
// should over the Fluent-Bit, ECS and OTel spellings.
func TestBuildESQueryFromWhere_ExpandsCanonicalK8sLabels(t *testing.T) {
	got, err := buildESQueryFromWhere(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"namespace": {query.Eq: "nudgebee"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		`"namespace":"nudgebee"`,                              // canonical name kept as a candidate
		`"kubernetes.namespace_name":"nudgebee"`,              // Fluent-Bit
		`"kubernetes.namespace":"nudgebee"`,                   // ECS / Elastic Agent
		`"resource.attributes.k8s.namespace.name":"nudgebee"`, // OTel-native
		`"kubernetes.namespace_name.keyword":"nudgebee"`,      // text field's aggregatable multi-field
		`"minimum_should_match":1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expanded query missing %s\ngot: %s", want, got)
		}
	}
}

// Expansion must not touch a field the user picked from the index itself —
// those are already real ES field names.
func TestBuildESQueryFromWhere_LeavesNonCanonicalFieldAlone(t *testing.T) {
	got, err := buildESQueryFromWhere(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"elastic_agent.version": {query.Eq: "8.19.11"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "should") {
		t.Fatalf("non-canonical field must render as a single term, got: %s", got)
	}
	if !strings.Contains(got, `"elastic_agent.version":"8.19.11"`) {
		t.Fatalf("expected a plain term clause, got: %s", got)
	}
}

// A negated operator keeps its negation across the expansion: whereToBool puts
// the whole should into must_not, i.e. "no candidate field holds this value".
// Returning negate=false here would invert the filter.
func TestBinaryClauseForField_PropagatesNegation(t *testing.T) {
	clause, negate, err := binaryClauseForField("pod", query.Nq, "api-server-0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !negate {
		t.Fatal("expected _neq to stay a negation after expansion")
	}
	out, _ := json.Marshal(clause)
	if !strings.Contains(string(out), `"kubernetes.pod_name":"api-server-0"`) {
		t.Fatalf("expected pod candidates in the negated clause, got: %s", out)
	}

	full, err := buildESQueryFromWhere(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"pod": {query.Nq: "api-server-0"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(full, "must_not") {
		t.Fatalf("expected the expanded clause under must_not, got: %s", full)
	}
}

// An operator the builder cannot render must still fail rather than silently
// dropping the filter, expansion or not.
func TestBinaryClauseForField_PropagatesRenderError(t *testing.T) {
	if _, _, err := binaryClauseForField("namespace", query.IsNull, ""); err == nil {
		t.Fatal("expected the underlying operator error to propagate")
	}
}
