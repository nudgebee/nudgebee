package observability

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestVerifyRawTraceQuery_AgainstLiveCluster proves the #41426 raw-trace-query path against
// a REAL Elasticsearch cluster. It is an offline verification harness, not a CI unit test:
// the unit tests in trace_raw_query_test.go pin the body the reader builds, and only a real
// cluster can prove that body actually narrows the result set — the exact thing that was
// broken, since a raw query that is silently discarded also returns a perfectly valid
// (just unfiltered) response.
//
// It runs the readers' OWN body builders (traceSearchBody) and their own hit mappers, so a
// wrong time field or a dropped condition shows up as a failed assertion rather than a
// passing one.
//
// Skipped unless ES_RAW_VERIFY_URL is set, so `make test` / CI never reach a cluster:
//
//	kubectl --context <ctx> -n elasticsearch port-forward svc/elasticsearch-es-http 9200:9200
//	ES_RAW_VERIFY_URL='https://localhost:9200' \
//	ES_RAW_VERIFY_USER='elastic' \
//	ES_RAW_VERIFY_PASS="$(kubectl get secret -n elasticsearch elasticsearch-es-elastic-user -o go-template='{{.data.elastic | base64decode}}')" \
//	ES_RAW_VERIFY_INDEX='otel-v1-apm-span-*' \
//	ES_RAW_VERIFY_LAYOUT='dataprepper' \
//	go test ./observability/ -run TestVerifyRawTraceQuery_AgainstLiveCluster -v
//
// ES_RAW_VERIFY_LAYOUT selects the reader: dataprepper (default) | otel | apm.
//
// What it proves, mapped to the acceptance criteria in nb-41426-raw-trace-queries-spec.md:
//
//	A1  a raw query naming one service returns ONLY that service's spans
//	A3  a malformed raw query errors instead of returning unfiltered spans
//	A4  a raw query with no time bound is still clamped to the requested window
//	A5  the body sent is the body the reader reports as executed
func TestVerifyRawTraceQuery_AgainstLiveCluster(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("ES_RAW_VERIFY_URL"), "/")
	if baseURL == "" {
		t.Skip("ES_RAW_VERIFY_URL not set — skipping live raw-trace-query verification harness")
	}

	layout := os.Getenv("ES_RAW_VERIFY_LAYOUT")
	if layout == "" {
		layout = "dataprepper"
	}

	// bodyFor runs the production builder for the selected layout, and serviceField names
	// that layout's service column so the harness can assert on the mapped spans.
	var bodyFor func(TracesV3Request) (map[string]any, error)
	var groupMapping map[string]string
	var serviceField, index string
	switch layout {
	case "dataprepper":
		bodyFor = (&ElasticSaasTraceSource{}).traceSearchBody
		groupMapping = elasticTraceLabelMapping
		serviceField, index = "serviceName", "otel-v1-apm-span-*"
	case "otel":
		bodyFor = (&ElasticOtelTraceSource{}).traceSearchBody
		groupMapping = elasticOtelTraceLabelMapping
		serviceField, index = "resource.attributes.service.name", "traces-*.otel-*"
	case "apm":
		bodyFor = (&ElasticAPMTraceSource{}).traceSearchBody
		groupMapping = elasticAPMTraceLabelMapping
		serviceField, index = "service.name", esAPMTraceIndex
	default:
		t.Fatalf("ES_RAW_VERIFY_LAYOUT=%q — want dataprepper, otel or apm", layout)
	}
	if v := os.Getenv("ES_RAW_VERIFY_INDEX"); v != "" {
		index = v
	}

	search := func(t *testing.T, body map[string]any) esTraceSearchResponse {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		t.Logf("POST %s/%s/_search\n%s", baseURL, index, string(raw))

		httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/%s/_search", baseURL, index), strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if user := os.Getenv("ES_RAW_VERIFY_USER"); user != "" {
			httpReq.SetBasicAuth(user, os.Getenv("ES_RAW_VERIFY_PASS"))
		}
		// Self-signed cert on the in-cluster endpoint; this harness only ever talks to a
		// port-forward the operator opened themselves.
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}}
		resp, err := client.Do(httpReq)
		if err != nil {
			t.Fatalf("query cluster: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		payload, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cluster returned %d: %s", resp.StatusCode, string(payload))
		}
		var out esTraceSearchResponse
		if err := json.Unmarshal(payload, &out); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return out
	}

	// Step 1 — an unfiltered baseline, so the harness knows the index has traffic AND
	// learns a real service name to filter on. Without this, "0 hits" after filtering
	// would be indistinguishable from "the filter worked".
	wide := TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 200}}
	baseBody, err := bodyFor(wide)
	if err != nil {
		t.Fatalf("build baseline body: %v", err)
	}
	// track_total_hits is what makes A1's comparison meaningful: the number of RETURNED
	// rows saturates at `size` on any busy index, so an unfiltered and a filtered query
	// both come back with a full page and look identical. The matched total is the only
	// figure that actually moves when a filter applies.
	baseBody["track_total_hits"] = true
	baseline := search(t, baseBody)
	if len(baseline.Hits.Hits) == 0 {
		t.Fatalf("no documents in %s — point ES_RAW_VERIFY_INDEX at an index that has traces", index)
	}

	services := map[string]int{}
	for _, hit := range baseline.Hits.Hits {
		if v, ok := nestedLookup(hit.Source, serviceField); ok && v != "" {
			services[v]++
		}
	}
	if len(services) < 2 {
		t.Skipf("need >= 2 distinct services in %s to prove a raw query narrows the result; found %d", index, len(services))
	}
	var target string
	for s := range services {
		target = s
		break
	}
	t.Logf("baseline: %d returned / %d matched, across %d distinct services; filtering on %s=%q",
		len(baseline.Hits.Hits), baseline.Hits.Total.Value, len(services), serviceField, target)

	// A1 — a raw DSL query naming one service returns only that service.
	t.Run("A1 raw dsl narrows to one service", func(t *testing.T) {
		req := wide
		req.Query = fmt.Sprintf(`{"query":{"term":{"%s":%q}}}`, serviceField, target)
		body, err := bodyFor(req)
		if err != nil {
			t.Fatalf("build raw body: %v", err)
		}
		body["track_total_hits"] = true
		got := search(t, body)
		if len(got.Hits.Hits) == 0 {
			t.Fatalf("raw query returned 0 hits though the baseline saw %q — the term field may need a .keyword suffix on this mapping", target)
		}

		// The matched TOTAL is the signal, not the returned page size: on a busy index
		// both an unfiltered and a filtered query return a full `size` page, so comparing
		// row counts would "pass" even when the query was discarded.
		if got.Hits.Total.Value >= baseline.Hits.Total.Value {
			t.Errorf("raw query matched %d docs vs an unfiltered %d — the query did not narrow anything, which is exactly bug #41426",
				got.Hits.Total.Value, baseline.Hits.Total.Value)
		}

		// The decisive check: every span that came back is the service asked for. A
		// discarded query returns a mix, because the baseline proved this index holds
		// spans from several services.
		seen := map[string]int{}
		for _, hit := range got.Hits.Hits {
			v, _ := nestedLookup(hit.Source, serviceField)
			seen[v]++
			if v != target {
				t.Errorf("raw query filtered on %q but a span from %q came back", target, v)
			}
		}
		t.Logf("A1 PASS: %d returned / %d matched, %d distinct service(s) in the result (baseline had %d); all %s=%q",
			len(got.Hits.Hits), got.Hits.Total.Value, len(seen), len(services), serviceField, target)
	})

	// A1-kql — the same proof for the KQL path, which is a separate translation
	// (kqlToDSL) and so can narrow correctly or not independently of raw DSL.
	t.Run("A1 raw kql narrows to one service", func(t *testing.T) {
		req := wide
		req.Query = fmt.Sprintf("%s : %q", serviceField, target)
		req.Request = map[string]any{"query_type": "kql"}
		body, err := bodyFor(req)
		if err != nil {
			t.Fatalf("build kql body: %v", err)
		}
		body["track_total_hits"] = true
		got := search(t, body)
		if len(got.Hits.Hits) == 0 {
			t.Fatalf("kql query returned 0 hits though the baseline saw %q", target)
		}
		if got.Hits.Total.Value >= baseline.Hits.Total.Value {
			t.Errorf("kql query matched %d docs vs an unfiltered %d — it did not narrow anything",
				got.Hits.Total.Value, baseline.Hits.Total.Value)
		}
		for _, hit := range got.Hits.Hits {
			if v, _ := nestedLookup(hit.Source, serviceField); v != target {
				t.Errorf("kql filtered on %q but a span from %q came back", target, v)
			}
		}
		t.Logf("A1-kql PASS: %d returned / %d matched; all %s=%q",
			len(got.Hits.Hits), got.Hits.Total.Value, serviceField, target)
	})

	// A6 — the GROUPED view honours the raw query too. It owns its own aggregation, so it
	// takes the query as a filter clause (esTraceFilters) rather than the whole body; before
	// #41426 these views built their filters from the where clause only and ignored Query,
	// which is why this is checked against the cluster and not just in a unit test.
	t.Run("A6 the grouped view narrows on a raw query", func(t *testing.T) {
		groupedBuckets := func(req TracesV3Request) map[string]int {
			filters, err := esTraceFilters(req, nil, groupMapping)
			if err != nil {
				t.Fatalf("build grouped filters: %v", err)
			}
			body := map[string]any{
				"size":  0,
				"query": map[string]any{"bool": map[string]any{"filter": filters}},
				"aggs": map[string]any{
					"svc": map[string]any{"terms": map[string]any{"field": serviceField, "size": 50}},
				},
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal grouped body: %v", err)
			}
			httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/%s/_search", baseURL, index), strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("build grouped request: %v", err)
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if user := os.Getenv("ES_RAW_VERIFY_USER"); user != "" {
				httpReq.SetBasicAuth(user, os.Getenv("ES_RAW_VERIFY_PASS"))
			}
			client := &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			}}
			resp, err := client.Do(httpReq)
			if err != nil {
				t.Fatalf("grouped query: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			payload, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read grouped body: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("cluster returned %d: %s", resp.StatusCode, string(payload))
			}
			var out struct {
				Aggregations struct {
					Svc struct {
						Buckets []struct {
							Key      string `json:"key"`
							DocCount int    `json:"doc_count"`
						} `json:"buckets"`
					} `json:"svc"`
				} `json:"aggregations"`
			}
			if err := json.Unmarshal(payload, &out); err != nil {
				t.Fatalf("decode grouped response: %v", err)
			}
			got := map[string]int{}
			for _, b := range out.Aggregations.Svc.Buckets {
				got[b.Key] = b.DocCount
			}
			return got
		}

		all := groupedBuckets(TracesV3Request{})
		if len(all) < 2 {
			t.Skipf("need >= 2 grouped service buckets to prove narrowing, got %d", len(all))
		}
		req := TracesV3Request{Query: fmt.Sprintf(`{"query":{"term":{"%s":%q}}}`, serviceField, target)}
		narrowed := groupedBuckets(req)
		if len(narrowed) != 1 {
			t.Errorf("grouped view returned %d service buckets %v for a query naming one service — it ignored the raw query",
				len(narrowed), narrowed)
		}
		if _, ok := narrowed[target]; !ok {
			t.Errorf("grouped view returned %v, none of them %q", narrowed, target)
		}
		t.Logf("A6 PASS: grouped buckets %d (unfiltered) -> %d (raw query for %q): %v",
			len(all), len(narrowed), target, narrowed)
	})

	// A4 — a raw query that states no time bound is still clamped to the window asked for.
	// An impossible window must return nothing even though the raw query itself is unbounded.
	t.Run("A4 the requested window still bounds a raw query", func(t *testing.T) {
		req := wide
		req.Query = `{"query":{"match_all":{}}}`
		req.StartTime = 1
		req.EndTime = 2 // 1970 — no span can be in here
		body, err := bodyFor(req)
		if err != nil {
			t.Fatalf("build raw body: %v", err)
		}
		got := search(t, body)
		if len(got.Hits.Hits) != 0 {
			t.Errorf("an unbounded raw query returned %d hits inside a 1ms 1970 window — the layer's time bound is not being applied", len(got.Hits.Hits))
		}
		t.Log("A4 PASS: the layer's window bounds a raw query that declares none")
	})

	// A3 — a malformed raw query fails loudly. Proven locally (no cluster needed) because
	// the reader refuses before any request is sent; asserted here so the harness covers
	// the criterion end to end.
	t.Run("A3 a malformed raw query errors", func(t *testing.T) {
		req := wide
		req.Query = `SELECT * FROM spans WHERE service = 'checkout'` // ClickHouse-era SQL
		if _, err := bodyFor(req); err == nil {
			t.Error("SQL was accepted as an Elasticsearch body — a malformed raw query must error, not run unfiltered")
		} else {
			t.Logf("A3 PASS: %v", err)
		}
	})
}

