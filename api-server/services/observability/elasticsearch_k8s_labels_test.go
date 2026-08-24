package observability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
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

// A `.keyword` suffix is only real when the parent is mapped `text`. Shippers map
// kubernetes.namespace_name / kubernetes.namespace as plain keyword, so the suffixed
// name does not exist and a term against it matches zero documents with HTTP 200 —
// the caller reads "no logs" where the truth is "wrong field". Verified against the
// dev cluster on logs-kubernetes.container_logs-*: term on kubernetes.namespace_name
// returned 10000 hits, term on kubernetes.namespace_name.keyword returned 0.
func TestBinaryClauseForField_SuffixedNonCanonicalFieldMatchesBothSpellings(t *testing.T) {
	clause, negate, err := binaryClauseForField("kubernetes.namespace_name.keyword", query.Eq, "default")
	assert.NoError(t, err)
	assert.False(t, negate)

	b, ok := clause["bool"].(map[string]any)
	assert.True(t, ok, "a suffixed field must expand into a bool/should, not a bare term")
	assert.Equal(t, 1, b["minimum_should_match"])

	shoulds, ok := b["should"].([]any)
	assert.True(t, ok)
	assert.Len(t, shoulds, 2, "both the suffixed and the bare spelling must be tried")

	rendered := fmt.Sprintf("%v", shoulds)
	assert.Contains(t, rendered, "kubernetes.namespace_name.keyword")
	assert.Contains(t, rendered, "kubernetes.namespace_name")
}

// The bare spelling is deliberately NOT expanded to `.keyword`: a term against a text
// field can match analyzed tokens and would widen results rather than repair them.
func TestBinaryClauseForField_BareNonCanonicalFieldIsNotExpanded(t *testing.T) {
	clause, _, err := binaryClauseForField("some.vendor.field", query.Eq, "x")
	assert.NoError(t, err)
	_, isBool := clause["bool"]
	assert.False(t, isBool, "a bare non-canonical field must render as a single clause")
}

func TestESKeywordSuffixCandidates(t *testing.T) {
	assert.Equal(t, []string{"a.b.keyword", "a.b"}, esKeywordSuffixCandidates("a.b.keyword"))
	assert.Nil(t, esKeywordSuffixCandidates("a.b"), "unsuffixed field yields no candidates")
	assert.Nil(t, esKeywordSuffixCandidates(".keyword"), "a bare suffix is not a field")
}

// llm-server advertises `_body` to the query generator unconditionally, but no shipper
// writes a field with that name — Fluent-Bit writes `log`, ECS `message`, OTel `body`.
// Unmapped, the generator's `{"_body": {"_ilike": "%error%"}}` reached ES as a wildcard
// on a non-existent field and matched nothing, silently.
func TestESCandidateFields_BodyResolvesToShipperSpellings(t *testing.T) {
	got := esCandidateFields("_body")
	for _, want := range []string{"log", "message", "body", "content"} {
		assert.Contains(t, got, want, "the log-body canonical must cover %s", want)
	}
	// The .keyword variants are not optional. A wildcard is a term-level query, so on
	// an analyzed `text` body field a multi-word pattern (`*connection reset*`) matches
	// no single token and returns nothing; only the unanalyzed subfield keeps the line
	// as one term. Dropping them to save a scan reintroduced the silent-empty failure
	// this change set exists to remove.
	for _, want := range []string{"log.keyword", "message.keyword"} {
		assert.Contains(t, got, want, "multi-word body search needs the unanalyzed %s", want)
	}
}
