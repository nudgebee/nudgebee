package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestESIndexURL_KeepsPatternsAndContainsInjection(t *testing.T) {
	cases := []struct{ index, want string }{
		{"metrics-*", "http://es/metrics-%2A/_search"},
		{"ccs-self:metrics-*", "http://es/ccs-self:metrics-%2A/_search"},
		{"logs-a,logs-b", "http://es/logs-a,logs-b/_search"},
		{"logs-a, logs-b", "http://es/logs-a,logs-b/_search"},
		{"metrics-*,-metrics-old", "http://es/metrics-%2A,-metrics-old/_search"},
		{"*:fluentk8-*", "http://es/%2A:fluentk8-%2A/_search"},
		{"<logs-{now/d}>", "http://es/%3Clogs-%7Bnow%2Fd%7D%3E/_search"},
		{"<logs-{now/d+1d}>", "http://es/%3Clogs-%7Bnow%2Fd%2B1d%7D%3E/_search"},
		{"victim/_delete_by_query?x=", "http://es/victim%2F_delete_by_query%3Fx=/_search"},
		{"victim/_doc#", "http://es/victim%2F_doc%23/_search"},
		{"..", "http://es/%2E%2E/_search"},
		{"a,../_security/user/x", "http://es/a,%2E%2E%2F_security%2Fuser%2Fx/_search"},
		{"../_security/user/x", "http://es/%2E%2E%2F_security%2Fuser%2Fx/_search"},
		{".ds-metrics-app-2026.10.10-000001", "http://es/.ds-metrics-app-2026.10.10-000001/_search"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, esIndexURL("http://es", c.index, "_search"), c.index)
	}
}

// What reaches the cluster: the raw request line, as a proxy or Elasticsearch sees it.
func TestESIndexURL_RequestsStayOnTheirEndpoint(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.RequestURI)
		_, _ = w.Write([]byte(`{"idx":{"mappings":{"properties":{"a":{"type":"keyword"}}}},"aggregations":{"label_values":{"buckets":[]}}}`))
	}))
	defer srv.Close()
	cfg := &ElasticsearchConfig{Url: srv.URL}

	_, _ = esFetchMetricFields(cfg, "victim/_delete_by_query?x=")
	_, _ = esFetchLabelValues(cfg, "victim/_delete_by_query?x=", "a")
	_, _ = esFetchMetricFields(cfg, "ccs-self:metrics-*")

	require.GreaterOrEqual(t, len(got), 3)
	for _, g := range got[:len(got)-1] {
		assert.NotContains(t, g, "/_delete_by_query", "the injected endpoint must stay inside the index name")
		assert.Regexp(t, `^(GET|POST) /victim%2F_delete_by_query%3Fx=/_(mapping|search)$`, g)
	}
	assert.Equal(t, "GET /ccs-self:metrics-%2A/_field_caps?fields=*", got[len(got)-1], "remote patterns still reach field caps")
}