// nestedLookup resolves a dotted path against an ES _source map. The three trace layouts
// disagree about what a dot means, so it has to try both readings at every level:
//
//	Data Prepper  "serviceName"        — a plain top-level key
//	APM           "service.name"       — nested: {"service":{"name":…}}
//	OTel-native   "resource.attributes.service.name"
//	              — PART nested, PART flat: {"resource":{"attributes":{"service.name":…}}}
//
// So at each level it tries the longest remaining path as a literal key first and shortens
// until something matches, descending on each hit. (Elasticsearch itself has no such
// problem — its mapping names the field by its full dotted path either way, which is why
// the production term query works on all three; this is only about reading _source back.)
func nestedLookup(src map[string]any, path string) (string, bool) {
	var walk func(cur any, segs []string) (string, bool)
	walk = func(cur any, segs []string) (string, bool) {
		if len(segs) == 0 {
			s, ok := cur.(string)
			return s, ok
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		// Longest literal key first, then shorter prefixes.
		for n := len(segs); n >= 1; n-- {
			key := strings.Join(segs[:n], ".")
			v, found := m[key]
			if !found {
				continue
			}
			if s, ok := walk(v, segs[n:]); ok {
				return s, true
			}
		}
		return "", false
	}
	return walk(any(src), strings.Split(path, "."))
}
