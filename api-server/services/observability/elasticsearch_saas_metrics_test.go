package observability

import (
	"encoding/json"
	"nudgebee/services/query"
	"strings"
	"testing"
)

func TestNormalizeESMetricsWhere_EqAppendsKeyword(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"attributes.metric.attributes.service@name": {query.Eq: "services-server"},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["attributes.metric.attributes.service@name.keyword"]; !ok {
		t.Fatalf("expected .keyword suffix, got fields: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_NumericEqDoesNotAppend(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"metric.attributes.http@response@status_code": {query.Eq: float64(200)},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["metric.attributes.http@response@status_code"]; !ok {
		t.Fatalf("expected bare field for numeric value, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_AlreadyKeyword(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"name.keyword": {query.Eq: "traces.span.metrics.calls"},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["name.keyword"]; !ok || len(got.Binary) != 1 {
		t.Fatalf("expected unchanged .keyword field, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_InWithStringSlice(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"serviceName": {query.In: []any{"services-server", "llm-server"}},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["serviceName.keyword"]; !ok {
		t.Fatalf("expected .keyword for _in string slice, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_NestedAndOrNot(t *testing.T) {
	nested := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"serviceName": {query.Eq: "services-server"},
		},
	}
	wc := query.QueryWhereClause{
		And: []query.QueryWhereClause{nested},
		Or:  []query.QueryWhereClause{nested},
		Not: &nested,
	}
	got := normalizeESMetricsWhere(wc)
	for _, branch := range [][]query.QueryWhereClause{got.And, got.Or} {
		if _, ok := branch[0].Binary["serviceName.keyword"]; !ok {
			t.Fatalf("nested And/Or not normalized")
		}
	}
	if _, ok := got.Not.Binary["serviceName.keyword"]; !ok {
		t.Fatalf("nested Not not normalized")
	}
}

func TestNormalizeESMetricsWhere_UserPayload(t *testing.T) {
	// Exact payload from user's failing request.
	raw := `[{"_binary":{"attributes.metric.attributes.service@name":{"_eq":"services-server"}}}]`
	var clauses []query.QueryWhereClause
	if err := json.Unmarshal([]byte(raw), &clauses); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := normalizeESMetricsWhere(clauses[0])
	clause, err := whereToBool(got)
	if err != nil {
		t.Fatalf("whereToBool: %v", err)
	}
	out, _ := json.Marshal(clause)
	if !strings.Contains(string(out), "service@name.keyword") {
		t.Fatalf("generated DSL missing .keyword suffix: %s", out)
	}
}

func mapKeys(m query.BinaryWhereClause) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
